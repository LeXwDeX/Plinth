#!/usr/bin/env bash
# Recreates the Agent's own dev-test account after a release (dev-test data is fresh on every release).
# Run through the release wrapper, which sets BASE and ADMIN_* from the stack's runtime env:
#   python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/ensure-agent-account.sh
# AGENT_TEST_USERNAME / AGENT_TEST_PASSWORD come from `<release root>/production/agent-test.env` on the
# dev-test host (mode 0600, never in Git); export them before calling. Nothing secret is printed.
set -u
: "${BASE:?}" "${ADMIN_USERNAME:?}" "${ADMIN_PASSWORD:?}" "${AGENT_TEST_USERNAME:?}" "${AGENT_TEST_PASSWORD:?}"
D=$(mktemp -d "${TMPDIR:-/tmp}/agent-account.XXXXXX"); trap 'rm -rf "$D"' EXIT
body() { python3 -c 'import json,os,sys; print(json.dumps({k: os.environ[v] if v.isupper() else v for k, v in (a.split("=", 1) for a in sys.argv[1:])}))' "$@"; }
post() { curl -s -o "$D/out.json" -w '%{http_code}' -X POST "$BASE$1" -H 'Content-Type: application/json' -H "Origin: $BASE" "${@:3}" --data-binary "$2"; }
c=$(post /api/auth/login "$(body username=ADMIN_USERNAME password=ADMIN_PASSWORD)" -c "$D/admin")
[ "$c" = 200 ] || { echo "FAIL: admin login ($c)"; exit 1; }
c=$(post /api/admin/users "$(body username=AGENT_TEST_USERNAME password=AGENT_TEST_PASSWORD 'name=Agent 测试开发账号' email=agent-test@example.test)" -b "$D/admin")
case "$c" in 200|201) echo "created agent account";; 409) echo "agent account already exists";; *) echo "FAIL: create agent account ($c)"; exit 1;; esac
c=$(post /api/auth/login "$(body username=AGENT_TEST_USERNAME password=AGENT_TEST_PASSWORD)" -c "$D/agent")
[ "$c" = 200 ] && echo "PASS: agent account logs in" || { echo "FAIL: agent login ($c)"; exit 1; }
