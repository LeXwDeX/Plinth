#!/usr/bin/env bash
set -Eeuo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

legacy_root="${test_root}/legacy"
external_root="${test_root}/external"
mkdir -p \
  "${legacy_root}/storage/generated" \
  "${legacy_root}/storage/thumbnails" \
  "${legacy_root}/storage/task-inputs" \
  "${external_root}/assets" \
  "${external_root}/cache" \
  "${external_root}/audit" \
  "${external_root}/db"
: >"${external_root}/db/prod.db"

if ART_AI_PROTECTED_PATHS= scripts/backup-go-production.sh create \
  --legacy-root "$legacy_root" \
  --legacy-database "${external_root}/db/prod.db" \
  --legacy-assets "${external_root}/assets" \
  --legacy-cache "${external_root}/cache" \
  --legacy-audit "${external_root}/audit" \
  --destination "${external_root}/assets/backup" \
  >"${test_root}/overlap.out" 2>&1; then
  printf '错误接受了与源目录重叠的备份目标\n' >&2
  exit 1
fi
grep -q '不能与源目录重叠' "${test_root}/overlap.out"

# 宽泛目录规则不写具体服务器路径（#261）：根、一级、二级目录与 ART_AI_PROTECTED_PATHS
# 逐字列出的目录被拒绝；ETL 脚本使用同一规则。
for script in scripts/backup-go-production.sh scripts/run-go-etl-container.sh; do
  ! grep -q '/DATA' "$script" || { printf '%s 仍写着具体服务器路径\n' "$script" >&2; exit 1; }
  eval "$(sed -n '/^[a-z]*_broad_directory() {/,/^}/p' "$script" | sed 's/^[a-z]*_broad_directory()/broad_directory()/')"
  for path in / /srv /srv/app /opt/protected/root; do
    ART_AI_PROTECTED_PATHS=/opt/protected/root:/srv/other/ broad_directory "$path" ||
      { printf '%s 接受了宽泛目录 %s\n' "$script" "$path" >&2; exit 1; }
  done
  ART_AI_PROTECTED_PATHS=/srv/other/ broad_directory /srv/other ||
    { printf '%s 未按 ART_AI_PROTECTED_PATHS 拒绝\n' "$script" >&2; exit 1; }
  for path in /srv/app/backups /opt/protected/root/sub "${test_root}/external/backup"; do
    if ART_AI_PROTECTED_PATHS=/opt/protected/root broad_directory "$path"; then
      printf '%s 错误拒绝了 %s\n' "$script" "$path" >&2
      exit 1
    fi
  done
  unset -f broad_directory
done

if ART_AI_PROTECTED_PATHS= scripts/backup-go-production.sh create --destination / \
  >"${test_root}/broad.out" 2>&1; then
  printf '错误接受了根目录作为备份目标\n' >&2
  exit 1
fi
grep -q '拒绝把宽泛目录作为备份目标' "${test_root}/broad.out"

# 受保护目录必须由执行环境显式给出（#334）：未设置 ART_AI_PROTECTED_PATHS 时，
# 备份 create 与 ETL 拒绝写入；列出相对路径同样拒绝。显式空串表示该主机没有额外目录。
if (unset ART_AI_PROTECTED_PATHS; scripts/backup-go-production.sh create \
  --destination "${test_root}/backup-unset") >"${test_root}/unset.out" 2>&1; then
  printf '未设置 ART_AI_PROTECTED_PATHS 时 backup create 未拒绝\n' >&2
  exit 1
fi
grep -q '未设置 ART_AI_PROTECTED_PATHS' "${test_root}/unset.out"
[[ ! -e "${test_root}/backup-unset" ]] ||
  { printf '拒绝前已创建备份目标\n' >&2; exit 1; }
if ART_AI_PROTECTED_PATHS=relative/dir scripts/backup-go-production.sh create \
  --destination "${test_root}/backup-relative" >"${test_root}/relative.out" 2>&1; then
  printf 'ART_AI_PROTECTED_PATHS 含相对路径时 backup create 未拒绝\n' >&2
  exit 1
fi
grep -q 'ART_AI_PROTECTED_PATHS 只能列绝对路径' "${test_root}/relative.out"
mkdir -p "${test_root}/etl-backup"
if (unset ART_AI_PROTECTED_PATHS; scripts/run-go-etl-container.sh \
  --backup-root "${test_root}/etl-backup" --target-root "${test_root}/etl-target" \
  --image-tag test) >"${test_root}/etl-unset.out" 2>&1; then
  printf '未设置 ART_AI_PROTECTED_PATHS 时 ETL 未拒绝\n' >&2
  exit 1
fi
grep -q '未设置 ART_AI_PROTECTED_PATHS' "${test_root}/etl-unset.out"
[[ ! -e "${test_root}/etl-target" ]] ||
  { printf '拒绝前已创建 ETL 目标\n' >&2; exit 1; }

printf 'backup 路径重叠测试通过\n'
