#!/usr/bin/env bash
# Gate「public tree」(dev_test.md §2, #261): fails when a tracked file contains an internal
# identifier. Same scan as `scripts/make-public-snapshot.py make`; see that file for the patterns.
#
#   scripts/check-public-tree.sh            # tracked files as they are in this working tree
#   scripts/check-public-tree.sh REV [...]  # tracked files of a revision (git archive)
#   extra make-public-snapshot.py check options (--pattern, --patterns-file, --no-local) follow
#
# CI has no local environments file, so only the built-in patterns (and any given here) apply;
# locally the untracked deploy/local values are scanned as well. Needs bash, git and python3.
set -euo pipefail
SCRIPTS=$(cd "$(dirname "$0")" && pwd)
command -v python3 >/dev/null 2>&1 || { echo "check-public-tree: python3 is required" >&2; exit 2; }
if [ "$#" -gt 0 ] && [ "${1#-}" = "$1" ]; then
  REV=$1
  shift
  exec python3 "$SCRIPTS/make-public-snapshot.py" check --rev "$REV" "$@"
fi
exec python3 "$SCRIPTS/make-public-snapshot.py" check --worktree "$@"
