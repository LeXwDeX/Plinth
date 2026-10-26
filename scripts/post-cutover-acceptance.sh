#!/usr/bin/env bash
set -Eeuo pipefail

acceptance_base_url="${ACCEPTANCE_BASE_URL:?ACCEPTANCE_BASE_URL is required}"
acceptance_database="${ACCEPTANCE_DATABASE:?ACCEPTANCE_DATABASE is required}"
acceptance_env="${ACCEPTANCE_ENV:?ACCEPTANCE_ENV is required}"
acceptance_baseline="${ACCEPTANCE_BASELINE_FILE:?ACCEPTANCE_BASELINE_FILE is required}"
acceptance_tmp="$("/usr/bin/mktemp" -d)"
acceptance_login_body="${acceptance_tmp}/login.json"
acceptance_login_response="${acceptance_tmp}/login-response.json"
acceptance_session_response="${acceptance_tmp}/session-response.json"
acceptance_cookie_jar="${acceptance_tmp}/cookies.txt"
acceptance_response="${acceptance_tmp}/response.json"
acceptance_health_response="${acceptance_tmp}/health.json"
acceptance_schema_response="${acceptance_tmp}/schema.json"
acceptance_version_headers="${acceptance_tmp}/version.headers"
acceptance_ui_response="${acceptance_tmp}/index.html"
acceptance_web_asset="${acceptance_tmp}/web-asset.bin"
acceptance_download="${acceptance_tmp}/asset.bin"
acceptance_download_headers="${acceptance_tmp}/asset.headers"

