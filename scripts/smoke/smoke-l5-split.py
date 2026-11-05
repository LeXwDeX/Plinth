#!/usr/bin/env python3
"""L5: split generation.

Three independent chains, each in its own session. A chain without an
available Offering in the current Schema is reported SKIP with the reason.
  segment  Tripo mesh segmentation (process_model / plan_model_processing,
           Offering tripo/geometry/segment) on a freshly generated Tripo model.
  bang     Hyper3D Bang part split (hyper3d/bang/rodin-task) on a freshly
           generated Rodin model.
  image    Tripo smart segmentation of an image (tripo/geometry/smartsegment-image)
           on an image generated in the session.
Pass criterion per chain: the split task exists with the expected Offering,
is SUCCEEDED, and its required artifacts are READY. L5_SPLIT_ONLY=segment,bang,image
limits the run. Real model generations; needs ART_AI_LIVE_ACK=1.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

SEGMENT_OFFERING = "tripo/geometry/segment"
BANG_OFFERING = "hyper3d/bang/rodin-task"
IMAGE_OFFERING = "tripo/geometry/smartsegment-image"

TRIPO_MODEL = "生成一个简单的低多边形木凳 3D 模型，由座面和四条腿组成。"
SEGMENT_TURN = ("把刚才这个 3D 模型拆分成部件：用 Tripo 的网格分割服务（tripo-segment），"
                "按连通性拆分，其余参数用默认值。")
RODIN_MODEL = "生成一个简单的低多边形木凳 3D 模型，由座面和四条腿组成。"
BANG_TURN = "把刚才这个 3D 模型用 Hyper3D Bang 拆分成部件，其余参数用默认值。"
IMAGE_TURN = "对这张图做 Tripo 智能分割（tripo-smartsegment-image）生成部件，其余参数用默认值。"
IMAGE_PROMPT = "生成一张机械茶壶的产品渲染图：壶身、壶盖、壶嘴、壶把清晰分明，白色背景。"


def selected():
    value = os.environ.get("L5_SPLIT_ONLY", "")
    return {part.strip() for part in value.split(",") if part.strip()} or {"segment", "bang", "image"}


def offering_ids(live, capability):
    return {o["id"] for o in live.offerings(capability) if o.get("available")}


def model_for(live, capability, offering_id):
    return common.offering_model_ids(live.offerings(capability)).get(offering_id)


def judge_split(live, report, label, tasks, capability, offering_id):
    """The split task: right Offering, SUCCEEDED, required artifacts READY."""
    matching = [t for t in tasks if t.get("capability") == capability and t.get("offering_id") == offering_id]
    if not report.check(f"{label} split task created with {offering_id}", bool(matching),
                        str(common.describe_tasks(tasks))):
        return
    done = [t for t in matching if t.get("state") == common.SUCCESS_STATE]
    if report.check(f"{label} split task SUCCEEDED", bool(done), str([t.get("state") for t in matching])):
        live.verify_task_artifacts(done[0]["id"], f"{label} split")


def model_chain(live, report, label, base_capability, base_model, base_prompt, split_offering, split_turn):
    session = live.new_session()
    # The chain needs this provider's source model (Bang splits only a Rodin task), so pick it the way
    # a person does; otherwise the Agent may choose another model for the purpose (#103).
    live.lock_model_3d(session, base_model)
    reply, tasks, settled = live.run_turn(session, base_prompt, "model-3d", base_model, {base_capability: 1})
    if not report.check(f"{label} source model generated",
                        settled and common.succeeded_count(tasks, base_capability) >= 1,
                        str(common.describe_tasks(tasks))):
        return
    before = {t.get("id") for t in tasks}
    reply, tasks, settled = live.run_turn(session, split_turn, "model-3d", base_model, {"mesh-segmentation": 1})
    names = common.tool_names(live.messages(session))
    report.check(f"{label} agent used a processing tool",
                 any(n in ("process_model", "plan_model_processing") for n in names), str(sorted(set(names))))
    if reply is None:
        return
    new = [t for t in tasks if t.get("id") not in before]
    judge_split(live, report, label, new, "mesh-segmentation", split_offering)


def segment_chain(live, report):
    if SEGMENT_OFFERING not in offering_ids(live, "mesh-segmentation"):
        return report.skipped("segment", f"{SEGMENT_OFFERING} not available in the current Schema")
    base = model_for(live, "text-to-3d", "tripo/tripo-v3.1-full/text-to-3d")
    if not base:
        return report.skipped("segment", "Tripo text-to-3D source Offering not available")
    model_chain(live, report, "segment", "text-to-3d", base, TRIPO_MODEL, SEGMENT_OFFERING, SEGMENT_TURN)


def bang_chain(live, report):
    if BANG_OFFERING not in offering_ids(live, "mesh-segmentation"):
        return report.skipped("bang", f"{BANG_OFFERING} not available in the current Schema")
    base = model_for(live, "text-to-3d", "hyper3d/rodin-gen-2.5/text-to-3d")
    if not base:
        return report.skipped("bang", "Rodin text-to-3D source Offering not available (Bang needs a Rodin task)")
    model_chain(live, report, "bang", "text-to-3d", base, RODIN_MODEL, BANG_OFFERING, BANG_TURN)


def image_chain(live, report):
    model = model_for(live, "image-to-3d", IMAGE_OFFERING)
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    if not model:
        return report.skipped("image", f"{IMAGE_OFFERING} not available in the current Schema")
    if not image_model:
        return report.skipped("image", "no text-to-image Offering to prepare the source image")
    session = live.new_session()
    reply, tasks, settled = live.run_turn(session, IMAGE_PROMPT, "image", image_model, {"text-to-image": 1})
    if not report.check("image source generated", settled and common.succeeded_count(tasks, "text-to-image") >= 1,
                        str(common.describe_tasks(tasks))):
        return
    before = {t.get("id") for t in tasks}
    live.lock_model_3d(session, model)
    reply, tasks, settled = live.run_turn(session, IMAGE_TURN, "model-3d", model, {"image-to-3d": 1})
    if reply is None:
        return
    new = [t for t in tasks if t.get("id") not in before]
    judge_split(live, report, "image", new, "image-to-3d", IMAGE_OFFERING)


def body(live, report):
    chains = {"segment": segment_chain, "bang": bang_chain, "image": image_chain}
    wanted = selected()
    for name, chain in chains.items():
        if name in wanted:
            chain(live, report)


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-split", body, __doc__))
