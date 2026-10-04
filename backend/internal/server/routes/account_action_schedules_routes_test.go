package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountSchedulesRoutesRequireAdminProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, OpenAIOAuth: &adminhandler.OpenAIOAuthHandler{}}}
	chains := map[string][]string{}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		names := c.HandlerNames()
		chains[c.Request.Method+" "+c.FullPath()] = names[:len(names)-1]
		status := http.StatusForbidden
		if c.GetHeader("Authorization") == "" {
			status = http.StatusUnauthorized
		}
		servermiddleware.AbortWithError(c, status, "ADMIN_REQUIRED", "Administrator access required")
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, audit, stepUp, nil, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/admin/openai/accounts/1/reset-quota"},
		{http.MethodGet, "/api/v1/admin/accounts/1/action-schedules"},
		{http.MethodPost, "/api/v1/admin/accounts/1/action-schedules"},
		{http.MethodPut, "/api/v1/admin/accounts/1/action-schedules/2"},
		{http.MethodDelete, "/api/v1/admin/accounts/1/action-schedules/2"},
		{http.MethodGet, "/api/v1/admin/accounts/1/action-schedules/2/runs"},
	} {
		for _, authorization := range []string{"", "Bearer non-admin"} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", authorization)
			router.ServeHTTP(recorder, request)
			status := http.StatusForbidden
			if authorization == "" {
				status = http.StatusUnauthorized
			}
			require.Equal(t, status, recorder.Code, route.method+" "+route.path)
		}
	}
	baseline := chains["POST /api/v1/admin/openai/accounts/:id/reset-quota"]
	require.NotEmpty(t, baseline)
	for route, chain := range chains {
		require.Equal(t, baseline, chain, route)
	}
}
