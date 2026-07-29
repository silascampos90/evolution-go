package chatwoot_routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	chatwoot_handler "github.com/evolution-foundation/evolution-go/pkg/chatwoot/handler"
	"github.com/gin-gonic/gin"
)

// denyAll simula o AuthAdmin rejeitando uma requisição sem apikey válida.
func denyAll(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusUnauthorized)
}

// TestRegister_RoutesExistAndAreAdminGuarded garante duas coisas: que o Register
// não entra em panic por conflito de wildcard no Gin (o que derrubaria o binário
// no boot), e que as rotas novas ficam atrás do middleware de admin.
func TestRegister_RoutesExistAndAreAdminGuarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng := gin.New()
	Register(eng, chatwoot_handler.NewAdminHandler(nil), nil, denyAll)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/chatwoot/config"},
		{http.MethodPut, "/chatwoot/config"},
		{http.MethodPost, "/chatwoot/config/test"},
		{http.MethodGet, "/chatwoot/links"},
		{http.MethodPost, "/chatwoot/links"},
		{http.MethodPost, "/chatwoot/links/vendas/reconnect"},
		{http.MethodDelete, "/chatwoot/links/vendas"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		eng.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s não está registrada", c.method, c.path)
			continue
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s deveria ser barrada pelo AuthAdmin, got %d", c.method, c.path, rec.Code)
		}
	}
}
