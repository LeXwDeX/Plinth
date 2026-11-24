#!/usr/bin/env python3
"""L5: every Hyper3D Rodin API function once, through Plinth's public task API (#539). Default target: the fake.

Chain (one request per step, fixed Offerings, prepare -> grants -> tasks):

  text       Rodin Gen-2.5 text-to-3D, Quad, 10000 faces, texture_mode high, TAPose, quad_normal, seed 1
  > bang_task      Bang on the text task (the Adapter sends its Rodin uuid as asset_id)
  > bang_upload    Bang on the text task's own GLB uploaded as a model file, with a reference picture
  > texture        Texture of the same uploaded GLB with the reference picture (skipped with --no-texture)
  image_short  image-to-3D, two pictures, image_label ["F"] (fewer marks than pictures), independent

The GLB the text step delivers is downloaded and uploaded again for the Bang upload mode and for Texture; if it is
larger than the Offering's upload limit (10 MiB) those two steps are skipped and say so (a real model with the 2K
texture mode is expected to fit; the 6K display request is not). The audit's list for the whole chain is 3.5
credits; this product path costs a little more because Texture here is the default 2-credit request:

  text 1.0 + bang_task 0.5 + bang_upload 0.5 + texture 2.0 + image_short 1.0 = about 5.0 Hyper3D credits.

The default ceiling --max-credits is 7: a step whose frozen quote would cross it, and every later step, is not sent.
The unit price of a Rodin credit is not listed by Hyper3D. The account needs Full API access (a Basic plan answers
API_NO_ACTIVE_SUBSCRIPTION).

Not covered here (the direct align Profile covers them, see scripts/smoke/README.md): check_balance with usage_page, the
refused-value probe (seed 65536), the texture request with the official `images` field, Bang with asset_id and model
together. Those talk to Rodin directly and are run by fake-upstream-align after the user agreed.

Gate (AGENTS.md "上游接口与 Fake"): a fake-shaped target runs with no switch and refuses --live; any other
target needs --live AND ART_AI_LIVE_ACK=1 or the script exits 2 before a request. The shape comes from
ART_AI_UPSTREAM_SHAPE (release-env.py smoke sets it). Each real run also needs the user's consent first.

  python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-hyper3d.py
  ART_AI_UPSTREAM_SHAPE=fake BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... python3 scripts/smoke/smoke-l5-hyper3d.py
  python3 scripts/release-env.py smoke dev-test -- env ART_AI_LIVE_ACK=1 python3 scripts/smoke/smoke-l5-hyper3d.py --live --report DIR

Every POST is sent once; a failure or timeout is only observed. The server resends a Rodin submission only when
Rodin answered 503 with a Retry-After (the connection's retry_submit_server_error, narrowed by #563), which means the
job was not taken; once those resends run out, and for every other 5xx or a timeout, the step ends as "submission
result unknown" and may have been billed, which the balance reconciliation at the end would show. The script cannot
turn the resend off. Re-run a failed step with --only STEP
--seed SOURCE=TASK_ID (a new request, after reading why). Raw payloads and downloaded bytes live in a 0700 directory
outside the checkout and are removed at the end (--keep-raw keeps them). Rodin keeps results for about seven days.

Exit 0 = every check passed, 1 = a check failed, 2 = refused or misused. It never prints credentials, cookies,
signed URLs or full prompts.
"""
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_endpoints as engine  # noqa: E402

NAME = "smoke-l5-hyper3d"
PROVIDER = "hyper3d"
UNIT = "Hyper3D credits"
DEFAULT_CEILING = 7

TEXT_TO_3D = "hyper3d/rodin-gen-2.5/text-to-3d"
IMAGE_TO_3D = "hyper3d/rodin-gen-2.5/image-to-3d"
BANG_TASK = "hyper3d/bang/rodin-task"
BANG_LOCAL = "hyper3d/bang/local-model"
TEXTURE = "hyper3d/texture/local-model"

CHARACTER = ("A game character standing upright in an A pose, simple low-poly armor, plain neutral colours, "
             "plain light background, no text")
TEXTURE_PROMPT = "plain matte red"
FACES = 10000
# Rodin Quad returns about 2.18 triangles per requested face (the 2026-11-13 real run, kept in the fake's sized model);
# the 1.9 real round (2026-11-24, #568) got 21002 for 10000 faces (2.10), inside the window below.
TRIANGLES_PER_FACE = 2.18
TRIANGLE_TOLERANCE = 0.35
DEFAULT_UPLOAD_LIMIT = 10 << 20


# ------------------------------------------------------------------ verification

def verify_text(ctx, task):
    path = ctx.file("text", "model", "glb")
    summary = engine.glb_summary(path) if path else None
    if not summary:
        return [("GLB readable", False, "no readable GLB 2.0")]
    low, high = engine.triangle_window(FACES, TRIANGLES_PER_FACE, TRIANGLE_TOLERANCE)
    return [("GLB readable", True, f"{summary['meshes']} mesh(es), textures {summary['texture_edges']}"),
            ("GLB triangles follow the request", low <= summary["triangles"] <= high,
             f"{summary['triangles']} triangles for {FACES} Quad faces (expected {low}..{high})"),
            ("GLB textured", summary["materials"] > 0 and summary["images"] > 0,
             f"{summary['materials']} material(s), {summary['images']} image(s)")]


