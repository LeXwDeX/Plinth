#!/usr/bin/env python3
"""Cross-user and administrator acceptance checks on a throwaway local stack (docs/acceptance-1.0.md).

Builds ./cmd/art-ai from this checkout and starts one isolated stack on a free loopback port in the production shape
(APP_ENV=production, session login, PROVIDER_MODE=mock, AGENT_MODE=live against an in-process fake LLM that records the
system prompt of every turn). The admin password, the two ordinary users' passwords and the session secret are random
per run and live only in this process and the child's environment; nothing is written to the repository.

Covers (see scripts/acceptance/README.md):
  AUTH-03  ordinary user: every /api/admin/* route is 403, anonymous 401; admin lists the new user as non-admin
  AUTH-09  user B against user A's session, task, event, asset, archive and upload routes: each response equals the
           response for a nonexistent id (same status, same masked body); anonymous 401; A's data is untouched
  DLV-03   cross-user and anonymous asset / archive downloads
  ADM-01   admin users / generations / usage lists agree with what was created; admin download of A's artifact works,
           is byte-identical to the owner's download and writes audit rows whose actor is the admin
  AGT-11   admin publishes a marked system prompt -> the next agent turn uses it; resetting restores the default;
           ordinary users can neither read nor write the full configuration and /effective does not leak the prompt
  --ui     optional Playwright pass (cross_user_ui.mjs): ordinary user sees no admin entry and /admin pages are denied,
           admin pages render, admin download in the UI writes the audit row

Time: about 2 minutes (3 minutes with --ui). Side effects: a work directory under the system temp dir (removed on
success unless --keep). Needs go and build/go-web; --ui also needs Playwright (see README).

  python3 scripts/acceptance/cross_user.py [--ui] [--keep] [--work DIR] [--web-root DIR]
"""
import argparse, atexit, copy, http.server, json, os, re, secrets, shutil, subprocess, sys, tempfile, threading, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from acceptance_lib import *

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("--repo", default=repo_root(), help="checkout to build and serve (default: this one)")
ap.add_argument("--web-root", help="built SPA directory (default: <repo>/build/go-web)")
ap.add_argument("--work", help="work directory (default: new temp directory)")
ap.add_argument("--keep", action="store_true", help="keep the work directory (database, storage, logs)")
ap.add_argument("--ui", action="store_true", help="also run the Playwright UI pass (cross_user_ui.mjs)")
ARGS = ap.parse_args()

REPO = os.path.abspath(ARGS.repo)
WEB_ROOT = check_web_assets(REPO, ARGS.web_root)
WORK = ARGS.work or tempfile.mkdtemp(prefix="plinth-acceptance-cross-")
os.makedirs(WORK + "/data/storage", exist_ok=True); os.makedirs(WORK + "/data/logs", exist_ok=True)
APP_PORT, LLM_PORT = free_ports(2)
BASE = f"http://127.0.0.1:{APP_PORT}"
CRED = {"admin": ("admin", secrets.token_urlsafe(18)), "a": ("acc_user_a", secrets.token_urlsafe(18)), "b": ("acc_user_b", secrets.token_urlsafe(18))}
res = []
def rec(i, st, ev): res.append((i, st)); print(f"{st}: {i} | {ev}", flush=True)
def check(cond): return "PASS" if cond else "FAIL"

# ---- fake LLM: streams "ok" and records the system prompt of each turn
LLM_LOG = []
class FakeLLM(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0)); b = json.loads(self.rfile.read(n) or b"{}")
        LLM_LOG.append("\n".join(m.get("content", "") for m in b.get("messages", []) if m.get("role") == "system" and isinstance(m.get("content"), str)))
        self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
        ev = lambda o: self.wfile.write(("data: " + json.dumps(o) + "\n\n").encode())
        ev({"id": "c1", "object": "chat.completion.chunk", "model": "fake", "choices": [{"index": 0, "delta": {"role": "assistant", "content": "ok"}, "finish_reason": None}]})
        ev({"id": "c1", "object": "chat.completion.chunk", "model": "fake", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}})
        self.wfile.write(b"data: [DONE]\n\n")
llm = http.server.ThreadingHTTPServer(("127.0.0.1", LLM_PORT), FakeLLM)
threading.Thread(target=llm.serve_forever, daemon=True).start()

