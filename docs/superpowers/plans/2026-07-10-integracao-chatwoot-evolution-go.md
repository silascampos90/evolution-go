# Integração Chatwoot ↔ evolution-go — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ligar cada instância de WhatsApp do evolution-go a uma inbox do Chatwoot (canal API), com mensagens de texto 1:1 fluindo nos dois sentidos e uma tela de gestão em `/chatwoot-admin`.

**Architecture:** Tudo nativo em Go dentro do evolution-go. Um *producer* novo injeta mensagens recebidas do WhatsApp na API do Chatwoot; um *webhook receiver* recebe as respostas do agente e as envia ao WhatsApp via o `SendService` existente; um *service* de gestão auto-provisiona a inbox no Chatwoot e guarda o vínculo em campos do model `Instance`. A comunicação de rede é por uma rede Docker compartilhada.

**Tech Stack:** Go 1.25, Gin, GORM (Postgres), whatsmeow. Testes com `testing` puro + `httptest` + `go-sqlmock` (sem testify). Design de referência: `docs/superpowers/specs/2026-07-10-integracao-chatwoot-evolution-go-design.md`.

## Global Constraints

- **Escopo MVP:** só texto, só conversas 1:1. Ignorar grupos (`@g.us`), status (`status@broadcast`), mídia. Sem confirmação de entrega (✓✓). Sem fila persistente de reenvio.
- **Correlação:** o JID do WhatsApp (ex: `5511988880001@s.whatsapp.net`) é o `source_id` no Chatwoot. Sem tabela de-para.
- **Vínculo instância↔inbox:** campos no model `Instance`, não tabela separada.
- **Padrão de testes:** package interno (mesmo package do código, para testar funções não exportadas), `testing` stdlib, `t.Fatalf`. HTTP externo mockado com `net/http/httptest`.
- **Padrão de código:** seguir o estilo existente do evolution-go (nomes de pacote `snake_case` como `chatwoot_client`, construtores `NewX`, erros retornados, logs via `loggerWrapper`).
- **Rede Docker:** evolution chama `http://chatwoot-rails:3000`; webhook_url é `http://evolution-go:8080/chatwoot/webhook/<nome>`.
- **Gate de licença:** já neutralizado (patch offline em `pkg/core/c0.go`). A rota do webhook receiver NÃO leva `authMiddleware.Auth` (o Chatwoot chama sem `apikey`); autentica-se por HMAC.
- **Build/verify:** `docker build -t evolution-go:local .` compila o projeto (não há Go no host). Testes: `docker run --rm -v $(pwd):/app -w /app golang:1.25.0-alpine go test ./pkg/chatwoot/... ./pkg/events/chatwoot/...` ou, se houver Go local, `go test`.

---

### Task 1: Campos de vínculo Chatwoot no model Instance

**Files:**
- Modify: `pkg/instance/model/instance_model.go:10-36`

**Interfaces:**
- Produces: campos novos no struct `Instance`: `ChatwootEnabled bool`, `ChatwootInboxID string`, `ChatwootInboxIdentifier string`, `ChatwootWebhookSecret string`. Usados pelas Tasks 4, 5, 6, 7.

- [ ] **Step 1: Adicionar os campos ao struct `Instance`**

Em `pkg/instance/model/instance_model.go`, dentro do struct `Instance`, logo após a linha `CreatedAt        time.Time ...` (linha 27) e antes do comentário `// Advanced Settings`, inserir:

```go
	// Chatwoot integration
	ChatwootEnabled         bool   `json:"chatwootEnabled" gorm:"default:false"`
	ChatwootInboxID         string `json:"chatwootInboxId" gorm:"default:''"`
	ChatwootInboxIdentifier string `json:"chatwootInboxIdentifier" gorm:"default:''"`
	ChatwootWebhookSecret   string `json:"chatwootWebhookSecret" gorm:"default:''"`
```

- [ ] **Step 2: Verificar que o AutoMigrate cobre o Instance**

Confirmar (leitura) que `cmd/evolution-go/main.go:265` já chama `db.AutoMigrate(&instance_model.Instance{}, ...)`. Nenhuma mudança necessária — o AutoMigrate cria as colunas novas automaticamente no próximo boot.

- [ ] **Step 3: Build para garantir que compila**

Run: `docker build -t evolution-go:local .`
Expected: build OK (exit 0), imagem gerada.

- [ ] **Step 4: Commit**

```bash
git add pkg/instance/model/instance_model.go
git commit -m "feat(chatwoot): add instance link fields for chatwoot integration"
```

---

### Task 2: Model + repositório da config global do Chatwoot

**Files:**
- Create: `pkg/chatwoot/model/chatwoot_config.go`
- Create: `pkg/chatwoot/repository/chatwoot_config_repository.go`
- Create: `pkg/chatwoot/repository/chatwoot_config_repository_test.go`
- Modify: `cmd/evolution-go/main.go:265` (adicionar o model ao AutoMigrate)

**Interfaces:**
- Produces:
  - `chatwoot_model.ChatwootConfig{ ID uint; BaseURL string; APIToken string; AccountID string }`
  - `chatwoot_repository.ChatwootConfigRepository` interface: `Get() (*chatwoot_model.ChatwootConfig, error)`, `Save(cfg *chatwoot_model.ChatwootConfig) error`
  - `chatwoot_repository.NewChatwootConfigRepository(db *gorm.DB) ChatwootConfigRepository`

- [ ] **Step 1: Escrever o teste do repositório (falhando)**

Create `pkg/chatwoot/repository/chatwoot_config_repository_test.go`:

