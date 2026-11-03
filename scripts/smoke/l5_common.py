"""Shared helpers for the L5 live-capability smoke scripts (smoke-l5-*.py).

Everything that decides something (waiting rules, tool-call parsing, argument
validation, redaction) is a plain function so scripts/tests/test_l5_common.py
can cover it without a server. The HTTP client and the report writer are thin.

The scripts spend real money, so main() refuses to run without ART_AI_LIVE_ACK=1.
They never print credentials, cookies, signed-URL queries or full prompts.
"""

import argparse
import http.cookiejar
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ACK_VARIABLE = "ART_AI_LIVE_ACK"
TERMINAL_TASK_STATES = ("SUCCEEDED", "FAILED", "CANCELED", "CANCELLED")
GENERATING_PLACEHOLDER = "__GENERATING__"
DEFAULT_SESSION_TITLE = "新对话"
SUCCESS_STATE = "SUCCEEDED"


# ---------------------------------------------------------------- pure logic

def ack_error(environ):
    """Return the refusal message when the paid-run acknowledgement is absent."""
    if environ.get(ACK_VARIABLE) == "1":
        return None
    return (f"refusing to run: this script sends real, paid model requests; "
            f"set {ACK_VARIABLE}=1 to acknowledge the cost")


def missing_environment(environ, names=("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD")):
    return [name for name in names if not environ.get(name)]


def validate_base(base):
    parsed = urllib.parse.urlparse(base or "")
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.path not in ("", "/"):
        return "BASE must be an http(s) origin without a path"
    if parsed.query or parsed.fragment or parsed.username or parsed.password:
        return "BASE must not carry credentials, a query or a fragment"
    return None


def parse_arguments(argv, description=""):
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("--report", help="directory for the report and downloaded artifacts")
    parser.add_argument("--turn-timeout", type=int, default=1800, help="seconds to wait for one chat turn")
    parser.add_argument("--task-timeout", type=int, default=2400, help="seconds to wait for expected tasks")
    arguments = parser.parse_args(argv)
    for name in ("turn_timeout", "task_timeout"):
        if getattr(arguments, name) < 30:
            parser.error(f"--{name.replace('_', '-')} must be at least 30 seconds")
    return arguments


def redact_url(text):
    """Drop query strings from any URL inside text so signed parameters never print."""
    return re.sub(r"(https?://[^\s?\"']+|/api/[^\s?\"']+)\?[^\s\"']*", r"\1?<redacted>", text or "")


def one_line(text, limit=80):
    return redact_url(" ".join((text or "").split()))[:limit]


def parse_tool_calls(message):
    """Tool calls of one chat message. The API serialises them as a JSON string."""
    raw = (message or {}).get("toolCalls")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else []
        except ValueError:
            return []
    if not isinstance(raw, list):
        return []
    return [call for call in raw if isinstance(call, dict)]


def tool_names(messages):
    return [call.get("name") for message in messages for call in parse_tool_calls(message) if call.get("name")]


def tool_errors(messages):
    """Names of tool calls that carry an error. The messages wire has no per-call result; the extra
    fields are checked only in case a later wire adds them. A failed turn is detected by reply_failed."""
    failed = []
    for message in messages:
        for call in parse_tool_calls(message):
            if call.get("error"):
                failed.append(call.get("name"))
    return failed


def tool_arguments(call):
    """Arguments of one tool call; the API serialises them as a JSON string."""
    raw = (call or {}).get("arguments")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else {}
        except ValueError:
            return {}
    return raw if isinstance(raw, dict) else {}


def is_multiview_task(task, model_id):
    """The task ran on the multi-view Offering: its offering id names multiview and its model is the chosen one."""
    return "multiview" in str(task.get("offering_id", "")) and task.get("model_id") == model_id


def image_handles(messages):
    """Handles of the image attachments over all messages, in order of first appearance."""
    found = []
    for message in messages:
        for attachment in message.get("attachments") or []:
            if isinstance(attachment, dict) and attachment.get("kind") == "image":
                handle = attachment.get("handle")
                if handle and handle not in found:
                    found.append(handle)
    return found


