//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type customScheduledResetTestAccounts struct {
	AccountRepository
	db *sql.DB
}

func (r customScheduledResetTestAccounts) GetByID(ctx context.Context, id int64) (*Account, error) {
	a := &Account{ID: id}
	var credentials []byte
	if err := r.db.QueryRowContext(ctx, `SELECT platform,type,status,credentials FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id).
		Scan(&a.Platform, &a.Type, &a.Status, &credentials); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(credentials, &a.Credentials); err != nil {
		return nil, err
	}
	return a, nil
}

type customScheduledResetTestCache struct {
	BillingCache
	calls int
}

func (c *customScheduledResetTestCache) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	c.calls++
	return errors.New("synthetic subscription cache failure")
}

type customScheduledResetFixture struct {
	db          *sql.DB
	service     *CustomCodexResetService
	identity    string
	application string
}

func newCustomScheduledResetFixture(t *testing.T) *customScheduledResetFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn := os.Getenv("CUSTOM_RESET_TEST_DSN")
	if dsn == "" {
		container, err := postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("scheduled_reset_test"),
			postgres.WithUsername("postgres"), postgres.WithPassword("synthetic-reset-test-only"), postgres.BasicWaitStrategies())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "scheduled_reset_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	params := parsed.Query()
	params.Set("search_path", schema)
	params.Set("application_name", schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TABLE accounts(id BIGINT PRIMARY KEY,platform TEXT,type TEXT,status TEXT,credentials JSONB NOT NULL DEFAULT '{"chatgpt_account_id":"scheduled-stable-id"}',deleted_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id),group_id BIGINT REFERENCES groups(id),PRIMARY KEY(account_id,group_id));
 CREATE TABLE user_subscriptions(id BIGINT PRIMARY KEY,user_id BIGINT,group_id BIGINT,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,daily_usage_usd NUMERIC(20,10),weekly_usage_usd NUMERIC(20,10),monthly_usage_usd NUMERIC(20,10),daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{"241_custom_codex_subscription_reset.sql", "242_custom_codex_reset_history.sql", "243_custom_codex_window_probes.sql", "248_custom_account_action_schedules.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err, name)
	}
	repo := customScheduledResetTestAccounts{db: db}
	s := NewCustomCodexResetService(db, repo, &SubscriptionService{})
	t.Cleanup(s.cancel)
	return &customScheduledResetFixture{db: db, service: s, application: schema,
		identity: customResetIdentity(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{"chatgpt_account_id": "scheduled-stable-id"}})}
}

func (f *customScheduledResetFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.db.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func (f *customScheduledResetFixture) seed(t *testing.T) {
	t.Helper()
	f.service.subscriptions = &SubscriptionService{}
	f.exec(t, `TRUNCATE accounts,groups,user_subscriptions CASCADE`)
	f.exec(t, `INSERT INTO accounts(id,platform,type,status) VALUES(1,'openai','oauth','active');
 INSERT INTO groups(id,name) VALUES(1,'scheduled group'),(2,'unrelated group');
 INSERT INTO account_groups VALUES(1,1);
 INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,deleted_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start) VALUES
 (1,1,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',NULL,10,40,90,date_trunc('day',NOW()),NOW()-INTERVAL '1 day'),
 (2,2,1,'active',NOW()-INTERVAL '1 day',NOW()-INTERVAL '1 second',NULL,10,40,90,NOW(),NOW()),
 (3,3,1,'revoked',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',NULL,10,40,90,NOW(),NOW()),
 (4,4,2,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',NULL,10,40,90,NOW(),NOW()),
 (5,5,1,'active',NOW()+INTERVAL '1 day',NOW()+INTERVAL '2 days',NULL,10,40,90,NOW(),NOW()),
 (7,7,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',NOW(),10,40,90,NOW(),NOW());`)
}

func (f *customScheduledResetFixture) addSubscription(t *testing.T) {
	t.Helper()
	f.exec(t, `INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start)
 VALUES(6,6,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '3 days',20,50,100,NOW(),NOW())`)
}

type customScheduledResetSnapshot struct {
	daily, weekly, monthly  float64
	expires, starts         time.Time
	dailyStart, weeklyStart time.Time
	status                  string
}

func (f *customScheduledResetFixture) snapshots(t *testing.T) map[int64]customScheduledResetSnapshot {
	t.Helper()
	rows, err := f.db.Query(`SELECT id,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,expires_at,starts_at,daily_window_start,weekly_window_start,status FROM user_subscriptions ORDER BY id`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	result := map[int64]customScheduledResetSnapshot{}
	for rows.Next() {
		var id int64
		var snapshot customScheduledResetSnapshot
		require.NoError(t, rows.Scan(&id, &snapshot.daily, &snapshot.weekly, &snapshot.monthly,
			&snapshot.expires, &snapshot.starts, &snapshot.dailyStart, &snapshot.weeklyStart, &snapshot.status))
		result[id] = snapshot
	}
	require.NoError(t, rows.Err())
	return result
}

func (f *customScheduledResetFixture) actions(t *testing.T) int {
	t.Helper()
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM custom_codex_reset_actions`).Scan(&count))
	return count
}

func (f *customScheduledResetFixture) waitForLock(t *testing.T, queryPattern string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting bool
		err := f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE $2)`, f.application, queryPattern).Scan(&waiting)
		return err == nil && waiting
	}, 2*time.Second, 10*time.Millisecond, "query should wait for the conflicting row lock")
}

func TestCustomCodexScheduledResetTransactions(t *testing.T) {
	f := newCustomScheduledResetFixture(t)
	ctx := context.Background()
	t.Run("resolves current active subscriptions and preserves monthly usage and expiry", func(t *testing.T) {
		f.seed(t)
		view, err := f.service.Preview(ctx, 1)
		require.NoError(t, err)
		require.Len(t, view.Subscriptions, 1)
		// A subscription acquired after schedule creation participates at execution.
		f.addSubscription(t)
		before := f.snapshots(t)
		operation := uuid.NewString()
		count, err := f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Equal(t, 2, count)
		after := f.snapshots(t)
		for id, prior := range before {
			current := after[id]
			require.Equal(t, prior.monthly, current.monthly, "subscription %d monthly usage", id)
			require.True(t, prior.expires.Equal(current.expires), "subscription %d expiry", id)
			require.True(t, prior.starts.Equal(current.starts), "subscription %d start", id)
			require.Equal(t, prior.status, current.status, "subscription %d status", id)
			if id == 1 || id == 6 {
				require.Zero(t, current.daily)
				require.Zero(t, current.weekly)
			} else {
				require.Equal(t, prior, current, "ineligible or unrelated subscription %d", id)
			}
		}
		rows, err := f.db.Query(`SELECT subscription_id,source,actor_id,daily_before,weekly_before,daily_after,weekly_after FROM custom_codex_reset_actions WHERE operation_id=$1 ORDER BY subscription_id`, fmt.Sprintf("scheduled:1:%s", operation))
		require.NoError(t, err)
		defer func() { require.NoError(t, rows.Close()) }()
		ids := []int64{}
		for rows.Next() {
			var id, actor int64
			var source string
			var daily, weekly, afterDaily, afterWeekly float64
			require.NoError(t, rows.Scan(&id, &source, &actor, &daily, &weekly, &afterDaily, &afterWeekly))
			ids = append(ids, id)
			require.Equal(t, "scheduled", source)
			require.EqualValues(t, 99, actor)
			require.Equal(t, before[id].daily, daily)
			require.Equal(t, before[id].weekly, weekly)
			require.Zero(t, afterDaily)
			require.Zero(t, afterWeekly)
		}
		require.NoError(t, rows.Err())
		require.Equal(t, []int64{1, 6}, ids)
	})

	t.Run("operation replay preserves new charges and subscriptions acquired later", func(t *testing.T) {
		f.seed(t)
		operation := uuid.NewString()
		count, err := f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		f.exec(t, `UPDATE user_subscriptions SET daily_usage_usd=3,weekly_usage_usd=7 WHERE id=1`)
		f.addSubscription(t)
		before := f.snapshots(t)
		count, err = f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, before, f.snapshots(t))
		require.Equal(t, 1, f.actions(t))
	})

	t.Run("an empty completed operation does not reset a later subscription on replay", func(t *testing.T) {
		f.seed(t)
		f.exec(t, `UPDATE user_subscriptions SET status='revoked' WHERE id=1`)
		operation := uuid.NewString()
		count, err := f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Zero(t, count)
		f.addSubscription(t)
		before := f.snapshots(t)
		count, err = f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, before, f.snapshots(t))
		require.Zero(t, f.actions(t))
	})

	for _, tc := range []struct{ name, mutation, reason string }{
		{"added group", `INSERT INTO account_groups VALUES(1,2)`, "ACCOUNT_SCHEDULE_GROUPS_CHANGED"},
		{"removed group", `DELETE FROM account_groups WHERE account_id=1`, "ACCOUNT_SCHEDULE_GROUPS_CHANGED"},
		{"deleted group", `UPDATE groups SET deleted_at=NOW() WHERE id=1`, "ACCOUNT_SCHEDULE_GROUPS_CHANGED"},
		{"changed upstream identity", `UPDATE accounts SET credentials='{"chatgpt_account_id":"changed-identity"}' WHERE id=1`, "ACCOUNT_SCHEDULE_IDENTITY_CHANGED"},
	} {
		t.Run(tc.name+" leaves every subscription unchanged", func(t *testing.T) {
			f.seed(t)
			f.exec(t, tc.mutation)
			before := f.snapshots(t)
			count, err := f.service.Scheduled(ctx, 1, uuid.NewString(), []int64{1}, 99, f.identity)
			require.Error(t, err)
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.Zero(t, count)
			require.Equal(t, before, f.snapshots(t))
			require.Zero(t, f.actions(t))
		})
	}

	t.Run("cache failure reports a committed reset and invalidates all targets", func(t *testing.T) {
		f.seed(t)
		f.addSubscription(t)
		cache := &customScheduledResetTestCache{}
		f.service.subscriptions = &SubscriptionService{billingCacheService: &BillingCacheService{cache: cache}}
		operation := uuid.NewString()
		count, err := f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.ErrorIs(t, err, ErrScheduledSubscriptionCacheRefresh)
		require.Equal(t, 2, count)
		require.Equal(t, 2, cache.calls)
		for _, id := range []int64{1, 6} {
			require.Zero(t, f.snapshots(t)[id].daily)
			require.Zero(t, f.snapshots(t)[id].weekly)
		}
		require.Equal(t, 2, f.actions(t))
		f.exec(t, `UPDATE user_subscriptions SET daily_usage_usd=3,weekly_usage_usd=7 WHERE id IN(1,6)`)
		before := f.snapshots(t)
		count, err = f.service.Scheduled(ctx, 1, operation, []int64{1}, 99, f.identity)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, before, f.snapshots(t))
	})

	t.Run("a conflicting binding lock cancels without changing usage", func(t *testing.T) {
		f.seed(t)
		before := f.snapshots(t)
		tx, err := f.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, `SELECT 1 FROM account_groups WHERE account_id=1 FOR UPDATE`)
		require.NoError(t, err)
		deadline, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		count, err := f.service.Scheduled(deadline, 1, uuid.NewString(), []int64{1}, 99, f.identity)
		require.Error(t, err)
		require.ErrorIs(t, deadline.Err(), context.DeadlineExceeded)
		require.Zero(t, count)
		require.Equal(t, before, f.snapshots(t))
		require.Zero(t, f.actions(t))
	})

	t.Run("deleting a binding cannot pass the execution scope check", func(t *testing.T) {
		f.seed(t)
		blocked, err := f.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = blocked.Rollback() }()
		_, err = blocked.ExecContext(ctx, `SELECT 1 FROM user_subscriptions WHERE id=1 FOR UPDATE`)
		require.NoError(t, err)
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		type scheduledResult struct {
			count int
			err   error
		}
		scheduled := make(chan scheduledResult, 1)
		go func() {
			count, err := f.service.Scheduled(execCtx, 1, uuid.NewString(), []int64{1}, 99, f.identity)
			scheduled <- scheduledResult{count, err}
		}()
		// The reset has locked its scope and is waiting for a subscription writer.
		f.waitForLock(t, "%FOR UPDATE OF us")
		deleted := make(chan error, 1)
		go func() {
			_, err := f.db.ExecContext(execCtx, `DELETE FROM account_groups WHERE account_id=1 AND group_id=1`)
			deleted <- err
		}()
		f.waitForLock(t, "DELETE FROM account_groups%")
		require.NoError(t, blocked.Commit())
		result := <-scheduled
		require.NoError(t, result.err)
		require.Equal(t, 1, result.count)
		require.NoError(t, <-deleted)
		var bindings int
		require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM account_groups WHERE account_id=1`).Scan(&bindings))
		require.Zero(t, bindings)
		require.Zero(t, f.snapshots(t)[1].daily)
		require.Zero(t, f.snapshots(t)[1].weekly)
		require.Equal(t, 1, f.actions(t))
	})
}
