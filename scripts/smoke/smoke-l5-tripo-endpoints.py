#!/usr/bin/env python3
"""L5: every Tripo API function Plinth exposes, once, through Plinth's public task API (#567). Default target: the fake.

Chain (one request per step, fixed Offerings, prepare -> grants -> tasks; a later step uses an earlier one as source):

  text_geo       Tripo v3.1 text-to-3D, geometry only: texture and PBR off, standard geometry, 3000 faces, GLTF export
  > rig_check    rig check of text_geo (free)
  > rig_v25      rigging v2.5 of text_geo          > rig_v3   rigging v3.0 of text_geo
  > retarget     animation (retarget, preset:walk) of rig_v3
  > decimate     triangle decimation of text_geo to 1000 faces (six fixed export formats)
  > segment      segmentation of text_geo        > quick_cap  completion quick_cap of segment
                                                 > complete   AI completion of segment
  > texture      texture of text_geo (the Offering's defaults, five fixed export formats)
  > glb_to_stl   text_geo's own GLB uploaded to the library and converted to STL (a file_token input)
  > smartseg_model  Smart Segmentation of the same uploaded GLB
  image          v3.1 image-to-3D of a drawn picture, the cheapest parameters (as text_geo)
  multiview      v3.1 multiview-to-3D of two drawn views (front, left), the cheapest parameters
  i2mv           image to four views of the drawn picture   > edit_mv  edit of its front view
  splat          image to Gaussian splat of the drawn picture
  smartseg_image Smart Segmentation of the drawn picture
  p2_text, p2_image, p2_multiview   Tripo P2 text, image and multiview to 3D: texture off, 3000 faces
  default_text   v3.1 text-to-3D with the product's defaults (extreme texture, detailed geometry, six output_formats)

Pictures are drawn by the script (a flat figure of coloured parts on white), so no prompt or picture can meet a content
policy; the uploaded GLB is the text_geo step's own model. Offerings and parameters come from the public Schema
(/api/v1/offerings); a parameter the step does not name keeps the Offering's default.

Spending (Tripo credits, from the Catalog: the native quote of the generation Offerings, and base plus the fixed
export formats of the processing Offerings, which the public Offering shows as maximum_estimated_credits):

  text_geo 15, rig_check 0, rig_v25 45, rig_v3 45, retarget 10, decimate 95, segment 90, quick_cap 80, complete 100,
  texture 80, glb_to_stl 5, smartseg_model 105, image 25, multiview 25, i2mv 10, edit_mv 5, splat 30 (no published
  price; the Catalog's estimate), smartseg_image 135, p2_text 100, p2_image 100, p2_multiview 100, default_text 90

  = about 1290 in all. Without the fixed export formats the processing steps cost their base price only and the
  chain would be about 840 (the figure the issue was planned with); the export formats are not optional in these
  Offerings, so the base figure is not reachable.

The default ceiling --max-credits is 900: a step whose bound would cross it, and every later step, is not sent, so the
default run stops before the P2 steps and default_text. Pass --max-credits 1300 for the whole chain, or run the rest
later with --only and --seed. Check the account balance first: a Tripo task is billed when it is created.

Reconciliation: each step's bound (frozen native quote, or the processing estimate) is compared with what the task
itself shows (its native_quote and credits_charged), and with how much the provider balance moved across the step
(--no-step-balance skips the per-step reads; each waits --balance-wait seconds for the balance cache on a real target).
The upstream credits_consumed is not part of the public task API, so the balance movement is the measured figure.
The whole run is reconciled against the opening and closing balance as in the other endpoint scripts.

Not covered: tripo/tripo-v3.1/format-conversion (a convert from a task), tripo/tripo-v3.1/text-to-3d and image-to-3d
(the non-full forms) and tripo/geometry/decimate-quad are disabled in the Catalog and are not on the public API; the
six formats of a task are covered by default_text's output_formats and decimate's fixed exports instead. Tripo results
need no clean-up (no upstream delete is needed), so there is no --cleanup.

Gate (AGENTS.md "上游接口与 Fake"): a fake-shaped target runs with no switch and refuses --live; any other
target needs --live AND ART_AI_LIVE_ACK=1 or the script exits 2 before a request. The shape comes from
ART_AI_UPSTREAM_SHAPE (release-env.py smoke sets it). Each real run also needs the user's consent first.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-tripo-endpoints.py
  ART_AI_UPSTREAM_SHAPE=fake BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... python3 scripts/smoke/smoke-l5-tripo-endpoints.py
  python3 scripts/release-env.py smoke dev-test -- env ART_AI_LIVE_ACK=1 python3 scripts/smoke/smoke-l5-tripo-endpoints.py --live --max-credits 1300 --report DIR

Every POST is sent once; a failure or timeout is only observed. Re-run a failed step with --only STEP --seed
SOURCE=TASK_ID (a new request, after reading why). Raw payloads and downloaded bytes live in a 0700 directory
outside the checkout and are removed at the end (--keep-raw keeps them).

Exit 0 = every check passed, 1 = a check failed, 2 = refused or misused. It never prints credentials, cookies,
signed URLs or full prompts.
"""
import json
import os
import struct
import sys
import time

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import l5_endpoints as engine  # noqa: E402

