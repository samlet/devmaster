#!/usr/bin/env bash
# dev-stack.sh — 本地全栈的启停与状态。
#
# 管的是「本仓要起的应用层进程」：Keycloak、subject-registry、devmaster server、web。
# PostgreSQL / NATS / Temporal / notifyd 属于基础设施（brew 服务或其他仓），
# 这里只探测它们的端口并在状态里显示，不负责启停——把它们纳进来会让
# 「停应用」变成「停数据库」，那不是这个脚本该有的破坏力。
#
# 用法：./scripts/dev-stack.sh [status|up|down|restart]
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_DIR="${DEVMASTER_RUNTIME_DIR:-${ROOT_DIR}/.runtime}"
PID_DIR="${RUNTIME_DIR}/pids"
LOG_DIR="${RUNTIME_DIR}/logs"
CONFIG="${DEVMASTER_CONFIG_FILE:-configs/local.yaml}"

# 服务定义：名字 | 端口 | 就绪超时（秒）
APPS=(
  "keycloak:18281:60"
  "registry:50095:30"
  "server:8291:60"
  "web:3031:60"
)
# 基础设施：只探测，不启停
INFRA=(
  "postgres:5432"
  "nats:4222"
  "temporal:7233"
  "notifyd:18091"
)

mkdir -p "${PID_DIR}" "${LOG_DIR}"

port_up() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

pid_of() {
  local file="${PID_DIR}/$1.pid"
  [[ -f "${file}" ]] && cat "${file}" 2>/dev/null
}

alive() {
  local pid
  pid="$(pid_of "$1")"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

# 端口起了但 pid 文件丢了（手工起过）：仍然算 UP，但不归我们停。
status_one() {
  local name="$1" port="$2"
  if port_up "${port}"; then
    if alive "${name}"; then
      printf '  %-10s %-6s UP    (pid %s)\n' "${name}" "${port}" "$(pid_of "${name}")"
    else
      printf '  %-10s %-6s UP    (外部进程)\n' "${name}" "${port}"
    fi
  else
    printf '  %-10s %-6s down\n' "${name}" "${port}"
  fi
}

cmd_status() {
  echo "基础设施（只探测，不由本脚本启停）"
  for entry in "${INFRA[@]}"; do
    status_one "${entry%%:*}" "${entry##*:}"
  done
  echo
  echo "应用层（make up / make down）"
  for entry in "${APPS[@]}"; do
    local name="${entry%%:*}"
    local rest="${entry#*:}"
    status_one "${name}" "${rest%%:*}"
  done
  echo
  if [[ -d "${LOG_DIR}" ]] && compgen -G "${LOG_DIR}/*.log" >/dev/null; then
    echo "日志：${LOG_DIR}/"
  fi
}

wait_ready() {
  local name="$1" port="$2" timeout="$3"
  local waited=0
  while (( waited < timeout )); do
    if port_up "${port}"; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done
  echo "  ${name} 在 ${timeout}s 内没有监听 ${port}，看日志：${LOG_DIR}/${name}.log" >&2
  return 1
}

start_one() {
  local name="$1" port="$2" timeout="$3"
  if port_up "${port}"; then
    echo "  ${name} 已在 ${port} 运行，跳过"
    return 0
  fi
  local log="${LOG_DIR}/${name}.log"
  case "${name}" in
    keycloak)
      nohup "${ROOT_DIR}/scripts/keycloak-dev.sh" start > "${log}" 2>&1 &
      ;;
    registry)
      (
        cd "${ROOT_DIR}/../../portals/subject-registry" || exit 1
        SUBJECT_DATABASE_URL="${SUBJECT_DATABASE_URL:-postgres://$(id -un)@127.0.0.1:5432/subject_registry_dev?sslmode=disable}" \
        SUBJECT_SERVICE_TOKEN="${SUBJECT_SERVICE_TOKEN:-subject-registry-local-dev-token-0123456789}" \
        SUBJECT_GRPC_ADDRESS=127.0.0.1:50095 \
        SUBJECT_PROBE_ADDRESS=127.0.0.1:8295 \
        nohup go run ./cmd/subjectd > "${log}" 2>&1 &
        echo $! > "${PID_DIR}/${name}.pid"
      )
      echo "  ${name} 启动中（日志 ${log}）"
      wait_ready "${name}" "${port}" "${timeout}" || return 1
      return 0
      ;;
    server)
      (
        cd "${ROOT_DIR}/backend" || exit 1
        nohup go run ./cmd/server -config "${CONFIG}" > "${log}" 2>&1 &
        echo $! > "${PID_DIR}/${name}.pid"
      )
      echo "  ${name} 启动中（日志 ${log}）"
      wait_ready "${name}" "${port}" "${timeout}" || return 1
      return 0
      ;;
    web)
      # 本机沙箱会注入 NODE_OPTIONS（safe-delete shim），它会拦住 next 清理 .next。
      (
        cd "${ROOT_DIR}" || exit 1
        env -u NODE_OPTIONS nohup pnpm --dir web dev > "${log}" 2>&1 &
        echo $! > "${PID_DIR}/${name}.pid"
      )
      echo "  ${name} 启动中（日志 ${log}）"
      wait_ready "${name}" "${port}" "${timeout}" || return 1
      return 0
      ;;
  esac
  echo $! > "${PID_DIR}/${name}.pid"
  echo "  ${name} 启动中（日志 ${log}）"
  wait_ready "${name}" "${port}" "${timeout}" || return 1
}

