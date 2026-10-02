//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRunIntelligenceTestSendsPromptEffortAndGenerationBudget(t *testing.T) {
	prompt := "  Generate HTML with an SVG pelican riding a bicycle.\n不要测试。  "
	cases := []struct {
		name, platform, model, effort, protocol string
		oauth, chat                             bool
	}{
		{name: "OpenAI Responses", platform: PlatformOpenAI, model: "gpt-5.4", effort: "high", protocol: "responses"},
		{name: "OpenAI OAuth", platform: PlatformOpenAI, model: "gpt-5.4", effort: "high", protocol: "responses", oauth: true},
		{name: "OpenAI Chat", platform: PlatformOpenAI, model: "gpt-5.4", effort: "medium", protocol: "chat", chat: true},
		{name: "Claude adaptive", platform: PlatformAnthropic, model: "claude-sonnet-4-6", effort: "high", protocol: "anthropic"},
		{name: "Claude budget", platform: PlatformAnthropic, model: "claude-sonnet-4-5", effort: "low", protocol: "anthropic"},
		{name: "Gemini thinking level", platform: PlatformGemini, model: "gemini-3.1-pro-preview", effort: "high", protocol: "gemini"},
		{name: "Gemini thinking budget", platform: PlatformGemini, model: "gemini-2.5-flash", effort: "medium", protocol: "gemini"},
		{name: "CN adaptive one generation", platform: PlatformDeepseek, model: "deepseek-v4-flash", effort: "high", protocol: "chat"},
		{name: "OpenCode Responses", platform: PlatformOpenCodeGo, model: "grok-4.6", effort: "high", protocol: "responses"},
		{name: "OpenCode Anthropic", platform: PlatformOpenCodeGo, model: "minimax-m3", effort: "default", protocol: "anthropic"},
		{name: "Grok Responses", platform: PlatformGrok, model: "grok-4.6", effort: "high", protocol: "responses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := adaptiveCNAccountTestAccount(9201, tc.platform)
			account.Credentials["base_url"] = "http://upstream.example"
			account.Credentials["access_token"] = "intelligence-oauth-test"
			// Exercise exact/wildcard mapping; the selected public alias is never sent upstream.
			account.Credentials["model_mapping"] = map[string]any{"pelican-*": tc.model}
			if tc.oauth {
				account.Type = AccountTypeOAuth
			}
			if tc.chat {
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			}
			var response *http.Response
			want := ""
			switch tc.protocol {
			case "responses":
				response = adaptiveCNResponsesTestResponse()
				want = "responses ok"
			case "chat":
				response = adaptiveCNChatTestResponse()
				want = "chat ok"
			case "anthropic":
				response = newJSONResponse(http.StatusOK, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"anthropic ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
				want = "anthropic ok"
			case "gemini":
				response = newJSONResponse(http.StatusOK, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"gemini ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
				want = "gemini ok"
			}
			svc, upstream := adaptiveCNAccountTestService(account, response)
			result, err := svc.RunIntelligenceTest(context.Background(), account.ID, "pelican-test", prompt, tc.effort)
			require.NoError(t, err)
			require.Equal(t, want, result)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, HTTPUpstreamProfileLongStream, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
			payload := gjson.ParseBytes(upstream.lastBody)
			if tc.protocol != "gemini" {
				require.Equal(t, tc.model, payload.Get("model").String())
			}
			switch tc.protocol {
			case "responses":
				require.Equal(t, prompt, payload.Get("input.0.content.0.text").String())
				require.Equal(t, tc.effort, payload.Get("reasoning.effort").String())
				if tc.oauth {
					require.False(t, payload.Get("max_output_tokens").Exists())
				} else {
					require.Equal(t, int64(intelligenceMaxTokens), payload.Get("max_output_tokens").Int())
				}
				require.False(t, payload.Get("messages").Exists())
			case "chat":
				require.Equal(t, prompt, payload.Get("messages.0.content").String())
				require.Equal(t, tc.effort, payload.Get("reasoning_effort").String())
				limit := payload.Get("max_tokens")
				if tc.platform == PlatformOpenAI {
					limit = payload.Get("max_completion_tokens")
				}
				require.Equal(t, int64(intelligenceMaxTokens), limit.Int())
			case "anthropic":
				require.Equal(t, prompt, payload.Get("messages.0.content.0.text").String())
				require.Equal(t, int64(intelligenceMaxTokens), payload.Get("max_tokens").Int())
				if tc.model == "claude-sonnet-4-6" {
					require.Equal(t, "adaptive", payload.Get("thinking.type").String())
					require.Equal(t, "high", payload.Get("output_config.effort").String())
				}
				if tc.model == "claude-sonnet-4-5" {
					require.Equal(t, int64(1024), payload.Get("thinking.budget_tokens").Int())
				}
			case "gemini":
				require.Equal(t, prompt, payload.Get("contents.0.parts.0.text").String())
				require.Equal(t, int64(intelligenceMaxTokens), payload.Get("generationConfig.maxOutputTokens").Int())
				if strings.HasPrefix(tc.model, "gemini-3") {
					require.Equal(t, "high", payload.Get("generationConfig.thinkingConfig.thinkingLevel").String())
				} else {
					require.Equal(t, int64(8192), payload.Get("generationConfig.thinkingConfig.thinkingBudget").Int())
				}
			}
		})
	}
}