# ---- stack
BIN = WORK + "/art-ai"
build_binary(REPO, BIN)
rev = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO).decode().strip()
print(f"# cross-user stack, checkout {rev}, work={WORK}")
env = {"HOME": os.environ.get("HOME", WORK), "PATH": os.environ["PATH"], "APP_ENV": "production", "AUTH_MODE": "session", "PROVIDER_MODE": "mock",
       "AGENT_MODE": "live", "LLM_BASE_URL": f"http://127.0.0.1:{LLM_PORT}/v1", "LLM_API_KEY": secrets.token_hex(16), "LLM_MODEL": "fake-model",
       "APP_ADDR": f"127.0.0.1:{APP_PORT}", "SESSION_SECRET": secrets.token_urlsafe(48), "ADMIN_USERNAME": CRED["admin"][0],
       "ADMIN_PASSWORD": CRED["admin"][1], "ADMIN_EMAIL": "admin@example.test", "DATABASE_PATH": WORK + "/data/a.db",
       "STORAGE_ROOT": WORK + "/data/storage", "LOG_DIR": WORK + "/data/logs", "WEB_ASSETS_ROOT": WEB_ROOT, "LOG_LEVEL": "debug"}
proc = subprocess.Popen([BIN], env=env, cwd=REPO, stdout=open(WORK + "/data/stdout.log", "ab"), stderr=subprocess.STDOUT)

def _stop_stack():
    if proc.poll() is None:
        proc.terminate()
        try: proc.wait(10)
        except Exception: proc.kill(); proc.wait(5)
atexit.register(_stop_stack)  # also on exceptions and Ctrl-C

def finish(code):
    if proc.poll() is None:
        proc.terminate()
        try: proc.wait(10)
        except Exception: proc.kill()
    llm.shutdown()
    p = sum(1 for _, s in res if s == "PASS"); f = sum(1 for _, s in res if s == "FAIL")
    print(f"=== cross_user PASS={p} FAIL={f} OTHER={len(res) - p - f} ===")
    if not ARGS.keep and not f: shutil.rmtree(WORK, ignore_errors=True)
    else: print(f"# work directory kept: {WORK}")
    sys.exit(code or (1 if f else 0))

for _ in range(120):
    try:
        if urllib.request.urlopen(BASE + "/healthz", timeout=1).status == 200: break
    except Exception: time.sleep(0.25)
else:
    print("stack failed to start:", open(WORK + "/data/stdout.log", errors="replace").read()[-400:]); finish(2)

def login(key):
    api = Api(BASE); s, d, _ = api.j("POST", "/api/auth/login", {"username": CRED[key][0], "password": CRED[key][1]}); return api, s

def gen(api, cap, sid, prompt, tag, timeout=240):
    """prepare -> grant (-> approve) -> submit -> wait, on the first available recommended Offering of cap."""
    s, o, _ = api.j("GET", f"/api/v1/offerings?capability={cap}")
    offs = sorted([x for x in o.get("offerings", []) if x.get("available")], key=lambda x: not x.get("recommended"))
    s, p, _ = api.j("POST", "/api/v1/tasks/prepare", {"session_id": sid, "capability": cap, "offering_id": offs[0]["id"], "input": {"prompt": prompt}}); pid = p["prepared_input_id"]
    s, g, _ = api.j("POST", "/api/v1/tasks/grants", {"prepared_input_id": pid}, headers={"Idempotency-Key": f"{tag}-{pid}"}); rid = g.get("request_id")
    if g.get("status") == "pending":
        s, _, _ = api.j("PUT", f"/api/v1/tasks/grants/{rid}", {"decision": "approve"}); assert s == 200, f"approve {s}"
    s, t, _ = api.j("POST", "/api/v1/tasks", {"prepared_input_id": pid, "authorization_request_id": rid}, headers={"Idempotency-Key": f"{tag}-{pid}"}); tid = t["task_id"]
    end = time.time() + timeout
    while time.time() < end:
        s, t, _ = api.j("GET", f"/api/v1/tasks/{tid}")
        if t.get("state") in ("SUCCEEDED", "FAILED", "CANCELED"): return tid, t
        time.sleep(1.5)
    return tid, t

