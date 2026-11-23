package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UserAgentLimiter checks and records normalized client User-Agent identifiers per user.
type UserAgentLimiter interface {
	CheckAndRecord(ctx context.Context, userID int64, userRole string, rawUA string) error
}

type userAgentLimiterHolder struct {
	limiter UserAgentLimiter
}

var activeUserAgentLimiter atomic.Pointer[userAgentLimiterHolder]

// SetUserAgentLimiter registers the active User-Agent limiter used by API key auth middlewares.
func SetUserAgentLimiter(limiter UserAgentLimiter) {
	if limiter == nil {
		activeUserAgentLimiter.Store(nil)
		return
	}
	activeUserAgentLimiter.Store(&userAgentLimiterHolder{limiter: limiter})
}

func abortIfUserAgentLimitExceeded(c *gin.Context, apiKey *service.APIKey, googleStyle bool) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.User == nil {
		return false
	}
	holder := activeUserAgentLimiter.Load()
	if holder == nil || holder.limiter == nil {
		return false
	}

	err := holder.limiter.CheckAndRecord(c.Request.Context(), apiKey.User.ID, apiKey.User.Role, c.GetHeader("User-Agent"))
	if err == nil {
		return false
	}

	var limitErr *service.UserAgentLimitExceededError
	if !errors.As(err, &limitErr) {
		return false
	}

	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	MarkIngressRejected(c, IngressRejectUserAgentLimitExceeded)
	if googleStyle {
		abortWithGoogleError(c, http.StatusForbidden, limitErr.Error())
		return true
	}

	msg := limitErr.Error()
	c.JSON(http.StatusForbidden, gin.H{
		"code":    "USER_AGENT_LIMIT_EXCEEDED",
		"message": msg,
		"error": gin.H{
			"type":    "permission_error",
			"code":    "USER_AGENT_LIMIT_EXCEEDED",
			"message": msg,
		},
	})
	c.Abort()
	return true
}
