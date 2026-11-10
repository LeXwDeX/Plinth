#!/usr/bin/env python3
"""Fake-upstream stack smoke (issue #96): zero cost, loopback only, offline.

One command starts the fake upstream (cmd/fake-upstream), then a Plinth stack in
live mode whose Tripo, Meshy, Hyper3D and LLM base URLs point at it, and drives the
same HTTP API the browser uses:

  * the Tripo, Meshy and Hyper3D balances show up in the provider balance panel;
  * Meshy text-to-3d (preview + refine) -> remesh -> rigging -> animation, and
    text-to-3d -> retexture / convert, and Hyper3D Rodin text-to-3d -> Bang and
    Rodin image-to-3d with five pictures, run to SUCCEEDED with the same file
    checks as the Tripo chains;
  * Tripo text-to-3d -> rig-check -> rigging -> animation, and
    segmentation -> part completion, run to SUCCEEDED with every required output
    slot present, every file's size and SHA-256 matching, and authenticated
    delivery (no cookie, no bytes);
  * the Agent turn (fake OpenAI-compatible LLM -> tool call -> approval -> 3D
    task -> final reply) completes, the L1 Agent equivalent; the Hyper3D Agent
    chain (create_model_3d on Rodin -> process_model Bang rodin-task ->
    retexture_model Texture local-model with a picture attached to the chat) is
    approved step by step and delivers every step's files;
  * a batched Agent turn (a generation step and the three-view SkillRun in one
    completion) stops waiting, keeps both Tasks running, ends with the Schema
    note and finishes the three-view group in the background;
  * Hyper3D texture (a local model + a reference picture), the OpenAI, Gemini and Jimeng
    image Offerings (text-to-image and image-to-image) and the matting service run to
    SUCCEEDED with the same delivery checks;
  * the Tripo picture and model-upload endpoints (image-to-3D, splat, multiview and its
    edit, Smart Segmentation of a picture and of a model) and the triangle decimation run
    to SUCCEEDED too (the "tripo" group);
  * the three Tripo P2 low-poly endpoints expose the official P Series quote (100
    credits, standard texture +10), require the hidden extra-credit consent, and run
    text/image/multiview to SUCCEEDED with delivery checks (the "p2" group);
  * each fault scenario, selected by a fake test API key, ends the public Task of
    every provider in the expected state and error class; artifact-expired (the
    first signed URLs have expired) heals on the next read of the job, and
    artifact-expired-stuck (every URL issued before a renewal has expired) fails the
    Task until the fake renews its URLs; then the free result refetch of Tripo,
    Meshy and Hyper3D delivers it.

Usage: python3 scripts/smoke/smoke-fake-stack.py [--only NAME[,NAME]] [--keep] [--work DIR]
(NAME: balance, chain, meshy, hyper3d, hyper3d-agent, images, tripo, p2, agent, agent-background,
agent-quality, image-self-check, work-ledger, faults;
"agent" runs both agent scenarios, "hyper3d-agent" the Hyper3D chain alone,
"p2" the Tripo P2 quote/chain, "agent-background" the batched wait that keeps both Tasks running,
"work-ledger" the #248 frozen prompts, compaction, style anchor and self-check leftovers)
Needs `go`. Exit 0 = every selected check passed.

The stack runs inside the internal/app test binary (TestFakeUpstreamStack, built with -tags fakestack), not
cmd/art-ai: Tripo's native processing only reads source models from public HTTPS
hosts, and the test binary maps one reserved ".test" host to the loopback fake
instead of loosening that rule. Nothing here touches dev-test, formal-test or a
real upstream. Ports stay away from 18631-18635 (the #62 local stacks).
"""
import argparse
import hashlib
import http.cookiejar
import json
import os
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import zlib

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
FAKE_PORT = int(os.environ.get("FAKE_STACK_FAKE_PORT", "19410"))
STACK_PORT = int(os.environ.get("FAKE_STACK_PORT", "19411"))
ASSET_HOST = "fake-assets.example.test"  # keep in sync with internal/app/fake_upstream_stack_test.go
GOOD_KEY = "fake-test-key"

TEXT_TO_3D = "tripo/tripo-v3.1-full/text-to-3d"
MESHY_TEXT_TO_3D = "meshy/meshy-7.1/text-to-3d"
MESHY_REMESH = "meshy/processing/remesh"
MESHY_RETEXTURE = "meshy/processing/retexture"
MESHY_CONVERT = "meshy/processing/convert"
MESHY_RIGGING = "meshy/processing/rigging"
MESHY_ANIMATION = "meshy/processing/animation"
HYPER3D_TEXT_TO_3D = "hyper3d/rodin-gen-2.5/text-to-3d"
HYPER3D_IMAGE_TO_3D = "hyper3d/rodin-gen-2.5/image-to-3d"
HYPER3D_BANG = "hyper3d/bang/rodin-task"
HYPER3D_TEXTURE = "hyper3d/texture/local-model"
# provider -> (text-to-image, image-to-image); the matting service has one Offering.
IMAGE_OFFERINGS = {
    # Meshy's Nano Banana images use Meshy's own text-to-image / image-to-image routes (the Meshy fake).
    "meshy": ("meshy/nano-banana-pro/text-to-image", "meshy/nano-banana-pro/image-to-image"),
    "openai": ("openai/gpt-image-2/text-to-image", "openai/gpt-image-2/image-to-image"),
    "gemini": ("gemini/gemini-3-pro-image/text-to-image", "gemini/gemini-3-pro-image/image-to-image"),
    "jimeng": ("jimeng/seedream-4-5/text-to-image", "jimeng/seedream-4-5/image-to-image"),
}
MATTING = "matting/default/background-removal"
PROVIDER_HOSTS = ("tripo", "meshy", "hyper3d")
IMAGE_PROVIDERS = ("jimeng", "matting")  # connections the fake serves artifacts to (URLs or batches)
RIG_CHECK = "tripo/processing/rig-check"
RIGGING = "tripo/processing/rigging-v3"
ANIMATION = "tripo/processing/animation"
TRIPO_TEXTURE = "tripo/processing/texture"
TRIPO_IMAGE_TO_3D = "tripo/tripo-v3.1-full/image-to-3d"
TRIPO_MULTIVIEW_TO_3D = "tripo/tripo-v3.1-multiview/image-to-3d"
TRIPO_P2_TEXT_TO_3D = "tripo/tripo-p2/text-to-3d"
TRIPO_P2_IMAGE_TO_3D = "tripo/tripo-p2/image-to-3d"
TRIPO_P2_MULTIVIEW_TO_3D = "tripo/tripo-p2-multiview/image-to-3d"
TRIPO_SPLAT = "tripo/image-to-splat"
TRIPO_MULTIVIEW = "tripo/image-to-multiview"
TRIPO_EDIT_MULTIVIEW = "tripo/edit-multiview"
TRIPO_DECIMATE = "tripo/geometry/decimate-triangle"
TRIPO_SMARTSEGMENT_IMAGE = "tripo/geometry/smartsegment-image"
TRIPO_SMARTSEGMENT_MODEL = "tripo/geometry/smartsegment-model"
SEGMENT = "tripo/geometry/segment"
COMPLETE = "tripo/geometry/complete"

results = []


def record(name, ok, detail):
    results.append((name, ok))
    print(f"{'PASS' if ok else 'FAIL'}: {name} | {detail}", flush=True)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Api:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, method, path, body=None, headers=None, cookies=True):
        data = None
        merged = {"Origin": self.base, **(headers or {})}
        if body is not None:
            data = json.dumps(body).encode()
            merged["Content-Type"] = "application/json"
        request = urllib.request.Request(self.base + path, data=data, method=method, headers=merged)
        opener = self.opener if cookies else urllib.request.build_opener(NoRedirect())
        try:
            response = opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        except Exception as error:  # connection problems are test failures, not crashes
            return 0, b"", {}
        raw = response.read()
        return getattr(response, "status", None) or response.code, raw, dict(response.headers)

    def upload(self, path, field, filename, data, mime):
        """POST one file as multipart/form-data (the library upload); returns (status, json)."""
        boundary = "----fakestack" + hashlib.sha256(data).hexdigest()[:16]
        body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"{field}\"; filename=\"{filename}\"\r\n"
                f"Content-Type: {mime}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(self.base + path, data=body, method="POST", headers={
            "Origin": self.base, "Content-Type": f"multipart/form-data; boundary={boundary}"})
        try:
            response = self.opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        raw = response.read()
        try:
            return getattr(response, "status", None) or response.code, json.loads(raw)
        except ValueError:
            return getattr(response, "status", None) or response.code, {}

    def json(self, *args, **kwargs):
        status, raw, _headers = self.call(*args, **kwargs)
        try:
            return status, json.loads(raw)
        except ValueError:
            return status, {}

    def login(self):
        return self.json("POST", "/api/auth/mock", {})


class Processes:
    """Owns the processes this script started and nothing else."""

    def __init__(self):
        self.children = []

    def start(self, argv, env, log):
        handle = open(log, "ab")
        child = subprocess.Popen(argv, env=env, cwd=REPO, stdout=handle, stderr=subprocess.STDOUT, start_new_session=True)
        self.children.append(child)
        return child

    @staticmethod
    def stop(child):
        if child.poll() is None:
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                return
            try:
                child.wait(10)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)

    def stop_all(self):
        for child in self.children:
            self.stop(child)


def port_busy(port):
    try:
        socket.create_connection(("127.0.0.1", port), timeout=1).close()
        return True
    except OSError:
        return False


def require_free(port, what):
    """Refuse a port something else already listens on: a health answer from a
    foreign process must never be taken for our own instance."""
    if port_busy(port):
        raise RuntimeError(f"port {port} for the {what} is already in use; stop that process or set FAKE_STACK_FAKE_PORT / FAKE_STACK_PORT")


def wait_http(url, child, seconds=30):
    """Healthy only while the child we started is still alive."""
    end = time.time() + seconds
    while time.time() < end:
        if child.poll() is not None:
            return False
        try:
            if urllib.request.urlopen(url, timeout=1).status == 200 and child.poll() is None:
                return True
        except Exception:
            time.sleep(0.25)
    return False


def wait_port(port, child, seconds=15):
    end = time.time() + seconds
    while time.time() < end:
        if child.poll() is not None:
            return False
        if port_busy(port):
            return child.poll() is None
        time.sleep(0.2)
    return False


def build(work):
    fake = os.path.join(work, "fake-upstream")
    stack = os.path.join(work, "stack.test")
    subprocess.check_call(["go", "build", "-o", fake, "./cmd/fake-upstream"], cwd=REPO)
    subprocess.check_call(["go", "test", "-c", "-tags", "fakestack", "-o", stack, "./internal/app"], cwd=REPO)
    return fake, stack


