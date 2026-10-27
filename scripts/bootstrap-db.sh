#!/usr/bin/env bash
# 创建 devmaster 的 owner / runtime 角色与两个数据库。
#
# 为什么要两个角色：
#   devmaster_owner  拥有表，跑迁移（RLS 对 owner 默认失效，迁移里用了 FORCE）。
#   devmaster_app    运行时角色，非 owner、非 superuser，真正受 RLS 约束。
# 运行时直接用 owner 或超管连库，RLS 会静默失效，组织隔离就只是纸面约定。
set -euo pipefail

PSQL_DB="${PSQL_DB:-postgres}"
OWNER_ROLE="${OWNER_ROLE:-devmaster_owner}"
APP_ROLE="${APP_ROLE:-devmaster_app}"
DBS="${DBS:-devmaster_dev devmaster_test}"

role_exists() {
    psql -d "$PSQL_DB" -Atqc "SELECT 1 FROM pg_roles WHERE rolname = '$1'" | grep -q 1
}

db_exists() {
    psql -d "$PSQL_DB" -Atqc "SELECT 1 FROM pg_database WHERE datname = '$1'" | grep -q 1
}

for role in "$OWNER_ROLE" "$APP_ROLE"; do
    if role_exists "$role"; then
        echo "role $role already exists"
    else
        psql -d "$PSQL_DB" -v ON_ERROR_STOP=1 -c "CREATE ROLE \"$role\" LOGIN" >/dev/null
        echo "created role $role"
    fi
done

for db in $DBS; do
    if db_exists "$db"; then
        echo "database $db already exists"
    else
        createdb -O "$OWNER_ROLE" "$db"
        echo "created database $db owned by $OWNER_ROLE"
    fi
done
