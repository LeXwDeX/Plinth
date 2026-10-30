#!/usr/bin/env python3
"""Free API acceptance checks against a named environment (docs/acceptance-1.0.md AUTH/DLV/SEC/LOG/ADM/PRV).

Covers AUTH-01, AUTH-02 (spot), AUTH-03 (anonymous half), AUTH-05, AUTH-06 (spot), DLV-03 (anonymous half), DLV-04 (spot),
SEC-03, SEC-05, ADM-02, PRV-01 (current keyed state), LOG-01, LOG-02, LOG-03 (admin/login traffic). Items that need a
second account (AUTH-03/09 cross-user halves) are recorded as PENDING; cross_user.py covers them on a local stack.

Read-mostly and free: creates no accounts and no paid Tasks. Side effects on the target: a few admin logins and logouts,
two rejected login attempts, one 1 KiB PNG upload (a control for the size test) and rejected oversize/unsafe uploads.
LOG-02/03 read the server log over the read-only ssh path defined in the local environments file; without it they are BLOCKED.

Run (credentials injected, never written to files):
  python3 scripts/release-env.py smoke <env> -- python3 scripts/acceptance/api_free.py

Environment (injected by release-env.py smoke): BASE, ADMIN_USERNAME, ADMIN_PASSWORD, EXPECT_SHA.
Also injected: RELEASE_ENV (the environment name; LOG-02/03 read that same deployment's log and are BLOCKED when it is
unknown or its base_url differs from BASE). Optional: OVERSIZE_MB (size of the oversize upload,
default 64; must exceed the server upload limit).
Exit status 1 when any check FAILs; PENDING/BLOCKED do not fail the run.
"""
import argparse, json, os, re, subprocess, sys, urllib.request, urllib.error, http.cookiejar, uuid

REPO = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter).parse_args()
for _name in ("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD"):
    if not os.environ.get(_name):
        sys.exit(f"missing environment {_name}; run through: python3 scripts/release-env.py smoke <env> -- python3 scripts/acceptance/api_free.py")

BASE = os.environ["BASE"].rstrip("/")
U, P = os.environ["ADMIN_USERNAME"], os.environ["ADMIN_PASSWORD"]
ENV_NAME = os.environ.get("RELEASE_ENV", "")
res = []

def rec(i, status, ev):
    res.append((i, status)); print(f"{status}: {i} | {ev}")

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None

def call(method, path, body=None, headers=None, jar=None, raw=None, ctype="application/json"):
    h = dict(headers or {})
    data = raw
    if body is not None:
        data = json.dumps(body).encode(); h["Content-Type"] = ctype
    elif raw is not None and ctype: h["Content-Type"] = ctype
    ops = [NoRedirect()] + ([urllib.request.HTTPCookieProcessor(jar)] if jar is not None else [])
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
    try:
        r = urllib.request.build_opener(*ops).open(req, timeout=60)
    except urllib.error.HTTPError as e: r = e
    except Exception as e: return 0, {}, b"", str(e)
    b = r.read()
    return r.status if hasattr(r, "status") else r.code, dict(r.headers), b, ""

def js(b):
    try: return json.loads(b)
    except Exception: return {}

def login(extra_origin=True):
    jar = http.cookiejar.CookieJar()
    st, h, b, _ = call("POST", "/api/auth/login", {"username": U, "password": P}, {"Origin": BASE}, jar)
    return st, h, b, jar

def sc_headers(h):
    # urllib folds repeated headers; read via a fresh raw call for Set-Cookie
    return h.get("Set-Cookie", "")

# ---- AUTH-01
req = urllib.request.Request(BASE + "/api/auth/login", data=json.dumps({"username": U, "password": P}).encode(),
                             headers={"Content-Type": "application/json", "Origin": BASE}, method="POST")
r = urllib.request.build_opener(NoRedirect()).open(req, timeout=30)
cookies = r.headers.get_all("Set-Cookie") or []
flags = " ".join(cookies).lower()
cookie_value = None
for c in cookies:
    cookie_value = c.split(";", 1)[0].split("=", 1)[1]
ok = r.status == 200 and "httponly" in flags and "samesite=lax" in flags
rec("AUTH-01", "PASS" if ok else "FAIL", f"login {r.status}, Set-Cookie HttpOnly={'httponly' in flags} SameSite=Lax={'samesite=lax' in flags} Secure={'secure' in flags}, X-Request-ID={r.headers.get('X-Request-ID')}")
st, h, b, jar = login()
session_rid = h.get("X-Request-Id")