def private_catalog(work, jimeng_urls=False):
    """The default Catalog with four changes for Tripo, Meshy and Hyper3D: the fake
    artifact host is an allowed asset host, they poll once a second, and a failing
    materialization is retried once. The matting service polls once a second too,
    and the Jimeng Offerings accept the fake artifact host. With jimeng_urls the
    Jimeng Offerings also ask for a signed picture URL instead of an inline picture
    (the Schema fixes response_format to b64_json, so a URL, and with it a
    missing, corrupt or expired URL, never occurs in production); the fault runs
    use that catalog. The background-wait threshold drops to one second so a test
    Task reaches it while the fake still polls; the shipped value stays untouched.
    The default Catalog file and its base URLs stay untouched."""
    with open(os.path.join(REPO, "internal/catalog/default_catalog.json"), encoding="utf-8") as handle:
        catalog = json.load(handle)
    catalog["application"]["chat"]["agent"]["background_wait"]["after_seconds"] = 1
    for offering in catalog["offerings"]:
        provider = offering.get("provider_id")
        if provider in PROVIDER_HOSTS:
            offering["asset_hosts"] = sorted(set(offering.get("asset_hosts", [])) | {ASSET_HOST})
            offering["poll_interval_seconds"] = 1
            # Fault runs must end in minutes, not in the production retry budget.
            offering["materialize_attempts"] = 2
        elif provider == "matting":
            offering["poll_interval_seconds"] = 1
        elif provider == "jimeng":
            offering["asset_hosts"] = sorted(set(offering.get("asset_hosts", [])) | {ASSET_HOST})
            offering["materialize_attempts"] = 2
            for parameter in offering.get("parameters", []):
                if jimeng_urls and parameter.get("name") == "response_format":
                    parameter["options"] = list(parameter.get("options", [])) + [{"value": "url", "label": "url"}]
                    parameter["default"] = "url"
                    parameter.pop("forced_when", None)
                    parameter.pop("fixed", None)
    path = os.path.join(work, "catalog-urls.json" if jimeng_urls else "catalog.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(catalog, handle, ensure_ascii=False)
    return path


def web_assets_root(work):
    """The static shell the stack serves. The API under test does not depend on it, so a
    checkout without a frontend build (a worktree, or the CI job that does not build the
    frontend) falls back to the common checkout's build and then to a minimal shell."""
    web = os.environ.get("WEB_ASSETS_ROOT") or os.path.join(REPO, "build", "go-web")
    if os.path.isfile(os.path.join(web, "index.html")):
        return web
    common = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=REPO,
                            capture_output=True, text=True).stdout.strip()
    if common:
        shared = os.path.join(os.path.dirname(common), "build", "go-web")
        if os.path.isfile(os.path.join(shared, "index.html")):
            return shared
    shell = os.path.join(work, "web-shell")
    os.makedirs(shell, exist_ok=True)
    with open(os.path.join(shell, "index.html"), "w", encoding="utf-8") as handle:
        handle.write("<!doctype html><meta charset=\"utf-8\"><title>fake stack</title>\n")
    return shell


def stack_env(work, name, catalog, key):
    root = os.path.join(work, name)
    for sub in ("storage", "logs"):
        os.makedirs(os.path.join(root, sub), exist_ok=True)
    web = web_assets_root(work)
    env = {
        "HOME": os.environ.get("HOME", ""), "PATH": os.environ["PATH"],
        "APP_ENV": "development", "PROVIDER_MODE": "live", "AGENT_MODE": "live", "AUTH_MODE": "development",
        "DEV_MOCK_AUTH": "true", "DEV_MOCK_AUTH_BYPASS_USER_STATE": "true",
        "DEV_MOCK_USER": '{"id":"fake_stack","name":"fake stack","email":"fake@example.test"}',
        "APP_ADDR": f"127.0.0.1:{STACK_PORT}", "DATABASE_PATH": os.path.join(root, "app.db"),
        "STORAGE_ROOT": os.path.join(root, "storage"), "LOG_DIR": os.path.join(root, "logs"),
        "WEB_ASSETS_ROOT": web, "CATALOG_FILE": catalog,
        "TRIPO_API_KEY": key, "TRIPO_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/tripo/v3",
        # Meshy's adapter appends openapi/v1 or openapi/v2, so its base stops at /meshy.
        "MESHY_API_KEY": key, "MESHY_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/meshy",
        "HYPER3D_API_KEY": key, "HYPER3D_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/hyper3d/api/v2",
        # Meshy processing needs both (the Catalog's conditional capabilities).
        "PUBLIC_BASE_URL": f"http://127.0.0.1:{STACK_PORT}", "ASSET_SIGNING_KEY": "fake-stack-asset-signing-key-0123456789",
        "LLM_API_KEY": key, "LLM_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/openai/v1", "LLM_MODEL": "deepseek",
        # The image providers and the matting service (phase 3). Jimeng's adapter appends
        # api/v3 and the matting adapter api/v1 to their bases.
        "OPENAI_API_KEY": key, "OPENAI_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/openai/images/v1",
        "GEMINI_API_KEY": key, "GEMINI_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/gemini/images/v1",
        "JIMENG_API_KEY": key, "JIMENG_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/jimeng",
        "MATTING_API_KEY": key, "MATTING_BASE_URL": f"http://127.0.0.1:{FAKE_PORT}/matting",
        "FAKE_UPSTREAM_STACK_URL": f"http://127.0.0.1:{FAKE_PORT}",
    }
    return env, root


def start_stack(processes, stack_binary, work, name, catalog, key):
    env, root = stack_env(work, name, catalog, key)
    require_free(STACK_PORT, "stack")
    child = processes.start([stack_binary, "-test.run", "^TestFakeUpstreamStack$", "-test.timeout", "0"], env,
                            os.path.join(root, "stack.log"))
    if not wait_http(f"http://127.0.0.1:{STACK_PORT}/healthz", child):
        tail = ""
        try:
            with open(os.path.join(root, "stack.log"), encoding="utf-8", errors="replace") as handle:
                tail = "".join(handle.readlines()[-20:])
        except OSError:
            pass
        raise RuntimeError(f"stack {name} did not become healthy (exited={child.poll() is not None}); "
                           f"see {root}/stack.log; last lines:\n{tail}")
    api = Api(f"http://127.0.0.1:{STACK_PORT}")
    api.login()
    return child, api


# ---- API helpers ----

def new_session(api):
    return api.json("POST", "/api/chat/sessions", {"title": ""})[1]["id"]


def decision_body(api, decision):
    """The approval decision body with the public Schema's member (chat.api.fields.decision, #120),
    read once like the browser reads it. Without the declaration the body is empty, so the server
    refuses the decision and the step reports it instead of guessing a member name."""
    if not hasattr(api, "decision_member"):
        status, schema = api.json("GET", "/api/v1/schema")
        schema = schema.get("schema", schema) if status == 200 and isinstance(schema, dict) else {}
        api.decision_member = (((schema.get("chat") or {}).get("api") or {}).get("fields") or {}).get("decision")
    return {api.decision_member: decision} if api.decision_member else {}


def chat_schema_facts(api):
    """The public Schema's chat field names and the turn-background route, read once
    like the browser reads them. Without them the background choice is not offered."""
    if not hasattr(api, "chat_schema_facts"):
        status, schema = api.json("GET", "/api/v1/schema")
        schema = schema.get("schema", schema) if status == 200 and isinstance(schema, dict) else {}
        fields = ((schema.get("chat") or {}).get("api") or {}).get("fields") or {}
        routes = ((schema.get("runtime") or {}).get("http") or {}).get("legacy_routes") or {}
        api.chat_schema_facts = (fields, routes.get("chat_turn_background") or "")
    return api.chat_schema_facts


def submit(api, offering, capability, payload, session, style_lock_skipped=False):
    """prepare -> grant (-> human approval) -> submit. Returns (task_id, why).
    style_lock_skipped is the direct prepare's one-turn exemption (#187)."""
    body = {"session_id": session, "capability": capability, "offering_id": offering, "input": payload}
    if style_lock_skipped:
        body["style_lock_skipped"] = True
    status, prepared = api.json("POST", "/api/v1/tasks/prepare", body)
    if status not in (200, 201):
        return None, f"prepare {status} {prepared}"
    pid = prepared["prepared_input_id"]
    key = {"Idempotency-Key": "fake-stack-" + pid}
    status, grant = api.json("POST", "/api/v1/tasks/grants", {"prepared_input_id": pid}, headers=key)
    if status not in (200, 201):
        return None, f"grant {status} {grant}"
    request_id = grant.get("request_id")
    if grant.get("status") == "pending":
        status, decided = api.json("PUT", f"/api/v1/tasks/grants/{request_id}", decision_body(api, "approve"))
        if status != 200:
            return None, f"approve {status} {decided}"
    status, task = api.json("POST", "/api/v1/tasks", {"prepared_input_id": pid, "authorization_request_id": request_id}, headers=key)
    if status not in (200, 201, 202):
        return None, f"submit {status} {task}"
    return task.get("task_id") or task.get("id"), ""


def wait_task(api, task_id, seconds=90):
    end = time.time() + seconds
    task = {}
    while time.time() < end:
        status, task = api.json("GET", f"/api/v1/tasks/{task_id}")
        if status == 200 and task.get("state") in ("SUCCEEDED", "FAILED", "CANCELED"):
            return task
        time.sleep(0.5)
    return task


def check_delivery(api, task, offering, submitted=None):
    """Required slots present; every asset's bytes match its declared size and
    hash and arrive with the sandboxed delivery headers. An unknown asset id is
    a 404. (The development mock login makes every request the one mock user,
    so cross-user denial is not observable here; internal/transport/httpapi
    tests cover it.)"""
    problems = []
    # A slot with a minimum of 0 is delivered when the upstream offers it; a slot
    # without a variant is met by any asset of its role.
    delivered = {(a.get("role"), a.get("variant")) for a in task.get("assets") or []}
    for output in offering["outputs"]:
        if output.get("delivery") != "required" or output.get("minimum", 1) == 0:
            continue
        # A conditional slot (the views of an edited multiview) is due when its condition holds for the
        # submitted input. Only the "present" operator is evaluated; any other counts as due.
        condition = output.get("required_when")
        if condition and condition.get("operator") == "present" and not (submitted or {}).get(condition.get("parameter")):
            continue
        slot = (output["role"], output.get("variant"))
        if not any(role == slot[0] and (slot[1] is None or variant == slot[1]) for role, variant in delivered):
            problems.append(f"missing required slot {slot}")
    for asset in task.get("assets") or []:
        status, raw, headers = api.call("GET", asset["url"])
        label = f"{asset.get('role')}/{asset.get('variant')}"
        if status != 200 or len(raw) != asset["size"] or hashlib.sha256(raw).hexdigest() != asset["sha256"]:
            problems.append(f"bytes of {label}: status {status}, {len(raw)} bytes")
        lowered = {name.lower(): value for name, value in headers.items()}
        if lowered.get("x-content-type-options") != "nosniff" or "sandbox" not in lowered.get("content-security-policy", ""):
            problems.append(f"{label} lacks the sandboxed delivery headers")
    if task.get("assets"):
        status, _, _ = api.call("GET", task["assets"][0]["url"].rsplit("/", 1)[0] + "/asset_does_not_exist")
        if status != 404:
            problems.append(f"unknown asset answered {status}")
    return problems, len(task.get("assets") or [])


def run_step(api, offerings, label, offering, capability, payload, session, expect_state="SUCCEEDED", style_lock_skipped=False):
    task_id, why = submit(api, offering, capability, payload, session, style_lock_skipped=style_lock_skipped)
    if not task_id:
        record(label, False, why)
        return None
    task = wait_task(api, task_id)
    if task.get("state") != expect_state:
        record(label, False, f"state {task.get('state')} failure {json.dumps(task.get('failure'), ensure_ascii=False)}")
        return task
    problems, count = check_delivery(api, task, offerings[offering], payload)
    record(label, not problems, f"{task['state']}, {count} assets verified (size, SHA-256, sandboxed delivery headers)" if not problems else "; ".join(problems))
    return task


