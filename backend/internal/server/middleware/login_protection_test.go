package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func loginProtectionTestRouter(t *testing.T) (*gin.Engine, *service.LoginProtectionService, *miniredis.Miniredis, *auditCaptureRepository) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	protection := service.NewLoginProtectionService(client)
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	t.Cleanup(audit.Stop)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	auth := router.Group("/api/v1/auth")
	auth.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)), NewLoginProtectionMiddleware(protection))
	handler := func(c *gin.Context) {
		switch c.Query("outcome") {
		case "success":
			MarkLoginSuccess(c)
			response.Success(c, gin.H{"access_token": "test"})
		case "redirect":
			MarkLoginSuccess(c)
			c.Redirect(http.StatusFound, "/auth/callback#access_token=test")
		case "2fa":
			response.Success(c, gin.H{"requires_2fa": true})
		case "operational":
			response.InternalError(c, "service unavailable")
		case "captcha":
			response.BadRequest(c, "invalid captcha")
		default:
			if !RecordLoginFailure(c, service.ErrInvalidCredentials) {
				response.ErrorFrom(c, service.ErrInvalidCredentials)
			}
		}
	}
	auth.POST("/login", handler)
	auth.POST("/login/2fa", handler)
	auth.POST("/passkey/login/finish", handler)
	auth.POST("/oauth/pending/bind-login", handler)
	auth.GET("/oauth/github/callback", handler)
	auth.POST("/logout", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router, protection, rdb, repo
}

func loginProtectionRequest(router http.Handler, method, path, remoteIP string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteIP + ":12345"
	// An untrusted caller cannot evade the counter using spoofed headers.
	req.Header.Set("X-Forwarded-For", "198.51.100.200")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestLoginProtectionThirdFailureBansAllLoginMethodsAndAudits(t *testing.T) {
	router, protection, _, repo := loginProtectionTestRouter(t)
	paths := []string{"/api/v1/auth/login?email=one", "/api/v1/auth/login/2fa", "/api/v1/auth/oauth/pending/bind-login?email=two"}
	for i, path := range paths {
		w := loginProtectionRequest(router, http.MethodPost, path, "203.0.113.10")
		if i < 2 {
			require.Equal(t, http.StatusUnauthorized, w.Code)
		} else {
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), "LOGIN_IP_BANNED")
		}
	}
	for _, path := range []string{"/api/v1/auth/login?outcome=success", "/api/v1/auth/passkey/login/finish?outcome=success"} {
		w := loginProtectionRequest(router, http.MethodPost, path, "203.0.113.10")
		require.Equal(t, http.StatusForbidden, w.Code)
	}
	w := loginProtectionRequest(router, http.MethodGet, "/api/v1/auth/oauth/github/callback?outcome=success", "203.0.113.10")
	require.Equal(t, http.StatusForbidden, w.Code)
	w = loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login?outcome=success", "203.0.113.11")
	require.Equal(t, http.StatusOK, w.Code)
	w = loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/logout", "203.0.113.10")
	require.Equal(t, http.StatusOK, w.Code)
	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		created, blockedGET := false, false
		for _, log := range repo.logs {
			if log.Action == service.AuditActionLoginIPBan {
				created = log.ClientIP == "203.0.113.10" && log.Extra["failure_count"] == 3
			}
			if log.Action == service.AuditActionLoginIPBlocked && log.Method == http.MethodGet {
				blockedGET = log.ClientIP == "203.0.113.10"
			}
		}
		return created && blockedGET
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, protection.Unban(context.Background(), "203.0.113.10"))
	w = loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login?outcome=success", "203.0.113.10")
	require.Equal(t, http.StatusOK, w.Code)
}

func TestLoginProtectionSuccessResetsButTwoFactorChallengeDoesNot(t *testing.T) {
	router, protection, _, _ := loginProtectionTestRouter(t)
	for _, outcome := range []string{"failure", "failure", "success", "failure", "captcha", "operational", "2fa", "failure"} {
		w := loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login?outcome="+outcome, "203.0.113.10")
		require.NotEqual(t, http.StatusForbidden, w.Code, outcome)
	}
	ban, err := protection.Check(context.Background(), "203.0.113.10")
	require.NoError(t, err)
	require.Nil(t, ban)
	w := loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login/2fa", "203.0.113.10")
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestLoginProtectionFailsClosedWhenRedisUnavailable(t *testing.T) {
	router, _, rdb, _ := loginProtectionTestRouter(t)
	rdb.Close()
	w := loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login?outcome=success", "203.0.113.10")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "LOGIN_PROTECTION_UNAVAILABLE")
}

func TestLoginProtectionCompletedOAuthRedirectResetsFailures(t *testing.T) {
	router, _, _, _ := loginProtectionTestRouter(t)
	for range 2 {
		w := loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login", "203.0.113.10")
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	w := loginProtectionRequest(router, http.MethodGet, "/api/v1/auth/oauth/github/callback?outcome=redirect", "203.0.113.10")
	require.Equal(t, http.StatusFound, w.Code)
	for range 2 {
		w = loginProtectionRequest(router, http.MethodPost, "/api/v1/auth/login", "203.0.113.10")
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}
