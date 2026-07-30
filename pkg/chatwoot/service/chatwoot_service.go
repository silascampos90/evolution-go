package chatwoot_service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_model "github.com/evolution-foundation/evolution-go/pkg/instance/model"
	instance_service "github.com/evolution-foundation/evolution-go/pkg/instance/service"
	event_types "github.com/evolution-foundation/evolution-go/pkg/internal/event_types"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	"gorm.io/gorm"
)

// ErrLinkAlreadyExists indica que já existe uma instância com o nome pedido.
// É um erro de uso esperado (o operador deveria reconectar), não uma falha —
// o handler o mapeia para 409.
var ErrLinkAlreadyExists = errors.New("conexão já existe")

// ErrLinkNotFound indica que não existe conexão com o nome pedido. É erro de
// uso esperado, não falha — o handler o mapeia para 404.
var ErrLinkNotFound = errors.New("conexão não encontrada")

// ErrAPITokenRequired indica que o PUT da config veio sem apiToken e não há
// nenhum token salvo para manter. Erro de uso esperado — o handler o mapeia
// para 400.
var ErrAPITokenRequired = errors.New("apiToken é obrigatório na primeira configuração")

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

// SaveConfig grava a config do Chatwoot. Um apiToken vazio significa "mantenha
// o token já salvo": o token nunca volta em claro para a tela (só mascarado),
// então exigi-lo em todo PUT obrigava o operador a redigitá-lo para mudar só o
// ID da conta — e um clique fora do drawer descartava um token digitado que não
// dá para recuperar do servidor. Sem config salva ainda não há o que manter, e
// aí o token continua obrigatório (ErrAPITokenRequired).
func (s *ChatwootService) SaveConfig(baseURL, apiToken, accountID string) error {
	if apiToken == "" {
		current, err := s.configRepo.Get()
		if err != nil {
			return err
		}
		if current == nil || current.APIToken == "" {
			return ErrAPITokenRequired
		}
		apiToken = current.APIToken
	}
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
		return nil, fmt.Errorf("%w: %q; use Reconectar em vez de criar outra", ErrLinkAlreadyExists, name)
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
	} else {
		// Inbox reusada: o secret só vem no payload do GET /inboxes quando o
		// api_access_token é de um ADMINISTRADOR da conta (ver FindInboxByName).
		// Com token de agente ele volta vazio, e aí o webhook_handler rejeita
		// toda resposta de agente (fail closed por assinatura HMAC): a ponte
		// ficaria só de ida, com o cartão verde e nenhum sinal de erro. Falha
		// aqui, antes de criar a instância. A inbox NÃO é apagada — não é nossa.
		if inbox.Secret == "" {
			return nil, fmt.Errorf("a inbox %q já existe no Chatwoot, mas o segredo do webhook não veio na resposta: o token de API precisa ser de um administrador da conta para recuperá-lo (sem ele as respostas dos agentes seriam rejeitadas)", name)
		}
		if inbox.WebhookURL != webhookURL {
			if err := client.UpdateInboxWebhook(inbox.ID, webhookURL); err != nil {
				return nil, fmt.Errorf("corrigir webhook da inbox: %w", err)
			}
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
	// Mesma disciplina do CreateLink: só "não encontrado" é ausência; qualquer
	// outro erro do banco é propagado com a causa real, em vez de virar um
	// enganoso "conexão não encontrada" na cara do operador.
	instance, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("buscar instância: %w", err)
	}
	if instance == nil {
		return nil, fmt.Errorf("%w: %q", ErrLinkNotFound, name)
	}
	if !instance.ChatwootEnabled || instance.ChatwootInboxID == "" {
		return nil, fmt.Errorf("a instância %q não está vinculada a uma inbox do Chatwoot", name)
	}

	// Os CINCO campos que Connect sobrescreve precisam ser relidos da instância.
	// Rabbitmq/Nats/WebSocket são string: vazio significa desligado, e o Connect
	// PERSISTE isso. Passar só Subscribe e WebhookUrl desligaria silenciosamente
	// o fan-out de eventos de quem usa fila — sem erro, sem log.
	_, _, _, err = s.instanceSvc.Connect(&instance_service.ConnectStruct{
		Subscribe:       []string{event_types.MESSAGE, event_types.READ_RECEIPT},
		WebhookUrl:      instance.Webhook,
		RabbitmqEnable:  instance.RabbitmqEnable,
		NatsEnable:      instance.NatsEnable,
		WebSocketEnable: instance.WebSocketEnable,
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

// SetInbox aponta uma conexão existente para outra inbox, escolhida pelo id.
//
// Existe porque casar por nome é ambíguo: duas inboxes podem ter o mesmo nome, e
// o FindInboxByName pega a primeira do payload — a conexão pode acabar vinculada
// a uma inbox diferente da que o operador vê na tela. Aqui o id manda.
//
// Não cria nem apaga inbox nenhuma: só troca o vínculo e corrige o webhook da
// inbox de destino para apontar para esta instância.
func (s *ChatwootService) SetInbox(name string, inboxID int) error {
	instance, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("buscar instância: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("%w: %q", ErrLinkNotFound, name)
	}

	cfg, err := s.configRepo.Get()
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("config do chatwoot ausente")
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)

	inbox, err := client.GetInboxByID(inboxID)
	if err != nil {
		return fmt.Errorf("buscar inbox %d: %w", inboxID, err)
	}
	// Mesma disciplina do CreateLink ao reusar inbox: sem o secret o receiver
	// rejeita todo webhook do Chatwoot por HMAC, e a ponte fica muda só no
	// sentido de saída — sem nenhum sinal na tela.
	if inbox.Secret == "" {
		return fmt.Errorf("a inbox %d não devolveu o secret do webhook; o token da API precisa ser de um administrador da conta", inboxID)
	}

	webhookURL := fmt.Sprintf("%s/chatwoot/webhook/%s", s.selfBaseURL, name)
	if inbox.WebhookURL != webhookURL {
		if err := client.UpdateInboxWebhook(inbox.ID, webhookURL); err != nil {
			return fmt.Errorf("apontar o webhook da inbox para esta instância: %w", err)
		}
	}

	instance.ChatwootEnabled = true
	instance.ChatwootInboxID = fmt.Sprintf("%d", inbox.ID)
	instance.ChatwootInboxIdentifier = inbox.Identifier
	instance.ChatwootWebhookSecret = inbox.Secret
	return s.instanceRepo.Update(instance)
}

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
	// Mesma disciplina do CreateLink e do ReconnectLink: erro real de banco não
	// pode se disfarçar de "não encontrada".
	instance, err := s.instanceRepo.GetInstanceByName(name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("buscar instância: %w", err)
	}
	if instance == nil {
		return fmt.Errorf("%w: %q", ErrLinkNotFound, name)
	}

	// Ordem deliberada: a inbox é apagada no Chatwoot ANTES de o desvínculo ser
	// gravado. Se o Update falhar depois, sobra um vínculo apontando para uma
	// inbox que já não existe — o operador vê o cartão e tenta remover de novo,
	// o que é recuperável. Gravar primeiro apagaria o id da inbox do banco e a
	// inbox órfã ficaria sem nenhuma forma de ser removida pela tela.
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

// randToken gera um sufixo curto (8 hex chars) usado para compor o token da
// instância auto-provisionada.
func randToken() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