func TestIntelligenceStreamRequiresSuccessfulCompletion(t *testing.T) {
	cases := []struct {
		name, protocol, stream string
		success                bool
	}{
		{"Responses truncated", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", false},
		{"Responses incomplete", "responses", "data: {\"type\":\"response.incomplete\"}\n\n", false},
		{"Responses failed done", "responses", "data: {\"type\":\"response.done\",\"response\":{\"status\":\"failed\"}}\n\n", false},
		{"Responses native terminal text", "responses", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"<svg/>\"}]}]}}", true},
		{"Claude truncated", "anthropic", "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n\n", false},
		{"Claude length limit", "anthropic", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", false},
		{"Claude missing stop reason", "anthropic", "data:{\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n\ndata:{\"type\":\"message_stop\"}", false},
		{"Claude terminal no newline", "anthropic", "data:{\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<svg/>\"}}\n\ndata:{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata:{\"type\":\"message_stop\"}", true},
		{"Gemini truncated", "gemini", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n", false},
		{"Gemini length limit", "gemini", "data: {\"candidates\":[{\"finishReason\":\"MAX_TOKENS\"}]}\n\n", false},
		{"Gemini excludes thought", "gemini", "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"private reasoning\"},{\"text\":\"<svg/>\"}]},\"finishReason\":\"STOP\"}]}}\n\n", true},
		{"Chat premature DONE", "chat", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n", false},
		{"Chat length limit", "chat", "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\n", false},
		{"Chat native terminal", "chat", "data: {\"choices\":[{\"delta\":{\"content\":\"<svg/>\"},\"finish_reason\":\"stop\"}]}\n\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := &intelligenceCaptureWriter{header: make(http.Header)}
			c, _ := gin.CreateTestContext(writer)
			c.Request = (&http.Request{}).WithContext(context.Background())
			err := (&AccountTestService{}).processIntelligenceStream(c, strings.NewReader(tc.stream), tc.protocol)
			if tc.success {
				require.NoError(t, err)
				require.True(t, writer.complete)
				require.Equal(t, "<svg/>", writer.output.String())
			} else {
				require.Error(t, err)
				require.False(t, writer.complete)
			}
		})
	}
}

func TestIntelligenceCaptureBoundsOutputAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &intelligenceCaptureWriter{header: make(http.Header), cancel: cancel}
	emit := func(text string) error {
		event, _ := json.Marshal(TestEvent{Type: "content", Text: text})
		_, err := writer.Write(append(append([]byte("data: "), event...), []byte("\n\n")...))
		return err
	}
	require.NoError(t, emit(strings.Repeat("x", intelligenceMaxOutputBytes)))
	require.Error(t, emit("one more byte"))
	require.Equal(t, intelligenceMaxOutputBytes, writer.output.Len())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestIntelligenceValidationUsesMappedTextModelAndChecksEffort(t *testing.T) {
	svc := &AccountTestService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-text": "gpt-5.4", "public-media": "gpt-image-2"}}}
	require.NoError(t, svc.ValidateIntelligenceTest(account, "gpt-image-text", "default"))
	require.Error(t, svc.ValidateIntelligenceTest(account, "public-media", "high"))
	account.Credentials = nil
	account.Platform = PlatformAnthropic
	require.Error(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-6", "none"))
	require.Error(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "max"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "low"))
	account.Platform = PlatformGemini
	require.Error(t, svc.ValidateIntelligenceTest(account, "gemini-3.1-pro-preview", "medium"))
	account.Platform = PlatformZhipu
	require.Error(t, svc.ValidateIntelligenceTest(account, "glm-5.2", "low"))
	require.Error(t, svc.ValidateIntelligenceTest(account, "glm-5.2", "medium"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "glm-5.2", "high"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "glm-5.3", "low"))
	account.Platform = PlatformGemini
	require.NoError(t, svc.ValidateIntelligenceTest(account, "models/gemini-3.1-pro-preview", "low"))
	ctx := context.WithValue(context.Background(), accountIntelligenceContextKey{}, &accountIntelligenceRequest{prompt: "<svg/>", effort: "low"})
	payload := map[string]any{}
	require.NoError(t, applyIntelligencePayload(ctx, "gemini", "models/gemini-3.1-pro-preview", payload, false))
	require.Equal(t, "low", payload["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)["thinkingLevel"])
}

func TestRunIntelligenceTestRejectsTypeSafeBeforeCallingUpstream(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9202, PlatformTypeSafe)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	err := svc.ValidateIntelligenceTest(account, "jev-latest", "default")
	require.ErrorContains(t, err, "TypeSafe 不支持生成 HTML / SVG")
	_, err = svc.RunIntelligenceTest(context.Background(), account.ID, "jev-latest", "Generate SVG", "default")
	require.ErrorContains(t, err, "TypeSafe 不支持生成 HTML / SVG")
	require.Empty(t, upstream.requests)
}

func TestRunIntelligenceTestPropagatesReadFailureAndCancellation(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9202, PlatformOpenAI)
	account.Credentials["base_url"] = "http://upstream.example"
	svc, upstream := adaptiveCNAccountTestService(account, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: passthroughErrReadCloser{err: io.ErrUnexpectedEOF}})
	_, err := svc.RunIntelligenceTest(context.Background(), account.ID, "gpt-5.4", "generate html", "default")
	require.Error(t, err)
	upstream.responses = []*http.Response{adaptiveCNResponsesTestResponse()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.RunIntelligenceTest(ctx, account.ID, "gpt-5.4", "generate html", "default")
	require.True(t, errors.Is(err, context.Canceled), "expected context cancellation, got %v", err)
}

func TestRunIntelligenceTestAntigravityUsesGenerationRatherThanProbe(t *testing.T) {
	cases := []struct {
		name, model, effort string
		budget              int64
	}{
		{"Claude budget", "claude-sonnet-4-5", "medium", 8192},
		{"Gemini level", "gemini-3.8-flash-low", "low", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 9203, Name: "antigravity-test", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "token", "project_id": "project", "model_mapping": map[string]any{"public-model": tc.model}}}
			response := newJSONResponse(http.StatusOK, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"<svg/>\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
			svc, upstream := adaptiveCNAccountTestService(account, response)
			svc.antigravityGatewayService = &AntigravityGatewayService{accountRepo: svc.accountRepo, httpUpstream: upstream, tokenProvider: &AntigravityTokenProvider{}, settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}})}
			result, err := svc.RunIntelligenceTest(context.Background(), account.ID, "public-model", "Generate a pelican SVG animation", tc.effort)
			require.NoError(t, err)
			require.Equal(t, "<svg/>", result)
			require.Len(t, upstream.requests, 1)
			payload := gjson.ParseBytes(upstream.lastBody)
			require.Equal(t, "project", payload.Get("project").String())
			require.Contains(t, payload.Get("request.contents").Raw, "Generate a pelican SVG animation")
			require.GreaterOrEqual(t, payload.Get("request.generationConfig.maxOutputTokens").Int(), int64(32768))
			if tc.budget > 0 {
				require.Equal(t, tc.budget, payload.Get("request.generationConfig.thinkingConfig.thinkingBudget").Int())
			} else {
				require.Equal(t, tc.effort, payload.Get("request.generationConfig.thinkingConfig.thinkingLevel").String())
			}
		})
	}
}

