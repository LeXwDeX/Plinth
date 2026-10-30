#!/usr/bin/env bash
set -Eeuo pipefail

candidate_web_base_url="${CANDIDATE_WEB_BASE_URL:-http://127.0.0.1:13335}"
candidate_owner="${CANDIDATE_OWNER:-candidate-admin}"
candidate_model="${CANDIDATE_MODEL:-gpt-image-2.5-sunburst}"
candidate_admin_username="${CANDIDATE_ADMIN_USERNAME:-candidate-admin}"
candidate_admin_password="${CANDIDATE_ADMIN_PASSWORD:-candidate-only-password}"
candidate_allow_generation="${CANDIDATE_ALLOW_GENERATION:-false}"
candidate_webhook_path_secret="${CANDIDATE_WEBHOOK_PATH_SECRET:-}"
candidate_webhook_key="${CANDIDATE_WEBHOOK_KEY:-}"
candidate_managed_username="candidate-user-${BASHPID:-$$}-${RANDOM}"
candidate_managed_password="candidate-user-password"
candidate_repository_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"
candidate_admin_display_name="${CANDIDATE_ADMIN_DISPLAY_NAME:-$(
  jq -er \
    '.application.runtime.configuration.settings.admin_display_name.default' \
    "${candidate_repository_root}/internal/catalog/default_catalog.json"
)}"
candidate_admin_email="${CANDIDATE_ADMIN_EMAIL:-$(
  jq -er \
    '.application.runtime.configuration.settings.admin_email.default' \
    "${candidate_repository_root}/internal/catalog/default_catalog.json"
)}"
candidate_webp_fixture="${CANDIDATE_WEBP_FIXTURE:-${candidate_repository_root}/public/gpt-skills/_placeholder.webp}"
candidate_asset_file="$(mktemp)"
candidate_legacy_image_file="$(mktemp)"
candidate_model_asset_file="$(mktemp)"
candidate_input_asset_file="$(mktemp)"
candidate_canvas_merge_file="$(mktemp)"
candidate_version_headers_file="$(mktemp)"
candidate_deleted_item_file="$(mktemp)"
candidate_auth_response_file="$(mktemp)"
candidate_cookie_jar="$(mktemp)"
candidate_chat_stream_file="$(mktemp)"
candidate_task_stream_file="$(mktemp)"
candidate_task_stream_headers_file="$(mktemp)"
candidate_admin_generation_download_file="$(mktemp)"
candidate_admin_generation_download_headers_file="$(mktemp)"
candidate_generated_task_ids_file="$(mktemp)"
candidate_webhook_response_file="$(mktemp)"
candidate_ui_file="$(mktemp)"
candidate_failure_line=""

record_candidate_failure() {
  candidate_failure_line="$1"
}

candidate_capability_route() {
  local candidate_capability="$1"
  local candidate_legacy_route

  candidate_legacy_route="$(
    jq -er \
      --arg capability "$candidate_capability" \
      '
        .schema.capabilities[]
        | select(.id == $capability)
        | .legacy_api.path
      ' <<<"$candidate_schema_json"
  )"
  printf '%s\n' "$candidate_legacy_route"
}

candidate_assert_png_file() {
  local candidate_label="$1"
  local candidate_file="$2"
  local candidate_signature

  candidate_signature="$(
    od -An -tx1 -N8 "$candidate_file" |
      tr -d '[:space:]'
  )"
  if [[ "$candidate_signature" != "89504e470d0a1a0a" ]]; then
    printf '%s is not a PNG asset; signature=%s\n' \
      "$candidate_label" \
      "$candidate_signature" >&2
    return 1
  fi
}

cleanup_candidate_generated_tasks() {
  local generated_task_id

  [[ -s "$candidate_generated_task_ids_file" ]] || return 0
  [[ -n "${candidate_text_to_image_route:-}" ]] || return 0
  while IFS= read -r generated_task_id; do
    [[ -n "$generated_task_id" ]] || continue
    curl --silent --show-error --max-time 5 \
      -X DELETE \
      -b "$candidate_cookie_jar" \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}${candidate_text_to_image_route}/${generated_task_id}" \
      >/dev/null 2>&1 || true
  done <"$candidate_generated_task_ids_file"
}

cleanup_candidate_resources() {
  local candidate_exit_status=$?

  if [[ "$candidate_exit_status" -ne 0 ]]; then
    printf 'candidate acceptance failed at line %s\n' \
      "${candidate_failure_line:-unknown}" >&2
  fi

  cleanup_candidate_generated_tasks
  if [[ -n "${candidate_chat_session_id:-}" ]]; then
    curl --silent --show-error --max-time 5 \
      -X DELETE \
      -b "$candidate_cookie_jar" \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}" \
      >/dev/null 2>&1 || true
  fi
  if [[ -n "${candidate_managed_user_id:-}" ]]; then
    curl --silent --show-error --max-time 5 \
      -b "$candidate_cookie_jar" \
      -X DELETE \
      "${candidate_web_base_url}/api/admin/users/${candidate_managed_user_id}" \
      >/dev/null 2>&1 || true
  fi
  if [[ -n "${candidate_canvas_project_id:-}" ]]; then
    curl --silent --show-error --max-time 5 \
      -b "$candidate_cookie_jar" \
      -X DELETE \
      "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}" \
      >/dev/null 2>&1 || true
  fi
  if [[ -n "${candidate_task_id:-}" &&
    -n "${candidate_text_to_image_route:-}" ]]; then
    curl --silent --show-error --max-time 5 \
      -b "$candidate_cookie_jar" \
      -X DELETE \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}${candidate_text_to_image_route}/${candidate_task_id}" \
      >/dev/null 2>&1 || true
  fi
  if [[ -n "${candidate_3d_task_id:-}" &&
    -n "${candidate_text_to_3d_route:-}" ]]; then
    curl --silent --show-error --max-time 5 \
      -b "$candidate_cookie_jar" \
      -X DELETE \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}${candidate_text_to_3d_route}/${candidate_3d_task_id}" \
      >/dev/null 2>&1 || true
  fi

  rm -f \
    "$candidate_asset_file" \
    "$candidate_legacy_image_file" \
    "$candidate_model_asset_file" \
    "$candidate_input_asset_file" \
    "$candidate_canvas_merge_file" \
    "$candidate_version_headers_file" \
    "$candidate_deleted_item_file" \
    "$candidate_auth_response_file" \
    "$candidate_cookie_jar" \
    "$candidate_chat_stream_file" \
    "$candidate_task_stream_file" \
    "$candidate_task_stream_headers_file" \
    "$candidate_admin_generation_download_file" \
    "$candidate_admin_generation_download_headers_file" \
    "$candidate_generated_task_ids_file" \
    "$candidate_webhook_response_file" \
    "$candidate_ui_file"

  return "$candidate_exit_status"
}

verify_candidate_logout() {
  local candidate_logout_json
  candidate_logout_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -b "$candidate_cookie_jar" \
      -H "Origin: ${candidate_web_base_url}" \
      -X DELETE "${candidate_web_base_url}/api/auth/session"
  )"
  jq -e '.success == true' <<<"$candidate_logout_json" >/dev/null
}

trap 'record_candidate_failure "$LINENO"' ERR
trap 'cleanup_candidate_resources' EXIT
printf 'candidate schema-driven asset' >"$candidate_input_asset_file"

candidate_version_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -D "$candidate_version_headers_file" \
    "${candidate_web_base_url}/api/version"
)"
grep -Eiq '^x-art-ai-runtime:[[:space:]]*go[[:space:]]*$' "$candidate_version_headers_file"
jq -er '.version | select(test("^[0-9]{8}\\([0-9]+\\)$"))' \
  <<<"$candidate_version_json" >/dev/null

