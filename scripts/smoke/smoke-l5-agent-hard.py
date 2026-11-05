#!/usr/bin/env python3
"""L5: tricky agent requests. Each case is its own session with a stated expectation.

Hard assertions only look at tool calls, tasks, approval cards and artifacts;
text is checked for a keyword at most (LLM wording varies). Cases:
  multi-step      one sentence: 2D image, then edit it, then 3D -> text-to-image,
                  image-to-image and image-to-3d tasks all SUCCEEDED.
  change-mind     "never mind, make it blue" -> a new image task SUCCEEDED whose
                  prompt mentions blue.
  ordinal         "use image 2" with two images -> one image-to-image task SUCCEEDED
                  and the tool call references exactly the session's second image.
  contradiction   conflicting size demands -> no crash; at most one image task,
                  or a clarifying reply without tasks.
  unsupported     ask for a video -> no task; the reply says it cannot.
  oversize        absurd pixel size -> at least one SUCCEEDED image, none beyond the declared maximum.
  manual-approval manual mode + 3D request -> a pending approval card, zero tasks;
                  denying it returns 2xx, leaves no pending card, the turn finishes and zero tasks exist.
  ambiguous       "convert it to 3D" with two images -> either a clarifying reply
                  with no 3D task, or exactly one 3D task SUCCEEDED; never several.
  english / mixed English and Chinese requests each yield one SUCCEEDED image task.
  oral-three-view "give me front, side and back, then model it" -> a three-view
                  group with front/left/back SUCCEEDED, then an image-to-3d task.
Needs ART_AI_LIVE_ACK=1; see README.md (L5).
"""
import concurrent.futures
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

CANNOT_WORDS = ("没有", "出不来", "只能", "不会", "不支持", "无法", "不能", "做不到", "没有这个", "暂不", "还不", "不提供", "cannot", "can't", "unable", "not support")
MAX_PIXELS_SIDE = 8192
SUCCESS = common.SUCCESS_STATE


def new_tasks(live, session, before):
    return [t for t in live.tasks(session) if t.get("id") not in before]


def ids(live, session):
    return {t.get("id") for t in live.tasks(session)}


def prompts_of(live, session, *names):
    return " ".join(str(common.tool_arguments(c).get("prompt", "")) for c in common.calls_named(live.messages(session), *names))


def two_images(live, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "同时生成两张不同的图：一张红色灯笼，一张蓝色瓷瓶。两张请并行提交。",
                                          "image", models["image"], {"text-to-image": 2})
    return session, settled and common.succeeded_count(tasks, "text-to-image") >= 2


