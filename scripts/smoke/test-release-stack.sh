#!/usr/bin/env bash
# Offline contract tests for environment-driven releases: the release entry
# names no host or path of its own and runs the historical dev-test command
# sequence for the host/root it is given, scripts/release-env.py resolves
# named environments and enforces the promotion rule, and the server steps
# honour the passed layout. No real SSH, Docker, forge or server is used:
# ssh/scp/gh/docker/curl are PATH mocks that record calls. Hosts below are
# placeholders; real targets live only in the untracked environments file.
set -euo pipefail

SCRIPTS=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL $*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Part 1: local entries (mock ssh/scp/gh, real git).
# ---------------------------------------------------------------------------
mkdir -p "$WORK/bin" "$WORK/origin.git" "$WORK/repo"
git -C "$WORK/origin.git" init -q --bare
git -C "$WORK/repo" init -q
git -C "$WORK/repo" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m first
FIRST=$(git -C "$WORK/repo" rev-parse HEAD)
echo payload >"$WORK/repo/file.txt"
git -C "$WORK/repo" add file.txt
git -C "$WORK/repo" -c user.name=t -c user.email=t@example.invalid commit -q -m second
SECOND=$(git -C "$WORK/repo" rev-parse HEAD)
git -C "$WORK/repo" remote add origin "$WORK/origin.git"
git -C "$WORK/repo" push -q origin HEAD:main
git -C "$WORK/repo" checkout -q -b unpublished
echo other >"$WORK/repo/other.txt"
git -C "$WORK/repo" add other.txt
git -C "$WORK/repo" -c user.name=t -c user.email=t@example.invalid commit -q -m unpublished
UNPUBLISHED=$(git -C "$WORK/repo" rev-parse HEAD)

cat >"$WORK/bin/ssh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
host=$1; shift
command=$*
printf 'ssh %s %s\n' "$host" "$command" >>"$MOCK_TRACE"
case "$command" in
  *'tar -x -C '*' -f '*) ;;
  *'tar -x -C '*) shasum -a 256 | cut -d' ' -f1 >>"$MOCK_TRACE" ;;
  *'docker inspect -f'*) printf '%s\n' "$MOCK_RUNNING" ;;
  *'/releases/manifest.txt'*) printf '%s\n' "$MOCK_MANIFEST" ;;
  *'python3 - '*'/production/runtime.env'*) cat >/dev/null; printf '{"ADMIN_USERNAME":"u","ADMIN_PASSWORD":"p"}\n' ;;
  *'release-steps.sh'*) [ "${MOCK_SERVER_FAIL:-0}" = 0 ] || exit 1 ;;
esac
MOCK
cat >"$WORK/bin/scp" <<'MOCK'
#!/usr/bin/env bash
if [[ "$2" == */art-ai-source.* ]]; then
  printf 'scp -q archive:%s %s\n' "$(shasum -a 256 <"$2" | cut -d' ' -f1)" "$3" >>"$MOCK_TRACE"
else
  printf 'scp %s\n' "$*" >>"$MOCK_TRACE"
fi
MOCK
cat >"$WORK/bin/gh" <<'MOCK'
#!/usr/bin/env bash
printf 'gh %s\n' "$*" >>"$MOCK_TRACE"
cat "$MOCK_NOTE"
MOCK
cat >"$WORK/bin/date" <<'MOCK'
#!/usr/bin/env bash
echo 20261020T010203Z
MOCK
chmod +x "$WORK/bin/"*
mkdir -p "$WORK/tmp"
export PATH="$WORK/bin:$PATH" MOCK_TRACE="$WORK/trace" MOCK_NOTE="$WORK/note.json" TMPDIR="$WORK/tmp"
ARCHIVE_SHA=$(git -C "$WORK/repo" archive origin/main | shasum -a 256 | cut -d' ' -f1)
SHORT=${SECOND:0:7}
# The entry has no host or root of its own (#249); dev-test is a placeholder here.
DEFAULT_HOST=root@dev.example.invalid
DEFAULT_ROOT=/srv/dev
! grep -Eq 'RELEASE_(HOST|ROOT):-' "$SCRIPTS/release-go-testenv.sh" || fail "release-go-testenv.sh still has a host/root default"

# 1a. Without a target the entry refuses before any remote call; with one it
#     runs exactly the commands of the pre-parameterisation script.
: >"$MOCK_TRACE"
if (cd "$WORK/repo" && env -u RELEASE_HOST -u RELEASE_ROOT bash "$SCRIPTS/release-go-testenv.sh") >/dev/null 2>&1; then
  fail "entry ran without RELEASE_HOST/RELEASE_ROOT"
fi
[ ! -s "$MOCK_TRACE" ] || fail "entry contacted a host without a target"
(cd "$WORK/repo" && RELEASE_HOST=$DEFAULT_HOST RELEASE_ROOT=$DEFAULT_ROOT bash "$SCRIPTS/release-go-testenv.sh") >"$WORK/default.log"
cat >"$WORK/expected-default" <<EOF
ssh $DEFAULT_HOST mv $DEFAULT_ROOT/app $DEFAULT_ROOT/app.prev-\$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true; mkdir -p $DEFAULT_ROOT/app
ssh $DEFAULT_HOST tar -x -C $DEFAULT_ROOT/app
$ARCHIVE_SHA
scp -q $SCRIPTS/release-server-steps.sh $DEFAULT_HOST:/tmp/art-ai-release-steps.sh
scp -q $SCRIPTS/retain-art-ai-images.py $DEFAULT_HOST:/tmp/art-ai-retain-images.py
ssh $DEFAULT_HOST RETENTION_SCRIPT=/tmp/art-ai-retain-images.py bash /tmp/art-ai-release-steps.sh '$SHORT-20261020T010203Z' '$SECOND' '$DEFAULT_ROOT'
EOF
diff -u "$WORK/expected-default" "$MOCK_TRACE" || fail "default release commands changed"
grep -qx "\[release\] main=$SECOND tag=$SHORT-20261020T010203Z" "$WORK/default.log" || fail "default banner changed"
echo "PASS entry/target-required-and-commands-unchanged"

