"""Fault pictures for the matting fake, and the checks of what the public API shows for them (#301).

On the dev-test fake shape the browser, the smoke scripts and the Agent only ever talk to Plinth;
Plinth's own Adapter talks to the fake, and it sends no test header, uses the fixed key
"fake-test-key" and rewrites the upload filenames. The one thing that reaches the fake as it was
uploaded is the picture itself, so the matting fake reads the faults of a file from the file
(internal/testutil/fakeupstream/matting/api-v1, FileFaultMarker): a list

    fake-file-faults:<fault>[,<fault>...];

in the head of the file, with the fault words of its X-Fake-File-Faults header (rejected, failed,
gpu_oom, renamed). solid_png writes such a list into a PNG text chunk; nothing else is needed on
the Plinth side, so the same pictures work through the browser, the library upload and the Agent.

run() drives the two paths this opens on a deployed stack, through the public Task API, and asserts
the public projection against the Schema's own sentences:

  * #284: a batch that comes back with fewer results than pictures is a SUCCEEDED Task whose
    notices name the pictures that were not matted ("第 2 张、第 4 张没有处理成功，其余已完成。");
  * #299: a batch whose only picture ran out of GPU memory is a FAILED Task of the capacity class
    that is not retried, and the public text is the class's shared Schema sentence (the service's own
    text has no public channel).

Everything that decides something is a plain function, so scripts/tests/test_fake_faults.py covers it
without a server; Client and run() are thin. The module only talks to the API it is given.

To try a path by hand (the browser's matting panel on a fake-shaped dev-test), write a picture with
the fault in it and upload it like any other:

    python3 scripts/smoke/fake_faults.py picture failed.png --fault failed
    python3 scripts/smoke/fake_faults.py picture oom.png --size 2048x2048 --fault gpu_oom
"""
import argparse
import collections
import hashlib
import http.cookiejar
import json
import re
import struct
import sys
import time
import urllib.error
import urllib.request
import zlib

MARKER = "fake-file-faults:"
MARKER_END = ";"
# The fault words of the matting fake (mattingapiv1.FileFaultWords).
FILE_FAULTS = ("rejected", "failed", "gpu_oom", "renamed")

MATTING = "matting/default/background-removal"
CAPABILITY = "background-removal"
PARTIAL_CODE = "partial_inputs"
# What the service itself said about a picture its GPU could not hold (#299). It has no public
# channel: the public failure carries the class's shared Schema sentence only.
SERVICE_TEXT = ("显存", "BIREFNET_PROCESS_RES", "SDMATTE_PROCESS_RES", "图片尺寸")

# A picture of the incident of 2026-11-13: 2048x2048, which the service's GPU could not hold at the
# process resolutions of that day.
INCIDENT_SIDE = 2048

PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


# ---------------------------------------------------------------- pictures

def fault_mark(faults):
    """The marker text that asks the matting fake for the given faults."""
    unknown = [word for word in faults if word not in FILE_FAULTS]
    if unknown or not faults:
        raise ValueError(f"a marker needs fault words from {FILE_FAULTS}, got {list(faults)}")
    return MARKER + ",".join(faults) + MARKER_END


def png_chunk(kind, data):
    body = kind + data
    return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)


def solid_png(width, height, faults=(), color=(200, 60, 60)):
    """A valid, plain RGB PNG; with faults, a tEXt chunk right after the header carries the marker.

    Deterministic and free of third-party imaging libraries, like the other smoke pictures. The text
    chunk uses the standard "Comment" keyword and a valid CRC, so every decoder (Plinth's upload
    validation included) still reads the picture, and it lies within the head the fake looks at.
    """
    header = PNG_SIGNATURE + png_chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    text = png_chunk(b"tEXt", b"Comment\x00" + fault_mark(faults).encode("ascii")) if faults else b""
    row = b"\x00" + bytes(color) * width
    return header + text + png_chunk(b"IDAT", zlib.compress(row * height)) + png_chunk(b"IEND", b"")


# ---------------------------------------------------------------- Schema sentences

