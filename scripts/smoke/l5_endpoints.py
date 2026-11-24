"""Engine of the endpoint-chain L5 scripts: smoke-l5-meshy-endpoints.py and smoke-l5-hyper3d.py (#539).

The two scripts walk every API function of one 3D provider once, through Plinth's own public task
API (prepare -> grants -> tasks), with fixed Offerings, and keep the evidence a real round needs.
Everything that decides something is a plain function, so scripts/tests/test_l5_endpoints.py covers
it without a server; Api, Runner and the writers are thin.

Rules every run keeps (AGENTS.md "上游接口与 Fake"):

* The target decides the gate (upstream_shape.py, fake-default). A fake-shaped target runs with no
  switch and refuses --live; any other target (live, mixed or unknown) needs both --live and
  ART_AI_LIVE_ACK=1, or the script exits 2 before a single request. Each real run also needs the
  user's consent in the conversation first.
* Every POST is sent once. A failed or timed-out submission is observed (the task is read, never
  re-sent); no step is ever replayed. A step that has to run again is a new request the operator
  starts, with --only and --seed, after reading why the first one failed.
* A spending ceiling (--max-credits, provider credits) is checked against each step's frozen quote
  before the step is submitted; a step that would cross it, and every later step, is not sent.
* Downloaded bytes and the raw task payloads live in a 0700 directory outside the checkout and are
  removed when the run ends (--keep-raw keeps them and says where). The report holds no prompt text, no
  URL query and no credential: secrets are read from the environment only and never printed.
"""

import argparse
import decimal
import hashlib
import json
import os
import shutil
import struct
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

sys.dont_write_bytecode = True  # run from the repository: leave no __pycache__ in scripts/smoke
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import upstream_shape  # noqa: E402

Decimal = decimal.Decimal
REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
LEGACY_UNIT = "credits"
TERMINAL = ("SUCCEEDED", "FAILED", "CANCELED", "CANCELLED")
GLB_MAGIC = b"glTF"
ZIP_MAGIC = b"PK\x03\x04"
PNG_MAGIC = b"\x89PNG\r\n\x1a\n"
# A failure the service answered with a definite refusal: no job was created, so a new request with
# other parameters is not a replay (the 8K -> 2K fallback of the multi-image step).
DEFINITE_REFUSALS = ("invalid_input", "rejected")


# ------------------------------------------------------------------ gate

def gate(argv, environ):
    """(shape, refusal): the fake-default gate of upstream_shape.py applied to the script's argv."""
    return upstream_shape.resolve("fake-default", upstream_shape.LIVE_FLAG in argv, environ)


def outside_repository(path):
    """The directory is not inside the checkout, so nothing in it can be committed by accident."""
    root = os.path.realpath(REPO)
    target = os.path.realpath(path)
    return os.path.commonpath([root, target]) != root


def private_directory():
    """A new 0700 directory outside the checkout (tempfile creates it with mode 0700)."""
    path = tempfile.mkdtemp(prefix="l5-endpoints-")
    os.chmod(path, 0o700)
    if not outside_repository(path):
        shutil.rmtree(path, ignore_errors=True)
        raise RuntimeError("the temporary directory lies inside the checkout; set TMPDIR elsewhere")
    return path


# ------------------------------------------------------------------ arguments

def add_shared_arguments(parser, default_ceiling, unit_label):
    parser.add_argument("--report", help="directory for the redacted report and evidence files")
    parser.add_argument("--live", action="store_true",
                        help="confirm real, paid upstream requests (also needs ART_AI_LIVE_ACK=1)")
    parser.add_argument("--max-credits", default=str(default_ceiling),
                        help=f"spending ceiling in {unit_label}; a step whose frozen quote crosses it is not sent "
                             f"(default %(default)s)")
    parser.add_argument("--only", default="", help="comma-separated step names to run (needs --seed for their sources)")
    parser.add_argument("--seed", action="append", default=[], metavar="STEP=TASK_ID",
                        help="a finished earlier step to reuse as a source instead of running it again (repeatable)")
    parser.add_argument("--task-timeout", type=int, default=3600, help="seconds to wait for one task (default %(default)s)")
    parser.add_argument("--poll", type=float, default=5.0, help="seconds between task reads (default %(default)s)")
    parser.add_argument("--balance-wait", type=float, default=40.0,
                        help="seconds to wait before reading the closing balance: the panel is cached for a short "
                             "time (default %(default)s)")
    parser.add_argument("--keep-raw", action="store_true",
                        help="keep the raw task payloads and downloaded bytes (printed path, mode 0700) instead of deleting them")


