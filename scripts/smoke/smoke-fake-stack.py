#!/usr/bin/env python3
"""Fake-upstream stack smoke (issue #96): zero cost, loopback only, offline.

One command starts the fake upstream (cmd/fake-upstream), then a Plinth stack in
live mode whose Tripo, Meshy, Hyper3D and LLM base URLs point at it, and drives the
same HTTP API the browser uses:

  * the Tripo, Meshy and Hyper3D balances show up in the provider balance panel;
  * Meshy text-to-3d (preview + refine) -> remesh -> rigging -> animation, and
    text-to-3d -> retexture / convert, and Hyper3D Rodin text-to-3d -> Bang and
    Rodin image-to-3d with five pictures, run to SUCCEEDED with the same file
    checks as the Tripo chains, and the delivered Rodin pack follows its request
    (#303): the display request's 6144 base color, a game character's 17,440
    triangles for 8000 Quad faces and 2048 textures for texture_mode high;
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
  * the external MCP listener (#332) runs the whole generation loop with an
    access key and no cookie or browser: upload -> image-to-3D -> poll -> the
    twelve delivered files with their SHA-256 -> keyed and signed downloads ->
    inspect_model -> remesh, with the status block's step counts unchanged by
    reads and replays (the "mcp" group);
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
    Meshy and Hyper3D delivers it;
  * the production list workflow (#327) proposes a draft through the chat tool, edits
    it under expected_version, confirms and replaces it, rejects another, expires a
    one-second draft, keeps an unavailable route with its reason, plans no 3D step for
    a 2D-only list, and answers a foreign owner with 404 (the "asset-list" group,
    three private stacks).
  * the scene decomposition flow (#331) cuts two regions out of a synthetic scene
    picture at original resolution, pixel-compares them with the source, proves a
    repeated rectangle shares one asset across calls and turns, refuses invalid
    regions without storing anything, re-cuts the merged union region through an
    edit's crop instruction, proposes with reference_input_asset_ids and confirms
    the list onto the board, all with no Task, step or charge (the
    "asset-list-crops" group).
  * the direction card (#374, kind next_step) under a fixed function: the session
    fixes a function the way the Harness panel saves it, the Agent still opens a
    two-option card whose first option clears the selection, a tool outside the
    function (update_production_brief) is not offered and writes nothing, the
    page's order (clear the selection with the latest revision, then answer the
    card) keeps the enabled operations, marks the card answered and hands the
    chosen direction to the Agent, and the same call then runs (the "next-step"
    group).
  * the acceptance board (#329): both items of a confirmed list reach review
    with their snapshot, one item is accepted (project asset registered) and
    the other is returned with a declared reason, the returned item reworks in
    its own sub-session to round two, and the batch ZIP then packs both
    accepted items with a manifest and a README whose bytes match every
    snapshot digest; only accepted items are ever packed, and a foreign owner
    reads and decides nothing (the "asset-list-review" group).

Usage: python3 scripts/smoke/smoke-fake-stack.py [--only NAME[,NAME]] [--keep] [--work DIR]
(NAME: balance, chain, meshy, hyper3d, hyper3d-agent, images, matting-faults, tripo, p2, project, agent, agent-background,
agent-quality, image-self-check, image-purpose, next-step, self-check, offering-knowledge, work-ledger, vision-budget, visual-anchors, plan-checkpoint, preferences, mcp, asset-list, asset-list-crops, asset-list-run, asset-list-review, faults;
"mcp" runs the external MCP listener's generation loop on the main stack (access key, upload, image-to-3D, the twelve
files, keyed and signed downloads, inspect_model, remesh); the listener binds loopback port 19413
(FAKE_STACK_MCP_PORT) and the run still proves zero real upstream requests;
"matting-faults" the #301 matting fault paths chosen by the uploaded pictures themselves (the only way a deployed
stack reaches them): #284's notice naming the pictures that were not matted, #299's out-of-memory batch as a
partial success and as a capacity failure, and an unmarked batch (scripts/smoke/fake_faults.py,
scripts/smoke/smoke-fake-faults.py runs the same checks on a fake-shaped dev-test);
"plan-checkpoint" the #251 budget plan checkpoint: a failed inherited check pauses the next step,
the declared contingency is inserted within the approved budget, and no second approval card appears;
"preferences" the #241 session choices: four candidates, pick/reject with reasons, the next prompt carrying them,
the framed block surviving compaction, edit/delete effects, and a second owner's 404;
"agent" runs both agent scenarios, "hyper3d-agent" the Hyper3D chain alone,
"p2" the Tripo P2 quote/chain, "agent-background" the batched wait that keeps both Tasks running,
"project" the #240 project inheritance: promote a finished picture, new session, reused style reference,
"image-purpose" the #239 2D purpose card, brief, purpose-driven model and 2D lock,
"next-step" the #374 direction card under a fixed function: the card opens, a tool outside the function
is not offered, clearing the selection and answering the card follow the page's order and give every tool back,
"offering-knowledge" the #249 knowledge projection, derived model list, purpose coverage,
over-limit refusal and final_prompt report,
"work-ledger" the #248 frozen prompts, compaction, style anchor and self-check leftovers,
"self-check" the #246 persisted 3D self-check contract: reference comparison, records,
limits, corrections, compaction and background results,
"vision-budget" the #247 image detail, contact sheet and vision token accounting,
"visual-anchors" the #250 system-prompt visibility, confirmation card, priority trim and retexture style reference,
"mask-selection" the #244 frozen selection mask, mask binding, approval-card note,
mask-unsupported refusal and create_selection_mask;
"asset-list-review" the #329 acceptance board: both items reach review with their snapshot,
one is accepted and one returned with a declared reason, the returned item reworks in its own
sub-session to round two, and the batch ZIP packs only accepted items with a manifest, a README
and bytes matching every snapshot digest, all on a private stack with execution turned on)
Needs `go`. Exit 0 = every selected check passed.

The stack runs inside the internal/app test binary (TestFakeUpstreamStack, built with -tags fakestack), not
cmd/art-ai: Tripo's native processing only reads source models from public HTTPS
hosts, and the test binary maps one reserved ".test" host to the loopback fake
instead of loosening that rule. Nothing here touches dev-test, formal-test or a
real upstream. Ports stay away from 18631-18635 (the #62 local stacks); the
external MCP listener defaults to 19413 (FAKE_STACK_MCP_PORT).
"""
import argparse
import base64
import datetime
import hashlib
import http.cookiejar
import json
import os
import secrets
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zlib

# The gate runs this script and then checks the tree is unchanged: importing the sibling module must not
# leave a __pycache__ behind (this script had no sibling import before #301).
sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fake_faults  # noqa: E402  (sibling module; the script runs from any directory)
import mcp_client  # noqa: E402  (the external MCP listener's wire client, #332)

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
FAKE_PORT = int(os.environ.get("FAKE_STACK_FAKE_PORT", "19410"))
STACK_PORT = int(os.environ.get("FAKE_STACK_PORT", "19411"))
# The external MCP listener (#332) is another listener of the same stack; keep it
# off 19410/19411/19412 and the 18631-18635 local stacks.
MCP_PORT = int(os.environ.get("FAKE_STACK_MCP_PORT", str(STACK_PORT + 2)))
ASSET_HOST = "fake-assets.example.test"  # keep in sync with internal/app/fake_upstream_stack_test.go
GOOD_KEY = "fake-test-key"
# The external MCP listener authenticates a key against the user directory, so
# the key's owner must be a persisted, active user; the development mock
# identity is not one. The stack bootstraps this local administrator for the
# "mcp" scenario. The password is random per run and lives only in the stack's
# environment, never in the source tree.
MCP_OWNER_USERNAME = "fake-stack-mcp-owner"
MCP_OWNER_PASSWORD = "fake-stack-" + secrets.token_urlsafe(18)

TEXT_TO_3D = "tripo/tripo-v3.1-full/text-to-3d"
MESHY_TEXT_TO_3D = "meshy/meshy-7.1/text-to-3d"
MESHY_IMAGE_TO_3D = "meshy/meshy-7.1/image-to-3d"
MESHY_REMESH = "meshy/processing/remesh"
MESHY_RESIZE = "meshy/processing/resize"
MESHY_RETEXTURE = "meshy/processing/retexture"
MESHY_CONVERT = "meshy/processing/convert"
MESHY_RIGGING = "meshy/processing/rigging"
MESHY_ANIMATION = "meshy/processing/animation"
# The fake's image-to-3D answer (#332): six model formats (glb, obj, fbx, stl,
# usdz, 3mf), the textured OBJ's MTL, four PBR maps (base_color, metallic,
# normal, roughness) and one thumbnail.
MCP_IMAGE_TO_3D_ASSET_COUNT = 12
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


FAKE_CONTROL = f"http://127.0.0.1:{FAKE_PORT}"


def fake_control(method="GET", path="/fake/control/chat-requests"):
    """Read or clear the fake's own control plane (never a real upstream)."""
    request = urllib.request.Request(FAKE_CONTROL + path, method=method)
    try:
        response = urllib.request.urlopen(request, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    raw = response.read()
    return getattr(response, "status", None) or response.code, raw


def fake_chat_requests():
    status, raw = fake_control()
    if status != 200:
        return []
    try:
        return json.loads(raw).get("requests", [])
    except ValueError:
        return []


def last_request_with(predicate):
    """The newest recorded chat request whose messages satisfy predicate."""
    for request in reversed(fake_chat_requests()):
        if predicate(request.get("messages") or []):
            return request
    return None


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


def require_free(port, what, env_hint="FAKE_STACK_FAKE_PORT / FAKE_STACK_PORT"):
    """Refuse a port something else already listens on: a health answer from a
    foreign process must never be taken for our own instance."""
    if port_busy(port):
        raise RuntimeError(f"port {port} for the {what} is already in use; stop that process or set {env_hint}")


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


def private_catalog(work, jimeng_urls=False, disabled=(), prompt_limits=None, self_check=False, name="catalog.json", manual_image_edits=False, preferences=True, preference_compaction=False, draft_ttl_seconds=None, execution_enabled=None, poll_interval_milliseconds=None, presence_heartbeat_seconds=None, presence_grace_seconds=None, max_parallel_items=None, max_rounds_per_item=None, continuation_message=None, rework_message=None, plan_continuation_message=None):
    """The default Catalog with four changes for Tripo, Meshy and Hyper3D: the fake
    artifact host is an allowed asset host, they poll once a second, and a failing
    materialization is retried once. The matting service polls once a second too,
    and the Jimeng Offerings accept the fake artifact host. With jimeng_urls the
    Jimeng Offerings also ask for a signed picture URL instead of an inline picture
    (the Schema fixes response_format to b64_json, so a URL, and with it a
    missing, corrupt or expired URL, never occurs in production); the fault runs
    use that catalog. The background-wait threshold drops to one second so a test
    Task reaches it while the fake still polls; the shipped value stays untouched.
    draft_ttl_seconds shortens the production list's draft TTL (#327), so the
    expiry arm does not wait the shipped week. The execution_* and continuation
    options override application.asset_list.execution (#328), which is disabled
    in the shipped Catalog: a smoke run turns it on and makes polling, presence
    and the per-item round limit short enough to observe, and replaces the
    continuation text with the fake script marker that drives each item.
    rework_message replaces the returned item's rework copy (#329b) the same
    way, so the second round is driven by the fake too. The default Catalog
    file and its base URLs stay untouched."""
    with open(os.path.join(REPO, "internal/catalog/default_catalog.json"), encoding="utf-8") as handle:
        catalog = json.load(handle)
    catalog["application"]["chat"]["agent"]["background_wait"]["after_seconds"] = 1
    # #241: a preferences-off Catalog disables the feature server-side (no
    # injection, no writes), which is the "before" arm of the #238 comparison.
    # The preferences scenario keeps only one recent round so a manual
    # compaction really has something to summarise.
    if not preferences:
        catalog["application"]["chat"]["preferences"]["maximum_entries"] = 0
    if preference_compaction:
        catalog["application"]["chat"]["context_compaction"]["keep_recent_rounds"] = 1
    if draft_ttl_seconds is not None:
        catalog["application"]["asset_list"]["limits"]["draft_ttl_seconds"] = draft_ttl_seconds
    execution = catalog["application"]["asset_list"]["execution"]
    if execution_enabled is not None:
        execution["enabled"] = execution_enabled
    if poll_interval_milliseconds is not None:
        execution["poll_interval_milliseconds"] = poll_interval_milliseconds
    if presence_heartbeat_seconds is not None:
        execution["presence"]["heartbeat_seconds"] = presence_heartbeat_seconds
    if presence_grace_seconds is not None:
        execution["presence"]["grace_seconds"] = presence_grace_seconds
    if max_parallel_items is not None:
        execution["max_parallel_items"] = max_parallel_items
    if max_rounds_per_item is not None:
        execution["max_rounds_per_item"] = max_rounds_per_item
    if continuation_message is not None:
        execution["continuation"]["message"] = continuation_message
    if rework_message is not None:
        catalog["application"]["asset_list"]["review"]["messages"]["rework_message"] = rework_message
    if plan_continuation_message is not None:
        # #410: the go-ahead of an approved plan is the last user message of its
        # turn, so the fake marker in it drives the step the plan continues with.
        catalog["application"]["chat"]["approval_policy"]["budget"]["continuation"]["message"] = plan_continuation_message
    if manual_image_edits:
        # The mask scenario needs an approval card for a 2D edit step (#244);
        # the shipped policy asks a human only for the listed capabilities.
        policy = catalog["application"]["chat"]["approval_policy"]
        for capability in ("image-to-image", "text-to-image"):
            if capability not in policy["manual_approval_capabilities"]:
                policy["manual_approval_capabilities"].append(capability)
    for offering in catalog["offerings"]:
        provider = offering.get("provider_id")
        if offering.get("id") in disabled:
            offering["enabled"] = False
        if prompt_limits and offering.get("id") in prompt_limits:
            for parameter in offering.get("parameters", []):
                if parameter.get("semantic") == "prompt":
                    parameter["max_length"] = prompt_limits[offering["id"]]
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
    if disabled:
        # The Catalog refuses a legacy model projection whose capability lost
        # its last enabled Offering. Apply the Catalog's own counting rule
        # (enabled; a native-priced Offering counts only when its model
        # declares native_pricing) and drop the projections and models that no
        # enabled Offering serves, so an arm that disables a whole capability
        # still loads. A model id another enabled Offering keeps (the
        # offering-knowledge arm's Tripo P2) stays untouched.
        discovery = catalog["application"].get("provider_discovery") or {}
        models = discovery.get("legacy_models") or []
        configured = {model.get("model_id"): model for model in models}
        enabled_models = {}
        for offering in catalog["offerings"]:
            if not offering.get("enabled", True):
                continue
            model_id = offering.get("model_id")
            if offering.get("native_pricing") is not None and not (
                    model_id in configured and configured[model_id].get("native_pricing")):
                continue
            enabled_models.setdefault(model_id, set()).add(offering.get("capability"))
        kept = []
        for model in models:
            capabilities = enabled_models.get(model.get("model_id"))
            if not capabilities:
                continue
            for field in ("parameter_projections", "constraint_projections"):
                projections = model.get(field) or {}
                model[field] = {capability: value for capability, value in projections.items()
                                if capability in capabilities}
            kept.append(model)
        discovery["legacy_models"] = kept
    if self_check:
        chat = catalog["application"]["chat"]
        chat["self_check"]["limits"] = {
            "max_rounds_per_result": 2,
            "max_corrections_per_result": 1,
            "max_renders_per_round": 2,
        }
        chat["self_check"]["reference"]["size"] = 256
    path = os.path.join(work, "catalog-urls.json" if jimeng_urls else name)
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
        # The bootstrap administrator owns the "mcp" scenario's access key (the
        # listener checks the key's owner in the user directory).
        "ADMIN_USERNAME": MCP_OWNER_USERNAME, "ADMIN_PASSWORD": MCP_OWNER_PASSWORD,
        "APP_ADDR": f"127.0.0.1:{STACK_PORT}", "DATABASE_PATH": os.path.join(root, "app.db"),
        # The external MCP listener (#332) is loopback-only, like everything else.
        "MCP_PUBLIC_LISTEN_ADDR": f"127.0.0.1:{MCP_PORT}",
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
    require_free(MCP_PORT, "external MCP listener", "FAKE_STACK_MCP_PORT")
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

def new_session(api, project_id=""):
    body = {"title": ""}
    if project_id:
        body["project_id"] = project_id
    return api.json("POST", "/api/chat/sessions", body)[1]["id"]


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


def chat_context_facts(api):
    """The public Schema's context response members and session-context route, read
    once like the browser reads them (#247)."""
    if not hasattr(api, "chat_context_facts"):
        status, schema = api.json("GET", "/api/v1/schema")
        schema = schema.get("schema", schema) if status == 200 and isinstance(schema, dict) else {}
        response_fields = ((schema.get("chat") or {}).get("api") or {}).get("response_fields") or {}
        routes = ((schema.get("runtime") or {}).get("http") or {}).get("routes") or {}
        api.chat_context_facts = (response_fields, routes.get("chat_session_context") or "")
    return api.chat_context_facts


def submit(api, offering, capability, payload, session, style_lock_skipped=False, reasons=None):
    """prepare -> grant (-> human approval) -> submit. Returns (task_id, why).
    style_lock_skipped is the direct prepare's one-turn exemption (#187); reasons
    explains each advised parameter the request changed (#295)."""
    body = {"session_id": session, "capability": capability, "offering_id": offering, "input": payload}
    if style_lock_skipped:
        body["style_lock_skipped"] = True
    if reasons:
        body["parameter_reasons"] = reasons
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


def run_step(api, offerings, label, offering, capability, payload, session, expect_state="SUCCEEDED", style_lock_skipped=False,
             reasons=None):
    task_id, why = submit(api, offering, capability, payload, session, style_lock_skipped=style_lock_skipped, reasons=reasons)
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


def make_rgba_picture(width, height, transparent=None):
    """A deterministic RGBA PNG: opaque white everywhere except the transparent
    rectangle. This is the selection mask the tests upload (#244)."""

    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            clear = transparent is not None and (transparent[0] <= x < transparent[2] and transparent[1] <= y < transparent[3])
            row.extend((0, 0, 0, 0) if clear else (255, 255, 255, 255))
        rows.append(b"\x00" + bytes(row))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
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
        # #277: another preset of the biped rig, and a quadruped skeleton with its own preset (the request
        # values are constructed from the official pages; the fake does not tell a quadruped model apart).
        run_step(api, offerings, "tripo animation (run)", ANIMATION, "animation",
                 {"source_task": rig["id"], "animation_action": "preset:run"}, session)
    quad = run_step(api, offerings, "tripo rigging (quadruped)", RIGGING, "rigging",
                    {"source_task": gen["id"], "rig_type": "quadruped", "authorize_extra_credits": True}, session)
    if quad and quad.get("state") == "SUCCEEDED":
        run_step(api, offerings, "tripo animation (quadruped walk)", ANIMATION, "animation",
                 {"source_task": quad["id"], "animation_action": "preset:quadruped:walk"}, session)
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
        # Smart Segmentation of a model takes a library model file, so a binary GLB is uploaded:
        # the Meshy text-to-3D delivers one (the Tripo fake's GLTF export is a GLB too since #293,
        # but this step does not depend on which task it came from).
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


def step(api, offerings, label, offering, capability, base, session, style_lock_skipped=False, reasons=None):
    return run_step(api, offerings, label, offering, capability, payload_for(offerings[offering], base), session,
                    style_lock_skipped=style_lock_skipped, reasons=reasons)




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


def glb_summary(data):
    """The triangles and the edge of every embedded PNG of a GLB, read from its own
    bytes (the index accessors and the PNG headers) the way Plinth's checks read a
    delivered model. None when the bytes are not a GLB 2.0 with PNG images."""
    if len(data) < 20:
        return None
    magic, version, _total, json_length, chunk = struct.unpack_from("<4sIII4s", data)
    if magic != b"glTF" or version != 2 or chunk != b"JSON":
        return None
    document = json.loads(data[20:20 + json_length])
    binary = 20 + json_length + 8
    triangles = 0
    for mesh in document.get("meshes", []):
        for primitive in mesh.get("primitives", []):
            if primitive.get("mode", 4) != 4:
                continue
            accessor = primitive.get("indices", primitive["attributes"]["POSITION"])
            triangles += document["accessors"][accessor]["count"] // 3
    edges = []
    for image in document.get("images", []):
        offset = binary + document["bufferViews"][image["bufferView"]].get("byteOffset", 0)
        header = data[offset:offset + 24]
        if header[:8] != b"\x89PNG\r\n\x1a\n":
            return None
        width, height = struct.unpack(">II", header[16:24])
        edges.append(max(width, height))
    return triangles, edges


# The most triangles the fake's sized model carries (fakeupstream.MaxSizedModelTriangles,
# #303: the humanoid fixture cut into 41 x 41 squares); a Rodin request for more is
# held at it. Keep in sync with internal/testutil/fakeupstream/sizedmodel.go.
FAKE_MAX_MODEL_TRIANGLES = 121032


def check_rodin_pack(api, label, task, want):
    """The delivered PBR pack follows the request (#303): its triangles and the edge of
    every embedded texture, read back through the stack. Returns the model's bytes."""
    model = next((a for a in (task or {}).get("assets") or [] if a.get("role") == "model" and a.get("variant") == "glb"), None)
    status, raw, _headers = api.call("GET", model["url"]) if model else (0, b"", {})
    got = glb_summary(raw) if status == 200 else None
    record(label, got == want, f"model (triangles, texture edges) {got}, want {want}")
    return raw if status == 200 else None


def scenario_hyper3d(api, offerings):
    session = new_session(api)
    # The display request (the Offering's defaults: Raw, 2,000,000 faces, texture_mode
    # extreme-high) delivers the 6144 base color beside two 2048 maps and the fake's
    # triangle ceiling. Such a model is too large to be uploaded as a Texture source
    # (static-glb-v1 takes textures up to 4096), as with the real service.
    display = step(api, offerings, "hyper3d rodin text-to-3d (display request)", HYPER3D_TEXT_TO_3D, "text-to-3d",
                   {"prompt": "a plain cube"}, session)
    if display and display.get("state") == "SUCCEEDED":
        check_rodin_pack(api, "hyper3d rodin display pack: 6K base color", display, (FAKE_MAX_MODEL_TRIANGLES, [6144, 2048, 2048]))
    # The game character the way the phone budget asks for it: 8000 Quad faces (2.18
    # triangles each), the 2K texture mode, the pose and bake switches. The three
    # parameters that leave their advised defaults carry a reason, as a purpose gives
    # it (#295).
    reason = "the game character purpose asks for it"
    character = step(api, offerings, "hyper3d rodin text-to-3d (game character)", HYPER3D_TEXT_TO_3D, "text-to-3d",
                     {"prompt": "a game character standing in an A pose", "mesh_mode": "Quad", "quality_override": 8000,
                      "texture_mode": "high", "TAPose": True, "quad_normal": True}, session,
                     reasons={"mesh_mode": reason, "quality_override": reason, "texture_mode": reason})
    if character and character.get("state") == "SUCCEEDED":
        check_rodin_pack(api, "hyper3d rodin game character pack: 17,440 triangles, 2K textures", character,
                         (17440, [2048, 2048, 2048]))
    # The chain's model: the 2K texture mode, small enough for the Texture upload.
    gen = step(api, offerings, "hyper3d rodin text-to-3d", HYPER3D_TEXT_TO_3D, "text-to-3d",
               {"prompt": "a plain cube", "texture_mode": "high"}, session,
               reasons={"texture_mode": "the model is chained into the Texture step, whose upload takes textures up to 4096"})
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
        raw = check_rodin_pack(api, "hyper3d rodin pack: 2K textures", gen, (FAKE_MAX_MODEL_TRIANGLES, [2048, 2048, 2048]))
        model_id = upload_asset(api, "model.glb", raw, "model/gltf-binary") if raw else None
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


def scenario_matting_faults(api, offerings):
    """#301: the matting paths only a fault reaches, chosen by the uploaded pictures (the way a
    deployed dev-test stack must, scripts/smoke/smoke-fake-faults.py): #284's notice naming the
    pictures that were not matted, #299's out-of-memory batch as a partial success and as a capacity
    failure, and an unmarked batch that says nothing. The public Task is held against the Schema's own
    sentences; the stack's Adapter sends no test header and its key selects no scenario."""
    if MATTING not in offerings:
        record("matting-faults: the matting Offering is registered", False, f"{MATTING} is not in the public Offerings")
        return
    fake_faults.run(api, record)


def mcp_wait_task(mcp, task_id, seconds=90):
    """Poll get_task with the listener's suggested interval (or five seconds)
    until the view is terminal; returns (task view, last tool result)."""
    deadline = time.time() + seconds
    view, last = {}, {}
    while time.time() < deadline:
        last = mcp.tool("get_task", {"task_id": task_id})
        task = last["structured"].get("task")
        view = task if isinstance(task, dict) else {}
        if view.get("terminal"):
            return view, last
        try:
            wait = float(view.get("poll_after_seconds") or 5)
        except (TypeError, ValueError):
            wait = 5
        time.sleep(max(0.2, min(wait, 5)))
    return view, last


def mcp_tasks_wait(client, task_id, seconds=90):
    """Poll tasks/get until the Tasks extension status is terminal; returns the
    last result object (the task status and, once completed, the tool result)."""
    deadline = time.time() + seconds
    last = {}
    while time.time() < deadline:
        status, _headers, raw = client.call("tasks/get", {"taskId": task_id})
        payload = {}
        try:
            payload = json.loads(raw)
        except (TypeError, ValueError):
            payload = {}
        last = (payload.get("result") or {}) if isinstance(payload, dict) else {}
        if last.get("status") in ("completed", "failed", "cancelled"):
            return last
        try:
            wait = float(last.get("pollIntervalMs") or 1000) / 1000.0
        except (TypeError, ValueError):
            wait = 1
        time.sleep(max(0.2, min(wait, 5)))
    return last


def mcp_downloads(client, assets):
    """Fetch every asset's bytes; keyed by asset id, never by URL (a signed URL
    is a credential until it expires)."""
    blobs = {}
    for asset in assets:
        status, headers, data = client.get(asset.get("download_url") or "")
        blobs[str(asset.get("id") or "")] = {"status": status, "bytes": data, "headers": headers}
    return blobs


def mcp_tampered_token(url):
    """The same signed address with the last token character changed. The last
    base64 digit carries the signature's final bits, so flipping its top two
    bits always changes the signed bytes (changing only the padding bits would
    not)."""
    head, _separator, token = url.rpartition("/")
    if not token:
        return url
    alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
    index = alphabet.find(token[-1])
    replacement = alphabet[index ^ 0b110000] if index >= 0 else "A"
    return head + "/" + token[:-1] + replacement


def mcp_rfc3339(value):
    """True when the value is an RFC 3339 timestamp with a zone."""
    if not isinstance(value, str):
        return False
    try:
        parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    return parsed.tzinfo is not None


def scenario_mcp(api, mcp_base):
    """The external MCP listener (#332): a personal access key, no browser.

    The stack's bootstrap administrator only signs in to issue the key (a
    person does that in the account settings); everything after it carries
    nothing but the Bearer key, and the signed downloads carry no credential at
    all. The listener is on loopback, and the run still fails on any
    non-loopback upstream request."""
    base = mcp_base.rstrip("/")
    keyless = mcp_client.McpClient(base)
    status, headers, _raw = keyless.call("tools/list", {})
    record("mcp tools/list without a key is 401", status == 401 and "Bearer" in mcp_client.header(headers, "WWW-Authenticate"),
           f"status {status}")

    # The key's owner must be a persisted user (the listener checks it), which
    # the development mock identity is not. A separate client signs in as the
    # stack's bootstrap administrator, so the other scenarios keep their mock
    # cookie.
    owner = Api(api.base)
    status, _signed_in = owner.json("POST", "/api/auth/login",
                                    {"username": MCP_OWNER_USERNAME, "password": MCP_OWNER_PASSWORD})
    record("mcp access key owner signs in", status == 200, f"status {status}")
    status, created = owner.json("POST", "/api/v1/account/access-keys",
                                 {"name": "fake stack mcp", "scopes": ["generate"], "ttl_days": 1})
    key = (created or {}).get("key") or ""
    record("mcp access key issued", status == 201 and key.startswith("plk_"), f"status {status}")
    if not key:
        return
    mcp = mcp_client.McpClient(base, key)

    status, _headers, raw = mcp.call("tools/list", {})
    names = set()
    try:
        names = {tool.get("name") for tool in (json.loads(raw).get("result") or {}).get("tools") or []}
    except (AttributeError, TypeError, ValueError):
        pass
    wanted = {"upload_image", "create_model", "process_model", "get_task", "get_task_assets", "inspect_model", "get_status"}
    record("mcp tools/list lists the generated-work tools", status == 200 and wanted <= names, f"status {status}, {len(names)} tools")

    status, _headers, raw = mcp.call("server/discover", {})
    discover_capabilities = {}
    try:
        discover_capabilities = (json.loads(raw).get("result") or {}).get("capabilities") or {}
    except (TypeError, ValueError):
        discover_capabilities = {}
    extensions = discover_capabilities.get("extensions") or {}
    record("mcp discover advertises the Tasks extension",
           status == 200 and mcp_client.TASKS_EXTENSION in extensions,
           f"status {status}, extensions {sorted(extensions)}")

    upload = mcp.tool("upload_image", {"data": base64.b64encode(make_png(512)).decode(), "filename": "mcp-fake-stack.png"})
    image = upload["structured"].get("image") or {}
    image_id = image.get("id") or ""
    record("mcp upload_image stores a 512x512 PNG",
           not upload["is_error"] and bool(image_id) and image.get("media_type") == "image/png",
           f"status {upload['http_status']}, media {image.get('media_type')}")
    if not image_id:
        return

    asset = "mcp-fake-stack"
    idem = "mcp-" + uuid.uuid4().hex
    started = mcp.tool("create_model", {"images": [image_id], "asset": asset, "idempotency_key": idem})
    data = started["structured"]
    block = data.get("status") or {}
    service = data.get("service") or {}
    task_id = data.get("task_id") or ""
    balances = {entry.get("provider") for entry in block.get("balances") or [] if isinstance(entry, dict)}
    record("mcp create_model starts a counted image-to-3D step",
           not started["is_error"] and bool(task_id) and data.get("step_counted") is True
           and service.get("id") == MESHY_IMAGE_TO_3D and block.get("step_mode") == "agent"
           and (block.get("asset") or {}).get("used_steps") == 1 and block.get("total_used_steps") == 1
           and balances == {"meshy", "tripo", "hyper3d"},
           f"status {started['http_status']}, service {service.get('id')}, "
           f"steps {(block.get('asset') or {}).get('used_steps')}, balances {sorted(balances)}")
    if not task_id:
        return

    replay = mcp.tool("create_model", {"images": [image_id], "asset": asset, "idempotency_key": idem})
    replay_data = replay["structured"]
    replay_block = replay_data.get("status") or {}
    record("mcp create_model replay is the same task without another step",
           not replay["is_error"] and replay_data.get("task_id") == task_id and replay_data.get("replayed") is True
           and (replay_block.get("asset") or {}).get("used_steps") == 1,
           f"status {replay['http_status']}, replayed {replay_data.get('replayed')}, "
           f"steps {(replay_block.get('asset') or {}).get('used_steps')}")

    conflict = mcp.tool("create_model", {"images": [image_id], "asset": asset + "-other", "idempotency_key": idem})
    conflict_data = conflict["structured"]
    conflict_error = conflict_data.get("error") or {}
    conflict_block = conflict_data.get("status") or {}
    record("mcp create_model with the same key and another asset is a conflict",
           conflict["is_error"] and conflict_error.get("code") == "idempotency_conflict"
           and conflict_block.get("total_used_steps") == 1,
           f"status {conflict['http_status']}, code {conflict_error.get('code')}, "
           f"steps {conflict_block.get('total_used_steps')}")

    view, _last = mcp_wait_task(mcp, task_id)
    record("mcp get_task reaches SUCCEEDED", view.get("state") == "SUCCEEDED", f"state {view.get('state')}")

    files = mcp.tool("get_task_assets", {"task_id": task_id, "signed": False})
    assets = files["structured"].get("assets") or []
    record("mcp get_task_assets lists the twelve image-to-3D files",
           files["structured"].get("complete") is True and len(assets) == MCP_IMAGE_TO_3D_ASSET_COUNT,
           f"complete {files['structured'].get('complete')}, {len(assets)} assets")

    blobs = mcp_downloads(mcp, assets)
    problems = mcp_client.asset_problems(assets, blobs)
    record("mcp keyed downloads match size and SHA-256", bool(assets) and not problems,
           f"{len(assets)} files" + ("" if not problems else "; " + "; ".join(problems[:5])))

    urls_keyed = bool(assets) and all("/mcp/tasks/" in (asset.get("download_url") or "") for asset in assets)
    record("mcp unsigned download addresses are keyed",
           urls_keyed and files["structured"].get("download_expires_at") is None,
           f"expires_at {files['structured'].get('download_expires_at')}")
    refused = [keyless.get(asset.get("download_url") or "")[0] for asset in assets]
    record("mcp unsigned downloads without a key are 401",
           bool(refused) and all(status == 401 for status in refused), f"{len(refused)} links, {sorted(set(refused))}")
    if assets:
        unknown = (assets[0].get("download_url") or "").rsplit("/", 1)[0] + "/asset_does_not_exist"
        status, _headers, _raw = mcp.get(unknown)
    else:
        status = 0
    record("mcp unknown asset download is 404", status == 404, f"status {status}")

    signed = mcp.tool("get_task_assets", {"task_id": task_id, "signed": True})
    signed_data = signed["structured"]
    signed_assets = signed_data.get("assets") or []
    urls_signed = bool(signed_assets) and all("/mcp/signed-assets/" in (asset.get("download_url") or "") for asset in signed_assets)
    record("mcp signed download addresses are short-lived",
           urls_signed and mcp_rfc3339(signed_data.get("download_expires_at")),
           f"{len(signed_assets)} links, expires_at {signed_data.get('download_expires_at')}")
    signed_blobs = mcp_downloads(keyless, signed_assets)
    signed_problems = mcp_client.asset_problems(signed_assets, signed_blobs)
    record("mcp signed downloads need no key and match SHA-256", bool(signed_assets) and not signed_problems,
           f"{len(signed_assets)} files" + ("" if not signed_problems else "; " + "; ".join(signed_problems[:5])))
    if signed_assets:
        status, _headers, _raw = keyless.get(mcp_tampered_token(signed_assets[0].get("download_url") or ""))
    else:
        status = 0
    record("mcp tampered signed token is 404", status == 404, f"status {status}")

    inspect = mcp.tool("inspect_model", {"task_id": task_id})
    inspected = inspect["structured"]
    report = inspected.get("report") or {}
    usage = inspected.get("usage_check") or {}
    checks = usage.get("checks") or []
    complete_checks = all(isinstance(item, dict) and bool(item.get("check")) and bool(item.get("status"))
                          and "actual" in item and isinstance(item.get("budget"), dict) for item in checks)
    record("mcp inspect_model reads the saved GLB",
           not inspect["is_error"] and isinstance(report.get("triangles"), (int, float)) and report["triangles"] > 0
           and bool(checks) and complete_checks and isinstance(inspected.get("status"), dict),
           f"status {inspect['http_status']}, triangles {report.get('triangles')}, checks {len(checks)}")

    status_result = mcp.tool("get_status", {})
    status_block = status_result["structured"].get("status") or {}
    record("mcp inspect_model and get_status count no step",
           not status_result["is_error"] and status_block.get("total_used_steps") == 1,
           f"total steps {status_block.get('total_used_steps')}")

    again = mcp.tool("get_task", {"task_id": task_id})
    assets_again = mcp.tool("get_task_assets", {"task_id": task_id})
    inspection_null = ((again["structured"].get("status") or {}).get("inspection") is None
                       and (assets_again["structured"].get("status") or {}).get("inspection") is None)
    record("mcp-only tasks have no platform inspection", inspection_null, "inspection is null on both reads")

    remesh_idem = "mcp-remesh-" + uuid.uuid4().hex
    remesh = mcp.tool("process_model", {"operation": "remesh", "source_task_id": task_id,
                                        "asset": asset, "idempotency_key": remesh_idem})
    remesh_data = remesh["structured"]
    remesh_block = remesh_data.get("status") or {}
    remesh_task_id = remesh_data.get("task_id") or ""
    record("mcp process_model remesh counts the second step",
           not remesh["is_error"] and bool(remesh_task_id) and remesh_data.get("step_counted") is True
           and (remesh_block.get("asset") or {}).get("used_steps") == 2,
           f"status {remesh['http_status']}, steps {(remesh_block.get('asset') or {}).get('used_steps')}")
    if not remesh_task_id:
        return

    remesh_view, _last = mcp_wait_task(mcp, remesh_task_id)
    record("mcp remesh reaches SUCCEEDED", remesh_view.get("state") == "SUCCEEDED", f"state {remesh_view.get('state')}")

    remesh_files = mcp.tool("get_task_assets", {"task_id": remesh_task_id})
    remesh_assets = remesh_files["structured"].get("assets") or []
    _status, offerings = api.json("GET", "/api/v1/offerings")
    offering = next((entry for entry in (offerings or {}).get("offerings") or [] if entry.get("id") == MESHY_REMESH), {})
    slots = mcp_client.required_slots(offering)
    delivered = {(entry.get("role"), entry.get("variant")) for entry in remesh_assets}
    missing = [slot for slot in slots
               if not any(role == slot[0] and (slot[1] is None or variant == slot[1]) for role, variant in delivered)]
    record("mcp remesh delivers every required slot",
           bool(slots) and remesh_files["structured"].get("complete") is True and not missing,
           f"{len(slots)} required slots, {len(remesh_assets)} files"
           + ("" if not missing else "; missing " + str(missing[:5])))
    remesh_blobs = mcp_downloads(mcp, remesh_assets)
    remesh_problems = mcp_client.asset_problems(remesh_assets, remesh_blobs)
    record("mcp remesh files match size and SHA-256", bool(remesh_assets) and not remesh_problems,
           f"{len(remesh_assets)} files" + ("" if not remesh_problems else "; " + "; ".join(remesh_problems[:5])))

    # The Tasks extension (#332 stage 2): a client that declares it receives a
    # CreateTaskResult from a generation tool and polls it with tasks/get;
    # tasks/cancel cancels a running task through the same generation kernel.
    tasks = mcp_client.McpClient(base, key, tasks=True)
    task_start = tasks.tool("generate_image", {"prompt": "tasks extension smoke", "asset": asset,
                                               "idempotency_key": "mcp-tasks-" + uuid.uuid4().hex})
    task_ref = task_start.get("result") or {}
    task_ref_id = task_ref.get("taskId") or ""
    record("mcp tools/call returns a CreateTaskResult to a Tasks client",
           not task_start["is_error"] and task_ref.get("resultType") == "task" and bool(task_ref_id)
           and task_ref.get("status") in ("working", "completed")
           and isinstance(task_ref.get("ttlMs"), (int, float))
           and isinstance(task_ref.get("pollIntervalMs"), (int, float)),
           f"status {task_start['http_status']}, type {task_ref.get('resultType')}, task {bool(task_ref_id)}")
    if task_ref_id:
        completed_task = mcp_tasks_wait(tasks, task_ref_id)
        completed_result = completed_task.get("result") or {}
        completed_structured = completed_result.get("structuredContent") or {}
        completed_view = completed_structured.get("task") or {}
        record("mcp tasks/get reaches completed with the get_task result",
               completed_task.get("status") == "completed" and completed_view.get("id") == task_ref_id
               and isinstance(completed_structured.get("status"), dict),
               f"status {completed_task.get('status')}, task {completed_view.get('id') == task_ref_id}")

    status, _headers, raw = tasks.call("tasks/get", {"taskId": "task_does_not_exist"})
    missing_code = None
    try:
        missing_code = ((json.loads(raw).get("error") or {}).get("code"))
    except (TypeError, ValueError):
        missing_code = None
    record("mcp tasks/get refuses an unknown task", status == 404 and missing_code == -32004,
           f"status {status}, code {missing_code}")

    # A 2D task can finish while the cancel request travels, so retry a few
    # times; at least one cancellation must find the task still running.
    cancel_detail, cancel_id = {}, ""
    for _attempt in range(3):
        started_cancel = tasks.tool("generate_image", {"prompt": "cancel me", "asset": asset,
                                                       "idempotency_key": "mcp-cancel-" + uuid.uuid4().hex})
        cancel_id = (started_cancel.get("result") or {}).get("taskId") or ""
        if not cancel_id:
            continue
        cancel_status, _headers, cancel_raw = tasks.call("tasks/cancel", {"taskId": cancel_id})
        if cancel_status != 200:
            break
        try:
            cancel_result = json.loads(cancel_raw).get("result") or {}
        except (TypeError, ValueError):
            cancel_result = {}
        cancel_detail = (cancel_result.get("_meta") or {}).get(mcp_client.TASKS_EXTENSION) or {}
        if cancel_detail.get("alreadyTerminal") is False:
            break
    record("mcp tasks/cancel cancels a running task",
           cancel_detail.get("status") == "cancelled" and cancel_detail.get("taskId") == cancel_id,
           f"status {cancel_detail.get('status')}, task {bool(cancel_id)}")
    if cancel_detail:
        canceled_view, _last = mcp_wait_task(mcp, cancel_id, seconds=30)
        record("mcp canceled task is CANCELED", canceled_view.get("state") == "CANCELED",
               f"state {canceled_view.get('state')}")
        status, _headers, raw = tasks.call("tasks/cancel", {"taskId": cancel_id})
        try:
            again = ((json.loads(raw).get("result") or {}).get("_meta") or {}).get(mcp_client.TASKS_EXTENSION) or {}
        except (TypeError, ValueError):
            again = {}
        record("mcp tasks/cancel on a terminal task says so",
               status == 200 and again.get("alreadyTerminal") is True,
               f"status {status}, already {again.get('alreadyTerminal')}")


def scenario_agent(api, offerings):
    """The L1 Agent equivalent: fake LLM -> tool call -> approval -> 3D task -> reply."""
    session = new_session(api)
    # A new session is in the auto mode (#406), where nobody approves the 3D step; this scenario is about the
    # approval card, so it puts the session in the strict mode first.
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "manual"})
    if status != 200:
        record("agent turn: switch the session to strict approval", False, f"approval mode {status}")
        return
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
    # Strict (manual) approval: the steps the shipped manual policy asks about wait for
    # their own card instead of a budget plan approving them automatically.
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


