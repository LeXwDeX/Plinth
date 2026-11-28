#!/usr/bin/env python3
"""#704 「生成纹理」开关的真实上游验证：Tripo H3 v3.1 文生 3D 关纹理一次、默认一次（付费，live-only）。

层：prod-paid（prod 或 --live-upstream 的 dev-test）。花费：两次 Tripo 文生 3D，v1.9.6 实测报价
texture=false 为 60 Tripo 积分、默认 90 Tripo 积分；跑前按当次官方价格与余额核对。每次运行前须在当前对话
取得用户同意（AGENTS.md「上游接口与 Fake」），并同时给出 --live 与 ART_AI_LIVE_ACK=1，否则退出码 2；
目标不是真实上游形态（ART_AI_UPSTREAM_SHAPE!=live）时也拒绝。选择卡不自动回答（自动回答曾替人批准额外的 retexture）。

期望（v1.9.6 正式测试环境实测）：
  - texture 是用户可编辑参数（surface 不是 readonly，没有 adjustment_policy）；
  - texture=false：冻结输入 texture=false、pbr=false；报价无纹理组件（detailed_geometry 20 + format_conversions 30，
    总额 60）；必需产物 READY；交付 GLB 0 texture、0 image、1 material；
  - 默认：报价 90（多 extreme_texture 20 与 standard_texture 10）；交付 GLB 有贴图（实测 3 texture、3 image）。

用法（Agent 专用账号经 AGENT_TEST_* 注入；脚本把它交给 scripts/smoke/l5_common 登录）：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke <环境> -- python3 scripts/regression/texture-live-tripo.py --live
输出与 L5 脚本相同：PASS/FAIL/INFO 行与 `=== texture-live-tripo: PASS=n FAIL=n SKIP=n ===`，有 FAIL 时退出码 1。
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(HERE, "..", "smoke"))
import l5_common as common  # noqa: E402
from lib import glb_counts, use_agent_account_for_l5  # noqa: E402

PROMPT = "生成一个简单的木箱 3D 模型，带盖子。"
OFFERING_ID = "tripo/tripo-v3.1-full/text-to-3d"


def run_case(live, report, offering, texture_off):
    label = "texture=false" if texture_off else "default (texture=true)"
    session = live.new_session()
    live.lock_model_3d(session, offering["model_id"])
    live.set_brief(session, {"purpose": "render"})
    parameters = {"texture": False} if texture_off else {}
    reply, tasks, settled = live.run_turn(session, PROMPT, "model-3d", offering["model_id"], {"text-to-3d": 1},
                                          parameters=parameters)
    done = [t for t in tasks if t.get("capability") == "text-to-3d" and t.get("state") == common.SUCCESS_STATE]
    if not report.check(f"[{label}] 3D task SUCCEEDED", settled and bool(done), str(common.describe_tasks(tasks))):
        return
    task = live.task(done[0]["id"])
    frozen = (task.get("input") or {}).get("parameters") or {}
    quote = task.get("native_quote") or {}
    components = [(c.get("id"), c.get("amount")) for c in quote.get("components") or []]
    print(f"INFO: [{label}] frozen texture={frozen.get('texture')} pbr={frozen.get('pbr')}; "
          f"quote total={quote.get('total_amount')} {quote.get('unit_label')} components={components}", flush=True)
    if texture_off:
        report.check(f"[{label}] texture=false reached the frozen task input", frozen.get("texture") is False, f"frozen={frozen.get('texture')}")
        report.check(f"[{label}] pbr forced to false", frozen.get("pbr") in (False, None), f"frozen={frozen.get('pbr')}")
        report.check(f"[{label}] quote has no texture component", not any("texture" in str(c[0]) for c in components), str(components))
    live.verify_task_artifacts(done[0]["id"], f"[{label}] 3D")
    glb = [a for a in task.get("assets") or [] if str(a.get("format", "")).lower() == "glb"]
    if not glb:
        return report.failed(f"[{label}] a GLB asset in the delivery", "no glb asset")
    destination = os.path.join(tempfile.mkdtemp(prefix="plinth-texture-live-"), "model.glb")
    live.save_asset(done[0]["id"], glb[0].get("id") or glb[0].get("asset_id"), destination)
    with open(destination, "rb") as handle:
        counts = glb_counts(handle.read())
    os.remove(destination)
    print(f"INFO: [{label}] GLB counts {counts}", flush=True)
    if texture_off:
        report.check(f"[{label}] GLB has no textures", counts.get("textures", 1) == 0 and counts.get("images", 1) == 0, str(counts))
    else:
        report.check(f"[{label}] GLB has textures (control)", counts.get("textures", 0) > 0, str(counts))


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False  # never approve extra steps on the person's behalf
    offering = next((o for o in live.offerings("text-to-3d") if o.get("id") == OFFERING_ID), None)
    if offering is None:
        return report.failed("the Tripo H3 v3.1 text-to-3D Offering is available")
    texture = {p.get("name"): p for p in offering.get("parameters") or []}.get("texture") or {}
    report.check("texture is a user-editable parameter (not readonly, no advised policy)",
                 (texture.get("presentation") or {}).get("surface") != "readonly" and not texture.get("adjustment_policy"),
                 json.dumps({k: texture.get(k) for k in ("presentation", "adjustment_policy")}, ensure_ascii=False)[:160])
    run_case(live, report, offering, True)
    run_case(live, report, offering, False)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("texture-live-tripo", body, __doc__))
