#!/usr/bin/env python3
"""Local isolated Mock-stack acceptance checks (docs/acceptance-1.0.md, API half of the Mock-mode items).

Builds ./cmd/art-ai from this checkout and starts throwaway zero-key stacks (PROVIDER_MODE=mock, AGENT_MODE=mock, dev mock
auth) on free loopback ports with their own database and storage under a fresh work directory. Nothing leaves the machine.

Covers LOG-05 (invalid LOG_LEVEL refuses to start), PRV-02 (live mode without keys: remote Offerings unavailable, reason
text from the public Schema, no silent substitution), IMG-01..06, M3D-01..04 (API half; M3D-04 is PARTIAL: one-step plan),
DLV-02 (archive), TSK-02..05, AGT-05..07 (API half), SEC-04 (429 + Retry-After). CNV-01 is reported NOT-COVERED
(frontend store behaviour; see browser_matrix and scripts/test-canvas-recovery.mjs).

Needs: go, and build/go-web (run `pnpm build` once). Time: about 3 minutes. Side effects: only the work directory
(default: a new directory under the system temp dir, removed on success unless --keep). Exit status 1 on any FAIL.

  python3 scripts/acceptance/mock_stack.py [--work DIR] [--keep] [--base-port N]
"""
import argparse, atexit, json, os, re, signal, subprocess, sys, tempfile, time, threading, hashlib, shutil, uuid
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from acceptance_lib import *

ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
ap.add_argument("--repo", default=repo_root(), help="checkout to build and serve (default: this one)")
ap.add_argument("--web-root", help="built SPA directory (default: <repo>/build/go-web)")
ap.add_argument("--work", help="work directory (default: new temp directory)")
ap.add_argument("--keep", action="store_true", help="keep the work directory (databases, storage, logs)")
ap.add_argument("--base-port", type=int, help="first of 5 consecutive loopback ports (default: pick free ports)")
ARGS = ap.parse_args()

REPO = os.path.abspath(ARGS.repo)
PORTS = [ARGS.base_port + i for i in range(5)] if ARGS.base_port else free_ports(5)
PORT = PORTS[0]
WORK = ARGS.work or tempfile.mkdtemp(prefix="plinth-acceptance-mock-")
os.makedirs(WORK, exist_ok=True)
BIN = os.path.join(WORK, "art-ai")
BASE = f"http://127.0.0.1:{PORT}"
WEB_ROOT = check_web_assets(REPO, ARGS.web_root)
res = []
def rec(i, st, ev): res.append((i, st)); print(f"{st}: {i} | {ev}", flush=True)

PROCS = []

def env_for(port, mode="mock", extra=None, sub="main"):
    d = os.path.join(WORK, sub); os.makedirs(d + "/storage", exist_ok=True); os.makedirs(d + "/logs", exist_ok=True)
    e = {"HOME": os.environ.get("HOME", WORK), "PATH": os.environ["PATH"], "APP_ENV": "development", "PROVIDER_MODE": mode, "AGENT_MODE": "mock",
         "AUTH_MODE": "development", "DEV_MOCK_AUTH": "true", "DEV_MOCK_USER": '{"id":"mock_acceptance","name":"acceptance","email":"acceptance@example.test"}', "DEV_MOCK_AUTH_BYPASS_USER_STATE": "true", "APP_ADDR": f"127.0.0.1:{port}",
         "WEB_ASSETS_ROOT": WEB_ROOT, "DATABASE_PATH": d + "/a.db", "STORAGE_ROOT": d + "/storage", "LOG_DIR": d + "/logs"}
    e.update(extra or {}); return e, d

def start(port, mode="mock", extra=None, sub="main", wait=True):
    e, d = env_for(port, mode, extra, sub)
    p = subprocess.Popen([BIN], env=e, cwd=REPO, stdout=open(d + "/stdout.log", "ab"), stderr=subprocess.STDOUT)
    PROCS.append(p)
    if wait:
        for _ in range(80):
            if p.poll() is not None: return p, d
            try:
                if urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1).status == 200: return p, d
            except Exception: time.sleep(0.25)
    return p, d

def stop(p):
    if p.poll() is None:
        p.terminate()
        try: p.wait(10)
        except Exception:
            p.kill()
            try: p.wait(5)
            except Exception: pass
    if p in PROCS: PROCS.remove(p)

