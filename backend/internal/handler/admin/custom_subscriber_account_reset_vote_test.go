package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubSubscriberResetVoteReader struct {
	lastUserID     int64
	lastAccountIDs []int64
	states         map[int64]*service.SubscriberResetVoteState
	err            error
}

func (s *stubSubscriberResetVoteReader) GetStatesForAccounts(
	_ context.Context,
	userID int64,
	accounts []*service.Account,
) (map[int64]*service.SubscriberResetVoteState, error) {
	s.lastUserID = userID
	s.lastAccountIDs = make([]int64, 0, len(accounts))
	for _, a := range accounts {
		if a != nil {
			s.lastAccountIDs = append(s.lastAccountIDs, a.ID)
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.states, nil
}

func TestSubscriberAccountHandler_AttachesResetVoteStates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	adminSvc.accounts = []service.Account{
		{
			ID:          101,
			Name:        "Shared-OpenAI",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Extra:       map[string]any{"subscriber_visible": true},
			GroupIDs:    []int64{10},
			Groups:      []*service.Group{{ID: 10, Name: "SubGroup", Platform: service.PlatformOpenAI, SubscriptionType: "subscription"}},
		},
	}

	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	reader := &stubSubscriberResetVoteReader{
		states: map[int64]*service.SubscriberResetVoteState{
			101: {
				Supported:   true,
				Eligible:    true,
				CanVote:     true,
				Status:      service.SubscriberResetVoteStatusOpen,
				VoteCount:   2,
				Threshold:   3,
				CanInitiate: false,
			},
		},
	}
	h.subscriberResetVotes = reader

	router := gin.New()
	router.GET("/accounts", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 55})
		h.ListSubscriberAccounts(c, map[int64]struct{}{10: {}}, map[int64]string{10: "SubGroup"}, false)
	})

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(55), reader.lastUserID)
	require.Equal(t, []int64{101}, reader.lastAccountIDs)

	var body struct {
		Data struct {
			Items []SubscriberAccountItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 1)
	require.NotNil(t, body.Data.Items[0].ResetVote)
	require.Equal(t, 2, body.Data.Items[0].ResetVote.VoteCount)
	require.True(t, body.Data.Items[0].ResetVote.CanVote)

	// Vote reader failure must not break account listing.
	reader.err = errors.New("vote db down")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/accounts", nil))
	require.Equal(t, http.StatusOK, w2.Code)
}