THREE_VIEW_IMAGE = IMAGE_OFFERINGS["openai"][1]


def _three_view_groups(api, session):
    body = api.json("GET", f"/api/v1/chat/sessions/{session}/three-views")[1]
    return body.get("items") or []


def _three_view_group(api, session):
    groups = _three_view_groups(api, session)
    return groups[0] if groups else None


def _wait_three_view(api, session, seconds=120):
    """Waits for the group's required views to finish (accepted or not)."""
    end = time.time() + seconds
    group = None
    while time.time() < end:
        group = _three_view_group(api, session)
        if group:
            slots = {slot.get("key"): slot for slot in group.get("slots") or []}
            required = [slot for slot in slots.values() if not slot.get("optional")]
            if required and all(slot.get("status") in ("succeeded", "failed", "rejected", "denied", "canceled")
                                for slot in required):
                return group
        time.sleep(0.3)
    return group


def _prepared_inputs(work, stack, session):
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        rows = db.execute("SELECT payload_json FROM generation_prepared_inputs WHERE session_id=?",
                          (session,)).fetchall()
        db.close()
    except sqlite3.Error:
        return []
    parsed = []
    for (payload,) in rows:
        try:
            parsed.append(json.loads(payload))
        except ValueError:
            continue
    return parsed


def _prepared_reference_assets(payload):
    normalized = payload.get("normalized_input")
    if isinstance(normalized, str):
        try:
            normalized = json.loads(normalized)
        except ValueError:
            return []
    return normalized.get("reference_assets") or [] if isinstance(normalized, dict) else []


def _prepared_size(payload):
    """The frozen output size of a prepared image step ("WxH"), or ""."""
    normalized = payload.get("normalized_input")
    if isinstance(normalized, str):
        try:
            normalized = json.loads(normalized)
        except ValueError:
            return ""
    return normalized.get("size") or "" if isinstance(normalized, dict) else ""


def _send_chat(api, session, body, seconds=45):
    """Sends one chat message, retrying while a continuation turn still owns it."""
    end = time.time() + seconds
    status, payload = 0, {}
    while time.time() < end:
        status, payload = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
        if status != 409:
            return status, payload
        time.sleep(0.5)
    return status, payload


def scenario_three_view(work, api, offerings):
    """#243: an approved budget finishes front, left and back without a click,
    feeds the front output into the later views and submits a P2 multiview 3D
    step; manual mode starts only the front view until the person asks for the
    next one. #283: the reference is a tall character (32x144 in a 96x192
    picture), so the frozen size of every view is a portrait one instead of the
    model's default square."""
    picture = upload_asset(api, "reference.png",
                           make_picture(96, 192, (255, 255, 255), [((32, 24, 64, 168), (52, 58, 70))]), "image/png")
    if not picture:
        record("three-view: upload the reference picture", False, "upload refused")
        return
    picture_url = f"/api/v1/assets/{picture}"
    plan = {
        "title": "三视图链路",
        "steps": [
            {"capability": "image-to-image", "purpose": "三视图正面"},
            {"capability": "image-to-image", "purpose": "三视图左侧"},
            {"capability": "image-to-image", "purpose": "三视图背面"},
            {"capability": "image-to-3d", "model": TRIPO_P2_MULTIVIEW_TO_3D, "purpose": "多视图生成 3D"},
        ],
        "budgets": [{"unit": "legacy.credits", "amount": 100}, {"unit": "tripo.credit", "amount": 200}],
    }
    session = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "budget"})
    if status != 200:
        record("three-view: budget approval mode", False, f"PUT {status}")
        return
    marker = "[[fake:tool:propose_production_plan " + json.dumps(plan, ensure_ascii=False, separators=(",", ":")) + "]]"
    status, _ = _send_chat(api, session, {"content": "按计划做三视图 " + marker,
                                           "creative_domain": "image", "ai_model": THREE_VIEW_IMAGE,
                                           "image_urls": [picture_url]})
    if status not in (200, 201, 202):
        record("three-view: propose the plan", False, f"send {status}")
        return
    budget_id, why = "", ""
    end = time.time() + 30
    while time.time() < end:
        pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
        plan_card = next((item for item in pending if item.get("kind") == "plan"), None)
        if plan_card:
            budget_id = plan_card.get("request_id") or ""
            break
        time.sleep(0.4)
    if not budget_id:
        record("three-view: budget card appears", False, "no plan card")
        return
    status, approved = api.json("PUT", f"/api/v1/chat/sessions/{session}/budgets/{budget_id}",
                                decision_body(api, "approve"))
    if status != 200 or approved.get("state") != "active":
        record("three-view: approve the plan", False, f"PUT {status} {approved}")
        return
    record("three-view: plan approved and active", True, f"budget {budget_id} state {approved.get('state')}")

    views = '[[fake:tool:create_three_view {"reference_image_indices":[1],"subject_type":"humanoid"}]]'
    status, _ = _send_chat(api, session, {"content": "生成三视图 " + views, "creative_domain": "image",
                                           "ai_model": THREE_VIEW_IMAGE, "image_urls": [picture_url]})
    if status not in (200, 201, 202):
        record("three-view: start the views", False, f"send {status}")
        return
    group = _wait_three_view(api, session)
    slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
    front = slots.get("front") or {}
    record("three-view: the budget finishes front, left and back without a click",
           bool(group) and group.get("complete") is True
           and slots.get("front", {}).get("status") == "succeeded"
           and slots.get("left", {}).get("status") == "succeeded"
           and slots.get("back", {}).get("status") == "succeeded"
           and slots.get("right", {}).get("status") == "not_started",
           f"complete={group.get('complete') if group else None} slots={ {k: v.get('status') for k, v in slots.items()} }")
    image_tasks = [task for task in session_tasks(api, session)
                   if task.get("capability") == "image-to-image" and task.get("state") != "CANCELED"]
    record("three-view: exactly three image tasks run, the optional right view does not",
           len(image_tasks) == 3, f"image tasks {[t.get('state') for t in image_tasks]}")
    pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
    record("three-view: no approval card is left pending", len(pending) == 0,
           f"pending {[(item.get('kind'), item.get('status')) for item in pending]}")
    front_asset = front.get("output_asset_id") or ""
    prepared = _prepared_inputs(work, "main", session)
    fed_front = {}
    for needle, key in (("clean left view", "left"), ("clean back view", "back")):
        match = next((item for item in prepared
                      if needle in json.dumps(item.get("normalized_input"), ensure_ascii=False)), None)
        assets = _prepared_reference_assets(match) if match else []
        fed_front[key] = bool(front_asset) and bool(assets) and assets[0] == front_asset
    record("three-view: left and back frozen inputs use the front output as their source",
           fed_front.get("left") and fed_front.get("back") and bool(front_asset),
           f"front asset {front_asset or 'none'}, left/back fed front {fed_front}")
    sizes = {}
    for needle, key in (("clean, neutral front view", "front"), ("clean left view", "left"),
                        ("clean back view", "back")):
        match = next((item for item in prepared
                      if needle in json.dumps(item.get("normalized_input"), ensure_ascii=False)), None)
        sizes[key] = _prepared_size(match) if match else ""

    def portrait(size):
        try:
            width, height = (int(part) for part in size.split("x"))
        except ValueError:
            return False
        return 0 < width < height

    record("three-view: a tall character's views are framed as portraits, not the default square",
           all(portrait(size) for size in sizes.values()), f"frozen sizes {sizes}")
    run_id = (group or {}).get("run_id") or ""

    # The accepted group feeds the multiview 3D step of the same plan.
    before = {task.get("id") for task in session_tasks(api, session)}
    submit = ('[[fake:tool:create_model_3d_from_views {"three_view_group":"' + run_id
              + '","model":"tripo-p2-multiview"}]]')
    status, _ = _send_chat(api, session, {"content": "用三视图生成 3D " + submit,
                                           "creative_domain": "model-3d", "ai_model": TRIPO_P2_MULTIVIEW_TO_3D,
                                           "domainModels": {"model-3d": TRIPO_P2_MULTIVIEW_TO_3D}})
    if status not in (200, 201, 202):
        record("three-view: submit the multiview 3D step", False, f"send {status}")
        return
    task = None
    end = time.time() + 60
    while time.time() < end and not task:
        for candidate in session_tasks(api, session):
            if candidate.get("id") in before or candidate.get("capability") != "image-to-3d":
                continue
            task = candidate
            break
        time.sleep(0.3)
    if task:
        task = wait_task(api, task.get("id"))
    record("three-view: the group submits a P2 multiview 3D task",
           bool(task) and task.get("state") == "SUCCEEDED" and task.get("offering_id") == TRIPO_P2_MULTIVIEW_TO_3D,
           f"task {task.get('id') if task else None} state {task.get('state') if task else None} "
           f"offering {task.get('offering_id') if task else None}")

    # Manual (strict) mode: only the front view starts by itself; the person
    # starts the left view, whose frozen input names the front output.
    strict = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{strict}/approval", {"approvalMode": "manual"})
    if status != 200:
        record("three-view strict: manual approval mode", False, f"PUT {status}")
        return
    status, _ = _send_chat(api, strict, {"content": "生成三视图 " + views, "creative_domain": "image",
                                          "ai_model": THREE_VIEW_IMAGE, "image_urls": [picture_url]})
    if status not in (200, 201, 202):
        record("three-view strict: start the front view", False, f"send {status}")
        return
    group = None
    end = time.time() + 60
    while time.time() < end:
        group = _three_view_group(api, strict)
        slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
        if slots.get("front", {}).get("task_id"):
            break
        time.sleep(0.3)
    slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
    front = slots.get("front") or {}
    record("three-view strict: only the front view starts by itself",
           bool(group) and front.get("status") in ("succeeded", "running") and
           slots.get("left", {}).get("status") == "not_started" and
           len([t for t in session_tasks(api, strict)
                if t.get("capability") == "image-to-image" and t.get("state") != "CANCELED"]) == 1,
           f"front {front.get('status')}, left {slots.get('left', {}).get('status')}")
    if front.get("task_id"):
        wait_task(api, front.get("task_id"))
    else:
        record("three-view strict: front task exists", False, "no front task")
        return
    # Strict mode has no server-side continuation: even after the front view
    # finished, left and back stay not started until the person clicks.
    group = _three_view_group(api, strict)
    slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
    strict_tasks = [t for t in session_tasks(api, strict)
                    if t.get("capability") == "image-to-image" and t.get("state") != "CANCELED"]
    record("three-view strict: the finished front does not start the next view",
           bool(group) and slots.get("left", {}).get("status") == "not_started" and
           slots.get("back", {}).get("status") == "not_started" and len(strict_tasks) == 1,
           f"front {slots.get('front', {}).get('status')}, left {slots.get('left', {}).get('status')}, "
           f"back {slots.get('back', {}).get('status')}, tasks {[t.get('state') for t in strict_tasks]}")
    view_route = f"/api/v1/chat/sessions/{strict}/three-views/{(group or {}).get('run_id')}/views/left"
    status, body = api.json("POST", view_route, {"action": "start", "attempt": 0})
    record("three-view strict: the person starts the left view", status == 200 and
           next((slot.get("status") for slot in body.get("group", {}).get("slots", [])
                 if slot.get("key") == "left"), "") in ("starting", "running", "succeeded"),
           f"POST {status}")
    end = time.time() + 90
    group = None
    while time.time() < end:
        group = _three_view_group(api, strict)
        slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
        if slots.get("left", {}).get("status") in ("succeeded", "failed", "rejected", "denied", "canceled"):
            break
        time.sleep(0.3)
    slots = {slot.get("key"): slot for slot in (group or {}).get("slots") or []}
    front, left = slots.get("front") or {}, slots.get("left") or {}
    record("three-view strict: the clicked left view succeeds and back never starts",
           left.get("status") == "succeeded" and slots.get("back", {}).get("status") == "not_started",
           f"left {left.get('status')}, back {slots.get('back', {}).get('status')}")
    prepared = _prepared_inputs(work, "main", strict)
    match = next((item for item in prepared
                  if "clean left view" in json.dumps(item.get("normalized_input"), ensure_ascii=False)), None)
    assets = _prepared_reference_assets(match) if match else []
    front_asset = (slots.get("front") or {}).get("output_asset_id") or ""
    record("three-view strict: the frozen left input contains the front output",
           bool(front_asset) and bool(assets) and assets[0] == front_asset,
           f"front {front_asset or 'none'} left source {assets[:1]}")


def running_replies(api, session):
    """#403: the assistant messages the two reads the browser uses (the whole list and the newest page) report as
    running, as {read: [(message id, stored tool call count)]}. The stored count tells a reply that already holds
    its first round apart from the placeholder."""
    found = {}
    for name, path in (("list", f"/api/chat/sessions/{session}/messages"),
                       ("page", f"/api/chat/sessions/{session}/messages?limit=50")):
        body = api.json("GET", path)[1]
        messages = body if isinstance(body, list) else body.get("messages", [])
        running = []
        for message in messages:
            if message.get("role") != "assistant" or message.get("isPending") is not True:
                continue
            raw = message.get("toolCalls") or "[]"
            try:
                calls = json.loads(raw) if isinstance(raw, str) else raw
            except ValueError:
                calls = []
            running.append((message.get("id"), len(calls) if isinstance(calls, list) else 0))
        found[name] = running
    return found


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
    # #403: the turn is parked on its Tasks after its first round stored the tool calls. Reading the messages now
    # must still report the reply as running; it used to read as finished, because only the placeholder text counted.
    parked = running_replies(api, session)
    record("agent background: the parked turn's reply is reported as running, with its stored tool calls (#403)",
           all(len(parked[name]) == 1 and parked[name][0][1] > 0 for name in ("list", "page")), str(parked))

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
    # #403: the mark goes away with the turn. The reply text is written first, so give the turn's end a moment.
    ended = running_replies(api, session)
    deadline = time.time() + 15
    while time.time() < deadline and any(ended.values()):
        time.sleep(0.3)
        ended = running_replies(api, session)
    record("agent background: the ended turn's reply is no longer reported as running (#403)",
           not any(ended.values()), str(ended))


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


def _choice_cards(api, session):
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/choices")
    if status != 200:
        return []
    return body.get("choices") or body.get("items") or []


def _latest_prepared_input(work, stack, session):
    """The session's newest frozen prepared input, or an empty object."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        row = db.execute("SELECT payload_json FROM generation_prepared_inputs "
                         "WHERE session_id=? ORDER BY created_at DESC, id DESC LIMIT 1", (session,)).fetchone()
        db.close()
    except sqlite3.Error:
        return {}
    try:
        return json.loads(row["payload_json"]) if row and row["payload_json"] else {}
    except ValueError:
        return {}


def _image_message(api, session, content, domain="image", image_urls=None):
    """Send one turn with a fake-LLM tool marker; wait for its final reply."""
    before = assistant_reply_count(api, session)
    body = {"content": content, "creative_domain": domain}
    if image_urls:
        body["image_urls"] = image_urls
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    if status not in (200, 201, 202):
        return False
    end = time.time() + 45
    while time.time() < end:
        if assistant_reply_count(api, session) > before:
            return True
        time.sleep(0.5)
    return False


def scenario_image_purpose(api, offerings, work):
    """#239: 2D purpose card, brief, purpose-driven model and parameters, the
    explicit 2D lock, and the 3D input checklist, all through the fake stack."""
    session = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
    if status != 200:
        record("#239 auto approval", False, f"PUT {status}")
        return

    # (a) a new 2D drawing without a brief opens the 2D purpose card, whose
    # options are exactly the declared image2d_usage profiles.
    card = '[[fake:tool:ask_choice {"kind":"image_usage_profile"}]]'
    if not _image_message(api, session, "画一张游戏角色立绘 " + card):
        record("#239 2D purpose card", False, "turn did not finish")
    else:
        cards = [item for item in _choice_cards(api, session) if item.get("kind") == "image_usage_profile"]
        _, schema = api.json("GET", "/api/v1/schema")
        schema = schema.get("schema", schema) if isinstance(schema, dict) else {}
        declared = [profile.get("id") for profile in
                    ((schema.get("chat") or {}).get("image2d_usage") or {}).get("profiles") or []]
        options = [option.get("id") for option in (cards[-1].get("options") or [])] if cards else []
        record("#239 2D purpose card", bool(cards) and options == declared,
               f"kind={cards[-1].get('kind') if cards else None} options={options} declared={declared}")

    # (b) the answer writes the 2D purpose into the brief.
    if not _image_message(api, session, "用途是角色立绘 [[fake:tool:update_production_brief "
                                        '{"image_use":"illustration","image_transparent":false}]]'):
        record("#239 2D brief write", False, "turn did not finish")
    else:
        status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/brief")
        brief = (body or {}).get("brief") or {}
        record("#239 2D brief write", status == 200 and brief.get("image_use") == "illustration",
               f"status={status} brief={brief}")

    # (c) refine_image follows the purpose: the recommended model and its
    # overrides are frozen into the input, and the result names model_choice.
    draw = ('[[fake:tool:refine_image {"prompt":"full-body cyberpunk female assassin, clean digital illustration, '
            'confident pose, transparent background, no text, no watermark"}]]')
    task = {}
    before_tasks = {item.get("id") for item in session_tasks(api, session)}
    if not _image_message(api, session, "开始画 " + draw):
        record("#239 2D purpose-driven generation", False, "turn did not finish")
    else:
        deadline = time.time() + 90
        while time.time() < deadline:
            fresh = [item for item in session_tasks(api, session) if item.get("id") not in before_tasks]
            if fresh:
                task = wait_task(api, fresh[0]["id"], 90)
                break
            time.sleep(0.5)
        task = task or {}
        prepared = _latest_prepared_input(work, "main", session)
        normalized = prepared.get("normalized_input") or {}
        used = task.get("offering_id")
        contents = _checkpoint_completed_contents(work, "main", session)
        reported = any('"model_choice"' in content and '"profile"' in content for content in contents)
        ok = (task.get("state") == "SUCCEEDED" and used == "gemini/gemini-3-pro-image/text-to-image"
              and normalized.get("aspect_ratio") == "3:4" and normalized.get("image_size") == "4K"
              and (prepared.get("model_choice") or {}).get("source") == "profile"
              and (prepared.get("model_choice") or {}).get("profile") == "illustration"
              and reported)
        record("#239 2D purpose-driven generation", ok,
               f"offering={used} state={task.get('state')} aspect_ratio={normalized.get('aspect_ratio')} "
               f"model_choice={prepared.get('model_choice')} result_model_choice={reported}")

    # (c2) #282: the fake picture is 64x64, far below the 1536 short edge of the
    # illustration purpose, so the delivery checklist fails short_edge, keeps the
    # failed check for the ledger and names a model that can reach it.
    handles = image_handles(api, session)
    if not handles:
        record("#282 delivery checklist judges the purpose", False, "no generated image handle")
    else:
        handle = handles[-1]
        inspect = '[[fake:tool:inspect_image {"image":"' + handle + '"}]]'
        if not _image_message(api, session, "交付前检查 " + inspect):
            record("#282 delivery checklist judges the purpose", False, "turn did not finish")
        else:
            summaries = _image_self_check_summaries(api, session)
            summary = next((item for item in reversed(summaries) if item.get("handle") == handle), None) or {}
            failed = [item.get("check") for item in summary.get("failed_checks") or []]
            contents = _checkpoint_completed_contents(work, "main", session)
            named = any('"check":"short_edge"' in content and "Gemini 3 Pro" in content
                        and '"purpose":"illustration"' in content for content in contents)
            record("#282 delivery checklist judges the purpose", "short_edge" in failed and named,
                   f"handle={handle} failed={failed} model_named={named} summary={summary}")

    # (d) the explicit 2D lock wins: a different named model is refused before
    # submission, and a later call without a model runs the locked one.
    lock = api.json("PUT", f"/api/v1/chat/sessions/{session}/model2d", {"explicit_model_2d": "nano-banana-2"})
    record("#239 2D lock writes", lock[0] == 200 and "nano-banana-2" in json.dumps(lock[1]),
           f"status={lock[0]} body={lock[1]}")
    before_tasks = {item.get("id") for item in session_tasks(api, session)}
    conflict = ('[[fake:tool:refine_image {"model":"nano-banana-pro","prompt":"a red cube"}]]')
    _image_message(api, session, "用另一个模型画 " + conflict)
    no_task = {item.get("id") for item in session_tasks(api, session)} == before_tasks
    contents = _checkpoint_completed_contents(work, "main", session)
    refused = any("与用户在本会话明确选定的模型" in content for content in contents)
    record("#239 shared model conflict is not silently overridden", no_task and refused,
           f"new_tasks={len({item.get('id') for item in session_tasks(api, session)} - before_tasks)} refusal={refused}")
    locked = ('[[fake:tool:refine_image {"prompt":"a red cube"}]]')
    before_tasks = {item.get("id") for item in session_tasks(api, session)}
    if not _image_message(api, session, "按锁定的模型画 " + locked):
        record("#239 locked 2D model runs", False, "turn did not finish")
    else:
        task = {}
        deadline = time.time() + 90
        while time.time() < deadline:
            fresh = [item for item in session_tasks(api, session) if item.get("id") not in before_tasks]
            if fresh:
                task = wait_task(api, fresh[0]["id"], 90)
                break
            time.sleep(0.5)
        record("#239 locked 2D model runs", task.get("offering_id") == "meshy/nano-banana-2/text-to-image",
               f"offering={task.get('offering_id')} state={task.get('state')}")

    # (e) a single-subject, solid-background picture passes the 3d_input
    # checklist through inspect_image.
    white, red = (250, 250, 250), (200, 30, 30)
    status, uploaded = api.upload("/api/v1/assets", "file", "3d-input.png",
                                  make_picture(128, 128, white, [((40, 32, 88, 96), red)]), "image/png")
    asset_id = uploaded.get("id") if status in (200, 201) else None
    if not asset_id:
        record("#239 3D input picture upload", False, f"HTTP {status}")
        return
    picture_url = uploaded.get("url") or f"/api/v1/assets/{asset_id}"
    _image_message(api, session, "这张图作为 3D 输入素材", image_urls=[picture_url])
    handle = ""
    status, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
    for message in (body if isinstance(body, list) else body.get("messages", [])):
        for attachment in message.get("attachments") or []:
            if attachment.get("kind") == "image" and attachment.get("handle"):
                handle = attachment["handle"]
    if not handle:
        record("#239 3D input picture handle", False, "no image handle")
        return
    inspect = '[[fake:tool:inspect_image {"image":"' + handle + '","checklist":"3d_input"}]]'
    if not _image_message(api, session, "检查 3D 输入 " + inspect):
        record("#239 3D input checklist", False, "turn did not finish")
        return
    summaries = _image_self_check_summaries(api, session)
    summary = next((item for item in reversed(summaries) if item.get("handle") == handle), None)
    failed = (summary or {}).get("checks", {}).get("failed")
    record("#239 3D input checklist", summary is not None and failed == 0,
           f"handle={handle} failed={failed} summary={summary}")

    # (f) without image_use the default 2D purpose applies: an empty brief still
    # gets the default recommendation and its frame override (#239 review).
    default_session = new_session(api)
    api.json("PUT", f"/api/v1/chat/sessions/{default_session}/approval", {"approvalMode": "auto"})
    default_draw = ('[[fake:tool:refine_image {"prompt":"a lone lighthouse on a cliff at dusk, '
                    'clean digital concept art, no text, no watermark"}]]')
    before_tasks = {item.get("id") for item in session_tasks(api, default_session)}
    if not _image_message(api, default_session, "随便画一张概念图 " + default_draw):
        record("#239 default 2D purpose applies", False, "turn did not finish")
    else:
        task = {}
        deadline = time.time() + 90
        while time.time() < deadline:
            fresh = [item for item in session_tasks(api, default_session) if item.get("id") not in before_tasks]
            if fresh:
                task = wait_task(api, fresh[0]["id"], 90)
                break
            time.sleep(0.5)
        task = task or {}
        prepared = _latest_prepared_input(work, "main", default_session)
        normalized = prepared.get("normalized_input") or {}
        model_choice = prepared.get("model_choice") or {}
        ok = (task.get("state") == "SUCCEEDED"
              and task.get("offering_id") == "gemini/gemini-3-pro-image/text-to-image"
              and normalized.get("aspect_ratio") == "16:9"
              and model_choice.get("source") == "profile"
              and model_choice.get("profile") == "concept_art")
        record("#239 default 2D purpose applies", ok,
               f"offering={task.get('offering_id')} state={task.get('state')} "
               f"aspect_ratio={normalized.get('aspect_ratio')} model_choice={model_choice}")


# ---- #374: the direction card (kind next_step) under a fixed function ----

# The function the scenario fixes. UV unwrap has one available service in the shipped Catalog and takes no parameter
# besides its source model, so the Harness panel adopts that service by itself (harnessAutoSelectOffering) and saves
# no fixed or fillable parameter. With no source model in the session the harness reports needs_input and the
# function's own tool stays hidden: the scenario needs the tool set a fixed function leaves, not a runnable step.
NEXT_STEP_OPERATION = "uv-unwrap"
# A free session write that belongs to no function and is not among chat.harness.base_tools. Its effect is one GET
# away, so it shows whether the Agent was offered the tool without a Task, an approval or a charge in the way.
NEXT_STEP_BRIEF_CALL = '[[fake:tool:update_production_brief {"purpose":"game_prop"}]]'
NEXT_STEP_BRIEF_REQUEST = "这个模型是游戏道具，把用途记进简报 "


def _harness_source_role(parameter):
    """sourceParameterRole of src/lib/workspace/processing-source.ts: which source a parameter takes, or None."""
    semantic = parameter.get("semantic")
    if semantic:
        return {"source-task": "task", "source-asset": "asset", "source-url": "url"}.get(semantic)
    name, kind = parameter.get("name"), parameter.get("type")
    if (name, kind) == ("source_task", "task"):
        return "task"
    if (name, kind) == ("source_asset", "asset") or name == "source_assets":
        return "asset"
    if (name, kind) == ("source_url", "text"):
        return "url"
    return None


def _harness_pin_selection(saved, declaration, operation, offering):
    """The selection the Harness panel saves when the person picks `operation` and its only available service:
    harnessOperationSelection (the first explicit goal adopts the Schema's allowed-operation defaults), then
    harnessOfferingSelection and harnessSelectionInput (src/components/workspace/harness-panel.tsx). The default
    source is the Agent's own choice among the session's models (harnessDefaultModelSources). Only valid for an
    offering that takes no parameter besides its source, which the caller checks."""
    started = bool(saved.get("operation_id")) or bool(saved.get("enabled_operations"))
    enabled = saved.get("enabled_operations") if started else declaration.get("default_enabled_operations")
    agent_source = (declaration.get("default_task_source_mode") == "agent"
                    and any(mode.get("id") == "agent" for mode in declaration.get("task_source_modes") or [])
                    and any(_harness_source_role(parameter) in ("task", "asset") for parameter in offering.get("parameters") or []))
    pinned = dict(saved)
    pinned.update({
        "operation_id": operation["id"], "enabled_operations": sorted(set(enabled or []) | {operation["id"]}),
        "allow_prerequisites": (saved.get("allow_prerequisites") if saved.get("operation_id")
                                else declaration.get("default_allow_prerequisites")),
        "offering_id": offering["id"], "offering_revision": offering["revision"],
        "sources": [{"kind": "task_asset", "selection_mode": "agent", "parameter": "*"}] if agent_source else [],
        "fixed_parameters": {}, "fillable_parameters": []})
    return pinned


def _harness_clear_selection(saved):
    """harnessClearSelection: the saved selection without its operation, service, sources and parameters. The
    enabled operations stay exactly as saved, so clearing changes nothing else."""
    cleared = {key: value for key, value in saved.items() if key not in ("operation_id", "offering_id", "offering_revision")}
    cleared.update({"enabled_operations": list(saved.get("enabled_operations") or []), "sources": [],
                    "fixed_parameters": {}, "fillable_parameters": []})
    return cleared


def _session_brief(api, session):
    """The session's production brief, or None when it cannot be read."""
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/brief")
    return ((body or {}).get("brief") or {}) if status == 200 else None


def _session_tool_names(api, session):
    """Every tool the session's assistant messages called, oldest first (the browser's tool-call list)."""
    messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    names = []
    for message in messages:
        raw = message.get("toolCalls")
        try:
            calls = (json.loads(raw) if isinstance(raw, str) else raw) or []
        except ValueError:
            continue
        names.extend(call.get("name") for call in calls if isinstance(call, dict))
    return names


def _next_step_turn(api, session, content, domain="3d", seconds=60, **fields):
    """One chat turn whose fake LLM answer is the tool call embedded in content; retries while another turn still
    owns the session and waits for this turn's reply. Returns (finished, why)."""
    before = assistant_reply_count(api, session)
    status, _ = _send_chat(api, session, {"content": content, "creative_domain": domain, **fields})
    if status not in (200, 201, 202):
        return False, f"send {status}"
    end = time.time() + seconds
    while time.time() < end:
        if assistant_reply_count(api, session) > before:
            return True, ""
        time.sleep(0.4)
    return False, f"no reply within {seconds}s"


def scenario_next_step(api):
    """#374: the direction card under a fixed function. A fixed function leaves the Agent its own tool and the
    base tools (chat.harness.base_tools, ask_choice among them). The Agent opens a next_step card whose first
    option clears the function selection; the page saves that selection first, with the latest revision and
    every other setting as saved, and sends the answer after it. Until then a tool outside the function is not
    offered; once the selection is clear the same call runs. The Agent here is the fake OpenAI-compatible LLM,
    so every call is a [[fake:tool:...]] marker, and no real upstream is involved."""
    session = new_session(api)
    harness = f"/api/v1/chat/sessions/{session}/harness"
    status, schema = api.json("GET", "/api/v1/schema")
    schema = schema.get("schema", schema) if status == 200 and isinstance(schema, dict) else {}
    declaration = (schema.get("chat") or {}).get("harness") or {}
    messages = (schema.get("content") or {}).get("messages") or {}

    # (1) fix the function the way the Harness panel does: the operation and its only available service, the
    # Agent's own choice of source model, the allowed operations the Schema defaults to.
    operation = next((item for item in declaration.get("operations") or [] if item.get("id") == NEXT_STEP_OPERATION), {})
    listed = api.json("GET", "/api/v1/offerings?capability=" + urllib.parse.quote(operation.get("capability") or "-"))[1]
    services = [item for item in (listed or {}).get("offerings") or []
                if item.get("available") and item.get("capability") == operation.get("capability")
                and (not operation.get("model_ids") or item.get("model_id") in operation["model_ids"])]
    extra = [parameter.get("name") for parameter in (services[0].get("parameters") or []) if _harness_source_role(parameter) is None] if services else []
    if not operation or operation.get("unavailable_reason") or len(services) != 1 or extra:
        record("#374 next-step: the function is fixed through the session harness", False,
               f"operation={operation.get('id')} unavailable_reason={operation.get('unavailable_reason')} "
               f"available services={[item.get('id') for item in services]} parameters besides the source={extra}: "
               f"the scenario needs a declared, available function with exactly one service and no other parameter")
        return
    status, view = api.json("GET", harness)
    saved = view.get("selection") or {}
    status_put, put = api.json("PUT", harness, {
        "expected_revision": saved.get("revision", -1),
        "selection": _harness_pin_selection(saved, declaration, operation, services[0])})
    pinned = put.get("selection") or {}
    ok = (status == 200 and status_put == 200 and pinned.get("operation_id") == operation["id"]
          and pinned.get("offering_id") == services[0]["id"] and pinned.get("revision") == saved.get("revision", -1) + 1)
    record("#374 next-step: the function is fixed through the session harness", ok,
           f"GET {status}, PUT {status_put}, operation={pinned.get('operation_id')} service={pinned.get('offering_id')} "
           f"sources={pinned.get('sources')} revision {saved.get('revision')} -> {pinned.get('revision')} "
           f"status={put.get('status')} enabled={pinned.get('enabled_operations')}")
    if not ok:
        return

    # (2) under the fixed function the Agent can still open the direction card.
    card_arguments = {
        "kind": "next_step", "recommended": "clear_selection",
        "options": [
            {"id": "clear_selection", "label": "清空功能选择", "action": {"type": "clear_operation"},
             "description": "回到自由模式，所有工具重新可用，再按你的需求继续。"},
            {"id": "stay_in_function", "label": "保持当前功能", "description": "继续只做当前固定的功能。"}]}
    marker = "[[fake:tool:ask_choice " + json.dumps(card_arguments, ensure_ascii=False) + "]]"
    finished, why = _next_step_turn(api, session, "先画一张角色立绘，再给它建模 " + marker)
    cards = [item for item in _choice_cards(api, session) if item.get("kind") == "next_step"]
    card = cards[0] if cards else {}
    options = card.get("options") or []
    first_action = (options[0].get("action") if options else None) or {}
    still_fixed = ((api.json("GET", harness)[1].get("selection") or {}).get("operation_id") == operation["id"])
    ok = (finished and len(cards) == 1 and card.get("state") == "pending" and len(options) == 2
          and first_action == {"type": "clear_operation"} and not options[1].get("action") and still_fixed)
    record("#374 next-step: ask_choice opens the direction card under the fixed function", ok,
           f"turn finished={finished} {why}; cards={len(cards)} state={card.get('state')} tool={card.get('tool')!r} "
           f"options={[(option.get('id'), option.get('action')) for option in options]} still fixed={still_fixed}")
    if not ok:
        return

    # (3) a tool outside the fixed function is not offered: the same marker writes nothing.
    brief_before, called_before = _session_brief(api, session), _session_tool_names(api, session)
    finished, why = _next_step_turn(api, session, NEXT_STEP_BRIEF_REQUEST + NEXT_STEP_BRIEF_CALL)
    brief_fixed, called_fixed = _session_brief(api, session), _session_tool_names(api, session)
    # Not even attempted: the fake LLM only calls a tool the request offers, so a refused call would show up in
    # the tool-call list; the empty brief alone would not tell "not offered" from "offered and refused".
    ok = finished and brief_before == {} and brief_fixed == {} and called_fixed == called_before
    record("#374 next-step: a tool outside the fixed function is not offered and writes nothing", ok,
           f"turn finished={finished} {why}; brief before={brief_before} after the call={brief_fixed}; "
           f"tools called before={called_before} after={called_fixed}")

    # (4) the page's order: clear the selection with the latest revision, then answer the card.
    view = api.json("GET", harness)[1]
    saved = view.get("selection") or {}
    status_put, _ = api.json("PUT", harness, {"expected_revision": saved.get("revision", -1),
                                              "selection": _harness_clear_selection(saved)})
    view = api.json("GET", harness)[1]
    after = view.get("selection") or {}
    ok = (status_put == 200 and not after.get("operation_id") and not after.get("offering_id")
          and not after.get("offering_revision") and not after.get("sources") and not after.get("fixed_parameters")
          and not after.get("fillable_parameters") and after.get("enabled_operations") == saved.get("enabled_operations")
          and after.get("revision") == saved.get("revision", -1) + 1 and view.get("status") == "free")
    record("#374 next-step: clearing the selection keeps the enabled operations", ok,
           f"PUT {status_put}, operation={after.get('operation_id')!r} service={after.get('offering_id')!r} "
           f"sources={after.get('sources')} revision {saved.get('revision')} -> {after.get('revision')} status={view.get('status')} "
           f"enabled before={saved.get('enabled_operations')} after={after.get('enabled_operations')} "
           f"allow_prerequisites {saved.get('allow_prerequisites')} -> {after.get('allow_prerequisites')}")
    if not ok:
        return
    pick = options[0]
    template = messages.get("ui.chat.choice.answer_text") or "选择：{title}"
    finished, why = _next_step_turn(api, session, template.replace("{title}", pick.get("label") or pick["id"]),
                                    choiceAnswer={"choice_id": card["id"], "option_id": pick["id"]})
    answered = next((item for item in _choice_cards(api, session) if item.get("id") == card["id"]), {})
    answer = answered.get("answer") or {}
    # The Agent reads the chosen direction as the card kind's answer text, appended to the person's message.
    told = last_request_with(lambda texts: bool(texts) and texts[-1].get("role") == "user"
                             and "[Direction card answer]" in (texts[-1].get("text") or "")) is not None
    ok = (finished and answered.get("state") == "answered" and answer.get("option_id") == pick["id"]
          and answer.get("use_default") is False and answer.get("source") == "card" and told)
    record("#374 next-step: the answer marks the card answered and reaches the Agent", ok,
           f"turn finished={finished} {why}; state={answered.get('state')} answer={answer} direction text sent to the LLM={told}")

    # (5) with the selection clear the same call runs.
    finished, why = _next_step_turn(api, session, NEXT_STEP_BRIEF_REQUEST + NEXT_STEP_BRIEF_CALL)
    brief_free, called_free = _session_brief(api, session), _session_tool_names(api, session)
    ok = (finished and called_free[len(called_fixed):] == ["update_production_brief"]
          and (brief_free or {}).get("purpose") == "game_prop")
    record("#374 next-step: the same call runs once the selection is cleared", ok,
           f"turn finished={finished} {why}; brief under the fixed function={brief_fixed} now={brief_free}; "
           f"new tool calls={called_free[len(called_fixed):]}")


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


def _quality_send(api, session, content, domain, model, image_urls=None):
    body = {"content": content, "creative_domain": domain, "ai_model": model}
    if domain == "image" and model:
        body["domainModels"] = {"image": model}
    if image_urls:
        body["image_urls"] = image_urls
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


def _quality_bind_session(api, session, request, domain, model, model_3d=""):
    """The approval mode and explicit model picks a quality case's session needs."""
    api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "budget"})
    if request.get("modality") == "3d" and model_3d:
        # #103: only an explicit pick in the session's model picker binds the 3D tools.
        api.json("PUT", f"/api/v1/chat/sessions/{session}/model3d", {"explicit_model_3d": model_3d})
    elif request.get("modality") == "2d" and model:
        # #239: the same explicit pick binds the 2D tool above the purpose
        # recommendation. A request that names a model in a step leaves that
        # choice to the Agent, so the purpose can still recommend one.
        named = any((item.get("arguments") or {}).get("model")
                    for step in request.get("steps") or []
                    for item in ([step["tool"]] if step.get("tool") else step.get("tools") or [])
                    if item.get("name") == "refine_image")
        if not named:
            api.json("PUT", f"/api/v1/chat/sessions/{session}/model2d", {"explicit_model_2d": model})


