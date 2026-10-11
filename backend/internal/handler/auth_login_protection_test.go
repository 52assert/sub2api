//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newProtectedLoginRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(middleware2.NewLoginProtectionMiddleware(service.NewLoginProtectionService(repository.NewLoginProtectionCache(client))))
	return router
}

func protectedLoginRequest(router *gin.Engine, path, body, clientIP string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = clientIP + ":12345"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestPasswordLoginProtectionCountsFailuresAndClearsAfterFullLogin(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	passwordHash, err := handler.authService.HashPassword("correct-password")
	require.NoError(t, err)
	_, err = client.User.Create().SetEmail("owner@example.com").
		SetPasswordHash(passwordHash).SetStatus(service.StatusActive).Save(context.Background())
	require.NoError(t, err)
	router := newProtectedLoginRouter(t)
	router.POST("/api/v1/auth/login", handler.Login)
	const clientIP = "203.0.113.10"
	const wrong = `{"email":"owner@example.com","password":"wrong"}`
	const correct = `{"email":"owner@example.com","password":"correct-password"}`

	// Invalid request bodies are not authentication attempts.
	for range 3 {
		require.Equal(t, http.StatusBadRequest, protectedLoginRequest(router, "/api/v1/auth/login", `{}`, clientIP).Code)
	}
	for range 2 {
		require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/login", wrong, clientIP).Code)
	}
	require.Equal(t, http.StatusOK, protectedLoginRequest(router, "/api/v1/auth/login", correct, clientIP).Code)
	for range 2 {
		require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/login", wrong, clientIP).Code)
	}
	third := protectedLoginRequest(router, "/api/v1/auth/login", wrong, clientIP)
	require.Equal(t, http.StatusForbidden, third.Code)
	require.Contains(t, third.Body.String(), "LOGIN_IP_BANNED")
	require.Equal(t, http.StatusForbidden, protectedLoginRequest(router, "/api/v1/auth/login", correct, clientIP).Code)
	require.Equal(t, http.StatusOK, protectedLoginRequest(router, "/api/v1/auth/login", correct, "203.0.113.11").Code)
}

type loginProtectionUserErrorRepo struct{ service.UserRepository }

func (*loginProtectionUserErrorRepo) GetByEmail(context.Context, string) (*service.User, error) {
	return nil, errors.New("database unavailable")
}

func TestPasswordLoginProtectionDoesNotCountServiceFailures(t *testing.T) {
	handler, _ := newOAuthPendingFlowTestHandler(t, false)
	router := newProtectedLoginRouter(t)
	router.POST("/api/v1/auth/login", handler.Login)
	const body = `{"email":"missing@example.com","password":"wrong"}`
	const clientIP = "203.0.113.12"
	for range 2 {
		require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/login", body, clientIP).Code)
	}
	workingService := handler.authService
	handler.authService = service.NewAuthService(nil, &loginProtectionUserErrorRepo{}, nil, nil,
		&config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
	for range 3 {
		require.Equal(t, http.StatusServiceUnavailable, protectedLoginRequest(router, "/api/v1/auth/login", body, clientIP).Code)
	}
	handler.authService = workingService
	third := protectedLoginRequest(router, "/api/v1/auth/login", body, clientIP)
	require.Equal(t, http.StatusForbidden, third.Code)
	require.Contains(t, third.Body.String(), "LOGIN_IP_BANNED")
}

func TestLoginProtectionDoesNotClearFailuresForTwoFactorChallenge(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{service.SettingKeyTotpEnabled: "true"},
		totpCache:     &oauthPendingFlowTotpCacheStub{},
	})
	const secret = "JBSWY3DPEHPK3PXP"
	passwordHash, err := handler.authService.HashPassword("correct-password")
	require.NoError(t, err)
	_, err = client.User.Create().SetEmail("owner@example.com").SetPasswordHash(passwordHash).
		SetStatus(service.StatusActive).SetTotpEnabled(true).SetTotpSecretEncrypted(secret).
		Save(context.Background())
	require.NoError(t, err)
	router := newProtectedLoginRouter(t)
	router.POST("/api/v1/auth/login", handler.Login)
	router.POST("/api/v1/auth/login/2fa", handler.Login2FA)
	const clientIP = "203.0.113.13"
	const passwordBody = `{"email":"owner@example.com","password":"correct-password"}`
	wrongCode := "000000"
	if totp.Validate(wrongCode, secret) {
		wrongCode = "111111"
	}
	require.False(t, totp.Validate(wrongCode, secret))

	require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/login",
		`{"email":"owner@example.com","password":"wrong"}`, clientIP).Code)
	for attempt := 2; attempt <= 3; attempt++ {
		challenge := protectedLoginRequest(router, "/api/v1/auth/login", passwordBody, clientIP)
		require.Equal(t, http.StatusOK, challenge.Code)
		data := decodeJSONResponseData(t, challenge)
		require.Equal(t, true, data["requires_2fa"])
		body := fmt.Sprintf(`{"temp_token":%q,"totp_code":%q}`, data["temp_token"], wrongCode)
		result := protectedLoginRequest(router, "/api/v1/auth/login/2fa", body, clientIP)
		if attempt == 2 {
			require.Equal(t, http.StatusBadRequest, result.Code)
			require.Contains(t, result.Body.String(), "TOTP_INVALID_CODE")
		} else {
			require.Equal(t, http.StatusForbidden, result.Code)
			require.Contains(t, result.Body.String(), "LOGIN_IP_BANNED")
		}
	}
}

