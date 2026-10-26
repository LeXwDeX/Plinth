#!/usr/bin/env bash
set -Eeuo pipefail

reference_web_base_url="${REFERENCE_WEB_BASE_URL:?missing REFERENCE_WEB_BASE_URL (the reference environment base_url)}"
candidate_web_base_url="${CANDIDATE_WEB_BASE_URL:?missing CANDIDATE_WEB_BASE_URL}"
candidate_admin_username="${CANDIDATE_ADMIN_USERNAME:?missing CANDIDATE_ADMIN_USERNAME}"
candidate_admin_password="${CANDIDATE_ADMIN_PASSWORD:?missing CANDIDATE_ADMIN_PASSWORD}"
provider_diff_details="${PROVIDER_DIFF_DETAILS:-false}"
provider_diff_capability="${PROVIDER_DIFF_CAPABILITY:-}"
provider_audit_root="$(mktemp -d "${TMPDIR:-/tmp}/meshyai-provider-audit.XXXXXX")"

cleanup_provider_audit() {
  if [[ -d "$provider_audit_root" &&
    "$(basename -- "$provider_audit_root")" == meshyai-provider-audit.* ]]; then
    find "$provider_audit_root" -depth -mindepth 1 -delete
    rmdir "$provider_audit_root"
  fi
}
trap cleanup_provider_audit EXIT

provider_login_payload="$(
  jq -nc \
    --arg username "$candidate_admin_username" \
    --arg password "$candidate_admin_password" \
    '{username: $username, password: $password}'
)"

provider_login() {
  local provider_base_url="$1"
  local provider_cookie_jar="$2"

  curl --fail --silent --show-error --max-time 15 \
    -c "$provider_cookie_jar" \
    -H 'Content-Type: application/json' \
    -d "$provider_login_payload" \
    "${provider_base_url}/api/auth/login" >/dev/null
}

provider_fetch() {
  local provider_base_url="$1"
  local provider_cookie_jar="$2"
  local provider_query="$3"
  local provider_destination="$4"

  curl --fail --silent --show-error --max-time 15 \
    -b "$provider_cookie_jar" \
    "${provider_base_url}/api/providers${provider_query}" |
    jq -S . >"$provider_destination"
}

reference_cookie_jar="${provider_audit_root}/reference.cookie"
candidate_cookie_jar="${provider_audit_root}/candidate.cookie"
reference_all_json="${provider_audit_root}/reference-all.json"
candidate_all_json="${provider_audit_root}/candidate-all.json"
provider_capabilities_file="${provider_audit_root}/capabilities"

provider_login "$reference_web_base_url" "$reference_cookie_jar"
provider_login "$candidate_web_base_url" "$candidate_cookie_jar"
provider_fetch \
  "$reference_web_base_url" \
  "$reference_cookie_jar" \
  "" \
  "$reference_all_json"
provider_fetch \
  "$candidate_web_base_url" \
  "$candidate_cookie_jar" \
  "" \
  "$candidate_all_json"

jq -r '
    (if type == "array" then .[] else .providers[]? end)
    | .capabilities[]?
  ' \
  "$reference_all_json" \
  "$candidate_all_json" |
  sort -u >"$provider_capabilities_file"

provider_differences=0

compare_provider_projection() {
  local provider_capability="$1"
  local provider_query=""
  local reference_json="${provider_audit_root}/reference.json"
  local candidate_json="${provider_audit_root}/candidate.json"

  if [[ "$provider_capability" == "__all__" ]]; then
    cp "$reference_all_json" "$reference_json"
    cp "$candidate_all_json" "$candidate_json"
  else
    provider_query="?capability=${provider_capability}"
    provider_fetch \
      "$reference_web_base_url" \
      "$reference_cookie_jar" \
      "$provider_query" \
      "$reference_json"
    provider_fetch \
      "$candidate_web_base_url" \
      "$candidate_cookie_jar" \
      "$provider_query" \
      "$candidate_json"
  fi

  if cmp -s "$reference_json" "$candidate_json"; then
    printf '%s exact=yes\n' "$provider_capability"
    return 0
  fi

  printf '%s exact=no\n' "$provider_capability"
  provider_differences=$((provider_differences + 1))
  if [[ "$provider_diff_details" == "true" ]]; then
    diff -u \
      --label "reference:${provider_capability}" \
      --label "candidate:${provider_capability}" \
      "$reference_json" \
      "$candidate_json" || true
  fi
}

if [[ -n "$provider_diff_capability" ]]; then
  compare_provider_projection "$provider_diff_capability"
else
  compare_provider_projection "__all__"
  while IFS= read -r provider_capability; do
    [[ -n "$provider_capability" ]] || continue
    compare_provider_projection "$provider_capability"
  done <"$provider_capabilities_file"
fi

if [[ "$provider_differences" -ne 0 ]]; then
  printf 'provider discovery exact differences=%d\n' \
    "$provider_differences" >&2
  exit 1
fi

printf 'provider discovery exact parity passed\n'