def _quality_create_project(api, request):
    """The project a #240 quality case runs in, or "" without one."""
    config = request.get("project") or {}
    if not config:
        return ""
    status, created = api.json("POST", "/api/v1/projects", {"name": config.get("name") or request["id"]})
    if status != 201 or not created.get("id"):
        return ""
    project_id = created["id"]
    conventions = config.get("conventions") or {}
    if conventions:
        api.json("PUT", f"/api/v1/projects/{project_id}/conventions", {"brief": conventions})
    return project_id


def _quality_image_task(tasks):
    """The newest succeeded 2D Task and its first delivered picture."""
    for task in reversed(tasks):
        if task.get("capability") != "text-to-image" or task.get("state") != "SUCCEEDED":
            continue
        assets = task.get("assets") or []
        picture = next((asset for asset in assets if asset.get("role") == "image"), assets[0] if assets else None)
        if picture and picture.get("id"):
            return task, picture
    return None, None


def _quality_promote_first_image(api, project_id, request, step, session):
    """Promote the newest image Task's first picture and write the style guide.

    Only the owner-verified reference is stored; the bytes stay with the Task.
    """
    task, picture = _quality_image_task(session_tasks(api, session))
    if not task or not picture:
        return False
    spec = step.get("asset") or {}
    source = {"task_id": task["id"], "asset_id": picture["id"]}
    status, promoted = api.json("POST", f"/api/v1/projects/{project_id}/promote", {
        "source": source,
        "kind": spec.get("kind") or "character",
        "name": spec.get("name") or "asset",
        "description": spec.get("description") or "",
    })
    if status != 201:
        return False
    guide = (request.get("project") or {}).get("style_guide") or {}
    if guide:
        references = [source] if guide.get("reference_from_asset", True) else []
        status, _saved = api.json("PUT", f"/api/v1/projects/{project_id}/style-guide", {
            "enabled": guide.get("enabled", True),
            "text": guide.get("text") or "",
            "palette": list(guide.get("palette") or []),
            "references": references,
        })
        if status != 200:
            return False
    return True


def _quality_feedback(api, session, feedback, tasks, facts, domain=None, model=None):
    """Run #241 feedback actions between turns; returns (stage, why). A step's
    tasks are named by their frozen prompt (or index), so the actions never
    depend on the API's list order."""
    stage = None
    for action in feedback:
        kind = action.get("action")
        if kind in ("pick", "reject"):
            task = None
            if action.get("prompt"):
                task = next((item for item in tasks
                             if str(((item.get("input") or {}).get("prompt") or "")) == action["prompt"]), None)
            elif isinstance(action.get("task"), int):
                task = tasks[action["task"]] if action["task"] < len(tasks) else None
            if not task:
                return stage, f"no task for {kind}"
            asset = next((item for item in task.get("assets") or [] if item.get("id")), None)
            if not asset:
                return stage, f"the task {task.get('id')} has no asset"
            if kind == "pick":
                status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/pick", {
                    "kind": "image", "taskId": task.get("id"), "assetId": asset.get("id"),
                })
                if status != 200:
                    return stage, f"pick {status} {body}"
                status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/preferences")
                selected = next((item for item in body.get("preferences") or [] if item.get("kind") == "selected"), None)
                if selected:
                    facts["selected_direction"] = selected.get("direction") or ""
                continue
            status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/preferences", {
                "kind": "image", "taskId": task.get("id"), "assetId": asset.get("id"),
                **({"reasonPreset": action["reasonPreset"]} if action.get("reasonPreset") else {}),
                **({"reasonText": action["reasonText"]} if action.get("reasonText") else {}),
            })
            if status != 200:
                return stage, f"reject {status} {body}"
            rejected = next((item for item in body.get("preferences") or [] if item.get("kind") == "rejected"), None)
            if rejected:
                facts["rejected_direction"] = rejected.get("direction") or ""
                facts["rejected_reason"] = rejected.get("reasonText") or ""
                facts["rejected_id"] = rejected.get("id") or ""
            continue
        if kind == "compact":
            status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
            # The shipped Catalog keeps five recent rounds. A trajectory that
            # reaches the compact action earlier gets plain filler turns (no
            # tool, no Task, no tool-call budget) until the manual compaction has
            # something older to summarise.
            for _ in range(6):
                if status == 200 or not domain or not model:
                    break
                if not _quality_send(api, session, "再补充一点背景设定，稍后继续。", domain, model):
                    return stage, "a filler turn was refused"
                if not _quality_turn_done(api, session):
                    return stage, "a filler turn did not finish"
                status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
            if status != 200:
                return stage, f"compact {status} {body}"
            stage = "after_compaction"
            continue
        preferences = []
        if kind in ("edit", "delete"):
            status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/preferences")
            preferences = body.get("preferences") or []
            rejected_id = facts.get("rejected_id") or next(
                (item.get("id") for item in preferences if item.get("kind") == "rejected"), "")
            if not rejected_id:
                return stage, f"no rejected preference to {kind}"
        if kind == "edit":
            edit = {name: action[name] for name in ("direction", "reasonPreset", "reasonText", "active") if name in action}
            status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/preferences/{rejected_id}", edit)
            if status != 200:
                return stage, f"edit {status} {body}"
            edited = (body.get("preferences") or [{}])[0]
            facts["edited_direction"] = edited.get("direction") or ""
            facts["edited_reason"] = edited.get("reasonText") or ""
            stage = "edited"
            continue
        if kind == "delete":
            status, _ = api.json("DELETE", f"/api/v1/chat/sessions/{session}/preferences/{rejected_id}")
            if status != 200:
                return stage, f"delete {status}"
            stage = "deleted"
            continue
        return stage, f"unknown action {kind}"
    return stage, ""


def run_quality_case(api, request, domain, model, model_3d="", work=""):
    """One request through the fake-LLM chat path; returns the run dict for agent_quality.

    A request with a project builds the #240 fixture first: project_setup steps
    promote the first picture and open the project's next session; only tool
    steps count as tool calls. #241 steps may carry several tools in one user
    message (one result card) and feedback actions between turns.

    work is the stack's work directory when the request exercises processing
    plans: plan cards are approved through the budget route, and the revision
    step reads the paused plan's id/version from the stack's own store (the
    same values get_processing_plan now reports to the Agent).
    """
    started = time.time()
    project_id = _quality_create_project(api, request)
    session = new_session(api, project_id)
    _quality_bind_session(api, session, request, domain, model, model_3d)
    sessions = [session]
    cards, seen = [], set()
    turns = 0
    # #241: the preference facts and the fake prompts built from them at each stage.
    preference_stage = "captured"
    preference_facts, preference_captured, preference_prompts = {}, [], []
    last_step_tasks = []
    reference_urls = []
    if request.get("reference_images"):
        # A deterministic reference picture, uploaded like the person's own.
        white, red = (250, 250, 250), (200, 30, 30)
        asset_id = upload_asset(api, "reference.png",
                                make_picture(128, 128, white, [((40, 32, 88, 96), red)]), "image/png")
        if asset_id:
            reference_urls = [f"/api/v1/assets/{asset_id}"]
    for step in request["steps"]:
        setup = step.get("project_setup")
        if setup == "new_session":
            session = new_session(api, project_id)
            _quality_bind_session(api, session, request, domain, model, model_3d)
            sessions.append(session)
            continue
        if setup == "promote_first_image":
            if not _quality_promote_first_image(api, project_id, request, step, session):
                record(f"agent-quality {request['id']}: promote the first picture", False,
                       f"project={project_id} session={session}")
                break
            continue
        # A step may carry several tool markers in one reply (#242 drafts three
        # concept directions and the direction card together); single-tool
        # steps keep their historical shape.
        step_tools = step.get("tools") or ([step["tool"]] if step.get("tool") else [])
        markers = []
        preference_prompt = False
        for tool_spec in step_tools:
            arguments = dict(tool_spec.get("arguments") or {})
            if tool_spec["name"] == "create_model_3d_from_image" and "input_override_reason" not in arguments:
                # The fake upstream's generated picture is a 1x1 placeholder, not a
                # real 3D input. The reference-trajectory runner records the
                # person's explicit override; the live output run does not use
                # these steps and the real Agent must pass the gate or ask.
                arguments["input_override_reason"] = "the fake upstream image is a placeholder, not a real 3D input"
            model_task = _quality_model_task(api, session)
            if model_task:
                for name in ("model", "source_task_id", "source"):
                    if arguments.get(name) == "@model1":
                        arguments[name] = model_task
            if tool_spec["name"] == "revise_processing_plan" and work:
                # The plan pauses on its checkpoint after the background step
                # finishes; the Agent reads its id/version from get_processing_plan.
                paused = _wait_processing_plan(
                    work, session,
                    lambda plan: plan["state"] == "PAUSED" and plan["failure"] == "plan_checkpoint_failed",
                    seconds=180)
                if paused:
                    if arguments.get("plan_id") in (None, "", "@plan1"):
                        arguments["plan_id"] = paused["id"]
                    if arguments.get("expected_plan_version") in (None, 0, "@plan_version"):
                        arguments["expected_plan_version"] = paused["version"]
                    if arguments.get("step_index") == "@checkpoint_step":
                        arguments["step_index"] = next(
                            (index for index, item in enumerate(paused["steps"])
                             if (item.get("checkpoint") or {}).get("state") == "failed"), 0)
            if tool_spec["name"] == "refine_image" and arguments.get("prompt_from_preferences"):
                preference_prompt = True
            markers.append(_quality_marker({"name": tool_spec["name"], "arguments": arguments}))
        step_tasks = []
        if markers:
            names = "+".join(tool_spec["name"] for tool_spec in step_tools)
            content = f"{step['user']} {' '.join(markers)}"
            before_tasks = {task.get("id") for task in session_tasks(api, session)}
            if not _quality_send(api, session, content, domain, model,
                                 image_urls=reference_urls if turns == 0 else None):
                record(f"agent-quality {request['id']}: send {names}", False, "message refused")
                break
            turns += 1
            if not _quality_turn_done(api, session):
                record(f"agent-quality {request['id']}: turn {names}", False, "turn did not finish")
                break
            # A card ends its turn; answer or approve it, then follow the turn it starts.
            for _ in range(4):
                answered = False
                for card in _quality_pending(api, session):
                    request_id = card.get("request_id")
                    if not request_id or request_id in seen:
                        continue
                    seen.add(request_id)
                    if card.get("kind") == "plan":
                        cards.append({"kind": "plan", "id": request_id, "tool": card.get("tool"), "state": "approved"})
                        api.json("PUT", f"/api/v1/chat/sessions/{session}/budgets/{request_id}", decision_body(api, "approve"))
                    else:
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
                    detail = wait_task(api, task.get("id"))
                    if detail:
                        step_tasks.append(detail)
            # A processing plan advances in the background: wait for its checkpoint
            # pause or final state, then observe every Task it submitted.
            if work and any(tool_spec["name"] in ("plan_model_processing", "revise_processing_plan")
                            for tool_spec in step_tools):
                _wait_processing_plan(
                    work, session,
                    lambda plan: plan["state"] in ("SUCCEEDED", "FAILED")
                    or (plan["state"] == "PAUSED" and plan["failure"] == "plan_checkpoint_failed"),
                    seconds=180)
                for task in session_tasks(api, session):
                    if task.get("id") not in before_tasks:
                        detail = wait_task(api, task.get("id"))
                        if detail and all(detail.get("id") != item.get("id") for item in step_tasks):
                            step_tasks.append(detail)
            last_step_tasks = step_tasks
            if preference_prompt and step_tasks:
                for task in step_tasks:
                    prompt = str((task.get("input") or {}).get("prompt") or "")
                    preference_prompts.append({"stage": preference_stage, "prompt": prompt})
        if step.get("feedback"):
            next_stage, why = _quality_feedback(api, session, step["feedback"], last_step_tasks, preference_facts,
                                               domain=domain, model=model)
            if why:
                record(f"agent-quality {request['id']}: feedback", False, why)
                break
            if next_stage:
                preference_stage = next_stage
            status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/preferences")
            current = body.get("preferences") or []
            if any(item.get("kind") == "selected" for item in current) and any(
                    item.get("kind") == "rejected" for item in current):
                preference_captured = current
    messages = []
    tasks, task_ids = [], set()
    for quality_session in sessions:
        messages.extend(_quality_messages(api, quality_session))
        for task in session_tasks(api, quality_session):
            if task.get("id") and task["id"] not in task_ids:
                task_ids.add(task["id"])
                tasks.append(task)
    images = sum(1 for message in messages for attachment in message.get("attachments") or []
                 if isinstance(attachment, dict) and attachment.get("kind") == "image")
    return {
        "session_id": session,
        "messages": messages,
        "tasks": tasks,
        "plans": _processing_plans(work, "main", session) if work else [],
        "cards": cards,
        "final_reply": _quality_final_reply(_quality_messages(api, sessions[-1])),
        "image_count": images,
        "turns": turns,
        "elapsed_ms": int((time.time() - started) * 1000),
        "preference_captured": preference_captured,
        "preference_prompts": preference_prompts,
        "preference_facts": preference_facts,
    }


def scenario_project(api, offerings, work):
    """#240: a project's first finished picture becomes its standard image and
    style reference; a second session in the same project inherits the style
    guide, reuses the picture without a user upload, and names the asset; a
    deleted project asset leaves the finished Task's bytes downloadable."""
    fake_control("DELETE")
    status, project = api.json("POST", "/api/v1/projects", {"name": "红发剑士系列"})
    project_id = project.get("id") or ""
    record("#240 create the project", status == 201 and bool(project_id),
           f"status {status} {json.dumps(project, ensure_ascii=False)[:200]}")
    if not project_id:
        return
    status, session = api.json("POST", "/api/chat/sessions", {"title": "", "project_id": project_id})
    session_id = session.get("id") or ""
    record("#240 the new session belongs to the project",
           status in (200, 201) and session.get("projectId") == project_id
           and session.get("styleLockSource") == "project",
           f"status {status} projectId={session.get('projectId')} source={session.get('styleLockSource')}")
    if not session_id:
        return

    # The project's production default seeds the next session's empty brief.
    status, conventions = api.json("PUT", f"/api/v1/projects/{project_id}/conventions", {
        "brief": {"image_use": "illustration"},
        "texts": {"naming": "role-<name>", "coordinates": "Y up, metres"},
    })
    record("#240 save the project conventions",
           status == 200 and ((conventions.get("brief") or {}).get("image_use") == "illustration"),
           f"status {status} {json.dumps(conventions, ensure_ascii=False)[:200]}")

    # (a) The first picture is generated in the project session.
    first = step(api, offerings, "#240 role A image", IMAGE_OFFERINGS["openai"][0], "text-to-image",
                 {"prompt": "full-body red-haired swordswoman, cel-shaded illustration, plain background"}, session_id)
    if not first or first.get("state") != "SUCCEEDED":
        record("#240 promote the first picture", False, f"state={(first or {}).get('state')}")
        return
    picture = next((asset for asset in first.get("assets") or [] if asset.get("id")), None)
    if not picture:
        record("#240 promote the first picture", False, f"no picture asset in {first.get('assets')}")
        return

    # (b) The user confirms the promotion; only the reference is stored.
    status, promoted = api.json("POST", f"/api/v1/projects/{project_id}/promote", {
        "source": {"task_id": first["id"], "asset_id": picture["id"]},
        "kind": "character", "name": "角色 A", "description": "红发剑士的标准全身立绘",
    })
    record("#240 promote the first picture as the standard image",
           status == 201 and (promoted.get("images") or [{}])[0].get("task_id") == first["id"],
           f"status {status} {json.dumps(promoted, ensure_ascii=False)[:240]}")
    if status != 201:
        return

    style_text = "项目风格：赛璐璐平涂、暖色高光、干净轮廓（PROJECT-STYLE-240）"
    status, guide = api.json("PUT", f"/api/v1/projects/{project_id}/style-guide", {
        "enabled": True, "text": style_text, "palette": ["#c94f3d", "#f2d7b6"],
        "references": [{"task_id": first["id"], "asset_id": picture["id"]}],
    })
    record("#240 save the project style guide",
           status == 200 and guide.get("enabled") is True and len(guide.get("references") or []) == 1
           and guide.get("text") == style_text,
           f"status {status} {json.dumps(guide, ensure_ascii=False)[:240]}")

    # (c) A new session in the same project inherits the guide and the default
    # purpose; the second picture needs no project reference from the person.
    status, second = api.json("POST", "/api/chat/sessions", {"title": "", "project_id": project_id})
    second_id = second.get("id") or ""
    record("#240 the project's next session inherits the style source",
           status in (200, 201) and second.get("projectId") == project_id
           and second.get("styleLockSource") == "project",
           f"status {status} projectId={second.get('projectId')} source={second.get('styleLockSource')}")
    if not second_id:
        return
    status, brief = api.json("GET", f"/api/v1/chat/sessions/{second_id}/brief")
    record("#240 the empty brief inherits the project's default purpose",
           status == 200 and (brief.get("brief") or {}).get("image_use") == "illustration",
           f"status {status} brief={(brief or {}).get('brief')}")

    base_picture = upload_asset(api, "role-b-base.png",
                                make_picture(96, 96, (250, 250, 250), [((24, 24, 72, 72), (200, 60, 60))]), "image/png")
    if not base_picture:
        record("#240 the second picture reuses the project reference", False, "the base picture upload was refused")
        return
    marker = ('[[fake:tool:refine_image {"prompt":"same series as role A, cel-shaded flat colour, '
              'warm highlights, plain background","reference_handles":["@img1"]}]]')
    task, why, _approval = agent_tool_turn(
        api, second_id, "画同系列的角色 B " + marker,
        image_urls=[f"/api/v1/assets/{base_picture}"], creative_domain="image",
        ai_model=IMAGE_OFFERINGS["meshy"][1])
    task_id = (task or {}).get("task_id") or (task or {}).get("id")
    if not task or why or task.get("state") != "SUCCEEDED":
        record("#240 the second picture runs in the project", False, why or f"state={(task or {}).get('state')}")
        return
    record("#240 the second picture runs in the project", True, f"task {task_id} SUCCEEDED")

    frozen = _visual_anchor_frozen_input(work, task_id)
    references = frozen.get("reference_assets") or []
    prompt = str(frozen.get("prompt") or "")
    record("#240 the inherited project style text is frozen into the turn",
           style_text in prompt and "Project style" in prompt,
           f"prompt={prompt[-200:]!r}")
    record("#240 the project style picture enters the frozen input",
           len(references) == 2 and references[1] == base_picture,
           f"references={references}")

    contents = _visual_anchor_wait_tool_content(work, second_id, "style_sources")
    sources = []
    for content in reversed(contents):
        try:
            candidate = json.loads(content)
        except ValueError:
            continue
        if isinstance(candidate, dict) and candidate.get("style_sources"):
            sources = candidate["style_sources"]
            break
    record("#240 the tool result names the reused project asset", sources == ["角色 A"], f"style_sources={sources}")

    # (d) Deleting the project asset removes only the reference row.
    status, _raw, _headers = api.call("DELETE", f"/api/v1/projects/{project_id}/assets/{promoted['id']}")
    record("#240 deleting the project asset succeeds", status == 204, f"status {status}")
    status, raw, _headers = api.call("GET", picture["url"])
    record("#240 the finished Task's product survives the deleted project asset",
           status == 200 and len(raw) == picture["size"]
           and hashlib.sha256(raw).hexdigest() == picture["sha256"],
           f"status {status}, {len(raw)} bytes, want {picture['size']}")


def scenario_agent_quality(api, offerings, report_dir="", work=""):
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
        # A request may pin the session's 3D model when its plan names one
        # (for example the Meshy 7.1 checkpoint case).
        session_model_3d = request.get("model_3d") or model_3d
        run = run_quality_case(api, request, domain, model, session_model_3d, work)
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


def _checkpoint_completed_contents(work, stack, session):
    """The turn checkpoint's completed tool messages, which the messages wire does
    not carry (the #245 refusal helper reads the same store)."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        row = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT 1",
                         (session,)).fetchone()
        db.close()
    except sqlite3.Error:
        return []
    if not row or not row["checkpoint_json"]:
        return []
    try:
        checkpoint = json.loads(row["checkpoint_json"])
    except ValueError:
        return []
    contents = []
    for message in checkpoint.get("completed") or []:
        content = message.get("Content") or message.get("content")
        if content:
            contents.append(str(content))
    return contents


def _checkpoint_content_with(work, stack, session, needle, start=""):
    for content in reversed(_checkpoint_completed_contents(work, stack, session)):
        if needle in content and (not start or content.startswith(start)):
            return content
    return ""


def fake_request_bodies(path):
    """The recent POST bodies the fake upstream served for path, oldest first.
    The fake exposes them only for this script (a test-only debug route), so
    the scenario can assert the tool descriptions the Agent really received."""
    url = (f"http://127.0.0.1:{FAKE_PORT}/fake/control/requests?path="
           + urllib.parse.quote(path, safe=""))
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            bodies = json.loads(response.read())
    except (OSError, ValueError):
        return []
    return [body for body in bodies if isinstance(body, dict)]


def _tool_descriptions(request_body):
    tools = {}
    for tool in request_body.get("tools") or []:
        function = tool.get("function") or {}
        name = function.get("name")
        if name:
            tools[name] = function.get("description") or ""
    return tools


def _prepared_prompt(work, stack, session, needle):
    """The frozen prompt of the session's prepared input whose text carries
    needle; the prepared input is what the server really submitted."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        rows = db.execute("SELECT payload_json FROM generation_prepared_inputs "
                          "WHERE session_id=? ORDER BY created_at DESC, id DESC", (session,)).fetchall()
        db.close()
    except sqlite3.Error:
        return ""
    for row in rows:
        try:
            payload = json.loads(row["payload_json"])
        except ValueError:
            continue
        normalized = payload.get("normalized_input") or {}
        prompt = normalized.get("prompt")
        if isinstance(prompt, str) and needle in prompt:
            return prompt
    return ""


def _offering_turn(api, session, content, domain, model):
    before = assistant_reply_count(api, session)
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages",
                         {"content": content, "creative_domain": domain, "ai_model": model})
    if status not in (200, 201, 202):
        return False
    end = time.time() + 45
    while time.time() < end:
        if assistant_reply_count(api, session) > before:
            return True
        time.sleep(0.5)
    return False


def scenario_offering_knowledge(work, stack_binary, processes, catalog):
    """#249: shipped Offering knowledge, derived model choices, purpose coverage,
    prompt transparency and the pre-submit refusal all on the fake stack."""
    disabled_id = "tripo/tripo-p2/text-to-3d"
    child, api = start_stack(processes, stack_binary, work, "offering-knowledge", catalog, GOOD_KEY)
    try:
        status, body = api.json("GET", "/api/v1/offerings")
        offerings = {item["id"]: item for item in body.get("offerings", [])}
        knowledge = (offerings.get("meshy/nano-banana-pro/text-to-image") or {}).get("knowledge") or {}
        ok = (status == 200 and knowledge.get("strengths") and knowledge.get("weaknesses")
              and knowledge.get("limitations")
              and "prompt" not in knowledge and "reference_use" not in knowledge and "sources" not in knowledge)
        record("#249 public Offering knowledge", bool(ok),
               f"{len(offerings)} public offerings; knowledge keys={sorted(knowledge)}")
        status, english_body = api.json("GET", "/api/v1/offerings", headers={"Accept-Language": "en"})
        english = {item["id"]: item for item in (english_body.get("offerings") or [])}
        translated = (english.get("meshy/nano-banana-pro/text-to-image") or {}).get("knowledge") or {}
        translated_strengths = translated.get("strengths") or []
        record("#249 public Offering knowledge follows the request language",
               status == 200 and translated_strengths[:1] ==
               ["Strong prompt following with stable complex compositions and multi-subject relations."],
               f"status={status} strengths={translated_strengths[:1]}")
        record("#249 a disabled Offering leaves the public list", disabled_id not in offerings,
               f"disabled={disabled_id} present={disabled_id in offerings}")

        # The catalog data promises purpose coverage and the low-poly multiview
        # override; this is the loaded file, not a hand-written copy.
        with open(catalog, encoding="utf-8") as handle:
            document = json.load(handle)
        usage = document["application"]["chat"]["model3d_usage"]
        game = next(profile for profile in usage["profiles"] if profile["id"] == "game_character")
        override = game.get("parameter_overrides", {}).get("tripo/tripo-v3.1-multiview/image-to-3d", {})
        # #294: the character keeps Tripo on triangle output (a P2 task with quad
        # output delivers an FBX and no GLB); smart low poly accepts at most
        # 20000 triangles.
        covered = usage.get("override_coverage_required") is True and override.get("face_limit") == 20000 \
            and "quad" not in override and override.get("smart_low_poly") is True
        record("#249 purpose override coverage", covered,
               f"required={usage.get('override_coverage_required')} multiview={override}")

        session = new_session(api)
        api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
        model = next((item["model_id"] for item in offerings.values()
                      if item.get("available") and item.get("capability") == "text-to-3d"), "")
        if not _offering_turn(api, session, "列一下可用模型 [[fake:tool:list_offerings {}]]", "model-3d", model):
            record("#249 list_offerings turn", False, "turn did not finish")
        else:
            content = _checkpoint_content_with(work, "offering-knowledge", session,
                                               '"capabilities"', start='{"capabilities"')
            projected = {}
            try:
                projected = json.loads(content) if content else {}
            except ValueError:
                projected = {}
            by_id = {}
            for capability in projected.get("capabilities") or []:
                for offering in capability.get("offerings") or []:
                    by_id[offering.get("offering_id")] = offering
            sample = by_id.get("meshy/nano-banana-pro/text-to-image", {})
            sample_knowledge = sample.get("knowledge") or {}
            record("#249 list_offerings projects knowledge",
                   bool(sample_knowledge.get("strengths")) and bool(sample_knowledge.get("prompt"))
                   and "sources" not in sample_knowledge,
                   f"projected={len(by_id)} knowledge keys={sorted(sample_knowledge)}")
            record("#249 list_offerings follows the enabled Offerings", disabled_id not in by_id,
                   f"disabled_id present={disabled_id in by_id}")

        # A prompt over the variant's 20-character limit is refused before a
        # Task exists: no card, no charge. The session's explicit 3D model makes
        # the tool descriptions carry that model's own writing guidance.
        api.json("PUT", f"/api/v1/chat/sessions/{session}/model3d", {"explicit_model_3d": "meshy-7.1"})
        before_tasks = {task.get("id") for task in session_tasks(api, session)}
        marker = ('[[fake:tool:create_model_3d {"prompt":"a very long prompt that exceeds the limit",'
                  '"model":"meshy-7.1"}]]')
        if not _offering_turn(api, session, "做一个模型 " + marker, "model-3d", "meshy-7.1"):
            record("#249 over-limit prompt", False, "turn did not finish")
        else:
            content = _checkpoint_content_with(work, "offering-knowledge", session, "over the limit of 20 characters")
            new_tasks = {task.get("id") for task in session_tasks(api, session)} - before_tasks
            record("#249 over-limit prompt is refused before submission", bool(content) and not new_tasks,
                   f"refusal={'yes' if content else 'no'} new_tasks={len(new_tasks)}")
            descriptions = {}
            for body in reversed(fake_request_bodies("/openai/v1/chat/completions")):
                candidate = _tool_descriptions(body)
                if "create_model_3d" in candidate and "create_model_3d_from_image" in candidate:
                    descriptions = candidate
                    break
            guidance = descriptions.get("create_model_3d", "")
            no_text = descriptions.get("create_model_3d_from_image", "")
            record("#249 tool descriptions carry the selected model's writing guidance",
                   "Prompt guidance for Meshy 7.1" in guidance
                   and "text prompt: the service reads the text prompt" in guidance
                   and "aim for 30-60 words" in guidance
                   and "When this tool uses the selected model (Meshy 7.1)" in no_text
                   and "not sent to this service" in no_text,
                   f"guidance={guidance[:80]!r} no_text={no_text[:80]!r}")

        # A reference-image prompt is rewritten by the reference template; the
        # result names the exact text and the layer that changed it. The
        # reference is a picture a Task really generated, so the chat reference
        # policy accepts it.
        session2 = new_session(api)
        api.json("PUT", f"/api/v1/chat/sessions/{session2}/approval", {"approvalMode": "auto"})
        generated = step(api, offerings, "#249 final_prompt reference picture",
                         "openai/gpt-image-2/text-to-image", "text-to-image",
                         {"prompt": "a red cube on a table"}, session2)
        picture = ""
        if generated and generated.get("state") == "SUCCEEDED":
            picture = next((asset.get("url") for asset in generated.get("assets") or []
                            if asset.get("role") == "image"), "")
        if not picture:
            record("#249 final_prompt", False, "reference picture generation failed")
        else:
            marker = '[[fake:tool:refine_image {"prompt":"make the cube blue"}]]'
            before = assistant_reply_count(api, session2)
            # No workspace picker model: the session's default 2D purpose
            # recommends the image-to-image offering (#239), so the prompt
            # layers are the only thing this scenario varies.
            status, _ = api.json("POST", f"/api/chat/sessions/{session2}/messages",
                                 {"content": "改成蓝色 " + marker, "image_urls": [picture],
                                  "creative_domain": "image"})
            end = time.time() + 60
            while status in (200, 201, 202) and time.time() < end and assistant_reply_count(api, session2) <= before:
                time.sleep(0.5)
            content = _checkpoint_content_with(work, "offering-knowledge", session2, '"final_prompt"')
            result = {}
            try:
                result = json.loads(content) if content else {}
            except ValueError:
                result = {}
            note = result.get("prompt_note") or {}
            layers = note.get("layers") or []
            record("#249 final_prompt is the submitted text",
                   isinstance(result.get("final_prompt"), str) and "make the cube blue" in result["final_prompt"]
                   and bool(layers),
                   f"final_prompt={str(result.get('final_prompt'))[:80]!r} layers={[l.get('layer') for l in layers]}")
            prepared_prompt = _prepared_prompt(work, "offering-knowledge", session2, "make the cube blue")
            record("#249 final_prompt is the frozen prepared input",
                   bool(prepared_prompt) and result.get("final_prompt") == prepared_prompt
                   and any(layer.get("layer") == "reference-image template" for layer in layers),
                   f"prepared={prepared_prompt[:80]!r} final={str(result.get('final_prompt'))[:80]!r} "
                   f"layers={[l.get('layer') for l in layers]}")
    finally:
        processes.stop(child)


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


def _work_ledger_checkpoint(work, session, stack="main"):
    """The newest turn checkpoint of the session: the request the model got and
    this round's tool receipts. The checkpoint stores Go field names; older
    records may carry lowercase keys, so both spellings are accepted."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
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


def _work_ledger_recent_systems(work, session, stack="main", limit=6):
    """The system prompts of the newest checkpoints, newest first. A turn that
    is still running can leave a checkpoint with no request yet, so the caller
    must not depend on the newest row alone."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        rows = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT ?",
                          (session, limit)).fetchall()
        db.close()
    except sqlite3.Error:
        return []
    systems = []
    for row in rows:
        try:
            checkpoint = json.loads(row["checkpoint_json"])
        except ValueError:
            continue
        system = _work_ledger_system_prompt(checkpoint)
        if system:
            systems.append(system)
    return systems


def _work_ledger_wait_system(work, session, needle, seconds=20, stack="main"):
    """Wait until one of the newest checkpoints' system prompts carries a
    marker; the turn's checkpoint may land a moment after its reply."""
    end = time.time() + seconds
    systems = []
    while time.time() < end:
        systems = _work_ledger_recent_systems(work, session, stack)
        for system in systems:
            if needle in system:
                return system
        time.sleep(0.5)
    return systems[0] if systems else ""


