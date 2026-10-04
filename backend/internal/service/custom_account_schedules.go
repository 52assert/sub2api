package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/robfig/cron/v3"
)

const (
	CustomAccountScheduleResetCard          = "reset_card"
	CustomAccountScheduleResetSubscriptions = "reset_subscriptions"
	customAccountScheduleGrace              = 10 * time.Minute
	customAccountScheduleTimeout            = 2 * time.Minute
	customAccountScheduleAbandoned          = 5 * time.Minute
)

type CustomAccountScheduleInput struct {
	Action         string `json:"action"`
	Frequency      string `json:"frequency"`
	Timezone       string `json:"timezone"`
	RunAt          string `json:"run_at"`
	TimeOfDay      string `json:"time_of_day"`
	CronExpression string `json:"cron_expression"`
	Weekday        *int   `json:"weekday"`
	Enabled        bool   `json:"enabled"`
}

type CustomAccountSchedule struct {
	ID             int64      `json:"id"`
	AccountID      int64      `json:"account_id"`
	Action         string     `json:"action"`
	Frequency      string     `json:"frequency"`
	Timezone       string     `json:"timezone"`
	RunAt          *time.Time `json:"run_at"`
	TimeOfDay      string     `json:"time_of_day"`
	CronExpression string     `json:"cron_expression"`
	Weekday        *int       `json:"weekday"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      *time.Time `json:"next_run_at"`
	LastRunAt      *time.Time `json:"last_run_at"`
	LastStatus     string     `json:"last_status"`
	LastMessage    string     `json:"last_message"`
	CreatedAt      time.Time  `json:"created_at"`
	identity       string
	groupIDs       []int64
	actor          int64
}

type CustomAccountScheduleGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type CustomAccountSchedulesView struct {
	Items    []CustomAccountSchedule      `json:"items"`
	Timezone string                       `json:"timezone"`
	Groups   []CustomAccountScheduleGroup `json:"groups"`
}

type CustomAccountScheduleRun struct {
	ID          int64      `json:"id"`
	ScheduleID  int64      `json:"schedule_id"`
	AccountID   int64      `json:"account_id"`
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	WarningCode string     `json:"warning_code"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	ResetCount  int        `json:"reset_count"`
	requestID   string
}

type customAccountScheduleQuota interface {
	QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error)
	ResetCreditTargetedForSchedule(context.Context, int64, string, string, string) (*OpenAIQuotaResetResult, error)
	CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error
}
type customAccountScheduledSubscriptions interface {
	Scheduled(context.Context, int64, string, []int64, int64, ...string) (int, error)
}

type CustomAccountScheduleService struct {
	db        *sql.DB
	accounts  AccountRepository
	quota     customAccountScheduleQuota
	recoverer openAIQuotaResetWorkflowRecoverer
	resets    customAccountScheduledSubscriptions
	timezone  string
	now       func() time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	started   bool
	wg        sync.WaitGroup
}