```go
package chatwoot_repository

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sqlmock: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB, WithoutReturning: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	return gdb, mock
}

func TestSaveInsertsWhenNoRow(t *testing.T) {
	gdb, mock := newMockDB(t)

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "chatwoot_configs"`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewChatwootConfigRepository(gdb)
	err := repo.Save(&chatwoot_model.ChatwootConfig{BaseURL: "http://x:3000", APIToken: "tok", AccountID: "1"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/repository/... -run TestSaveInsertsWhenNoRow -v"`
Expected: FAIL — pacotes `chatwoot_model`/`NewChatwootConfigRepository` não existem.

- [ ] **Step 3: Criar o model**

Create `pkg/chatwoot/model/chatwoot_config.go`:

```go
package chatwoot_model

// ChatwootConfig é um singleton (uma linha) com os dados de acesso à API do Chatwoot.
type ChatwootConfig struct {
	ID        uint   `json:"id" gorm:"primaryKey"`
	BaseURL   string `json:"baseUrl"`
	APIToken  string `json:"-"` // sensível: nunca serializar de volta
	AccountID string `json:"accountId"`
}

func (ChatwootConfig) TableName() string { return "chatwoot_configs" }
```

- [ ] **Step 4: Criar o repositório**

Create `pkg/chatwoot/repository/chatwoot_config_repository.go`:

```go
package chatwoot_repository

import (
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"gorm.io/gorm"
)

type ChatwootConfigRepository interface {
	Get() (*chatwoot_model.ChatwootConfig, error)
	Save(cfg *chatwoot_model.ChatwootConfig) error
}

type chatwootConfigRepository struct {
	db *gorm.DB
}

func NewChatwootConfigRepository(db *gorm.DB) ChatwootConfigRepository {
	return &chatwootConfigRepository{db: db}
}

// Get retorna a config singleton, ou (nil, nil) se ainda não existe.
func (r *chatwootConfigRepository) Get() (*chatwoot_model.ChatwootConfig, error) {
	var cfg chatwoot_model.ChatwootConfig
	err := r.db.First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save mantém sempre uma única linha (ID=1).
func (r *chatwootConfigRepository) Save(cfg *chatwoot_model.ChatwootConfig) error {
	cfg.ID = 1
	return r.db.Save(cfg).Error
}
```

- [ ] **Step 5: Registrar o model no AutoMigrate**

Em `cmd/evolution-go/main.go`, na função `migrate` (linha ~265), adicionar `&chatwoot_model.ChatwootConfig{}` à lista do `db.AutoMigrate(...)`. Adicionar o import `chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"`. Resultado:

```go
	err := db.AutoMigrate(&instance_model.Instance{}, &message_model.Message{}, &label_model.Label{}, &chatwoot_model.ChatwootConfig{})
```

- [ ] **Step 6: Rodar o teste e ver passar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/repository/... -v"`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/chatwoot/model pkg/chatwoot/repository cmd/evolution-go/main.go
git commit -m "feat(chatwoot): add global config model and repository"
```

---

### Task 3: Cliente HTTP da API do Chatwoot

**Files:**
- Create: `pkg/chatwoot/client/chatwoot_client.go`
- Create: `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Consumes: nada (autocontido).
- Produces:
  - `chatwoot_client.Client` com `NewClient(baseURL, apiToken, accountID string) *Client`
  - `(*Client) CreateInbox(name, webhookURL string) (*Inbox, error)` — `Inbox{ ID int; Identifier string; Secret string }`
  - `(*Client) FindOrCreateContact(name, phone, sourceID string, inboxID int) (*Contact, error)` — `Contact{ ID int }`
  - `(*Client) CreateConversation(inboxID, contactID int, sourceID string) (*Conversation, error)` — `Conversation{ ID int }`
  - `(*Client) CreateIncomingMessage(conversationID int, content, sourceID string) error`
  - `(*Client) Ping() error`

- [ ] **Step 1: Escrever o teste (falhando) com httptest**

Create `pkg/chatwoot/client/chatwoot_client_test.go`:

```go
package chatwoot_client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateInboxParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api_access_token") != "tok" {
			t.Fatalf("missing api_access_token header")
		}
		if r.URL.Path != "/api/v1/accounts/1/inboxes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":               42,
			"inbox_identifier": "abc123",
			"channel": map[string]any{"secret": "s3cr3t"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	inbox, err := c.CreateInbox("vendas", "http://evolution-go:8080/chatwoot/webhook/vendas")
	if err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if inbox.ID != 42 || inbox.Identifier != "abc123" || inbox.Secret != "s3cr3t" {
		t.Fatalf("bad inbox: %+v", inbox)
	}
}

func TestCreateIncomingMessageSendsCorrectBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"id": 5})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	if err := c.CreateIncomingMessage(7, "olá", "wamid.X"); err != nil {
		t.Fatalf("CreateIncomingMessage: %v", err)
	}
	if got["content"] != "olá" || got["message_type"] != "incoming" || got["source_id"] != "wamid.X" {
		t.Fatalf("bad body: %+v", got)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/client/... -v"`
Expected: FAIL — pacote não existe.

- [ ] **Step 3: Implementar o cliente**

Create `pkg/chatwoot/client/chatwoot_client.go`:

```go
package chatwoot_client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL   string
	apiToken  string
	accountID string
	http      *http.Client
}

func NewClient(baseURL, apiToken, accountID string) *Client {
	return &Client{
		baseURL:   baseURL,
		apiToken:  apiToken,
		accountID: accountID,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

type Inbox struct {
	ID         int
	Identifier string
	Secret     string
}
type Contact struct{ ID int }
type Conversation struct{ ID int }

func (c *Client) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	url := fmt.Sprintf("%s/api/v1/accounts/%s%s", c.baseURL, c.accountID, path)
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.apiToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chatwoot %s %s -> %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

// Ping valida a config chamando um endpoint leve da conta.
func (c *Client) Ping() error {
	return c.do(http.MethodGet, "/conversations", nil, nil)
}

func (c *Client) CreateInbox(name, webhookURL string) (*Inbox, error) {
	body := map[string]any{
		"name": name,
		"channel": map[string]any{
			"type":        "api",
			"webhook_url": webhookURL,
		},
	}
	var raw struct {
		ID         int    `json:"id"`
		Identifier string `json:"inbox_identifier"`
		Channel    struct {
			Secret string `json:"secret"`
		} `json:"channel"`
	}
	if err := c.do(http.MethodPost, "/inboxes", body, &raw); err != nil {
		return nil, err
	}
	return &Inbox{ID: raw.ID, Identifier: raw.Identifier, Secret: raw.Channel.Secret}, nil
}

// FindOrCreateContact cria um contato e o contact_inbox com source_id.
// O Chatwoot deduplica por telefone; em caso de conflito, faz a busca por source_id.
func (c *Client) FindOrCreateContact(name, phone, sourceID string, inboxID int) (*Contact, error) {
	body := map[string]any{
		"name":          name,
		"phone_number":  phone,
		"inbox_id":      inboxID,
		"source_id":     sourceID,
	}
	var raw struct {
		Payload struct {
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodPost, "/contacts", body, &raw); err != nil {
		return nil, err
	}
	return &Contact{ID: raw.Payload.Contact.ID}, nil
}

func (c *Client) CreateConversation(inboxID, contactID int, sourceID string) (*Conversation, error) {
	body := map[string]any{
		"inbox_id":   inboxID,
		"contact_id": contactID,
		"source_id":  sourceID,
	}
	var raw struct {
		ID int `json:"id"`
	}
	if err := c.do(http.MethodPost, "/conversations", body, &raw); err != nil {
		return nil, err
	}
	return &Conversation{ID: raw.ID}, nil
}

func (c *Client) CreateIncomingMessage(conversationID int, content, sourceID string) error {
	body := map[string]any{
		"content":      content,
		"message_type": "incoming",
		"source_id":    sourceID,
	}
	path := fmt.Sprintf("/conversations/%d/messages", conversationID)
	return c.do(http.MethodPost, path, body, nil)
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/client/... -v"`
Expected: PASS (2 testes).

- [ ] **Step 5: Commit**

```bash
git add pkg/chatwoot/client
git commit -m "feat(chatwoot): add http client for chatwoot application api"
```

---

### Task 4: Chatwoot Producer (WhatsApp → Chatwoot)

**Files:**
- Create: `pkg/events/chatwoot/chatwoot_producer.go`
- Create: `pkg/events/chatwoot/chatwoot_producer_test.go`

**Interfaces:**
- Consumes: `chatwoot_client.Client` (Task 3); `chatwoot_repository.ChatwootConfigRepository` (Task 2); `instance_repository.InstanceRepository` (existente).
- Produces:
  - `chatwoot_producer.NewChatwootProducer(configRepo, instanceRepo, loggerWrapper) producer_interfaces.Producer`
  - função pura testável `parseIncomingText(payload []byte) (*incomingMsg, bool)` onde `incomingMsg{ JID, PushName, Text, Wamid, InstanceID string }`, retornando `false` quando o evento deve ser ignorado (não é Message, é FromMe, é grupo/status, ou não tem texto).

- [ ] **Step 1: Escrever o teste da função pura de parse (falhando)**

Create `pkg/events/chatwoot/chatwoot_producer_test.go`:

```go
package chatwoot_producer

import "testing"

func envelope(sender, chat, pushName, text, id string, fromMe bool) []byte {
	return []byte(`{
		"event":"Message",
		"instanceId":"inst-1",
		"data":{
			"Info":{"Sender":"` + sender + `","Chat":"` + chat + `","PushName":"` + pushName + `","ID":"` + id + `","IsFromMe":` + boolStr(fromMe) + `},
			"Message":{"conversation":"` + text + `"}
		}
	}`)
}
func boolStr(b bool) string { if b { return "true" }; return "false" }

func TestParseIncomingText_Valid(t *testing.T) {
	msg, ok := parseIncomingText(envelope("5511988880001@s.whatsapp.net", "5511988880001@s.whatsapp.net", "João", "olá", "wamid.A", false))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if msg.JID != "5511988880001@s.whatsapp.net" || msg.PushName != "João" || msg.Text != "olá" || msg.Wamid != "wamid.A" {
		t.Fatalf("bad parse: %+v", msg)
	}
}

func TestParseIncomingText_IgnoresFromMe(t *testing.T) {
	if _, ok := parseIncomingText(envelope("x@s.whatsapp.net", "x@s.whatsapp.net", "me", "oi", "wamid.B", true)); ok {
		t.Fatal("expected ok=false for FromMe")
	}
}

func TestParseIncomingText_IgnoresGroup(t *testing.T) {
	if _, ok := parseIncomingText(envelope("x@s.whatsapp.net", "123-456@g.us", "grp", "oi", "wamid.C", false)); ok {
		t.Fatal("expected ok=false for group chat")
	}
}

func TestParseIncomingText_IgnoresNonMessage(t *testing.T) {
	if _, ok := parseIncomingText([]byte(`{"event":"Receipt","instanceId":"i","data":{}}`)); ok {
		t.Fatal("expected ok=false for non-Message event")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/events/chatwoot/... -v"`
Expected: FAIL — pacote/func não existem.

- [ ] **Step 3: Implementar o producer + parse**

Create `pkg/events/chatwoot/chatwoot_producer.go`:

```go
package chatwoot_producer

import (
	"encoding/json"
	"strings"
	"sync"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	producer_interfaces "github.com/evolution-foundation/evolution-go/pkg/events/interfaces"
	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
)

type incomingMsg struct {
	JID        string
	PushName   string
	Text       string
	Wamid      string
	InstanceID string
}

// parseIncomingText extrai uma mensagem de texto 1:1 recebida do envelope de evento.
// Retorna ok=false quando o evento deve ser ignorado.
func parseIncomingText(payload []byte) (*incomingMsg, bool) {
	var env struct {
		Event      string `json:"event"`
		InstanceID string `json:"instanceId"`
		Data       struct {
			Info struct {
				Sender   string `json:"Sender"`
				Chat     string `json:"Chat"`
				PushName string `json:"PushName"`
				ID       string `json:"ID"`
				IsFromMe bool   `json:"IsFromMe"`
			} `json:"Info"`
			Message struct {
				Conversation      string `json:"conversation"`
				ExtendedTextMsg   struct {
					Text string `json:"text"`
				} `json:"extendedTextMessage"`
			} `json:"Message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, false
	}
	if env.Event != "Message" || env.Data.Info.IsFromMe {
		return nil, false
	}
	chat := env.Data.Info.Chat
	if strings.HasSuffix(chat, "@g.us") || strings.Contains(chat, "status@broadcast") {
		return nil, false
	}
	text := env.Data.Message.Conversation
	if text == "" {
		text = env.Data.Message.ExtendedTextMsg.Text
	}
	if text == "" {
		return nil, false
	}
	return &incomingMsg{
		JID:        env.Data.Info.Sender,
		PushName:   env.Data.Info.PushName,
		Text:       text,
		Wamid:      env.Data.Info.ID,
		InstanceID: env.InstanceID,
	}, true
}

