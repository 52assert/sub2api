package admin

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type containerUpdateService interface {
	IsContainerBuild() bool
	StartContainerUpdate(context.Context, string) (*service.ContainerUpdateJob, error)
	ContainerUpdateStatus(context.Context) (*service.ContainerUpdateJob, error)
}

func (h *SystemHandler) performContainerUpdate(c *gin.Context, updater containerUpdateService) {
	var request struct {
		Image string `json:"image" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Error(c, http.StatusBadRequest, "请选择要安装的版本")
		return
	}
	executeAdminIdempotentJSON(c, "admin.system.container-update", request,
		service.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
			job, err := updater.StartContainerUpdate(ctx, request.Image)
			if err != nil {
				return nil, err
			}
			return gin.H{"message": "升级任务已提交", "need_restart": false, "job": job}, nil
		})
}

// ContainerUpdateStatus returns durable state from the host even after this
// application container has been replaced. The route requires admin auth.
func (h *SystemHandler) ContainerUpdateStatus(c *gin.Context) {
	updater, ok := h.updateSvc.(containerUpdateService)
	if !ok || !updater.IsContainerBuild() {
		response.Error(c, http.StatusNotFound, "Container updater is not available")
		return
	}
	job, err := updater.ContainerUpdateStatus(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "无法读取升级任务，请稍后重试")
		return
	}
	response.Success(c, job)
}
