package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
)

const (
	SettingKeyCustomUserAgentLimitSettings = "custom_user_agent_limit_settings"

	defaultUserAgentLimitEnabled       = true
	defaultUserAgentLimitMax           = 5
	defaultUserAgentLimitWindowDays    = 30
	defaultUserAgentLimitExemptAdmin   = true
	minUserAgentLimitMax               = 1
	maxUserAgentLimitMax               = 1000
	minUserAgentLimitWindowDays        = 1
	maxUserAgentLimitWindowDays        = 365
	maxUserAgentPolicyNoteBytes        = 500
	userAgentLimitCacheTTL             = 5 * time.Second
	userAgentLimitTouchCacheTTL        = 5 * time.Minute
	userAgentLimitMaxTouchCacheEntries = 50000
	userAgentLimitDBTimeout            = 5 * time.Second
	userAgentAdvisoryLockPrefix        = int64(0x55414C00) << 32
)

// CustomUserAgentLimitSettings configures the rolling-window distinct User-Agent limit.
type CustomUserAgentLimitSettings struct {
	Enabled       bool `json:"enabled"`
	MaxUserAgents int  `json:"max_user_agents"`
	WindowDays    int  `json:"window_days"`
	ExemptAdmin   bool `json:"exempt_admin"`
}

// DefaultCustomUserAgentLimitSettings returns default User-Agent limit settings (enabled, 5 per 30 days, admins exempt).
func DefaultCustomUserAgentLimitSettings() CustomUserAgentLimitSettings {
	return CustomUserAgentLimitSettings{
		Enabled:       defaultUserAgentLimitEnabled,
		MaxUserAgents: defaultUserAgentLimitMax,
		WindowDays:    defaultUserAgentLimitWindowDays,
		ExemptAdmin:   defaultUserAgentLimitExemptAdmin,
	}
}

// CustomUserAgentRecord represents one normalized User-Agent entry seen for a user.
type CustomUserAgentRecord struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	UAKey        string    `json:"ua_key"`
	UALabel      string    `json:"ua_label"`
	LastRawUA    string    `json:"last_raw_ua"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	RequestCount int64     `json:"request_count"`
}

// CustomUserAgentUserPolicy stores optional per-user overrides for User-Agent limits.
// MaxUserAgents: nil = follow global default, 0 = exempt/unlimited, >0 = custom per-user max.
type CustomUserAgentUserPolicy struct {
	MaxUserAgents *int       `json:"max_user_agents"`
	Note          string     `json:"note"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// CustomUserAgentUserSummary summarizes a user's active rolling-window User-Agent count and effective limit.
type CustomUserAgentUserSummary struct {
	UserID         int64  `json:"user_id"`
	Enabled        bool   `json:"enabled"`
	ActiveCount    int    `json:"active_count"`
	EffectiveLimit int    `json:"effective_limit"`
	GlobalLimit    int    `json:"global_limit"`
	CustomLimit    *int   `json:"custom_limit"`
	Exempt         bool   `json:"exempt"`
	AtLimit        bool   `json:"at_limit"`
	Exceeded       bool   `json:"exceeded"`
	WindowDays     int    `json:"window_days"`
	Note           string `json:"note"`
}

// CustomUserAgentUserDetail holds a user's full User-Agent policy, summary, and active rolling-window records.
type CustomUserAgentUserDetail struct {
	UserID    int64                        `json:"user_id"`
	UserEmail string                       `json:"user_email"`
	Username  string                       `json:"username"`
	UserRole  string                       `json:"user_role"`
	Settings  CustomUserAgentLimitSettings `json:"settings"`
	Policy    CustomUserAgentUserPolicy    `json:"policy"`
	Summary   CustomUserAgentUserSummary   `json:"summary"`
	Records   []CustomUserAgentRecord      `json:"records"`
}

// UserAgentLimitExceededError is returned when a request introduces a new normalized User-Agent beyond the user's limit.
type UserAgentLimitExceededError struct {
	UserID       int64
	ActiveCount  int
	MaxAllowed   int
	WindowDays   int
	NormalizedUA string
}