type chatwootProducer struct {
	configRepo    chatwoot_repository.ChatwootConfigRepository
	instanceRepo  instance_repository.InstanceRepository
	loggerWrapper *logger_wrapper.LoggerManager
	// cache jid -> conversationID por instância, para pular lookups
	convCache sync.Map // key: instanceID+"|"+jid  value: convCacheEntry
}

type convCacheEntry struct {
	ContactID      int
	ConversationID int
}

func NewChatwootProducer(
	configRepo chatwoot_repository.ChatwootConfigRepository,
	instanceRepo instance_repository.InstanceRepository,
	loggerWrapper *logger_wrapper.LoggerManager,
) producer_interfaces.Producer {
	return &chatwootProducer{
		configRepo:    configRepo,
		instanceRepo:  instanceRepo,
		loggerWrapper: loggerWrapper,
	}
}

func (p *chatwootProducer) CreateGlobalQueues() error { return nil }

// Produce recebe o envelope de evento; roda de forma assíncrona.
func (p *chatwootProducer) Produce(queueName string, payload []byte, _ string, userID string) error {
	go p.handle(payload, userID)
	return nil
}

func (p *chatwootProducer) handle(payload []byte, userID string) {
	log := p.loggerWrapper.GetLogger(userID)

	msg, ok := parseIncomingText(payload)
	if !ok {
		return
	}

	instance, err := p.instanceRepo.GetInstanceByID(msg.InstanceID)
	if err != nil || !instance.ChatwootEnabled || instance.ChatwootInboxID == "" {
		return
	}

	cfg, err := p.configRepo.Get()
	if err != nil || cfg == nil {
		log.LogError("[%s] chatwoot: config global ausente", userID)
		return
	}

	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
	inboxID := atoi(instance.ChatwootInboxID)

	cacheKey := msg.InstanceID + "|" + msg.JID
	if v, ok := p.convCache.Load(cacheKey); ok {
		entry := v.(convCacheEntry)
		if err := client.CreateIncomingMessage(entry.ConversationID, msg.Text, msg.Wamid); err != nil {
			log.LogError("[%s] chatwoot: falha ao injetar mensagem: %v", userID, err)
		}
		return
	}

	contact, err := client.FindOrCreateContact(msg.PushName, phoneFromJID(msg.JID), msg.JID, inboxID)
	if err != nil {
		log.LogError("[%s] chatwoot: falha contato: %v", userID, err)
		return
	}
	conv, err := client.CreateConversation(inboxID, contact.ID, msg.JID)
	if err != nil {
		log.LogError("[%s] chatwoot: falha conversa: %v", userID, err)
		return
	}
	p.convCache.Store(cacheKey, convCacheEntry{ContactID: contact.ID, ConversationID: conv.ID})

	if err := client.CreateIncomingMessage(conv.ID, msg.Text, msg.Wamid); err != nil {
		log.LogError("[%s] chatwoot: falha ao injetar mensagem: %v", userID, err)
	}
}