# ---- scenarios ----

def make_png(size=64):
    """A deterministic solid PNG (no third-party imaging library): the picture the
    image-to-image, texture and matting steps upload as their input."""
    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)
    rows = b"".join(b"\x00" + bytes((200, 60, 60)) * size for _ in range(size))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b""))


def make_picture(width, height, background, subjects):
    """A deterministic RGB PNG: a plain or busy background with solid rectangles."""
    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            colour = background(x, y) if callable(background) else background
            for (x0, y0, x1, y1), fill in subjects:
                if x0 <= x < x1 and y0 <= y < y1:
                    colour = fill
                    break
            row.extend(colour)
        rows.append(b"\x00" + bytes(row))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(b"".join(rows))) + chunk(b"IEND", b""))


def upload_asset(api, filename, data, mime):
    """Library upload (POST /api/v1/assets); returns the asset id or None."""
    status, body = api.upload("/api/v1/assets", "file", filename, data, mime)
    return body.get("id") if status in (200, 201) else None


def payload_for(offering, base):
    """The request payload: the given fields, the extra-credit authorization when
    the Offering declares it, and the declared default of every required
    parameter left out (a multi-format Offering needs its formats spelled out)."""
    declared = {p["name"]: p for p in offering.get("parameters") or []}
    payload = dict(base)
    if "authorize_extra_credits" in declared:
        payload["authorize_extra_credits"] = True
    for name, parameter in declared.items():
        if name not in payload and parameter.get("required") and parameter.get("default") is not None:
            payload[name] = parameter["default"]
    return payload


def scenario_balance(api, offerings):
    status, body = api.json("GET", "/api/meshy/balance")
    accounts = {a.get("provider"): (a.get("native_balance") or {}).get("amount") for a in body.get("accounts", [])}
    want = {"tripo": "5000", "meshy": "1000", "hyper3d": "100"}
    got = {name: accounts.get(name) for name in want}
    record("balance panel shows the Tripo, Meshy and Hyper3D balances", status == 200 and got == want,
           f"status {status}, native balances {got} (want {want})")


def scenario_chain(api, offerings):
    session = new_session(api)
    gen = run_step(api, offerings, "tripo text-to-3d", TEXT_TO_3D, "text-to-3d",
                   {"prompt": "a plain cube", "authorize_extra_credits": True}, session)
    if not gen or gen.get("state") != "SUCCEEDED":
        return
    run_step(api, offerings, "tripo rig-check", RIG_CHECK, "rig-check", {"source_task": gen["id"]}, session)
    rig = run_step(api, offerings, "tripo rigging", RIGGING, "rigging",
                   {"source_task": gen["id"], "authorize_extra_credits": True}, session)
    if rig and rig.get("state") == "SUCCEEDED":
        run_step(api, offerings, "tripo animation", ANIMATION, "animation", {"source_task": rig["id"]}, session)
    run_step(api, offerings, "tripo texture", TRIPO_TEXTURE, "retexture",
             {"source_task": gen["id"], "prompt": "plain matte grey", "authorize_extra_credits": True}, session)
    seg = run_step(api, offerings, "tripo segment", SEGMENT, "mesh-segmentation",
                   {"source_task": gen["id"], "authorize_extra_credits": True}, session)
    if seg and seg.get("state") == "SUCCEEDED":
        run_step(api, offerings, "tripo complete (after segment)", COMPLETE, "mesh-completion",
                 {"source_task": seg["id"], "authorize_extra_credits": True}, session)


def scenario_tripo_images(api, offerings):
    """The Tripo endpoints that take a picture or a model upload (image-to-3D, splat,
    multiview and its edit, Smart Segmentation of a picture and of a model) and the
    triangle decimation: every one runs to SUCCEEDED with its files delivered."""
    session = new_session(api)
    picture = upload_asset(api, "reference.png", make_png(), "image/png")
    if not picture:
        record("tripo images: upload the reference picture", False, "the library upload was refused")
        return
    gen = step(api, offerings, "tripo image-to-3d", TRIPO_IMAGE_TO_3D, "image-to-3d", {"reference_assets": [picture]}, session)
    # Multiview-to-3D sends the ordered pictures as view-key `inputs` (front, left by default).
    step(api, offerings, "tripo multiview-to-3d", TRIPO_MULTIVIEW_TO_3D, "image-to-3d",
         {"reference_assets": [picture, picture], "reference_views": ["front", "back"]}, session)
    step(api, offerings, "tripo image-to-splat", TRIPO_SPLAT, "image-to-splat", {"image": picture}, session)
    views = step(api, offerings, "tripo image-to-multiview", TRIPO_MULTIVIEW, "image-to-multiview", {"image": picture}, session)
    if views and views.get("state") == "SUCCEEDED":
        step(api, offerings, "tripo edit-multiview", TRIPO_EDIT_MULTIVIEW, "edit-multiview",
             {"source_task": views["id"], "front_prompt": "make the object green"}, session)
        # Other views than the front: the result carries exactly the views asked for.
        step(api, offerings, "tripo edit-multiview (left and back)", TRIPO_EDIT_MULTIVIEW, "edit-multiview",
             {"source_task": views["id"], "left_prompt": "make it red", "back_prompt": "make it blue"}, session)
    step(api, offerings, "tripo smartsegment (picture)", TRIPO_SMARTSEGMENT_IMAGE, "image-to-3d", {"image": picture}, session)
    if gen and gen.get("state") == "SUCCEEDED":
        step(api, offerings, "tripo decimate (triangle)", TRIPO_DECIMATE, "remesh", {"source_task": gen["id"]}, session)
        # Smart Segmentation of a model takes a library model file. Tripo's own image-to-3D delivers
        # glTF text by default, so the binary GLB comes from a Meshy text-to-3D and is uploaded.
        source = step(api, offerings, "tripo smartsegment (model): source GLB from meshy text-to-3d", MESHY_TEXT_TO_3D, "text-to-3d",
                      {"prompt": "a plain cube"}, session)
        model = next((a for a in (source or {}).get("assets") or [] if a.get("role") == "model" and a.get("variant") == "glb"), None)
        status, raw, _headers = api.call("GET", model["url"]) if model else (0, b"", {})
        upload_status, uploaded = api.upload("/api/v1/assets", "file", "model.glb", raw, "model/gltf-binary") if status == 200 else (0, {})
        model_id = uploaded.get("id") if upload_status in (200, 201) else None
        if model_id:
            step(api, offerings, "tripo smartsegment (model)", TRIPO_SMARTSEGMENT_MODEL, "mesh-segmentation", {"source_asset": model_id}, session)
        else:
            record("tripo smartsegment (model)", False,
                   f"could not prepare the model upload (download {status}, upload {upload_status} {json.dumps(uploaded, ensure_ascii=False)[:200]}, {len(raw)} bytes starting {raw[:4]!r})")


def p2_prepare(api, session, offering, capability, payload):
    return api.json("POST", "/api/v1/tasks/prepare", {
        "session_id": session, "capability": capability, "offering_id": offering, "input": payload})


def scenario_p2(api, offerings):
    """Tripo P2: the official P Series price is public (base 100 credits, standard
    texture +10); prepare quotes 110 for a textured model, the hidden consent is
    required above the base price, the Agent path freezes 100/110 for the
    untextured and fast variants, and the text, image and multiview endpoints run
    to SUCCEEDED with the same delivery checks as the rest."""
    ids = (TRIPO_P2_TEXT_TO_3D, TRIPO_P2_IMAGE_TO_3D, TRIPO_P2_MULTIVIEW_TO_3D)
    missing = [name for name in ids if name not in offerings]
    if missing:
        record("tripo P2: Offerings listed", False, f"missing {missing}")
        return
    public = []
    for name in ids:
        offering = offerings[name]
        quote = offering.get("default_native_quote") or {}
        ok = (offering.get("price_mode") == "native" and quote.get("unit") == "tripo.credit"
              and quote.get("total_amount") == "110" and "estimated_credits" not in offering
              and "native_pricing" not in offering)
        public.append(ok)
        record(f"tripo P2: {name} public quote", ok,
               f"price_mode {offering.get('price_mode')}, unit {quote.get('unit')}, total {quote.get('total_amount')}, "
               f"estimated_credits {'present' if 'estimated_credits' in offering else 'absent'}")
    if not all(public):
        return
    session = new_session(api)
    picture = upload_asset(api, "p2-reference.png", make_png(), "image/png")
    if not picture:
        record("tripo P2: upload the reference picture", False, "the library upload was refused")
        return
    cases = {
        TRIPO_P2_TEXT_TO_3D: ("text-to-3d", {"prompt": "a game-ready wooden crate"}),
        TRIPO_P2_IMAGE_TO_3D: ("image-to-3d", {"reference_assets": [picture]}),
        TRIPO_P2_MULTIVIEW_TO_3D: ("image-to-3d",
                                   {"reference_assets": [picture, picture], "reference_views": ["front", "back"]}),
    }
    for name, (capability, base) in cases.items():
        short = name.split("/")[1]
        payload = payload_for(offerings[name], base)
        status, prepared = p2_prepare(api, session, name, capability, payload)
        quote = prepared.get("native_quote") or {}
        record(f"tripo P2 {short}: prepare with consent quotes 110", status == 201 and quote.get("total_amount") == "110",
               f"status {status}, native_quote {quote}")
        refused = dict(payload)
        refused.pop("authorize_extra_credits", None)
        status, body = p2_prepare(api, session, name, capability, refused)
        record(f"tripo P2 {short}: prepare without consent is refused", status == 400,
               f"status {status} {json.dumps(body, ensure_ascii=False)[:200]}")
        standard = dict(payload, texture_quality="standard")
        status, prepared = p2_prepare(api, session, name, capability, standard)
        quote = prepared.get("native_quote") or {}
        record(f"tripo P2 {short}: standard texture quotes 110", status == 201 and quote.get("total_amount") == "110",
               f"status {status}, native_quote {quote}")
    # The 100 (texture=false) and fast-tier quotes cannot be reached through the
    # public prepare body: changing an advised parameter there needs a reason, and
    # the public body carries none. TestTripoP2NativeQuoteUsesOnlyTheOfficiallyListedPrices
    # covers that matrix at the Catalog layer.
    for name, (capability, base) in cases.items():
        short = name.split("/")[1]
        task = run_step(api, offerings, f"tripo P2 {short}: submit and deliver", name, capability,
                        payload_for(offerings[name], base), session)
        if task and task.get("state") == "SUCCEEDED":
            quote = task.get("native_quote") or {}
            record(f"tripo P2 {short}: task keeps the frozen 110 quote",
                   quote.get("unit") == "tripo.credit" and quote.get("total_amount") == "110",
                   f"native_quote {quote}")


def step(api, offerings, label, offering, capability, base, session, style_lock_skipped=False):
    return run_step(api, offerings, label, offering, capability, payload_for(offerings[offering], base), session,
                    style_lock_skipped=style_lock_skipped)




