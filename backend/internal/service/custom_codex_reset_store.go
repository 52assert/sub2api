package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

var errCustomResetHistoryMissing = errors.New("missing subscription history at compensation boundary")

// These fork-owned tables deliberately keep event deduplication and financial
// mutations in the same PostgreSQL transaction. Redis is not the source of truth.
type CustomResetSubscription struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"user_id"`
	GroupID     int64      `json:"group_id"`
	GroupName   string     `json:"group_name"`
	Daily       float64    `json:"daily_usage_usd"`
	Weekly      float64    `json:"weekly_usage_usd"`
	DailyStart  *time.Time `json:"-"`
	WeeklyStart *time.Time `json:"-"`
}
type CustomResetView struct {
	Enabled       bool                      `json:"enabled"`
	Status        string                    `json:"status"`
	Subscriptions []CustomResetSubscription `json:"subscriptions"`
	Fingerprint   string                    `json:"fingerprint"`
	NextCheck     *time.Time                `json:"next_check_at"`
	LastChecked   *time.Time                `json:"last_checked_at"`
	PollError     string                    `json:"poll_error"`
	LastEventAt   *time.Time                `json:"last_event_at"`
}
type customResetQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func customResetSubscriptions(ctx context.Context, q customResetQuery, id int64, lock bool) ([]CustomResetSubscription, error) {
	query := `SELECT us.id,us.user_id,us.group_id,g.name,us.daily_usage_usd,us.weekly_usage_usd,us.daily_window_start,us.weekly_window_start
 FROM user_subscriptions us JOIN groups g ON g.id=us.group_id
 WHERE us.deleted_at IS NULL AND g.deleted_at IS NULL AND us.status='active'
 AND us.starts_at <= NOW() AND us.expires_at > NOW()
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=$1 AND ag.group_id=us.group_id)
 ORDER BY us.id`
	if lock {
		query += " FOR UPDATE OF us"
	}
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []CustomResetSubscription{}
	for rows.Next() {
		var v CustomResetSubscription
		if err = rows.Scan(&v.ID, &v.UserID, &v.GroupID, &v.GroupName, &v.Daily, &v.Weekly, &v.DailyStart, &v.WeeklyStart); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func customResetFingerprint(subs []CustomResetSubscription) string {
	h := sha256.New()
	for _, v := range subs {
		_, _ = fmt.Fprintf(h, "%d:%d:%d;", v.ID, v.UserID, v.GroupID)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (s *CustomCodexResetService) eligible(ctx context.Context, id int64) (*Account, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || a.IsShadow() || a.IsCredentialShadow() || a.IsSyntheticUITest() {
		return nil, infraerrors.BadRequest("CODEX_RESET_ACCOUNT", "Only a real OpenAI OAuth parent account is supported")
	}
	return a, nil
}
func (s *CustomCodexResetService) Preview(ctx context.Context, id int64) (*CustomResetView, error) {
	if _, err := s.eligible(ctx, id); err != nil {
		return nil, err
	}
	subs, err := customResetSubscriptions(ctx, s.db, id, false)
	if err != nil {
		return nil, err
	}
	v := &CustomResetView{Status: "disabled", Subscriptions: subs, Fingerprint: customResetFingerprint(subs)}
	err = s.db.QueryRowContext(ctx, `SELECT enabled,status FROM custom_codex_reset_policy WHERE account_id=$1`, id).Scan(&v.Enabled, &v.Status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT checked_at,next_at,error FROM custom_codex_reset_poll WHERE id=1`).Scan(&v.LastChecked, &v.NextCheck, &v.PollError); err != nil {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT MAX(announced_at) FROM custom_codex_reset_events`).Scan(&v.LastEventAt); err != nil {
		return nil, err
	}
	return v, nil
}
func (s *CustomCodexResetService) Configure(ctx context.Context, id int64, enabled bool) error {
	a, err := s.eligible(ctx, id)
	if err != nil {
		return err
	}
	snapshot, _ := json.Marshal(customAccountSnapshot(a))
	_, err = s.db.ExecContext(ctx, `INSERT INTO custom_codex_reset_policy(account_id,enabled,baseline,status)
 VALUES($1,$2,$3,CASE WHEN $2 THEN 'watching' ELSE 'disabled' END)
 ON CONFLICT(account_id) DO UPDATE SET enabled=EXCLUDED.enabled,
 enabled_at=CASE WHEN custom_codex_reset_policy.enabled=EXCLUDED.enabled THEN custom_codex_reset_policy.enabled_at ELSE NOW() END,
 baseline=EXCLUDED.baseline,status=EXCLUDED.status,updated_at=NOW()`, id, enabled, string(snapshot))
	if err == nil && enabled {
		return s.recordQuotaHistory(ctx, a)
	}
	return err
}

// An account lock serializes local reset-card attempts with official verification.
// The durable attempt is recorded BEFORE contacting upstream, including timeouts.
func customResetAccountLock(ctx context.Context, db *sql.DB, id int64) (*sql.Conn, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ok bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('custom-codex-reset-account:' || $1::text,0))`, id).Scan(&ok)
	if err != nil || !ok {
		_ = conn.Close()
		if err == nil {
			err = infraerrors.Conflict("CODEX_RESET_BUSY", "Account reset verification is in progress; retry shortly")
		}
		return nil, nil, err
	}
	return conn, func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(c, `SELECT pg_advisory_unlock(hashtextextended('custom-codex-reset-account:' || $1::text,0))`, id); err != nil {
			// A pooled connection must never retain a session advisory lock.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}
func beginCustomResetCard(ctx context.Context, db *sql.DB, id int64) (func(), error) {
	conn, release, err := customResetAccountLock(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO custom_codex_reset_card_attempts(account_id) VALUES($1)`, id); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Manual resets clear daily/weekly usage at execution time. Automatic resets
// subtract only the balances recorded at or before the announcement, preserving
// all later charges, including consumption during polling and verification.
// Each window is protected separately when another reset already changed it.
func (s *CustomCodexResetService) apply(ctx context.Context, id int64, operation, event, fingerprint string, actor int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Lock the job before subscriptions, matching announcement capture order.
	// A newer announcement may supersede a job while its quota probe is running.
	var baselineIdentity string
	var cycleStart, compensationAt time.Time
	if event != "" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(baseline->>'identity',''),cycle_start,compensation_at FROM custom_codex_reset_jobs WHERE event_id=$1 AND account_id=$2 AND status IN ('pending','awaiting_usage') FOR UPDATE`, event, id).Scan(&baselineIdentity, &cycleStart, &compensationAt); err != nil {
			return 0, err
		}
	}
	// Lock group membership against concurrent account edits before preview check.
	var upstreamAccountID string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(credentials->>'chatgpt_account_id','') FROM accounts WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth' FOR UPDATE`, id).Scan(&upstreamAccountID); err != nil {
		return 0, err
	}
	subs, err := customResetSubscriptions(ctx, tx, id, true)
	if err != nil {
		return 0, err
	}
	if event == "" && customResetFingerprint(subs) != fingerprint {
		return 0, infraerrors.Conflict("CODEX_RESET_TARGETS_CHANGED", "Subscriptions changed; refresh the preview")
	}
	if event != "" {
		currentIdentity := customResetIdentity(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": upstreamAccountID}})
		if baselineIdentity == "" || baselineIdentity != currentIdentity {
			return 0, infraerrors.Conflict("CODEX_RESET_IDENTITY_CHANGED", "Upstream account identity changed; manual review required")
		}
		var allowed bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_codex_reset_policy p JOIN custom_codex_reset_events e ON e.id=$2 WHERE p.account_id=$1 AND p.enabled AND p.enabled_at < e.announced_at)
 AND NOT EXISTS(SELECT 1 FROM custom_codex_reset_card_attempts c JOIN custom_codex_reset_events e ON e.id=$2 WHERE c.account_id=$1 AND c.attempted_at >= LEAST(e.announced_at - INTERVAL '10 minutes', (SELECT (j.baseline->>'at')::timestamptz FROM custom_codex_reset_jobs j WHERE j.event_id=e.id AND j.account_id=$1)))`, id, event).Scan(&allowed)
		if err != nil {
			return 0, err
		}
		if !allowed {
			return 0, infraerrors.Conflict("CODEX_RESET_EXCLUDED", "Policy disabled or reset card attempt detected")
		}
	}
	now := time.Now()
	source := "manual"
	if event != "" {
		source = "official"
	}
	changed := []CustomResetSubscription{}
	for _, v := range subs {
		dailyReset, weeklyReset := false, false
		var historyID int64
		if event != "" {
			var targetUnchanged bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM custom_codex_reset_targets t
 JOIN custom_codex_reset_jobs j ON j.event_id=t.event_id AND j.account_id=$4
 WHERE t.event_id=$1 AND t.subscription_id=$2 AND t.group_id=$3 AND $3=ANY(j.group_ids)
 )`, event, v.ID, v.GroupID, id).Scan(&targetUnchanged)
			if err != nil {
				return 0, err
			}
			if !targetUnchanged {
				continue
			}
			if err = tx.QueryRowContext(ctx, `SELECT id FROM custom_codex_subscription_history WHERE subscription_id=$1 AND recorded_at<=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`, v.ID, compensationAt).Scan(&historyID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return 0, errCustomResetHistoryMissing
				}
				return 0, err
			}
			// Daily rollover must not suppress the independent weekly refund.
			// Row locks keep these flags stable until the financial update commits.
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(bool_or(h.daily_reset),FALSE),COALESCE(bool_or(h.weekly_reset),FALSE)
 FROM custom_codex_subscription_history h
 WHERE h.subscription_id=$1 AND h.recorded_at>$2`, v.ID, compensationAt).Scan(&dailyReset, &weeklyReset); err != nil {
				return 0, err
			}
			if dailyReset && weeklyReset {
				continue
			}
		}

		if event != "" {
			var superseded bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_codex_reset_actions a JOIN custom_codex_reset_events e ON e.id=$2 WHERE a.subscription_id=$1 AND a.operation_id<>$3 AND a.created_at>=e.observed_at)`, v.ID, event, operation).Scan(&superseded)
			if err != nil {
				return 0, err
			}
			if superseded {
				continue
			}
		}
		// SQL decimal arithmetic avoids round-trip rounding of billing values.
		var claimed int64
		err = tx.QueryRowContext(ctx, `INSERT INTO custom_codex_reset_actions(operation_id,subscription_id,account_id,source,actor_id,daily_before,weekly_before,daily_after,weekly_after)
 SELECT $1,id,$3,$4,NULLIF($5,0),daily_usage_usd,weekly_usage_usd,daily_usage_usd,weekly_usage_usd FROM user_subscriptions WHERE id=$2
 ON CONFLICT DO NOTHING RETURNING subscription_id`, operation, v.ID, id, source, actor).Scan(&claimed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if event == "" {
			_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET daily_usage_usd=0,weekly_usage_usd=0,daily_window_start=$2,weekly_window_start=$3,updated_at=NOW() WHERE id=$1`, v.ID, timezone.StartOfDay(now), now)
		} else {
			if _, err = tx.ExecContext(ctx, `INSERT INTO custom_codex_group_cycles(event_id,group_id,cycle_start) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, event, v.GroupID, cycleStart); err != nil {
				return 0, err
			}
			var sharedStart time.Time
			if err = tx.QueryRowContext(ctx, `SELECT cycle_start FROM custom_codex_group_cycles WHERE event_id=$1 AND group_id=$2`, event, v.GroupID).Scan(&sharedStart); err != nil {
				return 0, err
			}

			_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions us SET
 daily_usage_usd=CASE WHEN NOT $5 AND us.daily_window_start IS NOT DISTINCT FROM h.daily_start THEN GREATEST(0,us.daily_usage_usd-h.daily_usage) ELSE us.daily_usage_usd END,
 weekly_usage_usd=CASE WHEN NOT $6 AND us.weekly_window_start IS NOT DISTINCT FROM h.weekly_start THEN GREATEST(0,us.weekly_usage_usd-h.weekly_usage) ELSE us.weekly_usage_usd END,
 daily_window_start=CASE WHEN NOT $5 AND us.daily_window_start IS NOT DISTINCT FROM h.daily_start THEN $3::timestamptz ELSE us.daily_window_start END,
 weekly_window_start=CASE WHEN NOT $6 AND us.weekly_window_start IS NOT DISTINCT FROM h.weekly_start THEN $4::timestamptz ELSE us.weekly_window_start END,
 updated_at=NOW() FROM custom_codex_subscription_history h WHERE us.id=$1 AND h.subscription_id=us.id AND h.id=$2`, v.ID, historyID, timezone.StartOfDay(now), sharedStart, dailyReset, weeklyReset)
		}
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE custom_codex_reset_actions a SET daily_after=us.daily_usage_usd,weekly_after=us.weekly_usage_usd FROM user_subscriptions us WHERE a.operation_id=$1 AND a.subscription_id=$2 AND us.id=a.subscription_id`, operation, v.ID)
		if err != nil {
			return 0, err
		}
		changed = append(changed, v)
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	for _, v := range subs {
		if err = s.subscriptions.invalidateSubscriptionCaches(v.UserID, v.GroupID); err != nil {
			return len(changed), err
		}
	}
	return len(changed), nil
}
func (s *CustomCodexResetService) Manual(ctx context.Context, id int64, key, fingerprint string, actor int64) (int, error) {
	if _, err := s.eligible(ctx, id); err != nil {
		return 0, err
	}
	return s.apply(ctx, id, fmt.Sprintf("manual:%d:%s", id, key), "", fingerprint, actor)
}
