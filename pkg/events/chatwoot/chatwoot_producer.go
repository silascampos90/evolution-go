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
				Conversation    string `json:"conversation"`
				ExtendedTextMsg struct {
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
	// cache jid -> conversationID por instancia, para pular lookups
	convCache sync.Map // key: instanceID+"|"+jid  value: convCacheEntry
	// workers mantem um worker goroutine por cacheKey (instanceID+"|"+jid), que
	// drena seu channel em ordem FIFO. Isso garante que mensagens do mesmo JID
	// sejam processadas na ordem de chegada, enquanto JIDs diferentes continuam
	// em paralelo. O worker tambem serializa a secao check-then-create de
	// contato/conversa para sua key, entao nenhum mutex extra e necessario.
	workers sync.Map // key: cacheKey (string)  value: chan []byte
}

const workerChanBuffer = 64

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

// Produce recebe o envelope de evento e o enfileira no worker responsavel
// pelo JID de origem, preservando a ordem de chegada por conversa.
func (p *chatwootProducer) Produce(queueName string, payload []byte, _ string, userID string) error {
	msg, ok := parseIncomingText(payload)
	if !ok {
		return nil
	}

	cacheKey := msg.InstanceID + "|" + msg.JID
	p.workerFor(cacheKey, userID) <- payload
	return nil
}

// workerFor retorna o channel do worker responsavel por cacheKey, criando-o
// (e iniciando sua unica goroutine consumidora) na primeira chamada para essa
// key. A goroutine drena o channel em ordem FIFO, entao mensagens do mesmo
// JID sao processadas na ordem de chegada; JIDs diferentes tem workers
// distintos e continuam em paralelo. Como apenas essa goroutine chama handle
// para a key, a secao check-then-create de contato/conversa fica serializada
// sem necessidade de mutex. O worker vive pela duracao do processo, mesmo
// trade-off ja aceito para o convCache (sem eviction).
func (p *chatwootProducer) workerFor(cacheKey, userID string) chan []byte {
	newCh := make(chan []byte, workerChanBuffer)
	actual, loaded := p.workers.LoadOrStore(cacheKey, newCh)
	ch := actual.(chan []byte)
	if !loaded {
		go func() {
			for payload := range ch {
				p.handle(payload, userID)
			}
		}()
	}
	return ch
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

	// Sem entrada em cache (primeira mensagem do JID, ou cache perdido em um
	// restart): reconcilia com o Chatwoot (fonte da verdade) em vez de criar
	// contato/conversa as cegas, para nao falhar no 422 de telefone duplicado
	// nem criar conversas duplicadas apos um restart.
	phone := phoneFromJID(msg.JID)
	contact, err := client.FindContactByPhone(phone)
	if err != nil {
		log.LogError("[%s] chatwoot: falha ao buscar contato: %v", userID, err)
		return
	}
	if contact == nil {
		contact, err = client.FindOrCreateContact(msg.PushName, phone, msg.JID, inboxID)
		if err != nil {
			log.LogError("[%s] chatwoot: falha contato: %v", userID, err)
			return
		}
	}

	convID, ok, err := client.FindOpenConversation(contact.ID)
	if err != nil {
		log.LogError("[%s] chatwoot: falha ao buscar conversa: %v", userID, err)
		return
	}
	if !ok {
		conv, err := client.CreateConversation(inboxID, contact.ID, msg.JID)
		if err != nil {
			log.LogError("[%s] chatwoot: falha conversa: %v", userID, err)
			return
		}
		convID = conv.ID
	}
	p.convCache.Store(cacheKey, convCacheEntry{ContactID: contact.ID, ConversationID: convID})

	if err := client.CreateIncomingMessage(convID, msg.Text, msg.Wamid); err != nil {
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
