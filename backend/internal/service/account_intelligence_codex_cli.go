package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/google/uuid"
)

const (
	intelligenceCLIStdoutLimit = 8 << 20
	intelligenceCLIStderrLimit = 64 << 10
	intelligenceCLIFileLimit   = 128
	intelligenceCLIDetailLimit = 4096
	intelligenceCLIStderrTail  = 2048
)

var (
	intelligenceCLIVersionPattern = regexp.MustCompile(`^codex-cli (\d+)\.(\d+)\.(\d+)(?:[\w.+-]*)$`)
	intelligenceCLIBearerPattern  = regexp.MustCompile(`(?i)\bBearer\s+[^\s,"'<>]+`)
	intelligenceCLITokenPattern   = regexp.MustCompile(`\b(?:eyJ[\w-]+\.[\w-]+\.[\w-]+|sk-[\w-]+)\b`)
	intelligenceCLIEmailPattern   = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	intelligenceCLIURLPattern     = regexp.MustCompile(`(?i)\b(?:https?|socks[45]?)://[^\s"'<>]+`)
	intelligenceCLIPathPattern    = regexp.MustCompile(`(?:[A-Za-z]:[\\/]|/)[A-Za-z0-9_.-][^\s"'<>]*`)
)

func (s *AccountTestService) ValidateIntelligenceTestRunner(ctx context.Context, account *Account, model, effort, runner string) error {
	if runner == "" || runner == IntelligenceTestRunnerHTTP {
		return s.ValidateIntelligenceTest(account, model, effort)
	}
	if runner != IntelligenceTestRunnerCodex {
		return infraerrors.BadRequest("INVALID_TEST_RUNNER", "Select a valid test runner.")
	}
	if err := s.ValidateIntelligenceTest(account, model, effort); err != nil {
		return err
	}
	credential, err := s.intelligenceCLICredentialAccount(ctx, account)
	if err != nil {
		return infraerrors.BadRequest("INVALID_CODEX_CLI_ACCOUNT", err.Error())
	}
	if _, err := intelligenceCLIModel(account, credential, model); err != nil {
		return infraerrors.BadRequest("INVALID_TEST_MODEL", "所选账号的模型映射无效")
	}
	if _, err := s.intelligenceCLIVersion(ctx); err != nil {
		return infraerrors.BadRequest("CODEX_CLI_UNAVAILABLE", "Codex CLI 0.160.0 or newer must be installed in the backend runtime.")
	}
	if err := s.intelligenceCLISandboxAvailable(ctx); err != nil {
		return infraerrors.BadRequest("CODEX_CLI_SANDBOX_UNAVAILABLE", "服务器不支持 Codex 测试所需的文件隔离，请检查隔离程序、Linux 内核与容器权限配置。")
	}
	return nil
}

func (s *AccountTestService) intelligenceCLICredentialAccount(ctx context.Context, account *Account) (*Account, error) {
	if account == nil || account.Platform != PlatformOpenAI {
		return nil, errors.New("codex CLI 测试仅支持 OpenAI 账号")
	}
	credential, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, errors.New("无法读取所选账号的凭据")
	}
	if credential.IsOpenAIAgentIdentity() || credential.IsOpenAIPersonalAccessToken() || credential.Type == AccountTypeSetupToken {
		return nil, errors.New("该账号认证方式暂不支持原生 Codex CLI 测试，请使用 HTTP 测试")
	}
	if credential.Type != AccountTypeOAuth && credential.Type != AccountTypeAPIKey {
		return nil, errors.New("codex CLI 测试仅支持 OAuth 或 API Key 账号")
	}
	if credential.Type == AccountTypeAPIKey && !openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return nil, errors.New("codex CLI 测试需要账号使用 Responses 协议")
	}
	if credential.Type == AccountTypeOAuth {
		if _, err := intelligenceCLIAuthJSON(credential, credential.GetOpenAIAccessToken()); err != nil {
			return nil, errors.New("codex CLI 测试需要包含有效身份信息的 OAuth 凭据")
		}
	} else if strings.TrimSpace(credential.GetOpenAIProtocolAPIKey()) == "" {
		return nil, errors.New("所选账号缺少 API Key")
	}
	return credential, nil
}