func (e *UserAgentLimitExceededError) Error() string {
	return fmt.Sprintf(
		"近 %d 天内使用的客户端环境 (User-Agent) 数量已达上限 (%d/%d)，已拒绝新客户端环境 %q 的请求，请联系管理员处理 (User-Agent limit exceeded: %d/%d distinct clients in the last %d days)",
		e.WindowDays, e.ActiveCount, e.MaxAllowed, e.NormalizedUA, e.ActiveCount, e.MaxAllowed, e.WindowDays,
	)
}

type cachedUserAgentPolicyEntry struct {
	policy    CustomUserAgentUserPolicy
	expiresAt time.Time
}

type userAgentTouchKey struct {
	userID int64
	uaKey  string
}

// CustomUserAgentLimitService enforces and manages per-user rolling-window User-Agent limits.
type CustomUserAgentLimitService struct {
	db          *sql.DB
	settingRepo SettingRepository
	now         func() time.Time

	settingsMu        sync.RWMutex
	settingsCached    *CustomUserAgentLimitSettings
	settingsExpiresAt time.Time

	policyMu    sync.RWMutex
	policyCache map[int64]cachedUserAgentPolicyEntry

	touchMu    sync.Mutex
	touchCache map[userAgentTouchKey]time.Time
}

// ProvideCustomUserAgentLimitService constructs the User-Agent limit service for dependency injection.
func ProvideCustomUserAgentLimitService(db *sql.DB, settingRepo SettingRepository) *CustomUserAgentLimitService {
	return &CustomUserAgentLimitService{
		db:          db,
		settingRepo: settingRepo,
		now:         time.Now,
		policyCache: make(map[int64]cachedUserAgentPolicyEntry),
		touchCache:  make(map[userAgentTouchKey]time.Time),
	}
}

func (s *CustomUserAgentLimitService) nowFunc() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func userAgentAdvisoryLockID(userID int64) int64 {
	return userAgentAdvisoryLockPrefix | (userID & 0xffffffff)
}

func sanitizeUserAgentLimitSettings(in CustomUserAgentLimitSettings) (CustomUserAgentLimitSettings, error) {
	if in.MaxUserAgents < minUserAgentLimitMax || in.MaxUserAgents > maxUserAgentLimitMax {
		return CustomUserAgentLimitSettings{}, infraerrors.BadRequest(
			"INVALID_USER_AGENT_LIMIT_SETTINGS",
			fmt.Sprintf("max_user_agents must be between %d and %d", minUserAgentLimitMax, maxUserAgentLimitMax),
		)
	}
	if in.WindowDays < minUserAgentLimitWindowDays || in.WindowDays > maxUserAgentLimitWindowDays {
		return CustomUserAgentLimitSettings{}, infraerrors.BadRequest(
			"INVALID_USER_AGENT_LIMIT_SETTINGS",
			fmt.Sprintf("window_days must be between %d and %d", minUserAgentLimitWindowDays, maxUserAgentLimitWindowDays),
		)
	}
	return in, nil
}

// GetSettings returns the effective User-Agent limit settings (or defaults when not configured).
func (s *CustomUserAgentLimitService) GetSettings(ctx context.Context) (*CustomUserAgentLimitSettings, error) {
	defaults := DefaultCustomUserAgentLimitSettings()
	if s == nil {
		return &defaults, nil
	}

	now := s.nowFunc()
	s.settingsMu.RLock()
	if s.settingsCached != nil && now.Before(s.settingsExpiresAt) {
		cp := *s.settingsCached
		s.settingsMu.RUnlock()
		return &cp, nil
	}
	s.settingsMu.RUnlock()

	if s.settingRepo == nil {
		return &defaults, nil
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyCustomUserAgentLimitSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			s.cacheSettings(defaults, now)
			cp := defaults
			return &cp, nil
		}
		return nil, fmt.Errorf("get user-agent limit settings: %w", err)
	}

	parsed := defaults
	if strings.TrimSpace(raw) != "" {
		if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr != nil {
			slog.Warn("invalid custom_user_agent_limit_settings JSON, falling back to defaults", "error", jsonErr)
			parsed = defaults
		}
	}
	if parsed.MaxUserAgents < minUserAgentLimitMax || parsed.MaxUserAgents > maxUserAgentLimitMax {
		parsed.MaxUserAgents = defaults.MaxUserAgents
	}
	if parsed.WindowDays < minUserAgentLimitWindowDays || parsed.WindowDays > maxUserAgentLimitWindowDays {
		parsed.WindowDays = defaults.WindowDays
	}

	s.cacheSettings(parsed, now)
	cp := parsed
	return &cp, nil
}

