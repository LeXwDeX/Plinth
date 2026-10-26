#!/usr/bin/env bash
set -Eeuo pipefail

cutover_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"
cutover_compose_file="${cutover_root}/docker-compose.go-production.yml"
cutover_mode="${1:-}"
if [[ -n "$cutover_mode" ]]; then
  shift
fi
cutover_env_file=""
cutover_image_tag=""
cutover_source_commit=""
cutover_build="true"
cutover_finalize_legacy_id=""
# Compose 项目名由调用方给出（环境文件 compose_project）；仓库不写任何环境的项目名（#261）。
cutover_project="${GO_PROD_PROJECT:-}"
cutover_preflight_project="${GO_PREFLIGHT_PROJECT:-${cutover_project}_preflight}"
# 旧 Next 容器、Compose 项目与服务名同样由调用方给出（finalize-go-production.sh 按其参数导出）；
# takeover 必填，preflight 不接触旧容器（#261）。
cutover_legacy_container="${CUTOVER_LEGACY_CONTAINER:-}"
cutover_legacy_project="${CUTOVER_LEGACY_PROJECT:-}"
cutover_legacy_service="${CUTOVER_LEGACY_SERVICE:-}"
cutover_preflight_port="${CUTOVER_PREFLIGHT_PORT:-13341}"
cutover_wait_timeout="${CUTOVER_WAIT_TIMEOUT:-180}"
# 发布根下的 production 目录由调用方给出（release-server-steps.sh、finalize-go-production.sh）；
# 仓库不写死任何服务器路径（#249）。
cutover_prod_root="${GO_PROD_ROOT:-}"
cutover_ready_marker="${cutover_prod_root}/migration-ready.txt"
cutover_preflight_root=""
cutover_stopped_legacy="false"

cutover_usage() {
  printf '%s\n' \
    '用法:' \
    '  cutover-go-production.sh preflight --env-file <runtime.env> --image-tag <tag> --source-commit <sha> [--no-build]' \
    '  cutover-go-production.sh takeover  --env-file <runtime.env> --image-tag <tag> --source-commit <sha> [--no-build]' \
    '      [--finalize-legacy-id <由外层已冻结并停止的容器 ID>]' \
    '' \
    'preflight 仅以同一镜像在回环端口和临时目录验收，不接触旧生产。' \
    'takeover 预检通过后才停止旧容器，并由独立 Go compose 项目接管配置端口。'
}

cutover_fail() {
  printf 'Go 生产切换失败：%s\n' "$1" >&2
  exit 1
}

cutover_parse_arguments() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --env-file)
        [[ "$#" -ge 2 ]] || cutover_fail "--env-file 缺少值"
        cutover_env_file="$2"
        shift 2
        ;;
      --image-tag)
        [[ "$#" -ge 2 ]] || cutover_fail "--image-tag 缺少值"
        cutover_image_tag="$2"
        shift 2
        ;;
      --source-commit)
        [[ "$#" -ge 2 ]] || cutover_fail "--source-commit 缺少值"
        cutover_source_commit="$2"
        shift 2
        ;;
      --finalize-legacy-id)
        [[ "$#" -ge 2 && -n "$2" ]] || cutover_fail "--finalize-legacy-id 缺少值"
        cutover_finalize_legacy_id="$2"
        shift 2
        ;;
      --no-build)
        cutover_build="false"
        shift
        ;;
      --help|-h)
        cutover_usage
        exit 0
        ;;
      *)
        cutover_fail "未知参数 $1"
        ;;
    esac
  done
}

cutover_file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
    return
  fi
  stat -c '%a' "$1"
}

cutover_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

cutover_marker_value() {
  local key="$1"

  awk -F= -v expected="$key" '
    $1 == expected {
      count++
      print substr($0, length(expected) + 2)
    }
    END {
      if (count != 1) {
        exit 1
      }
    }
  ' "$cutover_ready_marker"
}