func phoneFromJID(jid string) string {
	num := jid
	if i := strings.IndexByte(num, '@'); i >= 0 {
		num = num[:i]
	}
	return "+" + num
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/events/chatwoot/... -v"`
Expected: PASS (4 testes).

- [ ] **Step 5: Commit**

```bash
git add pkg/events/chatwoot
git commit -m "feat(chatwoot): add producer that injects incoming whatsapp messages"
```

---

### Task 5: Ligar o producer no whatsmeowService

**Files:**
- Modify: `pkg/whatsmeow/service/whatsmeow.go` (struct `whatsmeowService` ~79-100; construtor `NewWhatsmeowService` ~2793-2835; `sendToQueueOrWebhook` ~2285-2321)
- Modify: `cmd/evolution-go/main.go` (instanciar o producer ~132 e passar no `NewWhatsmeowService` ~165)

**Interfaces:**
- Consumes: `chatwoot_producer.NewChatwootProducer(...)` (Task 4); `chatwoot_repository.NewChatwootConfigRepository(db)` (Task 2).
- Produces: campo `chatwootProducer producer_interfaces.Producer` no `whatsmeowService`, disparado dentro de `sendToQueueOrWebhook` quando `instance.ChatwootEnabled`.

- [ ] **Step 1: Adicionar o campo ao struct `whatsmeowService`**

Em `pkg/whatsmeow/service/whatsmeow.go`, no struct `whatsmeowService` (após `natsProducer producer_interfaces.Producer`, linha ~97):

```go
	chatwootProducer   producer_interfaces.Producer
```

- [ ] **Step 2: Adicionar o parâmetro ao construtor e atribuir**

Na assinatura de `NewWhatsmeowService` (~2793), adicionar como último parâmetro antes de `loggerWrapper`:

```go
	chatwootProducer producer_interfaces.Producer,
```

E no literal de retorno `&whatsmeowService{...}` (após `natsProducer: natsProducer,`):

```go
		chatwootProducer:   chatwootProducer,
```

- [ ] **Step 3: Adicionar o branch em `sendToQueueOrWebhook`**

Em `sendToQueueOrWebhook` (~2285), após o bloco do webhook (após a linha 2320, antes do `}` final da função):

```go
	if instance.ChatwootEnabled {
		err := w.chatwootProducer.Produce(queueName, jsonData, "", instance.Id)
		if err != nil {
			w.loggerWrapper.GetLogger(instance.Id).LogError("[%s] Failed to send message to chatwoot: %s", instance.Id, err)
			return
		}
	}
```

- [ ] **Step 4: Instanciar o producer e a repo em main.go**

Em `cmd/evolution-go/main.go`, após a linha `websocketProducer := websocket_producer.NewWebsocketProducer(loggerWrapper)` (~linha 133), adicionar:

```go
	chatwootConfigRepo := chatwoot_repository.NewChatwootConfigRepository(db)
	chatwootProducer := chatwoot_producer.NewChatwootProducer(chatwootConfigRepo, instanceRepository, loggerWrapper)
```

Atenção à ordem: `instanceRepository` é criado na linha ~161 (`instance_repository.NewInstanceRepository(db)`). **Mover** a criação de `instanceRepository` para ANTES deste bloco (logo após os producers), ou criar `chatwootProducer` logo após a linha do `instanceRepository`. Escolha: inserir as duas linhas acima logo após `instanceRepository := instance_repository.NewInstanceRepository(db)` (linha ~161).

Adicionar os imports:

```go
	chatwoot_producer "github.com/evolution-foundation/evolution-go/pkg/events/chatwoot"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
```

Passar `chatwootProducer` na chamada `whatsmeow_service.NewWhatsmeowService(...)` como último argumento antes de `loggerWrapper`.

- [ ] **Step 5: Build**

Run: `docker build -t evolution-go:local .`
Expected: build OK (exit 0).

- [ ] **Step 6: Smoke test — subir e ver o branch não quebrar o boot**

Run: `docker compose -f docker-compose.local.yml up -d && sleep 8 && docker compose -f docker-compose.local.yml logs evolution-go | tail -5`
Expected: boot normal, sem panic; log de licença offline presente.

- [ ] **Step 7: Commit**

```bash
git add pkg/whatsmeow/service/whatsmeow.go cmd/evolution-go/main.go
git commit -m "feat(chatwoot): wire chatwoot producer into event dispatch"
```

---

### Task 6: Webhook Receiver (Chatwoot → WhatsApp)

**Files:**
- Create: `pkg/chatwoot/handler/webhook_handler.go`
- Create: `pkg/chatwoot/handler/webhook_handler_test.go`

**Interfaces:**
- Consumes: `instance_repository.InstanceRepository` (`GetInstanceByName`); `send_service.SendService` (`SendText`); `send_service.TextStruct`.
- Produces:
  - `chatwoot_handler.NewWebhookHandler(instanceRepo, sendService, loggerWrapper) *WebhookHandler`
  - `(*WebhookHandler) Handle(ctx *gin.Context)` — rota `POST /chatwoot/webhook/:instance`
  - funções puras testáveis: `validSignature(secret, timestamp string, body []byte, header string) bool` e `shouldForward(payload []byte) (jid, text string, ok bool)`.

- [ ] **Step 1: Escrever os testes das funções puras (falhando)**

Create `pkg/chatwoot/handler/webhook_handler_test.go`:

```go
package chatwoot_handler

import "testing"

func TestShouldForward_OutgoingText(t *testing.T) {
	body := []byte(`{
		"event":"message_created",
		"message_type":"outgoing",
		"private":false,
		"content":"bom dia!",
		"conversation":{"contact_inbox":{"source_id":"5511988880001@s.whatsapp.net"}}
	}`)
	jid, text, ok := shouldForward(body)
	if !ok || jid != "5511988880001@s.whatsapp.net" || text != "bom dia!" {
		t.Fatalf("expected forward; got jid=%q text=%q ok=%v", jid, text, ok)
	}
}

func TestShouldForward_IgnoresIncoming(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"incoming","private":false,"content":"olá","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for incoming (anti-echo)")
	}
}

func TestShouldForward_IgnoresPrivateNote(t *testing.T) {
	body := []byte(`{"event":"message_created","message_type":"outgoing","private":true,"content":"nota","conversation":{"contact_inbox":{"source_id":"x"}}}`)
	if _, _, ok := shouldForward(body); ok {
		t.Fatal("expected ok=false for private note")
	}
}

func TestValidSignature(t *testing.T) {
	// HMAC_SHA256("s3cr3t", "123.body") calculado externamente
	secret := "s3cr3t"
	ts := "123"
	body := []byte("body")
	good := computeSig(secret, ts, body) // helper exposto no impl para o teste
	if !validSignature(secret, ts, body, "sha256="+good) {
		t.Fatal("expected valid signature")
	}
	if validSignature(secret, ts, body, "sha256=deadbeef") {
		t.Fatal("expected invalid signature to fail")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/handler/... -v"`
Expected: FAIL — pacote/funções não existem.

- [ ] **Step 3: Implementar o handler**

Create `pkg/chatwoot/handler/webhook_handler.go`:

```go
package chatwoot_handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	send_service "github.com/evolution-foundation/evolution-go/pkg/sendMessage/service"
	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	instanceRepo  instance_repository.InstanceRepository
	sendService   send_service.SendService
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewWebhookHandler(
	instanceRepo instance_repository.InstanceRepository,
	sendService send_service.SendService,
	loggerWrapper *logger_wrapper.LoggerManager,
) *WebhookHandler {
	return &WebhookHandler{instanceRepo: instanceRepo, sendService: sendService, loggerWrapper: loggerWrapper}
}

func computeSig(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	return hex.EncodeToString(mac.Sum(nil))
}

func validSignature(secret, timestamp string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	expected := "sha256=" + computeSig(secret, timestamp, body)
	return hmac.Equal([]byte(expected), []byte(header))
}

// shouldForward decide se o evento deve virar uma mensagem no WhatsApp.
// Só encaminha mensagens outgoing, não-privadas, de message_created.
func shouldForward(body []byte) (jid, text string, ok bool) {
	var p struct {
		Event       string `json:"event"`
		MessageType string `json:"message_type"`
		Private     bool   `json:"private"`
		Content     string `json:"content"`
		Conversation struct {
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", false
	}
	if p.Content == "" || p.Conversation.ContactInbox.SourceID == "" {
		return "", "", false
	}
	return p.Conversation.ContactInbox.SourceID, p.Content, true
}

func (h *WebhookHandler) Handle(ctx *gin.Context) {
	instanceName := ctx.Param("instance")
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad body"})
		return
	}

	instance, err := h.instanceRepo.GetInstanceByName(instanceName)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	ts := ctx.GetHeader("X-Chatwoot-Timestamp")
	sig := ctx.GetHeader("X-Chatwoot-Signature")
	if !validSignature(instance.ChatwootWebhookSecret, ts, body, sig) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	jid, text, ok := shouldForward(body)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	number := jid
	if i := strings.IndexByte(number, '@'); i >= 0 {
		number = number[:i]
	}

	_, err = h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance)
	if err != nil {
		h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
}
```

- [ ] **Step 4: Rodar os testes e ver passar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/handler/... -v"`
Expected: PASS (4 testes).

- [ ] **Step 5: Commit**

```bash
git add pkg/chatwoot/handler/webhook_handler.go pkg/chatwoot/handler/webhook_handler_test.go
git commit -m "feat(chatwoot): add webhook receiver forwarding agent replies to whatsapp"
```

---

### Task 7: Service de gestão + endpoints REST (config, links, auto-provisionamento)

**Files:**
- Create: `pkg/chatwoot/service/chatwoot_service.go`
- Create: `pkg/chatwoot/service/chatwoot_service_test.go`
- Create: `pkg/chatwoot/handler/admin_handler.go`

**Interfaces:**
- Consumes: `chatwoot_repository.ChatwootConfigRepository`; `chatwoot_client.Client`; `instance_repository.InstanceRepository`; `instance_service.InstanceService` (para criar a instância — `Create`); `config.Config` (para `GlobalApiKey` / base do webhook).
- Produces:
  - `chatwoot_service.NewChatwootService(configRepo, instanceRepo, instanceService, selfBaseURL, loggerWrapper) *ChatwootService`
  - `(*ChatwootService) SaveConfig(baseURL, apiToken, accountID string) error`
  - `(*ChatwootService) TestConfig() error`
  - `(*ChatwootService) ListLinks() ([]LinkView, error)` — `LinkView{ InstanceName, Number, InboxID, InboxName, Connected, Enabled }`
  - `(*ChatwootService) CreateLink(name string) (*CreateLinkResult, error)` — auto-provisiona inbox + cria instância; `CreateLinkResult{ InstanceID, InstanceToken, InboxID string }`
  - `chatwoot_handler.NewAdminHandler(service) *AdminHandler` com métodos `GetConfig`, `PutConfig`, `TestConfig`, `GetLinks`, `PostLink`, `ServeUI`.

- [ ] **Step 1: Escrever o teste do CreateLink (falhando), com Chatwoot mockado**

Create `pkg/chatwoot/service/chatwoot_service_test.go`:

```go
package chatwoot_service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
)

// fakeConfigRepo e fakeInstanceCreator implementam as dependências mínimas.
type fakeConfigRepo struct{ cfg *chatwoot_model.ChatwootConfig }

func (f *fakeConfigRepo) Get() (*chatwoot_model.ChatwootConfig, error) { return f.cfg, nil }
func (f *fakeConfigRepo) Save(c *chatwoot_model.ChatwootConfig) error  { f.cfg = c; return nil }

func TestCreateLink_ProvisionsInboxAndPersistsFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "inbox_identifier": "abc", "channel": map[string]any{"secret": "sek"},
		})
	}))
	defer srv.Close()

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instSvc := newFakeInstanceService()

	svc := NewChatwootService(cfgRepo, instRepo, instSvc, "http://evolution-go:8080", newTestLogger())
	res, err := svc.CreateLink("vendas")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if res.InboxID != "42" {
		t.Fatalf("expected inbox 42, got %s", res.InboxID)
	}
	saved := instRepo.updated
	if saved == nil || !saved.ChatwootEnabled || saved.ChatwootInboxID != "42" || saved.ChatwootWebhookSecret != "sek" {
		t.Fatalf("instance fields not persisted: %+v", saved)
	}
}
```

> Nota de implementação: os fakes `newFakeInstanceRepo`, `newFakeInstanceService`, `newTestLogger` devem ser escritos no mesmo arquivo de teste, implementando apenas os métodos usados por `CreateLink` (`Create`, `Update`, `GetInstanceByName`) e retornando structs previsíveis. O `loggerWrapper` pode ser um `logger_wrapper.NewLoggerManager(...)` real com saída de teste, ou um wrapper mínimo — replicar o mesmo construtor usado em `main.go`.

- [ ] **Step 2: Rodar e ver falhar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/service/... -v"`
Expected: FAIL — pacote não existe.

- [ ] **Step 3: Implementar o service**

Create `pkg/chatwoot/service/chatwoot_service.go`:

```go
package chatwoot_service

import (
	"fmt"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
)

type ChatwootService struct {
	configRepo    chatwoot_repository.ChatwootConfigRepository
	instanceRepo  instance_repository.InstanceRepository
	instanceSvc   instance_service.InstanceService
	selfBaseURL   string // ex http://evolution-go:8080
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewChatwootService(
	configRepo chatwoot_repository.ChatwootConfigRepository,
	instanceRepo instance_repository.InstanceRepository,
	instanceSvc instance_service.InstanceService,
	selfBaseURL string,
	loggerWrapper *logger_wrapper.LoggerManager,
) *ChatwootService {
	return &ChatwootService{configRepo, instanceRepo, instanceSvc, selfBaseURL, loggerWrapper}
}

func (s *ChatwootService) SaveConfig(baseURL, apiToken, accountID string) error {
	return s.configRepo.Save(&chatwoot_model.ChatwootConfig{BaseURL: baseURL, APIToken: apiToken, AccountID: accountID})
}

func (s *ChatwootService) TestConfig() error {
	cfg, err := s.configRepo.Get()
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("config não definida")
	}
	return chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID).Ping()
}

type LinkView struct {
	InstanceName string `json:"instanceName"`
	Number       string `json:"number"`
	InboxID      string `json:"inboxId"`
	Connected    bool   `json:"connected"`
	Enabled      bool   `json:"enabled"`
}

func (s *ChatwootService) ListLinks() ([]LinkView, error) {
	instances, err := s.instanceRepo.GetAll("")
	if err != nil {
		return nil, err
	}
	views := []LinkView{}
	for _, inst := range instances {
		if !inst.ChatwootEnabled {
			continue
		}
		views = append(views, LinkView{
			InstanceName: inst.Name,
			Number:       inst.Jid,
			InboxID:      inst.ChatwootInboxID,
			Connected:    inst.Connected,
			Enabled:      inst.ChatwootEnabled,
		})
	}
	return views, nil
}

type CreateLinkResult struct {
	InstanceID    string `json:"instanceId"`
	InstanceToken string `json:"instanceToken"`
	InboxID       string `json:"inboxId"`
}

func (s *ChatwootService) CreateLink(name string) (*CreateLinkResult, error) {
	cfg, err := s.configRepo.Get()
	if err != nil || cfg == nil {
		return nil, fmt.Errorf("config do chatwoot ausente")
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)

	webhookURL := fmt.Sprintf("%s/chatwoot/webhook/%s", s.selfBaseURL, name)
	inbox, err := client.CreateInbox(name, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("criar inbox: %w", err)
	}

	// Cria a instância reusando o service existente.
	token := name + "-" + randToken()
	created, err := s.instanceSvc.Create(&instance_service.CreateStruct{Name: name, Token: token})
	if err != nil {
		return nil, fmt.Errorf("criar instância: %w", err)
	}

	created.ChatwootEnabled = true
	created.ChatwootInboxID = fmt.Sprintf("%d", inbox.ID)
	created.ChatwootInboxIdentifier = inbox.Identifier
	created.ChatwootWebhookSecret = inbox.Secret
	if err := s.instanceRepo.Update(created); err != nil {
		return nil, fmt.Errorf("persistir vínculo: %w", err)
	}

	return &CreateLinkResult{InstanceID: created.Id, InstanceToken: token, InboxID: created.ChatwootInboxID}, nil
}
```

> Nota: `randToken()` deve gerar um sufixo curto (ex: 8 hex chars via `crypto/rand`) — implementar no mesmo pacote. Confirmar a assinatura exata de `instanceSvc.Create` e `CreateStruct` em `pkg/instance/service/instance_service.go:165-207` (campos `Name`, `Token`); ajustar o retorno (o `Create` pode devolver `(*instance_model.Instance, error)` ou um DTO — usar o tipo real retornado; se for DTO, buscar a instância com `GetInstanceByName` para ter o `*instance_model.Instance` a atualizar). O import `instance_model` cobre esse caso.

- [ ] **Step 4: Implementar o admin handler**

Create `pkg/chatwoot/handler/admin_handler.go`:

```go
package chatwoot_handler

import (
	"net/http"

	chatwoot_service "github.com/evolution-foundation/evolution-go/pkg/chatwoot/service"
	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	service *chatwoot_service.ChatwootService
}

func NewAdminHandler(service *chatwoot_service.ChatwootService) *AdminHandler {
	return &AdminHandler{service: service}
}

func (h *AdminHandler) PutConfig(ctx *gin.Context) {
	var body struct {
		BaseURL   string `json:"baseUrl"`
		APIToken  string `json:"apiToken"`
		AccountID string `json:"accountId"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.SaveConfig(body.BaseURL, body.APIToken, body.AccountID); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "saved"})
}

