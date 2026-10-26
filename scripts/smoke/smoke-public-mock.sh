#!/usr/bin/env bash
# Public Mock smoke for a self-deployed Plinth stack (#260; manual local run after a self-deploy).
# Public CI does not build images (#257), so this is not a CI job.
#
# Zero keys, zero cost: it refuses to run unless the stack's .env says PROVIDER_MODE=mock and
# AGENT_MODE=mock and every *_API_KEY is empty, and it only talks to a loopback address.
# It needs bash, curl and python3, nothing outside this repository.
#
#   SMOKE_ENV_FILE=.env [BASE=http://127.0.0.1:13333] [EXPECT_SHA=<full commit>] \
#   [SMOKE_STATE=<file>] bash scripts/smoke/smoke-public-mock.sh [run|verify]
#
# The admin account is ADMIN_USERNAME (default admin) / ADMIN_PASSWORD from SMOKE_ENV_FILE.
#
# run (default), checklist IDs from docs/acceptance-1.0.md:
#   DEP-04 /healthz and /readyz 200             DEP-05 /api/version equals EXPECT_SHA (when set)
#   SEC-06 sign-in page has a CSP and no off-site script   PRV-04 no secret in HTML, schema, offerings
#   AUTH-01 sign-in sets an HttpOnly SameSite=Lax cookie; a wrong password gets 401
#   SEC-03 cross-site state change 403          LOG-01 4xx body request_id equals X-Request-ID
#   DEP-07 / IMG-01 / M3D-01 one Mock image and one Mock 3D task through the public
#          prepare -> grant (approve the pending 3D card) -> submit path, every asset downloaded
#          and checked against its declared size and SHA-256 (DLV-01: twice, same bytes)
#   DLV-02 ZIP archive starts with README.txt, no http(s):// in it
#   DLV-03 anonymous asset / archive requests 401   DLV-04 model file headers
#   AUTH-05 sign-out revokes the cookie
# With SMOKE_STATE set, run records the task and asset digests there, and
# verify (after `docker compose restart` or `down` + `up`) signs in again and checks that the
# same tasks are SUCCEEDED and their assets still download with the same SHA-256 (DEP-08).
set -u

MODE="${1:-run}"
case "$MODE" in run|verify) ;; *) echo "usage: $0 [run|verify]" >&2; exit 2 ;; esac
BASE="${BASE:-http://127.0.0.1:13333}"
BASE="${BASE%/}"
case "$BASE" in
  http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*) ;;
  *) echo "ERROR: BASE must be a loopback address (this smoke creates tasks), got $BASE" >&2; exit 2 ;;
