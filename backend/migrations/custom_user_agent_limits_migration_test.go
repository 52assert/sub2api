package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomUserAgentLimitsMigration(t *testing.T) {
	content, err := FS.ReadFile("253_custom_user_agent_limits.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS custom_user_agent_records")
	require.Contains(t, sql, "UNIQUE (user_id, ua_key)")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_custom_user_agent_records_user_last_seen")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS custom_user_agent_user_policies")
	require.Contains(t, sql, "max_user_agents INTEGER CHECK (max_user_agents IS NULL OR max_user_agents >= 0)")
}
