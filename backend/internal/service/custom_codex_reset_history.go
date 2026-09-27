package service

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *CustomCodexResetService) captureTargets(ctx context.Context, tx *sql.Tx, e *customResetRecord) error {
	// Wait for in-flight billing transactions before taking a fresh statement
	// snapshot of their committed history. A single SELECT FOR UPDATE would keep
	// the older statement snapshot when it had to wait for a subscription lock.
	rows, err := tx.QueryContext(ctx, `SELECT us.id FROM user_subscriptions us
 WHERE us.deleted_at IS NULL AND us.status='active' AND us.starts_at<=$2 AND us.expires_at>NOW()
 AND EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.event_id=$1 AND us.group_id=ANY(j.group_ids))
 ORDER BY us.id FOR UPDATE OF us`, e.ID, e.AnnouncedAt)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO custom_codex_reset_targets(event_id,subscription_id,group_id,reset_revision,daily_base,weekly_base,daily_start,weekly_start)
 SELECT $1,us.id,us.group_id,h.reset_revision,h.daily_usage,h.weekly_usage,h.daily_start,h.weekly_start
 FROM user_subscriptions us JOIN groups g ON g.id=us.group_id
 JOIN LATERAL (SELECT * FROM custom_codex_subscription_history h WHERE h.subscription_id=us.id AND h.recorded_at<=$2 ORDER BY h.recorded_at DESC,h.id DESC LIMIT 1) h ON TRUE
 WHERE us.deleted_at IS NULL AND g.deleted_at IS NULL AND us.status='active' AND us.starts_at<=$2 AND us.expires_at>NOW()
 AND EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.event_id=$1 AND us.group_id=ANY(j.group_ids))`, e.ID, e.AnnouncedAt)
	if err != nil {
		return err
	}
	// Do not silently invent a pre-event balance for events predating migration
	// or missing history. This account must be reviewed manually in that case.
	_, err = tx.ExecContext(ctx, `UPDATE custom_codex_reset_jobs j SET status='missing_subscription_history'
 WHERE j.event_id=$1 AND EXISTS(SELECT 1 FROM user_subscriptions us JOIN groups g ON g.id=us.group_id
 WHERE us.group_id=ANY(j.group_ids) AND us.deleted_at IS NULL AND g.deleted_at IS NULL AND us.status='active' AND us.starts_at<=$2 AND us.expires_at>NOW()
 AND NOT EXISTS(SELECT 1 FROM custom_codex_reset_targets t WHERE t.event_id=$1 AND t.subscription_id=us.id))`, e.ID, e.AnnouncedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE custom_codex_reset_policy p SET status=j.status FROM custom_codex_reset_jobs j WHERE j.event_id=$1 AND j.account_id=p.account_id AND j.status='missing_subscription_history'`, e.ID)
	return err
}

func (s *CustomCodexResetService) recordQuotaHistory(ctx context.Context, a *Account) error {
	snapshot := customAccountSnapshot(a)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if !snapshot.At.IsZero() {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO custom_codex_quota_history(account_id,observed_at,snapshot) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, a.ID, snapshot.At, string(raw)); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE custom_codex_reset_policy SET baseline=$2 WHERE account_id=$1
 AND NOT EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.account_id=$1 AND j.status IN ('pending','awaiting_usage'))`, a.ID, string(raw))
	return err
}

func (s *CustomCodexResetService) pruneHistory(ctx context.Context) error {
	// Keep two days plus one predecessor per subscription/account. Idle accounts
	// and subscriptions retain their last known state rather than losing a baseline.
	for _, query := range []string{
		`DELETE FROM custom_codex_subscription_history h WHERE h.recorded_at<NOW()-INTERVAL '2 days' AND EXISTS(SELECT 1 FROM custom_codex_subscription_history newer WHERE newer.subscription_id=h.subscription_id AND newer.recorded_at<NOW()-INTERVAL '2 days' AND (newer.recorded_at,newer.id)>(h.recorded_at,h.id))`,
		`DELETE FROM custom_codex_quota_history h WHERE h.observed_at<NOW()-INTERVAL '2 days' AND EXISTS(SELECT 1 FROM custom_codex_quota_history newer WHERE newer.account_id=h.account_id AND newer.observed_at<NOW()-INTERVAL '2 days' AND newer.observed_at>h.observed_at)`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
