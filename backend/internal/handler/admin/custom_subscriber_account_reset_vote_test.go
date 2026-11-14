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
	refreshResult  *service.SubscriberAccountResetCreditsResult
	refreshErr     error
	adminDetails   *service.AdminAccountResetVoteDetails
	adminErr       error
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

func (s *stubSubscriberResetVoteReader) RefreshAccountResetCredits(_ context.Context, accountID int64) (*service.SubscriberAccountResetCreditsResult, error) {
	s.lastAccountIDs = []int64{accountID}
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return s.refreshResult, nil
}

func (s *stubSubscriberResetVoteReader) GetAdminAccountVoteDetails(_ context.Context, accountID int64) (*service.AdminAccountResetVoteDetails, error) {
	s.lastAccountIDs = []int64{accountID}
	if s.adminErr != nil {
		return nil, s.adminErr
	}
	return s.adminDetails, nil
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

type resetVoteAdminServiceStub struct {
	*stubAdminService
}

func (s *resetVoteAdminServiceStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	out := make([]*service.Account, 0, len(ids))
	for i := range s.accounts {
		if _, ok := wanted[s.accounts[i].ID]; ok {
			acc := s.accounts[i]
			out = append(out, &acc)
		}
	}
	return out, nil
}

func TestSubscriberAccountHandler_RefreshResetCreditsAndAdminVoteDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseAdminSvc := newStubAdminService()
	baseAdminSvc.accounts = []service.Account{
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
	adminSvc := &resetVoteAdminServiceStub{stubAdminService: baseAdminSvc}

	reader := &stubSubscriberResetVoteReader{
		refreshResult: &service.SubscriberAccountResetCreditsResult{
			FetchedAt:      1770000000,
			CachePersisted: true,
			RateLimitResetCredits: &service.OpenAIRateLimitResetCredits{
				AvailableCount: 2,
				Credits:        []service.OpenAIRateLimitResetCreditDetail{{ExpiresAt: "2026-11-20T00:00:00Z"}},
			},
		},
		adminDetails: &service.AdminAccountResetVoteDetails{
			AccountID:     101,
			AccountName:   "Shared-OpenAI",
			Supported:     true,
			Status:        service.SubscriberResetVoteStatusOpen,
			VoteCount:     1,
			Threshold:     3,
			EligibleCount: 2,
			Voters:        []service.SubscriberResetVoteParticipant{{UserID: 7, Username: "alice"}},
			NonVoters:     []service.SubscriberResetVoteParticipant{{UserID: 8, Username: "bob"}},
		},
	}

	h := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.subscriberResetVotes = reader

	router := gin.New()
	router.POST("/accounts/:id/refresh-forbidden", func(c *gin.Context) {
		h.RefreshSubscriberResetCredits(c, 101, map[int64]struct{}{99: {}}, false)
	})
	router.POST("/accounts/:id/refresh-ok", func(c *gin.Context) {
		h.RefreshSubscriberResetCredits(c, 101, map[int64]struct{}{10: {}}, false)
	})
	router.GET("/admin/accounts/:id/reset-votes", h.GetAccountResetVotes)

	wForbidden := httptest.NewRecorder()
	router.ServeHTTP(wForbidden, httptest.NewRequest(http.MethodPost, "/accounts/101/refresh-forbidden", nil))
	require.Equal(t, http.StatusForbidden, wForbidden.Code)

	wOk := httptest.NewRecorder()
	router.ServeHTTP(wOk, httptest.NewRequest(http.MethodPost, "/accounts/101/refresh-ok", nil))
	require.Equal(t, http.StatusOK, wOk.Code)

	wBadID := httptest.NewRecorder()
	router.ServeHTTP(wBadID, httptest.NewRequest(http.MethodGet, "/admin/accounts/abc/reset-votes", nil))
	require.Equal(t, http.StatusBadRequest, wBadID.Code)

	wAdmin := httptest.NewRecorder()
	router.ServeHTTP(wAdmin, httptest.NewRequest(http.MethodGet, "/admin/accounts/101/reset-votes", nil))
	require.Equal(t, http.StatusOK, wAdmin.Code)
	var adminBody struct {
		Data service.AdminAccountResetVoteDetails `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wAdmin.Body.Bytes(), &adminBody))
	require.Len(t, adminBody.Data.Voters, 1)
	require.Len(t, adminBody.Data.NonVoters, 1)
}