def case_multi_step(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(
        session, "先生成一张红色小灯笼的 2D 图，再把这张图改成雪夜场景，最后把改好的图转成 3D 模型。一次做完。",
        "model-3d", models["3d"], {"text-to-image": 1, "image-to-image": 1, "image-to-3d": 1},
        domainModels={"image": models["image"], "model-3d": models["3d"]})
    states = {c: common.succeeded_count(tasks, c) for c in ("text-to-image", "image-to-image", "image-to-3d")}
    report.check("multi-step: 2D, edit and 3D all SUCCEEDED", settled and all(states.values()), str(states))


def case_change_mind(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "生成一只红色的小狐狸，简洁插画。", "image", models["image"],
                                          {"text-to-image": 1})
    if not report.check("change-mind: first image SUCCEEDED", settled and common.succeeded_count(tasks, "text-to-image") >= 1):
        return
    before = ids(live, session)
    reply, tasks, settled = live.run_turn(session, "算了，改成蓝色的。", "image", models["image"], {})
    fresh = new_tasks(live, session, before)
    ok = any(t.get("state") == SUCCESS and t.get("capability") in ("text-to-image", "image-to-image") for t in fresh)
    report.check("change-mind: a new image task SUCCEEDED", ok, str(common.describe_tasks(fresh)))
    prompt = prompts_of(live, session, "refine_image").lower()
    report.check("change-mind: prompt carries the new colour", "蓝" in prompt or "blue" in prompt)


def case_ordinal(live, report, models):
    session, ready = two_images(live, models)
    if not report.check("ordinal: two source images", ready):
        return
    before = ids(live, session)
    mark = len(live.messages(session))
    reply, _, _ = live.run_turn(session, "用第 2 张图，改成夜景版本。", "image", models["image"], {})
    fresh = [t for t in new_tasks(live, session, before) if t.get("capability") == "image-to-image"]
    report.check("ordinal: image-to-image task SUCCEEDED", any(t.get("state") == SUCCESS for t in fresh),
                 str(common.describe_tasks(new_tasks(live, session, before))) + " reply="
                 + common.one_line((reply or {}).get("content"), 70))
    messages = live.messages(session)
    handles = common.image_handles(messages[:mark])
    second = handles[1] if len(handles) >= 2 else None
    refs = [common.reference_targets(c) for c in common.calls_named(messages[mark:], "refine_image")]
    # the second picture of the session is the reference; "#2" is the same picture by index
    right = [r for r in refs if len(r) == 1 and r[0].lstrip("@") in (str(second or "").lstrip("@"), "#2")]
    report.check("ordinal: the single reference is image 2", bool(second) and bool(right) and len(right) == len(refs),
                 f"second={second} refs={refs}")


def case_contradiction(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "生成一张图：要正方形 1:1，同时又要横版 16:9，两个都要满足。",
                                          "image", models["image"], {})
    images = [t for t in live.tasks(session) if t.get("capability") in ("text-to-image", "image-to-image")]
    ok = reply is not None and common.reply_is_clean(reply) and len(images) <= 1
    report.check("contradiction: clean reply, at most one image task", ok,
                 f"{len(images)} task(s); reply={common.one_line((reply or {}).get('content'), 50)}")
    if images:
        report.check("contradiction: the task finished", images[0].get("state") in common.TERMINAL_TASK_STATES)


def case_unsupported(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "帮我生成一段 10 秒的 4K 视频，主角是一只奔跑的狐狸。", "image",
                                          models["image"], {})
    content = ((reply or {}).get("content") or "").lower()
    report.check("unsupported: no paid task was created", not live.tasks(session), str(common.describe_tasks(live.tasks(session))))
    report.check("unsupported: reply says it cannot", any(word in content for word in CANNOT_WORDS),
                 common.one_line(content, 60))


def case_oversize(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "生成一张 100000x100000 像素的超大图，一棵树。", "image",
                                          models["image"], {})
    images = [t for t in live.tasks(session) if t.get("state") == SUCCESS and t.get("capability") == "text-to-image"]
    sizes = [live.first_required_image(t["id"]) for t in images]
    report.check("oversize: a SUCCEEDED image within the declared maximum",
                 reply is not None and settled and bool(images) and all(s and max(s) <= MAX_PIXELS_SIDE for s in sizes),
                 f"settled={settled} {len(images)} image(s) {sizes}")


def case_manual_approval(live, report, models):
    import time
    session = live.new_session(approval="manual")
    status, _ = live.send(session, "生成一个简单的低多边形木凳 3D 模型。", "model-3d", models["3d"])
    if not report.check("manual-approval: message accepted", status in (200, 201, 202)):
        return
    pending = []
    deadline = time.time() + 600
    while time.time() < deadline and not pending:
        time.sleep(10)
        pending = live.pending_approvals(session)
    report.check("manual-approval: a pending approval card appears", bool(pending), f"{len(pending)} card(s)")
    report.check("manual-approval: no task before approval", not live.tasks(session))
    denied = []
    for card in pending:
        request_id = card.get("request_id") or card.get("requestId") or card.get("id")
        status, _ = live.decide_grant(request_id, approve=False)
        denied.append(status)
    report.check("manual-approval: every deny returned 2xx", bool(denied) and all(200 <= s < 300 for s in denied),
                 str(denied))
    finished = live.wait_reply(session, timeout=600)
    report.check("manual-approval: the turn finished after denying", finished is not None)
    report.check("manual-approval: no card is pending any more", not live.pending_approvals(session))
    report.check("manual-approval: denying keeps zero tasks", not live.tasks(session))


def case_ambiguous(live, report, models):
    session, ready = two_images(live, models)
    if not report.check("ambiguous: two source images", ready):
        return
    before = ids(live, session)
    reply, tasks, settled = live.run_turn(session, "把它转 3D。", "model-3d", models["3d"], {})
    threed = [t for t in new_tasks(live, session, before) if t.get("capability") == "image-to-3d"]
    asked = len(threed) == 0 and common.reply_is_clean(reply)
    chosen = len(threed) == 1 and threed[0].get("state") == SUCCESS
    report.check("ambiguous: asked first, or converted exactly one image", asked or chosen,
                 f"{len(threed)} 3D task(s), {'asked' if asked else 'converted' if chosen else 'other'}")


def case_languages(live, report, models):
    for label, prompt in (("english", "Generate a picture of a red paper lantern, simple flat illustration."),
                          ("mixed", "帮我 generate 一张 cyberpunk 风格的 street 夜景，neon 灯牌多一点。")):
        session = live.new_session()
        reply, tasks, settled = live.run_turn(session, prompt, "image", models["image"], {"text-to-image": 1})
        report.check(f"{label}: one image task SUCCEEDED", settled and common.succeeded_count(tasks, "text-to-image") == 1,
                     str(common.describe_tasks(tasks)))


def case_oral_three_view(live, report, models):
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, "生成一张Q版小熊猫角色的正面全身立绘，白色背景。", "image",
                                          models["image"], {"text-to-image": 1})
    if not report.check("oral-three-view: source image", settled and common.succeeded_count(tasks, "text-to-image") >= 1):
        return
    reply, tasks, settled = live.run_turn(session, "帮我出个正侧背然后建模。", "model-3d", models["multiview"],
                                          {"image-to-3d": 1})
    _, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/three-views")
    groups = data.get("items") or []
    states = common.slot_states(groups[-1]) if groups else {}
    group_done = all(states.get(k) == "succeeded" for k in ("front", "left", "back"))
    views_done = common.succeeded_count(tasks, "image-to-multiview") >= 1
    report.check("oral-three-view: front/side/back views SUCCEEDED (group or multiview task)", group_done or views_done,
                 f"group={states} multiview_tasks={common.succeeded_count(tasks, 'image-to-multiview')}")
    report.check("oral-three-view: 3D task SUCCEEDED", settled and common.succeeded_count(tasks, "image-to-3d") >= 1,
                 str(common.describe_tasks(tasks)))


CASES = [case_multi_step, case_change_mind, case_ordinal, case_contradiction, case_unsupported, case_oversize,
         case_manual_approval, case_ambiguous, case_languages, case_oral_three_view]


def body(live, report):
    image = common.pick_model_id(live.offerings("text-to-image"))
    models = {"image": image, "3d": common.single_image_3d_model(live), "multiview": common.multiview_3d_model(live)}
    if not all(models.values()):
        return report.failed("models available", str({k: bool(v) for k, v in models.items()}))
    only = {p.strip() for p in os.environ.get("L5_AGENT_HARD_ONLY", "").split(",") if p.strip()}
    chosen = [c for c in CASES if not only or c.__name__[5:] in only]

    def guarded(case):
        try:
            case(live, report, models)
        except Exception as error:
            report.failed(case.__name__[5:] + " crashed", f"{type(error).__name__}: {common.one_line(str(error), 100)}")

    with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
        list(pool.map(guarded, chosen))


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-agent-hard", body, __doc__))
