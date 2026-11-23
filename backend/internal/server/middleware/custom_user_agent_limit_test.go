package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubUserAgentLimiter struct {
	calls []struct {
		userID   int64
		userRole string
		rawUA    string
	}
	err error
}

func (s *stubUserAgentLimiter) CheckAndRecord(_ context.Context, userID int64, userRole string, rawUA string) error {
	s.calls = append(s.calls, struct {
		userID   int64
		userRole string
		rawUA    string
	}{userID: userID, userRole: userRole, rawUA: rawUA})
	return s.err
}

type stubUALimitAPIKeyRepo struct {
	service.APIKeyRepository
	byKey map[string]*service.APIKey
}

func (r *stubUALimitAPIKeyRepo) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	if r != nil {
		if k, ok := r.byKey[key]; ok {
			return k, nil
		}
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r *stubUALimitAPIKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKey(ctx, key)
}

func (r *stubUALimitAPIKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

func (r *stubUALimitAPIKeyRepo) IncrementRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

func (r *stubUALimitAPIKeyRepo) ResetRateLimitWindows(context.Context, int64) error {
	return nil
}

func (r *stubUALimitAPIKeyRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return nil, nil
}

func newTestAPIKeyServiceForUALimit(apiKey *service.APIKey) *service.APIKeyService {
	return service.NewAPIKeyService(&stubUALimitAPIKeyRepo{
		byKey: map[string]*service.APIKey{apiKey.Key: apiKey},
	}, nil, nil, nil, nil, nil, &config.Config{})
}

func makeUALimitTestAPIKey() *service.APIKey {
	groupID := int64(1)
	return &service.APIKey{
		ID:      10,
		Key:     "sk-ua-limit-test",
		UserID:  42,
		Status:  service.StatusActive,
		GroupID: &groupID,
		Group: &service.Group{
			ID:     1,
			Name:   "g1",
			Status: service.StatusActive,
		},
		User: &service.User{
			ID:      42,
			Role:    service.RoleUser,
			Status:  service.StatusActive,
			Balance: 100,
		},
	}
}

func TestAPIKeyAuth_UserAgentLimitExceededAndSkipBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiKey := makeUALimitTestAPIKey()
	svc := newTestAPIKeyServiceForUALimit(apiKey)
	cfg := &config.Config{RunMode: config.RunModeStandard}

	limiter := &stubUserAgentLimiter{
		err: &service.UserAgentLimitExceededError{
			UserID:       42,
			ActiveCount:  5,
			MaxAllowed:   5,
			WindowDays:   30,
			NormalizedUA: "Codex Desktop",
		},
	}
	SetUserAgentLimiter(limiter)
	defer SetUserAgentLimiter(nil)

	r := gin.New()
	mw := apiKeyAuthWithSubscription(svc, nil, cfg)
	r.POST("/v1/responses", mw, func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/v1/usage", mw, func(c *gin.Context) { c.Status(http.StatusOK) })

	// Gateway request rejected with 403 and both OpenAI/Anthropic and top-level fields.
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.Key)
	req.Header.Set("User-Agent", "Codex Desktop/0.171.0-alpha.13")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "USER_AGENT_LIMIT_EXCEEDED", body["code"])
	require.Contains(t, body["message"], "User-Agent")
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "permission_error", errObj["type"])
	require.Equal(t, "USER_AGENT_LIMIT_EXCEEDED", errObj["code"])
	require.Len(t, limiter.calls, 1)
	require.Equal(t, "Codex Desktop/0.171.0-alpha.13", limiter.calls[0].rawUA)

	// /v1/usage skips billing & UA limit checks.
	usageReq := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	usageReq.Header.Set("Authorization", "Bearer "+apiKey.Key)
	usageReq.Header.Set("User-Agent", "Codex Desktop/0.171.0-alpha.13")
	usageW := httptest.NewRecorder()
	r.ServeHTTP(usageW, usageReq)
	require.Equal(t, http.StatusOK, usageW.Code)
	require.Len(t, limiter.calls, 1)

	// When limiter allows, normal requests pass.
	limiter.err = nil
	okReq := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	okReq.Header.Set("Authorization", "Bearer "+apiKey.Key)
	okReq.Header.Set("User-Agent", "Codex Desktop/0.169.1 (Mac OS 26.3.1)")
	okW := httptest.NewRecorder()
	r.ServeHTTP(okW, okReq)
	require.Equal(t, http.StatusOK, okW.Code)
	require.Len(t, limiter.calls, 2)
}

func TestAPIKeyAuthGoogle_UserAgentLimitExceeded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiKey := makeUALimitTestAPIKey()
	svc := newTestAPIKeyServiceForUALimit(apiKey)
	cfg := &config.Config{RunMode: config.RunModeStandard}

	limiter := &stubUserAgentLimiter{
		err: &service.UserAgentLimitExceededError{
			UserID:       42,
			ActiveCount:  5,
			MaxAllowed:   5,
			WindowDays:   30,
			NormalizedUA: "OpenAI/Python",
		},
	}
	SetUserAgentLimiter(limiter)
	defer SetUserAgentLimiter(nil)

	r := gin.New()
	r.POST("/v1beta/models/gemini:generateContent", APIKeyAuthWithSubscriptionGoogle(svc, nil, cfg), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:generateContent", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	req.Header.Set("User-Agent", "OpenAI/Python 3.51.0")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(http.StatusForbidden), errObj["code"])
	require.Equal(t, "PERMISSION_DENIED", errObj["status"])
	require.Contains(t, errObj["message"], "OpenAI/Python")
}
