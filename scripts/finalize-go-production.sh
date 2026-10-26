#!/usr/bin/env bash
set -Eeuo pipefail

finalize_repo_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"
finalize_backup_script="${GO_BACKUP_SCRIPT:-${finalize_repo_root}/scripts/backup-go-production.sh}"
finalize_etl_script="${GO_ETL_SCRIPT:-${finalize_repo_root}/scripts/run-go-etl-container.sh}"
finalize_cutover_script="${GO_CUTOVER_SCRIPT:-${finalize_repo_root}/scripts/cutover-go-production.sh}"
finalize_acceptance_script="${GO_ACCEPTANCE_SCRIPT:-${finalize_repo_root}/scripts/post-cutover-acceptance.sh}"
finalize_compose_file="${finalize_repo_root}/docker-compose.go-production.yml"
finalize_expected_hostname=""
finalize_expected_docker_id=""
finalize_source_baseline=""
finalize_go_bind_ip=""
finalize_go_host_port=""
finalize_preflight_port=""
finalize_env_file=""
finalize_image_tag=""
finalize_source_commit=""
finalize_approved_acknowledgement=""
finalize_legacy_root=""
finalize_legacy_database=""
finalize_legacy_assets=""
finalize_legacy_cache=""
finalize_legacy_audit=""
finalize_legacy_container=""
finalize_legacy_project=""
finalize_legacy_service=""
finalize_go_root=""
finalize_production_root=""
finalize_run_id="${FINALIZE_RUN_ID:-$(date -u '+%Y%m%dT%H%M%SZ')}"
finalize_image_name="art-ai-go-production"
finalize_project=""
finalize_acceptance_base_url=""
finalize_acceptance_baseline=""
finalize_minimum_free_kib=""
finalize_backup_root=""
finalize_staging_root=""
finalize_archive_root=""
finalize_legacy_stopped="false"
finalize_legacy_already_stopped="false"
finalize_promoted="false"
finalize_archived_items=()
finalize_promoted_items=()
finalize_committed="false"

finalize_usage() {
  printf '%s\n' \
    '用法:' \
    '  finalize-go-production.sh --env-file <runtime.env> --image-tag <tag>' \
    '      --source-commit <main 的完整提交 SHA>' \
    '      --expected-hostname <现场主机名> --expected-docker-id <Docker daemon ID>' \
    '      --source-baseline <权限 0600 的来源快照基线 JSON>' \
    '      --go-bind-ip <IPv4/IPv6> --go-host-port <端口> --preflight-port <回环端口>' \
    '      --approved-source-loss-acknowledgement <已批准清单>' \
    '      --legacy-root <旧项目根> --legacy-container <旧容器全名>' \
    '      --legacy-database <prod.db> --legacy-assets <上传目录>' \
    '      --legacy-cache <任务缓存目录> --legacy-audit <审计附件目录>' \
    '      --legacy-compose-project <旧 Compose 项目>' \
    '      --legacy-compose-service <旧 Compose Service>' \
    '      [--legacy-already-stopped]' \
    '      --go-root <Go 数据根> --go-project <Go Compose 项目>' \
    '      --acceptance-base-url <正式入口 URL>' \
    '      --acceptance-baseline <权限 0600 的正式基线 JSON>' \
    '      --minimum-free-kib <事务开始前最小可用 KiB>' \
    '' \
    '事务顺序：同镜像预检 → 停止旧写入 → 最终备份 → 严格 ETL →' \
    '问题集精确重绑定 → 迁移数据提升为生产 → Go 接管 → 线上验收。' \
    '验收成功前任一步失败都会回滚；事务亲自停止的旧容器才会被恢复启动。' \
    '已替换目录只移动归档，不删除。'
}

finalize_fail() {
  printf 'Go 最终迁移与切换失败：%s\n' "$1" >&2
  exit 1
}

finalize_file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
    return
  fi
  stat -c '%a' "$1"
}

finalize_file_owner() {
  if stat -f '%u' "$1" >/dev/null 2>&1; then
    stat -f '%u' "$1"
    return
  fi
  stat -c '%u' "$1"
}

finalize_canonical_directory() {
  (
    cd -- "$1"
    pwd -P
  )
}

finalize_canonical_file() {
  (
    cd -- "$(dirname -- "$1")"
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$1")"
  )
}

