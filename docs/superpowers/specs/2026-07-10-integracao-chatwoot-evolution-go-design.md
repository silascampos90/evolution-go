# Integração nativa evolution-go ↔ Chatwoot (canal WhatsApp)

**Data:** 2026-07-10
**Status:** Aprovado (design) — pronto para plano de implementação
**Escopo desta spec:** MVP — mensagens de **texto** em conversas **1:1**, bidirecional, com UI de gestão. Mídia, grupos, status/stories e confirmação de entrega ficam para ciclos futuros (ver "Fora de escopo").

## Objetivo

Permitir que cada instância de WhatsApp do evolution-go funcione como um canal de atendimento de uma inbox do Chatwoot, com relação **1:1** (uma instância = um número = uma inbox). Um mesmo evolution-go serve várias instâncias, cada uma ligada a uma inbox diferente. Toda a integração é nativa em Go, dentro do próprio evolution-go — sem serviço-ponte externo.

O usuário assumiu explicitamente a responsabilidade de licença/legal deste fork (ver contexto em [[evolution-go-fork-local-setup]] e [[chatwoot-fork-local-setup]]).

## Decisões de design (resumo)

1. **Nativo em Go**, um projeto só — melhor latência e menos infra que uma ponte separada.
2. **Auto-provisionamento pelo evolution-go**: o usuário digita só o nome; o evolution cria a inbox API no Chatwoot, configura o webhook de volta e cria a instância.
3. **UI própria** em `/chatwoot-admin` (HTML+JS servido pelo Go), layout de **cards**, um por conexão. Não estende o Manager compilado (cujo fonte não está no repo).
4. **Vínculo guardado como campos no model `Instance`** (não tabela separada) — o Producer já recebe a `Instance` no fan-out.
5. **JID do WhatsApp usado como `source_id`** no Chatwoot — dispensa tabela de-para de conversas; o Chatwoot é a fonte da verdade da correlação.
6. **Contato criado automaticamente** (nome = pushName, telefone = número); o Chatwoot deduplica por telefone.
7. **Webhook de volta por instância no path** (`/chatwoot/webhook/:instance`).
8. **Filtro anti-eco**: só reenvia ao WhatsApp mensagens `outgoing` e não-privadas.
9. **Comunicação por rede Docker compartilhada** (nomes de serviço).

## Arquitetura

Quatro peças dentro do evolution-go. Duas de *transporte* (dados) e uma de *controle* (config); a quarta (envio) já existe.

```
                    ┌─────────────────────── evolution-go ───────────────────────┐
 WhatsApp ──msg──▶  │  whatsmeow → [1] Chatwoot Producer ──HTTP──▶  Chatwoot API  │
 WhatsApp ◀──msg──  │  [4] SendService ◀── [2] Webhook Receiver ◀──HTTP── Chatwoot│
                    │  [3] Chatwoot Admin (/chatwoot-admin) + REST de gestão      │
                    └─────────────────────────────────────────────────────────────┘
```

As peças se comunicam pelo estado no banco (campos no `Instance`), não por chamadas diretas — cada uma é testável isolada.

### [1] Chatwoot Producer (WhatsApp → Chatwoot)

- **Onde:** novo pacote `pkg/events/chatwoot/chatwoot_producer.go`, implementando a interface `producer_interfaces.Producer` (`Produce`, `CreateGlobalQueues`), espelhando `pkg/events/webhook/webhook_producer.go`.
- **Instanciação:** em `cmd/evolution-go/main.go` (perto da criação dos outros producers, ~linha 132) e injetado em `NewWhatsmeowService` (`pkg/whatsmeow/service/whatsmeow.go`, construtor ~2793-2831; campos do struct ~90-97 e do `MyClient` ~124-129; cópia ~489-494).
- **Gatilho:** branch novo em `sendToQueueOrWebhook` (`whatsmeow.go` ~2313, ao lado do webhook), guardado por `instance.ChatwootEnabled`.
- **Recebe:** o mesmo envelope JSON dos outros producers (`event`, `data.Info`, `data.Message`, `instanceId`, etc.).

Fluxo interno do `Produce`:
1. Filtra: `event == "Message"`, mensagem `incoming` (não `Info.IsFromMe`), conversa 1:1 (ignora grupo/status — checar sufixo do JID `@g.us`/`status@broadcast`).
2. Extrai: `jid = Info.Sender`, `nome = Info.PushName`, `texto = Message.conversation || extendedTextMessage.text`, `wamid = Info.ID`.
3. Dedupe por `wamid` (o whatsmeow reentrega eventos em reconexão) — persistido como `source_id` da mensagem no Chatwoot.
4. Garante contato + `contact_inbox` com `source_id = jid` (idempotente).
5. Garante conversa (reusa aberta ou cria).
6. Injeta a mensagem `incoming`.

