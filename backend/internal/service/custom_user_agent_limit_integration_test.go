//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func customUserAgentLimitTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("CUSTOM_SCHEDULE_TEST_DSN")
	if dsn == "" {
		container, err := postgres.Run(
			ctx,
			"postgres:17-alpine",
			postgres.WithDatabase("ua_limit_test"),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("ua-limit-test-only"),
			postgres.BasicWaitStrategies(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		require.NoError(t, err)
	}

	original, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("custom_ua_limit_%d", time.Now().UnixNano())
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
		CREATE TABLE users (
			id BIGINT PRIMARY KEY,
			username TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			deleted_at TIMESTAMPTZ
		);
	`)
	require.NoError(t, err)

	migration, err := migrations.FS.ReadFile("253_custom_user_agent_limits.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)

	return db
}

type uaIntegrationSettingRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
}

func (r *uaIntegrationSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (r *uaIntegrationSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func TestCustomUserAgentLimitEndToEnd(t *testing.T) {
	db := customUserAgentLimitTestDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, role) VALUES
		(1, 'admin', 'admin@example.com', 'admin'),
		(10, 'alice', 'alice@example.com', 'user');
	`)
	require.NoError(t, err)

	repo := &uaIntegrationSettingRepo{values: make(map[string]string)}
	svc := ProvideCustomUserAgentLimitService(db, repo)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	// 5 distinct environments allowed; client version upgrades deduplicate into slot #1.
	firstEnvs := []string{
		"Codex Desktop/0.169.1 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1111.71704)",
		"Codex Desktop/0.171.0-alpha.13 (Mac OS 27.2.0; arm64) unknown (Codex Desktop; 26.1117.31558)",
		"Codex Desktop/0.169.1 (Mac OS 26.3.1",
		"codex-tui/0.164.0 (Ubuntu 24.4.0; x86_64) vscode/1.147.0 (codex-tui; 0.171.0)",
		"OpenAI/Python 3.46.0",
	}
	for _, ua := range firstEnvs {
		require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, ua))
	}
	// Version upgrade of slot #1 & slot #5 must stay within 5.
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "Codex Desktop/0.171.0-alpha.13 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1117.41037)"))
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "OpenAI/Python 3.51.0"))

	// Bypass touchCache to verify concurrent 6th UA race safety under advisory lock.
	svc.invalidateAllCaches()
	err = svc.CheckAndRecord(ctx, 10, RoleUser, "Codex Desktop/0.171.0-alpha.13")
	var limitErr *UserAgentLimitExceededError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, 5, limitErr.ActiveCount)
	require.Equal(t, "Codex Desktop", limitErr.NormalizedUA)

	// Existing slot #2 continues to work after rejection.
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "Codex Desktop/0.171.0-alpha.13 (Mac OS 27.2.0; arm64) unknown (Codex Desktop; 26.1117.31558)"))

	// Verify request_count incremented properly for slot #1 (initial + upgrade = 2) and slot #5 (initial + upgrade = 2).
	detail, err := svc.GetUserDetail(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 5, detail.Summary.ActiveCount)
	require.True(t, detail.Summary.AtLimit)
	require.Len(t, detail.Records, 5)

	// Concurrent 5th slot contention after deleting one slot: only one of two new UAs may claim slot #5.
	var deleteTargetID int64
	for _, rec := range detail.Records {
		if rec.UALabel == "OpenAI/Python" {
			deleteTargetID = rec.ID
			break
		}
	}
	require.NotZero(t, deleteTargetID)
	require.NoError(t, svc.DeleteUserRecord(ctx, 10, deleteTargetID))

	svc.invalidateAllCaches()
	var succeeded atomic.Int32
	var rejected atomic.Int32
	var wg sync.WaitGroup
	for _, ua := range []string{"Go-http-client/2.0", "curl/8.7.1"} {
		wg.Add(1)
		go func(rawUA string) {
			defer wg.Done()
			if e := svc.CheckAndRecord(ctx, 10, RoleUser, rawUA); e == nil {
				succeeded.Add(1)
			} else {
				var le *UserAgentLimitExceededError
				require.ErrorAs(t, e, &le)
				rejected.Add(1)
			}
		}(ua)
	}
	wg.Wait()
	require.Equal(t, int32(1), succeeded.Load())
	require.Equal(t, int32(1), rejected.Load())

	// Advance clock by 31 days -> expired entries age out cleanly, and previously seen UAs can re-enter.
	now = now.Add(31 * 24 * time.Hour)
	svc.invalidateAllCaches()
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "curl/8.7.1"))
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "magpie/0.1.1546"))

	detailAfterExpiry, err := svc.GetUserDetail(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 2, detailAfterExpiry.Summary.ActiveCount)
	for _, rec := range detailAfterExpiry.Records {
		if rec.UALabel == "curl" || rec.UALabel == "magpie" {
			require.Equal(t, int64(1), rec.RequestCount)
		}
	}

	// Admin exempt by default even beyond 5 UAs.
	for i := 0; i < 7; i++ {
		require.NoError(t, svc.CheckAndRecord(ctx, 1, RoleAdmin, fmt.Sprintf("client-%d/1.0", i)))
	}

	// Per-user override to custom max 1 blocks when user already has 2 active UAs, exempt=0 lifts block.
	_, err = svc.UpdateUserPolicy(ctx, 10, intPtr(1), "restrict test")
	require.NoError(t, err)
	err = svc.CheckAndRecord(ctx, 10, RoleUser, "extra-client/1.0")
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, 1, limitErr.MaxAllowed)

	_, err = svc.UpdateUserPolicy(ctx, 10, intPtr(0), "exempt vip")
	require.NoError(t, err)
	require.NoError(t, svc.CheckAndRecord(ctx, 10, RoleUser, "extra-client/1.0"))

	// Clear records resets count to 0.
	cleared, err := svc.ClearUserRecords(ctx, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, cleared, int64(3))

	detailAfterClear, err := svc.GetUserDetail(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 0, detailAfterClear.Summary.ActiveCount)
	require.Empty(t, detailAfterClear.Records)
}