def reference_targets(call):
    """References one image-tool call passes: handles as given, and 1-based indices as "#<n>"."""
    arguments = tool_arguments(call)
    handles = [str(item) for item in arguments.get("reference_handles") or []]
    indices = [f"#{item}" for item in arguments.get("reference_image_indices") or []]
    return handles + indices


def calls_named(messages, *names):
    return [call for message in messages for call in parse_tool_calls(message) if call.get("name") in names]


def image_dimensions(data):
    """(width, height) of PNG, JPEG or WebP bytes, or None."""
    if len(data) >= 24 and data[:8] == b"\x89PNG\r\n\x1a\n":
        return int.from_bytes(data[16:20], "big"), int.from_bytes(data[20:24], "big")
    if data[:2] == b"\xff\xd8":
        index = 2
        while index + 9 < len(data):
            if data[index] != 0xFF:
                index += 1
                continue
            marker = data[index + 1]
            if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
                index += 2
                continue
            length = int.from_bytes(data[index + 2:index + 4], "big")
            if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
                return int.from_bytes(data[index + 7:index + 9], "big"), int.from_bytes(data[index + 5:index + 7], "big")
            index += 2 + length
        return None
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP" and len(data) >= 30:
        kind = data[12:16]
        if kind == b"VP8X":
            return 1 + int.from_bytes(data[24:27], "little"), 1 + int.from_bytes(data[27:30], "little")
        if kind == b"VP8L":
            bits = int.from_bytes(data[21:25], "little")
            return 1 + (bits & 0x3FFF), 1 + ((bits >> 14) & 0x3FFF)
        if kind == b"VP8 ":
            return int.from_bytes(data[26:28], "little") & 0x3FFF, int.from_bytes(data[28:30], "little") & 0x3FFF
    return None


def option_ratio(value):
    """Width/height of a size option such as '1536x1024' or '16:9'; None when it is not a ratio."""
    match = re.fullmatch(r"\s*(\d+)\s*[x:X*]\s*(\d+)\s*", str(value))
    if not match or int(match.group(2)) == 0:
        return None
    return int(match.group(1)) / int(match.group(2))


def ratio_matches(dimensions, expected, tolerance=0.03):
    if not dimensions or not expected or dimensions[1] == 0:
        return False
    return abs(dimensions[0] / dimensions[1] - expected) / expected <= tolerance


def option_area(value):
    match = re.fullmatch(r"\s*(\d+)\s*[x*X]\s*(\d+)\s*", str(value))
    return int(match.group(1)) * int(match.group(2)) if match else 0


def size_options(offering):
    """(parameter name, [option values]) of the output-size parameter an Offering declares, or None."""
    for parameter in offering.get("parameters") or []:
        if parameter.get("semantic") == "output-size" and parameter.get("options"):
            return parameter["name"], [str(item["value"]) for item in parameter["options"]]
    return None


def pick_shapes(options, default=None):
    """Cheapest option per shape: square, portrait, landscape, plus one non-default option of another ratio."""
    chosen = {}
    for value in sorted(options, key=lambda v: (option_area(v), options.index(v))):
        ratio = option_ratio(value)
        if ratio is None:
            continue
        shape = "square" if abs(ratio - 1) < 0.02 else ("portrait" if ratio < 1 else "landscape")
        chosen.setdefault(shape, value)
    taken = set(chosen.values())
    for value in sorted(options, key=lambda v: (option_area(v), options.index(v))):
        ratio = option_ratio(value)
        if value not in taken and value != default and ratio is not None and all(
                abs(ratio - option_ratio(other)) > 0.02 for other in taken):
            chosen["other"] = value
            break
    return chosen


def solid_png(width, height, rgb):
    """A valid solid-colour PNG built with the standard library (stand-in for a canvas snapshot)."""
    import struct
    import zlib

    def chunk(kind, payload):
        body = kind + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    row = b"\x00" + bytes(rgb) * width
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(row * height)) + chunk(b"IEND", b""))


def canvas_attachment_ids(messages):
    """Handles of the canvas-role attachments of the user messages, oldest first."""
    found = []
    for message in messages:
        if message.get("role") != "user":
            continue
        for attachment in message.get("attachments") or []:
            if isinstance(attachment, dict) and (attachment.get("role") == "canvas" or attachment.get("canvas")):
                found.append(attachment.get("handle") or attachment.get("assetId") or attachment.get("asset_id") or "")
    return found


