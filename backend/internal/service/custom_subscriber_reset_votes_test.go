//go:build unit

package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type stubVoteAccountRepo struct {
	AccountRepository
	byID map[int64]*Account
	err  error
}

func (r *stubVoteAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.err != nil {
		return nil, r.err
	}
	acc, ok := r.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return acc, nil
}

type stubVoteQuotaClient struct {
	usage        *OpenAIQuotaUsage
	queryErr     error
	targetedRes  *OpenAIQuotaResetResult
	targetedErr  error
	requestIDRes *OpenAIQuotaResetResult
	requestIDErr error

	targetedCalls   int
	lastCreditID    string
	lastRequestID   string
	requestIDCalls  int
	cachedSnapshots int
}

func (q *stubVoteQuotaClient) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	return q.usage, nil
}

func (q *stubVoteQuotaClient) ResetCreditTargeted(_ context.Context, _ int64, creditID, redeemRequestID string) (*OpenAIQuotaResetResult, error) {
	q.targetedCalls++
	q.lastCreditID = creditID
	q.lastRequestID = redeemRequestID
	return q.targetedRes, q.targetedErr
}

func (q *stubVoteQuotaClient) ResetCreditWithRequestID(_ context.Context, _ int64, redeemRequestID string) (*OpenAIQuotaResetResult, error) {
	q.requestIDCalls++
	q.lastRequestID = redeemRequestID
	return q.requestIDRes, q.requestIDErr
}

func (q *stubVoteQuotaClient) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	q.cachedSnapshots++
	return nil
}

type stubVoteRecoverer struct {
	calls int
	err   error
}

func (r *stubVoteRecoverer) RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return &SuccessfulTestRecoveryResult{}, nil
}

type stubVoteResetApplier struct {
	calls         int
	lastAccountID int64
	lastOperation string
	lastActorID   int64
	resetCount    int
	err           error
}

func (a *stubVoteResetApplier) ResetByVote(_ context.Context, accountID int64, operationID string, actorUserID int64) (int, error) {
	a.calls++
	a.lastAccountID = accountID
	a.lastOperation = operationID
	a.lastActorID = actorUserID
	return a.resetCount, a.err
}

func makeVoteTestOpenAIAccount(id int64) *Account {
	return &Account{
		ID:       id,
		Name:     "Shared Codex",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra:    map[string]any{SubscriberVisibleExtraKey: true},
	}
}

func voteRoundColumns() []string {
	return []string{
		"id", "account_id", "threshold", "status", "request_id",
		"initiated_at", "expires_at", "triggered_at", "completed_at",
		"card_status", "subscription_reset_status", "subscription_reset_count",
		"error_code", "error_message", "warning_code", "vote_count", "has_voted",
	}
}

func voteRoundUpdateColumns() []string {
	return []string{
		"id", "account_id", "threshold", "status", "request_id",
		"initiated_at", "expires_at", "triggered_at", "completed_at",
		"card_status", "subscription_reset_status", "subscription_reset_count",
		"error_code", "error_message", "warning_code",
	}
}

func TestCustomSubscriberResetVoteGetStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{}, nil, nil, nil)
	svc.now = func() time.Time { return now }

	parentID := int64(99)
	accOpenAI := makeVoteTestOpenAIAccount(1)
	accQuotaOnly := makeVoteTestOpenAIAccount(2)
	accNotSubscribed := makeVoteTestOpenAIAccount(3)
	accSucceeded := makeVoteTestOpenAIAccount(4)
	accExpired := makeVoteTestOpenAIAccount(5)
	accClaude := &Account{ID: 6, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{SubscriberVisibleExtraKey: true}}
	accShadow := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, Extra: map[string]any{SubscriberVisibleExtraKey: true}}
	accHidden := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	accSynthetic := &Account{ID: 9, Name: "Synthetic Test", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{SubscriberVisibleExtraKey: true, "synthetic_ui_test": true}}

	// Supported account IDs in order: 1, 2, 3, 4, 5
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT ag.account_id`)).
		WithArgs(sqlmock.AnyArg(), SubscriptionTypeSubscription).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(1).AddRow(3).AddRow(4).AddRow(5))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT ag.account_id`)).
		WithArgs(int64(42), sqlmock.AnyArg(), StatusActive, now, SubscriptionTypeSubscription).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(1).AddRow(4).AddRow(5))

	roundRows := sqlmock.NewRows(voteRoundColumns()).
		AddRow(int64(11), int64(1), 3, "open", "req-11", now.Add(-1*time.Hour), now.Add(23*time.Hour), nil, nil, "pending", "pending", 0, "", "", "", 2, true).
		AddRow(int64(12), int64(3), 3, "open", "req-12", now.Add(-2*time.Hour), now.Add(22*time.Hour), nil, nil, "pending", "pending", 0, "", "", "", 1, false).
		AddRow(int64(13), int64(4), 3, "succeeded", "req-13", now.Add(-3*time.Hour), now.Add(21*time.Hour), now.Add(-2*time.Hour), now.Add(-2*time.Hour), "succeeded", "succeeded", 5, "", "", "", 3, true).
		AddRow(int64(14), int64(5), 3, "succeeded", "req-14", now.Add(-25*time.Hour), now.Add(-1*time.Hour), now.Add(-24*time.Hour), now.Add(-24*time.Hour), "succeeded", "succeeded", 4, "", "", "", 3, true)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT DISTINCT ON (r.account_id)`)).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnRows(roundRows)

	states, err := svc.GetStatesForAccounts(context.Background(), 42, []*Account{
		accOpenAI, accQuotaOnly, accNotSubscribed, accSucceeded, accExpired,
		accClaude, accShadow, accHidden, accSynthetic,
	})
	require.NoError(t, err)
	require.Len(t, states, 9)

	// Open round where user already voted
	require.True(t, states[1].Supported)
	require.True(t, states[1].Eligible)
	require.False(t, states[1].CanVote)
	require.True(t, states[1].HasVoted)
	require.Equal(t, 2, states[1].VoteCount)
	require.Equal(t, SubscriberResetVoteStatusOpen, states[1].Status)

	// Quota-only account
	require.True(t, states[2].Supported)
	require.False(t, states[2].Eligible)
	require.False(t, states[2].CanVote)
	require.Equal(t, SubscriberResetVoteIneligibleQuotaGroupOnly, states[2].IneligibleReason)

	// Subscription groups exist, user not subscribed
	require.True(t, states[3].Supported)
	require.False(t, states[3].Eligible)
	require.False(t, states[3].CanVote)
	require.Equal(t, SubscriberResetVoteIneligibleNotSubscribed, states[3].IneligibleReason)
	require.Equal(t, 1, states[3].VoteCount)

	// Succeeded round within 24h cooldown
	require.Equal(t, SubscriberResetVoteStatusSucceeded, states[4].Status)
	require.True(t, states[4].CooldownActive)
	require.False(t, states[4].CanVote)
	require.False(t, states[4].CanInitiate)
	require.Equal(t, 5, states[4].SubscriptionResetCount)

	// Expired round after 24h -> ready to initiate a fresh round
	require.Equal(t, SubscriberResetVoteStatusNone, states[5].Status)
	require.False(t, states[5].CooldownActive)
	require.True(t, states[5].CanInitiate)
	require.True(t, states[5].CanVote)
	require.False(t, states[5].HasVoted)
	require.Equal(t, 0, states[5].VoteCount)

	// Unsupported account types
	for _, id := range []int64{6, 7, 8, 9} {
		require.False(t, states[id].Supported, "account %d should be unsupported", id)
		require.False(t, states[id].CanVote)
		require.Equal(t, SubscriberResetVoteIneligibleUnsupported, states[id].IneligibleReason)
	}

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCustomSubscriberResetVoteCastVoteRejections(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("non-subscription user rejected with 403", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, nil, nil, nil)
		svc.now = func() time.Time { return now }

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).
			WithArgs(int64(77), int64(1), StatusActive, now, SubscriptionTypeSubscription).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectRollback()

		_, err = svc.CastVote(context.Background(), 77, 1)
		require.Error(t, err)
		require.Equal(t, "RESET_VOTE_SUBSCRIPTION_REQUIRED", infraerrors.Reason(err))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate vote within 24h rejected with 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, nil, nil, nil)
		svc.now = func() time.Time { return now }

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, threshold, status`)).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(voteRoundUpdateColumns()).AddRow(
				int64(10), int64(1), 3, "open", "req-10",
				now.Add(-time.Hour), now.Add(23*time.Hour), nil, nil,
				"pending", "pending", 0, "", "", "",
			))
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_voters`)).
			WithArgs(int64(10), int64(42), now).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()

		_, err = svc.CastVote(context.Background(), 42, 1)
		require.Error(t, err)
		require.Equal(t, "RESET_VOTE_ALREADY_VOTED", infraerrors.Reason(err))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cooldown active before 24h elapses", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, nil, nil, nil)
		svc.now = func() time.Time { return now }

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, threshold, status`)).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(voteRoundUpdateColumns()).AddRow(
				int64(10), int64(1), 3, "succeeded", "req-10",
				now.Add(-2*time.Hour), now.Add(22*time.Hour), now.Add(-1*time.Hour), now.Add(-1*time.Hour),
				"succeeded", "succeeded", 3, "", "", "",
			))
		mock.ExpectRollback()

		_, err = svc.CastVote(context.Background(), 42, 1)
		require.Error(t, err)
		require.Equal(t, "RESET_VOTE_COOLDOWN_ACTIVE", infraerrors.Reason(err))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCustomSubscriberResetVoteInitiateAndReinitiateAfter24h(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("initiates first round counting as vote 1", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, nil, nil, nil)
		svc.now = func() time.Time { return now }
		svc.newUUID = func() string { return "round-uuid-1" }

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, threshold, status`)).
			WithArgs(int64(1)).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(1), int64(11), 3, "round-uuid-1", now, now.Add(24*time.Hour)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_voters`)).
			WithArgs(int64(101), int64(11), now).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		res, err := svc.CastVote(context.Background(), 11, 1)
		require.NoError(t, err)
		require.True(t, res.VoteRecorded)
		require.False(t, res.TriggeredReset)
		require.Equal(t, int64(101), res.State.RoundID)
		require.Equal(t, 1, res.State.VoteCount)
		require.Equal(t, SubscriberResetVoteStatusOpen, res.State.Status)
		require.True(t, res.State.HasVoted)
		require.False(t, res.State.CanVote)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("starts fresh round once previous round expired after 24h", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, nil, nil, nil)
		svc.now = func() time.Time { return now }
		svc.newUUID = func() string { return "round-uuid-2" }

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, threshold, status`)).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(voteRoundUpdateColumns()).AddRow(
				int64(99), int64(1), 3, "succeeded", "old-req",
				now.Add(-24*time.Hour), now, now.Add(-23*time.Hour), now.Add(-23*time.Hour),
				"succeeded", "succeeded", 2, "", "", "",
			))
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(1), int64(11), 3, "round-uuid-2", now, now.Add(24*time.Hour)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(102)))
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_voters`)).
			WithArgs(int64(102), int64(11), now).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		res, err := svc.CastVote(context.Background(), 11, 1)
		require.NoError(t, err)
		require.True(t, res.VoteRecorded)
		require.False(t, res.TriggeredReset)
		require.Equal(t, int64(102), res.State.RoundID)
		require.Equal(t, 1, res.State.VoteCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCustomSubscriberResetVoteThresholdTriggerAndFailures(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	expectVoteThreeTransition := func(mock sqlmock.Sqlmock) {
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock`)).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, threshold, status`)).
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows(voteRoundUpdateColumns()).AddRow(
				int64(20), int64(1), 3, "open", "req-20",
				now.Add(-time.Hour), now.Add(23*time.Hour), nil, nil,
				"pending", "pending", 0, "", "", "",
			))
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO custom_subscriber_reset_vote_voters`)).
			WithArgs(int64(20), int64(33), now).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM custom_subscriber_reset_vote_voters`)).
			WithArgs(int64(20)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(20), now).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}

	t.Run("vote 3 consumes targeted card and resets subscriptions", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		quota := &stubVoteQuotaClient{
			usage: &OpenAIQuotaUsage{
				RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 2},
				autoResetCandidates: []openAIAutoResetCreditCandidate{
					{ID: "card-later", ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)},
					{ID: "card-soon", ExpiresAt: now.Add(6 * time.Hour).Format(time.RFC3339)},
				},
			},
			targetedRes: &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2},
		}
		recoverer := &stubVoteRecoverer{}
		applier := &stubVoteResetApplier{resetCount: 4}
		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, quota, recoverer, applier)
		svc.now = func() time.Time { return now }

		expectVoteThreeTransition(mock)
		mock.ExpectExec(regexp.QuoteMeta(`SET card_status = 'succeeded'`)).
			WithArgs(int64(20), now).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(20), "succeeded", "succeeded", "succeeded", 4, "", "", "", now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		res, err := svc.CastVote(context.Background(), 33, 1)
		require.NoError(t, err)
		require.True(t, res.VoteRecorded)
		require.True(t, res.TriggeredReset)
		require.True(t, res.ResetSucceeded)
		require.Equal(t, 1, quota.targetedCalls)
		require.Equal(t, "card-soon", quota.lastCreditID)
		require.Equal(t, "req-20", quota.lastRequestID)
		require.Equal(t, 1, recoverer.calls)
		require.Equal(t, 1, quota.cachedSnapshots)
		require.Equal(t, 1, applier.calls)
		require.Equal(t, "req-20", applier.lastOperation)
		require.Equal(t, int64(33), applier.lastActorID)
		require.Equal(t, 4, res.State.SubscriptionResetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("vote 3 falls back to ResetCreditWithRequestID when card details missing", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		quota := &stubVoteQuotaClient{
			usage: &OpenAIQuotaUsage{
				RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1},
			},
			requestIDRes: &OpenAIQuotaResetResult{Code: "success", WindowsReset: 2},
		}
		applier := &stubVoteResetApplier{resetCount: 2}
		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, quota, &stubVoteRecoverer{}, applier)
		svc.now = func() time.Time { return now }

		expectVoteThreeTransition(mock)
		mock.ExpectExec(regexp.QuoteMeta(`SET card_status = 'succeeded'`)).
			WithArgs(int64(20), now).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(20), "succeeded", "succeeded", "succeeded", 2, "", "", "", now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		res, err := svc.CastVote(context.Background(), 33, 1)
		require.NoError(t, err)
		require.True(t, res.ResetSucceeded)
		require.Equal(t, 1, quota.requestIDCalls)
		require.Equal(t, "req-20", quota.lastRequestID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("vote 3 with no reset card marks no_credit and skips subscription reset", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		quota := &stubVoteQuotaClient{
			usage: &OpenAIQuotaUsage{
				RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 0},
			},
		}
		applier := &stubVoteResetApplier{resetCount: 2}
		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, quota, &stubVoteRecoverer{}, applier)
		svc.now = func() time.Time { return now }

		expectVoteThreeTransition(mock)
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(20), "failed", "no_credit", "pending", 0, "NO_RESET_CARD", "No reset card is available for this account", "", now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		res, err := svc.CastVote(context.Background(), 33, 1)
		require.NoError(t, err)
		require.True(t, res.TriggeredReset)
		require.False(t, res.ResetSucceeded)
		require.Equal(t, "no_credit", res.State.CardStatus)
		require.Equal(t, 0, applier.calls)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("card succeeds but subscription reset fails preserves card_status succeeded", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		quota := &stubVoteQuotaClient{
			usage: &OpenAIQuotaUsage{
				RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1},
				autoResetCandidates: []openAIAutoResetCreditCandidate{
					{ID: "card-1", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
				},
			},
			targetedRes: &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2},
		}
		applier := &stubVoteResetApplier{err: errors.New("db failure")}
		svc := newCustomSubscriberResetVoteServiceWithDeps(db, &stubVoteAccountRepo{
			byID: map[int64]*Account{1: makeVoteTestOpenAIAccount(1)},
		}, quota, &stubVoteRecoverer{}, applier)
		svc.now = func() time.Time { return now }

		expectVoteThreeTransition(mock)
		mock.ExpectExec(regexp.QuoteMeta(`SET card_status = 'succeeded'`)).
			WithArgs(int64(20), now).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_subscriber_reset_vote_rounds`)).
			WithArgs(int64(20), "failed", "succeeded", "failed", 0, "ACTION_FAILED", "Reset card succeeded, but resetting subscription group usage failed", "", now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		res, err := svc.CastVote(context.Background(), 33, 1)
		require.NoError(t, err)
		require.True(t, res.TriggeredReset)
		require.False(t, res.ResetSucceeded)
		require.Equal(t, "succeeded", res.State.CardStatus)
		require.Equal(t, "failed", res.State.SubscriptionResetStatus)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
