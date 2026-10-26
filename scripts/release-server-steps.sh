#!/usr/bin/env bash
# AGT-12 发布服务器段（在目标主机上执行）：构建 → 预检 → 停机窗口（记录旧
# 镜像标签 + 一致性备份）→ 同一 Compose 项目更新唯一 Go 服务 → 健康检查；
# 健康失败自动回滚到旧镜像标签并留下失败报告。
#
# 与正式生产切换的边界（dev_ops.md / README-go.md）：
#   - 普通版本发布走 docker compose 更新本栈 Compose 项目的唯一
#     art-ai-go 服务；**不重跑 ETL，不改写 migration-ready.txt**；
#   - 带迁移就绪证明的 cutover takeover 属于正式生产切换入口（由
#     scripts/finalize-go-production.sh 使用），普通发布不得调用；
#   - 默认待停机备份 production/data、production/storage-go、production/web-data；
#     新容器或健康失败时按备份范围恢复并以旧镜像标签重新拉起。仅 dev-test 可设
#     RELEASE_BACKUP_SCOPE=database（只备份/恢复 data，资产与 web-data 保留），
#     健康且成功清单写入后按 RELEASE_BACKUP_KEEP（1-2）回收更早的已结束发布备份。
#
# 用法: bash release-server-steps.sh <tag> <sha> <remote_root>
#
# <remote_root> 与下列栈参数都由 scripts/release-env.py 按未纳入 Git 的
# deploy/local/environments.json 中该环境的设置传入，本脚本不写死任何主机路径：
#   RELEASE_COMPOSE_PROJECT  本栈 Compose 项目名（必填，来自环境文件 compose_project；
#                            仓库不写任何环境的项目名，#261）
#   RELEASE_SINGLE_STACK     =1 表示本机只有这一个 Plinth 项目（单栈主机）：镜像保留只看
#                            本项目，legacy 目录限在 <remote_root>/production/legacy-*
#   RELEASE_PROTECT_PROJECTS 共享主机上其他 Plinth Compose 项目（逗号分隔，环境文件
#                            protect_projects）；其容器引用的镜像不被清理
#   RELEASE_IMAGE_KEEP       镜像保留数（缺省 5）
#   RELEASE_BACKUP_SCOPE     full（缺省，全量备份且不清理）或 database（仅 dev-test；
#                            只备份/恢复 production/data，发布成功后再清理旧备份）
#   RELEASE_BACKUP_KEEP      仅 database 模式必填，保留最新两个已结束发布备份，
#                            取值 1-2；进行中的快照可临时排除，诊断日志独立保留
#   同一发布根的发布备份/恢复、备份清理与手工恢复共用 <root>/.release.lock；
#   本脚本在构建或改动数据前获取，retention 继承该描述符，缺失或伪造即拒绝。
#   RELEASE_ALLOW_BOOTSTRAP  =1 时允许空栈首次启动（无旧容器、无数据库）
#   GO_PROD_*_DIR / GO_LEGACY_*_DIR / GO_WEB_BIND_IP / GO_WEB_HOST_PORT
#                            Compose 主机路径与绑定地址，导出给每次 compose 与预检；
#                            数据目录必须就是 <remote_root>/production/<名称>（未传时按此导出）；
#                            legacy 目录没有缺省值，单栈主机上必须在 <remote_root>/production/
#                            下以 legacy 开头，共享主机上须为不含 .. 的绝对路径。
#                            设置后成功发布写出 production/compose.env 供手工 compose。
#   RELEASE_EXTRA_COMPOSE_FILE 追加在主 Compose 文件之后的服务器本地覆盖文件（例如固定
#                            项目网络与容器地址），必须是 <remote_root>/production/ 下的普通文件；
#                            只用于本栈服务，隔离预检不使用它
#   RELEASE_BUILD_NETWORK    docker build --network（例如构建容器需用宿主源地址访问内网 Go 代理或基础镜像源）
#   RELEASE_GOPROXY          docker build 的 GOPROXY 构建参数（缺省用 Dockerfile 默认值）
#   RELEASE_BUILD_ADD_HOSTS  docker build --add-host，逗号分隔的 主机名:地址（解析内网 Go 代理等构建依赖主机）
#   ART_AI_PROTECTED_PATHS   本栈根与环境文件 protected_paths（冒号分隔）；发布本身不用，只记入
#                            production/compose.env，供在该主机手工运行的备份/ETL/finalize 读取（#334）
set -euo pipefail
# Release artifacts can contain private data. Do not inherit a permissive mask.
umask 077