def scenario_meshy(api, offerings):
    """Meshy: text-to-3d runs preview and refine inside one Task; the processing
    steps take it as their source (input_task_id), as the Plinth chain does."""
    session = new_session(api)
    gen = step(api, offerings, "meshy text-to-3d (preview + refine)", MESHY_TEXT_TO_3D, "text-to-3d",
               {"prompt": "a plain humanoid robot"}, session)
    if not gen or gen.get("state") != "SUCCEEDED":
        return
    remesh = step(api, offerings, "meshy remesh", MESHY_REMESH, "remesh",
                  {"source_task": gen["id"], "topology": "triangle", "target_polycount": 10000}, session)
    if remesh and remesh.get("state") == "SUCCEEDED":
        rig = step(api, offerings, "meshy rigging", MESHY_RIGGING, "rigging", {"source_task": remesh["id"]}, session)
        if rig and rig.get("state") == "SUCCEEDED":
            step(api, offerings, "meshy animation", MESHY_ANIMATION, "animation",
                 {"source_task": rig["id"], "action_id": 0}, session)
    step(api, offerings, "meshy retexture", MESHY_RETEXTURE, "retexture",
         {"source_task": gen["id"], "prompt": "plain matte grey"}, session)
    step(api, offerings, "meshy convert", MESHY_CONVERT, "format-conversion", {"source_task": gen["id"]}, session)


def scenario_hyper3d(api, offerings):
    session = new_session(api)
    gen = step(api, offerings, "hyper3d rodin text-to-3d", HYPER3D_TEXT_TO_3D, "text-to-3d",
               {"prompt": "a plain cube"}, session)
    # Rodin documents 1-5 reference images; the fake enforces that bound (#191), so
    # five images exercise the widest multipart body the Offering accepts.
    pictures = [upload_asset(api, f"view-{index}.png", make_png(32 + index), "image/png") for index in range(5)]
    if all(pictures):
        step(api, offerings, "hyper3d rodin image-to-3d (5 images)", HYPER3D_IMAGE_TO_3D, "image-to-3d",
             {"reference_assets": pictures}, session)
    else:
        record("hyper3d rodin image-to-3d (5 images)", False, "the library refused one of the five pictures")
    if gen and gen.get("state") == "SUCCEEDED":
        step(api, offerings, "hyper3d bang", HYPER3D_BANG, "mesh-segmentation", {"source_task": gen["id"]}, session)
        # Texture takes a local model and a reference picture, both library assets: the
        # generated GLB is downloaded with the session cookie and uploaded as a model file.
        model = next((a for a in gen.get("assets") or [] if a.get("role") == "model" and a.get("variant") == "glb"), None)
        status, raw, _headers = api.call("GET", model["url"]) if model else (0, b"", {})
        model_id = upload_asset(api, "model.glb", raw, "model/gltf-binary") if status == 200 else None
        picture_id = upload_asset(api, "reference.png", make_png(), "image/png")
        if not model_id or not picture_id:
            record("hyper3d texture", False, f"could not prepare the inputs (model asset {model_id}, picture {picture_id})")
        else:
            step(api, offerings, "hyper3d texture (local model + reference picture)", HYPER3D_TEXTURE, "retexture",
                 {"source_asset": model_id, "reference_image": picture_id, "prompt": "plain matte red"}, session)


def task_prompt(task):
    return ((task or {}).get("input") or {}).get("prompt") or ""


def scenario_style_lock_direct(api, offerings):
    """#187: canvas direct prepare carries the session lock, the one-turn
    exemption removes it, and matting/3D stay outside the lock."""
    session = new_session(api)
    source = upload_asset(api, "style-source.png", make_png(), "image/png")
    style = upload_asset(api, "style-reference.png", make_png(32), "image/png")
    if not source or not style:
        record("style lock: upload the pictures", False, "the library upload was refused")
        return
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/style-lock",
                            {"enabled": True, "text": "ink and paper", "references": [style]})
    record("style lock: save the session lock",
           status == 200 and (body.get("styleLock") or {}).get("enabled") is True,
           f"status {status} {json.dumps(body, ensure_ascii=False)[:200]}")
    locked = step(api, offerings, "style lock: image-to-image carries the lock", IMAGE_OFFERINGS["openai"][1],
                  "image-to-image", {"prompt": "make the cube blue", "reference_assets": [source]}, session)
    locked_prompt = task_prompt(locked)
    record("style lock: the frozen task prompt carries the style",
           "Style lock:" in locked_prompt and "ink and paper" in locked_prompt,
           f"prompt {locked_prompt[-160:]!r}")
    skipped = step(api, offerings, "style lock: one-turn exemption", IMAGE_OFFERINGS["openai"][1],
                   "image-to-image", {"prompt": "make the cube blue", "reference_assets": [source]}, session,
                   style_lock_skipped=True)
    skipped_prompt = task_prompt(skipped)
    record("style lock: the exempt task prompt stays clean",
           "Style lock" not in skipped_prompt and "ink and paper" not in skipped_prompt,
           f"prompt {skipped_prompt!r}")
    matting = step(api, offerings, "style lock: matting stays outside", MATTING, "background-removal",
                   {"source_assets": [source]}, session)
    matting_input = json.dumps((matting or {}).get("input") or {}, ensure_ascii=False)
    record("style lock: matting input has no style text",
           "ink and paper" not in matting_input and "Style lock" not in matting_input, matting_input[:160])
    three_d = step(api, offerings, "style lock: 3D stays outside", TEXT_TO_3D, "text-to-3d",
                   {"prompt": "a plain cube"}, session)
    three_d_prompt = task_prompt(three_d)
    record("style lock: 3D prompt has no style text",
           "Style lock" not in three_d_prompt and "ink and paper" not in three_d_prompt,
           f"prompt {three_d_prompt!r}")


def scenario_images(api, offerings):
    """The image Offerings (OpenAI, Gemini, Jimeng) and the matting service: each
    runs to SUCCEEDED with its required image delivered, intact and sandboxed."""
    session = new_session(api)
    picture = upload_asset(api, "reference.png", make_png(), "image/png")
    if not picture:
        record("images: upload the reference picture", False, "the library upload was refused")
        return
    for provider, (text_to_image, image_to_image) in IMAGE_OFFERINGS.items():
        step(api, offerings, f"{provider} text-to-image", text_to_image, "text-to-image", {"prompt": "a plain red cube"}, session)
        step(api, offerings, f"{provider} image-to-image", image_to_image, "image-to-image",
             {"prompt": "make the cube blue", "reference_assets": [picture]}, session)
    step(api, offerings, "matting background-removal", MATTING, "background-removal", {"source_assets": [picture]}, session)
    scenario_style_lock_direct(api, offerings)


def scenario_agent(api, offerings):
    """The L1 Agent equivalent: fake LLM -> tool call -> approval -> 3D task -> reply."""
    session = new_session(api)
    marker = '[[fake:tool:create_model_3d {"prompt":"a plain cube"}]]'
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": "make a cube " + marker, "creative_domain": "3d", "ai_model": TEXT_TO_3D})
    if status not in (200, 201, 202):
        record("agent turn", False, f"send {status}")
        return
    pending = []
    end = time.time() + 30
    while time.time() < end and not pending:
        time.sleep(0.5)
        pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items", [])
    if not pending:
        record("agent turn", False, "the tool call produced no approval card")
        return
    status, decision = api.json("PUT", f"/api/v1/tasks/grants/{pending[0]['request_id']}", decision_body(api, "approve"))
    task_id = decision.get("task_id")
    if status != 200 or not task_id:
        record("agent turn", False, f"approve {status} {decision}")
        return
    task = wait_task(api, task_id)
    reply = ""
    end = time.time() + 30
    while time.time() < end and "Fake upstream reply" not in reply:
        time.sleep(0.5)
        messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
        messages = messages if isinstance(messages, list) else messages.get("messages", [])
        reply = " ".join(str(m.get("content")) for m in messages if m.get("role") == "assistant")
    # The tool takes the account's default 3D model; with Meshy configured that is
    # no longer Tripo, so the delivery is checked against the Offering the Task ran.
    used = task.get("offering_id")
    problems, count = check_delivery(api, task, offerings[used]) if task.get("state") == "SUCCEEDED" and used in offerings else (["task not SUCCEEDED"], 0)
    ok = task.get("state") == "SUCCEEDED" and not problems and "Fake upstream reply" in reply
    record("agent turn: chat -> tool call -> approval -> 3D task -> reply", ok,
           f"{used} task {task.get('state')}, {count} assets verified, assistant reply present={'Fake upstream reply' in reply}"
           + ("" if not problems else "; " + "; ".join(problems)))


def assistant_reply_count(api, session):
    messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    return sum(1 for message in messages
               if message.get("role") == "assistant" and "Fake upstream reply" in str(message.get("content")))


def session_tool_calls(api, session):
    """The session's accepted tool-call results as task_id -> approval source."""
    messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    found = {}
    for message in messages:
        raw = message.get("toolCalls")
        if not raw:
            continue
        try:
            calls = json.loads(raw) if isinstance(raw, str) else raw
        except ValueError:
            continue
        for call in calls if isinstance(calls, list) else []:
            task_id = call.get("taskId")
            if task_id:
                found[task_id] = call.get("approvalSource") or ""
    return found


def agent_tool_turn(api, session, content, image_urls=None, creative_domain=None, ai_model=None):
    """One chat turn whose fake LLM answer is the tool call embedded in content.
    A paid step that asks for a card is approved once; a step the session's
    approval policy grants automatically (manual mode leaves mesh-segmentation
    to policy) is followed through its tool-call result instead. Waits for the
    Task, then returns (task, why, approval source)."""
    body = {"content": content}
    if image_urls:
        body["image_urls"] = image_urls
    if creative_domain:
        body["creative_domain"] = creative_domain
    if ai_model:
        body["ai_model"] = ai_model
    replies = assistant_reply_count(api, session)
    known = set(session_tool_calls(api, session))
    # The approved continuation may still own the session for a moment after its
    # Task ends; a 409 means another turn is active, so retry briefly.
    end = time.time() + 30
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    while status == 409 and time.time() < end:
        time.sleep(0.5)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    if status not in (200, 201, 202):
        return None, f"send {status}", ""
    task_id, source = None, ""
    end = time.time() + 30
    while time.time() < end:
        pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items", [])
        if pending:
            status, decision = api.json("PUT", f"/api/v1/tasks/grants/{pending[0]['request_id']}", decision_body(api, "approve"))
            task_id = decision.get("task_id")
            if status != 200 or not task_id:
                return None, f"approve {status} {decision}", ""
            source = "human"
            break
        granted = session_tool_calls(api, session)
        fresh = set(granted) - known
        if fresh:
            task_id = sorted(fresh)[0]
            source = granted[task_id] or "policy"
            break
        time.sleep(0.5)
    if not task_id:
        return None, "the tool call produced no approval card or task", ""
    task = wait_task(api, task_id)
    end = time.time() + 60
    while time.time() < end and assistant_reply_count(api, session) <= replies:
        time.sleep(0.5)
    return task, "", source