func (h *AdminHandler) TestConfig(ctx *gin.Context) {
	if err := h.service.TestConfig(); err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) GetLinks(ctx *gin.Context) {
	links, err := h.service.ListLinks()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": links})
}

func (h *AdminHandler) PostLink(ctx *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || body.Name == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	res, err := h.service.CreateLink(body.Name)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": res})
}
```

- [ ] **Step 5: Rodar os testes e ver passar**

Run: `docker run --rm -v "$(pwd)":/app -w /app golang:1.25.0-alpine sh -c "go test ./pkg/chatwoot/... -v"`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/chatwoot/service pkg/chatwoot/handler/admin_handler.go pkg/chatwoot/service/chatwoot_service_test.go
git commit -m "feat(chatwoot): add management service and REST admin handlers"
```

---

### Task 8: UI de gestão (/chatwoot-admin) servida pelo Go

**Files:**
- Create: `pkg/chatwoot/ui/chatwoot_admin.html`
- Create: `pkg/chatwoot/ui/embed.go`
- Modify: `pkg/chatwoot/handler/admin_handler.go` (adicionar `ServeUI`)

**Interfaces:**
- Produces: `(*AdminHandler) ServeUI(ctx *gin.Context)` que devolve o HTML embutido; `chatwoot_ui.IndexHTML []byte`.