func (s *AccountTestService) intelligenceCLIPath() string {
	if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.IntelligenceTest.CLIPath) != "" {
		return strings.TrimSpace(s.cfg.IntelligenceTest.CLIPath)
	}
	return "/usr/local/bin/codex"
}

func (s *AccountTestService) intelligenceCLISandboxPath() string {
	if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.IntelligenceTest.SandboxPath) != "" {
		return strings.TrimSpace(s.cfg.IntelligenceTest.SandboxPath)
	}
	return "/usr/local/bin/codex-test-sandbox"
}

func (s *AccountTestService) intelligenceCLISandboxAvailable(ctx context.Context) error {
	path := s.intelligenceCLISandboxPath()
	if !filepath.IsAbs(path) {
		return errors.New("codex test sandbox path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("codex test sandbox executable is unavailable")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, path, "--check")
	cmd.Env = intelligenceCLIEnvironment("", "")
	configureIntelligenceCLIProcess(cmd)
	output := &intelligenceCLIBoundedBuffer{limit: 4096, cancel: cancel}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return errors.New("codex test sandbox is not supported by this server")
	}
	terminateIntelligenceCLIChildren(cmd)
	return nil
}

func (s *AccountTestService) intelligenceCLIVersion(ctx context.Context) (string, error) {
	cliPath := s.intelligenceCLIPath()
	if !filepath.IsAbs(cliPath) {
		return "", errors.New("codex CLI path must be absolute")
	}
	info, err := os.Stat(cliPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("codex CLI executable is unavailable")
	}
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	versionDir, err := os.MkdirTemp("/tmp", "sub2api-codex-version-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(versionDir) }()
	cmd := exec.CommandContext(versionCtx, cliPath, "--version")
	cmd.Env = intelligenceCLIEnvironment(versionDir, "")
	configureIntelligenceCLIProcess(cmd)
	output := &intelligenceCLIBoundedBuffer{limit: 4096, cancel: cancel}
	stderr := &intelligenceCLIBoundedBuffer{limit: intelligenceCLIStderrLimit, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if versionCtx.Err() != nil {
			return "", versionCtx.Err()
		}
		return "", errors.New("cannot read Codex CLI version")
	}
	terminateIntelligenceCLIChildren(cmd)
	version := strings.TrimSpace(output.String())
	matches := intelligenceCLIVersionPattern.FindStringSubmatch(version)
	if len(matches) != 4 || len(strings.TrimPrefix(version, "codex-cli ")) > 80 {
		return "", errors.New("unrecognized Codex CLI executable")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	if major == 0 && minor < 160 {
		return "", errors.New("codex CLI is too old")
	}
	return strings.TrimPrefix(version, "codex-cli "), nil
}

func (s *AccountTestService) RunIntelligenceTestDetailed(ctx context.Context, accountID int64, model, prompt, effort, runner string) (IntelligenceTestGenerationResult, error) {
	if runner == "" || runner == IntelligenceTestRunnerHTTP {
		output, err := s.RunIntelligenceTest(ctx, accountID, model, prompt, effort)
		return IntelligenceTestGenerationResult{Output: output}, err
	}
	if runner != IntelligenceTestRunnerCodex {
		return IntelligenceTestGenerationResult{}, errors.New("invalid intelligence test runner")
	}
	return s.runCodexCLIIntelligenceTest(ctx, accountID, model, prompt, effort)
}

func (s *AccountTestService) runCodexCLIIntelligenceTest(ctx context.Context, accountID int64, model, prompt, effort string) (result IntelligenceTestGenerationResult, err error) {
	if strings.TrimSpace(prompt) == "" || len(prompt) > 32<<10 {
		return result, errors.New("invalid Codex CLI prompt")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return result, err
	}
	if err := s.ValidateIntelligenceTest(account, model, effort); err != nil {
		return result, err
	}
	credential, err := s.intelligenceCLICredentialAccount(ctx, account)
	if err != nil {
		return result, err
	}
	result.RunnerVersion, err = s.intelligenceCLIVersion(ctx)
	if err != nil {
		return result, err
	}
	if err := s.intelligenceCLISandboxAvailable(ctx); err != nil {
		return result, err
	}
	result.EffectiveModel, err = intelligenceCLIModel(account, credential, model)
	if err != nil {
		return result, err
	}
	token := credential.GetOpenAIProtocolAPIKey()
	if credential.Type == AccountTypeOAuth {
		if s.openaiGatewayService != nil {
			token, _, err = s.openaiGatewayService.GetAccessToken(ctx, account)
			if err != nil {
				return result, err
			}
		} else {
			token = credential.GetOpenAIAccessToken()
		}
	}
	authJSON, err := intelligenceCLIAuthJSON(credential, token)
	if err != nil {
		return result, err
	}
	taskDir, err := os.MkdirTemp("/tmp", "sub2api-codex-test-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(taskDir) }()
	cliHome := filepath.Join(taskDir, "codex")
	workspace := filepath.Join(taskDir, "workspace")
	for _, directory := range []string{cliHome, workspace, filepath.Join(taskDir, "runtime")} {
		if err := os.Mkdir(directory, 0700); err != nil {
			return result, err
		}
	}
	if err := os.WriteFile(filepath.Join(cliHome, "auth.json"), authJSON, 0600); err != nil {
		return result, err
	}
	installationID := s.intelligenceCLIInstallationID(credential.ID)
	if err := os.WriteFile(filepath.Join(cliHome, "installation_id"), []byte(installationID), 0600); err != nil {
		return result, err
	}
	finalPath := filepath.Join(taskDir, "final-message.txt")
	// The outer guard confines the entire CLI and every tool process to taskDir.
	// This native CLI flag avoids nesting a user-namespace sandbox in Docker;
	// execution always enters the mandatory guard below and never bypasses it.
	args := []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "--color", "never", "--cd", workspace, "--model", result.EffectiveModel, "--output-last-message", finalPath, "-c", `web_search="disabled"`, "-c", `approval_policy="never"`, "--sandbox", "danger-full-access", "-c", `shell_environment_policy.inherit="none"`, "-c", "shell_environment_policy.set=" + `{PATH="/usr/local/bin:/usr/bin:/bin:/opt/codex/codex-path",LANG="C.UTF-8",HOME=` + strconv.Quote(workspace) + `,TMPDIR=` + strconv.Quote(workspace) + `}`}
	if effort != "" && effort != "default" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(effort))
	}
	if credential.Type == AccountTypeAPIKey {
		baseURL := credential.GetOpenAIBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		baseURL, err = s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return result, errors.New("invalid Codex CLI upstream URL")
		}
		baseURL = strings.TrimSuffix(buildOpenAIResponsesURLForPlatform(PlatformOpenAI, baseURL), "/responses")
		args = append(args, "-c", `model_provider="intelligence_test"`, "-c", "model_providers.intelligence_test="+`{name="Intelligence test",wire_api="responses",requires_openai_auth=true,supports_websockets=false,base_url=`+strconv.Quote(baseURL)+`}`)
	}
	args = append(args, "-")
	proxyURL := ""
	if account.Proxy != nil && account.ProxyID != nil {
		proxyURL = account.Proxy.URL()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guardArgs := append([]string{s.intelligenceCLIPath(), taskDir, "--"}, args...)
	cmd := exec.CommandContext(runCtx, s.intelligenceCLISandboxPath(), guardArgs...)
	cmd.Dir = workspace
	cmd.Env = intelligenceCLIEnvironment(cliHome, proxyURL)
	cmd.Stdin = strings.NewReader(prompt)
	configureIntelligenceCLIProcess(cmd)
	stdout := &intelligenceCLIBoundedBuffer{limit: intelligenceCLIStdoutLimit, cancel: cancel}
	stderr := &intelligenceCLIBoundedBuffer{limit: intelligenceCLIStderrLimit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	// Tool processes cannot outlive a completed or interrupted test.
	terminateIntelligenceCLIChildren(cmd)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return result, intelligenceCLIFailure(errors.New("codex CLI output exceeded its size limit"), "", nil)
	}
	// CLI failures often have a nonzero exit code. Parse their JSON events before
	// returning so the provider's actual failure is not reduced to "exit status 1".
	message, eventErr := intelligenceCLICompletedMessage(stdout.String())
	redactions := intelligenceCLIRedactions(account, credential, token, proxyURL, taskDir, s.intelligenceCLIPath(), s.intelligenceCLISandboxPath())
	if runErr != nil {
		failure := &intelligenceCLIError{cause: fmt.Errorf("codex CLI execution failed: %w", runErr)}
		var eventFailure *intelligenceCLIError
		if errors.As(eventErr, &eventFailure) {
			failure.reason = eventFailure.reason
		} else if eventErr != nil && stdout.String() != "" {
			failure.cause = fmt.Errorf("codex CLI execution failed (%s): %w", eventErr, runErr)
		}
		return result, intelligenceCLIFailure(failure, stderr.String(), redactions)
	}
	if eventErr != nil {
		return result, intelligenceCLIFailure(eventErr, stderr.String(), redactions)
	}
	if final, readErr := intelligenceCLIReadFile(taskDir, "final-message.txt"); readErr == nil {
		message = string(final)
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return result, intelligenceCLIFailure(readErr, "", redactions)
	}
	if len(message) > intelligenceMaxOutputBytes {
		return result, intelligenceCLIFailure(errors.New("codex CLI final message exceeded its size limit"), "", redactions)
	}
	result.FinalMessage = strings.TrimSpace(message)
	result.Output, result.ArtifactName, err = intelligenceCLIArtifact(workspace)
	if err != nil {
		return result, intelligenceCLIFailure(err, "", redactions, "final message: "+result.FinalMessage)
	}
	if result.Output == "" {
		if result.FinalMessage == "" {
			return result, intelligenceCLIFailure(errors.New("codex CLI returned no output"), "", redactions)
		}
		result.Output = result.FinalMessage
	}
	return result, nil
}

func intelligenceCLIModel(account, credential *Account, selected string) (string, error) {
	model := account.GetMappedModel(strings.TrimSpace(selected))
	if credential.Type == AccountTypeOAuth {
		model = normalizeOpenAIModelForUpstream(credential, model)
	}
	if model == "" || len(model) > 200 || strings.HasPrefix(model, "-") || strings.ContainsAny(model, "\r\n\x00") {
		return "", errors.New("invalid Codex CLI model mapping")
	}
	return model, nil
}

func (s *AccountTestService) intelligenceCLIInstallationID(accountID int64) string {
	secret := ""
	if s != nil && s.cfg != nil {
		secret = s.cfg.JWT.Secret
	}
	if secret == "" {
		return uuid.NewString()
	}
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "sub2api/intelligence-test/codex/%d", accountID)
	id := uuid.UUID{}
	copy(id[:], hash.Sum(nil))
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

func intelligenceCLIAuthJSON(account *Account, token string) ([]byte, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("codex CLI account credential is missing")
	}
	if account.Type == AccountTypeAPIKey {
		return json.Marshal(map[string]any{"auth_mode": "apikey", "OPENAI_API_KEY": token})
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("codex CLI OAuth credential is not a JWT")
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("codex CLI OAuth credential claims are invalid")
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(claimBytes, &claims); err != nil || claims.Auth.AccountID == "" {
		return nil, errors.New("codex CLI OAuth credential identity is missing")
	}
	accountID := account.GetChatGPTAccountID()
	if accountID == "" {
		accountID = claims.Auth.AccountID
	}
	// This is the CLI's external-access-token auth format. The CLI uses the
	// access JWT itself for identity claims and does not refresh external tokens.
	return json.Marshal(map[string]any{
		"auth_mode": "chatgptAuthTokens", "OPENAI_API_KEY": nil,
		"tokens":       map[string]any{"id_token": token, "access_token": token, "refresh_token": "", "account_id": accountID},
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
	})
}

func intelligenceCLIEnvironment(cliHome, proxyURL string) []string {
	env := make([]string, 0, 16)
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, "TERM=dumb", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=codex_cli_rs")
	if cliHome != "" {
		env = append(env, "CODEX_HOME="+cliHome, "HOME="+filepath.Dir(cliHome), "TMPDIR="+filepath.Dir(cliHome), "XDG_RUNTIME_DIR="+filepath.Join(filepath.Dir(cliHome), "runtime"))
	}
	if proxyURL != "" {
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
			env = append(env, key+"="+proxyURL)
		}
	}
	return env
}

type intelligenceCLIBoundedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *intelligenceCLIBoundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(data) > b.limit-b.buffer.Len() {
		b.exceeded = true
		b.cancel()
		return len(data), nil
	}
	if !b.exceeded {
		_, _ = b.buffer.Write(data)
	}
	return len(data), nil
}

func (b *intelligenceCLIBoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *intelligenceCLIBoundedBuffer) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

// A CLI error keeps private diagnostics separate from the fixed message shared
// with every signed-in user. reason is raw event data until the runner sanitizes
// it with the selected account's credentials; Error never includes raw reason.
type intelligenceCLIError struct {
	cause         error
	reason        string
	diagnostic    string
	publicMessage string
}

func (e *intelligenceCLIError) Error() string {
	if e.diagnostic != "" {
		return "codex CLI failed: " + e.diagnostic
	}
	return e.cause.Error()
}

func (e *intelligenceCLIError) Unwrap() error { return e.cause }

func intelligenceCLIFailure(cause error, stderr string, redactions []string, details ...string) error {
	reason := ""
	var eventFailure *intelligenceCLIError
	if errors.As(cause, &eventFailure) {
		reason = eventFailure.reason
	}
	reason = intelligenceCLISanitizeDiagnostic(reason, redactions)
	stderr = intelligenceCLISanitizeDiagnostic(stderr, redactions)
	// Sanitize before taking the tail so a truncated credential cannot escape an
	// exact-value redaction. stdout/stderr are already bounded during collection.
	if len(stderr) > intelligenceCLIStderrTail {
		stderr = stderr[len(stderr)-intelligenceCLIStderrTail:]
		for !utf8.ValidString(stderr) {
			stderr = stderr[1:]
		}
	}
	diagnostic := intelligenceCLISanitizeDiagnostic(cause.Error(), redactions)
	classification := reason
	if reason != "" {
		diagnostic += "; " + reason
	} else {
		classification = diagnostic + " " + stderr
	}
	if stderr != "" {
		diagnostic += "; stderr: " + stderr
	}
	for _, detail := range details {
		diagnostic += "; " + intelligenceCLISanitizeDiagnostic(detail, redactions)
	}
	if len(diagnostic) > intelligenceCLIDetailLimit {
		diagnostic = diagnostic[:intelligenceCLIDetailLimit]
		for !utf8.ValidString(diagnostic) {
			diagnostic = diagnostic[:len(diagnostic)-1]
		}
	}
	return &intelligenceCLIError{cause: cause, diagnostic: diagnostic, publicMessage: intelligenceCLIPublicFailure(classification)}
}