def compose_notice(messages, code, inputs):
    """The sentence of a succeeded Task's notice, built from Schema messages only: the same rule as
    catalog.ComposeTaskNotice. With positions and a "task.notice.<code>_named" sentence, {inputs} is
    the positions ("task.notice.input" with {ordinal} filled) joined by "task.notice.input_separator";
    otherwise "task.notice.<code>". Empty when the Schema has no text."""
    if not code:
        return ""
    base = f"task.notice.{code}"
    if inputs:
        sentence = messages.get(base + "_named", "")
        if sentence:
            item = messages.get("task.notice.input", "")
            names = [item.replace("{ordinal}", str(position)) for position in inputs]
            return sentence.replace("{inputs}", messages.get("task.notice.input_separator", "").join(names))
    return messages.get(base, "")


# ---------------------------------------------------------------- the cases

Case = collections.namedtuple("Case", "label pictures state delivered missing failure_class",
                              defaults=((), "SUCCEEDED", 0, (), ""))


def cases():
    """What run() submits. Every picture is (width, height, faults); positions are 1-based."""
    def plain(side=64):
        return (side, side, ())

    return (
        Case("#284 pictures that were not matted are named in the Task's notice",
             pictures=(plain(), (64, 64, ("failed",)), plain(), (64, 64, ("rejected",))),
             state="SUCCEEDED", delivered=2, missing=(2, 4)),
        Case("#299 the batch of the real run: a picture the GPU cannot hold next to a matted one",
             pictures=((INCIDENT_SIDE, INCIDENT_SIDE, ("gpu_oom",)), plain(16)),
             state="SUCCEEDED", delivered=1, missing=(1,)),
        Case("#299 a batch whose only picture ran out of GPU memory fails as a capacity failure",
             pictures=((INCIDENT_SIDE, INCIDENT_SIDE, ("gpu_oom",)),),
             state="FAILED", failure_class="upstream_unavailable"),
        Case("pictures without a marker are matted and the Task says nothing",
             pictures=(plain(), plain()),
             state="SUCCEEDED", delivered=2),
    )


def case_problems(case, task, messages):
    """What differs between the public Task and what the case, with the Schema's sentences, says."""
    problems = []
    if task.get("state") != case.state:
        return [f"state {task.get('state')}, want {case.state} (failure {json.dumps(task.get('failure'), ensure_ascii=False)})"]
    public = json.dumps({"failure": task.get("failure"), "notices": task.get("notices")}, ensure_ascii=False)
    leaked = [text for text in SERVICE_TEXT if text in public]
    if leaked:
        problems.append(f"the service's own text reached the public projection: {leaked}")
    delivered = len(task.get("assets") or [])
    if delivered != case.delivered:
        problems.append(f"{delivered} assets delivered, want {case.delivered}")
    if case.state == "SUCCEEDED":
        problems += success_problems(case, task, messages)
    else:
        problems += failure_problems(case, task, messages)
    return problems


def success_problems(case, task, messages):
    problems = []
    if task.get("failure"):
        problems.append("a succeeded Task carries a failure")
    notices = task.get("notices") or []
    if not case.missing:
        return problems + ([f"notices on a Task that delivered everything: {notices}"] if notices else [])
    want = compose_notice(messages, PARTIAL_CODE, list(case.missing))
    if not want:
        return problems + [f"the Schema has no sentence for the {PARTIAL_CODE} notice"]
    if len(notices) != 1:
        return problems + [f"{len(notices)} notices, want one: {notices}"]
    notice = notices[0]
    if notice.get("code") != PARTIAL_CODE:
        problems.append(f"notice code {notice.get('code')!r}, want {PARTIAL_CODE!r}")
    if notice.get("inputs") != list(case.missing):
        problems.append(f"notice names the pictures {notice.get('inputs')}, want {list(case.missing)}")
    if notice.get("detail") != want:
        problems.append(f"notice text {notice.get('detail')!r}, want the Schema's sentence {want!r}")
    return problems


def failure_problems(case, task, messages):
    problems = []
    failure = task.get("failure") or {}
    if failure.get("class") != case.failure_class:
        problems.append(f"failure class {failure.get('class')!r}, want {case.failure_class!r}")
    if failure.get("retryable") is not False:
        problems.append(f"failure retryable {failure.get('retryable')!r}, want false (the same picture would run out again)")
    want = messages.get("failure." + case.failure_class, "")
    if not want:
        problems.append(f"the Schema has no sentence for failure.{case.failure_class}")
    elif failure.get("detail") != want:
        problems.append(f"failure text {failure.get('detail')!r}, want the Schema's sentence {want!r}")
    if task.get("notices"):
        problems.append(f"notices on a failed Task: {task.get('notices')}")
    return problems