- [ ] **Step 1: Criar o HTML da UI (layout de cards aprovado)**

Create `pkg/chatwoot/ui/chatwoot_admin.html` — página autocontida (HTML+CSS+JS inline, sem dependências externas) com: botão "Config Chatwoot" (abre form baseURL/token/accountId → PUT /chatwoot/config, botão Testar → POST /chatwoot/config/test), botão "Nova conexão" (input nome → POST /chatwoot/links → exibe token e instruções de QR), e a lista de cards populada por GET /chatwoot/links (nome, número, `#inbox`, status 🟢/🟡). Todas as chamadas enviam o header `apikey` (campo preenchido no topo da página, guardado em `localStorage`). Usar `fetch`. Manter simples e legível.

- [ ] **Step 2: Criar o embed**

Create `pkg/chatwoot/ui/embed.go`:

```go
package chatwoot_ui

import _ "embed"

//go:embed chatwoot_admin.html
var IndexHTML []byte
```

- [ ] **Step 3: Adicionar `ServeUI` ao admin handler**

Em `pkg/chatwoot/handler/admin_handler.go`, adicionar o import `chatwoot_ui "github.com/evolution-foundation/evolution-go/pkg/chatwoot/ui"` e o método:

```go
func (h *AdminHandler) ServeUI(ctx *gin.Context) {
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", chatwoot_ui.IndexHTML)
}
```