esac
ENV_FILE="${SMOKE_ENV_FILE:-}"
if [ -z "$ENV_FILE" ] || [ ! -f "$ENV_FILE" ]; then
  echo "ERROR: set SMOKE_ENV_FILE to the stack's .env (it proves Mock mode and holds the admin password)" >&2
  exit 2
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
JAR="$WORK/cookies.txt"
PASS=0; FAIL=0
ok()  { echo "PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
finish() { echo "=== SMOKE-PUBLIC-MOCK $MODE PASS=$PASS FAIL=$FAIL ==="; [ "$FAIL" = 0 ]; exit $?; }
abort() { bad "$1"; echo "=== ABORT ==="; finish; }
# req <out> <curl args...>: body to <out>, prints the HTTP status.
req() { local out="$1"; shift; curl -sS -o "$out" -w '%{http_code}' "$@" 2>/dev/null || echo 000; }
# py <script> [args...]: small JSON helpers; never prints secrets.
py() { python3 -c "$@"; }

# The .env is read as data (KEY=VALUE lines), never sourced.
ENV_JSON="$WORK/env.json"
py '
import json, re, sys
values = {}
for line in open(sys.argv[1], encoding="utf-8"):
    m = re.match(r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$", line.rstrip("\n"))
    if m:
        v = m.group(2).strip()
        if len(v) >= 2 and v[0] == v[-1] and v[0] in "\"'"'"'":
            v = v[1:-1]
        values[m.group(1)] = v
json.dump(values, open(sys.argv[2], "w"))
' "$ENV_FILE" "$ENV_JSON" || { echo "ERROR: cannot read $ENV_FILE" >&2; exit 2; }
envv() { py 'import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2], ""))' "$ENV_JSON" "$1"; }

if [ "$(envv PROVIDER_MODE)" != mock ] || [ "$(envv AGENT_MODE)" != mock ]; then
  echo "ERROR: $ENV_FILE must set PROVIDER_MODE=mock and AGENT_MODE=mock; this smoke never runs against live Providers" >&2
  exit 2
fi
KEYS=$(py 'import json,sys; print(" ".join(k for k, v in json.load(open(sys.argv[1])).items() if k.endswith("_API_KEY") and v))' "$ENV_JSON")
if [ -n "$KEYS" ]; then
  echo "ERROR: $ENV_FILE sets $KEYS; the public smoke runs with zero keys" >&2
  exit 2
fi
ADMIN_USER=$(envv ADMIN_USERNAME); ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PW=$(envv ADMIN_PASSWORD)
[ -n "$ADMIN_PW" ] || { echo "ERROR: ADMIN_PASSWORD is empty in $ENV_FILE" >&2; exit 2; }

ORIGIN=(-H "Origin: $BASE")
JSONH=(-H 'Content-Type: application/json')

login() {
  local body="$WORK/login.json" headers="$WORK/login.h"
  rm -f "$JAR"
  py 'import json,sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$ADMIN_USER" "$ADMIN_PW" > "$WORK/login.req"
  local code
  code=$(req "$body" -D "$headers" -X POST "$BASE/api/auth/login" "${JSONH[@]}" "${ORIGIN[@]}" --data-binary @"$WORK/login.req" -c "$JAR")
  rm -f "$WORK/login.req"
  echo "$code"
}

health() {
  for path in /healthz /readyz; do
    [ "$(req "$WORK/h.json" "$BASE$path")" = 200 ] && ok "DEP-04 $path 200" || bad "DEP-04 $path"
  done
}

# download <url> <out>: authenticated GET, prints the HTTP status.
download() { req "$2" -b "$JAR" "$BASE$1"; }

# check_assets <task.json> <tag>: every asset downloads twice with its declared size and SHA-256.
check_assets() {
  local task="$1" tag="$2" n=0 id url media size sha role
  while IFS=$'\t' read -r id url media size sha role; do
    [ -n "$id" ] || continue
    n=$((n+1))
    local f1="$WORK/$id.1" f2="$WORK/$id.2"
    if [ "$(download "$url" "$f1")" != 200 ] || [ "$(download "$url" "$f2")" != 200 ]; then
      bad "$tag asset $id ($media) download"; continue
    fi
    if py '
import hashlib, sys
a, b, size, sha, media = open(sys.argv[1], "rb").read(), open(sys.argv[2], "rb").read(), int(sys.argv[3]), sys.argv[4], sys.argv[5]
magic = {"image/png": b"\x89PNG\r\n\x1a\n", "model/gltf-binary": b"glTF"}.get(media)
ok = a == b and len(a) == size and hashlib.sha256(a).hexdigest() == sha and (magic is None or a.startswith(magic))
sys.exit(0 if ok else 1)
' "$f1" "$f2" "$size" "$sha" "$media"; then
      ok "$tag $role ($media): size, SHA-256 and bytes match on two downloads"
    else
      bad "$tag asset $id ($media): bytes differ from the declared size/SHA-256"
    fi
  done < <(py '
import json, sys
for a in json.load(open(sys.argv[1])).get("assets") or []:
    print("\t".join(str(a.get(k, "")) for k in ("id", "url", "media_type", "size", "sha256")) + "\t" + "/".join(str(a[k]) for k in ("role", "variant") if a.get(k)))
' "$task")
  [ "$n" -gt 0 ] || bad "$tag task has no assets"
}

wait_task() { # wait_task <taskID> <out> <seconds>
  local deadline=$((SECONDS+$3)) state
  while [ $SECONDS -lt $deadline ]; do
    if [ "$(req "$2" -b "$JAR" "$BASE/api/v1/tasks/$1")" = 200 ]; then
      state=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("state",""))' "$2")
      case "$state" in
        SUCCEEDED) return 0 ;;
        FAILED|CANCELED) echo "  task $1 ended $state: $(head -c 400 "$2")"; return 1 ;;
      esac
    fi
    sleep 2
  done
  echo "  task $1 did not finish in $3s"; return 1
}

