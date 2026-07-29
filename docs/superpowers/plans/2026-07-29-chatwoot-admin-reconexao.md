# Reconexão Chatwoot sem duplicar inbox — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir reconectar uma instância do WhatsApp sem criar uma inbox nova no Chatwoot, e elevar a tela `/chatwoot-admin` ao padrão visual do Manager.

**Architecture:** Três correções no pacote `pkg/chatwoot` (client HTTP, service, handlers/rotas) mais a reescrita da página embarcada por `go:embed`. O `CreateLink` passa a validar antes de criar e a fazer rollback; um caminho novo de reconexão reusa instância e inbox existentes; a busca de conversa aberta passa a filtrar por inbox. Nenhuma alteração de modelo de dados e nenhuma alteração no repositório do Chatwoot.

**Tech Stack:** Go 1.25, Gin, GORM, testes com `net/http/httptest` e fakes de repositório. Front-end em HTML/CSS/JS vanilla, arquivo único, sem build step.

**Spec:** [`docs/superpowers/specs/2026-07-29-chatwoot-admin-reconexao-design.md`](../specs/2026-07-29-chatwoot-admin-reconexao-design.md)

## Global Constraints

- **Sem migração de banco.** Os campos `ChatwootEnabled`, `ChatwootInboxID`, `ChatwootInboxIdentifier` e `ChatwootWebhookSecret` já existem em `pkg/instance/model/instance_model.go:30-33`.
- **Sem alteração no Dockerfile nem em `manager/dist`.** A UI continua embarcada por `go:embed` em `pkg/chatwoot/ui/embed.go`.
- **Sem alteração de código no repositório do Chatwoot.**
- **Comentários e mensagens de erro em português**, seguindo o padrão do pacote `pkg/chatwoot`.
- **`Connect` sempre recebe `Subscribe` explícito** com `event_types.MESSAGE` e `event_types.READ_RECEIPT`. Chamá-lo com `Subscribe` vazio faz `instance_service.go:214-215` reduzir a assinatura a só `MESSAGE`, quebrando o sync de status de mensagem.
- **`Connect` sempre recebe `WebhookUrl: instance.Webhook`.** Ele sobrescreve o campo a partir do corpo da requisição (`instance_service.go:233`); passar vazio apagaria o webhook do usuário.
- **Nunca usar `POST /instance/disconnect` nos fluxos do Chatwoot.** Ele zera `instance.Events` (`instance_service.go:322`), deixando a instância viva com a ponte muda.
- Rodar `gofmt -w` nos arquivos Go tocados antes de cada commit.

## Contratos da API do Chatwoot (verificados no fonte)

| Chamada | Formato | Referência |
|---|---|---|
| `GET /api/v1/accounts/{id}/inboxes` | `{"payload": [inbox, ...]}` | `app/views/api/v1/accounts/inboxes/index.json.jbuilder` |
| `PATCH /api/v1/accounts/{id}/inboxes/{inbox_id}` | corpo `{"channel": {...}}`, resposta = inbox | `config/routes.rb:259` |
| `DELETE /api/v1/accounts/{id}/inboxes/{inbox_id}` | — | `config/routes.rb:259` |
| `GET /api/v1/accounts/{id}/contacts/{id}/conversations` | `{"payload": [conversation, ...]}` com `id`, `inbox_id`, `status` | `_conversation.json.jbuilder:29,47,51` |

Campos de uma inbox no payload: `id`, `name`, `channel_type`. Para `channel_type == "Channel::Api"` e token de administrador, também `secret`, `inbox_identifier` e `webhook_url` (`app/views/api/v1/models/_inbox.json.jbuilder:116-121`).

## Estrutura de arquivos

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `pkg/chatwoot/client/chatwoot_client.go` | fala HTTP com a API do Chatwoot | modificar (3 métodos novos, 1 assinatura alterada) |
| `pkg/chatwoot/client/chatwoot_client_test.go` | testes do client | modificar |
| `pkg/chatwoot/service/chatwoot_service.go` | regras de vínculo instância↔inbox | modificar (2 métodos novos, 3 alterados) |
| `pkg/chatwoot/service/chatwoot_service_test.go` | testes do service | modificar |
| `pkg/chatwoot/handler/admin_handler.go` | HTTP da API de gestão | modificar (3 handlers novos) |
| `pkg/chatwoot/routes.go` | registro de rotas | modificar (3 rotas novas) |
| `pkg/events/chatwoot/chatwoot_producer.go` | ponte WhatsApp→Chatwoot | modificar (1 chamada) |
| `pkg/chatwoot/ui/chatwoot_admin.html` | tela de gestão | reescrever |

---

### Task 1: Client — `FindInboxByName`

**Files:**
- Modify: `pkg/chatwoot/client/chatwoot_client.go:31-37` (struct `Inbox`), append novo método
- Test: `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Consumes: `Client.do`, struct `Inbox` (existentes)
- Produces: `func (c *Client) FindInboxByName(name string) (*Inbox, error)` — devolve `(nil, nil)` quando não encontra. Struct `Inbox` ganha os campos `Name string` e `WebhookURL string`.

- [ ] **Step 1: Escrever o teste que falha**

Adicionar ao final de `pkg/chatwoot/client/chatwoot_client_test.go`:

```go
func TestFindInboxByNameReturnsApiInbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/1/inboxes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 7, "name": "vendas", "channel_type": "Channel::WebWidget"},
				{
					"id": 42, "name": "vendas", "channel_type": "Channel::Api",
					"inbox_identifier": "abc123", "secret": "s3cr3t",
					"webhook_url": "http://evolution-go:8080/chatwoot/webhook/vendas",
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	inbox, err := c.FindInboxByName("vendas")
	if err != nil {
		t.Fatalf("FindInboxByName: %v", err)
	}
	if inbox == nil {
		t.Fatal("expected to find the Channel::Api inbox, got nil")
	}
	if inbox.ID != 42 || inbox.Secret != "s3cr3t" || inbox.Identifier != "abc123" {
		t.Fatalf("bad inbox: %+v", inbox)
	}
	if inbox.WebhookURL != "http://evolution-go:8080/chatwoot/webhook/vendas" {
		t.Fatalf("bad webhook url: %q", inbox.WebhookURL)
	}
}

func TestFindInboxByNameReturnsNilWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 7, "name": "suporte", "channel_type": "Channel::Api"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	inbox, err := c.FindInboxByName("vendas")
	if err != nil {
		t.Fatalf("FindInboxByName: %v", err)
	}
	if inbox != nil {
		t.Fatalf("expected nil for missing inbox, got %+v", inbox)
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha**

Run: `go test ./pkg/chatwoot/client/ -run TestFindInboxByName -v`
Expected: FAIL — `inbox.FindInboxByName undefined` (erro de compilação).

- [ ] **Step 3: Estender o struct `Inbox`**

Substituir o struct em `chatwoot_client.go:31-35` por:

```go
type Inbox struct {
	ID         int
	Identifier string
	Secret     string
	Name       string
	WebhookURL string
}
```

- [ ] **Step 4: Implementar `FindInboxByName`**

Adicionar logo após `CreateInbox`:

```go
// FindInboxByName procura uma inbox do tipo Channel::Api pelo nome exato.
// Retorna (nil, nil) quando não existe — ausência não é erro.
//
// O secret e o inbox_identifier só aparecem no payload quando o api_access_token
// pertence a um administrador da conta (ver _inbox.json.jbuilder no Chatwoot).
// É por isso que reusar uma inbox existente consegue recuperar o segredo do HMAC.
func (c *Client) FindInboxByName(name string) (*Inbox, error) {
	var raw struct {
		Payload []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			ChannelType string `json:"channel_type"`
			Identifier  string `json:"inbox_identifier"`
			Secret      string `json:"secret"`
			WebhookURL  string `json:"webhook_url"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodGet, "/inboxes", nil, &raw); err != nil {
		return nil, err
	}
	for _, in := range raw.Payload {
		if in.ChannelType != "Channel::Api" || in.Name != name {
			continue
		}
		return &Inbox{
			ID:         in.ID,
			Identifier: in.Identifier,
			Secret:     in.Secret,
			Name:       in.Name,
			WebhookURL: in.WebhookURL,
		}, nil
	}
	return nil, nil
}
```

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `go test ./pkg/chatwoot/... -v`
Expected: PASS em tudo, inclusive os testes já existentes.

- [ ] **Step 6: Commit**

```bash
gofmt -w pkg/chatwoot/client/chatwoot_client.go pkg/chatwoot/client/chatwoot_client_test.go
git add pkg/chatwoot/client/
git commit -m "feat(chatwoot): add FindInboxByName to look up existing api inboxes"
```

---

### Task 2: Client — `UpdateInboxWebhook` e `DeleteInbox`

**Files:**
- Modify: `pkg/chatwoot/client/chatwoot_client.go` (append)
- Test: `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Consumes: `Client.do`
- Produces: `func (c *Client) UpdateInboxWebhook(inboxID int, webhookURL string) error`, `func (c *Client) DeleteInbox(inboxID int) error`