finalize_validate_http_origin() {
  local name="$1"
  local value="$2"
  local authority
  local host
  local port

  [[ "$value" == http://* || "$value" == https://* ]] ||
    finalize_fail "$name 必须使用 http:// 或 https://"
  [[ "$value" != *['@?#']* ]] ||
    finalize_fail "$name 不得包含 userinfo、query 或 fragment"
  authority="${value#http://}"
  authority="${authority#https://}"
  [[ -n "$authority" && "$authority" != *"/"* ]] ||
    finalize_fail "$name 必须是无路径的 HTTP(S) origin"
  host="${authority%%:*}"
  port=""
  if [[ "$authority" == *:* ]]; then
    port="${authority##*:}"
    [[ -n "$port" ]] || finalize_fail "$name 端口无效"
    [[ "$authority" != *:*:* ]] ||
      finalize_fail "$name 暂不支持 IPv6 地址"
  fi
  [[ "$host" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ &&
    "$host" != *..* ]] ||
    finalize_fail "$name 主机名无效"
  if [[ -n "$port" ]]; then
    [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] ||
      finalize_fail "$name 端口无效"
    ((port <= 65535)) || finalize_fail "$name 端口超出范围"
  fi
}

finalize_validate_baseline() {
  jq -e '
    def count: type == "number" and . >= 0 and floor == .;
    (.users | count) and
    (.generations | count) and
    (.usage_tasks | count) and
    (.usage_credits | count) and
    (.audit_minimum | count) and
    (.output_assets | count) and
    (.input_assets | count) and
    (.chat_sessions | count) and
    (.chat_messages | count) and
    (.canvas_projects | count) and
    (.canvas_nodes | count) and
    (.external_webhook_url | type == "string")
  ' "$finalize_acceptance_baseline" >/dev/null ||
    finalize_fail "正式验收基线格式无效"
  finalize_external_webhook_url="$(
    jq -er '.external_webhook_url' "$finalize_acceptance_baseline"
  )"
  finalize_validate_http_origin \
    "正式验收基线 external_webhook_url" \
    "$finalize_external_webhook_url"
}

finalize_paths_overlap() {
  [[ "$1" == "$2" || "$1" == "$2/"* || "$2" == "$1/"* ]]
}

finalize_parse_arguments() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --expected-hostname)
        [[ "$#" -ge 2 ]] || finalize_fail "--expected-hostname 缺少值"
        finalize_expected_hostname="$2"
        shift 2
        ;;
      --expected-docker-id)
        [[ "$#" -ge 2 ]] || finalize_fail "--expected-docker-id 缺少值"
        finalize_expected_docker_id="$2"
        shift 2
        ;;
      --source-baseline)
        [[ "$#" -ge 2 ]] || finalize_fail "--source-baseline 缺少值"
        finalize_source_baseline="$2"
        shift 2
        ;;
      --go-bind-ip)
        [[ "$#" -ge 2 ]] || finalize_fail "--go-bind-ip 缺少值"
        finalize_go_bind_ip="$2"
        shift 2
        ;;
      --go-host-port)
        [[ "$#" -ge 2 ]] || finalize_fail "--go-host-port 缺少值"
        finalize_go_host_port="$2"
        shift 2
        ;;
      --preflight-port)
        [[ "$#" -ge 2 ]] || finalize_fail "--preflight-port 缺少值"
        finalize_preflight_port="$2"
        shift 2
        ;;
      --env-file)
        [[ "$#" -ge 2 ]] || finalize_fail "--env-file 缺少值"
        finalize_env_file="$2"
        shift 2
        ;;
      --image-tag)
        [[ "$#" -ge 2 ]] || finalize_fail "--image-tag 缺少值"
        finalize_image_tag="$2"
        shift 2
        ;;
      --source-commit)
        [[ "$#" -ge 2 ]] || finalize_fail "--source-commit 缺少值"
        finalize_source_commit="$2"
        shift 2
        ;;
      --approved-source-loss-acknowledgement)
        [[ "$#" -ge 2 ]] ||
          finalize_fail \
            "--approved-source-loss-acknowledgement 缺少值"
        finalize_approved_acknowledgement="$2"
        shift 2
        ;;
      --legacy-root)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-root 缺少值"
        finalize_legacy_root="$2"
        shift 2
        ;;
      --legacy-container)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-container 缺少值"
        finalize_legacy_container="$2"
        shift 2
        ;;
      --legacy-database)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-database 缺少值"
        finalize_legacy_database="$2"
        shift 2
        ;;
      --legacy-assets)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-assets 缺少值"
        finalize_legacy_assets="$2"
        shift 2
        ;;
      --legacy-cache)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-cache 缺少值"
        finalize_legacy_cache="$2"
        shift 2
        ;;
      --legacy-audit)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-audit 缺少值"
        finalize_legacy_audit="$2"
        shift 2
        ;;
      --legacy-compose-project)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-compose-project 缺少值"
        finalize_legacy_project="$2"
        shift 2
        ;;
      --legacy-compose-service)
        [[ "$#" -ge 2 ]] || finalize_fail "--legacy-compose-service 缺少值"
        finalize_legacy_service="$2"
        shift 2
        ;;
      --legacy-already-stopped)
        finalize_legacy_already_stopped="true"
        shift
        ;;
      --go-root)
        [[ "$#" -ge 2 ]] || finalize_fail "--go-root 缺少值"
        finalize_go_root="$2"
        shift 2
        ;;
      --go-project)
        [[ "$#" -ge 2 ]] || finalize_fail "--go-project 缺少值"
        finalize_project="$2"
        shift 2
        ;;
      --acceptance-base-url)
        [[ "$#" -ge 2 ]] || finalize_fail "--acceptance-base-url 缺少值"
        finalize_acceptance_base_url="${2%/}"
        shift 2
        ;;
      --acceptance-baseline)
        [[ "$#" -ge 2 ]] || finalize_fail "--acceptance-baseline 缺少值"
        finalize_acceptance_baseline="$2"
        shift 2
        ;;
      --minimum-free-kib)
        [[ "$#" -ge 2 ]] || finalize_fail "--minimum-free-kib 缺少值"
        finalize_minimum_free_kib="$2"
        shift 2
        ;;
      --help|-h)
        finalize_usage
        exit 0
        ;;
      *)
        finalize_fail "未知参数 $1"
        ;;
    esac
  done
}