# Every stack process is tracked and stopped on any exit path (normal end, exception, Ctrl-C, SIGTERM). A run that did not
# finish cleanly keeps its work directory so the logs and databases of the failed run stay available.
FINISHED = False
def cleanup_all():
    for p in list(PROCS): stop(p)
    if not FINISHED: print(f"# run did not finish; work directory kept: {WORK}", flush=True)
atexit.register(cleanup_all)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))
signal.signal(signal.SIGHUP, lambda *_: sys.exit(129))

build_binary(REPO, BIN)
rev = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO).decode().strip()
dirty = subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO).decode().strip()
print(f"# mock stack, checkout {rev}{' (DIRTY)' if dirty else ''}, work={WORK}")

# ---- LOG-05, PRV-02 first (separate processes)
p, d = start(PORTS[1], extra={"LOG_LEVEL": "bogus"}, sub="log05", wait=False)
try: code = p.wait(15)
except Exception: code = None; stop(p)
out = open(d + "/stdout.log", errors="replace").read()[-300:]
rec("LOG-05", "PASS" if code not in (None, 0) and "log" in out.lower() else "FAIL", f"LOG_LEVEL=bogus exit={code}; message={out.strip()[-160:]!r}")

p, d = start(PORTS[2], mode="live", sub="prv02", extra={"DEV_MOCK_AUTH": "true"})
if p.poll() is not None:
    rec("PRV-02", "BLOCKED", f"live-mode zero-key stack did not start, exit={p.poll()}: {open(d+'/stdout.log',errors='replace').read()[-200:]!r}")
else:
    a2 = Api(f"http://127.0.0.1:{PORTS[2]}"); a2.mock_login()
    s, o, h = a2.j("GET", "/api/v1/offerings")
    offs = o.get("offerings") or []
    remote = [x for x in offs if x.get("provider_id") not in ("local",)]
    unav = [x for x in remote if not x.get("available")]
    s_, sch2, _ = a2.j("GET", "/api/v1/schema")
    flat = json.dumps(sch2, ensure_ascii=False)
    m = re.search(r'"ui\.studio\.params\.unavailable":\s*"([^"]+)"', flat)
    reasons = bool(m); reason_text = m.group(1) if m else None
    local_ok = all(x.get("available") for x in offs if x.get("provider_id") == "local")
    # silent substitution: preparing an unavailable offering must be refused, not rerouted
    tgt = unav[0] if unav else None
    prep = None
    if tgt:
        s2, sess, _ = a2.j("POST", "/api/chat/sessions", {"title": ""})
        prep, _, _ = a2.j("POST", "/api/v1/tasks/prepare", {"session_id": sess["id"], "capability": tgt["capability"], "offering_id": tgt["id"], "input": {"prompt": "x"}})
    keys = sorted(k for k in (unav[0] if unav else {}) if "reason" in k or "unavail" in k)
    rec("PRV-02", "PASS" if s == 200 and remote and len(unav) == len(remote) and reasons and local_ok and prep and prep >= 400 else "FAIL",
        f"live mode, zero keys: remote offerings {len(remote)}, unavailable {len(unav)}, UI reason text from public Schema ui.studio.params.unavailable={reason_text!r}, local available={local_ok}; prepare of unavailable {tgt and tgt['id']} -> {prep}")
stop(p)

# ---- main stack
main, mdir = start(PORT)
if main.poll() is not None: print("main stack failed to start"); sys.exit(2)
a = Api(BASE); a.mock_login()
s, sch, _ = a.j("GET", "/api/v1/schema")
S = sch.get("schema", {})

def new_session():
    s, d, _ = a.j("POST", "/api/chat/sessions", {"title": ""}); return d["id"]

def upload(data, name="ref.png", ct="image/png"):
    body, c = multipart(name, ct, data); s, d, _ = a.j("POST", "/api/v1/assets", raw=body, ctype=c); return s, d

def download(url): return a.call("GET", url)

def wait(tid, secs=240, until=("SUCCEEDED", "FAILED", "CANCELED")):
    end = time.time() + secs; t = {}
    while time.time() < end:
        s, t, _ = a.j("GET", f"/api/v1/tasks/{tid}")
        if s == 200 and t.get("state") in until: return t
        time.sleep(1)
    return t