func NewCustomAccountScheduleService(db *sql.DB, accounts AccountRepository, quota *OpenAIQuotaService, rateLimit *RateLimitService, resets *CustomCodexResetService, timezone string) *CustomAccountScheduleService {
	if _, err := time.LoadLocation(timezone); timezone == "" || err != nil {
		timezone = "Asia/Shanghai"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &CustomAccountScheduleService{db: db, accounts: accounts, timezone: timezone, now: time.Now, ctx: ctx, cancel: cancel}
	// Avoid typed nil interfaces when an optional dependency is absent.
	if quota != nil {
		s.quota = quota
	}
	if rateLimit != nil {
		s.recoverer = rateLimit
	}
	if resets != nil {
		s.resets = resets
	}
	return s
}

func (s *CustomAccountScheduleService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.ctx.Err() != nil {
		return
	}
	s.started = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.work(s.ctx); err != nil && s.ctx.Err() == nil {
				slog.Warn("custom_account_schedule_work_failed", "reason", infraerrors.Reason(err))
			}
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *CustomAccountScheduleService) Stop() { s.mu.Lock(); s.cancel(); s.mu.Unlock(); s.wg.Wait() }

func (s *CustomAccountScheduleService) eligible(ctx context.Context, id int64) (*Account, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || a.IsShadow() || a.IsCredentialShadow() || a.IsSyntheticUITest() {
		return nil, infraerrors.BadRequest("ACCOUNT_SCHEDULE_ACCOUNT", "Only a real OpenAI OAuth parent account is supported")
	}
	if customResetIdentity(a) == "" {
		return nil, infraerrors.BadRequest("ACCOUNT_SCHEDULE_IDENTITY", "Re-authorize the account before scheduling actions")
	}
	return a, nil
}

func validateCustomAccountSchedule(input CustomAccountScheduleInput, defaultTimezone string, now time.Time) (*CustomAccountSchedule, error) {
	bad := func(message string) (*CustomAccountSchedule, error) {
		return nil, infraerrors.BadRequest("ACCOUNT_SCHEDULE_INPUT", message)
	}
	if input.Action != CustomAccountScheduleResetCard && input.Action != CustomAccountScheduleResetSubscriptions {
		return bad("Unknown scheduled action")
	}
	zone := strings.TrimSpace(input.Timezone)
	if zone == "" {
		zone = defaultTimezone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return bad("Use a valid IANA timezone")
	}
	v := &CustomAccountSchedule{Action: input.Action, Frequency: input.Frequency, Timezone: zone, Enabled: input.Enabled}
	if input.Frequency != "cron" && input.CronExpression != "" {
		return bad("cron_expression is only valid for Cron schedules")
	}
	switch input.Frequency {
	case "once":
		if input.TimeOfDay != "" || input.Weekday != nil {
			return bad("One-time actions require only run_at")
		}
		run, parseErr := time.Parse(time.RFC3339, input.RunAt)
		if parseErr != nil {
			run, parseErr = time.ParseInLocation("2006-01-02T15:04", input.RunAt, loc)
			if parseErr == nil && run.In(loc).Format("2006-01-02T15:04") != input.RunAt {
				parseErr = errors.New("nonexistent local time")
			}
		}
		if parseErr != nil {
			return bad("run_at must be RFC3339 or YYYY-MM-DDTHH:mm in the selected timezone")
		}
		if !run.After(now) {
			return bad("One-time actions must be scheduled in the future")
		}
		run = run.UTC()
		v.RunAt = &run
	case "daily", "weekly":
		if input.RunAt != "" {
			return bad("Recurring actions require time_of_day")
		}
		parsed, parseErr := time.Parse("15:04", input.TimeOfDay)
		if parseErr != nil || parsed.Format("15:04") != input.TimeOfDay {
			return bad("time_of_day must be HH:mm")
		}
		v.TimeOfDay = input.TimeOfDay
		if input.Frequency == "weekly" {
			if input.Weekday == nil || *input.Weekday < 0 || *input.Weekday > 6 {
				return bad("weekday must be 0 (Sunday) through 6 (Saturday)")
			}
			day := *input.Weekday
			v.Weekday = &day
		} else if input.Weekday != nil {
			return bad("Daily actions do not accept weekday")
		}
	case "cron":
		if input.RunAt != "" || input.TimeOfDay != "" || input.Weekday != nil {
			return bad("Cron schedules require only cron_expression")
		}
		v.CronExpression = strings.Join(strings.Fields(input.CronExpression), " ")
		if _, err := parseCustomAccountScheduleCron(v.CronExpression, zone); err != nil {
			return bad("cron_expression must be a valid five-field Cron expression")
		}
	default:
		return bad("frequency must be once, daily, weekly, or cron")
	}
	next, err := nextCustomAccountSchedule(v, now)
	if err != nil {
		return nil, err
	}
	if v.Enabled {
		v.NextRunAt = next
	}
	return v, nil
}

// Always move to the next future slot. A daily/weekly action runs once per local
// date; DST gaps are skipped and the repeated hour is not executed twice.
func nextCustomAccountSchedule(v *CustomAccountSchedule, after time.Time) (*time.Time, error) {
	if v.Frequency == "once" {
		if v.RunAt != nil && v.RunAt.After(after) {
			t := v.RunAt.UTC()
			return &t, nil
		}
		return nil, nil
	}
	if v.Frequency == "cron" {
		schedule, err := parseCustomAccountScheduleCron(v.CronExpression, v.Timezone)
		if err != nil {
			return nil, err
		}
		next := schedule.Next(after)
		if next.IsZero() {
			return nil, infraerrors.BadRequest("ACCOUNT_SCHEDULE_TIME", "Cron expression has no upcoming occurrence")
		}
		next = next.UTC()
		return &next, nil
	}
	loc, err := time.LoadLocation(v.Timezone)
	if err != nil {
		return nil, err
	}
	clock, err := time.Parse("15:04", v.TimeOfDay)
	if err != nil {
		return nil, err
	}
	local := after.In(loc)
	for day := 0; day < 15; day++ {
		date := time.Date(local.Year(), local.Month(), local.Day()+day, 12, 0, 0, 0, loc)
		if v.Frequency == "weekly" && (v.Weekday == nil || int(date.Weekday()) != *v.Weekday) {
			continue
		}
		t := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if t.Hour() != clock.Hour() || t.Minute() != clock.Minute() || t.Day() != date.Day() {
			continue
		}
		if t.After(after) {
			utc := t.UTC()
			return &utc, nil
		}
	}
	return nil, infraerrors.BadRequest("ACCOUNT_SCHEDULE_TIME", "Cannot find the next scheduled time")
}

func parseCustomAccountScheduleCron(expression, zone string) (cron.Schedule, error) {
	if len(expression) > 256 {
		return nil, fmt.Errorf("cron expression is too long")
	}
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected five fields")
	}
	// The saved timezone is authoritative; reject descriptors/TZ overrides.
	for _, field := range fields {
		if strings.Contains(field, "=") || strings.Contains(field, "@") {
			return nil, fmt.Errorf("unexpected Cron prefix")
		}
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "Local" {
		return nil, fmt.Errorf("invalid timezone")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return parser.Parse("CRON_TZ=" + zone + " " + strings.Join(fields, " "))
}

func (s *CustomAccountScheduleService) work(ctx context.Context) error {
	if err := s.recoverAbandoned(ctx); err != nil {
		return err
	}
	// Bound each sweep so shutdown and newly edited schedules are observed.
	for range 20 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v, run, err := s.claim(ctx)
		if err != nil {
			return err
		}
		if v == nil {
			return nil
		}
		if run == nil {
			continue
		}
		execCtx, cancel := context.WithTimeout(ctx, customAccountScheduleTimeout)
		result := s.execute(execCtx, v, run)
		cancel()
		// Persist a result even when Stop cancelled the upstream request. This
		// context performs only local bookkeeping and never another redemption.
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = s.finish(finishCtx, v, run, result)
		finishCancel()
		if err != nil {
			return err
		}
	}
	return nil
}

type customAccountScheduleResult struct {
	status, message, warning string
	count                    int
	pause                    bool
}

func (s *CustomAccountScheduleService) execute(ctx context.Context, v *CustomAccountSchedule, run *CustomAccountScheduleRun) customAccountScheduleResult {
	fail := func(message string, pause bool) customAccountScheduleResult {
		return customAccountScheduleResult{status: "failed", message: message, pause: pause}
	}
	if result := s.verifyScope(ctx, v); result != nil {
		return *result
	}
	if v.Action == CustomAccountScheduleResetSubscriptions {
		if s.resets == nil {
			return fail("Subscription reset service is unavailable", false)
		}
		count, err := s.resets.Scheduled(ctx, v.AccountID, run.requestID, v.groupIDs, v.actor, v.identity)
		if err != nil {
			if errors.Is(err, ErrScheduledSubscriptionCacheRefresh) {
				return customAccountScheduleResult{status: "succeeded", message: "Active subscriptions reset; cache refresh needs attention", count: count, warning: "subscription_cache_refresh_failed"}
			}
			reason := infraerrors.Reason(err)
			return fail("Subscription reset failed ("+safeCustomAccountScheduleReason(reason)+")", reason == "ACCOUNT_SCHEDULE_GROUPS_CHANGED" || reason == "ACCOUNT_SCHEDULE_IDENTITY_CHANGED" || reason == "CODEX_RESET_SCOPE_CHANGED")
		}
		return customAccountScheduleResult{status: "succeeded", message: "Active subscriptions reset", count: count}
	}
	if s.quota == nil {
		return fail("Reset card service is unavailable", false)
	}
	usage, err := s.quota.QueryUsage(ctx, v.AccountID)
	if err != nil {
		return fail("Could not check available reset cards", false)
	}
	candidate, err := selectCustomAccountScheduleCard(usage, s.now())
	if err != nil {
		return fail(err.Error(), false)
	}
	// QueryUsage may refresh a token or spend time upstream. Reject account
	// edits that happened during that query before preparing the redemption.
	if result := s.verifyScope(ctx, v); result != nil {
		return *result
	}
	// The exact card and request UUID are durably fixed before consumption.
	attempt, err := s.db.ExecContext(ctx, `UPDATE custom_account_action_schedule_runs SET credit_id=$2 WHERE id=$1 AND status='started'`, run.ID, candidate.ID)
	if err != nil {
		return fail("Could not record the reset card attempt", false)
	}
	if n, rowsErr := attempt.RowsAffected(); rowsErr != nil || n != 1 {
		return fail("Attempt is no longer active; no card was consumed", true)
	}
	reset, err := s.quota.ResetCreditTargetedForSchedule(ctx, v.AccountID, candidate.ID, run.requestID, v.identity)
	if err != nil {
		if infraerrors.Reason(err) == "ACCOUNT_SCHEDULE_IDENTITY_CHANGED" {
			return fail("Account identity changed; schedule paused", true)
		}
		code := infraerrors.Code(err)
		if infraerrors.Reason(err) == "OPENAI_QUOTA_RESET_REQUEST_FAILED" || code >= http.StatusInternalServerError || code == http.StatusRequestTimeout || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return customAccountScheduleResult{status: "unknown", message: "Reset card result is unknown; schedule paused for manual review", pause: true}
		}
		return fail("Reset card failed ("+safeCustomAccountScheduleReason(infraerrors.Reason(err))+")", false)
	}
	if reset == nil {
		return customAccountScheduleResult{status: "unknown", message: "Reset card result is unknown; schedule paused for manual review", pause: true}
	}
	code := strings.ToLower(strings.TrimSpace(reset.Code))
	if code == "no_credit" {
		return fail("No reset card is available", false)
	}
	if code != "ok" && code != "success" {
		return customAccountScheduleResult{status: "unknown", message: "Reset card result is unknown; schedule paused for manual review", pause: true}
	}
	// A successful redemption belongs to the pinned upstream identity. Do not
	// clear runtime limits or refresh state for a newly rebound account.
	a, identityErr := s.eligible(ctx, v.AccountID)
	if identityErr != nil || customResetIdentity(a) != v.identity {
		return customAccountScheduleResult{status: "succeeded", message: "Reset card consumed; account identity changed before refresh", count: reset.WindowsReset, warning: "account_identity_changed", pause: true}
	}
	post := RunOpenAIQuotaResetPostProcess(ctx, v.AccountID, s.quota, s.recoverer, s.accounts.GetByID)
	message := "Reset card consumed successfully"
	if post.WarningCode != "" {
		message = "Reset card consumed; account refresh needs attention"
	}
	return customAccountScheduleResult{status: "succeeded", message: message, count: reset.WindowsReset, warning: post.WarningCode}
}