# 1b. Unsafe stack parameters are refused before any remote call.
for bad in "RELEASE_SERVER_ENV=A=1;rm" "RELEASE_SERVER_ENV=A=\$(x)" "RELEASE_SERVER_ENV=A='b'" \
  "RELEASE_KEEP_SOURCE_SNAPSHOTS=0" "RELEASE_KEEP_SOURCE_SNAPSHOTS=x" "RELEASE_ROOT=relative" \
  "RELEASE_ROOT=/srv/../etc" "RELEASE_HOST=root@a;b" "RELEASE_HOST=@host"; do
  : >"$MOCK_TRACE"
  if (cd "$WORK/repo" && env RELEASE_HOST=$DEFAULT_HOST RELEASE_ROOT=$DEFAULT_ROOT "$bad" \
    bash "$SCRIPTS/release-go-testenv.sh") >/dev/null 2>&1; then
    fail "entry accepted $bad"
  fi
  [ ! -s "$MOCK_TRACE" ] || fail "entry contacted a host for $bad"
done
echo "PASS entry/unsafe-parameters-refused"

# Environments file with placeholder hosts.
FORGE=https://forge.example.invalid/owner/project
export ART_AI_ENVIRONMENTS_FILE="$WORK/environments.json"
cat >"$ART_AI_ENVIRONMENTS_FILE" <<EOF
{"version": 1, "acceptance": {"forge_repo": "owner/project", "forge_url": "$FORGE", "issue": 7},
 "environments": {
  "dev-test": {"ssh": "$DEFAULT_HOST", "root": "$DEFAULT_ROOT", "base_url": "http://dev.example.invalid:13333",
               "compose_project": "dev_project", "protect_projects": ["candidate_project", "other_project"],
               "protected_paths": ["/srv/legacy-app"], "backup": {"scope": "database", "keep": 2}},
  "formal-test": {"ssh": "root@formal.example.invalid", "root": "/srv/formal", "base_url": "http://formal.example.invalid:13333",
                  "compose_project": "formal-project", "promote_from": "dev-test", "protected_paths": [],
                  "extra_compose_file": "/srv/formal/production/compose.network.yml", "source_transfer": "scp",
                  "build": {"network": "host", "goproxy": "https://proxy.example.invalid/go/",
                            "add_hosts": ["npm.example.invalid:10.0.0.5", "proxy.example.invalid:10.0.0.6"]},
                  "single_stack": {"image_keep": 1, "keep_source_snapshots": 1, "allow_bootstrap": true},
                  "layout": {"GO_WEB_BIND_IP": "10.0.0.9", "GO_WEB_HOST_PORT": "13333",
                             "GO_PROD_DB_DIR": "/srv/formal/production/data",
                             "GO_LEGACY_INPUT_ASSETS_DIR": "/srv/formal/production/legacy-assets"}}}}
EOF
note() { # $1=revision $2=image tag $3=L1_agent status
  python3 - "$1" "$2" "$3" "$FORGE" >"$MOCK_NOTE" <<'PY'
import json, sys
revision, tag, agent, forge = sys.argv[1:]
check = lambda status: {"status": status, "failed": 0, "passed": 3, "skipped": 0, "soft_failed": 0,
                        "evidence": forge + "/issues/7#issuecomment-1"}
report = {"formal_promotion": 1, "revision": revision, "image_tag": tag,
          "checks": {"L0": check("pass"), "L1": check("pass"), "L1_agent": check(agent), "related": check("pass")}}
print(json.dumps({"body": "accepted\n```json\n" + json.dumps(report) + "\n```\n",
                  "issue_url": "https://api.forge.example.invalid/repos/owner/project/issues/7"}))
PY
}
TAG2="$SHORT-20261019T000000Z"
export MOCK_RUNNING="$SECOND"
export MOCK_MANIFEST="$TAG2 $SECOND 2026-10-19T00:00:00Z"

# 1c. release-env.py dev-test == the plain entry for the same target, plus the
#     Compose project and the other projects on its host (#261: the repository
#     names no environment's project, so the server steps need them passed),
#     and the protected directories for the migration tools (#334).
: >"$MOCK_TRACE"
(cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release dev-test) >/dev/null
sed "s#^ssh \\([^ ]*\\) RETENTION_SCRIPT=#ssh \\1 RELEASE_COMPOSE_PROJECT=dev_project RELEASE_PROTECT_PROJECTS=candidate_project,other_project RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2 ART_AI_PROTECTED_PATHS=$DEFAULT_ROOT:/srv/legacy-app RETENTION_SCRIPT=#" \
  "$WORK/expected-default" >"$WORK/expected-dev-test"
diff -u "$WORK/expected-dev-test" "$MOCK_TRACE" || fail "dev-test via release-env.py differs from the plain entry"
if (cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release dev-test --revision "$FIRST") >/dev/null 2>&1; then
  fail "dev-test accepted a pinned revision"
fi
echo "PASS env/dev-test-equals-plain-entry"