def scenario_hyper3d_agent(api, offerings):
    """The Agent's Hyper3D chain (#191): create_model_3d on Rodin, process_model
    mesh-segmentation on the Hyper3D Bang (rodin-task), then retexture_model on
    Hyper3D Texture (local-model) with a picture attached to the chat. Every card
    the policy raises is approved (mesh-segmentation itself is granted by the
    shipped manual policy), and every step must deliver its required files."""
    session = new_session(api)
    # Strict (manual) approval, so every step of the chain waits for its own card
    # instead of a budget policy approving the cheaper steps automatically.
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "manual"})
    if status != 200:
        record("hyper3d agent: switch the session to manual approval", False, f"approval mode {status}")
        return
    status, uploaded = api.upload("/api/v1/assets", "file", "reference.png", make_png(), "image/png")
    picture_id = uploaded.get("id") if status in (200, 201) else None
    if not picture_id:
        record("hyper3d agent: upload the reference picture", False, f"the library upload was refused ({status})")
        return
    picture_url = uploaded.get("url") or f"/api/v1/assets/{picture_id}"

    def check(label, task, why, approval, offering):
        if not task or why:
            record(label, False, why or "no task")
            return None
        if task.get("state") != "SUCCEEDED":
            record(label, False, f"state {task.get('state')} failure {json.dumps(task.get('failure'), ensure_ascii=False)}")
            return None
        used = task.get("offering_id")
        if used != offering:
            record(label, False, f"ran {used}, want {offering}")
            return None
        problems, count = check_delivery(api, task, offerings[used])
        detail = f"{used} {task['state']}, {approval or 'unknown'} approval, {count} assets verified (size, SHA-256, sandboxed delivery headers)"
        if problems:
            detail += "; " + "; ".join(problems)
        record(label, not problems, detail)
        return task if not problems else None

    create = '[[fake:tool:create_model_3d {"prompt":"a plain cube","model":"rodin-gen-2.5"}]]'
    model, why, approval = agent_tool_turn(api, session, "make a cube " + create, creative_domain="3d")
    model = check("hyper3d agent: create_model_3d -> Rodin", model, why, approval, HYPER3D_TEXT_TO_3D)
    if not model:
        return
    process = ('[[fake:tool:process_model {"step":"mesh-segmentation","source_task_id":"'
               + model["id"] + '","offering_id":"' + HYPER3D_BANG + '"}]]')
    bang, why, approval = agent_tool_turn(api, session, "split the cube " + process)
    bang = check("hyper3d agent: process_model -> Bang rodin-task", bang, why, approval, HYPER3D_BANG)
    if not bang:
        return
    texture = ('[[fake:tool:retexture_model {"offering_id":"' + HYPER3D_TEXTURE
               + '","source_task_id":"' + bang["id"]
               + '","prompt":"plain matte red","reference_handles":["@img1"]}]]')
    texture_task, why, approval = agent_tool_turn(api, session, "retexture it " + texture, image_urls=[picture_url])
    check("hyper3d agent: retexture_model -> Texture local-model with the chat picture",
          texture_task, why, approval, HYPER3D_TEXTURE)


def session_tasks(api, session):
    """The session's Tasks (the browser's task list)."""
    status, body = api.json("GET", f"/api/v1/tasks?session_id={session}&limit=50")
    if status != 200:
        return []
    return body.get("tasks") or body.get("data") or []


def scenario_agent_background(api, offerings):
    """#189: one assistant completion submits a generation step and the
    three-view SkillRun. With the session in auto mode both submit in that one
    pass, the SkillRun Task joins the generation wait, and stopping the wait
    keeps both running; the turn ends with the Schema note and the three-view
    group finishes in the background."""
    session = new_session(api)
    picture = upload_asset(api, "reference.png", make_png(), "image/png")
    fields, route = chat_schema_facts(api)
    available_member = fields.get("turn_background_available", "")
    backgrounded_member = fields.get("turn_backgrounded", "")
    if not picture or not route or not available_member or not backgrounded_member:
        record("agent background: Schema and picture available", False,
               f"picture={picture} route={route!r} fields={available_member!r}/{backgrounded_member!r}")
        return
    # Auto mode submits both paid steps inside the turn; budget mode would move
    # the 3D approval to a card, so that call would never wait in the batch.
    approval_field = fields.get("approval_mode", "approvalMode")
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {approval_field: "auto"})
    if status != 200:
        record("agent background: session switches to auto mode", False, f"approval {status} {body}")
        return
    background_path = route.replace("{sessionID}", session)
    markers = ('[[fake:tool:create_model_3d {"prompt":"a plain cube"}]] '
               '[[fake:tool:create_three_view {"reference_image_indices":[1]}]]')
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": "make a cube and three views of this picture " + markers,
        "creative_domain": "image",
        "ai_model": IMAGE_OFFERINGS["meshy"][1],
        "domainModels": {"model-3d": MESHY_TEXT_TO_3D},
        "image_urls": [f"/api/v1/assets/{picture}"],
    })
    if status not in (200, 201, 202):
        record("agent background: send the batched turn", False, f"send {status}")
        return

    # Wait for the batch: the generation call submits and waits, so the
    # three-view receipt joins the same wait. The three-view group is read
    # here too, while its front Task still runs.
    generation_task, three_view_task, front_status = "", "", ""
    available = False
    deadline = time.time() + 45
    while time.time() < deadline:
        if not generation_task:
            for task in session_tasks(api, session):
                if task.get("capability") == "text-to-3d" and task.get("state") != "CANCELED":
                    generation_task = task.get("task_id") or task.get("id") or ""
                    break
        if not three_view_task:
            items = api.json("GET", f"/api/v1/chat/sessions/{session}/three-views")[1].get("items", [])
            if items:
                front = next((slot for slot in items[0].get("slots", []) if slot.get("key") == "front"), {})
                three_view_task, front_status = front.get("task_id", ""), front.get("status", "")
        status, body = api.json("GET", background_path)
        if status == 200 and body.get(available_member):
            available = True
            break
        time.sleep(0.2)
    record("agent background: batched turn becomes background-ready", available and bool(three_view_task),
           f"available={available}, 3D task {generation_task or 'none'}, three-view task {three_view_task or 'none'}")
    if not available or not three_view_task:
        return
    record("agent background: three-view front waits in its group", front_status == "running",
           f"front status {front_status!r}, task {three_view_task}")

    status, body = api.json("POST", background_path)
    record("agent background: POST ends the wait", status == 200 and body.get(backgrounded_member) is True,
           f"POST {status} {body}")
    second_status, _ = api.json("POST", background_path)
    record("agent background: the second POST is refused", second_status == 409, f"second POST {second_status}")

    three_view = wait_task(api, three_view_task)
    items = api.json("GET", f"/api/v1/chat/sessions/{session}/three-views")[1].get("items", [])
    front = next((slot for slot in items[0].get("slots", []) if slot.get("key") == "front"), {}) if items else {}
    record("agent background: three-view front finishes in the background",
           three_view.get("state") == "SUCCEEDED" and front.get("status") == "succeeded" and len(items) == 1,
           f"task {three_view.get('state')}, group front {front.get('status')!r}, groups {len(items)}")

    generation = wait_task(api, generation_task) if generation_task else {}
    used = generation.get("offering_id")
    problems, count = check_delivery(api, generation, offerings[used]) if generation.get("state") == "SUCCEEDED" and used in offerings else (["task not SUCCEEDED"], 0)
    record("agent background: the generation task keeps running",
           generation.get("state") == "SUCCEEDED" and not problems,
           f"{used} task {generation.get('state')}, {count} assets verified" + ("" if not problems else "; " + "; ".join(problems)))
    record("agent background: neither task was cancelled",
           three_view.get("state") != "CANCELED" and generation.get("state") != "CANCELED",
           f"three-view {three_view.get('state')}, generation {generation.get('state')}")

    reply = ""
    deadline = time.time() + 30
    while time.time() < deadline and "已停止等待" not in reply:
        time.sleep(0.3)
        messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
        messages = messages if isinstance(messages, list) else messages.get("messages", [])
        reply = " ".join(str(m.get("content")) for m in messages if m.get("role") == "assistant")
    record("agent background: the ended turn names the background Task",
           "已停止等待" in reply and "三视图卡片" in reply, reply[:160])


# scenario -> provider -> (expected state, expected failure class). The fault is
# chosen by the stack's API key, so one stack per scenario exercises every provider.
# provider -> (Offering, capability, input); "$picture" is the uploaded reference picture.
FAULT_OFFERINGS = {
    "tripo": (TEXT_TO_3D, "text-to-3d", {"prompt": "a plain cube"}),
    "meshy": (MESHY_TEXT_TO_3D, "text-to-3d", {"prompt": "a plain cube"}),
    "hyper3d": (HYPER3D_TEXT_TO_3D, "text-to-3d", {"prompt": "a plain cube"}),
    "openai": (IMAGE_OFFERINGS["openai"][0], "text-to-image", {"prompt": "a plain red cube"}),
    "gemini": (IMAGE_OFFERINGS["gemini"][0], "text-to-image", {"prompt": "a plain red cube"}),
    "jimeng": (IMAGE_OFFERINGS["jimeng"][0], "text-to-image", {"prompt": "a plain red cube"}),
    "matting": (MATTING, "background-removal", {"source_assets": ["$picture"]}),
}
THREE_D = ("tripo", "meshy", "hyper3d")
IMAGES = ("openai", "gemini", "jimeng", "matting")
REFETCHABLE = THREE_D  # matting is refetchable too, but its downloads are not signed URLs
FAULTS = {
    "insufficient-balance": {**{p: ("FAILED", "insufficient_balance") for p in THREE_D},
                             **{p: ("FAILED", "insufficient_balance") for p in ("openai", "gemini", "jimeng")}},
    # A key credit limit is its own class for Meshy (credential_quota, #73); the OpenAI image gateway and the
    # Gemini connection (#139) answer it like an empty balance (insufficient_quota code); Ark documents it as a
    # 429 QuotaExceeded, which the Jimeng connection declares credential_quota (#139: it used to read as a
    # retryable rate limit). None of the three image answers was observed on the real service.
    "key-quota": {"tripo": ("FAILED", "insufficient_balance"), "meshy": ("FAILED", "credential_quota"),
                  "hyper3d": ("FAILED", "insufficient_balance"), "openai": ("FAILED", "insufficient_balance"),
                  "gemini": ("FAILED", "insufficient_balance"), "jimeng": ("FAILED", "credential_quota")},
    # The image fakes fail a task with their vendor's content-safety refusal, read as content_rejected (#35).
    "task-failed": {"tripo": ("FAILED", "rejected"), "meshy": ("FAILED", "invalid_input"), "hyper3d": ("FAILED", "rejected"),
                    "openai": ("FAILED", "content_rejected"), "gemini": ("FAILED", "content_rejected"),
                    "jimeng": ("FAILED", "content_rejected"), "matting": ("FAILED", "rejected")},
    # An inline picture that is not a picture is a protocol failure; a corrupt download fails the integrity check.
    "artifact-corrupt": {**{p: ("FAILED", "integrity_failure") for p in THREE_D}, "openai": ("FAILED", "protocol"),
                         "gemini": ("FAILED", "protocol"), "jimeng": ("FAILED", "integrity_failure"),
                         "matting": ("FAILED", "integrity_failure")},
    "artifact-404": {**{p: ("FAILED", "upstream_unavailable") for p in THREE_D}, "jimeng": ("FAILED", "upstream_unavailable"),
                     "matting": ("FAILED", "rejected")},
    # The signed URLs of the first read have expired. Tripo, Meshy and Hyper3D read the job again on the
    # materialization retry and get fresh URLs, so the Task still ends SUCCEEDED (self-healing) ...
    "artifact-expired": {**{p: ("SUCCEEDED", None) for p in THREE_D}, "jimeng": ("FAILED", "upstream_unavailable")},
    # ... and when every URL issued before a renewal has expired (the key asks for 1000 issues; Tripo re-reads
    # its task for the format conversions too, so no small count is reliable), the Task fails like a missing
    # artifact. After the fake renews its URLs (POST /fake/control/renew-artifacts) the free result refetch
    # delivers it. Jimeng's one-shot answer has no job to read again.
    "artifact-expired-stuck": {**{p: ("FAILED", "upstream_unavailable") for p in THREE_D}, "jimeng": ("FAILED", "upstream_unavailable")},
}
# The API key a stack uses for a scenario, where it is not just fake-<scenario>.
FAULT_KEYS = {"artifact-expired-stuck": "fake-artifact-expired-1000"}


