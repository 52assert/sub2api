package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCustomAccountScheduleValidation(t *testing.T) {
	now := time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC)
	day := 0
	for _, test := range []struct {
		name  string
		input CustomAccountScheduleInput
		want  string
	}{
		{"local once", CustomAccountScheduleInput{Action: "reset_card", Frequency: "once", RunAt: "2026-10-04T20:00", Enabled: true}, "2026-10-04T12:00:00Z"},
		{"RFC3339 once", CustomAccountScheduleInput{Action: "reset_card", Frequency: "once", Timezone: "America/New_York", RunAt: "2026-10-04T09:00:00-04:00", Enabled: true}, "2026-10-04T13:00:00Z"},
		{"daily tomorrow", CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "18:00", Enabled: true}, "2026-10-05T10:00:00Z"},
		{"weekly next Sunday", CustomAccountScheduleInput{Action: "reset_subscriptions", Frequency: "weekly", TimeOfDay: "18:00", Weekday: &day, Enabled: true}, "2026-10-11T10:00:00Z"},
		{"Cron uses explicit timezone", CustomAccountScheduleInput{Action: "reset_card", Frequency: "cron", Timezone: "America/New_York", CronExpression: "0 9 * * 1-5", Enabled: true}, "2026-10-05T13:00:00Z"},
		{"Cron every minute", CustomAccountScheduleInput{Action: "reset_card", Frequency: "cron", CronExpression: "* * * * *", Enabled: true}, "2026-10-04T11:01:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := validateCustomAccountSchedule(test.input, "Asia/Shanghai", now)
			require.NoError(t, err)
			require.NotNil(t, v.NextRunAt)
			require.Equal(t, test.want, v.NextRunAt.Format(time.RFC3339))
		})
	}
	badDay := 7
	for _, input := range []CustomAccountScheduleInput{
		{Action: "other", Frequency: "daily", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "monthly", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "daily", Timezone: "no/such-zone", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "daily", Timezone: "Local", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "daily", TimeOfDay: "2:00"},
		{Action: "reset_card", Frequency: "daily", TimeOfDay: "24:00"},
		{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00", Weekday: &day},
		{Action: "reset_card", Frequency: "weekly", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "weekly", TimeOfDay: "12:00", Weekday: &badDay},
		{Action: "reset_card", Frequency: "once", RunAt: "2026-10-04T10:00:00Z"},
		{Action: "reset_card", Frequency: "once", RunAt: "tomorrow"},
		{Action: "reset_card", Frequency: "once", RunAt: "2026-10-05T12:00", TimeOfDay: "12:00"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "* * * * * *"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "@every 1s"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "CRON_TZ=UTC 0 0 * * *"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "TZ=UTC 0 0 * *"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "0 0 30 2 *"},
		{Action: "reset_card", Frequency: "cron", CronExpression: "0 0 * * *", TimeOfDay: "00:00"},
		{Action: "reset_card", Frequency: "daily", TimeOfDay: "00:00", CronExpression: "0 0 * * *"},
	} {
		_, err := validateCustomAccountSchedule(input, "Asia/Shanghai", now)
		require.Error(t, err, "%+v", input)
	}
	v, err := validateCustomAccountSchedule(CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00"}, "Asia/Shanghai", now)
	require.NoError(t, err)
	require.Nil(t, v.NextRunAt)
}

func TestCustomAccountScheduleDSTAndMissedSlots(t *testing.T) {
	v := &CustomAccountSchedule{Frequency: "daily", Timezone: "America/New_York", TimeOfDay: "02:30"}
	next, err := nextCustomAccountSchedule(v, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "2026-03-09T06:30:00Z", next.Format(time.RFC3339), "skip nonexistent spring time")
	v.TimeOfDay = "01:30"
	next, err = nextCustomAccountSchedule(v, time.Date(2026, 11, 1, 5, 31, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T06:30:00Z", next.Format(time.RFC3339), "do not repeat daily action during fall-back")
	v.Timezone = "Asia/Shanghai"
	v.TimeOfDay = "08:00"
	next, err = nextCustomAccountSchedule(v, time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, "2026-10-21T00:00:00Z", next.Format(time.RFC3339), "advance past all missed days")
	_, err = validateCustomAccountSchedule(CustomAccountScheduleInput{Action: "reset_card", Frequency: "once", Timezone: "America/New_York", RunAt: "2026-03-08T02:30", Enabled: true}, "UTC", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	require.Error(t, err, "reject nonexistent once datetime-local")
}

func TestCustomAccountScheduleCardSelection(t *testing.T) {
	now := time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC)
	usage := &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 3}, autoResetCandidates: []openAIAutoResetCreditCandidate{
		{ID: "later", ExpiresAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
		{ID: "expired", ExpiresAt: now.Add(-time.Second).Format(time.RFC3339)},
		{ID: "earliest", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	}}
	c, err := selectCustomAccountScheduleCard(usage, now)
	require.NoError(t, err)
	require.Equal(t, "earliest", c.ID)
	require.Equal(t, "later", usage.autoResetCandidates[0].ID, "must not mutate shared quota data")
	usage.RateLimitResetCredits.AvailableCount = 4
	_, err = selectCustomAccountScheduleCard(usage, now)
	require.ErrorContains(t, err, "incomplete")
	usage.RateLimitResetCredits.AvailableCount = 3
	usage.autoResetCandidates[0].ID = ""
	_, err = selectCustomAccountScheduleCard(usage, now)
	require.ErrorContains(t, err, "incomplete")
	usage.autoResetCandidates[0].ID = "later"
	usage.autoResetCandidates[0].ExpiresAt = "invalid"
	_, err = selectCustomAccountScheduleCard(usage, now)
	require.ErrorContains(t, err, "invalid")
	for i := range usage.autoResetCandidates {
		usage.autoResetCandidates[i].ExpiresAt = now.Format(time.RFC3339)
	}
	_, err = selectCustomAccountScheduleCard(usage, now)
	require.ErrorContains(t, err, "unexpired")
}

type customAccountScheduleTestRepo struct {
	AccountRepository
	account *Account
	err     error
}

func (r *customAccountScheduleTestRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

type customAccountScheduleTestQuota struct {
	usage                  *OpenAIQuotaUsage
	queryErr               error
	reset                  func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error)
	queryCount, resetCount int
	cacheErr               error
	onQuery                func()
	lastIdentity           string
}

func (q *customAccountScheduleTestQuota) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	q.queryCount++
	if q.onQuery != nil {
		q.onQuery()
	}
	return q.usage, q.queryErr
}
func (q *customAccountScheduleTestQuota) ResetCreditTargetedForSchedule(ctx context.Context, id int64, card, request, expectedIdentity string) (*OpenAIQuotaResetResult, error) {
	q.resetCount++
	q.lastIdentity = expectedIdentity
	return q.reset(ctx, id, card, request)
}
func (q *customAccountScheduleTestQuota) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	return q.cacheErr
}

type customAccountScheduleTestRecoverer struct{}

func (*customAccountScheduleTestRecoverer) RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	return &SuccessfulTestRecoveryResult{}, nil
}

