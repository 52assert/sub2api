package admin

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

// SubscriberGroupRef represents a safe, read-only group badge reference for subscribers.
type SubscriberGroupRef struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Platform         string `json:"platform"`
	SubscriptionType string `json:"subscription_type,omitempty"`
}

// SubscriberAccountItem represents a strictly redacted, read-only account view for subscription members.
type SubscriberAccountItem struct {
	ID                        int64                      `json:"id"`
	Name                      string                     `json:"name"`
	Platform                  string                     `json:"platform"`
	Type                      string                     `json:"type"`
	Credentials               map[string]any             `json:"credentials,omitempty"`
	Extra                     map[string]any             `json:"extra,omitempty"`
	OllamaCloudUsage          *service.OllamaCloudUsageState `json:"ollama_cloud_usage,omitempty"`
	OpenCodeGoUsage           *service.OpenCodeGoUsageState  `json:"opencode_go_usage,omitempty"`
	Concurrency               int                        `json:"concurrency"`
	LoadFactor                *int                       `json:"load_factor,omitempty"`
	CurrentConcurrency        int                        `json:"current_concurrency"`
	Status                    string                     `json:"status"`
	Schedulable               bool                       `json:"schedulable"`
	LastUsedAt                *time.Time                 `json:"last_used_at"`
	ExpiresAt                 *int64                     `json:"expires_at"`
	AutoPauseOnExpired        bool                       `json:"auto_pause_on_expired"`
	CreatedAt                 time.Time                  `json:"created_at"`
	UpdatedAt                 time.Time                  `json:"updated_at"`
	RateLimitedAt             *time.Time                 `json:"rate_limited_at"`
	RateLimitResetAt          *time.Time                 `json:"rate_limit_reset_at"`
	OverloadUntil             *time.Time                 `json:"overload_until"`
	TempUnschedulableUntil    *time.Time                 `json:"temp_unschedulable_until"`
	SessionWindowStart        *time.Time                 `json:"session_window_start"`
	SessionWindowEnd          *time.Time                 `json:"session_window_end"`
	SessionWindowStatus       string                     `json:"session_window_status"`
	WindowCostLimit           *float64                   `json:"window_cost_limit,omitempty"`
	WindowCostStickyReserve   *float64                   `json:"window_cost_sticky_reserve,omitempty"`
	CurrentWindowCost         *float64                   `json:"current_window_cost,omitempty"`
	MaxSessions               *int                       `json:"max_sessions,omitempty"`
	SessionIdleTimeoutMin     *int                       `json:"session_idle_timeout_minutes,omitempty"`
	ActiveSessions            *int                       `json:"active_sessions,omitempty"`
	BaseRPM                   *int                           `json:"base_rpm,omitempty"`
	RPMStrategy               *string                        `json:"rpm_strategy,omitempty"`
	RPMStickyBuffer           *int                           `json:"rpm_sticky_buffer,omitempty"`
	CurrentRPM                *int                       `json:"current_rpm,omitempty"`
	QuotaLimit                *float64                   `json:"quota_limit,omitempty"`
	QuotaUsed                 *float64                   `json:"quota_used,omitempty"`
	QuotaDailyLimit           *float64                   `json:"quota_daily_limit,omitempty"`
	QuotaDailyUsed            *float64                   `json:"quota_daily_used,omitempty"`
	QuotaWeeklyLimit          *float64                   `json:"quota_weekly_limit,omitempty"`
	QuotaWeeklyUsed           *float64                   `json:"quota_weekly_used,omitempty"`
	QuotaDailyResetMode       *string                    `json:"quota_daily_reset_mode,omitempty"`
	QuotaDailyResetHour       *int                       `json:"quota_daily_reset_hour,omitempty"`
	QuotaWeeklyResetMode      *string                    `json:"quota_weekly_reset_mode,omitempty"`
	QuotaWeeklyResetDay       *int                       `json:"quota_weekly_reset_day,omitempty"`
	QuotaWeeklyResetHour      *int                       `json:"quota_weekly_reset_hour,omitempty"`
	QuotaResetTimezone        *string                    `json:"quota_reset_timezone,omitempty"`
	QuotaDailyResetAt         *string                    `json:"quota_daily_reset_at,omitempty"`
	QuotaWeeklyResetAt        *string                    `json:"quota_weekly_reset_at,omitempty"`
	QuotaDimension            string                     `json:"quota_dimension,omitempty"`
	ParentPlanType            string                     `json:"parent_plan_type,omitempty"`
	ParentPrivacyMode         string                     `json:"parent_privacy_mode,omitempty"`
	ParentSubscriptionExpires string                     `json:"parent_subscription_expires_at,omitempty"`
	GroupIDs                  []int64                    `json:"group_ids,omitempty"`
	Groups                    []*SubscriberGroupRef      `json:"groups,omitempty"`
}

