#!/usr/bin/env python3
"""Paid 3D test-environment acceptance. Run with runtime.env sourced on the dev-test host.

This is an operator script, not product auto-consent. It uses an authenticated
test user to approve each exact prepared task, then verifies every required
model format is served from the Plinth origin with stable cached bytes.
"""

import hashlib
import http.cookiejar
import io
import json
import os
import re
import struct
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


BASE = os.environ.get("ART_AI_TEST_BASE", "http://127.0.0.1:13333").rstrip("/")
TIMEOUT = int(os.environ.get("ART_AI_3D_TIMEOUT", "1200"))
# Rodin Extreme-High may run up to its Offering job timeout (3600 s).
PROVIDER_TIMEOUT = {"hyper3d": 3600}
FORMAT_SETS = {
    "meshy": (
        "meshy/meshy-7.1/text-to-3d",
        {"glb", "obj", "fbx", "stl", "usdz", "3mf"},
        {"prompt": "A simple low-poly wooden stool, plain background"},
    ),
    "tripo": (
        "tripo/tripo-v3.1-full/text-to-3d",
        {"converted.gltf", "converted.usdz", "converted.fbx", "converted.obj", "converted.stl", "converted.3mf"},
        {
            "prompt": "A simple low-poly wooden stool",
            "output_formats": ["GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"],
            "authorize_extra_credits": True,
        },
    ),
    # #188: one required PBR GLB (3 Hyper3D credits); other formats come from
    # the format-completion routes, not this Task.
    "hyper3d": (
        "hyper3d/rodin-gen-2.5/text-to-3d",
        {"glb"},
        {"prompt": "A simple low-poly wooden stool", "authorize_extra_credits": True},
    ),
}


def fail(message):
    raise RuntimeError(message)


def request(opener, method, path, payload=None, headers=None, binary=False):
    address = urllib.parse.urljoin(BASE + "/", path.lstrip("/"))
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    supplied = {"Origin": BASE}
    if data is not None:
        supplied["Content-Type"] = "application/json"
    supplied.update(headers or {})
    req = urllib.request.Request(address, data=data, headers=supplied, method=method)
    try:
        with opener.open(req, timeout=120 if binary else 45) as response:
            contents = response.read()
            return contents if binary else json.loads(contents)
    except urllib.error.HTTPError as error:
        fail(f"{method} {path}: HTTP {error.code}")


def assert_private_response(payload):
    serialized = json.dumps(payload, ensure_ascii=False).lower()
    if re.search(r"https?://|https?%3a%2f%2f", serialized):
        fail("public task API contains an absolute external address")
    for marker in ("meshy.ai", "tripo3d.ai", "hyper3d.ai", "hyper3d.com", "task_id=", "x-api-key"):
        if marker in serialized:
            fail(f"public API exposed private provider marker: {marker}")


def submit(opener, provider, existing_session=None):
    offering, _, model_input = FORMAT_SETS[provider]
    if existing_session:
        session_id = existing_session
    else:
        session = request(opener, "POST", "/api/chat/sessions", {"title": f"3D isolation {provider} {int(time.time())}"})
        session_id = session["id"]
    prepared = request(
        opener,
        "POST",
        "/api/v1/tasks/prepare",
        {"session_id": session_id, "capability": "text-to-3d", "offering_id": offering, "input": model_input},
    )
    prepared_id = prepared["prepared_input_id"]
    execution_key = f"3d-execution-{prepared_id}"
    grant = request(opener, "POST", "/api/v1/tasks/grants", {"prepared_input_id": prepared_id}, {"Idempotency-Key": execution_key})
    grant_id = grant["request_id"]
    # The session approval mode may already have approved this step: the POST
    # then answers status "approved" with approved_by_policy. Only a grant that
    # is really pending needs the operator decision; a decision on an approved
    # grant would be refused (409).
    status = grant.get("status", "")
    if status == "pending":
        request(opener, "PUT", f"/api/v1/tasks/grants/{grant_id}", {"decision": "approve"})
        print(f"{provider} grant {grant_id}: approved by the test operator", flush=True)
    elif status in ("approved", "claimed", "bound"):
        source = "policy" if grant.get("approved_by_policy") else "an earlier decision"
        print(f"{provider} grant {grant_id}: already approved by {source}", flush=True)
    else:
        fail(f"{provider} grant {grant_id} has unexpected status {status!r}")
    accepted = request(
        opener,
        "POST",
        "/api/v1/tasks",
        {"prepared_input_id": prepared_id, "authorization_request_id": grant_id},
        {"Idempotency-Key": execution_key},
    )
    return accepted["task_id"], session_id