type customAccountScheduleTestSubscriptions struct {
	calls int
	count int
	err   error
}

func (s *customAccountScheduleTestSubscriptions) Scheduled(_ context.Context, _ int64, _ string, _ []int64, _ int64, _ ...string) (int, error) {
	s.calls++
	return s.count, s.err
}

func newCustomAccountScheduleTestService(t *testing.T) (*CustomAccountScheduleService, sqlmock.Sqlmock, *CustomAccountSchedule, *CustomAccountScheduleRun) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	a := &Account{ID: 27, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "stable"}}
	s := NewCustomAccountScheduleService(db, &customAccountScheduleTestRepo{account: a}, nil, nil, nil, "Asia/Shanghai")
	t.Cleanup(s.Stop)
	v := &CustomAccountSchedule{ID: 1, AccountID: 27, Action: "reset_card", identity: customResetIdentity(a), groupIDs: []int64{3}}
	r := &CustomAccountScheduleRun{ID: 9, ScheduleID: 1, AccountID: 27, requestID: "f0efe6b5-a494-470e-824c-cb354a69b007"}
	return s, mock, v, r
}
func expectCustomAccountScheduleGroups(mock sqlmock.Sqlmock, ids ...int64) {
	rows := sqlmock.NewRows([]string{"id", "name"})
	for _, id := range ids {
		rows.AddRow(id, fmt.Sprintf("group-%d", id))
	}
	mock.ExpectQuery("SELECT g.id,g.name FROM account_groups").WithArgs(int64(27)).WillReturnRows(rows)
}

