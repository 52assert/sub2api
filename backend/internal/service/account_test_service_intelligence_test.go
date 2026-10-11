//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

func TestRunIntelligenceTestDoesNotFallbackToHTTPWithoutCLI(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9202, PlatformOpenAI)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNResponsesTestResponse())
	svc.cfg = &config.Config{}
	svc.cfg.IntelligenceTest.CLIPath = "/missing-codex-cli"
	_, err := svc.RunIntelligenceTest(context.Background(), account.ID, "gpt-5.4", "generate html", "default")
	require.Error(t, err)
	require.Empty(t, upstream.requests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.RunIntelligenceTest(ctx, account.ID, "gpt-5.4", "generate html", "default")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, upstream.requests)
}

func TestIntelligenceUsesStoredModelOutputCaps(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9205, PlatformOpenAI)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"gpt-4o": {ID: "gpt-4o", MaxOutputTokens: 8192}}})
	require.Equal(t, 8192, intelligenceOutputTokenBudget(account, "gpt-4o"))
	svc := &AccountTestService{}
	account.Platform = PlatformAnthropic
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", MaxOutputTokens: 8192}}})
	require.Error(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "high"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "low"))
}