func (s *CustomUserAgentLimitService) cacheSettings(settings CustomUserAgentLimitSettings, now time.Time) {
	s.settingsMu.Lock()
	cp := settings
	s.settingsCached = &cp
	s.settingsExpiresAt = now.Add(userAgentLimitCacheTTL)
	s.settingsMu.Unlock()
}

// UpdateSettings validates, persists, and invalidates caches for global User-Agent limit settings.
func (s *CustomUserAgentLimitService) UpdateSettings(ctx context.Context, input CustomUserAgentLimitSettings) (*CustomUserAgentLimitSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	validated, err := sanitizeUserAgentLimitSettings(input)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(validated)
	if err != nil {
		return nil, fmt.Errorf("marshal user-agent limit settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCustomUserAgentLimitSettings, string(raw)); err != nil {
		return nil, fmt.Errorf("save user-agent limit settings: %w", err)
	}

	s.invalidateAllCaches()
	s.cacheSettings(validated, s.nowFunc())
	cp := validated
	return &cp, nil
}

func (s *CustomUserAgentLimitService) invalidateAllCaches() {
	s.settingsMu.Lock()
	s.settingsCached = nil
	s.settingsExpiresAt = time.Time{}
	s.settingsMu.Unlock()

	s.policyMu.Lock()
	s.policyCache = make(map[int64]cachedUserAgentPolicyEntry)
	s.policyMu.Unlock()

	s.touchMu.Lock()
	s.touchCache = make(map[userAgentTouchKey]time.Time)
	s.touchMu.Unlock()
}

func (s *CustomUserAgentLimitService) invalidateUserCaches(userID int64) {
	s.policyMu.Lock()
	delete(s.policyCache, userID)
	s.policyMu.Unlock()

	s.touchMu.Lock()
	for k := range s.touchCache {
		if k.userID == userID {
			delete(s.touchCache, k)
		}
	}
	s.touchMu.Unlock()
}

func (s *CustomUserAgentLimitService) getCachedTouch(userID int64, uaKey string, now time.Time) bool {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	ts, ok := s.touchCache[userAgentTouchKey{userID: userID, uaKey: uaKey}]
	if !ok {
		return false
	}
	if now.Sub(ts) < userAgentLimitTouchCacheTTL {
		return true
	}
	delete(s.touchCache, userAgentTouchKey{userID: userID, uaKey: uaKey})
	return false
}

func (s *CustomUserAgentLimitService) setCachedTouch(userID int64, uaKey string, now time.Time) {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	if len(s.touchCache) >= userAgentLimitMaxTouchCacheEntries {
		cutoff := now.Add(-userAgentLimitTouchCacheTTL)
		for k, ts := range s.touchCache {
			if ts.Before(cutoff) {
				delete(s.touchCache, k)
			}
		}
		if len(s.touchCache) >= userAgentLimitMaxTouchCacheEntries {
			s.touchCache = make(map[userAgentTouchKey]time.Time)
		}
	}
	s.touchCache[userAgentTouchKey{userID: userID, uaKey: uaKey}] = now
}

func (s *CustomUserAgentLimitService) getUserPolicy(ctx context.Context, userID int64, now time.Time) (CustomUserAgentUserPolicy, error) {
	s.policyMu.RLock()
	entry, ok := s.policyCache[userID]
	s.policyMu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.policy, nil
	}

	var policy CustomUserAgentUserPolicy
	if s.db == nil {
		return policy, nil
	}
	var maxUA sql.NullInt64
	var note sql.NullString
	var updatedAt sql.NullTime
	err := s.db.QueryRowContext(
		ctx,
		`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`,
		userID,
	).Scan(&maxUA, &note, &updatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return policy, fmt.Errorf("query user-agent user policy: %w", err)
	}
	if err == nil {
		if maxUA.Valid {
			v := int(maxUA.Int64)
			policy.MaxUserAgents = &v
		}
		if note.Valid {
			policy.Note = note.String
		}
		if updatedAt.Valid {
			t := updatedAt.Time.UTC()
			policy.UpdatedAt = &t
		}
	}

	s.policyMu.Lock()
	s.policyCache[userID] = cachedUserAgentPolicyEntry{
		policy:    policy,
		expiresAt: now.Add(userAgentLimitCacheTTL),
	}
	s.policyMu.Unlock()
	return policy, nil
}

