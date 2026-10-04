package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type scheduledQuotaRebindingAccounts struct {
	AccountRepository
	approved, replacement *Account
	loads                 atomic.Int64
}

func (r *scheduledQuotaRebindingAccounts) GetByID(context.Context, int64) (*Account, error) {
	if r.loads.Add(1) == 1 {
		return r.approved, nil
	}
	return r.replacement, nil
}

func scheduledQuotaTestAccount(identity string) *Account {
	return &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": identity}}
}

func TestOpenAIQuotaScheduleRejectsIdentityChangedDuringPreparation(t *testing.T) {
	approved := scheduledQuotaTestAccount("approved-upstream-account")
	replacement := scheduledQuotaTestAccount("newly-bound-upstream-account")
	repo := &scheduledQuotaRebindingAccounts{approved: approved, replacement: replacement}
	tokens := &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(approved):    "synthetic-approved-token",
		OpenAITokenCacheKey(replacement): "synthetic-replacement-token",
	}}
	var posts, factories atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"ok","windows_reset":1}`))
	}))
	t.Cleanup(server.Close)
	redirect := newQuotaRedirectingFactory(server)
	service := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), func(proxy string) (*req.Client, error) {
		factories.Add(1)
		return redirect(proxy)
	}, nil)

	// The initial shadow check observes the approved account, but request
	// preparation observes a rebind. Checking only the initial read is unsafe.
	result, err := service.ResetCreditTargetedForSchedule(context.Background(), approved.ID,
		"fixed-reset-card", uuid.NewString(), customResetIdentity(approved))
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, "ACCOUNT_SCHEDULE_IDENTITY_CHANGED", infraerrors.Reason(err))
	require.GreaterOrEqual(t, repo.loads.Load(), int64(2))
	require.Zero(t, factories.Load(), "reject before constructing an upstream client")
	require.Zero(t, posts.Load(), "a rebound account must never receive the redemption")
}

func TestOpenAIQuotaSchedulePreservesApprovedIdentityAndFixedRedemption(t *testing.T) {
	account := scheduledQuotaTestAccount("approved-upstream-account")
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "synthetic-token"}}
	type request struct {
		method, path, identity, authorization string
		body                                  map[string]string
		err                                   error
	}
	requests := make(chan request, 2)
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		captured := request{method: r.Method, path: r.URL.Path, identity: r.Header.Get("chatgpt-account-id"),
			authorization: r.Header.Get("Authorization")}
		captured.err = json.NewDecoder(r.Body).Decode(&captured.body)
		requests <- captured
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"ok","windows_reset":2}`))
	}))
	t.Cleanup(server.Close)
	service := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(server), nil)
	requestID := uuid.NewString()

	result, err := service.ResetCreditTargetedForSchedule(context.Background(), account.ID,
		" fixed-reset-card ", " "+requestID+" ", customResetIdentity(account))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "ok", result.Code)
	require.Equal(t, 2, result.WindowsReset)
	require.EqualValues(t, 1, posts.Load(), "one targeted redemption request")
	captured := <-requests
	require.NoError(t, captured.err)
	require.Equal(t, http.MethodPost, captured.method)
	require.Equal(t, "/backend-api/wham/rate-limit-reset-credits/consume", captured.path)
	require.Equal(t, account.GetChatGPTAccountID(), captured.identity)
	require.Equal(t, "Bearer synthetic-token", captured.authorization)
	require.Equal(t, map[string]string{"credit_id": "fixed-reset-card", "redeem_request_id": requestID}, captured.body)
}