def submit(offering, cap, inp, sid=None, approve=True):
    """prepare -> grant (-> approve) -> submit. Returns (task_id, info)."""
    sid = sid or new_session()
    s, p, _ = a.j("POST", "/api/v1/tasks/prepare", {"session_id": sid, "capability": cap, "offering_id": offering, "input": inp})
    if s not in (200, 201): return None, {"stage": "prepare", "status": s, "body": p}
    pid = p["prepared_input_id"]
    s, g, _ = a.j("POST", "/api/v1/tasks/grants", {"prepared_input_id": pid}, headers={"Idempotency-Key": "acc-" + pid})
    if s not in (200, 201): return None, {"stage": "grant", "status": s, "body": g, "prepared": p}
    rid = g.get("request_id")
    if g.get("status") == "pending":
        if not approve: return None, {"stage": "pending", "request_id": rid}
        s, dd, _ = a.j("PUT", f"/api/v1/tasks/grants/{rid}", {"decision": "approve"})
        if s != 200: return None, {"stage": "approve", "status": s, "body": dd}
    s, t, h = a.j("POST", "/api/v1/tasks", {"prepared_input_id": pid, "authorization_request_id": rid}, headers={"Idempotency-Key": "acc-" + pid})
    if s not in (200, 201, 202): return None, {"stage": "submit", "status": s, "body": t}
    return t.get("task_id") or t.get("id"), {"prepared": p, "grant": g, "session": sid, "rid": h.get("X-Request-Id")}

def assets_ok(t):
    """Every asset downloads with declared size/sha. Returns (ok, list of (role,variant,media,size))."""
    out = []; ok = True
    for x in t.get("assets") or []:
        s, h, b, _ = download(x["url"])
        good = s == 200 and len(b) == x["size"] and hashlib.sha256(b).hexdigest() == x["sha256"]
        ok &= good; out.append((x.get("role"), x.get("variant"), x["media_type"], x["size"], good))
    return ok and bool(out), out

def img_assets(t): return [x for x in t.get("assets") or [] if x.get("role") == "image"]

def run(offering, cap, inp, **k):
    tid, info = submit(offering, cap, inp, **k)
    if not tid: return None, info
    return wait(tid), info

T2I, I2I, MAT = "openai/gpt-image-2/text-to-image", "openai/gpt-image-2/image-to-image", "matting/default/background-removal"
TXT3D, IMG3D = "meshy/meshy-7.1/text-to-3d", "meshy/meshy-7.1/image-to-3d"
offerings = {o["id"]: o for o in a.j("GET", "/api/v1/offerings")[1].get("offerings", [])}

# ---- IMG-01/02
t, info = run(T2I, "text-to-image", {"prompt": "a red cat"})
if t and t.get("state") == "SUCCEEDED":
    ok, lst = assets_ok(t); im = img_assets(t)
    s, h, b, _ = download(im[0]["url"]); dim = png_decode(b); 
    params = [p["name"] for p in offerings[T2I]["parameters"]]
    rec("IMG-01 (API half; param popover/card/stage are UI)", "PASS" if ok and dim["w"] else "FAIL", f"task {t['id']} SUCCEEDED, {len(im)} image asset(s) verified size+sha256, credits={t.get('credits_charged')}, schema params={params[:6]}..; request-id {info['rid']}")
    want = offerings[T2I]["parameters"]; dsz = next(p for p in want if p["name"] == "size").get("default", "")
    sz = next(p for p in want if p["name"] == "size")
    t2, _i = run(T2I, "text-to-image", {"prompt": "portrait", "size": "1024x1536"})
    d2 = png_decode(download(img_assets(t2)[0]["url"])[2]) if t2 and t2.get("state") == "SUCCEEDED" else {"w": 0, "h": 0}
    rec("IMG-02", "PASS" if dim["w"] == dim["h"] and d2["w"] == 1024 and d2["h"] == 1536 and not any(o["value"] == "auto" for o in sz.get("options", [])) else "FAIL",
        f"no size given: schema default {dsz}, asset bytes {dim['w']}x{dim['h']} (1:1 explicit; Mock caps the long side at 1536, internal/providers/mock/render_image.go:77, so 2048 is delivered as 1536); explicit 1024x1536 -> {d2['w']}x{d2['h']}; 'auto' option present=False")
