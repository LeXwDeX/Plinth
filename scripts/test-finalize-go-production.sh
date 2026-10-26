#!/usr/bin/env bash
set -Eeuo pipefail

test_root="$(mktemp -d)"
test_root="$(cd -- "$test_root" && pwd -P)"
test_bin="${test_root}/bin"
test_legacy_root="${test_root}/legacy"
test_go_root="${test_root}/go"
test_log="${test_root}/docker.log"

cleanup() {
  if [[ "${TEST_KEEP_ROOT:-0}" == "1" ]]; then
    printf '保留测试目录：%s\n' "$test_root" >&2
    return
  fi
  rm -rf "$test_root"
}
trap cleanup EXIT

mkdir -p \
  "$test_bin" "$test_go_root" \
  "$test_legacy_root/storage/generated" \
  "$test_legacy_root/storage/thumbnails" \
  "$test_legacy_root/storage/task-inputs" \
  "$test_legacy_root/docker-data/uploads" \
  "$test_legacy_root/docker-data/asset-cache" \
  "$test_legacy_root/docker-data/audit-assets" \
  "$test_legacy_root/docker-data/db"
: >"$test_legacy_root/docker-data/db/prod.db"
for file in runtime.env approved.json; do
  : >"${test_root}/${file}"
  chmod 0600 "${test_root}/${file}"
done
cat >"${test_root}/baseline.json" <<'EOF'
{
  "users": 0,
  "generations": 0,
  "usage_tasks": 0,
  "usage_credits": 0,
  "audit_minimum": 0,
  "output_assets": 0,
  "input_assets": 0,
  "chat_sessions": 0,
  "chat_messages": 0,
  "canvas_projects": 0,
  "canvas_nodes": 0,
  "external_webhook_url": "https://webhook.example.test"
}
EOF
chmod 0600 "${test_root}/baseline.json"
if [[ "$(id -u)" == "0" ]]; then
  chown 100:101 "${test_root}/approved.json"
fi

cat >"${test_bin}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >>"${TEST_DOCKER_LOG}"
if [[ "$1" == "info" ]]; then printf 'fixture-daemon\n'; exit 0; fi
if [[ "$1 $2" == "container inspect" ]]; then
  case "${3:-}" in
    --format)
      case "$4" in
        *compose.project*) printf 'com.docker.compose.project=legacy-project\n' ;;
        *compose.service*) printf 'com.docker.compose.service=legacy-service\n' ;;
        *State.Running*) printf 'false\n' ;;
        *'.Name'*) printf '/legacy-container\n' ;;
        *'.Id'*) printf 'legacy-id\n' ;;
      esac
      ;;
  esac
  exit 0
fi
exit 0
EOF
chmod 0755 "${test_bin}/docker"

for stub in backup etl; do
  cat >"${test_bin}/${stub}" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod 0755 "${test_bin}/${stub}"
done
cat >"${test_bin}/cutover" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' \
  "generated=${GO_LEGACY_GENERATED_IMAGES_DIR:-}" \
  "cache=${GO_LEGACY_ASSET_CACHE_DIR:-}" \
  "thumbnails=${GO_LEGACY_THUMBNAIL_CACHE_DIR:-}" \
  "assets=${GO_LEGACY_INPUT_ASSETS_DIR:-}" \
  >>"${TEST_CUTOVER_LOG}"
exit 23
EOF
chmod 0755 "${test_bin}/cutover"
test_cutover_log="${test_root}/cutover.log"

python3 - "$test_root" <<'PY_BASELINE'
import importlib.util,json,pathlib,sys
root=pathlib.Path(sys.argv[1])
spec=importlib.util.spec_from_file_location("source_check","scripts/verify-cutover-source.py")
check=importlib.util.module_from_spec(spec);spec.loader.exec_module(check)
path=root/"source-baseline.json"
path.write_text(json.dumps({"format":"art-ai-production-source-baseline-v1","inventory_sha256":"a"*64,"database_snapshot_sha256":check.snapshot_sha256(root/"legacy/docker-data/db/prod.db")}))
path.chmod(0o600)
PY_BASELINE

common_args=(
  --expected-hostname "$(hostname)"
  --expected-docker-id fixture-daemon
  --source-baseline "${test_root}/source-baseline.json"
  --go-bind-ip 127.0.0.1
  --go-host-port 24101
  --preflight-port 24102
  --env-file "${test_root}/runtime.env"
  --image-tag test-image
  --source-commit 0123456789012345678901234567890123456789
  --approved-source-loss-acknowledgement "${test_root}/approved.json"
  --legacy-root "$test_legacy_root"
  --legacy-database "$test_legacy_root/docker-data/db/prod.db"
  --legacy-assets "$test_legacy_root/docker-data/uploads"
  --legacy-cache "$test_legacy_root/docker-data/asset-cache"
  --legacy-audit "$test_legacy_root/docker-data/audit-assets"
  --legacy-container legacy-container
  --legacy-compose-project legacy-project
  --legacy-compose-service legacy-service
  --go-root "$test_go_root"
  --go-project formal-go
  --acceptance-base-url https://formal.example.test
  --acceptance-baseline "${test_root}/baseline.json"
  --minimum-free-kib 1
)

