package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const customAccountScheduleColumns = `id,account_id,action,frequency,timezone,run_at,time_of_day,cron_expression,weekday,enabled,next_run_at,last_run_at,last_status,last_message,created_at,identity_snapshot,group_ids,actor_id`

type customAccountScheduleScanner interface{ Scan(...any) error }

func scanCustomAccountSchedule(row customAccountScheduleScanner) (*CustomAccountSchedule, error) {
	v := &CustomAccountSchedule{}
	var daytime sql.NullString
	var expression sql.NullString
	var weekday sql.NullInt64
	var groups pq.Int64Array
	err := row.Scan(&v.ID, &v.AccountID, &v.Action, &v.Frequency, &v.Timezone, &v.RunAt, &daytime, &expression, &weekday, &v.Enabled, &v.NextRunAt, &v.LastRunAt, &v.LastStatus, &v.LastMessage, &v.CreatedAt, &v.identity, &groups, &v.actor)
	if err != nil {
		return nil, err
	}
	v.TimeOfDay = daytime.String
	v.CronExpression = expression.String
	if weekday.Valid {
		day := int(weekday.Int64)
		v.Weekday = &day
	}
	v.groupIDs = []int64(groups)
	return v, nil
}

func (s *CustomAccountScheduleService) groups(ctx context.Context, q customResetQuery, id int64) ([]CustomAccountScheduleGroup, error) {
	rows, err := q.QueryContext(ctx, `SELECT g.id,g.name FROM account_groups ag JOIN groups g ON g.id=ag.group_id WHERE ag.account_id=$1 AND g.deleted_at IS NULL ORDER BY g.id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groups := []CustomAccountScheduleGroup{}
	for rows.Next() {
		var g CustomAccountScheduleGroup
		if err = rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *CustomAccountScheduleService) List(ctx context.Context, accountID int64) (*CustomAccountSchedulesView, error) {
	if _, err := s.eligible(ctx, accountID); err != nil {
		return nil, err
	}
	groups, err := s.groups(ctx, s.db, accountID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+customAccountScheduleColumns+` FROM custom_account_action_schedules WHERE account_id=$1 AND deleted_at IS NULL ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	v := &CustomAccountSchedulesView{Items: []CustomAccountSchedule{}, Timezone: s.timezone, Groups: groups}
	for rows.Next() {
		item, e := scanCustomAccountSchedule(rows)
		if e != nil {
			return nil, e
		}
		v.Items = append(v.Items, *item)
	}
	return v, rows.Err()
}

func (s *CustomAccountScheduleService) Create(ctx context.Context, accountID int64, input CustomAccountScheduleInput, actor int64) (*CustomAccountSchedule, error) {
	a, err := s.eligible(ctx, accountID)
	if err != nil {
		return nil, err
	}
	v, err := validateCustomAccountSchedule(input, s.timezone, s.now())
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.snapshot(ctx, tx, a, v); err != nil {
		return nil, err
	}
	v.actor = actor
	row := tx.QueryRowContext(ctx, `INSERT INTO custom_account_action_schedules(account_id,action,frequency,timezone,run_at,time_of_day,cron_expression,weekday,enabled,next_run_at,identity_snapshot,group_ids,actor_id)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,$13) RETURNING `+customAccountScheduleColumns, accountID, v.Action, v.Frequency, v.Timezone, v.RunAt, v.TimeOfDay, v.CronExpression, v.Weekday, v.Enabled, v.NextRunAt, v.identity, pq.Array(v.groupIDs), v.actor)
	v, err = scanCustomAccountSchedule(row)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *CustomAccountScheduleService) snapshot(ctx context.Context, tx *sql.Tx, a *Account, v *CustomAccountSchedule) error {
	// The account row is also locked by subscription reset and account updates.
	// Re-read identity in this transaction, rather than trusting a cached account.
	var identity string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(credentials->>'chatgpt_account_id','') FROM accounts WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth' FOR UPDATE`, a.ID).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("ACCOUNT_SCHEDULE_ACCOUNT", "Account not found")
	}
	if err != nil {
		return err
	}
	locked := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": identity}}
	if identity == "" || customResetIdentity(locked) != customResetIdentity(a) {
		return infraerrors.Conflict("ACCOUNT_SCHEDULE_IDENTITY_CHANGED", "Account identity changed; refresh and try again")
	}
	groups, err := s.groups(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	if v.Action == CustomAccountScheduleResetSubscriptions && len(groups) == 0 {
		return infraerrors.BadRequest("ACCOUNT_SCHEDULE_GROUPS", "Bind an account group before scheduling subscription resets")
	}
	v.identity = customResetIdentity(locked)
	v.groupIDs = make([]int64, 0, len(groups))
	for _, g := range groups {
		v.groupIDs = append(v.groupIDs, g.ID)
	}
	return nil
}

func (s *CustomAccountScheduleService) Update(ctx context.Context, accountID, scheduleID int64, input CustomAccountScheduleInput, actor int64) (*CustomAccountSchedule, error) {
	a, err := s.eligible(ctx, accountID)
	if err != nil {
		return nil, err
	}
	v, err := validateCustomAccountSchedule(input, s.timezone, s.now())
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockOwned(ctx, tx, accountID, scheduleID); err != nil {
		return nil, err
	}
	if err = s.snapshot(ctx, tx, a, v); err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `UPDATE custom_account_action_schedules SET action=$3,frequency=$4,timezone=$5,run_at=$6,time_of_day=NULLIF($7,''),cron_expression=NULLIF($8,''),weekday=$9,enabled=$10,next_run_at=$11,identity_snapshot=$12,group_ids=$13,actor_id=$14,updated_at=NOW()
 WHERE id=$1 AND account_id=$2 AND deleted_at IS NULL RETURNING `+customAccountScheduleColumns, scheduleID, accountID, v.Action, v.Frequency, v.Timezone, v.RunAt, v.TimeOfDay, v.CronExpression, v.Weekday, v.Enabled, v.NextRunAt, v.identity, pq.Array(v.groupIDs), actor)
	v, err = scanCustomAccountSchedule(row)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *CustomAccountScheduleService) lockOwned(ctx context.Context, tx *sql.Tx, accountID, scheduleID int64) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM custom_account_action_schedules WHERE id=$1 AND account_id=$2 AND deleted_at IS NULL FOR UPDATE`, scheduleID, accountID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return infraerrors.NotFound("ACCOUNT_SCHEDULE_NOT_FOUND", "Schedule not found")
	}
	if err != nil {
		return err
	}
	var running bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_account_action_schedule_runs WHERE schedule_id=$1 AND status='started')`, scheduleID).Scan(&running); err != nil {
		return err
	}
	if running {
		return infraerrors.Conflict("ACCOUNT_SCHEDULE_RUNNING", "Wait for the current action to finish before editing this schedule")
	}
	return nil
}

