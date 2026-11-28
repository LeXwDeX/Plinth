#!/usr/bin/env python3
"""#704 「生成纹理」开关的真实上游验证：开关存成会话参数（与面板相同），消息不带 parameters（付费，live-only）。

层：prod-paid（prod 或 --live-upstream 的 dev-test）。三个场景：
  A  Tripo H3 v3.1 文生 3D，texture=false 经 PUT /api/v1/agent/params 保存；
  B  Tripo H3 v3.1 图生 3D（先出一张 2D 源图），texture=false 同样保存；
  C  Meshy 7.1 图生 3D（先出一张 2D 源图），should_texture=false 同样保存。
花费：一次 Tripo 文生 3D（v1.9.6 实测 60 Tripo 积分）、一次 Tripo 图生 3D（默认报价 100，关纹理更低）、一次 Meshy 7.1
图生 3D 与两张 2D 源图；跑前按当次官方价格与余额核对。每次运行前须在当前对话取得用户同意，并同时给出 --live 与
ART_AI_LIVE_ACK=1，否则退出码 2；目标不是真实上游形态时也拒绝。选择卡不自动回答。

期望：保存后读回 value=false、source=user；Agent 的生成冻结开关为 false；报价无纹理组件；必需产物 READY；
交付 GLB 0 texture、0 image。v1.9.6 实测只跑完 A（全部通过，报价 60、GLB 0 贴图）；B、C 未跑完，尚无结论。

用法：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke <环境> -- python3 scripts/regression/texture-live-session-param.py --live
输出：PASS/FAIL/INFO 行与 `=== texture-live-session-param: PASS=n FAIL=n SKIP=n ===`，有 FAIL 时退出码 1。
"""
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(HERE, "..", "smoke"))
import l5_common as common  # noqa: E402
from lib import glb_counts, use_agent_account_for_l5  # noqa: E402

TEXT_PROMPT = "生成一个简单的木箱 3D 白模，带盖子，不要贴图，不需要任何后续处理。"
PICTURE_PROMPT = "画一个简单的木箱，白色背景，正面视角。"
IMAGE_PROMPT = "把刚才这张木箱图生成 3D 白模，不要贴图，不需要任何后续处理。"


def save_param(live, session, offering, name, value):
    path = f"/api/v1/agent/params?scope={session}&offering_id={offering['id']}"
    status, _ = live.client.call("PUT", path, {"params": {name: value}})
    _, data = live.client.call("GET", path)
    return status, ((data or {}).get("parameter_states") or {}).get(name) or {}


def verify(live, report, label, task_id, switch):
    task = live.task(task_id)
    frozen = (task.get("input") or {}).get("parameters") or {}
    quote = task.get("native_quote") or {}
    components = [(c.get("id"), c.get("amount")) for c in quote.get("components") or []]
    print(f"INFO: [{label}] frozen {switch}={frozen.get(switch)} pbr={frozen.get('pbr', frozen.get('enable_pbr'))}; "
          f"quote total={quote.get('total_amount')} {quote.get('unit_label')} components={components}", flush=True)
    report.check(f"[{label}] {switch}=false reached the frozen task input", frozen.get(switch) is False, f"frozen={frozen.get(switch)}")
    report.check(f"[{label}] the quote has no texture component", not any("texture" in str(c[0]) for c in components), str(components))
    live.verify_task_artifacts(task_id, f"[{label}] 3D")
    glb = [a for a in task.get("assets") or [] if str(a.get("format", "")).lower() == "glb"]
    if not glb:
        return report.failed(f"[{label}] a GLB asset in the delivery", "no glb asset")
    destination = os.path.join(tempfile.mkdtemp(prefix="plinth-texture-live-"), "model.glb")
    live.save_asset(task_id, glb[0].get("id") or glb[0].get("asset_id"), destination)
    with open(destination, "rb") as handle:
        counts = glb_counts(handle.read())
    os.remove(destination)
    report.check(f"[{label}] GLB has no textures", counts.get("textures", 1) == 0 and counts.get("images", 1) == 0, str(counts))


def case(live, report, label, capability, offering, switch, picture_first):
    session = live.new_session()
    live.lock_model_3d(session, offering["model_id"])
    live.set_brief(session, {"purpose": "render"})
    status, state = save_param(live, session, offering, switch, False)
    report.check(f"[{label}] {switch}=false saved as a session param (PUT 200, source user)",
                 status == 200 and state.get("value") is False and state.get("source") == "user", f"PUT {status} state={state}")
    prompt = TEXT_PROMPT
    if picture_first:
        model2d = common.pick_model_id(live.offerings("text-to-image"))
        _, tasks, settled = live.run_turn(session, PICTURE_PROMPT, "image", model2d, {"text-to-image": 1})
        if not report.check(f"[{label}] a 2D picture to start from", settled and any(
                t.get("capability") == "text-to-image" and t.get("state") == common.SUCCESS_STATE for t in tasks),
                str(common.describe_tasks(tasks))):
            return
        prompt = IMAGE_PROMPT
    _, tasks, settled = live.run_turn(session, prompt, "model-3d", offering["model_id"], {capability: 1})
    done = [t for t in tasks if t.get("capability") == capability and t.get("state") == common.SUCCESS_STATE]
    if report.check(f"[{label}] {capability} SUCCEEDED", settled and bool(done), str(common.describe_tasks(tasks))):
        verify(live, report, label, done[0]["id"], switch)


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False  # never approve extra steps on the person's behalf
    text = {o.get("id"): o for o in live.offerings("text-to-3d")}
    image = {o.get("id"): o for o in live.offerings("image-to-3d")}
    plan = (("A tripo text, saved param", "text-to-3d", text.get("tripo/tripo-v3.1-full/text-to-3d"), "texture", False),
            ("B tripo image, saved param", "image-to-3d", image.get("tripo/tripo-v3.1-full/image-to-3d"), "texture", True),
            ("C meshy-7.1 image, saved param", "image-to-3d", image.get("meshy/meshy-7.1/image-to-3d"), "should_texture", True))
    for label, capability, offering, switch, picture_first in plan:
        if offering is None:
            report.failed(f"[{label}] Offering is available")
            continue
        case(live, report, label, capability, offering, switch, picture_first)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("texture-live-session-param", body, __doc__))
