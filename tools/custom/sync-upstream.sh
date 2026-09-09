#!/usr/bin/env bash
set -euo pipefail

: "${GH_REPO:?GH_REPO must identify this fork}"
if [[ "$GH_REPO" != "52assert/sub2api" ]]; then
  echo 'Refusing to sync an unexpected repository.' >&2
  exit 1
fi

# Push with the repository-scoped SSH deploy key, so upstream workflow changes
# can also be mirrored. Never force-push or copy upstream tags into releases.
git fetch --no-tags https://github.com/Wei-Shaw/sub2api.git \
  refs/heads/main:refs/remotes/upstream/main
git fetch --no-tags origin main custom
git merge-base --is-ancestor origin/main upstream/main || {
  echo 'Fork main has diverged from upstream. Resolve it manually; no reset was performed.' >&2
  exit 1
}
git push origin refs/remotes/upstream/main:refs/heads/main

if git merge-base --is-ancestor upstream/main origin/custom; then
  echo 'custom already contains the latest upstream commits.'
  exit 0
fi

owner=${GH_REPO%%/*}
pr_number=$(gh api --method GET "repos/$GH_REPO/pulls" \
  -f state=open -f base=custom -f head="$owner:main" --jq '.[0].number // empty')
if [[ -z "$pr_number" ]]; then
  body_file=$(mktemp)
  trap 'rm -f "$body_file"' EXIT
  cat > "$body_file" <<'BODY'
将官方 main 的最新提交合入定制版本，保留上游历史和现有定制功能。

自动验证会测试合并后的代码，包括前后端检查与构建。有冲突或检查失败时，请先处理问题。请使用 **Create a merge commit** 合并；不要 squash、rebase 或删除 main 分支。

合并后会构建此 Fork 的 Docker 镜像。生产环境仍需选择经过验证的固定镜像版本后升级。
BODY
  pr_number=$(gh api --method POST "repos/$GH_REPO/pulls" \
    -f title='chore: sync official updates into custom' \
    -f head=main -f base=custom -F "body=@$body_file" --jq '.number')
fi

head_sha=$(git rev-parse upstream/main)
gh api --method POST "repos/$GH_REPO/statuses/$head_sha" \
  -f state=pending -f context='custom/validation' \
  -f description='Waiting for validation of the merged custom version' > /dev/null

# Explicit dispatch avoids relying on workflows being triggered by GITHUB_TOKEN
# PR events. The workflow lives on custom, while main stays an upstream mirror.
gh workflow run fork-ci.yml --repo "$GH_REPO" --ref custom -f pr_number="$pr_number"
echo "Sync PR: https://github.com/$GH_REPO/pull/$pr_number"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "Sync PR: https://github.com/$GH_REPO/pull/$pr_number" >> "$GITHUB_STEP_SUMMARY"
fi
