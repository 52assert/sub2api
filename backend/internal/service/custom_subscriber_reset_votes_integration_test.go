//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type subscriberVoteIntegrationAccountRepo struct {
	AccountRepository
	db *sql.DB
}

func (r *subscriberVoteIntegrationAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	a := &Account{ID: id}
	var credentials, extra []byte
	err := r.db.QueryRowContext(
		ctx,
		`SELECT platform, type, status, credentials, COALESCE(extra, '{}'::jsonb) FROM accounts WHERE id=$1 AND deleted_at IS NULL`,
		id,
	).Scan(&a.Platform, &a.Type, &a.Status, &credentials, &extra)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(credentials, &a.Credentials); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(extra, &a.Extra); err != nil {
		return nil, err
	}
	return a, nil
}

type subscriberVoteIntegrationQuota struct {
	targetedCalls atomic.Int32
	usage         *OpenAIQuotaUsage
}

func (q *subscriberVoteIntegrationQuota) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	return q.usage, nil
}

func (q *subscriberVoteIntegrationQuota) ResetCreditTargeted(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
	q.targetedCalls.Add(1)
	return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
}

func (q *subscriberVoteIntegrationQuota) ResetCreditWithRequestID(context.Context, int64, string) (*OpenAIQuotaResetResult, error) {
	q.targetedCalls.Add(1)
	return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
}

func (*subscriberVoteIntegrationQuota) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	return nil
}

type subscriberVoteIntegrationRecoverer struct{}

func (*subscriberVoteIntegrationRecoverer) RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	return &SuccessfulTestRecoveryResult{}, nil
}

func customSubscriberResetVoteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("CUSTOM_SCHEDULE_TEST_DSN")
	if dsn == "" {
		container, err := postgres.Run(
			ctx,
			"postgres:17-alpine",
			postgres.WithDatabase("votes_test"),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("votes-test-only"),
			postgres.BasicWaitStrategies(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)
	}
	original, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("custom_votes_%d", time.Now().UnixNano())
	_, err = original.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := original.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		require.NoError(t, e)
		_ = original.Close()
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(32)

	_, err = db.Exec(`
		CREATE TABLE users(id BIGINT PRIMARY KEY, email TEXT, deleted_at TIMESTAMPTZ);
		CREATE TABLE accounts(id BIGINT PRIMARY KEY, platform TEXT, type TEXT, status TEXT, credentials JSONB DEFAULT '{"chatgpt_account_id":"stable-id"}', extra JSONB DEFAULT '{"subscriber_visible":true}', deleted_at TIMESTAMPTZ);
		CREATE TABLE groups(id BIGINT PRIMARY KEY, name TEXT, subscription_type TEXT NOT NULL DEFAULT 'subscription', deleted_at TIMESTAMPTZ);
		CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id), group_id BIGINT REFERENCES groups(id), PRIMARY KEY(account_id, group_id));
		CREATE TABLE user_subscriptions(id BIGINT PRIMARY KEY, user_id BIGINT REFERENCES users(id), group_id BIGINT REFERENCES groups(id), status TEXT, starts_at TIMESTAMPTZ, expires_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ, daily_usage_usd NUMERIC(20,10), weekly_usage_usd NUMERIC(20,10), monthly_usage_usd NUMERIC(20,10), daily_window_start TIMESTAMPTZ, weekly_window_start TIMESTAMPTZ, updated_at TIMESTAMPTZ);
	`)
	require.NoError(t, err)

	for _, name := range []string{
		"241_custom_codex_subscription_reset.sql",
		"242_custom_codex_reset_history.sql",
		"243_custom_codex_window_probes.sql",
		"248_custom_account_action_schedules.sql",
		"251_custom_subscriber_account_reset_votes.sql",
	} {
		migration, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr)
		_, execErr := db.Exec(string(migration))
		require.NoError(t, execErr)
	}

	return db
}

