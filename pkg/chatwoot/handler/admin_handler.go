package chatwoot_handler

import (
	"errors"
	"net/http"
	"net/url"

	chatwoot_service "github.com/evolution-foundation/evolution-go/pkg/chatwoot/service"
	chatwoot_ui "github.com/evolution-foundation/evolution-go/pkg/chatwoot/ui"
	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	service *chatwoot_service.ChatwootService
}

func NewAdminHandler(service *chatwoot_service.ChatwootService) *AdminHandler {
	return &AdminHandler{service: service}
}

func (h *AdminHandler) ServeUI(ctx *gin.Context) {
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", chatwoot_ui.IndexHTML)
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
	if body.BaseURL == "" || body.AccountID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "baseUrl e accountId são obrigatórios"})
		return
	}
	// O esquema é validado aqui, não só na tela: o baseUrl volta para o
	// navegador e vira o href do "Abrir no Chatwoot". Sem esta checagem um
	// "javascript:..." salvo no banco convidava o operador a clicar nele.
	parsed, err := url.Parse(body.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "baseUrl precisa ser uma URL http:// ou https:// completa"})
		return
	}
	// apiToken vazio = manter o que já está salvo (ver SaveConfig). Só é erro
	// de uso quando não existe config nenhuma para manter.
	if err := h.service.SaveConfig(body.BaseURL, body.APIToken, body.AccountID); err != nil {
		if errors.Is(err, chatwoot_service.ErrAPITokenRequired) {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
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
		if errors.Is(err, chatwoot_service.ErrLinkAlreadyExists) {
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *AdminHandler) GetConfig(ctx *gin.Context) {
	view, err := h.service.GetConfig()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": view})
}

func (h *AdminHandler) PostReconnect(ctx *gin.Context) {
	name := ctx.Param("instance")
	res, err := h.service.ReconnectLink(name)
	if err != nil {
		// Mesma forma do PostLink: "não existe" é 404, o resto é 500. Mapear
		// tudo para 400 fazia um Chatwoot fora do ar parecer erro de digitação
		// do operador para qualquer alerta baseado em log.
		if errors.Is(err, chatwoot_service.ErrLinkNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *AdminHandler) DeleteLink(ctx *gin.Context) {
	name := ctx.Param("instance")
	// Comparação estrita de propósito: apagar a inbox destrói o histórico de
	// conversas, então só o literal "true" opta por isso. Ausente, "", "false",
	// "1" e "TRUE" preservam a inbox.
	deleteInbox := ctx.Query("deleteInbox") == "true"
	if err := h.service.DeleteLink(name, deleteInbox); err != nil {
		if errors.Is(err, chatwoot_service.ErrLinkNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
