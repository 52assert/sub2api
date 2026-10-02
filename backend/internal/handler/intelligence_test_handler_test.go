//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligenceHandlerRepo struct{ record *service.IntelligenceTest }

func (r *intelligenceHandlerRepo) Create(_ context.Context, record *service.IntelligenceTest) error {
	record.ID = 1
	record.CreatedAt = time.Now()
	r.record = record
	return nil
}
func (*intelligenceHandlerRepo) ClaimNext(context.Context) (*service.IntelligenceTest, error) {
	return nil, nil
}
func (*intelligenceHandlerRepo) Finish(context.Context, *service.IntelligenceTest) error { return nil }
func (r *intelligenceHandlerRepo) List(context.Context) ([]*service.IntelligenceTest, error) {
	return []*service.IntelligenceTest{r.record}, nil
}
func (r *intelligenceHandlerRepo) GetByID(context.Context, int64) (*service.IntelligenceTest, error) {
	return r.record, nil
}
func (*intelligenceHandlerRepo) RecoverInterrupted(context.Context, time.Time) error { return nil }

type intelligenceHandlerAccounts struct{ service.AccountRepository }

func (intelligenceHandlerAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	return &service.Account{ID: 42, Name: "private-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "private-secret"}}, nil
}

func TestIntelligenceTestHandlerAcceptsBackgroundTaskAndSharesResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &intelligenceHandlerRepo{}
	accounts := intelligenceHandlerAccounts{}
	generator := service.NewAccountTestService(accounts, nil, nil, nil, nil, nil, nil, nil)
	svc := service.NewIntelligenceTestService(repo, accounts, generator)
	t.Cleanup(svc.Stop)
	h := NewIntelligenceTestHandler(svc)
	router := gin.New()
	router.POST("/admin/accounts/:id/intelligence-tests", h.Create)
	router.GET("/intelligence-tests", h.List)
	router.GET("/intelligence-tests/:id", h.GetByID)
	request := httptest.NewRequest(http.MethodPost, "/admin/accounts/42/intelligence-tests", strings.NewReader(`{"model":"gpt-5","reasoning_effort":"default"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code)
	require.Equal(t, "queued", repo.record.Status)
	require.Equal(t, service.DefaultIntelligenceTestPrompt, repo.record.Prompt)
	for _, path := range []string{"/intelligence-tests", "/intelligence-tests/1"} {
		response = httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code)
		require.NotContains(t, response.Body.String(), "account_id")
		require.NotContains(t, response.Body.String(), "private-secret")
		require.NotContains(t, response.Body.String(), "private-account")
		var envelope struct {
			Code int `json:"code"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.Zero(t, envelope.Code)
	}
}