def _work_ledger_request_marker(work, session, stack, marker, seconds=120):
    """Wait for the turn whose request ends with a user message carrying the
    marker; returns (checkpoint, system prompt). The turn row may be written
    before its request, and a compaction can delay it, so read recent rows."""
    import sqlite3
    end = time.time() + seconds
    while time.time() < end:
        try:
            db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
            db.row_factory = sqlite3.Row
            rows = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT 24",
                              (session,)).fetchall()
            db.close()
        except sqlite3.Error:
            rows = []
        for row in rows:
            try:
                checkpoint = json.loads(row["checkpoint_json"])
            except ValueError:
                continue
            request = checkpoint.get("Request") or checkpoint.get("request") or {}
            messages = request.get("Messages") or request.get("messages") or []
            for message in messages:
                role = message.get("Role") or message.get("role") or ""
                content = str(message.get("Content") or message.get("content") or "")
                if role == "user" and marker in content:
                    return checkpoint, _work_ledger_system_prompt(checkpoint)
        time.sleep(0.5)
    return {}, ""


def _self_check_results_recent(work, session, needle, limit=24):
    """Parse every recent checkpoint's tool receipts; return the JSON objects
    and the plain texts carrying the needle."""
    import sqlite3
    parsed, texts = [], []
    try:
        db = sqlite3.connect(f"file:{work}/self-check/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        rows = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at DESC LIMIT ?",
                          (session, limit)).fetchall()
        db.close()
    except sqlite3.Error:
        return parsed, texts
    for row in rows:
        try:
            checkpoint = json.loads(row["checkpoint_json"])
        except ValueError:
            continue
        for message in checkpoint.get("completed") or []:
            content = str(message.get("Content") if "Content" in message else message.get("content") or "")
            if not content:
                continue
            try:
                value = json.loads(content)
            except ValueError:
                if needle in content:
                    texts.append(content)
                continue
            parsed.append(value)
            if needle in content:
                texts.append(content)
    return parsed, texts


def _work_ledger_wait_system_all(work, session, needles, seconds=60, stack="main"):
    """Wait until one recent system prompt carries every needle."""
    end = time.time() + seconds
    systems = []
    while time.time() < end:
        systems = _work_ledger_recent_systems(work, session, stack)
        for system in systems:
            if all(needle in system for needle in needles):
                return system
        time.sleep(0.5)
    return systems[0] if systems else ""


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


def _work_ledger_selection_turn(api, session, content, asset_id, task_id):
    """Send one plain turn that selects an existing generated object. The
    selection is persisted in the turn options, which is the existing fact the
    ledger's rejected-direction projection reads."""
    replies = assistant_reply_count(api, session)
    body = {"content": content, "creative_domain": "image",
            "selection": [{"kind": "image", "assetId": asset_id, "taskId": task_id}]}
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


def _mask_send(api, session, body):
    """Send one turn, retrying briefly while the previous turn's stream is still active."""
    end = time.time() + 30
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    while status == 409 and time.time() < end:
        time.sleep(0.5)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    return status in (200, 201, 202)


def _mask_wait_turn(api, session, replies, seconds=90):
    end = time.time() + seconds
    while time.time() < end:
        if assistant_reply_count(api, session) > replies:
            return True
        time.sleep(0.5)
    return False


def _mask_approve(api, session, seconds=30):
    end = time.time() + seconds
    while time.time() < end:
        pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items", [])
        if pending:
            return pending[0]
        time.sleep(0.5)
    return None


def scenario_mask_selection(api, offerings, work):
    """#244: a painted selection is a frozen canvas input; refine_image binds it
    into the Offering's mask parameter, the approval card shows the mask input,
    the fake upstream changes only the masked pixels, a model without masks is
    refused without creating a Task, and the read-only create_selection_mask
    tool makes a usable mask for a later refine. Fake only.

    Cross-user denial is not observable here (the development mock login makes
    every request the one mock user, see check_delivery); internal/chat and
    internal/transport/httpapi tests cover it."""
    import l5_common

    session = new_session(api)
    # The private Catalog puts 2D edits into the manual capabilities, so this scenario asks
    # for the strict mode itself (#415); the shipped default is auto.
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "manual"})
    if status != 200:
        record("#244 mask: manual approval mode", False, f"PUT {status}")
        return
    model = "gpt-image-2.6-sunburst"
    size = 64
    red = (200, 40, 40)
    source = make_picture(size, size, (235, 235, 235), [((16, 16, 48, 48), red)])
    mask = make_rgba_picture(size, size, (16, 16, 48, 48))
    source_id = upload_asset(api, "mask-source.png", source, "image/png")
    mask_id = upload_asset(api, "selection-mask.png", mask, "image/png")
    if not source_id or not mask_id:
        record("#244 mask fixtures upload", False, f"source={source_id} mask={mask_id}")
        return

    selection = [{"kind": "image", "assetId": source_id, "url": f"/api/v1/assets/{source_id}"}]
    marker = ('[[fake:tool:refine_image {"prompt":"make the selected area blue","model":"' + model + '"}]]')
    body = {"content": "把这里改成蓝色 " + marker, "creative_domain": "image", "ai_model": model,
            "selection": selection, "inputSource": "canvas",
            "selectionMask": {"maskAssetId": mask_id}}
    before_tasks = {item.get("id") for item in session_tasks(api, session)}
    # The create_selection_mask turn targets the generated result of this turn
    # when it succeeds: @selected then carries TaskID+AssetID, the attachment
    # shape whose asset lives in the Task store (regression: reading the input
    # store first made generated pictures unreadable).
    use_selection = selection
    if not _mask_send(api, session, body):
        record("#244 canvas mask turn", False, "send refused")
    else:
        card = _mask_approve(api, session)
        inputs = (card or {}).get("inputs") or []
        mask_inputs = [item for item in inputs if item.get("parameter") == "mask"]
        prepared = _latest_prepared_input(work, "mask", session)
        normalized = prepared.get("normalized_input") or {}
        ok_card = (bool(mask_inputs) and mask_inputs[0].get("asset_id") == mask_id
                   and bool((card or {}).get("mask_note")))
        record("#244 approval card shows the mask input and the local-edit note", ok_card,
               f"mask_inputs={mask_inputs} mask_note={(card or {}).get('mask_note')}")
        record("#244 executor binds the frozen mask into the Offering mask parameter",
               normalized.get("mask") == mask_id,
               f"mask={normalized.get('mask')} offering={prepared.get('offering_id')}")
        if card:
            api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
        task = {}
        end = time.time() + 90
        while time.time() < end:
            fresh = [item for item in session_tasks(api, session) if item.get("id") not in before_tasks]
            if fresh:
                task = wait_task(api, fresh[0]["id"], 90)
                break
            time.sleep(0.5)
        ok = task.get("state") == "SUCCEEDED"
        outside_same = inside_changed = False
        if ok and task.get("assets"):
            status, output, _headers = api.call("GET", task["assets"][0]["url"])
            if status == 200:
                try:
                    width, height, rows = l5_common.decode_png(output)
                    swidth, sheight, source_rows = l5_common.decode_png(source)
                    if (width, height) == (swidth, sheight):
                        outside_same = all(
                            rows[y][3 * x:3 * x + 3] == source_rows[y][3 * x:3 * x + 3]
                            for y in range(height) for x in range(width)
                            if not (16 <= x < 48 and 16 <= y < 48))
                        inside_changed = any(
                            rows[y][3 * x:3 * x + 3] != source_rows[y][3 * x:3 * x + 3]
                            for y in range(16, 48) for x in range(16, 48))
                except ValueError as error:
                    outside_same = inside_changed = False
                    record("#244 output decodes", False, str(error))
        record("#244 masked generation succeeds and changes only the selection", ok and outside_same and inside_changed,
               f"state={task.get('state')} outside_same={outside_same} inside_changed={inside_changed}")
        if ok and task.get("assets") and task["assets"][0].get("id"):
            use_selection = [{"kind": "image", "assetId": task["assets"][0]["id"], "taskId": task["id"]}]

    # (b) a model without a mask parameter is refused: no Task, no whole-image redraw.
    gemini = "gemini-3-pro-image"
    before_tasks = {item.get("id") for item in session_tasks(api, session)}
    before_replies = assistant_reply_count(api, session)
    refusal_marker = ('[[fake:tool:refine_image {"prompt":"make the selected area blue","model":"' + gemini + '"}]]')
    refusal_body = {"content": "把这个区域改蓝 " + refusal_marker, "creative_domain": "image", "ai_model": gemini,
                    "selection": selection, "inputSource": "canvas",
                    "selectionMask": {"maskAssetId": mask_id}}
    if not _mask_send(api, session, refusal_body):
        record("#244 mask-unsupported model refusal", False, "send refused")
    else:
        _mask_wait_turn(api, session, before_replies)
        contents = _checkpoint_completed_contents(work, "mask", session)
        refused = any("不支持蒙版" in content for content in contents)
        new_tasks = {item.get("id") for item in session_tasks(api, session)} - before_tasks
        no_card = not api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items")
        record("#244 mask-unsupported model refusal", refused and not new_tasks and no_card,
               f"refused={refused} new_tasks={len(new_tasks)} no_card={no_card}")

    # (c) create_selection_mask builds a region mask from the generated result,
    # the reply shows it, and that URL works as refine_image's mask.
    record("#244 create_selection_mask targets a generated result", use_selection is not selection,
           f"selection={use_selection}")
    before_replies = assistant_reply_count(api, session)
    tool_marker = '[[fake:tool:create_selection_mask {"image":"@selected","mode":"region","x0":8,"y0":8,"x1":24,"y1":24}]]'
    tool_body = {"content": "圈出这里 " + tool_marker, "creative_domain": "image", "ai_model": model,
                 "selection": use_selection, "inputSource": "canvas"}
    mask_url = ""
    if not _mask_send(api, session, tool_body):
        record("#244 create_selection_mask", False, "send refused")
    else:
        _mask_wait_turn(api, session, before_replies)
        contents = _checkpoint_completed_contents(work, "mask", session)
        for content in contents:
            try:
                parsed = json.loads(content)
            except ValueError:
                continue
            if isinstance(parsed, dict) and parsed.get("mask_url"):
                mask_url = parsed["mask_url"]
        messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
        messages = messages if isinstance(messages, list) else messages.get("messages", [])
        shown = any(mask_url and mask_url in (message.get("imageUrls") or []) for message in messages)
        record("#244 create_selection_mask returns a mask URL shown in the reply", bool(mask_url) and shown,
               f"mask_url={mask_url} shown={shown}")
    if mask_url:
        before_tasks = {item.get("id") for item in session_tasks(api, session)}
        use_marker = ('[[fake:tool:refine_image {"prompt":"make the selected area blue","model":"' + model +
                      '","mask":"' + mask_url + '"}]]')
        use_body = {"content": "用这个蒙版改这里 " + use_marker, "creative_domain": "image", "ai_model": model,
                    "selection": selection, "inputSource": "canvas"}
        if not _mask_send(api, session, use_body):
            record("#244 generated mask drives refine_image", False, "send refused")
        else:
            card = _mask_approve(api, session)
            if card:
                api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
            task = {}
            end = time.time() + 90
            while time.time() < end:
                fresh = [item for item in session_tasks(api, session) if item.get("id") not in before_tasks]
                if fresh:
                    task = wait_task(api, fresh[0]["id"], 90)
                    break
                time.sleep(0.5)
            record("#244 generated mask drives refine_image", task.get("state") == "SUCCEEDED",
                   f"state={task.get('state')} card={bool(card)}")


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
    # #239 makes the default 2D purpose recommend a different model than the
    # workspace picker's; the ledger scenario is not about that conflict, so it
    # pins the text-to-image model explicitly like a person would. The call has
    # no reference picture, so it must stay text-to-image.
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/model2d",
                            {"explicit_model_2d": IMAGE_OFFERINGS["openai"][0]})
    if status != 200:
        record("#248 explicit 2D model", False, f"{status} {body}")
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
    # A rejected direction is projected from existing records: the person
    # selected the first result in a user message, then picked the second
    # result. No explicit-rejection write (#241) exists yet.
    first_asset = (task.get("assets") or [{}])[0]
    second_asset = (second.get("assets") or [{}])[0]
    if not first_asset.get("id") or not second_asset.get("id"):
        record("#248 rejected direction from existing selection records", False, "task assets without ids")
        return
    selected = _work_ledger_selection_turn(api, session, "keep this direction for now",
                                            first_asset["id"], first_task_id)
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/pick",
                            {"kind": "image", "assetId": second_asset["id"], "taskId": second["id"]})
    recorded = selected and status == 200
    if not recorded:
        record("#248 rejected direction from existing selection records", False,
               f"selection={selected} pick={status} {body if status != 200 else ''}")
        return
    # A read-only tool turn stores its request checkpoint; read the system
    # prompt that turn received after the pick moved.
    probe = _work_ledger_marker("get_production_step", {"step": second["id"]})
    if not _work_ledger_plain_turn(api, session, "confirm the kept step " + probe, creative_domain="image"):
        record("#248 rejected direction turn", False, "turn did not finish")
        return
    system = _work_ledger_wait_system(work, session, "不要再做")
    record("#248 rejected direction is projected from the existing records",
           "不要再做" in system and first_task_id in system and "已被后续选择取代" in system,
           f"block={'yes' if '不要再做' in system else 'no'} "
           f"task={'yes' if first_task_id in system else 'no'} "
           f"reason={'yes' if '已被后续选择取代' in system else 'no'}")
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
    # The read-only tool must attribute the self-check to this step with the
    # tool's own pass/unknown/fail counts; unknown checks are never folded
    # into passed.
    checkpoint = _work_ledger_checkpoint(work, session)
    readable = None
    for content in _work_ledger_tool_contents(checkpoint):
        if '"raw_prompt"' not in content:
            continue
        try:
            candidate = json.loads(content)
        except ValueError:
            continue
        if (candidate.get("step") or {}).get("task_id") == model["id"]:
            readable = candidate
            break
    model_step = (readable or {}).get("step") or {}
    self_checks = model_step.get("self_check") or []
    inspect_check = next((check for check in self_checks if check.get("tool") == "inspect_model"), None)
    counted = (bool(inspect_check) and inspect_check.get("passed", 0) > 0
               and inspect_check.get("unknown", 0) > 0 and inspect_check.get("failed", 0) > 0
               and bool(inspect_check.get("leftovers")))
    record("#248 get_production_step attributes the inspect_model self-check to its step", counted,
           f"step={model_step.get('step')} checks={json.dumps(inspect_check, ensure_ascii=False) if inspect_check else 'missing'}")
    # The next turn's ledger line must carry those exact counts and the failed
    # check names. Requiring them on the same line as the step handle and Task
    # ID rules out an incidental match in the static craft prompt, which also
    # mentions triangles, inspect_model and 遗留 on every model-3d turn.
    needle = model_step.get("step") or model["id"]
    system = _work_ledger_wait_system(work, session, needle)
    ledger_line = ""
    for line in system.splitlines():
        if needle in line and model["id"] in line and "inspect_model" in line:
            ledger_line = line
            break
    expected = ""
    if inspect_check:
        expected = (f"inspect_model 通过 {inspect_check.get('passed', 0)} "
                    f"未判定 {inspect_check.get('unknown', 0)} 失败 {inspect_check.get('failed', 0)}")
    leftovers = [item.get("check") for item in (inspect_check or {}).get("leftovers") or [] if item.get("check")]
    visible = bool(expected) and expected in ledger_line and all(name in ledger_line for name in leftovers)
    record("#248 inspect_model leftovers reach the next turn", counted and visible,
           f"counts={expected or 'missing'} leftovers={','.join(leftovers) or 'missing'} "
           f"ledger={'yes' if ledger_line else 'no'}")
    # Ledger content never reaches a server log.
    logs = _work_ledger_log_text(work)
    record("#248 ledger content is absent from server logs", secret not in logs and long_prompt not in logs,
           f"logs={len(logs)} bytes, secret={'present' if secret in logs else 'absent'}")


def wait_agent_reply(api, session, replies_before, seconds=90):
    end = time.time() + seconds
    while time.time() < end:
        if assistant_reply_count(api, session) > replies_before:
            return True
        time.sleep(0.4)
    return False


def image_handles(api, session):
    """Every image handle of the session's messages, in order, unique."""
    messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    handles = []
    for message in messages:
        for attachment in message.get("attachments") or []:
            if attachment.get("kind") == "image" and attachment.get("handle"):
                handles.append(attachment["handle"])
    return list(dict.fromkeys(handles))


def _self_check_records(api, session):
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/self-checks")
    if status != 200:
        return []
    records = body.get("selfChecks")
    return records if isinstance(records, list) else []


def _self_check_record(records, task_id):
    return next((record for record in records if record.get("taskId") == task_id), None)


def _self_check_send(api, session, content, model, image_urls=None, replies_before=None):
    replies = assistant_reply_count(api, session) if replies_before is None else replies_before
    body = {"content": content, "creative_domain": "model-3d", "ai_model": model}
    if image_urls:
        body["image_urls"] = image_urls
    end = time.time() + 30
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    while status == 409 and time.time() < end:
        time.sleep(0.5)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    if status not in (200, 201, 202):
        return False
    return wait_agent_reply(api, session, replies, 120)


def _self_check_results(work, session):
    """The newest turn's tool results, parsed where they are JSON (a refusal is text)."""
    parsed, texts = [], []
    for content in _checkpoint_completed_contents(work, "self-check", session):
        try:
            parsed.append(json.loads(content))
        except ValueError:
            texts.append(content)
    return parsed, texts


def scenario_self_check(work, stack_binary, processes, catalog):
    """#246: the persisted 3D self-check contract on the fake stack.

    One image-to-3D result gets a reference comparison and an inspection, a
    written conclusion, survives a context compaction, refuses more rounds and
    renders at the Schema limits, records a has-issues conclusion at the limit,
    counts paid corrections, and a background-finished result is listed as
    unchecked on the next turn, then inspected, compared against the reference
    and given its own passed record by that next turn. Fake only: no real
    upstream, no paid call.
    """
    child, api = start_stack(processes, stack_binary, work, "self-check", catalog, GOOD_KEY)
    try:
        offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
        model = next((item["model_id"] for item in offerings.values()
                      if item.get("available") and item.get("capability") == "text-to-3d"), "")
        if not model:
            record("#246 self-check setup", False, "no text-to-3d offering")
            return
        session = new_session(api)
        api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
        api.json("PUT", f"/api/v1/chat/sessions/{session}/model3d", {"explicit_model_3d": model})
        white, red = (250, 250, 250), (200, 30, 30)
        picture_id = upload_asset(api, "reference.png", make_picture(128, 128, white, [((40, 32, 88, 96), red)]), "image/png")
        if not picture_id:
            record("#246 reference upload", False, "the library upload was refused")
            return
        if not _self_check_send(api, session, "这是参考图", model, image_urls=[f"/api/v1/assets/{picture_id}"]):
            record("#246 reference attachment", False, "the reference turn did not finish")
            return
        handles = image_handles(api, session)
        if not handles:
            record("#246 reference attachment", False, "no image handle")
            return
        handle = handles[-1]
        task, why, _ = agent_tool_turn(
            api, session,
            "开始建模 " + _work_ledger_marker("create_model_3d_from_image",
                                             {"prompt": "a stylized red robot, plain background",
                                              "reference_handles": [handle]}),
            creative_domain="model-3d", ai_model=model)
        if not task or why or task.get("state") != "SUCCEEDED":
            record("#246 image-to-3D result", False, why or f"state={(task or {}).get('state')}")
            return
        task_id = task.get("task_id") or task.get("id")
        record("#246 image-to-3D result", True, f"task={task_id}")

        # One turn runs the inspection and the reference comparison; the server
        # writes both evidence rows and the Agent sees the side-by-side handles.
        replies = assistant_reply_count(api, session)
        compare_marker = _work_ledger_marker("compare_model_reference", {"model": task_id})
        inspect_marker = _work_ledger_marker("inspect_model", {"model": task_id})
        if not _self_check_send(api, session, "检查并对照参考图 " + inspect_marker + " " + compare_marker,
                                model, replies_before=replies):
            record("#246 inspect and reference turn", False, "turn did not finish")
            return
        records = _self_check_records(api, session)
        first = _self_check_record(records, task_id)
        kinds = [check.get("kind") for check in (first or {}).get("checks") or []]
        record("#246 the server records the real inspect and reference calls",
               first is not None and "inspect" in kinds and "reference" in kinds
               and first.get("status") == "unchecked",
               f"record={first}")
        messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
        messages = messages if isinstance(messages, list) else messages.get("messages", [])
        summary = None
        for message in messages:
            for call in _tool_calls(message):
                if call.get("name") == "compare_model_reference" and call.get("summary"):
                    summary = call["summary"]
        record("#246 the comparison summary names its views and handles",
               bool(summary) and summary.get("views") == ["front"] and bool(summary.get("handles")),
               f"summary={summary}")

        # The Agent writes the conclusion; the state and counters come back.
        replies = assistant_reply_count(api, session)
        if not _self_check_send(api, session, "记录结论 " + _work_ledger_marker("record_self_check", {
                "model": task_id, "status": "passed", "summary": "matches the reference",
                "findings": ["silhouette and colour match"], "fixes": [], "remaining": []}),
                model, replies_before=replies):
            record("#246 record conclusion turn", False, "turn did not finish")
            return
        first = _self_check_record(_self_check_records(api, session), task_id)
        conclusion = (first or {}).get("conclusion") or {}
        record("#246 passed conclusion is persisted",
               (first or {}).get("status") == "passed" and conclusion.get("status") == "passed"
               and (first or {}).get("roundsUsed") == 1 and (first or {}).get("rendersInRound") >= 1,
               f"record={first}")

        # A second result that is never checked is listed as unchecked and has no
        # record; the result itself still downloads.
        second, why, _ = agent_tool_turn(
            api, session,
            "再做一版 " + _work_ledger_marker("create_model_3d_from_image",
                                              {"prompt": "a second stylized robot",
                                               "reference_handles": [handle]}),
            creative_domain="model-3d", ai_model=model)
        if not second or why or second.get("state") != "SUCCEEDED":
            record("#246 second unchecked result", False, why or f"state={(second or {}).get('state')}")
            return
        second_id = second.get("task_id") or second.get("id")
        # The model listing reads the saved model file; let the finished Task
        # finish materializing its asset before the next prompt lists it.
        time.sleep(2.5)
        # No separate plain turn: the next tool turn's prompt is the next-turn
        # evidence, and it lists the unchecked result before the call runs.
        records = _self_check_records(api, session)

        # Round and render bounds are counted by the server; the record writer
        # stays available so the Agent can close the result truthfully.
        replies = assistant_reply_count(api, session)
        markers = " ".join(_work_ledger_marker("inspect_model", {"model": task_id}) for _ in range(2))
        if not _self_check_send(api, session, "再检查一次 " + markers, model, replies_before=replies):
            record("#246 round limit turn", False, "turn did not finish")
            return
        texts = []
        deadline = time.time() + 60
        while time.time() < deadline and not any("self-check limit declared" in text for text in texts):
            _, texts = _self_check_results_recent(work, session, "self-check limit declared")
            if not any("self-check limit declared" in text for text in texts):
                time.sleep(0.5)
        record("#246 the per-result round limit is refused with the Schema text",
               any("self-check limit declared" in text for text in texts), f"{texts}")
        prompt = _work_ledger_wait_system_all(work, session,
                                              [second_id, "未自检", "不得说成已检查"],
                                              seconds=60, stack="self-check")
        record("#246 an unchecked result stays unchecked and is named in the next prompt",
               _self_check_record(records, second_id) is None
               and second_id in prompt and "未自检" in prompt and "不得说成已检查" in prompt,
               f"records={records} prompt={prompt[-300:]}")
        replies = assistant_reply_count(api, session)
        markers = " ".join(_work_ledger_marker("render_model_views", {"model": task_id, "views": ["front"]})
                           for _ in range(3))
        if not _self_check_send(api, session, "再渲染 " + markers, model, replies_before=replies):
            record("#246 render limit turn", False, "turn did not finish")
            return
        texts = []
        deadline = time.time() + 60
        while time.time() < deadline and not any("self-check limit declared" in text for text in texts):
            _, texts = _self_check_results_recent(work, session, "self-check limit declared")
            if not any("self-check limit declared" in text for text in texts):
                time.sleep(0.5)
        record("#246 the per-round render limit is refused with the Schema text",
               any("self-check limit declared" in text for text in texts), f"{texts}")
        replies = assistant_reply_count(api, session)
        if not _self_check_send(api, session, "记录遗留问题 " + _work_ledger_marker("record_self_check", {
                "model": task_id, "status": "has_issues", "summary": "one seam remains",
                "findings": ["texture seam"], "fixes": ["re-rendered"], "remaining": ["seam on the back"]}),
                model, replies_before=replies):
            record("#246 issues conclusion turn", False, "turn did not finish")
            return
        first = _self_check_record(_self_check_records(api, session), task_id)
        record("#246 a has-issues conclusion is still writable at the limits",
               (first or {}).get("status") == "has_issues"
               and ((first or {}).get("conclusion") or {}).get("remaining") == ["seam on the back"],
               f"record={first}")

        # Two paid corrections: the first reaches the declared bound, the second
        # is not refused but the result says the bound is reached.
        correction_blocks = []
        for attempt in range(2):
            step, why, _ = agent_tool_turn(
                api, session,
                f"修一下第 {attempt + 1} 次 " + _work_ledger_marker(
                    "retexture_model", {"source_task_id": task_id, "prompt": "cleaner texture",
                                       "use_default_offering": True}),
                creative_domain="model-3d", ai_model=model)
            result_item = None
            deadline = time.time() + 90
            while time.time() < deadline and result_item is None:
                parsed, _ = _self_check_results_recent(work, session, "corrections_used")
                candidates = [item for item in parsed if isinstance(item.get("self_check"), dict)
                              and item.get("self_check", {}).get("corrections_used", 0) >= attempt + 1]
                if candidates:
                    result_item = max(candidates, key=lambda item: item["self_check"]["corrections_used"])
                else:
                    time.sleep(0.5)
            correction_blocks.append(result_item)
            if not step or why or step.get("state") != "SUCCEEDED":
                record(f"#246 correction {attempt + 1}", False, why or f"state={(step or {}).get('state')}")
                return
        first_block = (correction_blocks[0] or {}).get("self_check") or {}
        second_block = (correction_blocks[1] or {}).get("self_check") or {}
        record("#246 correction counts and the Schema limit note",
               first_block.get("corrections_used") == 1
               and second_block.get("corrections_used") == 2
               and second_block.get("corrections_limit") == 1
               and bool((correction_blocks[1] or {}).get("self_check_note")),
               f"blocks={correction_blocks}")

        # A background-finished result is listed as pending self-check on the
        # next turn; it has no record yet.
        fields, route = chat_schema_facts(api)
        available_member = fields.get("turn_background_available", "")
        backgrounded_member = fields.get("turn_backgrounded", "")
        background_path = route.replace("{sessionID}", session) if route else ""
        replies = assistant_reply_count(api, session)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
            "content": "后台做一个立方体 " + _work_ledger_marker("create_model_3d", {"prompt": "a plain cube"}),
            "creative_domain": "model-3d", "ai_model": model,
        })
        background_task, task_id_background = None, ""
        deadline = time.time() + 45
        while status in (200, 201, 202) and time.time() < deadline:
            for candidate in session_tasks(api, session):
                if candidate.get("capability") == "text-to-3d" and candidate.get("state") != "CANCELED":
                    background_task, task_id_background = candidate, candidate.get("task_id") or candidate.get("id")
                    break
            ready = api.json("GET", background_path)[1].get(available_member) if background_path and available_member else False
            if background_task and ready:
                break
            time.sleep(0.3)
        if not background_task or not available_member or not backgrounded_member:
            record("#246 background turn", False,
                   f"task={background_task} route={background_path!r} fields={available_member!r}/{backgrounded_member!r}")
            return
        status, body = api.json("POST", background_path)
        record("#246 background wait stops", status == 200 and body.get(backgrounded_member) is True, f"POST {status} {body}")
        background_task = wait_task(api, task_id_background, 120)
        time.sleep(2.5)
        # A final conclusion survives the manual compaction, both in the store
        # and in the next turn's prompt. The session already has enough rounds
        # older than the kept window.
        replies = assistant_reply_count(api, session)
        if not _self_check_send(api, session, "最后记录一次结论 " + _work_ledger_marker("record_self_check", {
                "model": task_id, "status": "has_issues", "summary": "one seam remains",
                "findings": ["texture seam"], "fixes": ["re-rendered twice"], "remaining": ["seam on the back"]}),
                model, replies_before=replies):
            record("#246 final conclusion turn", False, "turn did not finish")
            return
        prompt = _work_ledger_wait_system_all(work, session,
                                              [task_id_background, "后台完成，待自检"],
                                              seconds=90, stack="self-check")
        record("#246 a background-finished result is listed as pending self-check",
               background_task.get("state") == "SUCCEEDED"
               and _self_check_record(_self_check_records(api, session), task_id_background) is None
               and task_id_background in prompt and "后台完成，待自检" in prompt,
               f"task={background_task.get('state')} prompt={prompt[-300:]}")
        # The next turn checks that background result for real: the model is
        # resolved by its Task ID, an explicit reference is rendered beside it,
        # and the conclusion is written against task_id_background, never the
        # first task. This is the regression this scenario exists for: a
        # background Task that resolveReviewedModel/inspect_model or
        # record_self_check cannot reach would fail right here.
        replies = assistant_reply_count(api, session)
        background_evidence = (
            _work_ledger_marker("inspect_model", {"model": task_id_background}) + " "
            + _work_ledger_marker("compare_model_reference", {
                "model": task_id_background, "image": handle, "view": "front"}))
        if not _self_check_send(api, session, "自检后台完成的结果 " + background_evidence,
                                model, replies_before=replies):
            record("#246 background self-check turn", False, "turn did not finish")
            return
        background_record = _self_check_record(_self_check_records(api, session), task_id_background)
        background_checks = [check for check in (background_record or {}).get("checks") or []
                             if check.get("ok")]
        background_kinds = [check.get("kind") for check in background_checks]
        record("#246 the background result is inspected and compared against the reference",
               background_record is not None
               and "inspect" in background_kinds and "reference" in background_kinds,
               f"record={background_record}")
        replies = assistant_reply_count(api, session)
        if not _self_check_send(api, session, "给后台结果写结论 " + _work_ledger_marker("record_self_check", {
                "model": task_id_background, "status": "passed", "summary": "the background cube matches",
                "findings": ["shape and colour match"], "fixes": [], "remaining": []}),
                model, replies_before=replies):
            record("#246 background conclusion turn", False, "turn did not finish")
            return
        background_record = _self_check_record(_self_check_records(api, session), task_id_background)
        record("#246 the background result's conclusion is persisted",
               (background_record or {}).get("status") == "passed"
               and ((background_record or {}).get("conclusion") or {}).get("status") == "passed",
               f"record={background_record}")
        time.sleep(1)
        compaction_status, compaction_body = 0, {}
        for _attempt in range(4):
            compaction_status, compaction_body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
            if compaction_status == 200:
                break
            time.sleep(1)
        record("#246 the manual compaction completes",
               compaction_status == 200 and bool(compaction_body.get("compactions")),
               f"status={compaction_status} body={compaction_body}")
        record("#246 the compaction leaves the persisted conclusion readable",
               (_self_check_record(_self_check_records(api, session), task_id) or {}).get("status") == "has_issues"
               and (((_self_check_record(_self_check_records(api, session), task_id) or {}).get("conclusion") or {}).get("remaining") == ["seam on the back"]),
               f"record={_self_check_record(_self_check_records(api, session), task_id)}")
        replies = assistant_reply_count(api, session)
        if not _self_check_send(api, session, "压缩后确认结论 " + _work_ledger_marker("record_self_check", {
                "model": task_id, "status": "has_issues", "summary": "one seam remains",
                "findings": ["texture seam"], "fixes": ["re-rendered twice"], "remaining": ["seam on the back"]}),
                model, replies_before=replies):
            record("#246 post-compaction conclusion turn", False, "turn did not finish")
            return
        prompt = _work_ledger_wait_system_all(work, session,
                                              [task_id, "已自检有遗留问题", "one seam remains"],
                                              seconds=90, stack="self-check")
        record("#246 the compaction keeps the conclusion in the next prompt",
               task_id in prompt and "已自检有遗留问题" in prompt and "one seam remains" in prompt,
               prompt[-400:])
        background_line = next(
            (line for line in prompt.splitlines()
             if task_id_background in line and "｜轮次" in line), "")
        record("#246 a checked background result is no longer listed as pending",
               "已自检通过" in background_line
               and "后台完成，待自检" not in background_line,
               background_line or prompt[-400:])

    finally:
        processes.stop(child)


def _tool_calls(message):
    raw = message.get("toolCalls")
    if not raw:
        return []
    try:
        calls = json.loads(raw) if isinstance(raw, str) else raw
    except ValueError:
        return []
    return calls if isinstance(calls, list) else []


def scenario_vision_budget(api, offerings, work):
    """#247: purpose-based vision budget on the fake stack.

    A reference picture plus six generated candidates: the reference stays a
    high-detail picture of its own role, the candidates come back as one
    labelled contact sheet whose handles the Agent can name. An inspect_image
    region then returns a crop at the crop detail. A 3D turn shows that a
    self-check render reaches the next request at high detail beside the
    result thumbnail instead of taking its place.
    """
    fake_control("DELETE")
    session = new_session(api)
    fields, route = chat_schema_facts(api)
    approval_field = fields.get("approval_mode", "approvalMode")
    api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {approval_field: "auto"})
    image_model = IMAGE_OFFERINGS["openai"][1]
    # #239's default 2D purpose recommends another model; this scenario tests
    # the vision budget, so it pins the image-to-image model to keep the two
    # features from refusing each other.
    api.json("PUT", f"/api/v1/chat/sessions/{session}/model2d", {"explicit_model_2d": image_model})
    white, red = (250, 250, 250), (200, 30, 30)
    reference = make_picture(128, 128, white, [((40, 32, 88, 96), red)])
    status, body = api.upload("/api/v1/assets", "file", "vision-reference.png", reference, "image/png")
    asset_id = body.get("id") if status in (200, 201) else None
    if not asset_id:
        record("#247 reference upload", False, f"HTTP {status}")
        return
    replies_before = assistant_reply_count(api, session)
    # #360 makes the per-task image count a person's parameter: the Agent may no
    # longer adjust "n", so the six candidates are asked for on the person's side
    # of the turn (the message's own parameters) instead of through set_params.
    marker = '[[fake:tool:refine_image {"prompt":"a red cube on a white background","reference_handles":["@img1"]}]]'
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": "按这张参考图出六个候选 " + marker,
        "image_urls": [f"/api/v1/assets/{asset_id}"],
        "parameters": {"n": 6},
        "creative_domain": "image", "ai_model": image_model,
    })
    if status not in (200, 201, 202) or not wait_agent_reply(api, session, replies_before, 120):
        record("#247 candidate turn", False, f"send {status}")
        return
    handles = image_handles(api, session)
    if len(handles) < 7:
        record("#247 candidate handles", False, f"handles={handles}")
        return

    def has_reference(messages):
        return any(image.get("detail") == "high" and "用户上传" in (image.get("label") or "")
                   for message in messages for image in message.get("images") or [])

    def has_contact_sheet(messages):
        for message in messages:
            for image in message.get("images") or []:
                label = image.get("label") or ""
                named = [handle for handle in handles if handle in label]
                if image.get("detail") == "high" and len(named) >= 6:
                    return True
        return False

    follow_up = last_request_with(lambda messages: has_reference(messages) and has_contact_sheet(messages))
    record("#247 six candidates come back as a handle-labelled contact sheet", follow_up is not None,
           f"handles={handles} request={'found' if follow_up else 'missing'}")
    if follow_up is None:
        return
    # The same turn's reference picture is still there, at high detail, beside
    # the one contact-sheet picture.
    record("#247 the same-turn reference is not squeezed out", has_reference(follow_up["messages"]),
           f"images={[(image.get('detail'), image.get('label')) for message in follow_up['messages'] for image in message.get('images') or []]}")

    # The context view separates the estimated vision tokens from the per-request
    # token bound and the picture places; the places are never reported as tokens.
    response_fields, context_route = chat_context_facts(api)
    context_path = context_route.replace("{sessionID}", session) if context_route else ""
    status, context = api.json("GET", context_path) if context_path else (0, {})
    token_field = response_fields.get("context_vision_tokens", "contextVisionTokens")
    token_budget_field = response_fields.get("context_vision_token_budget", "contextVisionTokenBudget")
    image_budget_field = response_fields.get("context_vision_budget", "contextVisionBudget")
    record("#247 the context view names the vision token bound and the picture places",
           status == 200 and context.get(token_budget_field) == 12000 and context.get(image_budget_field) == 10
           and (context.get(token_field) or 0) > 0,
           f"status={status} context={context}")

    # Name one candidate and look closely: the crop must arrive at the crop
    # detail with its handle and region in the label.
    target = handles[2]
    replies_before = assistant_reply_count(api, session)
    inspect = ('[[fake:tool:inspect_image {"image":"' + target +
               '","region":{"x":0.2,"y":0.2,"width":0.5,"height":0.5},"show_picture":true}]]')
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": "放大看看这张 " + inspect, "creative_domain": "image", "ai_model": image_model,
    })
    if status not in (200, 201, 202) or not wait_agent_reply(api, session, replies_before, 90):
        record("#247 inspect crop turn", False, f"send {status}")
        return

    def has_crop_picture(messages):
        for message in messages:
            for image in message.get("images") or []:
                label = image.get("label") or ""
                if image.get("detail") == "high" and target in label and "局部裁剪" in label and "×" in label:
                    return True
        return False

    crop_request = last_request_with(has_crop_picture)
    record("#247 the named original opens at its crop detail", crop_request is not None,
           f"handle={target} request={'found' if crop_request else 'missing'}")

    # 3D self-check: a render keeps its own place at high detail beside the
    # result thumbnail on the next turn.
    model3d = next((item["model_id"] for item in offerings.values()
                    if item.get("available") and item.get("capability") == "text-to-3d"), "")
    if not model3d:
        record("#247 3D render section", False, "no text-to-3d offering")
        return
    render_session = new_session(api)
    api.json("PUT", f"/api/v1/chat/sessions/{render_session}/approval", {approval_field: "auto"})
    api.json("PUT", f"/api/v1/chat/sessions/{render_session}/model3d", {"explicit_model_3d": model3d})
    replies_before = assistant_reply_count(api, render_session)
    status, _ = api.json("POST", f"/api/chat/sessions/{render_session}/messages", {
        "content": "做一个立方体 [[fake:tool:create_model_3d {\"prompt\":\"a plain cube\"}]]",
        "creative_domain": "model-3d", "ai_model": model3d,
    })
    task = None
    end = time.time() + 120
    while time.time() < end and task is None:
        for candidate in session_tasks(api, render_session):
            if candidate.get("capability") == "text-to-3d" and candidate.get("state") == "SUCCEEDED":
                task = candidate
                break
        time.sleep(0.5)
    if task is not None:
        task = wait_task(api, task.get("task_id") or task.get("id"), 120)
    if status not in (200, 201, 202) or task is None or task.get("state") != "SUCCEEDED":
        record("#247 3D render section", False, f"task={task}")
        return
    wait_agent_reply(api, render_session, replies_before, 90)
    task_id = task.get("task_id") or task.get("id")
    replies_before = assistant_reply_count(api, render_session)
    render_marker = '[[fake:tool:render_model_views {"model":"' + task_id + '","views":["front"]}]]'
    status, _ = api.json("POST", f"/api/chat/sessions/{render_session}/messages", {
        "content": "渲染正视图自检 " + render_marker,
        "creative_domain": "model-3d", "ai_model": model3d,
    })
    wait_agent_reply(api, render_session, replies_before, 120)
    if status not in (200, 201, 202):
        record("#247 3D render turn", False, f"send {status}")
        return
    # A plain next turn: the request's history must carry both pictures.
    replies_before = assistant_reply_count(api, render_session)
    api.json("POST", f"/api/chat/sessions/{render_session}/messages", {
        "content": "这一版怎么样", "creative_domain": "model-3d", "ai_model": model3d,
    })
    wait_agent_reply(api, render_session, replies_before, 90)

    def has_render_and_thumbnail(messages):
        render = thumbnail = False
        for message in messages:
            for image in message.get("images") or []:
                label = image.get("label") or ""
                if image.get("detail") == "high" and "自检渲染图" in label:
                    render = True
                if image.get("detail") == "high" and "预览缩略图" in label:
                    thumbnail = True
        return render and thumbnail

    render_request = last_request_with(has_render_and_thumbnail)
    record("#247 a self-check render is high detail and keeps its own place", render_request is not None,
           "request found" if render_request else "no request carried both the render and the thumbnail")


