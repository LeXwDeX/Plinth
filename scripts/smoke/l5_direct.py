"""Shared helpers of the direct L5 scripts (#540): smoke-l5-2d-direct.py, smoke-l5-matting.py, matting-probe.py.

These scripts do not drive the Agent. They send one native request per task (prepare, grant, submit, each
at most once), observe the Task, and check what the public API delivers. They default to the fake upstream
shape and cost nothing there; against a real or mixed target they need both live switches
(upstream_shape.py "fake-default": --live and ART_AI_LIVE_ACK=1). Everything that decides something is a
plain function so scripts/tests/test_l5_direct.py covers it without a server; the HTTP side is thin.

Rules every direct script keeps:
  * a submission is sent once. A refused step is recorded, an unknown outcome (a connection error or a 5xx
    on the submit) is only observed through the session's task list, and nothing is replayed;
  * raw answers (task JSON, delivered bytes, service documents) go to a private 0700 directory outside the
    working tree and are removed when the run ends (--keep-private keeps them for the one who asked); the
    report carries a scrubbed summary only: no prompts, no signed-URL queries, no addresses, no keys;
  * keys come from the environment only and are never printed.
"""
import hashlib
import json
import os
import shutil
import stat
import struct
import sys
import tempfile
import time
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
CONTENT_PROBE_ACK = "ART_AI_CONTENT_PROBE_ACK"
ACCEPTED_STATUSES = (200, 201, 202)
MAX_PROBE_PROMPT_BYTES = 4000
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"

# A 256x256 baseline JPEG (a blue disc on a pale gradient, quality 40, no metadata beyond the JFIF header),
# generated once so the scripts need no imaging library. test_l5_direct.py decodes its size.
JPEG_256_B64 = (
    "/9j/4AAQSkZJRgABAQAASABIAAD/4QBMRXhpZgAATU0AKgAAAAgAAYdpAAQAAAABAAAAGgAAAAAAA6ABAAMAAAABAAEAAKACAAQA"
    "AAABAAABAKADAAQAAAABAAABAAAAAAD/7QA4UGhvdG9zaG9wIDMuMAA4QklNBAQAAAAAAAA4QklNBCUAAAAAABDUHYzZjwCyBOmA"
    "CZjs+EJ+/8AAEQgBAAEAAwEiAAIRAQMRAf/EAB8AAAEFAQEBAQEBAAAAAAAAAAABAgMEBQYHCAkKC//EALUQAAIBAwMCBAMFBQQE"
    "AAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldY"
    "WVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk"
    "5ebn6Onq8fLz9PX29/j5+v/EAB8BAAMBAQEBAQEBAQEAAAAAAAABAgMEBQYHCAkKC//EALURAAIBAgQEAwQHBQQEAAECdwABAgMR"
    "BAUhMQYSQVEHYXETIjKBCBRCkaGxwQkjM1LwFWJy0QoWJDThJfEXGBkaJicoKSo1Njc4OTpDREVGR0hJSlNUVVZXWFlaY2RlZmdo"
    "aWpzdHV2d3h5eoKDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uLj5OXm5+jp6vLz"
    "9PX29/j5+v/bAEMACQkJCQkJEAkJEBYQEBAWHhYWFhYeJh4eHh4eJi4mJiYmJiYuLi4uLi4uLjc3Nzc3N0BAQEBASEhISEhISEhI"
    "SP/bAEMBCwwMEhESHxERH0szKjNLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLS//dAAQA"
    "EP/aAAwDAQACEQMRAD8A9hpKKKgkKSiimMKSiigApKKKYwpKKKBhSUUlMAooooGFJRRTGFJRSUALSUUlMYUUUlAwoopKYBRRSUDC"
    "iikpgFFFJQMKKKSgD//Q9gpKKKkgKSiigYUlFFMApKKSgYUUUlMAooooGFJRRTGFJRSUAFFFJTGFFFJQAUUUlMYUUUlAwoopKYBR"
    "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"
)


# ------------------------------------------------------------------ pictures

def png_chunk(kind, data):
    body = kind + data
    return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)


