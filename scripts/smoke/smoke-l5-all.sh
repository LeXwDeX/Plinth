#!/usr/bin/env bash
# L5 live-capability regression: run every smoke-l5-*.py in order and summarise.
# Costs real money; needs both live switches (--live and ART_AI_LIVE_ACK=1, #219) and a
# dev-test released live (release-env.py release dev-test --live-upstream). Run through
# the release tool so BASE, ADMIN_*, EXPECT_SHA and ART_AI_UPSTREAM_SHAPE are set:
#   ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l5-all.sh --live [--report DIR]
# L5 runs locally only; it does not read runtime.env.
set -u
cd "$(dirname "$0")" || exit 2
LIVE_ARGS=()
REPORT_ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --live) LIVE_ARGS=(--live); shift ;;
    --report) [ -n "${2:-}" ] || { echo "--report needs a directory" >&2; exit 2; }; REPORT_ARGS=(--report "$2"); shift 2 ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
done
python3 upstream_shape.py live-only "${LIVE_ARGS[@]+"${LIVE_ARGS[@]}"}" >/dev/null || exit 2

scripts=(chat-models 2d-to-3d parallel three-view split 2d-params canvas 3d-params 2d-split agent-hard budget)
status=0
declare -a lines
for name in "${scripts[@]}"; do
  echo "##### smoke-l5-$name"
  log=$(mktemp)
  python3 "smoke-l5-$name.py" "${LIVE_ARGS[@]}" "${REPORT_ARGS[@]+"${REPORT_ARGS[@]}"}" 2>&1 | tee "$log"
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