PENDING_ASSISTANT_CONTENT = "__GENERATING__"


def _visual_anchor_reply_count(api, session):
    """Every assistant message whose content is no longer the pending marker."""
    messages = api.json("GET", f"/api/chat/sessions/{session}/messages")[1]
    messages = messages if isinstance(messages, list) else messages.get("messages", [])
    return sum(1 for message in messages
               if message.get("role") == "assistant"
               and str(message.get("content") or "").strip() not in ("", PENDING_ASSISTANT_CONTENT))


def _visual_anchor_wait_reply(api, session, before, seconds=120):
    end = time.time() + seconds
    while time.time() < end:
        if _visual_anchor_reply_count(api, session) > before:
            return True
        time.sleep(0.4)
    return False


def _visual_anchor_turn(api, session, content, image_urls=None, domain="image", model=None):
    """One Agent turn whose fake reply needs no Task; waits for its answer."""
    replies = _visual_anchor_reply_count(api, session)
    body = {"content": content}
    if image_urls:
        body["image_urls"] = image_urls
    if domain:
        body["creative_domain"] = domain
    if model:
        body["ai_model"] = model
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", body)
    if status not in (200, 201, 202):
        return False
    return _visual_anchor_wait_reply(api, session, replies)


def _visual_anchor_answer(api, session, card, option_id, content):
    """Answer a card on the card, carrying the fake tool marker of the Agent's
    continuation in the user text."""
    replies = _visual_anchor_reply_count(api, session)
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": content,
        "choiceAnswer": {"choice_id": card["id"], "option_id": option_id},
        "creative_domain": "image",
    })
    if status not in (200, 201, 202):
        return False
    return _visual_anchor_wait_reply(api, session, replies)


def _visual_anchor_system_with(work, session, needle, seconds=20):
    """A stored turn request whose system prompt carries the text. Turn rows
    include ones whose checkpoint is not written yet, so scan every row instead
    of trusting the newest."""
    import sqlite3
    end = time.time() + seconds
    while time.time() < end:
        try:
            db = sqlite3.connect(f"file:{work}/main/app.db?mode=ro", uri=True)
            db.row_factory = sqlite3.Row
            rows = db.execute("SELECT checkpoint_json FROM go_chat_turns WHERE session_id=?",
                              (session,)).fetchall()
            db.close()
        except sqlite3.Error:
            rows = []
        for row in rows:
            if not row["checkpoint_json"]:
                continue
            try:
                system = _work_ledger_system_prompt(json.loads(row["checkpoint_json"]))
            except ValueError:
                continue
            if needle in system:
                return system
        time.sleep(0.5)
    return ""


def _visual_anchor_frozen_input(work, task_id, stack="main"):
    """The Task's frozen input from the stack database. The public Task input
    withholds asset ids by design, so the smoke reads the frozen record."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        db.row_factory = sqlite3.Row
        row = db.execute("SELECT input_json FROM generation_tasks WHERE id=?", (task_id,)).fetchone()
        db.close()
    except sqlite3.Error:
        return {}
    if not row or not row["input_json"]:
        return {}
    try:
        return json.loads(row["input_json"])
    except ValueError:
        return {}


def _visual_anchor_handles(api, session):
    """The picture handles the session gave its messages, by asset id."""
    status, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
    messages = body if isinstance(body, list) else (body or {}).get("messages", [])
    by_asset = {}
    for message in messages:
        for attachment in message.get("attachments") or []:
            asset_id = attachment.get("asset_id") or attachment.get("assetId")
            handle = attachment.get("handle")
            if asset_id and handle:
                by_asset[asset_id] = handle
    return by_asset


def _visual_anchor_card(api, session, kind="visual_anchor"):
    for card in _quality_choices(api, session):
        if card.get("state") == "pending" and card.get("kind") == kind:
            return card
    return None


def _visual_anchor_wait_tool_content(work, session, needle, seconds=20):
    """Wait until the newest checkpoint's tool receipts carry the text."""
    end = time.time() + seconds
    contents = []
    while time.time() < end:
        contents = _work_ledger_tool_contents(_work_ledger_checkpoint(work, session))
        if any(needle in content for content in contents):
            return contents
        time.sleep(0.5)
    return contents


def _preference_send(api, session, content, image_model, expected, timeout=120):
    """Send one turn and wait for its reply and its fresh Tasks. Returns
    (fresh task ids, why); fresh ids are ordered by first appearance."""
    replies = assistant_reply_count(api, session)
    known = set(session_tool_calls(api, session))
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": content, "creative_domain": "image", "ai_model": image_model,
    })
    end = time.time() + 30
    while status == 409 and time.time() < end:
        time.sleep(0.5)
        status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
            "content": content, "creative_domain": "image", "ai_model": image_model,
        })
    if status not in (200, 201, 202):
        return [], f"send {status}"
    end = time.time() + timeout
    fresh = []
    while time.time() < end:
        current = [task for task in session_tool_calls(api, session) if task not in known]
        if len(current) >= expected and assistant_reply_count(api, session) > replies:
            fresh = current
            break
        time.sleep(0.5)
    if not fresh:
        return [], "the turn produced no Task"
    tasks = []
    for task_id in fresh:
        task = wait_task(api, task_id, 90)
        if task.get("state") != "SUCCEEDED":
            return [], f"task {task_id} state {task.get('state')}"
        tasks.append(task)
    return tasks, ""


def _preference_prompt_turn(api, session, work, image_model, label):
    """One turn whose refine_image prompt is filled from the system preference
    block. Returns (task, frozen prompt, why)."""
    marker = _work_ledger_marker("refine_image", {"prompt_from_preferences": True})
    tasks, why = _preference_send(api, session, f"{label} {marker}", image_model, 1)
    if not tasks:
        return None, "", why
    task = tasks[0]
    task_id = task.get("task_id") or task.get("id")
    frozen = _visual_anchor_frozen_input(work, task_id, stack="preferences")
    return task, str(frozen.get("prompt") or ""), ""


def scenario_preferences(api, offerings, work, enabled=True):
    """#241: the session's choice facts on the fake stack.

    Four candidates come from four refine_image calls in one turn. The person
    picks one and rejects another with a preset and a note; the next turn's
    fake-only prompt_from_preferences convention proves the Agent received the
    chosen and refused directions. The block survives a context compaction, an
    edit replaces the old text, a delete stops the injection, and a second
    owner sees 404 everywhere.
    """
    fake_control("DELETE")
    session = new_session(api)
    fields, _route = chat_schema_facts(api)
    approval_field = fields.get("approval_mode", "approvalMode")
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {approval_field: "auto"})
    if status != 200:
        record("#241 auto approval", False, f"status {status}")
        return
    preferences_route = f"/api/v1/chat/sessions/{session}/preferences"
    if not enabled:
        status, body = api.json("GET", preferences_route)
        record("#241 a preferences-off Catalog declares no records",
               status == 200 and body.get("preferences") == [],
               f"status={status} body={json.dumps(body, ensure_ascii=False)[:160]}")
        status, body = api.json("POST", preferences_route, {"kind": "image", "taskId": "t", "assetId": "a"})
        record("#241 a preferences-off Catalog refuses writes", status == 400,
               f"status={status} body={json.dumps(body, ensure_ascii=False)[:160]}")
        return

    image_model = IMAGE_OFFERINGS["meshy"][0]
    prompts = [f"PREF-DIR-{index} a watercolor fox in the snow" for index in range(4)]
    markers = " ".join(_work_ledger_marker("refine_image", {"prompt": prompt}) for prompt in prompts)
    tasks, why = _preference_send(api, session, "给四个方向各出一张 " + markers, image_model, 4)
    if not tasks:
        record("#241 four candidates", False, why)
        return
    record("#241 four candidates in one result card", len(tasks) == 4, f"tasks={len(tasks)}")
    by_prompt = {}
    for task in tasks:
        task_id = task.get("task_id") or task.get("id")
        frozen = _visual_anchor_frozen_input(work, task_id, stack="preferences")
        by_prompt[str(frozen.get("prompt") or "")] = task
    if not all(prompt in by_prompt for prompt in prompts):
        record("#241 every candidate froze its own direction", False,
               f"prompts={sorted(by_prompt)}")
        return
    handles = {}
    end = time.time() + 20
    while time.time() < end:
        handles = _visual_anchor_handles(api, session)
        if len(handles) >= 4:
            break
        time.sleep(0.5)
    assets = {prompt: (by_prompt[prompt].get("assets") or [{}])[0].get("id") for prompt in prompts}
    if not all(assets[prompt] and handles.get(assets[prompt]) for prompt in prompts):
        record("#241 every candidate has a handle", False, f"assets={assets} handles={handles}")
        return
    # Pick candidate 0, reject candidate 1 with a declared reason and a note.
    first = by_prompt[prompts[0]]
    second = by_prompt[prompts[1]]
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/pick", {
        "kind": "image", "taskId": first.get("task_id") or first.get("id"), "assetId": assets[prompts[0]],
    })
    record("#241 pick a candidate", status == 200, f"status={status} {json.dumps(body, ensure_ascii=False)[:160]}")
    status, body = api.json("POST", preferences_route, {
        "kind": "image", "taskId": second.get("task_id") or second.get("id"), "assetId": assets[prompts[1]],
        "reasonPreset": "style", "reasonText": "too realistic",
    })
    record("#241 reject a candidate with a reason", status == 200 and bool(body.get("preferences")),
           f"status={status} {json.dumps(body, ensure_ascii=False)[:200]}")
    status, body = api.json("GET", preferences_route)
    items = body.get("preferences") or []
    selected = [item for item in items if item.get("kind") == "selected"]
    rejected = [item for item in items if item.get("kind") == "rejected"]
    other_handles = {handles[assets[prompt]] for prompt in prompts[1:]}
    record("#241 the selection records its direction and the unselected candidates",
           status == 200 and len(selected) == 1
           and selected[0].get("direction") == prompts[0]
           and set(selected[0].get("alternatives") or []) == other_handles,
           f"status={status} selected={json.dumps(selected, ensure_ascii=False)[:260]}")
    record("#241 the rejection records its direction and reason",
           len(rejected) == 1 and rejected[0].get("direction") == prompts[1]
           and rejected[0].get("reasonPreset") == "style"
           and rejected[0].get("reasonText") == "too realistic"
           and rejected[0].get("explicit") is True,
           f"rejected={json.dumps(rejected, ensure_ascii=False)[:260]}")
    if not selected or not rejected:
        return

    # A later pick of an earlier candidate (A -> B -> A) is the newest action on
    # A: its record keeps the ID but becomes the newest selection again, so the
    # prompt must continue from A instead of the superseded B.
    for prompt in (prompts[2], prompts[0]):
        task = by_prompt[prompt]
        status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/pick", {
            "kind": "image", "taskId": task.get("task_id") or task.get("id"), "assetId": assets[prompt],
        })
        record(f"#241 re-pick candidate {prompts.index(prompt)}", status == 200,
               f"status={status} {json.dumps(body, ensure_ascii=False)[:160]}")
    status, body = api.json("GET", preferences_route)
    admitted = body.get("preferences") or []
    reselected = [item for item in admitted if item.get("kind") == "selected"]
    record("#241 re-selecting an earlier candidate becomes the newest selection",
           status == 200 and len(reselected) == 2
           and reselected[0].get("direction") == prompts[0]
           and reselected[0].get("assetId") == assets[prompts[0]],
           f"status={status} selected={json.dumps(reselected, ensure_ascii=False)[:300]}")

    # The next Agent turn reads the preference block and carries it into the
    # tool prompt (the fake-only prompt_from_preferences convention).
    _task, prompt, why = _preference_prompt_turn(api, session, work, image_model, "按偏好再画一张")
    record("#241 the next prompt carries the chosen direction",
           bool(_task) and prompts[0] in prompt, why or f"prompt={prompt[:220]!r}")
    record("#241 the next prompt follows the latest reselection",
           prompts[2] not in prompt, f"prompt={prompt[:220]!r}")
    record("#241 the next prompt avoids the refused direction and its reason",
           prompts[1] in prompt and "too realistic" in prompt and "风格不对" in prompt,
           f"prompt={prompt[:220]!r}")
    system = _work_ledger_wait_system(work, session, prompts[0], stack="preferences")
    record("#241 the system prompt frames the preferences",
           "<<<PREFERENCES>>>" in system and "<<<END_PREFERENCES>>>" in system
           and prompts[0] in system and prompts[1] in system and "too realistic" in system
           and "- 已选用：PREF-DIR-2" not in system,
           f"frame={'yes' if '<<<PREFERENCES>>>' in system else 'no'}")

    # A manual compaction keeps the preference block: it is stored session data,
    # not part of the message window.
    for index in range(3):
        if not _work_ledger_plain_turn(api, session, f"keep thinking {index}", creative_domain="image"):
            record(f"#241 filler round {index}", False, "turn did not finish")
            return
    status, body = api.json("POST", f"/api/v1/chat/sessions/{session}/context/compaction")
    compactions = body.get("compactions") or []
    record("#241 manual compaction", status == 200 and bool(compactions),
           f"status={status} compactions={len(compactions)}")
    _task, prompt, why = _preference_prompt_turn(api, session, work, image_model, "压缩后按偏好")
    record("#241 the prompt keeps the choices after compaction",
           bool(_task) and prompts[0] in prompt and prompts[1] in prompt and "too realistic" in prompt,
           why or f"prompt={prompt[:220]!r}")
    system = _work_ledger_wait_system(work, session, prompts[0], stack="preferences")
    record("#241 the system prompt keeps the framed block after compaction",
           "<<<PREFERENCES>>>" in system and prompts[0] in system and prompts[1] in system,
           f"frame={'yes' if '<<<PREFERENCES>>>' in system else 'no'}")

    # An edit replaces the refused direction and its reason in the next prompt.
    edited_direction = "PREF-DIR-EDIT an ink-wash fox"
    edited_reason = "washed out"
    status, body = api.json("PUT", f"{preferences_route}/{rejected[0]['id']}", {
        "direction": edited_direction, "reasonPreset": "other", "reasonText": edited_reason,
    })
    record("#241 edit a preference", status == 200 and
           (body.get("preferences") or [{}])[0].get("direction") == edited_direction,
           f"status={status} {json.dumps(body, ensure_ascii=False)[:200]}")
    _task, prompt, why = _preference_prompt_turn(api, session, work, image_model, "编辑后再画")
    record("#241 the edited direction replaces the old one",
           bool(_task) and edited_direction in prompt and edited_reason in prompt
           and prompts[1] not in prompt and "too realistic" not in prompt,
           why or f"prompt={prompt[:240]!r}")

    # Deleting the record stops the injection on the next turn.
    status, _ = api.json("DELETE", f"{preferences_route}/{rejected[0]['id']}")
    record("#241 delete a preference", status == 200, f"status={status}")
    status, body = api.json("GET", preferences_route)
    record("#241 a deleted preference is no longer listed",
           status == 200 and all(item.get("id") != rejected[0]["id"] for item in body.get("preferences") or []),
           f"status={status}")
    _task, prompt, why = _preference_prompt_turn(api, session, work, image_model, "删除后再画")
    record("#241 a deleted preference is no longer injected",
           bool(_task) and edited_direction not in prompt and "washed out" not in prompt
           and prompts[0] in prompt,
           why or f"prompt={prompt[:240]!r}")

    # A second owner sees 404 on every preference route and no content leaks.
    import sqlite3
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    try:
        db = sqlite3.connect(f"file:{work}/preferences/app.db", uri=True, timeout=10)
        db.execute("INSERT OR REPLACE INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at) "
                   "VALUES ('foreign-session','owner-other','Other','other',?,?)", (now, now))
        db.execute("INSERT OR REPLACE INTO chat_session_preferences "
                   "(id,owner_id,session_id,kind,source,direction,reason_preset,reason_text,task_id,asset_id,handle,"
                   "alternatives_json,explicit,active,user_edited,created_at,updated_at) "
                   "VALUES ('foreign-preference','owner-other','foreign-session','rejected','reject',"
                   "'secret direction','style','secret reason','','','@img-secret','[]',1,1,0,?,?)", (now, now))
        db.commit()
        db.close()
    except sqlite3.Error as error:
        record("#241 a second owner's rows exist for the 404 check", False, str(error))
        return
    checked = []
    for method, path, body in [
        ("GET", "/api/v1/chat/sessions/foreign-session/preferences", None),
        ("POST", "/api/v1/chat/sessions/foreign-session/preferences",
         {"kind": "image", "taskId": "own-task", "assetId": "own-asset"}),
        ("PUT", "/api/v1/chat/sessions/foreign-session/preferences/foreign-preference", {"direction": "taken"}),
        ("DELETE", "/api/v1/chat/sessions/foreign-session/preferences/foreign-preference", None),
    ]:
        status, body = api.json(method, path, body)
        checked.append((method, status, "secret" in json.dumps(body)))
    record("#241 a second owner gets 404 on every preference route",
           all(item[1] == 404 and not item[2] for item in checked), f"{checked}")
    status, body = api.json("GET", preferences_route)
    record("#241 a foreign write never entered this session",
           all(item.get("id") != "foreign-preference" for item in body.get("preferences") or []),
           f"status={status}")


def scenario_visual_anchors(api, offerings, work):
    """#250: the session's visual anchors on the fake stack.

    The style lock reaches the Agent's system prompt; a proposed identity
    anchor only lands after the person accepts the confirmation card and never
    changes the person's style lock; a declined proposal writes nothing; a
    later generation uses the anchor without re-selecting pictures; a
    single-picture 3D model keeps the anchor and drops the ordinary reference
    with a named handle; and Meshy retexture sends the style lock's picture as
    its style_asset without the style text.
    """
    fake_control("DELETE")
    session = new_session(api)
    fields, _route = chat_schema_facts(api)
    approval_field = fields.get("approval_mode", "approvalMode")
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {approval_field: "auto"})
    if status != 200:
        record("#250 auto approval", False, f"status {status}")
        return
    hero_asset = upload_asset(api, "anchor-hero.png", make_png(48), "image/png")
    style_asset = upload_asset(api, "anchor-style.png", make_png(56), "image/png")
    villain_asset = upload_asset(api, "anchor-villain.png", make_png(40), "image/png")
    ordinary_asset = upload_asset(api, "anchor-ordinary.png", make_png(64), "image/png")
    if not all((hero_asset, style_asset, villain_asset, ordinary_asset)):
        record("#250 upload the anchor pictures", False,
               f"assets={hero_asset},{style_asset},{villain_asset},{ordinary_asset}")
        return
    style_text = "ANCHOR-STYLE-9c41 印象派笔触与暖金色光线"
    status, body = api.json("PUT", f"/api/v1/chat/sessions/{session}/style-lock",
                            {"enabled": True, "text": style_text, "references": [style_asset]})
    record("#250 the session style lock is saved",
           status == 200 and (body.get("styleLock") or {}).get("enabled") is True,
           f"status {status} {json.dumps(body, ensure_ascii=False)[:200]}")
    # The shipped 2D purpose (concept art) recommends this model; using the
    # workspace model avoids the #239 purpose/model conflict card.
    image_model = IMAGE_OFFERINGS["meshy"][1]
    # The Agent never sees an input-asset id: the pictures it can name are the
    # handles the session gives the messages. Send all four in one turn, then
    # read the handles the way the Agent's context shows them.
    upload_urls = [f"/api/v1/assets/{asset}" for asset in (hero_asset, style_asset, villain_asset, ordinary_asset)]

    # (a) The Agent sees the style lock in its system prompt. The fake-only
    # marker makes the reply itself carry the answer, so the check reads what
    # the model really received.
    if not _visual_anchor_turn(api, session, "检查系统提示 [[fake:system-contains ANCHOR-STYLE-9c41]]",
                               image_urls=upload_urls, domain="image", model=image_model):
        record("#250 the style lock reaches the system prompt", False, "turn did not finish")
    else:
        reply = str((_quality_final_reply(_quality_messages(api, session)) or {}).get("content") or "")
        record("#250 the style lock reaches the system prompt",
               "present: ANCHOR-STYLE-9c41" in reply,
               f"reply={reply[:120]!r}")
    handles = _visual_anchor_handles(api, session)
    record("#250 the Agent can name the pictures by their handles",
           all(handles.get(asset) for asset in (hero_asset, style_asset, villain_asset, ordinary_asset)),
           f"handles={handles}")
    # (b) The Agent proposes an identity anchor; the person accepts on the card.
    hero_proposal = {"action": "create", "name": "hero", "kind": "identity",
                     "description": "red cloak", "references": [handles.get(hero_asset)]}
    propose = _work_ledger_marker("ask_choice", {
        "kind": "visual_anchor", "question": "把这张图设为角色标准形象？",
        "proposal": {"arguments": hero_proposal},
    })
    if not _visual_anchor_turn(api, session, "提议保存角色形象 " + propose, domain="image", model=image_model):
        record("#250 visual anchor confirmation card", False, "turn did not finish")
        return
    card = _visual_anchor_card(api, session)
    options = {option.get("id"): option for option in (card or {}).get("options") or []}
    record("#250 the confirmation card offers accept and decline",
           card is not None and set(options) == {"accept", "decline"}
           and options.get("accept", {}).get("label") == "设为视觉锚",
           f"card={json.dumps(card, ensure_ascii=False)[:240] if card else 'missing'}")
    system = _visual_anchor_system_with(work, session, "ANCHOR-STYLE-9c41")
    record("#250 the system prompt frames the lock and its references",
           "<<<VISUAL_ANCHORS>>>" in system and "<<<END_VISUAL_ANCHORS>>>" in system
           and "参考图 1 张" in system,
           f"marker={'yes' if '<<<VISUAL_ANCHORS>>>' in system else 'no'} "
           f"references={'yes' if '参考图 1 张' in system else 'no'}")
    # The same user text must appear once, and the two blocks must not give
    # opposite instructions about repeating it (#250 review).
    record("#250 one prompt carries the style lock once with one rule",
           system.count("ANCHOR-STYLE-9c41") == 1 and "逐字保持" not in system
           and system.count("<<<END_VISUAL_ANCHORS>>>") == 1
           and "<<<WORK_ANCHORS>>>" in system,
           f"texts={system.count('ANCHOR-STYLE-9c41')} "
           f"verbatim={'yes' if '逐字保持' in system else 'no'} "
           f"visual_close={system.count('<<<END_VISUAL_ANCHORS>>>')}")
    if card is None:
        return
    accept = _work_ledger_marker("set_visual_anchor", hero_proposal)
    if not _visual_anchor_answer(api, session, card, "accept", "保存这个锚点 " + accept):
        record("#250 accept the proposed anchor", False, "answer turn did not finish")
        return
    receipts = _visual_anchor_wait_tool_content(work, session, "视觉锚已保存")
    record("#250 the accepted anchor is written", bool(receipts) and
           any("视觉锚已保存" in content for content in receipts),
           f"receipts={len(receipts)}")
    # An Agent call that no answered card names is refused even though it uses
    # a handle the session really gave (#250 review).
    intruder = _work_ledger_marker("set_visual_anchor", {
        "action": "create", "name": "intruder", "kind": "identity",
        "references": [handles.get(ordinary_asset)],
    })
    if not _visual_anchor_turn(api, session, "未经确认直接写入 " + intruder,
                               domain="image", model=image_model):
        record("#250 an unconfirmed write is refused", False, "turn did not finish")
    else:
        receipts = _visual_anchor_wait_tool_content(work, session, "用户确认卡")
        record("#250 an unconfirmed write is refused",
               any("还没有经用户确认卡确认" in content for content in receipts),
               f"receipts={len(receipts)}")
    _visual_anchor_turn(api, session, "检查未确认锚点 [[fake:system-contains 身份锚「intruder」]]",
                        domain="image", model=image_model)
    reply = str((_quality_final_reply(_quality_messages(api, session)) or {}).get("content") or "")
    record("#250 an unconfirmed write stores no anchor", "absent: 身份锚「intruder」" in reply,
           f"reply={reply[:120]!r}")
    # A declined proposal writes nothing and leaves the person's lock alone.
    villain_proposal = {"action": "create", "name": "villain", "kind": "identity",
                        "description": "black cloak", "references": [handles.get(villain_asset)]}
    propose = _work_ledger_marker("ask_choice", {
        "kind": "visual_anchor", "question": "把这张图也设为角色标准形象？",
        "proposal": {"arguments": villain_proposal},
    })
    if not _visual_anchor_turn(api, session, "再提议一个 " + propose, domain="image", model=image_model):
        record("#250 the declined proposal card", False, "turn did not finish")
        return
    card = _visual_anchor_card(api, session)
    system = _visual_anchor_system_with(work, session, "身份锚「hero」")
    record("#250 the next turn sees the accepted identity anchor",
           "身份锚「hero」" in system and "<<<VISUAL_ANCHORS>>>" in system,
           f"anchor={'yes' if '身份锚「hero」' in system else 'no'}")
    decline = _work_ledger_marker("set_visual_anchor", {"action": "decline"})
    if card is None or not _visual_anchor_answer(api, session, card, "decline", "不用保存 " + decline):
        record("#250 decline the proposed anchor", False, f"card={card}")
        return
    _visual_anchor_turn(api, session, "看看现在的锚点 [[fake:system-contains 身份锚「villain」]]",
                        domain="image", model=image_model)
    reply = str((_quality_final_reply(_quality_messages(api, session)) or {}).get("content") or "")
    villain_system = _visual_anchor_system_with(work, session, "身份锚「villain」", seconds=2)
    record("#250 a declined anchor writes nothing",
           "absent: 身份锚「villain」" in reply and "身份锚「villain」" not in villain_system,
           f"reply={reply[:120]!r} villain={'yes' if '身份锚「villain」' in villain_system else 'no'}")
    status, lock = api.json("GET", f"/api/v1/chat/sessions/{session}/style-lock")
    saved = (lock.get("styleLock") or lock or {})
    record("#250 anchor writes never change the person's style lock",
           status == 200 and saved.get("text") == style_text and saved.get("enabled") is True
           and saved.get("references") == [style_asset],
           f"status={status} lock={json.dumps(lock, ensure_ascii=False)[:200]}")

    # (c) A later generation names the anchor and carries its picture without
    # any handle from the person.
    marker = _work_ledger_marker("refine_image", {"prompt": "a portrait of the ranger", "anchors": ["hero"]})
    task, why, _approval = agent_tool_turn(api, session, "画她坐着的样子 " + marker,
                                           creative_domain="image", ai_model=image_model)
    if not task or why or task.get("state") != "SUCCEEDED":
        record("#250 generation uses the anchor", False, why or f"state={(task or {}).get('state')}")
        return
    task_id = task.get("task_id") or task.get("id")
    anchor_input = _visual_anchor_frozen_input(work, task_id)
    references = anchor_input.get("reference_assets") or []
    prompt = str(anchor_input.get("prompt") or "")
    record("#250 generation uses the anchor picture without a handle",
           references == [hero_asset, style_asset] and "保留「hero」的身份" in prompt,
           f"references={references} prompt={prompt[:160]!r}")

    # (c2) The picture the Agent just made becomes an identity anchor. The
    # Agent only names its session handle; the stored reference is the
    # canonical Task asset URL. The next turn must carry that picture in the
    # model's vision context at high detail: the anchor path used to inline
    # input asset ids only, so a Task URL was silently skipped (#250 review).
    handles = _visual_anchor_handles(api, session)
    generated_handles = [handles.get(asset.get("id")) for asset in task.get("assets") or []
                         if handles.get(asset.get("id"))]
    if not generated_handles:
        record("#250 a generated result can be an identity anchor", False,
               f"no handle for the result assets {task.get('assets')}")
    else:
        made_proposal = {"action": "create", "name": "made-hero", "kind": "identity",
                         "description": "the portrait just generated",
                         "references": [generated_handles[0]]}
        propose = _work_ledger_marker("ask_choice", {
            "kind": "visual_anchor", "question": "把刚生成的这张设为角色标准形象？",
            "proposal": {"arguments": made_proposal},
        })
        card = None
        if _visual_anchor_turn(api, session, "把刚生成的图设为锚点 " + propose,
                               domain="image", model=image_model):
            card = _visual_anchor_card(api, session)
        if card is None:
            record("#250 a generated result can be an identity anchor", False,
                   "the confirmation card is missing")
        else:
            accept = _work_ledger_marker("set_visual_anchor", made_proposal)
            saved = _visual_anchor_answer(api, session, card, "accept", "保存这个锚点 " + accept)
            receipts = _visual_anchor_wait_tool_content(work, session, "视觉锚已保存")
            _visual_anchor_turn(
                api, session, "检查刚保存的锚点 [[fake:system-contains 身份锚「made-hero」]]",
                domain="image", model=image_model)
            reply = str((_quality_final_reply(_quality_messages(api, session)) or {}).get("content") or "")
            record("#250 a generated result can be an identity anchor",
                   saved and bool(receipts) and "present: 身份锚「made-hero」" in reply,
                   f"saved={saved} receipts={len(receipts)} reply={reply[:120]!r}")

            def has_generated_anchor_image(messages):
                for message in messages:
                    for image in message.get("images") or []:
                        if (image.get("detail") == "high" and image.get("data_uri")
                                and "身份锚「made-hero」参考图 1" in (image.get("label") or "")):
                            return True
                return False

            anchor_request = last_request_with(has_generated_anchor_image)
            record("#250 a generated-result anchor reaches vision at high detail",
                   anchor_request is not None,
                   f"handle={generated_handles[0]} "
                   f"request={'found' if anchor_request else 'missing'} "
                   f"images={[(image.get('detail'), image.get('data_uri'), image.get('label')) for request in fake_chat_requests()[-3:] for message in request.get('messages') or [] for image in message.get('images') or []]}")

    # (d) A single-picture model keeps the anchor and drops the ordinary
    # reference; the tool result names the dropped handle. Both pictures are
    # real session handles: the ordinary one was sent in the first turn.
    ordinary_handle = handles.get(ordinary_asset)
    marker = _work_ledger_marker("create_model_3d_from_image", {
        "prompt": "a ranger miniature", "anchors": ["hero"],
        "reference_handles": [ordinary_handle], "input_override_reason": "fake stack single-picture check",
    })
    model_task, why, _approval = agent_tool_turn(
        api, session, "把参考和标准形象一起做成模型 " + marker,
        creative_domain="model-3d", ai_model="meshy/meshy-7.1/image-to-3d")
    if not model_task or why or model_task.get("state") != "SUCCEEDED":
        record("#250 the single-picture model keeps the anchor", False,
               why or f"state={(model_task or {}).get('state')}")
        return
    model_task_id = model_task.get("task_id") or model_task.get("id")
    model_input = _visual_anchor_frozen_input(work, model_task_id)
    model_references = model_input.get("reference_assets") or []
    record("#250 the single-picture model keeps the anchor and drops the ordinary reference",
           model_references == [hero_asset],
           f"references={model_references} input={json.dumps(model_input, ensure_ascii=False)[:200]}")
    receipts = _visual_anchor_wait_tool_content(work, session, "reference_note")
    note = ""
    for content in reversed(receipts):
        try:
            candidate = json.loads(content)
        except ValueError:
            continue
        if isinstance(candidate, dict) and candidate.get("reference_note"):
            note = str(candidate["reference_note"])
            break
    record("#250 the tool result names the dropped ordinary reference",
           "丢弃了普通参考" in note and bool(ordinary_handle) and ordinary_handle in note,
           f"reference_note={note[:200]!r} handle={ordinary_handle}")

    # (e) Meshy retexture sends the style lock picture as its style_asset and
    # never injects the style text into the 3D prompt.
    source_task = model_task.get("task_id") or model_task.get("id")
    marker = _work_ledger_marker("retexture_model", {
        "source_task_id": source_task, "offering_id": "meshy/processing/retexture",
    })
    texture, why, _approval = agent_tool_turn(api, session, "换成苔藓贴图 " + marker,
                                              creative_domain="model-3d")
    if not texture or why or texture.get("state") != "SUCCEEDED":
        record("#250 retexture carries the style reference", False,
               why or f"state={(texture or {}).get('state')}")
        return
    texture_id = texture.get("task_id") or texture.get("id")
    texture_input = _visual_anchor_frozen_input(work, texture_id)
    texture_prompt = str(texture_input.get("prompt") or "")
    record("#250 retexture sends the style lock picture as style_asset",
           texture_input.get("style_asset") == style_asset,
           f"style_asset={texture_input.get('style_asset')} input={json.dumps(texture_input, ensure_ascii=False)[:200]}")
    record("#250 retexture never injects the style text",
           "prompt" not in texture_input and style_text not in texture_prompt and "Style lock" not in texture_prompt,
           f"prompt={texture_prompt!r}")


def _processing_plans(work, stack, session):
    """Reads the session's processing plans from the stack's SQLite file, as
    the panel's own list route would (state, failure and frozen steps)."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        rows = db.execute(
            "SELECT id, state, failure, steps_json, version FROM processing_plans "
            "WHERE session_id=? ORDER BY created_at DESC", (session,)).fetchall()
        db.close()
    except sqlite3.Error:
        return []
    plans = []
    for plan_id, state, failure, steps_json, version in rows:
        try:
            steps = json.loads(steps_json)
        except ValueError:
            steps = []
        plans.append({"id": plan_id, "state": state, "failure": failure or "",
                      "steps": steps, "version": version})
    return plans


def _tool_results(work, stack, session, tool_name):
    """JSON tool results the Agent received for one tool, oldest first.

    They live in the persisted chat checkpoint's completed messages; the
    public messages wire carries only the call summary. This reads the same
    server-side copy the Agent sees, never the processing-plans table."""
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        rows = db.execute(
            "SELECT checkpoint_json FROM go_chat_turns WHERE session_id=? ORDER BY updated_at ASC",
            (session,)).fetchall()
        db.close()
    except sqlite3.Error:
        return []
    results, seen = [], set()
    for (checkpoint_json,) in rows:
        try:
            checkpoint = json.loads(checkpoint_json or "{}")
        except ValueError:
            continue
        calls = {call.get("ID"): call.get("Name")
                 for call in (checkpoint.get("assistant") or {}).get("ToolCalls") or []}
        for message in checkpoint.get("completed") or []:
            if message.get("Role") != "tool" or calls.get(message.get("ToolCallID")) != tool_name:
                continue
            content = message.get("Content") or ""
            if content in seen:
                continue
            try:
                payload = json.loads(content)
            except ValueError:
                continue
            if not isinstance(payload, dict):
                continue
            seen.add(content)
            results.append(payload)
    return results


def _wait_processing_plan(work, session, predicate, seconds=150):
    end = time.time() + seconds
    plan = None
    while time.time() < end:
        plans = _processing_plans(work, "main", session)
        if plans:
            plan = plans[0]
            if predicate(plan):
                return plan
        time.sleep(0.5)
    return plan


def _plan_step_marker(prompt="a plain cube", model=MESHY_TEXT_TO_3D):
    """The fake marker that asks for one paid 3D step of a plan."""
    return "[[fake:tool:create_model_3d " + json.dumps(
        {"prompt": prompt, "model": model}, ensure_ascii=False, separators=(",", ":")) + "]]"


def _wait_tasks(api, session, count, seconds=150):
    """At least `count` Tasks of the session, as the task list gives them."""
    end = time.time() + seconds
    tasks = []
    while time.time() < end:
        tasks = session_tasks(api, session)
        if len(tasks) >= count:
            return tasks
        time.sleep(0.5)
    return tasks


def _session_budget(api, session):
    """The session's current plan as the session projection gives it."""
    # The session read is the legacy route the browser uses; the plan travels in
    # its `budget` member with the server's per-step standing (#410).
    return api.json("GET", f"/api/chat/sessions/{session}")[1].get("budget") or {}