def parse_arguments(argv, description, default_ceiling, unit_label, add_arguments=None):
    parser = argparse.ArgumentParser(description=description, formatter_class=argparse.RawDescriptionHelpFormatter)
    add_shared_arguments(parser, default_ceiling, unit_label)
    if add_arguments:
        add_arguments(parser)
    arguments = parser.parse_args(argv)
    if arguments.task_timeout < 30:
        parser.error("--task-timeout must be at least 30 seconds")
    if arguments.poll <= 0:
        parser.error("--poll must be positive")
    try:
        arguments.ceiling = parse_amount(arguments.max_credits)
    except ValueError as error:
        parser.error(str(error))
    try:
        arguments.seeds = parse_seeds(arguments.seed)
    except ValueError as error:
        parser.error(str(error))
    arguments.only_steps = [name for name in (item.strip() for item in arguments.only.split(",")) if name]
    return arguments


def parse_amount(text):
    amount = common.native_amount(text)
    if amount is None or amount <= 0:
        raise ValueError(f"--max-credits {text!r} is not a positive number")
    return amount


def parse_seeds(items):
    seeds = {}
    for item in items:
        name, separator, task_id = item.partition("=")
        if not separator or not name.strip() or not task_id.strip():
            raise ValueError(f"--seed {item!r} is not STEP=TASK_ID")
        if name.strip() in seeds:
            raise ValueError(f"--seed names {name.strip()!r} twice")
        seeds[name.strip()] = task_id.strip()
    return seeds


# ------------------------------------------------------------------ plan

class Step:
    """One request of a chain. `build(ctx)` returns the input parameters, or None to skip with ctx.skip_reason."""

    def __init__(self, name, offering, capability, build, needs=(), optional=False, free=False, verify=None,
                 only_if=None, reason="L5 acceptance run: the documented value this sample needs"):
        self.name = name
        self.offering = offering
        self.capability = capability
        self.build = build
        self.needs = tuple(needs)
        self.optional = optional
        self.free = free
        self.verify = verify
        self.only_if = only_if
        self.reason = reason


def plan_problems(steps, only, seeds):
    """Why a selection cannot run: an unknown name, or a step whose sources are neither selected before it
    nor seeded. [] when it can. An empty `only` selects every step."""
    names = [step.name for step in steps]
    problems = [f"unknown step {name!r}" for name in list(only) + list(seeds) if name not in names]
    if problems:
        return problems
    selected = set(only) if only else set(names)
    available = set(seeds)
    for step in steps:
        if step.name in selected:
            for need in step.needs:
                if need not in available:
                    problems.append(f"step {step.name} needs {need}: select it earlier or pass --seed {need}=TASK_ID")
            available.add(step.name)
    return problems


def selected_steps(steps, only):
    chosen = set(only)
    return [step for step in steps if not chosen or step.name in chosen]


def cleanup_order(results):
    """Task IDs of the steps that ran, in reverse run order: a step is deleted before the sources it used,
    because the chain runs sources first."""
    return [result["task_id"] for result in reversed(results) if result.get("task_id")]


# ------------------------------------------------------------------ parameters and prices

def fill_parameters(offering, parameters):
    """The request's input: the given values, every required parameter left out at its declared default, and
    the Offering's extra-credit consent when it names one (a paid step must carry it to be quoted and run)."""
    payload = dict(parameters)
    for parameter in offering.get("parameters") or []:
        name = parameter.get("name")
        if name not in payload and parameter.get("required") and parameter.get("default") is not None:
            payload[name] = parameter["default"]
    consent = offering.get("extra_credit_consent_parameter")
    if consent:
        payload[consent] = True
    return payload


def advised_reasons(offering, parameters, reason):
    """parameter_reasons for the advised parameters the request changes: the server refuses an advised change
    without a reason, and a reason for any other parameter."""
    reasons = {}
    for parameter in offering.get("parameters") or []:
        name = parameter.get("name")
        if parameter.get("adjustment_policy") == "advised" and name in parameters \
                and parameters[name] != parameter.get("default"):
            reasons[name] = reason
    return reasons