func TestOAuthBindLoginSharesFailureCountWithPasswordLogin(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	passwordHash, err := handler.authService.HashPassword("correct-password")
	require.NoError(t, err)
	user, err := client.User.Create().SetEmail("owner@example.com").
		SetPasswordHash(passwordHash).SetStatus(service.StatusActive).Save(context.Background())
	require.NoError(t, err)
	session, err := client.PendingAuthSession.Create().SetSessionToken("login-protection-session").
		SetIntent("adopt_existing_user_by_email").SetProviderType("oidc").SetProviderKey("https://issuer.example").
		SetProviderSubject("login-protection-subject").SetTargetUserID(user.ID).
		SetBrowserSessionKey("login-protection-browser").SetExpiresAt(time.Now().UTC().Add(time.Minute)).
		Save(context.Background())
	require.NoError(t, err)
	router := newProtectedLoginRouter(t)
	router.POST("/api/v1/auth/login", handler.Login)
	router.POST("/api/v1/auth/oauth/oidc/bind-login", handler.BindOIDCOAuthLogin)
	const clientIP = "203.0.113.14"
	const body = `{"email":"owner@example.com","password":"wrong"}`
	cookies := []*http.Cookie{
		encodedCookie(oauthPendingSessionCookieName, session.SessionToken),
		encodedCookie(oauthPendingBrowserCookieName, session.BrowserSessionKey),
	}
	require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/login", body, clientIP).Code)
	require.Equal(t, http.StatusUnauthorized, protectedLoginRequest(router, "/api/v1/auth/oauth/oidc/bind-login", body, clientIP, cookies...).Code)
	third := protectedLoginRequest(router, "/api/v1/auth/oauth/oidc/bind-login", body, clientIP, cookies...)
	require.Equal(t, http.StatusForbidden, third.Code)
	require.Contains(t, third.Body.String(), "LOGIN_IP_BANNED")
	stored, err := client.PendingAuthSession.Get(context.Background(), session.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ConsumedAt)
}

type passkeyFinishLoginSessionStub struct {
	service.PasskeySessionStore
	session *service.PasskeySession
}

func (s *passkeyFinishLoginSessionStub) Consume(context.Context, string) (*service.PasskeySession, error) {
	return s.session, nil
}

func TestPasskeyFinishLoginProtectionBansFailedAssertionsButNotExpiredSessions(t *testing.T) {
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "sub2api.example.com",
		RPOrigins:     []string{"https://sub2api.example.com"},
	}}
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: "true"}, cfg)
	sessions := &passkeyFinishLoginSessionStub{}
	passkeys, err := service.NewPasskeyService(cfg, nil, sessions, nil)
	require.NoError(t, err)
	handler := NewPasskeyHandler(passkeys, nil, settings)
	router := newProtectedLoginRouter(t)
	router.POST("/api/v1/auth/passkey/login/finish", handler.FinishLogin)
	const clientIP = "203.0.113.15"
	const body = `{"session_token":"test-session","credential":{}}`

	for range 3 {
		result := protectedLoginRequest(router, "/api/v1/auth/passkey/login/finish", body, clientIP)
		require.Equal(t, http.StatusBadRequest, result.Code)
		require.Contains(t, result.Body.String(), "PASSKEY_SESSION_INVALID")
	}
	sessions.session = &service.PasskeySession{Kind: "login"}
	for range 2 {
		result := protectedLoginRequest(router, "/api/v1/auth/passkey/login/finish", body, clientIP)
		require.Equal(t, http.StatusUnauthorized, result.Code)
		require.Contains(t, result.Body.String(), "PASSKEY_VERIFICATION_FAILED")
	}
	third := protectedLoginRequest(router, "/api/v1/auth/passkey/login/finish", body, clientIP)
	require.Equal(t, http.StatusForbidden, third.Code)
	require.Contains(t, third.Body.String(), "LOGIN_IP_BANNED")
}
