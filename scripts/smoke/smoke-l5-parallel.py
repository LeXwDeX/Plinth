#!/usr/bin/env python3
"""L5: one turn that asks for two images in parallel; both tasks must SUCCEED.

Waits until two text-to-image tasks exist before judging, then checks required
artifacts and whether the two tasks overlapped in time. Two small image
generations. Needs ART_AI_LIVE_ACK=1; see README.md (L5).
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

PROMPT = "同时生成两张不同的图：一张红色灯笼，一张蓝色瓷瓶。两张请并行提交。"


def overlapped(tasks):
    spans = sorted((t.get("created_at") or "", t.get("finished_at") or "") for t in tasks)
    return len(spans) >= 2 and spans[1][0] < spans[0][1]


def body(live, report):
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    if not image_model:
        report.failed("text-to-image model available")
        return
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, PROMPT, "image", image_model, {"text-to-image": 2})
    if reply is None:
        return
    images = [t for t in tasks if t.get("capability") == "text-to-image"]
    report.check("two image tasks created and finished", settled and len(images) >= 2, str(common.describe_tasks(tasks)))
    succeeded = [t for t in images if t.get("state") == common.SUCCESS_STATE]
    report.check("both image tasks SUCCEEDED", len(succeeded) >= 2, f"{len(succeeded)}/{len(images)}")
    for index, task in enumerate(succeeded[:2], 1):
        live.verify_task_artifacts(task["id"], f"image {index}")
    report.passed("submission overlap", "overlapping") if overlapped(images) else \
        report.skipped("submission overlap", "tasks did not overlap in time (informational)")


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-parallel", body, __doc__))