- [ ] **Step 4: Build para garantir que o embed compila**

Run: `docker build -t evolution-go:local .`
Expected: build OK.

- [ ] **Step 5: Commit**

```bash
git add pkg/chatwoot/ui pkg/chatwoot/handler/admin_handler.go
git commit -m "feat(chatwoot): add admin UI page served by evolution-go"
```

---

### Task 9: Registrar rotas + rede Docker compartilhada (integração)

**Files:**
- Create: `pkg/chatwoot/routes.go`
- Modify: `cmd/evolution-go/main.go` (chamar o registrador de rotas + montar as dependências do service)
- Modify: `docker-compose.local.yml` (rede externa)
- Modify: `/home/silas/projeto/chatwoot/docker-compose.override.yaml` (rede externa + alias)

**Interfaces:**
- Consumes: `AdminHandler` (Task 7/8), `WebhookHandler` (Task 6), `ChatwootService` (Task 7).
- Produces: `chatwoot_routes.Register(eng *gin.Engine, admin *chatwoot_handler.AdminHandler, webhook *chatwoot_handler.WebhookHandler, adminAuth gin.HandlerFunc)`.

- [ ] **Step 1: Criar o registrador de rotas (padrão passkey — fora do struct Routes)**

Create `pkg/chatwoot/routes.go`:

