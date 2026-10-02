//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type intelligenceCLIAccounts struct {
	AccountRepository
	items map[int64]*Account
}

func (r intelligenceCLIAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	if account := r.items[id]; account != nil {
		return account, nil
	}
	return nil, fmt.Errorf("missing account")
}

func intelligenceCLITestToken(accountID string) string {
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_plan_type": "pro"}, "sub": "test-user", "email": "test@example.invalid"})
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func intelligenceCLITestService(t *testing.T, account *Account, body string) (*AccountTestService, string) {
	t.Helper()
	fixture := t.TempDir()
	cliPath := filepath.Join(fixture, "codex")
	// The fixture captures only synthetic credentials, and never contacts upstream.
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'codex-cli 0.160.0\\n'; exit 0; fi\n" +
		"capture='" + fixture + "'\n" +
		"printf '%s\\n' \"$@\" > \"$capture/args\"\n" +
		"env > \"$capture/env\"\ncat \"$CODEX_HOME/auth.json\" > \"$capture/auth\"\n" +
		"printf '%s' \"$CODEX_HOME\" > \"$capture/home\"\n" +
		"cat > \"$capture/prompt\"\n" +
		"while [ \"$#\" -gt 0 ]; do if [ \"$1\" = \"--output-last-message\" ]; then shift; final=\"$1\"; fi; shift; done\n" + body
	require.NoError(t, os.WriteFile(cliPath, []byte(script), 0700))
	guardPath := filepath.Join(fixture, "guard")
	require.NoError(t, os.WriteFile(guardPath, []byte("#!/bin/sh\nif [ \"$1\" = \"--check\" ]; then exit 0; fi\ncli=\"$1\"\nshift 3\nexec \"$cli\" \"$@\"\n"), 0700))
	cfg := &config.Config{}
	cfg.IntelligenceTest.CLIPath = cliPath
	cfg.IntelligenceTest.SandboxPath = guardPath
	cfg.JWT.Secret = strings.Repeat("private-server-secret", 2)
	repo := intelligenceCLIAccounts{items: map[int64]*Account{account.ID: account}}
	return NewAccountTestService(repo, nil, nil, nil, nil, nil, cfg, nil), fixture
}

const intelligenceCLISuccessScript = `printf '<!doctype html><html><body><svg></svg></body></html>' > index.html
printf 'Created the animation.' > "$final"
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"Created the animation."}}' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":20}}'
`

func TestIntelligenceCodexCLIUsesSelectedAccountMappedModelAndOriginalPrompt(t *testing.T) {
	t.Setenv("DATABASE_PASSWORD", "must-not-inherit")
	t.Setenv("OPENAI_API_KEY", "must-not-inherit-api-key")
	token := intelligenceCLITestToken("selected-chatgpt-account")
	account := &Account{ID: 12, Name: "Selected", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": token, "refresh_token": "must-not-export-refresh", "chatgpt_account_id": "selected-chatgpt-account", "model_mapping": map[string]any{"animation": "gpt-5.4"}}}
	svc, fixture := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	prompt := "生成 html\nsvg 绘制鹈鹕骑自行车；不用进行测试"
	require.NoError(t, svc.ValidateIntelligenceTestRunner(context.Background(), account, "animation", "high", IntelligenceTestRunnerCodex))
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "animation", prompt, "high", IntelligenceTestRunnerCodex)
	require.NoError(t, err)
	require.Equal(t, "0.160.0", result.RunnerVersion)
	require.Equal(t, "gpt-5.4", result.EffectiveModel)
	require.Equal(t, "index.html", result.ArtifactName)
	require.Contains(t, result.Output, "<svg>")
	require.Equal(t, "Created the animation.", result.FinalMessage)
	actualPrompt, err := os.ReadFile(filepath.Join(fixture, "prompt"))
	require.NoError(t, err)
	require.Equal(t, prompt, string(actualPrompt))
	args, err := os.ReadFile(filepath.Join(fixture, "args"))
	require.NoError(t, err)
	require.Contains(t, string(args), "--model\ngpt-5.4\n")
	require.Contains(t, string(args), `model_reasoning_effort="high"`)
	require.Contains(t, string(args), "--ignore-user-config")
	require.Contains(t, string(args), "--ignore-rules")
	require.Contains(t, string(args), "shell_environment_policy.inherit=\"none\"")
	auth, err := os.ReadFile(filepath.Join(fixture, "auth"))
	require.NoError(t, err)
	require.Contains(t, string(auth), `"auth_mode":"chatgptAuthTokens"`)
	require.Contains(t, string(auth), `"account_id":"selected-chatgpt-account"`)
	require.Contains(t, string(auth), `"id_token":"`+token+`"`)
	require.Contains(t, string(auth), `"refresh_token":""`)
	require.NotContains(t, string(auth), "must-not-export-refresh")
	env, err := os.ReadFile(filepath.Join(fixture, "env"))
	require.NoError(t, err)
	require.NotContains(t, string(env), "must-not-inherit")
	require.Contains(t, string(env), "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=codex_cli_rs")
	home, err := os.ReadFile(filepath.Join(fixture, "home"))
	require.NoError(t, err)
	_, err = os.Stat(string(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIntelligenceCodexCLIUsesResponsesAPIKeyProvider(t *testing.T) {
	account := &Account{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key", "base_url": "https://api.example.invalid/v1"}}
	svc, fixture := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-5.4", "animation", "default", IntelligenceTestRunnerCodex)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", result.EffectiveModel)
	auth, err := os.ReadFile(filepath.Join(fixture, "auth"))
	require.NoError(t, err)
	require.Contains(t, string(auth), `"OPENAI_API_KEY":"synthetic-api-key"`)
	args, err := os.ReadFile(filepath.Join(fixture, "args"))
	require.NoError(t, err)
	require.Contains(t, string(args), `base_url="https://api.example.invalid/v1"`)
	require.Contains(t, string(args), `wire_api="responses"`)
	require.NotContains(t, string(args), "synthetic-api-key")
	require.NotContains(t, string(args), "model_reasoning_effort=")
}

func TestIntelligenceCodexCLIShadowUsesParentAuthAndSelectedMappingAndProxy(t *testing.T) {
	parentID := int64(19)
	proxyID := int64(7)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": intelligenceCLITestToken("parent-chatgpt-id")}}
	shadow := &Account{ID: 20, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID,
		Credentials: map[string]any{"model_mapping": map[string]any{"animation": "gpt-5.4"}},
		ProxyID:     &proxyID, Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example.invalid", Port: 8080}}
	svc, fixture := intelligenceCLITestService(t, shadow, intelligenceCLISuccessScript)
	svc.accountRepo = intelligenceCLIAccounts{items: map[int64]*Account{shadow.ID: shadow, parent.ID: parent}}
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), shadow.ID, "animation", "animation", "high", IntelligenceTestRunnerCodex)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", result.EffectiveModel)
	auth, err := os.ReadFile(filepath.Join(fixture, "auth"))
	require.NoError(t, err)
	require.Contains(t, string(auth), `"account_id":"parent-chatgpt-id"`)
	env, err := os.ReadFile(filepath.Join(fixture, "env"))
	require.NoError(t, err)
	require.Contains(t, string(env), "HTTPS_PROXY=http://proxy.example.invalid:8080")
}

func TestIntelligenceCodexCLIRejectsFailedOrIncompleteSuccessfulExit(t *testing.T) {
	for _, events := range []string{`{"type":"turn.failed"}`, `{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`, `not-json`} {
		t.Run(events, func(t *testing.T) {
			account := &Account{ID: 14, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
			svc, _ := intelligenceCLITestService(t, account, "printf '<svg></svg>' > result.svg\nprintf '%s\\n' '"+events+"'\n")
			_, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-5.4", "animation", "default", IntelligenceTestRunnerCodex)
			require.Error(t, err)
		})
	}
}

func TestIntelligenceCodexCLIRecoversFromReconnectEvents(t *testing.T) {
	account := &Account{ID: 14, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
	// Native Codex emits top-level errors while reconnecting, then reports its
	// transport fallback as an item. A completed turn remains a successful test.
	script := `printf '%s\n' '{"type":"error","message":"Reconnecting... 2/5 (stream disconnected before completion)"}' '{"type":"error","message":"Reconnecting... 3/5 (stream disconnected before completion)"}' '{"type":"item.completed","item":{"type":"error","message":"Falling back from WebSockets to HTTPS transport"}}'
` + intelligenceCLISuccessScript
	svc, _ := intelligenceCLITestService(t, account, script)
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-6.1-sol", "animation", "max", IntelligenceTestRunnerCodex)
	require.NoError(t, err)
	require.Contains(t, result.Output, "<svg>")
	require.Equal(t, "Created the animation.", result.FinalMessage)
}

func TestIntelligenceCodexCLIUsesTerminalFailureAfterReconnect(t *testing.T) {
	for _, events := range []string{
		`{"type":"error","message":"Reconnecting... 2/5 (stream disconnected before completion)"}`,
		`{"type":"error","message":"Temporary network error"}` + "\n" + `{"type":"turn.failed","error":{"message":"Account quota exceeded"}}`,
		`{"type":"turn.completed"}` + "\n" + `{"type":"turn.failed","error":{"message":"Account quota exceeded"}}`,
		`{"type":"turn.completed"}` + "\n" + `{"type":"turn.started"}` + "\n" + `{"type":"error","message":"Reconnecting... 2/5 (stream disconnected before completion)"}`,
	} {
		_, err := intelligenceCLICompletedMessage(events)
		var failure *intelligenceCLIError
		require.ErrorAs(t, err, &failure)
		if strings.Contains(events, "turn.failed") {
			require.Equal(t, "Account quota exceeded", failure.reason)
		} else {
			require.Contains(t, failure.reason, "Reconnecting")
		}
	}
}

func TestIntelligenceCodexCLINonzeroExitKeepsSafeProviderDiagnostics(t *testing.T) {
	account := &Account{ID: 14, Name: "Private provider identity", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "synthetic-api-key", "refresh_token": "private-refresh", "chatgpt_account_id": "private-chatgpt-id"}}
	script := `printf '%s\n' '{"type":"turn.failed","error":{"message":"Unsupported value for this model","param":"reasoning_effort","code":"unsupported_value"}}'
printf '%s\n' 'api_key=synthetic-api-key refresh_token=private-refresh account_id=private-chatgpt-id Private provider identity identity@example.invalid https://user:pass@proxy.private:8080/v1 /tmp/private/auth.json' "$CODEX_HOME/auth.json" >&2
exit 1
`
	svc, fixture := intelligenceCLITestService(t, account, script)
	_, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-6.1-sol", "animation", "max", IntelligenceTestRunnerCodex)
	var failure *intelligenceCLIError
	require.ErrorAs(t, err, &failure)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Contains(t, failure.publicMessage, "thinking level")
	require.Contains(t, err.Error(), "exit status 1")
	require.Contains(t, err.Error(), "reasoning_effort")
	for _, private := range []string{"synthetic-api-key", "private-refresh", "private-chatgpt-id", account.Name, "identity@example.invalid", "proxy.private", "/tmp/private", fixture} {
		require.NotContains(t, err.Error(), private)
		require.NotContains(t, failure.publicMessage, private)
	}
}

func TestIntelligenceCodexCLIFailureClassification(t *testing.T) {
	for _, tc := range []struct{ reason, hint string }{
		{"Invalid reasoning_effort: max is not supported", "thinking level"},
		{"401 Unauthorized: token expired", "authentication"},
		{"The model gpt-test does not exist", "model is unavailable"},
		{"Account quota exceeded", "usage or rate limit"},
		{"error sending request: connection refused", "proxy and network"},
		{"codex CLI returned no output", "without returning any output"},
		{"codex CLI generation did not complete", "before completion"},
		{"codex CLI output exceeded its size limit", "size limit"},
		{"codex CLI returned invalid JSON events", "invalid response events"},
		{"Unexpected provider fault api_key=unknown-secret private@example.invalid", ""},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			err := intelligenceCLIFailure(errors.New(tc.reason), "", nil)
			var failure *intelligenceCLIError
			require.ErrorAs(t, err, &failure)
			if tc.hint == "" {
				require.Empty(t, failure.publicMessage)
			} else {
				require.Contains(t, failure.publicMessage, tc.hint)
			}
			require.NotContains(t, err.Error(), "unknown-secret")
			require.NotContains(t, err.Error(), "private@example.invalid")
		})
	}
}

func TestIntelligenceCodexCLIDiagnosticsSanitizeBeforeTakingBoundedTail(t *testing.T) {
	secret := strings.Repeat("private-credential", 200)
	token := intelligenceCLITestToken("private-oauth-identity")
	redactions := intelligenceCLIRedactions(nil, nil, secret, token, "https://user:pass@proxy.private:8080")
	stderr := strings.Repeat("startup warning ", 1000) + secret + " Bearer " + token + " private-oauth-identity test-user https://user:pass@proxy.private:8080 /tmp/private/auth.json " + strings.Repeat("last warning ", 100)
	err := intelligenceCLIFailure(errors.New("CLI exited"), stderr, redactions)
	require.LessOrEqual(t, len(err.Error()), intelligenceCLIDetailLimit+len("codex CLI failed: "))
	for _, private := range []string{secret, "private-credential", token, "private-oauth-identity", "test-user", "proxy.private", "/tmp/private"} {
		require.NotContains(t, err.Error(), private)
	}
	require.Contains(t, err.Error(), "last warning")
}

func TestIntelligenceCodexCLICanKeepSVGInFinalMessage(t *testing.T) {
	account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	script := `printf '<svg xmlns="http://www.w3.org/2000/svg"/>' > "$final"
printf '%s\n' '{"type":"turn.completed"}'
`
	svc, _ := intelligenceCLITestService(t, account, script)
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-5.4", "output SVG", "default", IntelligenceTestRunnerCodex)
	require.NoError(t, err)
	require.Equal(t, result.FinalMessage, result.Output)
	require.Empty(t, result.ArtifactName)
}

func TestIntelligenceCodexCLICanKeepTextAndMarkdownWithoutArtifacts(t *testing.T) {
	for _, answer := range []string{"答案是 42。", "**答案：42**\n\n计算如下。"} {
		for _, fromFile := range []bool{true, false} {
			t.Run(fmt.Sprintf("file=%t/%s", fromFile, answer), func(t *testing.T) {
				account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
				event, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": answer}})
				require.NoError(t, err)
				script := "printf '%s\\n' '" + string(event) + "' '{\"type\":\"turn.completed\"}'\n"
				if fromFile {
					script += "printf '%s' '" + answer + "' > \"$final\"\n"
				}
				svc, _ := intelligenceCLITestService(t, account, script)
				result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-6.1-sol", "求解数学题，不调用工具，只回答结果。", "max", IntelligenceTestRunnerCodex)
				require.NoError(t, err)
				require.Equal(t, answer, result.Output)
				require.Equal(t, answer, result.FinalMessage)
				require.Empty(t, result.ArtifactName)
			})
		}
	}
}

func TestIntelligenceCodexCLIRejectsCompletedTurnWithoutOutput(t *testing.T) {
	account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	svc, _ := intelligenceCLITestService(t, account, "printf '  ' > \"$final\"\nprintf '%s\\n' '{\"type\":\"turn.completed\"}'\n")
	result, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-6.1-sol", "answer the question", "max", IntelligenceTestRunnerCodex)
	var failure *intelligenceCLIError
	require.ErrorAs(t, err, &failure)
	require.Contains(t, failure.publicMessage, "without returning any output")
	require.Empty(t, result.Output)
}

func TestIntelligenceCodexCLIVersionIgnoresStartupWarnings(t *testing.T) {
	account := &Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	svc, _ := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	script, err := os.ReadFile(svc.intelligenceCLIPath())
	require.NoError(t, err)
	updated := strings.Replace(string(script), "printf 'codex-cli", "printf 'WARNING: helper aliases unavailable\\n' >&2; printf 'codex-cli", 1)
	require.NoError(t, os.WriteFile(svc.intelligenceCLIPath(), []byte(updated), 0700))
	version, err := svc.intelligenceCLIVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.160.0", version)
}

func TestIntelligenceCodexCLICancellationCleansTask(t *testing.T) {
	account := &Account{ID: 15, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-api-key"}}
	svc, fixture := intelligenceCLITestService(t, account, "sleep 30 &\nprintf '%s' \"$!\" > \"$capture/child\"\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.RunIntelligenceTestDetailed(ctx, account.ID, "gpt-5.4", "animation", "default", IntelligenceTestRunnerCodex)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 3*time.Second)
	home, err := os.ReadFile(filepath.Join(fixture, "home"))
	require.NoError(t, err)
	_, err = os.Stat(string(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIntelligenceCodexCLIArtifactRejectsEscapingSymlinkAndOversizedFile(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.html")
	require.NoError(t, os.WriteFile(outside, []byte("private-data"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(workspace, "result.html")))
	_, _, err := intelligenceCLIArtifact(workspace)
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(workspace, "result.html")))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "result.html"), []byte(strings.Repeat("x", intelligenceMaxOutputBytes+1)), 0600))
	_, _, err = intelligenceCLIArtifact(workspace)
	require.Error(t, err)
	_, err = intelligenceCLIReadFile(workspace, "../private.html")
	require.Error(t, err)
}

func TestIntelligenceCodexCLIBufferStopsAtLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buffer := &intelligenceCLIBoundedBuffer{limit: 4, cancel: cancel}
	_, err := buffer.Write([]byte("1234"))
	require.NoError(t, err)
	_, err = buffer.Write([]byte("too much"))
	require.NoError(t, err)
	require.Equal(t, "1234", buffer.String())
	require.True(t, buffer.Exceeded())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestIntelligenceCodexCLIValidationRejectsUnsupportedAccountOrMissingBinary(t *testing.T) {
	account := &Account{ID: 16, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	svc, _ := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	for _, invalid := range []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeSetupToken},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "non-jwt"}},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity}},
	} {
		require.Error(t, svc.ValidateIntelligenceTestRunner(context.Background(), invalid, "gpt-5.4", "default", IntelligenceTestRunnerCodex))
	}
	svc.cfg.IntelligenceTest.CLIPath = filepath.Join(t.TempDir(), "missing-codex")
	require.Error(t, svc.ValidateIntelligenceTestRunner(context.Background(), account, "gpt-5.4", "default", IntelligenceTestRunnerCodex))
	require.NoError(t, svc.ValidateIntelligenceTestRunner(context.Background(), account, "gpt-5.4", "default", IntelligenceTestRunnerHTTP))
}

