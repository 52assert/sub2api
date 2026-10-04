package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountScheduleHandlerFake struct {
	accountActionScheduleService
	account, schedule, actor int64
	input                    service.CustomAccountScheduleInput
	limit                    int
	calls                    int
}

func (f *accountScheduleHandlerFake) Create(_ context.Context, account int64, input service.CustomAccountScheduleInput, actor int64) (*service.CustomAccountSchedule, error) {
	f.account, f.actor, f.input = account, actor, input
	f.calls++
	return &service.CustomAccountSchedule{ID: 42, AccountID: account, Action: input.Action}, nil
}

func (f *accountScheduleHandlerFake) Update(_ context.Context, account, schedule int64, input service.CustomAccountScheduleInput, actor int64) (*service.CustomAccountSchedule, error) {
	f.schedule = schedule
	return f.Create(context.Background(), account, input, actor)
}

func (f *accountScheduleHandlerFake) Runs(_ context.Context, account, schedule int64, limit int) ([]service.CustomAccountScheduleRun, error) {
	f.account, f.schedule, f.limit = account, schedule, limit
	f.calls++
	if account != 7 {
		return nil, infraerrors.NotFound("ACCOUNT_SCHEDULE_NOT_FOUND", "Schedule not found for this account")
	}
	return []service.CustomAccountScheduleRun{{ID: 5, ScheduleID: schedule, AccountID: account, Status: "succeeded"}}, nil
}

func TestAccountActionScheduleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	makeRouter := func(fake *accountScheduleHandlerFake, actor int64) *gin.Engine {
		router := gin.New()
		if actor > 0 {
			router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor}) })
		}
		h := &AccountHandler{accountActionSchedules: fake}
		router.POST("/accounts/:id/action-schedules", h.CreateActionSchedule)
		router.PUT("/accounts/:id/action-schedules/:schedule_id", h.UpdateActionSchedule)
		router.GET("/accounts/:id/action-schedules/:schedule_id/runs", h.ActionScheduleRuns)
		return router
	}
	request := func(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}
	t.Run("records the authenticated actor and Cron settings", func(t *testing.T) {
		fake := &accountScheduleHandlerFake{}
		response := request(makeRouter(fake, 9), http.MethodPost, "/accounts/7/action-schedules", `{"action":"reset_card","frequency":"cron","timezone":"Asia/Shanghai","cron_expression":"0 8 * * 1","enabled":true}`)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, int64(7), fake.account)
		require.Equal(t, int64(9), fake.actor)
		require.Equal(t, "0 8 * * 1", fake.input.CronExpression)
		require.True(t, fake.input.Enabled)
	})
	t.Run("passes account ownership and schedule ID on update", func(t *testing.T) {
		fake := &accountScheduleHandlerFake{}
		response := request(makeRouter(fake, 9), http.MethodPut, "/accounts/7/action-schedules/42", `{"action":"reset_subscriptions","frequency":"daily","time_of_day":"08:00","enabled":false}`)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, int64(42), fake.schedule)
		require.Equal(t, int64(7), fake.account)
		require.False(t, fake.input.Enabled)
	})
	t.Run("rejects unauthenticated writes and invalid inputs before service calls", func(t *testing.T) {
		for _, test := range []struct {
			actor  int64
			path   string
			body   string
			status int
		}{
			{0, "/accounts/7/action-schedules", `{}`, http.StatusUnauthorized},
			{9, "/accounts/-1/action-schedules", `{}`, http.StatusBadRequest},
			{9, "/accounts/invalid/action-schedules", `{}`, http.StatusBadRequest},
			{9, "/accounts/7/action-schedules", `{"enabled":"yes"}`, http.StatusBadRequest},
		} {
			fake := &accountScheduleHandlerFake{}
			response := request(makeRouter(fake, test.actor), http.MethodPost, test.path, test.body)
			require.Equal(t, test.status, response.Code)
			require.Zero(t, fake.calls)
		}
	})
	t.Run("limits execution history and propagates ownership errors", func(t *testing.T) {
		fake := &accountScheduleHandlerFake{}
		router := makeRouter(fake, 9)
		for _, limit := range []string{"0", "101", "abc"} {
			require.Equal(t, http.StatusBadRequest, request(router, http.MethodGet, "/accounts/7/action-schedules/42/runs?limit="+limit, "").Code)
		}
		require.Zero(t, fake.calls)
		require.Equal(t, http.StatusOK, request(router, http.MethodGet, "/accounts/7/action-schedules/42/runs", "").Code)
		require.Equal(t, 20, fake.limit)
		require.Equal(t, http.StatusNotFound, request(router, http.MethodGet, "/accounts/8/action-schedules/42/runs", "").Code)
	})
}
