#!/usr/bin/env bash
# 版本门禁验收：已发布放行、不存在拒绝。
# 前置：devmaster server 在 127.0.0.1:50091；库里存在已发布的 HOSP 1.0.0。
set -euo pipefail

ADDRESS="${ADDRESS:-127.0.0.1:50091}"
PRODUCT="${PRODUCT:-HOSP}"
RELEASED_VERSION="${RELEASED_VERSION:-1.0.0}"
MISSING_VERSION="${MISSING_VERSION:-9.9.9}"

run_gate() {
    (cd "$(dirname "$0")/../backend" && go run ./cmd/version-gate \
        -address "$ADDRESS" -product "$PRODUCT" -version "$1")
}

echo "== 已发布版本应当放行"
run_gate "$RELEASED_VERSION"

echo "== 不存在的版本应当拒绝"
set +e
run_gate "$MISSING_VERSION"
code=$?
set -e
if [[ "$code" -ne 1 ]]; then
    echo "期望退出码 1，实际 $code" >&2
    exit 1
fi
echo "门禁验收通过"