func accountGroupIDsFromService(acc *service.Account) []int64 {
	if acc == nil {
		return nil
	}
	seen := make(map[int64]struct{})
	out := make([]int64, 0, len(acc.Groups)+len(acc.AccountGroups))
	for _, g := range acc.Groups {
		if g == nil || g.ID <= 0 {
			continue
		}
		if _, ok := seen[g.ID]; !ok {
			seen[g.ID] = struct{}{}
			out = append(out, g.ID)
		}
	}
	for _, ag := range acc.AccountGroups {
		if ag.GroupID <= 0 {
			continue
		}
		if _, ok := seen[ag.GroupID]; !ok {
			seen[ag.GroupID] = struct{}{}
			out = append(out, ag.GroupID)
		}
	}
	return out
}

func isAccountAuthorizedForSubscriber(acc *service.Account, allowedGroupIDs map[int64]struct{}, isAdmin bool) bool {
	if !service.IsAccountSubscriberVisible(acc) {
		return false
	}
	if isAdmin {
		return true
	}
	if len(allowedGroupIDs) == 0 {
		return false
	}
	for _, gid := range accountGroupIDsFromService(acc) {
		if _, ok := allowedGroupIDs[gid]; ok {
			return true
		}
	}
	return false
}

var subscriberSafeCredentialKeys = []string{
	"plan_type",
	"subscription_tier",
	"auth_mode",
	"tier_id",
	"oauth_type",
	"provider_mode",
}

var subscriberSafeExtraExactKeys = map[string]struct{}{
	"privacy_mode":              {},
	"subscription_expires_at":   {},
	"subscription_tier":         {},
	"model_rate_limits":         {},
	"allow_overages":            {},
	"grok_billing_snapshot":     {},
	"grok_usage_snapshot":       {},
	"grok_quota_snapshot":       {},
	"openai_compact_mode":       {},
	"openai_compact_supported":  {},
	"openai_compact_checked_at": {},
	"quota_daily_reset_mode":    {},
	"quota_weekly_reset_mode":   {},
	"quota_daily_reset_at":      {},
	"quota_weekly_reset_at":     {},
	"quota_daily_start":         {},
	"quota_weekly_start":        {},
	"codex_usage_updated_at":    {},
}

var subscriberSafeExtraPrefixes = []string{
	"codex_5h_",
	"codex_7d_",
	"openai_oauth_last_5h_",
	"openai_oauth_last_7d_",
	"openai_oauth_usage_",
}

var subscriberSafeExtraSuffixes = []string{
	"_5h_used_percent",
	"_5h_reset_at",
	"_weekly_used_percent",
	"_weekly_reset_at",
	"_monthly_used_percent",
	"_monthly_reset_at",
	"_usage_updated_at",
	"_balance",
	"_balance_currency",
	"_balances",
	"_balance_low",
}