candidate_schema_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    "${candidate_web_base_url}/api/v1/schema"
)"
candidate_home_path="$(
  jq -er \
    '.schema.runtime.frontend.client_routes.home
     | select(type == "string" and startswith("/"))' \
    <<<"$candidate_schema_json"
)"
candidate_login_path="$(
  jq -er \
    '.schema.runtime.frontend.client_routes.login
     | select(type == "string" and startswith("/"))' \
    <<<"$candidate_schema_json"
)"
candidate_web_asset_prefix="$(
  jq -er \
    '.schema.runtime.frontend.web_asset_prefix
     | select(type == "string" and startswith("/") and endswith("/"))' \
    <<<"$candidate_schema_json"
)"
candidate_favicon_path="$(
  jq -er \
    '.schema.runtime.frontend.favicon_path
     | select(type == "string" and startswith("/"))' \
    <<<"$candidate_schema_json"
)"
curl --fail-with-body --silent --show-error --max-time 10 \
  -o "$candidate_ui_file" \
  "${candidate_web_base_url}${candidate_favicon_path}"
[[ -s "$candidate_ui_file" ]]
curl --fail-with-body --silent --show-error --max-time 15 \
  -o "$candidate_ui_file" \
  "${candidate_web_base_url}${candidate_home_path}"
candidate_web_asset_paths="$(
  grep -Eo '(src|href)="[^"]+"' "$candidate_ui_file" |
    sed -E 's/^(src|href)="([^"]+)"$/\2/' |
    awk -v prefix="$candidate_web_asset_prefix" \
      'index($0, prefix) == 1'
)"
if [[ "$(wc -l <<<"$candidate_web_asset_paths" | tr -d '[:space:]')" -lt 2 ]]; then
  printf 'candidate home page did not reference versioned browser assets\n' >&2
  exit 1
fi
while IFS= read -r candidate_web_asset_path; do
  [[ -n "$candidate_web_asset_path" ]] || continue
  curl --fail-with-body --silent --show-error --max-time 15 \
    -o "$candidate_ui_file" \
    "${candidate_web_base_url}${candidate_web_asset_path}"
  [[ -s "$candidate_ui_file" ]]