else:
    rec("IMG-01", "FAIL", f"{t and t.get('state')} {info}"); rec("IMG-02", "BLOCKED", "IMG-01 failed")
# IMG-03
sid = new_session()
t, info = run(T2I, "text-to-image", {"prompt": "two cats", "n": 2}, sid=sid)
if t and t.get("state") == "SUCCEEDED":
    im = img_assets(t); shas = {x["sha256"] for x in im}; img_task = t
    cand = {"kind": "task", "taskId": t["id"], "assetId": im[-1]["id"], "role": "image"}
    s1, d1, _ = a.j("PUT", f"/api/v1/chat/sessions/{sid}/pick", cand)
    s2, d2, _ = a.j("GET", f"/api/v1/chat/sessions/{sid}/pick")
    rec("IMG-03 (API half; side-by-side cards are UI)", "PASS" if len(im) >= 2 and len(shas) >= 2 and s1 == 200 and s2 == 200 and json.dumps(d1.get("pick") or d1, sort_keys=True) == json.dumps(d2.get("pick") or d2, sort_keys=True) else "FAIL",
        f"n=2 -> {len(im)} image assets, {len(shas)} distinct sha; pick PUT={s1} GET after={s2} picked={json.dumps(d2, ensure_ascii=False)[:160]}")
else:
    rec("IMG-03", "FAIL", f"{t and t.get('state')} {info}")
# IMG-04 / IMG-05 / IMG-06
ref = png(64, 64, lambda x, y: (x * 4 % 256, y * 4 % 256, 120, 255))
s, up = upload(ref); rid_ = up.get("id")
t, info = run(I2I, "image-to-image", {"prompt": "recolor", "reference_assets": [rid_]})
bad_tid, bad = submit(I2I, "image-to-image", {"prompt": "recolor", "reference_assets": ["asset_does_not_exist"]})
if t and t.get("state") == "SUCCEEDED":
    s, h, b, _ = download(img_assets(t)[0]["url"]); w, hh, bpp, rows = png_pixels(b)
    rec("IMG-04", "PASS" if bad_tid is None and bad.get("stage") == "prepare" and bad.get("status") == 400 else "FAIL",
        f"i2i with uploaded ref SUCCEEDED, output {w}x{hh}; invalid ref -> {bad.get('stage')} {bad.get('status')} {json.dumps(bad.get('body'), ensure_ascii=False)[:100]} (no task created, no silent text-to-image); composition-keep is Mock-side behaviour")
else:
    rec("IMG-04", "FAIL", f"{t and t.get('state')} {info}")
# inpaint: mask transparent in the right half
sq = png(64, 64, lambda x, y: (x * 4 % 256, y * 4 % 256, 120, 255))
mask = png(64, 64, lambda x, y: (0, 0, 0, 0) if x >= 32 else (0, 0, 0, 255))
_, upr = upload(sq); _, upm = upload(mask, "mask.png")
t, info = run(I2I, "image-to-image", {"prompt": "patch", "reference_assets": [upr["id"]], "mask": upm["id"]})
if t and t.get("state") == "SUCCEEDED":
    s, h, b, _ = download(img_assets(t)[0]["url"]); w, hh, bpp, rows = png_pixels(b)
    if (w, hh) == (64, 64):
        _, _, bs, rs = png_pixels(sq); keep = chg = 0; keep_ok = True; changed = 0
        for y in range(64):
            for x in range(64):
                o = bytes(rows[y][x*bpp:x*bpp+3]); r0 = bytes(rs[y][x*4:x*4+3])
                if x < 32:
                    keep += 1; keep_ok &= o == r0
                else:
                    chg += 1; changed += o != r0
        rec("IMG-05", "PASS" if keep_ok and changed > 0 else "FAIL", f"masked edit: unmasked half identical={keep_ok} ({keep} px), masked half changed {changed}/{chg} px")
    else:
        rec("IMG-05", "FAIL", f"output size {w}x{hh} differs from reference 64x64, cannot compare pixels")
