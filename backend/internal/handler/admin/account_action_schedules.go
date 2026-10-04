package admin

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type accountActionScheduleService interface {
	List(context.Context, int64) (*service.CustomAccountSchedulesView, error)
	Create(context.Context, int64, service.CustomAccountScheduleInput, int64) (*service.CustomAccountSchedule, error)
	Update(context.Context, int64, int64, service.CustomAccountScheduleInput, int64) (*service.CustomAccountSchedule, error)
	Delete(context.Context, int64, int64) error
	Runs(context.Context, int64, int64, int) ([]service.CustomAccountScheduleRun, error)
}

func (h *AccountHandler) SetCustomAccountScheduleService(s *service.CustomAccountScheduleService) {
	h.accountActionSchedules = s
}

func (h *AccountHandler) actionScheduleIDs(c *gin.Context, withSchedule bool) (int64, int64, bool) {
	account, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || account <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, 0, false
	}
	var schedule int64
	if withSchedule {
		schedule, err = strconv.ParseInt(c.Param("schedule_id"), 10, 64)
		if err != nil || schedule <= 0 {
			response.BadRequest(c, "Invalid schedule ID")
			return 0, 0, false
		}
	}
	if h.accountActionSchedules == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account schedule service unavailable")
		return 0, 0, false
	}
	return account, schedule, true
}

func (h *AccountHandler) ListActionSchedules(c *gin.Context) {
	account, _, ok := h.actionScheduleIDs(c, false)
	if !ok {
		return
	}
	view, err := h.accountActionSchedules.List(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

func (h *AccountHandler) saveActionSchedule(c *gin.Context, update bool) {
	account, schedule, ok := h.actionScheduleIDs(c, update)
	if !ok {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, http.StatusUnauthorized, "Administrator authentication required")
		return
	}
	var input service.CustomAccountScheduleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid schedule settings")
		return
	}
	var result *service.CustomAccountSchedule
	var err error
	if update {
		result, err = h.accountActionSchedules.Update(c.Request.Context(), account, schedule, input, subject.UserID)
	} else {
		result, err = h.accountActionSchedules.Create(c.Request.Context(), account, input, subject.UserID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) CreateActionSchedule(c *gin.Context) { h.saveActionSchedule(c, false) }
func (h *AccountHandler) UpdateActionSchedule(c *gin.Context) { h.saveActionSchedule(c, true) }

func (h *AccountHandler) DeleteActionSchedule(c *gin.Context) {
	account, schedule, ok := h.actionScheduleIDs(c, true)
	if !ok {
		return
	}
	if err := h.accountActionSchedules.Delete(c.Request.Context(), account, schedule); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountHandler) ActionScheduleRuns(c *gin.Context) {
	account, schedule, ok := h.actionScheduleIDs(c, true)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		response.BadRequest(c, "Result limit must be between 1 and 100")
		return
	}
	runs, err := h.accountActionSchedules.Runs(c.Request.Context(), account, schedule, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, runs)
}