finalize_validate_host() {
  [[ "$(hostname)" == "$finalize_expected_hostname" ]] || finalize_fail "执行主机身份不匹配"
  [[ "$(docker info --format '{{ .ID }}')" == "$finalize_expected_docker_id" ]] ||
    finalize_fail "Docker daemon 身份不匹配"
}

finalize_validate_legacy_container() {
  local project_label
  local service_label
  local running
  local actual_name

  docker container inspect "$finalize_legacy_container" >/dev/null 2>&1 ||
    finalize_fail "旧生产容器不存在"
  actual_name="$(docker container inspect --format '{{ .Name }}' "$finalize_legacy_container")"
  [[ "$actual_name" == "/${finalize_legacy_container}" ]] ||
    finalize_fail "旧容器全名不匹配，拒绝停写"
  project_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.project={{ index .Config.Labels "com.docker.compose.project" }}' \
      "$finalize_legacy_container"
  )"
  service_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.service={{ index .Config.Labels "com.docker.compose.service" }}' \
      "$finalize_legacy_container"
  )"
  [[ "$project_label" == "com.docker.compose.project=${finalize_legacy_project}" &&
    "$service_label" == "com.docker.compose.service=${finalize_legacy_service}" ]] ||
    finalize_fail \
      "旧容器标签异常，拒绝停写：$project_label $service_label"
  running="$(
    docker container inspect \
      --format '{{ .State.Running }}' \
      "$finalize_legacy_container"
  )"
  if [[ "$finalize_legacy_already_stopped" == "true" ]]; then
    [[ "$running" == "false" ]] ||
      finalize_fail "已声明旧容器停写，但旧生产容器仍在运行"
    return
  fi
  [[ "$running" == "true" ]] ||
    finalize_fail \
      "旧生产容器未运行；确认已停写后须显式传入 --legacy-already-stopped"
}

finalize_legacy_container_id() {
  docker container inspect \
    --format '{{ .Id }}' \
    "$finalize_legacy_container"
}

finalize_move_if_present() {
  local source="$1"
  local destination="$2"

  if [[ -e "$source" || -L "$source" ]]; then
    mv -- "$source" "$destination"
  fi
}

