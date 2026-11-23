package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeUserAgentForLimit(t *testing.T) {
	t.Run("strips client and trailing build versions while keeping OS version and arch", func(t *testing.T) {
		k1, l1 := NormalizeUserAgentForLimit("Codex Desktop/0.169.1 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1111.71704)")
		k2, l2 := NormalizeUserAgentForLimit("Codex Desktop/0.171.0-alpha.13 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1117.31558)")
		require.Equal(t, "Codex Desktop (Mac OS 26.3.1; arm64)", l1)
		require.Equal(t, "codex desktop (mac os 26.3.1; arm64)", k1)
		require.Equal(t, k1, k2)
		require.Equal(t, l1, l2)
	})

	t.Run("separates different OS versions, truncated env, and bare UA", func(t *testing.T) {
		cases := []struct {
			name      string
			raw       string
			wantLabel string
		}{
			{
				name:      "macOS 26.3.1 full",
				raw:       "Codex Desktop/0.169.1 (Mac OS 26.3.1; arm64) unknown (Codex Desktop; 26.1111.71704)",
				wantLabel: "Codex Desktop (Mac OS 26.3.1; arm64)",
			},
			{
				name:      "macOS 27.2.0 different OS version",
				raw:       "Codex Desktop/0.171.0-alpha.13 (Mac OS 27.2.0; arm64) unknown (Codex Desktop; 26.1117.31558)",
				wantLabel: "Codex Desktop (Mac OS 27.2.0; arm64)",
			},
			{
				name:      "truncated unclosed parenthesis",
				raw:       "Codex Desktop/0.169.1 (Mac OS 26.3.1",
				wantLabel: "Codex Desktop (Mac OS 26.3.1)",
			},
			{
				name:      "bare client without env",
				raw:       "Codex Desktop/0.171.0-alpha.13",
				wantLabel: "Codex Desktop",
			},
			{
				name:      "codex tui ignores terminal and originator versions",
				raw:       "codex-tui/0.164.0 (Mac OS 27.0.1; arm64) ghostty/1.3.1 (codex-tui; 0.163.0)",
				wantLabel: "codex-tui (Mac OS 27.0.1; arm64)",
			},
			{
				name:      "codex tui different terminal same machine merges",
				raw:       "codex-tui/0.167.1 (Mac OS 27.0.1; arm64) iTerm.app/3.7.4 (codex-tui; 0.166.0)",
				wantLabel: "codex-tui (Mac OS 27.0.1; arm64)",
			},
			{
				name:      "intellij air upgrades collapse",
				raw:       "IntelliJ Air/0.171.0 (Windows 10.0.26200; x86_64) unknown (codex-tui; 0.171.0)",
				wantLabel: "IntelliJ Air (Windows 10.0.26200; x86_64)",
			},
			{
				name:      "openai python sdk strips trailing space-separated version",
				raw:       "OpenAI/Python 3.51.0",
				wantLabel: "OpenAI/Python",
			},
			{
				name:      "claude cli strips version keeps external cli tag",
				raw:       "claude-cli/1.0.83 (external, cli)",
				wantLabel: "claude-cli (external, cli)",
			},
			{
				name:      "go http client strips version",
				raw:       "Go-http-client/2.0",
				wantLabel: "Go-http-client",
			},
			{
				name:      "empty user agent",
				raw:       "   \t\n ",
				wantLabel: "(empty)",
			},
		}

		seen := make(map[string]string)
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				key, label := NormalizeUserAgentForLimit(tc.raw)
				require.Equal(t, tc.wantLabel, label)
				seen[tc.name] = key
			})
		}

		require.Equal(t, seen["codex tui ignores terminal and originator versions"], seen["codex tui different terminal same machine merges"])
		require.NotEqual(t, seen["macOS 26.3.1 full"], seen["macOS 27.2.0 different OS version"])
		require.NotEqual(t, seen["truncated unclosed parenthesis"], seen["bare client without env"])
		require.NotEqual(t, seen["truncated unclosed parenthesis"], seen["macOS 26.3.1 full"])
	})

	t.Run("normalizes whitespace and case-folds key", func(t *testing.T) {
		k1, _ := NormalizeUserAgentForLimit("  codex-tui/0.164.0   (Mac OS  27.0.1;arm64)  ")
		k2, _ := NormalizeUserAgentForLimit("Codex-TUI/0.165.0 (Mac OS 27.0.1; arm64)")
		require.Equal(t, k1, k2)
	})
}
