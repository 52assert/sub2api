-- Add vote-triggered reset action source and subscriber reset voting tables.

ALTER TABLE custom_codex_reset_actions
    DROP CONSTRAINT IF EXISTS custom_codex_reset_actions_source_check;

ALTER TABLE custom_codex_reset_actions
    ADD CONSTRAINT custom_codex_reset_actions_source_check
    CHECK (source IN ('manual', 'official', 'scheduled', 'vote'));

CREATE TABLE IF NOT EXISTS custom_subscriber_reset_vote_rounds (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    initiator_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    threshold INT NOT NULL DEFAULT 3 CHECK (threshold > 0),
    status TEXT NOT NULL CHECK (status IN ('open', 'executing', 'succeeded', 'failed')),
    request_id TEXT NOT NULL UNIQUE,
    initiated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    triggered_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    card_status TEXT NOT NULL DEFAULT 'pending' CHECK (card_status IN ('pending', 'succeeded', 'failed', 'unknown', 'no_credit')),
    subscription_reset_status TEXT NOT NULL DEFAULT 'pending' CHECK (subscription_reset_status IN ('pending', 'succeeded', 'failed')),
    subscription_reset_count INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    warning_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_custom_subscriber_reset_vote_rounds_account_latest
    ON custom_subscriber_reset_vote_rounds (account_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_custom_subscriber_reset_vote_rounds_expires_at
    ON custom_subscriber_reset_vote_rounds (expires_at);

CREATE TABLE IF NOT EXISTS custom_subscriber_reset_vote_voters (
    id BIGSERIAL PRIMARY KEY,
    round_id BIGINT NOT NULL REFERENCES custom_subscriber_reset_vote_rounds(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (round_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_custom_subscriber_reset_vote_voters_round_id
    ON custom_subscriber_reset_vote_voters (round_id, id ASC);
