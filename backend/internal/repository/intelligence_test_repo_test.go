package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var intelligenceTestMockColumns = []string{
	"id", "account_id", "platform", "model", "reasoning_effort", "prompt",
	"status", "created_at", "started_at", "completed_at", "duration_ms", "output", "error",
}

func expectIntelligenceTestLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock\(244, 1\)`).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestIntelligenceTestRepositoryRejectsFullQueueBeforeInsertion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*status IN \('queued', 'running'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(20))
	mock.ExpectRollback()
	err = NewIntelligenceTestRepository(db).Create(context.Background(), &service.IntelligenceTest{})
	require.ErrorIs(t, err, service.ErrIntelligenceTestQueueFull)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryClaimsWithGlobalCapacityAndSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)UPDATE intelligence_tests.*status = 'running'.*COUNT\(\*\).*status = 'running'\) < \$1.*FOR UPDATE SKIP LOCKED.*RETURNING`).
		WithArgs(2).
		WillReturnRows(sqlmock.NewRows(intelligenceTestMockColumns).
			AddRow(4, 12, "openai", "model", "high", "prompt", "running", now, now, nil, 0, "", ""))
	mock.ExpectCommit()
	item, err := NewIntelligenceTestRepository(db).ClaimNext(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(4), item.ID)
	require.Equal(t, int64(12), item.AccountID)
	require.NotNil(t, item.StartedAt)
	require.Nil(t, item.CompletedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryEmptyClaimReturnsNil(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	mock.ExpectQuery("UPDATE intelligence_tests").WithArgs(2).
		WillReturnRows(sqlmock.NewRows(intelligenceTestMockColumns))
	mock.ExpectCommit()
	item, err := NewIntelligenceTestRepository(db).ClaimNext(context.Background())
	require.NoError(t, err)
	require.Nil(t, item)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryFinishCannotOverwriteRecoveredTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	now := time.Now().UTC()
	mock.ExpectExec(`(?s)UPDATE intelligence_tests.*WHERE id = \$1 AND status = 'running'`).
		WithArgs(int64(4), "succeeded", now, int64(123), "<svg/>", "").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err = NewIntelligenceTestRepository(db).Finish(context.Background(), &service.IntelligenceTest{
		ID: 4, Status: "succeeded", CompletedAt: &now, DurationMS: 123, Output: "<svg/>",
	})
	require.ErrorIs(t, err, service.ErrIntelligenceTestNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryFinishRollsBackIfRetentionFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	now := time.Now().UTC()
	mock.ExpectExec("UPDATE intelligence_tests").
		WithArgs(int64(4), "succeeded", now, int64(123), "<svg/>", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)DELETE FROM intelligence_tests.*status IN \('succeeded', 'failed'\).*OFFSET \$1`).
		WithArgs(10).WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	err = NewIntelligenceTestRepository(db).Finish(context.Background(), &service.IntelligenceTest{
		ID: 4, Status: "succeeded", CompletedAt: &now, DurationMS: 123, Output: "<svg/>",
	})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryPublicListOmitsPrivateAccountAndOutput(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT id, 0 AS account_id.*'' AS output.*ORDER BY created_at DESC, id DESC`).
		WillReturnRows(sqlmock.NewRows(intelligenceTestMockColumns).
			AddRow(4, 0, "openai", "model", "high", "prompt", "succeeded", now, now, now, 123, "", ""))
	items, err := NewIntelligenceTestRepository(db).List(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Zero(t, items[0].AccountID)
	require.Empty(t, items[0].Output)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligenceTestRepositoryRecoveryKeepsOnlyFinishedRetention(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectIntelligenceTestLock(mock)
	cutoff := time.Now().UTC().Add(-time.Hour)
	mock.ExpectExec(`(?s)UPDATE intelligence_tests.*status = 'failed'.*output = '', error = 'Test execution.*WHERE status = 'running' AND started_at < \$1`).
		WithArgs(cutoff).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)DELETE FROM intelligence_tests.*status IN \('succeeded', 'failed'\).*OFFSET \$1`).
		WithArgs(10).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, NewIntelligenceTestRepository(db).RecoverInterrupted(context.Background(), cutoff))
	require.NoError(t, mock.ExpectationsWereMet())
}