else:
    rec("IMG-05", "FAIL", f"{t and t.get('state')} {info}")
t, info = run(MAT, "background-removal", {"source_assets": [rid_]})
if t and t.get("state") == "SUCCEEDED":
    s, h, b, _ = download(img_assets(t)[0]["url"]); dd = png_decode(b)
    w, hh, bpp, rows = png_pixels(b); alpha0 = sum(1 for r in rows for x in range(w) if bpp == 4 and r[x*4+3] == 0)
    rec("IMG-06", "PASS" if dd["ct"] == 6 and alpha0 > 0 else "FAIL", f"background removal SUCCEEDED, PNG colour type {dd['ct']}, fully transparent px {alpha0}; unconfigured-service-unavailable half is PRV-02-style (real mode) not run")
else:
    rec("IMG-06", "FAIL", f"{t and t.get('state')} {info} (mock matting)")

# ---- M3D-01/03
sid3 = new_session()
t, info = run(TXT3D, "text-to-3d", {"prompt": "a knight"}, sid=sid3)
if t and t.get("state") == "SUCCEEDED":
    ok, lst = assets_ok(t)
    req = next(p for p in offerings[TXT3D]["parameters"] if p["name"] == "target_formats")["default"]
    have = {x[1] for x in lst if x[0] == "model"}
    glb = [x for x in t["assets"] if x.get("variant") == "glb"][0]; _, _, gb, _ = download(glb["url"])
    thumb = any(x.get("role") == "thumbnail" for x in t["assets"])
    rec("M3D-01 (API half; viewer rotate/zoom is UI/B3)", "PASS" if ok and gb[:4] == b"glTF" and thumb else "FAIL", f"task {t['id']} SUCCEEDED, all {len(lst)} assets verified, GLB magic ok={gb[:4]==b'glTF'}, thumbnail={thumb}")
    s, fm, _ = a.j("GET", f"/api/v1/tasks/{t['id']}/formats")
    rec("M3D-03 (API half)", "PASS" if set(req) <= have else "FAIL", f"required formats {req}, delivered model variants {sorted(have)}, formats route {s}")
    s, h, zb, _ = a.call("GET", f"/api/v1/tasks/{t['id']}/archive")
    rec("DLV-02(extra, local)", "PASS" if s == 200 and zb[:2] == b"PK" else "FAIL", f"archive {s} size {len(zb)}")
    m3d_task = t
else:
    rec("M3D-01", "FAIL", f"{t and t.get('state')} {info}"); rec("M3D-03", "BLOCKED", "M3D-01 failed"); m3d_task = None
# M3D-02
t, info = run(IMG3D, "image-to-3d", {"reference_assets": [rid_]})
if t and t.get("state") == "SUCCEEDED":
    s, lin, _ = a.j("GET", f"/api/v1/chat/sessions/{info['session']}/lineage")
    txt = json.dumps(lin)
    node = next((n for n in lin.get("nodes", []) if n.get("task_id") == t["id"]), {})
    edges = lin.get("edges", [])
    rel = [e for e in edges if t["id"] in json.dumps(e)]
    rec("M3D-02 (API half; task-detail UI text is B3)", "PASS" if rid_ in txt and rel else "FAIL", f"image-to-3d SUCCEEDED; lineage {s}: edges touching the task={json.dumps(rel)[:220]}; uploaded source asset present in lineage={rid_ in txt}")
else:
    rec("M3D-02", "FAIL", f"{t and t.get('state')} {info}")

