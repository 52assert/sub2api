package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceTestCreationRequiresAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{IntelligenceTest: handler.NewIntelligenceTestHandler(nil), Admin: &handler.AdminHandlers{}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
		} else {
			c.AbortWithStatus(http.StatusForbidden)
		}
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), h, adminAuth, audit, stepUp, nil, nil)
	for _, tc := range []struct {
		token string
		want  int
	}{
		{want: http.StatusUnauthorized}, {token: "Bearer regular-user", want: http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/intelligence-tests", nil)
		request.Header.Set("Authorization", tc.token)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.want, recorder.Code)
	}
}

func TestIntelligenceTestResultsRequireAuthenticationAndAllowRegularUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{IntelligenceTest: handler.NewIntelligenceTestHandler(nil)}
	auth := servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	audit := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	RegisterUserRoutes(router.Group("/api/v1"), h, auth, audit, nil, nil)
	for _, path := range []string{"/api/v1/intelligence-tests", "/api/v1/intelligence-tests/1"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	}
	// An ordinary authenticated session reaches input validation rather than an admin gate.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/intelligence-tests/invalid", nil)
	request.Header.Set("Authorization", "Bearer regular-user")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