func TestRunIntelligenceTestBedrockPreservesPromptEffortAndCompleteResponse(t *testing.T) {
	account := &Account{ID: 9204, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Concurrency: 1, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "bedrock-test", "region": "us-east-1", "model_mapping": map[string]any{"public-bedrock": "claude-sonnet-4-6", "claude-sonnet-4-6": "claude-sonnet-4-5"}}}
	response := newJSONResponse(http.StatusOK, `{"content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"<html>"},{"type":"text","text":"</html>"}],"stop_reason":"end_turn"}`)
	svc, upstream := adaptiveCNAccountTestService(account, response)
	result, err := svc.RunIntelligenceTest(context.Background(), account.ID, "public-bedrock", "Generate HTML", "high")
	require.NoError(t, err)
	require.Equal(t, "<html></html>", result)
	payload := gjson.ParseBytes(upstream.lastBody)
	require.Equal(t, "Generate HTML", payload.Get("messages.0.content.0.text").String())
	require.False(t, payload.Get("output_config").Exists())
	require.Equal(t, "enabled", payload.Get("thinking.type").String())
	require.Equal(t, int64(16384), payload.Get("thinking.budget_tokens").Int())
	require.Equal(t, int64(32768), payload.Get("max_tokens").Int())
	require.Equal(t, "Bearer bedrock-test", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, upstream.lastReq.URL.Path, "claude-sonnet-4-6")
	require.NotContains(t, upstream.lastReq.URL.Path, "claude-sonnet-4-5")
	require.Contains(t, upstream.lastReq.URL.Path, "/invoke")
	require.False(t, payload.Get("stream").Exists())
}

