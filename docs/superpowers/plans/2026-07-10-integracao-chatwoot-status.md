# Integração Chatwoot ↔ evolution-go — Fatia Status de Entrega — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Sincronizar `delivered`/`read` das mensagens do agente de volta ao Chatwoot, para o indicador avançar (sent → entregue → lido).

**Architecture:** No envio (webhook receiver), grava `wamid → (conversation display_id, message id)` numa tabela. Quando o WhatsApp devolve `events.Receipt`, o producer (que passa a assinar `READ_RECEIPT`) consulta o mapa e faz `PUT /messages/{id}` com o status. Nenhuma mudança no Chatwoot.

**Tech Stack:** Go 1.25, GORM/Postgres, Gin, whatsmeow. Testes `testing`+`httptest`+`go-sqlmock` (sem testify). Design: `docs/superpowers/specs/2026-07-10-integracao-chatwoot-status-design.md`.

## Global Constraints

- Só mensagens **outgoing** do agente entram no mapa. Recibo de wamid não mapeado → ignora.
- `state` do envelope Receipt: `"Delivered"`→`delivered`, `"Read"`→`read`, `"ReadSelf"`→ignora. `event` == `"Receipt"`; wamids em `data.MessageIDs` (array); estado em `postMap["state"]` no topo do envelope.
- Chatwoot `PUT /api/v1/accounts/{aid}/conversations/{cid}/messages/{mid}` body `{"status": "..."}`, restrito a inbox API; `cid` = conversation display_id, `mid` = message id. Chatwoot bloqueia `read→delivered` sozinho.
- Payload do webhook (outgoing) tem `id` (mid) e `conversation.id` (cid = display_id).
- Envio: `SendText`/`SendMediaFile` retornam `*MessageSendStruct`; `wamid = resp.Info.ID`.
- Subscrição: instância precisa de `READ_RECEIPT` em `Events` (CSV) para o Receipt chegar ao producer. Constante `event_types.READ_RECEIPT = "READ_RECEIPT"`.
- Padrão de testes: package interno, stdlib + httptest + go-sqlmock. Rodar em Docker com deps cgo: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "apk add --no-cache git build-base libjpeg-turbo-dev libwebp-dev >/dev/null && go test ./pkg/chatwoot/... ./pkg/events/chatwoot/... 2>&1 | tail -30"`. Build: `docker build -t evolution-go:local .`. `gofmt -w` os alterados.

---

### Task 1: Model + repositório message_maps

**Files:**
- Create: `pkg/chatwoot/model/message_map.go`, `pkg/chatwoot/repository/message_map_repository.go`, `pkg/chatwoot/repository/message_map_repository_test.go`
- Modify: `cmd/evolution-go/main.go` (AutoMigrate)

**Interfaces:**
- Produces:
  - `chatwoot_model.MessageMap{ Wamid string; ConversationID int; MessageID int; InstanceID string }`
  - `chatwoot_repository.MessageMapRepository`: `Save(m *chatwoot_model.MessageMap) error`, `Get(wamid string) (*chatwoot_model.MessageMap, error)` (nil,nil se ausente).
  - `chatwoot_repository.NewMessageMapRepository(db *gorm.DB) MessageMapRepository`

- [ ] **Step 1: Teste (falhando)** — `message_map_repository_test.go`:

```go
package chatwoot_repository

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB2(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB, WithoutReturning: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm: %v", err)
	}
	return gdb, mock
}