func intelligenceCLIPublicFailure(reason string) string {
	lower := strings.ToLower(reason)
	contains := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(lower, part) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("reasoning_effort", "reasoning effort", "thinking level") && contains("unsupported", "not supported", "invalid", "supported values"):
		return "The selected thinking level is not supported by this model. Choose another level and retry."
	case contains("unauthorized", "authentication", "invalid api key", "incorrect api key", "invalid_api_key", "invalid credentials", "token expired", "expired token", "401 unauthorized", "403 forbidden"):
		return "Account authentication failed. Refresh the account credentials and retry."
	case contains("rate limit", "rate_limit", "usage limit", "quota", "insufficient credits", "credit balance", "too many requests"):
		return "The account reached its usage or rate limit. Wait or choose another account and retry."
	case contains("model") && contains("not found", "not_found", "does not exist", "unavailable", "not available", "unsupported", "not supported", "no access", "not have access"):
		return "The selected model is unavailable for this account. Choose another model and retry."
	case contains("connection", "connect error", "network", "dns", "timed out", "timeout", "stream disconnected", "error sending request", "tls", "certificate"):
		return "Codex CLI could not connect to the upstream service. Check the account proxy and network, then retry."
	case contains("returned no output", "generated an empty artifact"):
		return "Codex CLI completed without returning any output. Please retry."
	case contains("generation did not complete", "response incomplete", "stream ended"):
		return "Codex CLI generation ended before completion. Please retry."
	case contains("exceeded its size limit", "generated too many files"):
		return "Codex CLI output exceeded the test size limit. Request a smaller artifact and retry."
	case contains("invalid json events"):
		return "Codex CLI returned invalid response events. Please retry."
	default:
		return ""
	}
}