func TestIntelligenceCodexCLIDoesNotBypassMissingGuard(t *testing.T) {
	account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	svc, fixture := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	svc.cfg.IntelligenceTest.SandboxPath = filepath.Join(t.TempDir(), "missing-guard")
	require.Error(t, svc.ValidateIntelligenceTestRunner(context.Background(), account, "gpt-5.4", "default", IntelligenceTestRunnerCodex))
	_, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-5.4", "animation", "high", IntelligenceTestRunnerCodex)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(fixture, "args"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIntelligenceCodexCLIDoesNotRunWhenGuardPreflightFails(t *testing.T) {
	account := &Account{ID: 23, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key"}}
	svc, fixture := intelligenceCLITestService(t, account, intelligenceCLISuccessScript)
	require.NoError(t, os.WriteFile(svc.intelligenceCLISandboxPath(), []byte("#!/bin/sh\nexit 1\n"), 0700))
	require.Error(t, svc.ValidateIntelligenceTestRunner(context.Background(), account, "gpt-5.4", "default", IntelligenceTestRunnerCodex))
	_, err := svc.RunIntelligenceTestDetailed(context.Background(), account.ID, "gpt-5.4", "animation", "high", IntelligenceTestRunnerCodex)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(fixture, "args"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIntelligenceCodexCLIInstallationIDStableAndPrivate(t *testing.T) {
	svc := &AccountTestService{cfg: &config.Config{JWT: config.JWTConfig{Secret: strings.Repeat("secret", 8)}}}
	a := svc.intelligenceCLIInstallationID(12)
	require.Equal(t, a, svc.intelligenceCLIInstallationID(12))
	require.NotEqual(t, a, svc.intelligenceCLIInstallationID(13))
	_, err := uuid.Parse(a)
	require.NoError(t, err)
	require.NotContains(t, a, "secret")
}
