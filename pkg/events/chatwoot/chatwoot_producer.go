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
	// cache jid -> conversationID por instância, para pular lookups
	convCache sync.Map // key: instanceID+"|"+jid  value: convCacheEntry
	// keyLocks serializa a seção check-then-create de contato/conversa por cacheKey,
	// evitando que duas goroutines para o mesmo JID criem conversas duplicadas.
	// JIDs diferentes continuam processando em paralelo.
	keyLocks sync.Map // key: cacheKey (string)  value: *sync.Mutex
}

// lockKey retorna (criando se necessário) o mutex associado a cacheKey.
func (p *chatwootProducer) lockKey(cacheKey string) *sync.Mutex {
	m, _ := p.keyLocks.LoadOrStore(cacheKey, &sync.Mutex{})
	return m.(*sync.Mutex)
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

	// Serializa check-then-create por cacheKey (instância+JID), para que duas
	// mensagens concorrentes do mesmo contato não criem conversas duplicadas.
	// JIDs diferentes possuem mutexes distintos e continuam em paralelo.
	mu := p.lockKey(cacheKey)
	mu.Lock()
	defer mu.Unlock()

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