NAME = "smoke-l5-tripo-endpoints"
PROVIDER = "tripo"
UNIT = "Tripo credits"
DEFAULT_CEILING = 900

TEXT_TO_3D = "tripo/tripo-v3.1-full/text-to-3d"
IMAGE_TO_3D = "tripo/tripo-v3.1-full/image-to-3d"
MULTIVIEW_TO_3D = "tripo/tripo-v3.1-multiview/image-to-3d"
P2_TEXT = "tripo/tripo-p2/text-to-3d"
P2_IMAGE = "tripo/tripo-p2/image-to-3d"
P2_MULTIVIEW = "tripo/tripo-p2-multiview/image-to-3d"
RIG_CHECK = "tripo/processing/rig-check"
RIGGING = "tripo/processing/rigging"
RIGGING_V3 = "tripo/processing/rigging-v3"
ANIMATION = "tripo/processing/animation"
DECIMATE = "tripo/geometry/decimate-triangle"
SEGMENT = "tripo/geometry/segment"
QUICK_CAP = "tripo/geometry/quick-cap"
COMPLETE = "tripo/geometry/complete"
TEXTURE = "tripo/processing/texture"
GLB_TO_STL = "tripo/format-conversion/uploaded-glb-to-stl"
SMARTSEG_IMAGE = "tripo/geometry/smartsegment-image"
SMARTSEG_MODEL = "tripo/geometry/smartsegment-model"
SPLAT = "tripo/image-to-splat"
IMAGE_TO_MULTIVIEW = "tripo/image-to-multiview"
EDIT_MULTIVIEW = "tripo/edit-multiview"

SIX_FORMATS = ("gltf", "usdz", "fbx", "obj", "stl", "3mf")
VIEWS = ("front", "left", "back", "right")
GEOMETRY_FACES = 3000
DECIMATE_FACES = 1000
# A face bound the real service keeps within a few percent; the fake's model is one fixed fixture, so the bound is
# enforced against a real upstream only (the count is recorded either way).
BOUND_SLACK = 1.05
# Tripo's decimation takes face_limit as a target, not a ceiling. The real round of 2026-11-24 (#577) sent face_limit 1000
# (v3.0, quad false, bake, smart_uv; the task repeated face_limit 1000) for a 2,814-triangle source and got 1,566
# triangles, 57% over. The bound is twice the target, which leaves room for that overshoot on another source and still
# fails a decimation that kept the source (text_geo is capped at GEOMETRY_FACES); a decimation must also come out with
# fewer triangles than its source. Both are enforced on a real upstream only, like BOUND_SLACK.
DECIMATE_SLACK = 2.0
DEFAULT_UPLOAD_LIMIT = 10 << 20
PICTURE_EDGE = 512

CHARACTER = ("A single full-body low-poly humanoid robot standing upright in a T-pose, arms straight out to the sides, "
             "plain grey metal, simple blocky shapes, no text")
