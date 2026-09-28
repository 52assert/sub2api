-- Fork-owned, durable at-most-once probes after a natural weekly reset.
-- Claims commit before upstream IO so failures/restarts cannot repeat a probe.
ALTER TABLE custom_codex_reset_policy
    ADD COLUMN IF NOT EXISTS natural_probe_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS natural_probe_enabled_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS custom_codex_window_probes (
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    identity TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('bootstrap', 'weekly')),
    reset_at TIMESTAMPTZ NOT NULL,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'attempted',
    PRIMARY KEY (account_id, identity, kind, reset_at)
);
