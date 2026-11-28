"""Shared helpers for the regression scripts in scripts/regression (docs/regression-matrix.md).

Standard library only. Nothing is read from the environment at import time, so `python3 -m py_compile`
and the offline refusal paths work without a target.

Environment (scripts/release-env.py smoke <env> injects all of it; there are no default addresses):
  BASE                                       target origin, e.g. what release-env.py resolves for the environment
  AGENT_TEST_USERNAME, AGENT_TEST_PASSWORD   the Agent test account of that environment
  ART_AI_UPSTREAM_SHAPE                      fake | mixed | live (set by release-env.py smoke)

Gates:
  require_fake()   fake-only scripts: refuse (exit 2) unless the target is in the fake upstream shape and
                   --live was not given. /api/version carries no upstream shape, so the shape comes from
                   ART_AI_UPSTREAM_SHAPE, the same rule as scripts/acceptance/release-1-9-*.mjs.
  The paid/live scripts use scripts/smoke/l5_common.execute, which needs both --live and ART_AI_LIVE_ACK=1.

The scripts never print passwords, cookies or signed URLs.
"""
import hashlib
import http.cookiejar
import json
import os
import struct
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zlib

T2I = "openai/gpt-image-2/text-to-image"
M3T = "meshy/meshy-7.1/text-to-3d"
TERMINAL = ("succeeded", "failed", "rejected", "denied", "canceled")
TASK_TERMINAL = ("SUCCEEDED", "FAILED", "CANCELED")


def log(*parts):
    print(*parts, flush=True)


# ---------------------------------------------------------------- gates

def missing_env(names=("BASE", "AGENT_TEST_USERNAME", "AGENT_TEST_PASSWORD"), environ=None):
    environ = os.environ if environ is None else environ
    return [name for name in names if not environ.get(name)]


def base_problem(base):
    parsed = urllib.parse.urlparse(base or "")
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.path not in ("", "/"):
        return "BASE must be an http(s) origin without a path"
    if parsed.query or parsed.fragment or parsed.username or parsed.password:
        return "BASE must not carry credentials, a query or a fragment"
    return None


def fake_refusal(argv=None, environ=None):
    """Return the refusal text for a fake-only script, or None when it may run."""
    argv = sys.argv[1:] if argv is None else argv
    environ = os.environ if environ is None else environ
    if "--live" in argv:
        return "refusing to run: this script is fake-only; --live is not accepted"
    shape = environ.get("ART_AI_UPSTREAM_SHAPE", "")
    if shape != "fake":
        return (f"refusing to run: the target upstream shape is {shape or 'unknown'!r}, not 'fake'; "
                "run through scripts/release-env.py smoke dev-test (fake release) so no real generation starts")
    missing = missing_env(environ=environ)
    if missing:
        return "missing environment: " + ", ".join(missing) + " (run through scripts/release-env.py smoke dev-test)"
    return base_problem(environ.get("BASE"))


def require_fake():
    problem = fake_refusal()
    if problem:
        print(problem, file=sys.stderr)
        sys.exit(2)


def use_agent_account_for_l5():
    """scripts/smoke/l5_common logs in with ADMIN_*; map the Agent test account onto it when present."""
    if os.environ.get("AGENT_TEST_USERNAME") and os.environ.get("AGENT_TEST_PASSWORD"):
        os.environ["ADMIN_USERNAME"] = os.environ["AGENT_TEST_USERNAME"]
        os.environ["ADMIN_PASSWORD"] = os.environ["AGENT_TEST_PASSWORD"]


# ---------------------------------------------------------------- report

class Report:
    def __init__(self, name):
        self.name = name
        self.passed = 0
        self.failed = 0

    def check(self, label, ok, detail=""):
        if ok:
            self.passed += 1
        else:
            self.failed += 1
        log(("PASS: " if ok else "FAIL: ") + label + (f" - {detail}" if detail else ""))
        return bool(ok)

    def finish(self):
        log(f"=== {self.name}: PASS={self.passed} FAIL={self.failed} ===")
        return 1 if self.failed or not self.passed else 0


# ---------------------------------------------------------------- HTTP

class Api:
    def __init__(self, base=None):
        self.base = (base or os.environ["BASE"]).rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def raw(self, method, path, body=None, headers=None, data=None):
        h = {"Origin": self.base}
        if headers:
            h.update(headers)
        if body is not None:
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        url = path if path.startswith("http") else self.base + path
        for attempt in range(8):
            request = urllib.request.Request(url, data=data, method=method, headers=h)
            try:
                with self.opener.open(request, timeout=90) as response:
                    return response.status, response.read()
            except urllib.error.HTTPError as error:
                if error.code == 429 and attempt < 7:
                    time.sleep(2 + attempt * 2)
                    continue
                return error.code, error.read()
        return 0, b""

    def json(self, method, path, body=None, headers=None):
        status, raw = self.raw(method, path, body, headers)
        try:
            return status, json.loads(raw or b"{}")
        except ValueError:
            return status, {"_raw": raw[:200].decode("utf8", "replace")}

    def login(self):
        status, _ = self.json("POST", "/api/auth/login", {"username": os.environ["AGENT_TEST_USERNAME"],
                                                          "password": os.environ["AGENT_TEST_PASSWORD"]})
        if status != 200:
            raise SystemExit(f"login failed: HTTP {status}")
        return self