done <<<"$candidate_web_asset_paths"
candidate_webhook_route="$(
  jq -er '.schema.provider_webhooks[0].route' \
    <<<"$candidate_schema_json"
)"
candidate_webhook_path_parameter="$(
  jq -er '.schema.provider_webhooks[0].path_parameter' \
    <<<"$candidate_schema_json"
)"
candidate_webhook_key_header="$(
  jq -er '.schema.provider_webhooks[0].key_headers[0]' \
    <<<"$candidate_schema_json"
)"
candidate_webhook_job_id_field="$(
  jq -er '.schema.provider_webhooks[0].payload_job_id_fields[0]' \
    <<<"$candidate_schema_json"
)"
candidate_webhook_api_json="$(
  jq -ec '.schema.provider_webhooks[0].api' \
    <<<"$candidate_schema_json"
)"
candidate_webhook_placeholder="{${candidate_webhook_path_parameter}}"
candidate_text_to_image_route="$(
  candidate_capability_route text-to-image
)"
candidate_text_to_3d_route="$(
  candidate_capability_route text-to-3d
)"
jq -e '
  .schema.revision
  and (.schema.capabilities | length == 9)
  and (.schema.authentication.login.title == "Plinth")
  and (.schema.provider_webhooks | length >= 1)
  and (.schema.provider_webhooks[0].id == "meshy-task-status")
  and (.schema.provider_webhooks[0].provider_id == "meshy")
  and (.schema.provider_webhooks[0].route == "/{webhookSecret}/wbh")
  and (.schema.provider_webhooks[0].path_secret_setting == "meshy_webhook_path_secret")
  and (.schema.provider_webhooks[0].key_setting == "meshy_webhook_key")
  and (.schema.provider_webhooks[0].payload_job_id_fields == ["id", "task_id"])
  and (.schema.provider_webhooks[0].api.fields.received == "received")
  and (.schema.provider_webhooks[0].api.values.ok == "ok")
  and (.schema.provider_webhooks[0].api.statuses.ok == 200)
  and (.schema.runtime.http.default_page_size > 0)
  and (.schema.runtime.http.legacy_routes.asset_upload == "/api/assets/upload")
  and (.schema.runtime.http.legacy_routes.cache_status == "/api/cache-status")
  and (.schema.runtime.http.legacy_routes.provider_balance == "/api/meshy/balance")
  and (.schema.runtime.http.legacy_routes.canvas_projects == "/api/canvas/projects")
  and (.schema.runtime.http.legacy_routes.canvas_project == "/api/canvas/projects/{projectID}")
  and (.schema.runtime.http.legacy_routes.canvas_nodes == "/api/canvas/projects/{projectID}/nodes")
  and (.schema.runtime.http.legacy_routes.canvas_merge == "/api/canvas/merge-images")
  and any(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . == "canvas_merge"
  )
  and (.schema.runtime.http.legacy_routes.task_stream == "/api/meshy/stream/{type}/{taskID}")
  and (.schema.runtime.http.legacy_routes.admin_users == "/api/admin/users")
  and (.schema.runtime.http.legacy_routes.admin_user == "/api/admin/users/{userID}")
  and (.schema.runtime.http.legacy_routes.admin_audit_logs == "/api/admin/audit-logs")
  and (.schema.runtime.http.legacy_routes.admin_generations == "/api/admin/generations")
  and (.schema.runtime.http.legacy_routes.admin_generation == "/api/admin/generations/{taskID}")
  and (.schema.runtime.http.legacy_routes.admin_generation_download == "/api/admin/generations/{taskID}/download")
  and (.schema.runtime.http.legacy_routes.admin_usage == "/api/admin/usage")
  and (.schema.runtime.http.legacy_routes.admin_balance_summary == "/api/admin/balance-summary")
  and (.schema.runtime.http.legacy_routes.admin_user_assets == "/api/admin/user-assets/{userID}")
  and (.schema.runtime.http.legacy_routes.admin_backfill_status == "/api/admin/backfill-status")
  and (.schema.runtime.http.legacy_routes | has("auth_sso_redirect") | not)
  and (.schema.runtime.http.legacy_routes | has("auth_sso_callback") | not)
  and (.schema.runtime.http.legacy_routes | has("auth_sso_logout") | not)
  and any(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . == "admin_backfill_status"
  )
  and all(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . != "auth_sso_redirect"
  )
  and all(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . != "auth_sso_callback"
  )
  and all(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . != "auth_sso_logout"
  )
  and any(
    .schema.runtime.http.candidate_proxy.legacy_route_keys[];
    . == "meshy_proxy_asset"
  )
  and (.schema.authentication | has("sso") | not)
  and (.schema.authentication.login.redirect_query == "redirect")
  and (.schema.administration.usage.api.fields.data == "data")
  and (.schema.administration.usage.api.fields.estimated_credits == "estimatedCredits")
  and (.schema.administration.usage.balance.account_id == "meshy")
  and (.schema.administration.user_assets.api.envelope_fields.user == "user")
  and (.schema.administration.user_assets.api.envelope_fields.summary == "summary")
  and (.schema.administration.user_assets.api.page_fields.page_size == "pageSize")
  and (.schema.administration.user_assets.api.query.page_size == "pageSize")
  and (.schema.administration.user_assets.api.path_parameters.user_id == "userID")
  and (.schema.administration.user_assets.permission == "generations.manage")
  and (.schema.administration.user_assets.limits.frontend_page_size == 20)
  and any(
    .schema.administration.user_assets.filters.download_actions[];
    .action == "download_asset" and .label == "下载"
  )
  and (.schema.administration.user_assets.presentation.text.uploads_title == "上传的文件")
  and (.schema.administration.backfill_status.api.fields.sync_fixed == "syncFixed")
  and (.schema.administration.backfill_status.api.fields.async_fixed == "asyncFixed")
  and (.schema.administration.backfill_status.permission == "generations.manage")
  and (.schema.administration.backfill_status.statuses.pending == "PENDING")
  and (.schema.administration.backfill_status.statuses.succeeded == "SUCCEEDED")
  and (.schema.administration.backfill_status.sync_provider_ids == ["openai", "gemini"])
  and (.schema.administration.backfill_status.cache_result_fields.model_urls == "model_urls")
  and (.schema.administration.backfill_status.messages.no_pending == "没有需要回填的 PENDING 记录")
  and (.schema.runtime.http.legacy_routes.meshy_proxy_asset == "/api/meshy/proxy-asset")
  and (.schema.runtime.asset_delivery.proxy.api.query.url == "url")
  and (.schema.runtime.asset_delivery.proxy.api.statuses.forbidden == 403)
  and (.schema.runtime.asset_delivery.proxy.cache.status_header == "X-Cache")
  and (.schema.runtime.asset_delivery.proxy.thumbnail.request_value == "thumb")
  and (.schema.runtime.asset_delivery.proxy.messages.missing_url == "缺少 url 参数")
  and (.schema.runtime.asset_delivery.proxy.messages.forbidden_host == "不允许访问的域名")
  and any(
    .schema.runtime.asset_delivery.proxy.security.allowed_hosts[];
    . == "assets.meshy.ai"
  )
  and ([.schema.runtime.http.legacy_provider_balance.accounts[].account_id] | sort == ["meshy", "tripo"])
  and (.schema.runtime as $runtime
    | all($runtime.http.legacy_provider_balance.accounts[];
      .account_id as $account_id
      | $runtime.provider_accounts[$account_id].provider_id == $account_id
      and $runtime.provider_accounts[$account_id].balance.adapter_key == "http-json"))
  and (.schema.canvas.api.fields.name == "name")
  and (.schema.canvas.api.fields.node_count == "_count")
  and (.schema.canvas.defaults.project_name == "未命名项目")
  and (.schema.canvas.defaults.node_width == 256)
  and (.schema.canvas.audit.create_action == "create_task")
  and (.schema.canvas.audit.delete_action == "delete_task")
  and (.schema.canvas.audit.merge_action == "upload_file")
  and (.schema.canvas.audit.project_resource_type == "canvas_project")
  and (.schema.canvas.frontend.auto_save_delay_milliseconds == 1500)
  and (.schema.canvas.presentation.text["project.list.new"] == "新建画板")
  and (.schema.task_stream.api.fields.task_id == "id")
  and (.schema.task_stream.events.message == "message")
  and (.schema.administration.users.api.fields.username == "username")
  and (.schema.administration.users.api.fields.is_admin == "isAdmin")
  and (.schema.administration.users.permission == "users.manage")
  and (.schema.administration.users.limits.username_minimum_characters == 2)
  and (.schema.administration.users.limits.password_minimum_characters == 6)
  and (.schema.administration.users.presentation.text.list_title == "用户列表")
  and (.schema.administration.audit_logs.api.fields.user_id == "userId")
  and (.schema.administration.audit_logs.api.query.page_size == "pageSize")
  and (.schema.administration.audit_logs.permission == "audit.read")
  and (.schema.administration.audit_logs.defaults.id_prefix == "audit_")
  and (.schema.administration.audit_logs.request_headers.forwarded_for == "X-Forwarded-For")
  and (.schema.administration.audit_logs.write_policy.maximum_attempts == 2)
  and (.schema.administration.audit_logs.write_policy.retry_delay_milliseconds == 1000)
  and (.schema.administration.audit_logs.limits.default_page_size == 50)
  and (.schema.administration.audit_logs.limits.maximum_page_size == 100)
  and (.schema.administration.audit_logs.presentation.text.title == "操作日志")
  and (.schema.administration.generations.api.fields.task_id == "taskId")
  and (.schema.administration.generations.api.query.page_size == "pageSize")
  and (.schema.administration.generations.permission == "generations.manage")
  and (.schema.administration.generations.limits.default_page_size == 50)
  and (.schema.administration.generations.assets.thumbnail_role == "thumbnail")
  and (.schema.administration.generations.assets.legacy_cache_root_setting == "legacy_asset_cache_root")
  and (.schema.administration.generations.assets.legacy_cache_prefix_characters == 2)
  and (.schema.administration.generations.assets.legacy_cache_metadata_suffix == ".meta.json")
  and (.schema.administration.generations.assets.legacy_cache_content_type_field == "contentType")
  and (.schema.administration.generations.assets.remote_download_timeout_milliseconds > 0)
  and (.schema.administration.generations.assets.maximum_remote_redirects > 0)
  and (.schema.administration.generations.assets.remote_thumbnail_url_query == "url")
  and (.schema.administration.generations.presentation.user_id_preview_characters == 8)
  and (.schema.administration.generations.presentation.text.title == "生成记录")
  and any(
    .schema.task_stream.aliases[];
    .external_type == "text-to-image"
    and .capability == "text-to-image"
  )
  and (.schema.runtime.cache.fields.active_downloads == "activeDownloads")
  and (.schema.runtime.cache.fields.total_cache_size_mb == "totalCacheSizeMB")
  and (.schema.runtime.input_assets.upload_max_bytes == 10485760)
  and (.schema.runtime.input_assets.stored_content_policy == "preserve-source")
  and (.schema.runtime.input_assets.legacy_storage_root_setting == "legacy_input_assets_root")
  and (.schema.runtime.input_assets.api.fields.asset_id == "asset_id")
  and (.schema.runtime.input_assets.api.fields.media_type == "mime_type")
  and (.schema.runtime.input_assets.api.fields.data_uri == "data_uri")
  and (.schema.runtime.input_assets.audit.upload_action == "upload_file")
  and (.schema.runtime.input_assets.audit.access_action == "access_asset")
  and (.schema.runtime.task_audit.create_action == "create_task")
  and (.schema.runtime.task_audit.delete_action == "delete_task")
  and (.schema.runtime.task_audit.read_other_action == "admin_read_task")
  and (.schema.runtime.task_audit.resource_type == "task")
  and (.schema.runtime.task_deletion.local_record_policy == "tombstone")
  and (.schema.runtime.task_deletion.upstream_policy == "when-supported")
  and (.schema.runtime.task_deletion.audit_event_type == "task.deleted")
  and (.schema.runtime.asset_delivery.task_asset_audit.download_action == "download_asset")
  and any(
    .schema.capabilities[];
    .id == "animation"
    and .legacy_api.path == "/api/meshy/animations"
    and .legacy_api.result_field == "result"
    and .legacy_api.task_aliases[0].name == "rig_task_id"
    and .legacy_api.task_aliases[0].target_parameter == "source_task"
    and .legacy_api.result_assets[0].name == "animation_glb_url"
    and .legacy_api.result_assets[0].variant == "animation_glb_url"
  )
  and (.schema.runtime | has("configuration") | not)
' <<<"$candidate_schema_json" >/dev/null
if grep -Eq 'APP_ENV|AUTH_TOKENS_JSON' <<<"$candidate_schema_json"; then
  printf 'public Product Schema exposed server environment wiring\n' >&2
  exit 1
fi

candidate_ui_routes="$(
  jq -r '
    [
      .schema.content.navigation[].href,
      .schema.administration.navigation[].href,
      .schema.runtime.frontend.client_routes.login
    ]
    | unique[]
  ' <<<"$candidate_schema_json"
)"
while IFS= read -r candidate_ui_route; do
  [[ -n "$candidate_ui_route" ]] || continue
  curl --fail-with-body --silent --show-error --max-time 15 \
    -o "$candidate_ui_file" \
    "${candidate_web_base_url}${candidate_ui_route}"
  if ! grep -Eiq '<!doctype html|<html' "$candidate_ui_file"; then
    printf 'candidate UI route %s did not return HTML\n' \
      "$candidate_ui_route" >&2
    exit 1
  fi
