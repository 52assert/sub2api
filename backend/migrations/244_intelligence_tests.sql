-- Durable, globally shared intelligence-test jobs and their most recent results.
-- Account identifiers are private; deleting an account preserves its test result.
CREATE TABLE IF NOT EXISTS intelligence_tests (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    platform VARCHAR(32) NOT NULL,
    model VARCHAR(200) NOT NULL,
    reasoning_effort VARCHAR(32) NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_intelligence_tests_created_at
    ON intelligence_tests(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_intelligence_tests_queued
    ON intelligence_tests(created_at, id) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_intelligence_tests_running
    ON intelligence_tests(started_at) WHERE status = 'running';