func TestCustomSubscriberResetVotesEndToEnd(t *testing.T) {
	db := customSubscriberResetVoteTestDB(t)
	ctx := context.Background()

	_, err := db.Exec(`
		INSERT INTO users(id, email) VALUES
			(1, 'sub1@example.com'),
			(2, 'sub2@example.com'),
			(3, 'sub3@example.com'),
			(4, 'sub4@example.com'),
			(5, 'quota@example.com');
		INSERT INTO accounts(id, platform, type, status) VALUES
			(10, 'openai', 'oauth', 'active'),
			(20, 'openai', 'oauth', 'active');
		INSERT INTO groups(id, name, subscription_type) VALUES
			(101, 'codex-sub', 'subscription'),
			(102, 'codex-quota', 'standard');
		INSERT INTO account_groups(account_id, group_id) VALUES
			(10, 101),
			(10, 102),
			(20, 102);
		INSERT INTO user_subscriptions(id, user_id, group_id, status, starts_at, expires_at, daily_usage_usd, weekly_usage_usd, monthly_usage_usd, daily_window_start, weekly_window_start) VALUES
			(1, 1, 101, 'active', NOW()-INTERVAL '2 days', NOW()+INTERVAL '20 days', 12.5, 45.0, 90.0, NOW()-INTERVAL '4 hours', NOW()-INTERVAL '2 days'),
			(2, 2, 101, 'active', NOW()-INTERVAL '2 days', NOW()+INTERVAL '20 days', 8.0, 22.0, 60.0, NOW()-INTERVAL '4 hours', NOW()-INTERVAL '2 days'),
			(3, 3, 101, 'active', NOW()-INTERVAL '2 days', NOW()+INTERVAL '20 days', 5.0, 15.0, 40.0, NOW()-INTERVAL '4 hours', NOW()-INTERVAL '2 days'),
			(4, 4, 101, 'active', NOW()-INTERVAL '2 days', NOW()+INTERVAL '20 days', 3.0, 9.0, 20.0, NOW()-INTERVAL '4 hours', NOW()-INTERVAL '2 days'),
			(5, 5, 102, 'active', NOW()-INTERVAL '2 days', NOW()+INTERVAL '20 days', 18.0, 66.0, 120.0, NOW()-INTERVAL '4 hours', NOW()-INTERVAL '2 days');
	`)
	require.NoError(t, err)

	now := time.Now().UTC()
	repo := &subscriberVoteIntegrationAccountRepo{db: db}
	resets := NewCustomCodexResetService(db, repo, nil)
	t.Cleanup(resets.Stop)
	quota := &subscriberVoteIntegrationQuota{
		usage: &OpenAIQuotaUsage{
			RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1},
			autoResetCandidates: []openAIAutoResetCreditCandidate{
				{ID: "card-live", ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)},
			},
		},
	}
	svc := newCustomSubscriberResetVoteServiceWithDeps(db, repo, quota, &subscriberVoteIntegrationRecoverer{}, resets)
	svc.now = func() time.Time { return now }

	// Quota-group member cannot vote on account 10 or quota-only account 20.
	_, err = svc.CastVote(ctx, 5, 10)
	require.Equal(t, "RESET_VOTE_SUBSCRIPTION_REQUIRED", infraerrors.Reason(err))
	_, err = svc.CastVote(ctx, 5, 20)
	require.Equal(t, "RESET_VOTE_SUBSCRIPTION_REQUIRED", infraerrors.Reason(err))

	// Votes 1, 2, 3, 4 cast concurrently -> threshold=3 triggers reset exactly once!
	var wg sync.WaitGroup
	results := make([]*SubscriberResetVoteCastResult, 4)
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.CastVote(ctx, int64(idx+1), 10)
		}(i)
	}
	wg.Wait()

	triggeredCount := 0
	cooldownRejections := 0
	for i := 0; i < 4; i++ {
		if errs[i] == nil {
			if results[i].TriggeredReset {
				triggeredCount++
				require.True(t, results[i].ResetSucceeded)
			}
		} else {
			require.Contains(t, []string{"RESET_VOTE_EXECUTING", "RESET_VOTE_COOLDOWN_ACTIVE"}, infraerrors.Reason(errs[i]))
			cooldownRejections++
		}
	}
	require.Equal(t, 1, triggeredCount)
	require.Equal(t, 1, cooldownRejections)
	require.Equal(t, int32(1), quota.targetedCalls.Load())

	// Subscription group users (1..4) had daily/weekly usage reset, while standard/quota group user 5 did NOT!
	var subDaily, subWeekly float64
	require.NoError(t, db.QueryRow(`SELECT daily_usage_usd, weekly_usage_usd FROM user_subscriptions WHERE id = 1`).Scan(&subDaily, &subWeekly))
	require.Zero(t, subDaily)
	require.Zero(t, subWeekly)

	var quotaDaily, quotaWeekly float64
	require.NoError(t, db.QueryRow(`SELECT daily_usage_usd, weekly_usage_usd FROM user_subscriptions WHERE id = 5`).Scan(&quotaDaily, &quotaWeekly))
	require.Equal(t, 18.0, quotaDaily)
	require.Equal(t, 66.0, quotaWeekly)

	// Audit source recorded as 'vote'.
	var actionCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM custom_codex_reset_actions WHERE account_id = 10 AND source = 'vote'`).Scan(&actionCount))
	require.Equal(t, 4, actionCount)

	// Re-initiating before 24 hours is blocked.
	_, err = svc.CastVote(ctx, 1, 10)
	require.Equal(t, "RESET_VOTE_COOLDOWN_ACTIVE", infraerrors.Reason(err))

	// Advance time past 24 hours -> fresh vote round can be initiated cleanly.
	now = now.Add(24*time.Hour + time.Second)
	reinitRes, err := svc.CastVote(ctx, 1, 10)
	require.NoError(t, err)
	require.True(t, reinitRes.VoteRecorded)
	require.False(t, reinitRes.TriggeredReset)
	require.Equal(t, 1, reinitRes.State.VoteCount)
	require.Equal(t, SubscriberResetVoteStatusOpen, reinitRes.State.Status)
}
