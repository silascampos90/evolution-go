package chatwoot_handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	chatwoot_client "github.com/evolution-foundation/evolution-go/pkg/chatwoot/client"
	chatwoot_repository "github.com/evolution-foundation/evolution-go/pkg/chatwoot/repository"
	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	send_service "github.com/evolution-foundation/evolution-go/pkg/sendMessage/service"
	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	instanceRepo  instance_repository.InstanceRepository
	sendService   send_service.SendService
	configRepo    chatwoot_repository.ChatwootConfigRepository
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewWebhookHandler(
	instanceRepo instance_repository.InstanceRepository,
	sendService send_service.SendService,
	configRepo chatwoot_repository.ChatwootConfigRepository,
	loggerWrapper *logger_wrapper.LoggerManager,
) *WebhookHandler {
	return &WebhookHandler{instanceRepo: instanceRepo, sendService: sendService, configRepo: configRepo, loggerWrapper: loggerWrapper}
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
// shouldForward agora também devolve os anexos.
func shouldForward(body []byte) (jid, text string, attachments []outAttachment, ok bool) {
	var p struct {
		Event        string          `json:"event"`
		MessageType  string          `json:"message_type"`
		Private      bool            `json:"private"`
		Content      string          `json:"content"`
		Attachments  []outAttachment `json:"attachments"`
		Conversation struct {
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", nil, false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", nil, false
	}
	if p.Conversation.ContactInbox.SourceID == "" {
		return "", "", nil, false
	}
	// Precisa ter conteúdo OU anexo.
	if p.Content == "" && len(p.Attachments) == 0 {
		return "", "", nil, false
	}
	return p.Conversation.ContactInbox.SourceID, p.Content, p.Attachments, true
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

	jid, text, attachments, ok := shouldForward(body)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	number := jid
	if i := strings.IndexByte(number, '@'); i >= 0 {
		number = number[:i]
	}

	if len(attachments) == 0 {
		if _, err := h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance); err != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
		return
	}

	cfg, err := h.configRepo.Get()
	if err != nil || cfg == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "config do chatwoot ausente"})
		return
	}
	client := chatwoot_client.NewClient(cfg.BaseURL, cfg.APIToken, cfg.AccountID)
	for i, att := range attachments {
		fileBytes, ct, derr := client.DownloadBytes(att.DataURL)
		if derr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao baixar anexo: %v", instance.Id, derr)
			continue
		}
		if ct == "" {
			ct = att.ContentType
		}
		caption := ""
		if i == 0 {
			caption = text // legenda vai no primeiro anexo
		}
		filename := att.DataURL[strings.LastIndexByte(att.DataURL, '/')+1:]
		media := &send_service.MediaStruct{
			Number:   number,
			Type:     fileTypeToMediaType(att.FileType),
			Caption:  caption,
			Filename: filename,
		}
		if _, serr := h.sendService.SendMediaFile(media, fileBytes, instance); serr != nil {
			h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa: falha ao enviar mídia: %v", instance.Id, serr)
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
}
