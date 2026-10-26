#!/usr/bin/env bash
set -Eeuo pipefail

inventory_legacy_root=""
inventory_legacy_container=""
inventory_output=""
inventory_temp_root="${TMPDIR:-/tmp}"
inventory_tmp=""
inventory_required_tables=(
  users
  asset_registry
  usage_ledger
  balance_snapshots
  chat_sessions
  chat_messages
  task_cache
  task_input_snapshot
  audit_logs
  canvas_projects
  canvas_nodes
)
inventory_required_storage=(
  assets
  generated
  cache
  audit
  thumbnails
  task-inputs
)

inventory_usage() {
  printf '%s\n' \
    '用法:' \
    '  collect-production-migration-inventory.sh' \
    '    --legacy-root <正式生产旧 Next 项目根>' \
    '    --legacy-container <正式生产旧 Next 容器全名>' \
    '    --output <全新脱敏报告文件>' \
    '    [--temp-root <SQLite 快照临时目录>]' \
    '' \
    '脚本只读取旧容器元数据、SQLite 和 storage 统计；不会停止容器、修改源数据、' \
    '读取 .env/容器环境变量或访问外部网络。输出文件权限固定为 0600。'
}

inventory_fail() {
  printf '正式生产只读盘点失败：%s\n' "$1" >&2
  exit 1
}

inventory_require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    inventory_fail "缺少必需命令 $1"
}

inventory_parse_arguments() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --legacy-root)
        [[ "$#" -ge 2 ]] || inventory_fail "--legacy-root 缺少值"
        inventory_legacy_root="$2"
        shift 2
        ;;
      --legacy-container)
        [[ "$#" -ge 2 ]] || inventory_fail "--legacy-container 缺少值"
        inventory_legacy_container="$2"
        shift 2
        ;;
      --output)
        [[ "$#" -ge 2 ]] || inventory_fail "--output 缺少值"
        inventory_output="$2"
        shift 2
        ;;
      --temp-root)
        [[ "$#" -ge 2 ]] || inventory_fail "--temp-root 缺少值"
        inventory_temp_root="$2"
        shift 2
        ;;
      --help|-h)
        inventory_usage
        exit 0
        ;;
      *)
        inventory_fail "未知参数 $1"
        ;;
    esac
  done
}

inventory_canonical_directory() {
  (
    cd -- "$1"
    pwd -P
  )
}

inventory_file_mode() {
  if stat -c '%a' "$1" >/dev/null 2>&1; then
    stat -c '%a' "$1"
    return
  fi
  stat -f '%Lp' "$1"
}

inventory_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

inventory_file_bytes() {
  if stat -c '%s' "$1" >/dev/null 2>&1; then
    stat -c '%s' "$1"
    return
  fi
  stat -f '%z' "$1"
}

inventory_directory_bytes() {
  local directory="$1"
  while IFS= read -r -d '' inventory_path; do
    inventory_file_bytes "$inventory_path"
  done < <(find "$directory" -type f -print0) |
    awk '{sum += $1} END {print sum + 0}'
}

inventory_mount_source() {
  local destination="$1"

  docker container inspect \
    --format \
    "{{ range .Mounts }}{{ if eq .Destination \"${destination}\" }}{{ .Source }}{{ end }}{{ end }}" \
    "$inventory_legacy_container"
}

inventory_cleanup() {
  if [[ "${BASH_SUBSHELL:-0}" != "0" ]]; then
    return
  fi
  if [[ -n "$inventory_tmp" &&
    -d "$inventory_tmp" &&
    "$(basename -- "$inventory_tmp")" == \
      art-ai-production-inventory.* ]]; then
    find "$inventory_tmp" -type f -delete
    rmdir "$inventory_tmp"
  fi
}
trap inventory_cleanup EXIT

inventory_parse_arguments "$@"
[[ -n "$inventory_legacy_root" ]] ||
  inventory_fail "必须指定 --legacy-root"