cutover_verify_migration_ready() {
  local format
  local marker_image_tag
  local database_sha256
  local report_name
  local report_sha256
  local created_at_utc
  local manifest_name
  local manifest_sha256
  local manifest
  local database="${GO_PROD_DB_DIR}/art-ai.db"
  local report

  [[ -f "$cutover_ready_marker" && ! -L "$cutover_ready_marker" ]] ||
    cutover_fail "迁移就绪证明不存在或不是普通文件"
  if [[ "$(cutover_file_mode "$cutover_ready_marker")" != "600" ]]; then
    cutover_fail "迁移就绪证明权限必须为 0600"
  fi
  if ! awk -F= '
    BEGIN {
      allowed["format"] = 1
      allowed["image_tag"] = 1
      allowed["database_sha256"] = 1
      allowed["report_name"] = 1
      allowed["report_sha256"] = 1
      allowed["created_at_utc"] = 1
      allowed["manifest_name"] = 1
      allowed["manifest_sha256"] = 1
    }
    !allowed[$1] || seen[$1]++ { exit 1 }
    END {
      if (NR != 8) {
        exit 1
      }
      for (key in allowed) {
        if (seen[key] != 1) {
          exit 1
        }
      }
    }
  ' "$cutover_ready_marker"; then
    cutover_fail "迁移就绪证明格式无效"
  fi

  format="$(cutover_marker_value format)" ||
    cutover_fail "迁移就绪证明缺少 format"
  marker_image_tag="$(cutover_marker_value image_tag)" ||
    cutover_fail "迁移就绪证明缺少 image_tag"
  database_sha256="$(cutover_marker_value database_sha256)" ||
    cutover_fail "迁移就绪证明缺少 database_sha256"
  report_name="$(cutover_marker_value report_name)" ||
    cutover_fail "迁移就绪证明缺少 report_name"
  report_sha256="$(cutover_marker_value report_sha256)" ||
    cutover_fail "迁移就绪证明缺少 report_sha256"
  created_at_utc="$(cutover_marker_value created_at_utc)" ||
    cutover_fail "迁移就绪证明缺少 created_at_utc"

  manifest_name="$(cutover_marker_value manifest_name)" ||
    cutover_fail "迁移就绪证明缺少 manifest_name"
  manifest_sha256="$(cutover_marker_value manifest_sha256)" ||
    cutover_fail "迁移就绪证明缺少 manifest_sha256"
  [[ "$manifest_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    cutover_fail "迁移就绪证明中的 manifest SHA-256 无效"
  [[ "$manifest_name" == "${report_name}.manifest.json" ]] ||
    cutover_fail "迁移就绪证明中的 manifest 名不匹配"
  [[ "$format" == "art-ai-go-migration-ready-v1" ]] ||
    cutover_fail "迁移就绪证明版本不支持"
  [[ "$marker_image_tag" == "$cutover_image_tag" ]] ||
    cutover_fail "迁移就绪证明与待部署镜像版本不一致"
  [[ "$database_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    cutover_fail "迁移就绪证明中的数据库 SHA-256 无效"
  [[ "$report_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    cutover_fail "迁移就绪证明中的报告 SHA-256 无效"
  if [[ ! "$report_name" =~ ^[A-Za-z0-9._-]+$ ||
    "$report_name" == "." ||
    "$report_name" == ".." ]]; then
    cutover_fail "迁移就绪证明中的报告名不安全"
  fi
  [[ "$created_at_utc" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] ||
    cutover_fail "迁移就绪证明中的时间无效"

  report="${cutover_prod_root}/reports/${report_name}"
  [[ -f "$database" && ! -L "$database" ]] ||
    cutover_fail "已迁移 Go 数据库不存在或不是普通文件"
  [[ -f "$report" && ! -L "$report" ]] ||
    cutover_fail "已迁移对账报告不存在或不是普通文件"
  [[ "$(cutover_sha256 "$database")" == "$database_sha256" ]] ||
    cutover_fail "已迁移 Go 数据库与就绪证明不一致"
  [[ "$(cutover_sha256 "$report")" == "$report_sha256" ]] ||
    cutover_fail "已迁移对账报告与就绪证明不一致"
  manifest="${cutover_prod_root}/reports/${manifest_name}"
  [[ -f "$manifest" && ! -L "$manifest" ]] ||
    cutover_fail "已迁移 completeness manifest 不存在或不是普通文件"
  [[ "$(cutover_sha256 "$manifest")" == "$manifest_sha256" ]] ||
    cutover_fail "已迁移 completeness manifest 与就绪证明不一致"
  jq -e -f "${cutover_root}/scripts/etl-completeness-filter.jq" "$manifest" >/dev/null ||
    cutover_fail "已迁移 completeness manifest 未通过"
  if ! awk '
    $0 == "对账结论: 通过" { green=1 }
    index($0, "对账失败:") == 1 { bad=1 }
    END { exit !(green && !bad) }
  ' "$report"; then
    cutover_fail "已迁移对账报告未通过"
  fi
}

cutover_cleanup_preflight() {
  if declare -p cutover_preflight_compose >/dev/null 2>&1; then
    if docker container inspect \
      "${cutover_preflight_project}-art-ai-go-1" >/dev/null 2>&1; then
      "${cutover_preflight_compose[@]}" \
        stop --timeout 10 art-ai-go >/dev/null 2>&1 || true
      "${cutover_preflight_compose[@]}" \
        rm -f art-ai-go >/dev/null 2>&1 || true
    fi
    docker network rm \
      "${cutover_preflight_project}_default" >/dev/null 2>&1 || true
  fi
  if [[ -n "$cutover_preflight_root" &&
    -d "$cutover_preflight_root" &&
    "$(basename -- "$cutover_preflight_root")" == \
      meshyai-go-prod-preflight.* ]]; then
    find "$cutover_preflight_root" \
      -depth -mindepth 1 -delete >/dev/null 2>&1 || true
    rmdir "$cutover_preflight_root" >/dev/null 2>&1 || true
    cutover_preflight_root=""
  fi
}

cutover_restore_legacy() {
  local cutover_exit_status=$?

  cutover_cleanup_preflight
  # finalize owns directory restoration and the original container lifecycle.
  # Its child must never rebind a name or start legacy before old data returns.
  if [[ -n "$cutover_finalize_legacy_id" ]]; then
    return "$cutover_exit_status"
  fi
  if [[ "$cutover_exit_status" -ne 0 &&
    "$cutover_stopped_legacy" == "true" ]]; then
    printf 'Go 接管失败，恢复旧生产容器 %s\n' \
      "$cutover_legacy_container" >&2
    if ! "${cutover_compose[@]}" \
      stop --timeout 10 art-ai-go >/dev/null 2>&1; then
      printf '回滚未完成：无法停止 Go 服务，拒绝启动旧容器\n' >&2
      return 1
    fi
    docker start "$cutover_legacy_id" >/dev/null || {
      printf '回滚未完成：旧容器恢复启动失败\n' >&2
      return 1
    }
  fi
  return "$cutover_exit_status"
}

cutover_validate_legacy_container() {
  local project_label
  local service_label
  local reference="${1:-$cutover_legacy_container}"

  if ! docker container inspect \
    "$reference" >/dev/null 2>&1; then
    return 1
  fi
  project_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.project={{ index .Config.Labels "com.docker.compose.project" }}' \
      "$reference"
  )"
  service_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.service={{ index .Config.Labels "com.docker.compose.service" }}' \
      "$reference"
  )"
  if [[ "$project_label" != \
    "com.docker.compose.project=${cutover_legacy_project}" ||
    "$service_label" != \
      "com.docker.compose.service=${cutover_legacy_service}" ]]; then
    cutover_fail \
      "旧容器标签异常，拒绝停止：$project_label $service_label"
  fi
  return 0
}

cutover_legacy_container_id() {
  docker container inspect \
    --format '{{ .Id }}' \
    "$cutover_legacy_container"
}

cutover_health_url() {
  local host="${GO_WEB_BIND_IP}"
  case "$host" in
    0.0.0.0) host="127.0.0.1" ;;
    ::|\[::\]) host="[::1]" ;;
    *:*) [[ "$host" == \[*\] ]] || host="[$host]" ;;
  esac
  printf 'http://%s:%s/readyz\n' "$host" "$GO_WEB_HOST_PORT"
}

cutover_copy_preflight_sample() {
  local source="$1"
  local target="$2"
  local sample

  sample="$(find "$source" -type f -print -quit)"
  if [[ -n "$sample" ]]; then
    mkdir -p "$target"
    cp -- "$sample" "$target/"
  fi
}

cutover_prepare_directories() {
  export GO_PROD_DB_DIR="${GO_PROD_DB_DIR:-${cutover_prod_root}/data}"
  export GO_PROD_STORAGE_DIR="${GO_PROD_STORAGE_DIR:-${cutover_prod_root}/storage-go}"
  export GO_PROD_WEB_DATA_DIR="${GO_PROD_WEB_DATA_DIR:-${cutover_prod_root}/web-data}"
  export GO_PROD_LOG_DIR="${GO_PROD_LOG_DIR:-${cutover_prod_root}/logs}"
  local legacy
  for legacy in GO_LEGACY_GENERATED_IMAGES_DIR GO_LEGACY_ASSET_CACHE_DIR \
    GO_LEGACY_THUMBNAIL_CACHE_DIR GO_LEGACY_INPUT_ASSETS_DIR; do
    [[ -n "${!legacy:-}" ]] || cutover_fail "${legacy} 未设置（由环境文件 layout 给出）"
    export "${legacy?}"
  done

  mkdir -p \
    "$GO_PROD_DB_DIR" \
    "$GO_PROD_STORAGE_DIR" \
    "$GO_PROD_WEB_DATA_DIR"
  for directory in \
    "$GO_LEGACY_GENERATED_IMAGES_DIR" \
    "$GO_LEGACY_ASSET_CACHE_DIR" \
    "$GO_LEGACY_THUMBNAIL_CACHE_DIR" \
    "$GO_LEGACY_INPUT_ASSETS_DIR"; do
    [[ -d "$directory" && ! -L "$directory" ]] ||
      cutover_fail "旧资产目录不存在、不是目录或是符号链接：$directory"
  done
  if [[ "$(id -u)" == "0" ]]; then
    chown -R 100:101 \
      "$GO_PROD_DB_DIR" \
      "$GO_PROD_STORAGE_DIR" \
      "$GO_PROD_WEB_DATA_DIR"
  fi
}

cutover_preflight() {
  cutover_cleanup_preflight
  cutover_preflight_root="$(mktemp -d \
    "${TMPDIR:-/tmp}/meshyai-go-prod-preflight.XXXXXX")"
  mkdir -p \
    "${cutover_preflight_root}/data" \
    "${cutover_preflight_root}/storage-go" \
    "${cutover_preflight_root}/web-data" \
    "${cutover_preflight_root}/logs" \
    "${cutover_preflight_root}/legacy/generated" \
    "${cutover_preflight_root}/legacy/cache" \
    "${cutover_preflight_root}/legacy/thumbnails" \
    "${cutover_preflight_root}/legacy/assets"
  cutover_copy_preflight_sample \
    "$GO_LEGACY_GENERATED_IMAGES_DIR" \
    "${cutover_preflight_root}/legacy/generated"
  cutover_copy_preflight_sample \
    "$GO_LEGACY_ASSET_CACHE_DIR" \
    "${cutover_preflight_root}/legacy/cache"
  cutover_copy_preflight_sample \
    "$GO_LEGACY_THUMBNAIL_CACHE_DIR" \
    "${cutover_preflight_root}/legacy/thumbnails"
  cutover_copy_preflight_sample \
    "$GO_LEGACY_INPUT_ASSETS_DIR" \
    "${cutover_preflight_root}/legacy/assets"
  if [[ "$(id -u)" == "0" ]]; then
    chown -R 100:101 "$cutover_preflight_root"
  fi

  GO_PROD_DB_DIR="${cutover_preflight_root}/data" \
    GO_PROD_STORAGE_DIR="${cutover_preflight_root}/storage-go" \
    GO_PROD_WEB_DATA_DIR="${cutover_preflight_root}/web-data" \
    GO_PROD_LOG_DIR="${cutover_preflight_root}/logs" \
    GO_LEGACY_GENERATED_IMAGES_DIR="${cutover_preflight_root}/legacy/generated" \
    GO_LEGACY_ASSET_CACHE_DIR="${cutover_preflight_root}/legacy/cache" \
    GO_LEGACY_THUMBNAIL_CACHE_DIR="${cutover_preflight_root}/legacy/thumbnails" \
    GO_LEGACY_INPUT_ASSETS_DIR="${cutover_preflight_root}/legacy/assets" \
    GO_WEB_BIND_IP=127.0.0.1 \
    GO_WEB_HOST_PORT="$cutover_preflight_port" \
    "${cutover_preflight_compose[@]}" \
    up -d --no-build --wait \
    --wait-timeout "$cutover_wait_timeout" art-ai-go
  curl --fail --silent --show-error --max-time 10 \
    "http://127.0.0.1:${cutover_preflight_port}/readyz" >/dev/null
  curl --fail --silent --show-error --max-time 15 \
    "http://127.0.0.1:${cutover_preflight_port}/" >/dev/null
  cutover_cleanup_preflight
  printf '同镜像 Go 生产预检通过\n'
}

cutover_parse_arguments "$@"
[[ "$cutover_prod_root" == /* ]] ||
  cutover_fail "GO_PROD_ROOT 必须是发布根下 production 目录的绝对路径"
[[ "$cutover_project" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] ||
  cutover_fail "GO_PROD_PROJECT 必须给出本栈 Compose 项目名"
if [[ "$cutover_mode" == "takeover" ]]; then
  for cutover_legacy_name in "$cutover_legacy_container" "$cutover_legacy_project" "$cutover_legacy_service"; do
    [[ "$cutover_legacy_name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] ||
      cutover_fail "takeover 必须给出 CUTOVER_LEGACY_CONTAINER、CUTOVER_LEGACY_PROJECT 与 CUTOVER_LEGACY_SERVICE"
  done
fi
case "$cutover_mode" in
  preflight|takeover)
    ;;
  *)
    cutover_usage >&2
    exit 2
    ;;
esac
if [[ -n "$cutover_finalize_legacy_id" ]]; then
  [[ "$cutover_mode" == "takeover" && "$cutover_build" == "false" ]] ||
    cutover_fail "--finalize-legacy-id 仅适用于外层 finalize 的 --no-build takeover"
fi
[[ -n "$cutover_env_file" ]] ||
  cutover_fail "必须指定 --env-file"
[[ -n "$cutover_image_tag" ]] ||
  cutover_fail "必须指定 --image-tag"
[[ "$cutover_source_commit" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] ||
  cutover_fail "必须指定完整且合法的 --source-commit"
[[ -f "$cutover_env_file" ]] ||
  cutover_fail "Go 生产环境文件不存在"
if [[ "$(cutover_file_mode "$cutover_env_file")" != "600" ]]; then
  cutover_fail "Go 生产环境文件权限必须为 0600"
fi

cutover_env_file="$(
  cd -- "$(dirname -- "$cutover_env_file")"
  printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$cutover_env_file")"
)"
export GO_RUNTIME_ENV_FILE="$cutover_env_file"
export GO_IMAGE_TAG="$cutover_image_tag"
export GO_SOURCE_COMMIT="$cutover_source_commit"
export GO_WEB_BIND_IP="${GO_WEB_BIND_IP:-0.0.0.0}"
export GO_WEB_HOST_PORT="${GO_WEB_HOST_PORT:-13333}"

cutover_prepare_directories
cutover_compose=(
  docker compose
  --env-file "$cutover_env_file"
  -p "$cutover_project"
  -f "$cutover_compose_file"
)
cutover_preflight_compose=(
  docker compose
  --env-file "$cutover_env_file"
  -p "$cutover_preflight_project"
  -f "$cutover_compose_file"
)
trap 'cutover_restore_legacy' EXIT

"${cutover_compose[@]}" config --quiet
cutover_images="$("${cutover_compose[@]}" config --images)"
if [[ "$(
  printf '%s\n' "$cutover_images" |
    awk 'NF { count++ } END { print count+0 }'
)" != "1" ]]; then
  cutover_fail "生产拓扑必须解析为唯一镜像"
fi
cutover_image="$(
  printf '%s\n' "$cutover_images" |
    awk 'NF { print; exit }'
)"
if [[ "$cutover_build" == "true" ]]; then
  "${cutover_compose[@]}" build art-ai-go
fi
cutover_image_identity="$(
  docker image inspect \
    --format \
    '{{ index .Config.Labels "org.opencontainers.image.version" }}|{{ index .Config.Labels "org.opencontainers.image.revision" }}' \
    "$cutover_image"
)"
cutover_image_version="${cutover_image_identity%%|*}"
cutover_image_revision="${cutover_image_identity#*|}"
if [[ "$cutover_image_version" != "$cutover_image_tag" ]]; then
  cutover_fail \
    "镜像版本标签不一致：期望 $cutover_image_tag，实际 $cutover_image_version"
fi
if [[ "$cutover_image_revision" != "$cutover_source_commit" ]]; then
  cutover_fail \
    "镜像源码提交不一致：期望 $cutover_source_commit，实际 $cutover_image_revision"
fi

cutover_preflight
if [[ "$cutover_mode" == "preflight" ]]; then
  printf 'Go 生产预检完成，旧生产未变更\n'
  exit 0
fi

cutover_verify_migration_ready

if [[ -n "$cutover_finalize_legacy_id" ]]; then
  [[ "$(cutover_legacy_container_id)" == "$cutover_finalize_legacy_id" ]] ||
    cutover_fail "旧容器名字不再对应 finalize 冻结的 ID，拒绝接管"
  cutover_validate_legacy_container "$cutover_finalize_legacy_id" ||
    cutover_fail "finalize 冻结的旧容器不存在或无法验证"
  [[ "$(docker container inspect --format '{{ .State.Running }}' "$cutover_finalize_legacy_id")" == "false" ]] ||
    cutover_fail "finalize 冻结的旧容器不再保持停止，拒绝接管"
  cutover_legacy_id="$cutover_finalize_legacy_id"
  [[ "$(cutover_legacy_container_id)" == "$cutover_legacy_id" ]] ||
    cutover_fail "旧容器在接管前发生替换，拒绝接管"
else
  cutover_validate_legacy_container ||
    cutover_fail "旧生产容器不存在或无法验证，拒绝接管"
  cutover_legacy_id="$(cutover_legacy_container_id)"
  if [[ "$(
    docker container inspect \
      --format '{{ .State.Running }}' \
      "$cutover_legacy_container"
  )" == "true" ]]; then
    cutover_validate_legacy_container
    [[ "$(cutover_legacy_container_id)" == "$cutover_legacy_id" ]] ||
      cutover_fail "旧生产容器在停写前发生替换，拒绝接管"
    docker stop "$cutover_legacy_id" >/dev/null
    cutover_stopped_legacy="true"
  fi
fi
cutover_production_health_url="$(cutover_health_url)"

"${cutover_compose[@]}" \
  up -d --no-build --wait \
  --wait-timeout "$cutover_wait_timeout" art-ai-go
curl --fail --silent --show-error --max-time 10 \
  "$cutover_production_health_url" >/dev/null

cutover_stopped_legacy="false"
printf 'Go 生产栈已接管 %s:%s，旧容器保持停止且未删除\n' \
  "$GO_WEB_BIND_IP" "$GO_WEB_HOST_PORT"
