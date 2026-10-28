#!/usr/bin/env bash
# 起一个 devmaster 专用的 Keycloak（内存库 + 导入 realm），不动其他实例。
#
# 为什么单独一个端口：已有的 Keycloak（如 artifact 的 :18280）用 --db=dev-mem，
# 重启即丢数据，而且它的 admin 凭据不可知。这里用 --import-realm 在启动时
# 建好 realm 与测试用户，整个过程不需要 admin 登录，也不依赖上一个实例的状态。
#
# 用法：
#   ./scripts/keycloak-dev.sh start    # 前台起在 18281
#   ./scripts/keycloak-dev.sh info     # 打印 issuer 与测试账号
set -euo pipefail

KEYCLOAK_HOME="${KEYCLOAK_HOME:-$HOME/workspace/srvs/keycloak-26.7.4}"
PORT="${DEVMASTER_KEYCLOAK_PORT:-18281}"
MGMT_PORT="${DEVMASTER_KEYCLOAK_MGMT_PORT:-18381}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REALM_FILE="${ROOT_DIR}/deploy/keycloak/realm-devmaster.json"

if [[ ! -x "${KEYCLOAK_HOME}/bin/kc.sh" ]]; then
  echo "KEYCLOAK_HOME=${KEYCLOAK_HOME} 下找不到 bin/kc.sh" >&2
  exit 2
fi
if [[ ! -f "${REALM_FILE}" ]]; then
  echo "找不到 realm 定义：${REALM_FILE}" >&2
  exit 2
fi

command="${1:-start}"
case "${command}" in
  start)
    import_dir="${KEYCLOAK_HOME}/data/import"
    mkdir -p "${import_dir}"
    # import-realm 只读这个目录，复制而不是软链，避免 Keycloak 的清理行为改到仓库文件。
    cp "${REALM_FILE}" "${import_dir}/realm-devmaster.json"
    echo "导入目录：${import_dir}"
    echo "issuer：http://127.0.0.1:${PORT}/realms/devmaster"
    exec "${KEYCLOAK_HOME}/bin/kc.sh" start-dev \
      --db=dev-mem \
      --http-port "${PORT}" \
      --http-host 127.0.0.1 \
      --http-management-port "${MGMT_PORT}" \
      --http-management-host 127.0.0.1 \
      --hostname-strict=false \
      --import-realm
    ;;
  info)
    echo "issuer   : http://127.0.0.1:${PORT}/realms/devmaster"
    echo "client   : devmaster-web（public, PKCE S256）"
    echo "callback : http://127.0.0.1:3031/api/auth/callback"
    echo "测试账号 : alice / devmaster（DEV_MEMBER, DEV_LEAD）"
    echo "           bob   / devmaster（DEV_MEMBER, RELEASE_APPROVER）"
    ;;
  *)
    echo "用法：$0 [start|info]" >&2
    exit 2
    ;;
esac