done <<<"$candidate_ui_routes"

candidate_invalid_webhook_path="${candidate_webhook_route//$candidate_webhook_placeholder/candidate-invalid-webhook}"
candidate_invalid_webhook_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_webhook_response_file" \
    -w '%{http_code}' \
    "${candidate_web_base_url}${candidate_invalid_webhook_path}"
)"
[[ "$candidate_invalid_webhook_status" == "$(
  jq -r '.statuses.not_found' <<<"$candidate_webhook_api_json"
)" ]]
jq -e \
  --argjson api "$candidate_webhook_api_json" \
  '.[$api.fields.error] == $api.values.not_found' \
  "$candidate_webhook_response_file" >/dev/null

if [[ -n "$candidate_webhook_path_secret" ]]; then
  candidate_webhook_path="${candidate_webhook_route//$candidate_webhook_placeholder/$candidate_webhook_path_secret}"
  candidate_webhook_health_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      "${candidate_web_base_url}${candidate_webhook_path}"
  )"
  jq -e \
    --argjson api "$candidate_webhook_api_json" \
    '
      .[$api.fields.status] == $api.values.ok
      and .[$api.fields.service] == $api.values.service
      and (.[$api.fields.timestamp] | type == "string" and length > 0)
    ' <<<"$candidate_webhook_health_json" >/dev/null

  candidate_webhook_key_args=()
  if [[ -n "$candidate_webhook_key" ]]; then
    candidate_webhook_key_args=(
      -H "${candidate_webhook_key_header}: ${candidate_webhook_key}"
    )
    candidate_webhook_unauthorized_status="$(
      curl --silent --show-error --max-time 10 \
        -o "$candidate_webhook_response_file" \
        -w '%{http_code}' \
        -X POST \
        -H 'Content-Type: application/json' \
        -H "${candidate_webhook_key_header}: wrong-candidate-key" \
        --data '{}' \
        "${candidate_web_base_url}${candidate_webhook_path}"
    )"
    [[ "$candidate_webhook_unauthorized_status" == "$(
      jq -r '.statuses.unauthorized' <<<"$candidate_webhook_api_json"
    )" ]]
    jq -e \
      --argjson api "$candidate_webhook_api_json" \
      '.[$api.fields.error] == $api.values.unauthorized' \
      "$candidate_webhook_response_file" >/dev/null
  fi

  candidate_webhook_bad_request_status="$(
    curl --silent --show-error --max-time 10 \
      -o "$candidate_webhook_response_file" \
      -w '%{http_code}' \
      -X POST \
      -H 'Content-Type: application/json' \
      "${candidate_webhook_key_args[@]}" \
      --data-binary '{' \
      "${candidate_web_base_url}${candidate_webhook_path}"
  )"
  [[ "$candidate_webhook_bad_request_status" == "$(
    jq -r '.statuses.bad_request' <<<"$candidate_webhook_api_json"
  )" ]]
  jq -e \
    --argjson api "$candidate_webhook_api_json" \
    '.[$api.fields.error] == $api.values.bad_request' \
    "$candidate_webhook_response_file" >/dev/null

  candidate_webhook_unknown_payload="$(
    jq -nc \
      --arg field "$candidate_webhook_job_id_field" \
      --arg id "candidate-unknown-${BASHPID:-$$}-${RANDOM}" \
      '{($field):$id}'
  )"
  candidate_webhook_ack_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -X POST \
      -H 'Content-Type: application/json' \
      "${candidate_webhook_key_args[@]}" \
      --data-binary "$candidate_webhook_unknown_payload" \
      "${candidate_web_base_url}${candidate_webhook_path}"
  )"
  jq -e \
    --argjson api "$candidate_webhook_api_json" \
    '
      .[$api.fields.received] == true
      and (.[$api.fields.timestamp] | type == "string" and length > 0)
    ' <<<"$candidate_webhook_ack_json" >/dev/null
fi

candidate_missing_login_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_auth_response_file" \
    -w '%{http_code}' \
    -X POST "${candidate_web_base_url}/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d '{}'
)"
[[ "$candidate_missing_login_status" == "400" ]]
jq -e '.error == "请输入用户名和密码"' \
  "$candidate_auth_response_file" >/dev/null

candidate_login_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -c "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/auth/login" \
    -H 'Content-Type: application/json' \
    --data-binary "$(
      jq -nc \
        --arg username "$candidate_admin_username" \
        --arg password "$candidate_admin_password" \
        '{username:$username,password:$password}'
    )"
)"
jq -e \
  --arg expected_name "$candidate_admin_display_name" \
  --arg expected_email "$candidate_admin_email" \
  '
  .success == true
  and .user.id
  and .user.name == $expected_name
  and .user.email == $expected_email
' <<<"$candidate_login_json" >/dev/null

candidate_session_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/auth/session"
)"
candidate_owner="$(jq -er '.user.id | select(type == "string" and length > 0)' <<<"$candidate_session_json")"
jq -e \
  --arg owner "$candidate_owner" \
  --arg login_user_id "$(jq -er '.user.id' <<<"$candidate_login_json")" \
  '
  .user.id == $owner
  and .user.id == $login_user_id
  and (.user.name | type == "string" and length > 0)
  and (.user.email | type == "string")
  and .isAdmin == true
  ' <<<"$candidate_session_json" >/dev/null

candidate_expected_mock_balance="$(
  jq -er '
    .schema.administration.usage.balance.account_id as $account_id
    | .schema.runtime.provider_accounts[$account_id].balance.mock_value
    | select(type == "number" and . >= 0)
  ' <<<"$candidate_schema_json"
)"

candidate_balance_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/meshy/balance"
)"
# NOTE: the catalog schema can exceed Linux MAX_ARG_STRLEN (128 KiB) as a single argv
# string (E2BIG -> "Argument list too long"), so feed it to jq via --slurpfile + process
# substitution (an fd path, not argv) and bind $schema from the slurped array; every
# $schema use below is unchanged.
jq -e \
  --slurpfile schema_slurp <(printf '%s' "$candidate_schema_json") \
  --argjson mock "$candidate_allow_generation" \
  '
    ($schema_slurp[0]) as $schema
    | $schema.schema.runtime as $runtime
    | $runtime.http.legacy_provider_balance as $contract
    | .[$contract.accounts_field] as $actual
    | ($actual | type == "array")
      and ([$actual[][$contract.provider_field]] | sort)
        == ([$contract.accounts[].account_id] | sort)
      and all($actual[];
        . as $account
        | $account[$contract.provider_field] as $id
        | $runtime.provider_accounts[$id] as $configuration
        | ($configuration | type == "object")
          and $configuration.provider_id == $id
          and ($account | has($contract.error_field) | not)
          and ($account[$contract.balance_field] | type == "number" and . >= 0)
          and (if $mock
            then $account[$contract.balance_field] == $configuration.balance.mock_value
            else true end))
  ' <<<"$candidate_balance_json" >/dev/null

candidate_skills_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    "${candidate_web_base_url}/api/v1/chat/skills"
)"
candidate_skill_id="$(
  jq -er '
    (.revision | select(type == "string" and length > 0)) as $revision
    | (.skills | select(length == 162))[0].id
    | select(type == "string" and length > 0)
  ' <<<"$candidate_skills_json"
)"
jq -e '
  .skills[0].prompt
  and .skills[0].recommended_size
  and .skills[0].thumbnail_path
' <<<"$candidate_skills_json" >/dev/null