def await_task(opener, task_id, timeout=TIMEOUT):
    deadline = time.monotonic() + timeout
    last = ""
    while time.monotonic() < deadline:
        task = request(opener, "GET", f"/api/v1/tasks/{task_id}")
        assert_private_response(task)
        state = task.get("state", "")
        if state != last:
            print(f"task {task_id}: {state}", flush=True)
            last = state
        if state == "SUCCEEDED":
            return task
        if state in ("FAILED", "CANCELED"):
            fail(f"task {task_id} reached {state}; inspect server-side task events")
        time.sleep(8)
    fail(f"task {task_id} did not finish within {timeout}s; last state {last}")


def check_textured_glb(provider, body):
    # The required Rodin slot must be the textured PBR pack, not the extra
    # untextured Draco mesh that Extreme-High also returns (#188).
    magic, version, total, json_length, chunk = struct.unpack_from("<4sIII4s", body)
    if magic != b"glTF" or version != 2 or total != len(body) or chunk != b"JSON":
        fail(f"{provider} model is not a complete GLB 2.0")
    document = json.loads(body[20:20 + json_length])
    if not document.get("materials") or not document.get("images"):
        fail(f"{provider} model has no materials or embedded textures")
    if "KHR_draco_mesh_compression" in document.get("extensionsRequired", []):
        fail(f"{provider} model is the Draco geometry-only mesh")


def check_assets(opener, provider, task):
    expected = FORMAT_SETS[provider][1]
    assets = task.get("assets", [])
    required = {asset.get("variant", ""): asset for asset in assets if asset.get("required")}
    missing = expected - required.keys()
    if missing:
        fail(f"{provider} missing required cached variants: {sorted(missing)}")
    for asset in assets:
        variant = asset.get("variant", "")
        label = f"{asset.get('role', 'asset')}/{variant or '(primary)'}"
        url = asset.get("url", "")
        parsed = urllib.parse.urlparse(url)
        if not url.startswith("/") or url.startswith("//") or parsed.netloc or parsed.scheme:
            fail(f"{provider} {label} has nonlocal asset URL")
        first = request(opener, "GET", url, binary=True)
        second = request(opener, "GET", url, binary=True)
        digest = hashlib.sha256(first).hexdigest()
        if first != second or digest != asset.get("sha256") or len(first) != asset.get("size"):
            fail(f"{provider} {label} failed byte-level cache verification")
        markers = (b"meshy.ai", b"tripo3d.ai", b"hyper3d.ai", b"hyper3d.com")
        if any(marker in first.lower() for marker in markers):
            fail(f"{provider} {label} contains provider address")
        if zipfile.is_zipfile(io.BytesIO(first)):
            with zipfile.ZipFile(io.BytesIO(first)) as archive:
                for item in archive.namelist():
                    with archive.open(item) as member:
                        previous = b""
                        while chunk := member.read(65536):
                            window = (previous + chunk).lower()
                            if any(marker in window for marker in markers):
                                fail(f"{provider} {label} contains provider address")
                            previous = window[-256:]
        if provider == "hyper3d" and asset.get("required") and asset.get("role") == "model":
            check_textured_glb(provider, first)
        print(f"{provider} {label}: READY {len(first)} bytes sha256={digest[:12]} magic={first[:8].hex()}", flush=True)


def main():
    if os.environ.get("ART_AI_LIVE_3D_ACK") != "1":
        fail("set ART_AI_LIVE_3D_ACK=1 to run approved paid test operations")
    username = os.environ.get("ADMIN_USERNAME")
    password = os.environ.get("ADMIN_PASSWORD")
    if not username or not password:
        fail("test environment admin credentials are unavailable")
    providers = [item.strip() for item in os.environ.get("ART_AI_3D_PROVIDERS", "meshy,tripo").split(",") if item.strip()]
    if not providers or any(item not in FORMAT_SETS for item in providers):
        fail("ART_AI_3D_PROVIDERS must list meshy, tripo and/or hyper3d")
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    request(opener, "POST", "/api/auth/login", {"username": username, "password": password})
    existing_session = os.environ.get("ART_AI_3D_SESSION_ID", "")
    for index, provider in enumerate(providers):
        task_id, session_id = submit(opener, provider, existing_session if index == 0 else None)
        print(f"{provider} session {session_id} accepted task {task_id}", flush=True)
        task = await_task(opener, task_id, int(os.environ.get("ART_AI_3D_TIMEOUT", PROVIDER_TIMEOUT.get(provider, TIMEOUT))))
        check_assets(opener, provider, task)
    print("PASS: 3D server cache and public address isolation", flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, KeyError, ValueError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