def last_assistant(messages):
    for message in reversed(messages or []):
        if message.get("role") == "assistant":
            return message
    return None


# Orchestrator.fail writes the failure text into the assistant content and appends the Schema's
# error_reference ("（错误 ID：{id}）", English " (Error ID: {id})"). The other failure copies are the Schema's
# agent_not_configured / agent_content_rejected / agent_images_unsupported / no_result texts.
ERROR_REFERENCE = re.compile(r"[（(]\s*(?:错误 ID|Error ID)\s*[:：]")
FAILURE_PREFIXES = ("Agent 未配置", "请求被上游模型服务的内容安全审核拦截", "当前 Agent 模型拒绝了带图片的请求",
                    "⚠️ 未返回结果", "未知错误")


def reply_failed(reply):
    """The assistant message content is a failure text written by Orchestrator.fail."""
    content = ((reply or {}).get("content") or "").strip()
    if not content:
        return False
    return bool(ERROR_REFERENCE.search(content)) or content.startswith(FAILURE_PREFIXES)


def reply_is_final(session, messages):
    """The turn is over: not pending, and the last assistant message is real content (a failure text counts)."""
    if not session or session.get("isPending"):
        return False
    reply = last_assistant(messages)
    if not reply:
        return False
    content = (reply.get("content") or "").strip()
    if content == GENERATING_PLACEHOLDER:
        return False
    return bool(content)


def reply_is_clean(reply):
    """Final content that is not a failure text."""
    if not reply:
        return False
    content = (reply.get("content") or "").strip()
    return bool(content) and content != GENERATING_PLACEHOLDER and not reply_failed(reply) and not reply.get("error")


def tasks_by_capability(tasks):
    counts = {}
    for task in tasks:
        counts[task.get("capability")] = counts.get(task.get("capability"), 0) + 1
    return counts


def tasks_settled(tasks, expected):
    """Decide whether the expected tasks exist and every task is terminal.

    expected maps capability -> minimum number of tasks. Never settled while an
    expected task has not been created yet, even if all existing tasks are terminal.
    Returns (settled, reason).
    """
    counts = tasks_by_capability(tasks)
    for capability, minimum in expected.items():
        if counts.get(capability, 0) < minimum:
            return False, f"waiting for {capability} task ({counts.get(capability, 0)}/{minimum})"
    running = [task for task in tasks if task.get("state") not in TERMINAL_TASK_STATES]
    if running:
        return False, f"{len(running)} task(s) still running"
    return True, "all expected tasks terminal"


def succeeded_count(tasks, capability):
    return sum(1 for task in tasks if task.get("capability") == capability and task.get("state") == SUCCESS_STATE)


def describe_tasks(tasks):
    described = []
    for task in tasks:
        state = task.get("state")
        failure = (task.get("failure") or {}).get("class")
        described.append((task.get("capability"), task.get("model_id"), f"{state}/{failure}" if failure else state))
    return described


def title_generated(title):
    return bool(title) and title != DEFAULT_SESSION_TITLE


def required_assets_ready(task):
    """Return (ok, reason) for the required artifacts of a terminal task payload."""
    if task.get("state") != SUCCESS_STATE:
        return False, f"state {task.get('state')}"
    if task.get("resources_pending"):
        return False, "resources still pending"
    required = [asset for asset in task.get("assets") or [] if asset.get("required")]
    if not required:
        return False, "no required artifact declared"
    for asset in required:
        if not asset.get("url") or not isinstance(asset.get("size"), int) or asset["size"] <= 0:
            return False, f"required artifact {asset.get('role')} has no bytes"
        status = asset.get("status")
        if status is not None and str(status).upper() != "READY":
            return False, f"required artifact {asset.get('role')} is {status}"
    return True, f"{len(required)} required artifact(s) ready"


def pick_model_id(offerings, preferred=()):
    """Model ID (not Offering ID) of the first available offering, preferring the given IDs/substrings."""
    available = [item for item in offerings if item.get("available") and item.get("model_id")]
    for wanted in preferred:
        for item in available:
            if wanted in (item["model_id"], item.get("id")) or wanted in item["id"]:
                return item["model_id"]
    return available[0]["model_id"] if available else None


