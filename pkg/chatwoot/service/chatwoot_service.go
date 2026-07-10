package chatwoot_service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	event_types "github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
)

// instanceCreator é o subconjunto de instance_service.InstanceService usado por
// este service. Definido localmente (idioma Go "accept interfaces, return
// structs") para que os testes não precisem fakear a interface inteira.
type instanceCreator interface {
	Create(data *instance_service.CreateStruct) (*instance_model.Instance, error)
}

// linkInstanceRepo é o subconjunto de instance_repository.InstanceRepository
// usado por este service.
type linkInstanceRepo interface {
	GetAll(clientName string) ([]*instance_model.Instance, error)
	Update(*instance_model.Instance) error
}

type ChatwootService struct {
	configRepo    chatwoot_repository.ChatwootConfigRepository
	instanceRepo  linkInstanceRepo
	instanceSvc   instanceCreator
	selfBaseURL   string // ex http://evolution-go:8080
	clientName    string
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewChatwootService(
	configRepo chatwoot_repository.ChatwootConfigRepository,
	instanceRepo linkInstanceRepo,
	instanceSvc instanceCreator,
	selfBaseURL string,
	clientName string,
	loggerWrapper *logger_wrapper.LoggerManager,
) *ChatwootService {
	return &ChatwootService{configRepo, instanceRepo, instanceSvc, selfBaseURL, clientName, loggerWrapper}
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
	InboxName    string `json:"inboxName"`
	Connected    bool   `json:"connected"`
	Enabled      bool   `json:"enabled"`
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
		views = append(views, LinkView{
			InstanceName: inst.Name,
			Number:       inst.Jid,
			InboxID:      inst.ChatwootInboxID,
			InboxName:    inst.Name,
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
	if err != nil {
		return nil, err
	}
	if cfg == nil {
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
	created.Events = event_types.MESSAGE
	if err := s.instanceRepo.Update(created); err != nil {
		return nil, fmt.Errorf("persistir vínculo: %w", err)
	}

	return &CreateLinkResult{InstanceID: created.Id, InstanceToken: token, InboxID: created.ChatwootInboxID}, nil
}

// randToken gera um sufixo curto (8 hex chars) usado para compor o token da
// instância auto-provisionada.
func randToken() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
