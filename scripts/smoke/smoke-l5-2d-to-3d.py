#!/usr/bin/env python3
"""L5: fresh session, one turn that asks for a 2D concept image and then a 3D model.

Checks: the title is generated while the first turn is still running; the turn
produces a text-to-image task and an image-to-3d task, both SUCCEEDED with
their required artifacts READY. Waits for both capabilities to appear before
judging. Needs ART_AI_LIVE_ACK=1; see README.md (L5).
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

PROMPT = "帮我做一个仙侠山林大树场景：先生成一张2D概念图，然后直接把这张图转成3D模型。"
TITLE_WAIT_SECONDS = 120


def body(live, report):
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    model_3d = common.single_image_3d_model(live)
    if not (image_model and model_3d):
        report.failed("models available", f"image={bool(image_model)} 3d={bool(model_3d)}")
        return
    session = live.new_session()
    status, _ = live.send(session, PROMPT, "model-3d", model_3d,
                          domainModels={"image": image_model, "model-3d": model_3d})
    if not report.check("first message accepted", status in (200, 201, 202), f"HTTP {status}"):
        return
    deadline = time.time() + TITLE_WAIT_SECONDS
    title, running = "", False
    while time.time() < deadline:
        time.sleep(4)
        data = live.session(session) or {}
        title = data.get("title") or ""
        running = not common.reply_is_final(data, live.messages(session))
        if common.title_generated(title):
            break
    report.check("title generated while the first turn runs", common.title_generated(title) and running,
                 f"title={'set' if common.title_generated(title) else 'default'} turn_running={running}")
    reply = live.wait_reply(session)
    if not report.check("final reply", reply is not None):
        return
    tasks, settled = live.wait_tasks(session, {"text-to-image": 1, "image-to-3d": 1})
    report.check("2D and 3D tasks created and finished", settled, str(common.describe_tasks(tasks)))
    for capability in ("text-to-image", "image-to-3d"):
        done = [t for t in tasks if t.get("capability") == capability and t.get("state") == common.SUCCESS_STATE]
        if report.check(f"{capability} SUCCEEDED", bool(done)):
            live.verify_task_artifacts(done[0]["id"], capability)


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-2d-to-3d", body, __doc__))
