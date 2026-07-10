package chatwoot_handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	instance_repository "github.com/evolution-foundation/evolution-go/pkg/instance/repository"
	logger_wrapper "github.com/evolution-foundation/evolution-go/pkg/logger"
	send_service "github.com/evolution-foundation/evolution-go/pkg/sendMessage/service"
	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	instanceRepo  instance_repository.InstanceRepository
	sendService   send_service.SendService
	loggerWrapper *logger_wrapper.LoggerManager
}

func NewWebhookHandler(
	instanceRepo instance_repository.InstanceRepository,
	sendService send_service.SendService,
	loggerWrapper *logger_wrapper.LoggerManager,
) *WebhookHandler {
	return &WebhookHandler{instanceRepo: instanceRepo, sendService: sendService, loggerWrapper: loggerWrapper}
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

// shouldForward decide se o evento deve virar uma mensagem no WhatsApp.
// Só encaminha mensagens outgoing, não-privadas, de message_created.
func shouldForward(body []byte) (jid, text string, ok bool) {
	var p struct {
		Event        string `json:"event"`
		MessageType  string `json:"message_type"`
		Private      bool   `json:"private"`
		Content      string `json:"content"`
		Conversation struct {
			ContactInbox struct {
				SourceID string `json:"source_id"`
			} `json:"contact_inbox"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", false
	}
	if p.Event != "message_created" || p.MessageType != "outgoing" || p.Private {
		return "", "", false
	}
	if p.Content == "" || p.Conversation.ContactInbox.SourceID == "" {
		return "", "", false
	}
	return p.Conversation.ContactInbox.SourceID, p.Content, true
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

	jid, text, ok := shouldForward(body)
	if !ok {
		ctx.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	number := jid
	if i := strings.IndexByte(number, '@'); i >= 0 {
		number = number[:i]
	}

	_, err = h.sendService.SendText(&send_service.TextStruct{Number: number, Text: text}, instance)
	if err != nil {
		h.loggerWrapper.GetLogger(instance.Id).LogError("[%s] chatwoot->wa send failed: %v", instance.Id, err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "sent"})
}