func TestSaveInsertsMap(t *testing.T) {
	gdb, mock := newMockDB2(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "message_maps"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewMessageMapRepository(gdb)
	err := repo.Save(&chatwoot_model.MessageMap{Wamid: "wamid.A", ConversationID: 2, MessageID: 11, InstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar** — `go test ./pkg/chatwoot/repository/... -run TestSaveInsertsMap -v` (via Docker). FAIL (símbolos ausentes).

- [ ] **Step 3: Model** — `pkg/chatwoot/model/message_map.go`:

```go
package chatwoot_model

// MessageMap correlaciona o id de uma mensagem enviada no WhatsApp (wamid) com a
// mensagem correspondente no Chatwoot, para propagar status de entrega/leitura.
type MessageMap struct {
	Wamid          string `json:"wamid" gorm:"primaryKey"`
	ConversationID int    `json:"conversationId"`
	MessageID      int    `json:"messageId"`
	InstanceID     string `json:"instanceId"`
}

func (MessageMap) TableName() string { return "message_maps" }
```

- [ ] **Step 4: Repositório** — `pkg/chatwoot/repository/message_map_repository.go`:

```go
package chatwoot_repository

import (
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MessageMapRepository interface {
	Save(m *chatwoot_model.MessageMap) error
	Get(wamid string) (*chatwoot_model.MessageMap, error)
}

type messageMapRepository struct {
	db *gorm.DB
}

func NewMessageMapRepository(db *gorm.DB) MessageMapRepository {
	return &messageMapRepository{db: db}
}

// Save é idempotente por wamid (upsert).
func (r *messageMapRepository) Save(m *chatwoot_model.MessageMap) error {
	return r.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(m).Error
}

// Get retorna (nil, nil) quando não há mapeamento para o wamid.
func (r *messageMapRepository) Get(wamid string) (*chatwoot_model.MessageMap, error) {
	var m chatwoot_model.MessageMap
	err := r.db.Where("wamid = ?", wamid).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
```

- [ ] **Step 5: AutoMigrate** — em `cmd/evolution-go/main.go`, função `migrate`, adicionar `&chatwoot_model.MessageMap{}` à lista do `db.AutoMigrate(...)` (o import `chatwoot_model` já existe).

- [ ] **Step 6: Rodar teste + build** — teste do repo passa; `docker build` exit 0.

- [ ] **Step 7: Commit** — `git add pkg/chatwoot/model/message_map.go pkg/chatwoot/repository/message_map_repository.go pkg/chatwoot/repository/message_map_repository_test.go cmd/evolution-go/main.go && git commit -m "feat(chatwoot): add message-map model and repository for status sync"`

---

### Task 2: Cliente UpdateMessageStatus

**Files:**
- Modify: `pkg/chatwoot/client/chatwoot_client.go`, `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Produces: `(*Client) UpdateMessageStatus(cid, mid int, status string) error`.

- [ ] **Step 1: Teste (falhando)** — em `chatwoot_client_test.go`:

```go
func TestUpdateMessageStatus(t *testing.T) {
	var gotPath, gotStatus, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotStatus, _ = body["status"].(string)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	if err := c.UpdateMessageStatus(2, 11, "delivered"); err != nil {
		t.Fatalf("UpdateMessageStatus: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/accounts/1/conversations/2/messages/11" || gotStatus != "delivered" {
		t.Fatalf("bad request: %s %s status=%s", gotMethod, gotPath, gotStatus)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar** — FAIL (método ausente).

- [ ] **Step 3: Implementar** — em `chatwoot_client.go`, reusando o helper `do`:

```go
func (c *Client) UpdateMessageStatus(cid, mid int, status string) error {
	body := map[string]any{"status": status}
	path := fmt.Sprintf("/conversations/%d/messages/%d", cid, mid)
	return c.do(http.MethodPut, path, body, nil)
}
```

- [ ] **Step 4: Rodar teste + build** — PASS; `docker build` exit 0. `gofmt -w`.

- [ ] **Step 5: Commit** — `git add pkg/chatwoot/client && git commit -m "feat(chatwoot): add UpdateMessageStatus client method"`

---

### Task 3: Receiver grava o mapa wamid→(cid,mid)

**Files:**
- Modify: `pkg/chatwoot/handler/webhook_handler.go`, `pkg/chatwoot/handler/webhook_handler_test.go`

**Interfaces:**
- Consumes: `chatwoot_repository.MessageMapRepository` (Task 1); `chatwoot_model.MessageMap`.
- Produces: `NewWebhookHandler` ganha `messageMapRepo` como parâmetro (após `configRepo`); `shouldForward` passa a retornar também `mid, cid int` (extraídos do payload).

- [ ] **Step 1: Testes (falhando)** — ajustar chamadas existentes de `shouldForward` para a nova assinatura, e adicionar:

```go
func TestShouldForward_ExtractsIds(t *testing.T) {
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"content":"oi",
		"id":11,
		"conversation":{"id":2,"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}}
	}`)
	jid, text, atts, mid, cid, ok := shouldForward(body)
	if !ok || jid == "" || text != "oi" || len(atts) != 0 || mid != 11 || cid != 2 {
		t.Fatalf("bad: mid=%d cid=%d ok=%v", mid, cid, ok)
	}
}
```
(As demais chamadas de `shouldForward` nos testes existentes recebem os dois novos retornos via `_`.)

- [ ] **Step 2: Rodar e ver falhar** — FAIL (assinatura).

- [ ] **Step 3: Implementar** — em `webhook_handler.go`:

Estender `shouldForward` para extrair `id` (mid) e `conversation.id` (cid):
```go
func shouldForward(body []byte) (jid, text string, attachments []outAttachment, mid, cid int, ok bool) {
	var p struct {
		Event        string          `json:"event"`
		MessageType  string          `json:"message_type"`
		Private      bool            `json:"private"`
		Content      string          `json:"content"`
		Attachments  []outAttachment `json:"attachments"`
		ID           int             `json:"id"`
		Conversation struct {
			ID           int `json:"id"`
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", nil, 0, 0, false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", nil, 0, 0, false
	}
	if p.Conversation.ContactInbox.SourceID == "" {
		return "", "", nil, 0, 0, false
	}
	if p.Content == "" && len(p.Attachments) == 0 {
		return "", "", nil, 0, 0, false
	}
	return p.Conversation.ContactInbox.SourceID, p.Content, p.Attachments, p.ID, p.Conversation.ID, true
}
```

Adicionar `messageMapRepo` ao struct `WebhookHandler` e a `NewWebhookHandler(instanceRepo, sendService, configRepo, messageMapRepo, loggerWrapper)`.

No `Handle`, capturar o wamid do retorno e gravar o mapa. Para o ramo sem anexo (texto):
```go
		resp, err := h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance)
		if err != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		h.storeMap(resp, mid, cid, instance.Id)
		ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
		return
```
Para o ramo de anexos, após cada `SendMediaFile` bem-sucedido:
```go
		resp, serr := h.sendService.SendMediaFile(media, fileBytes, instance)
		if serr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao enviar mídia: %v", instance.Id, serr)
			continue
		}
		sentCount++
		h.storeMap(resp, mid, cid, instance.Id)
```
Helper:
```go
func (h *WebhookHandler) storeMap(resp *send_service.MessageSendStruct, mid, cid int, instanceID string) {
	if resp == nil || mid == 0 || cid == 0 {
		return
	}
	wamid := resp.Info.ID
	if wamid == "" {
		return
	}
	if err := h.messageMapRepo.Save(&chatwoot_model.MessageMap{
		Wamid: wamid, ConversationID: cid, MessageID: mid, InstanceID: instanceID,
	}); err != nil {
		h.loggerWrapper.GetLogger(instanceID).LogError("[%s] chatwoot: falha ao gravar message map: %v", instanceID, err)
	}
}
```
Adicionar imports `chatwoot_model`, `chatwoot_repository`. Atualizar a chamada de `shouldForward` no `Handle` para os 6 retornos.

- [ ] **Step 4: Rodar testes do handler + build** — o build vai FALHAR até a Task 5 atualizar o call-site de `NewWebhookHandler` em main.go. Rodar os testes do pacote handler (compila isoladamente? não — o pacote referencia o novo param só na assinatura; compila). Rodar `go test ./pkg/chatwoot/handler/...`. Deixar o `docker build` para a Task 5. `gofmt -w`.

- [ ] **Step 5: Commit** (pode ser junto da Task 5 se preferir manter o build verde) — `git add pkg/chatwoot/handler && git commit -m "feat(chatwoot): record wamid->message mapping on outgoing send"`

---

### Task 4: Producer trata eventos Receipt

**Files:**
- Modify: `pkg/events/chatwoot/chatwoot_producer.go`, `pkg/events/chatwoot/chatwoot_producer_test.go`

**Interfaces:**
- Consumes: `chatwoot_repository.MessageMapRepository`; `chatwoot_client` (UpdateMessageStatus); `chatwoot_repository.ChatwootConfigRepository` (já injetado).
- Produces: `NewChatwootProducer` ganha `messageMapRepo` (após `instanceRepo`); função pura `parseReceipt(payload []byte) (state string, wamids []string, instanceID string, ok bool)`.

- [ ] **Step 1: Testes de parse (falhando)** — em `chatwoot_producer_test.go`:

```go
func TestParseReceipt_Delivered(t *testing.T) {
	env := []byte(`{"event":"Receipt","state":"Delivered","instanceId":"inst-1","data":{"MessageIDs":["wamid.A","wamid.B"]}}`)
	state, ids, inst, ok := parseReceipt(env)
	if !ok || state != "Delivered" || inst != "inst-1" || len(ids) != 2 || ids[0] != "wamid.A" {
		t.Fatalf("bad: state=%s ids=%v inst=%s ok=%v", state, ids, inst, ok)
	}
}

func TestParseReceipt_IgnoresNonReceipt(t *testing.T) {
	if _, _, _, ok := parseReceipt([]byte(`{"event":"Message","data":{}}`)); ok {
		t.Fatal("esperado ok=false para não-Receipt")
	}
}

func TestParseReceipt_IgnoresReadSelf(t *testing.T) {
	if _, _, _, ok := parseReceipt([]byte(`{"event":"Receipt","state":"ReadSelf","instanceId":"i","data":{"MessageIDs":["x"]}}`)); ok {
		t.Fatal("esperado ok=false para ReadSelf")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar** — FAIL.

- [ ] **Step 3: Implementar** — em `chatwoot_producer.go`:

Adicionar `messageMapRepo` ao struct `chatwootProducer` e ao construtor `NewChatwootProducer(configRepo, instanceRepo, messageMapRepo, loggerWrapper)`.

`parseReceipt` (pura):
```go
// parseReceipt extrai o estado e os wamids de um envelope de Receipt.
// Retorna ok=false para não-Receipt ou ReadSelf (ignorado).
func parseReceipt(payload []byte) (state string, wamids []string, instanceID string, ok bool) {
	var env struct {
		Event      string `json:"event"`
		State      string `json:"state"`
		InstanceID string `json:"instanceId"`
		Data       struct {
			MessageIDs []string `json:"MessageIDs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", nil, "", false
	}
	if env.Event != "Receipt" {
		return "", nil, "", false
	}
	if env.State != "Delivered" && env.State != "Read" {
		return "", nil, "", false
	}
	if len(env.Data.MessageIDs) == 0 {
		return "", nil, "", false
	}
	return env.State, env.Data.MessageIDs, env.InstanceID, true
}
```

No `Produce`, tratar Receipt antes do fluxo de mensagem:
```go
func (p *chatwootProducer) Produce(queueName string, payload []byte, _ string, userID string) error {
	if _, _, _, ok := parseReceipt(payload); ok {
		go p.handleReceipt(payload, userID)
		return nil
	}
	msg, ok := parseIncoming(payload)
	if !ok {
		return nil
	}
	cacheKey := msg.InstanceID + "|" + msg.JID
	p.workerFor(cacheKey, userID) <- payload
	return nil
}
```

`handleReceipt`:
```go
func (p *chatwootProducer) handleReceipt(payload []byte, userID string) {
	log := p.loggerWrapper.GetLogger(userID)
	state, wamids, _, ok := parseReceipt(payload)
	if !ok {
		return
	}
	status := "delivered"
	if state == "Read" {
		status = "read"
	}
	cfg, err := p.configRepo.Get()
	if err != nil || cfg == nil {
		return
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
	for _, wamid := range wamids {
		m, err := p.messageMapRepo.Get(wamid)
		if err != nil || m == nil {
			continue // wamid não mapeado (ex.: msg antiga) — ignora
		}
		if err := client.UpdateMessageStatus(m.ConversationID, m.MessageID, status); err != nil {
			log.LogError("[%s] chatwoot: falha ao atualizar status (%s): %v", userID, status, err)
		}
	}
}
```

- [ ] **Step 4: Rodar testes do producer + build** — o build só fecha com a Task 5 (main.go passa o novo param). Rodar `go test ./pkg/events/chatwoot/...`; deixar o `docker build` para a Task 5. `gofmt -w`.

- [ ] **Step 5: Commit** — `git add pkg/events/chatwoot && git commit -m "feat(chatwoot): update chatwoot message status from whatsapp receipts"`

---

### Task 5: Subscrição READ_RECEIPT + wire + verificação

**Files:**
- Modify: `pkg/chatwoot/service/chatwoot_service.go` (CreateLink Events), `cmd/evolution-go/main.go` (repo + constructors)

**Interfaces:**
- Consumes: tudo das Tasks 1–4.

- [ ] **Step 1: CreateLink assina READ_RECEIPT** — em `chatwoot_service.go`, localizar `created.Events = event_types.MESSAGE` e trocar por:
```go
	created.Events = event_types.MESSAGE + "," + event_types.READ_RECEIPT
```

- [ ] **Step 2: Wire em main.go** — localizar por conteúdo:
  - Criar o repo junto aos demais (após `chatwootConfigRepo := ...`): `chatwootMessageMapRepo := chatwoot_repository.NewMessageMapRepository(db)`.
  - Passar `chatwootMessageMapRepo` em `NewChatwootProducer(chatwootConfigRepo, instanceRepository, chatwootMessageMapRepo, loggerWrapper)`.
  - Passar `chatwootMessageMapRepo` em `NewWebhookHandler(instanceRepository, sendMessageService, chatwootConfigRepo, chatwootMessageMapRepo, loggerWrapper)`.
  Manter a ordem exata dos parâmetros conforme as novas assinaturas (Tasks 3 e 4). Verificar lendo as duas assinaturas.

- [ ] **Step 3: Build** — `docker build -t evolution-go:local .` (exit 0 — compila Tasks 3+4+5).

- [ ] **Step 4: Rodar a suíte inteira** — `go test ./pkg/chatwoot/... ./pkg/events/chatwoot/...` (via Docker) — tudo verde.

- [ ] **Step 5: Recriar container + backfill da ritoDesk** — a instância existente `ritoDesk` tem `Events="MESSAGE"`; para receber recibos agora, atualizar no banco:
```bash
docker compose -f docker-compose.local.yml exec -T postgres psql -U postgres -d evogo_users -c "update instances set events='MESSAGE,READ_RECEIPT' where name='ritoDesk';"
docker compose -f docker-compose.local.yml up -d --force-recreate evolution-go
# ritoDesk reconecta sozinha (CONNECT_ON_STARTUP=true); os Events atualizados valem no reconnect.
```

- [ ] **Step 6: Verificação end-to-end (manual)** — com a `ritoDesk` conectada:
  - Envie uma mensagem pela tela do Chatwoot → ela deve marcar **entregue** (2 checks) quando o aparelho do cliente receber, e **lido** quando o cliente abrir a conversa.
  - Verificar logs do producer se algum status falhar (`falha ao atualizar status`).

- [ ] **Step 7: Commit** — `git add pkg/chatwoot/service/chatwoot_service.go cmd/evolution-go/main.go && git commit -m "feat(chatwoot): subscribe to read receipts and wire status sync"`

## Notas

- A tabela `message_maps` cresce sem poda (aceitável no MVP; poda é evolução futura).
- Recibos de wamids não mapeados (mensagens anteriores a esta fatia) são ignorados silenciosamente — normal.
