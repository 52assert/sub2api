//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCustomCodexProbePinsModelAndMedium(t *testing.T) {
	c, _ := newTestContext()
	capture := &customResetProbeCapture{}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), customResetProbeKey{}, capture))
	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))
	resp.Header.Set("x-codex-primary-used-percent", "0.5")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	repo := &openAIAccountTestRepo{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{ID: 27, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token", "model_mapping": map[string]any{"gpt-6-astra": "gpt-5.4"}}}
	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "hi", "default"))
	require.Len(t, upstream.requests, 1)
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
	require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
	require.Equal(t, "hi", gjson.GetBytes(body, "input.0.content.0.text").String())
	require.NotNil(t, capture.snapshot.Weekly)
	require.Equal(t, 0.5, *capture.snapshot.Weekly)
}