def refetch_delivers(api, task, offerings, offering):
    """The free path after an expired URL: POST the Task's refetch, wait for the new
    Task and check it delivered the same files. Returns (ok, detail)."""
    status, body = api.json("POST", f"/api/v1/tasks/{task['id']}/refetch")
    new_id = body.get("task_id")
    if status not in (200, 201, 202) or not new_id:
        return False, f"refetch {status} {json.dumps(body, ensure_ascii=False)[:200]}"
    fresh = wait_task(api, new_id, seconds=120)
    if fresh.get("state") != "SUCCEEDED":
        return False, f"refetch task {fresh.get('state')} {json.dumps(fresh.get('failure'), ensure_ascii=False)}"
    problems, count = check_delivery(api, fresh, offering)
    return not problems, f"refetch task SUCCEEDED, {count} assets verified" if not problems else "; ".join(problems)


def scenario_fault(api, offerings, scenario):
    session = new_session(api)
    picture = upload_asset(api, "reference.png", make_png(), "image/png")
    stuck = []
    for provider, (state, failure_class) in FAULTS[scenario].items():
        offering_id, capability, base = FAULT_OFFERINGS[provider]
        payload = {name: ([picture] if value == ["$picture"] else value) for name, value in base.items()}
        label = f"fault {scenario} ({provider})"
        task_id, why = submit(api, offering_id, capability, payload_for(offerings[offering_id], payload), session)
        if not task_id:
            # Refused before a Task exists (for example a creation error surfaced at submit).
            record(label, False, why)
            continue
        task = wait_task(api, task_id, seconds=120)
        got = (task.get("failure") or {}).get("class")
        ok = task.get("state") == state and (failure_class is None or got == failure_class)
        detail = f"state {task.get('state')}, class {got} (want {state}, {failure_class or 'any'})"
        if ok and state == "SUCCEEDED":
            # A self-healed Task must still deliver every required file, intact.
            problems, count = check_delivery(api, task, offerings[offering_id])
            ok, detail = not problems, detail + (f"; {count} assets verified" if not problems else "; " + "; ".join(problems))
        record(label, ok, detail)
        if scenario == "artifact-expired-stuck" and ok:
            stuck.append((provider, task, offering_id))
    if stuck:
        # The fake renews its URLs for every task at once, so the refetches wait until each
        # provider's Task has failed.
        urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{FAKE_PORT}/fake/control/renew-artifacts", data=b"", method="POST"), timeout=10).read()
        for provider, task, offering_id in stuck:
            if provider in REFETCHABLE:
                done, detail = refetch_delivers(api, task, offerings, offerings[offering_id])
                record(f"fault {scenario} ({provider}): free refetch delivers", done, detail)
            else:
                status, body = api.json("POST", f"/api/v1/tasks/{task['id']}/refetch")
                record(f"fault {scenario} ({provider}): no refetch for a one-shot result", status == 409,
                       f"refetch {status} reason {(body.get('refetch') or {}).get('reason')}")


# Written by the stack's loopback guard (fakeStackLoopbackOnly in internal/app) for every
# request to a host that is neither loopback nor the fake artifact host (#218).
REFUSAL_MARKER = "fake-stack: refused non-loopback request"


def check_no_real_upstream(work):
    """Every stack this run started must have stayed on loopback: the guard refuses and
    logs any other destination, so one marker line in any stack log fails the run."""
    logs, refused = 0, []
    for root, _dirs, files in os.walk(work):
        if "stack.log" in files:
            logs += 1
            with open(os.path.join(root, "stack.log"), encoding="utf-8", errors="replace") as handle:
                refused += [line.strip() for line in handle if REFUSAL_MARKER in line]
    record("zero real upstream requests", logs > 0 and not refused,
           f"{logs} stack logs, {len(refused)} refused" + ("; " + "; ".join(refused[:5]) if refused else ""))


# ---------------------------------------------------------------- agent quality (#238)
#
# The free trajectory evaluation: the fixed request set under
# scripts/smoke/agent-quality/ drives the real chat/orchestration path, while the
# fake LLM only turns the marker in each user step into the tool call the request
# set names. Cards, approvals, Tasks, renders and the final reply are real. This
# measures the system's realization of a reference trajectory (not a model's
# choices); the live output run in eval-agent-output.py measures real Agent
# behavior and artifacts.

def _quality_git_state():
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO, text=True).strip())
    return revision, dirty


def _quality_marker(tool):
    """The fake LLM's tool marker for one step (compact JSON keeps the marker regex simple)."""
    arguments = json.dumps(tool.get("arguments") or {}, ensure_ascii=False, separators=(",", ":"))
    return f"[[fake:tool:{tool['name']} {arguments}]]"


def _quality_model_task(api, session):
    """The newest finished 3D task of the session, which the review tools can name by ID.

    The generated model's @handle is attached to the result message but the review
    tools did not resolve it in this fake flow; a Task ID is the stable reference.
    """
    tasks = [task for task in session_tasks(api, session)
             if task.get("capability") in ("text-to-3d", "image-to-3d", "multiview-to-3d")
             and task.get("state") == "SUCCEEDED"]
    return tasks[-1]["id"] if tasks else ""


def _quality_choices(api, session):
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/choices")
    return body.get("choices") or [] if status == 200 else []


def _quality_pending(api, session):
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")
    return body.get("items") or [] if status == 200 else []


def _quality_answer(api, session, card, purpose, domain, model):
    options = [option for option in card.get("options") or [] if isinstance(option, dict) and option.get("id")]
    pick = next((option for option in options if purpose and option["id"] == purpose), None)
    pick = pick or next((option for option in options if option.get("recommended")), options[0] if options else None)
    if pick is None:
        return False
    body = {"content": "按默认选项继续。", "choiceAnswer": {"choice_id": card["id"], "option_id": pick["id"]},
            "creative_domain": domain, "ai_model": model}
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    return status in (200, 201, 202)


def _quality_send(api, session, content, domain, model):
    body = {"content": content, "creative_domain": domain, "ai_model": model}
    end = time.time() + 30
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    while status == 409 and time.time() < end:
        time.sleep(0.5)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    return status in (200, 201, 202)


def _quality_turn_done(api, session, timeout=90):
    """Wait for the session's chat stream to carry its done event (or answer 204: no turn is held)."""
    request = urllib.request.Request(f"{api.base}/api/chat/sessions/{session}/stream?fromId=0",
                                     headers={"Accept": "text/event-stream", "Origin": api.base})
    end = time.time() + timeout
    try:
        with api.opener.open(request, timeout=min(max(timeout, 1), 30)) as response:
            if response.status == 204:
                return True
            for raw in response:
                line = raw.decode("utf-8", "replace").strip()
                if line.startswith("data:"):
                    try:
                        payload = json.loads(line[len("data:"):].strip())
                    except ValueError:
                        continue
                    if isinstance(payload, dict) and payload.get("type") == "done":
                        return True
                if time.time() >= end:
                    return False
    except urllib.error.HTTPError as error:
        return error.code == 204
    except (urllib.error.URLError, TimeoutError, OSError):
        return False
    return False


def _quality_messages(api, session):
    status, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
    if status != 200:
        return []
    return body if isinstance(body, list) else body.get("messages", [])


def _quality_final_reply(messages):
    for message in reversed(messages or []):
        if message.get("role") == "assistant" and (message.get("content") or "").strip():
            return message
    return {}


def run_quality_case(api, request, domain, model, model_3d=""):
    """One request through the fake-LLM chat path; returns the run dict for agent_quality."""
    started = time.time()
    session = new_session(api)
    api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "manual"})
    if request.get("modality") == "3d" and model_3d:
        # #103: only an explicit pick in the session's model picker binds the 3D tools.
        api.json("PUT", f"/api/v1/chat/sessions/{session}/model3d", {"explicit_model_3d": model_3d})
    cards, seen = [], set()
    turns = 0
    for step in request["steps"]:
        arguments = dict(step["tool"].get("arguments") or {})
        if step["tool"]["name"] == "create_model_3d_from_image" and "input_override_reason" not in arguments:
            # The fake upstream's generated picture is a 1x1 placeholder, not a
            # real 3D input. The reference-trajectory runner records the
            # person's explicit override; the live output run does not use
            # these steps and the real Agent must pass the gate or ask.
            arguments["input_override_reason"] = "the fake upstream image is a placeholder, not a real 3D input"
        tool = {"name": step["tool"]["name"], "arguments": arguments}
        model_task = _quality_model_task(api, session)
        if model_task and tool["arguments"].get("model") == "@model1":
            tool["arguments"]["model"] = model_task
        content = f"{step['user']} {_quality_marker(tool)}"
        before_tasks = {task.get("id") for task in session_tasks(api, session)}
        if not _quality_send(api, session, content, domain, model):
            record(f"agent-quality {request['id']}: send {step['tool']['name']}", False, "message refused")
            break
        turns += 1
        if not _quality_turn_done(api, session):
            record(f"agent-quality {request['id']}: turn {step['tool']['name']}", False, "turn did not finish")
            break
        # A card ends its turn; answer or approve it, then follow the turn it starts.
        for _ in range(4):
            answered = False
            for card in _quality_pending(api, session):
                request_id = card.get("request_id")
                if not request_id or request_id in seen:
                    continue
                seen.add(request_id)
                cards.append({"kind": "approval", "id": request_id, "tool": card.get("tool"),
                              "capability": card.get("capability"), "state": "approved"})
                status, decision = api.json("PUT", f"/api/v1/tasks/grants/{request_id}", decision_body(api, "approve"))
                task_id = decision.get("task_id")
                if status == 200 and task_id:
                    wait_task(api, task_id)
                answered = True
            for card in _quality_choices(api, session):
                card_id = card.get("id")
                if card.get("state") != "pending" or not card_id or card_id in seen:
                    continue
                seen.add(card_id)
                cards.append({"kind": "choice", "id": card_id, "tool": card.get("tool"),
                              "card_kind": card.get("kind"), "state": "answered"})
                _quality_answer(api, session, card, request.get("purpose"), domain, model)
                answered = True
            if not answered:
                break
            if not _quality_turn_done(api, session):
                break
        for task in session_tasks(api, session):
            if task.get("id") not in before_tasks:
                wait_task(api, task.get("id"))
    messages = _quality_messages(api, session)
    tasks = session_tasks(api, session)
    images = sum(1 for message in messages for attachment in message.get("attachments") or []
                 if isinstance(attachment, dict) and attachment.get("kind") == "image")
    return {
        "session_id": session,
        "messages": messages,
        "tasks": tasks,
        "cards": cards,
        "final_reply": _quality_final_reply(messages),
        "image_count": images,
        "turns": turns,
        "elapsed_ms": int((time.time() - started) * 1000),
    }