# generate <capability> <tag> <session> <prompt>: one Mock task through prepare -> grant -> submit.
generate() {
  local cap="$1" tag="$2" sid="$3" prompt="$4" offering pid rid gstatus code tid
  [ "$(req "$WORK/off.json" -b "$JAR" "$BASE/api/v1/offerings?capability=$cap")" = 200 ] || { bad "$tag offerings"; return 1; }
  offering=$(py '
import json, sys
offs = [o for o in json.load(open(sys.argv[1])).get("offerings") or [] if o.get("available")]
offs.sort(key=lambda o: not o.get("recommended"))
print(offs[0]["id"] if offs else "")
' "$WORK/off.json")
  [ -n "$offering" ] || { bad "$tag: no available $cap offering"; return 1; }
  py 'import json,sys; print(json.dumps({"session_id": sys.argv[1], "capability": sys.argv[2], "offering_id": sys.argv[3], "input": {"prompt": sys.argv[4]}}))' \
    "$sid" "$cap" "$offering" "$prompt" > "$WORK/prep.req"
  code=$(req "$WORK/prep.json" -b "$JAR" -X POST "$BASE/api/v1/tasks/prepare" "${JSONH[@]}" "${ORIGIN[@]}" --data-binary @"$WORK/prep.req")
  pid=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("prepared_input_id",""))' "$WORK/prep.json" 2>/dev/null)
  case "$code" in 200|201) ;; *) pid= ;; esac
  [ -n "$pid" ] || { bad "$tag prepare $offering ($code): $(head -c 300 "$WORK/prep.json")"; return 1; }
  code=$(req "$WORK/grant.json" -b "$JAR" -X POST "$BASE/api/v1/tasks/grants" "${JSONH[@]}" "${ORIGIN[@]}" \
    -H "Idempotency-Key: smoke-$pid" -d "{\"prepared_input_id\":\"$pid\"}")
  rid=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("request_id",""))' "$WORK/grant.json" 2>/dev/null)
  gstatus=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("status",""))' "$WORK/grant.json" 2>/dev/null)
  case "$code" in 200|201) ;; *) bad "$tag grant ($code)"; return 1 ;; esac
  if [ "$gstatus" = pending ]; then
    # A pending approval card (manual mode: text-to-3d needs a person); the smoke approves it.
    code=$(req "$WORK/decide.json" -b "$JAR" -X PUT "$BASE/api/v1/tasks/grants/$rid" "${JSONH[@]}" "${ORIGIN[@]}" -d '{"decision":"approve"}')
    [ "$code" = 200 ] && ok "$tag approval card approved" || { bad "$tag approve ($code)"; return 1; }
  fi
  code=$(req "$WORK/submit.json" -b "$JAR" -X POST "$BASE/api/v1/tasks" "${JSONH[@]}" "${ORIGIN[@]}" \
    -H "Idempotency-Key: smoke-$pid" -d "{\"prepared_input_id\":\"$pid\",\"authorization_request_id\":\"$rid\"}")
  tid=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("task_id",""))' "$WORK/submit.json" 2>/dev/null)
  case "$code" in 200|201|202) ;; *) bad "$tag submit ($code): $(head -c 300 "$WORK/submit.json")"; return 1 ;; esac
  [ -n "$tid" ] || { bad "$tag submit returned no task id"; return 1; }
  if wait_task "$tid" "$WORK/$tag.task.json" 240; then
    ok "$tag task SUCCEEDED ($offering)"
  else
    bad "$tag task $tid did not succeed"; return 1
  fi
  echo "$tid" > "$WORK/$tag.id"
}

if [ "$MODE" = verify ]; then
  STATE="${SMOKE_STATE:-}"
  [ -n "$STATE" ] && [ -f "$STATE" ] || { echo "ERROR: verify needs SMOKE_STATE from an earlier run" >&2; exit 2; }
  health
  [ "$(login)" = 200 ] && ok "sign in again" || abort "sign in after restart"
  while IFS=$'\t' read -r tid aid sha; do
    [ -n "$tid" ] || continue
    if [ "$(req "$WORK/t.json" -b "$JAR" "$BASE/api/v1/tasks/$tid")" != 200 ] || \
       [ "$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("state",""))' "$WORK/t.json")" != SUCCEEDED ]; then
      bad "DEP-08 task $tid missing or not SUCCEEDED"; continue
    fi
    if [ "$(download "/api/v1/tasks/$tid/assets/$aid" "$WORK/a.bin")" = 200 ] && \
       [ "$(py 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$WORK/a.bin")" = "$sha" ]; then
      ok "DEP-08 task $tid asset $aid kept its bytes"
    else
      bad "DEP-08 task $tid asset $aid changed or is missing"
    fi
  done < "$STATE"
  finish