- [ ] **Step 1: Escrever os testes que falham**

Adicionar a `chatwoot_client_test.go`:

```go
func TestUpdateInboxWebhookSendsChannelPayload(t *testing.T) {
	var got map[string]any
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	if err := c.UpdateInboxWebhook(42, "http://evolution-go:8080/chatwoot/webhook/vendas"); err != nil {
		t.Fatalf("UpdateInboxWebhook: %v", err)
	}
	if method != http.MethodPatch || path != "/api/v1/accounts/1/inboxes/42" {
		t.Fatalf("unexpected request: %s %s", method, path)
	}
	channel, ok := got["channel"].(map[string]any)
	if !ok || channel["webhook_url"] != "http://evolution-go:8080/chatwoot/webhook/vendas" {
		t.Fatalf("bad body: %+v", got)
	}
}

func TestDeleteInboxIssuesDelete(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	if err := c.DeleteInbox(42); err != nil {
		t.Fatalf("DeleteInbox: %v", err)
	}
	if method != http.MethodDelete || path != "/api/v1/accounts/1/inboxes/42" {
		t.Fatalf("unexpected request: %s %s", method, path)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test ./pkg/chatwoot/client/ -run "TestUpdateInboxWebhook|TestDeleteInbox" -v`
Expected: FAIL — métodos indefinidos.

- [ ] **Step 3: Implementar os dois métodos**

Adicionar após `FindInboxByName`:

```go
// UpdateInboxWebhook corrige o webhook_url de uma inbox Channel::Api existente.
// Usado ao reusar uma inbox cujo webhook aponta para uma URL antiga do evolution-go.
func (c *Client) UpdateInboxWebhook(inboxID int, webhookURL string) error {
	body := map[string]any{
		"channel": map[string]any{"webhook_url": webhookURL},
	}
	path := fmt.Sprintf("/inboxes/%d", inboxID)
	return c.do(http.MethodPatch, path, body, nil)
}

// DeleteInbox remove uma inbox. Usado no rollback do CreateLink e na remoção
// explícita de um vínculo. Apagar uma inbox destrói o histórico de conversas
// dela no Chatwoot — só chame com intenção explícita do operador.
func (c *Client) DeleteInbox(inboxID int) error {
	path := fmt.Sprintf("/inboxes/%d", inboxID)
	return c.do(http.MethodDelete, path, nil, nil)
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go test ./pkg/chatwoot/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w pkg/chatwoot/client/chatwoot_client.go pkg/chatwoot/client/chatwoot_client_test.go
git add pkg/chatwoot/client/
git commit -m "feat(chatwoot): add inbox webhook update and delete to the client"
```

---

### Task 3: Client — conversa aberta escopada por inbox

Corrige o defeito D3 da spec: hoje a resposta do agente pode sair pelo número errado porque `FindOpenConversation` aceita conversa de qualquer inbox da conta.

**Files:**
- Modify: `pkg/chatwoot/client/chatwoot_client.go:179-196`
- Modify: `pkg/events/chatwoot/chatwoot_producer.go:330` (único chamador)
- Test: `pkg/chatwoot/client/chatwoot_client_test.go`

**Interfaces:**
- Produces: assinatura alterada — `func (c *Client) FindOpenConversation(contactID, inboxID int) (int, bool, error)`

- [ ] **Step 1: Escrever o teste que falha**

Adicionar a `chatwoot_client_test.go`:

```go
func TestFindOpenConversationIgnoresOtherInboxes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/1/contacts/123/conversations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 900, "inbox_id": 99, "status": "open"},
				{"id": 901, "inbox_id": 42, "status": "resolved"},
				{"id": 902, "inbox_id": 42, "status": "open"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	id, ok, err := c.FindOpenConversation(123, 42)
	if err != nil {
		t.Fatalf("FindOpenConversation: %v", err)
	}
	if !ok {
		t.Fatal("expected to find an open conversation in inbox 42")
	}
	if id != 902 {
		t.Fatalf("expected conversation 902 (inbox 42, open), got %d", id)
	}
}

func TestFindOpenConversationReturnsFalseWhenOnlyOtherInboxHasOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"payload": []map[string]any{
				{"id": 900, "inbox_id": 99, "status": "open"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "1")
	_, ok, err := c.FindOpenConversation(123, 42)
	if err != nil {
		t.Fatalf("FindOpenConversation: %v", err)
	}
	if ok {
		t.Fatal("expected no match: the only open conversation belongs to another inbox")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./pkg/chatwoot/client/ -run TestFindOpenConversation -v`
Expected: FAIL — número errado de argumentos na chamada.

- [ ] **Step 3: Alterar `FindOpenConversation`**

Substituir o método em `chatwoot_client.go:176-196` por:

```go
// FindOpenConversation retorna o display_id da primeira conversa com status
// "open" do contato **naquela inbox**. O display_id é o mesmo valor usado em
// /conversations/{id}/messages, retornado por CreateConversation.
//
// O filtro por inbox é obrigatório: GET /contacts/{id}/conversations devolve
// conversas de TODAS as inboxes da conta. Sem ele, uma mensagem que chega por
// uma inbox seria injetada numa conversa de outra, e a resposta do agente
// sairia pelo número de WhatsApp errado.
func (c *Client) FindOpenConversation(contactID, inboxID int) (int, bool, error) {
	path := fmt.Sprintf("/contacts/%d/conversations", contactID)
	var raw struct {
		Payload []struct {
			ID      int    `json:"id"`
			InboxID int    `json:"inbox_id"`
			Status  string `json:"status"`
		} `json:"payload"`
	}
	if err := c.do(http.MethodGet, path, nil, &raw); err != nil {
		return 0, false, err
	}
	for _, conv := range raw.Payload {
		if conv.Status == "open" && conv.InboxID == inboxID {
			return conv.ID, true, nil
		}
	}
	return 0, false, nil
}
```

- [ ] **Step 4: Atualizar o chamador no producer**

Em `pkg/events/chatwoot/chatwoot_producer.go:330`, trocar:

```go
	convID, ok, err := client.FindOpenConversation(contact.ID)
```

por:

```go
	convID, ok, err := client.FindOpenConversation(contact.ID, inboxID)
```

A variável `inboxID` já existe no escopo, definida em `chatwoot_producer.go:303`.

- [ ] **Step 5: Rodar a suíte inteira e confirmar que passa**

Run: `go build ./... && go test ./pkg/chatwoot/... ./pkg/events/... -v`
Expected: PASS, sem erro de compilação.

- [ ] **Step 6: Commit**