candidate_empty_enhance_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_auth_response_file" \
    -w '%{http_code}' \
    -X POST "${candidate_web_base_url}/api/prompt-enhance" \
    -b "$candidate_cookie_jar" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d '{"prompt":" "}'
)"
[[ "$candidate_empty_enhance_status" == "400" ]]
jq -e '.error == "提示词不能为空"' \
  "$candidate_auth_response_file" >/dev/null

if [[ "$candidate_allow_generation" == "true" ]]; then
  candidate_enhance_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -X POST "${candidate_web_base_url}/api/prompt-enhance" \
      -b "$candidate_cookie_jar" \
      -H 'Content-Type: application/json' \
      -H "X-User-ID: ${candidate_owner}" \
      --data-binary "$(
        jq -nc \
          --arg model "$candidate_model" \
          '{
            prompt:"candidate prompt enhancement",
            capability:"text-to-image",
            model:$model
          }'
      )"
  )"
  jq -e '
    .prompt == "candidate prompt enhancement"
    and (.negative_prompt | type == "string" and length > 0)
  ' <<<"$candidate_enhance_json" >/dev/null
fi

candidate_chat_session_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -X POST "${candidate_web_base_url}/api/chat/sessions" \
    -b "$candidate_cookie_jar" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d '{"title":"candidate chat"}'
)"
candidate_chat_session_id="$(
  jq -er '.id | select(type == "string" and length > 0)' \
    <<<"$candidate_chat_session_json"
)"
candidate_renamed_chat_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -X PATCH \
    "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}" \
    -b "$candidate_cookie_jar" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d '{"title":"candidate chat renamed"}'
)"
jq -e '.title == "candidate chat renamed"' \
  <<<"$candidate_renamed_chat_json" >/dev/null

if [[ "$candidate_allow_generation" == "true" ]]; then
  candidate_chat_submit_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -X POST \
      "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}/messages" \
      -b "$candidate_cookie_jar" \
      -H 'Content-Type: application/json' \
      -H "X-User-ID: ${candidate_owner}" \
      --data-binary "$(
        jq -nc \
          --arg skill_id "$candidate_skill_id" \
          --arg model "$candidate_model" \
          '{
            content:"candidate chat skill prompt",
            ai_model:$model,
            size:"1536x1024",
            _skill:{id:$skill_id,name:"untrusted client name"}
          }'
      )"
  )"
  jq -e \
    --arg session_id "$candidate_chat_session_id" \
    '
      .messageId
      and .sessionId
      and .sessionId == $session_id
    ' <<<"$candidate_chat_submit_json" >/dev/null

  curl --fail-with-body --silent --show-error --max-time 30 \
    -b "$candidate_cookie_jar" \
    -H "X-User-ID: ${candidate_owner}" \
    -o "$candidate_chat_stream_file" \
    "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}/stream?fromId=0"
  grep -Fq '"type":"task_registered"' "$candidate_chat_stream_file"
  grep -Fq '"type":"images"' "$candidate_chat_stream_file"
  grep -Fq '"type":"done"' "$candidate_chat_stream_file"
  grep -Fq 'MAPPA' "$candidate_chat_stream_file"
  sed -n 's/^data: //p' "$candidate_chat_stream_file" |
    jq -r '
      select(.type == "task_registered")
      | .taskId
      | select(type == "string" and length > 0)
    ' >>"$candidate_generated_task_ids_file"
  [[ -s "$candidate_generated_task_ids_file" ]]

  candidate_chat_messages_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -b "$candidate_cookie_jar" \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}/messages"
  )"
  candidate_chat_message_id="$(
    jq -er '
      select(length >= 2)
      | .[0].id
      | select(type == "string" and length > 0)
    ' <<<"$candidate_chat_messages_json"
  )"
  curl --fail-with-body --silent --show-error --max-time 10 \
    -X DELETE \
    -b "$candidate_cookie_jar" \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}/messages/${candidate_chat_message_id}" \
    >/dev/null
  cleanup_candidate_generated_tasks
fi
curl --fail-with-body --silent --show-error --max-time 10 \
  -X DELETE \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  "${candidate_web_base_url}/api/chat/sessions/${candidate_chat_session_id}" \
  >/dev/null

# Removed SSO endpoints must not set a session or redirect to a provider.
for candidate_removed_route in \
  'GET /api/auth/sso/callback' \
  'POST /api/auth/sso/callback' \
  'POST /api/auth/sso/logout' \
  'GET /api/auth/sso/redirect?redirect=https%3A%2F%2Fattacker.example%2Fsteal'; do
  candidate_removed_method="${candidate_removed_route%% *}"
  candidate_removed_path="${candidate_removed_route#* }"
  candidate_removed_status="$(
    curl --silent --show-error --max-time 10 \
      -o "$candidate_auth_response_file" \
      -D "$candidate_version_headers_file" \
      -w '%{http_code}' \
      -H "Origin: ${candidate_web_base_url}" \
      -X "$candidate_removed_method" "${candidate_web_base_url}${candidate_removed_path}"
  )"
  [[ "$candidate_removed_status" == "404" ]]
  if grep -Eiq '^(Location|Set-Cookie):' "$candidate_version_headers_file"; then
    printf 'retired auth endpoint unexpectedly redirected or changed session\n' >&2
    exit 1
  fi
done

candidate_proxy_missing_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_auth_response_file" \
    -w '%{http_code}' \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/meshy/proxy-asset"
)"
[[ "$candidate_proxy_missing_status" == "400" ]]
jq -e '.error == "缺少 url 参数"' \
  "$candidate_auth_response_file" >/dev/null

candidate_proxy_forbidden_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_auth_response_file" \
    -w '%{http_code}' \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/meshy/proxy-asset?url=https%3A%2F%2Fattacker.invalid%2Fasset.png"
)"
[[ "$candidate_proxy_forbidden_status" == "403" ]]
jq -e '.error == "不允许访问的域名"' \
  "$candidate_auth_response_file" >/dev/null

candidate_managed_user_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/admin/users" \
    -H 'Content-Type: application/json' \
    --data-binary "$(
      jq -nc \
        --arg username "$candidate_managed_username" \
        --arg password "$candidate_managed_password" \
        '{
          username:$username,
          password:$password,
          name:"Candidate Managed User",
          email:"managed@candidate.invalid",
          isAdmin:false
        }'
    )"
)"
candidate_managed_user_id="$(
  jq -er '
    .data
    | select(
        .id
        and .username
        and .name == "Candidate Managed User"
        and .email == "managed@candidate.invalid"
        and .isAdmin == false
        and .isActive == true
        and .lastLogin == null
        and .openId == null
      )
    | .id
  ' <<<"$candidate_managed_user_json"
)"
candidate_managed_update_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X PATCH \
    "${candidate_web_base_url}/api/admin/users/${candidate_managed_user_id}" \
    -H 'Content-Type: application/json' \
    -d '{
      "name":"Candidate Managed User Updated",
      "isAdmin":true,
      "password":"candidate-user-password-updated"
    }'
)"
jq -e '
  .data.name == "Candidate Managed User Updated"
  and .data.nickname == "Candidate Managed User Updated"
  and .data.isAdmin == true
  and .data.isActive == true
' <<<"$candidate_managed_update_json" >/dev/null
candidate_audit_logs_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/audit-logs?page=1&pageSize=5&userId=${candidate_owner}&action=admin_update_user"
)"
jq -e \
  --arg user_id "$candidate_managed_user_id" \
  --arg owner "$candidate_owner" \
  '
    .total >= 1
    and .page == 1
    and .pageSize == 5
    and any(
      .data[];
      .action == "admin_update_user"
      and .userId == $owner
      and .resourceType == "user"
      and .resourceId == $user_id
      and (.detail | fromjson | .username)
    )
  ' <<<"$candidate_audit_logs_json" >/dev/null
