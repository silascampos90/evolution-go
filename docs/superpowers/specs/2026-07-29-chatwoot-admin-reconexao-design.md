# Reconexão sem duplicar inbox + reforma da tela `/chatwoot-admin`

**Data:** 2026-07-29
**Status:** Aprovado (design) — pronto para plano de implementação
**Escopo:** corrigir os três defeitos que hoje quebram a integração Chatwoot em produção e elevar a tela `/chatwoot-admin` ao padrão visual do Manager. Tudo dentro do evolution-go; nenhuma alteração de código no Chatwoot.

Continuação de [`2026-07-10-integracao-chatwoot-evolution-go-design.md`](2026-07-10-integracao-chatwoot-evolution-go-design.md), que entregou o MVP mas deixou de fora as ações de ciclo de vida previstas na própria spec (seção [3], linhas 70 e 80).

## Problema

O usuário relatou que "a cada conexão o sistema cria uma caixa nova no Chatwoot", deixando conversas órfãs na inbox anterior. O cenário real confirmado é **reconexão do mesmo número** após a sessão do WhatsApp cair.

Três defeitos independentes se somam:

### D1 — `CreateLink` cria a inbox antes de validar a instância, sem rollback

`pkg/chatwoot/service/chatwoot_service.go:114` chama `CreateInbox` e só depois, em `:121`, chama `instanceSvc.Create`. Mas `pkg/instance/service/instance_service.go:175-179` rejeita nome repetido com `instance already exists`.

Consequência: toda tentativa de reconectar usando o mesmo nome cria uma inbox no Chatwoot, falha na criação da instância e **abandona a inbox**. Cada tentativa acumula mais uma.

### D2 — A tela não tem como reconectar

`pkg/chatwoot/ui/chatwoot_admin.html:451-468` renderiza cards estáticos, sem ações. Quando a sessão cai, o único botão disponível é "Nova conexão" — que dispara D1. O `DELETE /chatwoot/links/:instance` previsto no design original nunca foi implementado.

### D3 — A busca de conversa aberta ignora a inbox

`pkg/chatwoot/client/chatwoot_client.go:179-196` chama `GET /contacts/{id}/conversations` e devolve a primeira conversa com status `open`, **sem filtrar por inbox**. Confirmado no fonte do Chatwoot (`app/controllers/api/v1/accounts/contacts/conversations_controller.rb`) que esse endpoint retorna conversas de todas as inboxes da conta.

Com as inboxes duplicadas de D1, uma mensagem que chega pela inbox B é injetada numa conversa da inbox A. O agente responde ali, o Chatwoot dispara o webhook da inbox A, e **a resposta sai pelo número errado**.

## Decisões de design

1. **Corrigir no evolution-go**, mantendo a tela própria em `/chatwoot-admin`. O fonte do Manager buildado (`manager/dist`) não está em nenhum repositório local — é artefato commitado, sincronizado do upstream pelo bot (`sync: 0.7.2 from main`) e sobrescrito a cada release. Customizá-lo geraria conflito recorrente.
2. **Inbox por find-or-create**, não create cego. Viável porque `GET /inboxes` devolve `secret`, `inbox_identifier` e `webhook_url` para inboxes `Channel::Api` quando o token é de administrador (`app/views/api/v1/models/_inbox.json.jbuilder:116-121`) — o mesmo tipo de token que a config já exige.
3. **Reconectar reusa instância e inbox existentes.** Nenhuma chamada de criação no caminho de reconexão.
4. **Reconectar restaura o conjunto de eventos**, não só religa o cliente (ver "Armadilhas" abaixo).
5. **Conversa escopada por inbox** — filtro pelo `inbox_id` que já vem no payload.
6. **UI vanilla** (HTML/CSS/JS num arquivo, `go:embed`), reusando os design tokens extraídos do bundle do Manager. Sem estágio Node, sem mudança no Dockerfile.
7. **Sem migração de banco.** Os campos `ChatwootInboxID`, `ChatwootInboxIdentifier` e `ChatwootWebhookSecret` já existem em `pkg/instance/model/instance_model.go:30-33`.

## Armadilhas do código existente

Duas descobertas que condicionam o desenho do "reconectar":

- **`Connect` sobrescreve configuração.** `pkg/instance/service/instance_service.go:232-236` atribui `instance.Events`, `Webhook`, `RabbitmqEnable`, `NatsEnable` e `WebSocketEnable` a partir do corpo da requisição. Com `Subscribe` vazio ele assume só `MESSAGE` (`:214-215`), derrubando o `READ_RECEIPT` que o `CreateLink` configurou em `chatwoot_service.go:130` — o sync de status de mensagem pararia de funcionar silenciosamente.
- **`Disconnect` zera os eventos.** `instance_service.go:322` faz `instance.Events = ""`, o que mata a ponte até que alguém reconecte.

