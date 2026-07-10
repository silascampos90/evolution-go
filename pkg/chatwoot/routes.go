package chatwoot_routes

import (
	chatwoot_handler "github.com/evolution-foundation/evolution-go/pkg/chatwoot/handler"
	"github.com/gin-gonic/gin"
)

// Register monta as rotas da integração Chatwoot.
// adminAuth é o middleware AuthAdmin (GlobalApiKey); o webhook receiver NÃO usa auth (valida HMAC).
func Register(eng *gin.Engine, admin *chatwoot_handler.AdminHandler, webhook *chatwoot_handler.WebhookHandler, adminAuth gin.HandlerFunc) {
	// UI (pública — a página pede o apikey e o guarda no browser)
	eng.GET("/chatwoot-admin", admin.ServeUI)

	// Webhook do Chatwoot -> evolution (sem apikey; autenticado por HMAC)
	eng.POST("/chatwoot/webhook/:instance", webhook.Handle)

	// API de gestão (protegida por AuthAdmin)
	api := eng.Group("/chatwoot")
	api.Use(adminAuth)
	{
		api.PUT("/config", admin.PutConfig)
		api.POST("/config/test", admin.TestConfig)
		api.GET("/links", admin.GetLinks)
		api.POST("/links", admin.PostLink)
	}
}
