#!/usr/bin/env bash
# scripts/local-migration-rollback-drill.sh —— Issue66 W7 本地迁移回滚演练
# （计划 §10.3 第 2 项）。只使用本机临时目录，不触碰服务器、不发付费请求、
# 不改任何工作树内容（基线源码以临时 detached worktree 检出，演练后移除）。
#
# 流程：
#   1. 从基线提交 0c80eb0 构建旧二进制（内嵌旧 catalog）；
#   2. 旧二进制建库（迁移头 =0020）并经 /api/v1/tasks 写入真实任务行；
#   3. 停写，取得升级前 DB 快照（文件级拷贝，含 WAL 边车，记录版本/时间/哈希）；
#   4. 新二进制（当前工作树）升级到迁移头（0021/0022）并写入任务；
#      升级阶段同时以 CATALOG_FILE 演练外部 catalog 协同；
#   5. 版本守卫演示：旧二进制对升级后的库启动必须硬失败（错误含版本号）；
#   6. 恢复升级前快照，旧二进制重开必须成功、数据可读且不依赖新列；
#   7. 清理（worktree、进程、临时目录；DRILL_KEEP=1 保留现场）。
#
# 用法：在 provider-schema-foundation 工作树内执行
#   bash scripts/local-migration-rollback-drill.sh
# 可选环境变量：DRILL_WORKDIR（工作目录，默认 mktemp）、DRILL_LOG（日志路径，
# 默认 /tmp/w7-migration-rollback-drill-<date>.log）、DRILL_KEEP=1。

set -euo pipefail

BASE_COMMIT="${DRILL_BASE_COMMIT:-0c80eb04cc7c02817fe166123f9ef7622bf7350c}"
BASE_MIGRATION_HEAD=20
NEW_MIGRATION_HEAD=22
OFFERING_ID="meshy/meshy-7/text-to-3d"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKTREE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_GIT_DIR="$(git -C "$WORKTREE_DIR" rev-parse --git-common-dir)"
REPO_ROOT="$(cd "$REPO_GIT_DIR/.." && pwd)"

command -v go >/dev/null || { echo "FATAL: go 不在 PATH" >&2; exit 2; }
command -v curl >/dev/null || { echo "FATAL: curl 不在 PATH" >&2; exit 2; }
command -v sqlite3 >/dev/null || { echo "FATAL: sqlite3 不在 PATH" >&2; exit 2; }

WORK="${DRILL_WORKDIR:-$(mktemp -d "${TMPDIR:-/tmp}/art-ai-rollback-drill.XXXXXX")}"
LOG="${DRILL_LOG:-$WORKTREE_DIR//tmp/w7-migration-rollback-drill-$(date +%Y-%m-%d).log}"
mkdir -p "$WORK/bin" "$WORK/run/db" "$WORK/run/storage" "$WORK/run/legacy" "$(dirname "$LOG")"
exec > >(tee -a "$LOG") 2>&1

BASE_WORKTREE="$WORK/base-src"
BASELINE_ADDED=0
SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "$BASELINE_ADDED" -eq 1 ]; then
    git -C "$REPO_ROOT" worktree remove --force "$BASE_WORKTREE" >/dev/null 2>&1 || true
  fi
  if [ "${DRILL_KEEP:-0}" != "1" ]; then
    rm -rf "$WORK" || true
  else
    echo "[cleanup] DRILL_KEEP=1，保留现场：$WORK"
  fi
}
trap cleanup EXIT

log() { printf '\n===== %s =====\n' "$*"; }
fail() { echo "DRILL FAIL: $*" >&2; exit 1; }

# 有界等待：轮询 /readyz 直到 200，超时 fail。
wait_ready() {
  local addr="$1" label="$2" i
  for i in $(seq 1 60); do
    if curl -fsS -o /dev/null "http://$addr/readyz" 2>/dev/null; then
      echo "[ready] $label 就绪（${i}s 内）"
      return 0
    fi
    sleep 1
  done
  fail "$label 在 60s 内未就绪"
}

