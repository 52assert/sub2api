//go:build unit

package service

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const accountCLISuccessScript = `printf 'OK' > "$final"
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"OK"}}' '{"type":"turn.completed"}'
`

func TestAccountConnectionUsesCodexCLIWithSelectedModelAndPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "synthetic-api-key", "model_mapping": map[string]any{"selected": "gpt-5.4"}}}
	svc, fixture := intelligenceCLITestService(t, account, accountCLISuccessScript)
	upstream := &queuedHTTPUpstream{}
	svc.httpUpstream = upstream
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "selected", "Only reply OK.", AccountTestModeDefault))
	require.Empty(t, upstream.requests)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), `"type":"test_start","model":"gpt-5.4"`)
	require.Contains(t, recorder.Body.String(), "Codex CLI")
	require.Contains(t, recorder.Body.String(), `"type":"content","text":"OK"`)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete","success":true`)
	args, err := os.ReadFile(filepath.Join(fixture, "args"))
	require.NoError(t, err)
	require.Contains(t, string(args), "--model\ngpt-5.4\n")
	prompt, err := os.ReadFile(filepath.Join(fixture, "prompt"))
	require.NoError(t, err)
	require.Equal(t, "Only reply OK.", string(prompt))
}

func TestAccountConnectionCodexCLIUsesParentCredentialsAndShadowMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parentID := int64(31)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": intelligenceCLITestToken("parent-chatgpt-id")}}
	shadow := &Account{ID: 32, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{"selected": "gpt-5.4"}}}
	svc, fixture := intelligenceCLITestService(t, shadow, accountCLISuccessScript)
	svc.accountRepo = intelligenceCLIAccounts{items: map[int64]*Account{parent.ID: parent, shadow.ID: shadow}}
	c, recorder := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, shadow.ID, "selected", "", ""))
	require.Contains(t, recorder.Body.String(), `"success":true`)
	auth, err := os.ReadFile(filepath.Join(fixture, "auth"))
	require.NoError(t, err)
	require.Contains(t, string(auth), `"account_id":"parent-chatgpt-id"`)
	args, err := os.ReadFile(filepath.Join(fixture, "args"))
	require.NoError(t, err)
	require.Contains(t, string(args), "--model\ngpt-5.4\n")
}

func TestAccountConnectionCodexCLIFailureNeverFallsBackToHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 33, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
	svc, _ := intelligenceCLITestService(t, account, `printf '%s\n' '{"type":"turn.failed","error":{"message":"Unauthorized sk-private-secret"}}'; exit 1`)
	upstream := &queuedHTTPUpstream{}
	svc.httpUpstream = upstream
	c, recorder := newTestContext()

	require.Error(t, svc.TestAccountConnection(c, account.ID, "gpt-5.4", "", ""))
	require.Empty(t, upstream.requests)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"success":true`)
	require.NotContains(t, recorder.Body.String(), "sk-private-secret")
}

func TestAccountConnectionMissingCodexCLIFailsWithoutHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 34, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
	svc, _ := intelligenceCLITestService(t, account, accountCLISuccessScript)
	svc.cfg.IntelligenceTest.CLIPath = filepath.Join(t.TempDir(), "missing-codex")
	upstream := &queuedHTTPUpstream{}
	svc.httpUpstream = upstream
	c, recorder := newTestContext()

	require.Error(t, svc.TestAccountConnection(c, account.ID, "gpt-5.4", "", ""))
	require.Empty(t, upstream.requests)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestAccountBackgroundConnectionUsesCodexCLIAndDefaultPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 35, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
	svc, fixture := intelligenceCLITestService(t, account, accountCLISuccessScript)

	result, err := svc.RunTestBackground(context.Background(), account.ID, "")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "OK", result.ResponseText)
	args, err := os.ReadFile(filepath.Join(fixture, "args"))
	require.NoError(t, err)
	require.Contains(t, string(args), "--model\n"+openai.DefaultTestModel+"\n")
	prompt, err := os.ReadFile(filepath.Join(fixture, "prompt"))
	require.NoError(t, err)
	require.Equal(t, "Reply with OK. Do not use tools.", string(prompt))
}

func TestAccountConnectionMappedImagesKeepImageEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, selected := range []string{"paint", ""} {
		t.Run(selected, func(t *testing.T) {
			account := &Account{ID: 36, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "synthetic-api-key", "model_mapping": map[string]any{"paint": "gpt-image-2", openai.DefaultTestModel: "gpt-image-2"}}}
			svc, _ := intelligenceCLITestService(t, account, accountCLISuccessScript)
			svc.cfg.IntelligenceTest.CLIPath = filepath.Join(t.TempDir(), "missing-codex")
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, `{"data":[{"b64_json":"image"}]}`)}}
			svc.httpUpstream = upstream
			c, recorder := newTestContext()

			require.NoError(t, svc.TestAccountConnection(c, account.ID, selected, "draw a cat", ""))
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/v1/images/generations", upstream.requests[0].URL.Path)
			require.Contains(t, recorder.Body.String(), `"type":"image"`)
			require.Contains(t, recorder.Body.String(), `"success":true`)
		})
	}
}