# 1d. formal-test: accepted running revision is promoted with the stack layout.
note "$SECOND" "$TAG2" pass
: >"$MOCK_TRACE"
(cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release formal-test --acceptance-note 11) >"$WORK/formal.log"
cat >"$WORK/expected" <<EOF
ssh $DEFAULT_HOST docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' dev_project-art-ai-go-1
gh api repos/owner/project/issues/comments/11
ssh $DEFAULT_HOST cat $DEFAULT_ROOT/releases/manifest.txt
ssh root@formal.example.invalid mv /srv/formal/app /srv/formal/app.prev-\$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true; mkdir -p /srv/formal/app /srv/formal/releases
scp -q archive:$ARCHIVE_SHA root@formal.example.invalid:/srv/formal/releases/source-$SHORT-20261020T010203Z.tar
ssh root@formal.example.invalid tar -x -C /srv/formal/app -f /srv/formal/releases/source-$SHORT-20261020T010203Z.tar && rm -f /srv/formal/releases/source-$SHORT-20261020T010203Z.tar
scp -q $SCRIPTS/release-server-steps.sh root@formal.example.invalid:/tmp/art-ai-release-steps.sh
scp -q $SCRIPTS/retain-art-ai-images.py root@formal.example.invalid:/tmp/art-ai-retain-images.py
ssh root@formal.example.invalid RELEASE_COMPOSE_PROJECT=formal-project RELEASE_SINGLE_STACK=1 RELEASE_IMAGE_KEEP=1 RELEASE_ALLOW_BOOTSTRAP=1 GO_WEB_BIND_IP=10.0.0.9 GO_WEB_HOST_PORT=13333 GO_PROD_DB_DIR=/srv/formal/production/data GO_LEGACY_INPUT_ASSETS_DIR=/srv/formal/production/legacy-assets RELEASE_EXTRA_COMPOSE_FILE=/srv/formal/production/compose.network.yml RELEASE_BUILD_NETWORK=host RELEASE_GOPROXY=https://proxy.example.invalid/go/ RELEASE_BUILD_ADD_HOSTS=npm.example.invalid:10.0.0.5,proxy.example.invalid:10.0.0.6 ART_AI_PROTECTED_PATHS=/srv/formal RETENTION_SCRIPT=/tmp/art-ai-retain-images.py bash /tmp/art-ai-release-steps.sh '$SHORT-20261020T010203Z' '$SECOND' '/srv/formal'
ssh root@formal.example.invalid cd /srv/formal && ls -1d app.prev-[0-9]*T[0-9]*Z 2>/dev/null | sort | head -n -1 | xargs -r rm -rf --
EOF
diff -u "$WORK/expected" "$MOCK_TRACE" || fail "formal-test release commands differ"
[ -z "$(ls -A "$WORK/tmp")" ] || fail "local source archive left behind"
echo "PASS env/formal-test-promotes-accepted-revision"

# 1e. An earlier accepted revision may be named; the archive is that commit.
note "$FIRST" "${FIRST:0:7}-20261018T000000Z" pass
export MOCK_MANIFEST="${FIRST:0:7}-20261018T000000Z $FIRST 2026-10-18T00:00:00Z
$TAG2 $SECOND 2026-10-19T00:00:00Z"
: >"$MOCK_TRACE"
(cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release formal-test --revision "$FIRST" --acceptance-note 12) >/dev/null
grep -q "$(git -C "$WORK/repo" archive "$FIRST" | shasum -a 256 | cut -d' ' -f1)" "$MOCK_TRACE" || fail "named revision not archived"
grep -q "'${FIRST:0:7}-20261020T010203Z' '$FIRST'" "$MOCK_TRACE" || fail "named revision tag/sha not passed"
echo "PASS env/formal-test-named-earlier-revision"

# 1f. Promotion refusals never contact the formal-test host.
refuse() { # $1=label, $2=expected refusal text, rest=release-env arguments
  local label=$1 expected=$2; shift 2
  : >"$MOCK_TRACE"
  if (cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release formal-test "$@") >"$WORK/refuse.log" 2>&1; then
    fail "formal-test accepted: $label"
  fi
  ! grep -q formal.example.invalid "$MOCK_TRACE" || fail "formal-test contacted for: $label"
  grep -q "refused: .*$expected" "$WORK/refuse.log" || { cat "$WORK/refuse.log" >&2; fail "wrong refusal for: $label"; }
}
export MOCK_MANIFEST="$TAG2 $SECOND 2026-10-19T00:00:00Z"
note "$SECOND" "$TAG2" pass
refuse "missing acceptance note" "needs --acceptance-note"
note "$SECOND" "$TAG2" fail
refuse "failed Agent acceptance" "missing, failed, skipped" --acceptance-note 13
note "$SECOND" "$TAG2" skip
refuse "skipped Agent acceptance" "missing, failed, skipped" --acceptance-note 13
note "$FIRST" "$TAG2" pass
refuse "report for another revision" "revision does not match" --acceptance-note 13
note "$SECOND" "abcdef0-20261019T000000Z" pass
refuse "image tag absent from the dev-test manifest" "absent from the successful test release manifest" --acceptance-note 13
note "$UNPUBLISHED" "$TAG2" pass
export MOCK_MANIFEST="$TAG2 $UNPUBLISHED 2026-10-19T00:00:00Z"
refuse "revision not on origin/main" "is not on origin/main" --revision "$UNPUBLISHED" --acceptance-note 13
refuse "malformed revision" "40-character" --revision abc --acceptance-note 13
export MOCK_MANIFEST="$TAG2 $SECOND 2026-10-19T00:00:00Z"
note "$SECOND" "$TAG2" pass
export MOCK_SERVER_FAIL=1
: >"$MOCK_TRACE"
if (cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" release formal-test --acceptance-note 14) >/dev/null 2>&1; then
  fail "formal-test reported success after a server failure"
fi
! grep -q 'xargs -r rm' "$MOCK_TRACE" || fail "snapshots pruned after a failed release"
unset MOCK_SERVER_FAIL
echo "PASS env/promotion-refusals"

# 1g. Smoke wrapper exports BASE, admin login, EXPECT_SHA and container only.
(cd "$WORK/repo" && python3 "$SCRIPTS/release-env.py" smoke formal-test -- \
  python3 -c 'import os,json;print(json.dumps({k:os.environ.get(k) for k in ("BASE","ADMIN_USERNAME","ADMIN_PASSWORD","EXPECT_SHA","RELEASE_CONTAINER")}))') >"$WORK/smoke.json"
python3 - "$WORK/smoke.json" "$SECOND" <<'PY'
import json, sys
values = json.load(open(sys.argv[1]))
assert values == {"BASE": "http://formal.example.invalid:13333", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p",
                  "EXPECT_SHA": sys.argv[2], "RELEASE_CONTAINER": "formal-project-art-ai-go-1"}, values
PY
echo "PASS env/smoke-wrapper"

# 1h. Invalid environments files are refused.
for broken in '{"version": 2, "environments": {}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "relative", "base_url": "http://h", "compose_project": "p"}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "build": {"add_hosts": ["no-address"]}}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "build": {"add_hosts": "h:10.0.0.1"}}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "promote_from": "x"}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "single_stack": {}, "layout": {"OTHER": "1"}}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "protect_projects": ["Bad Name"]}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "protect_projects": ["p"]}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "protect_projects": "q"}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "backup": {"scope": "database", "keep": 2}}}}' \
  '{"version": 1, "environments": {"x": {"ssh": "root@h", "root": "/r", "base_url": "http://h", "compose_project": "p", "protect_projects": ["q"], "single_stack": {}}}}'; do
  printf '%s\n' "$broken" >"$WORK/broken.json"
  if ART_AI_ENVIRONMENTS_FILE="$WORK/broken.json" python3 "$SCRIPTS/release-env.py" list >/dev/null 2>&1; then
    fail "accepted environments file: $broken"
  fi
done
python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$SCRIPTS/../deploy/environments.example.json" || fail "template is not JSON"
echo "PASS env/invalid-files-refused"

# ---------------------------------------------------------------------------
# Part 2: server steps (mock docker/curl, stub capacity/preflight/retention).
# ---------------------------------------------------------------------------
SERVER="$WORK/server"
mkdir -p "$SERVER/bin"
cat >"$SERVER/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  ps) echo "ps $*" >>"$S_TRACE"; [ "${S_CONTAINERS:-0}" = 0 ] || echo abc123 ;;
  build) echo "build $*" >>"$S_TRACE" ;;
  run)
    case "$*" in
      *'id -u artai'*) echo '100 101' ;;
      *write-check*) ;;
      *) exit 1 ;;
    esac ;;
  inspect)
    [ "${S_CONTAINERS:-0}" = 1 ] || exit 1
    echo 'art-ai-go-production:previous' ;;
  compose)
    printf 'compose %s | bind=%s port=%s db=%s legacy=%s\n' "$*" "${GO_WEB_BIND_IP:-}" "${GO_WEB_HOST_PORT:-}" \
      "${GO_PROD_DB_DIR:-}" "${GO_LEGACY_INPUT_ASSETS_DIR:-}" >>"$S_TRACE"
    case " $* " in
      *' up '*)
        echo "$GO_IMAGE_TAG" >"$S_STATE"
        if [ "$GO_IMAGE_TAG" = candidate ] && [ "${S_CANDIDATE_WRITE:-0}" = 1 ]; then
          for directory in "$GO_PROD_DB_DIR" "$GO_PROD_STORAGE_DIR" "$GO_PROD_WEB_DATA_DIR"; do
            printf candidate >"$directory/candidate-marker"
          done
        fi ;;
      *' stop '*) echo stopped >"$S_STATE" ;;
    esac ;;
  *) echo "unexpected docker $*" >&2; exit 1 ;;