# ---------------------------------------------------------------- sessions, approvals, tasks

def decision_body(api, decision):
    if not hasattr(api, "_decision"):
        _, schema = api.json("GET", "/api/v1/schema")
        schema = schema.get("schema", schema)
        api._decision = (((schema.get("chat") or {}).get("api") or {}).get("fields") or {}).get("decision")
    return {api._decision: decision} if api._decision else {}


def new_session(api, title=""):
    return api.json("POST", "/api/chat/sessions", {"title": title})[1]["id"]


def set_mode(api, sid, mode):
    """mode: auto (自动) | budget (审批) | manual (严格)."""
    return api.json("PUT", f"/api/v1/chat/sessions/{sid}/approval", {"approvalMode": mode})


def pending(api, sid):
    return api.json("GET", f"/api/v1/chat/sessions/{sid}/pending-approvals")[1].get("items") or []


def deny_all(api, sid):
    for card in pending(api, sid):
        api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "deny"))


def tasks(api, sid):
    out, cursor = [], ""
    for _ in range(10):
        _, body = api.json("GET", f"/api/v1/tasks?session_id={sid}&limit=100" + (f"&cursor={cursor}" if cursor else ""))
        out += body.get("tasks") or []
        cursor = body.get("next_cursor") or ""
        if not cursor:
            break
    return out


def wait_task(api, task_id, seconds=90):
    end, task = time.time() + seconds, {}
    while time.time() < end:
        status, task = api.json("GET", f"/api/v1/tasks/{task_id}")
        if status == 200 and task.get("state") in TASK_TERMINAL:
            return task
        time.sleep(0.5)
    return task


def wait_tasks_settled(api, sid, at_least, seconds=120):
    end = time.time() + seconds
    while time.time() < end:
        found = tasks(api, sid)
        if len(found) >= at_least and all(t["state"] in TASK_TERMINAL for t in found):
            return found
        time.sleep(1)
    return tasks(api, sid)


def offerings(api):
    if not hasattr(api, "_offerings"):
        _, body = api.json("GET", "/api/v1/offerings")
        api._offerings = {o["id"]: o for o in body.get("offerings") or []}
    return api._offerings


def payload_for(api, offering_id, base):
    declared = {p["name"]: p for p in offerings(api)[offering_id].get("parameters") or []}
    payload = dict(base)
    if "authorize_extra_credits" in declared:
        payload["authorize_extra_credits"] = True
    for name, parameter in declared.items():
        if name not in payload and parameter.get("required") and parameter.get("default") is not None:
            payload[name] = parameter["default"]
    return payload


def prepare_grant(api, sid, offering_id, capability, payload, key_prefix="rg"):
    """The panel/user path: prepare, then ask for a grant. Returns a dict with grant_status, rid, pid, key."""
    status, prep = api.json("POST", "/api/v1/tasks/prepare", {"session_id": sid, "capability": capability,
                                                               "offering_id": offering_id, "input": payload})
    if status not in (200, 201):
        return {"err": f"prepare {status} {str(prep)[:300]}"}
    pid = prep["prepared_input_id"]
    key = {"Idempotency-Key": f"{key_prefix}-{pid}"}
    status, grant = api.json("POST", "/api/v1/tasks/grants", {"prepared_input_id": pid}, headers=key)
    if status not in (200, 201):
        return {"err": f"grant {status} {str(grant)[:300]}"}
    return {"grant_status": grant.get("status"), "rid": grant.get("request_id"), "pid": pid, "key": key, "grant": grant}


def submit(api, step, wait=False):
    status, task = api.json("POST", "/api/v1/tasks", {"prepared_input_id": step["pid"],
                                                      "authorization_request_id": step["rid"]}, headers=step["key"])
    step["task_status"] = status
    step["task_id"] = task.get("task_id") or task.get("id")
    if wait and step["task_id"]:
        step["task"] = wait_task(api, step["task_id"])
    return step


def submit2d(api, sid, n, wait=False):
    """One user-initiated 2D step; submitted at once unless the grant is pending."""
    step = prepare_grant(api, sid, T2I, "text-to-image", {"prompt": f"a plain cube {n}"})
    if "err" not in step and step["grant_status"] != "pending":
        submit(api, step, wait)
    return step


def direct(api, sid, offering_id, capability, base, wait=True):
    step = prepare_grant(api, sid, offering_id, capability, payload_for(api, offering_id, base))
    if "err" not in step and step["grant_status"] != "pending":
        submit(api, step, wait)
    return step


