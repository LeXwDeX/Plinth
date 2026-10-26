#!/usr/bin/env bash
set -Eeuo pipefail

backup_command="${1:-}"
if [[ -n "$backup_command" ]]; then
  shift
fi

backup_legacy_root=""
backup_legacy_database=""
backup_legacy_assets=""
backup_legacy_cache=""
backup_legacy_audit=""
backup_destination=""
backup_rsync_checksum="${BACKUP_RSYNC_CHECKSUM:-1}"
backup_required_tables=(
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
backup_required_storage=(
  assets
  generated
  cache
  audit
  thumbnails
  task-inputs
)

backup_usage() {
  printf '%s\n' \
    '用法:' \
    '  backup-go-production.sh create --legacy-root <旧项目根> --destination <备份目录>' \
    '      [--legacy-database <prod.db>] [--legacy-assets <目录>]' \
    '      [--legacy-cache <目录>] [--legacy-audit <目录>]' \
    '  backup-go-production.sh verify --destination <备份目录> [--legacy-root <旧项目根>]' \
    '' \
    'create 使用 SQLite 在线备份复制 prod.db，并完整同步 storage。' \
    'verify 校验数据库、11 张旧表、storage 目录；提供旧项目根时还会逐文件校验。'
}

backup_fail() {
  printf '备份失败：%s\n' "$1" >&2
  exit 1
}

backup_require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    backup_fail "缺少必需命令 $1"
}

backup_parse_arguments() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --legacy-root)
        [[ "$#" -ge 2 ]] || backup_fail "--legacy-root 缺少值"
        backup_legacy_root="$2"
        shift 2
        ;;
      --destination)
        [[ "$#" -ge 2 ]] || backup_fail "--destination 缺少值"
        backup_destination="$2"
        shift 2
        ;;
      --legacy-database)
        [[ "$#" -ge 2 ]] || backup_fail "--legacy-database 缺少值"
        backup_legacy_database="$2"
        shift 2
        ;;
      --legacy-assets)
        [[ "$#" -ge 2 ]] || backup_fail "--legacy-assets 缺少值"
        backup_legacy_assets="$2"
        shift 2
        ;;
      --legacy-cache)
        [[ "$#" -ge 2 ]] || backup_fail "--legacy-cache 缺少值"
        backup_legacy_cache="$2"
        shift 2
        ;;
      --legacy-audit)
        [[ "$#" -ge 2 ]] || backup_fail "--legacy-audit 缺少值"
        backup_legacy_audit="$2"
        shift 2
        ;;
      --help|-h)
        backup_usage
        exit 0
        ;;
      *)
        backup_fail "未知参数 $1"
        ;;
    esac
  done
}

backup_canonical_existing_directory() {
  (
    cd -- "$1"
    pwd -P
  )
}

backup_canonical_existing_file() {
  local parent

  parent="$(backup_canonical_existing_directory "$(dirname -- "$1")")"
  printf '%s/%s\n' "$parent" "$(basename -- "$1")"
}

backup_paths_overlap() {
  [[ "$1" == "$2" || "$1" == "$2/"* || "$2" == "$1/"* ]]
}

backup_absolute_target() {
  local target="$1"
  local parent
  local name

  parent="$(dirname -- "$target")"
  name="$(basename -- "$target")"
  mkdir -p -- "$parent"
  parent="$(backup_canonical_existing_directory "$parent")"
  printf '%s/%s\n' "$parent" "$name"
}

# 宽泛目录：根、一级与二级目录，以及 ART_AI_PROTECTED_PATHS（冒号分隔的绝对路径，
# 由执行环境在本地给出）逐字列出的目录。仓库不写具体服务器路径（#261）。
backup_broad_directory() {
  local path="$1" entry
  [[ "$path" =~ ^/[^/]+/[^/]+/.+ ]] || return 0
  while IFS= read -r entry; do
    [[ -n "$entry" && "$path" == "${entry%/}" ]] && return 0
  done < <(printf '%s\n' "${ART_AI_PROTECTED_PATHS:-}" | tr ':' '\n')
  return 1
}

