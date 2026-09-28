package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *AccountHandler) SetCustomCodexResetService(s *service.CustomCodexResetService) {
	h.customCodexReset = s
}
func (h *AccountHandler) subscriptionResetID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	if h.customCodexReset == nil {
		response.Error(c, http.StatusServiceUnavailable, "Subscription reset service unavailable")
		return 0, false
	}
	return id, true
}
func (h *AccountHandler) SubscriptionResetPreview(c *gin.Context) {
	id, ok := h.subscriptionResetID(c)
	if !ok {
		return
	}
	v, err := h.customCodexReset.Preview(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *AccountHandler) ConfigureSubscriptionReset(c *gin.Context) {
	id, ok := h.subscriptionResetID(c)
	if !ok {
		return
	}
	var request struct {
		Enabled             *bool `json:"enabled" binding:"required"`
		NaturalProbeEnabled *bool `json:"natural_probe_enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "enabled is required")
		return
	}
	if err := h.customCodexReset.Configure(c.Request.Context(), id, *request.Enabled, request.NaturalProbeEnabled); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"enabled": *request.Enabled})
}
func (h *AccountHandler) ResetGroupSubscriptions(c *gin.Context) {
	id, ok := h.subscriptionResetID(c)
	if !ok {
		return
	}
	var request struct {
		OperationID string `json:"operation_id" binding:"required"`
		Fingerprint string `json:"fingerprint" binding:"required,len=64"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "A preview and operation ID are required")
		return
	}
	operation, err := uuid.Parse(request.OperationID)
	if err != nil {
		response.BadRequest(c, "Invalid operation ID")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "Admin authentication required")
		return
	}
	n, err := h.customCodexReset.Manual(c.Request.Context(), id, operation.String(), request.Fingerprint, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"reset_count": n})
}
