#!/usr/bin/env python3
"""L5: asymmetric character image -> three views -> fill left/back -> multi-view 3D.

Flow: generate a character whose left and right sides differ; ask for three
views; start the missing `left` and `back` slots; then send a structured
multi-view 3D turn built from the group's views. The agent must call
create_model_3d_from_views and the 3D task must SUCCEED. Every succeeded view
is saved as view-<key>.png (with the report directory) so a person can check
that `left` shows the character's left side. Needs ART_AI_LIVE_ACK=1.
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

CHARACTER = ("生成一张Q版小狐狸角色的正面全身立绘，白色背景。小狐狸的右手（它自己的右手）高举一面红色小旗，"
             "左手空着垂在身侧，左耳戴一朵黄色小花。")
THREE_VIEWS = "用刚才这张图做三视图。"
MULTIVIEW = "用这组三视图的 正面 / 左侧 / 背面 视图生成 3D 模型"
GROUP_WAIT_SECONDS = 300


def latest_group(live, session):
    _, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/three-views")
    groups = data.get("items") or []
    return groups[-1] if groups else None


def wait_group(live, session, ready, timeout):
    deadline = time.time() + timeout
    group = None
    while time.time() < deadline:
        group = latest_group(live, session)
        if group and ready(group):
            return group, True
        time.sleep(15)
    return group, False


def save_views(live, report, group, directory):
    saved = []
    if not directory:
        return saved
    for slot in group.get("slots", []):
        if slot.get("status") != "succeeded" or not slot.get("output_asset_id"):
            continue
        target = os.path.join(directory, f"view-{common.safe_name(slot['key'])}.png")
        if live.save_asset(slot["task_id"], slot["output_asset_id"], target):
            saved.append(target)
            print(f"saved view {slot['key']} -> {target}", flush=True)
    report.check("view images saved", len(saved) >= 3, f"{len(saved)} file(s)")
    return saved


def body(live, report):
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    model_3d = common.multiview_3d_model(live)
    if not (image_model and model_3d):
        report.failed("models available", f"image={bool(image_model)} multiview={bool(model_3d)}")
        return
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, CHARACTER, "image", image_model, {"text-to-image": 1})
    if not report.check("character image generated", settled and common.succeeded_count(tasks, "text-to-image") >= 1,
                        str(common.describe_tasks(tasks))):
        return
    status, _ = live.send(session, THREE_VIEWS, "image", image_model)
    if not report.check("three-view turn accepted", status in (200, 201, 202), f"HTTP {status}"):
        return
    live.wait_reply(session)
    group, found = wait_group(live, session, lambda g: bool(g.get("slots")), GROUP_WAIT_SECONDS)
    if not report.check("three-view group created", found):
        return
    run = group["run_id"]
    keys = {slot["key"] for slot in group["slots"]}
    report.check("slots include front/left/back", keys >= {"front", "left", "back"}, str(sorted(keys)))
    for slot in group["slots"]:
        if slot["key"] in ("left", "back") and slot["status"] == "not_started":
            for _ in range(30):  # 429: the per-user task limit is reached; the start was refused, so wait and ask again
                status, _ = live.client.call(
                    "POST", f"/api/v1/chat/sessions/{session}/three-views/{run}/views/{slot['key']}",
                    {"action": "start", "attempt": slot.get("attempt", 0)})
                if status != 429:
                    break
                time.sleep(20)
            report.check(f"start {slot['key']} view", status == 200, f"HTTP {status}")
    group, settled = wait_group(live, session, common.views_settled, live.arguments.task_timeout)
    states = common.slot_states(group)
    report.check("front/left/back views succeeded",
                 settled and all(states.get(key) == "succeeded" for key in ("front", "left", "back")), str(states))
    save_views(live, report, group, live.arguments.report)
    request = common.multiview_request(run, model_3d, group.get("slots", []))
    report.check("multi-view request has front/left/back",
                 {view["view"] for view in request["views"]} >= {"front", "left", "back"},
                 str([view["view"] for view in request["views"]]))
    before = {task.get("id") for task in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, MULTIVIEW, "model-3d", model_3d, {"image-to-3d": 1},
                                          multiview=request)
    if reply is None:
        return
    new = [task for task in tasks if task.get("id") not in before]
    report.check("multi-view 3D task SUCCEEDED",
                 settled and any(t.get("capability") == "image-to-3d" and t.get("state") == common.SUCCESS_STATE
                                 for t in new),
                 str(common.describe_tasks(new)) + " reply=" + common.one_line((reply or {}).get("content"), 70))
    report.check("agent called create_model_3d_from_views",
                 "create_model_3d_from_views" in common.tool_names(live.messages(session)),
                 str(sorted(set(common.tool_names(live.messages(session))))))
    done = [t for t in new if t.get("capability") == "image-to-3d" and t.get("state") == common.SUCCESS_STATE]
    report.check("the 3D task ran on the multi-view Offering",
                 bool(done) and all(common.is_multiview_task(t, model_3d) for t in done),
                 str([(t.get("offering_id"), t.get("model_id")) for t in done]))
    if done:
        live.verify_task_artifacts(done[0]["id"], "multi-view 3D")


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-three-view", body, __doc__))