[[ -n "$inventory_legacy_container" ]] ||
  inventory_fail "必须指定 --legacy-container"
[[ "$inventory_legacy_container" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] ||
  inventory_fail "--legacy-container 不是安全的容器名"
[[ -n "$inventory_output" ]] ||
  inventory_fail "必须指定 --output"

for inventory_command in \
  awk chmod date df docker find hostname id mktemp mv rmdir \
  sqlite3 stat tr uname wc; do
  inventory_require_command "$inventory_command"
done
if ! command -v sha256sum >/dev/null 2>&1 &&
  ! command -v shasum >/dev/null 2>&1; then
  inventory_fail "缺少 sha256sum 或 shasum"
fi

[[ -d "$inventory_legacy_root" && ! -L "$inventory_legacy_root" ]] ||
  inventory_fail "旧 Next 项目根不存在、不是目录或是符号链接"
inventory_legacy_root="$(
  inventory_canonical_directory "$inventory_legacy_root"
)"
inventory_storage="${inventory_legacy_root}/storage"
inventory_database_root="$(inventory_mount_source /app/data)"
inventory_assets="$(inventory_mount_source /app/uploads)"
inventory_cache="$(inventory_mount_source /app/asset-cache)"
inventory_audit="$(inventory_mount_source /app/audit-assets)"
[[ -n "$inventory_database_root" ]] ||
  inventory_fail "旧容器缺少 /app/data 挂载，无法确定真实数据库"
[[ -n "$inventory_assets" ]] ||
  inventory_fail "旧容器缺少 /app/uploads 挂载，无法确定真实输入资产目录"
[[ -n "$inventory_cache" ]] ||
  inventory_fail "旧容器缺少 /app/asset-cache 挂载，无法确定真实任务缓存目录"
[[ -n "$inventory_audit" ]] ||
  inventory_fail "旧容器缺少 /app/audit-assets 挂载，无法确定真实审计附件目录"
inventory_database="${inventory_database_root}/prod.db"
[[ -d "$inventory_storage" && ! -L "$inventory_storage" ]] ||
  inventory_fail "旧 storage 不存在、不是目录或是符号链接"
[[ -f "$inventory_database" && ! -L "$inventory_database" ]] ||
  inventory_fail "旧 SQLite 不存在、不是普通文件或是符号链接"

inventory_output_parent="$(dirname -- "$inventory_output")"
inventory_output_name="$(basename -- "$inventory_output")"
[[ "$inventory_output_name" != "." &&
  "$inventory_output_name" != ".." ]] ||
  inventory_fail "--output 必须是文件路径"
[[ -d "$inventory_output_parent" &&
  ! -L "$inventory_output_parent" ]] ||
  inventory_fail "输出目录不存在、不是目录或是符号链接"
inventory_output_parent="$(
  inventory_canonical_directory "$inventory_output_parent"
)"
inventory_output="${inventory_output_parent}/${inventory_output_name}"
[[ ! -e "$inventory_output" && ! -L "$inventory_output" ]] ||
  inventory_fail "输出文件已存在，拒绝覆盖"
if [[ "$inventory_output" == "$inventory_legacy_root" ||
  "$inventory_output" == "$inventory_legacy_root/"* ]]; then
  inventory_fail "输出文件不能位于旧 Next 项目根内"
fi

docker container inspect "$inventory_legacy_container" >/dev/null 2>&1 ||
  inventory_fail "旧 Next 容器不存在"

for inventory_directory in generated thumbnails task-inputs; do
  [[ -d "${inventory_storage}/${inventory_directory}" ]] ||
    inventory_fail "旧 storage 缺少目录 ${inventory_directory}"
done
for inventory_directory_path in \
  "$inventory_assets" "$inventory_cache" "$inventory_audit"; do
  [[ -d "$inventory_directory_path" && ! -L "$inventory_directory_path" ]] ||
    inventory_fail "容器挂载源不存在、不是目录或是符号链接：$inventory_directory_path"
