# Chatwoot — verificação pós-deploy e limpeza das inboxes órfãs

Roteiro para validar a correção da reconexão em produção. Os passos aqui **não
podem ser executados em ambiente de desenvolvimento sem banco e sem Chatwoot
acessível** — é por isso que existem como roteiro em vez de teste automatizado.

Referência: [`docs/superpowers/specs/2026-07-29-chatwoot-admin-reconexao-design.md`](../../superpowers/specs/2026-07-29-chatwoot-admin-reconexao-design.md)

## O que já está verificado automaticamente

`go build ./...` e `go test ./...` cobrem o backend: find-or-create de inbox,
rollback transacional, guarda de nome duplicado falhando fechado, conversa
escopada por inbox, e o registro das rotas atrás do middleware de admin.

**Nada da interface foi aberto em navegador.** Toda a verificação da tela foi
por leitura de código e, na parte do modal de pareamento, por execução em jsdom
— que não tem layout. Os itens 1 e 5 abaixo são a primeira vez que essa tela
roda de verdade.

## 1. Fluxo que originou o trabalho: reconectar sem duplicar inbox

O defeito original: cada tentativa de reconectar com o mesmo nome criava uma
inbox no Chatwoot e a abandonava.

1. Abra `/chatwoot-admin`, preencha a apikey global.
2. Crie uma conexão chamada `teste-recon` e pareie pelo QR.
3. **Anote a contagem de inboxes** no Chatwoot (Configurações › Caixas de entrada).
4. No celular: WhatsApp › Aparelhos conectados › sair da sessão.
5. Na tela, aguarde o card virar 🔴 e clique **Reconectar**. Pareie de novo.
6. Confira que a contagem de inboxes **não mudou** e que a conversa anterior
   continua na mesma inbox.

**Esperado:** nenhuma inbox nova; histórico preservado.

## 2. Conflito de nome devolve 409, não 500

1. Com `teste-recon` existindo, clique **Nova conexão** e use o mesmo nome.

**Esperado:** erro imediato orientando a usar Reconectar, **nenhuma** inbox nova
no Chatwoot, e status HTTP 409 (não 500). Confira na aba Network do navegador.

## 3. Resposta sai pelo número correto com duas inboxes

Este é o defeito mais silencioso dos três: a busca de conversa aberta ignorava a
inbox, então a resposta do agente podia sair pelo número errado.

1. Tenha duas conexões ativas, com dois números diferentes (A e B).
2. Do mesmo contato, mande uma mensagem para o número **A**.
3. Responda pelo Chatwoot na conversa que apareceu.
4. Confira no WhatsApp do contato que a resposta chegou **do número A**.
5. Repita mandando para o número **B**.

**Esperado:** cada resposta sai pelo número que recebeu a mensagem.

## 4. Remoção destrutiva é opt-in

1. No card, menu `⋯` › **Remover**.
2. Digite o nome da conexão. Deixe o checkbox *"apagar também a inbox"*
   **desmarcado**. Confirme.

**Esperado:** o vínculo é desfeito e a **inbox continua existindo** no Chatwoot
com o histórico intacto.

## 5. Itens que só um navegador prova

Nenhum destes foi verificado. São os pontos onde a revisão de código apontou
risco residual:

- **Tab dentro do modal de QR.** No estado padrão o foco deve circular entre
  "Fechar" e o X do cabeçalho, sem parar nos botões escondidos de passkey e
  retry. Depois de expirar, o "Gerar novo QR" deve entrar no ciclo.
- **Modal em 320px e no tema escuro.** O QR é uma caixa de 220px; conferir que
  não há rolagem horizontal e que o contraste do fundo do QR contra o card
  funciona no escuro.
- **Linha de ações do card em ~600px.** Os botões devem quebrar em duas linhas,
  não cortar o rótulo. "Abrir no Chatwoot" precisa aparecer inteiro.
- **QR atrasado após expirar.** Com throttling de rede, deixar o `/instance/qr`
  responder depois dos 120s e confirmar que **não** aparece um QR novo depois de
  "O QR expirou.".
- **Passkey além dos 120s.** Se você tem uma conta que exige passkey WebAuthn:
  manter a cerimônia aberta mais de dois minutos e confirmar que o código de
  verificação **não** desaparece e que o poll de status continua.
- **`<img>` do QR após expirar.** Na aba Network, confirmar que a página do
  admin não é re-baixada como imagem.

## 6. Limpeza das inboxes órfãs acumuladas

O defeito rodou em produção, então há inboxes órfãs. Isso é trabalho manual de
propósito: **apagar uma inbox destrói o histórico de conversas dela**, e decidir
o que preservar é sua decisão, não do código.

1. Liste as inboxes do tipo API no Chatwoot.
2. Compare com as instâncias em `GET /chatwoot/links`.
3. Para cada inbox sem instância correspondente: veja se há conversa que valha
   preservar. Se houver, mova ou exporte antes.
4. Só então apague, pelo próprio Chatwoot.

Um atalho para o passo 2, com a apikey global:

```bash
curl -s -H "apikey: $GLOBAL_API_KEY" http://localhost:8080/chatwoot/links \
  | python3 -c 'import json,sys; [print(l["inboxId"], l["instanceName"]) for l in json.load(sys.stdin)["data"]]'
```

As inboxes que **não** aparecerem nessa lista são candidatas a órfãs.

## 7. Se algo falhar

Os logs do evolution-go registram cada etapa da ponte com o prefixo
`chatwoot:`. Erros úteis de procurar:

| Mensagem | Significa |
|---|---|
| `config do chatwoot ausente` | a configuração global não foi salva |
| `verificar nome da instância` | falha real de banco (não é "nome já existe") |
| `procurar inbox` / `criar inbox` | a API do Chatwoot recusou — confira se o token é de administrador |
| `chatwoot: falha ao remover inbox órfã` | o rollback não conseguiu apagar a inbox recém-criada; ela ficou órfã e precisa de limpeza manual |