func intelligenceCLIRedactions(account, credential *Account, values ...string) []string {
	redactions := append([]string(nil), values...)
	var collect func(any, bool, int)
	collect = func(value any, sensitive bool, depth int) {
		if depth > 8 {
			return
		}
		switch value := value.(type) {
		case string:
			if sensitive && value != "" {
				redactions = append(redactions, value, strings.Trim(strconv.Quote(value), `"`))
			}
		case map[string]any:
			for key, child := range value {
				key = strings.ToLower(key)
				private := sensitive || strings.Contains(key, "token") || strings.Contains(key, "key") || strings.Contains(key, "secret") || strings.Contains(key, "password") || strings.Contains(key, "email") || strings.Contains(key, "account") || strings.Contains(key, "user") || key == "sub" || key == "client_id"
				collect(child, private, depth+1)
			}
		case []any:
			for _, child := range value {
				collect(child, sensitive, depth+1)
			}
		}
	}
	for _, item := range []*Account{account, credential} {
		if item != nil {
			if item.Name != "" {
				redactions = append(redactions, item.Name)
			}
			collect(item.Credentials, false, 0)
		}
	}
	// Native OAuth diagnostics can mention the identity embedded in the access
	// token even when it is not stored as a separate credential field.
	for _, value := range values {
		parts := strings.Split(value, ".")
		if len(parts) == 3 {
			if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
				var claims map[string]any
				if json.Unmarshal(payload, &claims) == nil {
					collect(claims, false, 0)
				}
			}
		}
	}
	sort.Slice(redactions, func(i, j int) bool { return len(redactions[i]) > len(redactions[j]) })
	return redactions
}