esac
MOCK
cat >"$SERVER/bin/curl" <<'MOCK'
#!/usr/bin/env bash
echo "curl ${*: -1}" >>"$S_TRACE"
state=$(cat "$S_STATE")
if [ "$state" = stopped ] || { [ "${S_UNHEALTHY:-0}" = 1 ] && [ "$state" = candidate ]; }; then printf 503; else printf 200; fi
MOCK
printf '#!/usr/bin/env bash\nexit 0\n' >"$SERVER/bin/sleep"
cat >"$SERVER/bin/tar" <<'MOCK'
#!/usr/bin/env bash
echo "tar $*" >>"$S_TRACE"
exec /usr/bin/tar "$@"
MOCK
chmod +x "$SERVER/bin/"*

new_root() { # $1=name; prints the root path
  local root="$SERVER/$1"
  mkdir -p "$root/app/scripts" "$root/production"
  cp "$SCRIPTS/release-lock.py" "$root/app/scripts/release-lock.py"
  touch "$root/app/docker-compose.go-production.yml" "$root/app/Dockerfile.go-runtime"
  cat >"$root/app/scripts/release-capacity.py" <<'STUB'
import os, sys
scope = sys.argv[5] if len(sys.argv) > 5 else "full"
line = "capacity " + " ".join(sys.argv[2:4])
if scope != "full":
    line += " scope=" + scope
open(os.environ["S_TRACE"], "a").write(line + "\n")
STUB
  cat >"$root/app/scripts/retain-release-backups.py" <<'STUB'
import os, sys
open(os.environ["S_TRACE"], "a").write("backup-retention " + " ".join(sys.argv[1:]) + "\n")
open(os.environ["S_TRACE"], "a").write(
    "backup-retention-lock %s %s\n" % (os.environ.get("RELEASE_LOCK_FD", ""),
                                        os.environ.get("RELEASE_LOCK_ROOT", "")))
if os.environ.get("S_BACKUP_RETENTION_FAIL") == "1":
    raise SystemExit(1)
STUB
  cat >"$root/app/scripts/cutover-go-production.sh" <<'STUB'
#!/usr/bin/env bash
echo "preflight db=${GO_PROD_DB_DIR:-} legacy=${GO_LEGACY_GENERATED_IMAGES_DIR:-}" >>"$S_TRACE"
echo "preflight-project ${GO_PROD_PROJECT:-}" >>"$S_TRACE"
STUB
  cat >"$root/retention.py" <<'STUB'
import os, sys
open(os.environ["S_TRACE"], "a").write("retention " + " ".join(sys.argv[1:]) + "\n")
STUB
  printf "GO_APP_ENV='test'\nLLM_MODEL='deepseek'\n" >"$root/production/runtime.env"
  chmod 600 "$root/production/runtime.env"
  printf '%s\n' "$root"
}

layout() { # $1=root; prints KEY=VALUE words like release-env.py sends them
  printf '%s ' GO_WEB_BIND_IP=127.0.0.9 GO_WEB_HOST_PORT=13333 \
    "GO_PROD_DB_DIR=$1/production/data" "GO_PROD_STORAGE_DIR=$1/production/storage-go" \
    "GO_PROD_WEB_DATA_DIR=$1/production/web-data" "GO_PROD_LOG_DIR=$1/production/logs" \
    "GO_LEGACY_GENERATED_IMAGES_DIR=$1/production/legacy-generated" "GO_LEGACY_ASSET_CACHE_DIR=$1/production/legacy-cache" \
    "GO_LEGACY_THUMBNAIL_CACHE_DIR=$1/production/legacy-thumbnails" "GO_LEGACY_INPUT_ASSETS_DIR=$1/production/legacy-assets"
}

