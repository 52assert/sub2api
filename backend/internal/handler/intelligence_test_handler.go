package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type IntelligenceTestHandler struct {
	tests *service.IntelligenceTestService
}

func NewIntelligenceTestHandler(tests *service.IntelligenceTestService) *IntelligenceTestHandler {
	return &IntelligenceTestHandler{tests: tests}
}

// Create is registered only on the admin account-management router.
func (h *IntelligenceTestHandler) Create(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		Model           string `json:"model" binding:"required,max=200"`
		ReasoningEffort string `json:"reasoning_effort" binding:"max=16"`
		Prompt          string `json:"prompt" binding:"max=32768"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid model, thinking level or prompt")
		return
	}
	record, err := h.tests.Create(c.Request.Context(), id, req.Model, req.ReasoningEffort, req.Prompt)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Accepted(c, record)
}

func (h *IntelligenceTestHandler) List(c *gin.Context) {
	items, err := h.tests.List(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	if items == nil {
		items = []*service.IntelligenceTest{}
	}
	response.Success(c, gin.H{"items": items, "retention": service.IntelligenceTestRetention})
}

func (h *IntelligenceTestHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid test ID")
		return
	}
	record, err := h.tests.GetByID(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, record)
}
