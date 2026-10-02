#!/bin/sh
# Run inside the published image with Docker's default security settings.
set -eu

task_dir=$(mktemp -d /tmp/sub2api-codex-smoke.XXXXXX)
trap 'rm -rf "$task_dir" /app/data/codex-smoke-secret /app/data/codex-smoke-write' EXIT
mkdir "$task_dir/workspace"
printf 'fixture-service-secret\n' > /app/data/codex-smoke-secret
chmod 644 /app/data/codex-smoke-secret
chown -R sub2api:sub2api "$task_dir"

su-exec sub2api /usr/local/bin/codex-test-sandbox --check
su-exec sub2api /usr/local/bin/codex-test-sandbox /usr/local/bin/codex "$task_dir" -- --version
su-exec sub2api /usr/local/bin/codex-test-sandbox /bin/sh "$task_dir" -- -c '
  cd "$1/workspace"
  printf "<html><body>sandbox fixture</body></html>" > result.html
  test -s result.html
  if cat /app/data/codex-smoke-secret >/dev/null 2>&1; then
    echo "sandbox allowed reading service data" >&2
    exit 1
  fi
  if printf "unexpected" > /app/data/codex-smoke-write 2>/dev/null; then
    echo "sandbox allowed writing service data" >&2
    exit 1
  fi
' _ "$task_dir"
test ! -e /app/data/codex-smoke-write
printf 'Codex container sandbox smoke test passed\n'