# ---- AUTH-02 spot (generic message), single failure each (do not trip throttle)
st1, h1, b1, _ = call("POST", "/api/auth/login", {"username": U, "password": "wrong-" + uuid.uuid4().hex}, {"Origin": BASE})
st2, h2, b2, _ = call("POST", "/api/auth/login", {"username": "nouser-" + uuid.uuid4().hex[:8], "password": "x-wrong-pass"}, {"Origin": BASE})
same = js(b1).get("error") == js(b2).get("error") and js(b1).get("error")
rec("AUTH-02(spot; throttle covered by unit test)", "PASS" if (st1 == st2 == 401 and same) else "FAIL", f"wrong pw {st1}, unknown user {st2}, identical message={bool(same)}")

# ---- LOG-01
rid_hdr, rid_body = h1.get("X-Request-Id"), js(b1).get("request_id")
rec("LOG-01", "PASS" if rid_hdr and rid_hdr == rid_body else "FAIL", f"4xx status={st1} X-Request-ID={rid_hdr} body.request_id={rid_body}")
st, h, b, _ = call("GET", "/api/v1/no-such-route-b2", jar=jar)
rec("LOG-01(404 + header on 2xx)", "PASS" if h.get("X-Request-Id") else "FAIL", f"404 route status={st} X-Request-ID={h.get('X-Request-Id')}")
st, h, b, _ = call("GET", "/api/auth/session", jar=jar)
rec("AUTH-session(admin)", "PASS" if st == 200 and h.get("X-Request-Id") else "FAIL", f"status={st} X-Request-ID={h.get('X-Request-Id')}")
admin_rid = h.get("X-Request-Id")

# ---- AUTH-06 spot: /api/auth/mock must not work on dev-test
st, h, b, _ = call("POST", "/api/auth/mock", {}, {"Origin": BASE})
rec("AUTH-06(spot)", "PASS" if st in (401, 403, 404) else "FAIL", f"POST /api/auth/mock -> {st} X-Request-ID={h.get('X-Request-Id')}")

# ---- AUTH-03 / AUTH-09 cross-user: no second account allowed on dev-test
rec("AUTH-03", "PENDING", "needs a second (non-admin) dev-test account from maintainer; admin-route 403 for non-admin covered by internal/transport/httpapi administration_*_test.go")
rec("AUTH-09", "PENDING", "needs a second dev-test account from maintainer; cross-user denial covered by httpapi harness tests (task/asset owner scope)")
st, h, b, _ = call("GET", "/api/admin/users", jar=None)
rec("AUTH-03(anon half)", "PASS" if st in (401, 403) else "FAIL", f"anonymous GET /api/admin/users -> {st} X-Request-ID={h.get('X-Request-Id')}")

# ---- DLV-03 anonymous half
st, h, b, _ = call("GET", "/api/v1/tasks?limit=50", jar=jar)
tasks = js(b).get("tasks") or js(b).get("items") or []
target = None
for t in tasks:
    if t.get("state") == "SUCCEEDED":
        stt, _, bb, _ = call("GET", f"/api/v1/tasks/{t.get('task_id') or t.get('id')}", jar=jar)
        d = js(bb)
        if d.get("assets"): target = d; break
if not target:
    rec("DLV-03", "BLOCKED", f"no SUCCEEDED task with assets visible to admin (list status={st}, n={len(tasks)})")
else:
    tid = target.get("task_id") or target.get("id"); a = target["assets"][0]
    au = a.get("url") or f"/api/v1/tasks/{tid}/assets/{a['id']}"
    sa, ha, _, _ = call("GET", au, jar=None)
    sz, hz, _, _ = call("GET", f"/api/v1/tasks/{tid}/archive", jar=None)
    sw, hw, _, _ = call("GET", au, jar=jar)
    rec("DLV-03(anon half)", "PASS" if sa == 401 and sz == 401 and sw == 200 else "FAIL",
        f"anon asset={sa} (rid {ha.get('X-Request-Id')}), anon archive={sz}, owner asset={sw}; cross-user half PENDING (second account)")
    cc = ha.get("Cache-Control", ""); 
    rec("DLV-04(spot)", "PASS" if hw.get("X-Content-Type-Options") == "nosniff" else "FAIL", f"owner asset headers nosniff={hw.get('X-Content-Type-Options')} CD={hw.get('Content-Disposition')} CT={hw.get('Content-Type')}")