candidate_managed_list_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/users"
)"
jq -e \
  --arg user_id "$candidate_managed_user_id" \
  'any(.data[]; .id == $user_id and .isAdmin == true)' \
  <<<"$candidate_managed_list_json" >/dev/null
candidate_managed_delete_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X DELETE \
    "${candidate_web_base_url}/api/admin/users/${candidate_managed_user_id}"
)"
jq -e '.success == true' <<<"$candidate_managed_delete_json" >/dev/null
candidate_managed_list_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/users"
)"
jq -e \
  --arg user_id "$candidate_managed_user_id" \
  'all(.data[]; .id != $user_id)' \
  <<<"$candidate_managed_list_json" >/dev/null

candidate_cache_status_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/cache-status"
)"
jq -e '
  (.activeDownloads | type == "number")
  and (.activeItems | type == "array")
  and (.totalCachedFiles | type == "number")
  and (.totalCacheSizeMB | type == "number")
' <<<"$candidate_cache_status_json" >/dev/null

candidate_canvas_project_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/canvas/projects" \
    -H 'Content-Type: application/json' \
    -d '{"name":"candidate canvas"}'
)"
candidate_canvas_project_id="$(
  jq -er '.id | select(type == "string" and length > 0)' \
    <<<"$candidate_canvas_project_json"
)"
candidate_canvas_session_id="$(
  jq -er '.sessionId | select(type == "string" and length > 0)' \
    <<<"$candidate_canvas_project_json"
)"
jq -e '
  .name == "candidate canvas"
  and .viewport == "{\"zoom\":1,\"panX\":0,\"panY\":0}"
  and .bgStyle == "dots"
' <<<"$candidate_canvas_project_json" >/dev/null

candidate_canvas_replace_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X PUT \
    "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}/nodes" \
    -H 'Content-Type: application/json' \
    -d '{
      "nodes":[
        {"id":"candidate-high","type":"image","zIndex":5,"visible":false},
        {"id":"candidate-default","type":"text"}
      ]
    }'
)"
jq -e '.ok == true and .count == 2' \
  <<<"$candidate_canvas_replace_json" >/dev/null

candidate_canvas_loaded_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}"
)"
jq -e '
  (.nodes | length == 2)
  and .nodes[0].id == "candidate-default"
  and .nodes[0].x == 0
  and .nodes[0].width == 256
  and .nodes[0].data == "{}"
  and .nodes[0].visible == true
  and .nodes[0].locked == false
  and .nodes[1].id == "candidate-high"
  and .nodes[1].zIndex == 5
  and .nodes[1].visible == false
' <<<"$candidate_canvas_loaded_json" >/dev/null

candidate_canvas_updated_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X PATCH \
    "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}" \
    -H 'Content-Type: application/json' \
    -d '{
      "name":"candidate canvas renamed",
      "viewport":"{\"zoom\":2,\"panX\":10,\"panY\":20}",
      "bgStyle":"grid"
    }'
)"
jq -e '
  .name == "candidate canvas renamed"
  and .viewport == "{\"zoom\":2,\"panX\":10,\"panY\":20}"
  and .bgStyle == "grid"
' <<<"$candidate_canvas_updated_json" >/dev/null

candidate_canvas_list_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/canvas/projects"
)"
jq -e \
  --arg project_id "$candidate_canvas_project_id" \
  '
    any(
      .[];
      .id == $project_id
      and .name == "candidate canvas renamed"
      and ._count.nodes == 2
    )
  ' <<<"$candidate_canvas_list_json" >/dev/null

candidate_canvas_delete_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X DELETE \
    "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}"
)"
jq -e '.ok == true' <<<"$candidate_canvas_delete_json" >/dev/null
candidate_canvas_deleted_status="$(
  curl --silent --show-error --max-time 10 \
    -o "$candidate_auth_response_file" \
    -w '%{http_code}' \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/canvas/projects/${candidate_canvas_project_id}"
)"
[[ "$candidate_canvas_deleted_status" == "404" ]]
candidate_canvas_sessions_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/chat/sessions"
)"
jq -e \
  --arg session_id "$candidate_canvas_session_id" \
  'all(.[]; .id != $session_id)' \
  <<<"$candidate_canvas_sessions_json" >/dev/null

if [[ "$candidate_allow_generation" == "true" ]]; then
  candidate_mock_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -X POST "${candidate_web_base_url}/api/auth/mock" \
      -H 'Content-Type: application/json' \
      -d '{"name":"Candidate Body User","email":"body@local.invalid"}'
  )"
  jq -e '
    (.user.id | startswith("mock_"))
    and .user.name == "Candidate Body User"
    and .user.email == "body@local.invalid"
  ' <<<"$candidate_mock_json" >/dev/null
else
  candidate_mock_status="$(
    curl --silent --show-error --max-time 10 \
      -o "$candidate_auth_response_file" \
      -w '%{http_code}' \
      -X POST "${candidate_web_base_url}/api/auth/mock" \
      -H 'Content-Type: application/json' \
      -d '{"name":"Candidate Body User","email":"body@local.invalid"}'
  )"
  [[ "$candidate_mock_status" == "403" ]]
  candidate_mock_disabled_message="$(
    jq -er '.schema.authentication.messages.mock_disabled' \
      <<<"$candidate_schema_json"
  )"
  jq -e \
    --arg expected "$candidate_mock_disabled_message" \
    '.error == $expected' \
    "$candidate_auth_response_file" >/dev/null
fi