finalize_restore_after_failure() {
  local exit_status=$?
  local item
  local previous="${finalize_archive_root}/previous"
  local failed_new="${finalize_archive_root}/failed-new"
  local restore_failed="false"

  if [[ "$finalize_committed" == "true" ]]; then
    return "$exit_status"
  fi
  set +e
  if [[ "$finalize_promoted" == "true" ]]; then
    if ! GO_RUNTIME_ENV_FILE="$finalize_env_file" \
      GO_IMAGE_TAG="$finalize_image_tag" \
      GO_PROD_DB_DIR="${finalize_production_root}/data" \
      GO_PROD_STORAGE_DIR="${finalize_production_root}/storage-go" \
      GO_PROD_WEB_DATA_DIR="${finalize_production_root}/web-data" \
      GO_PROD_LOG_DIR="${finalize_production_root}/logs" \
      GO_LEGACY_GENERATED_IMAGES_DIR="${finalize_legacy_root}/storage/generated" \
      GO_LEGACY_ASSET_CACHE_DIR="$finalize_legacy_cache" \
      GO_LEGACY_THUMBNAIL_CACHE_DIR="${finalize_legacy_root}/storage/thumbnails" \
      GO_LEGACY_INPUT_ASSETS_DIR="$finalize_legacy_assets" \
      docker compose \
      --env-file "$finalize_env_file" \
      -p "$finalize_project" \
      -f "$finalize_compose_file" \
      stop --timeout 10 art-ai-go >/dev/null 2>&1; then
      printf '回滚未完成：无法停止 Go 服务，保留当前数据和 previous 归档，拒绝启动旧服务\n' >&2
      return 1
    fi
    mkdir -p "$failed_new" || return 1
    for item in ${finalize_promoted_items[@]+"${finalize_promoted_items[@]}"}; do
      finalize_move_if_present \
        "${finalize_production_root}/${item}" \
        "${failed_new}/${item}" || restore_failed="true"
    done
    for item in ${finalize_archived_items[@]+"${finalize_archived_items[@]}"}; do
      if [[ -e "${finalize_production_root}/${item}" || -L "${finalize_production_root}/${item}" ||
        ! -e "${previous}/${item}" ]]; then
        restore_failed="true"
        continue
      fi
      mv -- "${previous}/${item}" "${finalize_production_root}/${item}" || restore_failed="true"
    done
  fi
  if [[ "$restore_failed" == "true" ]]; then
    printf '回滚未完成：目录恢复失败，保留全部工件，拒绝启动旧服务\n' >&2
    return 1
  fi
  if [[ "$finalize_legacy_stopped" == "true" ]]; then
    printf '迁移或接管失败，恢复旧生产容器 %s\n' \
      "$finalize_legacy_container" >&2
    docker start "$finalize_legacy_id" >/dev/null || {
      printf '回滚未完成：旧生产容器恢复启动失败\n' >&2
      return 1
    }
  fi
  return "$exit_status"
}