def listed_problems(listed, task):
    """The Task as the task list shows it must carry the same notices, failure class and state."""
    if listed is None:
        return ["the Task is missing from the session's task list"]
    problems = []
    for name in ("state", "notices"):
        if listed.get(name) != task.get(name):
            problems.append(f"the task list shows {name} {listed.get(name)!r}, the Task {task.get(name)!r}")
    if (listed.get("failure") or {}).get("class") != (task.get("failure") or {}).get("class"):
        problems.append("the task list shows another failure class than the Task")
    return problems


# ---------------------------------------------------------------- the HTTP side

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Client:
    """The few calls the checks need, with a cookie jar and the same-origin header the API wants."""

    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None, headers=None):
        data = None
        merged = {"Origin": self.base, **(headers or {})}
        if body is not None:
            data = json.dumps(body).encode()
            merged["Content-Type"] = "application/json"
        request = urllib.request.Request(self.base + path, data=data, method=method, headers=merged)
        try:
            response = self.opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        except OSError:
            return 0, b"", {}
        raw = response.read()
        return getattr(response, "status", None) or response.code, raw, dict(response.headers)

    def json(self, method, path, body=None, headers=None):
        status, raw, _headers = self.call(method, path, body, headers)
        try:
            return status, json.loads(raw)
        except ValueError:
            return status, {}

    def upload(self, path, field, filename, data, mime):
        """POST one file as multipart/form-data (the library upload); returns (status, json)."""
        boundary = "----faultpicture" + hashlib.sha256(data).hexdigest()[:16]
        body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"{field}\"; filename=\"{filename}\"\r\n"
                f"Content-Type: {mime}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(self.base + path, data=body, method="POST", headers={
            "Origin": self.base, "Content-Type": f"multipart/form-data; boundary={boundary}"})
        try:
            response = self.opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        except OSError:
            return 0, {}
        raw = response.read()
        try:
            return getattr(response, "status", None) or response.code, json.loads(raw)
        except ValueError:
            return getattr(response, "status", None) or response.code, {}

    def login(self, username, password):
        return self.json("POST", "/api/auth/login", {"username": username, "password": password})


def clip(value, limit=200):
    return json.dumps(value, ensure_ascii=False)[:limit]


def schema_facts(api):
    """The public Schema's sentences (content.messages) and the approval decision member
    (chat.api.fields.decision), read once like the browser reads them."""
    status, schema = api.json("GET", "/api/v1/schema")
    schema = schema.get("schema", schema) if status == 200 and isinstance(schema, dict) else {}
    messages = (schema.get("content") or {}).get("messages") or {}
    member = (((schema.get("chat") or {}).get("api") or {}).get("fields") or {}).get("decision")
    return messages, member


def upload_picture(api, filename, data):
    status, body = api.upload("/api/v1/assets", "file", filename, data, "image/png")
    return (body.get("id") if status in (200, 201) else None), f"upload {status} {clip(body)}"


def submit_task(api, session, member, payload):
    """prepare -> grant (-> approval when the policy asks a person) -> submit. Returns (task_id, why)."""
    body = {"session_id": session, "capability": CAPABILITY, "offering_id": MATTING, "input": payload}
    status, prepared = api.json("POST", "/api/v1/tasks/prepare", body)
    if status not in (200, 201):
        return None, f"prepare {status} {clip(prepared)}"
    prepared_id = prepared["prepared_input_id"]
    key = {"Idempotency-Key": "fake-faults-" + prepared_id}
    status, grant = api.json("POST", "/api/v1/tasks/grants", {"prepared_input_id": prepared_id}, headers=key)
    if status not in (200, 201):
        return None, f"grant {status} {clip(grant)}"
    request_id = grant.get("request_id")
    if grant.get("status") == "pending":
        if not member:
            return None, "the grant is pending and the Schema declares no decision member"
        status, decided = api.json("PUT", f"/api/v1/tasks/grants/{request_id}", {member: "approve"})
        if status != 200:
            return None, f"approve {status} {clip(decided)}"
    status, task = api.json("POST", "/api/v1/tasks", {"prepared_input_id": prepared_id, "authorization_request_id": request_id}, headers=key)
    if status not in (200, 201, 202):
        return None, f"submit {status} {clip(task)}"
    return task.get("task_id") or task.get("id"), ""