# ---- M3D-04 processing plan
if m3d_task:
    glb = [x for x in m3d_task["assets"] if x.get("variant") == "glb"][0]
    proc = [("meshy/processing/remesh", {"target_formats": ["glb"], "topology": "quad", "target_polycount": 1000}),
            ("meshy/processing/retexture", None)]
    rt = offerings["meshy/processing/retexture"]; ri = offerings["meshy/processing/remesh"]
    body = {"session_id": sid3, "source_task_id": m3d_task["id"], "source_asset_id": glb["id"],
            "steps": [{"offering_id": "meshy/processing/remesh", "expected_offering_revision": ri["revision"], "input": {"target_formats": ["glb"], "topology": "quad", "target_polycount": 1000}}]}
    s, pl, h = a.j("POST", "/api/v1/processing-plans", body, headers={"Idempotency-Key": "acc-plan-" + uuid.uuid4().hex})
    if s in (200, 201, 202):
        end = time.time() + 120; last = pl
        while time.time() < end:
            s2, last, _ = a.j("GET", f"/api/v1/processing-plans/{pl['id']}")
            if last.get("state") in ("SUCCEEDED", "FAILED", "PAUSED", "NEEDS_ATTENTION", "CANCELED", "COMPLETED") or any(st.get("state") in ("PAUSED", "FAILED") for st in last.get("steps", [])): 
                if last.get("state") not in ("RUNNING", "PENDING"): break
            time.sleep(2)
        sts = [(st.get("state"), st.get("task_id") and "task", st.get("failure")) for st in last.get("steps", [])]
        rec("M3D-04 (single-step plan only: public Task per step)", "PARTIAL", f"plan {s}: state={last.get('state')} steps={sts}; pause/resume/retry/incompatible-source reject NOT exercised by this script (see notes)")
    else:
        rec("M3D-04", "FAIL", f"plan create {s}: {json.dumps(pl, ensure_ascii=False)[:200]}")
    bad = dict(body); bad["source_task_id"] = img_task["id"]; bad["source_asset_id"] = img_assets(img_task)[0]["id"]
    sb_, pb_, _ = a.j("POST", "/api/v1/processing-plans", bad, headers={"Idempotency-Key": "acc-plan-" + uuid.uuid4().hex})
    rec("M3D-04 incompatible source rejected", "PASS" if 400 <= sb_ < 500 else "FAIL", f"plan with an image Task as source -> {sb_} {json.dumps(pb_, ensure_ascii=False)[:140]}")
else:
    rec("M3D-04", "BLOCKED", "no 3D source task")

# ---- TSK-02/03/05
sidt = new_session()
tid, info = submit(T2I, "text-to-image", {"prompt": "slow one [mock:slow]"}, sid=sidt)
if tid:
    seen = []
    for _ in range(30):
        s, td, _ = a.j("GET", f"/api/v1/tasks/{tid}"); seen.append((td.get("state"), td.get("progress")))
        if td.get("state") in ("SUCCEEDED", "FAILED", "CANCELED"): break
        time.sleep(1)
    states = [x[0] for x in seen]
    a2 = Api(BASE); a2.mock_login()  # "reload": fresh client, same owner
    s, again, _ = a2.j("GET", f"/api/v1/tasks/{tid}")
    s3, lst, _ = a2.j("GET", "/api/v1/tasks")
    rec("TSK-02 (API half; page reload display is UI)", "PASS" if "RUNNING" in states and states[-1] == "SUCCEEDED" and again.get("state") == "SUCCEEDED" else "FAIL", f"observed states {sorted(set(states))} final {states[-1]}; fresh client sees {again.get('state')}; in task list={any(x.get('id')==tid for x in lst.get('tasks',[]))}")
else:
    rec("TSK-02", "FAIL", str(info))
# TSK-03 cancel
tid, info = submit(T2I, "text-to-image", {"prompt": "to cancel [mock:slow]"})
if tid:
    time.sleep(1); s, cd, _ = a.j("POST", f"/api/v1/tasks/{tid}/cancel", {}); t = wait(tid, 60)
    s2, cd2, _ = a.j("POST", f"/api/v1/tasks/{tid}/cancel", {})
    rec("TSK-03 cancel", "PASS" if s in (200, 202) and t.get("state") == "CANCELED" else "FAIL", f"cancel -> {s}, final {t.get('state')}; second cancel -> {s2}")
else:
    rec("TSK-03 cancel", "FAIL", str(info))
