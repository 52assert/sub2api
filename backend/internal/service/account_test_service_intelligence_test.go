//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

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

func TestIntelligenceValidationUsesStoredModelOutputCaps(t *testing.T) {
	account := adaptiveCNAccountTestAccount(9205, PlatformAnthropic)
	svc := &AccountTestService{}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", MaxOutputTokens: 8192}}})
	require.Error(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "high"))
	require.NoError(t, svc.ValidateIntelligenceTest(account, "claude-sonnet-4-5", "low"))
}
