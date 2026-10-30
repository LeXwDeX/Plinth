#!/usr/bin/env bash
# One-command browser matrix run against a named environment (default dev-test).
#   bash scripts/acceptance/browser_matrix/run.sh [environment]
# Subset example:
#   B3_ITEMS=CNV-02 B3_ENGINES=chromium B3_LOCALES=zh-CN B3_VIEWPORTS=1440x900 bash scripts/acceptance/browser_matrix/run.sh
# Credentials come from `release-env.py smoke`; nothing is written to files. Output: $OUT or the system temp dir.
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
ENVIRONMENT="${1:-dev-test}"
cd "$REPO"
exec python3 scripts/release-env.py smoke "$ENVIRONMENT" -- node "$HERE/matrix.mjs"