run_finalize() {
  PATH="${test_bin}:$PATH" \
    TEST_DOCKER_LOG="$test_log" \
    TEST_CUTOVER_LOG="$test_cutover_log" \
    GO_BACKUP_SCRIPT="${test_bin}/backup" \
    GO_ETL_SCRIPT="${test_bin}/etl" \
    GO_CUTOVER_SCRIPT="${test_bin}/cutover" \
    GO_ACCEPTANCE_SCRIPT="${test_bin}/backup" \
    FINALIZE_RUN_ID="$1" \
    ART_AI_PROTECTED_PATHS="${TEST_PROTECTED_PATHS-}" \
    scripts/finalize-go-production.sh "${common_args[@]}" "${@:2}"
}

# #334: 未设置 ART_AI_PROTECTED_PATHS 时，在停旧容器之前就拒绝。
if (unset ART_AI_PROTECTED_PATHS
  PATH="${test_bin}:$PATH" TEST_DOCKER_LOG="$test_log" TEST_CUTOVER_LOG="$test_cutover_log" \
    GO_BACKUP_SCRIPT="${test_bin}/backup" GO_ETL_SCRIPT="${test_bin}/etl" \
    GO_CUTOVER_SCRIPT="${test_bin}/cutover" GO_ACCEPTANCE_SCRIPT="${test_bin}/backup" \
    FINALIZE_RUN_ID=rejects-unset-protected \
    scripts/finalize-go-production.sh "${common_args[@]}") >"${test_root}/protected.out" 2>&1; then
  printf '未设置 ART_AI_PROTECTED_PATHS 时 finalize 未拒绝\n' >&2
  exit 1
fi
grep -q '未设置 ART_AI_PROTECTED_PATHS' "${test_root}/protected.out"
if [[ -s "$test_log" ]] && grep -q 'stop' "$test_log"; then
  printf '拒绝前已停止旧容器\n' >&2
  exit 1
fi

if run_finalize rejects-source-overlap \
  --go-root "$test_legacy_root" \
  >"${test_root}/overlap.out" 2>&1; then
  printf '错误接受了与 Go 根重叠的旧生产源路径\n' >&2
  exit 1
fi
grep -q '不能与 Go 数据根重叠' "${test_root}/overlap.out"

for unsafe_url in \
  https://user@formal.example.test \
  'https://formal.example.test?target=other' \
  https://formal.example.test/path; do
  if run_finalize rejects-url --acceptance-base-url "$unsafe_url" \
    >"${test_root}/url.out" 2>&1; then
    printf '错误接受了不安全的正式入口 URL：%s\n' "$unsafe_url" >&2
    exit 1
  fi
  grep -Eq 'https://|userinfo|无路径' "${test_root}/url.out"
done

if run_finalize accepts-http-origin \
  --acceptance-base-url http://10.0.0.12:13333 \
  >"${test_root}/http.out" 2>&1; then
  printf '预检 stub 失败时事务错误返回成功\n' >&2
  exit 1
fi
if grep -q '必须使用 https://' "${test_root}/http.out"; then
  printf '内网 HTTP 入口被错误拒绝\n' >&2
  exit 1
fi

if run_finalize rejects-low-disk --minimum-free-kib 999999999999 \
  >"${test_root}/disk.out" 2>&1; then
  printf '错误接受了磁盘空间不足的事务\n' >&2
  exit 1
fi
grep -q '可用空间不足' "${test_root}/disk.out"

if run_finalize rejects-implicit-stop >"${test_root}/implicit.out" 2>&1; then
  printf '未显式声明时错误接受了已停止旧容器\n' >&2
  exit 1
fi
grep -q -- '--legacy-already-stopped' "${test_root}/implicit.out"

: >"$test_log"
if run_finalize accepts-explicit-stop --legacy-already-stopped \
  >"${test_root}/explicit.out" 2>&1; then
  printf '预检 stub 失败时事务错误返回成功\n' >&2
  exit 1
fi
if grep -q '^start legacy-container$' "$test_log"; then
  printf '事务错误启动了原本已停止的旧容器\n' >&2
  exit 1
fi
for expected_mount in \
  "generated=${test_legacy_root}/storage/generated" \
  "cache=${test_legacy_root}/docker-data/asset-cache" \
  "thumbnails=${test_legacy_root}/storage/thumbnails" \
  "assets=${test_legacy_root}/docker-data/uploads"; do
  grep -Fqx "$expected_mount" "$test_cutover_log" || {
    printf 'cutover 未收到预期挂载：%s\n' "$expected_mount" >&2
    exit 1
  }
done

printf 'finalize 已停止旧容器模式测试通过\n'