# 受保护目录必须由执行环境显式给出（#334）：ART_AI_PROTECTED_PATHS 由
# scripts/release-env.py compose-env <环境> 按本地环境文件给出（本栈根与 protected_paths）。
# finalize 调用的备份与 ETL 同样要求；未设置时在停旧容器前拒绝；该主机确无额外受保护目录时显式设为空串。
finalize_require_protected_paths() {
  local entry
  [[ -n "${ART_AI_PROTECTED_PATHS+set}" ]] ||
    finalize_fail "未设置 ART_AI_PROTECTED_PATHS：先用 scripts/release-env.py compose-env <环境> 取得本机受保护目录（确无时显式设为空串）"
  while IFS= read -r entry; do
    [[ -z "$entry" || "$entry" == /* ]] ||
      finalize_fail "ART_AI_PROTECTED_PATHS 只能列绝对路径"
  done < <(printf '%s\n' "$ART_AI_PROTECTED_PATHS" | tr ':' '\n')
}

finalize_parse_arguments "$@"
finalize_require_protected_paths
[[ -n "$finalize_env_file" ]] || finalize_fail "必须指定 --env-file"
[[ -n "$finalize_image_tag" ]] || finalize_fail "必须指定 --image-tag"
[[ "$finalize_source_commit" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] ||
  finalize_fail "必须指定 main 的完整 --source-commit"
[[ -n "$finalize_approved_acknowledgement" ]] ||
  finalize_fail "必须指定已批准的源损失确认清单"
[[ -n "$finalize_legacy_root" ]] || finalize_fail "必须指定 --legacy-root"
[[ -n "$finalize_legacy_database" ]] || finalize_fail "必须指定 --legacy-database"
[[ -n "$finalize_legacy_assets" ]] || finalize_fail "必须指定 --legacy-assets"
[[ -n "$finalize_legacy_cache" ]] || finalize_fail "必须指定 --legacy-cache"
[[ -n "$finalize_legacy_audit" ]] || finalize_fail "必须指定 --legacy-audit"
[[ -n "$finalize_legacy_container" ]] || finalize_fail "必须指定 --legacy-container"
[[ -n "$finalize_legacy_project" ]] || finalize_fail "必须指定 --legacy-compose-project"
[[ -n "$finalize_legacy_service" ]] || finalize_fail "必须指定 --legacy-compose-service"
[[ -n "$finalize_go_root" ]] || finalize_fail "必须指定 --go-root"
[[ -n "$finalize_project" ]] || finalize_fail "必须指定 --go-project"
finalize_validate_http_origin \
  "--acceptance-base-url" \
  "$finalize_acceptance_base_url"
[[ -n "$finalize_acceptance_baseline" ]] ||
  finalize_fail "必须指定 --acceptance-baseline"
[[ "$finalize_minimum_free_kib" =~ ^[1-9][0-9]*$ ]] ||
  finalize_fail "必须指定正整数 --minimum-free-kib"
[[ "$finalize_expected_hostname" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] ||
  finalize_fail "必须指定合法 --expected-hostname"
[[ "$finalize_expected_docker_id" =~ ^[A-Za-z0-9][A-Za-z0-9:._-]*$ ]] ||
  finalize_fail "必须指定合法 --expected-docker-id"
[[ -n "$finalize_source_baseline" ]] || finalize_fail "必须指定 --source-baseline"
for finalize_port in "$finalize_go_host_port" "$finalize_preflight_port"; do
  [[ "$finalize_port" =~ ^[1-9][0-9]{0,4}$ ]] && ((finalize_port <= 65535)) ||
    finalize_fail "必须显式指定 1..65535 范围的 Go/preflight 端口"
done
[[ "$finalize_go_host_port" != "$finalize_preflight_port" ]] ||
  finalize_fail "Go 端口与 preflight 端口不能相同"
for finalize_project_name in "$finalize_project" "$finalize_legacy_project"; do
  [[ "$finalize_project_name" =~ ^[a-z0-9][a-z0-9_-]*$ ]] ||
    finalize_fail "Compose 项目名称无效"
done
[[ "$finalize_project" != "$finalize_legacy_project" &&
  "${finalize_project}_preflight" != "$finalize_legacy_project" ]] ||
  finalize_fail "旧服务、Go 与 preflight Compose 项目必须互斥"
command -v python3 >/dev/null 2>&1 || finalize_fail "缺少必需命令 python3"
finalize_go_bind_ip="$(python3 - "$finalize_go_bind_ip" <<'PY_IP'
import ipaddress, sys
try:
    address = ipaddress.ip_address(sys.argv[1])
    print(f"[{address}]" if address.version == 6 else address)
except ValueError:
    sys.exit(1)
PY_IP
)" || finalize_fail "必须显式指定合法 --go-bind-ip"
finalize_validate_host
finalize_production_root="${finalize_go_root}/production"
[[ "$finalize_image_tag" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] ||
  finalize_fail "--image-tag 不是安全的镜像标签"
[[ "$finalize_run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] ||
  finalize_fail "FINALIZE_RUN_ID 不是安全 basename"
for executable in \
  "$finalize_backup_script" \
  "$finalize_etl_script" \
  "$finalize_cutover_script"; do
  [[ -x "$executable" ]] || finalize_fail "脚本不可执行：$executable"
done
command -v jq >/dev/null 2>&1 || finalize_fail "缺少必需命令 jq"
[[ -f "$finalize_acceptance_script" &&
  ! -L "$finalize_acceptance_script" ]] ||
  finalize_fail "线上验收脚本不存在或不是普通文件"
[[ -f "$finalize_env_file" && ! -L "$finalize_env_file" ]] ||
  finalize_fail "Go 生产环境文件不存在或不是普通文件"
[[ "$(finalize_file_mode "$finalize_env_file")" == "600" ]] ||
  finalize_fail "Go 生产环境文件权限必须为 0600"
[[ -f "$finalize_approved_acknowledgement" &&
  ! -L "$finalize_approved_acknowledgement" ]] ||
  finalize_fail "已批准源损失确认清单不存在或不是普通文件"
[[ "$(finalize_file_mode "$finalize_approved_acknowledgement")" == "600" ]] ||
  finalize_fail "已批准源损失确认清单权限必须为 0600"
[[ -f "$finalize_acceptance_baseline" &&
  ! -L "$finalize_acceptance_baseline" ]] ||
  finalize_fail "正式验收基线不存在或不是普通文件"
[[ "$(finalize_file_mode "$finalize_acceptance_baseline")" == "600" ]] ||
  finalize_fail "正式验收基线权限必须为 0600"
finalize_validate_baseline
if [[ "$(id -u)" == "0" &&
  "$(finalize_file_owner "$finalize_approved_acknowledgement")" != "100" ]]; then
  finalize_fail "已批准源损失确认清单必须由隔离 ETL 用户 UID 100 所有"
fi
[[ -d "$finalize_legacy_root" ]] || finalize_fail "旧项目根不存在"
[[ -f "$finalize_legacy_database" && ! -L "$finalize_legacy_database" ]] ||
  finalize_fail "旧生产数据库不存在、不是普通文件或是符号链接"
for finalize_legacy_directory in \
  "$finalize_legacy_assets" \
  "$finalize_legacy_cache" \
  "$finalize_legacy_audit" \
  "${finalize_legacy_root}/storage/generated" \
  "${finalize_legacy_root}/storage/thumbnails" \
  "${finalize_legacy_root}/storage/task-inputs"; do
  [[ -d "$finalize_legacy_directory" && ! -L "$finalize_legacy_directory" ]] ||
    finalize_fail \
      "旧生产资产目录不存在、不是目录或是符号链接：$finalize_legacy_directory"
done
python3 "${finalize_repo_root}/scripts/verify-cutover-source.py" \
  --database "$finalize_legacy_database" --baseline "$finalize_source_baseline" ||
  finalize_fail "旧生产来源基线不一致"
[[ -d "$finalize_go_root" ]] || mkdir -p "$finalize_go_root"
finalize_go_root="$(finalize_canonical_directory "$finalize_go_root")"
finalize_legacy_root="$(
  finalize_canonical_directory "$finalize_legacy_root"
)"
finalize_legacy_database="$(finalize_canonical_file "$finalize_legacy_database")"
finalize_legacy_assets="$(finalize_canonical_directory "$finalize_legacy_assets")"
finalize_legacy_cache="$(finalize_canonical_directory "$finalize_legacy_cache")"
finalize_legacy_audit="$(finalize_canonical_directory "$finalize_legacy_audit")"
for finalize_source_path in \
  "$finalize_legacy_database" \
  "$finalize_legacy_assets" \
  "$finalize_legacy_cache" \
  "$finalize_legacy_audit" \
  "${finalize_legacy_root}/storage/generated" \
  "${finalize_legacy_root}/storage/thumbnails" \
  "${finalize_legacy_root}/storage/task-inputs"; do
  if finalize_paths_overlap "$finalize_go_root" "$finalize_source_path"; then
    finalize_fail "旧生产源路径不能与 Go 数据根重叠：$finalize_source_path"
  fi
done
mkdir -p "$finalize_production_root"
finalize_production_root="$(
  finalize_canonical_directory "$finalize_production_root"
)"
[[ "$finalize_production_root" == "${finalize_go_root}/production" ]] ||
  finalize_fail "生产数据根必须固定为 Go 根目录下的 production"
finalize_available_kib="$(
  df -Pk "$finalize_go_root" | awk 'NR == 2 {print $4}'
)"
[[ "$finalize_available_kib" =~ ^[0-9]+$ ]] ||
  finalize_fail "无法读取 Go 根目录可用空间"
if ((finalize_available_kib < finalize_minimum_free_kib)); then
  finalize_fail \
    "Go 根目录可用空间不足：需要至少 ${finalize_minimum_free_kib} KiB，当前 ${finalize_available_kib} KiB"
fi
finalize_env_file="$(finalize_canonical_file "$finalize_env_file")"
finalize_approved_acknowledgement="$(
  finalize_canonical_file "$finalize_approved_acknowledgement"
)"
finalize_acceptance_baseline="$(
  finalize_canonical_file "$finalize_acceptance_baseline"
)"
finalize_backup_root="${finalize_go_root}/backups/final-cutover-${finalize_run_id}"
finalize_staging_root="${finalize_go_root}/cutover-staging/${finalize_run_id}"
finalize_archive_root="${finalize_go_root}/production-archives/${finalize_run_id}"
for target in \
  "$finalize_backup_root" \
  "$finalize_staging_root" \
  "$finalize_archive_root"; do
  [[ ! -e "$target" && ! -L "$target" ]] ||
    finalize_fail "事务目标已存在，拒绝覆盖：$target"
done

export GO_WEB_BIND_IP="$finalize_go_bind_ip"
export GO_WEB_HOST_PORT="$finalize_go_host_port"
export CUTOVER_PREFLIGHT_PORT="$finalize_preflight_port"
export GO_PREFLIGHT_PROJECT="${finalize_project}_preflight"
export GO_PROD_ROOT="$finalize_production_root"
export GO_PROD_DB_DIR="${finalize_production_root}/data"
export GO_PROD_STORAGE_DIR="${finalize_production_root}/storage-go"
export GO_PROD_WEB_DATA_DIR="${finalize_production_root}/web-data"
export GO_LEGACY_GENERATED_IMAGES_DIR="${finalize_legacy_root}/storage/generated"
export GO_LEGACY_ASSET_CACHE_DIR="$finalize_legacy_cache"
export GO_LEGACY_THUMBNAIL_CACHE_DIR="${finalize_legacy_root}/storage/thumbnails"
export GO_LEGACY_INPUT_ASSETS_DIR="$finalize_legacy_assets"
export GO_PROD_PROJECT="$finalize_project"
export CUTOVER_LEGACY_CONTAINER="$finalize_legacy_container"
export CUTOVER_LEGACY_PROJECT="$finalize_legacy_project"
export CUTOVER_LEGACY_SERVICE="$finalize_legacy_service"
trap 'finalize_restore_after_failure' EXIT

finalize_validate_legacy_container
finalize_legacy_id="$(finalize_legacy_container_id)"
"$finalize_cutover_script" \
  preflight \
  --env-file "$finalize_env_file" \
  --image-tag "$finalize_image_tag" \
  --source-commit "$finalize_source_commit" \
  --no-build

finalize_validate_host
python3 "${finalize_repo_root}/scripts/verify-cutover-source.py" \
  --database "$finalize_legacy_database" --baseline "$finalize_source_baseline" ||
  finalize_fail "停写前旧生产来源基线不一致"
finalize_validate_legacy_container
[[ "$(finalize_legacy_container_id)" == "$finalize_legacy_id" ]] ||
  finalize_fail "旧生产容器在停写前发生替换，拒绝迁移"

if [[ "$finalize_legacy_already_stopped" != "true" ]]; then
  docker stop "$finalize_legacy_id" >/dev/null
  finalize_legacy_stopped="true"
fi

"$finalize_backup_script" \
  create \
  --legacy-root "$finalize_legacy_root" \
  --legacy-database "$finalize_legacy_database" \
  --legacy-assets "$finalize_legacy_assets" \
  --legacy-cache "$finalize_legacy_cache" \
  --legacy-audit "$finalize_legacy_audit" \
  --destination "$finalize_backup_root"

python3 "${finalize_repo_root}/scripts/verify-cutover-source.py" \
  --database "${finalize_backup_root}/storage/data/prod.db" --baseline "$finalize_source_baseline" --snapshot ||
  finalize_fail "最终停写快照与来源基线不一致"

set +e
"$finalize_etl_script" \
  --backup-root "$finalize_backup_root" \
  --target-root "$finalize_staging_root" \
  --image-name "$finalize_image_name" \
  --image-tag "$finalize_image_tag" \
  --report-name migration-final-strict.txt \
  --source-loss-template-name final-source-loss-candidate.json
finalize_strict_status=$?
set -e

if [[ "$finalize_strict_status" -ne 0 ]]; then
  finalize_candidate="${finalize_staging_root}/reports/final-source-loss-candidate.json"
  finalize_final_ack="${finalize_staging_root}/reports/final-source-loss-approved.json"
  [[ -f "$finalize_candidate" && ! -L "$finalize_candidate" ]] ||
    finalize_fail "严格 ETL 失败且未生成可核对的源损失候选清单"

  docker run \
    --rm \
    --user 100:101 \
    --network none \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --tmpfs /tmp:size=16m,mode=1777 \
    --volume \
    "${finalize_approved_acknowledgement}:/approved-source-loss.json:ro" \
    --volume "${finalize_staging_root}/reports:/reports" \
    --entrypoint /usr/local/bin/art-ai-etl \
    "${finalize_image_name}:${finalize_image_tag}" \
    rebind-source-loss-acknowledgement \
    --approved /approved-source-loss.json \
    --candidate /reports/final-source-loss-candidate.json \
    --output /reports/final-source-loss-approved.json

  "$finalize_etl_script" \
    --backup-root "$finalize_backup_root" \
    --target-root "$finalize_staging_root" \
    --image-name "$finalize_image_name" \
    --image-tag "$finalize_image_tag" \
    --report-name migration-final-approved.txt \
    --source-loss-acknowledgement "$finalize_final_ack"
fi

[[ -f "${finalize_staging_root}/migration-ready.txt" ]] ||
  finalize_fail "最终 ETL 未生成迁移就绪证明"
mkdir -p "${finalize_staging_root}/web-data"
if [[ "$(id -u)" == "0" ]]; then
  chown -R 100:101 \
    "${finalize_staging_root}/data" \
    "${finalize_staging_root}/storage-go" \
    "${finalize_staging_root}/reports" \
    "${finalize_staging_root}/web-data" \
    "${finalize_staging_root}/migration-ready.txt"
fi

mkdir -p "${finalize_archive_root}/previous"
finalize_promoted="true"
for finalize_item in \
  data storage-go reports web-data migration-ready.txt; do
  if [[ -e "${finalize_production_root}/${finalize_item}" ||
    -L "${finalize_production_root}/${finalize_item}" ]]; then
    mv -- "${finalize_production_root}/${finalize_item}" \
      "${finalize_archive_root}/previous/${finalize_item}"
    finalize_archived_items+=("$finalize_item")
  fi
done
for finalize_item in \
  data storage-go reports web-data migration-ready.txt; do
  [[ -e "${finalize_staging_root}/${finalize_item}" ]] ||
    finalize_fail "待提升数据缺少 ${finalize_item}"
  mv -- \
    "${finalize_staging_root}/${finalize_item}" \
    "${finalize_production_root}/${finalize_item}"
  finalize_promoted_items+=("$finalize_item")
done

"$finalize_cutover_script" \
  takeover \
  --finalize-legacy-id "$finalize_legacy_id" \
  --env-file "$finalize_env_file" \
  --image-tag "$finalize_image_tag" \
  --source-commit "$finalize_source_commit" \
  --no-build

ACCEPTANCE_BASE_URL="$finalize_acceptance_base_url" \
  ACCEPTANCE_DATABASE="${finalize_production_root}/data/art-ai.db" \
  ACCEPTANCE_ENV="$finalize_env_file" \
  ACCEPTANCE_BASELINE_FILE="$finalize_acceptance_baseline" \
  /usr/bin/env bash "$finalize_acceptance_script"

finalize_record="${finalize_production_root}/reports/cutover-record.txt"
(
  umask 077
  {
    printf 'format=art-ai-go-production-cutover-v1\n'
    printf 'completed_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'image_tag=%s\n' "$finalize_image_tag"
    printf 'source_commit=%s\n' "$finalize_source_commit"
    printf 'backup_root=%s\n' "$finalize_backup_root"
    printf 'archive_root=%s\n' "$finalize_archive_root"
  } >"$finalize_record"
)
finalize_committed="true"
printf 'Go 最终迁移与生产接管完成\n'
printf '最终备份：%s\n' "$finalize_backup_root"
printf '旧生产数据归档：%s\n' "$finalize_archive_root"
