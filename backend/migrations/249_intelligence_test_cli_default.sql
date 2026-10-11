-- New intelligence tests use native Codex CLI. Preserve historical runner values.
ALTER TABLE intelligence_tests
    ALTER COLUMN runner SET DEFAULT 'codex_cli';
