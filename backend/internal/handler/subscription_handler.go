package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SubscriptionSummaryItem represents a subscription item in summary
type SubscriptionSummaryItem struct {
	ID              int64   `json:"id"`
	GroupID         int64   `json:"group_id"`
	GroupName       string  `json:"group_name"`
	Status          string  `json:"status"`
	DailyUsedUSD    float64 `json:"daily_used_usd,omitempty"`
	DailyLimitUSD   float64 `json:"daily_limit_usd,omitempty"`
	WeeklyUsedUSD   float64 `json:"weekly_used_usd,omitempty"`
	WeeklyLimitUSD  float64 `json:"weekly_limit_usd,omitempty"`
	MonthlyUsedUSD  float64 `json:"monthly_used_usd,omitempty"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd,omitempty"`
	ExpiresAt       *string `json:"expires_at,omitempty"`
}

// SubscriptionProgressInfo represents subscription with progress info
type SubscriptionProgressInfo struct {
	Subscription *dto.UserSubscription         `json:"subscription"`
	Progress     *service.SubscriptionProgress `json:"progress"`
}

// SubscriberAccountProvider provides read-only, redacted subscriber account views.
type SubscriberAccountProvider interface {
	ListSubscriberAccounts(c *gin.Context, allowedGroupIDs map[int64]struct{}, groupNames map[int64]string, isAdmin bool)
	GetSubscriberBatchTodayStats(c *gin.Context, allowedGroupIDs map[int64]struct{}, isAdmin bool)
	GetSubscriberBatchUsage(c *gin.Context, allowedGroupIDs map[int64]struct{}, isAdmin bool)
}

// SubscriberResetVoteManager handles initiating and casting subscriber reset votes.
type SubscriberResetVoteManager interface {
	CastVote(ctx context.Context, userID, accountID int64) (*service.SubscriberResetVoteCastResult, error)
}

// SubscriptionHandler handles user subscription operations
type SubscriptionHandler struct {
	subscriptionService        *service.SubscriptionService
	subscriberAccountProvider  SubscriberAccountProvider
	subscriberResetVoteManager SubscriberResetVoteManager
}

// NewSubscriptionHandler creates a new user subscription handler
func NewSubscriptionHandler(subscriptionService *service.SubscriptionService) *SubscriptionHandler {
	return &SubscriptionHandler{
		subscriptionService: subscriptionService,
	}
}

// SetSubscriberAccountProvider wires the read-only subscriber account provider.
func (h *SubscriptionHandler) SetSubscriberAccountProvider(provider SubscriberAccountProvider) {
	if h == nil {
		return
	}
	h.subscriberAccountProvider = provider
}

// SetSubscriberResetVoteManager wires subscriber reset voting operations.
func (h *SubscriptionHandler) SetSubscriberResetVoteManager(manager SubscriberResetVoteManager) {
	if h == nil {
		return
	}
	h.subscriberResetVoteManager = manager
}

// List handles listing current user's subscriptions
// GET /api/v1/subscriptions
func (h *SubscriptionHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	subscriptions, err := h.subscriptionService.ListUserSubscriptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.UserSubscription, 0, len(subscriptions))
	for i := range subscriptions {
		out = append(out, *dto.UserSubscriptionFromService(&subscriptions[i]).Localized(c.GetHeader("Accept-Language")))
	}
	response.Success(c, out)
}

// GetActive handles getting current user's active subscriptions
// GET /api/v1/subscriptions/active
func (h *SubscriptionHandler) GetActive(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	subscriptions, err := h.subscriptionService.ListActiveUserSubscriptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.UserSubscription, 0, len(subscriptions))
	for i := range subscriptions {
		out = append(out, *dto.UserSubscriptionFromService(&subscriptions[i]).Localized(c.GetHeader("Accept-Language")))
	}
	response.Success(c, out)
}

// GetProgress handles getting subscription progress for current user
// GET /api/v1/subscriptions/progress
func (h *SubscriptionHandler) GetProgress(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	// Get all active subscriptions with progress
	subscriptions, err := h.subscriptionService.ListActiveUserSubscriptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	result := make([]SubscriptionProgressInfo, 0, len(subscriptions))
	for i := range subscriptions {
		sub := &subscriptions[i]
		progress, err := h.subscriptionService.GetSubscriptionProgress(c.Request.Context(), sub.ID)
		if err != nil {
			// Skip subscriptions with errors
			continue
		}
		if sub.Group != nil {
			progress.GroupName = sub.Group.I18n.LocalizeName(c.GetHeader("Accept-Language"), progress.GroupName)
		}
		result = append(result, SubscriptionProgressInfo{
			Subscription: dto.UserSubscriptionFromService(sub).Localized(c.GetHeader("Accept-Language")),
			Progress:     progress,
		})
	}

	response.Success(c, result)
}

