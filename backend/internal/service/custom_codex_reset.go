package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const customCodexResetURL = "https://didcodexreset.com/openapi/v1/records/latest?kind=reset_completed"
const customCodexResetInterval = time.Minute
const customCodexResetThreshold = 1.0

type customResetRecord struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	ResetType   string    `json:"resetType"`
	AnnouncedAt time.Time `json:"announcedAt"`
	Scope       struct {
		Plans   []string `json:"plans"`
		Windows []string `json:"windows"`
	} `json:"scope"`
}
type customQuotaSnapshot struct {
	Identity        string    `json:"identity"`
	At              time.Time `json:"at"`
	Weekly          *float64  `json:"weekly"`
	WeeklyMinutes   int       `json:"weekly_minutes"`
	WeeklyReset     time.Time `json:"weekly_reset"`
	FiveHour        *float64  `json:"five_hour"`
	FiveHourMinutes int       `json:"five_hour_minutes"`
	FiveHourReset   time.Time `json:"five_hour_reset"`
}

func customResetIdentity(a *Account) string {
	if a == nil || a.GetChatGPTAccountID() == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(a.GetChatGPTAccountID()))
	return hex.EncodeToString(sum[:])
}
func customAccountSnapshot(a *Account) customQuotaSnapshot {
	v := customSnapshot(a.Extra)
	v.Identity = customResetIdentity(a)
	return v
}
func customSnapshot(extra map[string]any) customQuotaSnapshot {
	number := func(k string) *float64 {
		v, ok := extra[k].(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	}
	integer := func(k string) int {
		switch v := extra[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		}
		return 0
	}
	stamp := func(k string) time.Time { v, _ := extra[k].(string); t, _ := time.Parse(time.RFC3339, v); return t }
	return customQuotaSnapshot{At: stamp("codex_usage_updated_at"), Weekly: number("codex_7d_used_percent"), WeeklyMinutes: integer("codex_7d_window_minutes"), WeeklyReset: stamp("codex_7d_reset_at"), FiveHour: number("codex_5h_used_percent"), FiveHourMinutes: integer("codex_5h_window_minutes"), FiveHourReset: stamp("codex_5h_reset_at")}
}

// Zero-length secondary windows are absent quotas, not proof of a 5h reset.
// A known weekly quota is mandatory because downstream weekly usage is reset.
func customResetEvidence(before, after customQuotaSnapshot, event, now time.Time) string {
	if before.Identity == "" || after.Identity != before.Identity {
		return "account_identity_changed"
	}
	if !after.At.After(event) || after.At.Before(now.Add(-5*time.Minute)) || after.At.After(now.Add(time.Minute)) {
		return "stale_snapshot"
	}
	if after.Weekly == nil || after.WeeklyMinutes != 10080 {
		return "missing_weekly_window"
	}
	if *after.Weekly < 0 || *after.Weekly > customCodexResetThreshold {
		return "usage_not_near_zero"
	}
	if after.FiveHourMinutes > 0 && (after.FiveHour == nil || *after.FiveHour < 0 || *after.FiveHour > customCodexResetThreshold) {
		return "usage_not_near_zero"
	}
	// A baseline from before the announcement avoids mistaking an ordinary window
	// rollover (or an already-idle account) for evidence of an exceptional reset.
	if before.At.IsZero() || !before.At.Before(event) || before.Weekly == nil || before.WeeklyMinutes != 10080 {
		return "missing_baseline"
	}
	if *before.Weekly <= customCodexResetThreshold {
		return "no_observed_drop"
	}
	if before.WeeklyReset.IsZero() || !before.WeeklyReset.After(after.At) {
		return "natural_reset_possible"
	}
	return "confirmed"
}

type CustomCodexResetService struct {
	db            *sql.DB
	accounts      AccountRepository
	subscriptions *SubscriptionService
	tests         *AccountTestService
	client        *http.Client
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func NewCustomCodexResetService(db *sql.DB, accounts AccountRepository, subs *SubscriptionService, tests *AccountTestService) *CustomCodexResetService {
	ctx, cancel := context.WithCancel(context.Background())
	return &CustomCodexResetService{db: db, accounts: accounts, subscriptions: subs, tests: tests, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, ctx: ctx, cancel: cancel}
}
func (s *CustomCodexResetService) Start() {
	// Separate workers: a slow inference probe must not delay the 1-minute feed poll.
	for _, run := range []func(context.Context) error{s.poll, s.work} {
		s.wg.Add(1)
		go func(run func(context.Context) error) {
			defer s.wg.Done()
			ticker := time.NewTicker(customCodexResetInterval)
			defer ticker.Stop()
			for {
				ctx, cancel := context.WithTimeout(s.ctx, 55*time.Second)
				err := run(ctx)
				cancel()
				if err != nil && s.ctx.Err() == nil {
					slog.Warn("custom_codex_subscription_reset", "error", err)
				}
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(run)
	}
}
func (s *CustomCodexResetService) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

func parseCustomResetRecord(reader io.Reader, now time.Time) (*customResetRecord, error) {
	var envelope struct {
		OK   bool               `json:"ok"`
		Data *customResetRecord `json:"data"`
	}
	data, err := io.ReadAll(io.LimitReader(reader, 128*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 128*1024 {
		return nil, errors.New("reset feed response too large")
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, errors.New("invalid reset feed JSON")
	}
	if !envelope.OK {
		return nil, errors.New("reset feed reported failure")
	}
	e := envelope.Data
	if e == nil {
		return nil, nil
	}
	if e.AnnouncedAt.IsZero() {
		return nil, nil
	}
	if e.ID == "" || len(e.ID) > 128 || e.Kind != "reset_completed" || e.AnnouncedAt.After(now.Add(time.Minute)) {
		return nil, errors.New("invalid reset event")
	}
	if e.ResetType != "global" {
		return nil, nil
	}
	all := false
	for _, p := range e.Scope.Plans {
		all = all || p == "all"
	}
	if !all {
		return nil, nil
	}
	// A 5h-only announcement cannot authorize downstream weekly resets.
	weeklyScope := false
	for _, window := range e.Scope.Windows {
		weeklyScope = weeklyScope || window == "unknown" || window == "all" || window == "7d" || window == "weekly"
	}
	if !weeklyScope {
		return nil, nil
	}
	// Scoped model/plan announcements cannot authorize a whole-group reset.
	return e, nil
}
func customResetRetryDelay(failures int, retry string, now time.Time) time.Duration {
	if secs, err := strconv.Atoi(retry); err == nil && secs > 0 && secs <= 365*24*60*60 {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(retry); err == nil && at.After(now) {
		return at.Sub(now)
	}
	if failures > 5 {
		failures = 5
	}
	delay := time.Minute * time.Duration(1<<failures)
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	return delay
}
func (s *CustomCodexResetService) poll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var next time.Time
	var failures int
	err = tx.QueryRowContext(ctx, `SELECT next_at,failures FROM custom_codex_reset_poll WHERE id=1 FOR UPDATE SKIP LOCKED`).Scan(&next, &failures)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if time.Now().Before(next) {
		return nil
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_codex_reset_policy WHERE enabled)`).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	pollStarted := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, customCodexResetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sub2api-custom-reset-monitor/1")
	resp, fetchErr := s.client.Do(req)
	retry := ""
	var record *customResetRecord
	if fetchErr == nil {
		defer func() { _ = resp.Body.Close() }()
		retry = resp.Header.Get("Retry-After")
		if resp.StatusCode != http.StatusOK {
			fetchErr = fmt.Errorf("reset feed HTTP %d", resp.StatusCode)
		} else {
			record, fetchErr = parseCustomResetRecord(resp.Body, time.Now())
		}
	}
	delay := customCodexResetInterval
	message := ""
	if fetchErr != nil {
		failures++
		delay = customResetRetryDelay(failures, retry, time.Now())
		message = "feed_unavailable"
	} else {
		failures = 0
	}
	nextPoll := pollStarted.Add(delay - time.Second)
	if fetchErr != nil {
		nextPoll = time.Now().Add(delay)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE custom_codex_reset_poll SET next_at=$1,failures=$2,checked_at=NOW(),error=$3 WHERE id=1`, nextPoll, failures, message); err != nil {
		return err
	}
	if record != nil && fetchErr == nil {
		if err = s.observe(ctx, tx, record); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return fetchErr
}
func (s *CustomCodexResetService) observe(ctx context.Context, tx *sql.Tx, e *customResetRecord) error {
	raw, _ := json.Marshal(e)
	res, err := tx.ExecContext(ctx, `INSERT INTO custom_codex_reset_events(id,announced_at,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, e.ID, e.AnnouncedAt, string(raw))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	var newer bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_codex_reset_events WHERE id<>$1 AND announced_at >= $2)`, e.ID, e.AnnouncedAt).Scan(&newer); err != nil {
		return err
	}
	if newer {
		return nil
	}
	// Historical events (including the initial latest result) never mutate usage.
	if time.Since(e.AnnouncedAt) > 2*time.Hour {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO custom_codex_reset_jobs(event_id,account_id,baseline,group_ids)
 SELECT $1,p.account_id,p.baseline,ARRAY(SELECT group_id FROM account_groups WHERE account_id=p.account_id ORDER BY group_id) FROM custom_codex_reset_policy p JOIN accounts a ON a.id=p.account_id
 WHERE p.enabled AND p.enabled_at<$2 AND a.deleted_at IS NULL AND a.status='active' AND a.platform='openai' AND a.type='oauth'`, e.ID, e.AnnouncedAt)
	if err != nil {
		return err
	}
	// One target snapshot per event/subscription even when several accounts share a group.
	_, err = tx.ExecContext(ctx, `INSERT INTO custom_codex_reset_targets(event_id,subscription_id,group_id,reset_revision,daily_base,weekly_base,daily_start,weekly_start)
 SELECT $1,us.id,us.group_id,us.custom_codex_reset_revision,us.daily_usage_usd,us.weekly_usage_usd,us.daily_window_start,us.weekly_window_start
 FROM user_subscriptions us JOIN groups g ON g.id=us.group_id
 WHERE us.deleted_at IS NULL AND g.deleted_at IS NULL AND us.status='active' AND us.starts_at<=$2 AND us.expires_at>NOW()
 AND EXISTS(SELECT 1 FROM account_groups ag JOIN custom_codex_reset_jobs j ON j.account_id=ag.account_id WHERE j.event_id=$1 AND ag.group_id=us.group_id)
 ORDER BY us.id FOR UPDATE OF us`, e.ID, e.AnnouncedAt)
	return err
}
func (s *CustomCodexResetService) work(ctx context.Context) error {
	// Transaction-level leader lock covers one worker batch, including probes.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var ok bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(824712930)`).Scan(&ok); err != nil || !ok {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT j.event_id,j.account_id,j.baseline,j.attempts,e.announced_at,e.observed_at
 FROM custom_codex_reset_jobs j JOIN custom_codex_reset_events e ON e.id=j.event_id
 WHERE j.status='pending' AND j.next_at<=NOW() ORDER BY j.next_at,j.account_id LIMIT 4`)
	if err != nil {
		return err
	}
	type job struct {
		event               string
		id                  int64
		baseline            []byte
		attempts            int
		announced, observed time.Time
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.event, &j.id, &j.baseline, &j.attempts, &j.announced, &j.observed); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var status string
		if time.Since(j.announced) > 2*time.Hour {
			status = "expired"
		} else {
			var baseline customQuotaSnapshot
			if err = json.Unmarshal(j.baseline, &baseline); err != nil {
				return err
			}
			status, err = s.verify(ctx, j.id, j.event, j.announced, j.observed, baseline, j.attempts)
			if err != nil {
				slog.Warn("custom_codex_reset_verify_failed", "account_id", j.id, "error", err)
				status = "pending"
			}
		}
		_, err = s.db.ExecContext(ctx, `UPDATE custom_codex_reset_jobs SET status=$3,updated_at=NOW(),next_at=GREATEST(next_at,NOW()+INTERVAL '1 minute') WHERE event_id=$1 AND account_id=$2`, j.event, j.id, status)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `UPDATE custom_codex_reset_policy SET status=$2,updated_at=NOW() WHERE account_id=$1 AND enabled`, j.id, status)
		if err != nil {
			return err
		}
	}
	// Keep a pre-event baseline without overwriting one while verification is pending.
	rows, err = s.db.QueryContext(ctx, `SELECT p.account_id FROM custom_codex_reset_policy p WHERE p.enabled AND NOT EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.account_id=p.account_id AND j.status='pending')`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		a, e := s.eligible(ctx, id)
		if e != nil {
			continue
		}
		raw, _ := json.Marshal(customAccountSnapshot(a))
		if _, err = s.db.ExecContext(ctx, `UPDATE custom_codex_reset_policy SET baseline=$2 WHERE account_id=$1 AND NOT (
 COALESCE((baseline->>'weekly')::numeric,0)>1 AND COALESCE(($2::jsonb->>'weekly')::numeric,100)<=1
 AND COALESCE((baseline->>'weekly_reset')::timestamptz,'epoch')>NOW()
 AND NOT EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.account_id=$1 AND j.status<>'pending' AND j.updated_at>COALESCE((custom_codex_reset_policy.baseline->>'at')::timestamptz,'epoch')))`, id, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
func (s *CustomCodexResetService) verify(ctx context.Context, id int64, event string, announced, observed time.Time, before customQuotaSnapshot, attempts int) (string, error) {
	conn, release, err := customResetAccountLock(ctx, s.db, id)
	if err != nil {
		return "pending", err
	}
	defer release()
	var allowed, card bool
	exclusionStart := announced.Add(-10 * time.Minute)
	if !before.At.IsZero() && before.At.Before(exclusionStart) {
		exclusionStart = before.At
	}
	err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM custom_codex_reset_policy WHERE account_id=$1 AND enabled AND enabled_at<$2),EXISTS(SELECT 1 FROM custom_codex_reset_card_attempts WHERE account_id=$1 AND attempted_at >= $3)`, id, announced, exclusionStart).Scan(&allowed, &card)
	if err != nil {
		return "pending", err
	}
	if !allowed {
		return "disabled", nil
	}
	if card {
		return "reset_card_excluded", nil
	}
	a, err := s.eligible(ctx, id)
	if err != nil {
		return "unsupported_account", nil
	}
	if a.Status != "active" {
		return "inactive_account", nil
	}
	after := customAccountSnapshot(a)
	if !after.At.After(announced) || time.Since(after.At) > 5*time.Minute {
		if time.Since(observed) < 2*time.Minute {
			return "pending", nil
		}
		if attempts >= 3 {
			return "probe_failed", nil
		}
		// Persist attempts before I/O so restarts cannot endlessly repeat a paid probe.
		if _, err = conn.ExecContext(ctx, `UPDATE custom_codex_reset_jobs SET attempts=attempts+1,next_at=NOW()+INTERVAL '5 minutes' WHERE event_id=$1 AND account_id=$2`, event, id); err != nil {
			return "pending", err
		}
		after, err = s.tests.probeCustomCodexReset(ctx, id)
		if err != nil {
			return "pending", err
		}
	}
	evidence := customResetEvidence(before, after, announced, time.Now())
	if evidence == "usage_not_near_zero" || evidence == "stale_snapshot" {
		return "pending", nil
	}
	if evidence != "confirmed" {
		return evidence, nil
	}
	_, err = s.apply(ctx, id, "official:"+event, event, "", 0)
	if err != nil {
		return "pending", err
	}
	return "succeeded", nil
}
