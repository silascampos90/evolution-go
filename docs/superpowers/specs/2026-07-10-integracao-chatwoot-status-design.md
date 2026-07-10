# Integração Chatwoot ↔ evolution-go — Fatia 3: Status de entrega

**Data:** 2026-07-10
**Status:** Aprovado (design)
**Depende de:** fatias de texto e mídia (já implementadas e em produção local).
**Escopo:** sincronizar o status das mensagens do **agente** (Chatwoot → WhatsApp) de volta para o Chatwoot — `delivered` (2 checks) e `read` (lido) — para o indicador da mensagem avançar. Fora de escopo: status de mensagens de grupo, `ReadSelf`, retry persistente de PUT.

## Objetivo

Quando o agente envia uma mensagem pelo Chatwoot, o evolution já a entrega ao WhatsApp. O WhatsApp devolve recibos de entrega/leitura (`events.Receipt`) que o evolution já recebe. Esta fatia correlaciona o recibo com a mensagem no Chatwoot e faz `PUT` do status, para o agente ver `sent → entregue → lido`.

## Correlação

- No **envio**, `SendText`/`SendMediaFile` retornam `*MessageSendStruct` cujo `Info.ID` é o **wamid** (id da mensagem no WhatsApp). O webhook do Chatwoot que originou o envio traz o **id interno da mensagem** (`id`) e o **display_id da conversa** (`conversation.id`). Logo, no receiver temos os três: `wamid`, `mid`, `cid`.
- Guardamos `wamid → (cid, mid)` numa **tabela** (durável: o recibo de leitura pode chegar horas depois e sobreviver a restart).
- No **recibo** (`event == "Receipt"`), o envelope traz `state` (`Delivered`/`Read`) no topo e `data.MessageIDs` (array de wamids). Para cada wamid, consultamos o mapa e fazemos `PUT`.

## Componentes

1. **Model + repo** `message_maps` (GORM): `Wamid string` (índice único), `ConversationID int` (display_id), `MessageID int` (id interno), `InstanceID string`. Registrado no AutoMigrate. Métodos: `Save(m)` (idempotente por wamid) e `Get(wamid) (*MessageMap, error)` (nil,nil se ausente).

2. **Cliente** `UpdateMessageStatus(cid, mid int, status string) error` → `PUT /api/v1/accounts/{account}/conversations/{cid}/messages/{mid}` com `{"status": status}`. `cid` = conversation display_id; `mid` = message id.

3. **Receiver** (`webhook_handler`): extrai `mid` (`id`) e `cid` (`conversation.id`) do payload; ao enviar cada mensagem/anexo ao WhatsApp, captura o `wamid` do retorno (`resp.Info.ID`) e grava `wamid → (cid, mid)`. Mídia com N anexos → N wamids para o mesmo `mid` (qualquer recibo atualiza a mesma mensagem — aceitável). Falha ao gravar o mapa é logada, não bloqueia o envio.

4. **Producer** (`chatwoot_producer`): no `Produce`, antes do fluxo de mensagem, se `event == "Receipt"` despacha `handleReceipt` (goroutine própria; não usa o worker por-JID). `handleReceipt` faz parse do envelope (state + MessageIDs), mapeia `Delivered→delivered`, `Read→read` (ignora `ReadSelf`), consulta o mapa por wamid, e chama `UpdateMessageStatus`. Constrói o client a partir da config global (já injetada).

5. **Subscrição**: `CreateLink` passa a setar `Events = "MESSAGE,READ_RECEIPT"` (o `READ_RECEIPT` faz o `CallWebhook` despachar os eventos `Receipt` ao producer). Backfill: a instância `ritoDesk` existente recebe `Events` atualizado.

## Regras / bordas

- Chatwoot bloqueia a transição `read → delivered` (não regride) — tratado no lado dele; nossos PUTs podem chegar fora de ordem sem problema.
- `status` válidos no Chatwoot: `sent/delivered/read/failed`. Usamos `delivered` e `read`.
- Só mensagens **outgoing** do agente entram no mapa (o receiver só roda para outgoing). Mensagens incoming não precisam de status.
- Recibo cujo wamid não está no mapa (ex.: mensagem enviada antes desta fatia, ou por outro canal) → ignora silenciosamente.

## Fora de escopo

- Retry persistente do PUT de status (se o Chatwoot estiver fora, loga e desiste).
- Status de mensagens em grupo/newsletter.
- Limpeza/expiração da tabela `message_maps` (cresce; poda é evolução futura).

## Arquivos afetados

**evolution-go (novos):** `pkg/chatwoot/model/message_map.go`, `pkg/chatwoot/repository/message_map_repository.go` (+ testes).
**evolution-go (alterados):** `pkg/chatwoot/client/chatwoot_client.go` (UpdateMessageStatus), `pkg/chatwoot/handler/webhook_handler.go` (grava mapa + captura wamid), `pkg/events/chatwoot/chatwoot_producer.go` (branch Receipt + handleReceipt), `pkg/chatwoot/service/chatwoot_service.go` (Events com READ_RECEIPT no CreateLink), `cmd/evolution-go/main.go` (AutoMigrate + wire do repo), testes.
**Chatwoot:** nenhuma mudança de código.