func (s *CustomAccountScheduleService) Delete(ctx context.Context, accountID, scheduleID int64) error {
	if _, err := s.eligible(ctx, accountID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockOwned(ctx, tx, accountID, scheduleID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE custom_account_action_schedules SET enabled=FALSE,next_run_at=NULL,deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND account_id=$2`, scheduleID, accountID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *CustomAccountScheduleService) Runs(ctx context.Context, accountID, scheduleID int64, limit int) ([]CustomAccountScheduleRun, error) {
	if _, err := s.eligible(ctx, accountID); err != nil {
		return nil, err
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_account_action_schedules WHERE id=$1 AND account_id=$2 AND deleted_at IS NULL)`, scheduleID, accountID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, infraerrors.NotFound("ACCOUNT_SCHEDULE_NOT_FOUND", "Schedule not found")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,schedule_id,account_id,action,status,message,warning_code,scheduled_at,started_at,finished_at,reset_count FROM custom_account_action_schedule_runs WHERE schedule_id=$1 AND account_id=$2 ORDER BY id DESC LIMIT $3`, scheduleID, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []CustomAccountScheduleRun{}
	for rows.Next() {
		var r CustomAccountScheduleRun
		if err = rows.Scan(&r.ID, &r.ScheduleID, &r.AccountID, &r.Action, &r.Status, &r.Message, &r.WarningCode, &r.ScheduledAt, &r.StartedAt, &r.FinishedAt, &r.ResetCount); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Claim and advance a slot in one transaction. The unique run slot is an extra
// defense against races/restarts; a process can never claim an existing attempt.
func (s *CustomAccountScheduleService) claim(ctx context.Context) (*CustomAccountSchedule, *CustomAccountScheduleRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	v, err := scanCustomAccountSchedule(tx.QueryRowContext(ctx, `SELECT `+customAccountScheduleColumns+` FROM custom_account_action_schedules s WHERE enabled AND deleted_at IS NULL AND next_run_at<=NOW()
 AND NOT EXISTS(SELECT 1 FROM custom_account_action_schedule_runs r WHERE r.schedule_id=s.id AND r.status='started')
 ORDER BY next_run_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return nil, nil, err
	}
	r := &CustomAccountScheduleRun{ScheduleID: v.ID, AccountID: v.AccountID, Action: v.Action, Status: "started", ScheduledAt: *v.NextRunAt, StartedAt: now, requestID: uuid.NewString()}
	next, nextErr := nextCustomAccountSchedule(v, now)
	enabled := next != nil && nextErr == nil
	if nextErr != nil {
		r.Status = "failed"
		r.Message = "Invalid schedule time; schedule paused"
		r.FinishedAt = &now
	}
	if now.Sub(r.ScheduledAt) > customAccountScheduleGrace {
		r.Status = "skipped"
		r.Message = "Scheduled time missed by more than 10 minutes; action skipped"
		r.FinishedAt = &now
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO custom_account_action_schedule_runs(schedule_id,account_id,action,status,message,scheduled_at,started_at,finished_at,request_id,actor_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(schedule_id,scheduled_at) DO NOTHING RETURNING id`, r.ScheduleID, r.AccountID, r.Action, r.Status, r.Message, r.ScheduledAt, r.StartedAt, r.FinishedAt, r.requestID, v.actor).Scan(&r.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	duplicate := errors.Is(err, sql.ErrNoRows)
	if !enabled {
		next = nil
	}
	if duplicate {
		_, err = tx.ExecContext(ctx, `UPDATE custom_account_action_schedules SET enabled=$2,next_run_at=$3,updated_at=NOW() WHERE id=$1`, v.ID, enabled, next)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE custom_account_action_schedules SET enabled=$2,next_run_at=$3,last_run_at=$4,last_status=$5,last_message=$6,updated_at=NOW() WHERE id=$1`, v.ID, enabled, next, r.StartedAt, r.Status, r.Message)
	}
	if err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	if duplicate || r.Status != "started" {
		return v, nil, nil
	}
	return v, r, nil
}

func (s *CustomAccountScheduleService) finish(ctx context.Context, v *CustomAccountSchedule, r *CustomAccountScheduleRun, result customAccountScheduleResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `UPDATE custom_account_action_schedule_runs SET status=$2,message=$3,warning_code=$4,reset_count=$5,finished_at=NOW() WHERE id=$1 AND status='started'`, r.ID, result.status, result.message, result.warning, result.count)
	if err != nil {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		var finished time.Time
		if err = tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&finished); err != nil {
			return err
		}
		next, nextErr := nextCustomAccountSchedule(v, finished)
		if nextErr != nil {
			result.pause = true
			result.message += "; no upcoming time could be calculated, schedule paused"
		}
		// A slow Cron execution can outlast its next interval. Advance past
		// intervals missed during execution rather than consuming cards in a
		// tight catch-up loop immediately after a success or failure.
		_, err = tx.ExecContext(ctx, `UPDATE custom_account_action_schedules SET last_status=$2,last_message=$3,enabled=CASE WHEN $4 THEN FALSE ELSE enabled END,next_run_at=CASE WHEN $4 THEN NULL WHEN enabled AND next_run_at<=$5 THEN $6 ELSE next_run_at END,updated_at=NOW() WHERE id=$1`, v.ID, result.status, result.message, result.pause, finished, next)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *CustomAccountScheduleService) recoverAbandoned(ctx context.Context) error {
	// An abandoned process might have redeemed a card or committed a reset.
	// Never resend; require explicit administrator review to resume.
	_, err := s.db.ExecContext(ctx, `WITH abandoned AS (
 UPDATE custom_account_action_schedule_runs SET status='unknown',message='Previous action did not finish; schedule paused for manual review',finished_at=NOW()
	 WHERE status='started' AND started_at < NOW()-($1 * INTERVAL '1 second') RETURNING schedule_id
 ) UPDATE custom_account_action_schedules SET enabled=FALSE,next_run_at=NULL,last_status='unknown',last_message='Previous action did not finish; schedule paused for manual review',updated_at=NOW()
 WHERE id IN (SELECT schedule_id FROM abandoned)`, int64(customAccountScheduleAbandoned.Seconds()))
	return err
}