```go
package chatwoot_routes

import (
	chatwoot_handler "github.com/evolution-foundation/evolution-go/pkg/chatwoot/handler"
	"github.com/gin-gonic/gin"
)

// Register monta as rotas da integração Chatwoot.
// adminAuth é o middleware AuthAdmin (GlobalApiKey); o webhook receiver NÃO usa auth (valida HMAC).
func Register(eng *gin.Engine, admin *chatwoot_handler.AdminHandler, webhook *chatwoot_handler.WebhookHandler, adminAuth gin.HandlerFunc) {
	// UI (pública — a página pede o apikey e o guarda no browser)
	eng.GET("/chatwoot-admin", admin.ServeUI)

	// Webhook do Chatwoot -> evolution (sem apikey; autenticado por HMAC)
	eng.POST("/chatwoot/webhook/:instance", webhook.Handle)

	// API de gestão (protegida por AuthAdmin)
	api := eng.Group("/chatwoot")
	api.Use(adminAuth)
	{
		api.PUT("/config", admin.PutConfig)
		api.POST("/config/test", admin.TestConfig)
		api.GET("/links", admin.GetLinks)
		api.POST("/links", admin.PostLink)
	}
}
```

- [ ] **Step 2: Montar dependências e registrar em main.go**

Em `cmd/evolution-go/main.go`, após `sendMessageService := send_service.NewSendService(...)` (linha ~190) e antes de `routes.NewRouter(...).AssignRoutes(r)` (linha ~228), adicionar:

```go
	chatwootSvc := chatwoot_service.NewChatwootService(
		chatwootConfigRepo,
		instanceRepository,
		instanceService,
		"http://evolution-go:8080",
		loggerWrapper,
	)
	chatwootAdmin := chatwoot_handler.NewAdminHandler(chatwootSvc)
	chatwootWebhook := chatwoot_handler.NewWebhookHandler(instanceRepository, sendMessageService, loggerWrapper)
	chatwoot_routes.Register(r, chatwootAdmin, chatwootWebhook, auth_middleware.NewMiddleware(config, instanceService).AuthAdmin)
```

Adicionar imports:

```go
	chatwoot_handler "github.com/evolution-foundation/evolution-go/pkg/chatwoot/handler"
	chatwoot_routes "github.com/evolution-foundation/evolution-go/pkg/chatwoot"
	chatwoot_service "github.com/evolution-foundation/evolution-go/pkg/chatwoot/service"
```

> Nota: `chatwootConfigRepo` já foi criado na Task 5. `selfBaseURL` fixo em `http://evolution-go:8080` (nome de serviço na rede compartilhada); se quiser tornar configurável depois, virar env.

- [ ] **Step 3: Build**

Run: `docker build -t evolution-go:local .`
Expected: build OK.

- [ ] **Step 4: Adicionar a rede compartilhada aos dois composes**

Criar a rede: `docker network create chatwoot-evo` (idempotente; ignorar erro se já existe).

Em `/home/silas/projeto/evolution-go/docker-compose.local.yml`, no serviço `evolution-go`, adicionar:

```yaml
    networks:
      - default
      - chatwoot-evo
```

E no fim do arquivo:

```yaml
networks:
  chatwoot-evo:
    external: true
```

Em `/home/silas/projeto/chatwoot/docker-compose.override.yaml`, no serviço `rails`, adicionar (mantendo o `!override` de ports já existente):

```yaml
    networks:
      default:
        aliases:
          - chatwoot-rails
      chatwoot-evo: {}
```

E no fim:

```yaml
networks:
  chatwoot-evo:
    external: true
```

> Nota: o serviço `rails` do Chatwoot escuta na porta **interna 3000**; na rede compartilhada o evolution o alcança em `http://chatwoot-rails:3000`.

- [ ] **Step 5: Subir tudo e testar o fluxo ponta a ponta**

```bash
docker network create chatwoot-evo 2>/dev/null || true
cd /home/silas/projeto/chatwoot && docker compose up -d
cd /home/silas/projeto/evolution-go && docker compose -f docker-compose.local.yml up -d
```

Verificações:
- `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/chatwoot-admin` → 200.
- Do container evolution, alcançar o Chatwoot: `docker compose -f docker-compose.local.yml exec evolution-go wget -qO- http://chatwoot-rails:3000/api >/dev/null && echo OK`.
- Salvar config via `PUT /chatwoot/config` (com token de admin do Chatwoot) e `POST /chatwoot/config/test` → `{"ok":true}`.
- `POST /chatwoot/links {"name":"vendas"}` → cria a inbox no Chatwoot (conferir no dashboard) e retorna o token da instância.
- Parear o QR (`GET /instance/qr` com o token) e enviar uma mensagem de um WhatsApp de teste → conversa aparece na inbox do Chatwoot.
- Responder no Chatwoot → mensagem chega no WhatsApp.

- [ ] **Step 6: Commit**

```bash
cd /home/silas/projeto/evolution-go
git add pkg/chatwoot/routes.go cmd/evolution-go/main.go docker-compose.local.yml
git commit -m "feat(chatwoot): register routes and shared docker network"
```

E no repo do Chatwoot:

```bash
cd /home/silas/projeto/chatwoot
git add docker-compose.override.yaml
git commit -m "chore: join shared chatwoot-evo network for evolution-go integration"
```

---

## Notas de verificação final (após todas as tasks)

- O gate de licença do evolution-go já está aberto (patch offline). A rota `/chatwoot/webhook/:instance` é registrada sem `authMiddleware.Auth` e se protege por HMAC — confirmar que responde (não 401 por falta de apikey) a um POST assinado.
- Filtro anti-eco é a proteção crítica: sem ele, cada mensagem injetada como `incoming` voltaria como webhook e seria reenviada. Coberto por `shouldForward` (Task 6) e testado.
- Dedupe por `wamid`: o `source_id` da mensagem no Chatwoot é o id do WhatsApp; reentregas do whatsmeow não duplicam porque o cache de conversa + o `source_id` permitem identificar. (Se necessário reforçar, adicionar checagem explícita de `source_id` existente antes do POST — fora do MVP.)
