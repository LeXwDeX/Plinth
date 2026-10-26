#!/usr/bin/env bash
set -Eeuo pipefail

candidate_web_base_url="${CANDIDATE_WEB_BASE_URL:-http://127.0.0.1:13335}"
candidate_owner="${CANDIDATE_OWNER:-candidate-admin}"
candidate_allow_generation="${CANDIDATE_ALLOW_GENERATION:-false}"
candidate_inline_png='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='
candidate_generated_tasks_file="$(mktemp)"
candidate_download_file="$(mktemp)"
candidate_background_file="$(mktemp)"
candidate_failure_line=""

record_candidate_failure() {
  candidate_failure_line="$1"
}

candidate_request() {
  curl --fail-with-body --silent --show-error --max-time 15 \
    -H "X-User-ID: ${candidate_owner}" \
    "$@"
}

cleanup_candidate_generation_resources() {
  local candidate_exit_status=$?
  local candidate_route
  local candidate_task_id

  if [[ "$candidate_exit_status" -ne 0 ]]; then
    printf 'all-generation acceptance failed at line %s\n' \
      "${candidate_failure_line:-unknown}" >&2
  fi
  if [[ -s "$candidate_generated_tasks_file" ]]; then
    while IFS=$'\t' read -r candidate_route candidate_task_id; do
      [[ -n "$candidate_route" && -n "$candidate_task_id" ]] || continue
      candidate_request \
        -X DELETE \
        "${candidate_web_base_url}${candidate_route}/${candidate_task_id}" \
        >/dev/null 2>&1 || true
    done <"$candidate_generated_tasks_file"
  fi
  rm -f \
    "$candidate_generated_tasks_file" \
    "$candidate_download_file" \
    "$candidate_background_file"
  return "$candidate_exit_status"
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

candidate_model_parameter() {
  local candidate_capability="$1"

  jq -er \
    --arg capability "$candidate_capability" \
    '
      .schema.capabilities[]
      | select(.id == $capability)
      | .legacy_api.model_parameter
    ' <<<"$candidate_schema_json"
}

candidate_multipart_field() {
  local candidate_capability="$1"
  local candidate_field="$2"

  jq -er \
    --arg capability "$candidate_capability" \
    --arg field "$candidate_field" \
    '
      .schema.capabilities[]
      | select(.id == $capability)
      | .legacy_api.multipart[$field]
      | select(type == "string" and length > 0)
    ' <<<"$candidate_schema_json"
}

candidate_default_model() {
  local candidate_capability="$1"

  candidate_request \
    "${candidate_web_base_url}${candidate_provider_route}?capability=${candidate_capability}" |
    jq -er '.defaultModel | select(type == "string" and length > 0)'
}

candidate_submit_json() {
  local candidate_capability="$1"
  local candidate_body="$2"
  local candidate_route
  local candidate_parameter
  local candidate_model
  local candidate_submit_body
  local candidate_submit_json
  local candidate_task_id

  candidate_route="$(candidate_capability_route "$candidate_capability")"
  candidate_parameter="$(candidate_model_parameter "$candidate_capability")"
  candidate_model="$(candidate_default_model "$candidate_capability")"
  candidate_submit_body="$(
    jq -c \
      --arg parameter "$candidate_parameter" \
      --arg model "$candidate_model" \
      '. + {($parameter): $model}' <<<"$candidate_body"
  )"
  candidate_submit_json="$(
    candidate_request \
      -X POST \
      "${candidate_web_base_url}${candidate_route}" \
      -H 'Content-Type: application/json' \
      --data-binary "$candidate_submit_body"
  )"
  candidate_task_id="$(
    jq -er '.result | select(type == "string" and length > 0)' \
      <<<"$candidate_submit_json"
  )"
  printf '%s\t%s\n' \
    "$candidate_route" \
    "$candidate_task_id" >>"$candidate_generated_tasks_file"
  printf '%s\n' "$candidate_task_id"
}