def offering_model_ids(offerings):
    return {item["id"]: item["model_id"] for item in offerings if item.get("available") and item.get("model_id")}


def multiview_request(group, model, slots):
    """Structured multi-view request from succeeded front/left/back(/right) slots of a three-view group."""
    views = [{"view": slot["key"], "task_id": slot["task_id"], "asset_id": slot["output_asset_id"]}
             for slot in slots
             if slot.get("status") == "succeeded" and slot.get("key") in ("front", "left", "back", "right")
             and slot.get("task_id") and slot.get("output_asset_id")]
    return {"group": group, "model": model, "views": views}


def slot_states(group):
    return {slot["key"]: slot["status"] for slot in (group or {}).get("slots", [])}


def views_settled(group, keys=("front", "left", "back")):
    states = slot_states(group)
    return all(states.get(key) in ("succeeded", "failed", "canceled", "denied") for key in keys)


def safe_name(text):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", text or "x").strip("-")[:60] or "x"


# --------------------------------------------------------------- report

class Report:
    def __init__(self, name, directory=None):
        self.name = name
        self.directory = directory
        self.items = []
        if directory:
            os.makedirs(directory, exist_ok=True)

    def record(self, status, item, detail=""):
        detail = one_line(detail, 200)
        self.items.append({"status": status, "item": item, "detail": detail})
        print(f"{status}: {item}" + (f" - {detail}" if detail else ""), flush=True)

    def passed(self, item, detail=""):
        self.record("PASS", item, detail)

    def failed(self, item, detail=""):
        self.record("FAIL", item, detail)

    def skipped(self, item, detail=""):
        self.record("SKIP", item, detail)

    def check(self, item, ok, detail=""):
        self.record("PASS" if ok else "FAIL", item, detail)
        return ok

    def counts(self):
        return {key: sum(1 for entry in self.items if entry["status"] == key) for key in ("PASS", "FAIL", "SKIP")}

    def summary_line(self):
        counts = self.counts()
        return f"=== {self.name}: PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']} ==="

    def exit_code(self):
        return 1 if self.counts()["FAIL"] else 0

    def finish(self):
        print(self.summary_line(), flush=True)
        if self.directory:
            with open(os.path.join(self.directory, f"{self.name}.json"), "w", encoding="utf-8") as handle:
                json.dump({"script": self.name, "counts": self.counts(), "items": self.items}, handle,
                          ensure_ascii=False, indent=2)
        return self.exit_code()


# ---------------------------------------------------------------- client

class Client:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None, timeout=60, headers=None):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Origin", self.base)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        for key, value in (headers or {}).items():
            request.add_header(key, value)
        try:
            with self.opener.open(request, timeout=timeout) as response:
                payload = response.read()
                return response.status, (json.loads(payload) if payload else {})
        except urllib.error.HTTPError as error:
            payload = error.read()
            try:
                return error.code, json.loads(payload)
            except ValueError:
                return error.code, {}
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, {}

    def probe_bytes(self, path, count=16):
        """First bytes of a protected asset (status, bytes), without downloading the whole file."""
        request = urllib.request.Request(self.base + path, headers={"Range": f"bytes=0-{count - 1}"})
        try:
            with self.opener.open(request, timeout=120) as response:
                return response.status, response.read(count)
        except urllib.error.HTTPError as error:
            return error.code, b""
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, b""

    def upload_image(self, data, media_type="image/png", filename="snapshot.png"):
        """Upload picture bytes as an input asset (multipart, field `file`); returns the asset ID or None."""
        boundary = "l5boundary7f3a9c"
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
                f"Content-Type: {media_type}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(self.base + "/api/assets/upload", data=body, method="POST")
        request.add_header("Origin", self.base)
        request.add_header("Content-Type", "multipart/form-data; boundary=" + boundary)
        try:
            with self.opener.open(request, timeout=120) as response:
                return json.loads(response.read()).get("asset_id")
        except (urllib.error.URLError, TimeoutError, OSError, ValueError):
            return None

    def fetch(self, path, limit=40 << 20):
        request = urllib.request.Request(self.base + path)
        try:
            with self.opener.open(request, timeout=300) as response:
                return response.status, response.read(limit)
        except urllib.error.HTTPError as error:
            return error.code, b""
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, b""

    def download(self, path, destination):
        request = urllib.request.Request(self.base + path)
        try:
            with self.opener.open(request, timeout=300) as response, open(destination, "wb") as handle:
                size = 0
                while True:
                    chunk = response.read(1 << 20)
                    if not chunk:
                        return response.status, size
                    handle.write(chunk)
                    size += len(chunk)
        except urllib.error.HTTPError as error:
            return error.code, 0
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, 0

    def login(self, username, password):
        status, _ = self.call("POST", "/api/auth/login", {"username": username, "password": password})
        return status == 200