```bash
gofmt -w pkg/chatwoot/client/chatwoot_client.go pkg/chatwoot/client/chatwoot_client_test.go pkg/events/chatwoot/chatwoot_producer.go
git add pkg/chatwoot/client/ pkg/events/chatwoot/
git commit -m "fix(chatwoot): scope open-conversation lookup to the instance inbox

GET /contacts/{id}/conversations returns conversations from every inbox in
the account. Picking the first open one meant a message arriving on one
inbox could be injected into another inbox's conversation, and the agent
reply would then go out through the wrong WhatsApp number."
```

---

### Task 4: Service — `CreateLink` idempotente e transacional

Corrige o defeito D1 da spec: hoje cada tentativa de reconectar com o mesmo nome abandona uma inbox no Chatwoot.

**Files:**
- Modify: `pkg/chatwoot/service/chatwoot_service.go:20-29` (interfaces locais), `:103-140` (`CreateLink`)
- Test: `pkg/chatwoot/service/chatwoot_service_test.go`

**Interfaces:**
- Consumes: `Client.FindInboxByName`, `Client.UpdateInboxWebhook`, `Client.DeleteInbox` (Tasks 1-2)
- Produces: `linkInstanceRepo` ganha `GetInstanceByName(name string) (*instance_model.Instance, error)`. `CreateLink` mantém a assinatura `func (s *ChatwootService) CreateLink(name string) (*CreateLinkResult, error)`.

- [ ] **Step 1: Escrever os testes que falham**

Primeiro, estender o fake do repositório em `chatwoot_service_test.go`. Substituir o struct `fakeInstanceRepo` e seu construtor (linhas 27-43) por:

```go
// fakeInstanceRepo implementa apenas o subconjunto usado pelo service (linkInstanceRepo).
type fakeInstanceRepo struct {
	byClient map[string][]*instance_model.Instance
	byName   map[string]*instance_model.Instance
	updated  *instance_model.Instance
}

func newFakeInstanceRepo() *fakeInstanceRepo {
	return &fakeInstanceRepo{
		byClient: map[string][]*instance_model.Instance{},
		byName:   map[string]*instance_model.Instance{},
	}
}

func (f *fakeInstanceRepo) GetAll(clientName string) ([]*instance_model.Instance, error) {
	return f.byClient[clientName], nil
}

// GetInstanceByName espelha o repositório real, que devolve erro quando não acha
// (gorm.ErrRecordNotFound).
func (f *fakeInstanceRepo) GetInstanceByName(name string) (*instance_model.Instance, error) {
	if inst, ok := f.byName[name]; ok {
		return inst, nil
	}
	return nil, errors.New("record not found")
}

func (f *fakeInstanceRepo) Update(instance *instance_model.Instance) error {
	f.updated = instance
	return nil
}
```

Adicionar `"errors"` aos imports do arquivo de teste.

Estender o fake do service (linhas 46-63) para poder simular falha:

```go
// fakeInstanceService implementa apenas o subconjunto usado pelo service (instanceManager).
type fakeInstanceService struct {
	created     *instance_model.Instance
	createErr   error
	connectedTo *instance_service.ConnectStruct
}

func newFakeInstanceService() *fakeInstanceService {
	return &fakeInstanceService{}
}

func (f *fakeInstanceService) Create(data *instance_service.CreateStruct) (*instance_model.Instance, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	inst := &instance_model.Instance{
		Id:         "inst-1",
		Name:       data.Name,
		Token:      data.Token,
		ClientName: "evolution",
	}
	f.created = inst
	return inst, nil
}

func (f *fakeInstanceService) Connect(data *instance_service.ConnectStruct, instance *instance_model.Instance) (*instance_model.Instance, string, string, error) {
	f.connectedTo = data
	return instance, instance.Jid, strings.Join(data.Subscribe, ","), nil
}
```

Adicionar `"strings"` aos imports do arquivo de teste.

Agora os testes novos:

```go
// chatwootRecorder é um Chatwoot falso que registra as chamadas recebidas,
// para os testes afirmarem o que foi (e o que NÃO foi) chamado.
type chatwootRecorder struct {
	calls        []string
	existingName string // se != "", GET /inboxes devolve uma inbox Channel::Api com esse nome
}

func (rec *chatwootRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.calls = append(rec.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inboxes"):
			payload := []map[string]any{}
			if rec.existingName != "" {
				payload = append(payload, map[string]any{
					"id": 42, "name": rec.existingName, "channel_type": "Channel::Api",
					"inbox_identifier": "abc123", "secret": "s3cr3t",
					"webhook_url": "http://evolution-go:8080/chatwoot/webhook/" + rec.existingName,
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"payload": payload})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "inbox_identifier": "abc123", "secret": "s3cr3t",
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (rec *chatwootRecorder) called(substr string) bool {
	for _, c := range rec.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// Regressão de D1: com a instância já existente, CreateLink deve falhar sem
// emitir NENHUMA chamada ao Chatwoot. Antes da correção ele criava a inbox
// primeiro e só depois descobria o conflito, abandonando a inbox.
func TestCreateLink_ExistingInstanceDoesNotTouchChatwoot(t *testing.T) {
	rec := &chatwootRecorder{}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{Id: "inst-1", Name: "vendas"}

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	_, err := svc.CreateLink("vendas")
	if err == nil {
		t.Fatal("expected CreateLink to fail when the instance already exists")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected zero Chatwoot calls, got %v", rec.calls)
	}
}

// Reusar a inbox existente em vez de criar outra com o mesmo nome.
func TestCreateLink_ReusesExistingInbox(t *testing.T) {
	rec := &chatwootRecorder{existingName: "vendas"}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	res, err := svc.CreateLink("vendas")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if res.InboxID != "42" {
		t.Fatalf("expected to reuse inbox 42, got %s", res.InboxID)
	}
	if rec.called("POST /api/v1/accounts/1/inboxes") {
		t.Fatalf("expected no inbox creation, calls were %v", rec.calls)
	}
	if instRepo.updated.ChatwootWebhookSecret != "s3cr3t" {
		t.Fatalf("expected the secret recovered from the existing inbox, got %q", instRepo.updated.ChatwootWebhookSecret)
	}
}

// Rollback: se a criação da instância falhar depois de termos criado a inbox,
// a inbox precisa ser apagada para não virar órfã.
func TestCreateLink_RollsBackInboxWhenInstanceCreationFails(t *testing.T) {
	rec := &chatwootRecorder{}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instSvc := newFakeInstanceService()
	instSvc.createErr = errors.New("boom")

	svc := NewChatwootService(cfgRepo, newFakeInstanceRepo(), instSvc, "http://evolution-go:8080", "evolution", newTestLogger(t))
	_, err := svc.CreateLink("vendas")
	if err == nil {
		t.Fatal("expected CreateLink to fail")
	}
	if !rec.called("DELETE /api/v1/accounts/1/inboxes/42") {
		t.Fatalf("expected the orphan inbox to be deleted, calls were %v", rec.calls)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test ./pkg/chatwoot/service/ -v`
Expected: FAIL — `GetInstanceByName` não faz parte de `linkInstanceRepo`, `Connect` não faz parte de `instanceCreator`.

- [ ] **Step 3: Ampliar as interfaces locais do service**

Substituir `chatwoot_service.go:17-29` por:

```go
// instanceManager é o subconjunto de instance_service.InstanceService usado por
// este service. Definido localmente (idioma Go "accept interfaces, return
// structs") para que os testes não precisem fakear a interface inteira.
type instanceManager interface {
	Create(data *instance_service.CreateStruct) (*instance_model.Instance, error)
	Connect(data *instance_service.ConnectStruct, instance *instance_model.Instance) (*instance_model.Instance, string, string, error)
}

// linkInstanceRepo é o subconjunto de instance_repository.InstanceRepository
// usado por este service.
type linkInstanceRepo interface {
	GetAll(clientName string) ([]*instance_model.Instance, error)
	GetInstanceByName(name string) (*instance_model.Instance, error)
	Update(*instance_model.Instance) error
}
```

