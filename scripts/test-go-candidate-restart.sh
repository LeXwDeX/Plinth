#!/usr/bin/env bash
set -euo pipefail

candidate_mode="${1:-}"
candidate_state_file="${2:-${CANDIDATE_RESTART_STATE_FILE:-}}"
candidate_web_base_url="${CANDIDATE_WEB_BASE_URL:-http://127.0.0.1:13335}"
candidate_allow_generation="${CANDIDATE_ALLOW_GENERATION:-false}"
candidate_asset_file="$(mktemp)"
candidate_cleanup_task="false"
candidate_task_id=""
candidate_task_route=""

cleanup_candidate_restart_test() {
  local candidate_exit_status=$?

  if [[ "$candidate_cleanup_task" == "true" ]] &&
    [[ -n "$candidate_task_id" ]] &&
    [[ -n "$candidate_task_route" ]]; then
    curl --silent --show-error --max-time 5 \
      -X DELETE \
      "${candidate_web_base_url}${candidate_task_route}/${candidate_task_id}" \
      >/dev/null 2>&1 || true
  fi
  rm -f "$candidate_asset_file"
  return "$candidate_exit_status"
}

trap 'cleanup_candidate_restart_test' EXIT

if [[ "$candidate_mode" != "prepare" && "$candidate_mode" != "verify" ]]; then
  printf 'usage: %s prepare|verify STATE_FILE\n' "$0" >&2
  exit 2
fi
if [[ -z "$candidate_state_file" ]]; then
  printf 'candidate restart state file is required\n' >&2
  exit 2
fi
if [[ "$candidate_allow_generation" != "true" ]]; then
  printf 'candidate restart test requires explicit mock generation opt-in\n' >&2
  exit 2
fi

candidate_schema_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    "${candidate_web_base_url}/api/v1/schema"
)"
candidate_legacy_task_route="$(
  jq -er '
    .schema.capabilities[]
    | select(.id == "text-to-image") | .legacy_api.path
    | select(
        type == "string"
        and (. == "/api" or startswith("/api/"))
      )
  ' <<<"$candidate_schema_json"
)"
candidate_model_parameter="$(
  jq -er '
    .schema.capabilities[]
    | select(.id == "text-to-image") | .legacy_api.model_parameter
    | select(type == "string" and length > 0)
  ' <<<"$candidate_schema_json"
)"
candidate_provider_route="$(
  jq -er '
    .schema.runtime.http.legacy_routes.providers
    | select(type == "string" and startswith("/"))
  ' <<<"$candidate_schema_json"
)"
candidate_task_route="$candidate_legacy_task_route"

if [[ "$candidate_mode" == "prepare" ]]; then
  candidate_providers_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      "${candidate_web_base_url}${candidate_provider_route}?capability=text-to-image"
  )"
  candidate_model="$(
    jq -er '
      .providers[0].id
      | select(type == "string" and length > 0)
    ' <<<"$candidate_providers_json"
  )"
  candidate_submit_body="$(
    jq -nc \
      --arg model_parameter "$candidate_model_parameter" \
      --arg model "$candidate_model" \
      '{
        prompt:"candidate persisted task across Go restart"
      } + {($model_parameter):$model}'
  )"
  candidate_submit_json="$(
    curl --fail-with-body --silent --show-error --max-time 10 \
      -X POST \
      -H 'Content-Type: application/json' \
      -d "$candidate_submit_body" \
      "${candidate_web_base_url}${candidate_task_route}"
  )"
  candidate_task_id="$(
    jq -er '.result | select(type == "string" and length > 0)' \
      <<<"$candidate_submit_json"
  )"

  candidate_task_json=""
  candidate_task_state=""
  for candidate_poll_attempt in {1..20}; do
    candidate_task_json="$(
      curl --fail-with-body --silent --show-error --max-time 10 \
        "${candidate_web_base_url}${candidate_task_route}/${candidate_task_id}"
    )"
    candidate_task_state="$(jq -er '.status' <<<"$candidate_task_json")"
    if [[ "$candidate_task_state" == "SUCCEEDED" ]]; then
      break
    fi
    sleep 0.5
  done
  [[ "$candidate_task_state" == "SUCCEEDED" ]]
  candidate_asset_path="$(
    jq -er '
      .output_urls[0]
      | select(type == "string" and startswith("/"))
    ' <<<"$candidate_task_json"
  )"
  curl --fail-with-body --silent --show-error --max-time 10 \
    -o "$candidate_asset_file" \
    "${candidate_web_base_url}${candidate_asset_path}"
  [[ -s "$candidate_asset_file" ]]
  candidate_asset_sha256="$(
    sha256sum "$candidate_asset_file" | awk '{print $1}'
  )"
  jq -nc \
    --arg task_id "$candidate_task_id" \
    --arg task_route "$candidate_task_route" \
    --arg asset_path "$candidate_asset_path" \
    --arg asset_sha256 "$candidate_asset_sha256" \
    '{
      task_id:$task_id,
      task_route:$task_route,
      asset_path:$asset_path,
      asset_sha256:$asset_sha256
    }' >"$candidate_state_file"
  printf 'prepared persisted candidate task=%s\n' "$candidate_task_id"
  exit 0
fi

[[ -s "$candidate_state_file" ]]
candidate_task_id="$(
  jq -er '.task_id | select(type == "string" and length > 0)' \
    "$candidate_state_file"
)"
candidate_asset_path="$(
  jq -er '.asset_path | select(type == "string" and startswith("/"))' \
    "$candidate_state_file"
)"
candidate_expected_asset_sha256="$(
  jq -er '
    .asset_sha256
    | select(type == "string" and test("^[0-9a-f]{64}$"))
  ' "$candidate_state_file"
)"
jq -e \
  --arg task_id "$candidate_task_id" \
  --arg task_route "$candidate_task_route" \
  '
    .task_id == $task_id
    and .task_route == $task_route
  ' "$candidate_state_file" >/dev/null
candidate_cleanup_task="true"

candidate_task_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    "${candidate_web_base_url}${candidate_task_route}/${candidate_task_id}"
)"
jq -e \
  --arg task_id "$candidate_task_id" \
  --arg asset_path "$candidate_asset_path" \
  '
    .id == $task_id
    and .status == "SUCCEEDED"
    and .progress == 100
    and .output_urls[0] == $asset_path
  ' <<<"$candidate_task_json" >/dev/null
curl --fail-with-body --silent --show-error --max-time 10 \
  -o "$candidate_asset_file" \
  "${candidate_web_base_url}${candidate_asset_path}"
[[ -s "$candidate_asset_file" ]]
candidate_asset_sha256="$(
  sha256sum "$candidate_asset_file" | awk '{print $1}'
)"
[[ "$candidate_asset_sha256" == "$candidate_expected_asset_sha256" ]]

candidate_delete_json="$(
  curl --fail-with-body --silent --show-error --max-time 10 \
    -X DELETE \
    "${candidate_web_base_url}${candidate_task_route}/${candidate_task_id}"
)"
jq -e '.success == true' <<<"$candidate_delete_json" >/dev/null
candidate_cleanup_task="false"
printf 'verified persisted candidate task=%s asset_sha256=%s\n' \
  "$candidate_task_id" \
  "$candidate_asset_sha256"
