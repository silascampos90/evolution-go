package chatwoot_handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_model "github.com/evolution-foundation/evolution-go/pkg/chatwoot/model"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	send_service "github.com/evolution-foundation/evolution-go/pkg/sendMessage/service"
	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	instanceRepo   instance_repository.InstanceRepository
	sendService    send_service.SendService
	configRepo     chatwoot_repository.ChatwootConfigRepository
	messageMapRepo chatwoot_repository.MessageMapRepository
	loggerWrapper  *logger_wrapper.LoggerManager
}

func NewWebhookHandler(
	instanceRepo instance_repository.InstanceRepository,
	sendService send_service.SendService,
	configRepo chatwoot_repository.ChatwootConfigRepository,
	messageMapRepo chatwoot_repository.MessageMapRepository,
	loggerWrapper *logger_wrapper.LoggerManager,
) *WebhookHandler {
	return &WebhookHandler{instanceRepo: instanceRepo, sendService: sendService, configRepo: configRepo, messageMapRepo: messageMapRepo, loggerWrapper: loggerWrapper}
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

type outAttachment struct {
	DataURL     string `json:"data_url"`
	FileType    string `json:"file_type"`
	ContentType string `json:"content_type"`
	Extension   string `json:"extension"`
}

func fileTypeToMediaType(fileType string) string {
	switch fileType {
	case "image":
		return "image"
	case "video":
		return "video"
	case "audio":
		return "audio"
	default:
		return "document"
	}
}

// shouldForward decide se o evento deve virar uma mensagem no WhatsApp.
// Só encaminha mensagens outgoing, não-privadas, de message_created.
// shouldForward também devolve os anexos e os ids (mid, cid) usados para
// gravar o mapa wamid->(conversationID, messageID).
func shouldForward(body []byte) (sourceID, phone, text string, attachments []outAttachment, mid, cid int, ok bool) {
	var p struct {
		Event        string          `json:"event"`
		MessageType  string          `json:"message_type"`
		Private      bool            `json:"private"`
		Content      string          `json:"content"`
		Attachments  []outAttachment `json:"attachments"`
		ID           int             `json:"id"`
		Conversation struct {
			ID           int `json:"id"`
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
			Meta struct {
				Sender struct {
					PhoneNumber string `json:"phone_number"`
				} `json:"sender"`
			} `json:"meta"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", "", nil, 0, 0, false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", "", nil, 0, 0, false
	}
	// Precisa ter conteúdo OU anexo.
	if p.Content == "" && len(p.Attachments) == 0 {
		return "", "", "", nil, 0, 0, false
	}
	// Devolve os dois candidatos crus; quem resolve é o Handle, para poder
	// responder com erro visível quando nenhum serve — em vez de descartar em
	// silêncio uma mensagem que o agente acha que enviou.
	return p.Conversation.ContactInbox.SourceID, p.Conversation.Meta.Sender.PhoneNumber,
		p.Content, p.Attachments, p.ID, p.Conversation.ID, true
}

// resolveNumber decide para qual número enviar no WhatsApp.
//
// O source_id do contact_inbox só é o JID quando foi o nosso producer que criou
// o vínculo. Para inboxes Channel::Api o Chatwoot gera SecureRandom.uuid quando
// ninguém informa um source_id (contact_inbox_builder.rb), o que acontece toda
// vez que a conversa nasce do lado do Chatwoot. Mandar esse UUID como número
// fazia o whatsmeow queimar as 3 tentativas do sendTextWithRetry com sleeps, e
// o Chatwoot desistia antes disso com Net::ReadTimeout — um erro que não dizia
// nada sobre a causa.
//
// Então: usa o source_id se ele tiver cara de número; senão cai para o telefone
// do contato, que vem no payload em conversation.meta.sender.phone_number.
func resolveNumber(sourceID, phoneNumber string) (string, bool) {
	if n, ok := digitsOnly(sourceID); ok {
		return n, true
	}
	return digitsOnly(phoneNumber)
}

// digitsOnly extrai o número de um JID ("5511999@s.whatsapp.net"), de um E.164
// ("+5511999") ou de um número cru. Devolve false para qualquer coisa que não
// seja plausivelmente um telefone — UUID, e-mail, vazio.
func digitsOnly(raw string) (string, bool) {
	s := raw
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "+")
	if len(s) < 8 || len(s) > 20 {
		return "", false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return s, true
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

	sourceID, phone, text, attachments, mid, cid, ok := shouldForward(body)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	number, ok := resolveNumber(sourceID, phone)
	if !ok {
		// Falha rápido e explícita: sem isso o número inválido ia para o
		// whatsmeow, que gasta ~15s em retries, e o Chatwoot desistia antes com
		// Net::ReadTimeout — erro que não diz nada sobre a causa.
		msg := fmt.Sprintf("não foi possível determinar o número: source_id=%q e telefone do contato=%q não parecem um número de WhatsApp", sourceID, phone)
		h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: %s", instance.Id, msg)
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": msg})
		return
	}

	if len(attachments) == 0 {
		resp, err := h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance)
		if err != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		h.storeMap(resp, mid, cid, instance.Id)
		ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
		return
	}

	cfg, err := h.configRepo.Get()
	if err != nil || cfg == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "config do chatwoot ausente"})
		return
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
	sentCount := 0
	for i, att := range attachments {
		fileBytes, _, derr := client.DownloadFromChatwoot(att.DataURL)
		if derr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao baixar anexo: %v", instance.Id, derr)
			continue
		}
		caption := ""
		if i == 0 {
			caption = text // legenda vai no primeiro anexo
		}
		media := &send_service.MediaStruct{
			Number:   number,
			Type:     fileTypeToMediaType(att.FileType),
			Caption:  caption,
			Filename: attachmentFilename(att),
		}
		resp, serr := h.sendService.SendMediaFile(media, fileBytes, instance)
		if serr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao enviar mídia: %v", instance.Id, serr)
			continue
		}
		sentCount++
		h.storeMap(resp, mid, cid, instance.Id)
	}
	if sentCount == 0 {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "todos os anexos falharam"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
}

// storeMap grava a correlação wamid->(cid, mid) para permitir que um recibo de
// entrega/leitura do WhatsApp seja propagado como status da mensagem no
// Chatwoot. É um no-op seguro quando faltar qualquer uma das informações.
func (h *WebhookHandler) storeMap(resp *send_service.MessageSendStruct, mid, cid int, instanceID string) {
	if resp == nil || mid == 0 || cid == 0 {
		return
	}
	wamid := resp.Info.ID
	if wamid == "" {
		return
	}
	if err := h.messageMapRepo.Save(&chatwoot_model.MessageMap{
		Wamid: wamid, ConversationID: cid, MessageID: mid, InstanceID: instanceID,
	}); err != nil {
		h.loggerWrapper.GetLogger(instanceID).LogError("[%s] chatwoot: falha ao gravar message map: %v", instanceID, err)
	}
}

// attachmentFilename deriva o nome do arquivo a partir do DataURL, removendo
// qualquer query string (ex. "?disposition=attachment"). Se o resultado for
// vazio, cai para "file.<extension>" ou apenas "file".
func attachmentFilename(att outAttachment) string {
	name := att.DataURL[strings.LastIndexByte(att.DataURL, '/')+1:]
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	if name != "" {
		return name
	}
	if att.Extension != "" {
		return "file." + att.Extension
	}
	return "file"
}
