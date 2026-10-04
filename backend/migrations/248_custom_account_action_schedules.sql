-- Fork-owned account actions. Attempts are committed before any upstream call;
-- an abandoned attempt is never retried, even after a process restart.
ALTER TABLE custom_codex_reset_actions DROP CONSTRAINT IF EXISTS custom_codex_reset_actions_source_check;
ALTER TABLE custom_codex_reset_actions ADD CONSTRAINT custom_codex_reset_actions_source_check CHECK (source IN ('manual','official','scheduled'));

-- Deduplicate the entire scheduled subscription operation, including an
-- operation that had no active subscribers. New subscribers cannot make an
-- already completed operation repeat its financial effect.
CREATE TABLE IF NOT EXISTS custom_account_subscription_reset_receipts (
    operation_id TEXT PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    reset_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS custom_account_action_schedules (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    action TEXT NOT NULL CHECK (action IN ('reset_card','reset_subscriptions')),
    frequency TEXT NOT NULL CHECK (frequency IN ('once','daily','weekly','cron')),
    timezone TEXT NOT NULL,
    run_at TIMESTAMPTZ,
    time_of_day VARCHAR(5),
    cron_expression TEXT,
    weekday SMALLINT CHECK (weekday BETWEEN 0 AND 6),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at TIMESTAMPTZ,
    identity_snapshot TEXT NOT NULL,
    group_ids BIGINT[] NOT NULL DEFAULT '{}',
    actor_id BIGINT NOT NULL DEFAULT 0,
    last_run_at TIMESTAMPTZ,
    last_status TEXT NOT NULL DEFAULT '',
    last_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CHECK ((frequency='once' AND run_at IS NOT NULL AND time_of_day IS NULL AND weekday IS NULL AND cron_expression IS NULL)
        OR (frequency='daily' AND run_at IS NULL AND time_of_day IS NOT NULL AND weekday IS NULL AND cron_expression IS NULL)
        OR (frequency='weekly' AND run_at IS NULL AND time_of_day IS NOT NULL AND weekday IS NOT NULL AND cron_expression IS NULL)
        OR (frequency='cron' AND run_at IS NULL AND time_of_day IS NULL AND weekday IS NULL AND cron_expression IS NOT NULL)),
    CHECK (NOT enabled OR next_run_at IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS custom_account_action_schedules_due_idx
    ON custom_account_action_schedules(next_run_at,id) WHERE enabled AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS custom_account_action_schedules_account_idx
    ON custom_account_action_schedules(account_id,id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS custom_account_action_schedule_runs (
    id BIGSERIAL PRIMARY KEY,
    schedule_id BIGINT NOT NULL REFERENCES custom_account_action_schedules(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    action TEXT NOT NULL CHECK (action IN ('reset_card','reset_subscriptions')),
    status TEXT NOT NULL CHECK (status IN ('started','succeeded','failed','skipped','unknown')),
    message TEXT NOT NULL DEFAULT '',
    warning_code TEXT NOT NULL DEFAULT '',
    scheduled_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    reset_count INTEGER NOT NULL DEFAULT 0,
    request_id UUID NOT NULL UNIQUE,
    actor_id BIGINT NOT NULL DEFAULT 0,
    -- Upstream card IDs are internal only and never returned by the API.
    credit_id TEXT NOT NULL DEFAULT '',
    UNIQUE(schedule_id,scheduled_at)
);
CREATE INDEX IF NOT EXISTS custom_account_action_schedule_runs_history_idx
    ON custom_account_action_schedule_runs(schedule_id,id DESC);
CREATE INDEX IF NOT EXISTS custom_account_action_schedule_runs_started_idx
    ON custom_account_action_schedule_runs(started_at) WHERE status='started';