def _wait_plan(api, session, check, seconds=90):
    end = time.time() + seconds
    budget = {}
    while time.time() < end:
        budget = _session_budget(api, session)
        if check(budget):
            return budget
        time.sleep(0.5)
    return budget


def scenario_plan_state(work, stack_binary, processes, catalog):
    """#410: a plan whose turn ended with steps left says so, and the person
    continues it from the plan card.

    A five-step plan runs four steps and the turn ends (the fake Agent simply
    stops). The plan is still active, so it is not done: the session projection
    shows four steps done and the fifth not run. "Continue" runs the plan's turn
    again — its go-ahead carries the fifth step — so the fifth step runs, the
    first four are not redone, and the plan then reads completed. A later step of
    the same capability finds no budget left in the finished plan and waits for a
    person, exactly like a step under a replaced plan. Fake only: no real
    upstream, no paid call.
    """
    child, api = start_stack(processes, stack_binary, work, "plan-state", catalog, GOOD_KEY)
    try:
        offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
        if MESHY_TEXT_TO_3D not in offerings:
            record("plan-state: setup", False, "no Meshy text-to-3d offering")
            return
        session = new_session(api)
        status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "budget"})
        if status != 200:
            record("plan-state: budget approval mode", False, f"PUT {status}")
            return
        proposal = "[[fake:tool:propose_production_plan " + json.dumps({
            "title": "五步计划",
            "steps": [{"capability": "text-to-3d", "model": MESHY_TEXT_TO_3D,
                       "purpose": f"第 {index} 步"} for index in range(1, 6)],
            "budgets": [{"unit": "legacy.credits", "amount": 400}],
        }, ensure_ascii=False, separators=(",", ":")) + "]]"
        status, _ = _send_chat(api, session, {"content": "做一个五步计划 " + proposal,
                                              "creative_domain": "model-3d",
                                              "ai_model": MESHY_TEXT_TO_3D,
                                              "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
        if status not in (200, 201, 202):
            record("plan-state: propose the plan", False, f"send {status}")
            return
        budget_id = ""
        end = time.time() + 30
        while time.time() < end and not budget_id:
            pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
            card = next((item for item in pending if item.get("kind") == "plan"), None)
            if card:
                budget_id = card.get("request_id") or ""
            else:
                time.sleep(0.4)
        if not budget_id:
            record("plan-state: the plan card appears", False, "no plan card")
            return
        status, approved = api.json("PUT", f"/api/v1/chat/sessions/{session}/budgets/{budget_id}",
                                    decision_body(api, "approve"))
        if status != 200 or approved.get("state") != "active":
            record("plan-state: approve the plan", False, f"PUT {status} {approved}")
            return
        record("plan-state: the plan is approved and active", True, f"budget {budget_id}")
        # The approval starts the plan's own turn, and this stack's go-ahead
        # carries the first step: the person approves and the work starts.
        _quality_turn_done(api, session)
        first = _wait_tasks(api, session, 1)
        record("plan-state: the approved plan runs its first step by itself",
               len(first) == 1, f"tasks {[(t.get('id'), t.get('state')) for t in first]}")
        if len(first) != 1:
            return

        # Three more turns run the second, third and fourth steps; then the turn
        # ends with the fifth left.
        for index in range(2, 5):
            task, why, _source = agent_tool_turn(api, session, f"第 {index} 步 " + _plan_step_marker(),
                                                 creative_domain="model-3d", ai_model=MESHY_TEXT_TO_3D)
            if not task or task.get("state") != "SUCCEEDED":
                record(f"plan-state: step {index} runs", False, f"{why} {task}")
                return
        tasks = session_tasks(api, session)
        record("plan-state: the first four steps ran once each",
               len([task for task in tasks if task.get("capability") == "text-to-3d"]) == 4,
               f"tasks {[(t.get('id'), t.get('capability'), t.get('state')) for t in tasks]}")
        # The fourth turn ends after its answer: only then may a step read "not
        # run", because until then the running turn may still submit it.
        _quality_turn_done(api, session)

        stopped = _wait_plan(api, session, lambda budget: len(budget.get("steps") or []) == 5
                             and (budget.get("steps") or [])[-1].get("state") == "not_run")
        states = [step.get("state") for step in (stopped.get("steps") or [])]
        record("plan-state: the plan is still active with four steps done",
               stopped.get("state") == "active" and states[:4] == ["done"] * 4,
               f"state={stopped.get('state')} steps={states}")
        record("plan-state: the fifth step reads not run, not waiting",
               states[4:] == ["not_run"], f"steps={states}")

        # The person continues: the go-ahead is the plan's own continuation
        # text, which carries the fifth step, so the turn the server starts runs
        # exactly that step.
        status, started = api.json("POST",
                                   f"/api/v1/chat/sessions/{session}/budgets/{budget_id}/continue", {})
        if status != 200 or not started.get("turn_message_id"):
            record("plan-state: continue the plan", False, f"POST {status} {started}")
            return
        record("plan-state: continue starts the plan's turn again", True,
               f"turn {started.get('turn_message_id')}")
        fifth = None
        end = time.time() + 150
        while time.time() < end and fifth is None:
            succeeded = [item for item in session_tasks(api, session)
                         if item.get("capability") == "text-to-3d" and item.get("state") == "SUCCEEDED"]
            if len(succeeded) >= 5:
                fifth = succeeded[-1]
            else:
                time.sleep(0.5)
        record("plan-state: the continued turn ran the fifth step", fifth is not None,
               f"task={(fifth or {}).get('id')}")
        tasks = session_tasks(api, session)
        record("plan-state: the first four steps were not redone",
               len([item for item in tasks if item.get("capability") == "text-to-3d"]) == 5,
               f"tasks {[(t.get('capability'), t.get('state')) for t in tasks]}")

        done = _wait_plan(api, session, lambda budget: budget.get("state") == "completed")
        record("plan-state: the plan reads completed once every step succeeded",
               done.get("state") == "completed"
               and all(step.get("state") == "done" for step in (done.get("steps") or [])),
               f"state={done.get('state')} steps={[step.get('state') for step in (done.get('steps') or [])]}")

        # A step of the same capability finds no budget left in the finished
        # plan, so it waits for a person instead of running by budget.
        status, _payload = _send_chat(api, session, {"content": "再来一步 " + _plan_step_marker("one more"),
                                                     "creative_domain": "model-3d",
                                                     "ai_model": MESHY_TEXT_TO_3D,
                                                     "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
        if status not in (200, 201, 202):
            record("plan-state: the next step is sent", False, f"send {status}")
            return
        card, pending = None, []
        end = time.time() + 60
        while time.time() < end and card is None:
            pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
            # A step card carries the tool it would run; the plan card is the
            # one that says kind=plan.
            card = next((item for item in pending
                         if item.get("kind") != "plan" and item.get("tool")), None)
            if card is None:
                time.sleep(0.5)
        record("plan-state: a completed plan leaves the next step to the person",
               card is not None,
               f"pending={[(item.get('kind'), item.get('tool'), item.get('status')) for item in pending]}")
        if card:
            api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "deny"))
        record("plan-state: no extra task ran after the plan completed",
               len([item for item in session_tasks(api, session) if item.get("capability") == "text-to-3d"]) == 5,
               f"tasks {[(t.get('capability'), t.get('state')) for t in session_tasks(api, session)]}")
    finally:
        processes.stop(child)


def scenario_plan_checkpoint(work, api, offerings):
    """#251: an approved budget plan stops after a failed checkpoint and the
    Agent revises it with the declared contingency, without a second approval."""
    session = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "budget"})
    if status != 200:
        record("plan-checkpoint: budget approval mode", False, f"PUT {status}")
        return
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/brief",
                         {"brief": {"purpose": "game_character"}})
    if status != 200:
        record("plan-checkpoint: write the game_character brief", False, f"PUT {status}")
        return
    plan = {
        "title": "预算内检查点修订",
        "steps": [
            {"capability": "text-to-3d", "model": MESHY_TEXT_TO_3D, "purpose": "生成角色底模"},
            {"capability": "remesh", "model": MESHY_REMESH, "purpose": "减面到游戏预算"},
            {"capability": "rigging", "model": MESHY_RIGGING, "purpose": "绑骨"},
        ],
        "budgets": [{"unit": "legacy.credits", "amount": 100}],
        "checkpoints": [{"after_capability": "remesh", "checks": ["triangles", "pivot_bottom_center"]}],
        "contingencies": [
            {"id": "low_poly", "trigger": ["triangles"],
             "step": {"capability": "remesh", "model": MESHY_REMESH, "purpose": "按用途面数预算再减面"}},
            {"id": "pivot", "trigger": ["pivot_bottom_center"],
             "step": {"capability": "resize", "model": MESHY_RESIZE, "purpose": "把原点移到底部中心"}},
        ],
    }
    marker = "[[fake:tool:propose_production_plan " + json.dumps(plan, ensure_ascii=False, separators=(",", ":")) + "]]"
    status, _ = _send_chat(api, session, {"content": "按计划做角色 " + marker,
                                          "creative_domain": "model-3d", "ai_model": MESHY_TEXT_TO_3D,
                                          "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
    if status not in (200, 201, 202):
        record("plan-checkpoint: propose the plan", False, f"send {status}")
        return
    budget_id = ""
    end = time.time() + 30
    while time.time() < end:
        pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
        card = next((item for item in pending if item.get("kind") == "plan"), None)
        if card:
            budget_id = card.get("request_id") or ""
            acceptance = (card.get("plan") or {}).get("acceptance") or []
            break
        time.sleep(0.4)
    if not budget_id:
        record("plan-checkpoint: plan card appears", False, "no plan card")
        return
    record("plan-checkpoint: the card shows acceptance criteria and a split estimate",
           len(acceptance) > 0 and bool(card.get("step_totals")) and bool(card.get("contingency_totals")),
           f"acceptance={len(acceptance)} step={card.get('step_totals')} contingencies={card.get('contingency_totals')}")
    status, approved = api.json("PUT", f"/api/v1/chat/sessions/{session}/budgets/{budget_id}",
                                decision_body(api, "approve"))
    if status != 200 or approved.get("state") != "active":
        record("plan-checkpoint: approve the plan", False, f"PUT {status} {approved}")
        return

    create = ('[[fake:tool:create_model_3d {"prompt":"a plain cube","model":"meshy-7.1"}]]')
    model, why, _ = agent_tool_turn(api, session, "生成底模 " + create, creative_domain="model-3d",
                                    ai_model=MESHY_TEXT_TO_3D)
    if not model or model.get("state") != "SUCCEEDED":
        record("plan-checkpoint: the budget generates the model", False, f"{why} {model}")
        return
    record("plan-checkpoint: the budget generates the model without a card", True,
           f"task {model.get('id')}")

    steps = [
        {"capability": "remesh", "offering_id": MESHY_REMESH,
         "parameters": {"topology": "triangle", "target_polycount": 100000}},
        {"capability": "rigging", "offering_id": MESHY_RIGGING, "parameters": {}},
    ]
    processing = {"source_task_id": model.get("id"), "steps": steps}
    marker = "[[fake:tool:plan_model_processing " + json.dumps(processing, ensure_ascii=False, separators=(",", ":")) + "]]"
    status, _ = _send_chat(api, session, {"content": "建立加工计划 " + marker,
                                          "creative_domain": "model-3d",
                                          "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
    if status not in (200, 201, 202):
        record("plan-checkpoint: create the processing plan", False, f"send {status}")
        return
    paused = _wait_processing_plan(work, session,
                                   lambda plan: plan["state"] == "PAUSED" and plan["failure"] == "plan_checkpoint_failed")
    if not paused:
        record("plan-checkpoint: the inherited checkpoint fails and pauses", False, "no paused plan")
        return
    checkpoint_step = next((step for step in paused["steps"] if step.get("checkpoint")), {})
    checkpoint = checkpoint_step.get("checkpoint") or {}
    results = checkpoint.get("results") or []
    record("plan-checkpoint: the server evaluates the inherited checkpoint after remesh",
           checkpoint_step.get("capability") == "remesh" and checkpoint_step.get("state") == "SUCCEEDED" and
           checkpoint.get("state") == "failed" and
           any(item.get("check") == "triangles" and item.get("status") == "fail" for item in results),
           f"checkpoint={checkpoint.get('state')} results={[(item.get('check'), item.get('status')) for item in results]}")
    processing_tasks = [task for task in session_tasks(api, session)
                        if task.get("capability") in ("remesh", "rigging", "resize")]
    record("plan-checkpoint: the next processing step (rigging) does not start",
           len(processing_tasks) == 1 and not any(task.get("capability") == "rigging" for task in processing_tasks),
           f"processing tasks {[(t.get('capability'), t.get('state')) for t in processing_tasks]}")

    # The Agent reads the paused plan through the tool surface: plan_id came
    # from the plan result, and get_processing_plan must report the current
    # version (otherwise revise can never match) and the actionable failure.
    plan_results = _tool_results(work, "main", session, "plan_model_processing")
    plan_id = plan_results[-1].get("plan_id") if plan_results else paused["id"]
    marker = "[[fake:tool:get_processing_plan " + json.dumps({"plan_id": plan_id}, ensure_ascii=False) + "]]"
    status, _ = _send_chat(api, session, {"content": "读取暂停的加工计划 " + marker,
                                          "creative_domain": "model-3d",
                                          "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
    if status not in (200, 201, 202):
        record("plan-checkpoint: get_processing_plan after the pause", False, f"send {status}")
        return
    _quality_turn_done(api, session, timeout=60)
    observed = None
    end = time.time() + 30
    while time.time() < end and observed is None:
        observed = next((result for result in reversed(_tool_results(work, "main", session, "get_processing_plan"))
                         if result.get("plan_id") == plan_id), None)
        if observed is None:
            time.sleep(0.3)
    observed_version = (observed or {}).get("version")
    detail = (observed or {}).get("failure_detail") or {}
    record("plan-checkpoint: the read-only plan tool reports the paused version",
           observed is not None and observed.get("state") == "PAUSED" and
           observed.get("failure") == "plan_checkpoint_failed" and
           isinstance(observed_version, int) and observed_version == paused["version"],
           f"observed={observed_version} paused={paused['version']} state={(observed or {}).get('state')}")
    record("plan-checkpoint: a failed checkpoint is classified as an actionable parameter problem",
           detail.get("category") == "parameter_invalid" and detail.get("next_action") == "fix_parameters",
           f"failure_detail={detail}")

    revision = {
        "plan_id": plan_id, "expected_plan_version": observed_version,
        "operation": "insert", "step_index": 0, "contingency_id": "low_poly",
        "parameters": {"topology": "triangle", "target_polycount": 20000},
        "reason": "检查点未通过：把原点移到底部中心",
    }
    marker = "[[fake:tool:revise_processing_plan " + json.dumps(revision, ensure_ascii=False, separators=(",", ":")) + "]]"
    status, _ = _send_chat(api, session, {"content": "按预案修订 " + marker,
                                          "creative_domain": "model-3d",
                                          "domainModels": {"model-3d": MESHY_TEXT_TO_3D}})
    if status not in (200, 201, 202):
        record("plan-checkpoint: revise insert", False, f"send {status}")
        return
    finished = _wait_processing_plan(work, session, lambda plan: plan["state"] in ("SUCCEEDED", "FAILED"))
    if not finished or finished["state"] != "SUCCEEDED":
        record("plan-checkpoint: the revision finishes inside the budget", False,
               f"plan {finished.get('state') if finished else None}/{finished.get('failure') if finished else None}")
        return
    inserted = next((step for step in finished["steps"]
                     if step.get("contingency_id") == "low_poly"), {})
    record("plan-checkpoint: the declared contingency ran as an insert",
           inserted.get("capability") == "remesh" and
           inserted.get("revision_operation") == "insert" and inserted.get("state") == "SUCCEEDED",
           f"inserted={inserted.get('capability')}/{inserted.get('revision_operation')}/{inserted.get('state')}")
    record("plan-checkpoint: rigging finished after the revision",
           finished["steps"][-1].get("capability") == "rigging" and finished["steps"][-1].get("state") == "SUCCEEDED",
           f"steps {[(step.get('capability'), step.get('state')) for step in finished['steps']]}")
    total = sum(int(step.get("estimated_credits") or 0) for step in finished["steps"])
    record("plan-checkpoint: the worst-case estimate stays within the approved 100",
           total <= 100, f"estimated {total} legacy.credits")
    pending = api.json("GET", f"/api/v1/chat/sessions/{session}/pending-approvals")[1].get("items") or []
    plans = _processing_plans(work, "main", session)
    record("plan-checkpoint: no second plan card and no pending approval",
           len(pending) == 0 and len(plans) == 1,
           f"pending={[(item.get('kind'), item.get('status')) for item in pending]} plans={len(plans)}")



def _concept_card(api, session):
    for card in _choice_cards(api, session):
        if card.get("state") == "pending" and card.get("kind") == "concept_direction":
            return card
    return None


def _fresh_tasks(api, session, before):
    return [task for task in session_tasks(api, session)
            if (task.get("id") or task.get("task_id")) not in before]


def _wait_fresh_tasks(api, session, before, count, seconds=120):
    end = time.time() + seconds
    while time.time() < end:
        fresh = _fresh_tasks(api, session, before)
        if len(fresh) >= count:
            return fresh
        time.sleep(0.5)
    return _fresh_tasks(api, session, before)


def scenario_concept_candidates(api, offerings, work):
    """#242: an open 2D request draws 3 concept sketches in one reply and opens
    a 3-option direction card whose thumbnails point at those sketches; the
    answer continues with refine_image. A budget plan declares 2 candidates of
    one step, the card shows the expanded candidate steps, and the approved
    continuation submits both 3D calls in one reply with different seeds."""
    session = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
    if status != 200:
        record("#242 concept sketches", False, f"approval auto {status}")
        return
    image_text_model, image_edit_model = IMAGE_OFFERINGS["meshy"]
    directions = [
        ("realistic", "写实", "真实材质、自然光与可信比例",
         "photorealistic forest guardian concept art, natural materials, soft studio lighting, no text"),
        ("cartoon", "卡通", "明快轮廓、夸张比例与高饱和配色",
         "stylized cartoon forest guardian concept art, bold clean outlines, exaggerated proportions, vivid colors"),
        ("low_poly", "低多边形", "几何切面、简洁色块与风格化造型",
         "low-poly stylized forest guardian concept art, faceted geometry, flat color blocks"),
    ]
    markers = []
    options = []
    for index, (option_id, label, description, prompt) in enumerate(directions):
        markers.append("[[fake:tool:create_concept_sketch " + json.dumps(
            {"prompt": prompt}, ensure_ascii=False, separators=(",", ":")) + "]]")
        options.append({
            "id": option_id, "label": label, "description": description,
            "thumbnail": "@img" + str(index + 1),
            "arguments": {"prompt": prompt, "reference_handles": ["@img" + str(index + 1)]},
        })
    markers.append("[[fake:tool:ask_choice " + json.dumps(
        {"kind": "concept_direction", "question": "想沿着哪个方向深入？", "options": options},
        ensure_ascii=False, separators=(",", ":")) + "]]")
    before = {(task.get("id") or task.get("task_id")) for task in session_tasks(api, session)}
    status, _ = _send_chat(api, session, {
        "content": "画一个森林守卫，先给几个方向 " + " ".join(markers),
        "creative_domain": "image",
        "ai_model": image_text_model,
        "domainModels": {"image": image_text_model},
    })
    if status not in (200, 201, 202):
        record("#242 concept sketches", False, f"send {status}")
        return
    card = None
    end = time.time() + 150
    while time.time() < end:
        card = _concept_card(api, session)
        if card is not None:
            break
        time.sleep(0.5)
    fresh = _fresh_tasks(api, session, before)
    record("#242 one reply draws three concept sketches", len(fresh) == 3 and
           all(task.get("state") == "SUCCEEDED" for task in fresh),
           f"tasks={[(task.get('id'), task.get('state'), task.get('capability')) for task in fresh]}")
    if card is None:
        record("#242 concept direction card", False, "no pending concept_direction card")
        return
    card_options = card.get("options") or []
    record("#242 the direction card carries three options with thumbnails",
           [option.get("id") for option in card_options] == [direction[0] for direction in directions]
           and all(option.get("thumbnail_url") for option in card_options)
           and all("arguments" not in option for option in card_options),
           f"options={[(option.get('id'), option.get('thumbnail_url')) for option in card_options]}")

    # Answer on the card: the fake continuation calls refine_image with the
    # chosen direction's sketch as its reference.
    chosen = next((option for option in card_options if option.get("id") == "cartoon"), card_options[0])
    continuation = "[[fake:tool:refine_image " + json.dumps(
        {"prompt": "stylized cartoon forest guardian concept art, refined full body",
         "reference_handles": ["@img2"]}, ensure_ascii=False, separators=(",", ":")) + "]]"
    before = {(task.get("id") or task.get("task_id")) for task in session_tasks(api, session)}
    status, _ = api.json("POST", f"/api/chat/sessions/{session}/messages", {
        "content": "沿着卡通方向继续 " + continuation,
        "choiceAnswer": {"choice_id": card["id"], "option_id": chosen["id"]},
        "creative_domain": "image",
        "ai_model": image_edit_model,
        "domainModels": {"image": image_edit_model},
    })
    if status not in (200, 201, 202):
        record("#242 direction answer continues", False, f"answer {status}")
        return
    fresh = _wait_fresh_tasks(api, session, before, 1)
    fresh = [wait_task(api, task.get("id") or task.get("task_id")) for task in fresh]
    prepared = _latest_prepared_input(work, "main", session)
    normalized = prepared.get("normalized_input") or {}
    references = normalized.get("reference_assets") or []
    record("#242 the chosen direction continues with refine_image and its sketch",
           len(fresh) == 1 and fresh[0].get("state") == "SUCCEEDED"
           and "cartoon forest guardian" in str(normalized.get("prompt") or "")
           and bool(references),
           f"tasks={[(task.get('id'), task.get('state')) for task in fresh]} "
           f"prompt={str(normalized.get('prompt'))[:80]!r} references={len(references)}")

    # 3D candidates: the plan declares two candidates of one step; the server
    # expands the plan card and the approved continuation submits both.
    session3d = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session3d}/approval", {"approvalMode": "budget"})
    if status != 200:
        record("#242 candidate plan card", False, f"approval {status}")
        return
    plan = {
        "title": "两个 3D 候选",
        "steps": [{"capability": "text-to-3d", "model": TRIPO_P2_TEXT_TO_3D, "purpose": "森林守卫候选",
                   "candidates": {"source": "seed", "count": 2}}],
        "budgets": [{"unit": "tripo.credit", "amount": 5000}],
    }
    marker = "[[fake:tool:propose_production_plan " + json.dumps(
        plan, ensure_ascii=False, separators=(",", ":")) + "]]"
    status, _ = _send_chat(api, session3d, {
        "content": "做两个候选 " + marker, "creative_domain": "model-3d",
        "domainModels": {"model-3d": TRIPO_P2_TEXT_TO_3D},
    })
    if status not in (200, 201, 202):
        record("#242 candidate plan card", False, f"send {status}")
        return
    budget_id, card_plan = "", {}
    end = time.time() + 30
    while time.time() < end:
        pending = api.json("GET", f"/api/v1/chat/sessions/{session3d}/pending-approvals")[1].get("items") or []
        plan_card = next((item for item in pending if item.get("kind") == "plan"), None)
        if plan_card:
            budget_id = plan_card.get("request_id") or ""
            card_plan = plan_card.get("plan") or {}
            break
        time.sleep(0.4)
    steps = card_plan.get("steps") or []
    record("#242 the plan card expands two candidates",
           bool(budget_id) and len(steps) == 2
           and [step.get("candidate_group") for step in steps] == [1, 1]
           and [step.get("candidate_index") for step in steps] == [1, 2],
           f"budget={budget_id} steps={[(step.get('candidate_group'), step.get('candidate_index')) for step in steps]}")
    if not budget_id:
        return
    status, approved = api.json("PUT", f"/api/v1/chat/sessions/{session3d}/budgets/{budget_id}",
                                decision_body(api, "approve"))
    if status != 200 or approved.get("state") != "active":
        record("#242 approving the candidate plan", False, f"PUT {status} {approved}")
        return
    calls = [
        "[[fake:tool:create_model_3d " + json.dumps(
            {"prompt": "a forest guardian, game-ready", "model": TRIPO_P2_TEXT_TO_3D,
             "model_seed": 111}, ensure_ascii=False, separators=(",", ":")) + "]]",
        "[[fake:tool:create_model_3d " + json.dumps(
            {"prompt": "a forest guardian, game-ready", "model": TRIPO_P2_TEXT_TO_3D,
             "model_seed": 222}, ensure_ascii=False, separators=(",", ":")) + "]]",
    ]
    before = {(task.get("id") or task.get("task_id")) for task in session_tasks(api, session3d)}
    status, _ = _send_chat(api, session3d, {
        "content": "并行提交两个候选 " + " ".join(calls), "creative_domain": "model-3d",
        "domainModels": {"model-3d": TRIPO_P2_TEXT_TO_3D},
    })
    if status not in (200, 201, 202):
        record("#242 parallel 3D candidates", False, f"send {status}")
        return
    fresh = _wait_fresh_tasks(api, session3d, before, 2)
    fresh = [wait_task(api, task.get("id") or task.get("task_id")) for task in fresh]
    seeds = []
    for task in fresh:
        frozen = _visual_anchor_frozen_input(work, task.get("id") or task.get("task_id"))
        seeds.append(frozen.get("model_seed"))
    record("#242 one reply submits two 3D candidates with different seeds",
           len(fresh) == 2 and all(task.get("state") == "SUCCEEDED" for task in fresh)
           and len(set(seeds)) == 2 and all(seed is not None for seed in seeds),
           f"tasks={[(task.get('id'), task.get('state')) for task in fresh]} seeds={seeds}")


def scenario_reroll(api, offerings, work):
    """#242: "one more" replays a succeeded task's frozen input with fresh
    seeds. The same Idempotency-Key returns the same task; manual mode waits
    for a person's decision."""
    session = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session}/approval", {"approvalMode": "auto"})
    if status != 200:
        record("#242 reroll", False, f"approval {status}")
        return
    create = "[[fake:tool:create_model_3d " + json.dumps(
        {"prompt": "a reroll cube", "model": TRIPO_P2_TEXT_TO_3D, "model_seed": 111},
        ensure_ascii=False, separators=(",", ":")) + "]]"
    task, why, _ = agent_tool_turn(api, session, "生成一个模型 " + create,
                                   creative_domain="model-3d", ai_model=TRIPO_P2_TEXT_TO_3D)
    if not task or task.get("state") != "SUCCEEDED":
        record("#242 reroll", False, why or f"task={task}")
        return
    source_id = task.get("id") or task.get("task_id")
    before_input = _visual_anchor_frozen_input(work, source_id)
    status, rerolled = api.json("POST", f"/api/v1/tasks/{source_id}/reroll", {}, {"Idempotency-Key": "concept-reroll-1"})
    new_id = (rerolled or {}).get("task_id") or ""
    same = api.json("POST", f"/api/v1/tasks/{source_id}/reroll", {}, {"Idempotency-Key": "concept-reroll-1"})[1]
    rerolled_task = wait_task(api, new_id) if new_id else {}
    new_input = _visual_anchor_frozen_input(work, new_id) if new_id else {}
    record("#242 reroll submits a new task, replays the same key and changes the seed",
           status == 202 and rerolled.get("status") == "submitted" and new_id and new_id != source_id
           and same.get("task_id") == new_id and same.get("replayed") is True
           and rerolled_task.get("state") == "SUCCEEDED"
           and new_input.get("model_seed") != before_input.get("model_seed"),
           f"status={status} new={new_id} replay={same.get('task_id')}/{same.get('replayed')} "
           f"state={rerolled_task.get('state')} seeds={before_input.get('model_seed')}->{new_input.get('model_seed')}")

    # Strict/manual mode: a 3D reroll waits for the person's decision.
    session_manual = new_session(api)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session_manual}/approval", {"approvalMode": "manual"})
    if status != 200:
        record("#242 strict reroll waits for a decision", False, f"approval {status}")
        return
    create = "[[fake:tool:create_model_3d " + json.dumps(
        {"prompt": "a strict reroll cube", "model": TRIPO_P2_TEXT_TO_3D},
        ensure_ascii=False, separators=(",", ":")) + "]]"
    task, why, _ = agent_tool_turn(api, session_manual, "生成一个模型 " + create,
                                   creative_domain="model-3d", ai_model=TRIPO_P2_TEXT_TO_3D)
    if not task or task.get("state") != "SUCCEEDED":
        record("#242 strict reroll waits for a decision", False, why or f"task={task}")
        return
    manual_id = task.get("id") or task.get("task_id")
    status, waiting = api.json("POST", f"/api/v1/tasks/{manual_id}/reroll", {}, {"Idempotency-Key": "strict-reroll-1"})
    record("#242 strict mode returns 202 awaiting_decision",
           status == 202 and (waiting or {}).get("status") == "awaiting_decision"
           and (waiting or {}).get("request_id") and (waiting or {}).get("idempotency_key") == "reroll:" + manual_id + ":strict-reroll-1",
           f"status={status} body={waiting}")


# ---------------------------------------------------------------- asset lists (#327)

# Every rigging Offering the shipped Catalog enables; the availability arm
# disables all of them so the text-to-3d-rig route has no runnable second step.
# text-to-3d itself stays enabled: a chat tool narrows its model choices from
# the enabled text-to-3d Offerings and the Catalog refuses a tool without one.
ASSET_LIST_RIGGING = (
    "meshy/processing/rigging",
    "tripo/processing/rigging",
    "tripo/processing/rigging-v3",
)


def _asset_list_marker(title, items):
    arguments = json.dumps({"title": title, "items": items}, ensure_ascii=False, separators=(",", ":"))
    return "[[fake:tool:propose_asset_list " + arguments + "]]"


def _asset_list_turn(api, work, stack, session, title, items, seconds=60,
                     content=None, creative_domain="model-3d"):
    """One chat turn whose fake LLM answer proposes the list; waits for the
    turn's final reply and returns (tool result JSON, why). content replaces
    the default message, so one turn can carry crop markers beside the
    proposal; creative_domain picks the tool set the fake may call."""
    before = assistant_reply_count(api, session)
    status, _ = _send_chat(api, session, {
        "content": content or ("提出制作清单 " + _asset_list_marker(title, items)),
        "creative_domain": creative_domain,
    })
    if status not in (200, 201, 202):
        return None, f"send {status}"
    end = time.time() + seconds
    while time.time() < end and assistant_reply_count(api, session) <= before:
        time.sleep(0.5)
    if assistant_reply_count(api, session) <= before:
        return None, "the proposal turn produced no final reply"
    end = time.time() + 10
    results = []
    while time.time() < end:
        results = _tool_results(work, stack, session, "propose_asset_list")
        if results:
            break
        time.sleep(0.5)
    if not results:
        return None, "the proposal tool left no result"
    return results[-1], ""


def _asset_list_setup(api, name):
    """Creates the project and its first session; returns (session, project, why)."""
    status, project = api.json("POST", "/api/v1/projects", {"name": name})
    project_id = (project or {}).get("id") or ""
    if status != 201 or not project_id:
        return "", "", f"create project {status} {json.dumps(project, ensure_ascii=False)[:120]}"
    status, session = api.json("POST", "/api/chat/sessions", {"title": "", "project_id": project_id})
    session_id = (session or {}).get("id") or ""
    if status not in (200, 201) or not session_id:
        return "", project_id, f"create session {status} {json.dumps(session, ensure_ascii=False)[:120]}"
    return session_id, project_id, ""


def _asset_list_summaries(api, session):
    status, body = api.json("GET", f"/api/v1/chat/sessions/{session}/asset-lists")
    return (body or {}).get("data") or [] if status == 200 else []


def _project_asset_lists(api, project_id):
    status, body = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists")
    return (body or {}).get("data") or [] if status == 200 else []


def scenario_asset_list_main(api, work):
    """#327 (a-f, i, j): the proposal, detail, edit, confirm, replace, reject,
    2D-only and foreign-owner arms on the shipped Catalog."""
    session, project_id, why = _asset_list_setup(api, "资产清单冒烟")
    if why:
        record("#327 create the asset list project", False, why)
        return
    character = {"kind": "character", "name": "主角 阿岚", "description": "红发剑士，全身绑骨", "route_id": "text-to-3d-rig"}
    prop = {"kind": "prop", "name": "长剑", "description": "单手长剑，低模"}
    result, why = _asset_list_turn(api, work, "asset-list", session, "第一章 角色与道具", [character, prop])
    if result is None:
        record("#327 the agent proposes a draft and the session lists it", False, why)
        return
    list_id = str(result.get("list_id") or "")
    summary = next((item for item in _asset_list_summaries(api, session) if item.get("id") == list_id), {})
    record("#327 the agent proposes a draft and the session lists it",
           bool(list_id) and result.get("version") == 0 and result.get("status") == "draft"
           and result.get("item_count") == 2 and result.get("planned_step_count") == 3
           and result.get("planned_model3d_step_count") == 3 and not result.get("unavailable_items")
           and summary.get("status") == "draft" and summary.get("effective_status") == "draft"
           and bool(summary.get("origin_message_id")) and summary.get("item_count") == 2
           and summary.get("planned_step_count") == 3 and summary.get("planned_model3d_step_count") == 3,
           f"tool={json.dumps(result, ensure_ascii=False)[:240]} summary={json.dumps(summary, ensure_ascii=False)[:200]}")
    if not list_id:
        return

    status, detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
    items = (detail or {}).get("items") or []
    first = items[0] if items else {}
    second = items[1] if len(items) > 1 else {}
    first_route = first.get("route") or []
    second_route = second.get("route") or []
    record("#327 the detail keeps kinds, routes and planned steps",
           status == 200 and detail.get("version") == 0 and detail.get("status") == "draft"
           and detail.get("effective_status") == "draft" and len(items) == 2
           and first.get("kind") == "character" and first.get("name") == character["name"]
           and first.get("route_id") == "text-to-3d-rig"
           and [(step.get("capability"), step.get("available")) for step in first_route]
           == [("text-to-3d", True), ("rigging", True)]
           and all(step.get("offering_id") for step in first_route)
           and first.get("planned_step_count") == 2 and first.get("planned_model3d_step_count") == 2
           and first.get("state") == "todo"
           and second.get("kind") == "prop" and second.get("name") == prop["name"]
           and second.get("route_id") == "text-to-3d"
           and [(step.get("capability"), step.get("available")) for step in second_route]
           == [("text-to-3d", True)]
           and second.get("planned_step_count") == 1 and second.get("planned_model3d_step_count") == 1,
           f"items={[(item.get('kind'), item.get('route_id'), [(step.get('capability'), step.get('available')) for step in item.get('route') or []]) for item in items]}")

    edited_items = [
        {"kind": "character", "name": "主角 阿岚", "description": "红发剑士，全身绑骨", "route_id": "text-to-3d-rig"},
        {"kind": "vehicle", "name": "霜狼战车", "description": "双人雪地载具"},
    ]
    edit_body = {"title": "第一章 角色与载具", "items": edited_items, "brief": {}, "expected_version": 0}
    status, edited = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}", edit_body)
    record("#327 an edit replaces the draft and bumps the version",
           status == 200 and edited.get("version") == 1 and edited.get("title") == "第一章 角色与载具"
           and [(item.get("kind"), item.get("name")) for item in edited.get("items") or []]
           == [("character", "主角 阿岚"), ("vehicle", "霜狼战车")]
           and edited.get("item_count") == 2 and edited.get("planned_step_count") == 3
           and edited.get("planned_model3d_step_count") == 3,
           f"status={status} version={edited.get('version')} "
           f"items={[(item.get('kind'), item.get('route_id')) for item in edited.get('items') or []]}")
    status, stale = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}", edit_body)
    record("#327 an edit with a stale expected_version is refused",
           status == 409 and stale.get("code") == "conflict",
           f"status={status} code={stale.get('code')}")

    status, confirmed = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                 {"decision": "confirm"})
    record("#327 confirming the draft activates it",
           status == 200 and confirmed.get("status") == "confirmed"
           and confirmed.get("effective_status") == "confirmed" and bool(confirmed.get("decided_at")),
           f"status={status} state={confirmed.get('status')} version={confirmed.get('version')}")
    status, repeated = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                {"decision": "confirm"})
    record("#327 repeating the confirmation is idempotent",
           status == 200 and repeated.get("status") == "confirmed"
           and repeated.get("version") == confirmed.get("version"),
           f"status={status} state={repeated.get('status')} version={repeated.get('version')}")
    summary = next((item for item in _project_asset_lists(api, project_id) if item.get("id") == list_id), {})
    record("#327 the project list shows the confirmed state",
           summary.get("status") == "confirmed" and summary.get("effective_status") == "confirmed"
           and summary.get("planned_step_count") == 3 and summary.get("planned_model3d_step_count") == 3,
           json.dumps(summary, ensure_ascii=False)[:200])

    second_result, why = _asset_list_turn(api, work, "asset-list", session, "第二章 雪山营地",
                                          [{"kind": "scene", "name": "雪山营地", "description": "远景营地"}])
    second_id = str((second_result or {}).get("list_id") or "")
    if not second_id:
        record("#327 a second proposal becomes another draft", False, why)
    else:
        record("#327 a second proposal becomes another draft",
               second_result.get("status") == "draft" and second_result.get("item_count") == 1,
               json.dumps(second_result, ensure_ascii=False)[:200])
        status, conflict = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{second_id}/decision",
                                    {"decision": "confirm"})
        record("#327 confirming while another list is active conflicts",
               status == 409 and conflict.get("code") == "asset_list_active_conflict"
               and conflict.get("active_list_id") == list_id
               and conflict.get("active_list_title") == "第一章 角色与载具",
               f"status={status} {json.dumps(conflict, ensure_ascii=False)[:200]}")
        replace_status, replaced = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{second_id}/decision",
                                            {"decision": "replace"})
        previous_status, previous = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
        record("#327 replace activates the new list and supersedes the old one",
               replace_status == 200 and replaced.get("status") == "confirmed"
               and previous_status == 200 and previous.get("status") == "superseded"
               and previous.get("superseded_by") == second_id,
               f"replace={replace_status}/{replaced.get('status')} previous={previous.get('status')}/"
               f"{previous.get('superseded_by')}")

    reject_result, why = _asset_list_turn(api, work, "asset-list", session, "第三章 备用道具",
                                          [{"kind": "prop", "name": "备用手斧", "description": "备用低模"}])
    reject_id = str((reject_result or {}).get("list_id") or "")
    if not reject_id:
        record("#327 rejecting a draft closes it", False, why)
    else:
        status, rejected = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{reject_id}/decision",
                                    {"decision": "reject", "reason": "暂不制作"})
        record("#327 rejecting a draft closes it",
               status == 200 and rejected.get("status") == "rejected"
               and rejected.get("effective_status") == "rejected"
               and rejected.get("decision_reason") == "暂不制作",
               f"status={status} state={rejected.get('status')} reason={rejected.get('decision_reason')}")

    image_result, why = _asset_list_turn(api, work, "asset-list", session, "第四章 2D 素材",
                                         [{"kind": "image", "name": "界面图标", "description": "一组 2D 图标"}])
    image_id = str((image_result or {}).get("list_id") or "")
    if not image_id:
        record("#327 a 2D-only list plans no 3D steps", False, why)
    else:
        status, image_detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{image_id}")
        image_items = (image_detail or {}).get("items") or []
        image_item = image_items[0] if image_items else {}
        image_route = image_item.get("route") or []
        image_summary = next((item for item in _asset_list_summaries(api, session) if item.get("id") == image_id), {})
        record("#327 a 2D-only list plans no 3D steps",
               image_result.get("item_count") == 1 and image_result.get("planned_step_count") == 1
               and image_result.get("planned_model3d_step_count") == 0
               and status == 200 and image_detail.get("planned_step_count") == 1
               and image_detail.get("planned_model3d_step_count") == 0
               and image_summary.get("planned_model3d_step_count") == 0
               and image_item.get("route_id") == "text-to-image"
               and [(step.get("capability"), step.get("available")) for step in image_route]
               == [("text-to-image", True)]
               and image_item.get("planned_model3d_step_count") == 0,
               f"tool={json.dumps(image_result, ensure_ascii=False)[:200]} "
               f"summary={json.dumps(image_summary, ensure_ascii=False)[:160]}")

    # A different persisted user sees neither the project's lists nor one list.
    owner = Api(api.base)
    status, _signed_in = owner.json("POST", "/api/auth/login",
                                    {"username": MCP_OWNER_USERNAME, "password": MCP_OWNER_PASSWORD})
    detail_status, _ = owner.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{image_id or list_id}")
    lists_status, _ = owner.json("GET", f"/api/v1/projects/{project_id}/asset-lists")
    record("#327 a foreign owner reads the lists as missing",
           status == 200 and detail_status == 404 and lists_status == 404,
           f"login={status} detail={detail_status} project lists={lists_status}")