validate_http_origin() {
  local name="$1"
  local value="$2"
  local authority
  local host
  local port

  [[ "$value" == http://* || "$value" == https://* ]] || {
    printf '%s must use http:// or https://\n' "$name" >&2
    return 1
  }
  [[ "$value" != *['@?#']* ]] || {
    printf '%s must not contain userinfo, query, or fragment\n' "$name" >&2
    return 1
  }
  authority="${value#http://}"
  authority="${authority#https://}"
  [[ -n "$authority" && "$authority" != *"/"* ]] || {
    printf '%s must be an HTTP(S) origin without a path\n' "$name" >&2
    return 1
  }
  host="${authority%%:*}"
  port=""
  if [[ "$authority" == *:* ]]; then
    port="${authority##*:}"
    [[ -n "$port" ]] || return 1
    [[ "$authority" != *:*:* ]] || return 1
  fi
  [[ "$host" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ &&
    "$host" != *..* ]] || return 1
  if [[ -n "$port" ]]; then
    [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || return 1
    ((port <= 65535)) || return 1
  fi
}

cleanup_acceptance() {
  /bin/rm -f \
    "$acceptance_login_body" \
    "$acceptance_login_response" \
    "$acceptance_session_response" \
    "$acceptance_cookie_jar" \
    "$acceptance_response" \
    "$acceptance_health_response" \
    "$acceptance_schema_response" \
    "$acceptance_version_headers" \
    "$acceptance_ui_response" \
    "$acceptance_web_asset" \
    "$acceptance_download" \
    "$acceptance_download_headers"
  /bin/rmdir "$acceptance_tmp"
}
report_acceptance_failure() {
  local acceptance_status=$?
  printf 'post-cutover acceptance failed at line %s (status=%s)\n' \
    "${BASH_LINENO[0]:-unknown}" \
    "$acceptance_status" >&2
  return "$acceptance_status"
}
trap report_acceptance_failure ERR
trap cleanup_acceptance EXIT
/bin/chmod 700 "$acceptance_tmp"
umask 077

[[ -f "$acceptance_env" && ! -L "$acceptance_env" ]]
[[ -f "$acceptance_database" && ! -L "$acceptance_database" ]]
[[ -f "$acceptance_baseline" && ! -L "$acceptance_baseline" ]]
[[ "$(stat -c '%a' "$acceptance_baseline")" == "600" ]]
acceptance_expected_users="$(/usr/bin/jq -er '.users | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_generations="$(/usr/bin/jq -er '.generations | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_usage_tasks="$(/usr/bin/jq -er '.usage_tasks | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_usage_credits="$(/usr/bin/jq -er '.usage_credits | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_audit_minimum="$(/usr/bin/jq -er '.audit_minimum | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_output_assets="$(/usr/bin/jq -er '.output_assets | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_input_assets="$(/usr/bin/jq -er '.input_assets | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_chat_sessions="$(/usr/bin/jq -er '.chat_sessions | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_chat_messages="$(/usr/bin/jq -er '.chat_messages | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_canvas_projects="$(/usr/bin/jq -er '.canvas_projects | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_expected_canvas_nodes="$(/usr/bin/jq -er '.canvas_nodes | select(type == "number" and . >= 0)' "$acceptance_baseline")"
acceptance_external_webhook_url="$(/usr/bin/jq -er '.external_webhook_url | select(type == "string")' "$acceptance_baseline")"
validate_http_origin "ACCEPTANCE_BASE_URL" "$acceptance_base_url"
validate_http_origin \
  "external_webhook_url" \
  "$acceptance_external_webhook_url"
set -a
# shellcheck disable=SC1090
source "$acceptance_env"
set +a
: "${ADMIN_USERNAME:?ADMIN_USERNAME is required}"
: "${ADMIN_PASSWORD:?ADMIN_PASSWORD is required}"
: "${MESHY_WEBHOOK_PATH_SECRET:?MESHY_WEBHOOK_PATH_SECRET is required}"

acceptance_health_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_health_response" \
    -w '%{http_code}' \
    "${acceptance_base_url}/api/health"
)"
[[ "$acceptance_health_status" == "200" ]]
/usr/bin/jq -e \
  '
    .status == "ok"
    and (.service | type == "string" and length > 0)
    and (.version | type == "string" and length > 0)
    and (.timestamp | type == "string" and length > 0)
  ' "$acceptance_health_response" >/dev/null

acceptance_version_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -D "$acceptance_version_headers" \
    -w '%{http_code}' \
    "${acceptance_base_url}/api/version"
)"
[[ "$acceptance_version_status" == "200" ]]
/usr/bin/grep -Eiq \
  '^x-art-ai-runtime:[[:space:]]*go[[:space:]]*$' \
  "$acceptance_version_headers"
/usr/bin/jq -e \
  '.version | type == "string" and length > 0' \
  "$acceptance_response" >/dev/null

acceptance_schema_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_schema_response" \
    -w '%{http_code}' \
    "${acceptance_base_url}/api/v1/schema"
)"
[[ "$acceptance_schema_status" == "200" ]]
/usr/bin/jq -e \
  '
    (.schema.runtime | has("configuration") | not)
    and (.schema.capabilities | type == "array" and length > 0)
  ' "$acceptance_schema_response" >/dev/null
if /usr/bin/grep -Eq 'APP_ENV|AUTH_TOKENS_JSON' \
  "$acceptance_schema_response"; then
  printf 'public schema exposed server environment bindings\n' >&2
  exit 1
fi

acceptance_ui_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_ui_response" \
    -w '%{http_code}' \
    "${acceptance_base_url}/"
)"
[[ "$acceptance_ui_status" == "200" ]]
acceptance_web_asset_prefix="$(
  /usr/bin/jq -er \
    '
      .schema.runtime.frontend.web_asset_prefix
      | select(type == "string" and startswith("/") and endswith("/"))
    ' "$acceptance_schema_response"
)"
mapfile -t acceptance_web_asset_paths < <(
  /usr/bin/grep -Eo '(src|href)="[^"]+"' "$acceptance_ui_response" |
    /usr/bin/sed -E 's/^(src|href)="([^"]+)"$/\2/' |
    /usr/bin/awk -v prefix="$acceptance_web_asset_prefix" \
      'index($0, prefix) == 1'
)
[[ "${#acceptance_web_asset_paths[@]}" -ge 2 ]]
for acceptance_web_asset_path in "${acceptance_web_asset_paths[@]}"; do
  acceptance_web_asset_status="$(
    /usr/bin/curl --silent --show-error --max-time 15 \
      -o "$acceptance_web_asset" \
      -w '%{http_code}' \
      "${acceptance_base_url}${acceptance_web_asset_path}"
  )"
  [[ "$acceptance_web_asset_status" == "200" ]]
  [[ -s "$acceptance_web_asset" ]]
done

acceptance_webhook_internal_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    "${acceptance_base_url}/${MESHY_WEBHOOK_PATH_SECRET}/wbh"
)"
[[ "$acceptance_webhook_internal_status" == "200" ]]
acceptance_webhook_external_status="$(
  /usr/bin/curl --silent --show-error --max-time 20 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -X POST \
    "${acceptance_external_webhook_url%/}/${MESHY_WEBHOOK_PATH_SECRET}/wbh/"
)"
if [[ "$acceptance_webhook_external_status" != "401" ]]; then
  printf 'external webhook unauthorized probe status=%s\n' \
    "$acceptance_webhook_external_status" >&2
  exit 1
fi