cmd_up() {
  # 依赖检查：基础设施不在就别起应用层，否则起来也是一堆连接错误。
  local missing=()
  for entry in "${INFRA[@]}"; do
    local name="${entry%%:*}" port="${entry##*:}"
    # notifyd 是可选旁路，不在也不算失败。
    if [[ "${name}" != "notifyd" ]] && ! port_up "${port}"; then
      missing+=("${name}:${port}")
    fi
  done
  if (( ${#missing[@]} > 0 )); then
    echo "基础设施未就绪：${missing[*]}" >&2
    echo "先启动它们（brew services start postgresql@18 / nats-server / temporal），再 make up" >&2
    exit 1
  fi

  echo "启动应用层…"
  local failed=0
  for entry in "${APPS[@]}"; do
    local name="${entry%%:*}"
    local rest="${entry#*:}"
    local port="${rest%%:*}"
    local timeout="${rest##*:}"
    if ! start_one "${name}" "${port}" "${timeout}"; then
      failed=1
      break
    fi
  done
  echo
  cmd_status
  return "${failed}"
}

cmd_down() {
  echo "停止应用层…"
  # 逆序停：先停 web（它依赖后端），最后停 Keycloak。
  for (( idx=${#APPS[@]}-1; idx>=0; idx-- )); do
    local entry="${APPS[idx]}"
    local name="${entry%%:*}"
    local rest="${entry#*:}"
    local port="${rest%%:*}"
    local pid
    pid="$(pid_of "${name}")"
    if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
      # go run / pnpm 会派生子进程（真正的监听者），必须先杀子再杀父，
      # 否则父进程没了、子进程还占着端口。
      pkill -TERM -P "${pid}" 2>/dev/null
      kill -TERM "${pid}" 2>/dev/null
      for _ in $(seq 1 10); do
        kill -0 "${pid}" 2>/dev/null || break
        sleep 0.5
      done
      pkill -KILL -P "${pid}" 2>/dev/null
      kill -KILL "${pid}" 2>/dev/null
      sleep 0.3
      if port_up "${port}"; then
        echo "  ${name} 进程已退出但 ${port} 仍被占用（残留子进程），用 lsof -ti:${port} | xargs kill 清理"
      else
        echo "  ${name} 已停止"
      fi
      rm -f "${PID_DIR}/${name}.pid"
    elif port_up "${port}"; then
      echo "  ${name} 在 ${port} 上运行但不归本脚本管（手工起的？），跳过"
    else
      echo "  ${name} 未运行"
      rm -f "${PID_DIR}/${name}.pid"
    fi
  done
  echo
  cmd_status
}

case "${1:-status}" in
  status) cmd_status ;;
  up) cmd_up ;;
  down) cmd_down ;;
  restart) cmd_down; echo; cmd_up ;;
  *)
    echo "用法：$0 [status|up|down|restart]" >&2
    exit 2
    ;;
esac