# TSK-05 + retry
tid, info = submit(T2I, "text-to-image", {"prompt": "no money [mock:insufficient_balance]"})
if tid:
    t = wait(tid, 120); s, full, h = a.j("GET", f"/api/v1/tasks/{tid}")
    txt = json.dumps(full, ensure_ascii=False)
    fail = {k: full.get(k) for k in ("state", "failure", "error", "failure_code", "error_code", "failure_message") if k in full}
    ftxt = json.dumps(full.get("failure"), ensure_ascii=False).lower(); leak = any(w in ftxt for w in ("traceback", "sqlite", "mock:", "goroutine", "http://", "https://", ".go:"))
    rec("TSK-05", "PASS" if t.get("state") == "FAILED" and not leak else "FAIL", f"state={t.get('state')} classification fields={json.dumps(fail, ensure_ascii=False)[:260]}; internal-reason leak={leak}")
    s, r1, _ = a.j("POST", f"/api/v1/tasks/{tid}/retry", {})
    rec("TSK-03 retry (failed task)", "INFO", f"retry on insufficient-balance task -> {s} {json.dumps(r1, ensure_ascii=False)[:200]}")
else:
    rec("TSK-05", "FAIL", str(info))

# ---- AGT-05/06/07
def say(sid, text, **extra):
    s, d, _ = a.j("POST", f"/api/chat/sessions/{sid}/messages", {"content": text, **extra}); time.sleep(2.5)
    s2, ms, _ = a.j("GET", f"/api/chat/sessions/{sid}/messages")
    return s, (ms if isinstance(ms, list) else ms.get("messages", []))
def pending(sid): return a.j("GET", f"/api/v1/chat/sessions/{sid}/pending-approvals")[1].get("items", [])
sa = new_session()
say(sa, "生成骑士3D模型", creative_domain="3d", ai_model=TXT3D)
say(sa, "再生成一个龙3D模型", creative_domain="3d", ai_model=TXT3D)
pl = pending(sa)
if len(pl) >= 2:
    ok_, no_ = pl[0]["request_id"], pl[1]["request_id"]
    res_ = []
    def dec(): res_.append(a.j("PUT", f"/api/v1/tasks/grants/{ok_}", {"decision": "approve"}))
    th = [threading.Thread(target=dec) for _ in range(2)]; [x.start() for x in th]; [x.join() for x in th]
    tids = {r[1].get("task_id") for r in res_}
    sr, rj, _ = a.j("PUT", f"/api/v1/tasks/grants/{no_}", {"decision": "deny"})
    time.sleep(2)
    s, tl, _ = a.j("GET", "/api/v1/tasks"); sess_tasks = [x for x in tl.get("tasks", []) if x.get("session_id") == sa]
    after = pending(sa)
    rec("AGT-05 approve/double-click/reject (API half)", "PASS" if len(tids) == 1 and None not in tids and len(sess_tasks) == 1 and sr == 200 and not after else "FAIL",
        f"2 concurrent approves -> statuses {[r[1].get('status') for r in res_]} one task {len(tids)==1}; session tasks={len(sess_tasks)} (reject created none); pending after={[(x.get("status"), x.get("request_id")[:10]) for x in after]}; reload-persistence of waiting cards: pending list re-read before decision had {len(pl)}")
    s, h, b, _ = a.call("GET", f"/api/v1/chat/sessions/{sa}/pending-approvals")
else:
    rec("AGT-05", "FAIL", f"expected 2 pending cards, got {len(pl)}")