# ---- SEC-03 cross-site POST
st, h, b, _ = call("POST", "/api/auth/login", {"username": U, "password": "x"}, {"Origin": "https://evil.example"})
st_auth, h2, _, _ = call("POST", "/api/v1/tasks/prepare", {}, {"Origin": "https://evil.example"}, jar)
rec("SEC-03", "PASS" if st == 403 and st_auth == 403 else "FAIL", f"login evil Origin -> {st} (rid {h.get('X-Request-Id')}); authenticated prepare evil Origin -> {st_auth} (rid {h2.get('X-Request-Id')})")

# ---- SEC-05 upload
def multipart(filename, ctype, payload):
    bd = "----b2" + uuid.uuid4().hex
    body = (f"--{bd}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\nContent-Type: {ctype}\r\n\r\n").encode() + payload + f"\r\n--{bd}--\r\n".encode()
    return body, f"multipart/form-data; boundary={bd}"
body, ct = multipart("x.html", "text/html", b"<script>alert(1)</script>")
st_h, hh, bh, _ = call("POST", "/api/v1/assets", raw=body, ctype=ct, headers={"Origin": BASE}, jar=jar)
big = b"\x89PNG\r\n\x1a\n" + b"\0" * (int(os.environ.get("OVERSIZE_MB", "64")) * 1024 * 1024)
body, ct = multipart("small.png", "image/png", big[:1024])
st_c, _, _, _ = call("POST", "/api/v1/assets", raw=body, ctype=ct, headers={"Origin": BASE}, jar=jar)
body, ct = multipart("big.png", "image/png", big)
st_b, hb, bb_, err = call("POST", "/api/v1/assets", raw=body, ctype=ct, headers={"Origin": BASE}, jar=jar)
rec("SEC-05(reject unsafe type + oversize)", "PASS" if st_h == 400 and st_b in (400, 413) and st_c in (200, 201) else "FAIL",
    f"same-shape 1 KiB control -> {st_c}; text/html upload -> {st_h} (rid {hh.get('X-Request-Id')}); {len(big)>>20} MiB png -> {st_b} {err} (rid {hb.get('X-Request-Id')}); body={bb_[:120]!r}")
# spoofed: HTML bytes labelled image/png
body, ct = multipart("spoof.png", "image/png", b"<html><script>alert(1)</script></html>")
st_s, hs, bs, _ = call("POST", "/api/v1/assets", raw=body, ctype=ct, headers={"Origin": BASE}, jar=jar)
if st_s in (400, 415, 422):
    rec("SEC-05(spoofed type)", "PASS", f"HTML bytes labelled image/png rejected {st_s} (rid {hs.get('X-Request-Id')})")
elif st_s in (200, 201):
    aid = js(bs).get("id"); sg, hg, _, _ = call("GET", f"/api/v1/assets/{aid}", jar=jar)
    safe = hg.get("X-Content-Type-Options") == "nosniff" and ("attachment" in hg.get("Content-Disposition", "") or "sandbox" in hg.get("Content-Security-Policy", "")) and "text/html" not in hg.get("Content-Type", "")
    rec("SEC-05(spoofed type)", "PASS" if safe else "FAIL", f"accepted {st_s}; delivery CT={hg.get('Content-Type')} CD={hg.get('Content-Disposition')} CSP={hg.get('Content-Security-Policy')} nosniff={hg.get('X-Content-Type-Options')}")
else:
    rec("SEC-05(spoofed type)", "FAIL", f"unexpected {st_s}")

# ---- ADM-02
audits = []
for q in ["?page=1&pageSize=5", "?action=login&from=not-a-date&to=also-bad", "?page=0&pageSize=999", "?page=2tail&pageSize=-7", "?action=login"]:
    s, h, b, _ = call("GET", "/api/admin/audit-logs" + q, jar=jar)
    audits.append((q, s, h.get("X-Request-Id"), len(js(b).get("data", js(b).get("logs", [])) or []) if s == 200 else None))
ok = all(a[1] < 500 and a[1] != 0 for a in audits) and audits[0][1] == 200
rec("ADM-02", "PASS" if ok else "FAIL", "; ".join(f"{q}->{s} n={n}" for q, s, _, n in audits) + f"; rid {audits[1][2]}")

