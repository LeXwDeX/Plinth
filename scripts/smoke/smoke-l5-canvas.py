#!/usr/bin/env python3
"""L5: canvas content changes through the scene API, then agent turns on the canvas.

Places two generated images as scene objects, moves and resizes one (each edit
must bump the scene revision), then asks the agent to edit from the canvas and
to turn the canvas content into 3D. The canvas turns send a
synthetic snapshot picture (solid colour, built with the standard library) as the
browser would send its flattened artboard, with the layer count and source
pictures. Checks: the canvas attachment exists, the edit does not reference an old
single-image handle, and an unchanged canvas (identical bytes uploaded again) keeps the
same snapshot handle across both turns (#53).
Needs ART_AI_LIVE_ACK=1.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

TWO_IMAGES = "同时生成两张不同的图：一张红色灯笼，一张蓝色瓷瓶。两张请并行提交。"
EDIT_FROM_BOARD = "按画板里现在的内容改图：把画板里的两张图合成一张新图，保持各自的主体。"
AGAIN_FROM_BOARD = "画板没动，再按画板内容出一版夜景风格的。"
BOARD_TO_3D = "把画板里的图转成 3D。"


def snapshot(live, session):
    status, data = live.client.call("GET", f"/api/v1/scenes/{session}/snapshot")
    return data if status == 200 else {}


def place(live, session, asset_id, x):
    return live.client.call("POST", f"/api/v1/scenes/{session}/objects", {
        "rect": {"x": x, "y": 0, "width": 512, "height": 512}, "anchor": {"x": x, "y": 0},
        "initial": {"kind": "bitmap", "asset_id": asset_id, "metadata_schema_revision": "v1"}})


def body(live, report):
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    model_3d = common.single_image_3d_model(live)
    if not (image_model and model_3d):
        return report.failed("models available")
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, TWO_IMAGES, "image", image_model, {"text-to-image": 2})
    images = [t for t in tasks if t.get("capability") == "text-to-image" and t.get("state") == common.SUCCESS_STATE]
    if not report.check("two source images generated", settled and len(images) >= 2):
        return
    objects = []
    for index, task in enumerate(images[:2]):
        asset = next((a for a in live.task(task["id"]).get("assets") or [] if a.get("required")), None)
        status, data = place(live, session, (asset or {}).get("id", ""), index * 600)
        if report.check(f"image {index + 1} placed on the canvas", status == 201, f"HTTP {status}"):
            objects.append(data)
    if len(objects) < 2:
        return
    revision = snapshot(live, session).get("revision")
    report.check("canvas holds two objects", len(snapshot(live, session).get("items") or []) == 2)
    moved = objects[0]
    status, data = live.client.call("PATCH", f"/api/v1/scenes/{session}/objects/{moved['id']}", {
        "expected_revision": moved.get("revision", 0),
        "rect": {"x": 40, "y": 40, "width": 512, "height": 512}, "anchor": {"x": 40, "y": 40}})
    after_move = snapshot(live, session).get("revision")
    report.check("move bumps the canvas revision", status == 200 and after_move != revision, f"HTTP {status}")
    status, data = live.client.call("PATCH", f"/api/v1/scenes/{session}/objects/{moved['id']}", {
        "expected_revision": (data or {}).get("revision", 0),
        "rect": {"x": 40, "y": 40, "width": 256, "height": 256}, "anchor": {"x": 40, "y": 40}})
    report.check("resize bumps the canvas revision",
                 status == 200 and snapshot(live, session).get("revision") != after_move, f"HTTP {status}")
    def canvas_turn_fields(png, sources):
        asset = live.client.upload_image(png)  # the browser uploads the flattened artboard at send time
        report.check("snapshot uploaded", bool(asset))
        return {"selection": [{"kind": "image", "assetId": asset, "canvasLayers": len(sources), "canvasSources": sources}],
                "inputSource": "canvas"}

    urls = [asset["url"] for t in images[:2] for asset in live.task(t["id"]).get("assets") or [] if asset.get("required")]
    board = common.solid_png(512, 256, (200, 60, 60))
    before = {t.get("id") for t in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, EDIT_FROM_BOARD, "image", image_model, {},
                                          **canvas_turn_fields(board, urls))
    new = [t for t in tasks if t.get("id") not in before and t.get("state") == common.SUCCESS_STATE]
    report.check("edit from the canvas: new image task SUCCEEDED", settled and bool(new),
                 str(common.describe_tasks(tasks[:3])) + " reply=" + common.one_line((reply or {}).get("content"), 60))
    first_ids = common.canvas_attachment_ids(live.messages(session))
    report.check("canvas turn carries a canvas snapshot attachment", len(first_ids) == 1 and bool(first_ids[0]),
                 f"{len(first_ids)} canvas attachment(s)")
    refs = [common.tool_arguments(c).get("reference_handles") or []
            for c in common.calls_named(live.messages(session), "refine_image")]
    used = [r for r in refs if first_ids and (first_ids[0] in r or "@selected" in r)]
    report.check("edit used the canvas snapshot as its reference, not an old single-image handle", bool(used), str(refs))
    # the board did not change: the identical snapshot is sent again and must keep its handle (#53)
    live.run_turn(session, AGAIN_FROM_BOARD, "image", image_model, {}, **canvas_turn_fields(board, urls))
    handles = common.canvas_attachment_ids(live.messages(session))
    report.check("unchanged canvas keeps one snapshot handle across turns (#53)",
                 len(handles) >= 1 and len(set(handles)) == 1, f"{len(handles)} canvas attachment(s), {len(set(handles))} distinct")
    # one layer on the board: the agent can turn it into a single 3D model
    # a board holding just that picture flattens to the picture itself
    status, one_layer = live.client.fetch(urls[0])
    if status != 200 or not one_layer:
        return report.failed("canvas to 3D: source picture readable", f"HTTP {status}")
    before = {t.get("id") for t in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, BOARD_TO_3D, "model-3d", model_3d, {},
                                          **canvas_turn_fields(one_layer, urls[:1]))
    done = [t for t in tasks if t.get("id") not in before and t.get("capability") == "image-to-3d"
            and t.get("state") == common.SUCCESS_STATE]
    if report.check("canvas to 3D: image-to-3d SUCCEEDED", settled and bool(done),
                    str(common.describe_tasks([t for t in tasks if t.get("id") not in before])) + " reply="
                    + common.one_line((reply or {}).get("content"), 70)):
        live.verify_task_artifacts(done[0]["id"], "canvas 3D")


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-canvas", body, __doc__))
