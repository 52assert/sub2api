-- Track per-user normalized User-Agent usage within a rolling window and optional per-user limits.

CREATE TABLE IF NOT EXISTS custom_user_agent_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ua_key TEXT NOT NULL,
    ua_label TEXT NOT NULL,
    last_raw_ua TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_count BIGINT NOT NULL DEFAULT 1,
    UNIQUE (user_id, ua_key)
);

CREATE INDEX IF NOT EXISTS idx_custom_user_agent_records_user_last_seen
    ON custom_user_agent_records (user_id, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS custom_user_agent_user_policies (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_user_agents INTEGER CHECK (max_user_agents IS NULL OR max_user_agents >= 0),
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