# ---- PRV-01 (current keyed state; single-key isolation is clean-machine)
s, h, b, _ = call("GET", "/api/v1/offerings", jar=jar)
offs = js(b).get("offerings") or []
by = {}
for o in offs:
    pv = o.get("provider") or o.get("provider_id") or (o.get("id", "").split(":")[0])
    d = by.setdefault(pv, [0, 0]); d[0] += 1; d[1] += 1 if o.get("available") else 0
rec("PRV-01", "PASS" if s == 200 and offs else "FAIL", f"offerings {s}, per provider total/available: " + ", ".join(f"{k}={v[0]}/{v[1]}" for k, v in sorted(by.items())) + "; unavailable ones carry reason: " + str(all(o.get("available") or o.get("unavailable_reason") or o.get("reason") for o in offs)))

# ---- LOG-02/03 via read-only ssh
try:
    if not ENV_NAME: raise LookupError("RELEASE_ENV not injected")
    env = json.loads(subprocess.check_output([sys.executable, f"{REPO}/scripts/release-env.py", "show", ENV_NAME]))
    if env.get("base_url", "").rstrip("/") != BASE: raise LookupError("environment base_url differs from BASE")
    ssh, root = env["ssh"], env["root"]
    logtxt = subprocess.run(["ssh", "-o", "BatchMode=yes", ssh, f"tail -n 40000 {root}/production/logs/art-ai.log"], capture_output=True, timeout=120).stdout.decode("utf-8", "replace")
except Exception as e:
    logtxt = ""; print("  log read failed:", type(e).__name__)
if not logtxt:
    rec("LOG-02", "BLOCKED", "cannot read the log of the environment under test (needs RELEASE_ENV matching BASE, and ssh)"); rec("LOG-03", "BLOCKED", "same")
else:
    line = next((l for l in logtxt.splitlines() if rid_hdr and rid_hdr in l), "")
    d = js(line.encode()) if line else {}
    rec("LOG-02", "PASS" if line else "FAIL", f"request id {rid_hdr} " + (f"found: keys={sorted(d.keys())[:14]} level={d.get('level')} status={d.get('status')}" if line else "not found in last 40000 log lines"))
    leaks = []
    if P and P in logtxt: leaks.append("admin password")
    if cookie_value and cookie_value in logtxt: leaks.append("session cookie value")
    for name, pat in [("Authorization/Bearer", r"(?i)bearer\s+[A-Za-z0-9._\-]{12,}"), ("provider key shape", r"\bsk-[A-Za-z0-9]{16,}"), ("signed query", r"(?i)[?&](signature|x-amz-signature|token|sig)=[^\"&\s]{8,}"), ("Set-Cookie value", r"(?i)set-cookie")]:
        m = re.search(pat, logtxt)
        if m: leaks.append(name)
    rec("LOG-03(M, admin/login traffic; real-generation half is +L)", "PASS" if not leaks else "FAIL", f"scanned {len(logtxt.splitlines())} lines; leaks={leaks or 'none'}")

# ---- AUTH-05 logout last (revokes only this session)
ch = "; ".join(f"{c.name}={c.value}" for c in jar)
pre, _, _, _ = call("GET", "/api/auth/session", headers={"Cookie": ch})
st, h, b, _ = call("DELETE", "/api/auth/session", headers={"Cookie": ch, "Origin": BASE})
a, ha, ba, _ = call("GET", "/api/auth/session", headers={"Cookie": ch})
t, ht, _, _ = call("GET", "/api/v1/tasks", headers={"Cookie": ch})
rec("AUTH-05", "PASS" if pre == 200 and st in (200, 204) and (a == 401 or (a == 200 and not js(ba).get("user"))) and t == 401 else "FAIL",
    f"before={pre}, DELETE session={st} (rid {h.get('X-Request-Id')}), after: /api/auth/session={a} user={'present' if js(ba).get('user') else 'absent'}, /api/v1/tasks={t} (rid {ht.get('X-Request-Id')})")

p = sum(1 for _, s in res if s == "PASS"); f = sum(1 for _, s in res if s == "FAIL"); o = len(res) - p - f
print(f"=== api_free PASS={p} FAIL={f} OTHER(PENDING/BLOCKED)={o} ===")
sys.exit(1 if f else 0)
