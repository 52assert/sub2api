package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionAdjustmentAcceptsExactExpirationAndLegacyDays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })
	originalExpiry := time.Now().UTC().AddDate(0, 0, 60)
	target := time.Now().In(time.FixedZone("UTC+8", 8*60*60)).AddDate(0, 0, 30).Truncate(time.Millisecond)
	for _, tc := range []struct {
		name string
		body string
		want time.Time
	}{
		{"absolute with timezone and milliseconds", `{"expires_at":"` + target.Format(time.RFC3339Nano) + `"}`, target.UTC()},
		{"legacy days", `{"days":30}`, originalExpiry.AddDate(0, 0, 30)},
		{"legacy shortening", `{"days":-7}`, originalExpiry.AddDate(0, 0, -7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &bulkActionHandlerSubscriptionRepo{sub: &service.UserSubscription{ID: 1, UserID: 2, GroupID: 3, ExpiresAt: originalExpiry, Status: service.SubscriptionStatusActive}}
			svc := service.NewSubscriptionService(nil, repo, nil, nil, nil)
			t.Cleanup(svc.Stop)
			router := gin.New()
			router.POST("/api/v1/admin/subscriptions/:id/extend", NewSubscriptionHandler(svc).Extend)

			response := bulkActionHandlerRequest(router, "/api/v1/admin/subscriptions/1/extend", tc.body, "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, 1, repo.extendCalls)
			require.True(t, tc.want.Equal(repo.sub.ExpiresAt))
			var result struct {
				Data struct {
					ExpiresAt time.Time `json:"expires_at"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, tc.want.Equal(result.Data.ExpiresAt))
		})
	}
}

func TestSubscriptionAdjustmentRejectsInvalidInputWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })
	originalExpiry := time.Now().UTC().AddDate(0, 0, 60)
	for _, body := range []string{
		`{}`, `{"days":null}`, `{"days":0}`, `{"days":36501}`, `{"days":-36501}`, `{"days":"30"}`,
		`{"expires_at":null}`, `{"expires_at":""}`, `{"expires_at":123}`,
		`{"days":30,"expires_at":"2099-01-01T00:00:00Z"}`,
		`{"expires_at":"2099-01-01"}`, `{"expires_at":"2099-01-01T00:00:00"}`,
		`{"expires_at":"2099-02-29T00:00:00Z"}`, `{"expires_at":"2099-01-01T1:00:00Z"}`,
		`{"expires_at":"2099-01-01T00:00:00+24:00"}`, `{"expires_at":"2099-01-01T00:00:00+01:60"}`,
		`{"expires_at":"2099-01-01T00:00:00,123Z"}`, `{"expires_at":"2100-01-01T00:00:00Z"}`,
		`{"expires_at":"2000-01-01T00:00:00Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			repo := &bulkActionHandlerSubscriptionRepo{sub: &service.UserSubscription{ID: 1, ExpiresAt: originalExpiry, Status: service.SubscriptionStatusActive}}
			svc := service.NewSubscriptionService(nil, repo, nil, nil, nil)
			t.Cleanup(svc.Stop)
			router := gin.New()
			router.POST("/api/v1/admin/subscriptions/:id/extend", NewSubscriptionHandler(svc).Extend)

			response := bulkActionHandlerRequest(router, "/api/v1/admin/subscriptions/1/extend", body, "")

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Zero(t, repo.extendCalls)
			require.Equal(t, originalExpiry, repo.sub.ExpiresAt)
		})
	}
}

func TestSubscriptionAdjustmentAbsoluteExpiryRetainsIdempotency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(newMemoryIdempotencyRepoStub(), cfg))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })
	target := time.Now().UTC().AddDate(0, 0, 30).Truncate(time.Millisecond)
	repo := &bulkActionHandlerSubscriptionRepo{sub: &service.UserSubscription{ID: 1, ExpiresAt: time.Now().AddDate(0, 0, 10), Status: service.SubscriptionStatusActive}}
	svc := service.NewSubscriptionService(nil, repo, nil, nil, nil)
	t.Cleanup(svc.Stop)
	router := gin.New()
	router.POST("/api/v1/admin/subscriptions/:id/extend", NewSubscriptionHandler(svc).Extend)
	path := "/api/v1/admin/subscriptions/1/extend"
	body := `{"expires_at":"` + target.Format(time.RFC3339Nano) + `"}`

	first := bulkActionHandlerRequest(router, path, body, "set-expiration-once")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	replayed := bulkActionHandlerRequest(router, path, body, "set-expiration-once")
	require.Equal(t, http.StatusOK, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, repo.extendCalls)
	require.JSONEq(t, first.Body.String(), replayed.Body.String())
	conflict := bulkActionHandlerRequest(router, path, `{"days":30}`, "set-expiration-once")
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Equal(t, 1, repo.extendCalls)
}