func intelligenceCLISanitizeDiagnostic(value string, redactions []string) string {
	for _, private := range redactions {
		if private != "" {
			value = strings.ReplaceAll(value, private, "[redacted]")
		}
	}
	value = intelligenceCLIBearerPattern.ReplaceAllString(value, "Bearer [redacted]")
	value = intelligenceCLITokenPattern.ReplaceAllString(value, "[redacted token]")
	value = logredact.RedactText(value, "api_key", "authorization", "token", "account_id", "chatgpt_account_id", "user_id", "client_id", "email", "proxy_password")
	value = intelligenceCLIEmailPattern.ReplaceAllString(value, "[redacted email]")
	value = intelligenceCLIURLPattern.ReplaceAllString(value, "[redacted URL]")
	value = intelligenceCLIPathPattern.ReplaceAllString(value, "[redacted path]")
	return strings.Join(strings.Fields(value), " ")
}

func intelligenceCLICompletedMessage(output string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), intelligenceCLIStdoutLimit)
	complete := false
	message := ""
	var failure *intelligenceCLIError
	for scanner.Scan() {
		var event struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Error   json.RawMessage `json:"error"`
			Item    struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return "", errors.New("codex CLI returned invalid JSON events")
		}
		switch event.Type {
		case "turn.started":
			complete = false
			message = ""
			failure = nil
		case "turn.completed":
			complete = true
			failure = nil
		case "turn.failed", "error":
			reason := event.Message
			if len(event.Error) > 0 {
				var detail struct {
					Message string `json:"message"`
					Param   string `json:"param"`
					Code    string `json:"code"`
				}
				if json.Unmarshal(event.Error, &detail) == nil && detail.Message != "" {
					reason = detail.Message
					if detail.Param != "" {
						reason += "; parameter: " + detail.Param
					}
					if detail.Code != "" {
						reason += "; error type: " + detail.Code
					}
				} else {
					var message string
					if json.Unmarshal(event.Error, &message) == nil {
						reason = message
					}
				}
			}
			failure = &intelligenceCLIError{cause: errors.New("codex CLI generation failed"), reason: reason}
			if event.Type == "turn.failed" {
				return "", failure
			}
		case "item.completed":
			if event.Item.Type == "agent_message" {
				message = event.Item.Text
			}
		}
	}
	if scanner.Err() != nil {
		return "", errors.New("codex CLI generation did not complete")
	}
	if !complete {
		if failure != nil {
			return "", failure
		}
		return "", errors.New("codex CLI generation did not complete")
	}
	return message, nil
}