# ------------------------------------------------------------ live helpers

def require_ok(status, what):
    """Raise on a non-200 status so execute() records a FAIL instead of reading an error body as empty data."""
    if status != 200:
        raise RuntimeError(f"{what}: HTTP {status}")


class Live:
    """Session-level operations used by the scripts."""

    def __init__(self, client, report, arguments):
        self.client = client
        self.report = report
        self.arguments = arguments

    # sessions
    def new_session(self, title=DEFAULT_SESSION_TITLE, approval="auto"):
        status, data = self.client.call("POST", "/api/chat/sessions", {"title": title})
        session_id = data.get("id")
        if status not in (200, 201) or not session_id:
            return None
        if approval:
            self.client.call("PUT", f"/api/v1/chat/sessions/{session_id}/approval", {"approvalMode": approval})
        return session_id

    def send(self, session_id, content, domain, model, **extra):
        body = {"content": content, "creative_domain": domain, "ai_model": model}
        body.update(extra)
        # 409 means the session has not released the previous turn yet; the rejected message was not
        # accepted, so waiting and sending it again does not duplicate anything.
        for _ in range(12):
            status, data = self.client.call("POST", f"/api/chat/sessions/{session_id}/messages", body)
            if status != 409:
                break
            time.sleep(15)
        if status not in (200, 201, 202):
            print(f"message rejected: HTTP {status} {one_line(json.dumps(data, ensure_ascii=False), 160)}", flush=True)
        return status, data

    def session(self, session_id):
        status, data = self.client.call("GET", f"/api/chat/sessions/{session_id}")
        return data if status == 200 else None

    def messages(self, session_id):
        status, data = self.client.call("GET", f"/api/chat/sessions/{session_id}/messages?limit=100")
        require_ok(status, "list messages")
        return (data.get("messages") if isinstance(data, dict) else data) or []

    def tasks(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/tasks?session_id={session_id}&limit=50")
        require_ok(status, "list tasks")
        return data.get("tasks") or data.get("data") or []

    def task(self, task_id):
        status, data = self.client.call("GET", f"/api/v1/tasks/{task_id}")
        return data if status == 200 else {}

    def offerings(self, capability):
        status, data = self.client.call("GET", f"/api/v1/offerings?capability={capability}")
        require_ok(status, "list offerings")
        return data.get("offerings") or data.get("data") or (data if isinstance(data, list) else [])

    # waiting: the reply must be final first, then the expected tasks must exist and be terminal
    def wait_reply(self, session_id, timeout=None, poll=8):
        deadline = time.time() + (timeout or self.arguments.turn_timeout)
        time.sleep(poll)
        while time.time() < deadline:
            messages = self.messages(session_id)
            if reply_is_final(self.session(session_id), messages):
                return last_assistant(messages)
            time.sleep(poll)
        return None

    def wait_tasks(self, session_id, expected, timeout=None, poll=15):
        deadline = time.time() + (timeout or self.arguments.task_timeout)
        tasks = []
        while time.time() < deadline:
            tasks = self.tasks(session_id)
            settled, _ = tasks_settled(tasks, expected)
            if settled:
                return tasks, True
            time.sleep(poll)
        return tasks, False

    def run_turn(self, session_id, content, domain, model, expected, **extra):
        """Send a message, wait for the final reply, then for the expected tasks. Returns (reply, tasks, ok)."""
        status, data = self.send(session_id, content, domain, model, **extra)
        if status not in (200, 201, 202):
            self.report.failed("message accepted", f"HTTP {status}")
            return None, [], False
        reply = self.wait_reply(session_id)
        if reply is None:
            self.report.failed("final reply", "timed out waiting for the turn to finish")
            return None, self.tasks(session_id), False
        tasks, settled = self.wait_tasks(session_id, expected)
        return reply, tasks, settled

    def login(self):
        environ = os.environ
        if not self.client.login(environ["ADMIN_USERNAME"], environ["ADMIN_PASSWORD"]):
            self.report.failed("login")
            return False
        self.report.passed("login")
        return True

    def verify_task_artifacts(self, task_id, label):
        """Terminal task payload: SUCCEEDED, required artifacts present, bytes readable through the API."""
        payload = self.task(task_id)
        ok, reason = required_assets_ready(payload)
        if not ok:
            self.report.failed(f"{label} required artifacts READY", reason)
            return False
        for asset in payload["assets"]:
            if not asset.get("required"):
                continue
            status, head = self.client.probe_bytes(asset["url"])
            if status not in (200, 206) or not head:
                self.report.failed(f"{label} required artifact readable", f"{asset.get('role')} HTTP {status}")
                return False
        self.report.passed(f"{label} required artifacts READY", reason)
        return True

    def first_required_image(self, task_id):
        """(width, height) of the first required image artifact of a task, or None."""
        for asset in self.task(task_id).get("assets") or []:
            if asset.get("required") and str(asset.get("media_type", "")).startswith("image/"):
                status, data = self.client.fetch(asset["url"])
                return common_dimensions(data) if status == 200 else None
        return None

    def pending_approvals(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/chat/sessions/{session_id}/pending-approvals")
        require_ok(status, "list pending approvals")
        items = data.get("approvals") or data.get("items") or data.get("data") or (data if isinstance(data, list) else [])
        return [item for item in items if item.get("status") == "pending"]

    def save_asset(self, task_id, asset_id, destination):
        status, size = self.client.download(f"/api/v1/tasks/{task_id}/assets/{asset_id}", destination)
        return status == 200 and size > 0


def single_image_3d_model(live):
    """model_id for single-image 3D: L5_3D_MODEL when set (a model_id the Schema lists), else the first
    available Tripo single-image Offering, else the first available image-to-3d Offering."""
    offerings = live.offerings("image-to-3d")
    wanted = os.environ.get("L5_3D_MODEL")
    if wanted:
        return wanted if any(o.get("model_id") == wanted and o.get("available") for o in offerings) else None
    plain = [o for o in offerings if "multiview" not in o.get("id", "") and "segment" not in o.get("id", "")]
    return pick_model_id([o for o in plain if o["id"].startswith("tripo/")]) or pick_model_id(plain)


def multiview_3d_model(live):
    """model_id for multi-view 3D: L5_MULTIVIEW_MODEL when set, else the first available Tripo multiview Offering."""
    offerings = [o for o in live.offerings("image-to-3d") if "multiview" in o.get("id", "")]
    wanted = os.environ.get("L5_MULTIVIEW_MODEL")
    if wanted:
        return wanted if any(o.get("model_id") == wanted and o.get("available") for o in offerings) else None
    return pick_model_id([o for o in offerings if o["id"].startswith("tripo/")] or offerings)


def common_dimensions(data):
    return image_dimensions(data)


def execute(name, body, description=""):
    """Entry point shared by every script: ACK gate, environment, login, body(live, report), summary."""
    refusal = ack_error(os.environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    missing = missing_environment(os.environ)
    if missing:
        print("missing environment: " + ", ".join(missing) + " (run through scripts/release-env.py smoke)",
              file=sys.stderr)
        return 2
    problem = validate_base(os.environ["BASE"])
    if problem:
        print(problem, file=sys.stderr)
        return 2
    arguments = parse_arguments(sys.argv[1:], description)
    report = Report(name, arguments.report)
    live = Live(Client(os.environ["BASE"]), report, arguments)
    try:
        if live.login():
            body(live, report)
    except KeyboardInterrupt:
        report.failed("interrupted")
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {one_line(str(error), 120)}")
    return report.finish()
