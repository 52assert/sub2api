package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	intelligenceTestQueueLimit   = 20
	intelligenceTestRunningLimit = 2
	intelligenceTestRetention    = service.IntelligenceTestRetention
)

type intelligenceTestRepository struct {
	db *sql.DB
}

func NewIntelligenceTestRepository(db *sql.DB) service.IntelligenceTestRepository {
	return &intelligenceTestRepository{db: db}
}

// All admission/claim/retention transactions take this same transaction-scoped
// lock. They finish before upstream IO, including when several servers run workers.
const intelligenceTestLockSQL = `SELECT pg_advisory_xact_lock(244, 1)`

const intelligenceTestColumns = `id, COALESCE(account_id, 0), account_name, platform, model,
reasoning_effort, prompt, status, created_at, started_at, completed_at,
duration_ms, output, error, runner, runner_version, effective_model, artifact_name, final_message`

// Public listings omit both the private account identifier and generated output.
const intelligenceTestListColumns = `id, 0 AS account_id, account_name, platform, model,
reasoning_effort, prompt, status, created_at, started_at, completed_at,
duration_ms, '' AS output, error, runner, runner_version, effective_model, artifact_name, '' AS final_message`

func scanIntelligenceTest(scan func(...any) error) (*service.IntelligenceTest, error) {
	item := &service.IntelligenceTest{}
	var startedAt, completedAt sql.NullTime
	if err := scan(&item.ID, &item.AccountID, &item.AccountName, &item.Platform, &item.Model,
		&item.ReasoningEffort, &item.Prompt, &item.Status, &item.CreatedAt,
		&startedAt, &completedAt, &item.DurationMS, &item.Output, &item.Error,
		&item.Runner, &item.RunnerVersion, &item.EffectiveModel, &item.ArtifactName, &item.FinalMessage); err != nil {
		return nil, err
	}
	if startedAt.Valid {
		item.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	return item, nil
}

func (r *intelligenceTestRepository) beginLocked(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, intelligenceTestLockSQL); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *intelligenceTestRepository) Create(ctx context.Context, item *service.IntelligenceTest) error {
	if item == nil {
		return fmt.Errorf("nil intelligence test")
	}
	tx, err := r.beginLocked(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var outstanding int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM intelligence_tests
WHERE status IN ('queued', 'running')`).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding >= intelligenceTestQueueLimit {
		return service.ErrIntelligenceTestQueueFull
	}
	var accountID any
	if item.AccountID > 0 {
		accountID = item.AccountID
	}
	if item.Runner == "" {
		item.Runner = service.IntelligenceTestRunnerHTTP
	}
	created, err := scanIntelligenceTest(tx.QueryRowContext(ctx, `INSERT INTO intelligence_tests
(account_id, account_name, platform, model, reasoning_effort, prompt, runner)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+intelligenceTestColumns,
		accountID, item.AccountName, item.Platform, item.Model, item.ReasoningEffort, item.Prompt, item.Runner).Scan)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*item = *created
	return nil
}

func (r *intelligenceTestRepository) ClaimNext(ctx context.Context) (*service.IntelligenceTest, error) {
	tx, err := r.beginLocked(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanIntelligenceTest(tx.QueryRowContext(ctx, `UPDATE intelligence_tests
SET status = 'running', started_at = clock_timestamp()
WHERE id = (
    SELECT id FROM intelligence_tests
    WHERE status = 'queued'
      AND (SELECT COUNT(*) FROM intelligence_tests WHERE status = 'running') < $1
    ORDER BY created_at, id
    LIMIT 1 FOR UPDATE SKIP LOCKED
)
RETURNING `+intelligenceTestColumns, intelligenceTestRunningLimit).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func cleanupIntelligenceTests(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM intelligence_tests
WHERE id IN (
    SELECT id FROM intelligence_tests
    WHERE status IN ('succeeded', 'failed')
    ORDER BY created_at DESC, id DESC
    OFFSET $1
)`, intelligenceTestRetention)
	return err
}

func (r *intelligenceTestRepository) Finish(ctx context.Context, item *service.IntelligenceTest) error {
	if item == nil {
		return fmt.Errorf("nil intelligence test")
	}
	if item.Status != "succeeded" && item.Status != "failed" {
		return fmt.Errorf("invalid intelligence test terminal status")
	}
	if item.DurationMS < 0 {
		return fmt.Errorf("invalid intelligence test duration")
	}
	completedAt := item.CompletedAt
	if completedAt == nil {
		now := time.Now().UTC()
		completedAt = &now
	}
	tx, err := r.beginLocked(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE intelligence_tests
SET status = $2, completed_at = $3, duration_ms = $4, output = $5, error = $6,
    runner_version = $7, effective_model = $8, artifact_name = $9, final_message = $10
WHERE id = $1 AND status = 'running'`, item.ID, item.Status, completedAt,
		item.DurationMS, item.Output, item.Error, item.RunnerVersion, item.EffectiveModel, item.ArtifactName, item.FinalMessage)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return service.ErrIntelligenceTestNotFound
	}
	if err = cleanupIntelligenceTests(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	item.CompletedAt = completedAt
	return nil
}

func (r *intelligenceTestRepository) List(ctx context.Context) ([]*service.IntelligenceTest, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+intelligenceTestListColumns+`
FROM intelligence_tests ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.IntelligenceTest, 0, intelligenceTestRetention)
	for rows.Next() {
		item, scanErr := scanIntelligenceTest(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *intelligenceTestRepository) GetByID(ctx context.Context, id int64) (*service.IntelligenceTest, error) {
	item, err := scanIntelligenceTest(r.db.QueryRowContext(ctx, `SELECT `+intelligenceTestColumns+`
FROM intelligence_tests WHERE id = $1`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrIntelligenceTestNotFound
	}
	return item, err
}

func (r *intelligenceTestRepository) RecoverInterrupted(ctx context.Context, cutoff time.Time) error {
	tx, err := r.beginLocked(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE intelligence_tests
SET status = 'failed', completed_at = clock_timestamp(),
    duration_ms = GREATEST(0, (EXTRACT(EPOCH FROM (clock_timestamp() - started_at)) * 1000)::BIGINT),
    output = '', error = 'Test execution was interrupted or timed out. Please submit a new test.'
WHERE status = 'running' AND started_at < $1`, cutoff.UTC())
	if err != nil {
		return err
	}
	if err = cleanupIntelligenceTests(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