func TestCustomAccountScheduleExecutionSafety(t *testing.T) {
	t.Run("scope change never calls upstream", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 4)
		q := &customAccountScheduleTestQuota{}
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.True(t, result.pause)
		require.Equal(t, "failed", result.status)
		require.Zero(t, q.queryCount)
		require.Zero(t, q.resetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("identity change never calls upstream", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		v.identity = "changed"
		result := s.execute(context.Background(), v, r)
		require.True(t, result.pause)
		require.Contains(t, result.message, "identity")
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("persist targeted attempt and retain successful warning", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 3)
		now := time.Now()
		s.now = func() time.Time { return now }
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "private-card-id", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)}}}, cacheErr: errors.New("cache down")}
		mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "private-card-id").WillReturnResult(sqlmock.NewResult(0, 1))
		q.reset = func(_ context.Context, id int64, card, request string) (*OpenAIQuotaResetResult, error) {
			require.NoError(t, mock.ExpectationsWereMet(), "durable card fixed before IO")
			require.Equal(t, int64(27), id)
			require.Equal(t, "private-card-id", card)
			require.Equal(t, r.requestID, request)
			return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
		}
		s.quota = q
		s.recoverer = &customAccountScheduleTestRecoverer{}
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "succeeded", result.status)
		require.Equal(t, 2, result.count)
		require.Equal(t, OpenAIQuotaResetWarningCacheRefreshFailed, result.warning)
		require.False(t, result.pause)
		require.Equal(t, 1, q.resetCount)
		require.Equal(t, v.identity, q.lastIdentity)
	})
	t.Run("timeout is unknown and paused", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 3)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}, reset: func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
			return nil, infraerrors.New(502, "OPENAI_QUOTA_RESET_REQUEST_FAILED", "secret upstream payload")
		}}
		mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "card").WillReturnResult(sqlmock.NewResult(0, 1))
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "unknown", result.status)
		require.True(t, result.pause)
		require.NotContains(t, result.message, "secret")
		require.Equal(t, 1, q.resetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("inactive persisted attempt never calls redemption", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 3)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
		mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "card").WillReturnResult(sqlmock.NewResult(0, 0))
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "failed", result.status)
		require.True(t, result.pause)
		require.Zero(t, q.resetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	for _, code := range []string{"ok", "success", "unexpected_secret_code", ""} {
		t.Run("reset result code "+code, func(t *testing.T) {
			s, mock, v, r := newCustomAccountScheduleTestService(t)
			expectCustomAccountScheduleGroups(mock, 3)
			expectCustomAccountScheduleGroups(mock, 3)
			q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}, reset: func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
				return &OpenAIQuotaResetResult{Code: code, WindowsReset: 2}, nil
			}}
			mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "card").WillReturnResult(sqlmock.NewResult(0, 1))
			s.quota = q
			result := s.execute(context.Background(), v, r)
			if code == "ok" || code == "success" {
				require.Equal(t, "succeeded", result.status)
				require.Equal(t, 2, result.count)
			} else {
				require.Equal(t, "unknown", result.status)
				require.True(t, result.pause)
				require.NotContains(t, result.message, "secret")
			}
			require.Equal(t, 1, q.resetCount)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("groups changed during quota read never redeem", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 4)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "failed", result.status)
		require.True(t, result.pause)
		require.Zero(t, q.resetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("identity changed after redemption is never recovered", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 3)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}, reset: func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
			repo, ok := s.accounts.(*customAccountScheduleTestRepo)
			require.True(t, ok)
			repo.account.Credentials["chatgpt_account_id"] = "new-identity"
			return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
		}}
		mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "card").WillReturnResult(sqlmock.NewResult(0, 1))
		s.quota = q
		s.recoverer = &customAccountScheduleTestRecoverer{}
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "succeeded", result.status)
		require.True(t, result.pause)
		require.Equal(t, "account_identity_changed", result.warning)
		require.Equal(t, 1, q.queryCount, "no postprocess quota read for a different account")
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("identity changed during quota read never redeems", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}, onQuery: func() {
			repo, ok := s.accounts.(*customAccountScheduleTestRepo)
			require.True(t, ok)
			repo.account.Credentials["chatgpt_account_id"] = "different"
		}}
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "failed", result.status)
		require.True(t, result.pause)
		require.Zero(t, q.resetCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("upstream preparation identity mismatch pauses without unknown", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		expectCustomAccountScheduleGroups(mock, 3)
		expectCustomAccountScheduleGroups(mock, 3)
		q := &customAccountScheduleTestQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}, reset: func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
			return nil, infraerrors.Conflict("ACCOUNT_SCHEDULE_IDENTITY_CHANGED", "Account identity changed")
		}}
		mock.ExpectExec("UPDATE custom_account_action_schedule_runs SET credit_id").WithArgs(r.ID, "card").WillReturnResult(sqlmock.NewResult(0, 1))
		s.quota = q
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "failed", result.status)
		require.True(t, result.pause)
		require.Equal(t, v.identity, q.lastIdentity)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("postcommit subscription warning is success", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		v.Action = "reset_subscriptions"
		expectCustomAccountScheduleGroups(mock, 3)
		sub := &customAccountScheduleTestSubscriptions{count: 2, err: fmt.Errorf("%w: cache down", ErrScheduledSubscriptionCacheRefresh)}
		s.resets = sub
		result := s.execute(context.Background(), v, r)
		require.Equal(t, "succeeded", result.status)
		require.Equal(t, 2, result.count)
		require.Equal(t, "subscription_cache_refresh_failed", result.warning)
		require.Equal(t, 1, sub.calls)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("cancel before redemption", func(t *testing.T) {
		s, mock, v, r := newCustomAccountScheduleTestService(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := s.execute(ctx, v, r)
		require.Equal(t, "failed", result.status)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCustomAccountScheduleOwnership(t *testing.T) {
	for _, op := range []string{"update", "delete", "runs"} {
		t.Run(op, func(t *testing.T) {
			s, mock, _, _ := newCustomAccountScheduleTestService(t)
			if op == "runs" {
				mock.ExpectQuery("SELECT EXISTS.*custom_account_action_schedules").WithArgs(int64(1), int64(27)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			} else {
				mock.ExpectBegin()
				mock.ExpectQuery("SELECT id FROM custom_account_action_schedules").WithArgs(int64(1), int64(27)).WillReturnError(sql.ErrNoRows)
				mock.ExpectRollback()
			}
			var err error
			switch op {
			case "update":
				_, err = s.Update(context.Background(), 27, 1, CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00", Enabled: true}, 1)
			case "delete":
				err = s.Delete(context.Background(), 27, 1)
			case "runs":
				_, err = s.Runs(context.Background(), 27, 1, 20)
			}
			require.Equal(t, "ACCOUNT_SCHEDULE_NOT_FOUND", infraerrors.Reason(err))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCustomAccountSchedulePrivateFields(t *testing.T) {
	v := CustomAccountSchedule{identity: "private-account", groupIDs: []int64{7}, actor: 123}
	r := CustomAccountScheduleRun{requestID: "private-request"}
	for _, value := range []any{v, r} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private")
	}
	require.Equal(t, "ACTION_FAILED", safeCustomAccountScheduleReason("upstream token=secret"))
	require.Equal(t, "ACTION_FAILED", safeCustomAccountScheduleReason(strings.Repeat("A", 101)))
}