Otimização: cache em memória `jid → {contact_id, conversation_id}` por instância; conversa nova = 2 chamadas HTTP (contato; conversa-com-mensagem), conversa existente = 1 chamada (só mensagem).

Falha (Chatwoot indisponível): retry com backoff; se esgotar, log ERROR com o payload. **Sem** fila persistente no MVP (ver Fora de escopo).

### [2] Webhook Receiver (Chatwoot → WhatsApp)

- **Onde:** nova rota HTTP `POST /chatwoot/webhook/:instance` (handler novo em `pkg/`; registrar junto às rotas existentes). Precede o gate de licença? Não — a rota é pública para o Chatwoot; autenticada por HMAC, não por apikey. Adicionar `/chatwoot/webhook` à allowlist do `GateMiddleware` se necessário.
- **Fluxo:**
  1. Valida `X-Chatwoot-Signature: sha256=HMAC_SHA256(webhook_secret, "<timestamp>.<body>")` usando o `ChatwootWebhookSecret` do vínculo; rejeita `401` se não bate.
  2. Filtra: `event == "message_created"` **e** `message_type == "outgoing"` **e** `private == false`. Caso contrário responde `200` e descarta (anti-eco — evita reenviar a própria mensagem `incoming` injetada por [1]).
  3. Extrai `jid = conversation.contact_inbox.source_id`, `texto = content`.
  4. Resolve a instância pelo `:instance` do path (`GetInstanceByName`/`GetInstanceByToken`) e chama `sendService.SendText(&TextStruct{Number: jid, Text: texto}, instance)`.
  5. Responde `200` (erro faz o Chatwoot marcar a mensagem como `failed`).

### [3] Chatwoot Admin (UI + REST de gestão)

- **UI:** `GET /chatwoot-admin` — página HTML+JS servida pelo Go, layout de cards. Mostra por conexão: nome da instância, número, inbox vinculada (`#id · nome`), status (🟢 conectado / 🟡 aguardando QR), ações (pausar, reconectar, remover, mostrar QR). Botões globais: "Config Chatwoot" e "Nova conexão".
- **Endpoints REST** (auth admin, header `apikey: GLOBAL_API_KEY`):

| Método | Rota | Faz |
|---|---|---|
| `GET` | `/chatwoot/config` | lê config global (token mascarado) |
| `PUT` | `/chatwoot/config` | salva `base_url` + `api_token` + `account_id` |
| `POST` | `/chatwoot/config/test` | testa a conexão com o Chatwoot |
| `GET` | `/chatwoot/links` | lista conexões + status (alimenta os cards) |
| `POST` | `/chatwoot/links` | cria conexão + auto-provisiona |
| `DELETE` | `/chatwoot/links/:instance` | desvincula (opção manter/apagar inbox no Chatwoot) |

Auto-provisionamento (`POST /chatwoot/links`):
1. `POST {base_url}/api/v1/accounts/{account_id}/inboxes` com `{ name, channel: { type: "api", webhook_url: "http://evolution-go:8080/chatwoot/webhook/<nome>" } }` → recebe `id`, `inbox_identifier`, `secret`.
2. Cria a instância (reusa `/instance/create`).
3. Grava no `Instance`: `ChatwootEnabled=true`, `ChatwootInboxID`, `ChatwootInboxIdentifier`, `ChatwootWebhookSecret=secret`.
4. UI então mostra o QR (reusa `/instance/qr`).

### [4] SendService (envio ao WhatsApp)

Já existe — `pkg/sendMessage/service/send_service.go`, `SendText(data *TextStruct, instance *instance_model.Instance)`. Sem mudança no MVP; apenas invocado por [2].

## Modelo de dados

### Config global do Chatwoot (singleton)
Uma linha (tabela própria ou reaproveitar `runtime_configs`): `base_url`, `api_token` (sensível — nunca reexposto na UI), `account_id`.

### Vínculo instância ↔ inbox
Campos novos no model `Instance` (`pkg/instance/model/instance_model.go`) — AutoMigrate cria as colunas:
- `ChatwootEnabled bool`
- `ChatwootInboxID string`
- `ChatwootInboxIdentifier string`
- `ChatwootWebhookSecret string`