E trocar o campo do struct em `chatwoot_service.go:35` de `instanceSvc instanceCreator` para `instanceSvc instanceManager`, e o parâmetro correspondente em `NewChatwootService` (`:43`).

- [ ] **Step 4: Reescrever `CreateLink`**

Substituir `chatwoot_service.go:103-140` por:

```go
func (s *ChatwootService) CreateLink(name string) (*CreateLinkResult, error) {
	// Valida o conflito de nome ANTES de tocar no Chatwoot. A ordem importa:
	// instanceSvc.Create rejeita nome repetido, e criar a inbox primeiro fazia
	// cada tentativa de reconexão abandonar uma inbox órfã no Chatwoot.
	if existing, err := s.instanceRepo.GetInstanceByName(name); err == nil && existing != nil {
		return nil, fmt.Errorf("já existe uma conexão chamada %q; use Reconectar em vez de criar outra", name)
	}

	cfg, err := s.configRepo.Get()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("config do chatwoot ausente")
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)

	webhookURL := fmt.Sprintf("%s/chatwoot/webhook/%s", s.selfBaseURL, name)

	// Find-or-create da inbox. Só marcamos para rollback a inbox que nós mesmos
	// criamos nesta chamada — uma inbox preexistente nunca é apagada aqui.
	inbox, err := client.FindInboxByName(name)
	if err != nil {
		return nil, fmt.Errorf("procurar inbox: %w", err)
	}
	createdInbox := false
	if inbox == nil {
		inbox, err = client.CreateInbox(name, webhookURL)
		if err != nil {
			return nil, fmt.Errorf("criar inbox: %w", err)
		}
		createdInbox = true
	} else if inbox.WebhookURL != webhookURL {
		if err := client.UpdateInboxWebhook(inbox.ID, webhookURL); err != nil {
			return nil, fmt.Errorf("corrigir webhook da inbox: %w", err)
		}
	}

	rollback := func() {
		if !createdInbox {
			return
		}
		if err := client.DeleteInbox(inbox.ID); err != nil {
			s.loggerWrapper.GetLogger("chatwoot").LogError("chatwoot: falha ao remover inbox órfã %d: %v", inbox.ID, err)
		}
	}

	// Cria a instância reusando o service existente.
	token := name + "-" + randToken()
	created, err := s.instanceSvc.Create(&instance_service.CreateStruct{Name: name, Token: token})
	if err != nil {
		rollback()
		return nil, fmt.Errorf("criar instância: %w", err)
	}

	created.ChatwootEnabled = true
	created.ChatwootInboxID = fmt.Sprintf("%d", inbox.ID)
	created.ChatwootInboxIdentifier = inbox.Identifier
	created.ChatwootWebhookSecret = inbox.Secret
	created.Events = event_types.MESSAGE + "," + event_types.READ_RECEIPT
	// Present as available so WhatsApp shows the delivery (second) check to the
	// sender. whatsmeow only sends active delivery receipts while presence is
	// available; without this the bridge sends "inactive" receipts (one check).
	created.AlwaysOnline = true
	if err := s.instanceRepo.Update(created); err != nil {
		rollback()
		return nil, fmt.Errorf("persistir vínculo: %w", err)
	}

	return &CreateLinkResult{InstanceID: created.Id, InstanceToken: token, InboxID: created.ChatwootInboxID}, nil
}
```

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `go build ./... && go test ./pkg/chatwoot/... -v`
Expected: PASS, incluindo `TestCreateLink_ProvisionsInboxAndPersistsFields` que já existia.

- [ ] **Step 6: Commit**

```bash
gofmt -w pkg/chatwoot/service/
git add pkg/chatwoot/service/
git commit -m "fix(chatwoot): make CreateLink idempotent and transactional

Validate the instance name before touching Chatwoot, reuse an existing
api inbox with the same name instead of creating a second one, and delete
the inbox we just created if instance creation fails. Retrying a link with
the same name used to leave one orphan inbox behind per attempt."
```

---

### Task 5: Service — `ReconnectLink`

Corrige o defeito D2 da spec: dar um caminho de reconexão que não passa por criação.

**Files:**
- Modify: `pkg/chatwoot/service/chatwoot_service.go` (append)
- Test: `pkg/chatwoot/service/chatwoot_service_test.go`

**Interfaces:**
- Consumes: `instanceManager.Connect`, `linkInstanceRepo.GetInstanceByName` (Task 4)
- Produces: `type ReconnectResult struct { InstanceID, InstanceToken, InboxID string }` e `func (s *ChatwootService) ReconnectLink(name string) (*ReconnectResult, error)`

- [ ] **Step 1: Escrever o teste que falha**

Adicionar a `chatwoot_service_test.go`:

```go
// Regressão da armadilha do Connect: ele sobrescreve instance.Events a partir
// do corpo da requisição, e com Subscribe vazio reduz a assinatura a só
// MESSAGE — o que mataria o sync de status (READ_RECEIPT). ReconnectLink
// precisa passar os dois eventos explicitamente e preservar o webhook.
func TestReconnectLink_PreservesEventsAndWebhook(t *testing.T) {
	instRepo := newFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{
		Id:              "inst-1",
		Name:            "vendas",
		Token:           "vendas-abc",
		Webhook:         "https://cliente.example/hook",
		ChatwootEnabled: true,
		ChatwootInboxID: "42",
	}
	instSvc := newFakeInstanceService()

	svc := NewChatwootService(&fakeConfigRepo{}, instRepo, instSvc, "http://evolution-go:8080", "evolution", newTestLogger(t))
	res, err := svc.ReconnectLink("vendas")
	if err != nil {
		t.Fatalf("ReconnectLink: %v", err)
	}
	if res.InstanceToken != "vendas-abc" || res.InboxID != "42" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if instSvc.connectedTo == nil {
		t.Fatal("expected Connect to be called")
	}
	subscribed := strings.Join(instSvc.connectedTo.Subscribe, ",")
	want := event_types.MESSAGE + "," + event_types.READ_RECEIPT
	if subscribed != want {
		t.Fatalf("expected Subscribe %q, got %q", want, subscribed)
	}
	if instSvc.connectedTo.WebhookUrl != "https://cliente.example/hook" {
		t.Fatalf("expected the existing webhook to be preserved, got %q", instSvc.connectedTo.WebhookUrl)
	}
}

func TestReconnectLink_RejectsInstanceWithoutChatwootLink(t *testing.T) {
	instRepo := newFakeInstanceRepo()
	instRepo.byName["avulsa"] = &instance_model.Instance{Id: "inst-2", Name: "avulsa", ChatwootEnabled: false}

	svc := NewChatwootService(&fakeConfigRepo{}, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	if _, err := svc.ReconnectLink("avulsa"); err == nil {
		t.Fatal("expected ReconnectLink to reject an instance that is not linked to Chatwoot")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test ./pkg/chatwoot/service/ -run TestReconnectLink -v`
Expected: FAIL — `svc.ReconnectLink undefined`.

- [ ] **Step 3: Implementar `ReconnectLink`**

Adicionar após `CreateLink` em `chatwoot_service.go`:

