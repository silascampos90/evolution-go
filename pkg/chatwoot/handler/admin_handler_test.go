package chatwoot_handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_service "github.com/evolution-foundation/evolution-go/pkg/chatwoot/service"
	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Fakes no mesmo espírito dos do package service: só o subconjunto de métodos
// que o ChatwootService consome, para os testes de handler exercitarem o
// mapeamento de status HTTP sem banco nem Chatwoot de verdade.
type adminFakeConfigRepo struct {
	cfg *chatwoot_model.ChatwootConfig
}

func (f *adminFakeConfigRepo) Get() (*chatwoot_model.ChatwootConfig, error) { return f.cfg, nil }
func (f *adminFakeConfigRepo) Save(c *chatwoot_model.ChatwootConfig) error  { f.cfg = c; return nil }

type adminFakeInstanceRepo struct {
	byName  map[string]*instance_model.Instance
	getErr  error
	updated *instance_model.Instance
}

func newAdminFakeInstanceRepo() *adminFakeInstanceRepo {
	return &adminFakeInstanceRepo{byName: map[string]*instance_model.Instance{}}
}

func (f *adminFakeInstanceRepo) GetAll(clientName string) ([]*instance_model.Instance, error) {
	return nil, nil
}

func (f *adminFakeInstanceRepo) GetInstanceByName(name string) (*instance_model.Instance, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if inst, ok := f.byName[name]; ok {
		return inst, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *adminFakeInstanceRepo) Update(instance *instance_model.Instance) error {
	f.updated = instance
	return nil
}

type adminFakeInstanceService struct{}

func (f *adminFakeInstanceService) Create(data *instance_service.CreateStruct) (*instance_model.Instance, error) {
	return &instance_model.Instance{Id: "inst-1", Name: data.Name, Token: data.Token}, nil
}

func (f *adminFakeInstanceService) Connect(data *instance_service.ConnectStruct, instance *instance_model.Instance) (*instance_model.Instance, string, string, error) {
	return instance, instance.Jid, "", nil
}

func newAdminTestLogger(t *testing.T) *logger_wrapper.LoggerManager {
	t.Helper()
	dir, err := os.MkdirTemp("", "chatwoot-handler-test-logs")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return logger_wrapper.NewLoggerManager(&config.Config{LogDirectory: dir})
}

// chatwootStub registra as chamadas recebidas, para os testes afirmarem se a
// inbox foi (ou não foi) apagada no Chatwoot.
type chatwootStub struct {
	calls []string
}

func (s *chatwootStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *chatwootStub) called(substr string) bool {
	for _, c := range s.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// newTestRouter monta as mesmas rotas do routes.go, sem middleware de auth.
func newTestRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	eng := gin.New()
	eng.PUT("/chatwoot/config", h.PutConfig)
	eng.POST("/chatwoot/links", h.PostLink)
	eng.POST("/chatwoot/links/:instance/reconnect", h.PostReconnect)
	eng.DELETE("/chatwoot/links/:instance", h.DeleteLink)
	return eng
}

func do(t *testing.T, eng *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Mapeamento de erro -> status
// ---------------------------------------------------------------------------

// Nome já em uso é conflito (409), não erro genérico: a tela usa o status para
// dizer "use Reconectar" em vez de "erro inesperado".
func TestPostLink_ConflictWhenNameAlreadyExists(t *testing.T) {
	instRepo := newAdminFakeInstanceRepo()
	instRepo.byName["vendas"] = &instance_model.Instance{Id: "inst-1", Name: "vendas"}

	svc := chatwoot_service.NewChatwootService(&adminFakeConfigRepo{}, instRepo, &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	rec := do(t, eng, http.MethodPost, "/chatwoot/links", `{"name":"vendas"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Conexão inexistente é 404 nos dois métodos que a reportam. Antes tudo caía em
// 400, o que fazia um Chatwoot fora do ar parecer erro de digitação.
func TestReconnectAndDelete_NotFoundIs404(t *testing.T) {
	svc := chatwoot_service.NewChatwootService(&adminFakeConfigRepo{}, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	if rec := do(t, eng, http.MethodPost, "/chatwoot/links/fantasma/reconnect", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("reconnect: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, eng, http.MethodDelete, "/chatwoot/links/fantasma", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Falha de infraestrutura é 5xx, para alerta de log distinguir de erro de uso.
func TestReconnectAndDelete_InfrastructureFailureIs500(t *testing.T) {
	instRepo := newAdminFakeInstanceRepo()
	instRepo.getErr = errors.New("connection refused")

	svc := chatwoot_service.NewChatwootService(&adminFakeConfigRepo{}, instRepo, &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	if rec := do(t, eng, http.MethodPost, "/chatwoot/links/vendas/reconnect", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("reconnect: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, eng, http.MethodDelete, "/chatwoot/links/vendas", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// deleteInbox: a comparação estrita com "true" é a garantia inteira de opt-in
// de uma ação que destrói o histórico de conversas.
// ---------------------------------------------------------------------------
func TestDeleteLink_DeleteInboxOptInIsStrict(t *testing.T) {
	cases := []struct {
		query      string
		wantDelete bool
	}{
		{"", false},
		{"?deleteInbox=", false},
		{"?deleteInbox=false", false},
		{"?deleteInbox=1", false},
		{"?deleteInbox=TRUE", false},
		{"?deleteInbox=True", false},
		{"?deleteInbox=yes", false},
		{"?deleteInbox=true", true},
	}
	for _, tc := range cases {
		stub := &chatwootStub{}
		srv := stub.server(t)

		cfgRepo := &adminFakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{BaseURL: srv.URL, APIToken: "t", AccountID: "1"}}
		instRepo := newAdminFakeInstanceRepo()
		instRepo.byName["vendas"] = &instance_model.Instance{
			Id: "inst-1", Name: "vendas", ChatwootEnabled: true, ChatwootInboxID: "42",
		}

		svc := chatwoot_service.NewChatwootService(cfgRepo, instRepo, &adminFakeInstanceService{},
			"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
		eng := newTestRouter(NewAdminHandler(svc))

		rec := do(t, eng, http.MethodDelete, "/chatwoot/links/vendas"+tc.query, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("query %q: expected 200, got %d: %s", tc.query, rec.Code, rec.Body.String())
		}
		got := stub.called("DELETE /api/v1/accounts/1/inboxes/42")
		if got != tc.wantDelete {
			t.Fatalf("query %q: inbox deleted = %v, want %v (calls: %v)", tc.query, got, tc.wantDelete, stub.calls)
		}
	}
}

// ---------------------------------------------------------------------------
// PUT /chatwoot/config
// ---------------------------------------------------------------------------

// O baseUrl volta para o navegador como href do "Abrir no Chatwoot": esquema
// que não seja http(s) é rejeitado no servidor, não só na tela.
func TestPutConfig_RejectsNonHttpScheme(t *testing.T) {
	for _, bad := range []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"chat.example.com",
		"ftp://chat.example.com",
	} {
		cfgRepo := &adminFakeConfigRepo{}
		svc := chatwoot_service.NewChatwootService(cfgRepo, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
			"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
		eng := newTestRouter(NewAdminHandler(svc))

		body := `{"baseUrl":"` + bad + `","apiToken":"t","accountId":"1"}`
		rec := do(t, eng, http.MethodPut, "/chatwoot/config", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("baseUrl %q: expected 400, got %d: %s", bad, rec.Code, rec.Body.String())
		}
		if cfgRepo.cfg != nil {
			t.Fatalf("baseUrl %q: expected nothing to be saved, got %+v", bad, cfgRepo.cfg)
		}
	}
}

func TestPutConfig_AcceptsHttpAndHttps(t *testing.T) {
	for _, good := range []string{"http://chat.example.com", "https://chat.example.com"} {
		cfgRepo := &adminFakeConfigRepo{}
		svc := chatwoot_service.NewChatwootService(cfgRepo, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
			"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
		eng := newTestRouter(NewAdminHandler(svc))

		body := `{"baseUrl":"` + good + `","apiToken":"t","accountId":"1"}`
		rec := do(t, eng, http.MethodPut, "/chatwoot/config", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("baseUrl %q: expected 200, got %d: %s", good, rec.Code, rec.Body.String())
		}
		if cfgRepo.cfg == nil || cfgRepo.cfg.BaseURL != good {
			t.Fatalf("baseUrl %q: not saved, got %+v", good, cfgRepo.cfg)
		}
	}
}

// Token omitido com config já salva: mantém o token do servidor. É o que
// permite mudar só o ID da conta sem redigitar um segredo que a tela não mostra.
func TestPutConfig_OmittedTokenKeepsStoredOne(t *testing.T) {
	cfgRepo := &adminFakeConfigRepo{cfg: &chatwoot_model.ChatwootConfig{
		BaseURL: "https://chat.example.com", APIToken: "token-secreto", AccountID: "1",
	}}
	svc := chatwoot_service.NewChatwootService(cfgRepo, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	rec := do(t, eng, http.MethodPut, "/chatwoot/config", `{"baseUrl":"https://chat.example.com","accountId":"9"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if cfgRepo.cfg.APIToken != "token-secreto" {
		t.Fatalf("expected the stored token to be kept, got %q", cfgRepo.cfg.APIToken)
	}
	if cfgRepo.cfg.AccountID != "9" {
		t.Fatalf("expected accountId to be updated, got %+v", cfgRepo.cfg)
	}
	// O token nunca pode aparecer na resposta.
	if strings.Contains(rec.Body.String(), "token-secreto") {
		t.Fatalf("token leaked in the response body: %s", rec.Body.String())
	}
}

// Sem config salva não há token para manter: 400, com mensagem de uso.
func TestPutConfig_OmittedTokenWithoutStoredConfigIs400(t *testing.T) {
	cfgRepo := &adminFakeConfigRepo{}
	svc := chatwoot_service.NewChatwootService(cfgRepo, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	rec := do(t, eng, http.MethodPut, "/chatwoot/config", `{"baseUrl":"https://chat.example.com","accountId":"1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if cfgRepo.cfg != nil {
		t.Fatalf("expected nothing to be saved, got %+v", cfgRepo.cfg)
	}
}

func TestPutConfig_RequiresBaseUrlAndAccountId(t *testing.T) {
	svc := chatwoot_service.NewChatwootService(&adminFakeConfigRepo{}, newAdminFakeInstanceRepo(), &adminFakeInstanceService{},
		"http://evolution-go:8080", "evolution", newAdminTestLogger(t))
	eng := newTestRouter(NewAdminHandler(svc))

	for _, body := range []string{
		`{"apiToken":"t","accountId":"1"}`,
		`{"baseUrl":"https://chat.example.com","apiToken":"t"}`,
	} {
		if rec := do(t, eng, http.MethodPut, "/chatwoot/config", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
}