TAG="$1"
SHA="$2"
ROOT="${3:?usage: release-server-steps.sh <tag> <sha> <remote_root>}"
case "$ROOT" in
  /*) ;;
  *) echo "[server] remote_root must be an absolute path; nothing was changed" >&2; exit 1 ;;
esac
APP="$ROOT/app"
RUNTIME_ENV="$ROOT/production/runtime.env"
COMPOSE_FILE="$APP/docker-compose.go-production.yml"
COMPOSE_PROJECT=${RELEASE_COMPOSE_PROJECT:-}
SINGLE_STACK=${RELEASE_SINGLE_STACK:-0}
PROTECT_PROJECTS=${RELEASE_PROTECT_PROJECTS:-}
if ! [[ "$COMPOSE_PROJECT" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]]; then
  echo "[server] RELEASE_COMPOSE_PROJECT is required (the environment's compose_project); nothing was changed" >&2
  exit 1
fi
if ! [[ "$SINGLE_STACK" =~ ^[01]$ ]] || ! [[ "$PROTECT_PROJECTS" =~ ^([a-z0-9][a-z0-9_-]{0,62}(,[a-z0-9][a-z0-9_-]{0,62})*)?$ ]] ||
  { [ "$SINGLE_STACK" = 1 ] && [ -n "$PROTECT_PROJECTS" ]; } || ! [[ "${RELEASE_IMAGE_KEEP:-5}" =~ ^[1-9]$ ]]; then
  echo "[server] RELEASE_SINGLE_STACK, RELEASE_PROTECT_PROJECTS or RELEASE_IMAGE_KEEP is invalid; nothing was changed" >&2
  exit 1
fi
# #364: only the disposable dev-test stack may opt into a database-only
# snapshot. The default stays the full data/storage-go/web-data backup and no
# backup retention; formal-test and legacy-formal never set these variables.
BACKUP_SCOPE=${RELEASE_BACKUP_SCOPE:-full}
BACKUP_KEEP=${RELEASE_BACKUP_KEEP:-}
case "$BACKUP_SCOPE" in
  full)
    if [ -n "$BACKUP_KEEP" ]; then
      echo "[server] RELEASE_BACKUP_KEEP requires RELEASE_BACKUP_SCOPE=database; nothing was changed" >&2
      exit 1
    fi
    BACKUP_TARGETS=(data storage-go web-data)
    BACKUP_TARGETS_CSV=data,storage-go,web-data
    ;;
  database)
    if ! [[ "$BACKUP_KEEP" =~ ^[12]$ ]]; then
      echo "[server] RELEASE_BACKUP_SCOPE=database requires RELEASE_BACKUP_KEEP=1-2; nothing was changed" >&2
      exit 1
    fi
    BACKUP_TARGETS=(data)
    BACKUP_TARGETS_CSV=data
    ;;
  *)
    echo "[server] RELEASE_BACKUP_SCOPE must be full or database; nothing was changed" >&2
    exit 1
    ;;
esac
COMPOSE_SERVICE=art-ai-go
CONTAINER="${COMPOSE_PROJECT}-${COMPOSE_SERVICE}-1"
MANIFEST="$ROOT/releases/manifest.txt"
REPORT_DIR="$ROOT/production/reports/release-failures"
BACKUP_DIR="$ROOT/backups"

# One cooperative lock per release root: deployment backup/restore, release
# backup retention and the documented manual recovery share it. Acquire it
# before any build or data change; an inherited descriptor from
# release-lock.py run must validate, otherwise refuse. A missing helper fails
# closed instead of running without exclusion.
RELEASE_LOCK_HELPER="$APP/scripts/release-lock.py"
if [ ! -f "$RELEASE_LOCK_HELPER" ] || [ -L "$RELEASE_LOCK_HELPER" ]; then
  echo "[server] release lock helper is missing; nothing was changed" >&2
  exit 1
fi
if [ -n "${RELEASE_LOCK_FD:-}" ] || [ -n "${RELEASE_LOCK_ROOT:-}" ]; then
  if ! python3 "$RELEASE_LOCK_HELPER" validate --root "$ROOT"; then
    echo "[server] inherited release lock is invalid; nothing was changed" >&2
    exit 1
  fi
else
  SELF=$(cd "$(dirname "$0")" && pwd)/$(basename "$0")
  exec python3 "$RELEASE_LOCK_HELPER" run --root "$ROOT" -- bash "$SELF" "$@"
fi

# 栈布局：调用方传入的 Compose 主机路径和绑定地址；所有 compose 调用与预检
# 一致，备份、恢复与容量检查的路径仍固定在 $ROOT/production 下。
LAYOUT_KEYS="GO_WEB_BIND_IP GO_WEB_HOST_PORT GO_PROD_DB_DIR GO_PROD_STORAGE_DIR GO_PROD_WEB_DATA_DIR
GO_PROD_LOG_DIR GO_LEGACY_GENERATED_IMAGES_DIR GO_LEGACY_ASSET_CACHE_DIR GO_LEGACY_THUMBNAIL_CACHE_DIR
GO_LEGACY_INPUT_ASSETS_DIR"
STACK_LAYOUT=0
validate_stack_layout() {
  local key value expected
  for key in $LAYOUT_KEYS; do
    value=$(printenv "$key" || true)
    [ -n "$value" ] || continue
    STACK_LAYOUT=1
    case "$key" in
      GO_PROD_DB_DIR) expected="$ROOT/production/data" ;;
      GO_PROD_STORAGE_DIR) expected="$ROOT/production/storage-go" ;;
      GO_PROD_WEB_DATA_DIR) expected="$ROOT/production/web-data" ;;
      GO_PROD_LOG_DIR) expected="$ROOT/production/logs" ;;
      GO_LEGACY_*)
        case "$value" in
          "$ROOT"/production/legacy[-/][a-z]*) expected="$value" ;;
          /*) [ "$SINGLE_STACK" = 1 ] && expected="" || expected="$value" ;;
          *) expected="" ;;
        esac
        ;;
      *) expected="$value" ;;
    esac
    case "$value" in *..*) expected="" ;; esac
    if [ "$value" != "$expected" ]; then
      echo "[server] $key does not match the release root layout; nothing was changed" >&2
      exit 1
    fi
    export "$key"
  done
}
validate_stack_layout
# 数据目录固定在发布根之下；未传时按发布根导出（Compose 文件不带主机路径缺省值）。
export GO_PROD_DB_DIR="${GO_PROD_DB_DIR:-$ROOT/production/data}"
export GO_PROD_STORAGE_DIR="${GO_PROD_STORAGE_DIR:-$ROOT/production/storage-go}"
export GO_PROD_WEB_DATA_DIR="${GO_PROD_WEB_DATA_DIR:-$ROOT/production/web-data}"
export GO_PROD_LOG_DIR="${GO_PROD_LOG_DIR:-$ROOT/production/logs}"
export GO_PROD_ROOT="$ROOT/production"

# 手工 compose 用的插值文件（不含密钥）：docker compose --env-file production/runtime.env
#   --env-file production/compose.env -p <项目> -f app/docker-compose.go-production.yml
#   [-f <RELEASE_EXTRA_COMPOSE_FILE>] …（后给的 --env-file 覆盖先给的）
write_compose_env() {
  local key value staged target="$ROOT/production/compose.env"
  [ "$STACK_LAYOUT" = 1 ] || return 0
  staged=$(mktemp "$target.XXXXXX") || return 1
  {
    echo "# Written by release-server-steps.sh after the last successful release; no secrets."
    echo "GO_IMAGE_TAG=$TAG"
    echo "GO_SOURCE_COMMIT=$SHA"
    echo "GO_RUNTIME_ENV_FILE=$RUNTIME_ENV"
    echo "RELEASE_BACKUP_SCOPE=$BACKUP_SCOPE"
    echo "RELEASE_BACKUP_TARGETS=$BACKUP_TARGETS_CSV"
    for key in $LAYOUT_KEYS ART_AI_PROTECTED_PATHS; do
      value=$(printenv "$key" || true)
      [ -z "$value" ] || echo "$key=$value"
    done
  } >"$staged" && mv "$staged" "$target" && return 0
  rm -f "$staged" || true
  return 1
}
HEALTH_HOST=${GO_WEB_BIND_IP:-127.0.0.1}
if [ "$HEALTH_HOST" = 0.0.0.0 ]; then
  HEALTH_HOST=127.0.0.1
fi
BASE="http://$HEALTH_HOST:${GO_WEB_HOST_PORT:-13333}"

# 空栈首次启动只在显式允许、本项目没有任何容器且没有数据库时进行。
BOOTSTRAP=0
if [ "${RELEASE_ALLOW_BOOTSTRAP:-0}" = 1 ]; then
  EXISTING_CONTAINERS=$(docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT")
  if [ -z "$EXISTING_CONTAINERS" ]; then
    if [ -e "$ROOT/production/data/art-ai.db" ]; then
      echo "[server] database exists without a $COMPOSE_PROJECT container; inspect before releasing" >&2
      exit 1
    fi
    BOOTSTRAP=1
  fi
fi
COMPOSE_FILES=(-f "$COMPOSE_FILE")
EXTRA_COMPOSE_FILE=${RELEASE_EXTRA_COMPOSE_FILE:-}
if [ -n "$EXTRA_COMPOSE_FILE" ]; then
  case "$EXTRA_COMPOSE_FILE" in
    "$ROOT"/production/*.yml | "$ROOT"/production/*.yaml) ;;
    *) EXTRA_COMPOSE_FILE="" ;;
  esac
  case "${RELEASE_EXTRA_COMPOSE_FILE}" in *..*) EXTRA_COMPOSE_FILE="" ;; esac
  if [ -z "$EXTRA_COMPOSE_FILE" ] || [ -L "$EXTRA_COMPOSE_FILE" ] || [ ! -f "$EXTRA_COMPOSE_FILE" ]; then
    echo "[server] RELEASE_EXTRA_COMPOSE_FILE must be a regular file under $ROOT/production; nothing was changed" >&2
    exit 1
  fi
  COMPOSE_FILES+=(-f "$EXTRA_COMPOSE_FILE")
fi
BUILD_OPTIONS=()
if [ -n "${RELEASE_BUILD_NETWORK:-}" ]; then
  BUILD_OPTIONS+=(--network "$RELEASE_BUILD_NETWORK")
fi
if [ -n "${RELEASE_GOPROXY:-}" ]; then
  BUILD_OPTIONS+=(--build-arg GOPROXY="$RELEASE_GOPROXY")
fi
if [ -n "${RELEASE_BUILD_ADD_HOSTS:-}" ]; then
  IFS=, read -r -a BUILD_ADD_HOSTS <<<"$RELEASE_BUILD_ADD_HOSTS"
  for entry in "${BUILD_ADD_HOSTS[@]}"; do
    case "$entry" in
      [A-Za-z0-9]*:[0-9A-Fa-f.:]*) BUILD_OPTIONS+=(--add-host "$entry") ;;
      *) echo "[server] RELEASE_BUILD_ADD_HOSTS entries must be host:address; nothing was changed" >&2; exit 1 ;;
    esac
  done
fi

# 本脚本由 ssh 直接执行（CWD 落在远端 home），所有路径一律绝对化。
compose() { # $1=image tag，其余为 compose 子命令
  local image_tag="$1"
  shift
  GO_IMAGE_TAG="$image_tag" \
    GO_SOURCE_COMMIT="$SHA" \
    GO_RUNTIME_ENV_FILE="$RUNTIME_ENV" \
    docker compose --env-file "$RUNTIME_ENV" \
      -p "$COMPOSE_PROJECT" "${COMPOSE_FILES[@]}" "$@"
}

health_ok() {
  [ "$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE/healthz")" = 200 ] &&
    [ "$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE/readyz")" = 200 ]
}

wait_healthy() { # $1=尝试次数
  local attempt
  for ((attempt = 0; attempt < "$1"; attempt++)); do
    if health_ok; then
      return 0
    fi
    sleep 3
  done
  return 1
}

record_manifest() {
  local staged
  mkdir -p "$(dirname "$MANIFEST")" || return 1
  staged=$(mktemp "${MANIFEST}.XXXXXX") || return 1
  if [ -e "$MANIFEST" ] && ! cat "$MANIFEST" >"$staged"; then
    rm -f "$staged" || true
    return 1
  fi
  if echo "$TAG $SHA $(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$staged" && mv "$staged" "$MANIFEST"; then
    return 0
  fi
  rm -f "$staged" || true
  return 1
}

fail_report() { # $1=阶段 $2=旧镜像标签
  mkdir -p "$REPORT_DIR" || return 1
  {
    echo "tag=$TAG sha=$SHA stage=$1 previous_image_tag=${2:-none}"
    echo "backup_scope=$BACKUP_SCOPE"
    echo "backup_targets=$BACKUP_TARGETS_CSV"
    echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "healthz=$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)"
    echo "readyz=$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w '%{http_code}' "$BASE/readyz" || true)"
    echo "backup=${BACKUP_DIR}/release-$TAG"
  } >"$REPORT_DIR/$TAG-$1.txt"
}

release_capacity_check() {
  echo "[server] capacity $1"
  if [ "$1" = bootstrap ]; then
    if [ "$BACKUP_SCOPE" = full ]; then
      python3 "$APP/scripts/release-capacity.py" "$ROOT" - bootstrap "$BACKUP_DIR/release-$TAG"
    else
      python3 "$APP/scripts/release-capacity.py" "$ROOT" - bootstrap "$BACKUP_DIR/release-$TAG" "$BACKUP_SCOPE"
    fi
    return
  fi
  if [ "$BACKUP_SCOPE" = full ]; then
    python3 "$APP/scripts/release-capacity.py" "$ROOT" "$CONTAINER" "$1" "$BACKUP_DIR/release-$TAG"
  else
    python3 "$APP/scripts/release-capacity.py" "$ROOT" "$CONTAINER" "$1" "$BACKUP_DIR/release-$TAG" "$BACKUP_SCOPE"
  fi
}

retain_test_images() {
  # Never run before candidate health and manifest success. No backup cleanup.
  local helper="${RETENTION_SCRIPT:-$APP/scripts/retain-art-ai-images.py}"
  local arguments=(--apply --rollback-image "art-ai-go-production:$OLD_TAG" --root "$ROOT" --project "$COMPOSE_PROJECT")
  local other
  if [ "$SINGLE_STACK" = 1 ]; then
    # Single-stack host: running image + this release's rollback image.
    arguments+=(--keep "${RELEASE_IMAGE_KEEP:-5}")
  else
    # Shared host: every other Plinth project keeps the images its containers use.
    for other in ${PROTECT_PROJECTS//,/ }; do
      arguments+=(--protect-project "$other")
    done
    if [ -n "${RELEASE_IMAGE_KEEP:-}" ]; then
      arguments+=(--keep "$RELEASE_IMAGE_KEEP")
    fi
  fi
  if ! python3 "$helper" "${arguments[@]}"; then
    echo "[server] WARNING: Plinth image retention incomplete; release remains successful" >&2
  fi
}

retain_release_backups() {
  # Only a database-only dev-test release prunes old snapshots, and only after
  # the candidate is healthy and the success manifest is written.
  [ "$BACKUP_SCOPE" = database ] || return 0
  local helper="$APP/scripts/retain-release-backups.py"
  if [ ! -f "$helper" ]; then
    echo "[server] WARNING: release backup retention helper is missing; release remains successful" >&2
    return 0
  fi
  if ! python3 "$helper" --root "$ROOT" --project "$COMPOSE_PROJECT" \
    --rollback-image "art-ai-go-production:$OLD_TAG" --keep "$BACKUP_KEEP" --apply; then
    echo "[server] WARNING: release backup retention incomplete; release remains successful" >&2
  fi
}

# Measurements fail closed. They are not a reservation against other services.
if [ "$BOOTSTRAP" = 1 ]; then
  mkdir -p "$ROOT/production" "$BACKUP_DIR"
  if ! release_capacity_check bootstrap; then
    echo "[server] capacity check failed; nothing was started" >&2
    exit 1
  fi
elif ! release_capacity_check before-build; then
  echo "[server] capacity check failed; previous service was not stopped" >&2
  exit 1
fi

echo "[server] build $TAG"
# 构建的 --add-host 与 GOPROXY 来自环境文件的 build 设置（#248、#249）；npm 源固定为公共
# registry（#257），不再有构建参数覆盖。
# 测试/正式栈运行含历史迁移工具的内部镜像目标（#259）；Dockerfile 默认目标是自部署镜像。
docker build ${BUILD_OPTIONS[@]+"${BUILD_OPTIONS[@]}"} \
  --target runtime-with-migration-tools \
  --build-arg GO_IMAGE_TAG="$TAG" --build-arg GO_SOURCE_COMMIT="$SHA" \
  -f "$APP/Dockerfile.go-runtime" -t "art-ai-go-production:$TAG" \
  "$APP" 2>&1 | tail -1

# #233：应用日志写入 production/logs。目录归镜像运行用户所有；在预检和停旧实例之前
# 用候选镜像以运行身份实际写删一次，不可写就中止，旧实例不动。
LOG_DIR_HOST="$ROOT/production/logs"
echo "[server] log directory $LOG_DIR_HOST"
read -r RUN_UID RUN_GID < <(docker run --rm --network none --entrypoint /bin/sh \
  "art-ai-go-production:$TAG" -c 'printf "%s %s\n" "$(id -u artai)" "$(id -g artai)"')
case "$RUN_UID:$RUN_GID" in
  0:*|*[!0-9:]*|:*|*:) echo "[server] runtime user identity is unavailable; previous service was not stopped" >&2; exit 1 ;;
esac
mkdir -p "$LOG_DIR_HOST"
if [ "$(id -u)" = 0 ]; then
  chown "$RUN_UID:$RUN_GID" "$LOG_DIR_HOST"
fi
chmod 0750 "$LOG_DIR_HOST"
if ! docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --user "$RUN_UID:$RUN_GID" --mount "type=bind,src=$LOG_DIR_HOST,dst=/probe" \
  --entrypoint /bin/sh "art-ai-go-production:$TAG" \
  -ec 'f=$(mktemp /probe/.write-check.XXXXXX); printf ok > "$f"; test -s "$f"; rm "$f"'; then
  echo "[server] log directory is not writable by the runtime user; previous service was not stopped" >&2
  exit 1
fi
# 自包含栈的空 legacy 兼容目录：由运行身份所有，供挂载与预检。
for directory in "${GO_LEGACY_GENERATED_IMAGES_DIR:-}" "${GO_LEGACY_ASSET_CACHE_DIR:-}" \
  "${GO_LEGACY_THUMBNAIL_CACHE_DIR:-}" "${GO_LEGACY_INPUT_ASSETS_DIR:-}"; do
  if [ -n "$directory" ] && [ ! -d "$directory" ]; then
    mkdir -p "$directory"
    if [ "$(id -u)" = 0 ]; then
      chown "$RUN_UID:$RUN_GID" "$directory"
    fi
    chmod 0750 "$directory"
  fi
done

echo "[server] preflight"
GO_PROD_PROJECT="$COMPOSE_PROJECT" bash "$APP/scripts/cutover-go-production.sh" preflight \
  --env-file "$RUNTIME_ENV" \
  --image-tag "$TAG" --source-commit "$SHA" --no-build 2>&1 | tail -1

bootstrap_failed() { # $1=failing stage; there is no previous version to restore
  compose "$TAG" stop "$COMPOSE_SERVICE" >/dev/null 2>&1 || true
  fail_report "bootstrap-$1" none || true
  echo "[server] bootstrap $1; candidate stopped, data kept for inspection" >&2
  exit 1
}

if [ "$BOOTSTRAP" = 1 ]; then
  echo "[server] bootstrap: no previous $COMPOSE_PROJECT instance, first start of $TAG"
  if ! compose "$TAG" up -d --no-deps "$COMPOSE_SERVICE" 2>&1 | tail -1; then
    bootstrap_failed candidate-up-failed
  fi
  if ! wait_healthy 30; then
    bootstrap_failed unhealthy-after-up
  fi
  if ! record_manifest; then
    bootstrap_failed manifest-write-failed
  fi
  write_compose_env || echo "[server] WARNING: compose.env not written" >&2
  echo "[server] healthy — bootstrapped $TAG, manifest updated"
  exit 0
fi

OLD_TAG="$(docker inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null |
  sed 's|^art-ai-go-production:||')"
if [ -z "$OLD_TAG" ]; then
  echo "[server] 旧实例 $CONTAINER 不存在，放弃发布（不新建生产实例）" >&2
  exit 1
fi
echo "[server] previous image tag=$OLD_TAG"

# Prepare a fresh backup destination while the previous service still runs.
# A repeated tag must not delete evidence from an earlier release attempt.
if ! mkdir -p "$BACKUP_DIR" || ! mkdir "$BACKUP_DIR/release-$TAG"; then
  fail_report "backup-setup-failed" "$OLD_TAG" || true
  echo "[server] backup setup failed; previous service was not stopped" >&2
  exit 1
fi
# Manual rollback reads this single-line scope file ("database" or "full").
if ! printf '%s\n' "$BACKUP_SCOPE" >"$BACKUP_DIR/release-$TAG/scope"; then
  fail_report "backup-setup-failed" "$OLD_TAG" || true
  echo "[server] backup scope metadata could not be written; previous service was not stopped" >&2
  exit 1
fi

restart_previous_unchanged() { # $1=failing stage; candidate has not written data
  local stage="$1"
  if ! compose "$OLD_TAG" up -d --no-deps "$COMPOSE_SERVICE" >/dev/null; then
    fail_report "rollback-start-failed" "$OLD_TAG" || true
    echo "[server] previous service could not be restarted" >&2
    exit 1
  fi
  if ! wait_healthy 30; then
    fail_report "rollback-also-unhealthy" "$OLD_TAG" || true
    echo "[server] previous service is unhealthy after restart" >&2
    exit 1
  fi
  fail_report "$stage" "$OLD_TAG" || true
  echo "[server] $stage; previous service restored without changing data" >&2
  exit 1
}

# 停机窗口：先停唯一服务，保证 SQLite 与存储目录备份一致。
if ! release_capacity_check before-stop; then
  fail_report "capacity-before-stop" "$OLD_TAG" || true
  echo "[server] capacity check failed; previous service was not stopped" >&2
  exit 1
fi
echo "[server] stop $COMPOSE_SERVICE"
if ! compose "$OLD_TAG" stop "$COMPOSE_SERVICE" >/dev/null; then
  restart_previous_unchanged "previous-stop-failed"
fi

# Re-measure the quiesced source trees before consuming archive space.
if ! release_capacity_check before-backup; then
  restart_previous_unchanged "capacity-before-backup"
fi

echo "[server] backup -> ${BACKUP_DIR}/release-$TAG ($BACKUP_TARGETS_CSV)"
for target in "${BACKUP_TARGETS[@]}"; do
  if ! tar -C "$ROOT/production" -cf "$BACKUP_DIR/release-$TAG/$target.tar" "$target"; then
    restart_previous_unchanged "backup-failed"
  fi
done

restore_backup() {
  local target restore_stage="$BACKUP_DIR/release-$TAG/restore"
  release_capacity_check restore || return 1
  rm -rf "$restore_stage" || return 1
  mkdir -p "$restore_stage" || return 1
  # Extract and validate every archive before replacing any live directory.
  for target in "${BACKUP_TARGETS[@]}"; do
    tar -tf "$BACKUP_DIR/release-$TAG/$target.tar" >/dev/null || return 1
    tar -C "$restore_stage" -xf "$BACKUP_DIR/release-$TAG/$target.tar" || return 1
    [ -d "$restore_stage/$target" ] || return 1
  done
  for target in "${BACKUP_TARGETS[@]}"; do
    rm -rf "$ROOT/production/$target" || return 1
    mv "$restore_stage/$target" "$ROOT/production/$target" || return 1
  done
}

rollback_after_update() { # $1=failing stage
  local stage="$1"
  echo "[server] $stage — stopping candidate before data restore" >&2
  if ! compose "$TAG" stop "$COMPOSE_SERVICE" >/dev/null; then
    fail_report "candidate-stop-failed" "$OLD_TAG" || true
    echo "[server] candidate could not be stopped; data was not modified" >&2
    exit 1
  fi
  if ! restore_backup; then
    fail_report "backup-restore-failed" "$OLD_TAG" || true
    if [ "$BACKUP_SCOPE" = database ]; then
      echo "[server] database restore failed; service remains stopped for recovery; storage-go and web-data were not modified" >&2
    else
      echo "[server] backup restore failed; service remains stopped for recovery" >&2
    fi
    exit 1
  fi
  if ! compose "$OLD_TAG" up -d --no-deps "$COMPOSE_SERVICE" 2>&1 | tail -1; then
    fail_report "rollback-start-failed" "$OLD_TAG" || true
    exit 1
  fi
  if wait_healthy 30; then
    fail_report "$stage" "$OLD_TAG" || true
    if [ "$BACKUP_SCOPE" = database ]; then
      echo "[server] restored database and rolled back to $OLD_TAG — healthy; candidate storage-go/web-data writes were kept (not a full rollback)" >&2
    else
      echo "[server] restored data and rolled back to $OLD_TAG — healthy" >&2
    fi
  else
    fail_report "rollback-also-unhealthy" "$OLD_TAG" || true
    echo "[server] rollback target also unhealthy — manual intervention required" >&2
  fi
  exit 1
}

if ! release_capacity_check before-up; then
  restart_previous_unchanged "capacity-before-up"
fi

echo "[server] up $TAG"
if ! compose "$TAG" up -d --no-deps "$COMPOSE_SERVICE" 2>&1 | tail -1; then
  rollback_after_update "candidate-up-failed"
fi

if wait_healthy 30; then
  if ! record_manifest; then
    rollback_after_update "manifest-write-failed"
  fi
  write_compose_env || echo "[server] WARNING: compose.env not written" >&2
  echo "[server] healthy — deployed $TAG (previous $OLD_TAG), manifest updated (backup_scope=$BACKUP_SCOPE)"
  retain_test_images
  retain_release_backups
  exit 0
fi

rollback_after_update "unhealthy-after-up"