/usr/bin/jq -nc \
  --arg username "$ADMIN_USERNAME" \
  --arg password "$ADMIN_PASSWORD" \
  '{username:$username,password:$password}' >"$acceptance_login_body"

acceptance_login_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_login_response" \
    -w '%{http_code}' \
    -c "$acceptance_cookie_jar" \
    -X POST \
    -H 'Content-Type: application/json' \
    --data-binary "@${acceptance_login_body}" \
    "${acceptance_base_url}/api/auth/login"
)"
[[ "$acceptance_login_status" == "200" ]]
/usr/bin/jq -e \
  '.success == true and (.user.id | type == "string" and length > 0)' \
  "$acceptance_login_response" >/dev/null

acceptance_session_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_session_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/auth/session"
)"
[[ "$acceptance_session_status" == "200" ]]
/usr/bin/jq -e \
  '.isAdmin == true and (.user.id | type == "string" and length > 0)' \
  "$acceptance_session_response" >/dev/null

acceptance_provider_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/providers?capability=text-to-image&projection=extended"
)"
[[ "$acceptance_provider_status" == "200" ]]
acceptance_provider_count="$(
  /usr/bin/jq -er \
    '.providers | select(type == "array") | length' \
    "$acceptance_response"
)"
[[ "$acceptance_provider_count" -gt 0 ]]
acceptance_capability_provider_counts=()
for acceptance_capability in \
  text-to-image \
  image-to-image \
  text-to-3d \
  image-to-3d \
  remesh \
  retexture \
  rigging \
  animation \
  background-removal; do
  acceptance_capability_status="$(
    /usr/bin/curl --silent --show-error --max-time 15 \
      -o "$acceptance_response" \
      -w '%{http_code}' \
      -b "$acceptance_cookie_jar" \
      "${acceptance_base_url}/api/providers?capability=${acceptance_capability}&projection=extended"
  )"
  [[ "$acceptance_capability_status" == "200" ]]
  acceptance_capability_provider_count="$(
    /usr/bin/jq -er \
      '.providers | select(type == "array") | length' \
      "$acceptance_response"
  )"
  [[ "$acceptance_capability_provider_count" -gt 0 ]]
  acceptance_capability_provider_counts+=(
    "${acceptance_capability}:${acceptance_capability_provider_count}"
  )
done
acceptance_capability_provider_summary="$(
  IFS=,
  printf '%s' "${acceptance_capability_provider_counts[*]}"
)"

acceptance_users_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/admin/users"
)"
[[ "$acceptance_users_status" == "200" ]]
acceptance_user_total="$(
  /usr/bin/jq -er '.data | select(type == "array") | length' \
    "$acceptance_response"
)"
[[ "$acceptance_user_total" == "$acceptance_expected_users" ]]

acceptance_generations_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/admin/generations?page=1&pageSize=1"
)"
[[ "$acceptance_generations_status" == "200" ]]
acceptance_generation_total="$(
  /usr/bin/jq -er '.total | select(type == "number")' \
    "$acceptance_response"
)"
[[ "$acceptance_generation_total" == "$acceptance_expected_generations" ]]

acceptance_usage_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/admin/usage?days=1000"
)"
[[ "$acceptance_usage_status" == "200" ]]
acceptance_usage_tasks="$(
  /usr/bin/jq -er \
    '[.data[].totalTasks] | add // 0' \
    "$acceptance_response"
)"
acceptance_usage_credits="$(
  /usr/bin/jq -er \
    '[.data[].estimatedCredits] | add // 0' \
    "$acceptance_response"
)"
[[ "$acceptance_usage_tasks" == "$acceptance_expected_usage_tasks" ]]
[[ "$acceptance_usage_credits" == "$acceptance_expected_usage_credits" ]]

acceptance_audit_status="$(
  /usr/bin/curl --silent --show-error --max-time 15 \
    -o "$acceptance_response" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/admin/audit-logs?page=1&pageSize=1"
)"
[[ "$acceptance_audit_status" == "200" ]]
acceptance_audit_total="$(
  /usr/bin/jq -er '.total | select(type == "number")' \
    "$acceptance_response"
)"
[[ "$acceptance_audit_total" -ge "$acceptance_expected_audit_minimum" ]]

acceptance_database_counts="$(
  /usr/bin/sqlite3 -readonly -separator '|' "$acceptance_database" \
    'SELECT
       (SELECT count(*) FROM generation_assets),
       (SELECT count(*) FROM generation_input_assets),
       (SELECT count(*) FROM go_chat_sessions),
       (SELECT count(*) FROM go_chat_messages),
       (SELECT count(*) FROM go_canvas_projects),
       (SELECT count(*) FROM go_canvas_nodes);'
)"
IFS='|' read -r \
  acceptance_output_assets \
  acceptance_input_assets \
  acceptance_chat_sessions \
  acceptance_chat_messages \
  acceptance_canvas_projects \
  acceptance_canvas_nodes <<<"$acceptance_database_counts"
