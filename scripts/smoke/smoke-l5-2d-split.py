#!/usr/bin/env python3
"""L5: 2D image splitting.

The Schema declares no dedicated 2D segmentation tool: splitting a character
into part images is done with refine_image (one image-to-image task per part,
from the character image), and background-removal cuts a part out. Cases:
  parts       character image -> "split into head / body / limbs part images":
              at least three image-to-image tasks SUCCEEDED, each with a reference.
  cutout      remove_background on a part image: background-removal SUCCEEDED.
  part-to-3d  a part image -> 3D model (create_model_3d_from_image), SUCCEEDED.
Tripo smart segmentation of an image (3D parts) is covered by smoke-l5-split.py.
Needs ART_AI_LIVE_ACK=1.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

CHARACTER = "生成一张Q版机器人角色的正面全身立绘，白色背景，头部、身体、双臂和双腿清晰分明。"
PARTS = "把这个角色拆成部件图：头部、身体、双臂、双腿，每个部件单独出一张图，白色背景。"
CUTOUT = "把身体那张部件图抠掉背景，只保留部件本身。"
PART_TO_3D = "把头部那张部件图转成 3D 模型。"


def body(live, report):
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    model_3d = common.single_image_3d_model(live)
    if not (image_model and model_3d):
        return report.failed("models available", f"image={bool(image_model)} 3d={bool(model_3d)}")
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, CHARACTER, "image", image_model, {"text-to-image": 1})
    if not report.check("character image generated", settled and common.succeeded_count(tasks, "text-to-image") >= 1,
                        str(common.describe_tasks(tasks))):
        return
    before = {t.get("id") for t in tasks}
    mark = len(live.messages(session))
    reply, tasks, settled = live.run_turn(session, PARTS, "image", image_model, {"image-to-image": 3})
    parts = [t for t in tasks if t.get("id") not in before and t.get("state") == common.SUCCESS_STATE
             and t.get("capability") in ("image-to-image", "text-to-image")]
    report.check("parts: at least three part images SUCCEEDED", settled and len(parts) >= 3,
                 str(common.describe_tasks([t for t in tasks if t.get("id") not in before])))
    calls = common.calls_named(live.messages(session)[mark:], "refine_image")
    with_reference = [c for c in calls if any(k in common.tool_arguments(c)
                                              for k in ("reference_handles", "reference_image_indices"))]
    report.check("parts: part images are generated from the character reference", len(with_reference) >= 3,
                 f"{len(with_reference)}/{len(calls)} refine_image call(s) with a reference")
    for index, task in enumerate(parts[:3], 1):
        live.verify_task_artifacts(task["id"], f"part image {index}")
    before = {t.get("id") for t in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, CUTOUT, "image", image_model, {"background-removal": 1})
    cut = [t for t in tasks if t.get("id") not in before and t.get("capability") == "background-removal"]
    if report.check("cutout: background-removal SUCCEEDED", settled and any(t.get("state") == common.SUCCESS_STATE for t in cut),
                    str(common.describe_tasks(tasks[-3:]))):
        live.verify_task_artifacts(next(t for t in cut if t.get("state") == common.SUCCESS_STATE)["id"], "cutout")
    before = {t.get("id") for t in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, PART_TO_3D, "model-3d", model_3d, {"image-to-3d": 1})
    names = common.tool_names(live.messages(session))
    report.check("part-to-3d: agent used an image-to-3D tool",
                 any(n in ("create_model_3d_from_image", "create_model_3d_from_views") for n in names),
                 str(sorted(set(names))))
    done = [t for t in tasks if t.get("id") not in before and t.get("capability") == "image-to-3d"
            and t.get("state") == common.SUCCESS_STATE]
    if report.check("part-to-3d: 3D task SUCCEEDED", settled and bool(done)):
        live.verify_task_artifacts(done[0]["id"], "part 3D")


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-2d-split", body, __doc__))
