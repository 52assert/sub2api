//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type customResetTransport func(*http.Request) (*http.Response, error)

func (f customResetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type customResetAccountRepo struct {
	AccountRepository
	extra map[string]any
}

func (r *customResetAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "active", Credentials: map[string]any{"chatgpt_account_id": "stable-id"}, Extra: r.extra}, nil
}
func TestCustomCodexResetTransactions(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("CUSTOM_RESET_TEST_DSN")
	if dsn == "" {
		container, err := postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("reset_test"), postgres.WithUsername("postgres"), postgres.WithPassword("reset-test-only"), postgres.BasicWaitStrategies())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("custom_reset_%d", time.Now().UnixNano())
	_, err = db.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := db.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, e)
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	scoped, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	original := db
	db = scoped
	_ = original.Close()
	// This test DSN is a disposable database only, never an application database.
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,platform TEXT,type TEXT,status TEXT,credentials JSONB DEFAULT '{"chatgpt_account_id":"stable-id"}',deleted_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id),group_id BIGINT REFERENCES groups(id),PRIMARY KEY(account_id,group_id));
 CREATE TABLE user_subscriptions(id BIGINT PRIMARY KEY,user_id BIGINT,group_id BIGINT,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,daily_usage_usd NUMERIC(20,10),weekly_usage_usd NUMERIC(20,10),monthly_usage_usd NUMERIC(20,10),daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{"241_custom_codex_subscription_reset.sql", "242_custom_codex_reset_history.sql"} {
		migration, e := migrations.FS.ReadFile(name)
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	exec := func(q string, args ...any) { t.Helper(); _, e := db.Exec(q, args...); require.NoError(t, e) }
	seed := func() {
		exec(`TRUNCATE custom_codex_reset_actions,custom_codex_reset_targets,custom_codex_reset_jobs,custom_codex_reset_events,custom_codex_reset_policy,custom_codex_reset_card_attempts,user_subscriptions,account_groups,groups,accounts CASCADE`)
		exec(`INSERT INTO accounts(id,platform,type,status) VALUES(1,'openai','oauth','active'),(2,'openai','oauth','active'); INSERT INTO groups(id,name) VALUES(1,'shared'),(2,'other'); INSERT INTO account_groups VALUES(1,1),(2,1);
 INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start) VALUES
 (1,1,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',10,40,90,date_trunc('day',NOW()),NOW()-INTERVAL '1 day'),
 (2,2,1,'active',NOW()-INTERVAL '1 day',NOW()-INTERVAL '1 second',10,40,90,NOW(),NOW()),
 (3,3,1,'revoked',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',10,40,90,NOW(),NOW()),
 (4,4,2,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',10,40,90,NOW(),NOW()),
 (5,5,1,'active',NOW()+INTERVAL '1 day',NOW()+INTERVAL '2 days',10,40,90,NOW(),NOW());`)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=NOW()-INTERVAL '2 hours'`)
	}
	subs := &SubscriptionService{}
	repo := &customResetAccountRepo{}
	s := NewCustomCodexResetService(db, repo, subs, nil)
	defer s.cancel()
	balance := func(id int64) (float64, float64, float64) {
		t.Helper()
		var d, w, m float64
		require.NoError(t, db.QueryRow(`SELECT daily_usage_usd,weekly_usage_usd,monthly_usage_usd FROM user_subscriptions WHERE id=$1`, id).Scan(&d, &w, &m))
		return d, w, m
	}
	observe := func(id string, announced time.Time) {
		t.Helper()
		tx, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		defer func() { _ = tx.Rollback() }()
		record := &customResetRecord{ID: id, AnnouncedAt: announced}
		require.NoError(t, s.observe(ctx, tx, record))
		require.NoError(t, tx.Commit())
	}
	enable := func() {
		exec(`INSERT INTO custom_codex_reset_policy(account_id,enabled,enabled_at,baseline) VALUES(1,true,NOW()-INTERVAL '1 hour',$1),(2,true,NOW()-INTERVAL '1 hour',$1)`, fmt.Sprintf(`{"identity":%q}`, customResetIdentity(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "stable-id"}})))
	}
	t.Run("poll is shared across instances and respects Retry-After", func(t *testing.T) {
		seed()
		enable()
		exec(`UPDATE custom_codex_reset_poll SET next_at=NOW()-INTERVAL '1 minute',failures=0`)
		var calls atomic.Int32
		s.client = &http.Client{Transport: customResetTransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, customCodexResetURL, r.URL.String())
			calls.Add(1)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"data":null}`))}, nil
		})}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); results <- s.poll(ctx) }()
		}
		wg.Wait()
		close(results)
		for e := range results {
			require.NoError(t, e)
		}
		require.Equal(t, int32(1), calls.Load())
		var next time.Time
		require.NoError(t, db.QueryRow(`SELECT next_at FROM custom_codex_reset_poll`).Scan(&next))
		require.InDelta(t, 599, time.Until(next).Seconds(), 3)
		exec(`UPDATE custom_codex_reset_poll SET next_at=NOW()-INTERVAL '1 minute'`)
		s.client = &http.Client{Transport: customResetTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		require.Error(t, s.poll(ctx))
		require.NoError(t, db.QueryRow(`SELECT next_at FROM custom_codex_reset_poll`).Scan(&next))
		require.Greater(t, time.Until(next), 59*time.Minute)
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		require.Equal(t, "feed_rate_limited", view.PollError)
		require.NotNil(t, view.NextCheck)
	})
	t.Run("manual scoped reset and replay preserve monthly and new charges", func(t *testing.T) {
		seed()
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		require.Len(t, view.Subscriptions, 1)
		n, e := s.Manual(ctx, 1, "request1", view.Fingerprint, 99)
		require.NoError(t, e)
		require.Equal(t, 1, n)
		d, w, m := balance(1)
		require.Equal(t, 0.0, d)
		require.Equal(t, 0.0, w)
		require.Equal(t, 90.0, m)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=3,weekly_usage_usd=3 WHERE id=1`)
		n, e = s.Manual(ctx, 1, "request1", view.Fingerprint, 99)
		require.NoError(t, e)
		require.Zero(t, n)
		d, w, _ = balance(1)
		require.Equal(t, 3.0, d)
		require.Equal(t, 3.0, w)
		for _, id := range []int64{2, 3, 4, 5} {
			d, w, m = balance(id)
			require.Equal(t, 10.0, d)
			require.Equal(t, 40.0, w)
			require.Equal(t, 90.0, m)
		}
	})
	t.Run("partial financial failure rolls back all subscriptions and audit", func(t *testing.T) {
		seed()
		exec(`INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd) VALUES(6,6,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day',10,20,30)`)
		exec(`ALTER TABLE user_subscriptions ADD CONSTRAINT reject_test_reset CHECK(id<>6 OR daily_usage_usd>0)`)
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		_, e = s.Manual(ctx, 1, "rollback", view.Fingerprint, 99)
		require.Error(t, e)
		d, w, _ := balance(1)
		require.Equal(t, 10.0, d)
		require.Equal(t, 40.0, w)
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM custom_codex_reset_actions`).Scan(&n))
		require.Zero(t, n)
		exec(`ALTER TABLE user_subscriptions DROP CONSTRAINT reject_test_reset`)
	})
	t.Run("deleted account cannot reset group subscriptions", func(t *testing.T) {
		seed()
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		exec(`UPDATE accounts SET deleted_at=NOW() WHERE id=1`)
		_, e = s.Manual(ctx, 1, "deleted", view.Fingerprint, 99)
		require.Error(t, e)
		d, _, _ := balance(1)
		require.Equal(t, 10.0, d)
	})
	t.Run("preview membership changes fail closed", func(t *testing.T) {
		seed()
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		exec(`INSERT INTO account_groups VALUES(1,2)`)
		_, e = s.Manual(ctx, 1, "changed", view.Fingerprint, 99)
		require.Error(t, e)
		d, _, _ := balance(1)
		require.Equal(t, 10.0, d)
	})
	t.Run("official shared groups preserve verification-period charges and deduplicate", func(t *testing.T) {
		seed()
		enable()
		observe("event1", time.Now().Add(-time.Minute))
		observe("event1", time.Now().Add(-time.Minute))
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+2.1234567891,weekly_usage_usd=weekly_usage_usd+2.1234567891 WHERE id=1`)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, id := range []int64{1, 2} {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				_, e := s.apply(ctx, id, "official:event1", "event1", "", 0)
				results <- e
			}(id)
		}
		wg.Wait()
		close(results)
		for e := range results {
			require.NoError(t, e)
		}
		d, w, m := balance(1)
		require.InDelta(t, 2.1234567891, d, 1e-10)
		require.InDelta(t, 2.1234567891, w, 1e-10)
		require.Equal(t, 90.0, m)
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM custom_codex_reset_actions`).Scan(&count))
		require.Equal(t, 1, count)
	})
	t.Run("announcement boundary preserves polling-delay and verification charges", func(t *testing.T) {
		seed()
		enable()
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+3,weekly_usage_usd=weekly_usage_usd+3 WHERE id=1`)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=NOW()-INTERVAL '9 minutes' WHERE subscription_id=1 AND daily_usage=13`)
		eventAt := time.Now().Add(-8 * time.Minute)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+2.1234567891,weekly_usage_usd=weekly_usage_usd+2.1234567891 WHERE id=1`)
		firstUse := time.Now().Add(-7 * time.Minute)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=$1 WHERE subscription_id=1 AND daily_usage>15`, firstUse)
		observe("delayed", eventAt)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+4,weekly_usage_usd=weekly_usage_usd+4 WHERE id=1`)
		n, e := s.apply(ctx, 1, "official:delayed", "delayed", "", 0)
		require.NoError(t, e)
		require.Equal(t, 1, n)
		d, w, m := balance(1)
		require.InDelta(t, 6.1234567891, d, 1e-10)
		require.InDelta(t, 6.1234567891, w, 1e-10)
		require.Equal(t, 90.0, m)
		var weeklyStart time.Time
		require.NoError(t, db.QueryRow(`SELECT weekly_window_start FROM user_subscriptions WHERE id=1`).Scan(&weeklyStart))
		require.WithinDuration(t, firstUse, weeklyStart, time.Microsecond)
	})
	t.Run("missing pre-announcement history never invents compensation", func(t *testing.T) {
		seed()
		enable()
		exec(`DELETE FROM custom_codex_subscription_history`)
		observe("missing-history", time.Now().Add(-time.Minute))
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM custom_codex_reset_jobs WHERE event_id='missing-history' AND account_id=1`).Scan(&status))
		require.Equal(t, "missing_subscription_history", status)
		_, e := s.apply(ctx, 1, "official:missing-history", "missing-history", "", 0)
		require.Error(t, e)
		d, w, _ := balance(1)
		require.Equal(t, 10.0, d)
		require.Equal(t, 40.0, w)
	})
	t.Run("idle reset starts weekly cycle on first charge and includes that charge", func(t *testing.T) {
		seed()
		enable()
		observe("idle", time.Now().Add(-time.Minute))
		_, e := s.apply(ctx, 1, "official:idle", "idle", "", 0)
		require.NoError(t, e)
		var weeklyStart *time.Time
		var pending bool
		require.NoError(t, db.QueryRow(`SELECT weekly_window_start,custom_codex_weekly_pending FROM user_subscriptions WHERE id=1`).Scan(&weeklyStart, &pending))
		require.Nil(t, weeklyStart)
		require.True(t, pending)
		firstUse := time.Now()
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+2.5,weekly_usage_usd=weekly_usage_usd+2.5,monthly_usage_usd=monthly_usage_usd+2.5 WHERE id=1`)
		require.NoError(t, db.QueryRow(`SELECT weekly_window_start,custom_codex_weekly_pending FROM user_subscriptions WHERE id=1`).Scan(&weeklyStart, &pending))
		require.NotNil(t, weeklyStart)
		require.WithinDuration(t, firstUse, *weeklyStart, 2*time.Second)
		require.False(t, pending)
		d, w, m := balance(1)
		require.Equal(t, 2.5, d)
		require.Equal(t, 2.5, w)
		require.Equal(t, 92.5, m)
	})
	t.Run("subscriptions in one group start at their own first consumption", func(t *testing.T) {
		seed()
		enable()
		exec(`INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start)
 SELECT 6,6,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start FROM user_subscriptions WHERE id=1`)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=NOW()-INTERVAL '20 minutes' WHERE subscription_id IN (1,6)`)
		eventAt := time.Now().Add(-10 * time.Minute)
		firstA, firstB := time.Now().Add(-8*time.Minute), time.Now().Add(-4*time.Minute)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+3,weekly_usage_usd=weekly_usage_usd+3 WHERE id IN (1,6)`)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=$1 WHERE subscription_id=1 AND is_charge`, firstA)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=$1 WHERE subscription_id=6 AND is_charge`, firstB)
		observe("separate-starts", eventAt)
		n, e := s.apply(ctx, 1, "official:separate-starts", "separate-starts", "", 0)
		require.NoError(t, e)
		require.Equal(t, 2, n)
		for id, want := range map[int64]time.Time{1: firstA, 6: firstB} {
			var start time.Time
			require.NoError(t, db.QueryRow(`SELECT weekly_window_start FROM user_subscriptions WHERE id=$1`, id).Scan(&start))
			require.WithinDuration(t, want, start, time.Microsecond)
			d, w, _ := balance(id)
			require.Equal(t, 3.0, d)
			require.Equal(t, 3.0, w)
		}
	})
	t.Run("manual reset supersedes a pending first-use cycle", func(t *testing.T) {
		seed()
		enable()
		observe("idle-manual", time.Now().Add(-time.Minute))
		_, e := s.apply(ctx, 1, "official:idle-manual", "idle-manual", "", 0)
		require.NoError(t, e)
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		_, e = s.Manual(ctx, 1, "manual-after-idle", view.Fingerprint, 99)
		require.NoError(t, e)
		var before, after time.Time
		require.NoError(t, db.QueryRow(`SELECT weekly_window_start FROM user_subscriptions WHERE id=1`).Scan(&before))
		exec(`UPDATE user_subscriptions SET daily_usage_usd=3,weekly_usage_usd=3 WHERE id=1`)
		require.NoError(t, db.QueryRow(`SELECT weekly_window_start FROM user_subscriptions WHERE id=1`).Scan(&after))
		require.Equal(t, before, after)
	})
	t.Run("quota history keeps pre-announcement evidence after new consumption", func(t *testing.T) {
		seed()
		enable()
		eventAt := time.Now().Add(-8 * time.Minute)
		repo.extra = map[string]any{"codex_usage_updated_at": eventAt.Add(-time.Minute).Format(time.RFC3339), "codex_7d_used_percent": 80.0, "codex_7d_window_minutes": 10080, "codex_7d_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
		a, e := repo.GetByID(ctx, 1)
		require.NoError(t, e)
		require.NoError(t, s.recordQuotaHistory(ctx, a))
		repo.extra["codex_usage_updated_at"] = time.Now().Format(time.RFC3339)
		repo.extra["codex_7d_used_percent"] = 12.0
		require.NoError(t, s.recordQuotaHistory(ctx, a))
		observe("history", eventAt)
		var raw []byte
		require.NoError(t, db.QueryRow(`SELECT baseline FROM custom_codex_reset_jobs WHERE event_id='history' AND account_id=1`).Scan(&raw))
		var before customQuotaSnapshot
		require.NoError(t, json.Unmarshal(raw, &before))
		require.Equal(t, 80.0, *before.Weekly)
		status, e := s.verify(ctx, 1, "history", eventAt, time.Now(), before, 0)
		require.NoError(t, e)
		require.Equal(t, "succeeded", status)
	})
	t.Run("capture waits for committed pre-announcement billing history", func(t *testing.T) {
		seed()
		enable()
		billing, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		defer func() { _ = billing.Rollback() }()
		_, e = billing.ExecContext(ctx, `UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+7,weekly_usage_usd=weekly_usage_usd+7 WHERE id=1`)
		require.NoError(t, e)
		var eventAt time.Time
		require.NoError(t, billing.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&eventAt))
		done := make(chan error, 1)
		go func() {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				done <- err
				return
			}
			defer func() { _ = tx.Rollback() }()
			if err = s.observe(ctx, tx, &customResetRecord{ID: "inflight", AnnouncedAt: eventAt}); err == nil {
				err = tx.Commit()
			}
			done <- err
		}()
		select {
		case e := <-done:
			t.Fatalf("capture must wait for billing commit: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		require.NoError(t, billing.Commit())
		require.NoError(t, <-done)
		var base float64
		require.NoError(t, db.QueryRow(`SELECT daily_base FROM custom_codex_reset_targets WHERE event_id='inflight' AND subscription_id=1`).Scan(&base))
		require.Equal(t, 17.0, base)
	})
	t.Run("first use five hours later resumes verification after exhausted probes", func(t *testing.T) {
		seed()
		enable()
		exec(`UPDATE custom_codex_reset_policy SET enabled_at=NOW()-INTERVAL '1 day'`)
		exec(`UPDATE custom_codex_subscription_history SET recorded_at=NOW()-INTERVAL '6 hours'`)
		eventAt := time.Now().Add(-5 * time.Hour)
		repo.extra = map[string]any{"codex_usage_updated_at": eventAt.Add(-time.Hour).Format(time.RFC3339), "codex_7d_used_percent": 80.0, "codex_7d_window_minutes": 10080, "codex_7d_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
		a, e := repo.GetByID(ctx, 1)
		require.NoError(t, e)
		before := customAccountSnapshot(a)
		require.NoError(t, s.recordQuotaHistory(ctx, a))
		observe("late-first-use", eventAt)
		status, e := s.verify(ctx, 1, "late-first-use", eventAt, eventAt, before, 3)
		require.NoError(t, e)
		require.Equal(t, "awaiting_usage", status)
		exec(`UPDATE custom_codex_reset_jobs SET status='awaiting_usage',attempts=3 WHERE account_id=1`)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+2,weekly_usage_usd=weekly_usage_usd+2 WHERE id=1`)
		repo.extra["codex_usage_updated_at"] = time.Now().Format(time.RFC3339)
		repo.extra["codex_7d_used_percent"] = 12.0
		require.NoError(t, s.work(ctx))
		require.NoError(t, db.QueryRow(`SELECT status FROM custom_codex_reset_jobs WHERE event_id='late-first-use' AND account_id=1`).Scan(&status))
		require.Equal(t, "succeeded", status)
		d, w, _ := balance(1)
		require.Equal(t, 2.0, d)
		require.Equal(t, 2.0, w)
	})
	t.Run("reauthorization after verification blocks official apply", func(t *testing.T) {
		seed()
		enable()
		observe("reauthorized", time.Now().Add(-time.Minute))
		exec(`UPDATE accounts SET credentials='{"chatgpt_account_id":"replacement"}' WHERE id=1`)
		_, e := s.apply(ctx, 1, "official:reauthorized", "reauthorized", "", 0)
		require.Error(t, e)
		d, w, _ := balance(1)
		require.Equal(t, 10.0, d)
		require.Equal(t, 40.0, w)
	})
	t.Run("card attempt blocks official apply and never resets subscription", func(t *testing.T) {
		seed()
		enable()
		observe("card", time.Now().Add(-time.Minute))
		release, e := beginCustomResetCard(ctx, db, 1)
		require.NoError(t, e)
		_, _, e = customResetAccountLock(ctx, db, 1)
		require.Error(t, e)
		release()
		_, e = s.apply(ctx, 1, "official:card", "card", "", 0)
		require.Error(t, e)
		d, w, _ := balance(1)
		require.Equal(t, 10.0, d)
		require.Equal(t, 40.0, w)
	})
	t.Run("disabled and newly enabled policies cannot apply old events", func(t *testing.T) {
		seed()
		enable()
		observe("disabled", time.Now().Add(-time.Minute))
		exec(`UPDATE custom_codex_reset_policy SET enabled_at=NOW() WHERE account_id=1`)
		_, e := s.apply(ctx, 1, "official:disabled", "disabled", "", 0)
		require.Error(t, e)
	})
	t.Run("manual reset supersedes pending official event", func(t *testing.T) {
		seed()
		enable()
		observe("pending", time.Now().Add(-time.Minute))
		view, e := s.Preview(ctx, 1)
		require.NoError(t, e)
		_, e = s.Manual(ctx, 1, "manual", view.Fingerprint, 99)
		require.NoError(t, e)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=4,weekly_usage_usd=4 WHERE id=1`)
		_, e = s.apply(ctx, 2, "official:pending", "pending", "", 0)
		require.NoError(t, e)
		d, w, _ := balance(1)
		require.Equal(t, 4.0, d)
		require.Equal(t, 4.0, w)
	})
	t.Run("existing daily-only reset preserves new charges despite unchanged anchors", func(t *testing.T) {
		seed()
		enable()
		observe("external-reset", time.Now().Add(-time.Minute))
		exec(`UPDATE user_subscriptions SET daily_usage_usd=0 WHERE id=1`)
		exec(`UPDATE user_subscriptions SET daily_usage_usd=daily_usage_usd+20,weekly_usage_usd=weekly_usage_usd+20 WHERE id=1`)
		n, e := s.apply(ctx, 1, "official:external-reset", "external-reset", "", 0)
		require.NoError(t, e)
		require.Zero(t, n)
		d, w, _ := balance(1)
		require.Equal(t, 20.0, d)
		require.Equal(t, 60.0, w)
	})
	t.Run("historical and out-of-order events do not create jobs", func(t *testing.T) {
		seed()
		enable()
		observe("old", time.Now().Add(-25*time.Hour))
		observe("new", time.Now().Add(-time.Minute))
		observe("late", time.Now().Add(-2*time.Minute))
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM custom_codex_reset_jobs WHERE event_id IN ('old','late')`).Scan(&n))
		require.Zero(t, n)
	})
	t.Run("fresh usage verifies without probe", func(t *testing.T) {
		seed()
		enable()
		eventAt := time.Now().Add(-time.Minute)
		observe("fresh", eventAt)
		repo.extra = map[string]any{"codex_usage_updated_at": time.Now().Format(time.RFC3339), "codex_7d_used_percent": 0.5, "codex_7d_window_minutes": 10080}
		high := 80.0
		before := customQuotaSnapshot{Identity: customResetIdentity(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "stable-id"}}), At: eventAt.Add(-time.Minute), Weekly: &high, WeeklyMinutes: 10080, WeeklyReset: time.Now().Add(time.Hour)}
		raw, e := json.Marshal(before)
		require.NoError(t, e)
		exec(`UPDATE custom_codex_reset_jobs SET baseline=$1`, string(raw))
		require.NoError(t, s.work(ctx))
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM custom_codex_reset_jobs WHERE event_id='fresh' AND account_id=1`).Scan(&status))
		require.Equal(t, "succeeded", status)
	})
}