def decide(api, step, decision="approve"):
    status, body = api.json("PUT", f"/api/v1/tasks/grants/{step['rid']}", decision_body(api, decision))
    step["decision_status"] = status
    if decision == "approve" and status == 200:
        submit(api, step)
    return step


def pre_grants(api, sid, n, tag="pre"):
    """n approved, unsubmitted 2D grants: they count towards the approval window like submitted steps."""
    for i in range(n):
        step = prepare_grant(api, sid, T2I, "text-to-image", {"prompt": f"{tag} {i}"}, key_prefix=f"rg-{tag}")
        if step.get("grant_status") != "approved":
            raise SystemExit(f"pre-grant {i} was not auto-approved: {step.get('grant_status') or step.get('err')}")
        if i % 10 == 9:
            time.sleep(0.2)


# ---------------------------------------------------------------- chat turns (fake chat model markers)

def agent2d(n):
    return '[[fake:tool:refine_image {"prompt":"a plain cube %d on white"}]]' % n


SOURCE_PICTURE = '[[fake:tool:refine_image {"prompt":"a plain standing character on a white background"}]]'


def three_view_marker(four=False):
    extra = ',"include_right":true' if four else ""
    return '[[fake:tool:create_three_view {"reference_handles":["@img1"],"subject_type":"humanoid"%s}]]' % extra


def replies(api, sid):
    _, body = api.json("GET", f"/api/chat/sessions/{sid}/messages")
    messages = body if isinstance(body, list) else (body.get("messages") or body.get("data") or [])
    done = [m for m in messages if m.get("role") == "assistant" and str(m.get("content") or "").strip()
            and not str(m.get("content") or "").startswith("__GENERATING__")]
    return done, messages


def last_assistant(api, sid):
    done, _ = replies(api, sid)
    return done[-1].get("content") if done else ""


def send(api, sid, content, **extra):
    body = {"content": content, "creative_domain": "image"}
    body.update(extra)
    status, answer = 0, {}
    for _ in range(60):
        status, answer = api.json("POST", f"/api/chat/sessions/{sid}/messages", body)
        if status != 409:
            break
        time.sleep(1)
    return status, answer


def turn(api, sid, content, seconds=240, **extra):
    before = len(replies(api, sid)[0])
    status, answer = send(api, sid, content, **extra)
    if status not in (200, 201, 202):
        return False, f"send {status} {str(answer)[:200]}"
    end = time.time() + seconds
    while time.time() < end:
        if len(replies(api, sid)[0]) > before:
            return True, ""
        time.sleep(0.5)
    return False, "turn not finished"


def wait_pending(api, sid, seconds=40):
    end = time.time() + seconds
    while time.time() < end:
        cards = pending(api, sid)
        if cards:
            return cards
        time.sleep(0.5)
    return []


# ---------------------------------------------------------------- three-view groups

def groups(api, sid):
    return api.json("GET", f"/api/v1/chat/sessions/{sid}/three-views")[1].get("items") or []


def slots_of(group):
    return {s["key"]: s for s in (group or {}).get("slots") or []}


def slot_states(group):
    return {key: slot.get("status") for key, slot in slots_of(group).items()}


def wait_group(api, sid, predicate, seconds=120):
    end, group = time.time() + seconds, None
    while time.time() < end:
        found = groups(api, sid)
        group = found[0] if found else None
        if group and predicate(group):
            return group
        time.sleep(0.3)
    return group


# ---------------------------------------------------------------- assets

def make_png(width=96, height=96, color=(200, 60, 60)):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    rows = b"".join(b"\x00" + b"".join(bytes(color if (x // 12 + y // 12) % 2 else (240, 240, 240))
                                       for x in range(width)) for y in range(height))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b""))


def upload(api, name, data, mime="image/png"):
    boundary = "----rg" + uuid.uuid4().hex
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{name}\"\r\n"
            f"Content-Type: {mime}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
    _, raw = api.raw("POST", "/api/v1/assets", data=body,
                     headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    try:
        return json.loads(raw).get("id")
    except ValueError:
        return None


def check_delivery(api, task):
    """Download every asset of a task; return (count, required roles, problems) on size and sha256."""
    problems, count, roles = [], 0, set()
    for asset in task.get("assets") or []:
        status, raw = api.raw("GET", asset["url"])
        count += 1
        if asset.get("required"):
            roles.add(asset.get("role"))
        if status != 200 or len(raw) != asset.get("size") or hashlib.sha256(raw).hexdigest() != asset.get("sha256"):
            problems.append(f"{asset.get('role')}/{asset.get('variant')}: HTTP {status}, {len(raw)} bytes")
    return count, sorted(roles), problems


def glb_counts(data):
    """textures / images / materials declared in a GLB's JSON chunk."""
    if data[:4] != b"glTF":
        return {"error": "not a glb"}
    length = struct.unpack("<I", data[12:16])[0]
    document = json.loads(data[20:20 + length])
    return {key: len(document.get(key) or []) for key in ("textures", "images", "materials")}