```go
type ReconnectResult struct {
	InstanceID    string `json:"instanceId"`
	InstanceToken string `json:"instanceToken"`
	InboxID       string `json:"inboxId"`
}

// ReconnectLink religa uma instância já vinculada, sem criar nada — nem inbox,
// nem instância. É o caminho correto quando a sessão do WhatsApp cai.
//
// Passa Subscribe e WebhookUrl explicitamente porque instance_service.Connect
// sobrescreve instance.Events e instance.Webhook a partir do que recebe: com
// Subscribe vazio ele assumiria só MESSAGE, derrubando o READ_RECEIPT de que o
// sync de status depende. Isso também cura uma instância que ficou com Events
// vazio depois de um Disconnect.
func (s *ChatwootService) ReconnectLink(name string) (*ReconnectResult, error) {
	instance, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil || instance == nil {
		return nil, fmt.Errorf("conexão %q não encontrada", name)
	}
	if !instance.ChatwootEnabled || instance.ChatwootInboxID == "" {
		return nil, fmt.Errorf("a instância %q não está vinculada a uma inbox do Chatwoot", name)
	}

	_, _, _, err = s.instanceSvc.Connect(&instance_service.ConnectStruct{
		Subscribe:  []string{event_types.MESSAGE, event_types.READ_RECEIPT},
		WebhookUrl: instance.Webhook,
	}, instance)
	if err != nil {
		return nil, fmt.Errorf("religar instância: %w", err)
	}

	return &ReconnectResult{
		InstanceID:    instance.Id,
		InstanceToken: instance.Token,
		InboxID:       instance.ChatwootInboxID,
	}, nil
}
```

- [ ] **Step 4: Rodar os testes e confirmar que passam**

Run: `go build ./... && go test ./pkg/chatwoot/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w pkg/chatwoot/service/
git add pkg/chatwoot/service/
git commit -m "feat(chatwoot): add ReconnectLink reusing the existing instance and inbox"
```

---

### Task 6: Service — `DeleteLink`, `GetConfig` e `ListLinks` estendido

**Files:**
- Modify: `pkg/chatwoot/service/chatwoot_service.go:66-95` (`LinkView`, `ListLinks`), append
- Test: `pkg/chatwoot/service/chatwoot_service_test.go`

**Interfaces:**
- Produces:
  - `LinkView` ganha `InstanceID string \`json:"instanceId"\`` e `InstanceToken string \`json:"instanceToken"\``
  - `type ConfigView struct { BaseURL, AccountID, APITokenMasked string; Configured bool }`
  - `func (s *ChatwootService) GetConfig() (*ConfigView, error)`
  - `func (s *ChatwootService) DeleteLink(name string, deleteInbox bool) error`

- [ ] **Step 1: Escrever os testes que falham**

Adicionar a `chatwoot_service_test.go`:

```go
func TestGetConfig_MasksToken(t *testing.T) {
	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{
		BaseURL: "https://chat.example", APIToken: "abcdefghijklmnop", AccountID: "1",
	}}
	svc := NewChatwootService(cfgRepo, newFakeInstanceRepo(), newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))

	view, err := svc.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if !view.Configured || view.BaseURL != "https://chat.example" || view.AccountID != "1" {
		t.Fatalf("unexpected view: %+v", view)
	}
	if strings.Contains(view.APITokenMasked, "abcdefghijklmnop") {
		t.Fatalf("token leaked in the masked view: %q", view.APITokenMasked)
	}
	if !strings.HasSuffix(view.APITokenMasked, "mnop") {
		t.Fatalf("expected the last 4 chars to be shown, got %q", view.APITokenMasked)
	}
}

func TestGetConfig_NotConfigured(t *testing.T) {
	svc := NewChatwootService(&fakeConfigRepo{}, newFakeInstanceRepo(), newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	view, err := svc.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if view.Configured {
		t.Fatalf("expected Configured=false, got %+v", view)
	}
}

func TestListLinks_ExposesInstanceToken(t *testing.T) {
	instRepo := newFakeInstanceRepo()
	instRepo.byClient["evolution"] = []*instance_model.Instance{
		{Id: "inst-1", Name: "vendas", Token: "vendas-abc", ChatwootEnabled: true, ChatwootInboxID: "42"},
	}
	svc := NewChatwootService(&fakeConfigRepo{}, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))

	links, err := svc.ListLinks()
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if links[0].InstanceToken != "vendas-abc" || links[0].InstanceID != "inst-1" {
		t.Fatalf("expected instance id and token in the view, got %+v", links[0])
	}
}

func TestDeleteLink_ClearsFieldsAndOptionallyDeletesInbox(t *testing.T) {
	rec := &chatwootRecorder{}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{
		Id: "inst-1", Name: "vendas", ChatwootEnabled: true, ChatwootInboxID: "42",
		ChatwootInboxIdentifier: "abc", ChatwootWebhookSecret: "sek",
	}

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	if err := svc.DeleteLink("vendas", false); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	saved := instRepo.updated
	if saved.ChatwootEnabled || saved.ChatwootInboxID != "" || saved.ChatwootWebhookSecret != "" {
		t.Fatalf("expected chatwoot fields cleared, got %+v", saved)
	}
	if rec.called("DELETE") {
		t.Fatalf("expected the inbox to be kept, calls were %v", rec.calls)
	}
}

func TestDeleteLink_DeletesInboxWhenAsked(t *testing.T) {
	rec := &chatwootRecorder{}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{
		Id: "inst-1", Name: "vendas", ChatwootEnabled: true, ChatwootInboxID: "42",
	}

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	if err := svc.DeleteLink("vendas", true); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if !rec.called("DELETE /api/v1/accounts/1/inboxes/42") {
		t.Fatalf("expected the inbox to be deleted, calls were %v", rec.calls)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test ./pkg/chatwoot/service/ -v`
Expected: FAIL — `GetConfig`, `DeleteLink` indefinidos; `LinkView` sem `InstanceToken`.

- [ ] **Step 3: Estender `LinkView` e `ListLinks`**

Substituir `chatwoot_service.go:66-95` por:

```go
type LinkView struct {
	InstanceName  string `json:"instanceName"`
	InstanceID    string `json:"instanceId"`
	InstanceToken string `json:"instanceToken"`
	Number        string `json:"number"`
	InboxID       string `json:"inboxId"`
	InboxName     string `json:"inboxName"`
	Connected     bool   `json:"connected"`
	Enabled       bool   `json:"enabled"`
}

func (s *ChatwootService) ListLinks() ([]LinkView, error) {
	instances, err := s.instanceRepo.GetAll(s.clientName)
	if err != nil {
		return nil, err
	}
	views := []LinkView{}
	for _, inst := range instances {
		if !inst.ChatwootEnabled {
			continue
		}
		// O token da instância vai no payload para a tela conseguir chamar
		// /instance/qr, /instance/status e /instance/logout sem recriar nada.
		// A rota é admin-only (GLOBAL_API_KEY), o mesmo nível de segredo.
		views = append(views, LinkView{
			InstanceName:  inst.Name,
			InstanceID:    inst.Id,
			InstanceToken: inst.Token,
			Number:        inst.Jid,
			InboxID:       inst.ChatwootInboxID,
			InboxName:     inst.Name,
			Connected:     inst.Connected,
			Enabled:       inst.ChatwootEnabled,
		})
	}
	return views, nil
}
```

- [ ] **Step 4: Implementar `GetConfig` e `DeleteLink`**

Adicionar após `ReconnectLink`:

```go
type ConfigView struct {
	BaseURL        string `json:"baseUrl"`
	AccountID      string `json:"accountId"`
	APITokenMasked string `json:"apiTokenMasked"`
	Configured     bool   `json:"configured"`
}

// GetConfig devolve a config para a tela reidratar o formulário. O token nunca
// é reexposto — só os 4 últimos caracteres, para o operador reconhecer qual é.
func (s *ChatwootService) GetConfig() (*ConfigView, error) {
	cfg, err := s.configRepo.Get()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &ConfigView{}, nil
	}
	return &ConfigView{
		BaseURL:        cfg.BaseURL,
		AccountID:      cfg.AccountID,
		APITokenMasked: maskToken(cfg.APIToken),
		Configured:     true,
	}, nil
}

// maskToken mostra apenas os 4 últimos caracteres. Tokens curtos (<= 4) são
// mascarados por inteiro.
func maskToken(token string) string {
	if len(token) <= 4 {
		return strings.Repeat("•", len(token))
	}
	return strings.Repeat("•", 8) + token[len(token)-4:]
}

// DeleteLink desfaz o vínculo entre a instância e a inbox. A instância em si
// não é removida — para isso existe DELETE /instance/delete/:instanceId.
//
// deleteInbox apaga também a inbox no Chatwoot, o que destrói o histórico de
// conversas dela. Só é feito por pedido explícito do operador.
func (s *ChatwootService) DeleteLink(name string, deleteInbox bool) error {
	instance, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil || instance == nil {
		return fmt.Errorf("conexão %q não encontrada", name)
	}

	if deleteInbox && instance.ChatwootInboxID != "" {
		cfg, err := s.configRepo.Get()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("config do chatwoot ausente")
		}
		client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
		if err := client.DeleteInbox(atoiSafe(instance.ChatwootInboxID)); err != nil {
			return fmt.Errorf("remover inbox: %w", err)
		}
	}

	instance.ChatwootEnabled = false
	instance.ChatwootInboxID = ""
	instance.ChatwootInboxIdentifier = ""
	instance.ChatwootWebhookSecret = ""
	return s.instanceRepo.Update(instance)
}

// atoiSafe converte o id da inbox guardado como string; devolve 0 em qualquer
// caractere não numérico, o que faz a chamada ao Chatwoot falhar de forma
// visível em vez de acertar uma inbox errada.
func atoiSafe(s string) int {
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

Adicionar `"strings"` aos imports de `chatwoot_service.go`.

- [ ] **Step 5: Rodar os testes e confirmar que passam**

Run: `go build ./... && go test ./pkg/chatwoot/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w pkg/chatwoot/service/
git add pkg/chatwoot/service/
git commit -m "feat(chatwoot): add GetConfig, DeleteLink and expose instance token in links"
```

---

### Task 7: Handlers e rotas

**Files:**
- Modify: `pkg/chatwoot/handler/admin_handler.go` (append 3 handlers)
- Modify: `pkg/chatwoot/routes.go:14-25`

**Interfaces:**
- Consumes: `ChatwootService.GetConfig`, `.ReconnectLink`, `.DeleteLink` (Tasks 5-6)
- Produces: rotas `GET /chatwoot/config`, `POST /chatwoot/links/:instance/reconnect`, `DELETE /chatwoot/links/:instance`

- [ ] **Step 1: Adicionar os handlers**

Adicionar ao final de `admin_handler.go`:

```go
func (h *AdminHandler) GetConfig(ctx *gin.Context) {
	view, err := h.service.GetConfig()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": view})
}