try:
    admin, s = login("admin")
    if s != 200: rec("setup", "FAIL", f"admin login {s}"); finish(2)

    # ---- AUTH-03: create two ordinary users, then probe admin routes
    for key, label in (("a", "User A"), ("b", "User B")):
        s, d, _ = admin.j("POST", "/api/admin/users", {"username": CRED[key][0], "password": CRED[key][1], "name": label, "email": f"{key}@example.test", "isAdmin": False})
        if s not in (200, 201): rec("setup", "FAIL", f"create {key}: {s} {json.dumps(d)[:200]}"); finish(2)
    A, sa = login("a"); B, sb = login("b"); anon = Api(BASE)
    s, d, _ = admin.j("GET", "/api/admin/users")
    roles = {u["username"]: u["isAdmin"] for u in d.get("data", [])}
    routes = ["/api/admin/users", "/api/admin/audit-logs", "/api/admin/generations", "/api/admin/usage", "/api/admin/balance-summary",
              "/api/admin/agent-configuration", "/api/admin/llm-settings", "/api/admin/user-assets/x"]
    got = [(p, A.j("GET", p)[0], anon.j("GET", p)[0]) for p in routes]
    print("  admin routes (path, ordinary user, anonymous):", got)
    sw, _, _ = A.j("POST", "/api/admin/users", {"username": "x", "password": secrets.token_urlsafe(12), "email": "x@example.test", "isAdmin": True})
    sp, _, _ = A.j("POST", "/api/admin/agent-configuration/publish", {})
    rec("AUTH-03", check(sa == sb == 200 and roles.get(CRED["a"][0]) is False and all(a == 403 and n == 401 for _, a, n in got) and sw == 403 and sp == 403),
        f"A/B login {sa}/{sb}, admin list shows A isAdmin={roles.get(CRED['a'][0])}; ordinary-user GET statuses {sorted({a for _, a, _ in got})} over {len(got)} admin routes, anonymous {sorted({n for _, _, n in got})}; POST create-user {sw}, publish {sp}")

    # ---- user A's data
    s, d, _ = A.j("POST", "/api/chat/sessions", {"title": "A private session"}); sid = d["id"]
    A.j("PUT", f"/api/v1/chat/sessions/{sid}/approval", {"approvalMode": "manual"})
    img_id, img = gen(A, "text-to-image", sid, "A small red lantern on a wooden table", "a")
    m3d_id, m3d = gen(A, "text-to-3d", sid, "A simple low-poly wooden stool", "a3")
    body, ct = multipart("up.png", "image/png", png(8, 8, lambda x, y: (255, 0, 0, 255)))
    s, up, _ = A.j("POST", "/api/v1/assets", raw=body, ctype=ct)
    if img.get("state") != "SUCCEEDED" or m3d.get("state") != "SUCCEEDED" or s not in (200, 201): rec("setup", "FAIL", f"A data: image {img.get('state')}, 3d {m3d.get('state')}, upload {s}"); finish(2)
    asset = img["assets"][0]; glb = next(a for a in m3d["assets"] if a["media_type"] == "model/gltf-binary"); upid = up["id"]

    # ---- AUTH-09 / DLV-03: B against A's resources vs nonexistent ids
    fake_s, fake_t, fake_a = "session_NOPENOPENOPENOPENOPE_00", "task_" + "0" * 32, "asset_" + "0" * 32
    def targets(s_, t_, t3_, a_, u_, g_):
        return [("GET", f"/api/chat/sessions/{s_}"), ("GET", f"/api/chat/sessions/{s_}/messages"), ("GET", f"/api/chat/sessions/{s_}/stream"),
                ("GET", f"/api/v1/chat/sessions/{s_}/harness"), ("GET", f"/api/v1/chat/sessions/{s_}/approval"), ("GET", f"/api/v1/tasks/{t_}"),
                ("GET", f"/api/v1/tasks/{t_}/events"), ("GET", f"/api/v1/tasks/{t_}/assets/{a_}"), ("GET", f"/api/v1/tasks/{t3_}/archive"),
                ("GET", f"/api/v1/tasks/{t3_}/assets/{g_}"), ("GET", f"/api/assets/{u_}"), ("GET", f"/api/v1/assets/{u_}"), ("GET", f"/api/v1/assets/{a_}"),
                ("GET", f"/api/v1/tasks/{t_}/recovery"), ("POST", f"/api/v1/tasks/{t_}/cancel")]
    real = targets(sid, img_id, m3d_id, asset["id"], upid, glb["id"]); fake = targets(fake_s, fake_t, fake_t, fake_a, fake_a, fake_a)
    norm = lambda b: re.sub(rb"req_[0-9a-f]+|[0-9a-f]{32}", b"X", b)
    rows = []; bad = []
    for (m, p), (_, pf) in zip(real, fake):
        kw = {"body": {}} if m == "POST" else {}
        ra, rb, rf, rn = A.call(m, p, **kw), B.call(m, p, **kw), B.call(m, pf, **kw), anon.call(m, p, **kw)
        same = rb[0] == rf[0] and norm(rb[2]) == norm(rf[2])
        rows.append((m, re.sub(r"[0-9a-f]{32}|session_[A-Za-z0-9_]+", "<id>", p), ra[0], rb[0], rf[0], rn[0]))
        if not same or rb[0] not in (403, 404) or rn[0] != 401: bad.append(f"{m} {rows[-1][1]}: B={rb[0]} nonexistent={rf[0]} anon={rn[0]} identical={same}")
    for r in rows: print("  ", r)
    intact = A.j("GET", f"/api/v1/tasks/{img_id}")[0] == 200 and A.j("GET", f"/api/chat/sessions/{sid}")[0] == 200
    s, tl, _ = B.j("GET", "/api/v1/tasks"); b_tasks = len(tl.get("tasks", tl.get("items", []))) if isinstance(tl, dict) else -1
    s, sl, _ = B.j("GET", "/api/chat/sessions"); b_sessions = [x for x in (sl.get("sessions", []) if isinstance(sl, dict) else sl) if x.get("id") == sid]
    rec("AUTH-09", check(not bad and intact and b_tasks == 0 and not b_sessions),
        f"{len(rows)} routes: B's response equals the nonexistent-id response and anonymous is 401 for all" if not bad else "; ".join(bad[:6]))
    rec("AUTH-09 (B's lists)", check(b_tasks == 0 and not b_sessions), f"B task list has {b_tasks} entries, B session list contains A's session={bool(b_sessions)}")
    sm, _, _, _ = B.call("POST", f"/api/chat/sessions/{sid}/messages", body={"content": "x", "creative_domain": "image"})
    sap, _, _, _ = B.call("PUT", f"/api/v1/chat/sessions/{sid}/approval", body={"approvalMode": "auto"})
    sd, hd, _, _ = B.call("DELETE", f"/api/chat/sessions/{sid}")
    mode = A.j("GET", f"/api/v1/chat/sessions/{sid}/approval")[1]
    rec("AUTH-09 (B writes)", check(sm in (403, 404) and sap in (403, 404) and sd in (403, 404) and A.call("GET", f"/api/chat/sessions/{sid}")[0] == 200 and "manual" in json.dumps(mode)),
        f"B post message {sm}, put approval {sap}, delete session {sd}; A's session still readable, approval mode still {json.dumps(mode)[:60]}")
    sa_, sz_ = anon.call("GET", asset["url"])[0], anon.call("GET", f"/api/v1/tasks/{m3d_id}/archive")[0]
    sb_, sbz = B.call("GET", asset["url"])[0], B.call("GET", f"/api/v1/tasks/{m3d_id}/archive")[0]
    rec("DLV-03", check(sa_ == sz_ == 401 and sb_ in (403, 404) and sbz in (403, 404)), f"anonymous asset/archive {sa_}/{sz_}; user B asset/archive {sb_}/{sbz}")

    # ---- ADM-01: admin lists and cross-user download with audit
    s, ug, _ = admin.j("GET", "/api/admin/generations")
    gen_ids = {g["taskId"] for g in ug.get("data", [])}; s2, usage, _ = admin.j("GET", "/api/admin/usage")
    rec("ADM-01 (lists)", check(s == s2 == 200 and {img_id, m3d_id} <= gen_ids), f"generations {s} total={ug.get('total')} includes both of A's tasks={ {img_id, m3d_id} <= gen_ids}; usage {s2} {json.dumps(usage)[:120]}")
    # Reading the audit log is itself audited (admin_read_audit_logs), so ignore those rows; downloads are audited for the
    # owner too, so judge by actor: the admin's reads of A's artifacts must appear as rows whose actor is the admin.
    admin_id = admin.j("GET", "/api/auth/session")[1].get("user", {}).get("id")
    def audit_rows(): return {x["id"]: (x["action"], x["userId"]) for x in admin.j("GET", "/api/admin/audit-logs?pageSize=100")[1].get("data", []) if x["action"] != "admin_read_audit_logs"}
    before_rows = audit_rows()
    own = A.call("GET", asset["url"])
    adl = admin.call("GET", asset["url"]); azip = admin.call("GET", f"/api/v1/tasks/{m3d_id}/archive")
    s3, h3, b3, _ = admin.call("GET", f"/api/admin/generations/{img_id}/download")
    new_rows = [v for k, v in audit_rows().items() if k not in before_rows]
    admin_actions = sorted(act for act, uid in new_rows if uid == admin_id); owner_actions = sorted(act for act, uid in new_rows if uid != admin_id)
    rec("ADM-01 / AUTH-09 (audit)", check(own[0] == 200 and adl[0] == 200 and azip[0] == 200 and s3 == 200 and admin_id and admin_actions.count("download_asset") >= 2 and "download_model_archive" in admin_actions
                                         and hashlib.sha256(adl[2]).hexdigest() == asset["sha256"] and hashlib.sha256(own[2]).hexdigest() == asset["sha256"]),
        f"owner download {own[0]} (audit rows by owner: {owner_actions}); admin download of A's artifact {adl[0]} (bytes match declared sha256), archive {azip[0]} and generation download {s3} ({len(b3)} bytes) wrote admin-actor rows {admin_actions}")

    # ---- AGT-11: publish -> next turn -> reset
    s, d, _ = A.j("POST", "/api/chat/sessions", {"title": "agt11"}); asid = d["id"]
    def turn(text):
        n = len(LLM_LOG); A.j("POST", f"/api/chat/sessions/{asid}/messages", {"content": text, "creative_domain": "image"})
        for _ in range(40):
            time.sleep(1)
            if len(LLM_LOG) > n: time.sleep(1.5); return LLM_LOG[n:]
        return []
    MARK = "ACCEPTANCE-MARKER-" + secrets.token_hex(4)
    r0 = turn("hello, first turn")
    s, cfg, _ = admin.j("GET", "/api/admin/agent-configuration"); rev0 = cfg["revision"]; c = cfg["configuration"]; default_prompt = c["system_prompt"]
    new = copy.deepcopy(c); new["system_prompt"] = MARK + " " + default_prompt
    sv, v, _ = admin.j("POST", "/api/admin/agent-configuration/validate", {"configuration": new})
    spb, pub, _ = admin.j("POST", "/api/admin/agent-configuration/publish", {"expected_revision": rev0, "configuration": new}); rev1 = pub.get("revision")
    r1 = turn("second turn")
    s, eff, _ = A.j("GET", "/api/v1/agent-configuration/effective")
    leak = MARK in json.dumps(eff) or default_prompt[:30] in json.dumps(eff) or "system_prompt" in eff
    sg, _, _ = A.j("GET", "/api/admin/agent-configuration"); sr, _, _ = A.j("POST", "/api/admin/agent-configuration/reset", {"expected_revision": rev1})
    srs, rs, _ = admin.j("POST", "/api/admin/agent-configuration/reset", {"expected_revision": rev1})
    r2 = turn("third turn")
    rec("AGT-11", check(r0 and r1 and r2 and not any(MARK in x for x in r0) and all(MARK in x for x in r1) and not any(MARK in x for x in r2) and all(default_prompt[:40] in x for x in r2)
                        and sv == 200 and spb == 200 and srs == 200 and not leak and sg == 403 and sr == 403 and rs.get("configuration", {}).get("system_prompt") == default_prompt),
        f"turns seen by the fake LLM {len(r0)}/{len(r1)}/{len(r2)}; marker before/after publish/after reset = {[any(MARK in x for x in r) for r in (r0, r1, r2)]}; validate {sv} publish {spb} reset {srs}; ordinary user read {sg} reset {sr}; /effective leaks prompt={leak}; revisions {rev0}->{rev1}->{rs.get('revision')}")

    # ---- optional UI pass
    if ARGS.ui:
        node = shutil.which("node")
        if not node: rec("UI", "BLOCKED", "node not found")
        else:
            uenv = dict(os.environ, BASE=BASE, ADMIN_USERNAME=CRED["admin"][0], ADMIN_PASSWORD=CRED["admin"][1], USER_USERNAME=CRED["b"][0], USER_PASSWORD=CRED["b"][1],
                        OUT=WORK + "/ui")
            r = subprocess.run([node, os.path.join(os.path.dirname(os.path.abspath(__file__)), "cross_user_ui.mjs")], env=uenv, capture_output=True, text=True, timeout=300)
            print(r.stdout[-3000:], r.stderr[-1500:])
            rec("AUTH-03 / ADM-01 (UI)", check(r.returncode == 0), f"cross_user_ui.mjs exit {r.returncode}; screenshots under {WORK}/ui")
except Exception as e:
    import traceback; traceback.print_exc(); rec("script", "FAIL", f"{type(e).__name__}: {e}")
finish(0)
