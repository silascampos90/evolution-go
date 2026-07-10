package chatwoot_service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	"github.com/evolution-foundation/evolution-go/pkg/config"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	event_types "github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
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
	updated  *instance_model.Instance
}

func newFakeInstanceRepo() *fakeInstanceRepo {
	return &fakeInstanceRepo{byClient: map[string][]*instance_model.Instance{}}
}

func (f *fakeInstanceRepo) GetAll(clientName string) ([]*instance_model.Instance, error) {
	return f.byClient[clientName], nil
}

func (f *fakeInstanceRepo) Update(instance *instance_model.Instance) error {
	f.updated = instance
	return nil
}

// fakeInstanceService implementa apenas o subconjunto usado pelo service (instanceCreator).
type fakeInstanceService struct {
	created *instance_model.Instance
}

func newFakeInstanceService() *fakeInstanceService {
	return &fakeInstanceService{}
}

func (f *fakeInstanceService) Create(data *instance_service.CreateStruct) (*instance_model.Instance, error) {
	inst := &instance_model.Instance{
		Id:         "inst-1",
		Name:       data.Name,
		Token:      data.Token,
		ClientName: "evolution",
	}
	f.created = inst
	return inst, nil
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
	if saved.Events != event_types.MESSAGE {
		t.Fatalf("expected instance to be subscribed to MESSAGE events, got Events=%q", saved.Events)
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