fi

# ---- run ------------------------------------------------------------------------------------
health

if [ "$(req "$WORK/v.json" "$BASE/api/version")" = 200 ]; then
  VERSION=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("version",""))' "$WORK/v.json")
  if [ -n "${EXPECT_SHA:-}" ]; then
    [ "$VERSION" = "$EXPECT_SHA" ] && ok "DEP-05 /api/version = $VERSION" || bad "DEP-05 /api/version $VERSION != $EXPECT_SHA"
  else
    [ -n "$VERSION" ] && ok "DEP-05 /api/version = $VERSION (EXPECT_SHA not set)" || bad "DEP-05 empty version"
  fi
else
  bad "DEP-05 /api/version"
fi

# Sign-in page: CSP header, no off-site script.
code=$(req "$WORK/login.html" -D "$WORK/login.html.h" "$BASE/login")
if [ "$code" = 200 ] && grep -qi '^content-security-policy:' "$WORK/login.html.h" && \
   ! grep -Eiq '<script[^>]+src="(https?:)?//' "$WORK/login.html"; then
  ok "SEC-06 sign-in page has a CSP and only same-origin scripts"
else
  bad "SEC-06 sign-in page ($code)"
fi

# Wrong password: 401, and LOG-01 error body carries the response's request id.
py 'import json; print(json.dumps({"username": "admin", "password": "wrong-password-for-smoke"}))' > "$WORK/bad.req"
code=$(req "$WORK/bad.json" -D "$WORK/bad.h" -X POST "$BASE/api/auth/login" "${JSONH[@]}" "${ORIGIN[@]}" --data-binary @"$WORK/bad.req")
[ "$code" = 401 ] && ok "AUTH-02 wrong password 401" || bad "AUTH-02 wrong password got $code"
if py '
import json, sys
hdr = {l.split(":", 1)[0].strip().lower(): l.split(":", 1)[1].strip() for l in open(sys.argv[1]) if ":" in l}
rid = hdr.get("x-request-id", "")
sys.exit(0 if rid and json.load(open(sys.argv[2])).get("request_id") == rid else 1)
' "$WORK/bad.h" "$WORK/bad.json" 2>/dev/null; then
  ok "LOG-01 error body request_id equals X-Request-ID"
else
  bad "LOG-01 error body request_id"
fi

code=$(login)
if [ "$code" = 200 ] && grep -i '^set-cookie:' "$WORK/login.h" | grep -qi 'httponly' && \
   grep -i '^set-cookie:' "$WORK/login.h" | grep -qi 'samesite=lax'; then
  ok "AUTH-01 admin sign-in, HttpOnly SameSite=Lax cookie"
else
  abort "AUTH-01 admin sign-in ($code)"
fi

# PRV-04: the public HTML and projections never carry the session secret or admin password.
req "$WORK/schema.json" -b "$JAR" "$BASE/api/v1/schema" >/dev/null
req "$WORK/offerings.json" -b "$JAR" "$BASE/api/v1/offerings" >/dev/null
if py '
import json, sys
env = json.load(open(sys.argv[1]))
secrets = [v for k, v in env.items() if v and len(v) >= 6 and k in ("SESSION_SECRET", "ADMIN_PASSWORD", "ASSET_SIGNING_KEY")]
blob = b"".join(open(p, "rb").read() for p in sys.argv[2:])
sys.exit(0 if secrets and not any(s.encode() in blob for s in secrets) else 1)
' "$ENV_JSON" "$WORK/login.html" "$WORK/schema.json" "$WORK/offerings.json"; then
  ok "PRV-04 no secret in the sign-in HTML, schema or offerings"
else
  bad "PRV-04 a secret appears in a public response"
fi

code=$(req "$WORK/x.json" -X POST "$BASE/api/chat/sessions" "${JSONH[@]}" -H 'Origin: http://evil.example' \
  -H 'Sec-Fetch-Site: cross-site' -b "$JAR" -d '{"title":"smoke-csrf"}')
