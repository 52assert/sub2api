-- Keep HTTP and native Codex executions distinguishable in shared history.
ALTER TABLE intelligence_tests
    ADD COLUMN IF NOT EXISTS runner VARCHAR(16) NOT NULL DEFAULT 'http'
        CHECK (runner IN ('http', 'codex_cli')),
    ADD COLUMN IF NOT EXISTS runner_version VARCHAR(80) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS effective_model VARCHAR(200) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS artifact_name VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS final_message TEXT NOT NULL DEFAULT '';