func sanitizeSubscriberCredentials(creds map[string]any) map[string]any {
	if len(creds) == 0 {
		return nil
	}
	out := make(map[string]any)
	for _, key := range subscriberSafeCredentialKeys {
		if v, ok := creds[key]; ok && v != nil {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeSubscriberExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	out := make(map[string]any)
	for key, val := range extra {
		if val == nil {
			continue
		}
		if key == "load_code_assist" {
			if lca, ok := val.(map[string]any); ok {
				safeLCA := make(map[string]any)
				for _, k := range []string{"paidTier", "currentTier", "ineligibleTiers"} {
					if v, exists := lca[k]; exists {
						safeLCA[k] = v
					}
				}
				if len(safeLCA) > 0 {
					out[key] = safeLCA
				}
			}
			continue
		}
		if _, ok := subscriberSafeExtraExactKeys[key]; ok {
			out[key] = val
			continue
		}
		matched := false
		for _, prefix := range subscriberSafeExtraPrefixes {
			if strings.HasPrefix(key, prefix) {
				out[key] = val
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for _, suffix := range subscriberSafeExtraSuffixes {
			if strings.HasSuffix(key, suffix) {
				out[key] = val
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toSubscriberAccountItem(
	item AccountWithConcurrency,
	allowedGroupIDs map[int64]struct{},
	groupNames map[int64]string,
	isAdmin bool,
) SubscriberAccountItem {
	acc := item.Account
	filteredGroupIDs := make([]int64, 0, len(acc.GroupIDs))
	filteredGroups := make([]*SubscriberGroupRef, 0, len(acc.Groups))
	seenGroupIDs := make(map[int64]struct{})

	for _, g := range acc.Groups {
		if g == nil {
			continue
		}
		if !isAdmin {
			if _, ok := allowedGroupIDs[g.ID]; !ok {
				continue
			}
		}
		seenGroupIDs[g.ID] = struct{}{}
		filteredGroupIDs = append(filteredGroupIDs, g.ID)
		name := g.Name
		if localized, ok := groupNames[g.ID]; ok && localized != "" {
			name = localized
		}
		filteredGroups = append(filteredGroups, &SubscriberGroupRef{
			ID:               g.ID,
			Name:             name,
			Platform:         g.Platform,
			SubscriptionType: g.SubscriptionType,
		})
	}

	for _, gid := range acc.GroupIDs {
		if _, seen := seenGroupIDs[gid]; seen {
			continue
		}
		if !isAdmin {
			if _, ok := allowedGroupIDs[gid]; !ok {
				continue
			}
		}
		seenGroupIDs[gid] = struct{}{}
		filteredGroupIDs = append(filteredGroupIDs, gid)
		if name, ok := groupNames[gid]; ok && name != "" {
			filteredGroups = append(filteredGroups, &SubscriberGroupRef{
				ID:       gid,
				Name:     name,
				Platform: acc.Platform,
			})
		}
	}

	return SubscriberAccountItem{
		ID:                        acc.ID,
		Name:                      acc.Name,
		Platform:                  acc.Platform,
		Type:                      acc.Type,
		Credentials:               sanitizeSubscriberCredentials(acc.Credentials),
		Extra:                     sanitizeSubscriberExtra(acc.Extra),
		OllamaCloudUsage:          acc.OllamaCloudUsage,
		OpenCodeGoUsage:           acc.OpenCodeGoUsage,
		Concurrency:               acc.Concurrency,
		LoadFactor:                acc.LoadFactor,
		CurrentConcurrency:        item.CurrentConcurrency,
		Status:                    acc.Status,
		Schedulable:               acc.Schedulable,
		LastUsedAt:                acc.LastUsedAt,
		ExpiresAt:                 acc.ExpiresAt,
		AutoPauseOnExpired:        acc.AutoPauseOnExpired,
		CreatedAt:                 acc.CreatedAt,
		UpdatedAt:                 acc.UpdatedAt,
		RateLimitedAt:             acc.RateLimitedAt,
		RateLimitResetAt:          acc.RateLimitResetAt,
		OverloadUntil:             acc.OverloadUntil,
		TempUnschedulableUntil:    acc.TempUnschedulableUntil,
		SessionWindowStart:        acc.SessionWindowStart,
		SessionWindowEnd:          acc.SessionWindowEnd,
		SessionWindowStatus:       acc.SessionWindowStatus,
		WindowCostLimit:           acc.WindowCostLimit,
		WindowCostStickyReserve:   acc.WindowCostStickyReserve,
		CurrentWindowCost:         item.CurrentWindowCost,
		MaxSessions:               acc.MaxSessions,
		SessionIdleTimeoutMin:     acc.SessionIdleTimeoutMin,
		ActiveSessions:            item.ActiveSessions,
		BaseRPM:                   acc.BaseRPM,
		RPMStrategy:               acc.RPMStrategy,
		RPMStickyBuffer:           acc.RPMStickyBuffer,
		CurrentRPM:                item.CurrentRPM,
		QuotaLimit:                acc.QuotaLimit,
		QuotaUsed:                 acc.QuotaUsed,
		QuotaDailyLimit:           acc.QuotaDailyLimit,
		QuotaDailyUsed:            acc.QuotaDailyUsed,
		QuotaWeeklyLimit:          acc.QuotaWeeklyLimit,
		QuotaWeeklyUsed:           acc.QuotaWeeklyUsed,
		QuotaDailyResetMode:       acc.QuotaDailyResetMode,
		QuotaDailyResetHour:       acc.QuotaDailyResetHour,
		QuotaWeeklyResetMode:      acc.QuotaWeeklyResetMode,
		QuotaWeeklyResetDay:       acc.QuotaWeeklyResetDay,
		QuotaWeeklyResetHour:      acc.QuotaWeeklyResetHour,
		QuotaResetTimezone:        acc.QuotaResetTimezone,
		QuotaDailyResetAt:         acc.QuotaDailyResetAt,
		QuotaWeeklyResetAt:        acc.QuotaWeeklyResetAt,
		QuotaDimension:            acc.QuotaDimension,
		ParentPlanType:            acc.ParentPlanType,
		ParentPrivacyMode:         acc.ParentPrivacyMode,
		ParentSubscriptionExpires: acc.ParentSubscriptionExpiresAt,
		GroupIDs:                  filteredGroupIDs,
		Groups:                    filteredGroups,
	}
}

func sortSubscriberAccounts(accounts []service.Account, sortBy, sortOrder string) {
	desc := strings.EqualFold(strings.TrimSpace(sortOrder), "desc")
	key := strings.ToLower(strings.TrimSpace(sortBy))
	slices.SortStableFunc(accounts, func(a, b service.Account) int {
		var c int
		switch key {
		case "id":
			c = cmp.Compare(a.ID, b.ID)
		case "status":
			c = cmp.Compare(a.Status, b.Status)
		case "schedulable":
			if a.Schedulable != b.Schedulable {
				if a.Schedulable {
					c = -1
				} else {
					c = 1
				}
			}
		case "last_used_at":
			var ta, tb int64
			if a.LastUsedAt != nil {
				ta = a.LastUsedAt.UnixNano()
			}
			if b.LastUsedAt != nil {
				tb = b.LastUsedAt.UnixNano()
			}
			c = cmp.Compare(ta, tb)
		case "expires_at":
			var ea, eb int64
			if a.ExpiresAt != nil {
				ea = a.ExpiresAt.UnixNano()
			}
			if b.ExpiresAt != nil {
				eb = b.ExpiresAt.UnixNano()
			}
			c = cmp.Compare(ea, eb)
		case "created_at":
			c = cmp.Compare(a.CreatedAt.UnixNano(), b.CreatedAt.UnixNano())
		default: // "name"
			c = cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
		}
		if desc {
			return -c
		}
		return c
	})
}

// ListSubscriberAccounts returns paginated, redacted accounts visible to the current subscriber.
func (h *AccountHandler) ListSubscriberAccounts(
	c *gin.Context,
	allowedGroupIDs map[int64]struct{},
	groupNames map[int64]string,
	isAdmin bool,
) {
	page, pageSize := response.ParsePagination(c)
	platform := strings.TrimSpace(c.Query("platform"))
	accountType := strings.TrimSpace(c.Query("type"))
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}
	sortBy := strings.TrimSpace(c.DefaultQuery("sort_by", "name"))
	sortOrder := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort_order", "asc")))
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "asc"
	}

	var groupFilterID int64
	if groupStr := strings.TrimSpace(c.Query("group")); groupStr != "" {
		parsedID, err := strconv.ParseInt(groupStr, 10, 64)
		if err != nil || parsedID <= 0 {
			response.Paginated(c, []SubscriberAccountItem{}, 0, page, pageSize)
			return
		}
		if !isAdmin {
			if _, ok := allowedGroupIDs[parsedID]; !ok {
				response.Paginated(c, []SubscriberAccountItem{}, 0, page, pageSize)
				return
			}
		}
		groupFilterID = parsedID
	}

	if !isAdmin && len(allowedGroupIDs) == 0 {
		response.Paginated(c, []SubscriberAccountItem{}, 0, page, pageSize)
		return
	}

	allCandidates, err := h.adminService.ListAccountsForSchedulerScoreFilter(
		c.Request.Context(),
		platform,
		accountType,
		status,
		search,
		groupFilterID,
		"",
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	visibleAccounts := make([]service.Account, 0, len(allCandidates))
	for i := range allCandidates {
		acc := &allCandidates[i]
		if !isAccountAuthorizedForSubscriber(acc, allowedGroupIDs, isAdmin) {
			continue
		}
		visibleAccounts = append(visibleAccounts, *acc)
	}

	sortSubscriberAccounts(visibleAccounts, sortBy, sortOrder)

	total := int64(len(visibleAccounts))
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start >= len(visibleAccounts) {
		response.Paginated(c, []SubscriberAccountItem{}, total, page, pageSize)
		return
	}
	end := start + pageSize
	if end > len(visibleAccounts) {
		end = len(visibleAccounts)
	}
	pageAccounts := visibleAccounts[start:end]

	accountIDs := make([]int64, len(pageAccounts))
	for i := range pageAccounts {
		accountIDs[i] = pageAccounts[i].ID
	}

	concurrencyCounts := make(map[int64]int)
	if h.concurrencyService != nil && len(accountIDs) > 0 {
		if cc, ccErr := h.concurrencyService.GetAccountConcurrencyBatch(c.Request.Context(), accountIDs); ccErr == nil && cc != nil {
			concurrencyCounts = cc
		}
	}

	windowCostAccountIDs := make([]int64, 0)
	sessionLimitAccountIDs := make([]int64, 0)
	rpmAccountIDs := make([]int64, 0)
	sessionIdleTimeouts := make(map[int64]time.Duration)
	for i := range pageAccounts {
		acc := &pageAccounts[i]
		if acc.IsAnthropicOAuthOrSetupToken() {
			if acc.GetWindowCostLimit() > 0 {
				windowCostAccountIDs = append(windowCostAccountIDs, acc.ID)
			}
			if acc.GetMaxSessions() > 0 {
				sessionLimitAccountIDs = append(sessionLimitAccountIDs, acc.ID)
				sessionIdleTimeouts[acc.ID] = time.Duration(acc.GetSessionIdleTimeoutMinutes()) * time.Minute
			}
			if acc.GetBaseRPM() > 0 {
				rpmAccountIDs = append(rpmAccountIDs, acc.ID)
			}
		}
	}

	var rpmCounts map[int64]int
	if len(rpmAccountIDs) > 0 && h.rpmCache != nil {
		rpmCounts, _ = h.rpmCache.GetRPMBatch(c.Request.Context(), rpmAccountIDs)
	}

	var activeSessions map[int64]int
	if len(sessionLimitAccountIDs) > 0 && h.sessionLimitCache != nil {
		activeSessions, _ = h.sessionLimitCache.GetActiveSessionCountBatch(c.Request.Context(), sessionLimitAccountIDs, sessionIdleTimeouts)
	}

	var windowCosts map[int64]float64
	if len(windowCostAccountIDs) > 0 && h.accountUsageService != nil {
		windowCosts = make(map[int64]float64)
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(c.Request.Context())
		g.SetLimit(10)
		for i := range pageAccounts {
			acc := &pageAccounts[i]
			if !acc.IsAnthropicOAuthOrSetupToken() || acc.GetWindowCostLimit() <= 0 {
				continue
			}
			accCopy := acc
			g.Go(func() error {
				startTime := accCopy.GetCurrentWindowStartTime()
				stats, statsErr := h.accountUsageService.GetAccountWindowStats(gctx, accCopy.ID, startTime)
				if statsErr == nil && stats != nil {
					mu.Lock()
					windowCosts[accCopy.ID] = stats.StandardCost
					mu.Unlock()
				}
				return nil
			})
		}
		_ = g.Wait()
	}

	enriched := make([]AccountWithConcurrency, len(pageAccounts))
	for i := range pageAccounts {
		acc := &pageAccounts[i]
		accountResponse := h.accountResponseFromService(acc)
		item := AccountWithConcurrency{
			Account:            accountResponse,
			CurrentConcurrency: concurrencyCounts[acc.ID],
		}
		if windowCosts != nil {
			if cost, ok := windowCosts[acc.ID]; ok {
				item.CurrentWindowCost = &cost
			}
		}
		if activeSessions != nil {
			if count, ok := activeSessions[acc.ID]; ok {
				item.ActiveSessions = &count
			}
		}
		if rpmCounts != nil {
			if rpm, ok := rpmCounts[acc.ID]; ok {
				item.CurrentRPM = &rpm
			}
		}
		enriched[i] = item
	}

	h.enrichShadowParents(c.Request.Context(), enriched)

	out := make([]SubscriberAccountItem, len(enriched))
	for i := range enriched {
		out[i] = toSubscriberAccountItem(enriched[i], allowedGroupIDs, groupNames, isAdmin)
	}

	response.Paginated(c, out, total, page, pageSize)
}

func (h *AccountHandler) filterAuthorizedSubscriberAccountIDs(
	c *gin.Context,
	requestedIDs []int64,
	allowedGroupIDs map[int64]struct{},
	isAdmin bool,
) ([]int64, error) {
	normalizedIDs := normalizeInt64IDList(requestedIDs)
	if len(normalizedIDs) == 0 || (!isAdmin && len(allowedGroupIDs) == 0) {
		return nil, nil
	}
	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), normalizedIDs)
	if err != nil {
		return nil, err
	}
	allowed := make([]int64, 0, len(accounts))
	for _, acc := range accounts {
		if isAccountAuthorizedForSubscriber(acc, allowedGroupIDs, isAdmin) {
			allowed = append(allowed, acc.ID)
		}
	}
	return allowed, nil
}

// GetSubscriberBatchTodayStats returns today's stats for subscriber-authorized accounts only.
func (h *AccountHandler) GetSubscriberBatchTodayStats(
	c *gin.Context,
	allowedGroupIDs map[int64]struct{},
	isAdmin bool,
) {
	var req BatchTodayStatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	authorizedIDs, err := h.filterAuthorizedSubscriberAccountIDs(c, req.AccountIDs, allowedGroupIDs, isAdmin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if len(authorizedIDs) == 0 {
		response.Success(c, gin.H{"stats": map[string]any{}})
		return
	}

	stats, err := h.accountUsageService.GetTodayStatsBatch(c.Request.Context(), authorizedIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"stats": stats})
}

// GetSubscriberBatchUsage returns passive/cached usage windows for subscriber-authorized accounts only.
func (h *AccountHandler) GetSubscriberBatchUsage(
	c *gin.Context,
	allowedGroupIDs map[int64]struct{},
	isAdmin bool,
) {
	var req BatchUsageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	authorizedIDs, err := h.filterAuthorizedSubscriberAccountIDs(c, req.AccountIDs, allowedGroupIDs, isAdmin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if len(authorizedIDs) == 0 {
		response.Success(c, gin.H{
			"usage":  map[string]any{},
			"errors": map[string]string{},
		})
		return
	}

	// Subscribers can never force upstream active probes; always pass force=false.
	usageByAccount, errorsByAccount, err := h.accountUsageService.GetUsageBatch(c.Request.Context(), authorizedIDs, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	for key, info := range usageByAccount {
		if info == nil {
			continue
		}
		sanitized := *info
		sanitized.ValidationURL = ""
		usageByAccount[key] = &sanitized
	}

	response.Success(c, gin.H{
		"usage":  usageByAccount,
		"errors": errorsByAccount,
	})
}