PROP = "A small wooden treasure chest with iron bands and a closed lid, plain light background, no text"
TEXTURE_PROMPT = "plain matte blue paint with light wear on the edges"
EDIT_PROMPT = "make the body plain red"
CHEAP = {"texture": False, "pbr": False, "geometry_quality": "standard", "face_limit": GEOMETRY_FACES,
         "output_formats": ["GLTF"]}
P2_CHEAP = {"texture": False, "pbr": False, "face_limit": GEOMETRY_FACES}


# ------------------------------------------------------------------ pictures

def figure_png(edge=PICTURE_EDGE, side=False):
    """A flat figure on white made of coloured parts (head, body, arms, legs), front or side view. A plain disc was
    refused once by Smart Segmentation (#89); a picture with parts succeeded."""
    half = edge // 2
    unit = edge / 16
    if side:
        parts = [((180, 60, 50), "disc", (half, 3 * unit, 1.4 * unit)),
                 ((60, 110, 190), "rect", (half - 1.2 * unit, 4.6 * unit, half + 1.2 * unit, 10 * unit)),
                 ((70, 150, 80), "rect", (half - 0.6 * unit, 5 * unit, half + 0.6 * unit, 9.5 * unit)),
                 ((90, 90, 90), "rect", (half - 1.0 * unit, 10 * unit, half + 1.0 * unit, 15 * unit))]
    else:
        parts = [((180, 60, 50), "disc", (half, 3 * unit, 1.4 * unit)),
                 ((60, 110, 190), "rect", (half - 2.2 * unit, 4.6 * unit, half + 2.2 * unit, 10 * unit)),
                 ((70, 150, 80), "rect", (half - 6.5 * unit, 5 * unit, half - 2.2 * unit, 6.2 * unit)),
                 ((70, 150, 80), "rect", (half + 2.2 * unit, 5 * unit, half + 6.5 * unit, 6.2 * unit)),
                 ((90, 90, 90), "rect", (half - 2.0 * unit, 10 * unit, half - 0.4 * unit, 15 * unit)),
                 ((90, 90, 90), "rect", (half + 0.4 * unit, 10 * unit, half + 2.0 * unit, 15 * unit))]
    rows = []
    for y in range(edge):
        row = bytearray()
        for x in range(edge):
            colour = (255, 255, 255)
            for tint, shape, box in parts:
                if shape == "disc":
                    cx, cy, radius = box
                    inside = (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius
                else:
                    inside = box[0] <= x < box[2] and box[1] <= y < box[3]
                if inside:
                    colour = tint
            row.extend(colour)
        rows.append(bytes(row))
    return common.encode_png(edge, edge, rows)


def uploaded(ctx, key, filename, make, media_type):
    """Upload once and remember the asset ID; None (and a skip reason) when the library refuses it."""
    if key not in ctx.uploads:
        ctx.uploads[key] = ctx.api.upload(filename, make(), media_type)
    if not ctx.uploads[key]:
        ctx.skip_reason = f"the library refused the {filename} upload"
    return ctx.uploads[key]


def front_picture(ctx):
    return uploaded(ctx, "front", "figure-front.png", figure_png, "image/png")


def side_picture(ctx):
    return uploaded(ctx, "left", "figure-left.png", lambda: figure_png(side=True), "image/png")


def model_upload(ctx, offering_id):
    """text_geo's own GLB as a library model, or None with a skip reason when it exceeds the Offering's limit."""
    path = ctx.file("text_geo", "model")
    if not path:
        ctx.skip_reason = "text_geo delivered no model to upload"
        return None
    limit = engine.parameter_limit(ctx.offerings.get(offering_id), "source_asset", DEFAULT_UPLOAD_LIMIT)
    size = os.path.getsize(path)
    if size > limit:
        ctx.skip_reason = f"the text_geo GLB is {size} bytes, over the {limit}-byte upload limit"
        return None

    def read():
        with open(path, "rb") as handle:
            return handle.read()
    return uploaded(ctx, "model", "text-geo.glb", read, "model/gltf-binary")


# ------------------------------------------------------------------ prices and reconciliation

def processing_price(offering, prepared):
    """The bound of a step with no native quote: the public highest estimate (base plus the fixed export formats),
    which is the price itself when the selection is fixed; None lets the engine use the plain estimate."""
    if common.quote_price((prepared or {}).get("native_quote")) is not None:
        return None
    highest = (offering or {}).get("maximum_estimated_credits")
    if isinstance(highest, int) and not isinstance(highest, bool) and highest > 0:
        return engine.Decimal(highest), engine.LEGACY_UNIT
    return None


def task_figures(task):
    """(native quote total, credits charged) the task itself shows, each None when absent."""
    quote = common.quote_price((task or {}).get("native_quote"))
    charged = (task or {}).get("credits_charged")
    charged = engine.Decimal(charged) if isinstance(charged, int) and not isinstance(charged, bool) else None
    return (quote[1] if quote else None), charged


def quote_checks(record, task):
    """The task keeps the quote the step was bounded by, and never shows a charge above it."""
    bound = engine.Decimal(record["quote"]["amount"]) if record.get("quote") else None
    native, charged = task_figures(task)
    record["task_native_quote"] = None if native is None else str(native)
    record["credits_charged"] = None if charged is None else str(charged)
    checks = []
    if bound is not None and native is not None:
        checks.append(("task keeps the frozen quote", native == bound, f"task {native}, prepared {bound}"))
    if bound is not None and charged is not None:
        checks.append(("charge within the bound", charged <= bound, f"charged {charged}, bound {bound}"))
    return checks


def start(ctx, report, opening):
    ctx.last_balance = opening


def on_step(ctx, report, record, task):
    """Per submitted step: the quote against the task's own figures, then (unless --no-step-balance) how far the
    balance moved across the step, written to the evidence and as an INFO line (a difference is evidence: another
    use of the account or the balance cache can move it too)."""
    if (task or {}).get("state") == "SUCCEEDED":
        for label, ok, detail in quote_checks(record, task):
            report.check(f"{record['step']} {label}", ok, detail)
    if ctx.arguments.no_step_balance:
        return
    if not ctx.fake:
        time.sleep(ctx.arguments.balance_wait)
    now = engine.read_balance(ctx.api, PROVIDER)
    previous = getattr(ctx, "last_balance", None)
    moved = (previous - now) if previous is not None and now is not None else None
    record["balance_moved"] = None if moved is None else str(moved)
    ctx.last_balance = now if now is not None else previous
    quoted = record.get("quote", {}).get("amount")
    note = "" if moved is None or quoted is None or moved == engine.Decimal(quoted) else \
        (" (the fake bills the task, its balance stays fixed)" if ctx.fake else " (differs)")
    print(f"INFO: {record['step']} cost: quoted {quoted}, balance moved {moved}{note}", flush=True)


# ------------------------------------------------------------------ verification

def delivered_formats(task):
    return sorted({str(asset.get("variant", "")).rsplit(".", 1)[-1].lower()
                   for asset in (task or {}).get("assets") or [] if asset.get("role") in ("model", "converted_model")
                   and asset.get("variant")})


def check_formats(task, wanted):
    present = set(delivered_formats(task))
    missing = sorted(set(wanted) - present)
    return ("formats", not missing, f"missing {missing}" if missing else f"{len(set(wanted))} format(s) delivered")


def glb_document_of(path):
    """The JSON chunk of a GLB file, or None."""
    try:
        with open(path, "rb") as handle:
            head = handle.read(20)
            if len(head) < 20:
                return None
            head += handle.read(struct.unpack_from("<I", head, 12)[0])
    except (OSError, struct.error):
        return None
    parsed = engine.glb_document(head)
    return parsed[0] if parsed else None


def model_path(ctx, name):
    return ctx.file(name, "model", "glb") or ctx.file(name, "model")


def glb_checks(ctx, name, bound=None, textured=False, parts=False, slack=BOUND_SLACK, source=None):
    path = model_path(ctx, name)
    summary = engine.glb_summary(path) if path else None
    checks = [("GLB readable", summary is not None, "GLB 2.0 read from its own bytes" if summary else "no readable GLB")]
    if not summary:
        return checks
    checks.append(("GLB length", summary["length_ok"], "declared length is the file's"))
    if textured:
        checks.append(("GLB textured", summary["materials"] > 0 and summary["images"] > 0,
                       f"{summary['materials']} material(s), {summary['images']} image(s), edges {summary['texture_edges']}"))
    if bound is not None:
        within = summary["triangles"] <= bound * slack
        checks.append(("GLB triangles", within or ctx.fake, f"{summary['triangles']} triangles, limit {bound}"
                       + (f" (x{slack:g})" if slack != BOUND_SLACK else "")
                       + ("" if within else " (recorded; enforced on a real upstream only)")))
    if source is not None:
        source_path = model_path(ctx, source)
        before = engine.glb_summary(source_path) if source_path else None
        if before:
            fewer = summary["triangles"] < before["triangles"]
            checks.append(("GLB reduced", fewer or ctx.fake, f"{summary['triangles']} triangles from {before['triangles']}"
                           + ("" if fewer else " (recorded; enforced on a real upstream only)")))
    if parts:
        split = summary["meshes"] >= 2
        checks.append(("GLB in parts", split or ctx.fake, f"{summary['meshes']} mesh(es)"
                       + ("" if split else " (the fake's model is one mesh; enforced on a real upstream only)")))
    return checks


def after_success(ctx, name, task, wanted):
    """The task once its post-success formats have landed (#462: the main model and GLTF make the task SUCCEEDED,
    the other requested formats follow while resources_pending is true), read again and never re-sent, and the
    problems of the formats that arrived late (downloaded, size and SHA-256 against the declaration, magic number)."""
    deadline = time.time() + ctx.arguments.task_timeout
    while (task.get("resources_pending") or set(wanted) - set(delivered_formats(task))) and time.time() < deadline:
        time.sleep(ctx.arguments.poll)
        status, data = ctx.api.json("GET", f"/api/v1/tasks/{task.get('id')}")
        if status == 200 and isinstance(data, dict):
            task = data
    problems = []
    for index, asset in enumerate(task.get("assets") or []):
        key = (name, asset.get("role"), asset.get("variant"))
        if asset.get("role") != "converted_model" or key in ctx.files or not asset.get("url"):
            continue
        destination = os.path.join(ctx.private, f"{name}-late-{index}-{common.safe_name(engine.asset_label(asset))}")
        status, size = ctx.api.download(asset["url"], destination)
        if status != 200 or size != asset.get("size") or engine.sha256_file(destination) != asset.get("sha256"):
            problems.append(f"{engine.asset_label(asset)} HTTP {status}, bytes differ from the declaration")
            continue
        with open(destination, "rb") as handle:
            bad = engine.magic_problem(asset, handle.read(16))
        if bad:
            problems.append(f"{engine.asset_label(asset)} {bad}")
        ctx.files[key] = destination
    ctx.results[name]["task"] = task
    return task, problems


def verify_geometry(name, bound=None, formats=("gltf",), textured=False):
    def verify(ctx, task):
        task, problems = after_success(ctx, name, task, formats)
        return [check_formats(task, formats),
                ("late formats", not problems, "; ".join(problems) or "every late format matches its declaration")] \
            + glb_checks(ctx, name, bound=bound, textured=textured)
    return verify


def verify_p2(name):
    def verify(ctx, task):
        return glb_checks(ctx, name, bound=GEOMETRY_FACES)
    return verify


def verify_rig_check(ctx, task):
    path = ctx.file("rig_check", "other", "report") or ctx.file("rig_check", "other")
    try:
        with open(path, "rb") as handle:
            report = json.loads(handle.read())
    except (OSError, TypeError, ValueError):
        return [("rig-check report readable", False, "not a JSON document")]
    keys = sorted(report) if isinstance(report, dict) else []
    return [("rig-check report readable", isinstance(report, dict), f"top-level keys {keys[:12]}")]


def verify_rig(name):
    def verify(ctx, task):
        document = glb_document_of(model_path(ctx, name)) or {}
        skinned = bool(document.get("skins"))
        return [check_formats(task, ("glb", "fbx")),
                ("GLB skinned", skinned or ctx.fake, f"{len(document.get('skins') or [])} skin(s)"
                 + ("" if skinned or not ctx.fake else " (recorded; enforced on a real upstream only)"))] \
            + glb_checks(ctx, name)
    return verify


def verify_retarget(ctx, task):
    document = glb_document_of(model_path(ctx, "retarget")) or {}
    animated = bool(document.get("animations"))
    return [("GLB animated", animated or ctx.fake, f"{len(document.get('animations') or [])} animation(s)"
             + ("" if animated or not ctx.fake else " (recorded; enforced on a real upstream only)"))] \
        + glb_checks(ctx, "retarget")


def verify_processed(name, formats=("glb", "fbx", "obj", "stl", "usdz", "3mf"), bound=None, textured=False, parts=False,
                     slack=BOUND_SLACK, source=None):
    def verify(ctx, task):
        return [check_formats(task, formats)] + glb_checks(ctx, name, bound=bound, textured=textured, parts=parts,
                                                           slack=slack, source=source)
    return verify


def stl_problem(path):
    """'' when the file is a binary STL whose triangle count fits its size, or an ASCII STL."""
    try:
        with open(path, "rb") as handle:
            data = handle.read(4096)
            size = os.path.getsize(path)
    except (OSError, TypeError):
        return "no STL file"
    if data[:5] == b"solid" and b"facet" in data:
        return ""
    if len(data) < 84:
        return "shorter than an STL header"
    count = struct.unpack_from("<I", data, 80)[0]
    return "" if count > 0 and size == 84 + 50 * count else f"{count} triangles do not fit {size} bytes"


def verify_stl(ctx, task):
    problem = stl_problem(ctx.file("glb_to_stl", "model", "stl") or ctx.file("glb_to_stl", "model"))
    return [("STL readable", not problem, problem or "binary or ASCII STL, triangle count fits the size")]


def verify_views(name, views):
    def verify(ctx, task):
        present = {asset.get("variant") for asset in (task or {}).get("assets") or [] if asset.get("role") == "image"}
        missing = sorted(set(views) - present)
        return [("views", not missing, f"missing {missing}" if missing else f"{', '.join(views)} delivered")]
    return verify


def verify_splat(ctx, task):
    asset = engine.find_asset(task, "gaussian-splat", "splat")
    size = (asset or {}).get("size")
    fits = isinstance(size, int) and size > 0 and size % 32 == 0
    return [("32-byte splat records", fits, f"{size} bytes")]


# ------------------------------------------------------------------ the chain

def source(name, extra=None):
    def build(ctx):
        params = {"source_task": ctx.task_id(name)}
        params.update(extra or {})
        return params
    return build


def build_image(ctx):
    picture = front_picture(ctx)
    return dict(CHEAP, reference_assets=[picture]) if picture else None


def build_multiview(cheap):
    def build(ctx):
        front, left = front_picture(ctx), side_picture(ctx)
        if not front or not left:
            return None
        return dict(cheap, reference_assets=[front, left], reference_views=["front", "left"])
    return build


def build_picture(name):
    def build(ctx):
        picture = front_picture(ctx)
        return {name: picture} if picture else None
    return build


def build_model(offering_id):
    def build(ctx):
        model = model_upload(ctx, offering_id)
        return {"source_asset": model} if model else None
    return build


def step(name, offering, capability, build, **kwargs):
    return engine.Step(name, offering, capability, build, price=processing_price, **kwargs)


def steps():
    geo = ("text_geo",)
    return [
        step("text_geo", TEXT_TO_3D, "text-to-3d", lambda ctx: dict(CHEAP, prompt=CHARACTER),
             verify=verify_geometry("text_geo", bound=GEOMETRY_FACES)),
        step("rig_check", RIG_CHECK, "rig-check", source("text_geo"), needs=geo, free=True, verify=verify_rig_check),
        step("rig_v25", RIGGING, "rigging", source("text_geo"), needs=geo, verify=verify_rig("rig_v25")),
        step("rig_v3", RIGGING_V3, "rigging", source("text_geo"), needs=geo, verify=verify_rig("rig_v3")),
        step("retarget", ANIMATION, "animation", source("rig_v3", {"animation_action": "preset:walk"}),
             needs=("rig_v3",), verify=verify_retarget),
        step("decimate", DECIMATE, "remesh", source("text_geo", {"face_limit": DECIMATE_FACES}), needs=geo,
             verify=verify_processed("decimate", bound=DECIMATE_FACES, slack=DECIMATE_SLACK, source="text_geo")),
        step("segment", SEGMENT, "mesh-segmentation", source("text_geo"), needs=geo,
             verify=verify_processed("segment", parts=True)),
        step("quick_cap", QUICK_CAP, "mesh-completion", source("segment"), needs=("segment",),
             verify=verify_processed("quick_cap")),
        step("complete", COMPLETE, "mesh-completion", source("segment"), needs=("segment",),
             verify=verify_processed("complete")),
        step("texture", TEXTURE, "retexture", source("text_geo", {"prompt": TEXTURE_PROMPT}), needs=geo,
             verify=verify_processed("texture", textured=True)),
        step("glb_to_stl", GLB_TO_STL, "format-conversion", build_model(GLB_TO_STL), needs=geo, verify=verify_stl),
        step("smartseg_model", SMARTSEG_MODEL, "mesh-segmentation", build_model(SMARTSEG_MODEL), needs=geo,
             verify=verify_processed("smartseg_model")),
        step("image", IMAGE_TO_3D, "image-to-3d", build_image, verify=verify_geometry("image", bound=GEOMETRY_FACES)),
        step("multiview", MULTIVIEW_TO_3D, "image-to-3d", build_multiview(CHEAP),
             verify=verify_geometry("multiview", bound=GEOMETRY_FACES)),
        step("i2mv", IMAGE_TO_MULTIVIEW, "image-to-multiview", build_picture("image"), verify=verify_views("i2mv", VIEWS)),
        step("edit_mv", EDIT_MULTIVIEW, "edit-multiview", source("i2mv", {"front_prompt": EDIT_PROMPT}), needs=("i2mv",),
             verify=verify_views("edit_mv", ("front",))),
        step("splat", SPLAT, "image-to-splat", build_picture("image"), verify=verify_splat),
        step("smartseg_image", SMARTSEG_IMAGE, "image-to-3d", build_picture("image"),
             verify=verify_processed("smartseg_image")),
        step("p2_text", P2_TEXT, "text-to-3d", lambda ctx: dict(P2_CHEAP, prompt=CHARACTER), verify=verify_p2("p2_text")),
        step("p2_image", P2_IMAGE, "image-to-3d",
             lambda ctx: dict(P2_CHEAP, reference_assets=[front_picture(ctx)]) if front_picture(ctx) else None,
             verify=verify_p2("p2_image")),
        step("p2_multiview", P2_MULTIVIEW, "image-to-3d", build_multiview(P2_CHEAP), verify=verify_p2("p2_multiview")),
        step("default_text", TEXT_TO_3D, "text-to-3d", lambda ctx: {"prompt": PROP},
             verify=verify_geometry("default_text", formats=SIX_FORMATS, textured=True)),
    ]


def add_arguments(parser):
    parser.add_argument("--no-step-balance", action="store_true",
                        help="do not read the balance after each step (on a real target each read waits --balance-wait)")


def main(argv=None, environ=None):
    return engine.execute(NAME, PROVIDER, steps(), __doc__, DEFAULT_CEILING, UNIT, argv=argv, environ=environ,
                          add_arguments=add_arguments, on_step=on_step, start=start)


if __name__ == "__main__":
    sys.exit(main())
