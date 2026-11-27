//go:build unit

package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionFromServiceAdmin_ExposesUserNotesOnlyForAdmin(t *testing.T) {
	now := time.Now().UTC()
	sub := &service.UserSubscription{
		ID:        10,
		UserID:    42,
		GroupID:   3,
		StartsAt:  now,
		ExpiresAt: now.Add(24 * time.Hour),
		Status:    service.SubscriptionStatusActive,
		Notes:     "sub-note",
		User: &service.User{
			ID:       42,
			Email:    "user@example.com",
			Username: "alice",
			Notes:    "VIP 客户备注",
			APIKeys:  []service.APIKey{{ID: 1, Key: "sk-should-not-nest"}},
		},
	}

	adminDTO := UserSubscriptionFromServiceAdmin(sub)
	adminJSON, err := json.Marshal(adminDTO)
	require.NoError(t, err)

	var adminMap map[string]any
	require.NoError(t, json.Unmarshal(adminJSON, &adminMap))
	require.Equal(t, "sub-note", adminMap["notes"])
	adminUser, ok := adminMap["user"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "VIP 客户备注", adminUser["notes"])
	require.NotContains(t, adminUser, "api_keys")
	require.NotContains(t, adminUser, "subscriptions")

	publicDTO := UserSubscriptionFromService(sub)
	publicJSON, err := json.Marshal(publicDTO)
	require.NoError(t, err)

	var publicMap map[string]any
	require.NoError(t, json.Unmarshal(publicJSON, &publicMap))
	publicUser, ok := publicMap["user"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, publicUser, "notes")
}
