package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPurgePreCodexCLIIntelligenceTestsMigration(t *testing.T) {
	content, err := FS.ReadFile("247_purge_pre_codex_cli_intelligence_tests.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DELETE FROM intelligence_tests")
	require.Contains(t, sql, "created_at < TIMESTAMPTZ '2026-10-02 18:26:43+08'")
}
