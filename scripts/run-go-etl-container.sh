#!/usr/bin/env bash
set -Eeuo pipefail

etl_script_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"
etl_backup_script="${GO_BACKUP_SCRIPT:-${etl_script_root}/scripts/backup-go-production.sh}"
etl_backup_root=""
etl_target_root=""
etl_image_tag=""
etl_image_name="art-ai-go-production"
etl_report_name=""
etl_source_loss_acknowledgement=""
etl_source_loss_template_name=""
etl_ready_marker=""

etl_usage() {
  printf '%s\n' \
    '用法:' \
    '  run-go-etl-container.sh --backup-root <已校验备份> --target-root <Go 数据根>' \
    '      --image-tag <tag> [--image-name <name>] [--report-name <basename>]' \
    '      [--source-loss-acknowledgement <权限 0600 的已批准清单>]' \
    '      [--source-loss-template-name <未批准候选清单 basename>]' \
    '' \
    '脚本先验证备份，再用无网络、只读根文件系统的同版本镜像执行 ETL。' \
    '只有 ETL 退出码为 0 且对账报告结论为“通过”时脚本才成功。'
}

etl_fail() {
  printf 'Go ETL 容器执行失败：%s\n' "$1" >&2
  exit 1
}

etl_parse_arguments() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --backup-root)
        [[ "$#" -ge 2 ]] || etl_fail "--backup-root 缺少值"
        etl_backup_root="$2"
        shift 2
        ;;
      --target-root)
        [[ "$#" -ge 2 ]] || etl_fail "--target-root 缺少值"
        etl_target_root="$2"
        shift 2
        ;;
      --image-tag)
        [[ "$#" -ge 2 ]] || etl_fail "--image-tag 缺少值"
        etl_image_tag="$2"
        shift 2
        ;;
      --image-name)
        [[ "$#" -ge 2 ]] || etl_fail "--image-name 缺少值"
        etl_image_name="$2"
        shift 2
        ;;
      --report-name)
        [[ "$#" -ge 2 ]] || etl_fail "--report-name 缺少值"
        etl_report_name="$2"
        shift 2
        ;;
      --source-loss-acknowledgement)
        [[ "$#" -ge 2 ]] ||
          etl_fail "--source-loss-acknowledgement 缺少值"
        etl_source_loss_acknowledgement="$2"
        shift 2
        ;;
      --source-loss-template-name)
        [[ "$#" -ge 2 ]] ||
          etl_fail "--source-loss-template-name 缺少值"
        etl_source_loss_template_name="$2"
        shift 2
        ;;
      --help|-h)
        etl_usage
        exit 0
        ;;
      *)
        etl_fail "未知参数 $1"
        ;;
    esac
  done
}

etl_canonical_directory() {
  (
    cd -- "$1"
    pwd -P
  )
}

etl_file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
    return
  fi
  stat -c '%a' "$1"
}

etl_file_owner() {
  if stat -f '%u' "$1" >/dev/null 2>&1; then
    stat -f '%u' "$1"
    return
  fi
  stat -c '%u' "$1"
}

etl_canonical_file() {
  (
    cd -- "$(dirname -- "$1")"
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$1")"
  )
}

etl_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