[ "$code" = 403 ] && ok "SEC-03 cross-site state change 403" || bad "SEC-03 cross-site got $code"

code=$(req "$WORK/session.json" -X POST "$BASE/api/chat/sessions" "${JSONH[@]}" "${ORIGIN[@]}" -b "$JAR" -d '{"title":"public mock smoke"}')
SID=$(py 'import json,sys; print(json.load(open(sys.argv[1])).get("id",""))' "$WORK/session.json" 2>/dev/null)
case "$code" in 200|201) [ -n "$SID" ] && ok "create session" || abort "create session: no id" ;; *) abort "create session ($code)" ;; esac
code=$(req "$WORK/approval.json" -X PUT "$BASE/api/v1/chat/sessions/$SID/approval" "${JSONH[@]}" "${ORIGIN[@]}" -b "$JAR" -d '{"approvalMode":"manual"}')
[ "$code" = 200 ] && ok "session approval mode manual" || bad "session approval mode ($code)"

: > "$WORK/state.tsv"
if generate text-to-image image "$SID" "A small red lantern on a wooden table"; then
  check_assets "$WORK/image.task.json" image
fi
if generate text-to-3d model "$SID" "A simple low-poly wooden stool, 3D model"; then
  check_assets "$WORK/model.task.json" model
  TID=$(cat "$WORK/model.id")
  # DLV-02: the ZIP starts with README.txt and the README holds no URL.
  if [ "$(download "/api/v1/tasks/$TID/archive" "$WORK/model.zip")" = 200 ] && py '
import re, sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = z.namelist()
readme = z.read(names[0]).decode("utf-8", "replace") if names else ""
sys.exit(0 if names and names[0] == "README.txt" and not re.search(r"https?://", readme) and len(names) > 1 else 1)
' "$WORK/model.zip"; then
    ok "DLV-02 archive starts with README.txt, no URL in it"
  else
    bad "DLV-02 archive"
  fi
  GLB=$(py '
import json, sys
for a in json.load(open(sys.argv[1])).get("assets") or []:
    if a.get("media_type") == "model/gltf-binary":
        print(a["url"]); break
' "$WORK/model.task.json")
  if [ -n "$GLB" ]; then
    # DLV-04: model files download as attachments that the browser never renders or sniffs.
    curl -sS -D "$WORK/glb.h" -o /dev/null -b "$JAR" "$BASE$GLB"
    if grep -qi '^content-disposition: *attachment' "$WORK/glb.h" && grep -qi '^x-content-type-options: *nosniff' "$WORK/glb.h" && \
       grep -qi '^content-security-policy: *sandbox' "$WORK/glb.h"; then
      ok "DLV-04 GLB: attachment, nosniff, CSP sandbox"
    else
      bad "DLV-04 GLB headers"
    fi
    # DLV-03: no cookie, no bytes.
    a=$(req "$WORK/anon" "$BASE$GLB"); z=$(req "$WORK/anon" "$BASE/api/v1/tasks/$TID/archive")
    [ "$a" = 401 ] && [ "$z" = 401 ] && ok "DLV-03 anonymous asset and archive 401" || bad "DLV-03 anonymous asset $a, archive $z"
  else
    bad "model task has no GLB"
  fi
fi

for tag in image model; do
  [ -f "$WORK/$tag.id" ] || continue
  py '
import json, sys
t = json.load(open(sys.argv[1]))
for a in t.get("assets") or []:
    print("\t".join((t["id"], a["id"], a["sha256"])))
' "$WORK/$tag.task.json" >> "$WORK/state.tsv"
done
[ -n "${SMOKE_STATE:-}" ] && cp "$WORK/state.tsv" "$SMOKE_STATE"

# AUTH-05: sign out, then the old cookie is refused.
code=$(req "$WORK/out.json" -X DELETE "$BASE/api/auth/session" "${ORIGIN[@]}" -b "$JAR")
after=$(req "$WORK/t.json" -b "$JAR" "$BASE/api/v1/tasks?limit=1")
[ "$code" = 200 ] && [ "$after" = 401 ] && ok "AUTH-05 sign-out revokes the cookie" || bad "AUTH-05 sign-out $code, then tasks $after"

finish
