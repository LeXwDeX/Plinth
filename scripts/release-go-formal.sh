#!/usr/bin/env bash
# Promote a tested exact main revision to the isolated legacy-formal stack (host and root: the
# legacy-formal entry of deploy/local/environments.json).
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
exec python3 "$SCRIPT_DIR/release-formal.py" local "$@"
