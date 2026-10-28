#!/usr/bin/env bash
# 证据存证验收：写一份证据进 artifact、回读校验字节一致、重复写必须回放同一修订。
#
# 前置：artifact content-api（默认 127.0.0.1:50071）+ Keycloak 在跑，
# 且 configs/local.yaml 的 artifact 段可用；该租户已 registry-seed devmaster profile。
set -euo pipefail

CONFIG="${DEVMASTER_CONFIG_FILE:-configs/local.yaml}"

check_once() {
    (cd "$(dirname "$0")/../backend" && go run ./cmd/evidence-check -config "$CONFIG" "$@")
}

field() {
    awk -F= -v key="$1" '$1 == key { print $2 }'
}

echo "== 首次存证：应当新建并回读校验通过"
first="$(check_once -title "evidence acceptance probe" -payload "acceptance bytes")"
echo "$first"
ref_one="$(printf '%s\n' "$first" | field ref)"
sha_one="$(printf '%s\n' "$first" | field payload_sha256)"
replayed_one="$(printf '%s\n' "$first" | field replayed)"
if [[ -z "$ref_one" || -z "$sha_one" ]]; then
    echo "首次存证没有返回 ref 或摘要" >&2
    exit 1
fi

echo "== 重复存证：同一载荷必须回放同一修订"
second="$(check_once -title "evidence acceptance probe" -payload "acceptance bytes")"
echo "$second"
ref_two="$(printf '%s\n' "$second" | field ref)"
replayed_two="$(printf '%s\n' "$second" | field replayed)"
if [[ "$ref_one" != "$ref_two" ]]; then
    echo "重复存证产生了新修订：$ref_one vs $ref_two" >&2
    exit 1
fi
if [[ "$replayed_two" != "true" ]]; then
    echo "重复存证没有命中幂等回放（replayed=$replayed_two）" >&2
    exit 1
fi

echo "== 载荷变化：必须落成另一份证据"
third="$(check_once -title "evidence acceptance probe" -payload "acceptance bytes changed")"
echo "$third"
ref_three="$(printf '%s\n' "$third" | field ref)"
if [[ "$ref_three" == "$ref_one" ]]; then
    echo "载荷变化却复用了同一修订" >&2
    exit 1
fi

echo "证据存证验收通过：ref=$ref_one sha256=$sha_one replayed_first=$replayed_one"