func resolveEffectiveUserAgentPolicy(
	settings CustomUserAgentLimitSettings,
	userRole string,
	policy CustomUserAgentUserPolicy,
) (effectiveLimit int, exempt bool) {
	if policy.MaxUserAgents != nil {
		if *policy.MaxUserAgents <= 0 {
			return 0, true
		}
		return *policy.MaxUserAgents, false
	}
	if settings.ExemptAdmin && strings.EqualFold(strings.TrimSpace(userRole), RoleAdmin) {
		return 0, true
	}
	return settings.MaxUserAgents, false
}

// CheckAndRecord enforces the rolling-window distinct User-Agent limit for a gateway request.
// Requests whose normalized User-Agent is already active in the window continue to succeed even if
// the user is already at the limit; introducing another distinct normalized User-Agent beyond
// the effective limit returns *UserAgentLimitExceededError.
func (s *CustomUserAgentLimitService) CheckAndRecord(ctx context.Context, userID int64, userRole string, rawUA string) error {
	if s == nil || s.db == nil || userID <= 0 {
		return nil
	}

	dbCtx, cancel := context.WithTimeout(ctx, userAgentLimitDBTimeout)
	defer cancel()

	settings, err := s.GetSettings(dbCtx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit settings lookup failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}
	if settings == nil || !settings.Enabled {
		return nil
	}

	now := s.nowFunc()
	uaKey, uaLabel := NormalizeUserAgentForLimit(rawUA)

	// Hot-path optimization: normalized UAs recently persisted for this user already count toward the
	// active rolling window, so skip opening a write transaction until the touch TTL elapses.
	if s.getCachedTouch(userID, uaKey, now) {
		return nil
	}

	policy, policyErr := s.getUserPolicy(dbCtx, userID, now)
	if policyErr != nil && ctx.Err() == nil {
		slog.Warn("user-agent policy lookup failed, using global policy", "user_id", userID, "error", policyErr)
	}
	effectiveMax, exempt := resolveEffectiveUserAgentPolicy(*settings, userRole, policy)

	rawStored := TruncateRawUserAgent(rawUA)
	cutoff := now.Add(-time.Duration(settings.WindowDays) * 24 * time.Hour)

	tx, err := s.db.BeginTx(dbCtx, nil)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit begin tx failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(dbCtx, `SELECT pg_advisory_xact_lock($1)`, userAgentAdvisoryLockID(userID)); err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit advisory lock failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}

	res, err := tx.ExecContext(
		dbCtx,
		`UPDATE custom_user_agent_records
		 SET ua_label = $3,
		     last_raw_ua = $4,
		     last_seen_at = $5,
		     request_count = request_count + 1
		 WHERE user_id = $1 AND ua_key = $2 AND last_seen_at >= $6`,
		userID, uaKey, uaLabel, rawStored, now, cutoff,
	)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit active update failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}
	if affected, affErr := res.RowsAffected(); affErr == nil && affected > 0 {
		if commitErr := tx.Commit(); commitErr != nil {
			if ctx.Err() == nil {
				slog.Warn("user-agent limit commit failed, failing open", "user_id", userID, "error", commitErr)
			}
			return nil
		}
		s.setCachedTouch(userID, uaKey, now)
		return nil
	}

	if !exempt && effectiveMax > 0 {
		var activeCount int
		if err := tx.QueryRowContext(
			dbCtx,
			`SELECT COUNT(*) FROM custom_user_agent_records WHERE user_id = $1 AND last_seen_at >= $2`,
			userID, cutoff,
		).Scan(&activeCount); err != nil {
			if ctx.Err() == nil {
				slog.Warn("user-agent limit count query failed, failing open", "user_id", userID, "error", err)
			}
			return nil
		}
		if activeCount >= effectiveMax {
			return &UserAgentLimitExceededError{
				UserID:       userID,
				ActiveCount:  activeCount,
				MaxAllowed:   effectiveMax,
				WindowDays:   settings.WindowDays,
				NormalizedUA: uaLabel,
			}
		}
	}

	if _, err := tx.ExecContext(
		dbCtx,
		`INSERT INTO custom_user_agent_records (
			user_id, ua_key, ua_label, last_raw_ua, first_seen_at, last_seen_at, request_count
		) VALUES ($1, $2, $3, $4, $5, $5, 1)
		ON CONFLICT (user_id, ua_key) DO UPDATE
		SET ua_label = EXCLUDED.ua_label,
		    last_raw_ua = EXCLUDED.last_raw_ua,
		    first_seen_at = CASE
		        WHEN custom_user_agent_records.last_seen_at < $6 THEN EXCLUDED.first_seen_at
		        ELSE custom_user_agent_records.first_seen_at
		    END,
		    last_seen_at = EXCLUDED.last_seen_at,
		    request_count = CASE
		        WHEN custom_user_agent_records.last_seen_at < $6 THEN 1
		        ELSE custom_user_agent_records.request_count + 1
		    END`,
		userID, uaKey, uaLabel, rawStored, now, cutoff,
	); err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit upsert failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}

	if err := tx.Commit(); err != nil {
		if ctx.Err() == nil {
			slog.Warn("user-agent limit commit failed, failing open", "user_id", userID, "error", err)
		}
		return nil
	}
	s.setCachedTouch(userID, uaKey, now)
	return nil
}