candidate_wait_succeeded() {
  local candidate_route="$1"
  local candidate_task_id="$2"
  local candidate_task_json=""
  local candidate_state=""
  local candidate_poll_attempt

  for candidate_poll_attempt in $(seq 1 40); do
    candidate_task_json="$(
      candidate_request \
        "${candidate_web_base_url}${candidate_route}/${candidate_task_id}"
    )"
    candidate_state="$(jq -er '.status' <<<"$candidate_task_json")"
    if [[ "$candidate_state" == "SUCCEEDED" ]]; then
      printf '%s\n' "$candidate_task_json"
      return 0
    fi
    case "$candidate_state" in
      FAILED | CANCELED | EXPIRED)
        printf '%s task %s reached %s: %s\n' \
          "$candidate_route" \
          "$candidate_task_id" \
          "$candidate_state" \
          "$candidate_task_json" >&2
        return 1
        ;;
    esac
    sleep 0.25
  done
  printf '%s task %s did not succeed; last response: %s\n' \
    "$candidate_route" \
    "$candidate_task_id" \
    "$candidate_task_json" >&2
  return 1
}

candidate_assert_download() {
  local candidate_label="$1"
  local candidate_path="$2"
  local candidate_expected_kind="${3:-}"
  local candidate_signature

  candidate_request \
    -o "$candidate_download_file" \
    "${candidate_web_base_url}${candidate_path}"
  if [[ ! -s "$candidate_download_file" ]]; then
    printf '%s returned an empty asset from %s\n' \
      "$candidate_label" \
      "$candidate_path" >&2
    return 1
  fi
  if [[ "$candidate_expected_kind" == "png" ]]; then
    candidate_signature="$(
      od -An -tx1 -N8 "$candidate_download_file" |
        tr -d '[:space:]'
    )"
    if [[ "$candidate_signature" != "89504e470d0a1a0a" ]]; then
      printf '%s is not a PNG asset; signature=%s\n' \
        "$candidate_label" \
        "$candidate_signature" >&2
      return 1
    fi
  fi
  printf '%s asset_bytes=%s\n' \
    "$candidate_label" \
    "$(wc -c <"$candidate_download_file" | tr -d ' ')"
}

candidate_assert_task_asset() {
  local candidate_label="$1"
  local candidate_task_json="$2"
  local candidate_filter="$3"
  local candidate_expected_kind="${4:-}"
  local candidate_asset_path

  candidate_asset_path="$(
    jq -er \
      "$candidate_filter | select(type == \"string\" and length > 0)" \
      <<<"$candidate_task_json"
  )"
  candidate_assert_download \
    "$candidate_label" \
    "$candidate_asset_path" \
    "$candidate_expected_kind"
}

candidate_delete_and_assert_missing() {
  local candidate_route
  local candidate_task_id
  local candidate_status

  while IFS=$'\t' read -r candidate_route candidate_task_id; do
    [[ -n "$candidate_route" && -n "$candidate_task_id" ]] || continue
    candidate_request \
      -X DELETE \
      "${candidate_web_base_url}${candidate_route}/${candidate_task_id}" |
      jq -e '.success == true' >/dev/null
    candidate_status="$(
      curl --silent --show-error --max-time 10 \
        -o /dev/null \
        -w '%{http_code}' \
        -H "X-User-ID: ${candidate_owner}" \
        "${candidate_web_base_url}${candidate_route}/${candidate_task_id}"
    )"
    [[ "$candidate_status" == "404" ]]
  done <"$candidate_generated_tasks_file"
  : >"$candidate_generated_tasks_file"
}

trap 'record_candidate_failure "$LINENO"' ERR
trap 'cleanup_candidate_generation_resources' EXIT

if [[ "$candidate_allow_generation" != "true" ]]; then
  printf 'all-generation acceptance requires explicit mock generation opt-in\n' >&2
  exit 1
fi

candidate_schema_json="$(
  candidate_request \
    "${candidate_web_base_url}/api/v1/schema"
)"
candidate_provider_route="$(
  jq -er '.schema.runtime.http.legacy_routes.providers' \
    <<<"$candidate_schema_json"
)"

candidate_image_to_image_route="$(
  candidate_capability_route image-to-image
)"
candidate_image_to_image_id="$(
  candidate_submit_json image-to-image "$(
    jq -nc \
      --arg image "$candidate_inline_png" \
      '{
        prompt: "candidate image-to-image contract",
        reference_image_urls: [$image]
      }'
  )"
)"
candidate_image_to_image_json="$(
  candidate_wait_succeeded \
    "$candidate_image_to_image_route" \
    "$candidate_image_to_image_id"
)"
candidate_assert_task_asset \
  image-to-image \
  "$candidate_image_to_image_json" \
  '.image_urls[0]' \
  png

