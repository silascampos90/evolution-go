package chatwoot_service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	event_types "github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"gorm.io/gorm"
)

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

type ChatwootService struct {
	configRepo    chatwoot_repository.ChatwootConfigRepository
	instanceRepo  linkInstanceRepo
	instanceSvc   instanceManager
	selfBaseURL   string // ex http://evolution-go:8080
	clientName    string
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewChatwootService(
	configRepo chatwoot_repository.ChatwootConfigRepository,
	instanceRepo linkInstanceRepo,
	instanceSvc instanceManager,
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
	// Valida o conflito de nome ANTES de tocar no Chatwoot. A ordem importa:
	// instanceSvc.Create rejeita nome repetido, e criar a inbox primeiro fazia
	// cada tentativa de reconexão abandonar uma inbox órfã no Chatwoot.
	//
	// Um erro de banco diferente de "não encontrado" precisa interromper aqui
	// (fail closed): se deixássemos passar, a inbox seria criada no Chatwoot
	// antes do nome ter sido validado de fato.
	existing, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("verificar nome da instância: %w", err)
	}
	if existing != nil {
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

// randToken gera um sufixo curto (8 hex chars) usado para compor o token da
// instância auto-provisionada.
func randToken() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
