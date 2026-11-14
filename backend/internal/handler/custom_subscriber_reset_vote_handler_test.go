package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubSubscriberResetVoteManager struct {
	lastUserID    int64
	lastAccountID int64
	res           *service.SubscriberResetVoteCastResult
	err           error
}

func (m *stubSubscriberResetVoteManager) CastVote(_ context.Context, userID, accountID int64) (*service.SubscriberResetVoteCastResult, error) {
	m.lastUserID = userID
	m.lastAccountID = accountID
	return m.res, m.err
}

func TestSubscriptionHandler_CastSubscriberAccountResetVote(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewSubscriptionHandler(nil)
		router := gin.New()
		router.POST("/accounts/:id/reset-votes", h.CastSubscriberAccountResetVote)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/10/reset-votes", nil))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("rejects invalid account id", func(t *testing.T) {
		h := NewSubscriptionHandler(nil)
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		})
		router.POST("/accounts/:id/reset-votes", h.CastSubscriberAccountResetVote)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/abc/reset-votes", nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("propagates 403 for non-subscription member", func(t *testing.T) {
		h := NewSubscriptionHandler(nil)
		h.SetSubscriberResetVoteManager(&stubSubscriberResetVoteManager{
			err: infraerrors.Forbidden("RESET_VOTE_SUBSCRIPTION_REQUIRED", "Only active members of this account's subscription groups can vote to reset"),
		})
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
		})
		router.POST("/accounts/:id/reset-votes", h.CastSubscriberAccountResetVote)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/10/reset-votes", nil))
		require.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("propagates 409 for duplicate vote or cooldown", func(t *testing.T) {
		h := NewSubscriptionHandler(nil)
		h.SetSubscriberResetVoteManager(&stubSubscriberResetVoteManager{
			err: infraerrors.Conflict("RESET_VOTE_ALREADY_VOTED", "You have already voted in this 24-hour reset round"),
		})
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		})
		router.POST("/accounts/:id/reset-votes", h.CastSubscriberAccountResetVote)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/10/reset-votes", nil))
		require.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("returns vote result on success", func(t *testing.T) {
		mgr := &stubSubscriberResetVoteManager{
			res: &service.SubscriberResetVoteCastResult{
				VoteRecorded:   true,
				TriggeredReset: true,
				ResetSucceeded: true,
				State: &service.SubscriberResetVoteState{
					Supported:              true,
					Eligible:               true,
					HasVoted:               true,
					Status:                 service.SubscriberResetVoteStatusSucceeded,
					VoteCount:              3,
					Threshold:              3,
					SubscriptionResetCount: 4,
				},
			},
		}
		h := NewSubscriptionHandler(nil)
		h.SetSubscriberResetVoteManager(mgr)
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		})
		router.POST("/accounts/:id/reset-votes", h.CastSubscriberAccountResetVote)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/10/reset-votes", nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, int64(7), mgr.lastUserID)
		require.Equal(t, int64(10), mgr.lastAccountID)

		var body struct {
			Data service.SubscriberResetVoteCastResult `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.True(t, body.Data.VoteRecorded)
		require.True(t, body.Data.TriggeredReset)
		require.True(t, body.Data.ResetSucceeded)
		require.Equal(t, 4, body.Data.State.SubscriptionResetCount)
	})

	t.Run("refresh reset credits rejects invalid account id", func(t *testing.T) {
		h := NewSubscriptionHandler(nil)
		router := gin.New()
		router.POST("/accounts/:id/reset-credits/refresh", h.RefreshSubscriberAccountResetCredits)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/not-an-id/reset-credits/refresh", nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	})
}
