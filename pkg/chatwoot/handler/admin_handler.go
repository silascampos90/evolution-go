package chatwoot_handler

import (
	"net/http"

	chatwoot_service "github.com/evolution-foundation/evolution-go/pkg/chatwoot/service"
	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	service *chatwoot_service.ChatwootService
}

func NewAdminHandler(service *chatwoot_service.ChatwootService) *AdminHandler {
	return &AdminHandler{service: service}
}

func (h *AdminHandler) PutConfig(ctx *gin.Context) {
	var body struct {
		BaseURL   string `json:"baseUrl"`
		APIToken  string `json:"apiToken"`
		AccountID string `json:"accountId"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.SaveConfig(body.BaseURL, body.APIToken, body.AccountID); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "saved"})
}

func (h *AdminHandler) TestConfig(ctx *gin.Context) {
	if err := h.service.TestConfig(); err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) GetLinks(ctx *gin.Context) {
	links, err := h.service.ListLinks()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": links})
}

func (h *AdminHandler) PostLink(ctx *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || body.Name == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	res, err := h.service.CreateLink(body.Name)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": res})
}
