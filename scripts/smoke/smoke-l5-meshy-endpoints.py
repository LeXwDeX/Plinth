#!/usr/bin/env python3
"""L5: every Meshy API function once, through Plinth's public task API (#539). Default target: the fake.

Chain (one request per step, fixed Offerings, prepare -> grants -> tasks):

  t2i (nano-banana-2, T-pose humanoid) > i2i (side view of the t2i picture)
  > t25_text (meshy-t2.5 text-to-3D: preview and refine inside one Task)
  > t25_image (meshy-t2.5 image-to-3D from the t2i picture, 2k)
  > multiview (meshy-7.1 multi-image, t2i + i2i, 2k geometry, 8K texture; if the service refuses 8K with a
    definite answer, one extra request multiview_2k with a 2K texture is sent, a new request not a replay)
  > remesh (source t25_image, triangle, 10000 faces)
  > convert, retexture, rigging, uv_unwrap, resize, analyze, multi_color (all from the remesh task)
  > animation (from rigging)
  > motion (text-to-motion, independent)

Spending (Meshy credits; the Catalog's estimates, which the frozen quotes replace where a quote exists):
about 142 in all (6 + 6 + 15 + 20 + 40 + 5 + 1 + 15 + 5 + 3 + 5 + 1 + 0 + 10 + 10); the audit's list
price for the same chain is about 132 (t2.5 image-to-3D 15 and retexture 10 at 2K). The unit price of a
credit, and the price of the t2.5 refine step, are not listed by Meshy. The default ceiling --max-credits is 160:
a step whose frozen quote would cross it, and every later step, is not sent. The 40-credit multi-image step is
the dearest; with the 8K refusal fallback the worst case adds 35 more, so raise the ceiling on purpose for that.

Gate (AGENTS.md "上游接口与 Fake"): a fake-shaped target runs with no switch and refuses --live; any other
target needs --live AND ART_AI_LIVE_ACK=1 or the script exits 2 before a request. The shape comes from
ART_AI_UPSTREAM_SHAPE (release-env.py smoke sets it). Each real run also needs the user's consent first.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-meshy-endpoints.py
  ART_AI_UPSTREAM_SHAPE=fake BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... python3 scripts/smoke/smoke-l5-meshy-endpoints.py
  python3 scripts/release-env.py smoke dev-test -- env ART_AI_LIVE_ACK=1 python3 scripts/smoke/smoke-l5-meshy-endpoints.py --live --report DIR

#581 probe (--probe-untextured-pbr, off by default): one extra image-to-3d (meshy-t2.5, from the t2i picture) with
should_texture=false and enable_pbr=true. Expected against a build before the #581 fix: Meshy answers 400 ("enable_pbr is
only supported when should_texture is true") and spends no credits; the 2026-11-25 run did exactly that. From the #581
fix on, the Catalog forces enable_pbr to false and leaves it off the wire when should_texture is false, so the same
step is an accepted mesh-only generation that spends credits (its frozen quote counts against --max-credits).

Every POST is sent once; a failure or timeout is only observed. Re-run a failed step with --only STEP --seed
SOURCE=TASK_ID (a new request, after reading why). Raw payloads and downloaded bytes live in a 0700 directory
outside the checkout and are removed at the end (--keep-raw keeps them).

Meshy keeps a result for three days. Plinth's public API has no upstream delete and does not name the upstream
job, so the end-of-run clean-up is a separate, explicit command: the run writes the Plinth task IDs in
reverse dependency order to its evidence file (cleanup); the operator maps each to the upstream IDs (the server's
generation_attempts.upstream_job_id) and runs

  python3 scripts/smoke/smoke-l5-meshy-endpoints.py --cleanup EVIDENCE.json --remote-ids MAP.json [--live]

with MESHY_API_KEY (and MESHY_BASE_URL when it is not the official one) in the environment. Each DELETE is sent once,
dependents first; a real clean-up needs both live switches like a run, a loopback base (the fake) needs none.

Exit 0 = every check passed, 1 = a check failed, 2 = refused or misused. It never prints credentials, cookies,
signed URLs or full prompts.
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import l5_endpoints as engine  # noqa: E402
import upstream_shape  # noqa: E402

NAME = "smoke-l5-meshy-endpoints"
PROVIDER = "meshy"
UNIT = "Meshy credits"
DEFAULT_CEILING = 160

T2I = "meshy/nano-banana-2/text-to-image"
I2I = "meshy/nano-banana-2/image-to-image"
T25_TEXT = "meshy/meshy-t2.5/text-to-3d"
T25_IMAGE = "meshy/meshy-t2.5/image-to-3d"
MULTIVIEW = "meshy/meshy-7.1-multiview/image-to-3d"
REMESH = "meshy/processing/remesh"
CONVERT = "meshy/processing/convert"
RETEXTURE = "meshy/processing/retexture"
RIGGING = "meshy/processing/rigging"
ANIMATION = "meshy/processing/animation"
UV_UNWRAP = "meshy/processing/uv-unwrap"
RESIZE = "meshy/processing/resize"
ANALYZE = "meshy/processing/analyze-printability"
MULTI_COLOR = "meshy/processing/multi-color-print"
MOTION = "meshy/prime/text-to-motion"

# Where each Offering's upstream job lives (Catalog provider_options: api_version and endpoint), for the clean-up.
UPSTREAM_PATH = {
    T2I: "/openapi/v1/text-to-image", I2I: "/openapi/v1/image-to-image",
    T25_TEXT: "/openapi/v2/text-to-3d", T25_IMAGE: "/openapi/v1/image-to-3d",
    MULTIVIEW: "/openapi/v1/multi-image-to-3d", REMESH: "/openapi/v1/remesh", CONVERT: "/openapi/v1/convert",
    RETEXTURE: "/openapi/v1/retexture", RIGGING: "/openapi/v1/rigging", ANIMATION: "/openapi/v1/animations",
    UV_UNWRAP: "/openapi/v1/uv-unwrap", RESIZE: "/openapi/v1/resize", ANALYZE: "/openapi/v1/print/analyze",
    MULTI_COLOR: "/openapi/v1/print/multi-color", MOTION: "/openapi/v1/text-to-motion",
}
SIX_FORMATS = ("glb", "obj", "fbx", "stl", "usdz", "3mf")
# target_polycount is a target, not a ceiling: the 1.9 real round (2026-11-24, #568) asked meshy-t2.5 text-to-3D for
# 15000 and got a GLB of 15804 triangles, +5.4%, the largest overshoot measured. The bound allows +10%, about twice that,
# and still catches what would be a real fault: a quad counted as two triangles (+100%) or a target that is ignored.
# The fake's model is one fixed fixture and is not sized to the request, so the bound is enforced against a real
# upstream only (the count is recorded either way).
BOUND_SLACK = 1.10

CHARACTER = ("A single full-body low-poly humanoid game character standing upright in a T-pose, "
             "arms straight out to the sides, plain neutral clothing, plain white background, no text")
SIDE_VIEW = "The same character seen from the side, side view, T-pose, plain white background, no text"
ROBOT = "A low-poly humanoid robot character standing upright in a T-pose, plain grey metal, no text"
RETEXTURE_PROMPT = "weathered bronze with a green patina, plain matte finish"
MOTION_PROMPT = "a person waves one hand"


# ------------------------------------------------------------------ verification

def model_formats(task):
    return sorted({asset.get("variant") for asset in task.get("assets") or []
                   if asset.get("role") == "model" and asset.get("variant")})


def check_formats(task, wanted):
    present = set(model_formats(task))
    missing = sorted(set(wanted) - present)
    return ("formats", not missing, f"missing {missing}" if missing else f"{len(set(wanted))} requested format(s) delivered")


def glb_checks(ctx, name, bound=None, textured=True, height=None):
    path = ctx.file(name, "model", "glb")
    summary = engine.glb_summary(path) if path else None
    checks = [("GLB readable", summary is not None, "GLB 2.0 read from its own bytes" if summary else "no readable GLB")]
    if not summary:
        return checks
    if textured:
        checks.append(("GLB textured", summary["materials"] > 0 and summary["images"] > 0,
                       f"{summary['materials']} material(s), {summary['images']} image(s), edges {summary['texture_edges']}"))
    if bound is not None:
        enforce = not ctx.fake
        within = summary["triangles"] <= bound * BOUND_SLACK
        checks.append(("GLB triangles", within or not enforce,
                       f"{summary['triangles']} triangles, target {bound}, limit {int(bound * BOUND_SLACK)}" + ("" if enforce else " (recorded; enforced on a real upstream only)")))
    if height is not None and summary["height"] is not None:
        enforce = not ctx.fake
        near = abs(summary["height"] - height) <= height * 0.05
        checks.append(("GLB height", near or not enforce,
                       f"{summary['height']:.3f} m, want {height}" + ("" if enforce else " (recorded; enforced on a real upstream only)")))
    return checks


def verify_t2i(ctx, task):
    asset = engine.find_asset(task, "image")
    dims = None
    if ctx.file("t2i", "image"):
        with open(ctx.file("t2i", "image"), "rb") as handle:
            dims = common.image_dimensions(handle.read(64 << 10))
    return [("one PNG picture", bool(asset) and asset.get("media_type") == "image/png", f"{asset and asset.get('media_type')}, {dims}")]


def verify_i2i(ctx, task):
    images = [asset for asset in task.get("assets") or [] if asset.get("role") == "image"]
    source = engine.find_asset(ctx.task("t2i"), "image")
    different = bool(images) and bool(source) and images[0].get("sha256") != source.get("sha256")
    return [("exactly one picture", len(images) == 1, f"{len(images)} picture(s)"),
            ("differs from the reference", different or ctx.fake, "bytes differ from the reference" if different
             else "same bytes (accepted on the fake only)")]


def verify_t25_text(ctx, task):
    return [check_formats(task, SIX_FORMATS)] + glb_checks(ctx, "t25_text", bound=15000)


def verify_t25_image(ctx, task):
    return [check_formats(task, SIX_FORMATS)] + glb_checks(ctx, "t25_image", bound=15000)


def verify_multiview(name):
    def verify(ctx, task):
        return [check_formats(task, SIX_FORMATS)] + glb_checks(ctx, name)
    return verify


def verify_remesh(ctx, task):
    return [check_formats(task, SIX_FORMATS)] + glb_checks(ctx, "remesh", bound=10000, textured=False)


def verify_convert(ctx, task):
    formats = model_formats(task)
    only = set(formats) == {"stl", "usdz"}
    return [check_formats(task, ("stl", "usdz")),
            ("only the requested formats", only or ctx.fake, f"delivered {formats}" + ("" if only else " (the fake delivers every format)"))]


def verify_retexture(ctx, task):
    texture = engine.find_asset(task, "texture")
    source = engine.find_asset(ctx.task("remesh"), "texture")
    changed = bool(texture) and bool(source) and texture.get("sha256") != source.get("sha256")
    return [check_formats(task, ("glb", "obj", "fbx", "usdz")),
            ("texture differs from the source", changed or ctx.fake or not texture,
             "bytes differ" if changed else "same or no texture bytes (accepted on the fake only)")]


def verify_rigging(ctx, task):
    variants = {asset.get("variant") for asset in task.get("assets") or []}
    both = {"rigged_character_glb_url", "rigged_character_fbx_url"} <= variants
    return [("rigged character GLB and FBX", both, f"{len([v for v in variants if v])} variant(s) delivered")]


def verify_animation(ctx, task):
    return [("animation GLB", engine.find_asset(task, "model", "animation_glb_url") is not None, "animation_glb_url delivered")]


def verify_uv_unwrap(ctx, task):
    return [("GLB and thumbnail", engine.find_asset(task, "model", "glb") is not None and engine.find_asset(task, "thumbnail") is not None,
             "glb + thumbnail")] + glb_checks(ctx, "uv_unwrap", textured=False)


def verify_resize(ctx, task):
    return glb_checks(ctx, "resize", textured=False, height=1.0)


def verify_analyze(ctx, task):
    path = ctx.file("analyze", "report", "printability.v1.json") or ctx.file("analyze", "report")
    try:
        with open(path, "rb") as handle:
            report = json.loads(handle.read())
    except (OSError, TypeError, ValueError):
        return [("printability report readable", False, "not a JSON document")]
    keys = sorted(report) if isinstance(report, dict) else []
    return [("printability report readable", isinstance(report, dict), f"top-level keys {keys[:12]}")]


def verify_multi_color(ctx, task):
    archive = engine.find_asset(task, "print-archive", "3mf")
    path = ctx.file("multi_color", "print-archive", "3mf")
    head = b""
    if path:
        with open(path, "rb") as handle:
            head = handle.read(4)
    return [("3MF archive", archive is not None and head == engine.ZIP_MAGIC, f"ZIP magic {head[:4]!r}")]


def verify_motion(ctx, task):
    return [("motion clip FBX", engine.find_asset(task, "clip", "fbx") is not None, "clip/fbx delivered")]


# ------------------------------------------------------------------ the chain

def build_t2i(ctx):
    return {"prompt": CHARACTER, "aspect_ratio": "1:1", "pose_mode": "t-pose"}


def build_i2i(ctx):
    return {"prompt": SIDE_VIEW, "aspect_ratio": "1:1", "reference_assets": [ctx.asset_id("t2i", "image")]}


def build_t25_text(ctx):
    return {"prompt": ROBOT}


def build_t25_image(ctx):
    return {"reference_assets": [ctx.asset_id("t2i", "image")], "texture_resolution": "2k", "target_polycount": 15000}


def build_t25_image_untextured(ctx):
    """#581 probe: should_texture off with enable_pbr on. Before the #581 fix Plinth sent the pair and Meshy answered
    400 with no charge; since the fix the Catalog drops enable_pbr and the step is a paid mesh-only generation."""
    return {"reference_assets": [ctx.asset_id("t2i", "image")], "should_texture": False, "enable_pbr": True,
            "target_polycount": 15000}


def verify_untextured_probe(ctx, task):
    state = (task or {}).get("state") if isinstance(task, dict) else None
    failure = engine.failure_evidence(task) or {}
    return [("#581 probe outcome", True, f"state={state} failure_class={failure.get('class')} ambiguous={failure.get('ambiguous')}")]


def probe_enabled(ctx):
    return bool(ctx.arguments.probe_untextured_pbr)


def multiview_build(resolution_of):
    def build(ctx):
        return {"reference_assets": [ctx.asset_id("t2i", "image"), ctx.asset_id("i2i", "image")],
                "geometry_resolution": "2k", "texture_resolution": resolution_of(ctx), "enable_pbr": True}
    return build


def multiview_refused(ctx):
    """The 8K multi-image request got a definite refusal (nothing was created upstream), so a 2K one is a new request."""
    result = ctx.results.get("multiview") or {}
    failure = engine.failure_evidence(result.get("task")) or {}
    ctx.skip_reason = "the 8K multi-image step was not refused with a definite answer"
    return result.get("state") == "FAILED" and failure.get("class") in engine.DEFINITE_REFUSALS and not failure.get("ambiguous") \
        and ctx.arguments.multiview_texture != "2k"


def from_remesh(extra):
    def build(ctx):
        params = {"source_task": ctx.task_id("remesh")}
        params.update(extra)
        return params
    return build


def steps():
    remesh_needs = ("remesh",)
    return [
        engine.Step("t2i", T2I, "text-to-image", build_t2i, verify=verify_t2i),
        engine.Step("i2i", I2I, "image-to-image", build_i2i, needs=("t2i",), verify=verify_i2i),
        engine.Step("t25_text", T25_TEXT, "text-to-3d", build_t25_text, verify=verify_t25_text),
        engine.Step("t25_image", T25_IMAGE, "image-to-3d", build_t25_image, needs=("t2i",), verify=verify_t25_image),
        engine.Step("t25_image_untextured", T25_IMAGE, "image-to-3d", build_t25_image_untextured, needs=("t2i",),
                    optional=True, only_if=probe_enabled, verify=verify_untextured_probe,
                    reason="#581 probe: should_texture=false with enable_pbr=true (400 with no charge before the #581 fix)"),
        engine.Step("multiview", MULTIVIEW, "image-to-3d", multiview_build(lambda ctx: ctx.arguments.multiview_texture),
                    needs=("t2i", "i2i"), verify=verify_multiview("multiview")),
        engine.Step("multiview_2k", MULTIVIEW, "image-to-3d", multiview_build(lambda ctx: "2k"), needs=("t2i", "i2i"),
                    optional=True, only_if=multiview_refused, verify=verify_multiview("multiview_2k")),
        engine.Step("remesh", REMESH, "remesh",
                    lambda ctx: {"source_task": ctx.task_id("t25_image"), "topology": "triangle", "target_polycount": 10000},
                    needs=("t25_image",), verify=verify_remesh),
        engine.Step("convert", CONVERT, "format-conversion", from_remesh({"target_formats": ["stl", "usdz"]}),
                    needs=remesh_needs, verify=verify_convert),
        engine.Step("retexture", RETEXTURE, "retexture",
                    from_remesh({"prompt": RETEXTURE_PROMPT, "texture_resolution": "2k", "enable_pbr": True,
                                 "target_formats": ["glb", "obj", "fbx", "usdz"]}),
                    needs=remesh_needs, verify=verify_retexture),
        engine.Step("rigging", RIGGING, "rigging", from_remesh({"include_fingers": False}), needs=remesh_needs,
                    verify=verify_rigging),
        engine.Step("animation", ANIMATION, "animation",
                    lambda ctx: {"source_task": ctx.task_id("rigging"), "action_id": 10}, needs=("rigging",),
                    verify=verify_animation),
        engine.Step("uv_unwrap", UV_UNWRAP, "uv-unwrap", from_remesh({}), needs=remesh_needs, verify=verify_uv_unwrap),
        engine.Step("resize", RESIZE, "resize", from_remesh({"resize_height": 1.0, "origin_at": "bottom"}),
                    needs=remesh_needs, verify=verify_resize),
        engine.Step("analyze", ANALYZE, "analyze-printability", from_remesh({"intended_print_height_mm": 50}),
                    needs=remesh_needs, free=True, verify=verify_analyze),
        engine.Step("multi_color", MULTI_COLOR, "multi-color-print",
                    from_remesh({"style": "realistic", "max_colors": 16, "max_depth": 6, "printer_brand": "bambu"}),
                    needs=remesh_needs, verify=verify_multi_color),
        engine.Step("motion", MOTION, "text-to-motion", lambda ctx: {"prompt": MOTION_PROMPT, "duration": "2"},
                    verify=verify_motion),
    ]


def add_arguments(parser):
    parser.add_argument("--multiview-texture", choices=("8k", "2k"), default="8k",
                        help="texture resolution of the multi-image step (default %(default)s, the Offering's own default)")
    parser.add_argument("--probe-untextured-pbr", action="store_true",
                        help="also run the #581 probe (image-to-3d with should_texture=false and enable_pbr=true): expect "
                             "400 with no charge before the #581 fix; a paid mesh-only task after it (see the header)")
    parser.add_argument("--cleanup", metavar="EVIDENCE.json",
                        help="instead of a run: delete the upstream jobs of an earlier run (see the header)")
    parser.add_argument("--remote-ids", metavar="MAP.json",
                        help="with --cleanup: {plinth task id: [upstream job id, ...]} in creation order")


def add_cleanup(ctx, report, evidence):
    """The clean-up list: each task in reverse dependency order with the upstream path its job lives under."""
    offerings = {step["task_id"]: step["offering"] for step in evidence.get("steps", []) if step.get("task_id")}
    evidence["cleanup"] = [{"task_id": task_id, "path": UPSTREAM_PATH.get(offerings.get(task_id), "")}
                           for task_id in evidence.get("cleanup_order", [])]


# ------------------------------------------------------------------ clean-up (the only direct upstream calls)

def cleanup_refusal(base, live_flag, environ):
    """Why the clean-up must not run, or None. A loopback base is the fake and needs no switch; any other base
    deletes real jobs and needs both live switches."""
    if not environ.get("MESHY_API_KEY"):
        return "MESHY_API_KEY is not set"
    parsed = urllib.parse.urlparse(base)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.query or parsed.fragment or parsed.username:
        return "the Meshy base URL must be an http(s) origin without credentials or a query"
    if parsed.hostname in ("127.0.0.1", "localhost", "::1"):
        return "refusing to run: --live was given but the Meshy base is a loopback fake" if live_flag else None
    return upstream_shape.live_switch_error(live_flag, environ)


def cleanup_plan(evidence, remote_ids):
    """[(plinth task id, path)] to DELETE, dependents first: every upstream job of a task, the newest first (a
    refine before its preview), and the problems (a task with no upstream ID or no known path)."""
    plan, problems = [], []
    for entry in evidence.get("cleanup") or []:
        task_id, path = entry.get("task_id"), entry.get("path")
        ids = (remote_ids or {}).get(task_id)
        if not path:
            problems.append(f"{task_id}: no upstream path")
        elif not ids:
            problems.append(f"{task_id}: no upstream ID in the map")
        else:
            plan.extend((task_id, f"{path}/{urllib.parse.quote(str(remote), safe='')}") for remote in reversed(ids))
    return plan, problems


def delete_once(base, path, key):
    request = urllib.request.Request(base.rstrip("/") + path, method="DELETE")
    request.add_header("Authorization", "Bearer " + key)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.status
    except urllib.error.HTTPError as error:
        error.close()
        return error.code
    except (urllib.error.URLError, TimeoutError, OSError):
        return 0


def cleanup_main(arguments, argv, environ):
    base = environ.get("MESHY_BASE_URL", "https://api.meshy.ai")
    refusal = cleanup_refusal(base, upstream_shape.LIVE_FLAG in argv, environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    if not arguments.remote_ids:
        print("--cleanup needs --remote-ids", file=sys.stderr)
        return 2
    try:
        with open(arguments.cleanup, encoding="utf-8") as handle:
            evidence = json.load(handle)
        with open(arguments.remote_ids, encoding="utf-8") as handle:
            remote_ids = json.load(handle)
    except (OSError, ValueError) as error:
        print(f"cannot read the clean-up inputs: {type(error).__name__}", file=sys.stderr)
        return 2
    plan, problems = cleanup_plan(evidence, remote_ids)
    report = common.Report(NAME + "-cleanup", arguments.report)
    for problem in problems:
        report.failed("clean-up input", problem)
    for task_id, path in plan:
        status = delete_once(base, path, environ["MESHY_API_KEY"])
        # 200 deleted; 404 already gone; 409 still running (left alone, never retried here).
        report.check(f"DELETE {task_id}", status in (200, 204, 404), f"HTTP {status}")
    return report.finish()


def main(argv=None, environ=None):
    argv = sys.argv[1:] if argv is None else list(argv)
    environ = os.environ if environ is None else environ
    if "--cleanup" in argv or any(item.startswith("--cleanup=") for item in argv):
        arguments = engine.parse_arguments(argv, __doc__, DEFAULT_CEILING, UNIT, add_arguments)
        return cleanup_main(arguments, argv, environ)
    return engine.execute(NAME, PROVIDER, steps(), __doc__, DEFAULT_CEILING, UNIT, argv=argv, environ=environ,
                          add_arguments=add_arguments, after=add_cleanup)


if __name__ == "__main__":
    sys.exit(main())