func dedupePositiveUserIDs(userIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(userIDs))
	out := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func buildUserAgentSummary(
	userID int64,
	userRole string,
	settings CustomUserAgentLimitSettings,
	policy CustomUserAgentUserPolicy,
	activeCount int,
) CustomUserAgentUserSummary {
	effectiveLimit, exempt := resolveEffectiveUserAgentPolicy(settings, userRole, policy)
	atLimit := settings.Enabled && !exempt && effectiveLimit > 0 && activeCount == effectiveLimit
	exceeded := settings.Enabled && !exempt && effectiveLimit > 0 && activeCount > effectiveLimit
	var customLimit *int
	if policy.MaxUserAgents != nil {
		v := *policy.MaxUserAgents
		customLimit = &v
	}
	return CustomUserAgentUserSummary{
		UserID:         userID,
		Enabled:        settings.Enabled,
		ActiveCount:    activeCount,
		EffectiveLimit: effectiveLimit,
		GlobalLimit:    settings.MaxUserAgents,
		CustomLimit:    customLimit,
		Exempt:         exempt,
		AtLimit:        atLimit,
		Exceeded:       exceeded,
		WindowDays:     settings.WindowDays,
		Note:           policy.Note,
	}
}

// GetUsersSummaryBatch returns rolling-window User-Agent summaries keyed by user ID.
func (s *CustomUserAgentLimitService) GetUsersSummaryBatch(ctx context.Context, userIDs []int64) (map[int64]*CustomUserAgentUserSummary, *CustomUserAgentLimitSettings, error) {
	if s == nil || s.db == nil {
		return nil, nil, infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	settingsPtr, err := s.GetSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	settings := *settingsPtr
	uniqueIDs := dedupePositiveUserIDs(userIDs)
	out := make(map[int64]*CustomUserAgentUserSummary, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return out, &settings, nil
	}

	roles := make(map[int64]string, len(uniqueIDs))
	roleRows, err := s.db.QueryContext(
		ctx,
		`SELECT id, role FROM users WHERE id = ANY($1) AND deleted_at IS NULL`,
		pq.Array(uniqueIDs),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query user roles for user-agent summary: %w", err)
	}
	for roleRows.Next() {
		var uid int64
		var role string
		if scanErr := roleRows.Scan(&uid, &role); scanErr != nil {
			_ = roleRows.Close()
			return nil, nil, fmt.Errorf("scan user role: %w", scanErr)
		}
		roles[uid] = role
	}
	if err := roleRows.Err(); err != nil {
		_ = roleRows.Close()
		return nil, nil, fmt.Errorf("iterate user roles: %w", err)
	}
	_ = roleRows.Close()

	cutoff := s.nowFunc().Add(-time.Duration(settings.WindowDays) * 24 * time.Hour)
	counts := make(map[int64]int, len(uniqueIDs))
	countRows, err := s.db.QueryContext(
		ctx,
		`SELECT user_id, COUNT(*)
		 FROM custom_user_agent_records
		 WHERE user_id = ANY($1) AND last_seen_at >= $2
		 GROUP BY user_id`,
		pq.Array(uniqueIDs), cutoff,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query user-agent counts batch: %w", err)
	}
	for countRows.Next() {
		var uid int64
		var cnt int
		if scanErr := countRows.Scan(&uid, &cnt); scanErr != nil {
			_ = countRows.Close()
			return nil, nil, fmt.Errorf("scan user-agent count: %w", scanErr)
		}
		counts[uid] = cnt
	}
	if err := countRows.Err(); err != nil {
		_ = countRows.Close()
		return nil, nil, fmt.Errorf("iterate user-agent counts: %w", err)
	}
	_ = countRows.Close()

	policies := make(map[int64]CustomUserAgentUserPolicy, len(uniqueIDs))
	policyRows, err := s.db.QueryContext(
		ctx,
		`SELECT user_id, max_user_agents, note, updated_at
		 FROM custom_user_agent_user_policies
		 WHERE user_id = ANY($1)`,
		pq.Array(uniqueIDs),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query user-agent policies batch: %w", err)
	}
	for policyRows.Next() {
		var uid int64
		var maxUA sql.NullInt64
		var note sql.NullString
		var updatedAt sql.NullTime
		if scanErr := policyRows.Scan(&uid, &maxUA, &note, &updatedAt); scanErr != nil {
			_ = policyRows.Close()
			return nil, nil, fmt.Errorf("scan user-agent policy: %w", scanErr)
		}
		var p CustomUserAgentUserPolicy
		if maxUA.Valid {
			v := int(maxUA.Int64)
			p.MaxUserAgents = &v
		}
		if note.Valid {
			p.Note = note.String
		}
		if updatedAt.Valid {
			t := updatedAt.Time.UTC()
			p.UpdatedAt = &t
		}
		policies[uid] = p
	}
	if err := policyRows.Err(); err != nil {
		_ = policyRows.Close()
		return nil, nil, fmt.Errorf("iterate user-agent policies: %w", err)
	}
	_ = policyRows.Close()

	for _, uid := range uniqueIDs {
		summary := buildUserAgentSummary(uid, roles[uid], settings, policies[uid], counts[uid])
		out[uid] = &summary
	}
	return out, &settings, nil
}

// GetUserDetail returns a user's rolling-window User-Agent policy, summary, and active normalized records.
func (s *CustomUserAgentLimitService) GetUserDetail(ctx context.Context, userID int64) (*CustomUserAgentUserDetail, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER_ID", "Valid user_id is required")
	}

	var email, username, role string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT email, COALESCE(username, ''), role FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID,
	).Scan(&email, &username, &role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "User not found")
		}
		return nil, fmt.Errorf("query user for user-agent detail: %w", err)
	}

	settingsPtr, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	settings := *settingsPtr

	var policy CustomUserAgentUserPolicy
	var maxUA sql.NullInt64
	var note sql.NullString
	var updatedAt sql.NullTime
	err = s.db.QueryRowContext(
		ctx,
		`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`,
		userID,
	).Scan(&maxUA, &note, &updatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("query user-agent policy: %w", err)
	}
	if err == nil {
		if maxUA.Valid {
			v := int(maxUA.Int64)
			policy.MaxUserAgents = &v
		}
		if note.Valid {
			policy.Note = note.String
		}
		if updatedAt.Valid {
			t := updatedAt.Time.UTC()
			policy.UpdatedAt = &t
		}
	}

	cutoff := s.nowFunc().Add(-time.Duration(settings.WindowDays) * 24 * time.Hour)
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, user_id, ua_key, ua_label, last_raw_ua, first_seen_at, last_seen_at, request_count
		 FROM custom_user_agent_records
		 WHERE user_id = $1 AND last_seen_at >= $2
		 ORDER BY last_seen_at DESC, id DESC`,
		userID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query user-agent records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]CustomUserAgentRecord, 0)
	for rows.Next() {
		var rec CustomUserAgentRecord
		if scanErr := rows.Scan(
			&rec.ID,
			&rec.UserID,
			&rec.UAKey,
			&rec.UALabel,
			&rec.LastRawUA,
			&rec.FirstSeenAt,
			&rec.LastSeenAt,
			&rec.RequestCount,
		); scanErr != nil {
			return nil, fmt.Errorf("scan user-agent record: %w", scanErr)
		}
		rec.FirstSeenAt = rec.FirstSeenAt.UTC()
		rec.LastSeenAt = rec.LastSeenAt.UTC()
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user-agent records: %w", err)
	}

	summary := buildUserAgentSummary(userID, role, settings, policy, len(records))
	return &CustomUserAgentUserDetail{
		UserID:    userID,
		UserEmail: email,
		Username:  username,
		UserRole:  role,
		Settings:  settings,
		Policy:    policy,
		Summary:   summary,
		Records:   records,
	}, nil
}

// UpdateUserPolicy sets or clears a per-user User-Agent limit override and note.
func (s *CustomUserAgentLimitService) UpdateUserPolicy(ctx context.Context, userID int64, maxUserAgents *int, note string) (*CustomUserAgentUserDetail, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER_ID", "Valid user_id is required")
	}
	if maxUserAgents != nil && (*maxUserAgents < 0 || *maxUserAgents > maxUserAgentLimitMax) {
		return nil, infraerrors.BadRequest(
			"INVALID_USER_AGENT_POLICY",
			fmt.Sprintf("max_user_agents must be between 0 and %d", maxUserAgentLimitMax),
		)
	}
	note = strings.TrimSpace(note)
	if len(note) > maxUserAgentPolicyNoteBytes {
		return nil, infraerrors.BadRequest(
			"INVALID_USER_AGENT_POLICY",
			fmt.Sprintf("note must be at most %s bytes", strconv.Itoa(maxUserAgentPolicyNoteBytes)),
		)
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "User not found")
		}
		return nil, fmt.Errorf("verify user for policy update: %w", err)
	}

	if maxUserAgents == nil && note == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM custom_user_agent_user_policies WHERE user_id = $1`, userID); err != nil {
			return nil, fmt.Errorf("delete user-agent policy: %w", err)
		}
	} else {
		var nullableMax sql.NullInt64
		if maxUserAgents != nil {
			nullableMax = sql.NullInt64{Int64: int64(*maxUserAgents), Valid: true}
		}
		now := s.nowFunc()
		if _, err := s.db.ExecContext(
			ctx,
			`INSERT INTO custom_user_agent_user_policies (user_id, max_user_agents, note, updated_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id) DO UPDATE
			 SET max_user_agents = EXCLUDED.max_user_agents,
			     note = EXCLUDED.note,
			     updated_at = EXCLUDED.updated_at`,
			userID, nullableMax, note, now,
		); err != nil {
			return nil, fmt.Errorf("upsert user-agent policy: %w", err)
		}
	}

	s.invalidateUserCaches(userID)
	return s.GetUserDetail(ctx, userID)
}

