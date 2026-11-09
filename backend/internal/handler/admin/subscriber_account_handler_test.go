package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubscriberAccountHandler_FiltersAndRedactsSensitiveFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	now := time.Now().UTC()
	notes := "secret internal note"
	rate := 1.5
	proxyID := int64(42)

	adminSvc.accounts = []service.Account{
		{
			ID:             101,
			Name:           "Shared-OpenAI-Master",
			Notes:          &notes,
			Platform:       service.PlatformOpenAI,
			Type:           service.AccountTypeOAuth,
			ProxyID:        &proxyID,
			Priority:       9,
			RateMultiplier: &rate,
			Concurrency:    5,
			Status:         service.StatusActive,
			ErrorMessage:   "upstream 401 token=sk-secret proxy=10.0.0.1",
			Schedulable:    true,
			Credentials: map[string]any{
				"email":              "owner@secret.com",
				"chatgpt_account_id": "acct_secret_123",
				"access_token":       "eyJ_secret_token",
				"plan_type":          "pro",
			},
			Extra: map[string]any{
				"subscriber_visible": true,
				"email_address":      "owner@secret.com",
				"privacy_mode":       "training_off",
				"upstream_billing_probe": map[string]any{
					"last_probed_at": "2026-11-09T00:00:00Z",
				},
			},
			GroupIDs: []int64{10, 20},
			Groups: []*service.Group{
				{ID: 10, Name: "VIP-Subscribed-Group", Platform: service.PlatformOpenAI, SubscriptionType: "subscription"},
				{ID: 20, Name: "Internal-Hidden-Group", Platform: service.PlatformOpenAI, SubscriptionType: "subscription"},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          102,
			Name:        "Hidden-OpenAI-Master",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Extra: map[string]any{
				"subscriber_visible": false,
			},
			GroupIDs: []int64{10},
			Groups: []*service.Group{
				{ID: 10, Name: "VIP-Subscribed-Group", Platform: service.PlatformOpenAI},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          103,
			Name:        "Other-Group-Visible-Account",
			Platform:    service.PlatformAnthropic,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Extra: map[string]any{
				"subscriber_visible": true,
			},
			GroupIDs: []int64{99},
			Groups: []*service.Group{
				{ID: 99, Name: "Other-Group", Platform: service.PlatformAnthropic},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	allowedGroups := map[int64]struct{}{10: {}}
	groupNames := map[int64]string{10: "VIP 订阅组"}
	router.GET("/api/v1/subscriptions/accounts", func(c *gin.Context) {
		handler.ListSubscriberAccounts(c, allowedGroups, groupNames, false)
	})
	router.POST("/api/v1/admin/accounts/:id/subscriber-visible", handler.SetSubscriberVisible)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/accounts?page=1&page_size=20", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var listResp struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Total int64            `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listResp))
	require.Equal(t, int64(1), listResp.Data.Total)
	require.Len(t, listResp.Data.Items, 1)

	item := listResp.Data.Items[0]
	require.Equal(t, float64(101), item["id"])
	require.Equal(t, "Shared-OpenAI-Master", item["name"])
	require.Equal(t, true, item["schedulable"])
	// Verify only subscribed group 10 is exposed, not internal group 20
	require.Equal(t, []any{float64(10)}, item["group_ids"])
	groups, ok := item["groups"].([]any)
	require.True(t, ok)
	require.Len(t, groups, 1)
	firstGroup := groups[0].(map[string]any)
	require.Equal(t, "VIP 订阅组", firstGroup["name"])

	// Verify sensitive fields are strictly omitted
	for _, forbiddenKey := range []string{
		"notes", "error_message", "proxy_id", "proxy", "priority",
		"rate_multiplier", "scheduler_score", "scheduler_scores",
		"parent_email", "parent_chatgpt_account_id",
	} {
		require.NotContains(t, item, forbiddenKey)
	}

	creds, ok := item["credentials"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pro", creds["plan_type"])
	require.NotContains(t, creds, "email")
	require.NotContains(t, creds, "chatgpt_account_id")
	require.NotContains(t, creds, "access_token")

	extra, ok := item["extra"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "training_off", extra["privacy_mode"])
	require.NotContains(t, extra, "email_address")
	require.NotContains(t, extra, "upstream_billing_probe")
	require.NotContains(t, extra, "subscriber_visible")

	// Test admin toggle endpoint
	toggleRec := httptest.NewRecorder()
	toggleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/101/subscriber-visible",
		bytes.NewBufferString(`{"subscriber_visible":true}`),
	)
	toggleReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(toggleRec, toggleReq)
	require.Equal(t, http.StatusOK, toggleRec.Code)
	require.Equal(t, 1, adminSvc.updateAccountExtraCalls)
}