stop_server() {
  [ -n "$SERVER_PID" ] || return 0
  kill -TERM "$SERVER_PID"
  wait "$SERVER_PID" 2>/dev/null || true
  echo "[stop] server pid=$SERVER_PID 已停止"
  SERVER_PID=""
}

start_server() { # $1=binary $2=label；其余经 env 传入
  local binary="$1" label="$2"
  echo "[start] ${label}：${binary} APP_ADDR=${APP_ADDR}"
  "$binary" >"$WORK/run/$label.stdout.log" 2>"$WORK/run/$label.stderr.log" &
  SERVER_PID=$!
  wait_ready "$APP_ADDR" "$label" || { tail -20 "$WORK/run/$label.stderr.log"; fail "$label 启动失败"; }
}

submit_task() { # $1=label
  local label="$1" body
  body="$(curl -fsS -X POST "http://$APP_ADDR/api/v1/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"capability\":\"text-to-3d\",\"offering_id\":\"$OFFERING_ID\",\"input\":{\"prompt\":\"rollback drill $label\"}}")" \
    || fail "$label 任务提交失败"
  echo "$body" | head -c 400; echo
  echo "$body" | rg -q '"task_id"' || fail "$label 响应缺少 task_id"
  echo "[write] $label 已写入任务行"
}

list_tasks() {
  curl -fsS "http://$APP_ADDR/api/v1/tasks?capability=text-to-3d" | head -c 400; echo
}

db_version() { sqlite3 "$DB_PATH" 'SELECT COALESCE(MAX(version),0) FROM go_schema_migrations;'; }

column_exists() { # $1=table $2=column
  [ "$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM pragma_table_info('$1') WHERE name='$2';")" = "1" ]
}

echo "=============================================================="
echo " Issue66 W7 本地迁移回滚演练 $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo " 工作目录：$WORK"
echo " 日志：$LOG"
echo " 基线提交：${BASE_COMMIT}（迁移头 ${BASE_MIGRATION_HEAD}）"
echo " 当前工作树：${WORKTREE_DIR}（迁移头 ${NEW_MIGRATION_HEAD}）"
echo "=============================================================="

DB_PATH="$WORK/run/db/art-ai.db"
SNAPSHOT_PREFIX="$WORK/snapshot-pre-upgrade/art-ai.db"
APP_ADDR="127.0.0.1:$((21000 + RANDOM % 20000))"
export APP_ADDR DATABASE_PATH="$DB_PATH" STORAGE_ROOT="$WORK/run/storage" \
  LEGACY_GENERATED_IMAGES_ROOT="$WORK/run/legacy" APP_ENV=development \
  AUTH_MODE=development PROVIDER_MODE=mock AGENT_MODE=mock

log "步骤 1/7：构建基线（旧）二进制"
git -C "$REPO_ROOT" worktree add --detach "$BASE_WORKTREE" "$BASE_COMMIT" \
  || fail "无法检出基线 worktree"
BASELINE_ADDED=1
if ! (cd "$BASE_WORKTREE" && go build -o "$WORK/bin/art-ai-base" ./cmd/art-ai); then
  fail "基线二进制构建失败"
fi
echo "[build] 基线二进制：$WORK/bin/art-ai-base"

log "步骤 2/7：新（当前工作树）二进制"
if ! (cd "$WORKTREE_DIR" && go build -o "$WORK/bin/art-ai-new" ./cmd/art-ai); then
  fail "新二进制构建失败"
fi
echo "[build] 新二进制：$WORK/bin/art-ai-new"

log "步骤 3/7：旧二进制建库 + 写入数据"
start_server "$WORK/bin/art-ai-base" "base-phase1"
[ "$(db_version)" = "$BASE_MIGRATION_HEAD" ] || fail "基线库迁移头应为 $BASE_MIGRATION_HEAD，实际 $(db_version)"
echo "[schema] 基线库迁移头 = $(db_version)"
submit_task base-phase1
sleep 2
stop_server

log "步骤 4/7：停写快照（升级前 DB 快照 = 回滚集合成员）"
mkdir -p "$(dirname "$SNAPSHOT_PREFIX")"
for suffix in "" "-wal" "-shm"; do
  [ -f "$DB_PATH$suffix" ] && cp "$DB_PATH$suffix" "$SNAPSHOT_PREFIX$suffix"