// DeleteUserRecord deletes one normalized User-Agent record belonging to userID.
func (s *CustomUserAgentLimitService) DeleteUserRecord(ctx context.Context, userID, recordID int64) error {
	if s == nil || s.db == nil {
		return infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	if userID <= 0 || recordID <= 0 {
		return infraerrors.BadRequest("INVALID_REQUEST", "Valid user_id and record_id are required")
	}
	res, err := s.db.ExecContext(
		ctx,
		`DELETE FROM custom_user_agent_records WHERE id = $1 AND user_id = $2`,
		recordID, userID,
	)
	if err != nil {
		return fmt.Errorf("delete user-agent record: %w", err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return infraerrors.NotFound("USER_AGENT_RECORD_NOT_FOUND", "User-Agent record not found")
	}
	s.invalidateUserCaches(userID)
	return nil
}

// ClearUserRecords deletes all recorded User-Agent entries for userID.
func (s *CustomUserAgentLimitService) ClearUserRecords(ctx context.Context, userID int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, infraerrors.ServiceUnavailable("USER_AGENT_LIMIT_UNAVAILABLE", "User-Agent limit service is unavailable")
	}
	if userID <= 0 {
		return 0, infraerrors.BadRequest("INVALID_USER_ID", "Valid user_id is required")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, infraerrors.NotFound("USER_NOT_FOUND", "User not found")
		}
		return 0, fmt.Errorf("verify user for clear records: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_user_agent_records WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("clear user-agent records: %w", err)
	}
	deleted, _ := res.RowsAffected()
	s.invalidateUserCaches(userID)
	return deleted, nil
}