def scenario_asset_list_unavailable(api, work):
    """#327 (h): a route whose second step has no enabled Offering stays on the
    list with its reason, and the planned steps are still counted."""
    session, project_id, why = _asset_list_setup(api, "资产清单不可用路线")
    if why:
        record("#327 an unavailable route stays with its reason and planned step", False, why)
        return
    result, why = _asset_list_turn(api, work, "asset-list-unavailable", session, "无路可走的清单",
                                   [{"kind": "character", "name": "无路角色", "description": "绑骨能力已全部禁用",
                                     "route_id": "text-to-3d-rig"}])
    if result is None:
        record("#327 an unavailable route stays with its reason and planned step", False, why)
        return
    list_id = str(result.get("list_id") or "")
    status, detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
    items = (detail or {}).get("items") or []
    route = (items[0].get("route") if items else None) or []
    unavailable = result.get("unavailable_items") or []
    record("#327 an unavailable route stays with its reason and planned step",
           bool(list_id) and result.get("status") == "draft"
           and result.get("planned_step_count") == 2 and result.get("planned_model3d_step_count") == 2
           and len(unavailable) == 1 and unavailable[0].get("position") == 1
           and unavailable[0].get("name") == "无路角色" and bool(unavailable[0].get("reason"))
           and status == 200 and detail.get("planned_model3d_step_count") == 2
           and len(items) == 1 and items[0].get("planned_step_count") == 2
           and items[0].get("planned_model3d_step_count") == 2
           and [(step.get("capability"), step.get("available")) for step in route]
           == [("text-to-3d", True), ("rigging", False)]
           and bool(route[-1].get("reason")),
           f"tool={json.dumps(result, ensure_ascii=False)[:260]} route={route}")


def scenario_asset_list_expiry(api, work):
    """#327 (g): a one-second draft TTL reads as expired without a write and
    refuses the decision with asset_list_expired."""
    session, project_id, why = _asset_list_setup(api, "资产清单草稿过期")
    if why:
        record("#327 an expired draft reads as expired and refuses the decision", False, why)
        return
    result, why = _asset_list_turn(api, work, "asset-list-ttl", session, "一秒草稿",
                                   [{"kind": "prop", "name": "过期长剑", "description": "草稿会过期"}])
    list_id = str((result or {}).get("list_id") or "")
    if not list_id:
        record("#327 an expired draft reads as expired and refuses the decision", False, why)
        return
    time.sleep(1.5)
    status, detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
    decision_status, decision = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                         {"decision": "confirm"})
    record("#327 an expired draft reads as expired and refuses the decision",
           status == 200 and detail.get("status") == "draft"
           and detail.get("effective_status") == "expired"
           and decision_status == 409 and decision.get("code") == "asset_list_expired",
           f"detail={detail.get('status')}/{detail.get('effective_status')} "
           f"decision={decision_status}/{decision.get('code')}")


# ---------------------------------------------------------------- asset list crops (#331)

# The synthetic scene picture of the crop scenario: a 4x3 grid of 64x64 solid
# blocks, so every region's pixels are predictable. Crops are cut at original
# resolution and compared pixel by pixel with these rows.
CROP_SOURCE_WIDTH = 256
CROP_SOURCE_HEIGHT = 192
CROP_REGION_A = {"x": 0, "y": 0, "width": 0.25, "height": 0.5}
CROP_REGION_B = {"x": 0.25, "y": 0.5, "width": 0.5, "height": 0.5}
# CROP_PIXEL_UNION is the bounding box of A and B; the edit's crop instruction
# re-cuts that merged region.
CROP_REGION_UNION = {"x": 0, "y": 0, "width": 0.75, "height": 1}
CROP_PIXEL_A = (0, 0, 64, 96)
CROP_PIXEL_B = (64, 96, 192, 192)
CROP_PIXEL_UNION = (0, 0, 192, 192)


def _crop_source_picture():
    """The deterministic scene picture: one solid colour per 64x64 block."""
    blocks = [
        ((column * 64, row * 64, (column + 1) * 64, (row + 1) * 64),
         (32 + column * 48, 48 + row * 56, 160 - column * 24))
        for row in range(CROP_SOURCE_HEIGHT // 64) for column in range(CROP_SOURCE_WIDTH // 64)
    ]
    return make_picture(CROP_SOURCE_WIDTH, CROP_SOURCE_HEIGHT, (20, 20, 20), blocks)


def _crop_marker(regions):
    arguments = json.dumps({"image": "@img1", "regions": regions}, ensure_ascii=False, separators=(",", ":"))
    return "[[fake:tool:crop_image_regions " + arguments + "]]"


def _crop_region(label, region):
    return {"label": label, "region": region}


def _crop_turn(api, work, stack, session, content, image_urls=None, seconds=60):
    """One chat turn on the image domain, which is where crop_image_regions
    lives; waits for the turn's final reply and returns every crop tool result
    the conversation kept so far, or (None, why)."""
    before = assistant_reply_count(api, session)
    body = {"content": content, "creative_domain": "image"}
    if image_urls:
        body["image_urls"] = image_urls
    status, _ = _send_chat(api, session, body)
    if status not in (200, 201, 202):
        return None, f"send {status}"
    end = time.time() + seconds
    while time.time() < end and assistant_reply_count(api, session) <= before:
        time.sleep(0.5)
    if assistant_reply_count(api, session) <= before:
        return None, "the crop turn produced no final reply"
    return _tool_results(work, stack, session, "crop_image_regions"), ""


def _wait_crop_label(work, stack, session, label, seconds=20):
    """Waits for the crop result carrying one label and returns it."""
    end = time.time() + seconds
    result = None
    while time.time() < end:
        result = _crop_result_by_label(_tool_results(work, stack, session, "crop_image_regions"), label)
        if result is not None:
            return result
        time.sleep(0.5)
    return result


def _crop_result_by_label(results, label):
    for result in results:
        for crop in result.get("crops") or []:
            if crop.get("label") == label:
                return result
    return None


def _crop_refusal(results, label):
    for result in results:
        if result.get("success") is False and "(" + label + ")" in str(result.get("error") or ""):
            return result
    return None


def _crop_asset_id(result):
    crops = (result or {}).get("crops") or []
    return str(crops[0].get("input_asset_id") or "") if len(crops) == 1 else ""


def _crop_normalized_matches(normalized, region):
    """One wire normalized region equals the requested region."""
    try:
        return all(abs(float(normalized.get(key)) - region[key]) < 1e-9
                   for key in ("x", "y", "width", "height"))
    except (TypeError, ValueError):
        return False


def _crop_matches(result, pixel, region):
    """One crop's asset id, pixel rectangle and normalized region as asked."""
    crops = (result or {}).get("crops") or []
    if len(crops) != 1:
        return False
    crop = crops[0]
    want = {"x0": pixel[0], "y0": pixel[1], "x1": pixel[2], "y1": pixel[3]}
    if crop.get("pixel_rect") != want or not str(crop.get("input_asset_id") or ""):
        return False
    normalized = crop.get("normalized_region") or {}
    return _crop_normalized_matches(normalized, region)


def _crop_asset_check(api, source_rows, asset_id, pixel):
    """Downloads one crop and compares it pixel by pixel with the source
    rectangle; returns (ok, detail)."""
    import l5_common

    status, raw, _headers = api.call("GET", f"/api/v1/assets/{asset_id}")
    if status != 200:
        return False, f"download {asset_id}: {status}"
    try:
        width, height, rows = l5_common.decode_png(raw)
    except ValueError as error:
        return False, f"decode {asset_id}: {error}"
    x0, y0, x1, y1 = pixel
    if (width, height) != (x1 - x0, y1 - y0):
        return False, f"{asset_id}: {width}x{height}, want {x1 - x0}x{y1 - y0}"
    for y in range(height):
        if rows[y] != source_rows[y0 + y][3 * x0:3 * x1]:
            return False, f"{asset_id}: row {y} differs from the source rectangle"
    return True, f"{asset_id}: {width}x{height} matches the source rectangle"


def _sqlite_count(work, stack, query, parameters=()):
    import sqlite3
    try:
        db = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        (count,) = db.execute(query, parameters).fetchone()
        db.close()
        return count
    except sqlite3.Error:
        return -1


def _balance_amounts(body):
    return {account.get("provider"): (account.get("native_balance") or {}).get("amount")
            for account in (body or {}).get("accounts") or []}


def scenario_asset_list_crops(api, work, stack):
    """#331 (b): a synthetic scene picture is decomposed with crop_image_regions
    and proposed with reference_input_asset_ids. Cuts are pixel-compared with
    the source at original resolution, a repeated rectangle keeps one asset
    across calls and turns, refused regions store nothing, an edit's crop
    instruction re-cuts the merged union region, and the confirmed list shows
    on the board. No Task, step or charge happens anywhere in the flow."""
    import l5_common

    source = _crop_source_picture()
    source_id = upload_asset(api, "scene-grid.png", source, "image/png")
    if not source_id:
        record("#331 the synthetic scene picture uploads", False, "upload refused")
        return
    record("#331 the synthetic scene picture uploads", True,
           f"{CROP_SOURCE_WIDTH}x{CROP_SOURCE_HEIGHT} {source_id}")
    session, project_id, why = _asset_list_setup(api, "场景图拆解冒烟")
    if why:
        record("#331 create the crop scenario project", False, why)
        return
    _status, balance_before = api.json("GET", "/api/meshy/balance")

    # Round 1: two real cuts, the same rectangle again in the same reply, and
    # two refused regions (past the edge, under the shortest-side minimum).
    markers = " ".join([
        _crop_marker([_crop_region("hero", CROP_REGION_A)]),
        _crop_marker([_crop_region("sword", CROP_REGION_B)]),
        _crop_marker([_crop_region("hero-again", CROP_REGION_A)]),
        _crop_marker([_crop_region("out", {"x": 0.9, "y": 0.9, "width": 0.2, "height": 0.2})]),
        _crop_marker([_crop_region("tiny", {"x": 0.5, "y": 0.5, "width": 0.1, "height": 0.1})]),
    ])
    results, why = _crop_turn(api, work, stack, session, "把场景图里的主体都裁出来 " + markers,
                              image_urls=[f"/api/v1/assets/{source_id}"])
    if results is None:
        record("#331 the first crop turn runs", False, why)
        return
    hero = _wait_crop_label(work, stack, session, "hero")
    sword = _wait_crop_label(work, stack, session, "sword")
    again = _wait_crop_label(work, stack, session, "hero-again")
    out = _crop_refusal(_tool_results(work, stack, session, "crop_image_regions"), "out")
    tiny = _crop_refusal(_tool_results(work, stack, session, "crop_image_regions"), "tiny")
    hero_id, sword_id = _crop_asset_id(hero), _crop_asset_id(sword)
    record("#331 the first turn cuts both regions at original resolution",
           bool(hero_id) and bool(sword_id)
           and (hero.get("source_width"), hero.get("source_height")) == (CROP_SOURCE_WIDTH, CROP_SOURCE_HEIGHT)
           and (sword.get("source_width"), sword.get("source_height")) == (CROP_SOURCE_WIDTH, CROP_SOURCE_HEIGHT)
           and _crop_matches(hero, CROP_PIXEL_A, CROP_REGION_A)
           and _crop_matches(sword, CROP_PIXEL_B, CROP_REGION_B),
           f"hero={json.dumps(hero, ensure_ascii=False)[:200]} "
           f"sword={json.dumps(sword, ensure_ascii=False)[:200]}")
    record("#331 a repeated rectangle in the same turn returns the same asset",
           bool(hero_id) and _crop_asset_id(again) == hero_id,
           f"hero={hero_id} again={_crop_asset_id(again)}")
    record("#331 a region past the source edge is refused",
           out is not None and out.get("code") == "invalid_region",
           json.dumps(out, ensure_ascii=False)[:200])
    record("#331 a region under the shortest-side minimum is refused",
           tiny is not None and tiny.get("code") == "image_too_small",
           json.dumps(tiny, ensure_ascii=False)[:200])
    count_first = _sqlite_count(work, stack,
                                "SELECT COUNT(*) FROM input_asset_crops WHERE source_asset_id=?", (source_id,))
    record("#331 refused regions store no crop asset",
           count_first == 2, f"provenance rows={count_first}, want 2")

    # Round 2: the same rectangle across turns, and the proposal whose items
    # directly name the first turn's crops.
    items = [
        {"kind": "character", "name": "主角 阿岚", "description": "红发剑士",
         "route_id": "text-to-3d", "reference_input_asset_ids": [hero_id]},
        {"kind": "prop", "name": "长剑", "description": "单手长剑",
         "route_id": "text-to-3d", "reference_input_asset_ids": [sword_id]},
    ]
    cross = _crop_marker([_crop_region("hero-cross", CROP_REGION_A)])
    content = "提出制作清单并再确认一次同一区域 " + cross + " " + _asset_list_marker("场景拆解清单", items)
    proposal, why = _asset_list_turn(api, work, stack, session, "场景拆解清单", items,
                                     content=content, creative_domain="image")
    list_id = str((proposal or {}).get("list_id") or "")
    record("#331 the second turn proposes the list with crop references",
           proposal is not None and proposal.get("status") == "draft"
           and proposal.get("item_count") == 2 and bool(list_id),
           why or json.dumps(proposal, ensure_ascii=False)[:240])
    cross_result = _wait_crop_label(work, stack, session, "hero-cross")
    record("#331 a repeated rectangle across turns returns the same asset",
           bool(hero_id) and _crop_asset_id(cross_result) == hero_id,
           f"hero={hero_id} cross={_crop_asset_id(cross_result)}")
    status, detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
    detail_items = (detail or {}).get("items") or []
    references = [[reference.get("input_asset_id") for reference in item.get("references") or []]
                  for item in detail_items]
    record("#331 the draft's items reference exactly the cut assets",
           status == 200 and (detail or {}).get("version") == 0
           and references == [[hero_id], [sword_id]],
           f"status={status} references={references} want {[[hero_id], [sword_id]]}")

    source_width, source_height, source_rows = l5_common.decode_png(source)
    hero_ok, hero_detail = _crop_asset_check(api, source_rows, hero_id, CROP_PIXEL_A)
    sword_ok, sword_detail = _crop_asset_check(api, source_rows, sword_id, CROP_PIXEL_B)
    record("#331 both crops decode to their source rectangles, pixel for pixel",
           (source_width, source_height) == (CROP_SOURCE_WIDTH, CROP_SOURCE_HEIGHT)
           and hero_ok and sword_ok, f"{hero_detail}; {sword_detail}")

    # The edit merges A and B: its crop instruction re-cuts the union region
    # and replaces the first item's crop reference with the new asset.
    edit_items = [
        {"kind": "character", "name": "主角 阿岚", "description": "红发剑士", "route_id": "text-to-3d",
         "references": [{"input_asset_id": hero_id}],
         "crop": {"source_input_asset_id": source_id, "region": CROP_REGION_UNION}},
        {"kind": "prop", "name": "长剑", "description": "单手长剑", "route_id": "text-to-3d",
         "references": [{"input_asset_id": sword_id}]},
    ]
    status, edited = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}",
                              {"title": "场景拆解清单（合并）", "items": edit_items,
                               "brief": {}, "expected_version": 0})
    edited_items = (edited or {}).get("items") or []
    first_references = [reference.get("input_asset_id")
                        for reference in (edited_items[0].get("references") or [])] if edited_items else []
    union_id = first_references[0] if len(first_references) == 1 else ""
    references_detail = [[reference.get("input_asset_id")
                          for reference in (item.get("references") or [])]
                         for item in edited_items]
    record("#331 the edit's crop instruction re-cuts the merged region",
           status == 200 and (edited or {}).get("version") == 1 and len(edited_items) == 2
           and bool(union_id) and union_id not in (hero_id, sword_id) and first_references == [union_id]
           and [reference.get("input_asset_id")
                for reference in (edited_items[1].get("references") or [])] == [sword_id],
           f"status={status} version={(edited or {}).get('version')} union={union_id} "
           f"references={references_detail}")
    union_ok, union_detail = _crop_asset_check(api, source_rows, union_id, CROP_PIXEL_UNION)
    count_edit = _sqlite_count(work, stack,
                               "SELECT COUNT(*) FROM input_asset_crops WHERE source_asset_id=?", (source_id,))
    record("#331 the merged crop matches the union rectangle and adds one provenance row",
           union_ok and count_edit == 3, f"{union_detail}; provenance rows={count_edit}, want 3")

    status, confirmed = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                 {"decision": "confirm"})
    board_status, board = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/board")
    board_list = (board or {}).get("list") or {}
    board_items = (board or {}).get("items") or []
    record("#331 the confirmed list appears on the board with both items",
           status == 200 and (confirmed or {}).get("status") == "confirmed"
           and board_status == 200 and board_list.get("id") == list_id
           and board_list.get("status") == "confirmed" and board_list.get("item_count") == 2
           and [item.get("name") for item in board_items] == ["主角 阿岚", "长剑"],
           f"decision={status}/{(confirmed or {}).get('status')} board={board_status} "
           f"items={[item.get('name') for item in board_items]}")

    # #331: each board item carries its crop reference and the same provenance
    # snapshot the list detail exposes: the edit's union crop on the first
    # item, the sword crop on the second. The scenario never completes an item,
    # so a result-free item must not name a representative asset.
    board_crop_checks = []
    for item, reference_id, region, pixel in zip(
            board_items, (union_id, sword_id), (CROP_REGION_UNION, CROP_REGION_B),
            (CROP_PIXEL_UNION, CROP_PIXEL_B)):
        source = item.get("source") or {}
        cut = item.get("region") or {}
        rect = cut.get("pixel_rect") or {}
        no_result = (item.get("result") or {}) == {}
        board_crop_checks.append(
            item.get("reference_asset_id") == reference_id
            and source.get("source_input_asset_id") == source_id
            and source.get("source_kind") == "input_asset"
            and (source.get("source_width"), source.get("source_height"))
            == (CROP_SOURCE_WIDTH, CROP_SOURCE_HEIGHT)
            and _crop_normalized_matches(cut, region)
            and (rect.get("x0"), rect.get("y0"), rect.get("x1"), rect.get("y1")) == pixel
            and ((no_result and "representative_asset_id" not in item)
                 or (not no_result and bool(item.get("representative_asset_id")))))
    record("#331 the board names each item's crop reference, source and region",
           len(board_items) == 2 and all(board_crop_checks),
           json.dumps([{key: item.get(key) for key in
                        ("name", "reference_asset_id", "source", "region",
                         "representative_asset_id")}
                       for item in board_items], ensure_ascii=False)[:500])

    # The whole flow is read-only: no public Task was created (so no 3D step
    # was counted) and the provider balances did not move.
    _status, balance_after = api.json("GET", "/api/meshy/balance")
    record("#331 the decomposition creates no Task, step or charge",
           not session_tasks(api, session)
           and _sqlite_count(work, stack, "SELECT COUNT(*) FROM generation_tasks") == 0
           and _balance_amounts(balance_before) == _balance_amounts(balance_after),
           f"tasks={len(session_tasks(api, session))} balances={_balance_amounts(balance_after)}")


# ---------------------------------------------------------------- asset list runs (#328)

# The dev mock login's owner id (stack_env's DEV_MOCK_USER); the presence rows
# are owner-scoped, so the heartbeat must name the same person the API sees.
ASSET_LIST_RUN_OWNER = "fake_stack"

# The continuation a confirmed list's runner writes as each item turn's user
# message. The fake script advances per assistant tool-call message after that
# user message: the first round creates one 3D task, then reports it done. The
# item name and round ride inside the frozen arguments, so two items and two
# rounds are distinguishable in the Tasks the fake upstream serves.
ASSET_LIST_RUN_CONTINUATION = (
    "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。"
    "先检查已有结果，不要重复提交已完成的步骤。"
    '[[fake:script:create_model_3d {"prompt":"a production asset for {name}, round {round}",'
    '"model":"tripo/tripo-p2/text-to-3d"}'
    '|submit_asset_list_item {"status":"done"}|done]]'
)

# The failure arm reports every item failed without creating a task; the run
# must still turn to the next item instead of stopping at the first one.
ASSET_LIST_RUN_FAILURE_CONTINUATION = (
    "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。"
    '[[fake:script:submit_asset_list_item {"status":"failed","reason":"fake run failure"}|done]]'
)

# The step-cap arm creates one task in round one and ends the turn without a
# report; round two's second create is refused at the account's one-step cap.
ASSET_LIST_RUN_CAP_CONTINUATION = (
    "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。"
    '[[fake:script:create_model_3d {"prompt":"a production asset for {name}, round {round}",'
    '"model":"tripo/tripo-p2/text-to-3d"}|done]]'
)


def _asset_list_presence(api, work, stack, project_id, present=True, tab_id="asset-list-run-tab",
                         age_seconds=0):
    """Sends one browser tab's heartbeat through the real presence route.

    age_seconds backdates the stored present row instead of waiting the shipped
    minimum 30-second grace window; backdating only shortens the wait, the route
    itself has its own unit tests. The runner still compares the row through its
    real PresenceStore port."""
    status, _ = api.json("POST", f"/api/v1/projects/{project_id}/presence",
                         {"tab_id": tab_id, "state": "present" if present else "leave"})
    if status not in (200, 204):
        raise RuntimeError(f"presence heartbeat for {project_id}/{tab_id} answered {status}")
    if not present or age_seconds <= 0:
        return
    import sqlite3
    database = os.path.join(work, stack, "app.db")
    connection = sqlite3.connect(database, timeout=10)
    try:
        stamp = time.strftime("%Y-%m-%dT%H:%M:%S",
                              time.gmtime(time.time() - age_seconds)) + ".000000000Z"
        changed = connection.execute(
            "UPDATE asset_list_presence SET last_seen_at = ? "
            "WHERE owner_id = ? AND project_id = ? AND tab_id = ?",
            (stamp, ASSET_LIST_RUN_OWNER, project_id, tab_id)).rowcount
        connection.commit()
    finally:
        connection.close()
    if changed != 1:
        raise RuntimeError(f"presence row for {project_id}/{tab_id} is missing; cannot backdate it")


def _asset_list_detail(api, project_id, list_id):
    status, detail = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}")
    return detail if status == 200 else {}


def _asset_list_run_of(detail):
    return (detail or {}).get("run") or {}


def _asset_list_counts(detail):
    return _asset_list_run_of(detail).get("item_counts") or {}


def _asset_list_all_in(detail, state, count):
    """The run counts and the items of one answer both show count items in
    state. The detail derives its counts from its own items (#390); checking
    both keeps a wait from settling on a torn answer."""
    items = (detail or {}).get("items") or []
    return (_asset_list_counts(detail).get(state) == count
            and len(items) == count and all(item.get("state") == state for item in items))


def _wait_asset_list_run(api, project_id, list_id, predicate, seconds=120, beat=None):
    """Polls one list until predicate holds. beat is the simulated browser
    heartbeat, written about once a second inside the 5-second Catalog interval."""
    end = time.time() + seconds
    detail = {}
    last_beat = 0.0
    while time.time() < end:
        detail = _asset_list_detail(api, project_id, list_id)
        if detail and predicate(detail):
            return detail
        if beat is not None and time.time() - last_beat >= 1.0:
            beat()
            last_beat = time.time()
        time.sleep(0.25)
    return detail


def _asset_list_item_tasks(api, item):
    session = (item or {}).get("session_id") or ""
    return session_tasks(api, session) if session else []


def _asset_list_messages(api, session):
    status, body = api.json("GET", f"/api/chat/sessions/{session}/messages")
    if status != 200:
        return []
    return body if isinstance(body, list) else body.get("messages") or []


def _asset_list_user_messages(api, session):
    return [message for message in _asset_list_messages(api, session)
            if message.get("role") == "user"]


def _asset_list_turn_rows(work, stack, sessions):
    """The persisted chat turn rows of the item sessions, read like _tool_results."""
    import sqlite3
    if not sessions:
        return 0
    try:
        database = sqlite3.connect(f"file:{work}/{stack}/app.db?mode=ro", uri=True)
        placeholders = ",".join("?" for _ in sessions)
        count = database.execute(
            f"SELECT COUNT(*) FROM go_chat_turns WHERE session_id IN ({placeholders})",
            tuple(sessions)).fetchone()[0]
        database.close()
        return count
    except sqlite3.Error:
        return -1


def _asset_list_run_arm_progress(work, stack_binary, processes, catalog):
    """A confirmed two-item list advances both items to review with no user
    message, pauses when the heartbeat stops, resumes when it returns, and does
    not repeat a turn or task when the stack restarts on the same database."""
    stack = "asset-list-run"
    child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
    try:
        session, project_id, why = _asset_list_setup(api, "自动推进清单")
        if why:
            record("#328 run: create the project and session", False, why)
            return
        beat = lambda: _asset_list_presence(api, work, stack, project_id)
        beat()
        result, why = _asset_list_turn(api, work, stack, session, "自动推进清单", [
            {"kind": "prop", "name": "自动长矛", "description": "低模长矛"},
            {"kind": "prop", "name": "自动盾牌", "description": "低模圆盾"},
        ])
        list_id = str((result or {}).get("list_id") or "")
        if not list_id:
            record("#328 run: the agent proposes the list", False, why)
            return
        status, _ = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                     {"decision": "confirm"})
        # The decision answer carries the list but not the freshly stored run
        # (run summaries decorate the read routes), so read it back at once.
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("status") == "running"
            and _asset_list_counts(value).get("todo") == 2, seconds=10, beat=beat)
        confirmed_run = _asset_list_run_of(detail)
        record("#328 run: confirming stores a running run with both items todo",
               status == 200 and confirmed_run.get("status") == "running"
               and confirmed_run.get("item_counts", {}).get("todo") == 2
               and bool(confirmed_run.get("version")),
               f"status={status} run={json.dumps(confirmed_run, ensure_ascii=False)[:200]}")

        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_all_in(value, "review", 2), seconds=120, beat=beat)
        counts = _asset_list_counts(detail)
        run = _asset_list_run_of(detail)
        items = detail.get("items") or []
        sessions = [item.get("session_id") or "" for item in items]
        item_tasks = [(item.get("name"), _asset_list_item_tasks(api, item)) for item in items]
        continuation_users = [message for item in items
                              for message in _asset_list_user_messages(api, item.get("session_id") or "")] \
            if all(sessions) else []
        record("#328 run: both items reach review without a person's message",
               counts.get("review") == 2 and counts.get("todo") == 0
               and counts.get("working") == 0 and counts.get("failed") == 0
               and run.get("status") == "running"
               and len(items) == 2
               and all(item.get("state") == "review" and item.get("round") == 1
                       and not item.get("state_reason") for item in items)
               and len(_asset_list_user_messages(api, session)) == 1
               and len(continuation_users) == 2
               and all("继续制作清单项" in str(message.get("content")) for message in continuation_users),
               f"counts={counts} items={[(i.get('state'), i.get('round')) for i in items]} "
               f"source user messages={len(_asset_list_user_messages(api, session))}")
        record("#328 run: each item ran exactly one fake upstream 3D step",
               len(items) == 2 and all(sessions)
               and all(len(tasks) == 1 and tasks[0].get("state") == "SUCCEEDED"
                       and tasks[0].get("capability") == "text-to-3d"
                       for _, tasks in item_tasks),
               f"tasks={[(name, [(task.get('state'), task.get('capability')) for task in tasks]) for name, tasks in item_tasks]}")

        # A heartbeat older than the grace window reads as absent (the Catalog's
        # minimum grace is 30 seconds, so the row is backdated instead of slept);
        # a paused run must not create another Task.
        _asset_list_presence(api, work, stack, project_id, age_seconds=60)
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("pause_reason") == "presence", seconds=20)
        run = _asset_list_run_of(detail)
        tasks_before = {item.get("id"): len(_asset_list_item_tasks(api, item)) for item in items}
        time.sleep(2)
        tasks_after = {item.get("id"): len(_asset_list_item_tasks(api, item)) for item in items}
        record("#328 run: a stopped heartbeat pauses the run and starts no task",
               run.get("status") == "paused" and run.get("pause_reason") == "presence"
               and tasks_before == tasks_after,
               f"run={run.get('status')}/{run.get('pause_reason')} tasks {tasks_before} -> {tasks_after}")

        beat()
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("status") == "running", seconds=20, beat=beat)
        run = _asset_list_run_of(detail)
        record("#328 run: a returning heartbeat resumes the run",
               run.get("status") == "running" and not run.get("pause_reason")
               and _asset_list_counts(detail).get("review") == 2,
               f"run={run.get('status')}/{run.get('pause_reason')} counts={_asset_list_counts(detail)}")

        # Restarting on the same database reconciles the finished item turns
        # without opening another turn or submitting another Task.
        beat()
        turns_before = _asset_list_turn_rows(work, stack, sessions)
        tasks_before = {item.get("id"): sorted(str(task.get("id") or task.get("task_id"))
                                               for task in _asset_list_item_tasks(api, item))
                        for item in items}
        processes.stop(child)
        child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
        for _ in range(4):
            beat()
            time.sleep(0.5)
        detail = _asset_list_detail(api, project_id, list_id)
        turns_after = _asset_list_turn_rows(work, stack, sessions)
        tasks_after = {item.get("id"): sorted(str(task.get("id") or task.get("task_id"))
                                              for task in _asset_list_item_tasks(api, item))
                       for item in (detail.get("items") or [])}
        record("#328 run: restarting on the same database repeats no turn or task",
               turns_before >= 0 and turns_before == turns_after
               and tasks_before == tasks_after
               and _asset_list_counts(detail).get("review") == 2,
               f"turn rows {turns_before} -> {turns_after} tasks {tasks_before} -> {tasks_after}")
    finally:
        processes.stop(child)


def _asset_list_run_arm_presence(work, stack_binary, processes, catalog):
    """No heartbeat before confirmation: the runner pauses the run before its
    first item turn. One returning heartbeat resumes it and finishes both
    items, so the resume really continues the work rather than only flipping a
    status."""
    stack = "asset-list-run-paused"
    child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
    try:
        session, project_id, why = _asset_list_setup(api, "在场推进清单")
        if why:
            record("#328 run presence: create the project and session", False, why)
            return
        result, why = _asset_list_turn(api, work, stack, session, "在场推进清单", [
            {"kind": "prop", "name": "在场长矛", "description": "低模长矛"},
            {"kind": "prop", "name": "在场圆盾", "description": "低模圆盾"},
        ])
        list_id = str((result or {}).get("list_id") or "")
        if not list_id:
            record("#328 run presence: the agent proposes the list", False, why)
            return
        status, _ = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                                     {"decision": "confirm"})
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("pause_reason") == "presence", seconds=30)
        run = _asset_list_run_of(detail)
        items = detail.get("items") or []
        record("#328 run presence: a run without presence pauses before its first turn",
               status == 200 and run.get("status") == "paused"
               and run.get("pause_reason") == "presence"
               and _asset_list_counts(detail).get("todo") == 2
               and all(not item.get("session_id") for item in items),
               f"status={status} run={run.get('status')}/{run.get('pause_reason')} "
               f"counts={_asset_list_counts(detail)} sessions={[bool(i.get('session_id')) for i in items]}")

        beat = lambda: _asset_list_presence(api, work, stack, project_id)
        beat()
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_all_in(value, "review", 2), seconds=120, beat=beat)
        items = detail.get("items") or []
        item_tasks = [_asset_list_item_tasks(api, item) for item in items]
        record("#328 run presence: a returning heartbeat resumes the run and finishes both items",
               _asset_list_run_of(detail).get("status") == "running"
               and _asset_list_counts(detail).get("review") == 2
               and len(items) == 2 and all(item.get("state") == "review" for item in items)
               and all(len(tasks) == 1 and tasks[0].get("state") == "SUCCEEDED" for tasks in item_tasks),
               f"run={_asset_list_run_of(detail).get('status')} counts={_asset_list_counts(detail)} "
               f"items={[item.get('state') for item in items]} "
               f"tasks={[[task.get('state') for task in tasks] for tasks in item_tasks]}")
    finally:
        processes.stop(child)


