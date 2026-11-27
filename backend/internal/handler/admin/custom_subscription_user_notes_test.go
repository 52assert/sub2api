//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type subscriptionUserNotesRepo struct {
	service.UserSubscriptionRepository
}

func (*subscriptionUserNotesRepo) GetByID(context.Context, int64) (*service.UserSubscription, error) {
	return &service.UserSubscription{
		ID:            1,
		UserID:        2,
		GroupID:       3,
		DailyUsageUSD: 4,
		ExpiresAt:     time.Now().Add(time.Hour),
		User: &service.User{
			ID:       2,
			Email:    "user@example.com",
			Username: "alice",
			Notes:    "内部用户备注",
		},
		Group: &service.Group{ID: 3, Name: "group"},
	}, nil
}

func TestAdminSubscriptionResponse_ExposesUserNotesOnlyToFullAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := service.NewSubscriptionService(nil, &subscriptionUserNotesRepo{}, nil, nil, nil)
	defer s.Stop()
	h := NewSubscriptionHandler(s)

	// Full admin sees user.notes
	wFull := httptest.NewRecorder()
	cFull, _ := gin.CreateTestContext(wFull)
	cFull.Request = httptest.NewRequest("GET", "/subscriptions/1", nil)
	cFull.Params = gin.Params{{Key: "id", Value: "1"}}
	h.GetByID(cFull)
	require.Equal(t, 200, wFull.Code)

	var fullResp struct {
		Data struct {
			User *struct {
				Notes string `json:"notes"`
			} `json:"user"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wFull.Body.Bytes(), &fullResp))
	require.NotNil(t, fullResp.Data.User)
	require.Equal(t, "内部用户备注", fullResp.Data.User.Notes)

	// Scoped admin with subscriptions.read still does not see user or user.notes
	wScoped := httptest.NewRecorder()
	cScoped, _ := gin.CreateTestContext(wScoped)
	cScoped.Request = httptest.NewRequest("GET", "/subscriptions/1", nil)
	cScoped.Request = cScoped.Request.WithContext(adminaccess.WithPrincipal(
		cScoped.Request.Context(),
		&adminaccess.Principal{Policy: adminaccess.Policy{GlobalPermissions: []string{"subscriptions.read"}}},
	))
	cScoped.Params = gin.Params{{Key: "id", Value: "1"}}
	h.GetByID(cScoped)
	require.Equal(t, 200, wScoped.Code)
	require.NotContains(t, wScoped.Body.String(), "内部用户备注")
	require.NotContains(t, wScoped.Body.String(), `"user"`)
}
