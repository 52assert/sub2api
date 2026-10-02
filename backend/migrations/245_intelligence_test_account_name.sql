-- Keep the display name used when a test was submitted, even after its account
-- is renamed or deleted. Credentials and account identifiers remain private.
ALTER TABLE intelligence_tests
    ADD COLUMN IF NOT EXISTS account_name TEXT NOT NULL DEFAULT '';

-- Existing records can recover their source name while the account still exists.
-- Do not overwrite snapshots if this migration is applied again.
UPDATE intelligence_tests AS tests
SET account_name = accounts.name
FROM accounts
WHERE tests.account_id = accounts.id AND tests.account_name = '';
