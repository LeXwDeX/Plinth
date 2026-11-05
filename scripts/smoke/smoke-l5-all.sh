#!/usr/bin/env bash
# L5 live-capability regression: run every smoke-l5-*.py in order and summarise.
# Costs real money; needs ART_AI_LIVE_ACK=1. Run through the release tool so BASE,
# ADMIN_* and EXPECT_SHA are set:
#   ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l5-all.sh [--report DIR]
# L5 runs locally only; it does not read runtime.env.
set -u
cd "$(dirname "$0")" || exit 2
if [ "${ART_AI_LIVE_ACK:-}" != "1" ]; then
  echo "refusing to run: L5 sends real, paid model requests; set ART_AI_LIVE_ACK=1 to acknowledge the cost" >&2
  exit 2
fi
REPORT_ARGS=()
if [ "${1:-}" = "--report" ] && [ -n "${2:-}" ]; then REPORT_ARGS=(--report "$2"); fi

scripts=(chat-models 2d-to-3d parallel three-view split 2d-params canvas 3d-params 2d-split agent-hard budget)
status=0
declare -a lines
for name in "${scripts[@]}"; do
  echo "##### smoke-l5-$name"
  log=$(mktemp)
  python3 "smoke-l5-$name.py" "${REPORT_ARGS[@]+"${REPORT_ARGS[@]}"}" 2>&1 | tee "$log"
  code=${PIPESTATUS[0]}
  summary=$(grep '^=== ' "$log" | tail -1)
  rm -f "$log"
  [ -n "$summary" ] || summary="=== smoke-l5-$name: no summary (exit $code) ==="
  lines+=("$summary")
  [ "$code" -eq 0 ] || status=1
done
echo
echo "##### L5 summary"
printf '%s\n' "${lines[@]}"
exit $status
