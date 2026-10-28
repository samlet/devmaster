#!/usr/bin/env bash
# 把 devmaster 的发布审批流程定义发布到 approver（幂等：重复执行会覆盖草稿并发布新版本）。
set -euo pipefail

APPROVER_URL="${APPROVER_URL:-http://127.0.0.1:18090}"
DEFINITION="${1:-$(dirname "$0")/../assets/approver/devmaster-release-approval.json}"
PROCESS_KEY="devmaster-release-approval"

if [[ ! -f "$DEFINITION" ]]; then
    echo "definition not found: $DEFINITION" >&2
    exit 1
fi

echo "publishing $PROCESS_KEY to $APPROVER_URL"
curl -fsS -X PUT "$APPROVER_URL/api/v1/process-definitions/$PROCESS_KEY/draft" \
    -H 'X-User-Id: devmaster-bootstrap' \
    -H 'X-Roles: PROCESS_ADMIN' \
    -H 'Content-Type: application/json' \
    --data-binary "@$DEFINITION" >/dev/null

curl -fsS -X POST "$APPROVER_URL/api/v1/process-definitions/$PROCESS_KEY/publish" \
    -H 'X-User-Id: devmaster-bootstrap' \
    -H 'X-Roles: PROCESS_ADMIN' >/dev/null

echo "published $PROCESS_KEY"