# 受保护目录必须由执行环境显式给出（#334）：ART_AI_PROTECTED_PATHS 由
# scripts/release-env.py compose-env <环境> 按本地环境文件给出（本栈根与 protected_paths）。
# 未设置时拒绝写入；该主机确无额外受保护目录时显式设为空串。
backup_require_protected_paths() {
  local entry
  [[ -n "${ART_AI_PROTECTED_PATHS+set}" ]] ||
    backup_fail "未设置 ART_AI_PROTECTED_PATHS：先用 scripts/release-env.py compose-env <环境> 取得本机受保护目录（确无时显式设为空串）"
  while IFS= read -r entry; do
    [[ -z "$entry" || "$entry" == /* ]] ||
      backup_fail "ART_AI_PROTECTED_PATHS 只能列绝对路径"
  done < <(printf '%s\n' "$ART_AI_PROTECTED_PATHS" | tr ':' '\n')
}

backup_validate_paths() {
  [[ -n "$backup_destination" ]] ||
    backup_fail "必须指定 --destination"
  if [[ "$backup_command" == create ]]; then
    backup_require_protected_paths
  fi
  backup_destination="$(backup_absolute_target "$backup_destination")"
  if backup_broad_directory "$backup_destination"; then
    backup_fail "拒绝把宽泛目录作为备份目标"
  fi

  if [[ -n "$backup_legacy_root" ]]; then
    [[ -d "$backup_legacy_root" ]] ||
      backup_fail "旧项目根不存在"
    backup_legacy_root="$(
      backup_canonical_existing_directory "$backup_legacy_root"
    )"
    if [[ "$backup_destination" == "$backup_legacy_root" ||
      "$backup_destination" == "$backup_legacy_root/"* ]]; then
      backup_fail "备份目录不能位于旧项目根内部"
    fi
    backup_source_storage="${backup_legacy_root}/storage"
    backup_legacy_database="${backup_legacy_database:-${backup_source_storage}/data/prod.db}"
    backup_legacy_assets="${backup_legacy_assets:-${backup_source_storage}/assets}"
    backup_legacy_cache="${backup_legacy_cache:-${backup_source_storage}/cache}"
    backup_legacy_audit="${backup_legacy_audit:-${backup_source_storage}/audit}"
    [[ -f "$backup_legacy_database" && ! -L "$backup_legacy_database" ]] ||
      backup_fail "旧生产数据库不存在、不是普通文件或是符号链接"
    backup_legacy_database="$(backup_canonical_existing_file "$backup_legacy_database")"
    for backup_source_directory in \
      "$backup_legacy_assets" "$backup_legacy_cache" "$backup_legacy_audit" \
      "${backup_source_storage}/generated" \
      "${backup_source_storage}/thumbnails" \
      "${backup_source_storage}/task-inputs"; do
      [[ -d "$backup_source_directory" && ! -L "$backup_source_directory" ]] ||
        backup_fail "旧生产资产目录不存在、不是目录或是符号链接：$backup_source_directory"
    done
    backup_legacy_assets="$(backup_canonical_existing_directory "$backup_legacy_assets")"
    backup_legacy_cache="$(backup_canonical_existing_directory "$backup_legacy_cache")"
    backup_legacy_audit="$(backup_canonical_existing_directory "$backup_legacy_audit")"
    if backup_paths_overlap "$backup_destination" \
      "$(dirname -- "$backup_legacy_database")"; then
      backup_fail "备份目录不能与源数据库目录重叠"
    fi
    for backup_source_directory in \
      "$backup_legacy_assets" "$backup_legacy_cache" "$backup_legacy_audit" \
      "${backup_source_storage}/generated" \
      "${backup_source_storage}/thumbnails" \
      "${backup_source_storage}/task-inputs"; do
      backup_source_directory="$(
        backup_canonical_existing_directory "$backup_source_directory"
      )"
      if backup_paths_overlap "$backup_destination" "$backup_source_directory"; then
        backup_fail \
          "备份目录不能与源目录重叠：$backup_source_directory"
      fi
    done
  fi
}

backup_source_directory() {
  case "$1" in
    assets) printf '%s\n' "$backup_legacy_assets" ;;
    cache) printf '%s\n' "$backup_legacy_cache" ;;
    audit) printf '%s\n' "$backup_legacy_audit" ;;
    *) printf '%s/%s\n' "$backup_source_storage" "$1" ;;
  esac
}

backup_rsync_arguments() {
  backup_rsync_args=(
    -aH
    --numeric-ids
    --delete
    --omit-dir-times
    --exclude=data/prod.db
    --exclude=data/prod.db-wal
    --exclude=data/prod.db-shm
  )
}

backup_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

backup_verify_database() {
  local database="$1"
  local quick_check
  local table
  local exists

  [[ -f "$database" ]] ||
    backup_fail "备份数据库不存在"
  if ! quick_check="$(
    sqlite3 -readonly "$database" 'PRAGMA quick_check;' 2>&1
  )"; then
    printf '数据库完整性校验失败：%s\n' "$quick_check" >&2
    return 1
  fi
  if [[ "$quick_check" != "ok" ]]; then
    printf '数据库完整性校验失败\n' >&2
    return 1
  fi
  for table in "${backup_required_tables[@]}"; do
    if ! exists="$(
      sqlite3 -readonly "$database" \
        "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='$table';"
    )"; then
      printf '数据库表校验失败：%s\n' "$table" >&2
      return 1
    fi
    if [[ "$exists" != "1" ]]; then
      printf '数据库缺少旧表：%s\n' "$table" >&2
      return 1
    fi
    if ! sqlite3 -readonly "$database" \
      "SELECT COUNT(*) FROM \"$table\";" >/dev/null; then
      printf '数据库表不可读：%s\n' "$table" >&2
      return 1
    fi
  done
}

backup_verify_storage_layout() {
  local storage="$1"
  local directory
  local sample

  for directory in "${backup_required_storage[@]}"; do
    if [[ ! -d "$storage/$directory" ]]; then
      printf 'storage 备份缺少目录：%s\n' "$directory" >&2
      return 1
    fi
    sample="$(find "$storage/$directory" -type f -print -quit)"
    if [[ -n "$sample" ]] && ! head -c 1 "$sample" >/dev/null; then
      printf 'storage 抽样文件不可读：%s\n' "$directory" >&2
      return 1
    fi
  done
}

backup_normalize_storage_permissions() {
  local storage="$1"

  find "$storage" -type d -exec chmod 0750 {} +
  find "$storage" -type f -exec chmod 0640 {} +
  if [[ "$(id -u)" == "0" ]]; then
    chown -R 0:0 "$storage"
  fi
}

backup_verify_storage_permissions() {
  local storage="$1"
  local invalid

  invalid="$(find "$storage" -type d ! -perm 0750 -print -quit)"
  [[ -z "$invalid" ]] || {
    printf 'storage 备份目录权限不安全或不可供 ETL 读取\n' >&2
    return 1
  }
  invalid="$(find "$storage" -type f ! -perm 0640 -print -quit)"
  [[ -z "$invalid" ]] || {
    printf 'storage 备份文件权限不安全或不可供 ETL 读取\n' >&2
    return 1
  }
  if [[ "$(id -u)" == "0" ]]; then
    invalid="$(
      find "$storage" \
        \( ! -user root -o ! -group root \) \
        -print -quit
    )"
    [[ -z "$invalid" ]] || {
      printf 'storage 备份必须归 root:root 所有\n' >&2
      return 1
    }
  fi
}

backup_verify_directory_against_source() {
  local source_storage="$1"
  local target_storage="$2"
  local differences
  local sample
  local relative
  local source_hash
  local target_hash

  backup_rsync_arguments
  if [[ "$backup_rsync_checksum" == "1" ]]; then
    backup_rsync_args+=(
      --checksum
    )
  fi
  differences="$(
    rsync "${backup_rsync_args[@]}" \
      --no-perms \
      --no-owner \
      --no-group \
      --dry-run \
      --itemize-changes \
      "$source_storage/" \
      "$target_storage/"
  )"
  if [[ -n "$differences" ]]; then
    printf 'storage 源目标校验不一致（仅列前 20 项）：\n' >&2
    printf '%s\n' "$differences" | head -n 20 >&2
    return 1
  fi

  sample="$(find "$source_storage" -type f -print -quit)"
  if [[ -n "$sample" ]]; then
    relative="${sample#"$source_storage/"}"
    [[ -f "$target_storage/$relative" ]] || {
      printf 'storage 抽样文件缺失\n' >&2
      return 1
    }
    source_hash="$(backup_sha256 "$sample")"
    target_hash="$(backup_sha256 "$target_storage/$relative")"
    if [[ "$source_hash" != "$target_hash" ]]; then
      printf 'storage 抽样文件 SHA-256 不一致\n' >&2
      return 1
    fi
  fi
}

backup_write_manifest() {
  local destination="$1"
  local source_database="$2"
  local target_database="$3"
  local manifest="$destination/manifest.txt"
  local table
  local directory
  local files
  local kib

  {
    printf 'format=art-ai-go-cutover-backup-v1\n'
    printf 'created_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'source_root=%s\n' "$backup_legacy_root"
    printf 'source_database_bytes=%s\n' "$(wc -c <"$source_database" | tr -d ' ')"
    printf 'backup_database_bytes=%s\n' "$(wc -c <"$target_database" | tr -d ' ')"
    printf 'backup_database_sha256=%s\n' "$(backup_sha256 "$target_database")"
    for table in "${backup_required_tables[@]}"; do
      printf 'table.%s.rows=%s\n' \
        "$table" \
        "$(sqlite3 -readonly "$target_database" \
          "SELECT COUNT(*) FROM \"$table\";")"
    done
    for directory in "${backup_required_storage[@]}"; do
      files="$(
        find "$destination/storage/$directory" -type f | wc -l | tr -d ' '
      )"
      kib="$(
        du -sk "$destination/storage/$directory" | awk '{print $1}'
      )"
      printf 'storage.%s.files=%s\n' "$directory" "$files"
      printf 'storage.%s.kib=%s\n' "$directory" "$kib"
    done
  } >"$manifest"
  chmod 0600 "$manifest"
}

backup_verify() {
  local destination="$1"
  local database="$destination/storage/data/prod.db"

  [[ -d "$destination/storage" ]] ||
    backup_fail "备份 storage 不存在"
  backup_verify_database "$database"
  backup_verify_storage_layout "$destination/storage"
  backup_verify_storage_permissions "$destination/storage"
  if [[ -n "$backup_legacy_root" ]]; then
    for directory in "${backup_required_storage[@]}"; do
      backup_verify_directory_against_source \
        "$(backup_source_directory "$directory")" \
        "$destination/storage/$directory"
    done
  fi
  printf '备份校验通过：%s\n' "$destination"
}

backup_create() {
  local source_database="$backup_legacy_database"
  local partial_destination="${backup_destination}.partial.$$"
  local target_database="$partial_destination/storage/data/prod.db"

  [[ -n "$backup_legacy_root" ]] ||
    backup_fail "create 必须指定 --legacy-root"
  [[ -f "$source_database" ]] ||
    backup_fail "旧生产数据库不存在"
  [[ ! -e "$backup_destination" ]] ||
    backup_fail "备份目标已存在，拒绝覆盖"
  [[ ! -e "$partial_destination" ]] ||
    backup_fail "备份临时目录已存在"

  mkdir -p "$partial_destination/storage/data"
  for directory in "${backup_required_storage[@]}"; do
    mkdir -p "$partial_destination/storage/$directory"
  done
  # 仅允许 owner 与 root 组遍历；隔离 ETL 容器以非 root UID +
  # supplemental root group 只读挂载该备份。
  chmod 0750 "$partial_destination"
  backup_rsync_arguments
  for directory in "${backup_required_storage[@]}"; do
    rsync "${backup_rsync_args[@]}" \
      "$(backup_source_directory "$directory")/" \
      "$partial_destination/storage/$directory/"
  done

  case "$source_database:$target_database" in
    *"'"*|*$'\n'*)
      backup_fail "数据库路径包含不支持的字符"
      ;;
  esac
  sqlite3 \
    -cmd '.timeout 60000' \
    "$source_database" \
    ".backup '$target_database'"
  # .backup 会保留源库的 WAL journal_mode。切回 DELETE 让备份成为不依赖
  # -wal/-shm 副文件的独立只读快照。
  if [[ "$(
    sqlite3 "$target_database" 'PRAGMA journal_mode=DELETE;'
  )" != "delete" ]]; then
    backup_fail "无法把备份库转换为独立 DELETE journal_mode"
  fi
  rm -f -- "${target_database}-wal" "${target_database}-shm"
  chmod 0640 "$target_database"

  # 第二次增量同步收敛第一次复制期间新增或替换的普通文件。
  for directory in "${backup_required_storage[@]}"; do
    rsync "${backup_rsync_args[@]}" \
      "$(backup_source_directory "$directory")/" \
      "$partial_destination/storage/$directory/"
  done
  backup_normalize_storage_permissions "$partial_destination/storage"
  backup_write_manifest \
    "$partial_destination" \
    "$source_database" \
    "$target_database"

  backup_verify_database "$target_database"
  backup_verify_storage_layout "$partial_destination/storage"
  for directory in "${backup_required_storage[@]}"; do
    backup_verify_directory_against_source \
      "$(backup_source_directory "$directory")" \
      "$partial_destination/storage/$directory"
  done
  mv "$partial_destination" "$backup_destination"
  printf '备份创建完成：%s\n' "$backup_destination"
  backup_verify "$backup_destination"
}

backup_parse_arguments "$@"
backup_require_command sqlite3
backup_require_command rsync
backup_require_command find
backup_require_command awk
backup_validate_paths

case "$backup_command" in
  create)
    backup_create
    ;;
  verify)
    backup_verify "$backup_destination"
    ;;
  --help|-h)
    backup_usage
    ;;
  *)
    backup_usage >&2
    exit 2
    ;;
esac
