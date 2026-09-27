-- Preserve the latest balance before an announcement, including charges made
-- during the polling delay. These snapshots commit with the subscription write.
CREATE TABLE IF NOT EXISTS custom_codex_subscription_history (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    reset_revision BIGINT NOT NULL,
    is_charge BOOLEAN NOT NULL DEFAULT FALSE,
    daily_reset BOOLEAN NOT NULL DEFAULT FALSE,
    weekly_reset BOOLEAN NOT NULL DEFAULT FALSE,
    daily_usage NUMERIC(20,10) NOT NULL,
    weekly_usage NUMERIC(20,10) NOT NULL,
    daily_start TIMESTAMPTZ,
    weekly_start TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS custom_codex_subscription_history_lookup
    ON custom_codex_subscription_history(subscription_id, recorded_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS custom_codex_subscription_history_time ON custom_codex_subscription_history(recorded_at);
CREATE OR REPLACE FUNCTION custom_codex_record_subscription_history() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' AND NEW.daily_usage_usd IS NOT DISTINCT FROM OLD.daily_usage_usd
       AND NEW.weekly_usage_usd IS NOT DISTINCT FROM OLD.weekly_usage_usd
       AND NEW.daily_window_start IS NOT DISTINCT FROM OLD.daily_window_start
       AND NEW.weekly_window_start IS NOT DISTINCT FROM OLD.weekly_window_start THEN
        RETURN NEW;
    END IF;
    INSERT INTO custom_codex_subscription_history(subscription_id,reset_revision,is_charge,daily_reset,weekly_reset,daily_usage,weekly_usage,daily_start,weekly_start)
    VALUES(NEW.id,NEW.custom_codex_reset_revision,TG_OP='UPDATE' AND (NEW.daily_usage_usd>OLD.daily_usage_usd OR NEW.weekly_usage_usd>OLD.weekly_usage_usd),
        TG_OP='UPDATE' AND (NEW.daily_usage_usd<OLD.daily_usage_usd OR NEW.daily_window_start IS DISTINCT FROM OLD.daily_window_start),
        TG_OP='UPDATE' AND (NEW.weekly_usage_usd<OLD.weekly_usage_usd OR NEW.weekly_window_start IS DISTINCT FROM OLD.weekly_window_start),NEW.daily_usage_usd,NEW.weekly_usage_usd,NEW.daily_window_start,NEW.weekly_window_start);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS custom_codex_record_subscription_history ON user_subscriptions;
CREATE TRIGGER custom_codex_record_subscription_history
    AFTER INSERT OR UPDATE OF daily_usage_usd,weekly_usage_usd,daily_window_start,weekly_window_start ON user_subscriptions
    FOR EACH ROW EXECUTE FUNCTION custom_codex_record_subscription_history();
INSERT INTO custom_codex_subscription_history(subscription_id,reset_revision,daily_usage,weekly_usage,daily_start,weekly_start)
SELECT id,custom_codex_reset_revision,daily_usage_usd,weekly_usage_usd,daily_window_start,weekly_window_start
FROM user_subscriptions;

CREATE TABLE IF NOT EXISTS custom_codex_quota_history (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    observed_at TIMESTAMPTZ NOT NULL,
    snapshot JSONB NOT NULL,
    PRIMARY KEY(account_id,observed_at)
);
CREATE INDEX IF NOT EXISTS custom_codex_quota_history_time ON custom_codex_quota_history(observed_at);
INSERT INTO custom_codex_quota_history(account_id,observed_at,snapshot)
SELECT account_id,(baseline->>'at')::timestamptz,baseline FROM custom_codex_reset_policy
WHERE baseline->>'at' IS NOT NULL AND (baseline->>'at')::timestamptz > '2000-01-01'::timestamptz
ON CONFLICT DO NOTHING;

-- Pending jobs from 241 captured detection-time balances and cannot be safely
-- reinterpreted as announcement-time compensation after this upgrade.
UPDATE custom_codex_reset_jobs SET status='missing_subscription_history',updated_at=NOW() WHERE status='pending';
UPDATE custom_codex_reset_policy p SET status='missing_subscription_history'
WHERE EXISTS(SELECT 1 FROM custom_codex_reset_jobs j WHERE j.account_id=p.account_id AND j.status='missing_subscription_history');

-- Pin one upstream account cycle per event/group, shared by all subscriptions.
ALTER TABLE custom_codex_reset_jobs ADD COLUMN IF NOT EXISTS cycle_start TIMESTAMPTZ;
ALTER TABLE custom_codex_reset_jobs ADD COLUMN IF NOT EXISTS compensation_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS custom_codex_group_cycles (
    event_id TEXT NOT NULL REFERENCES custom_codex_reset_events(id),
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    cycle_start TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(event_id,group_id)
);