def parameter_limit(offering, name, fallback):
    """The byte limit an Offering declares for an input parameter (max_bytes), else the fallback."""
    for parameter in (offering or {}).get("parameters") or []:
        if parameter.get("name") == name and isinstance(parameter.get("max_bytes"), int):
            return parameter["max_bytes"]
    return fallback


def step_price(prepared, offering, free=False):
    """(amount, unit) the step is frozen at: the native quote of the prepared input, else the Offering's
    credit estimate. (Decimal(0), None) for a step known to be free; None when nothing prices it."""
    price = common.quote_price((prepared or {}).get("native_quote"))
    if price is not None:
        return price[1], price[0]
    estimate = (offering or {}).get("estimated_credits")
    if isinstance(estimate, int) and not isinstance(estimate, bool) and estimate > 0:
        return Decimal(estimate), LEGACY_UNIT
    if free:
        return Decimal(0), None
    return None


class Budget:
    """The ceiling of one run. A step is reserved when it is submitted; a step that does not fit, and every
    step after it, is not sent."""

    def __init__(self, ceiling):
        self.ceiling = Decimal(ceiling)
        self.spent = Decimal(0)
        self.exhausted = False

    def fits(self, amount):
        return self.spent + amount <= self.ceiling

    def reserve(self, amount):
        if not self.fits(amount):
            self.exhausted = True
            return False
        self.spent += amount
        return True


def balance_of(payload, provider):
    """The native balance of one provider from the balance panel's answer, as a Decimal, or None when the
    panel could not read it (unknown is not zero)."""
    for account in (payload or {}).get("accounts") or []:
        if isinstance(account, dict) and account.get("provider") == provider:
            native = account.get("native_balance")
            return common.native_amount(native.get("amount")) if isinstance(native, dict) else None
    return None


def reconcile(before, after, quoted):
    """The run's spending against the balance: delta = before - after, and how it compares with the sum of the
    frozen quotes. The quotes are estimates (a refine price may be unlisted), so a difference is evidence, not
    a verdict; `readable` is false when either balance could not be read."""
    if before is None or after is None:
        return {"readable": False, "quoted": str(quoted)}
    delta = before - after
    return {"readable": True, "before": str(before), "after": str(after), "delta": str(delta),
            "quoted": str(quoted), "difference": str(delta - quoted), "matched": delta == quoted,
            "negative": delta < 0}


# ------------------------------------------------------------------ pictures

