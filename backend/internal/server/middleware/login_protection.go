package middleware

import (
	"context"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	loginProtectionContextKey = "sub2api.login_protection"
	loginSucceededContextKey  = "sub2api.login_succeeded"
)

// NewLoginProtectionMiddleware guards every public authentication flow,
// including OAuth callbacks, so a ban cannot be bypassed by another method.
func NewLoginProtectionMiddleware(protection *service.LoginProtectionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Logout must remain available to revoke an existing session.
		if c.FullPath() == "/api/v1/auth/logout" {
			c.Next()
			return
		}
		ban, err := protection.Check(c.Request.Context(), SecurityClientIP(c))
		if err != nil {
			loginProtectionUnavailable(c)
			return
		}
		if ban != nil {
			SetAuditAction(c, service.AuditActionLoginIPBlocked)
			writeLoginIPBan(c, ban)
			return
		}
		c.Set(loginProtectionContextKey, protection)
		c.Next()
		if c.GetBool(loginSucceededContextKey) && c.Writer.Status() >= 200 && c.Writer.Status() < 400 {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
			defer cancel()
			// A concurrent failure may have banned the IP; RecordSuccess never
			// removes a ban. Failed cleanup conservatively retains the counter.
			_ = protection.RecordSuccess(ctx, SecurityClientIP(c))
		}
	}
}

// RecordLoginFailure is called only for verified credential failures. It writes
// the third failure's ban before returning any response to the caller.
// A true return means the helper already wrote the response.
func RecordLoginFailure(c *gin.Context, authErr error) bool {
	value, ok := c.Get(loginProtectionContextKey)
	if !ok {
		return false
	}
	protection, ok := value.(*service.LoginProtectionService)
	if !ok || protection == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	ban, count, err := protection.RecordFailure(ctx, SecurityClientIP(c))
	if err != nil {
		loginProtectionUnavailable(c)
		return true
	}
	_, status := infraerrors.ToHTTP(authErr)
	SetAuditExtra(c, map[string]any{"failure_count": count, "error_code": status.Reason})
	if ban == nil {
		return false
	}
	SetAuditAction(c, service.AuditActionLoginIPBan)
	writeLoginIPBan(c, ban)
	return true
}

// MarkLoginSuccess must only be called when a full login issues access tokens;
// a successful password check awaiting 2FA is not a completed login.
func MarkLoginSuccess(c *gin.Context) {
	c.Set(loginSucceededContextKey, true)
}

func writeLoginIPBan(c *gin.Context, ban *service.LoginIPBan) {
	SetAuditExtra(c, map[string]any{"failure_count": ban.FailureCount, "blocked_ip": ban.ClientIP})
	response.ErrorWithDetails(c, http.StatusForbidden,
		"This IP has been permanently banned after 3 failed login attempts. Contact an administrator to unblock it.",
		"LOGIN_IP_BANNED", nil)
	c.Abort()
}

func loginProtectionUnavailable(c *gin.Context) {
	SetAuditExtra(c, map[string]any{"error_code": "LOGIN_PROTECTION_UNAVAILABLE"})
	response.ErrorWithDetails(c, http.StatusServiceUnavailable,
		"Login protection is temporarily unavailable. Please try again later.", "LOGIN_PROTECTION_UNAVAILABLE", nil)
	c.Abort()
}
