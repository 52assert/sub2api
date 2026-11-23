package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const maxBatchUserAgentSummaryIDs = 500

// SetCustomUserAgentLimitService wires CustomUserAgentLimitService into UserHandler without changing its constructor.
func (h *UserHandler) SetCustomUserAgentLimitService(s *service.CustomUserAgentLimitService) {
	if h == nil {
		return
	}
	h.userAgentLimit = s
}

// CustomUserAgentLimitService exposes the configured CustomUserAgentLimitService for router middleware wiring.
func (h *UserHandler) CustomUserAgentLimitService() *service.CustomUserAgentLimitService {
	if h == nil {
		return nil
	}
	return h.userAgentLimit
}

// SetCustomUserAgentLimitService wires CustomUserAgentLimitService into SettingHandler without changing its constructor.
func (h *SettingHandler) SetCustomUserAgentLimitService(s *service.CustomUserAgentLimitService) {
	if h == nil {
		return
	}
	h.userAgentLimit = s
}

// UpdateUserAgentLimitSettingsRequest represents admin updates to global User-Agent limit settings.
type UpdateUserAgentLimitSettingsRequest struct {
	Enabled       bool `json:"enabled"`
	MaxUserAgents int  `json:"max_user_agents" binding:"required,min=1,max=1000"`
	WindowDays    int  `json:"window_days" binding:"required,min=1,max=365"`
	ExemptAdmin   bool `json:"exempt_admin"`
}

// GetUserAgentLimitSettings returns the global User-Agent limit settings.
// GET /api/v1/admin/settings/user-agent-limit
func (h *SettingHandler) GetUserAgentLimitSettings(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	settings, err := h.userAgentLimit.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateUserAgentLimitSettings updates the global User-Agent limit settings.
// PUT /api/v1/admin/settings/user-agent-limit
func (h *SettingHandler) UpdateUserAgentLimitSettings(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	var req UpdateUserAgentLimitSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.userAgentLimit.UpdateSettings(c.Request.Context(), service.CustomUserAgentLimitSettings{
		Enabled:       req.Enabled,
		MaxUserAgents: req.MaxUserAgents,
		WindowDays:    req.WindowDays,
		ExemptAdmin:   req.ExemptAdmin,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

// BatchUserAgentSummariesRequest requests rolling-window User-Agent summaries for a list of users.
type BatchUserAgentSummariesRequest struct {
	UserIDs []int64 `json:"user_ids"`
}

// BatchGetUserAgentSummaries returns rolling-window User-Agent summaries for the given user IDs.
// POST /api/v1/admin/users/user-agent-summaries
func (h *UserHandler) BatchGetUserAgentSummaries(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	var req BatchUserAgentSummariesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.UserIDs) > maxBatchUserAgentSummaryIDs {
		response.BadRequest(c, "Too many user_ids (maximum 500)")
		return
	}
	summaries, settings, err := h.userAgentLimit.GetUsersSummaryBatch(c.Request.Context(), req.UserIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"settings":  settings,
		"summaries": summaries,
	})
}

// GetUserAgentLimitDetail returns a single user's User-Agent policy, summary, and active rolling-window records.
// GET /api/v1/admin/users/:id/user-agents
func (h *UserHandler) GetUserAgentLimitDetail(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	detail, err := h.userAgentLimit.GetUserDetail(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}

// UpdateUserAgentPolicyRequest updates a single user's User-Agent limit policy override.
// max_user_agents: null = follow global default, 0 = exempt/unlimited, 1..1000 = custom limit.
type UpdateUserAgentPolicyRequest struct {
	MaxUserAgents *int   `json:"max_user_agents" binding:"omitempty,min=0,max=1000"`
	Note          string `json:"note" binding:"omitempty,max=500"`
}

// UpdateUserAgentLimitPolicy updates a single user's User-Agent limit override and note.
// PUT /api/v1/admin/users/:id/user-agents/policy
func (h *UserHandler) UpdateUserAgentLimitPolicy(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req UpdateUserAgentPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	detail, err := h.userAgentLimit.UpdateUserPolicy(c.Request.Context(), userID, req.MaxUserAgents, req.Note)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}

// DeleteUserAgentRecord deletes a single normalized User-Agent record for a user.
// DELETE /api/v1/admin/users/:id/user-agents/:record_id
func (h *UserHandler) DeleteUserAgentRecord(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	recordID, err := strconv.ParseInt(c.Param("record_id"), 10, 64)
	if err != nil || recordID <= 0 {
		response.BadRequest(c, "Invalid record ID")
		return
	}
	if err := h.userAgentLimit.DeleteUserRecord(c.Request.Context(), userID, recordID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ClearUserAgentRecords removes all recorded User-Agent entries for a user.
// DELETE /api/v1/admin/users/:id/user-agents
func (h *UserHandler) ClearUserAgentRecords(c *gin.Context) {
	if h == nil || h.userAgentLimit == nil {
		response.Error(c, http.StatusServiceUnavailable, "User-Agent limit service is unavailable")
		return
	}
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	deleted, err := h.userAgentLimit.ClearUserRecords(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted_count": deleted})
}