candidate_provider_route="$(
  jq -er '.schema.runtime.http.legacy_routes.providers' \
    <<<"$candidate_schema_json"
)"
candidate_provider_capability_query="$(
  jq -er '.schema.provider_discovery.api.query.capability' \
    <<<"$candidate_schema_json"
)"
candidate_provider_projection_query="$(
  jq -er '.schema.provider_discovery.api.query.projection' \
    <<<"$candidate_schema_json"
)"
candidate_provider_extended_projection="$(
  jq -er '.schema.provider_discovery.api.projection_values.extended' \
    <<<"$candidate_schema_json"
)"
candidate_image_provider_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    --get \
    --data-urlencode \
      "${candidate_provider_capability_query}=image-to-image" \
    --data-urlencode \
      "${candidate_provider_projection_query}=${candidate_provider_extended_projection}" \
    "${candidate_web_base_url}${candidate_provider_route}"
)"
candidate_image_offerings_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    --get --data-urlencode 'capability=image-to-image' \
    "${candidate_web_base_url}/api/v1/offerings"
)"
# Both discovery endpoints expose enabled offerings, including providers whose
# runtime wiring is unavailable. Compare their declared set without filtering
# available=false or assuming a fixed model count/order.
# (--argjson schema -> --slurpfile + process substitution: the catalog schema can exceed
# Linux MAX_ARG_STRLEN (128 KiB) as a single argv string; same fix as the balance block
# above. $offerings stays --argjson (small). Every $schema use below is unchanged.)
jq -e \
  --argjson offerings "$candidate_image_offerings_json" \
  --slurpfile schema_slurp <(printf '%s' "$candidate_schema_json") \
  '
    ($schema_slurp[0]) as $schema
    | $schema.schema.provider_discovery.api as $api
    | $api.provider_fields as $fields
    | $api.parameter_fields as $parameters
    | def condition_wire:
        {($api.condition_fields.parameter): .parameter,
         ($api.condition_fields.operator): .operator,
         ($api.condition_fields.value): .value};
      def notice_wire:
        {($api.notice_fields.when): (.when | condition_wire),
         ($api.notice_fields.message_key): .message_key,
         ($api.notice_fields.tone): .tone};
    $offerings.offerings as $expected
    | .[$api.envelope_fields.providers] as $actual
    | ($expected | type == "array" and length > 0)
      and all($expected[]; .capability == "image-to-image" and (.model_id | type == "string" and length > 0))
      and ($actual | type == "array")
      and ([$actual[][$fields.id]] | sort) == ([$expected[].model_id] | sort)
      and (.[$api.envelope_fields.default_model] as $default | any($expected[]; .model_id == $default))
      and all($expected[];
        . as $offering
        | [$actual[] | select(.[$fields.id] == $offering.model_id)] as $matches
        | ($matches | length == 1)
          and ($matches[0][$fields.estimated_credits] == $offering.estimated_credits)
          and ($matches[0][$fields.capabilities] | index("image-to-image") != null)
          and (
            [$offering.parameters[] | select(.presentation.surface == "provider")] as $declared
            | $matches[0][$fields.parameter_schemas]["image-to-image"] as $projected
            | ($projected | type == "array")
              and ([$projected[][$parameters.name]] | sort) == ([$declared[].name] | sort)
              and all($declared[];
                . as $parameter
                | [$projected[] | select(.[$parameters.name] == $parameter.name)] as $items
                | ($items | length == 1)
                  and (($items[0][$parameters.disabled_when] // []) == [$parameter.presentation.disabled_when[]? | condition_wire])
                  and (($items[0][$parameters.notices] // []) == [$parameter.presentation.notices[]? | notice_wire])
              )
          )
      )
  ' <<<"$candidate_image_provider_json" >/dev/null

candidate_input_asset_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/assets/upload" \
    -H "X-User-ID: ${candidate_owner}" \
    -F "file=@${candidate_input_asset_file};type=image/png;filename=candidate.png"
)"
candidate_input_asset_id="$(
  jq -er '.asset_id | select(type == "string" and length > 0)' \
    <<<"$candidate_input_asset_json"
)"
candidate_input_asset_metadata="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}/api/assets/${candidate_input_asset_id}?format=json"
)"
jq -e '
  (.asset_type == "image")
  and (.original_filename == "candidate.png")
  and (.mime_type == "image/png")
  and (.data_uri | startswith("data:image/png;base64,"))
' <<<"$candidate_input_asset_metadata" >/dev/null

[[ -s "$candidate_webp_fixture" ]]
candidate_webp_asset_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/assets/upload" \
    -H "X-User-ID: ${candidate_owner}" \
    -F "file=@${candidate_webp_fixture};type=image/webp;filename=candidate-canvas.webp"
)"
candidate_webp_asset_id="$(
  jq -er '.asset_id | select(type == "string" and length > 0)' \
    <<<"$candidate_webp_asset_json"
)"
candidate_canvas_merge_json="$(
  curl --fail-with-body --silent --show-error --max-time 15 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}/api/canvas/merge-images" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d "$(
      jq -nc \
        --arg image_url "/api/assets/${candidate_webp_asset_id}" \
        '{
          nodes: [{
            imageUrl: $image_url,
            x: 0,
            y: 0,
            width: 2,
            height: 2
          }]
        }'
    )"
)"
candidate_canvas_merge_asset_id="$(
  jq -er '
    .assetId as $asset_id
    | select(
        ($asset_id | type == "string" and length > 0)
        and .url == ("/api/assets/" + $asset_id)
      )
    | $asset_id
  ' <<<"$candidate_canvas_merge_json"
)"
curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_canvas_merge_file" \
  "${candidate_web_base_url}/api/assets/${candidate_canvas_merge_asset_id}"
[[ -s "$candidate_canvas_merge_file" ]]
candidate_assert_png_file \
  "WebP Canvas merge output" \
  "$candidate_canvas_merge_file"

if [[ "$candidate_allow_generation" != "true" ]]; then
  verify_candidate_logout
  printf 'candidate safe HTTP contracts passed; generation tests disabled\n'
  exit 0
fi
[[ "$candidate_allow_generation" == "true" ]]

candidate_submit_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}${candidate_text_to_image_route}" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d "{
      \"prompt\":\"candidate public HTTP contract\",
      \"ai_model\":\"${candidate_model}\",
      \"size\":\"1024x1024\",
      \"quality\":\"high\"
    }"
)"
candidate_task_id="$(jq -er '.result | select(type == "string" and length > 0)' <<<"$candidate_submit_json")"

candidate_task_json=""
candidate_task_state=""
for candidate_poll_attempt in {1..20}; do
  candidate_task_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -b "$candidate_cookie_jar" \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}${candidate_text_to_image_route}/${candidate_task_id}"
  )"
  candidate_task_state="$(jq -er '.status' <<<"$candidate_task_json")"
  if [[ "$candidate_task_state" == "SUCCEEDED" ]]; then
    break
  fi
  sleep 0.5
done

[[ "$candidate_task_state" == "SUCCEEDED" ]]
[[ "$(jq -er '.progress' <<<"$candidate_task_json")" == "100" ]]
[[ "$(jq -er '.ai_model' <<<"$candidate_task_json")" == "$candidate_model" ]]
candidate_asset_path="$(jq -er '.output_urls[0] | select(type == "string" and startswith("/api/v1/"))' <<<"$candidate_task_json")"

curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_asset_file" \
  "${candidate_web_base_url}${candidate_asset_path}"
[[ -s "$candidate_asset_file" ]]
candidate_assert_png_file \
  "native task preview" \
  "$candidate_asset_file"

curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_legacy_image_file" \
  "${candidate_web_base_url}/api/gpt-images/${candidate_task_id}_0.png"
cmp "$candidate_asset_file" "$candidate_legacy_image_file"
candidate_assert_png_file \
  "legacy generated preview" \
  "$candidate_legacy_image_file"

candidate_history_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}/api/tasks?search=${candidate_task_id}"
)"
jq -e \
  --arg task_id "$candidate_task_id" \
  --arg owner_prompt "candidate public HTTP contract" \
  '
    (.total == 1)
    and (.data[0].id == $task_id)
    and (.data[0].prompt == $owner_prompt)
    and (.data[0].status == "SUCCEEDED")
    and (
      .data[0].thumbnailUrl
      | type == "string" and startswith("/api/v1/")
    )
  ' <<<"$candidate_history_json" >/dev/null
candidate_history_thumbnail_path="$(
  jq -er '.data[0].thumbnailUrl' <<<"$candidate_history_json"
)"
curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_legacy_image_file" \
  "${candidate_web_base_url}${candidate_history_thumbnail_path}"
cmp "$candidate_asset_file" "$candidate_legacy_image_file"
candidate_assert_png_file \
  "task history thumbnail" \
  "$candidate_legacy_image_file"

candidate_cached_after_generation_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/cache-status"
)"
jq -e '
  .totalCachedFiles >= 1
  and .totalCacheSizeMB >= 0
  and (.activeDownloads | type == "number")
  and (.activeItems | type == "array")
' <<<"$candidate_cached_after_generation_json" >/dev/null

curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -D "$candidate_task_stream_headers_file" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_task_stream_file" \
  "${candidate_web_base_url}/api/meshy/stream/text-to-image/${candidate_task_id}"
grep -Eiq '^content-type:[[:space:]]*text/event-stream' \
  "$candidate_task_stream_headers_file"
grep -Eq '^event: message$' "$candidate_task_stream_file"
candidate_task_stream_json="$(
  sed -n 's/^data: //p' "$candidate_task_stream_file" | tail -n 1
)"
jq -e \
  --arg task_id "$candidate_task_id" \
  '
    .id == $task_id
    and .type == "text-to-image"
    and .status == "SUCCEEDED"
    and .progress == 100
    and (.output_urls | length == 1)
  ' <<<"$candidate_task_stream_json" >/dev/null

printf 'candidate task=%s state=%s asset_bytes=%s\n' \
  "$candidate_task_id" \
  "$candidate_task_state" \
  "$(wc -c <"$candidate_asset_file" | tr -d ' ')"