// GetSummary handles getting a summary of current user's subscription status
// GET /api/v1/subscriptions/summary
func (h *SubscriptionHandler) GetSummary(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	// Get all active subscriptions
	subscriptions, err := h.subscriptionService.ListActiveUserSubscriptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	var totalUsed float64
	items := make([]SubscriptionSummaryItem, 0, len(subscriptions))

	for _, sub := range subscriptions {
		item := SubscriptionSummaryItem{
			ID:             sub.ID,
			GroupID:        sub.GroupID,
			Status:         sub.Status,
			DailyUsedUSD:   sub.DailyUsageUSD,
			WeeklyUsedUSD:  sub.WeeklyUsageUSD,
			MonthlyUsedUSD: sub.MonthlyUsageUSD,
		}

		// Add group info if preloaded
		if sub.Group != nil {
			item.GroupName = sub.Group.I18n.LocalizeName(c.GetHeader("Accept-Language"), sub.Group.Name)
			if sub.Group.DailyLimitUSD != nil {
				item.DailyLimitUSD = *sub.Group.DailyLimitUSD
			}
			if sub.Group.WeeklyLimitUSD != nil {
				item.WeeklyLimitUSD = *sub.Group.WeeklyLimitUSD
			}
			if sub.Group.MonthlyLimitUSD != nil {
				item.MonthlyLimitUSD = *sub.Group.MonthlyLimitUSD
			}
		}

		// Format expiration time
		if !sub.ExpiresAt.IsZero() {
			formatted := sub.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
			item.ExpiresAt = &formatted
		}

		// Track total usage (use monthly as the most comprehensive)
		totalUsed += sub.MonthlyUsageUSD

		items = append(items, item)
	}

	summary := struct {
		ActiveCount   int                       `json:"active_count"`
		TotalUsedUSD  float64                   `json:"total_used_usd"`
		Subscriptions []SubscriptionSummaryItem `json:"subscriptions"`
	}{
		ActiveCount:   len(subscriptions),
		TotalUsedUSD:  totalUsed,
		Subscriptions: items,
	}

	response.Success(c, summary)
}

func (h *SubscriptionHandler) resolveSubscriberAllowedGroups(c *gin.Context) (map[int64]struct{}, map[int64]string, bool, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return nil, nil, false, false
	}

	subscriptions, err := h.subscriptionService.ListActiveUserSubscriptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, nil, false, false
	}

	allowedGroupIDs := make(map[int64]struct{}, len(subscriptions))
	groupNames := make(map[int64]string, len(subscriptions))
	acceptLang := c.GetHeader("Accept-Language")
	for i := range subscriptions {
		sub := &subscriptions[i]
		if sub.GroupID <= 0 {
			continue
		}
		if sub.Group != nil && sub.Group.SubscriptionType != "" && !sub.Group.IsSubscriptionType() {
			continue
		}
		allowedGroupIDs[sub.GroupID] = struct{}{}
		if sub.Group != nil {
			groupNames[sub.GroupID] = sub.Group.I18n.LocalizeName(acceptLang, sub.Group.Name)
		}
	}

	role, _ := middleware2.GetUserRoleFromContext(c)
	return allowedGroupIDs, groupNames, role == service.RoleAdmin, true
}

// ListSubscriberAccounts handles listing redacted shared accounts visible to current subscriber
// GET /api/v1/subscriptions/accounts
func (h *SubscriptionHandler) ListSubscriberAccounts(c *gin.Context) {
	if h == nil || h.subscriberAccountProvider == nil {
		response.Paginated(c, []any{}, 0, 1, 20)
		return
	}
	allowedGroupIDs, groupNames, isAdmin, ok := h.resolveSubscriberAllowedGroups(c)
	if !ok {
		return
	}
	h.subscriberAccountProvider.ListSubscriberAccounts(c, allowedGroupIDs, groupNames, isAdmin)
}

// GetSubscriberAccountsBatchTodayStats handles getting today's stats for subscriber-visible accounts
// POST /api/v1/subscriptions/accounts/today-stats/batch
func (h *SubscriptionHandler) GetSubscriberAccountsBatchTodayStats(c *gin.Context) {
	if h == nil || h.subscriberAccountProvider == nil {
		response.Success(c, gin.H{"stats": map[string]any{}})
		return
	}
	allowedGroupIDs, _, isAdmin, ok := h.resolveSubscriberAllowedGroups(c)
	if !ok {
		return
	}
	h.subscriberAccountProvider.GetSubscriberBatchTodayStats(c, allowedGroupIDs, isAdmin)
}

// GetSubscriberAccountsBatchUsage handles getting passive usage windows for subscriber-visible accounts
// POST /api/v1/subscriptions/accounts/usage/batch
func (h *SubscriptionHandler) GetSubscriberAccountsBatchUsage(c *gin.Context) {
	if h == nil || h.subscriberAccountProvider == nil {
		response.Success(c, gin.H{"usage": map[string]any{}, "errors": map[string]string{}})
		return
	}
	allowedGroupIDs, _, isAdmin, ok := h.resolveSubscriberAllowedGroups(c)
	if !ok {
		return
	}
	h.subscriberAccountProvider.GetSubscriberBatchUsage(c, allowedGroupIDs, isAdmin)
}

// CastSubscriberAccountResetVote initiates or casts a vote to reset a shared account.
// POST /api/v1/subscriptions/accounts/:id/reset-votes
func (h *SubscriptionHandler) CastSubscriberAccountResetVote(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h == nil || h.subscriberResetVoteManager == nil {
		response.Error(c, http.StatusServiceUnavailable, "Reset voting service unavailable")
		return
	}
	res, err := h.subscriberResetVoteManager.CastVote(c.Request.Context(), subject.UserID, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, res)
}