Conclusão: `ReconnectLink` sempre passa `Subscribe` explícito (`MESSAGE`, `READ_RECEIPT`) e preserva `instance.Webhook`. Isso também cura uma instância que ficou com `Events` vazio após um disconnect.

## Parte 1 — Backend

### 1.1 Client HTTP (`pkg/chatwoot/client/chatwoot_client.go`)

Métodos novos:

| Método | Chamada | Uso |
|---|---|---|
| `FindInboxByName(name)` | `GET /inboxes` | filtra `channel_type == "Channel::Api"` e nome exato; devolve `nil, nil` quando não existe |
| `UpdateInboxWebhook(inboxID, url)` | `PATCH /inboxes/{id}` | corrige `channel.webhook_url` divergente |
| `DeleteInbox(inboxID)` | `DELETE /inboxes/{id}` | rollback e remoção |

Assinatura alterada: `FindOpenConversation(contactID, inboxID int)` passa a descartar conversas de outra inbox, comparando o `inbox_id` do payload (`app/views/api/v1/conversations/partials/_conversation.json.jbuilder:47`).

O struct `Inbox` ganha `Name` e `WebhookURL`.

### 1.2 Service (`pkg/chatwoot/service/chatwoot_service.go`)

**`CreateLink(name)` — idempotente e transacional.** Nova ordem:

1. `GetInstanceByName(name)`. Se existir, aborta com erro de conflito **antes de tocar no Chatwoot**. A mensagem orienta a usar Reconectar.
2. `FindInboxByName(name)`. Se existir, reusa (recuperando `secret` e `identifier`) e corrige o `webhook_url` se divergir. Senão, cria — e marca que a inbox é *nossa* nesta chamada.
3. `instanceSvc.Create(...)`. Se falhar **e** a inbox tiver sido criada no passo 2, `DeleteInbox` antes de propagar o erro.
4. Persiste o vínculo como hoje.

**`ReconnectLink(name)` — novo.** Carrega a instância, exige `ChatwootEnabled`, chama `instanceSvc.Connect` com `Subscribe: [MESSAGE, READ_RECEIPT]` e `WebhookUrl: instance.Webhook` (ver "Armadilhas"), e devolve `instanceToken` + `inboxId` para a tela conduzir o loop de QR. Não cria nada.

**`DeleteLink(name, deleteInbox bool)` — novo.** Limpa os campos Chatwoot da instância; com `deleteInbox`, também chama `DeleteInbox`. A instância em si não é removida — isso continua sendo `DELETE /instance/delete/:instanceId`.

**`GetConfig()` — novo.** Devolve `baseUrl`, `accountId` e o token mascarado, para a tela reidratar o formulário. Hoje não existe, e por isso a config sempre abre em branco.

**`ListLinks()` — alterado.** Passa a incluir `instanceId` e `instanceToken`, para a tela chamar `/instance/qr`, `/instance/status` e `/instance/logout` sem recriar nada. A rota é admin-only (`GLOBAL_API_KEY`), o mesmo nível de segredo.

### 1.3 Rotas (`pkg/chatwoot/routes.go`)

Adicionar ao grupo já protegido por `adminAuth`:

```
GET    /chatwoot/config
POST   /chatwoot/links/:instance/reconnect
DELETE /chatwoot/links/:instance          ?deleteInbox=true
```

As rotas de instância que a tela consome já existem e não mudam (`pkg/routes/routes.go:99-105`).

### 1.4 Limpeza em produção (operacional, não código)

Já há inboxes órfãs acumuladas. Após o deploy, auditar no Chatwoot as inboxes `Channel::Api` sem instância correspondente no evolution-go e removê-las manualmente, migrando antes qualquer conversa que valha preservar. Não automatizamos isso: apagar inbox destrói histórico, e a decisão de quais preservar é do operador.

## Parte 2 — UI (`pkg/chatwoot/ui/chatwoot_admin.html`)

Reescrita completa, mantendo `go:embed` e arquivo único.

**Linguagem visual** — tokens extraídos do bundle do Manager, para a tela parecer parte dele:

| Token | Claro | Escuro |
|---|---|---|
| `--primary` | `oklch(67.35% .153 159.64)` | `oklch(88.18% .202 159.34)` |
| `--foreground` | `oklch(.145 0 0)` | `oklch(.985 0 0)` |
| `--card` | `oklch(1 0 0)` | `oklch(.145 0 0)` |
| `--muted-foreground` | `oklch(.556 0 0)` | `oklch(.708 0 0)` |
| `--border` | `oklch(.922 0 0)` | `oklch(.269 0 0)` |
| `--radius` | `.625rem` | — |