func (s *CustomAccountScheduleService) verifyScope(ctx context.Context, v *CustomAccountSchedule) *customAccountScheduleResult {
	fail := func(message string, pause bool) *customAccountScheduleResult {
		return &customAccountScheduleResult{status: "failed", message: message, pause: pause}
	}
	if ctx.Err() != nil {
		return fail("Action cancelled before execution", false)
	}
	a, err := s.eligible(ctx, v.AccountID)
	if err != nil {
		reason := infraerrors.Reason(err)
		if reason == "ACCOUNT_SCHEDULE_ACCOUNT" || reason == "ACCOUNT_SCHEDULE_IDENTITY" || infraerrors.Code(err) == http.StatusNotFound {
			return fail("Account is no longer eligible; schedule paused", true)
		}
		return fail("Could not verify account eligibility", false)
	}
	if customResetIdentity(a) != v.identity {
		return fail("Account identity changed; schedule paused", true)
	}
	groups, err := s.groups(ctx, s.db, v.AccountID)
	if err != nil {
		return fail("Could not verify account groups", false)
	}
	if !sameCustomAccountScheduleGroups(v.groupIDs, groups) {
		return fail("Account groups changed; review and save the schedule to resume", true)
	}
	return nil
}

func safeCustomAccountScheduleReason(reason string) string {
	if reason == "" {
		return "ACTION_FAILED"
	}
	for _, r := range reason {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return "ACTION_FAILED"
		}
	}
	if len(reason) > 100 {
		return "ACTION_FAILED"
	}
	return reason
}