etl_write_ready_marker() {
  local database="${etl_data_dir}/art-ai.db"
  local manifest="${etl_manifest_path}"
  local temporary="${etl_ready_marker}.partial.$$"

  [[ -f "$database" && ! -L "$database" ]] ||
    etl_fail "ETL 未生成普通文件形式的 Go 数据库"
  [[ -f "$etl_report_path" && ! -L "$etl_report_path" ]] ||
    etl_fail "ETL 未生成普通文件形式的对账报告"
  [[ -f "$manifest" && ! -L "$manifest" ]] ||
    etl_fail "ETL 未生成普通文件形式的 completeness manifest"
  [[ ! -e "$etl_ready_marker" && ! -L "$etl_ready_marker" ]] ||
    etl_fail "迁移就绪证明已存在，拒绝覆盖"

  (
    umask 077
    {
      printf 'format=art-ai-go-migration-ready-v1\n'
      printf 'image_tag=%s\n' "$etl_image_tag"
      printf 'database_sha256=%s\n' "$(etl_sha256 "$database")"
      printf 'report_name=%s\n' "$etl_report_name"
      printf 'report_sha256=%s\n' "$(etl_sha256 "$etl_report_path")"
      printf 'manifest_name=%s\n' "$(basename -- "$manifest")"
      printf 'manifest_sha256=%s\n' "$(etl_sha256 "$manifest")"
      printf 'created_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    } >"$temporary"
  )
  chmod 0600 "$temporary"
  mv "$temporary" "$etl_ready_marker"
}

# 受保护目录必须由执行环境显式给出（#334）：ART_AI_PROTECTED_PATHS 由
# scripts/release-env.py compose-env <环境> 按本地环境文件给出（本栈根与 protected_paths）。
# 未设置时拒绝写入；该主机确无额外受保护目录时显式设为空串。
etl_require_protected_paths() {
  local entry
  [[ -n "${ART_AI_PROTECTED_PATHS+set}" ]] ||
    etl_fail "未设置 ART_AI_PROTECTED_PATHS：先用 scripts/release-env.py compose-env <环境> 取得本机受保护目录（确无时显式设为空串）"
  while IFS= read -r entry; do
    [[ -z "$entry" || "$entry" == /* ]] ||
      etl_fail "ART_AI_PROTECTED_PATHS 只能列绝对路径"
  done < <(printf '%s\n' "$ART_AI_PROTECTED_PATHS" | tr ':' '\n')
}

etl_parse_arguments "$@"
etl_require_protected_paths
[[ -n "$etl_backup_root" ]] || etl_fail "必须指定 --backup-root"
# 宽泛目录：根、一级与二级目录，以及 ART_AI_PROTECTED_PATHS（冒号分隔的绝对路径，
# 由执行环境在本地给出）逐字列出的目录。仓库不写具体服务器路径（#261）。
etl_broad_directory() {
  local path="$1" entry
  [[ "$path" =~ ^/[^/]+/[^/]+/.+ ]] || return 0
  while IFS= read -r entry; do
    [[ -n "$entry" && "$path" == "${entry%/}" ]] && return 0
  done < <(printf '%s\n' "${ART_AI_PROTECTED_PATHS:-}" | tr ':' '\n')
  return 1
}

[[ -n "$etl_target_root" ]] || etl_fail "必须指定 --target-root"
[[ -n "$etl_image_tag" ]] || etl_fail "必须指定 --image-tag"
if [[ ! "$etl_image_tag" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
  etl_fail "--image-tag 不是安全的镜像标签"
fi
[[ -x "$etl_backup_script" ]] || etl_fail "备份校验脚本不可执行"
[[ -d "$etl_backup_root" ]] || etl_fail "备份目录不存在"
etl_backup_root="$(etl_canonical_directory "$etl_backup_root")"

mkdir -p "$etl_target_root"
etl_target_root="$(etl_canonical_directory "$etl_target_root")"
if etl_broad_directory "$etl_target_root"; then
  etl_fail "拒绝把宽泛或旧生产目录作为 ETL 目标"
fi
if [[ "$etl_target_root" == "$etl_backup_root" ||
  "$etl_target_root" == "$etl_backup_root/"* ]]; then
  etl_fail "ETL 目标不能位于备份目录内部"
fi

if [[ -z "$etl_report_name" ]]; then
  etl_report_name="migration-$(date -u '+%Y%m%dT%H%M%SZ').txt"
fi
if [[ ! "$etl_report_name" =~ ^[A-Za-z0-9._-]+$ ||
  "$etl_report_name" == "." ||
  "$etl_report_name" == ".." ]]; then
  etl_fail "--report-name 只能是安全 basename"
fi
if [[ -n "$etl_source_loss_template_name" ]]; then
  if [[ ! "$etl_source_loss_template_name" =~ ^[A-Za-z0-9._-]+$ ||
    "$etl_source_loss_template_name" == "." ||
    "$etl_source_loss_template_name" == ".." ]]; then
    etl_fail "--source-loss-template-name 只能是安全 basename"
  fi
  [[ "$etl_source_loss_template_name" != "$etl_report_name" ]] ||
    etl_fail "源损失候选清单不能覆盖对账报告"
fi
if [[ -n "$etl_source_loss_acknowledgement" &&
  -n "$etl_source_loss_template_name" ]]; then
  etl_fail "源损失候选清单与已批准确认清单不能同时使用"
fi
if [[ -n "$etl_source_loss_acknowledgement" ]]; then
  [[ -f "$etl_source_loss_acknowledgement" &&
    ! -L "$etl_source_loss_acknowledgement" ]] ||
    etl_fail "源损失确认清单不存在或不是普通文件"
  if [[ "$(etl_file_mode "$etl_source_loss_acknowledgement")" != "600" ]]; then
    etl_fail "源损失确认清单权限必须为 0600"
  fi
  if [[ "$(id -u)" == "0" &&
    "$(etl_file_owner "$etl_source_loss_acknowledgement")" != "100" ]]; then
    etl_fail "源损失确认清单必须由隔离 ETL 用户 UID 100 所有"
  fi
  etl_source_loss_acknowledgement="$(
    etl_canonical_file "$etl_source_loss_acknowledgement"
  )"
fi

etl_data_dir="${etl_target_root}/data"
etl_storage_dir="${etl_target_root}/storage-go"
etl_report_dir="${etl_target_root}/reports"
etl_report_path="${etl_report_dir}/${etl_report_name}"
etl_manifest_path="${etl_report_path}.manifest.json"
etl_ready_marker="${etl_target_root}/migration-ready.txt"
[[ ! -e "$etl_ready_marker" && ! -L "$etl_ready_marker" ]] ||
  etl_fail "迁移就绪证明已存在；请使用全新的 ETL 目标"
etl_source_loss_template_path=""
if [[ -n "$etl_source_loss_template_name" ]]; then
  etl_source_loss_template_path="${etl_report_dir}/${etl_source_loss_template_name}"
  [[ ! -e "$etl_source_loss_template_path" ]] ||
    etl_fail "源损失候选清单已存在，拒绝覆盖"
fi
mkdir -p "$etl_data_dir" "$etl_storage_dir" "$etl_report_dir"
if [[ "$(id -u)" == "0" ]]; then
  chown -R 100:101 "$etl_data_dir" "$etl_storage_dir" "$etl_report_dir"
fi

"$etl_backup_script" verify --destination "$etl_backup_root"

etl_image="${etl_image_name}:${etl_image_tag}"
etl_actual_tag="$(
  docker image inspect \
    --format \
    '{{ index .Config.Labels "org.opencontainers.image.version" }}' \
    "$etl_image"
)"
if [[ "$etl_actual_tag" != "$etl_image_tag" ]]; then
  etl_fail \
    "镜像版本标签不一致：期望 $etl_image_tag，实际 $etl_actual_tag"
fi

etl_started_at="$(date +%s)"
etl_docker_arguments=(
  --rm \
  --user 100:101 \
  --group-add 0 \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:size=64m,mode=1777 \
  --volume "${etl_backup_root}:/legacy:ro" \
  --volume "${etl_data_dir}:/target-data" \
  --volume "${etl_storage_dir}:/target-storage" \
  --volume "${etl_report_dir}:/reports" \
  --entrypoint /usr/local/bin/art-ai-etl
)
etl_migrate_arguments=(
  migrate
  --legacy-root /legacy
  --target-db /target-data/art-ai.db
  --target-storage /target-storage
  --report "/reports/${etl_report_name}"
  --manifest "/reports/${etl_report_name}.manifest.json"
)
if [[ -n "$etl_source_loss_acknowledgement" ]]; then
  etl_docker_arguments+=(
    --volume \
    "${etl_source_loss_acknowledgement}:/source-loss-ack.json:ro"
  )
  etl_migrate_arguments+=(
    --source-loss-acknowledgement
    /source-loss-ack.json
  )
fi
if [[ -n "$etl_source_loss_template_name" ]]; then
  etl_migrate_arguments+=(
    --source-loss-template
    "/reports/${etl_source_loss_template_name}"
  )
fi
set +e
docker run \
  "${etl_docker_arguments[@]}" \
  "$etl_image" \
  "${etl_migrate_arguments[@]}"
etl_exit_status=$?
set -e
etl_finished_at="$(date +%s)"

if [[ -n "$etl_source_loss_template_path" ]]; then
  [[ -f "$etl_source_loss_template_path" &&
    ! -L "$etl_source_loss_template_path" ]] ||
    etl_fail "ETL 未生成源损失候选清单"
  chmod 0600 "$etl_source_loss_template_path"
  printf '源损失候选清单: %s\n' "$etl_source_loss_template_path"
fi
if [[ -f "$etl_report_path" && ! -L "$etl_report_path" ]]; then
  chmod 0600 "$etl_report_path"
fi
if [[ -f "$etl_manifest_path" && ! -L "$etl_manifest_path" ]]; then
  chmod 0600 "$etl_manifest_path"
fi
if [[ "$etl_exit_status" -ne 0 ]]; then
  exit "$etl_exit_status"
fi

[[ -f "$etl_report_path" ]] ||
  etl_fail "ETL 未生成对账报告"
[[ -f "$etl_manifest_path" && ! -L "$etl_manifest_path" ]] ||
  etl_fail "ETL 未生成 completeness manifest"
if ! jq -e -f "${etl_script_root}/scripts/etl-completeness-filter.jq" \
  "$etl_manifest_path" >/dev/null; then
  etl_fail "completeness manifest 未通过：$etl_manifest_path"
fi
etl_write_ready_marker
printf 'Go ETL 与对账通过：%s（耗时 %s 秒）\n' \
  "$etl_report_path" \
  "$((etl_finished_at - etl_started_at))"
printf '迁移就绪证明：%s\n' "$etl_ready_marker"