def verify_bang(name):
    def verify(ctx, task):
        path = ctx.file(name, "model", "glb")
        summary = engine.glb_summary(path) if path else None
        if not summary:
            return [("GLB readable", False, "no readable GLB 2.0")]
        parts = summary["meshes"]
        # The segmented model carries its parts as separate meshes; the fake's fixture is one mesh, so the count is
        # enforced against a real upstream only.
        return [("GLB readable", True, f"{parts} mesh(es)"),
                ("GLB in parts", parts >= 2 or ctx.fake, f"{parts} mesh(es)" + ("" if parts >= 2 else " (recorded; enforced on a real upstream only)"))]
    return verify


def verify_texture(ctx, task):
    path = ctx.file("texture", "model", "glb")
    summary = engine.glb_summary(path) if path else None
    if not summary:
        return [("GLB readable", False, "no readable GLB 2.0")]
    return [("GLB textured", summary["materials"] > 0 and summary["images"] > 0,
             f"{summary['materials']} material(s), {summary['images']} image(s), edges {summary['texture_edges']}")]


def verify_image(ctx, task):
    path = ctx.file("image_short", "model", "glb")
    summary = engine.glb_summary(path) if path else None
    return [("GLB readable", summary is not None, "GLB 2.0 read from its own bytes" if summary else "no readable GLB")]


# ------------------------------------------------------------------ inputs

def uploaded(ctx, key, filename, data, media_type):
    """Upload once and remember the asset ID; None (and a skip reason) when the library refuses it."""
    if key not in ctx.uploads:
        ctx.uploads[key] = ctx.api.upload(filename, data, media_type)
    if not ctx.uploads[key]:
        ctx.skip_reason = f"the library refused the {filename} upload"
    return ctx.uploads[key]


def model_upload(ctx, offering_id):
    """The text step's own GLB as a model file, or None with a skip reason when it exceeds the Offering's limit."""
    path = ctx.file("text", "model", "glb")
    if not path:
        ctx.skip_reason = "the text step delivered no GLB to upload"
        return None
    offering = ctx.offerings.get(offering_id)
    limit = engine.parameter_limit(offering, "source_asset", DEFAULT_UPLOAD_LIMIT)
    size = os.path.getsize(path)
    if size > limit:
        ctx.skip_reason = f"the text GLB is {size} bytes, over the {limit}-byte upload limit"
        return None
    with open(path, "rb") as handle:
        return uploaded(ctx, "model", "model.glb", handle.read(), "model/gltf-binary")


def reference_picture(ctx):
    return uploaded(ctx, "picture", "reference.png", engine.gradient_png(96, 96), "image/png")


def build_text(ctx):
    return {"prompt": CHARACTER, "mesh_mode": "Quad", "quality_override": FACES, "texture_mode": "high",
            "TAPose": True, "quad_normal": True, "seed": 1}


def build_bang_task(ctx):
    return {"source_task": ctx.task_id("text")}


def build_bang_upload(ctx):
    model, picture = model_upload(ctx, BANG_LOCAL), None
    if model:
        picture = reference_picture(ctx)
    return {"source_asset": model, "reference_image": picture, "material": "PBR"} if model and picture else None


def build_texture(ctx):
    model, picture = model_upload(ctx, TEXTURE), None
    if model:
        picture = reference_picture(ctx)
    return {"source_asset": model, "reference_image": picture, "prompt": TEXTURE_PROMPT} if model and picture else None


def build_image_short(ctx):
    first = uploaded(ctx, "view_front", "front.png", engine.gradient_png(96, 96, (200, 90, 60)), "image/png")
    second = uploaded(ctx, "view_side", "side.png", engine.gradient_png(96, 96, (60, 120, 200)), "image/png")
    if not first or not second:
        return None
    return {"reference_assets": [first, second], "reference_image_labels": ["F"], "mesh_mode": "Raw",
            "texture_mode": "high"}


def texture_enabled(ctx):
    ctx.skip_reason = "--no-texture"
    return not ctx.arguments.no_texture


def steps():
    return [
        engine.Step("text", TEXT_TO_3D, "text-to-3d", build_text, verify=verify_text),
        engine.Step("bang_task", BANG_TASK, "mesh-segmentation", build_bang_task, needs=("text",),
                    verify=verify_bang("bang_task")),
        engine.Step("bang_upload", BANG_LOCAL, "mesh-segmentation", build_bang_upload, needs=("text",), optional=True,
                    verify=verify_bang("bang_upload")),
        engine.Step("texture", TEXTURE, "retexture", build_texture, needs=("text",), optional=True,
                    only_if=texture_enabled, verify=verify_texture),
        engine.Step("image_short", IMAGE_TO_3D, "image-to-3d", build_image_short, verify=verify_image),
    ]


def add_arguments(parser):
    parser.add_argument("--no-texture", action="store_true",
                        help="skip the Texture step (the default 2-credit request)")


def main(argv=None, environ=None):
    return engine.execute(NAME, PROVIDER, steps(), __doc__, DEFAULT_CEILING, UNIT, argv=argv, environ=environ,
                          add_arguments=add_arguments)


if __name__ == "__main__":
    sys.exit(main())