run_steps() { # $1=root, rest=env assignments; sets RESULT
  local root=$1; shift
  : >"$S_TRACE"
  set +e
  env PATH="$SERVER/bin:$PATH" RETENTION_SCRIPT="$root/retention.py" "$@" \
    bash "$SCRIPTS/release-server-steps.sh" candidate "$SECOND" "$root" >"$root/out.log" 2>&1
  RESULT=$?
  set -e
}

run_locked_steps() { # $1=root, rest=env assignments; runs under the inherited release lock
  local root=$1; shift
  : >"$S_TRACE"
  set +e
  env PATH="$SERVER/bin:$PATH" RETENTION_SCRIPT="$root/retention.py" "$@" \
    python3 "$root/app/scripts/release-lock.py" run --root "$root" -- \
    bash "$SCRIPTS/release-server-steps.sh" candidate "$SECOND" "$root" >"$root/out.log" 2>&1
  RESULT=$?
  set -e
}
export S_TRACE="$WORK/server-trace" S_STATE="$WORK/server-state"
STABLE=(RELEASE_COMPOSE_PROJECT=formal-project RELEASE_SINGLE_STACK=1 RELEASE_IMAGE_KEEP=1 RELEASE_ALLOW_BOOTSTRAP=1)
SHARED=(RELEASE_COMPOSE_PROJECT=dev_project RELEASE_PROTECT_PROJECTS=candidate_project,other_project)
mode_of() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

# 2a. Bootstrap: empty stack, the passed layout drives every call.
root=$(new_root bootstrap); echo none >"$S_STATE"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=0 "${STABLE[@]}" $(layout "$root") \
  RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2
[ "$RESULT" = 0 ] || { cat "$root/out.log" >&2; fail "bootstrap did not succeed"; }
grep -qx 'capacity - bootstrap scope=database' "$S_TRACE" || fail "bootstrap capacity phase missing"
grep -q "^preflight db=$root/production/data legacy=$root/production/legacy-generated" "$S_TRACE" || fail "preflight did not get the layout"
grep -q -- "-p formal-project -f $root/app/docker-compose.go-production.yml up -d --no-deps art-ai-go | bind=127.0.0.9 port=13333 db=$root/production/data legacy=$root/production/legacy-assets" "$S_TRACE" || fail "compose did not get project/layout"
grep -qx 'curl http://127.0.0.9:13333/readyz' "$S_TRACE" || fail "health did not use the bind address"
grep -qx 'preflight-project formal-project' "$S_TRACE" || fail "preflight did not get the Compose project"
! grep -q '^retention\|^tar\|stop\|^backup-retention' "$S_TRACE" || fail "bootstrap ran retention, backup or stop"
grep -q "^candidate $SECOND " "$root/releases/manifest.txt" || fail "bootstrap manifest missing"
for name in generated cache thumbnails assets; do
  [ "$(mode_of "$root/production/legacy-$name")" = 750 ] || fail "legacy $name mode"
done
grep -qx "GO_IMAGE_TAG=candidate" "$root/production/compose.env" &&
  grep -qx "GO_SOURCE_COMMIT=$SECOND" "$root/production/compose.env" &&
  grep -qx "GO_RUNTIME_ENV_FILE=$root/production/runtime.env" "$root/production/compose.env" &&
  grep -qx "GO_LEGACY_ASSET_CACHE_DIR=$root/production/legacy-cache" "$root/production/compose.env" || fail "compose.env content"
[ "$(mode_of "$root/production/compose.env")" = 600 ] || fail "compose.env mode"
echo "PASS server/bootstrap"

# 2b. Bootstrap health failure: candidate stopped, report kept, no manifest.
root=$(new_root bootstrap-unhealthy); echo none >"$S_STATE"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=0 S_UNHEALTHY=1 "${STABLE[@]}" $(layout "$root")
[ "$RESULT" = 1 ] || fail "unhealthy bootstrap succeeded"
grep -q ' stop art-ai-go' "$S_TRACE" || fail "unhealthy bootstrap candidate not stopped"
[ -f "$root/production/reports/release-failures/candidate-bootstrap-unhealthy-after-up.txt" ] || fail "bootstrap report missing"
[ ! -e "$root/releases/manifest.txt" ] && [ ! -e "$root/production/compose.env" ] || fail "unhealthy bootstrap recorded"
echo "PASS server/bootstrap-unhealthy"

# 2c. A database without a container is never treated as an empty stack.
root=$(new_root bootstrap-db); mkdir -p "$root/production/data"; touch "$root/production/data/art-ai.db"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=0 "${STABLE[@]}" $(layout "$root")
[ "$RESULT" = 1 ] && ! grep -q '^build\|^compose' "$S_TRACE" || fail "bootstrap over an existing database"
echo "PASS server/bootstrap-refuses-existing-database"

# 2d. Without RELEASE_ALLOW_BOOTSTRAP a missing instance still aborts.
root=$(new_root no-bootstrap); echo none >"$S_STATE"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=0 RELEASE_COMPOSE_PROJECT=formal-project RELEASE_SINGLE_STACK=1 $(layout "$root")
[ "$RESULT" = 1 ] && ! grep -q ' up ' "$S_TRACE" || fail "missing instance was created without bootstrap"
echo "PASS server/no-bootstrap-without-permission"

# 2e. Single-stack update: backup, retention with one rollback image, compose.env.
root=$(new_root update); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
printf 'services: {}\n' >"$root/production/compose.network.yml"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=1 "${STABLE[@]}" $(layout "$root") \
  "RELEASE_EXTRA_COMPOSE_FILE=$root/production/compose.network.yml" RELEASE_BUILD_NETWORK=host \
  RELEASE_GOPROXY=https://proxy.example.invalid/go/ \
  RELEASE_BUILD_ADD_HOSTS=npm.example.invalid:10.0.0.5,proxy.example.invalid:10.0.0.6 \
  "ART_AI_PROTECTED_PATHS=$root:/srv/legacy-app"