Setados no fluxo de auto-provisionamento (não no `/instance/create` genérico).

### Correlação de conversas
O JID do WhatsApp é o `source_id` no Chatwoot. Entrada: JID → `contact_inbox.source_id`. Saída: `conversation.contact_inbox.source_id` → JID. Nenhuma tabela de-para no evolution.

## Autenticação

- **evolution → Chatwoot:** header `api_access_token` = token de um **usuário admin** do Chatwoot (necessário para criar inbox e contato; o Agent Bot token é restrito e não serve). Guardado na config global.
- **Chatwoot → evolution:** HMAC `X-Chatwoot-Signature` validado com o `secret` da inbox (guardado em `ChatwootWebhookSecret`). Sem config manual pelo usuário.
- **UI/REST de gestão:** header `apikey: GLOBAL_API_KEY` (admin), como o restante do evolution-go.

## Infra / rede Docker

Rede Docker externa compartilhada entre os dois stacks:
1. `docker network create chatwoot-evo`.
2. No `docker-compose.override.yaml` do Chatwoot: adicionar a rede `chatwoot-evo` (external) ao serviço `rails`, com alias `chatwoot-rails`.
3. No `docker-compose.local.yml` do evolution-go: adicionar a rede `chatwoot-evo` (external) ao serviço `evolution-go`.
4. URLs resultantes (portas **internas**): evolution → `http://chatwoot-rails:3000`; webhook_url → `http://evolution-go:8080/chatwoot/webhook/<nome>`.

## Fluxo do usuário (feliz)

1. Uma vez: abre `/chatwoot-admin` → "Config Chatwoot" → informa URL, token admin, account → "Testar" → "Salvar".
2. "Nova conexão" → digita nome (`vendas`) → "Criar e parear".
3. Sistema cria inbox no Chatwoot, configura webhook, cria instância (feedback de progresso).
4. Mostra QR → usuário pareia no celular.
5. Conexão vira card 🟢 na lista. Mensagens passam a fluir nos dois sentidos.

## Fora de escopo (ciclos futuros)

- Mídia (imagem, áudio/PTT, vídeo, documento) — exigirá storage (MinIO) ou repasse base64/URL.
- Grupos e status/stories.
- Confirmação de entrega (✓✓) via `PUT /messages/{id}` com status.
- Fila persistente de reenvio quando o Chatwoot está indisponível.
- Sincronização de nome/atributos do contato além da criação inicial.
- **Garantia de ordem estritamente end-to-end.** O producer processa mensagens do mesmo contato em ordem (worker FIFO por JID), mas a camada de dispatch do evolution-go (`whatsmeow.go`) despacha cada evento numa goroutine própria (`go CallWebhook`), então há uma janela residual em que duas mensagens quase simultâneas do mesmo contato podem chegar ao producer fora de ordem. Improvável no uso humano (mensagens segundos à parte); fechar isso exigiria alterar o dispatch central compartilhado por todos os producers.

## Notas de implementação (pós-revisão)

- **Correlação resiliente a restart (implementado):** o cache conversa↔contato é em memória. Ao perder o cache (restart), o producer reconcilia com o Chatwoot como fonte da verdade: resolve o contato via `POST /contacts/filter` (phone_number equal_to) e a conversa aberta via `GET /contacts/{id}/conversations` (status `open`), só criando se não existir. Evita erro de telefone duplicado (422) e conversas duplicadas.
- **`CHATWOOT_SELF_URL`:** a URL pública deste evolution-go (gravada no `webhook_url` de cada inbox) é configurável por env, com default `http://evolution-go:8080` (nome de serviço na rede compartilhada).

## Arquivos afetados (referência)

**evolution-go (novos):** `pkg/events/chatwoot/chatwoot_producer.go`, handler do webhook receiver, handlers/rotas de `/chatwoot/*` e `/chatwoot-admin`, client HTTP da API do Chatwoot, página HTML da UI.
**evolution-go (alterados):** `cmd/evolution-go/main.go` (instanciar producer + registrar rotas), `pkg/whatsmeow/service/whatsmeow.go` (campo no service/MyClient + branch em `sendToQueueOrWebhook`), `pkg/instance/model/instance_model.go` (campos Chatwoot), `docker-compose.local.yml` (rede).
**Chatwoot (alterado):** `docker-compose.override.yaml` (rede compartilhada). Nenhuma mudança de código no Chatwoot — usa a API e o webhook nativos do `Channel::Api`.
