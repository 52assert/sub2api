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

type uaLimitSettingRepoStub struct {
	SettingRepository
	values map[string]string
	getErr error
	setErr error
}

func (s *uaLimitSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	v, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (s *uaLimitSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func newTestUALimitService(t *testing.T, settings *CustomUserAgentLimitSettings) (*CustomUserAgentLimitService, sqlmock.Sqlmock, *uaLimitSettingRepoStub) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &uaLimitSettingRepoStub{values: make(map[string]string)}
	svc := ProvideCustomUserAgentLimitService(db, repo)
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }

	if settings != nil {
		_, err := svc.UpdateSettings(context.Background(), *settings)
		require.NoError(t, err)
	}
	return svc, mock, repo
}

func TestCustomUserAgentLimitService_SettingsLifecycle(t *testing.T) {
	svc, _, repo := newTestUALimitService(t, nil)

	got, err := svc.GetSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultCustomUserAgentLimitSettings(), *got)

	_, err = svc.UpdateSettings(context.Background(), CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 0,
		WindowDays:    30,
	})
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err))

	_, err = svc.UpdateSettings(context.Background(), CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 5,
		WindowDays:    366,
	})
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err))

	updated, err := svc.UpdateSettings(context.Background(), CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 8,
		WindowDays:    14,
		ExemptAdmin:   false,
	})
	require.NoError(t, err)
	require.Equal(t, 8, updated.MaxUserAgents)
	require.Equal(t, 14, updated.WindowDays)
	require.False(t, updated.ExemptAdmin)
	require.NotEmpty(t, repo.values[SettingKeyCustomUserAgentLimitSettings])
}

