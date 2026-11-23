package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubUALimitAdminSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *stubUALimitAdminSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *stubUALimitAdminSettingRepo) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func newTestUALimitHandlers(t *testing.T) (*SettingHandler, *UserHandler, sqlmock.Sqlmock, *stubUALimitAdminSettingRepo) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	settingRepo := &stubUALimitAdminSettingRepo{values: make(map[string]string)}
	uaSvc := service.ProvideCustomUserAgentLimitService(db, settingRepo)
	settingHandler := NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)
	settingHandler.SetCustomUserAgentLimitService(uaSvc)
	userHandler := NewUserHandler(nil, nil, nil, nil, nil, nil, nil)
	userHandler.SetCustomUserAgentLimitService(uaSvc)
	return settingHandler, userHandler, mock, settingRepo
}

func TestAdminUserAgentLimitHandlers_SettingsAndUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingH, userH, mock, _ := newTestUALimitHandlers(t)

	r := gin.New()
	r.GET("/api/v1/admin/settings/user-agent-limit", settingH.GetUserAgentLimitSettings)
	r.PUT("/api/v1/admin/settings/user-agent-limit", settingH.UpdateUserAgentLimitSettings)
	r.POST("/api/v1/admin/users/user-agent-summaries", userH.BatchGetUserAgentSummaries)
	r.GET("/api/v1/admin/users/:id/user-agents", userH.GetUserAgentLimitDetail)
	r.PUT("/api/v1/admin/users/:id/user-agents/policy", userH.UpdateUserAgentLimitPolicy)
	r.DELETE("/api/v1/admin/users/:id/user-agents/:record_id", userH.DeleteUserAgentRecord)
	r.DELETE("/api/v1/admin/users/:id/user-agents", userH.ClearUserAgentRecords)

	// 1. GET settings returns defaults.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/user-agent-limit", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"max_user_agents":5`)
	require.Contains(t, w.Body.String(), `"window_days":30`)

	// 2. PUT settings validates bounds & saves.
	badW := httptest.NewRecorder()
	r.ServeHTTP(badW, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/user-agent-limit",
		bytes.NewBufferString(`{"enabled":true,"max_user_agents":0,"window_days":30,"exempt_admin":true}`)))
	require.Equal(t, http.StatusBadRequest, badW.Code)

	okW := httptest.NewRecorder()
	r.ServeHTTP(okW, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/user-agent-limit",
		bytes.NewBufferString(`{"enabled":false,"max_user_agents":7,"window_days":15,"exempt_admin":false}`)))
	require.Equal(t, http.StatusOK, okW.Code)
	require.Contains(t, okW.Body.String(), `"max_user_agents":7`)
	require.Contains(t, okW.Body.String(), `"enabled":false`)

	// Restore enabled defaults for detail/policy tests.
	restoreW := httptest.NewRecorder()
	r.ServeHTTP(restoreW, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/user-agent-limit",
		bytes.NewBufferString(`{"enabled":true,"max_user_agents":5,"window_days":30,"exempt_admin":true}`)))
	require.Equal(t, http.StatusOK, restoreW.Code)

	// 3. Batch summaries empty list succeeds without DB queries.
	batchW := httptest.NewRecorder()
	r.ServeHTTP(batchW, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/user-agent-summaries",
		bytes.NewBufferString(`{"user_ids":[]}`)))
	require.Equal(t, http.StatusOK, batchW.Code)
	require.Contains(t, batchW.Body.String(), `"summaries":{}`)

	// 4. GET user detail.
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT email, COALESCE(username, ''), role FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"email", "username", "role"}).AddRow("u@example.com", "alice", "user"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, ua_key, ua_label, last_raw_ua, first_seen_at, last_seen_at, request_count`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "ua_key", "ua_label", "last_raw_ua", "first_seen_at", "last_seen_at", "request_count"}).
			AddRow(int64(9), int64(42), "codex desktop", "Codex Desktop", "Codex Desktop/0.171.0", now, now, int64(3)))

	detailW := httptest.NewRecorder()
	r.ServeHTTP(detailW, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/42/user-agents", nil))
	require.Equal(t, http.StatusOK, detailW.Code)
	var detailResp struct {
		Data service.CustomUserAgentUserDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detailW.Body.Bytes(), &detailResp))
	require.Equal(t, "u@example.com", detailResp.Data.UserEmail)
	require.Len(t, detailResp.Data.Records, 1)
	require.Equal(t, "Codex Desktop", detailResp.Data.Records[0].UALabel)

	// 5. PUT user policy with 0 (exempt) binds and persists properly, negative/over-limit fails 400.
	badPolW := httptest.NewRecorder()
	r.ServeHTTP(badPolW, httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/42/user-agents/policy",
		bytes.NewBufferString(`{"max_user_agents":-1}`)))
	require.Equal(t, http.StatusBadRequest, badPolW.Code)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO custom_user_agent_user_policies`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT email, COALESCE(username, ''), role FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"email", "username", "role"}).AddRow("u@example.com", "alice", "user"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT max_user_agents, note, updated_at FROM custom_user_agent_user_policies WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"max_user_agents", "note", "updated_at"}).AddRow(0, "vip", now))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, ua_key, ua_label, last_raw_ua, first_seen_at, last_seen_at, request_count`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "ua_key", "ua_label", "last_raw_ua", "first_seen_at", "last_seen_at", "request_count"}))

	exemptW := httptest.NewRecorder()
	r.ServeHTTP(exemptW, httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/42/user-agents/policy",
		bytes.NewBufferString(`{"max_user_agents":0,"note":"vip"}`)))
	require.Equal(t, http.StatusOK, exemptW.Code)
	require.Contains(t, exemptW.Body.String(), `"exempt":true`)

	// 6. DELETE single record and DELETE clear all.
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM custom_user_agent_records WHERE id = $1 AND user_id = $2`)).
		WithArgs(int64(9), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/42/user-agents/9", nil))
	require.Equal(t, http.StatusOK, delW.Code)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM custom_user_agent_records WHERE user_id = $1`)).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	clearW := httptest.NewRecorder()
	r.ServeHTTP(clearW, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/42/user-agents", nil))
	require.Equal(t, http.StatusOK, clearW.Code)
	require.Contains(t, clearW.Body.String(), `"deleted_count":2`)

	require.NoError(t, mock.ExpectationsWereMet())
}