func (h *AdminHandler) PostReconnect(ctx *gin.Context) {
	name := ctx.Param("instance")
	res, err := h.service.ReconnectLink(name)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *AdminHandler) DeleteLink(ctx *gin.Context) {
	name := ctx.Param("instance")
	deleteInbox := ctx.Query("deleteInbox") == "true"
	if err := h.service.DeleteLink(name, deleteInbox); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
```

- [ ] **Step 2: Registrar as rotas**

Substituir o bloco `api := eng.Group("/chatwoot")` em `routes.go:18-25` por:

```go
	// API de gestão (protegida por AuthAdmin)
	api := eng.Group("/chatwoot")
	api.Use(adminAuth)
	{
		api.GET("/config", admin.GetConfig)
		api.PUT("/config", admin.PutConfig)
		api.POST("/config/test", admin.TestConfig)
		api.GET("/links", admin.GetLinks)
		api.POST("/links", admin.PostLink)
		api.POST("/links/:instance/reconnect", admin.PostReconnect)
		api.DELETE("/links/:instance", admin.DeleteLink)
	}
```

- [ ] **Step 3: Compilar e verificar que o Gin não entra em conflito de rotas**

Run: `go build ./... && go vet ./pkg/chatwoot/...`
Expected: build OK, sem panic. O Gin faz panic em tempo de registro se houver conflito de wildcard — se acontecer, o binário falha ao subir, então o próximo passo é obrigatório.

- [ ] **Step 4: Subir o servidor e conferir que as rotas respondem**

Run: `go run ./cmd/evolution-go` num terminal e, noutro:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H "apikey: $GLOBAL_API_KEY" http://localhost:8080/chatwoot/config
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/chatwoot/config
```

Expected: `200` na primeira (com apikey) e `401` na segunda (sem apikey). Encerrar o servidor depois.

- [ ] **Step 5: Commit**

```bash
gofmt -w pkg/chatwoot/handler/ pkg/chatwoot/routes.go
git add pkg/chatwoot/handler/ pkg/chatwoot/routes.go
git commit -m "feat(chatwoot): expose config read, reconnect and unlink endpoints"
```

---

### Task 8: UI — fundação, tema e configuração

A partir daqui a tela é reescrita. Esta task entrega o esqueleto navegável: tokens, tema, header, drawer de config funcionando contra o `GET /chatwoot/config` novo.

**REQUIRED SUB-SKILL:** invocar `superpowers:frontend-design` antes de escrever o HTML.

**Files:**
- Rewrite: `pkg/chatwoot/ui/chatwoot_admin.html`

**Interfaces:**
- Consumes: `GET /chatwoot/config` → `{"data": {baseUrl, accountId, apiTokenMasked, configured}}`; `PUT /chatwoot/config`; `POST /chatwoot/config/test`
- Produces: funções JS `apiFetch(path, options)`, `toast(message, kind)`, `applyTheme(mode)`, reusadas nas Tasks 9-10

- [ ] **Step 1: Definir os tokens e o reset**

Substituir o `<style>` inteiro. Os tokens vêm do bundle do Manager (`manager/dist/assets/index-B6NZfWVh.css`) para a tela parecer parte dele:

```css
:root {
  --background: oklch(1 0 0);
  --foreground: oklch(.145 0 0);
  --card: oklch(1 0 0);
  --primary: oklch(67.35% .153 159.64);
  --primary-foreground: oklch(.985 0 0);
  --muted: oklch(.97 0 0);
  --muted-foreground: oklch(.556 0 0);
  --destructive: oklch(.577 .245 27.325);
  --border: oklch(.922 0 0);
  --radius: .625rem;
  --font-sans: Inter, ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}
html.dark {
  --background: oklch(.145 0 0);
  --foreground: oklch(.985 0 0);
  --card: oklch(.205 0 0);
  --primary: oklch(88.18% .202 159.34);
  --primary-foreground: oklch(.205 0 0);
  --muted: oklch(.269 0 0);
  --muted-foreground: oklch(.708 0 0);
  --border: oklch(.269 0 0);
}
* { box-sizing: border-box; }
body {
  margin: 0;
  padding: 32px 24px;
  font-family: var(--font-sans);
  background: var(--background);
  color: var(--foreground);
  -webkit-font-smoothing: antialiased;
}
```

O restante do CSS (botões, cards, drawer, modal, toasts, skeleton) é escrito seguindo a skill `frontend-design`, sempre referenciando estes tokens — nunca cores literais.

- [ ] **Step 2: Escrever o header e o gerenciamento de tema**

O `<html>` recebe a classe `dark` conforme `localStorage.getItem('evo_theme')`, com fallback para `window.matchMedia('(prefers-color-scheme: dark)')`. O toggle no header persiste a escolha:

```js
function applyTheme(mode) {
  document.documentElement.classList.toggle('dark', mode === 'dark');
  localStorage.setItem('evo_theme', mode);
}
const savedTheme = localStorage.getItem('evo_theme');
applyTheme(savedTheme || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));
```

- [ ] **Step 3: Implementar `toast` no lugar dos spans de status**

```js
function toast(message, kind) {
  const el = document.createElement('div');
  el.className = 'toast ' + (kind || 'info');
  el.textContent = message;
  document.getElementById('toasts').appendChild(el);
  setTimeout(() => el.classList.add('leaving'), 3600);
  setTimeout(() => el.remove(), 4000);
}
```

- [ ] **Step 4: Reidratar o drawer de config a partir do endpoint novo**

```js
async function loadConfig() {
  const { ok, data } = await apiFetch('/chatwoot/config');
  if (!ok || !data || !data.data) return null;
  const cfg = data.data;
  document.getElementById('cfg-base-url').value = cfg.baseUrl || '';
  document.getElementById('cfg-account-id').value = cfg.accountId || '1';
  // O token nunca volta em claro: mostramos a máscara como placeholder e só
  // enviamos no PUT se o operador digitar um valor novo.
  document.getElementById('cfg-api-token').placeholder = cfg.apiTokenMasked || 'Token de acesso da API';
  setConfigBadge(cfg.configured);
  return cfg;
}
```

O `PUT /chatwoot/config` continua exigindo os três campos. Se o operador deixar o token em branco tendo config salva, reenviar o token exige que ele digite de novo — documentar isso no texto de ajuda do drawer: *"deixe em branco só se for reenviar os outros campos; o token precisa ser digitado novamente"*. Para evitar essa pegadinha, o botão Salvar fica desabilitado enquanto o campo de token estiver vazio.

- [ ] **Step 5: Definir os helpers compartilhados**

Preservar do arquivo atual, sem alteração: `apiFetch`, `instanceFetch`, `escapeHtml` e `displayNumber` (`chatwoot_admin.html:255-267`, `:324-337`, `:445-449`, `:470-474`). Eles já fazem o certo e as Tasks 9-10 dependem deles.

Definir os dois novos, usados pelas Tasks 9-10:

```js
// Estado da config, carregado uma vez e reusado para montar links do Chatwoot.
let chatwootConfig = { baseUrl: '', accountId: '1', configured: false };

function setConfigBadge(configured) {
  const el = document.getElementById('config-badge');
  el.textContent = configured ? 'Chatwoot conectado' : 'Não configurado';
  el.className = 'badge ' + (configured ? 'ok' : 'warn');
  document.getElementById('needs-config').classList.toggle('open', !configured);
}

// Link direto para a inbox no Chatwoot, montado a partir da config carregada.
function inboxUrl(inboxId) {
  if (!chatwootConfig.baseUrl) return '#';
  return chatwootConfig.baseUrl.replace(/\/$/, '') +
    '/app/accounts/' + encodeURIComponent(chatwootConfig.accountId) +
    '/inbox/' + encodeURIComponent(inboxId);
}
```

Em `loadConfig` (Step 4), atribuir `chatwootConfig = cfg` antes de chamar `setConfigBadge(cfg.configured)`.

- [ ] **Step 6: Estado vazio guiado quando não há config**

Adicionar ao HTML um bloco `#needs-config` escondido por padrão (`.open` o revela — é o que `setConfigBadge` alterna):

```html
<div class="needs-config" id="needs-config">
  <h2>Configure o Chatwoot para começar</h2>
  <p>Informe a URL do seu Chatwoot, um token de API de administrador e o ID da conta. O token precisa ser de administrador — só assim o evolution consegue criar inboxes e recuperar o segredo do webhook.</p>
  <button class="primary" id="open-config-cta">Configurar agora</button>
</div>
```

`#open-config-cta` abre o drawer de config. Enquanto não houver config, o botão "Nova conexão" fica desabilitado — criar conexão sem config falharia com "config do chatwoot ausente", e é melhor impedir do que explicar o erro depois.

- [ ] **Step 7: Verificar manualmente**

Run: `go run ./cmd/evolution-go`, abrir `http://localhost:8080/chatwoot-admin`.
Verificar: tema alterna e persiste após reload; sem config salva, a tela mostra o estado guiado e "Nova conexão" está desabilitado; com a config salva, ao reabrir a tela os campos de URL e Account ID vêm preenchidos e o token aparece mascarado no placeholder; "Testar" produz um toast de sucesso ou de erro.

- [ ] **Step 8: Commit**

```bash
git add pkg/chatwoot/ui/chatwoot_admin.html
git commit -m "feat(chatwoot): rebuild admin UI shell with manager design tokens"
```

---

### Task 9: UI — cards de conexão com ações

**Files:**
- Modify: `pkg/chatwoot/ui/chatwoot_admin.html`

**Interfaces:**
- Consumes: `GET /chatwoot/links` (agora com `instanceId` e `instanceToken`), `DELETE /chatwoot/links/:instance?deleteInbox=`, `DELETE /instance/logout`; `apiFetch`, `toast` (Task 8)
- Produces: `renderCards(links)`, `loadLinks()`, `openQrModal(instanceToken, instanceName, inboxId)` (implementada na Task 10)

- [ ] **Step 1: Renderizar o card com estado e ações**

```js
function statusOf(link) {
  if (link.connected) return { dot: 'ok', label: 'Conectado' };
  if (!link.number) return { dot: 'warn', label: 'Aguardando pareamento' };
  return { dot: 'off', label: 'Desconectado' };
}

function renderCards(links) {
  const cardsEl = document.getElementById('cards');
  cardsEl.innerHTML = '';
  if (!links || links.length === 0) {
    cardsEl.innerHTML = '<div class="empty-state">Nenhuma conexão ainda. Use "Nova conexão" para criar a primeira.</div>';
    return;
  }
  links.forEach((link) => {
    const st = statusOf(link);
    const card = document.createElement('div');
    card.className = 'card';
    card.innerHTML =
      '<div class="card-head">' +
        '<span class="badge ' + st.dot + '">' + st.label + '</span>' +
        '<button class="icon-btn" data-menu="' + escapeHtml(link.instanceName) + '">⋯</button>' +
      '</div>' +
      '<div class="card-title">' + escapeHtml(link.instanceName) + '</div>' +
      '<div class="card-number">' + escapeHtml(displayNumber(link.number)) + '</div>' +
      '<div class="card-inbox">Inbox #' + escapeHtml(String(link.inboxId)) + '</div>' +
      '<div class="card-actions">' +
        '<button class="primary" data-reconnect="' + escapeHtml(link.instanceName) + '">Reconectar</button>' +
        '<a class="ghost" target="_blank" rel="noopener" href="' + escapeHtml(inboxUrl(link.inboxId)) + '">Abrir no Chatwoot</a>' +
      '</div>';
    cardsEl.appendChild(card);
  });
}
```

`inboxUrl` monta `{baseUrl}/app/accounts/{accountId}/inbox/{inboxId}` a partir da config carregada na Task 8.

- [ ] **Step 2: Ligar o botão Reconectar**

Delegação de evento no container, para os cards poderem ser re-renderizados livremente:

```js
document.getElementById('cards').addEventListener('click', async (ev) => {
  const name = ev.target.dataset && ev.target.dataset.reconnect;
  if (!name) return;
  ev.target.disabled = true;
  const { ok, data } = await apiFetch('/chatwoot/links/' + encodeURIComponent(name) + '/reconnect', { method: 'POST' });
  ev.target.disabled = false;
  if (!ok || !data || !data.data) {
    toast((data && data.error) || 'Falha ao reconectar', 'error');
    return;
  }
  openQrModal(data.data.instanceToken, name, data.data.inboxId);
});
```

- [ ] **Step 3: Implementar o menu com "Encerrar sessão" e "Remover"**

*Encerrar sessão* chama `DELETE /instance/logout` com o header `apikey: <instanceToken>` — **nunca** `POST /instance/disconnect`, que zera `instance.Events` e deixa a ponte muda sem sinal visível na tela.

*Remover* abre um diálogo de confirmação que exige digitar o nome da conexão, com um checkbox *"apagar também a inbox no Chatwoot"* desmarcado por padrão:

```js
async function confirmRemove(link) {
  const typed = window.prompt('Digite "' + link.instanceName + '" para confirmar a remoção do vínculo:');
  if (typed !== link.instanceName) return;
  const alsoInbox = window.confirm('Apagar TAMBÉM a inbox #' + link.inboxId + ' no Chatwoot?\n\nIsso destrói o histórico de conversas dela. Cancele para manter a inbox.');
  const qs = alsoInbox ? '?deleteInbox=true' : '';
  const { ok, data } = await apiFetch('/chatwoot/links/' + encodeURIComponent(link.instanceName) + qs, { method: 'DELETE' });
  if (!ok) {
    toast((data && data.error) || 'Falha ao remover', 'error');
    return;
  }
  toast('Vínculo removido', 'ok');
  loadLinks();
}
```

- [ ] **Step 4: Skeleton e auto-refresh**

Enquanto `loadLinks()` está em voo pela primeira vez, renderizar três cards de skeleton. Depois do primeiro carregamento, revalidar a cada 15 s com `setInterval(loadLinks, 15000)`, sem skeleton (atualização silenciosa) para o status não envelhecer na tela.

- [ ] **Step 5: Verificar manualmente**

Run: `go run ./cmd/evolution-go`, abrir `/chatwoot-admin` com pelo menos uma conexão existente.
Verificar: os cards mostram o estado correto; "Reconectar" abre o modal de QR sem criar inbox nova (conferir no Chatwoot que a contagem de inboxes não mudou); "Remover" com o checkbox desmarcado limpa o vínculo e mantém a inbox.

- [ ] **Step 6: Commit**

```bash
git add pkg/chatwoot/ui/chatwoot_admin.html
git commit -m "feat(chatwoot): add reconnect, unlink and logout actions to connection cards"
```

---

### Task 10: UI — modal de QR com estágio de passkey

**Files:**
- Modify: `pkg/chatwoot/ui/chatwoot_admin.html`

**Interfaces:**
- Consumes: `GET /instance/qr` e `GET /instance/status` com header `apikey: <instanceToken>`; `toast`, `loadLinks` (Tasks 8-9)
- Produces: `openQrModal(instanceToken, instanceName, inboxId)`, consumida pela Task 9 e pelo fluxo de criação

- [ ] **Step 1: Migrar o loop de pareamento para dentro do modal**

Preservar a lógica de temporizadores que já existe hoje (`QR_REFRESH_MS = 20000`, `STATUS_POLL_MS = 3000`, `PAIRING_TIMEOUT_MS = 120000`) e os handlers `refreshPairingQr` / `pollPairingStatus`, movendo a apresentação para o modal. Ao detectar conexão, fechar o modal, emitir `toast('Conectado', 'ok')` e chamar `loadLinks()`.

- [ ] **Step 2: Tratar o estágio de passkey**

`/instance/qr` devolve `passkeyStage`, `passkeyOpenUrl` e `passkeyCode` (`pkg/instance/service/instance_service.go:95-102`) quando a conta exige WebAuthn. Nesse estágio **não existe QR**: a tela atual ignora esses campos e fica presa em "Gerando QR…" para sempre.

```js
async function refreshPairingQr(instanceToken) {
  const { ok, data } = await instanceFetch('/instance/qr', instanceToken);
  const payload = (ok && data && data.data) || null;

  if (payload && payload.passkeyStage) {
    // A conta exige passkey WebAuthn — não há QR para escanear neste estágio.
    showPasskeyStage(payload.passkeyStage, payload.passkeyOpenUrl, payload.passkeyCode);
    return;
  }
  if (payload && payload.qrcode) {
    showQr(payload.qrcode);
    return;
  }
  showPairingMessage(ok ? 'Gerando QR…' : 'Aguardando instância iniciar…');
}
```

Os três helpers de apresentação usados acima:

```js
function showQr(dataUrl) {
  document.getElementById('passkey-box').classList.remove('open');
  const img = document.getElementById('pairing-qr');
  img.src = dataUrl;
  img.classList.add('show');
  showPairingMessage('Escaneie o QR no WhatsApp para parear.');
}

function showPairingMessage(text) {
  document.getElementById('pairing-message').textContent = text || '';
}

// A conta exige passkey WebAuthn: não há QR neste estágio, só um código de
// verificação e um link para concluir a cerimônia no WhatsApp Web.
function showPasskeyStage(stage, openUrl, code) {
  const img = document.getElementById('pairing-qr');
  img.classList.remove('show');
  img.src = '';
  document.getElementById('passkey-code').textContent = code || '';
  const link = document.getElementById('passkey-open');
  link.href = openUrl || '#';
  link.style.display = openUrl ? 'inline-flex' : 'none';
  document.getElementById('passkey-box').classList.add('open');
  showPairingMessage('Esta conta exige passkey. Abra o WhatsApp Web e confirme com o código abaixo.');
}
```

O `<a id="passkey-open">` usa `target="_blank" rel="noopener"`.

- [ ] **Step 3: Contador de expiração**

Exibir um contador regressivo de 120 s alimentado por `PAIRING_TIMEOUT_MS`. Ao zerar, parar os timers, esconder o QR e mostrar o botão "Gerar novo QR", que reinicia o loop com o mesmo token — sem chamar nenhum endpoint de criação.

- [ ] **Step 4: Fechar o modal com segurança**

Fechar por botão, por `Esc` e por clique no backdrop. Em todos os casos chamar `stopPairingLoop()` — deixar os `setInterval` vivos após fechar faria a tela seguir consultando `/instance/qr` indefinidamente.

- [ ] **Step 5: Verificar manualmente**

Run: `go run ./cmd/evolution-go`.
Verificar: criar uma conexão nova mostra o QR no modal e o card vira "Conectado" após parear; reconectar uma instância deslogada mostra QR novo sem criar inbox; fechar o modal interrompe o polling (conferir que não há mais requisições a `/instance/qr` na aba Network); deixar o QR expirar mostra o botão "Gerar novo QR" e ele volta a funcionar.

- [ ] **Step 6: Commit**

```bash
git add pkg/chatwoot/ui/chatwoot_admin.html
git commit -m "feat(chatwoot): move pairing into a modal and handle the passkey stage

/instance/qr returns passkeyStage/passkeyOpenUrl when the account requires
WebAuthn, and there is no QR to scan at that point. The old screen ignored
those fields and hung on 'Gerando QR…' forever."
```

---

### Task 11: Verificação ponta a ponta

**Files:** nenhum (verificação)

- [ ] **Step 1: Rodar a suíte inteira**

Run: `go build ./... && go test ./... `
Expected: PASS.

- [ ] **Step 2: Exercitar o cenário que originou o trabalho**

Com o Chatwoot e o evolution-go na rede compartilhada:

1. Criar uma conexão nova chamada `teste-recon` e parear.
2. Anotar a contagem de inboxes no Chatwoot.
3. Deslogar a sessão pelo celular (WhatsApp → Aparelhos conectados → sair).
4. Na tela, clicar **Reconectar** e parear de novo.
5. Conferir que a contagem de inboxes **não mudou** e que a conversa anterior continua na mesma inbox.

Expected: nenhuma inbox nova; a conversa segue na inbox original.

- [ ] **Step 3: Exercitar o conflito de nome**

Tentar criar uma conexão com o nome `teste-recon` de novo.
Expected: erro imediato orientando a usar Reconectar, e **nenhuma** inbox nova no Chatwoot.

- [ ] **Step 4: Exercitar o roteamento de resposta com duas inboxes**

Com duas conexões ativas (dois números) e o mesmo contato conversando com ambas:
1. Enviar mensagem do contato para o número A.
2. Responder pelo Chatwoot na conversa que apareceu.
3. Conferir que a resposta chegou pelo número A, não pelo B.

Expected: a resposta sai pelo número correto (regressão de D3).

- [ ] **Step 5: Commit final e push do branch**

```bash
git push -u origin feat/chatwoot-reconexao
```

---

## Pendência operacional (fora do código)

Após o deploy, auditar no Chatwoot as inboxes `Channel::Api` órfãs acumuladas pelo defeito D1 e removê-las manualmente, migrando antes qualquer conversa que valha preservar. Deliberadamente não automatizado: apagar inbox destrói histórico, e escolher quais preservar é decisão do operador.