[ "$RESULT" = 0 ] || { cat "$root/out.log" >&2; fail "single-stack update failed"; }
grep -q -- "-p formal-project -f $root/app/docker-compose.go-production.yml -f $root/production/compose.network.yml up -d" "$S_TRACE" || fail "extra compose file not applied"
grep -q -- "^build build --network host --build-arg GOPROXY=https://proxy.example.invalid/go/ --add-host npm.example.invalid:10.0.0.5 --add-host proxy.example.invalid:10.0.0.6 --target runtime-with-migration-tools" "$S_TRACE" || fail "build options missing"
grep -qx "ART_AI_PROTECTED_PATHS=$root:/srv/legacy-app" "$root/production/compose.env" || fail "compose.env lacks protected paths"
grep -qx 'capacity formal-project-art-ai-go-1 before-build' "$S_TRACE" || fail "update capacity container"
[ -f "$root/backups/release-candidate/data.tar" ] || fail "update backup missing"
grep -qx "retention --apply --rollback-image art-ai-go-production:previous --root $root --project formal-project --keep 1" "$S_TRACE" || fail "single-stack retention arguments"
! grep -q '^backup-retention' "$S_TRACE" || fail "full-scope release ran backup retention"
[ -f "$root/production/compose.env" ] || fail "update compose.env missing"
echo "PASS server/single-stack-update-retention"

# 2f. Layout paths outside the release root are refused before building.
for bad in "GO_PROD_DB_DIR=/elsewhere/production/data" "GO_LEGACY_ASSET_CACHE_DIR=/elsewhere/cache" \
  "GO_PROD_STORAGE_DIR=ROOT/production/data" "GO_LEGACY_INPUT_ASSETS_DIR=ROOT/production/legacy-x/../../etc"; do
  root=$(new_root "outside-$RANDOM")
  run_steps "$root" S_CONTAINERS=0 "${STABLE[@]}" "${bad/ROOT/$root}"
  [ "$RESULT" = 1 ] && ! grep -q '^build' "$S_TRACE" && grep -q 'does not match the release root layout' "$root/out.log" ||
    fail "layout accepted: $bad"
done
for bad in "/elsewhere/compose.network.yml" "ROOT/production/missing.yml" "ROOT/production/../x.yml" "ROOT/production/compose.txt"; do
  root=$(new_root "extra-$RANDOM"); touch "$root/production/compose.txt" "$root/x.yml"
  run_steps "$root" S_CONTAINERS=0 "${STABLE[@]}" "RELEASE_EXTRA_COMPOSE_FILE=${bad/ROOT/$root}"
  [ "$RESULT" = 1 ] && ! grep -q '^build' "$S_TRACE" || fail "extra compose file accepted: $bad"
done
echo "PASS server/layout-outside-root-refused"

# 2g. Shared host with only its project parameters: data directories derived
#     from the root, loopback health, retention under the root protecting the
#     other projects, no bootstrap probe, no compose.env and no build mirror.
#     The repository names no environment's project (#261): without one the
#     server steps refuse before building.
! grep -Eq 'RELEASE_COMPOSE_PROJECT:-[a-z]' "$SCRIPTS/release-server-steps.sh" || fail "server steps still default a Compose project"
root=$(new_root no-project); echo previous >"$S_STATE"
run_steps "$root" S_CONTAINERS=1
[ "$RESULT" = 1 ] && ! grep -q '^build\|^compose' "$S_TRACE" && grep -q 'RELEASE_COMPOSE_PROJECT is required' "$root/out.log" ||
  fail "server steps ran without a Compose project"
for bad in "RELEASE_PROTECT_PROJECTS=Bad,x" "RELEASE_SINGLE_STACK=yes" "RELEASE_IMAGE_KEEP=0"; do
  run_steps "$root" S_CONTAINERS=1 RELEASE_COMPOSE_PROJECT=dev_project "$bad"
  [ "$RESULT" = 1 ] && ! grep -q '^build' "$S_TRACE" || fail "server steps accepted: $bad"
done
run_steps "$root" S_CONTAINERS=1 "${STABLE[@]}" RELEASE_PROTECT_PROJECTS=other
[ "$RESULT" = 1 ] && ! grep -q '^build' "$S_TRACE" || fail "single-stack host accepted other projects"
root=$(new_root default); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
DEFAULT_PROJECT=dev_project
run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}"
[ "$RESULT" = 0 ] || { cat "$root/out.log" >&2; fail "default release failed"; }
! grep -q '^ps ' "$S_TRACE" || fail "default release probed for bootstrap"
grep -q -- "-p $DEFAULT_PROJECT -f .* up -d --no-deps art-ai-go | bind= port= db=$root/production/data legacy=\$" "$S_TRACE" || fail "default compose environment changed"
grep -q "^preflight db=$root/production/data legacy=\$" "$S_TRACE" || fail "default preflight environment changed"
grep -qx 'curl http://127.0.0.1:13333/readyz' "$S_TRACE" || fail "default health URL changed"
grep -qx "retention --apply --rollback-image art-ai-go-production:previous --root $root --project dev_project --protect-project candidate_project --protect-project other_project" "$S_TRACE" || fail "default retention arguments changed"
grep -qx 'full' "$root/backups/release-candidate/scope" || fail "full scope metadata changed"
grep -qx 'preflight-project dev_project' "$S_TRACE" || fail "default preflight project changed"
grep -qx "capacity $DEFAULT_PROJECT-art-ai-go-1 before-build" "$S_TRACE" || fail "default capacity container changed"
! grep -q '^backup-retention' "$S_TRACE" || fail "full-scope default release ran backup retention"
[ ! -e "$root/production/compose.env" ] || fail "default release wrote compose.env"
grep -q -- "^build build --target runtime-with-migration-tools" "$S_TRACE" || fail "default build options changed"
! grep -q -- "--add-host" "$S_TRACE" || fail "default build named a mirror"
! grep -q -- " -f .* -f " "$S_TRACE" || fail "default release used an extra compose file"
echo "PASS server/default"