func TestIntelligenceUsesStoredModelOutputCaps(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9205, PlatformOpenAI)
	account.Credentials["base_url"] = "http://upstream.example"
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-4o": {ID: "gpt-4o", MaxOutputTokens: 8192}}})
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	_, err := svc.RunIntelligenceTest(context.Background(), account.ID, "gpt-4o", "Generate SVG", "default")
	require.NoError(t, err)
	require.Equal(t, int64(8192), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Exists())
	account.Platform = PlatformAnthropic
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", MaxOutputTokens: 8192}}})
	require.Error(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "high"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "low"))
}

func TestRunIntelligenceTestVertexMapsModelOnce(t *testing.T) {
	account := &Account{ID: 9206, Platform: PlatformAnthropic, Type: AccountTypeServiceAccount, Concurrency: 1, Credentials: map[string]any{"project_id": "vertex-test", "location": "us-east5", "service_account_json": map[string]any{"client_email": "test@example.com", "private_key": "cached-token-key", "project_id": "vertex-test"}, "model_mapping": map[string]any{"public-vertex": "claude-sonnet-4-6", "claude-sonnet-4-6": "claude-opus-4-6"}}}
	response := newJSONResponse(http.StatusOK, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<svg/>\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	svc, upstream := adaptiveCNAccountTestService(account, response)
	svc.claudeTokenProvider = &ClaudeTokenProvider{tokenCache: &fakeGeminiTokenCache{token: "vertex-token"}}
	result, err := svc.RunIntelligenceTest(context.Background(), account.ID, "public-vertex", "Generate SVG", "medium")
	require.NoError(t, err)
	require.Equal(t, "<svg/>", result)
	require.Contains(t, upstream.lastReq.URL.Path, "claude-sonnet-4-6")
	require.NotContains(t, upstream.lastReq.URL.Path, "claude-opus-4-6")
	require.Equal(t, "Bearer vertex-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
	require.Equal(t, "Generate SVG", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
}