[[ "$acceptance_output_assets" == "$acceptance_expected_output_assets" ]]
[[ "$acceptance_input_assets" == "$acceptance_expected_input_assets" ]]
[[ "$acceptance_chat_sessions" == "$acceptance_expected_chat_sessions" ]]
[[ "$acceptance_chat_messages" == "$acceptance_expected_chat_messages" ]]
[[ "$acceptance_canvas_projects" == "$acceptance_expected_canvas_projects" ]]
[[ "$acceptance_canvas_nodes" == "$acceptance_expected_canvas_nodes" ]]

acceptance_asset_row="$(
  /usr/bin/sqlite3 -readonly -separator '|' "$acceptance_database" \
    "SELECT a.task_id, a.sha256
     FROM generation_assets AS a
     JOIN (
       SELECT task_id
       FROM generation_assets
       GROUP BY task_id
       HAVING count(*) = 1
          AND max(size_bytes) BETWEEN 1 AND 10485760
       ORDER BY task_id
       LIMIT 1
     ) AS candidate ON candidate.task_id = a.task_id
     WHERE length(a.sha256) = 64;"
)"
IFS='|' read -r acceptance_asset_task_id acceptance_asset_sha256 \
  <<<"$acceptance_asset_row"
[[ -n "$acceptance_asset_task_id" ]]
[[ "$acceptance_asset_sha256" =~ ^[0-9a-f]{64}$ ]]
acceptance_download_status="$(
  /usr/bin/curl --silent --show-error --max-time 30 \
    -o "$acceptance_download" \
    -D "$acceptance_download_headers" \
    -w '%{http_code}' \
    -b "$acceptance_cookie_jar" \
    "${acceptance_base_url}/api/admin/generations/${acceptance_asset_task_id}/download"
)"
[[ "$acceptance_download_status" == "200" ]]
[[ -s "$acceptance_download" ]]
/usr/bin/grep -Eiq '^content-disposition:[[:space:]]*attachment;' \
  "$acceptance_download_headers"
acceptance_download_sha256="$(
  /usr/bin/sha256sum "$acceptance_download" |
    /usr/bin/awk '{print $1}'
)"
[[ "$acceptance_download_sha256" == "$acceptance_asset_sha256" ]]
acceptance_download_bytes="$(
  /usr/bin/wc -c <"$acceptance_download" |
    /usr/bin/tr -d '[:space:]'
)"

printf '%s\n' \
  "health_status=${acceptance_health_status}" \
  "version_status=${acceptance_version_status}" \
  "version_runtime=go" \
  "schema_status=${acceptance_schema_status}" \
  "schema_server_configuration_exposed=false" \
  "ui_status=${acceptance_ui_status}" \
  "ui_web_assets_verified=${#acceptance_web_asset_paths[@]}" \
  "webhook_internal_status=${acceptance_webhook_internal_status}" \
  "webhook_external_unauthorized_status=${acceptance_webhook_external_status}" \
  "login_status=${acceptance_login_status}" \
  "session_status=${acceptance_session_status}" \
  "provider_status=${acceptance_provider_status}" \
  "provider_count=${acceptance_provider_count}" \
  "capability_provider_counts=${acceptance_capability_provider_summary}" \
  "users_status=${acceptance_users_status}" \
  "users_total=${acceptance_user_total}" \
  "generations_status=${acceptance_generations_status}" \
  "generations_total=${acceptance_generation_total}" \
  "usage_status=${acceptance_usage_status}" \
  "usage_tasks=${acceptance_usage_tasks}" \
  "usage_credits=${acceptance_usage_credits}" \
  "audit_status=${acceptance_audit_status}" \
  "audit_total=${acceptance_audit_total}" \
  "output_assets=${acceptance_output_assets}" \
  "input_assets=${acceptance_input_assets}" \
  "chat_sessions=${acceptance_chat_sessions}" \
  "chat_messages=${acceptance_chat_messages}" \
  "canvas_projects=${acceptance_canvas_projects}" \
  "canvas_nodes=${acceptance_canvas_nodes}" \
  "asset_download_status=${acceptance_download_status}" \
  "asset_download_bytes=${acceptance_download_bytes}" \
  "asset_download_sha256_match=true"