def wait_task(api, task_id, seconds=180):
    end = time.time() + seconds
    task = {}
    while time.time() < end:
        status, task = api.json("GET", f"/api/v1/tasks/{task_id}")
        if status == 200 and task.get("state") in ("SUCCEEDED", "FAILED", "CANCELED"):
            return task
        time.sleep(1)
    return task


def asset_problems(api, task):
    """Every delivered picture arrives with the bytes its declaration says."""
    problems = []
    for asset in task.get("assets") or []:
        status, raw, _headers = api.call("GET", asset["url"])
        if status != 200 or len(raw) != asset.get("size") or hashlib.sha256(raw).hexdigest() != asset.get("sha256"):
            problems.append(f"bytes of {asset.get('role')}: status {status}, {len(raw)} bytes")
        elif not raw.startswith(PNG_SIGNATURE):
            problems.append(f"{asset.get('role')} is not a PNG")
    return problems


def session_task(api, session, task_id):
    status, body = api.json("GET", f"/api/v1/tasks?session_id={session}&limit=50")
    tasks = (body.get("tasks") or body.get("data") or []) if status == 200 else []
    return next((entry for entry in tasks if entry.get("id") == task_id), None)


def run(api, record, wait_seconds=180):
    """Submit every case's batch first, so the fake holds them together, then check each Task.

    record(name, ok, detail) reports one check. The Tasks belong to a fresh session of the logged-in
    user: nothing depends on earlier data.
    """
    messages, member = schema_facts(api)
    if not messages:
        record("fault pictures: the public Schema's messages", False, "GET /api/v1/schema carries no content.messages")
        return
    status, session = api.json("POST", "/api/chat/sessions", {"title": ""})
    session = session.get("id") if status in (200, 201) else None
    if not session:
        record("fault pictures: create a session", False, f"status {status}")
        return
    submitted = []
    for number, case in enumerate(cases(), 1):
        assets, why = [], ""
        for position, (width, height, faults) in enumerate(case.pictures, 1):
            color = (30 + 40 * number, 40 + 30 * position, 200)
            asset, why = upload_picture(api, f"fault-picture-{number}-{position}.png", solid_png(width, height, faults, color))
            if not asset:
                break
            assets.append(asset)
        task_id = None
        if len(assets) == len(case.pictures):
            task_id, why = submit_task(api, session, member, {"source_assets": assets})
        submitted.append((case, task_id, why))
    for case, task_id, why in submitted:
        if not task_id:
            record(case.label, False, why)
            continue
        task = wait_task(api, task_id, wait_seconds)
        problems = case_problems(case, task, messages)
        if not problems:
            problems = asset_problems(api, task) + listed_problems(session_task(api, session, task_id), task)
        detail = (f"{task.get('state')}, {len(task.get('assets') or [])} assets, notices "
                  f"{[notice.get('inputs') for notice in task.get('notices') or []]}, failure class "
                  f"{(task.get('failure') or {}).get('class')}")
        record(case.label, not problems, detail if not problems else "; ".join(problems))


# ---------------------------------------------------------------- by hand

def main(argv=None):
    """`picture`: write a PNG whose own bytes ask the matting fake for faults, to upload by hand."""
    parser = argparse.ArgumentParser(description="Write a picture that chooses its own matting fake fault (#301).")
    modes = parser.add_subparsers(dest="mode", required=True)
    picture = modes.add_parser("picture", help="write a PNG whose own bytes carry fake-file-faults")
    picture.add_argument("output", help="the PNG file to write")
    picture.add_argument("--size", default="64x64", help="WIDTHxHEIGHT in pixels (default 64x64)")
    picture.add_argument("--fault", action="append", default=[], choices=FILE_FAULTS,
                         help="a fault the file gets (repeatable); none writes a plain picture")
    arguments = parser.parse_args(sys.argv[1:] if argv is None else argv)
    size = re.fullmatch(r"(\d+)x(\d+)", arguments.size)
    if not size or not all(1 <= int(side) <= 8192 for side in size.groups()):
        parser.error("--size must be WIDTHxHEIGHT with each side from 1 to 8192")
    data = solid_png(int(size.group(1)), int(size.group(2)), tuple(arguments.fault))
    with open(arguments.output, "wb") as handle:
        handle.write(data)
    print(f"{arguments.output}: {arguments.size} PNG, {len(data)} bytes, "
          + (fault_mark(arguments.fault) if arguments.fault else "no marker"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