def scenario_agent_quality(api, offerings, report_dir=""):
    """Run the fixed request set (#238) and write a trajectory report."""
    import agent_quality as quality

    data, path, digest = quality.load_requests()
    image_model = next((item["model_id"] for item in offerings.values()
                        if item.get("available") and item.get("capability") == "text-to-image" and item.get("model_id")), "")
    model_3d = next((item["model_id"] for item in offerings.values()
                     if item.get("available") and item.get("capability") == "text-to-3d" and item.get("model_id")), "")
    cases = []
    for request in data["requests"]:
        domain = "image" if request.get("modality") == "2d" else "model-3d"
        model = image_model if request.get("modality") == "2d" else model_3d
        run = run_quality_case(api, request, domain, model, model_3d)
        case = quality.evaluate_case(request, run)
        cases.append(case)
        failed = quality.case_hard_failures(case)
        record(f"agent-quality {request['id']}", not failed,
               f"tools={case['metrics']['tool_call_count']} paid={case['metrics']['paid_call_count']} "
               f"cards={case['metrics']['interruption_count']} inspect={case['metrics']['self_inspection']['inspect_model']} "
               f"render={case['metrics']['self_inspection']['render_model_views']}"
               + ("" if not failed else "; " + "; ".join(f"{check['id']}: {check['detail']}" for check in failed)))
    revision, dirty = _quality_git_state()
    report = quality.summarize(cases, "trajectory-fake", revision, dirty,
                               {"path": os.path.relpath(path, REPO), "version": data["version"], "sha256": digest})
    directory = report_dir or os.path.join(tempfile.gettempdir(), "plinth-agent-quality")
    json_path, markdown_path = quality.write_report(report, directory, "agent-quality-trajectory")
    print(f"# agent quality report: {json_path} and {markdown_path}", flush=True)
    return report


def _image_self_check_summaries(api, session):
    status, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
    messages = body if isinstance(body, list) else body.get("messages", [])
    summaries = []
    for message in messages:
        raw = message.get("toolCalls")
        if not raw:
            continue
        try:
            calls = json.loads(raw) if isinstance(raw, str) else raw
        except ValueError:
            continue
        for call in calls if isinstance(calls, list) else []:
            if call.get("name") == "inspect_image" and call.get("summary"):
                summaries.append(call["summary"])
    return summaries


def _image_self_check_refusal(work, session):
    """The latest turn's tool results live in the checkpoint, not the messages wire."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/main/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        row = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT 1",
                         (session,)).fetchone()
        db.close()
    except sqlite3.Error:
        return ""
    if not row or not row["checkpoint_json"]:
        return ""
    try:
        checkpoint = json.loads(row["checkpoint_json"])
    except ValueError:
        return ""
    for message in checkpoint.get("completed") or []:
        content = str(message.get("Content") or message.get("content") or "")
        if "不适合作为 3D 源" in content:
            return content
    return ""


def _image_self_check_send(api, session, content, model, replies_before):
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages",
                         {"content": content, "creative_domain": "model-3d", "ai_model": model})
    if status not in (200, 201, 202):
        return False
    end = time.time() + 60
    while time.time() < end:
        if assistant_reply_count(api, session) > replies_before:
            return True
        time.sleep(0.5)
    return False


def scenario_image_self_check(api, offerings, work):
    """#245: inspect_image checklists and the image-to-3D input gate on the fake stack."""
    session = new_session(api)
    api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
    model = next((item["model_id"] for item in offerings.values()
                  if item.get("available") and item.get("capability") == "text-to-3d"), "")
    if model:
        api.json("PUT", f"/api/v1/chat/sessions/{session}/model3d", {"explicit_model_3d": model})
    white, red = (250, 250, 250), (200, 30, 30)
    pictures = [
        ("good", make_picture(128, 128, white, [((40, 32, 88, 96), red)])),
        ("busy", make_picture(128, 128, lambda x, y: (0, 0, 0) if (x // 8 + y // 8) % 2 else (255, 255, 255),
                              [((40, 32, 88, 96), red)])),
        ("multi", make_picture(128, 128, white, [((20, 40, 48, 88), red), ((80, 40, 108, 88), red)])),
        ("edge", make_picture(128, 128, white, [((0, 40, 40, 88), red)])),
    ]
    urls = []
    for name, data in pictures:
        status, body = api.upload("/api/v1/assets", "file", name + ".png", data, "image/png")
        asset_id = body.get("id") if status in (200, 201) else None
        if not asset_id:
            record("#245 sample upload", False, f"{name}: HTTP {status}")
            return
        urls.append(f"/api/v1/assets/{asset_id}")
    replies = assistant_reply_count(api, session)
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages",
                         {"content": "这四张图是素材", "image_urls": urls, "creative_domain": "model-3d", "ai_model": model})
    if status not in (200, 201, 202):
        record("#245 sample attachments", False, f"send {status}")
        return
    end = time.time() + 30
    handles = []
    while time.time() < end and len(handles) < 4:
        time.sleep(0.3)
        _, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
        messages = body if isinstance(body, list) else body.get("messages", [])
        for message in messages:
            for attachment in message.get("attachments") or []:
                if attachment.get("kind") == "image" and attachment.get("handle"):
                    handles.append(attachment["handle"])
        handles = list(dict.fromkeys(handles))
    if len(handles) < 4:
        record("#245 sample handles", False, f"handles={handles}")
        return
    expected = {"good": 0, "busy": 1, "multi": 1, "edge": 1}
    for index, (name, _data) in enumerate(pictures):
        before = assistant_reply_count(api, session)
        marker = '[[fake:tool:inspect_image {"image":"' + handles[index] + '","checklist":"3d_input"}]]'
        if not _image_self_check_send(api, session, "检查 " + marker, model, before):
            record(f"#245 inspect_image {name}", False, "turn did not finish")
            continue
        summaries = _image_self_check_summaries(api, session)
        summary = next((item for item in reversed(summaries) if item.get("handle") == handles[index]), None)
        failed = (summary or {}).get("checks", {}).get("failed")
        ok = summary is not None and ((failed == 0) if expected[name] == 0 else (failed or 0) >= expected[name])
        record(f"#245 inspect_image {name}", ok, f"handle={handles[index]} failed={failed} summary={summary}")
    before_tasks = {task.get("id") for task in session_tasks(api, session)}
    before = assistant_reply_count(api, session)
    bad = json.dumps({"prompt": "a prop", "reference_handles": [handles[1]]}, ensure_ascii=False, separators=(",", ":"))
    _image_self_check_send(api, session, "提交 3D [[fake:tool:create_model_3d_from_image " + bad + "]]", model, before)
    refusal = _image_self_check_refusal(work, session)
    new_tasks = {task.get("id") for task in session_tasks(api, session)} - before_tasks
    record("#245 3D input gate refuses a busy picture", bool(refusal) and not new_tasks,
           f"refusal={'yes' if refusal else 'no'} tasks={len(new_tasks)}")
    before = assistant_reply_count(api, session)
    override = json.dumps({"prompt": "a prop", "reference_handles": [handles[1]],
                           "input_override_reason": "the person asked to keep the photo background"},
                          ensure_ascii=False, separators=(",", ":"))
    _image_self_check_send(api, session, "提交 3D [[fake:tool:create_model_3d_from_image " + override + "]]", model, before)
    task = None
    end = time.time() + 60
    while time.time() < end and task is None:
        for candidate in session_tasks(api, session):
            if candidate.get("id") not in before_tasks:
                task = candidate
                break
        time.sleep(0.5)
    if task is not None:
        task = wait_task(api, task["id"], 90)
    record("#245 3D input gate accepts an override reason", bool(task) and task.get("state") == "SUCCEEDED",
           f"task={task.get('id') if task else None} state={task.get('state') if task else None}")


def _work_ledger_checkpoint(work, session):
    """The newest turn checkpoint of the session: the request the model got and
    this round's tool receipts. The checkpoint stores Go field names; older
    records may carry lowercase keys, so both spellings are accepted."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/main/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        row = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT 1",
                         (session,)).fetchone()
        db.close()
    except sqlite3.Error:
        return {}
    if not row or not row["checkpoint_json"]:
        return {}
    try:
        return json.loads(row["checkpoint_json"])
    except ValueError:
        return {}


def _work_ledger_wait_system(work, session, needle, seconds=20):
    """Wait until the newest checkpoint's system prompt carries a marker; the
    turn's checkpoint may land a moment after its reply."""
    end = time.time() + seconds
    system = ""
    while time.time() < end:
        system = _work_ledger_system_prompt(_work_ledger_checkpoint(work, session))
        if needle in system:
            return system
        time.sleep(0.5)
    return system


def _work_ledger_system_prompt(checkpoint):
    request = checkpoint.get("Request") or checkpoint.get("request") or {}
    messages = request.get("Messages") or request.get("messages") or []
    if not messages:
        return ""
    first = messages[0] or {}
    return str(first.get("Content") or first.get("content") or "")


def _work_ledger_completed(checkpoint):
    for key in ("Completed", "completed"):
        value = checkpoint.get(key)
        if isinstance(value, list):
            return value
    return []


def _work_ledger_tool_contents(checkpoint):
    contents = []
    for message in _work_ledger_completed(checkpoint):
        content = message.get("Content") if "Content" in message else message.get("content")
        if content:
            contents.append(str(content))
    return contents


def _work_ledger_plain_turn(api, session, content, creative_domain="image", image_urls=None, ai_model=None):
    """Send one turn whose reply needs no Task and wait for the final answer."""
    replies = assistant_reply_count(api, session)
    body = {"content": content}
    if creative_domain:
        body["creative_domain"] = creative_domain
    if image_urls:
        body["image_urls"] = image_urls
    if ai_model:
        body["ai_model"] = ai_model
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    if status not in (200, 201, 202):
        return False
    end = time.time() + 60
    while time.time() < end:
        if assistant_reply_count(api, session) > replies:
            return True
        time.sleep(0.5)
    return False


def _work_ledger_marker(tool, arguments):
    return "[[fake:tool:" + tool + " " + json.dumps(arguments, ensure_ascii=False, separators=(",", ":")) + "]]"


def _work_ledger_log_text(work):
    text = ""
    for root, dirs, files in os.walk(work):
        for name in files:
            if os.path.basename(root) != "logs" and name != "stack.log":
                continue
            try:
                with open(os.path.join(root, name), encoding="utf-8", errors="replace") as handle:
                    text += handle.read()
            except OSError:
                pass
    return text