def _asset_list_run_arm_failure(work, stack_binary, processes):
    """A report of failure fails only its own item: the runner turns to the next
    item, and two reported failures complete the run instead of pausing it."""
    stack = "asset-list-run-failure"
    catalog = private_catalog(
        work, name="catalog-asset-list-run-failure.json",
        execution_enabled=True, poll_interval_milliseconds=500,
        presence_heartbeat_seconds=5, presence_grace_seconds=30,
        max_parallel_items=1, max_rounds_per_item=2,
        continuation_message=ASSET_LIST_RUN_FAILURE_CONTINUATION)
    child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
    try:
        session, project_id, why = _asset_list_setup(api, "失败隔离清单")
        if why:
            record("#328 run failure: create the project and session", False, why)
            return
        beat = lambda: _asset_list_presence(api, work, stack, project_id)
        beat()
        result, why = _asset_list_turn(api, work, stack, session, "失败隔离清单", [
            {"kind": "prop", "name": "失败长矛", "description": "低模长矛"},
            {"kind": "prop", "name": "失败圆盾", "description": "低模圆盾"},
        ])
        list_id = str((result or {}).get("list_id") or "")
        if not list_id:
            record("#328 run failure: the agent proposes the list", False, why)
            return
        status, _ = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                             {"decision": "confirm"})
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_counts(value).get("failed") == 2
            and _asset_list_run_of(value).get("status") == "completed", seconds=60, beat=beat)
        run = _asset_list_run_of(detail)
        items = detail.get("items") or []
        item_tasks = [_asset_list_item_tasks(api, item) for item in items]
        record("#328 run failure: a reported failure fails only its item and the next still runs",
               status == 200 and _asset_list_counts(detail).get("failed") == 2
               and _asset_list_counts(detail).get("todo") == 0
               and run.get("status") == "completed" and not run.get("pause_reason")
               and len(items) == 2
               and all(item.get("state") == "failed" and item.get("session_id")
                       and item.get("state_reason") == "fake run failure" for item in items)
               and all(not tasks for tasks in item_tasks),
               f"run={run.get('status')}/{run.get('pause_reason')} counts={_asset_list_counts(detail)} "
               f"items={[(i.get('state'), i.get('state_reason')) for i in items]} "
               f"tasks={[[task.get('state') for task in tasks] for tasks in item_tasks]}")
    finally:
        processes.stop(child)


def _asset_list_run_arm_step_cap(work, stack_binary, processes):
    """An account list cap of one lets round one create its task; round two's
    create is refused, so the item stays working, the run pauses at step_cap and
    the second item never starts or spends a step."""
    stack = "asset-list-run-cap"
    catalog = private_catalog(
        work, name="catalog-asset-list-run-cap.json",
        execution_enabled=True, poll_interval_milliseconds=500,
        presence_heartbeat_seconds=5, presence_grace_seconds=30,
        max_parallel_items=1, max_rounds_per_item=2,
        continuation_message=ASSET_LIST_RUN_CAP_CONTINUATION)
    child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
    try:
        session, project_id, why = _asset_list_setup(api, "步数上限清单")
        if why:
            record("#328 run step cap: create the project and session", False, why)
            return
        beat = lambda: _asset_list_presence(api, work, stack, project_id)
        beat()
        status, policy = api.json("GET", "/api/v1/account/step-policy")
        if status == 200:
            status, capped = api.json("PUT", "/api/v1/account/step-policy", {
                "expected_version": policy.get("version") or 0,
                "list": {"mode": "cap", "cap": 1},
                "mcp": {"mode": "unlimited", "cap": 20},
            })
        else:
            capped = {}
        record("#328 run step cap: the account's list step setting is a cap of one",
               status == 200 and (capped.get("list") or {}).get("mode") == "cap"
               and (capped.get("list") or {}).get("cap") == 1,
               f"status={status} policy={json.dumps(capped, ensure_ascii=False)[:200]}")
        result, why = _asset_list_turn(api, work, stack, session, "步数上限清单", [
            {"kind": "prop", "name": "上限长矛", "description": "低模长矛"},
            {"kind": "prop", "name": "上限圆盾", "description": "低模圆盾"},
        ])
        list_id = str((result or {}).get("list_id") or "")
        if not list_id:
            record("#328 run step cap: the agent proposes the list", False, why)
            return
        status, _ = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                             {"decision": "confirm"})
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("pause_reason") == "step_cap", seconds=90, beat=beat)
        run = _asset_list_run_of(detail)
        items = detail.get("items") or []
        first = items[0] if items else {}
        second = items[1] if len(items) > 1 else {}
        first_tasks = _asset_list_item_tasks(api, first)
        second_tasks = _asset_list_item_tasks(api, second)
        record("#328 run step cap: the second round at the cap blocks the item and pauses the run",
               status == 200 and run.get("status") == "paused"
               and run.get("pause_reason") == "step_cap"
               and first.get("state") == "working" and first.get("round") == 2
               and second.get("state") == "todo" and not second.get("session_id")
               and len(first_tasks) == 1 and first_tasks[0].get("state") == "SUCCEEDED"
               and not second_tasks,
               f"run={run.get('status')}/{run.get('pause_reason')} "
               f"items={[(i.get('state'), i.get('round')) for i in items]} "
               f"tasks={[[task.get('state') for task in tasks] for tasks in (first_tasks, second_tasks)]}")
        time.sleep(2)
        detail = _asset_list_detail(api, project_id, list_id)
        first_after = _asset_list_item_tasks(api, (detail.get("items") or [{}])[0])
        record("#328 run step cap: the paused run submits no further task",
               _asset_list_run_of(detail).get("pause_reason") == "step_cap"
               and len(first_after) == 1,
               f"pause={_asset_list_run_of(detail).get('pause_reason')} tasks={len(first_after)}")
    finally:
        processes.stop(child)


def scenario_asset_list_run(work, stack_binary, processes):
    """#328: a confirmed production list runs itself on the fake stack. Four
    private stacks: the progress arm (both items reach review with no human
    message, a stopped heartbeat pauses and a returning one resumes, a restart
    repeats no turn or task), the presence arm (no heartbeat before
    confirmation pauses the run before its first turn and a heartbeat finishes
    it), the failure arm (a reported failure turns to the next item) and the
    step-cap arm (a one-step account cap blocks round two and pauses the run).
    No arm sends a chat message after the proposal; the fake script in the
    runner's continuation drives every item turn."""
    with open(os.path.join(REPO, "internal/catalog/default_catalog.json"), encoding="utf-8") as handle:
        execution_disabled = json.load(handle)["application"]["asset_list"]["execution"]["enabled"]
    if execution_disabled:
        record("#328 run: the shipped Catalog keeps execution off", False,
               f"execution.enabled={execution_disabled!r}")
        return
    catalog = private_catalog(
        work, name="catalog-asset-list-run.json",
        execution_enabled=True, poll_interval_milliseconds=500,
        presence_heartbeat_seconds=5, presence_grace_seconds=30,
        max_parallel_items=1, max_rounds_per_item=2,
        continuation_message=ASSET_LIST_RUN_CONTINUATION)
    record("#328 run: the shipped Catalog keeps execution off and the private one turns it on", True,
           "execution.enabled=false -> true; poll 500ms, heartbeat 5s, grace 30s, parallel 1, rounds 2")
    _asset_list_run_arm_progress(work, stack_binary, processes, catalog)
    _asset_list_run_arm_presence(work, stack_binary, processes, catalog)
    _asset_list_run_arm_failure(work, stack_binary, processes)
    _asset_list_run_arm_step_cap(work, stack_binary, processes)


def scenario_asset_list(work, stack_binary, processes, catalog):
    """#327: the production list workflow on the fake stack. Three private
    stacks: the shipped Catalog for the workflow, every rigging Offering
    disabled for the availability arm, and a one-second draft TTL for expiry."""
    child, api = start_stack(processes, stack_binary, work, "asset-list", catalog, GOOD_KEY)
    try:
        scenario_asset_list_main(api, work)
    finally:
        processes.stop(child)
    unavailable = private_catalog(work, disabled=ASSET_LIST_RIGGING,
                                  name="catalog-asset-list-unavailable.json")
    child, api = start_stack(processes, stack_binary, work, "asset-list-unavailable", unavailable, GOOD_KEY)
    try:
        scenario_asset_list_unavailable(api, work)
    finally:
        processes.stop(child)
    expiry = private_catalog(work, draft_ttl_seconds=1, name="catalog-asset-list-ttl.json")
    child, api = start_stack(processes, stack_binary, work, "asset-list-ttl", expiry, GOOD_KEY)
    try:
        scenario_asset_list_expiry(api, work)
    finally:
        processes.stop(child)


# ---------------------------------------------------------------- asset list review (#329)

# The runner's continuation and the returned item's rework copy carry the fake
# LLM script: every item round creates one 3D Task and reports the item done,
# so each round has its own Task and the board sees the round it was produced
# in. The rework copy also carries the Schema's return context ({reasons},
# {note}), which the scenario checks in the sub-session's user messages.
ASSET_LIST_REVIEW_CONTINUATION = (
    "继续制作清单项「{name}」（{description}），路线 {route}，这是第 {round} 轮。"
    '[[fake:script:create_model_3d {"prompt":"a review asset for {name}, round {round}",'
    '"model":"tripo/tripo-p2/text-to-3d"}'
    '|submit_asset_list_item {"status":"done"}|done]]'
)

ASSET_LIST_REVIEW_REWORK = (
    "清单项「{name}」（{description}）被打回，这是第 {round} 轮重做。"
    "打回原因：{reasons}。说明：{note}。"
    '[[fake:script:create_model_3d {"prompt":"a reworked asset for {name}, round {round}",'
    '"model":"tripo/tripo-p2/text-to-3d"}'
    '|submit_asset_list_item {"status":"done"}|done]]'
)


def _asset_list_task_assets(api, task_id):
    """The task wire's assets by id: url, size and digest of the delivered bytes."""
    status, task = api.json("GET", f"/api/v1/tasks/{task_id}")
    if status != 200:
        return {}, f"task {task_id}: {status}"
    return {str(asset.get("id")): asset for asset in task.get("assets") or []}, ""


def _asset_list_board(api, project_id, list_id):
    status, board = api.json("GET", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/board")
    return (board if status == 200 else {}), status


def _asset_list_review_decision(api, project_id, list_id, body):
    status, answer = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/review", body)
    return status, answer or {}


def _asset_list_archive(api, project_id, list_id, item_ids=None):
    """GET the batch ZIP; item_ids narrows the selection like the browser does."""
    path = f"/api/v1/projects/{project_id}/asset-lists/{list_id}/archive"
    if item_ids:
        path += "?item_ids=" + ",".join(item_ids)
    return api.call("GET", path)


def _asset_list_review_states(detail):
    return [(item.get("state"), item.get("round")) for item in (detail or {}).get("items") or []]


def _asset_list_review_arrived(detail, accepted, review, round_of_returned):
    """The counts and the items of one answer describe the same board (#394),
    so a wait never settles on a torn read."""
    items = (detail or {}).get("items") or []
    counts = _asset_list_counts(detail)
    states = [item.get("state") for item in items]
    return (len(items) == 2 and counts.get("accepted") == accepted
            and counts.get("review") == review
            and states.count("accepted") == accepted and states.count("review") == review
            and items[1].get("round") == round_of_returned)


def _asset_list_snapshot(item):
    review = (item or {}).get("review")
    return review if isinstance(review, dict) else {}


def _header(headers, name):
    for key, value in (headers or {}).items():
        if key.lower() == name.lower():
            return value
    return ""


def _zip_entries(raw):
    """Decodes the batch ZIP into {name: bytes}; a corrupt archive is refused."""
    import io
    import zipfile
    try:
        archive = zipfile.ZipFile(io.BytesIO(raw))
    except zipfile.BadZipFile as error:
        return None, f"not a ZIP: {error}"
    corrupt = archive.testzip()
    if corrupt is not None:
        return None, f"corrupt entry {corrupt}"
    return {info.filename: archive.read(info.filename) for info in archive.infolist()}, ""


def _zip_manifest(entries):
    if entries is None or "manifest.json" not in entries:
        return {}, "no manifest.json"
    try:
        return json.loads(entries["manifest.json"]), ""
    except ValueError as error:
        return {}, f"manifest.json is not JSON: {error}"


def _asset_list_archive_bytes(entries, manifest, snapshots):
    """Every packed file matches its manifest facts and one artifact of the
    accepted snapshots; returns (problems, files)."""
    if entries is None:
        return ["the archive could not be read"], []
    files = [file for item in (manifest or {}).get("items") or [] for file in item.get("files") or []]
    recorded = [(artifact.get("sha256"), artifact.get("size"))
                for snapshot in snapshots for artifact in snapshot.get("artifacts") or []]
    problems = []
    if sorted(recorded) != sorted((file.get("sha256"), file.get("size")) for file in files):
        problems.append(f"manifest {sorted((f.get('sha256'), f.get('size')) for f in files)} "
                        f"!= snapshot {sorted(recorded)}")
    for file in files:
        body = entries.get(file.get("path"))
        if body is None:
            problems.append(f"missing {file.get('path')}")
        elif len(body) != file.get("size") or hashlib.sha256(body).hexdigest() != file.get("sha256"):
            problems.append(f"{file.get('path')} holds {len(body)} bytes, sha256 "
                            f"{hashlib.sha256(body).hexdigest()[:12]}")
    return problems, files


def scenario_asset_list_review(work, stack_binary, processes):
    """#329: the acceptance board on the fake stack. One private stack with
    execution on: both items reach review, one is accepted (its project asset
    is registered) and the other returned with a declared reason, the returned
    item reworks in its own sub-session to round two, and the batch ZIP packs
    only accepted items with bytes matching every acceptance snapshot. The
    bootstrap administrator reads and decides nothing."""
    stack = "asset-list-review"
    catalog = private_catalog(
        work, name="catalog-asset-list-review.json",
        execution_enabled=True, poll_interval_milliseconds=500,
        presence_heartbeat_seconds=5, presence_grace_seconds=30,
        max_parallel_items=1, max_rounds_per_item=2,
        continuation_message=ASSET_LIST_REVIEW_CONTINUATION,
        rework_message=ASSET_LIST_REVIEW_REWORK)
    child, api = start_stack(processes, stack_binary, work, stack, catalog, GOOD_KEY)
    try:
        session, project_id, why = _asset_list_setup(api, "清单验收看板")
        if why:
            record("#329 review: create the project and session", False, why)
            return
        beat = lambda: _asset_list_presence(api, work, stack, project_id)
        beat()
        result, why = _asset_list_turn(api, work, stack, session, "清单验收看板", [
            {"kind": "prop", "name": "验收长剑", "description": "低模单手长剑"},
            {"kind": "prop", "name": "验收圆盾", "description": "低模圆盾"},
        ])
        list_id = str((result or {}).get("list_id") or "")
        if not list_id:
            record("#329 review: the agent proposes the list", False, why)
            return
        status, _ = api.json("PUT", f"/api/v1/projects/{project_id}/asset-lists/{list_id}/decision",
                             {"decision": "confirm"})
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_all_in(value, "review", 2), seconds=120, beat=beat)
        items = detail.get("items") or []
        sessions = [item.get("session_id") or "" for item in items]
        item_tasks = [_asset_list_item_tasks(api, item) for item in items]
        record("#329 review: both items reach review with one successful step each",
               status == 200 and _asset_list_counts(detail).get("review") == 2
               and len(items) == 2
               and all(item.get("state") == "review" and item.get("round") == 1 for item in items)
               and len(set(sessions)) == 2 and all(sessions)
               and all(len(tasks) == 1 and tasks[0].get("state") == "SUCCEEDED" for tasks in item_tasks),
               f"status={status} counts={_asset_list_counts(detail)} "
               f"items={_asset_list_review_states(detail)} "
               f"tasks={[[task.get('state') for task in tasks] for tasks in item_tasks]}")

        board, board_status = _asset_list_board(api, project_id, list_id)
        board_list = board.get("list") or {}
        board_items = board.get("items") or []
        first = board_items[0] if board_items else {}
        second = board_items[1] if len(board_items) > 1 else {}
        first_snapshot = _asset_list_snapshot(first)
        second_snapshot = _asset_list_snapshot(second)

        def snapshot_ready(item, snapshot):
            item_result = item.get("result") or {}
            artifacts = snapshot.get("artifacts") or []
            model = snapshot.get("model3d") or {}
            return (item.get("state") == "review" and item.get("latest_review") is None
                    and snapshot.get("round") == 1 and bool(snapshot.get("captured_at"))
                    and bool(item_result.get("task_id"))
                    and snapshot.get("task_id") == item_result.get("task_id")
                    and bool(item.get("representative_asset_id"))
                    and len(artifacts) > 0
                    and all(artifact.get("asset_id") and artifact.get("size")
                            and artifact.get("sha256")
                            and str(artifact.get("media_type") or "").startswith(("model/", "image/"))
                            for artifact in artifacts)
                    and bool(model.get("asset_id"))
                    and len(model.get("checks") or []) > 0
                    and all(check.get("check") and check.get("status") in ("pass", "fail", "unknown")
                            for check in model.get("checks") or [])
                    and (snapshot.get("steps") or {}).get("model3d", 0) >= 1)

        board_detail = [{
            "state": item.get("state"),
            "task": (item.get("result") or {}).get("task_id"),
            "artifacts": len((item.get("review") or {}).get("artifacts") or []),
            "checks": len(((item.get("review") or {}).get("model3d") or {}).get("checks") or []),
        } for item in board_items]
        record("#329 review: the board renders each item's snapshot before any decision",
               board_status == 200 and board_list.get("id") == list_id
               and board_list.get("status") == "confirmed"
               and isinstance(board_list.get("version"), int)
               and [item.get("name") for item in board_items] == ["验收长剑", "验收圆盾"]
               and snapshot_ready(first, first_snapshot) and snapshot_ready(second, second_snapshot),
               f"board={board_status} list={board_list.get('status')} "
               f"version={board_list.get('version')} items={board_detail}")
        version = board_list.get("version")

        empty_status, empty_raw, _empty_headers = _asset_list_archive(api, project_id, list_id)
        try:
            empty_body = json.loads(empty_raw)
        except ValueError:
            empty_body = {}
        record("#329 review: the batch archive refuses while nothing is accepted",
               empty_status == 409 and empty_body.get("code") == "asset_list_archive_empty",
               f"status={empty_status} code={empty_body.get('code')}")

        stale_status, stale = _asset_list_review_decision(api, project_id, list_id, {
            "decision": "accept", "item_ids": [first.get("id")],
            "expected_version": max(0, (version or 0) - 1)})
        record("#329 review: a stale list version is refused",
               stale_status == 409 and stale.get("code") == "conflict",
               f"status={stale_status} code={stale.get('code')}")

        accept_status, accepted = _asset_list_review_decision(api, project_id, list_id, {
            "decision": "accept", "item_ids": [first.get("id")], "expected_version": version})
        accepted_results = accepted.get("items") or []
        accepted_result = accepted_results[0] if accepted_results else {}
        accepted_list = accepted.get("list") or {}
        accepted_items = accepted_list.get("items") or []
        project_asset_id = str(accepted_result.get("project_asset_id") or "")
        assets_status, assets_body = api.json("GET", f"/api/v1/projects/{project_id}/assets")
        project_assets = {str(asset.get("id")): asset
                          for asset in (assets_body or {}).get("assets") or []}
        project_asset = project_assets.get(project_asset_id) or {}
        record("#329 review: accepting an item registers its project asset",
               accept_status == 200 and accepted_result.get("verdict") == "approved"
               and project_asset_id.startswith("passet_")
               and len(accepted_items) == 2 and accepted_items[0].get("state") == "accepted"
               and accepted_list.get("version") == (version or 0) + 1
               and assets_status == 200
               and project_asset.get("kind") == "prop" and project_asset.get("name") == "验收长剑"
               and len(project_asset.get("models") or []) > 0,
               f"status={accept_status} verdict={accepted_result.get('verdict')} asset={project_asset_id} "
               f"version={accepted_list.get('version')}/{version} "
               f"project asset={json.dumps(project_asset, ensure_ascii=False)[:200]}")

        return_status, returned = _asset_list_review_decision(api, project_id, list_id, {
            "decision": "return", "item_ids": [second.get("id")], "reason_ids": ["style"],
            "note": "比例要再收一点", "expected_version": accepted_list.get("version")})
        returned_results = returned.get("items") or []
        returned_result = returned_results[0] if returned_results else {}
        returned_list = returned.get("list") or {}
        returned_items = returned_list.get("items") or []
        record("#329 review: returning an item records the declared reason and note",
               return_status == 200 and returned_result.get("verdict") == "returned"
               and len(returned_items) == 2
               and returned_items[0].get("state") == "accepted"
               and returned_items[1].get("state") == "returned"
               and returned_items[1].get("round") == 2,
               f"status={return_status} items={[(item.get('state'), item.get('round')) for item in returned_items]}")

        board, _board_status = _asset_list_board(api, project_id, list_id)
        returned_board = next((item for item in board.get("items") or []
                               if item.get("id") == second.get("id")), {})
        latest = returned_board.get("latest_review") or {}
        record("#329 review: the board keeps the returned reason and note",
               latest.get("verdict") == "returned" and latest.get("reason") == "风格不对"
               and latest.get("reasons") == ["style"] and latest.get("note") == "比例要再收一点"
               and latest.get("round") == 1,
               json.dumps(latest, ensure_ascii=False)[:240])

        one_status, one_raw, one_headers = _asset_list_archive(api, project_id, list_id)
        one_entries, one_why = _zip_entries(one_raw) if one_status == 200 else (None, "")
        one_manifest, _one_manifest_why = _zip_manifest(one_entries)
        one_problems, one_files = _asset_list_archive_bytes(one_entries, one_manifest, [first_snapshot])
        one_manifest_items = one_manifest.get("items") or []
        one_paths = [file.get("path") for file in one_files]
        record("#329 review: the batch archive packs only the accepted item",
               one_status == 200 and _header(one_headers, "Content-Type") == "application/zip"
               and len(one_manifest_items) == 1
               and one_manifest_items[0].get("name") == "验收长剑"
               and one_manifest_items[0].get("position") == 1
               and len(one_files) > 0 and not one_problems
               and one_entries is not None
               and list(one_entries) == ["manifest.json", "README.txt"] + one_paths
               and all(str(path).startswith("01-") for path in one_paths)
               and not any(str(path).startswith("02-") for path in one_paths),
               f"status={one_status} zip={one_why} items={[item.get('name') for item in one_manifest_items]} "
               f"files={len(one_files)} problems={one_problems[:3]}")

        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_review_arrived(value, 1, 1, 2), seconds=120, beat=beat)
        reworked_board, reworked_status = _asset_list_board(api, project_id, list_id)
        reworked_list = reworked_board.get("list") or {}
        reworked_items = reworked_board.get("items") or []
        second_after = next((item for item in reworked_items if item.get("id") == second.get("id")), {})
        second_snapshot = _asset_list_snapshot(second_after)
        second_tasks = _asset_list_item_tasks(api, second_after)
        rework_messages = _asset_list_user_messages(api, second_after.get("session_id") or "")
        record("#329 review: the returned item reworks in its own sub-session to round two",
               reworked_status == 200 and _asset_list_review_arrived(detail, 1, 1, 2)
               and second_after.get("state") == "review"
               and second_after.get("round") == 2
               and second_after.get("session_id") == (second.get("session_id") or "")
               and len(second_tasks) == 2
               and all(task.get("state") == "SUCCEEDED" for task in second_tasks)
               and second_snapshot.get("round") == 2
               and second_snapshot.get("task_id") == (second_after.get("result") or {}).get("task_id")
               and len(rework_messages) == 2
               and "被打回" in str(rework_messages[-1].get("content"))
               and "风格不对" in str(rework_messages[-1].get("content"))
               and "比例要再收一点" in str(rework_messages[-1].get("content"))
               and len(_asset_list_user_messages(api, session)) == 1,
               f"items={_asset_list_review_states(detail)} counts={_asset_list_counts(detail)} "
               f"tasks={[task.get('state') for task in second_tasks]} "
               f"snapshot_round={(second_snapshot or {}).get('round')} "
               f"messages={[str(message.get('content'))[:40] for message in rework_messages]}")

        rework_version = reworked_list.get("version")
        final_status, final = _asset_list_review_decision(api, project_id, list_id, {
            "decision": "accept", "item_ids": [second_after.get("id")],
            "expected_version": rework_version})
        final_results = final.get("items") or []
        final_result = final_results[0] if final_results else {}
        final_list = final.get("list") or {}
        final_items = final_list.get("items") or []
        final_asset_id = str(final_result.get("project_asset_id") or "")
        detail = _wait_asset_list_run(
            api, project_id, list_id,
            lambda value: _asset_list_run_of(value).get("status") == "completed"
            and _asset_list_all_in(value, "accepted", 2), seconds=30, beat=beat)
        record("#329 review: accepting the reworked item completes both items and the run",
               final_status == 200 and final_result.get("verdict") == "approved"
               and final_asset_id.startswith("passet_") and final_asset_id != project_asset_id
               and len(final_items) == 2
               and all(item.get("state") == "accepted" for item in final_items)
               and final_list.get("version") == (rework_version or 0) + 1
               and _asset_list_run_of(detail).get("status") == "completed"
               and _asset_list_counts(detail).get("accepted") == 2,
               f"status={final_status} asset={final_asset_id} "
               f"items={_asset_list_review_states(detail)} counts={_asset_list_counts(detail)} "
               f"run={_asset_list_run_of(detail).get('status')}")

        two_status, two_raw, two_headers = _asset_list_archive(api, project_id, list_id)
        two_entries, two_why = _zip_entries(two_raw) if two_status == 200 else (None, "")
        two_manifest, _two_manifest_why = _zip_manifest(two_entries)
        two_problems, two_files = _asset_list_archive_bytes(
            two_entries, two_manifest, [first_snapshot, second_snapshot])
        two_manifest_items = two_manifest.get("items") or []
        expected_paths = ["manifest.json", "README.txt"] + [file.get("path") for file in two_files]
        readme = two_entries.get("README.txt", b"").decode("utf-8", "replace") if two_entries else ""
        download_problems = []
        for snapshot in (first_snapshot, second_snapshot):
            task_assets, task_why = _asset_list_task_assets(api, snapshot.get("task_id") or "")
            if task_why:
                download_problems.append(task_why)
                continue
            for artifact in snapshot.get("artifacts") or []:
                asset = task_assets.get(str(artifact.get("asset_id"))) or {}
                if not asset.get("url"):
                    download_problems.append(f"{artifact.get('asset_id')}: no task asset")
                    continue
                download_status, artifact_raw, _artifact_headers = api.call("GET", asset["url"])
                if (download_status != 200 or len(artifact_raw) != artifact.get("size")
                        or hashlib.sha256(artifact_raw).hexdigest() != artifact.get("sha256")
                        or asset.get("sha256") != artifact.get("sha256")):
                    download_problems.append(f"{artifact.get('asset_id')}: status {download_status}")
        record("#329 review: the batch ZIP lists both accepted items with their round",
               two_status == 200
               and [item.get("name") for item in two_manifest_items] == ["验收长剑", "验收圆盾"]
               and [item.get("position") for item in two_manifest_items] == [1, 2]
               and [item.get("round") for item in two_manifest_items] == [1, 2]
               and [item.get("directory") for item in two_manifest_items] == ["01-验收长剑", "02-验收圆盾"]
               and two_manifest_items[1].get("task_id") == second_snapshot.get("task_id")
               and all(item.get("formats") and item.get("checks") for item in two_manifest_items),
               f"status={two_status} manifest={json.dumps(two_manifest_items, ensure_ascii=False)[:400]}")
        record("#329 review: every packed byte matches its snapshot digest",
               two_entries is not None and not two_problems and not download_problems
               and list(two_entries) == expected_paths
               and len(two_entries or {}) == 2 + len(two_files)
               and all(item.get("files") for item in two_manifest_items)
               and all(any(file.get("format") == "glb" for file in item.get("files") or [])
                       for item in two_manifest_items),
               f"zip={two_why} entries={len(two_entries or {})} files={len(two_files)} "
               f"problems={two_problems[:3]} downloads={download_problems[:3]}")
        disposition = _header(two_headers, "Content-Disposition")
        record("#329 review: the archive answer carries the declared filename and sandbox headers",
               two_status == 200
               and _header(two_headers, "Content-Type") == "application/zip"
               and f'filename="asset-list-{list_id}.zip"' in disposition
               and _header(two_headers, "X-Content-Type-Options") == "nosniff"
               and "sandbox" in _header(two_headers, "Content-Security-Policy")
               and "只包含已通过验收的清单项" in readme
               and "验收长剑" in readme and "验收圆盾" in readme
               and "01-验收长剑" in readme and "02-验收圆盾" in readme
               and f"共 2 项、{len(two_files)} 个文件" in readme,
               f"type={_header(two_headers, 'Content-Type')} disposition={disposition!r} "
               f"readme={readme[:120]!r}")

        selected_status, selected_raw, _selected_headers = _asset_list_archive(
            api, project_id, list_id, item_ids=[second_after.get("id")])
        selected_entries, _selected_why = _zip_entries(selected_raw) if selected_status == 200 else (None, "")
        selected_manifest, _selected_manifest_why = _zip_manifest(selected_entries)
        selected_items = selected_manifest.get("items") or []
        record("#329 review: item_ids narrows the batch to the chosen accepted item",
               selected_status == 200
               and [item.get("name") for item in selected_items] == ["验收圆盾"]
               and [item.get("position") for item in selected_items] == [2],
               f"status={selected_status} items={[item.get('name') for item in selected_items]}")

        owner = Api(api.base)
        status, _signed_in = owner.json("POST", "/api/auth/login",
                                        {"username": MCP_OWNER_USERNAME, "password": MCP_OWNER_PASSWORD})
        board_status, _ = owner.json("GET",
                                     f"/api/v1/projects/{project_id}/asset-lists/{list_id}/board")
        archive_status, _archive_raw, _archive_headers = _asset_list_archive(owner, project_id, list_id)
        review_status, _review = _asset_list_review_decision(owner, project_id, list_id, {
            "decision": "accept", "item_ids": [second_after.get("id")], "expected_version": 0})
        record("#329 review: a foreign owner reads and decides nothing",
               status == 200 and board_status == 404 and archive_status == 404
               and review_status == 404,
               f"login={status} board={board_status} archive={archive_status} review={review_status}")
    finally:
        processes.stop(child)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--only", default="", help="comma-separated: balance,chain,meshy,hyper3d,hyper3d-agent,images,matting-faults,tripo,p2,project,agent,agent-background,agent-quality,image-self-check,image-purpose,next-step,mask-selection,offering-knowledge,work-ledger,self-check,vision-budget,three-view,visual-anchors,plan-checkpoint,plan-state,concept-candidates,reroll,preferences,mcp,asset-list,asset-list-crops,asset-list-run,asset-list-review,faults")
    # #241/#238: the preferences-off arm disables the feature in the private
    # Catalog (maximum_entries=0) without changing the revision.
    parser.add_argument("--preferences", choices=("on", "off"), default="on",
                        help="off disables session preferences in the private Catalog (the #238 'before' arm)")
    parser.add_argument("--faults", default="", help="comma-separated fault scenarios to run (default: all of %s)" % ", ".join(FAULTS))
    parser.add_argument("--work", default="", help="work directory (default: a temporary one, removed afterwards)")
    parser.add_argument("--keep", action="store_true", help="keep the work directory")
    parser.add_argument("--report", default="", help="directory for the #238 agent-quality report (default: a temporary one)")
    args = parser.parse_args()
    selected = set(filter(None, args.only.split(","))) or {"balance", "chain", "meshy", "hyper3d", "images", "matting-faults", "tripo", "p2", "agent", "agent-background", "three-view", "next-step", "mcp", "asset-list", "asset-list-crops", "asset-list-run", "asset-list-review", "faults"}
    work = args.work or tempfile.mkdtemp(prefix="fake-stack-")
    os.makedirs(work, exist_ok=True)
    processes = Processes()
    try:
        fake_binary, stack_binary = build(work)
        catalog = private_catalog(work, preferences=args.preferences != "off")
        url_catalog = private_catalog(work, jimeng_urls=True, preferences=args.preferences != "off")
        require_free(FAKE_PORT, "fake upstream")
        require_free(STACK_PORT, "stack")
        fake_child = processes.start([fake_binary, "-addr", f"127.0.0.1:{FAKE_PORT}", "-artifact-origin", f"https://{ASSET_HOST}"],
                                     {"PATH": os.environ["PATH"]}, os.path.join(work, "fake.log"))
        if not wait_port(FAKE_PORT, fake_child):
            raise RuntimeError(f"the fake upstream did not listen; see {work}/fake.log")
        print(f"# work {work}; fake on :{FAKE_PORT}, stack on :{STACK_PORT}, MCP on :{MCP_PORT}", flush=True)

        if selected & {"balance", "chain", "meshy", "hyper3d", "hyper3d-agent", "images", "matting-faults", "tripo", "p2", "project", "agent", "agent-background", "agent-quality", "image-self-check", "image-purpose", "next-step", "work-ledger", "vision-budget", "three-view", "visual-anchors", "plan-checkpoint", "concept-candidates", "reroll", "mcp"}:
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
            if "matting-faults" in selected:
                scenario_matting_faults(api, offerings)
            if "tripo" in selected:
                scenario_tripo_images(api, offerings)
            if "p2" in selected:
                scenario_p2(api, offerings)
            if "project" in selected:
                scenario_project(api, offerings, work)
            if "agent" in selected:
                scenario_agent(api, offerings)
            if "agent" in selected or "hyper3d-agent" in selected:
                scenario_hyper3d_agent(api, offerings)
            if "agent-background" in selected:
                scenario_agent_background(api, offerings)
            if "mcp" in selected:
                scenario_mcp(api, f"http://127.0.0.1:{MCP_PORT}")
            if "agent-quality" in selected:
                scenario_agent_quality(api, offerings, args.report or os.path.join(work, "agent-quality"), work)
            if "image-self-check" in selected:
                scenario_image_self_check(api, offerings, work)
            if "three-view" in selected:
                scenario_three_view(work, api, offerings)
            if "plan-checkpoint" in selected:
                scenario_plan_checkpoint(work, api, offerings)
            if "concept-candidates" in selected:
                scenario_concept_candidates(api, offerings, work)
            if "reroll" in selected:
                scenario_reroll(api, offerings, work)
            if "image-purpose" in selected:
                scenario_image_purpose(api, offerings, work)
            if "next-step" in selected:
                scenario_next_step(api)
            if "work-ledger" in selected:
                scenario_work_ledger(api, offerings, work)
            if "vision-budget" in selected:
                scenario_vision_budget(api, offerings, work)
            if "visual-anchors" in selected:
                scenario_visual_anchors(api, offerings, work)
            processes.stop(child)
        if "offering-knowledge" in selected:
            offering_catalog = private_catalog(
                work,
                disabled=("tripo/tripo-p2/text-to-3d",),
                prompt_limits={"meshy/meshy-7.1/text-to-3d": 20},
                name="catalog-offering-knowledge.json",
            )
            scenario_offering_knowledge(work, stack_binary, processes, offering_catalog)
        if "self-check" in selected:
            self_check_catalog = private_catalog(work, self_check=True, name="catalog-self-check.json")
            scenario_self_check(work, stack_binary, processes, self_check_catalog)
        if "plan-state" in selected:
            # #410: the plan's go-ahead is the last user message of its turn, so
            # this stack's continuation text carries the fake marker of the step
            # the plan continues with; the shipped text drives a real Agent.
            plan_state_catalog = private_catalog(
                work,
                plan_continuation_message="继续计划 {title} " + _plan_step_marker("the fifth step"),
                name="catalog-plan-state.json",
            )
            scenario_plan_state(work, stack_binary, processes, plan_state_catalog)
        if "faults" in selected:
            for scenario in [name for name in FAULTS if not args.faults or name in args.faults.split(",")]:
                child, api = start_stack(processes, stack_binary, work, "fault-" + scenario, url_catalog, FAULT_KEYS.get(scenario, f"fake-{scenario}"))
                offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
                scenario_fault(api, offerings, scenario)
                processes.stop(child)
        if "mask-selection" in selected:
            mask_catalog = private_catalog(work, manual_image_edits=True, name="catalog-mask.json")
            child, api = start_stack(processes, stack_binary, work, "mask", mask_catalog, GOOD_KEY)
            offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
            scenario_mask_selection(api, offerings, work)
            processes.stop(child)
        if "preferences" in selected:
            # #241: a private Catalog whose compaction keeps one recent round, so
            # the scenario can prove the framed block survives a real compaction.
            preferences_catalog = private_catalog(
                work,
                name="catalog-preferences.json",
                preferences=args.preferences != "off",
                preference_compaction=True,
            )
            child, api = start_stack(processes, stack_binary, work, "preferences", preferences_catalog, GOOD_KEY)
            offerings = {o["id"]: o for o in api.json("GET", "/api/v1/offerings")[1]["offerings"]}
            scenario_preferences(api, offerings, work, enabled=args.preferences != "off")
            processes.stop(child)
        if "asset-list" in selected:
            # #327: its own stacks, because the expiry arm needs a one-second
            # draft TTL and the availability arm needs every rigging Offering
            # disabled; the workflow arm keeps the shipped Catalog.
            scenario_asset_list(work, stack_binary, processes, catalog)
        if "asset-list-crops" in selected:
            # #331: its own stack so the scenario can run alone under --only;
            # it needs only the shipped Catalog.
            child, api = start_stack(processes, stack_binary, work, "asset-list-crops", catalog, GOOD_KEY)
            try:
                scenario_asset_list_crops(api, work, "asset-list-crops")
            finally:
                processes.stop(child)
        if "asset-list-run" in selected:
            # #328: its own stacks, because execution is off in the shipped
            # Catalog and every arm needs a short poll and grace window.
            scenario_asset_list_run(work, stack_binary, processes)
        if "asset-list-review" in selected:
            # #329: its own stack with execution on, because the board needs
            # items that actually reached review.
            scenario_asset_list_review(work, stack_binary, processes)
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