def gradient_png(width=64, height=64, tint=(200, 90, 60)):
    """A deterministic RGB picture: a diagonal gradient with a dark disc, so a model upload has a subject."""
    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            dx, dy = x - width / 2, y - height / 2
            if dx * dx + dy * dy < (min(width, height) / 3) ** 2:
                row.extend((tint[0] * 3 // 4, tint[1] * 3 // 4, tint[2] * 3 // 4))
            else:
                row.extend(((255 * x) // max(width, 1), (255 * y) // max(height, 1), 235))
        rows.append(bytes(row))
    return common.encode_png(width, height, rows)


# ------------------------------------------------------------------ GLB reading

def glb_document(head):
    """(json document, offset of the binary chunk) of a GLB 2.0 from its first bytes, or None when they are not
    one. `head` holds at least the header and the whole JSON chunk."""
    if len(head) < 20 or head[:4] != GLB_MAGIC:
        return None
    version, total, json_length, chunk = struct.unpack_from("<III4s", head, 4)
    if version != 2 or chunk != b"JSON" or len(head) < 20 + json_length:
        return None
    try:
        document = json.loads(head[20:20 + json_length])
    except ValueError:
        return None
    return (document, 20 + json_length + 8) if isinstance(document, dict) else None


def glb_summary(path, probe=256 * 1024):
    """What a GLB file shows of itself, read from its own bytes: triangles of the triangle primitives, the edge of
    every embedded image, meshes, materials, the POSITION bounding box height and whether the declared length is the
    file's. None when the file is not a GLB 2.0."""
    try:
        size = os.path.getsize(path)
        with open(path, "rb") as handle:
            head = handle.read(20)
            if len(head) < 20:
                return None
            json_length = struct.unpack_from("<I", head, 12)[0]
            head += handle.read(json_length)
            parsed = glb_document(head)
            if parsed is None:
                return None
            document, binary = parsed
            declared = struct.unpack_from("<I", head, 8)[0]
            triangles, accessors = 0, document.get("accessors") or []
            low, high = None, None
            for mesh in document.get("meshes") or []:
                for primitive in mesh.get("primitives") or []:
                    if primitive.get("mode", 4) != 4:
                        continue
                    index = primitive.get("indices", (primitive.get("attributes") or {}).get("POSITION"))
                    if isinstance(index, int) and 0 <= index < len(accessors):
                        triangles += int(accessors[index].get("count", 0)) // 3
                    position = (primitive.get("attributes") or {}).get("POSITION")
                    if isinstance(position, int) and 0 <= position < len(accessors):
                        accessor = accessors[position]
                        if accessor.get("min") and accessor.get("max") and len(accessor["min"]) == 3:
                            low = accessor["min"][1] if low is None else min(low, accessor["min"][1])
                            high = accessor["max"][1] if high is None else max(high, accessor["max"][1])
            edges = []
            views = document.get("bufferViews") or []
            for image in document.get("images") or []:
                view = image.get("bufferView")
                if not isinstance(view, int) or not 0 <= view < len(views):
                    continue
                handle.seek(binary + int(views[view].get("byteOffset", 0)))
                dimensions = common.image_dimensions(handle.read(probe))
                if dimensions:
                    edges.append(max(dimensions))
            return {"triangles": triangles, "texture_edges": edges, "meshes": len(document.get("meshes") or []),
                    "materials": len(document.get("materials") or []), "images": len(document.get("images") or []),
                    "height": (high - low) if low is not None else None, "length_ok": declared == size}
    except (OSError, struct.error, ValueError, TypeError):
        return None


def triangle_window(requested_faces, factor, tolerance):
    """(low, high) triangles a Quad request of `requested_faces` faces is expected to deliver (factor triangles per
    face, with a relative tolerance)."""
    center = requested_faces * factor
    return int(center * (1 - tolerance)), int(center * (1 + tolerance))


# ------------------------------------------------------------------ asset checks

def asset_label(asset):
    return f"{asset.get('role')}/{asset.get('variant') or '-'}"


def find_asset(task, role, variant=None):
    for asset in (task or {}).get("assets") or []:
        if asset.get("role") == role and (variant is None or asset.get("variant") == variant):
            return asset
    return None


def required_slots_declared(offering):
    """The Offering declares at least one output that must be delivered (delivery required, minimum above zero). An
    on-demand Offering such as a format conversion declares none: its task delivers what was asked for."""
    return any(output.get("delivery") == "required" and output.get("minimum", 1) > 0
               for output in (offering or {}).get("outputs") or [])


def slot_problems(offering, task):
    """Required output slots (delivery required, minimum above zero) the task's assets do not fill."""
    delivered = [(asset.get("role"), asset.get("variant")) for asset in (task or {}).get("assets") or []]
    problems = []
    for output in (offering or {}).get("outputs") or []:
        if output.get("delivery") != "required" or output.get("minimum", 1) == 0:
            continue
        if not any(role == output.get("role") and (output.get("variant") is None or variant == output.get("variant"))
                   for role, variant in delivered):
            problems.append(f"missing {output.get('role')}/{output.get('variant') or '-'}")
    return problems


def magic_problem(asset, head):
    """The first bytes do not fit the format the asset's media type or variant names; '' when they do or when the
    format has no magic number this script knows."""
    media = str(asset.get("media_type", "")).lower()
    variant = str(asset.get("variant", "")).lower()
    if variant == "glb" or media == "model/gltf-binary":
        return "" if head[:4] == GLB_MAGIC else "not a GLB"
    if media == "image/png":
        return "" if head[:8] == PNG_MAGIC else "not a PNG"
    if variant in ("3mf", "usdz") or media == "application/zip":
        return "" if head[:4] == ZIP_MAGIC else "not a ZIP archive"
    return ""


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


# ------------------------------------------------------------------ evidence

def asset_evidence(task):
    """The assets of a task, without the delivery URL (its signed query must never be written)."""
    return [{"role": asset.get("role"), "variant": asset.get("variant"), "required": bool(asset.get("required")),
             "media_type": asset.get("media_type"), "size": asset.get("size"), "sha256": str(asset.get("sha256", ""))[:12]}
            for asset in (task or {}).get("assets") or []]


def failure_evidence(task):
    failure = (task or {}).get("failure") or {}
    return {"class": failure.get("class"), "retryable": failure.get("retryable"), "ambiguous": bool(failure.get("ambiguous"))} \
        if failure else None


# ------------------------------------------------------------------ the HTTP side

class Api:
    """The few calls a chain makes, over l5_common.Client (cookie session, JSON)."""

    def __init__(self, base):
        self.client = common.Client(base)
        self.base = self.client.base

    def login(self, username, password):
        return self.client.login(username, password)

    def json(self, method, path, body=None, headers=None, timeout=60):
        return self.client.call(method, path, body, timeout=timeout, headers=headers)

    def upload(self, filename, data, media_type):
        """Library upload (multipart, field `file`): the asset ID, or None."""
        boundary = "l5endpoints3b7e91"
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
                f"Content-Type: {media_type}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(self.base + "/api/v1/assets", data=body, method="POST")
        request.add_header("Origin", self.base)
        request.add_header("Content-Type", "multipart/form-data; boundary=" + boundary)
        try:
            with self.client.opener.open(request, timeout=300) as response:
                return json.loads(response.read()).get("id")
        except (urllib.error.URLError, TimeoutError, OSError, ValueError):
            return None

    def download(self, path, destination):
        return self.client.download(path, destination)


class Context:
    """What earlier steps left for later ones."""

    def __init__(self, api, private, arguments, shape):
        self.api = api
        self.private = private
        self.arguments = arguments
        self.shape = shape
        self.results = {}
        self.uploads = {}
        self.files = {}
        self.offerings = {}
        self.skip_reason = ""

    @property
    def fake(self):
        return self.shape == "fake"

    def task_id(self, name):
        return (self.results.get(name) or {}).get("task_id")

    def task(self, name):
        return (self.results.get(name) or {}).get("task") or {}

    def asset_id(self, name, role, variant=None):
        asset = find_asset(self.task(name), role, variant)
        return asset.get("id") if asset else None

    def file(self, name, role, variant=None):
        return self.files.get((name, role, variant)) or self.files.get((name, role, None))


class Runner:
    """Runs a chain: one submission per step, every task observed to its end, every delivered byte checked."""

    def __init__(self, ctx, report, offerings, budget, session_id, key_prefix):
        self.ctx = ctx
        self.report = report
        self.offerings = offerings
        self.budget = budget
        self.session_id = session_id
        self.key_prefix = key_prefix
        self.evidence = []

    # -- submission: prepare -> grants (-> approval) -> tasks, each POST once
    def submit(self, offering, parameters, reasons, step):
        api = self.ctx.api
        body = {"session_id": self.session_id, "capability": step.capability, "offering_id": offering["id"],
                "input": parameters}
        if reasons:
            body["parameter_reasons"] = reasons
        status, prepared = api.json("POST", "/api/v1/tasks/prepare", body)
        if status not in (200, 201) or not prepared.get("prepared_input_id"):
            return None, None, f"prepare: HTTP {status} {common.one_line(json.dumps(prepared, ensure_ascii=False), 120)}"
        price = step_price(prepared, offering, step.free)
        if price is None:
            return None, None, "no frozen quote and no credit estimate: the step cannot be bounded by the ceiling"
        if not self.budget.fits(price[0]):
            self.budget.exhausted = True
            return None, price, f"budget: {self.budget.spent} spent + {price[0]} quoted is over the ceiling {self.budget.ceiling}"
        prepared_id = prepared["prepared_input_id"]
        headers = {"Idempotency-Key": f"{self.key_prefix}-{prepared_id}"}
        status, grant = api.json("POST", "/api/v1/tasks/grants", {"prepared_input_id": prepared_id}, headers=headers)
        if status not in (200, 201):
            return None, price, f"grant: HTTP {status}"
        request_id = grant.get("request_id")
        if grant.get("status") == "pending":
            decision = self.decision(request_id)
            if decision != 200:
                return None, price, f"approval: HTTP {decision}"
        self.budget.reserve(price[0])  # from here the request may spend, whatever its answer says
        status, accepted = api.json("POST", "/api/v1/tasks", {"prepared_input_id": prepared_id,
                                                              "authorization_request_id": request_id}, headers=headers)
        task_id = (accepted or {}).get("task_id") or (accepted or {}).get("id")
        if status not in (200, 201, 202) or not task_id:
            return None, price, f"submit: HTTP {status} (not sent again: the task may or may not exist upstream)"
        return task_id, price, ""

    def decision(self, request_id):
        """Approve a pending step grant the way the browser does, with the Schema's own route and member."""
        api = self.ctx.api
        status, data = api.json("GET", "/api/v1/schema")
        schema = data.get("schema") if isinstance(data.get("schema"), dict) else data
        template = (((schema.get("runtime") or {}).get("http") or {}).get("routes") or {}).get("task_grant_decision")
        body = common.decision_body(((schema.get("chat") or {}).get("api") or {}).get("fields"), True)
        if status != 200 or not template or "{requestID}" not in template or not request_id or body is None:
            return 0
        route = template.replace("{requestID}", urllib.parse.quote(str(request_id), safe=""))
        return api.json("PUT", route, body)[0]

    # -- observation: read, never re-send
    def wait(self, task_id):
        api, arguments = self.ctx.api, self.ctx.arguments
        deadline = time.time() + arguments.task_timeout
        task, states = {}, []
        while time.time() < deadline:
            status, data = api.json("GET", f"/api/v1/tasks/{task_id}")
            if status == 200 and isinstance(data, dict):
                task = data
                if not states or states[-1] != task.get("state"):
                    states.append(task.get("state"))
                if task.get("state") in TERMINAL:
                    return task, states, True
            time.sleep(arguments.poll)
        return task, states, False

    def fetch_assets(self, step_name, task):
        """Download every asset of the task once into the private directory (the model files twice, to compare the
        bytes). Returns the problems found."""
        problems = []
        for index, asset in enumerate((task or {}).get("assets") or []):
            destination = os.path.join(self.ctx.private, f"{common.safe_name(step_name)}-{index}-{common.safe_name(asset_label(asset))}")
            status, size = self.ctx.api.download(asset["url"], destination)
            if status != 200 or size <= 0:
                problems.append(f"{asset_label(asset)} HTTP {status}")
                continue
            digest = sha256_file(destination)
            if size != asset.get("size") or digest != asset.get("sha256"):
                problems.append(f"{asset_label(asset)} bytes differ from the declaration")
            with open(destination, "rb") as handle:
                head = handle.read(16)
            bad = magic_problem(asset, head)
            if bad:
                problems.append(f"{asset_label(asset)} {bad}")
            if asset.get("role") == "model" and (asset.get("variant") == "glb" or asset.get("media_type") == "model/gltf-binary"):
                again = destination + ".again"
                status, _ = self.ctx.api.download(asset["url"], again)
                if status != 200 or sha256_file(again) != digest:
                    problems.append(f"{asset_label(asset)} second download differs")
                os.remove(again)
            self.ctx.files[(step_name, asset.get("role"), asset.get("variant"))] = destination
            self.ctx.files.setdefault((step_name, asset.get("role"), None), destination)
        return problems

    # -- one step
    def run(self, step):
        ctx, report = self.ctx, self.report
        label = step.name
        record = {"step": step.name, "offering": step.offering}
        missing = [need for need in step.needs if (ctx.results.get(need) or {}).get("state") != "SUCCEEDED"]
        if missing:
            report.skipped(label, "needs " + ", ".join(missing) + " to have succeeded")
            record["skipped"] = "source did not succeed"
            return self.finish(record, None)
        if step.only_if is not None and not step.only_if(ctx):
            report.skipped(label, ctx.skip_reason or "not needed")
            record["skipped"] = ctx.skip_reason or "condition not met"
            return self.finish(record, None)
        if self.budget.exhausted:
            report.skipped(label, "spending ceiling reached: not sent")
            record["skipped"] = "budget"
            return self.finish(record, None)
        offering = self.offerings.get(step.offering)
        if not offering or not offering.get("available"):
            (report.skipped if step.optional else report.failed)(label, "Offering not available on this target")
            record["skipped"] = "offering unavailable"
            return self.finish(record, None)
        ctx.skip_reason = ""
        parameters = step.build(ctx)
        if parameters is None:
            report.skipped(label, ctx.skip_reason or "inputs not available")
            record["skipped"] = ctx.skip_reason or "inputs not available"
            return self.finish(record, None)
        parameters = fill_parameters(offering, parameters)
        reasons = advised_reasons(offering, parameters, step.reason)
        started = time.time()
        task_id, price, why = self.submit(offering, parameters, reasons, step)
        if price:
            record["quote"] = {"amount": str(price[0]), "unit": price[1]}
        if not task_id:
            report.failed(f"{label} submitted", why)
            record["state"] = "NOT_SUBMITTED"
            record["why"] = why
            ctx.results[step.name] = {"state": "NOT_SUBMITTED", "task_id": None}
            return self.finish(record, None)
        report.passed(f"{label} submitted", f"task {task_id}" + (f", quoted {price[0]} {price[1] or ''}".rstrip() if price else ""))
        task, states, finished = self.wait(task_id)
        record.update({"task_id": task_id, "states": states, "seconds": round(time.time() - started, 1),
                       "state": task.get("state") if finished else "OBSERVING"})
        ctx.results[step.name] = {"state": record["state"], "task_id": task_id, "task": task, "price": price}
        if not finished:
            report.failed(f"{label} finished", f"still {task.get('state')} after {ctx.arguments.task_timeout}s; "
                          "observed only, never re-sent")
            return self.finish(record, task)
        if task.get("state") != "SUCCEEDED":
            failure = failure_evidence(task) or {}
            report.failed(f"{label} SUCCEEDED", f"{task.get('state')} class={failure.get('class')} ambiguous={failure.get('ambiguous')}")
            record["failure"] = failure or None
            return self.finish(record, task)
        problems = slot_problems(offering, task)
        if required_slots_declared(offering):
            ready, reason = common.required_assets_ready(task)
        else:
            ready, reason = bool(task.get("assets")), "no asset delivered"
        if not ready:
            problems.append(reason)
        problems += self.fetch_assets(step.name, task)
        report.check(f"{label} deliverables", not problems, "; ".join(problems) or f"{len(task.get('assets') or [])} asset(s), bytes match the declaration")
        record["assets"] = asset_evidence(task)
        if step.verify is not None:
            for check_label, ok, detail in step.verify(ctx, task) or []:
                report.check(f"{label} {check_label}", ok, detail)
        return self.finish(record, task)

    def finish(self, record, task):
        self.evidence.append(record)
        return record


def run_chain(ctx, report, steps, offerings, session_id, key_prefix, budget):
    """Run the selected steps in order; a seeded step is read, never run."""
    runner = Runner(ctx, report, offerings, budget, session_id, key_prefix)
    for name, task_id in ctx.arguments.seeds.items():
        status, task = ctx.api.json("GET", f"/api/v1/tasks/{task_id}")
        if status != 200 or task.get("state") != "SUCCEEDED":
            report.failed(f"seed {name}", f"task {task_id}: HTTP {status}, state {task.get('state')}")
            continue
        ctx.results[name] = {"state": "SUCCEEDED", "task_id": task_id, "task": task, "seeded": True}
        runner.fetch_assets(name, task)
        report.passed(f"seed {name}", f"task {task_id} reused, nothing sent")
    for step in selected_steps(steps, ctx.arguments.only_steps):
        runner.run(step)
    return runner


# ------------------------------------------------------------------ entry point

def read_balance(api, provider):
    status, data = api.json("GET", "/api/meshy/balance")
    return balance_of(data, provider) if status == 200 else None


def quoted_total(results):
    total = Decimal(0)
    for result in results.values():
        if result.get("price") and not result.get("seeded"):
            total += result["price"][0]
    return total


def write_json(directory, name, payload):
    if not directory:
        return
    os.makedirs(directory, exist_ok=True)
    with open(os.path.join(directory, name), "w", encoding="utf-8") as handle:
        json.dump(payload, handle, ensure_ascii=False, indent=2, default=str)


def execute(name, provider, steps, description, default_ceiling, unit_label, argv=None, environ=None,
            add_arguments=None, make_context=None, after=None):
    """Gate, environment, login, chain, balance, summary. Returns the exit code:
    0 every check passed, 1 a check failed, 2 refused or misused."""
    argv = sys.argv[1:] if argv is None else list(argv)
    environ = os.environ if environ is None else environ
    arguments = parse_arguments(argv, description, default_ceiling, unit_label, add_arguments)
    shape, refusal = gate(argv, environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    missing = common.missing_environment(environ)
    if missing:
        print("missing environment: " + ", ".join(missing) + " (run through scripts/release-env.py smoke)", file=sys.stderr)
        return 2
    problem = common.validate_base(environ["BASE"])
    if problem:
        print(problem, file=sys.stderr)
        return 2
    selection = plan_problems(steps, arguments.only_steps, arguments.seeds)
    if selection:
        print("; ".join(selection), file=sys.stderr)
        return 2
    report = common.Report(name, arguments.report)
    private = private_directory()
    print(f"# target shape {shape}; spending ceiling {arguments.ceiling}; raw files in a private directory "
          f"({'kept' if arguments.keep_raw else 'removed at the end'})", flush=True)
    api = Api(environ["BASE"])
    ctx = make_context(api, private, arguments, shape) if make_context else Context(api, private, arguments, shape)
    evidence = {"script": name, "shape": shape, "ceiling": str(arguments.ceiling)}
    try:
        if not api.login(environ["ADMIN_USERNAME"], environ["ADMIN_PASSWORD"]):
            report.failed("login")
            return report.finish()
        report.passed("login")
        status, offering_list = api.json("GET", "/api/v1/offerings")
        offerings = {item["id"]: item for item in offering_list.get("offerings") or []} if status == 200 else {}
        status, session = api.json("POST", "/api/chat/sessions", {"title": f"L5 {provider} endpoints"})
        session_id = session.get("id")
        if not offerings or status not in (200, 201) or not session_id:
            report.failed("session and Offerings", f"offerings {len(offerings)}, session HTTP {status}")
            return report.finish()
        ctx.offerings = offerings
        before = read_balance(api, provider)
        report.check("opening balance readable", before is not None, f"{provider} balance {before}")
        budget = Budget(arguments.ceiling)
        runner = run_chain(ctx, report, steps, offerings, session_id, f"l5-{provider}", budget)
        quoted = quoted_total(ctx.results)
        if arguments.ceiling and budget.spent > arguments.ceiling:
            report.failed("spending ceiling", f"{budget.spent} reserved over {arguments.ceiling}")
        time.sleep(arguments.balance_wait if shape != "fake" else 0)
        closing = read_balance(api, provider)
        if closing is not None and before is not None and closing == before and quoted > 0 and shape != "fake":
            for _ in range(3):  # the panel is cached for a short time
                time.sleep(arguments.balance_wait)
                closing = read_balance(api, provider)
                if closing != before:
                    break
        balance = reconcile(before, closing, quoted)
        report.check("closing balance readable", balance["readable"], f"{provider} balance {closing}")
        if balance["readable"]:
            print(f"RECONCILE: balance {balance['before']} -> {balance['after']} (spent {balance['delta']}), "
                  f"quoted {balance['quoted']}, difference {balance['difference']} (quotes are estimates)", flush=True)
            report.check("balance did not rise", not balance["negative"], f"delta {balance['delta']}")
        evidence.update({"steps": runner.evidence, "balance": balance, "spent_reserved": str(budget.spent),
                         "budget_exhausted": budget.exhausted,
                         "cleanup_order": cleanup_order([result for result in ctx.results.values() if not result.get("seeded")])})
        if after is not None:
            after(ctx, report, evidence)
    except KeyboardInterrupt:
        report.failed("interrupted")
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {common.one_line(str(error), 120)}")
    finally:
        write_json(arguments.report, f"{name}-evidence.json", evidence)
        if arguments.keep_raw:
            print(f"# raw files kept in {private} (mode 0700); delete them when done", flush=True)
        else:
            shutil.rmtree(private, ignore_errors=True)
    return report.finish()