# 2h. A shared host may mount legacy directories outside its root (absolute,
#     without ..); a missing root or a bad --add-host entry is refused first.
root=$(new_root shared-legacy); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}" "GO_LEGACY_GENERATED_IMAGES_DIR=$SERVER/legacy/generated" "GO_LEGACY_INPUT_ASSETS_DIR=$SERVER/legacy/assets"
[ "$RESULT" = 0 ] || { cat "$root/out.log" >&2; fail "shared-host legacy layout refused"; }
grep -q "^preflight db=$root/production/data legacy=$SERVER/legacy/generated\$" "$S_TRACE" || fail "shared legacy layout not exported"
grep -qx "GO_LEGACY_INPUT_ASSETS_DIR=$SERVER/legacy/assets" "$root/production/compose.env" || fail "shared-host compose.env lacks the layout"
for bad in GO_LEGACY_GENERATED_IMAGES_DIR=relative/generated GO_LEGACY_GENERATED_IMAGES_DIR=/srv/legacy/../etc RELEASE_BUILD_ADD_HOSTS=no-address; do
  root=$(new_root "shared-bad-$RANDOM")
  run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}" "$bad"
  [ "$RESULT" = 1 ] && ! grep -q '^build' "$S_TRACE" || fail "shared host accepted: $bad"
done
: >"$S_TRACE"
if env PATH="$SERVER/bin:$PATH" bash "$SCRIPTS/release-server-steps.sh" candidate "$SECOND" >/dev/null 2>&1; then
  fail "server steps ran without a remote root"
fi
! grep -q '^build' "$S_TRACE" || fail "server steps built without a remote root"
echo "PASS server/shared-host-layout"

# 2h-bis. Per-root release lock (#364): a concurrent supported recovery or
#         retention holds the lock; this release must stop before any build or
#         data change. An inherited descriptor from release-lock.py run must be
#         accepted and validated, and a forged one refused.
root=$(new_root lock-busy); echo previous >"$S_STATE"
mkdir -p "$root/production/data"
printf original >"$root/production/data/marker"
mkfifo "$root/hold.fifo"
python3 "$root/app/scripts/release-lock.py" hold --root "$root" <"$root/hold.fifo" >"$root/hold.out" 2>&1 &
holder=$!
exec 9>"$root/hold.fifo"
for _ in $(seq 1 200); do
  python3 "$root/app/scripts/release-lock.py" run --root "$root" -- true >/dev/null 2>&1 || break
  /bin/sleep 0.05
done
run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}"
exec 9>&-
kill "$holder" 2>/dev/null || true
wait "$holder" 2>/dev/null || true
[ "$RESULT" = 1 ] && grep -q 'release lock is busy' "$root/out.log" || {
  cat "$root/out.log" >&2; fail "release did not stop at the held release lock";
}
! grep -q '^build\|^compose\|^tar' "$S_TRACE" || fail "release acted while the lock was held"
[ "$(cat "$root/production/data/marker")" = original ] || fail "held lock changed live bytes"
echo "PASS server/release-lock-busy"

root=$(new_root lock-inherited); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
run_locked_steps "$root" S_CONTAINERS=1 "${SHARED[@]}"
[ "$RESULT" = 0 ] && grep -q "^candidate $SECOND " "$root/releases/manifest.txt" ||
  { cat "$root/out.log" >&2; fail "inherited release lock deadlocked or failed"; }
grep -q '^compose ' "$S_TRACE" || fail "inherited lock skipped the release"
echo "PASS server/release-lock-inherited"

root=$(new_root lock-invalid); echo previous >"$S_STATE"
run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}" RELEASE_LOCK_FD=0 RELEASE_LOCK_ROOT="$root"
[ "$RESULT" = 1 ] && grep -q 'inherited release lock' "$root/out.log" ||
  { cat "$root/out.log" >&2; fail "forged inherited lock was accepted"; }
! grep -q '^build\|^compose\|^tar' "$S_TRACE" || fail "forged inherited lock changed state"
echo "PASS server/release-lock-invalid-inherited"

# 2i. Database-only scope (#364): data.tar only, scope metadata, post-success
#     retention, and rollback that keeps candidate CAS/web-data bytes.
root=$(new_root database); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
printf original >"$root/production/data/marker"
printf original >"$root/production/storage-go/marker"
printf original >"$root/production/web-data/marker"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}" $(layout "$root") \
  RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2
[ "$RESULT" = 0 ] || { cat "$root/out.log" >&2; fail "database-scope update failed"; }
[ -f "$root/backups/release-candidate/data.tar" ] || fail "database backup missing"
[ ! -e "$root/backups/release-candidate/storage-go.tar" ] && [ ! -e "$root/backups/release-candidate/web-data.tar" ] ||
  fail "database scope archived asset or web-data trees"
grep -qx 'database' "$root/backups/release-candidate/scope" || fail "backup scope metadata missing"
grep -qx 'RELEASE_BACKUP_SCOPE=database' "$root/production/compose.env" || fail "compose.env lacks backup scope"
grep -qx 'RELEASE_BACKUP_TARGETS=data' "$root/production/compose.env" || fail "compose.env lacks backup targets"
grep -qx 'capacity dev_project-art-ai-go-1 before-build scope=database' "$S_TRACE" || fail "database capacity scope missing"
grep -qx "backup-retention --root $root --project dev_project --rollback-image art-ai-go-production:previous --keep 2 --apply" "$S_TRACE" ||
  fail "database backup retention arguments"
grep -Eq '^backup-retention-lock [0-9]+ .+/' "$S_TRACE" || fail "database retention did not inherit the release lock"
echo "PASS server/database-backup-scope"

root=$(new_root database-rollback); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
printf original >"$root/production/data/marker"
printf original >"$root/production/storage-go/marker"
printf original >"$root/production/web-data/marker"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=1 S_UNHEALTHY=1 S_CANDIDATE_WRITE=1 "${SHARED[@]}" $(layout "$root") \
  RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2
[ "$RESULT" = 1 ] || fail "database-scope unhealthy release succeeded"
[ "$(cat "$root/production/data/marker")" = original ] && [ ! -e "$root/production/data/candidate-marker" ] ||
  fail "database rollback did not restore only the database"
[ -f "$root/production/storage-go/candidate-marker" ] && [ "$(cat "$root/production/storage-go/marker")" = original ] ||
  fail "database rollback changed CAS bytes"
[ -f "$root/production/web-data/candidate-marker" ] && [ "$(cat "$root/production/web-data/marker")" = original ] ||
  fail "database rollback changed web-data bytes"
[ ! -e "$root/backups/release-candidate/storage-go.tar" ] && [ ! -e "$root/backups/release-candidate/web-data.tar" ] ||
  fail "database rollback created full archives"