Fonte Inter com fallback de sistema. Tema escuro por classe no `<html>`, com toggle persistido em `localStorage` e default seguindo `prefers-color-scheme`.

**Estrutura**

- Header com o nome da tela, badge do estado global da config (*conectado* / *não configurado*, alimentado por `GET /chatwoot/config` + `POST /chatwoot/config/test`) e toggle de tema.
- Estado vazio guiado quando não há config, levando ao drawer de configuração.
- Drawer de config com token mascarado reidratado, "Testar" com feedback inline.
- Grid de cards. Cada card: nome, número formatado, inbox com link direto para o Chatwoot, badge de status (conectado / aguardando pareamento / desconectado) e ações **Reconectar**, **Abrir no Chatwoot** e um menu com *Encerrar sessão* e *Remover*.

  *Encerrar sessão* chama `DELETE /instance/logout` — desparea o WhatsApp e exige novo QR. Deliberadamente **não** usamos `POST /instance/disconnect`: ele zera `instance.Events` (`instance_service.go:322`), deixando a instância viva mas com a ponte muda, um estado que a tela não teria como distinguir de "conectado". O rótulo diz "encerrar sessão" justamente porque a ação é destrutiva do pareamento, não uma pausa.
- Skeleton durante o carregamento; polling leve da lista para o status não envelhecer.
- Modal de QR com contador de expiração, "gerar novo" e estado de sucesso.
- Toasts substituindo os `<span>` de status atuais.
- Confirmação destrutiva ao remover, com checkbox *"apagar também a inbox no Chatwoot"* mapeado para `?deleteInbox=true`.

**Estágio de passkey.** `/instance/qr` já devolve `passkeyStage`, `passkeyOpenUrl` e `passkeyCode` (`pkg/instance/service/instance_service.go:95-102`) para contas que exigem WebAuthn — nesse estágio não existe QR para escanear. A tela atual ignora esses campos e trava exibindo "Gerando QR…" indefinidamente. A nova detecta o estágio e mostra o botão "Abrir WhatsApp Web" com o código de verificação.

## Testes

Seguir o padrão dos testes já existentes no pacote (`chatwoot_client_test.go`, `chatwoot_service_test.go`): `httptest` servindo um Chatwoot falso para o client, fakes de repositório para o service.

Casos que precisam de cobertura:

- `FindInboxByName` encontra, não encontra, e ignora inbox de outro `channel_type`.
- `FindOpenConversation` descarta conversa aberta de outra inbox (regressão de D3).
- `CreateLink` com instância já existente não emite nenhuma chamada ao Chatwoot (regressão de D1).
- `CreateLink` reusa inbox existente em vez de criar outra.
- `CreateLink` faz rollback da inbox quando a criação da instância falha.
- `ReconnectLink` passa `MESSAGE,READ_RECEIPT` em `Subscribe` e preserva o webhook (regressão da armadilha do `Connect`).
- `DeleteLink` com e sem `deleteInbox`.

A página HTML não tem harness de teste — verificação manual contra os fluxos descritos na Parte 2.

## Fora de escopo

- **Canal nativo `Channel::Evolution` no Chatwoot** (a "opção C" avaliada). Seria mais elegante — provisionamento e pareamento dentro do próprio Chatwoot, e os três defeitos desapareceriam por construção em vez de por conserto. Foi adiado por custo: model, migration, service de envio, controller de webhook, telas Vue e i18n, além de passar a alterar `app/` num fork que hoje só tem commits `ops:`, encarecendo cada merge com o upstream. A exploração feita (`Channel::Telegram`, `SendReplyJob`, `ChannelFactory.vue`, `inboxes_controller`) fica registrada aqui para quando esse ciclo começar.
- Vincular uma instância a uma inbox pré-existente escolhida pelo usuário (hoje o casamento é por nome).
- Múltiplos números na mesma inbox.
- Fila persistente de reenvio quando o Chatwoot está fora do ar.

## Arquivos afetados

**Alterados:** `pkg/chatwoot/client/chatwoot_client.go`, `pkg/chatwoot/service/chatwoot_service.go`, `pkg/chatwoot/handler/admin_handler.go`, `pkg/chatwoot/routes.go`, `pkg/chatwoot/ui/chatwoot_admin.html`, `pkg/events/chatwoot/chatwoot_producer.go` (nova assinatura de `FindOpenConversation`), e os testes correspondentes.

**Inalterados:** modelo de dados, `Dockerfile`, `manager/dist`, e todo o repositório do Chatwoot.