def scenario_work_ledger(api, offerings, work):
    """#248: the frozen production ledger survives history truncation and
    context compaction, the read-only get_production_step tool returns the
    full prompts and sources, the style anchor is injected verbatim, and the
    inspect_model leftovers reach the next turn. Fake only."""
    secret = "LEDGER-PROMPT-SECRET-9c41"
    long_prompt = ("a full-body concept art of a wandering ranger with a black cloak and a silver clasp, "
                   "standing in a misty pine forest, painted in warm gouache with visible brush strokes, "
                   "soft rim light, detailed leather boots and gloves, calm expression, muted earth palette, "
                   "centered composition, no text, no watermark, " + secret)
    if len(long_prompt) <= 120:
        record("#248 long prompt", False, f"{len(long_prompt)} characters")
        return
    session = new_session(api)
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
    if status != 200:
        record("#248 auto approval", False, f"{status} {body}")
        return
    marker = _work_ledger_marker("refine_image", {"prompt": long_prompt})
    task, why, _approval = agent_tool_turn(api, session, "make the ranger " + marker, creative_domain="image",
                                               ai_model=IMAGE_OFFERINGS["openai"][0])
    if not task or why or task.get("state") != "SUCCEEDED":
        record("#248 first generation", False, why or f"state {(task or {}).get('state')}")
        return
    first_task_id = task["id"]
    first_prompt = ((task.get("input") or {}).get("prompt") or "")
    record("#248 first generation froze the long prompt", first_prompt == long_prompt,
           f"task={first_task_id} prompt_chars={len(first_prompt)}")
    if first_prompt != long_prompt:
        return
    # More than keep_recent_rounds user rounds so a manual compaction has work.
    for index in range(5):
        if not _work_ledger_plain_turn(api, session, f"keep thinking {index}", creative_domain="image"):
            record(f"#248 filler round {index}", False, "turn did not finish")
            return
    status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
    compactions = body.get("compactions") or []
    record("#248 manual compaction after the long prompt", status == 200 and bool(compactions),
           f"status={status} compactions={len(compactions)} body={body if status != 200 else ''}")
    if status != 200:
        return
    # Retrieve the full step through the read-only tool after compaction.
    read = _work_ledger_marker("get_production_step", {"step": first_task_id})
    if not _work_ledger_plain_turn(api, session, "show me the exact frozen step " + read, creative_domain="image"):
        record("#248 get_production_step turn", False, "turn did not finish")
        return
    checkpoint = _work_ledger_checkpoint(work, session)
    payload = None
    for content in _work_ledger_tool_contents(checkpoint):
        if '"raw_prompt"' in content:
            try:
                candidate = json.loads(content)
            except ValueError:
                continue
            if (candidate.get("step") or {}).get("task_id") == first_task_id:
                payload = candidate
                break
    step = (payload or {}).get("step") or {}
    ok = (step.get("raw_prompt") == long_prompt and step.get("final_prompt") == long_prompt
          and bool(step.get("parameters")) and "source" in (step.get("parameters") or [{}])[0])
    record("#248 get_production_step returns the full prompt and sources after compaction", ok,
           f"raw_chars={len(str(step.get('raw_prompt') or ''))} final_chars={len(str(step.get('final_prompt') or ''))} "
           f"parameters={len(step.get('parameters') or [])}")
    system = _work_ledger_system_prompt(checkpoint)
    record("#248 ledger summary is in the next turn's system prompt",
           bool(step.get("step")) and step.get("step") in system and first_task_id in system
           and "<<<WORK_LEDGER>>>" in system,
           f"step={step.get('step')} ledger={'yes' if '<<<WORK_LEDGER>>>' in system else 'no'}")
    if not ok:
        return
    # The next generation changes only the cloak, keeping every other byte.
    changed = step["final_prompt"].replace("black cloak", "red cloak")
    marker = _work_ledger_marker("refine_image", {"prompt": changed})
    second, why, _approval = agent_tool_turn(api, session, "only change the cloak to red " + marker, creative_domain="image",
                                             ai_model=IMAGE_OFFERINGS["openai"][0])
    new_prompt = ((second or {}).get("input") or {}).get("prompt") or ""
    record("#248 the edited prompt differs only in the cloak fragment",
           bool(second) and second.get("state") == "SUCCEEDED" and new_prompt == changed
           and "black cloak" not in new_prompt and new_prompt != first_prompt,
           f"state={(second or {}).get('state')} new_chars={len(new_prompt)}")
    # The style anchor is the existing session style lock, injected verbatim.
    status, uploaded = api.upload("/api/v1/assets", "file", "style-ref.png", make_png(), "image/png")
    asset_id = uploaded.get("id") if status in (200, 201) else None
    if not asset_id:
        record("#248 style reference upload", False, f"{status}")
        return
    reference_url = uploaded.get("url") or f"/api/v1/assets/{asset_id}"
    if not _work_ledger_plain_turn(api, session, "here is the style reference", creative_domain="image",
                                   image_urls=[reference_url]):
        record("#248 style reference turn", False, "turn did not finish")
        return
    status, messages = api.json("GET", f"/api/chat/sessions/{session}/messages")
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    handle = ""
    for message in messages:
        for attachment in message.get("attachments") or []:
            if attachment.get("asset_id") == asset_id or attachment.get("assetId") == asset_id:
                if attachment.get("handle"):
                    handle = attachment["handle"]
    anchor_text = "印象派笔触、暖金色光线、厚涂质感、低饱和土色与冷灰阴影；" * 14
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/style-lock",
                            {"enabled": True, "text": anchor_text, "references": [asset_id]})
    record("#248 style lock is saved", status == 200 and bool(handle),
           f"status={status} handle={handle or 'missing'}")
    status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
    record("#248 second compaction with anchors", status == 200, f"status={status}")
    anchor_read = _work_ledger_marker("get_production_step", {"step": first_task_id})
    if not _work_ledger_plain_turn(api, session, "show the step again " + anchor_read, creative_domain="image"):
        record("#248 anchor assertion turn", False, "turn did not finish")
        return
    system = _work_ledger_wait_system(work, session, "WORK_ANCHORS")
    record("#248 style anchor text and reference survive compaction verbatim",
           anchor_text in system and bool(handle) and handle in system and "<<<WORK_ANCHORS>>>" in system,
           f"text={'yes' if anchor_text in system else 'no'} handle={'yes' if handle and handle in system else 'no'}")
    # A 3D self-check leftover is attributed to the step and read next turn.
    brief = _work_ledger_marker("update_production_brief", {"purpose": "game_character"})
    if not _work_ledger_plain_turn(api, session, "it is a game character " + brief, creative_domain="model-3d",
                                   ai_model=TEXT_TO_3D):
        record("#248 production brief turn", False, "turn did not finish")
        return
    model_marker = _work_ledger_marker("create_model_3d", {"prompt": "a stylized game character"})
    model, why, _approval = agent_tool_turn(api, session, "make the character " + model_marker, creative_domain="model-3d",
                                            ai_model="meshy/meshy-t2.5/text-to-3d")
    if not model or model.get("state") != "SUCCEEDED":
        record("#248 3D model for the self-check", False, why or f"state={(model or {}).get('state')}")
        return
    inspect = _work_ledger_marker("inspect_model", {"model": model["id"]})
    if not _work_ledger_plain_turn(api, session, "inspect the model " + inspect, creative_domain="model-3d",
                                   ai_model=TEXT_TO_3D):
        record("#248 inspect_model turn", False, "turn did not finish")
        return
    leftover_read = _work_ledger_marker("get_production_step", {"step": model["id"]})
    if not _work_ledger_plain_turn(api, session, "read the inspected step " + leftover_read, creative_domain="model-3d",
                                   ai_model=TEXT_TO_3D):
        record("#248 leftover visibility turn", False, "turn did not finish")
        return
    system = _work_ledger_wait_system(work, session, "inspect_model")
    record("#248 inspect_model leftovers reach the next turn",
           "triangles" in system and "inspect_model" in system and ("遗留" in system or "fail" in system.lower()),
           f"triangles={'yes' if 'triangles' in system else 'no'} inspect={'yes' if 'inspect_model' in system else 'no'}")
    # Ledger content never reaches a server log.
    logs = _work_ledger_log_text(work)
    record("#248 ledger content is absent from server logs", secret not in logs and long_prompt not in logs,
           f"logs={len(logs)} bytes, secret={'present' if secret in logs else 'absent'}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--only", default="", help="comma-separated: balance,chain,meshy,hyper3d,hyper3d-agent,images,tripo,p2,agent,agent-background,agent-quality,image-self-check,work-ledger,faults")
    parser.add_argument("--faults", default="", help="comma-separated fault scenarios to run (default: all of %s)" % ", ".join(FAULTS))
    parser.add_argument("--work", default="", help="work directory (default: a temporary one, removed afterwards)")
    parser.add_argument("--keep", action="store_true", help="keep the work directory")
    parser.add_argument("--report", default="", help="directory for the #238 agent-quality report (default: a temporary one)")
    args = parser.parse_args()
    selected = set(filter(None, args.only.split(","))) or {"balance", "chain", "meshy", "hyper3d", "images", "tripo", "p2", "agent", "agent-background", "faults"}
    work = args.work or tempfile.mkdtemp(prefix="fake-stack-")
    os.makedirs(work, exist_ok=True)
    processes = Processes()
    try:
        fake_binary, stack_binary = build(work)
        catalog = private_catalog(work)
        url_catalog = private_catalog(work, jimeng_urls=True)
        require_free(FAKE_PORT, "fake upstream")
        require_free(STACK_PORT, "stack")
        fake_child = processes.start([fake_binary, "-addr", f"127.0.0.1:{FAKE_PORT}", "-artifact-origin", f"https://{ASSET_HOST}"],
                                     {"PATH": os.environ["PATH"]}, os.path.join(work, "fake.log"))
        if not wait_port(FAKE_PORT, fake_child):
            raise RuntimeError(f"the fake upstream did not listen; see {work}/fake.log")
        print(f"# work {work}; fake on :{FAKE_PORT}, stack on :{STACK_PORT}", flush=True)

        if selected & {"balance", "chain", "meshy", "hyper3d", "hyper3d-agent", "images", "tripo", "p2", "agent", "agent-background", "agent-quality", "image-self-check", "work-ledger"}:
            child, api = start_stack(processes, stack_binary, work, "main", catalog, GOOD_KEY)
            offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
            if "balance" in selected:
                scenario_balance(api, offerings)
            if "chain" in selected:
                scenario_chain(api, offerings)
            if "meshy" in selected:
                scenario_meshy(api, offerings)
            if "hyper3d" in selected:
                scenario_hyper3d(api, offerings)
            if "images" in selected:
                scenario_images(api, offerings)
            if "tripo" in selected:
                scenario_tripo_images(api, offerings)
            if "p2" in selected:
                scenario_p2(api, offerings)
            if "agent" in selected:
                scenario_agent(api, offerings)
            if "agent" in selected or "hyper3d-agent" in selected:
                scenario_hyper3d_agent(api, offerings)
            if "agent-background" in selected:
                scenario_agent_background(api, offerings)
            if "agent-quality" in selected:
                scenario_agent_quality(api, offerings, args.report or os.path.join(work, "agent-quality"))
            if "image-self-check" in selected:
                scenario_image_self_check(api, offerings, work)
            if "work-ledger" in selected:
                scenario_work_ledger(api, offerings, work)
            processes.stop(child)
        if "faults" in selected:
            for scenario in [name for name in FAULTS if not args.faults or name in args.faults.split(",")]:
                child, api = start_stack(processes, stack_binary, work, "fault-" + scenario, url_catalog, FAULT_KEYS.get(scenario, f"fake-{scenario}"))
                offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
                scenario_fault(api, offerings, scenario)
                processes.stop(child)
        check_no_real_upstream(work)
    finally:
        processes.stop_all()
        if not args.keep and not args.work:
            shutil.rmtree(work, ignore_errors=True)
    failed = [name for name, ok in results if not ok]
    print(f"# {len(results) - len(failed)} passed, {len(failed)} failed")
    return 1 if failed or not results else 0


if __name__ == "__main__":
    sys.exit(main())