candidate_image_to_3d_route="$(
  candidate_capability_route image-to-3d
)"
candidate_image_to_3d_id="$(
  candidate_submit_json image-to-3d "$(
    jq -nc \
      --arg image "$candidate_inline_png" \
      '{
        image_url: $image,
        image_urls: [$image],
        target_formats: ["glb"],
        should_texture: true
      }'
  )"
)"
candidate_image_to_3d_json="$(
  candidate_wait_succeeded \
    "$candidate_image_to_3d_route" \
    "$candidate_image_to_3d_id"
)"
candidate_assert_task_asset \
  image-to-3d \
  "$candidate_image_to_3d_json" \
  '.model_urls.glb'

candidate_remesh_route="$(candidate_capability_route remesh)"
candidate_remesh_id="$(
  candidate_submit_json remesh '{
    "model_url": "https://assets.test/candidate-source.glb",
    "target_formats": ["glb"],
    "target_polycount": 10000
  }'
)"
candidate_remesh_json="$(
  candidate_wait_succeeded "$candidate_remesh_route" "$candidate_remesh_id"
)"
candidate_assert_task_asset \
  remesh \
  "$candidate_remesh_json" \
  '.model_urls.glb'

candidate_retexture_route="$(candidate_capability_route retexture)"
candidate_retexture_id="$(
  candidate_submit_json retexture '{
    "model_url": "https://assets.test/candidate-source.glb",
    "text_style_prompt": "candidate painted stone",
    "target_formats": ["glb"],
    "enable_pbr": true
  }'
)"
candidate_retexture_json="$(
  candidate_wait_succeeded \
    "$candidate_retexture_route" \
    "$candidate_retexture_id"
)"
candidate_assert_task_asset \
  retexture \
  "$candidate_retexture_json" \
  '.model_urls.glb'

candidate_rigging_route="$(candidate_capability_route rigging)"
candidate_rigging_id="$(
  candidate_submit_json rigging '{
    "model_url": "https://assets.test/candidate-character.glb",
    "height_meters": 1.8
  }'
)"
candidate_rigging_json="$(
  candidate_wait_succeeded "$candidate_rigging_route" "$candidate_rigging_id"
)"
candidate_assert_task_asset \
  rigging \
  "$candidate_rigging_json" \
  '.result.rigged_character_glb_url'

candidate_animation_route="$(candidate_capability_route animation)"
candidate_animation_id="$(
  candidate_submit_json animation "$(
    jq -nc \
      --arg rigging_task "$candidate_rigging_id" \
      '{rig_task_id: $rigging_task, action_id: 5}'
  )"
)"
candidate_animation_json="$(
  candidate_wait_succeeded "$candidate_animation_route" "$candidate_animation_id"
)"
candidate_assert_task_asset \
  animation \
  "$candidate_animation_json" \
  '.result.animation_glb_url'

base64 --decode \
  <<<"${candidate_inline_png#*,}" \
  >"$candidate_background_file"
candidate_background_route="$(
  candidate_capability_route background-removal
)"
candidate_background_files_field="$(
  candidate_multipart_field background-removal file_field
)"
candidate_background_params_field="$(
  candidate_multipart_field background-removal params_field
)"
candidate_background_json="$(
  candidate_request \
    -X POST \
    "${candidate_web_base_url}${candidate_background_route}" \
    -F "${candidate_background_files_field}=@${candidate_background_file};type=image/png;filename=candidate.png" \
    -F "${candidate_background_params_field}={\"expand\":240}"
)"
candidate_background_id="$(
  jq -er '.result | select(type == "string" and length > 0)' \
    <<<"$candidate_background_json"
)"
printf '%s\t%s\n' \
  "$candidate_background_route" \
  "$candidate_background_id" >>"$candidate_generated_tasks_file"
candidate_background_json="$(
  candidate_wait_succeeded \
    "$candidate_background_route" \
    "$candidate_background_id"
)"
candidate_assert_task_asset \
  background-removal \
  "$candidate_background_json" \
  '.tasks[0].download_path' \
  png

candidate_delete_and_assert_missing
printf 'all remaining generation capability contracts passed\n'
