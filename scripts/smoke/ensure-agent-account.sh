#!/usr/bin/env bash
# Makes sure the Agent's own account exists and logs in (#215). `release-env.py release <env>` runs it after
# every successful release: dev-test data is fresh each time, so the account is recreated; an environment
# that keeps its data (formal-test) answers 409 and the login is only checked. Manual run:
#   python3 scripts/release-env.py smoke <env> -- bash scripts/smoke/ensure-agent-account.sh
# The wrapper sets BASE and ADMIN_* from the stack's runtime env and AGENT_TEST_USERNAME /
# AGENT_TEST_PASSWORD from `<release root>/production/agent-test.env` on the host (mode 0600, never in Git).
# Nothing secret is printed.
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
