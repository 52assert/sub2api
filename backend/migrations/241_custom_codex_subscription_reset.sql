-- Fork-owned tables; do not modify upstream subscription schema or monthly usage.
CREATE TABLE IF NOT EXISTS custom_codex_reset_policy (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    enabled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    baseline JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'disabled',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS custom_codex_reset_poll (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    failures INTEGER NOT NULL DEFAULT 0,
    checked_at TIMESTAMPTZ,
    error TEXT NOT NULL DEFAULT ''
);
INSERT INTO custom_codex_reset_poll(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS custom_codex_reset_events (
    id TEXT PRIMARY KEY,
    announced_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    payload JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS custom_codex_reset_jobs (
    event_id TEXT NOT NULL REFERENCES custom_codex_reset_events(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    baseline JSONB NOT NULL DEFAULT '{}',
    group_ids BIGINT[] NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(event_id, account_id)
);
CREATE TABLE IF NOT EXISTS custom_codex_reset_targets (
    event_id TEXT NOT NULL REFERENCES custom_codex_reset_events(id),
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id),
    group_id BIGINT NOT NULL,
    daily_base NUMERIC(20,10) NOT NULL,
    weekly_base NUMERIC(20,10) NOT NULL,
    daily_start TIMESTAMPTZ,
    weekly_start TIMESTAMPTZ,
    PRIMARY KEY(event_id, subscription_id)
);
CREATE TABLE IF NOT EXISTS custom_codex_reset_actions (
    operation_id TEXT NOT NULL,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id),
    account_id BIGINT NOT NULL,
    source TEXT NOT NULL CHECK(source IN ('manual', 'official')),
    actor_id BIGINT,
    daily_before NUMERIC(20,10) NOT NULL,
    weekly_before NUMERIC(20,10) NOT NULL,
    daily_after NUMERIC(20,10) NOT NULL,
    weekly_after NUMERIC(20,10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(operation_id, subscription_id)
);
CREATE TABLE IF NOT EXISTS custom_codex_reset_card_attempts (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS custom_codex_reset_cards_account_time
    ON custom_codex_reset_card_attempts(account_id, attempted_at);
CREATE INDEX IF NOT EXISTS custom_codex_reset_jobs_due
    ON custom_codex_reset_jobs(next_at, account_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS custom_codex_reset_jobs_account_status
    ON custom_codex_reset_jobs(account_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS custom_codex_reset_actions_subscription_time
    ON custom_codex_reset_actions(subscription_id, created_at DESC);