func selectCustomAccountScheduleCard(usage *OpenAIQuotaUsage, now time.Time) (openAIAutoResetCreditCandidate, error) {
	fail := func(message string) (openAIAutoResetCreditCandidate, error) {
		return openAIAutoResetCreditCandidate{}, errors.New(message)
	}
	if usage == nil || usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount <= 0 {
		return fail("No reset card is available")
	}
	if len(usage.autoResetCandidates) < usage.RateLimitResetCredits.AvailableCount {
		return fail("Reset card details are incomplete; no card was consumed")
	}
	candidates := append([]openAIAutoResetCreditCandidate(nil), usage.autoResetCandidates...)
	for _, c := range candidates {
		if strings.TrimSpace(c.ID) == "" {
			return fail("Reset card details are incomplete; no card was consumed")
		}
		if _, err := time.Parse(time.RFC3339, c.ExpiresAt); err != nil {
			return fail("Reset card expiration is invalid; no card was consumed")
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, candidates[i].ExpiresAt)
		b, _ := time.Parse(time.RFC3339, candidates[j].ExpiresAt)
		return a.Before(b)
	})
	for _, c := range candidates {
		expires, _ := time.Parse(time.RFC3339, c.ExpiresAt)
		if expires.After(now) {
			return c, nil
		}
	}
	return fail("No unexpired reset card is available")
}

func sameCustomAccountScheduleGroups(expected []int64, current []CustomAccountScheduleGroup) bool {
	if len(expected) != len(current) {
		return false
	}
	for i, g := range current {
		if expected[i] != g.ID {
			return false
		}
	}
	return true
}
