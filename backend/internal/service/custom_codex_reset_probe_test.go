package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/stretchr/testify/require"
)

type customResetProbeTransport func(*http.Request) (*http.Response, error)

func (f customResetProbeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCustomCodexResetProbeRequest(t *testing.T) {
	proxy := &Proxy{Protocol: "http", Host: "reset-probe-test.invalid", Port: 1234}
	client, err := httppool.GetClient(httppool.Options{ProxyURL: proxy.URL(), Timeout: 15 * time.Second, ResponseHeaderTimeout: 10 * time.Second})
	require.NoError(t, err)
	original := client.Transport
	t.Cleanup(func() { client.Transport = original })
	repo := &accountUsageCodexProbeRepo{updateExtraCh: make(chan map[string]any, 1)}
	s := &AccountUsageService{accountRepo: repo}
	proxyID := int64(1)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID, Proxy: proxy,
		Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
	for _, tc := range []struct {
		name, stream string
		status       int
		ok           bool
	}{
		{"completed", "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", 200, true},
		{"stream failure", "data: {\"type\":\"response.failed\"}\n\n", 200, false},
		{"truncated", "data: {\"type\":\"response.created\"}\n\n", 200, false},
		{"rate limited with quota headers", "", 429, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client.Transport = customResetProbeTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, chatgptCodexURL, req.URL.String())
				require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
				require.Equal(t, "test-account", req.Header.Get("ChatGPT-Account-ID"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
				require.Equal(t, "gpt-6-astra", payload["model"])
				require.Equal(t, false, payload["store"])
				require.Equal(t, true, payload["stream"])
				inputs, ok := payload["input"].([]any)
				require.True(t, ok)
				require.Len(t, inputs, 1)
				input, ok := inputs[0].(map[string]any)
				require.True(t, ok)
				require.Equal(t, "user", input["role"])
				contents, ok := input["content"].([]any)
				require.True(t, ok)
				require.Len(t, contents, 1)
				content, ok := contents[0].(map[string]any)
				require.True(t, ok)
				require.Equal(t, "Reply with exactly OK", content["text"])
				headers := make(http.Header)
				headers.Set("x-codex-primary-used-percent", "0.1")
				headers.Set("x-codex-primary-reset-after-seconds", "604800")
				headers.Set("x-codex-primary-window-minutes", "10080")
				return &http.Response{StatusCode: tc.status, Header: headers, Body: io.NopCloser(strings.NewReader(tc.stream))}, nil
			})
			updates, err := s.probeCustomCodexReset(context.Background(), account)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, 0.1, updates["codex_7d_used_percent"])
				select {
				case saved := <-repo.updateExtraCh:
					require.Equal(t, updates, saved)
				case <-time.After(time.Second):
					t.Fatal("probe snapshot was not persisted")
				}
			} else {
				require.Error(t, err)
				require.Empty(t, updates)
			}
		})
	}
}

func TestCustomCodexResetProbeStream(t *testing.T) {
	for _, stream := range []string{
		"data: [DONE]\n\n",
		"data: not-json\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\"}}\n\n",
		"data: {\"type\":\"response.incomplete\"}\n\n",
	} {
		require.Error(t, consumeCustomCodexResetProbe(strings.NewReader(stream)))
	}
}

func TestCustomCodexWindowProbeBoundary(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name    string
		reset   time.Time
		minutes int
		active  bool
		want    string
	}{
		{"not yet due", now.Add(time.Second), 10080, true, ""},
		{"at boundary", now, 10080, true, "weekly"},
		{"missed while offline", now.Add(-time.Hour), 10080, true, "weekly"},
		{"five hour only", now, 300, true, "bootstrap"},
		{"missing window", now, 0, true, "bootstrap"},
		{"missing reset", time.Time{}, 10080, true, "bootstrap"},
		{"inactive", now, 10080, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "stable"}, Extra: map[string]any{
				"codex_usage_updated_at": now.Add(-24 * time.Hour).Format(time.RFC3339),
				"codex_7d_reset_at":      tc.reset.Format(time.RFC3339), "codex_7d_window_minutes": tc.minutes,
			}}
			if tc.active {
				a.Status = "active"
			}
			kind, boundary := customCodexWindowProbeBoundary(a, now)
			require.Equal(t, tc.want, kind)
			if kind == "weekly" {
				require.NotNil(t, boundary)
				require.True(t, tc.reset.Equal(*boundary))
			} else {
				require.Nil(t, boundary)
			}
		})
	}
}