func intelligenceCLIReadFile(directory, name string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > intelligenceMaxOutputBytes {
		return nil, errors.New("codex CLI artifact must be a bounded regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return nil, errors.New("codex CLI artifact changed during collection")
	}
	data, err := io.ReadAll(io.LimitReader(file, intelligenceMaxOutputBytes+1))
	if err != nil || len(data) > intelligenceMaxOutputBytes {
		return nil, errors.New("codex CLI artifact exceeded its size limit")
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("codex CLI artifact is not valid text")
	}
	return data, nil
}

func intelligenceCLIArtifact(workspace string) (string, string, error) {
	files := make([]string, 0, 4)
	entries := 0
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > intelligenceCLIFileLimit {
			return errors.New("codex CLI generated too many files")
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".html" && ext != ".htm" && ext != ".svg" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("codex CLI artifact is not a regular file")
		}
		name, err := filepath.Rel(workspace, path)
		if err != nil || !filepath.IsLocal(name) || !utf8.ValidString(name) || len(name) > 512 {
			return errors.New("invalid Codex CLI artifact path")
		}
		files = append(files, name)
		return nil
	})
	if err != nil || len(files) == 0 {
		return "", "", err
	}
	sort.Slice(files, func(i, j int) bool {
		priority := func(name string) int {
			switch strings.ToLower(filepath.ToSlash(name)) {
			case "index.html":
				return 0
			case "result.html":
				return 1
			}
			if strings.EqualFold(filepath.Ext(name), ".svg") {
				return 3
			}
			return 2
		}
		if priority(files[i]) != priority(files[j]) {
			return priority(files[i]) < priority(files[j])
		}
		return files[i] < files[j]
	})
	data, err := intelligenceCLIReadFile(workspace, files[0])
	if err != nil {
		return "", "", err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", "", errors.New("codex CLI generated an empty artifact")
	}
	return string(data), filepath.ToSlash(files[0]), nil
}