done

[[ -d "$inventory_temp_root" && ! -L "$inventory_temp_root" ]] ||
  inventory_fail "SQLite 快照临时目录不存在、不是目录或是符号链接"
inventory_temp_parent="$(inventory_canonical_directory "$inventory_temp_root")"
inventory_tmp="$(
  mktemp -d "${inventory_temp_parent}/art-ai-production-inventory.XXXXXX"
)"
chmod 0700 "$inventory_tmp"
inventory_tmp="$(inventory_canonical_directory "$inventory_tmp")"
inventory_snapshot="${inventory_tmp}/prod.db"
sqlite3 \
  -cmd '.timeout 10000' \
  "$inventory_database" \
  ".backup '${inventory_snapshot}'"
# Only normalize the private backup; the live source retains its journal mode.
# Match verify-cutover-source.py and the final backup's standalone DELETE digest.
[[ "$(sqlite3 "$inventory_snapshot" 'PRAGMA journal_mode=DELETE;')" == "delete" ]] ||
  inventory_fail "SQLite 私有快照无法切换为 DELETE 模式"
[[ "$(sqlite3 "$inventory_snapshot" 'PRAGMA quick_check;')" == "ok" ]] ||
  inventory_fail "SQLite 快照 quick_check 失败"
inventory_foreign_key_violations="$(
  sqlite3 "$inventory_snapshot" \
    'SELECT count(*) FROM pragma_foreign_key_check;'
)"