def encode_png(width, height, pixel, alpha):
    """A non-interlaced 8-bit PNG built with the standard library: RGBA when alpha, else RGB.

    pixel(x, y) returns an (r, g, b) tuple, or (r, g, b, a) when alpha is set."""
    channels = 4 if alpha else 3
    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            row.extend(pixel(x, y)[:channels])
        rows.append(b"\x00" + bytes(row))
    header = struct.pack(">IIBBBBB", width, height, 8, 6 if alpha else 2, 0, 0, 0)
    return (PNG_SIGNATURE + png_chunk(b"IHDR", header) + png_chunk(b"IDAT", zlib.compress(b"".join(rows)))
            + png_chunk(b"IEND", b""))


def subject_png(size, tint=(200, 60, 60)):
    """An opaque picture of a plain subject (a disc and a square) on a pale background: a harmless input
    that carries no scene, no person and no text."""
    centre = size // 2
    radius = size // 4

    def pixel(x, y):
        if (x - centre) ** 2 + (y - centre) ** 2 <= radius ** 2:
            return tint
        if size // 16 <= x < size // 16 * 5 and size // 16 <= y < size // 16 * 5:
            return (60, 90, 200)
        return (236 - y * 40 // size, 238 - x * 30 // size, 232)

    return encode_png(size, size, pixel, False)


def translucent_png(size, tint=(40, 150, 90)):
    """A picture with a transparent corner and a half-transparent disc, so the alpha flattening of the
    reference-image parameters has something to flatten."""
    centre = size // 2
    radius = size // 3

    def pixel(x, y):
        if x < size // 4 and y < size // 4:
            return (0, 0, 0, 0)
        if (x - centre) ** 2 + (y - centre) ** 2 <= radius ** 2:
            return tint + (128,)
        return (250, 240, 200, 255)

    return encode_png(size, size, pixel, True)


def mask_png(size):
    """An edit mask: opaque everywhere except a transparent central square (the editable region)."""
    low, high = size * 3 // 8, size * 5 // 8

    def pixel(x, y):
        return (0, 0, 0, 0) if low <= x < high and low <= y < high else (0, 0, 0, 255)

    return encode_png(size, size, pixel, True)


def jpeg_256():
    import base64
    return base64.b64decode(JPEG_256_B64)


def not_a_picture():
    """Bytes that are not a picture, written to be uploaded under a .png name. Plain text with no
    meaning, so no content policy is involved."""
    return b"this file is text, not a picture\n" * 16


def describe_image(data):
    """What a delivered picture is: {format, width, height, color_type, alpha}; format is png, jpeg,
    webp or None. color_type is the PNG header's value (6 = RGBA, 2 = RGB), None for other formats."""
    info = {"format": None, "width": None, "height": None, "color_type": None, "alpha": None}
    if data[:8] == PNG_SIGNATURE and len(data) >= 26:
        info["format"] = "png"
        info["color_type"] = data[25]
        info["alpha"] = data[25] in (4, 6)
    elif data[:2] == b"\xff\xd8":
        info["format"] = "jpeg"
        info["alpha"] = False
    elif data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        info["format"] = "webp"
    dimensions = common.image_dimensions(data)
    if dimensions:
        info["width"], info["height"] = dimensions
    return info


# ------------------------------------------------------------- private directory

def private_dir_problem(path, repo_root=REPO):
    """Why `path` cannot hold raw results, or None: an existing directory (not a link) owned by the caller
    with mode 0700 that is neither the working tree nor inside it, so nothing can be committed by accident."""
    try:
        info = os.lstat(path)
    except OSError:
        return "the private directory does not exist"
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        return "the private directory must be a real directory, not a link"
    if stat.S_IMODE(info.st_mode) != 0o700:
        return "the private directory must have mode 0700"
    if hasattr(os, "getuid") and info.st_uid != os.getuid():
        return "the private directory must belong to the caller"
    resolved = os.path.realpath(path)
    root = os.path.realpath(repo_root)
    if resolved == root or resolved.startswith(root + os.sep):
        return "the private directory must be outside the working tree"
    return None


class PrivateDir:
    """The directory raw results go to: the given one, or a fresh temporary 0700 directory. close() removes
    what the run wrote (the whole directory when it created it) unless keep is set."""

    def __init__(self, path=None, keep=False, repo_root=REPO):
        self.created = path is None
        self.keep = keep
        self.written = []
        if path is None:
            path = tempfile.mkdtemp(prefix="l5-direct-")
            os.chmod(path, 0o700)
        problem = private_dir_problem(path, repo_root)
        if problem:
            if self.created:
                shutil.rmtree(path, ignore_errors=True)
            raise ValueError(problem)
        self.path = path

    def write(self, name, data):
        """Write bytes (or text) to a new 0600 file; returns the file name. A name never leaves the directory."""
        name = os.path.basename(name)
        raw = data if isinstance(data, bytes) else data.encode("utf-8")
        descriptor = os.open(os.path.join(self.path, name), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(raw)
        if name not in self.written:
            self.written.append(name)
        return name

    def close(self):
        if self.keep:
            print(f"raw results kept in the private directory (--keep-private); remove it when done: {self.path}", flush=True)
            return
        if self.created:
            shutil.rmtree(self.path, ignore_errors=True)
            return
        for name in self.written:
            try:
                os.unlink(os.path.join(self.path, name))
            except OSError:
                pass


# ------------------------------------------------------------- shape

def generation_is_fake(shape):
    """Whether the generation side of the target is the fake: a fake target, and the mixed one (#416) whose
    generation Providers and matting service are fake while only its chat connections are real. These
    scripts never reach a chat model, so on both of them the judgement is the lenient one."""
    return shape in ("fake", "mixed")


# ------------------------------------------------------------- content probe gate

def content_probe_refusal(arguments, shape, environ, providers):
    """Why a content probe (or the options that only belong to one) must not run, or None.

    A content probe sends a prompt the user supplied, meant to be refused by the provider's safety layer.
    That can touch the account's risk controls, so it never runs by default and needs, besides the two live
    switches the shape gate already checked: --content-probe, one --provider, --probe-prompt-file, and a
    third switch ART_AI_CONTENT_PROBE_ACK=1. It is meaningless on the fake, which never blocks content."""
    if not arguments.content_probe:
        if arguments.provider or arguments.probe_prompt_file:
            return "--provider and --probe-prompt-file only apply to --content-probe"
        return None
    if generation_is_fake(shape):
        return "refusing to run: a content probe means something only against a real upstream (the fake never blocks content)"
    if environ.get(CONTENT_PROBE_ACK) != "1":
        return (f"refusing to run: a content probe sends a prompt meant to be refused and can touch the account's "
                f"risk controls; set {CONTENT_PROBE_ACK}=1 after the user agreed to this exact prompt")
    if arguments.provider not in providers:
        return "--content-probe needs --provider " + "|".join(providers)
    if not arguments.probe_prompt_file or not os.path.isfile(arguments.probe_prompt_file):
        return "--content-probe needs --probe-prompt-file pointing at a readable file"
    if arguments.large or arguments.only:
        return "--content-probe runs alone: do not combine it with --large or --only"
    return None


def load_probe_prompt(path):
    """The prompt of a content probe, read from the user's file: (text, sha256 hex, length). The text is
    only ever sent; it is neither printed nor stored. Raises ValueError for an empty or oversized file."""
    with open(path, "rb") as handle:
        raw = handle.read(MAX_PROBE_PROMPT_BYTES + 1)
    if len(raw) > MAX_PROBE_PROMPT_BYTES:
        raise ValueError(f"the probe prompt file is larger than {MAX_PROBE_PROMPT_BYTES} bytes")
    text = raw.decode("utf-8", errors="strict").strip()
    if not text:
        raise ValueError("the probe prompt file is empty")
    return text, hashlib.sha256(text.encode("utf-8")).hexdigest(), len(text)


# ------------------------------------------------------------- findings

class Findings:
    """Checks that are strict on a real target and only recorded on the fake.

    The fake does not model every behaviour of the services yet (it renders small fixed pictures, answers
    matting with an opaque white square, knows few parameter bounds): on a fake target such a deviation is
    an INFO line and an `info` entry of the evidence, a work item for the fake, not a pass; on a real target
    the same deviation fails the check. `strict` is therefore False on a fake or mixed shape
    (generation_is_fake)."""

    def __init__(self, report, strict):
        self.report = report
        self.strict = strict
        self.info = []

    def check(self, item, ok, detail=""):
        if ok:
            self.report.passed(item, detail)
            return True
        if self.strict:
            self.report.failed(item, detail)
            return False
        text = common.one_line(detail, 200)
        print(f"INFO (not modelled by the fake yet): {item}" + (f" - {text}" if text else ""), flush=True)
        self.info.append({"item": item, "detail": text})
        return False

    def note(self, item, detail=""):
        """A recorded observation that is never a pass or a fail (a deviation the contract allows)."""
        text = common.one_line(detail, 200)
        print(f"INFO: {item}" + (f" - {text}" if text else ""), flush=True)
        self.info.append({"item": item, "detail": text})


# ------------------------------------------------------------- direct submission

def submit_outcome(status):
    """What a submit answer means: accepted, refused (a definite 4xx: nothing was started) or unknown (no
    answer or a 5xx: a Task may exist). Only an unknown outcome is observed, and nothing is replayed."""
    if status in ACCEPTED_STATUSES:
        return "accepted"
    if status == 0 or status >= 500:
        return "unknown"
    return "refused"


class Submission:
    """The result of one direct submission."""

    def __init__(self):
        self.outcome = None  # accepted, refused or unknown
        self.task_id = None
        self.step = None  # the step that stopped it (prepare, grant, approve, submit) or None
        self.status = None
        self.detail = ""
        self.quote = None
        self.adjustments = None
        self.request_id = None

    def stop(self, step, status, payload, outcome="refused"):
        self.step, self.status, self.outcome = step, status, outcome
        self.detail = common.one_line(json.dumps(payload, ensure_ascii=False), 200) if payload else ""
        return self


def direct_submit(live, session_id, offering_id, capability, payload, reasons=None):
    """prepare -> grant (-> approve when the grant is pending) -> submit, each exactly once.

    The grant is decided with the Schema's own decision member, as the browser does; a session in the auto
    approval mode approves it by policy. Returns a Submission."""
    result = Submission()
    client = live.client
    body = {"session_id": session_id, "capability": capability, "offering_id": offering_id, "input": payload}
    if reasons:
        body["parameter_reasons"] = reasons
    status, prepared = client.call("POST", "/api/v1/tasks/prepare", body)
    prepared_id = prepared.get("prepared_input_id") if isinstance(prepared, dict) else None
    if status not in (200, 201) or not prepared_id:
        return result.stop("prepare", status, prepared)
    result.quote = prepared.get("native_quote")
    result.adjustments = prepared.get("parameter_adjustments")
    key = {"Idempotency-Key": "l5-direct-" + str(prepared_id)}
    status, grant = client.call("POST", "/api/v1/tasks/grants", {"prepared_input_id": prepared_id}, headers=key)
    request_id = grant.get("request_id") if isinstance(grant, dict) else None
    if status not in (200, 201) or not request_id:
        return result.stop("grant", status, grant)
    result.request_id = request_id
    if grant.get("status") == "pending":
        status, decided = live.decide_grant(request_id, True)
        if status != 200:
            return result.stop("approve", status, decided)
    status, accepted = client.call("POST", "/api/v1/tasks",
                                   {"prepared_input_id": prepared_id, "authorization_request_id": request_id},
                                   headers=key)
    outcome = submit_outcome(status)
    if outcome != "accepted":
        return result.stop("submit", status, accepted, outcome)
    task_id = (accepted.get("task_id") or accepted.get("id")) if isinstance(accepted, dict) else None
    if not task_id:
        return result.stop("submit", status, accepted, "unknown")
    result.outcome, result.task_id, result.status = "accepted", task_id, status
    return result


def observe_new_tasks(live, session_id, before, wait=10, sleep=time.sleep):
    """After an unknown submit outcome: list the session's tasks once, after a short wait, and return the
    ones that were not there before. Never submits again."""
    sleep(wait)
    status, data = live.client.call("GET", f"/api/v1/tasks?session_id={session_id}&limit=50")
    tasks = (data.get("tasks") or data.get("data") or []) if status == 200 and isinstance(data, dict) else []
    return [task for task in tasks if task.get("id") not in before]


def await_task(live, task_id, timeout, interval=5, now=time.time, sleep=time.sleep):
    """Poll one Task until it is terminal or the timeout passes. Returns (task payload, states, timed_out);
    states is the list of [state, seconds since the first poll] on every change."""
    started = now()
    deadline = started + timeout
    states, task = [], {}
    while True:
        status, payload = live.client.call("GET", f"/api/v1/tasks/{task_id}")
        if status == 200 and isinstance(payload, dict):
            task = payload
            state = task.get("state")
            if not states or states[-1][0] != state:
                states.append([state, round(now() - started, 1)])
            if state in common.TERMINAL_TASK_STATES:
                return task, states, False
        if now() >= deadline:
            return task, states, True
        sleep(interval)


def required_images(task):
    """The delivered picture assets of a Task payload (role, media type and size), in delivery order."""
    return [asset for asset in (task or {}).get("assets") or []
            if str(asset.get("media_type", "")).startswith("image/") or asset.get("role") == "image"]


def fetch_images(live, task, private=None, label="task", limit=96 << 20):
    """Download every delivered picture of a Task through its public asset URL and describe it. Bytes are
    written to the private directory, never kept in the result. Each entry: {index, role, variant, status,
    bytes, sha256, declared_size, declared_sha256, matches_declared, media_type, format, width, height,
    color_type, alpha}."""
    found = []
    for index, asset in enumerate(required_images(task), 1):
        status, raw = live.client.fetch(asset["url"], limit=limit) if asset.get("url") else (0, b"")
        digest = hashlib.sha256(raw).hexdigest() if raw else None
        entry = {
            "index": index, "role": asset.get("role"), "variant": asset.get("variant"), "status": status,
            "bytes": len(raw), "sha256": digest, "declared_size": asset.get("size"),
            "declared_sha256": asset.get("sha256"), "media_type": asset.get("media_type"),
            "matches_declared": bool(raw) and len(raw) == asset.get("size")
            and (not asset.get("sha256") or digest == asset.get("sha256")),
        }
        entry.update(describe_image(raw))
        if private is not None and raw:
            extension = {"png": "png", "jpeg": "jpg", "webp": "webp"}.get(entry["format"], "bin")
            private.write(f"{label}-{index}.{extension}", raw)
        found.append(entry)
    return found


# ------------------------------------------------------------- evidence

# Keys of a public Task payload kept in the evidence, and the keys of its frozen parameters that are dropped
# (a prompt is free text the user or the script wrote; the evidence never carries one).
TASK_KEYS = ("id", "state", "capability", "offering_id", "model_id", "created_at", "updated_at", "native_quote",
             "notices", "failure", "resources_pending")
DROPPED_PARAMETERS = ("prompt", "negative_prompt")


def scrub_value(value, depth=0):
    """A copy of a JSON value with every string cut short and every URL query removed."""
    if isinstance(value, dict):
        return {str(key): scrub_value(item, depth + 1) for key, item in list(value.items())[:60]}
    if isinstance(value, list):
        return [scrub_value(item, depth + 1) for item in value[:40]]
    if isinstance(value, str):
        return common.redact_url(value)[:160]
    return value


def task_summary(task):
    """The scrubbed part of a public Task payload that goes into the evidence: state, offering, quote,
    notices, failure, the frozen parameters without free text, and the assets without their URLs."""
    task = task or {}
    summary = {key: scrub_value(task[key]) for key in TASK_KEYS if key in task}
    parameters = common.frozen_parameters(task)
    summary["parameters"] = {key: scrub_value(value) for key, value in parameters.items()
                             if key not in DROPPED_PARAMETERS}
    summary["assets"] = [{key: scrub_value(asset.get(key)) for key in ("role", "variant", "media_type", "size",
                                                                      "sha256", "status", "required")
                          if key in asset} for asset in task.get("assets") or []]
    return summary


def write_evidence(directory, name, data):
    """Write the scrubbed evidence JSON next to the report; no-op without a report directory."""
    if not directory:
        return None
    path = os.path.join(directory, f"{name}-evidence.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(data, handle, ensure_ascii=False, indent=2, sort_keys=True)
    return path


def short_digest(digest):
    return (digest or "")[:12]


# ------------------------------------------------------------- multipart (direct service probes)

def multipart_body(fields, files, boundary="l5probeboundary40e1"):
    """(content type, body) of a multipart/form-data request. fields is a list of (name, text); files a
    list of (field, filename, media type, bytes)."""
    parts = []
    for name, value in fields:
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n'.encode()
                     + value.encode("utf-8") + b"\r\n")
    for field, filename, media_type, data in files:
        parts.append((f'--{boundary}\r\nContent-Disposition: form-data; name="{field}"; filename="{filename}"\r\n'
                      f"Content-Type: {media_type}\r\n\r\n").encode() + data + b"\r\n")
    parts.append(f"--{boundary}--\r\n".encode())
    return f"multipart/form-data; boundary={boundary}", b"".join(parts)

