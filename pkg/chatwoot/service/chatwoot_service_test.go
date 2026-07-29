package chatwoot_service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	event_types "github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"gorm.io/gorm"
)

// fakeConfigRepo implementa chatwoot_repository.ChatwootConfigRepository.
type fakeConfigRepo struct {
	cfg *chatwoot_model.ChatwootConfig
}

func (f *fakeConfigRepo) Get() (*chatwoot_model.ChatwootConfig, error) { return f.cfg, nil }
func (f *fakeConfigRepo) Save(c *chatwoot_model.ChatwootConfig) error  { f.cfg = c; return nil }

// fakeInstanceRepo implementa apenas o subconjunto usado pelo service (linkInstanceRepo).
type fakeInstanceRepo struct {
	byClient map[string][]*instance_model.Instance
	byName   map[string]*instance_model.Instance
	getErr   error // se != nil, GetInstanceByName devolve este erro em vez do lookup normal
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

// GetInstanceByName espelha o repositório real, que devolve gorm.ErrRecordNotFound
// quando não acha (e pode ser configurado para simular outros erros de banco).
func (f *fakeInstanceRepo) GetInstanceByName(name string) (*instance_model.Instance, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if inst, ok := f.byName[name]; ok {
		return inst, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeInstanceRepo) Update(instance *instance_model.Instance) error {
	f.updated = instance
	return nil
}

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

func newTestLogger(t *testing.T) *logger_wrapper.LoggerManager {
	t.Helper()
	dir, err := os.MkdirTemp("", "chatwoot-service-test-logs")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return logger_wrapper.NewLoggerManager(&config.Config{LogDirectory: dir})
}

func TestCreateLink_ProvisionsInboxAndPersistsFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "inbox_identifier": "abc", "secret": "sek",
		})
	}))
	defer srv.Close()

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instSvc := newFakeInstanceService()

	svc := NewChatwootService(cfgRepo, instRepo, instSvc, "http://evolution-go:8080", "evolution", newTestLogger(t))
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
	if saved.Events != event_types.MESSAGE+","+event_types.READ_RECEIPT {
		t.Fatalf("expected instance to be subscribed to MESSAGE and READ_RECEIPT events, got Events=%q", saved.Events)
	}
}

// TestListLinks_FiltersByClientName exercises the Issue 1 fix: GetAll must be
// called with the service's clientName (not ""), since instances are created
// with ClientName = config.ClientName and GetAll("") would return nothing.
func TestListLinks_FiltersByClientName(t *testing.T) {
	cfgRepo := &fakeConfigRepo{}
	instRepo := newFakeInstanceRepo()
	instRepo.byClient["evolution"] = []*instance_model.Instance{
		{Name: "vendas", Jid: "5511999@s.whatsapp.net", ChatwootEnabled: true, ChatwootInboxID: "42", Connected: true},
		{Name: "suporte", ChatwootEnabled: false},
	}
	instRepo.byClient[""] = []*instance_model.Instance{}

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))

	links, err := svc.ListLinks()
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link (only chatwoot-enabled), got %d: %+v", len(links), links)
	}
	if links[0].InstanceName != "vendas" || links[0].InboxID != "42" {
		t.Fatalf("unexpected link: %+v", links[0])
	}
}

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
	if !errors.Is(err, ErrLinkAlreadyExists) {
		t.Fatalf("expected errors.Is(err, ErrLinkAlreadyExists), got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected zero Chatwoot calls, got %v", rec.calls)
	}
}

// Fail closed: um erro de banco diferente de "não encontrado" ao checar o nome
// não pode deixar a execução prosseguir para o Chatwoot. Antes da correção o
// guard só checava err == nil, então uma falha de conexão era tratada como
// "nome livre" e a inbox era criada antes de sabermos se o nome era válido.
func TestCreateLink_DatabaseErrorDuringNameCheckFailsClosed(t *testing.T) {
	rec := &chatwootRecorder{}
	srv := rec.server(t)

	cfgRepo := &fakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
	instRepo := newFakeInstanceRepo()
	instRepo.getErr = errors.New("connection refused")

	svc := NewChatwootService(cfgRepo, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	_, err := svc.CreateLink("vendas")
	if err == nil {
		t.Fatal("expected CreateLink to fail when the name check errors")
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

// Regressão da armadilha do Connect: ele sobrescreve instance.Events a partir
// do corpo da requisição, e com Subscribe vazio reduz a assinatura a só
// MESSAGE — o que mataria o sync de status (READ_RECEIPT). ReconnectLink
// precisa passar os dois eventos explicitamente e preservar o webhook.
//
// O Connect sobrescreve (e persiste) CINCO campos, não dois: Rabbitmq, Nats e
// WebSocket são string e vazio significa desligado, então não repassá-los
// desligava o fan-out de eventos de quem usa fila, em silêncio.
func TestReconnectLink_PreservesEventsAndWebhook(t *testing.T) {
	instRepo := newFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{
		Id:              "inst-1",
		Name:            "vendas",
		Token:           "vendas-abc",
		Webhook:         "https://cliente.example/hook",
		RabbitmqEnable:  "enabled",
		NatsEnable:      "true",
		WebSocketEnable: "enabled",
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
	if instSvc.connectedTo.RabbitmqEnable != "enabled" {
		t.Fatalf("expected RabbitmqEnable to survive the reconnect, got %q", instSvc.connectedTo.RabbitmqEnable)
	}
	if instSvc.connectedTo.NatsEnable != "true" {
		t.Fatalf("expected NatsEnable to survive the reconnect, got %q", instSvc.connectedTo.NatsEnable)
	}
	if instSvc.connectedTo.WebSocketEnable != "enabled" {
		t.Fatalf("expected WebSocketEnable to survive the reconnect, got %q", instSvc.connectedTo.WebSocketEnable)
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

// Fail closed: um erro de banco diferente de "não encontrado" ao buscar a
// instância não pode virar um enganoso "conexão não encontrada". A causa real
// deve ser propagada (com %w).
func TestReconnectLink_DatabaseErrorDuringNameCheckFailsClosed(t *testing.T) {
	instRepo := newFakeInstanceRepo()
	instRepo.getErr = errors.New("connection refused")

	svc := NewChatwootService(&fakeConfigRepo{}, instRepo, newFakeInstanceService(), "http://evolution-go:8080", "evolution", newTestLogger(t))
	_, err := svc.ReconnectLink("qualquer")
	if err == nil {
		t.Fatal("expected ReconnectLink to fail when the name check errors")
	}
	// Verifica que o erro contém a causa real, não "não encontrada"
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected error to contain the underlying cause, got: %v", err)
	}
	if strings.Contains(err.Error(), "não encontrada") {
		t.Fatalf("expected error NOT to contain 'não encontrada', got: %v", err)
	}
}

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