candidate_admin_generations_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/generations?page=1&pageSize=5&userId=${candidate_owner}&taskType=text-to-image&status=SUCCEEDED"
)"
if ! jq -e \
  --arg task_id "$candidate_task_id" \
  --arg owner "$candidate_owner" \
  '
    .total >= 1
    and .page == 1
    and .pageSize == 5
    and any(
      .data[];
      .taskId == $task_id
      and .userId == $owner
      and .taskType == "text-to-image"
      and .taskStatus == "SUCCEEDED"
      and (.params | fromjson | .prompt == "candidate public HTTP contract")
      and (.resultUrls | fromjson | length == 1)
    )
  ' <<<"$candidate_admin_generations_json" >/dev/null; then
  jq \
    --arg task_id "$candidate_task_id" \
    --arg owner "$candidate_owner" \
    '{
      expected: {taskId: $task_id, userId: $owner},
      total,
      page,
      pageSize,
      data: [
        .data[]? | {
          taskId,
          userId,
          taskType,
          taskStatus,
          params,
          resultUrls
        }
      ]
    }' <<<"$candidate_admin_generations_json" >&2
  false
fi
candidate_admin_generation_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/generations/${candidate_task_id}"
)"
jq -e \
  --arg task_id "$candidate_task_id" \
  '
    .ledger.taskId == $task_id
    and .ledger.taskType == "text-to-image"
    and .ledger.taskStatus == "SUCCEEDED"
    and .ledger.params.prompt == "candidate public HTTP contract"
    and (.ledger.resultUrls | length == 1)
    and .taskCache.taskId == $task_id
    and .taskCache.status == "SUCCEEDED"
    and (.taskCache.data | type == "object")
  ' <<<"$candidate_admin_generation_json" >/dev/null
curl --fail-with-body --silent --show-error --max-time 10 \
  -D "$candidate_admin_generation_download_headers_file" \
  -b "$candidate_cookie_jar" \
  -o "$candidate_admin_generation_download_file" \
  "${candidate_web_base_url}/api/admin/generations/${candidate_task_id}/download"
grep -Eiq '^content-disposition:[[:space:]]*attachment;' \
  "$candidate_admin_generation_download_headers_file"
cmp "$candidate_admin_generation_download_file" "$candidate_asset_file"

candidate_admin_user_assets_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/user-assets/${candidate_owner}?pageSize=5&uploadsPage=1&generationsPage=1&downloadsPage=1"
)"
jq -e \
  --arg owner "$candidate_owner" \
  --arg input_asset_id "$candidate_input_asset_id" \
  --arg task_id "$candidate_task_id" \
  '
    .user.id == $owner
    and .summary.totalUploads >= 1
    and .summary.totalGenerations >= 1
    and .summary.totalDownloads >= 2
    and .summary.totalStorageBytes > 0
    and .uploads.page == 1
    and .uploads.pageSize == 5
    and any(
      .uploads.data[];
      .id == $input_asset_id
      and .assetType == "image"
      and .originalFilename == "candidate.png"
      and .mimeType == "image/png"
      and .fileSize > 0
    )
    and .generations.page == 1
    and .generations.pageSize == 5
    and any(
      .generations.data[];
      .taskId == $task_id
      and .taskType == "text-to-image"
      and .taskStatus == "SUCCEEDED"
      and (.resultUrls | fromjson | length == 1)
    )
    and .downloads.page == 1
    and .downloads.pageSize == 5
    and any(.downloads.data[]; .action == "access_asset")
    and any(.downloads.data[]; .action == "download_asset")
  ' <<<"$candidate_admin_user_assets_json" >/dev/null

candidate_admin_backfill_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST \
    "${candidate_web_base_url}/api/admin/backfill-status"
)"
jq -e '
  .message == "没有需要回填的 PENDING 记录"
  and .fixed == 0
  and (keys | sort) == ["fixed", "message"]
' <<<"$candidate_admin_backfill_json" >/dev/null

candidate_admin_usage_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/usage?days=7"
)"
jq -e \
  --arg owner "$candidate_owner" \
  '
    (.data | type == "array")
    and any(
      .data[];
      .userId == $owner
      and .totalTasks >= 1
      and .succeededTasks >= 1
      and .failedTasks >= 0
      and (.byType["text-to-image"] == .estimatedCredits)
    )
  ' <<<"$candidate_admin_usage_json" >/dev/null

candidate_admin_balance_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    "${candidate_web_base_url}/api/admin/balance-summary"
)"
jq -e \
  --argjson expected "$candidate_expected_mock_balance" \
  '
    .current_balance == $expected
    and (.history | type == "array")
    and any(
      .history[];
      .balance == $expected
      and (.captured_at | type == "string")
    )
  ' <<<"$candidate_admin_balance_json" >/dev/null

candidate_3d_submit_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X POST "${candidate_web_base_url}${candidate_text_to_3d_route}" \
    -H 'Content-Type: application/json' \
    -H "X-User-ID: ${candidate_owner}" \
    -d '{
      "mode":"preview",
      "prompt":"candidate public HTTP 3D contract",
      "ai_model":"meshy-7",
      "target_formats":["glb"]
    }'
)"
candidate_3d_task_id="$(jq -er '.result | select(type == "string" and length > 0)' <<<"$candidate_3d_submit_json")"

candidate_3d_task_json=""
candidate_3d_task_state=""
for candidate_poll_attempt in {1..20}; do
  candidate_3d_task_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -b "$candidate_cookie_jar" \
      -H "X-User-ID: ${candidate_owner}" \
      "${candidate_web_base_url}${candidate_text_to_3d_route}/${candidate_3d_task_id}"
  )"
  candidate_3d_task_state="$(jq -er '.status' <<<"$candidate_3d_task_json")"
  if [[ "$candidate_3d_task_state" == "SUCCEEDED" ]]; then
    break
  fi
  sleep 0.5
done

[[ "$candidate_3d_task_state" == "SUCCEEDED" ]]
[[ "$(jq -er '.progress' <<<"$candidate_3d_task_json")" == "100" ]]
[[ "$(jq -er '.ai_model' <<<"$candidate_3d_task_json")" == "meshy-7" ]]
candidate_model_asset_path="$(jq -er '.model_urls.glb | select(type == "string" and startswith("/api/v1/"))' <<<"$candidate_3d_task_json")"

curl --fail-with-body --silent --show-error --max-time 10 \
  -b "$candidate_cookie_jar" \
  -H "X-User-ID: ${candidate_owner}" \
  -o "$candidate_model_asset_file" \
  "${candidate_web_base_url}${candidate_model_asset_path}"
[[ -s "$candidate_model_asset_file" ]]

printf 'candidate 3d_task=%s state=%s asset_bytes=%s\n' \
  "$candidate_3d_task_id" \
  "$candidate_3d_task_state" \
  "$(wc -c <"$candidate_model_asset_file" | tr -d ' ')"

candidate_delete_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X DELETE \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}${candidate_text_to_image_route}/${candidate_task_id}"
)"
jq -e '.success == true' <<<"$candidate_delete_json" >/dev/null
candidate_deleted_status="$(
  curl --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -o "$candidate_deleted_item_file" \
    -w '%{http_code}' \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}${candidate_text_to_image_route}/${candidate_task_id}"
)"
[[ "$candidate_deleted_status" == "404" ]]
candidate_deleted_history_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}/api/tasks?search=${candidate_task_id}"
)"
jq -e '.total == 0 and (.data | length == 0)' \
  <<<"$candidate_deleted_history_json" >/dev/null

candidate_3d_delete_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -b "$candidate_cookie_jar" \
    -X DELETE \
    -H "X-User-ID: ${candidate_owner}" \
    "${candidate_web_base_url}${candidate_text_to_3d_route}/${candidate_3d_task_id}"
)"
jq -e '.success == true' <<<"$candidate_3d_delete_json" >/dev/null

cleanup_candidate_generated_tasks
verify_candidate_logout