done
echo "[snapshot] 快照版本 = $(sqlite3 "$SNAPSHOT_PREFIX" 'SELECT MAX(version) FROM go_schema_migrations;')"
echo "[snapshot] sha256：$(shasum -a 256 "$SNAPSHOT_PREFIX" | awk '{print $1}')"
echo "[snapshot] 时间：$(date -u +%Y-%m-%dT%H:%M:%SZ)"

log "步骤 5/7：新二进制升级到 head（0021/0022）+ 写入（外部 catalog 协同）"
export CATALOG_FILE="$WORK/run/catalog.json"
cp "$WORKTREE_DIR/internal/catalog/default_catalog.json" "$CATALOG_FILE"
start_server "$WORK/bin/art-ai-new" "upgrade-phase"
[ "$(db_version)" = "$NEW_MIGRATION_HEAD" ] || fail "升级后迁移头应为 $NEW_MIGRATION_HEAD，实际 $(db_version)"
echo "[schema] 升级后迁移头 = $(db_version)"
column_exists generation_attempts dispatch_checkpoint_json || fail "0021 列缺失"
column_exists generation_tasks resource_state_json || fail "0022 列缺失"
echo "[schema] dispatch_checkpoint_json / resource_state_json 列已存在"
submit_task upgrade-phase
sleep 2
stop_server
unset CATALOG_FILE

log "步骤 6/7：版本守卫——旧二进制对升级库必须硬失败"
set +e
APP_ADDR_SAVED="$APP_ADDR"
"$WORK/bin/art-ai-base" >"$WORK/run/guard.stdout.log" 2>"$WORK/run/guard.stderr.log" &
GUARD_PID=$!
GUARD_EXIT=0
for i in $(seq 1 20); do
  if ! kill -0 "$GUARD_PID" 2>/dev/null; then
    wait "$GUARD_PID" || GUARD_EXIT=$?
    break
  fi
  sleep 1
done
if kill -0 "$GUARD_PID" 2>/dev/null; then
  kill -TERM "$GUARD_PID" 2>/dev/null; wait "$GUARD_PID" 2>/dev/null
  GUARD_EXIT=1
  sed -n '1,5p' "$WORK/run/guard.stderr.log" >&2
  fail "旧二进制在升级库上没有按预期退出"
fi
set -e
echo "[guard] 旧二进制退出码 = ${GUARD_EXIT}（预期非 0）"
rg -n "高于当前二进制" "$WORK/run/guard.stdout.log" "$WORK/run/guard.stderr.log" \
  || fail "版本守卫错误信息未包含版本比较文案"
echo "[guard] 版本守卫错误已记录（含双方版本号）"
APP_ADDR="$APP_ADDR_SAVED"

log "步骤 7/7：恢复快照 → 旧二进制重开 + 数据可读 + 不依赖新列"
rm -f "$DB_PATH" "$DB_PATH-wal" "$DB_PATH-shm"
for suffix in "" "-wal" "-shm"; do
  [ -f "$SNAPSHOT_PREFIX$suffix" ] && cp "$SNAPSHOT_PREFIX$suffix" "$DB_PATH$suffix"
done
[ "$(db_version)" = "$BASE_MIGRATION_HEAD" ] || fail "恢复后迁移头应为 $BASE_MIGRATION_HEAD"
if column_exists generation_tasks resource_state_json; then
  fail "恢复后不应存在 0022 列（快照必须前置于升级）"
fi
echo "[restore] 快照已恢复（迁移头 $(db_version)，无 0021/0022 列）"
start_server "$WORK/bin/art-ai-base" "rollback-reopen"
echo "[read] 恢复后任务列表："
list_tasks
list_tasks | rg -q '"id"' || fail "恢复后读取任务列表失败"
stop_server

echo
echo "=============================================================="
echo " DRILL PASS：升级到 head / 快照 / 守卫硬错误 / 恢复重开 全部通过"
echo " 回滚口径：旧二进制 + 旧 catalog + 升级前 DB 快照（§10.3）"
echo "=============================================================="