grep -qx 'capacity dev_project-art-ai-go-1 restore scope=database' "$S_TRACE" || fail "database restore capacity scope missing"
! grep -q '^backup-retention' "$S_TRACE" || fail "failed database release ran backup retention"
grep -qx 'backup_scope=database' "$root/production/reports/release-failures/candidate-unhealthy-after-up.txt" ||
  fail "failure report lacks backup scope"
grep -qx 'backup_targets=data' "$root/production/reports/release-failures/candidate-unhealthy-after-up.txt" ||
  fail "failure report lacks backup targets"
echo "PASS server/database-rollback-keeps-assets"

for bad in "RELEASE_BACKUP_SCOPE=database" "RELEASE_BACKUP_SCOPE=full RELEASE_BACKUP_KEEP=2" \
  "RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=0" "RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=3" \
  "RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=x" "RELEASE_BACKUP_SCOPE=other RELEASE_BACKUP_KEEP=2"; do
  root=$(new_root "backup-bad-$RANDOM")
  # shellcheck disable=SC2086
  run_steps "$root" S_CONTAINERS=1 "${SHARED[@]}" $bad
  [ "$RESULT" = 1 ] && ! grep -q '^build\|^compose' "$S_TRACE" || fail "backup scope accepted: $bad"
done
echo "PASS server/backup-scope-refused-before-side-effects"

root=$(new_root database-retention-fail); echo previous >"$S_STATE"
mkdir -p "$root/production/data" "$root/production/storage-go" "$root/production/web-data"
# shellcheck disable=SC2046
run_steps "$root" S_CONTAINERS=1 S_BACKUP_RETENTION_FAIL=1 "${SHARED[@]}" $(layout "$root") \
  RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2
[ "$RESULT" = 0 ] && grep -q 'WARNING: release backup retention incomplete' "$root/out.log" &&
  grep -q '^candidate ' "$root/releases/manifest.txt" || fail "backup retention failure changed the release result"
echo "PASS server/backup-retention-warns"

# 2j. Real helper compatibility: the caller writes a single-line scope file;
#     the shipped helper must read it, and legacy full snapshots stay 3 archives.
python3 - "$SCRIPTS/retain-release-backups.py" "$WORK/helper-fixture" "$SECOND" <<'PY'
import importlib.util, json, os, pathlib, sys
from unittest.mock import patch
sys.dont_write_bytecode = True
helper_path, fixture, revision = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3]
root = fixture / "stack"
root.mkdir(parents=True)
for name in ("production/data", "production/storage-go", "production/web-data", "releases", "backups"):
    (root / name).mkdir(parents=True)
tag = revision[:7] + "-20261020T010203Z"
snapshot = root / "backups" / ("release-" + tag)
snapshot.mkdir()
(snapshot / "data.tar").write_bytes(b"data")
(snapshot / "scope").write_text("database\n")
(root / "releases/manifest.txt").write_text(f"{tag} {revision} 2026-10-20T01:02:03Z\n")
mounts = [
    {"Type": "bind", "Source": str(root / "production" / name), "Destination": destination}
    for destination, name in (("/app/data", "data"), ("/app/storage", "storage-go"), ("/app/web-data", "web-data"))
]
row = {
    "Config": {
        "Image": "art-ai-go-production:" + tag,
        "Labels": {
            "com.docker.compose.project": "dev_project",
            "com.docker.compose.service": "art-ai-go",
            "org.opencontainers.image.revision": revision,
        },
    },
    "State": {"Running": True},
    "Mounts": mounts,
}
spec = importlib.util.spec_from_file_location("backup_retention", helper_path)
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)
retention.read_mount_points = lambda: frozenset()
with patch.object(retention, "docker", lambda *args: json.dumps([row])):
    plan = retention.build_plan(str(root), "dev_project", 2, rollback="art-ai-go-production:previous")
assert retention.read_scope(snapshot / "scope") == ("data.tar",), retention.read_scope(snapshot / "scope")
assert plan["current"] == tag and plan["keep"] == 2 and plan["would_delete"] == [], plan
legacy = root / "backups" / ("release-" + revision[:7] + "-20261020T010204Z")
legacy.mkdir()
for name in retention.ARCHIVES:
    (legacy / name).write_bytes(name.encode())
_, expected, complete = retention.inspect_snapshot(
    legacy, os.lstat(root / "backups").st_dev, frozenset())
assert expected == retention.ARCHIVES and len(expected) == 3 and complete, (expected, complete)
print("PASS helper/scope-compatibility")
PY

# ---------------------------------------------------------------------------
# Part 3: capacity bootstrap phase measures no container.
# ---------------------------------------------------------------------------
python3 - "$SCRIPTS/release-capacity.py" "$WORK" <<'PY'
import importlib.util, pathlib, sys
from unittest.mock import patch
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("capacity", sys.argv[1])
c = importlib.util.module_from_spec(spec); spec.loader.exec_module(c)
root = pathlib.Path(sys.argv[2]) / "capacity"
(root / "app").mkdir(parents=True); (root / "docker").mkdir()
calls, needs = [], []
def command(*args):
    calls.append(args)
    if args[:2] == ("docker", "info"):
        return str(root / "docker")
    raise AssertionError("bootstrap must not inspect a container: %r" % (args,))
class Recording:
    def __init__(self): self.devices = {1: {"block": 4096}}; self.need = 0
    def filesystem(self, path): return 1
    def require(self, path, byte_count=0, inode_count=0): self.need += byte_count
    def verify(self): needs.append(self.need)
with patch.object(c, "command", command), patch.object(c, "Capacity", Recording), \
        patch.object(c, "tree", lambda path, apparent=True: (10, 1, 10)):
    c.check(root, "-", "bootstrap", root / "backups/release-x")
assert [a[:2] for a in calls] == [("docker", "info")], calls
assert needs and needs[0] >= c.BUILD_HEADROOM, needs
try:
    c.check(root, "-", "bootstrap", root / "elsewhere/release-x")
except c.CapacityError:
    pass
else:
    raise AssertionError("bootstrap archive path outside backups accepted")
print("PASS capacity/bootstrap-phase")
PY

echo 'release stack contract passed'