# AGT-06 auto approval
sb = new_session()
say(sb, "生成骑士3D模型", creative_domain="3d", ai_model=TXT3D)
waiting = pending(sb)
sm, md, _ = a.j("PUT", f"/api/v1/chat/sessions/{sb}/approval", {"approvalMode": "auto"})
still = pending(sb)
n0 = len([x for x in a.j("GET", "/api/v1/tasks")[1].get("tasks", []) if x.get("session_id") == sb])
_, msgs = say(sb, "再生成一个龙3D模型", creative_domain="3d", ai_model=TXT3D)
n1 = len([x for x in a.j("GET", "/api/v1/tasks")[1].get("tasks", []) if x.get("session_id") == sb])
last = msgs[-1]["content"][:80] if msgs else ""
rec("AGT-06", "PASS" if len(waiting) == 1 and len(still) == 1 and n0 == 0 and n1 == 1 and not pending(sb)[1:] else "FAIL", f"switch to auto -> {sm} {md.get('approvalMode')}; waiting card stays pending ({len(still)}), tasks before/after next request {n0}->{n1}; assistant note: {last!r}")
# AGT-07 scope
sc = new_session()
route = S.get("runtime", {}).get("http", {}).get("routes", {}).get("chat_session_agent_scope", "")
s, sc0, _ = a.j("GET", f"/api/v1/chat/sessions/{sc}/agent-scope")
scopes = [x for x in (S.get("chat", {}).get("agent_scope", {}) or {}).get("modes", [])] if isinstance(S.get("chat", {}).get("agent_scope"), dict) else []
s1, d1, _ = a.j("PUT", f"/api/v1/chat/sessions/{sc}/agent-scope", {"agentScope": "2d"})
_, m = say(sc, "生成一个骑士的3D模型", ai_model=TXT3D)
calls3d = [x for x in m if x["role"] == "assistant" and x.get("toolCalls") not in ("[]", "", None)]
r3d = m[-1]["content"][:90]
sc2 = new_session(); a.j("PUT", f"/api/v1/chat/sessions/{sc2}/agent-scope", {"agentScope": "3d"})
_, m2 = say(sc2, "帮我改图，把这张图改成红色", ai_model=I2I)
calls2d = [x for x in m2 if x["role"] == "assistant" and x.get("toolCalls") not in ("[]", "", None)]
rec("AGT-07", "PASS" if s1 == 200 and not calls3d and not calls2d and not pending(sc) and not pending(sc2) else "FAIL", f"scope GET {s} {json.dumps(sc0)[:60]}; PUT 2d -> {s1}; 3D request: tool calls={len(calls3d)} reply={r3d!r}; scope 3d + image edit: tool calls={len(calls2d)} reply={m2[-1]['content'][:90]!r}")

# ---- SEC-04 burst on the throwaway stack (own client; own limiter bucket is per-IP so run on a separate process)
stop(main)
sp, sd = start(PORTS[3], sub="sec04")
a4 = Api(f"http://127.0.0.1:{PORTS[3]}"); a4.mock_login()
codes = []; ra = None; body429 = None; rid429 = None
for i in range(1500):
    s, h, b, _ = a4.call("GET", "/api/v1/offerings?capability=text-to-image")
    codes.append(s)
    if s == 429: ra = h.get("Retry-After"); body429 = b[:160]; rid429 = h.get("X-Request-Id"); break
rec("SEC-04", "PASS" if 429 in codes and ra else "FAIL", f"{len(codes)} requests -> first 429 at #{len(codes)}, Retry-After={ra}, body={body429!r}, rid {rid429}")
stop(sp)

# ---- TSK-04 restart during a task (fresh stack on its own db)
rp, rd = start(PORTS[4], sub="tsk04"); ar = Api(f"http://127.0.0.1:{PORTS[4]}"); ar.mock_login()
a_backup, a = a, ar
tid, info = submit(T2I, "text-to-image", {"prompt": "restart me [mock:slow]"})
if tid:
    time.sleep(1); pre = a.j("GET", f"/api/v1/tasks/{tid}")[1]
    stop(rp); rp, rd = start(PORTS[4], sub="tsk04"); a = Api(f"http://127.0.0.1:{PORTS[4]}"); a.mock_login()
    t = wait(tid, 120); s, tl, _ = a.j("GET", "/api/v1/tasks"); n = len([x for x in tl.get("tasks", []) if x.get("id") == tid]); alln = len(tl.get("tasks", []))
    rec("TSK-04", "PASS" if t.get("state") in ("SUCCEEDED", "FAILED", "CANCELED") and alln == 1 else "FAIL", f"restarted while {pre.get('state')}; after restart final={t.get('state')} credits_charged={t.get('credits_charged')} tasks in account={alln} (no duplicate submission); Attempt count not exposed here")
else:
    rec("TSK-04", "FAIL", str(info))
stop(rp)

# ---- CNV-01
rec("CNV-01", "NOT-COVERED", "canvas auto-add + delete + reload is frontend store behaviour (scripts/test-canvas-recovery.mjs / Playwright B3-style); not reachable by API script")

p = sum(1 for _, s in res if s == "PASS"); f = sum(1 for _, s in res if s == "FAIL"); o = len(res) - p - f
print(f"=== mock_stack PASS={p} FAIL={f} OTHER={o} ===")
FINISHED = True
if not ARGS.keep and not f: shutil.rmtree(WORK, ignore_errors=True)
else: print(f"# work directory kept: {WORK}")
sys.exit(1 if f else 0)