func TestCustomUserAgentLimitService_CheckAndRecord_AllowsExistingAndRejectsSixth(t *testing.T) {
	svc, mock, _ := newTestUALimitService(t, &CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 5,
		WindowDays:    30,
		ExemptAdmin:   true,
	})
	ctx := context.Background()

	// 1. Existing active UA updates without counting against a new slot and gets touch-cached.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := svc.CheckAndRecord(ctx, 42, RoleUser, "Codex Desktop/0.169.1 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1111.71704)")
	require.NoError(t, err)

	// 2. Version upgrade on the same normalized environment hits touchCache with zero DB queries.
	err = svc.CheckAndRecord(ctx, 42, RoleUser, "Codex Desktop/0.171.0-alpha.13 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1117.31558)")
	require.NoError(t, err)

	// 3. New environment when user already has 5 active entries in last 30 days -> UserAgentLimitExceededError.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM custom_user_agent_records WHERE user_id = $1 AND last_seen_at >= $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectRollback()

	err = svc.CheckAndRecord(ctx, 42, RoleUser, "Codex Desktop/0.171.0-alpha.13 (Mac OS 27.2.0; arm64) unknown (Codex Desktop; 26.1117.31558)")
	var limitErr *UserAgentLimitExceededError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, int64(42), limitErr.UserID)
	require.Equal(t, 5, limitErr.ActiveCount)
	require.Equal(t, 5, limitErr.MaxAllowed)
	require.Equal(t, 30, limitErr.WindowDays)
	require.Equal(t, "Codex Desktop (Mac OS 27.2.0; arm64)", limitErr.NormalizedUA)

	// 4. First UA (already in touchCache) still succeeds cleanly even when limit was hit by a 6th UA.
	err = svc.CheckAndRecord(ctx, 42, RoleUser, "Codex Desktop/0.169.1 (Mac OS 26.3.1; arm64)")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCustomUserAgentLimitService_CheckAndRecord_AdminExemptAndPolicyOverride(t *testing.T) {
	svc, mock, _ := newTestUALimitService(t, &CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 5,
		WindowDays:    30,
		ExemptAdmin:   true,
	})
	ctx := context.Background()

	// Admin without per-user override: exempt -> records 6th+ UA without counting limit check.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(1)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, svc.CheckAndRecord(ctx, 1, RoleAdmin, "Go-http-client/2.0"))

	// Regular user with explicit exempt override (max_user_agents = 0) -> skips limit check and inserts.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(77)).
		WillReturnRows(sqlmock.NewRows([]string{"max_user_agents", "note", "updated_at"}).AddRow(0, "whitelist", svc.nowFunc()))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	require.NoError(t, svc.CheckAndRecord(ctx, 77, RoleUser, "OpenAI/Python 3.51.0"))

	// Regular user with custom limit = 2 -> rejected when active_count = 2.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(88)).
		WillReturnRows(sqlmock.NewRows([]string{"max_user_agents", "note", "updated_at"}).AddRow(2, "tight limit", svc.nowFunc()))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE custom_user_agent_records`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM custom_user_agent_records WHERE user_id = $1 AND last_seen_at >= $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectRollback()

	err := svc.CheckAndRecord(ctx, 88, RoleUser, "curl/8.7.1")
	var limitErr *UserAgentLimitExceededError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, 2, limitErr.MaxAllowed)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCustomUserAgentLimitService_DisabledAndFailOpen(t *testing.T) {
	// Disabled setting short-circuits without touching DB.
	svc, mock, _ := newTestUALimitService(t, &CustomUserAgentLimitSettings{
		Enabled:       false,
		MaxUserAgents: 5,
		WindowDays:    30,
		ExemptAdmin:   true,
	})
	require.NoError(t, svc.CheckAndRecord(context.Background(), 42, RoleUser, "curl/8.7.1"))

	// Unexpected DB begin failure fails open (does not return UserAgentLimitExceededError).
	svcEnabled, mockEnabled, _ := newTestUALimitService(t, &CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 5,
		WindowDays:    30,
		ExemptAdmin:   true,
	})
	mockEnabled.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mockEnabled.ExpectBegin().WillReturnError(errors.New("connection refused"))

	require.NoError(t, svcEnabled.CheckAndRecord(context.Background(), 42, RoleUser, "curl/8.7.1"))
	require.NoError(t, mock.ExpectationsWereMet())
	require.NoError(t, mockEnabled.ExpectationsWereMet())
}

func TestCustomUserAgentLimitService_AdminCRUDAndCacheInvalidation(t *testing.T) {
	svc, mock, _ := newTestUALimitService(t, &CustomUserAgentLimitSettings{
		Enabled:       true,
		MaxUserAgents: 5,
		WindowDays:    30,
		ExemptAdmin:   true,
	})
	ctx := context.Background()
	now := svc.nowFunc()
	svc.setCachedTouch(42, "curl", now)

	// DeleteUserRecord returns 404 when row is absent.
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM custom_user_agent_records WHERE id = $1 AND user_id = $2`)).
		WithArgs(int64(99), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := svc.DeleteUserRecord(ctx, 42, 99)
	require.Error(t, err)
	require.Equal(t, 404, infraerrors.Code(err))

	// DeleteUserRecord succeeds and clears touchCache for user 42.
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM custom_user_agent_records WHERE id = $1 AND user_id = $2`)).
		WithArgs(int64(10), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.DeleteUserRecord(ctx, 42, 10))
	require.False(t, svc.getCachedTouch(42, "curl", now))

	// ClearUserRecords checks user existence and deletes all records.
	svc.setCachedTouch(42, "openai/python", now)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM custom_user_agent_records WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	deleted, err := svc.ClearUserRecords(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.False(t, svc.getCachedTouch(42, "openai/python", now))

	// GetUsersSummaryBatch builds effective summaries across regular, admin, and overridden users.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, role FROM users WHERE id = ANY($1) AND deleted_at IS NULL`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).
			AddRow(int64(1), RoleAdmin).
			AddRow(int64(2), RoleUser).
			AddRow(int64(3), RoleUser))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id, COUNT(*)`)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "count"}).
			AddRow(int64(1), 8).
			AddRow(int64(2), 5).
			AddRow(int64(3), 7))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id, max_user_agents, note, updated_at`)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "max_user_agents", "note", "updated_at"}).
			AddRow(int64(3), 6, "vip", now))

	summaries, _, err := svc.GetUsersSummaryBatch(ctx, []int64{1, 2, 3})
	require.NoError(t, err)
	require.True(t, summaries[1].Exempt)
	require.Equal(t, 8, summaries[1].ActiveCount)
	require.False(t, summaries[2].Exempt)
	require.Equal(t, 5, summaries[2].EffectiveLimit)
	require.True(t, summaries[2].AtLimit)
	require.False(t, summaries[2].Exceeded)
	require.Equal(t, 6, summaries[3].EffectiveLimit)
	require.True(t, summaries[3].Exceeded)
	require.Equal(t, "vip", summaries[3].Note)

	require.NoError(t, mock.ExpectationsWereMet())
}
