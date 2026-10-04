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

type customAccountScheduleIntegrationRepo struct {
	AccountRepository
	db *sql.DB
}

func (r *customAccountScheduleIntegrationRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	a := &Account{ID: id}
	var credentials []byte
	err := r.db.QueryRowContext(ctx, `SELECT platform,type,status,credentials FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&a.Platform, &a.Type, &a.Status, &credentials)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(credentials, &a.Credentials)
	return a, err
}

type customAccountScheduleIntegrationQuota struct {
	usage           *OpenAIQuotaUsage
	reset           func(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error)
	queries, resets atomic.Int32
}

func (q *customAccountScheduleIntegrationQuota) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	q.queries.Add(1)
	return q.usage, nil
}
func (q *customAccountScheduleIntegrationQuota) ResetCreditTargetedForSchedule(ctx context.Context, id int64, card, request, expectedIdentity string) (*OpenAIQuotaResetResult, error) {
	q.resets.Add(1)
	if expectedIdentity == "" {
		return nil, fmt.Errorf("missing pinned identity")
	}
	return q.reset(ctx, id, card, request)
}
func (*customAccountScheduleIntegrationQuota) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	return nil
}

func customAccountScheduleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("CUSTOM_SCHEDULE_TEST_DSN")
	if dsn == "" {
		container, err := postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("schedules_test"), postgres.WithUsername("postgres"), postgres.WithPassword("schedules-test-only"), postgres.BasicWaitStrategies())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)
	}
	original, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("custom_schedules_%d", time.Now().UnixNano())
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
	db.SetMaxOpenConns(40)
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,platform TEXT,type TEXT,status TEXT,credentials JSONB DEFAULT '{"chatgpt_account_id":"stable-id"}',deleted_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id),group_id BIGINT REFERENCES groups(id),PRIMARY KEY(account_id,group_id));
 CREATE TABLE user_subscriptions(id BIGINT PRIMARY KEY,user_id BIGINT,group_id BIGINT,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,daily_usage_usd NUMERIC(20,10),weekly_usage_usd NUMERIC(20,10),monthly_usage_usd NUMERIC(20,10),daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{"241_custom_codex_subscription_reset.sql", "242_custom_codex_reset_history.sql", "243_custom_codex_window_probes.sql", "248_custom_account_action_schedules.sql"} {
		migration, e := migrations.FS.ReadFile(name)
		require.NoError(t, e)
		_, e = db.Exec(string(migration))
		require.NoError(t, e)
	}
	// Applying the new migration again must preserve all tables and constraints.
	migration, err := migrations.FS.ReadFile("248_custom_account_action_schedules.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	return db
}

func TestCustomAccountSchedulesTransactions(t *testing.T) {
	db := customAccountScheduleTestDB(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.Exec(query, args...)
		require.NoError(t, err)
	}
	seed := func() {
		exec(`TRUNCATE custom_account_action_schedule_runs,custom_account_action_schedules,custom_codex_reset_actions,custom_codex_reset_targets,custom_codex_reset_jobs,custom_codex_reset_events,custom_codex_reset_policy,custom_codex_reset_card_attempts,user_subscriptions,account_groups,groups,accounts CASCADE`)
		exec(`INSERT INTO accounts(id,platform,type,status) VALUES(1,'openai','oauth','active'),(2,'openai','oauth','active');
 INSERT INTO groups(id,name) VALUES(1,'subscribers'),(2,'different');INSERT INTO account_groups VALUES(1,1),(2,2);
 INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start) VALUES
 (1,1,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',10,40,90,NOW(),NOW()),
 (2,2,2,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',20,50,100,NOW(),NOW());`)
	}
	newService := func() *CustomAccountScheduleService {
		repo := &customAccountScheduleIntegrationRepo{db: db}
		resets := NewCustomCodexResetService(db, repo, &SubscriptionService{})
		t.Cleanup(resets.Stop)
		s := NewCustomAccountScheduleService(db, repo, nil, nil, resets, "Asia/Shanghai")
		t.Cleanup(s.Stop)
		return s
	}
	create := func(s *CustomAccountScheduleService, action, frequency string) *CustomAccountSchedule {
		t.Helper()
		input := CustomAccountScheduleInput{Action: action, Frequency: frequency, Timezone: "Asia/Shanghai", Enabled: true}
		switch frequency {
		case "once":
			input.RunAt = time.Now().Add(time.Hour).Format(time.RFC3339)
		case "daily":
			input.TimeOfDay = "08:00"
		case "weekly":
			input.TimeOfDay = "08:00"
			day := 0
			input.Weekday = &day
		case "cron":
			input.CronExpression = "*/5 * * * *"
		}
		v, err := s.Create(ctx, 1, input, 123)
		require.NoError(t, err)
		return v
	}
	makeDue := func(v *CustomAccountSchedule, ago time.Duration) {
		t.Helper()
		when := time.Now().Add(-ago).UTC()
		exec(`UPDATE custom_account_action_schedules SET next_run_at=$2,run_at=CASE WHEN frequency='once' THEN $2 ELSE run_at END WHERE id=$1`, v.ID, when)
	}
	load := func(s *CustomAccountScheduleService, id int64) *CustomAccountSchedule {
		t.Helper()
		view, err := s.List(ctx, 1)
		require.NoError(t, err)
		for _, v := range view.Items {
			if v.ID == id {
				return &v
			}
		}
		t.Fatalf("missing schedule %d", id)
		return nil
	}
	for _, frequency := range []string{"once", "daily", "weekly", "cron"} {
		t.Run("concurrent claim "+frequency, func(t *testing.T) {
			seed()
			s := newService()
			v := create(s, "reset_card", frequency)
			makeDue(v, time.Minute)
			var claims atomic.Int32
			var wg sync.WaitGroup
			errs := make(chan error, 20)
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, r, err := s.claim(ctx)
					if r != nil {
						claims.Add(1)
					}
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			require.Equal(t, int32(1), claims.Load())
			runs, err := s.Runs(ctx, 1, v.ID, 100)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			require.Equal(t, "started", runs[0].Status)
			stored := load(s, v.ID)
			if frequency == "once" {
				require.False(t, stored.Enabled)
				require.Nil(t, stored.NextRunAt)
			} else {
				require.True(t, stored.Enabled)
				require.True(t, stored.NextRunAt.After(time.Now()))
			}
			_, _, err = s.claim(ctx)
			require.NoError(t, err)
			runs, err = s.Runs(ctx, 1, v.ID, 100)
			require.NoError(t, err)
			require.Len(t, runs, 1, "a started slot is never retried")
		})
	}
	t.Run("missed slots skip all IO and jump to future", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "daily")
		makeDue(v, 72*time.Hour)
		q := &customAccountScheduleIntegrationQuota{}
		s.quota = q
		require.NoError(t, s.work(ctx))
		require.Zero(t, q.queries.Load())
		require.Zero(t, q.resets.Load())
		runs, err := s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, "skipped", runs[0].Status)
		require.NotNil(t, runs[0].FinishedAt)
		stored := load(s, v.ID)
		require.True(t, stored.Enabled)
		require.True(t, stored.NextRunAt.After(time.Now()))
		require.NoError(t, s.work(ctx))
		runs, err = s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1, "do not replay missed daily slots")
	})
	t.Run("crash never repeats redemption and pauses", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "cron")
		makeDue(v, time.Minute)
		_, r, err := s.claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, r)
		_, err = s.Update(ctx, 1, v.ID, CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00", Enabled: true}, 123)
		require.Equal(t, "ACCOUNT_SCHEDULE_RUNNING", infraerrors.Reason(err))
		exec(`UPDATE custom_account_action_schedule_runs SET started_at=NOW()-INTERVAL '6 minutes' WHERE id=$1`, r.ID)
		other := newService()
		q := &customAccountScheduleIntegrationQuota{}
		other.quota = q
		require.NoError(t, other.work(ctx))
		require.Zero(t, q.resets.Load())
		stored := load(other, v.ID)
		require.False(t, stored.Enabled)
		require.Equal(t, "unknown", stored.LastStatus)
		runs, err := other.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, "unknown", runs[0].Status)
	})
	t.Run("unique slot cannot be replayed after a failed run", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "daily")
		makeDue(v, time.Minute)
		claimed, r, err := s.claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.NoError(t, s.finish(ctx, claimed, r, customAccountScheduleResult{status: "failed", message: "No card"}))
		exec(`UPDATE custom_account_action_schedules SET enabled=TRUE,next_run_at=$2 WHERE id=$1`, v.ID, r.ScheduledAt)
		_, retry, err := s.claim(ctx)
		require.NoError(t, err)
		require.Nil(t, retry)
		runs, err := s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
	})
	t.Run("slow execution does not replay elapsed Cron intervals", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "cron")
		makeDue(v, time.Minute)
		claimed, r, err := s.claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, r)
		exec(`UPDATE custom_account_action_schedules SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, v.ID)
		require.NoError(t, s.finish(ctx, claimed, r, customAccountScheduleResult{status: "failed", message: "No card"}))
		stored := load(s, v.ID)
		require.True(t, stored.Enabled)
		require.True(t, stored.NextRunAt.After(time.Now()))
		_, retry, err := s.claim(ctx)
		require.NoError(t, err)
		require.Nil(t, retry)
	})
	t.Run("scheduled reset includes new active subscribers with saved groups", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_subscriptions", "once")
		exec(`INSERT INTO user_subscriptions(id,user_id,group_id,status,starts_at,expires_at,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start) VALUES(3,3,1,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '2 days',30,60,120,NOW(),NOW())`)
		makeDue(v, time.Minute)
		require.NoError(t, s.work(ctx))
		stored := load(s, v.ID)
		require.False(t, stored.Enabled)
		require.Equal(t, "succeeded", stored.LastStatus, stored.LastMessage)
		runs, err := s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, 2, runs[0].ResetCount)
		var daily, weekly, monthly float64
		require.NoError(t, db.QueryRow(`SELECT daily_usage_usd,weekly_usage_usd,monthly_usage_usd FROM user_subscriptions WHERE id=3`).Scan(&daily, &weekly, &monthly))
		require.Zero(t, daily)
		require.Zero(t, weekly)
		require.Equal(t, 120.0, monthly)
		var untouched float64
		require.NoError(t, db.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=2`).Scan(&untouched))
		require.Equal(t, 20.0, untouched)
		var source, operation string
		var actor int64
		require.NoError(t, db.QueryRow(`SELECT source,operation_id,actor_id FROM custom_codex_reset_actions WHERE subscription_id=3`).Scan(&source, &operation, &actor))
		require.Equal(t, "scheduled", source)
		require.Contains(t, operation, "scheduled:1:")
		require.Equal(t, int64(123), actor)
		require.NoError(t, s.work(ctx))
		runs, err = s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
	})
	t.Run("changed groups pause without financial mutation", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_subscriptions", "daily")
		exec(`DELETE FROM account_groups WHERE account_id=1; INSERT INTO account_groups VALUES(1,2)`)
		makeDue(v, time.Minute)
		require.NoError(t, s.work(ctx))
		stored := load(s, v.ID)
		require.False(t, stored.Enabled)
		require.Equal(t, "failed", stored.LastStatus)
		var daily float64
		require.NoError(t, db.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=2`).Scan(&daily))
		require.Equal(t, 20.0, daily)
	})
	t.Run("successful targeted card is durable and never repeated", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "once")
		makeDue(v, time.Minute)
		q := &customAccountScheduleIntegrationQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "synthetic-card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
		q.reset = func(_ context.Context, id int64, card, request string) (*OpenAIQuotaResetResult, error) {
			var persisted string
			err := db.QueryRow(`SELECT credit_id FROM custom_account_action_schedule_runs WHERE request_id=$1 AND status='started'`, request).Scan(&persisted)
			if err != nil {
				return nil, err
			}
			if id != 1 || card != "synthetic-card" || persisted != card {
				return nil, fmt.Errorf("invalid targeted attempt")
			}
			return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
		}
		s.quota = q
		s.recoverer = &customAccountScheduleTestRecoverer{}
		require.NoError(t, s.work(ctx))
		require.Equal(t, int32(1), q.resets.Load())
		runs, err := s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, "succeeded", runs[0].Status)
		require.Equal(t, 2, runs[0].ResetCount)
		require.NoError(t, s.work(ctx))
		require.Equal(t, int32(1), q.resets.Load())
	})
	t.Run("Stop cancels the request and persists unknown without retry", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "daily")
		makeDue(v, time.Minute)
		started := make(chan struct{})
		q := &customAccountScheduleIntegrationQuota{usage: &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "synthetic-card", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
		q.reset = func(ctx context.Context, _ int64, _ string, _ string) (*OpenAIQuotaResetResult, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		s.quota = q
		s.Start()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not begin the mock request")
		}
		stopping := time.Now()
		s.Stop()
		require.Less(t, time.Since(stopping), 2*time.Second)
		stored := load(s, v.ID)
		require.False(t, stored.Enabled)
		require.Equal(t, "unknown", stored.LastStatus)
		runs, err := s.Runs(ctx, 1, v.ID, 20)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, "unknown", runs[0].Status)
		require.NotNil(t, runs[0].FinishedAt)
		require.Equal(t, int32(1), q.resets.Load())
	})
	t.Run("CRUD rejects another account and retains deleted audit", func(t *testing.T) {
		seed()
		s := newService()
		v := create(s, "reset_card", "daily")
		_, err := s.Update(ctx, 2, v.ID, CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00", Enabled: true}, 123)
		require.Equal(t, "ACCOUNT_SCHEDULE_NOT_FOUND", infraerrors.Reason(err))
		require.Equal(t, "ACCOUNT_SCHEDULE_NOT_FOUND", infraerrors.Reason(s.Delete(ctx, 2, v.ID)))
		_, err = s.Runs(ctx, 2, v.ID, 20)
		require.Equal(t, "ACCOUNT_SCHEDULE_NOT_FOUND", infraerrors.Reason(err))
		makeDue(v, 11*time.Minute)
		require.NoError(t, s.work(ctx))
		_, err = s.Update(ctx, 1, v.ID, CustomAccountScheduleInput{Action: "reset_card", Frequency: "daily", TimeOfDay: "12:00", Enabled: true}, 999)
		require.NoError(t, err)
		var actor int64
		require.NoError(t, db.QueryRow(`SELECT actor_id FROM custom_account_action_schedule_runs WHERE schedule_id=$1`, v.ID).Scan(&actor))
		require.Equal(t, int64(123), actor, "editing a plan must preserve the original run actor")
		require.NoError(t, s.Delete(ctx, 1, v.ID))
		view, err := s.List(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, view.Items)
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM custom_account_action_schedule_runs WHERE schedule_id=$1`, v.ID).Scan(&count))
		require.Equal(t, 1, count)
	})
}
