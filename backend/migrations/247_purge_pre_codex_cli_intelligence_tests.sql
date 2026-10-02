-- One-time cleanup tied to the native Codex CLI cutover (2026-10-02 18:26:43 +08).
-- Records created before that moment are HTTP/CLI bring-up test data; later
-- records are kept, so the migration remains safe to replay on any database.
DELETE FROM intelligence_tests
WHERE created_at < TIMESTAMPTZ '2026-10-02 18:26:43+08';