inventory_report_partial="${inventory_tmp}/inventory.txt"
(
  umask 077
  {
    printf 'format=art-ai-formal-production-inventory-v2\n'
    printf 'collected_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'source_mutated=false\n'
    printf 'secrets_read=false\n'
    printf 'external_network_accessed=false\n'
    printf 'hostname=%s\n' "$(hostname)"
    printf 'kernel=%s\n' "$(uname -sr)"
    printf 'operator_uid=%s\n' "$(id -u)"
    printf 'legacy_root=%s\n' "$inventory_legacy_root"
    printf 'legacy_storage=%s\n' "$inventory_storage"
    printf 'legacy_database=%s\n' "$inventory_database"
    printf 'legacy_assets=%s\n' "$inventory_assets"
    printf 'legacy_cache=%s\n' "$inventory_cache"
    printf 'legacy_audit=%s\n' "$inventory_audit"
    printf 'legacy_container=%s\n' "$inventory_legacy_container"
    printf 'legacy_container_name=%s\n' "$(
      docker container inspect \
        --format '{{ .Name }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_container_image=%s\n' "$(
      docker container inspect \
        --format '{{ .Config.Image }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_container_image_id=%s\n' "$(
      docker container inspect \
        --format '{{ .Image }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_container_running=%s\n' "$(
      docker container inspect \
        --format '{{ .State.Running }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_compose_project=%s\n' "$(
      docker container inspect \
        --format \
        '{{ index .Config.Labels "com.docker.compose.project" }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_compose_service=%s\n' "$(
      docker container inspect \
        --format \
        '{{ index .Config.Labels "com.docker.compose.service" }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_compose_working_dir=%s\n' "$(
      docker container inspect \
        --format \
        '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' \
        "$inventory_legacy_container"
    )"
    printf 'legacy_port_bindings=%s\n' "$(
      docker container inspect \
        --format '{{ json .HostConfig.PortBindings }}' \
        "$inventory_legacy_container"
    )"
    while IFS= read -r inventory_mount; do
      [[ -n "$inventory_mount" ]] ||
        continue
      printf 'legacy_mount=%s\n' "$inventory_mount"
    done < <(
      docker container inspect \
        --format \
        '{{ range .Mounts }}{{ printf "%s|%s|%s|%t\n" .Type .Source .Destination .RW }}{{ end }}' \
        "$inventory_legacy_container"
    )
    printf 'database_snapshot_sha256=%s\n' "$(
      inventory_sha256 "$inventory_snapshot"
    )"
    printf 'database_snapshot_bytes=%s\n' "$(
      inventory_file_bytes "$inventory_snapshot"
    )"
    printf 'database_quick_check=ok\n'
    printf 'database_foreign_key_violations=%s\n' \
      "$inventory_foreign_key_violations"
    for inventory_table in "${inventory_required_tables[@]}"; do
      inventory_table_exists="$(
        sqlite3 "$inventory_snapshot" \
          "SELECT count(*) FROM sqlite_master
           WHERE type='table' AND name='${inventory_table}';"
      )"
      printf 'table_%s_exists=%s\n' \
        "$inventory_table" \
        "$inventory_table_exists"
      if [[ "$inventory_table_exists" == "1" ]]; then
        printf 'table_%s_rows=%s\n' \
          "$inventory_table" \
          "$(
            sqlite3 "$inventory_snapshot" \
              "SELECT count(*) FROM \"${inventory_table}\";"
          )"
      fi
    done
    inventory_invalid_usage_rows="$(
      sqlite3 "$inventory_snapshot" \
        "SELECT count(*) FROM usage_ledger
         WHERE taskId IS NULL OR trim(taskId) = ''
            OR typeof(estimatedCredits) != 'integer'
            OR estimatedCredits < 0;"
    )"
    [[ "$inventory_invalid_usage_rows" == "0" ]] ||
      inventory_fail "usage_ledger 存在无法作为基线的任务 ID 或积分"
    printf 'baseline_users=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM users;')"
    printf 'baseline_generations=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(DISTINCT taskId) FROM usage_ledger;')"
    printf 'baseline_usage_tasks=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM usage_ledger;')"
    printf 'baseline_usage_credits=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT coalesce(sum(estimatedCredits), 0) FROM usage_ledger;')"
    printf 'baseline_audit_minimum=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM audit_logs;')"
    printf 'baseline_input_assets=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM asset_registry;')"
    printf 'baseline_chat_sessions=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM chat_sessions;')"
    printf 'baseline_chat_messages=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM chat_messages;')"
    printf 'baseline_canvas_projects=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM canvas_projects;')"
    printf 'baseline_canvas_nodes=%s\n' \
      "$(sqlite3 "$inventory_snapshot" 'SELECT count(*) FROM canvas_nodes;')"
    for inventory_directory in "${inventory_required_storage[@]}"; do
      case "$inventory_directory" in
        assets) inventory_directory_path="$inventory_assets" ;;
        cache) inventory_directory_path="$inventory_cache" ;;
        audit) inventory_directory_path="$inventory_audit" ;;
        *) inventory_directory_path="${inventory_storage}/${inventory_directory}" ;;
      esac
      printf 'storage_%s_files=%s\n' \
        "$inventory_directory" \
        "$(
          find "$inventory_directory_path" -type f -print |
            wc -l |
            tr -d '[:space:]'
        )"
      printf 'storage_%s_bytes=%s\n' \
        "$inventory_directory" \
        "$(inventory_directory_bytes "$inventory_directory_path")"
    done
    printf 'filesystem_capacity_kb=%s\n' "$(
      df -Pk "$inventory_legacy_root" |
        awk 'NR == 2 {print $2}'
    )"
    printf 'filesystem_available_kb=%s\n' "$(
      df -Pk "$inventory_legacy_root" |
        awk 'NR == 2 {print $4}'
    )"
  } >"$inventory_report_partial"
)
chmod 0600 "$inventory_report_partial"
mv "$inventory_report_partial" "$inventory_output"
[[ "$(inventory_file_mode "$inventory_output")" == "600" ]] ||
  inventory_fail "报告权限不是 0600"

printf '正式生产只读盘点完成：%s\n' "$inventory_output"
printf '报告未读取 .env、容器环境变量或业务行内容；请将该报告返回用于生成迁移配置。\n'
