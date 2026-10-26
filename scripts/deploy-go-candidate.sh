#!/usr/bin/env bash
set -Eeuo pipefail

candidate_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"
# 候选栈 Compose 项目名由执行环境给出（本地环境文件）；仓库不写任何环境的项目名（#261）。
candidate_project="${CANDIDATE_PROJECT:-}"
if ! [[ "$candidate_project" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]]; then
  printf 'CANDIDATE_PROJECT must name the candidate Compose project\n' >&2
  exit 1
fi
candidate_compose_file="${candidate_root}/docker-compose.go-candidate.yml"
candidate_env_file="${CANDIDATE_ENV_FILE:-${candidate_root}/../candidate.env}"
candidate_wait_timeout="${CANDIDATE_WAIT_TIMEOUT:-120}"
candidate_old_web_container="${candidate_project}-art-ai-web-1"
candidate_preflight_project="${candidate_project}_preflight"
candidate_preflight_port="${CANDIDATE_PREFLIGHT_PORT:-13341}"
candidate_preflight_storage_root=""
candidate_old_web_stopped="false"

candidate_cleanup_preflight() {
  if declare -p candidate_preflight_compose >/dev/null 2>&1; then
    if docker container inspect \
      "${candidate_preflight_project}-art-ai-go-1" >/dev/null 2>&1; then
      "${candidate_preflight_compose[@]}" \
        stop --timeout 10 art-ai-go >/dev/null 2>&1 || true
      "${candidate_preflight_compose[@]}" \
        rm -f art-ai-go >/dev/null 2>&1 || true
    fi
    docker network rm \
      "${candidate_preflight_project}_default" >/dev/null 2>&1 || true
  fi
  if [[ -n "$candidate_preflight_storage_root" &&
    -d "$candidate_preflight_storage_root" &&
    "$(basename -- "$candidate_preflight_storage_root")" == \
      meshyai-go-preflight.* ]]; then
    find "$candidate_preflight_storage_root" \
      -depth -mindepth 1 -delete >/dev/null 2>&1 || true
    rmdir "$candidate_preflight_storage_root" >/dev/null 2>&1 || true
    candidate_preflight_storage_root=""
  fi
}

candidate_restore_old_web() {
  local candidate_exit_status=$?

  candidate_cleanup_preflight
  if [[ "$candidate_exit_status" -ne 0 &&
    "$candidate_old_web_stopped" == "true" ]]; then
    printf 'candidate deployment failed; restoring %s\n' \
      "$candidate_old_web_container" >&2
    docker start "$candidate_old_web_container" >/dev/null || true
  fi
  return "$candidate_exit_status"
}

trap 'candidate_restore_old_web' EXIT

if [[ ! -f "$candidate_env_file" ]]; then
  printf 'candidate environment file does not exist: %s\n' \
    "$candidate_env_file" >&2
  exit 1
fi

candidate_compose=(
  docker compose
  --env-file "$candidate_env_file"
  -p "$candidate_project"
  -f "$candidate_compose_file"
)
candidate_preflight_compose=(
  docker compose
  --env-file "$candidate_env_file"
  -p "$candidate_preflight_project"
  -f "$candidate_compose_file"
)
"${candidate_compose[@]}" config --quiet

candidate_images="$("${candidate_compose[@]}" config --images)"
if [[ "$(grep -cve '^[[:space:]]*$' <<<"$candidate_images")" -ne 1 ]]; then
  printf 'candidate topology must resolve exactly one image; got:\n%s\n' \
    "$candidate_images" >&2
  exit 1
fi
candidate_image="$(head -n 1 <<<"$candidate_images")"
docker image inspect "$candidate_image" >/dev/null

candidate_cleanup_preflight
candidate_preflight_storage_root="$(mktemp -d \
  "${TMPDIR:-/tmp}/meshyai-go-preflight.XXXXXX")"
mkdir -p \
  "${candidate_preflight_storage_root}/db" \
  "${candidate_preflight_storage_root}/blobs" \
  "${candidate_preflight_storage_root}/web-data" \
  "${candidate_preflight_storage_root}/web-storage/generated" \
  "${candidate_preflight_storage_root}/web-storage/assets" \
  "${candidate_preflight_storage_root}/legacy-cache" \
  "${candidate_preflight_storage_root}/legacy-thumbnails"
if [[ "$(id -u)" == "0" ]]; then
  chown -R 100:101 "$candidate_preflight_storage_root"
fi
GO_DB_DIR="${candidate_preflight_storage_root}/db" \
  GO_BLOB_DIR="${candidate_preflight_storage_root}/blobs" \
  GO_WEB_DATA_DIR="${candidate_preflight_storage_root}/web-data" \
  GO_WEB_STORAGE_DIR="${candidate_preflight_storage_root}/web-storage" \
  GO_LEGACY_ASSET_CACHE_DIR="${candidate_preflight_storage_root}/legacy-cache" \
  GO_LEGACY_THUMBNAIL_CACHE_DIR="${candidate_preflight_storage_root}/legacy-thumbnails" \
  GO_WEB_BIND_IP=127.0.0.1 \
  GO_WEB_HOST_PORT="$candidate_preflight_port" \
  "${candidate_preflight_compose[@]}" \
  up -d --no-build --wait \
  --wait-timeout "$candidate_wait_timeout" art-ai-go
curl --fail --silent --show-error --max-time 10 \
  "http://127.0.0.1:${candidate_preflight_port}/readyz" >/dev/null
curl --fail --silent --show-error --max-time 15 \
  "http://127.0.0.1:${candidate_preflight_port}/" >/dev/null
candidate_cleanup_preflight
printf 'candidate preflight single-container Go Web is healthy\n'

if docker container inspect "$candidate_old_web_container" >/dev/null 2>&1; then
  candidate_project_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.project={{ index .Config.Labels "com.docker.compose.project" }}' \
      "$candidate_old_web_container"
  )"
  candidate_service_label="$(
    docker container inspect \
      --format \
      'com.docker.compose.service={{ index .Config.Labels "com.docker.compose.service" }}' \
      "$candidate_old_web_container"
  )"
  if [[ "$candidate_project_label" != \
      "com.docker.compose.project=${candidate_project}" ||
    "$candidate_service_label" != \
      "com.docker.compose.service=art-ai-web" ]]; then
    printf 'refusing to stop container with unexpected labels: %s %s\n' \
      "$candidate_project_label" \
      "$candidate_service_label" >&2
    exit 1
  fi
  if [[ "$(
    docker container inspect \
      --format '{{ .State.Running }}' \
      "$candidate_old_web_container"
  )" == "true" ]]; then
    docker stop "$candidate_old_web_container" >/dev/null
    candidate_old_web_stopped="true"
  fi
fi

"${candidate_compose[@]}" \
  up -d --no-build --wait --wait-timeout "$candidate_wait_timeout" art-ai-go

candidate_old_web_stopped="false"
printf 'candidate single-container Go Web is healthy\n'
