package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAuditLogIPBanRoutesListAndUnban(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	protection := service.NewLoginProtectionService(client)
	ctx := context.Background()
	for range 3 {
		_, _, err := protection.RecordFailure(ctx, "203.0.113.10")
		require.NoError(t, err)
	}
	h := &handler.Handlers{Admin: &handler.AdminHandlers{
		AuditLog: admin.NewAuditLogHandler(nil, nil, protection),
	}}
	request := func(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	// These routes inherit administrator authentication. Anonymous callers
	// cannot list or remove a ban.
	protectedRouter := gin.New()
	protected := protectedRouter.Group("/api/v1/admin")
	protected.Use(gin.HandlerFunc(servermiddleware.NewAdminAuthMiddleware(nil, nil, nil, nil)))
	registerAuditLogRoutes(protected, h, nil)
	require.Equal(t, http.StatusUnauthorized, request(protectedRouter, http.MethodGet, "/api/v1/admin/audit-logs/ip-bans", "").Code)
	require.Equal(t, http.StatusUnauthorized, request(protectedRouter, http.MethodPost, "/api/v1/admin/audit-logs/ip-bans/unban", `{"client_ip":"203.0.113.10"}`).Code)
	ban, err := protection.Check(ctx, "203.0.113.10")
	require.NoError(t, err)
	require.NotNil(t, ban)

	// Exercise handler routing after the caller has passed admin authentication.
	router := gin.New()
	registerAuditLogRoutes(router.Group("/api/v1/admin"), h, nil)
	w := request(router, http.MethodGet, "/api/v1/admin/audit-logs/ip-bans?page=1&page_size=5", "")
	require.Equal(t, http.StatusOK, w.Code)
	var listed struct {
		Data service.LoginIPBanList `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.EqualValues(t, 1, listed.Data.Total)
	require.Equal(t, 3, listed.Data.Threshold)
	require.Zero(t, listed.Data.BanDurationSeconds)
	require.Len(t, listed.Data.Items, 1)
	require.Equal(t, "203.0.113.10", listed.Data.Items[0].ClientIP)
	require.Nil(t, listed.Data.Items[0].BlockedUntil)
	for _, body := range []string{`{}`, `{"client_ip":"not-an-ip"}`, `{"client_ip":"fe80::1%eth0"}`} {
		require.Equal(t, http.StatusBadRequest, request(router, http.MethodPost, "/api/v1/admin/audit-logs/ip-bans/unban", body).Code)
	}
	w = request(router, http.MethodPost, "/api/v1/admin/audit-logs/ip-bans/unban", `{"client_ip":"::ffff:203.0.113.10"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"client_ip":"203.0.113.10"`)
	ban, err = protection.Check(ctx, "203.0.113.10")
	require.NoError(t, err)
	require.Nil(t, ban)
	ban, count, err := protection.RecordFailure(ctx, "203.0.113.10")
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)
}
