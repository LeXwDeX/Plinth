#!/usr/bin/env python3
"""#704 「生成纹理」开关回归，覆盖所有带开关的 3D Offering（dev-test，fake 上游，零费用；只走 API）。

Offering 现场从 /api/v1/offerings 选出：文生 3D、图生 3D、多视图 3D 里声明了 texture（Tripo）或 should_texture（Meshy）的。
每个 Offering 的期望：
  S1  开关是用户可编辑参数：presentation.surface=provider，没有 adjustment_policy / adjustment_reason。
  S2  Schema 联动：Tripo 的 texture_quality、texture_version、export_texture_format、export_texture_size 在开关关闭时
      隐藏（visible_when truthy），pbr 被 forced_when 强制为 false；Meshy 的 enable_pbr 被强制为 false
      （Meshy 多视图没有声明这条规则，只记 INFO）。
  S3  新会话里该参数的 parameter_state 是默认值 true。
  D   默认生成：SUCCEEDED，产物逐个下载、字节数与 sha256 一致，必需角色含 model；冻结输入开关为 true。
  M   消息参数路径（发消息时带 parameters={开关:false}）：冻结输入开关为 false、PBR 为 false；Tripo 报价没有 texture*
      组件且总额低于默认；必需产物角色与默认相同。
  P   保存会话参数路径（PUT /api/v1/agent/params，与面板相同）：200，读回 value=false、source=user；其他 advised 参数
      仍被 400 拒绝；之后 Agent 的生成同 M 的期望（消息里不带 parameters）。
  G   交付 GLB 的贴图数（textures）：Tripo 默认 3 张、texture=false 0 张（tripo-v3/generation-texture-off-glb，
      constructed，真实只有 PROD 文生 3D 一次观测）；Meshy texture=false 0 张（meshy-openapi-v1/image-to-3d-texture-off-glb，
      constructed）。Meshy 默认的贴图数没有真实依据，只记录 INFO 不断言。
  E   #722 面板估算（GET /api/v1/agent/params 的 native_quote，面板「默认估算」读的就是它）：原生报价的 Offering
      新会话里等于 Offering 的 default_native_quote；保存开关 false 后没有 texture* 组件、总额低于默认，并与 P 路径
      冻结任务的报价总额相同；没有原生报价的 Offering（Meshy 单图）不返回 native_quote。

用法（地址与 Agent 专用账号由 release-env.py 注入）：
  python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/texture-switch-fake.py [--offering <id> ...]
造数：每个 Offering 一个会话（auto 方式），0–2 张 fake 源图与 3 个 fake 3D 任务；全部 Offering 约 15–25 分钟。
目标不是 fake 形态或给了 --live 时退出码 2。最后一行 `=== texture-switch-fake: PASS=n FAIL=n ===`；有 FAIL 时退出码 1。
"""
import argparse
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lib import (Api, Report, check_delivery, decision_body, glb_counts, last_assistant, log, new_session,  # noqa: E402
                 pending, require_fake, set_mode, tasks, turn, wait_task)

TOGGLES = ("texture", "should_texture")
CAPABILITIES = ("text-to-3d", "image-to-3d", "multiview-to-3d")
TRIPO_HIDDEN = ("texture_quality", "texture_version", "export_texture_format", "export_texture_size")


def select(api, wanted):
    found = {}
    for capability in CAPABILITIES:
        _, body = api.json("GET", f"/api/v1/offerings?capability={capability}")
        for offering in body.get("offerings") or []:
            if any(p["name"] in TOGGLES for p in offering.get("parameters") or []):
                found[offering["id"]] = offering
    return {k: v for k, v in sorted(found.items()) if not wanted or k in wanted}


def kind(offering):
    if offering["capability"] == "text-to-3d":
        return "text"
    return "views" if "multiview" in offering["id"] else "image"


def marker(k, offering, n):
    model = offering["model_id"]
    if k == "text":
        return '[[fake:tool:create_model_3d {"prompt":"a plain cube %d","model":"%s"}]]' % (n, model)
    if k == "image":
        return '[[fake:tool:create_model_3d_from_image {"prompt":"a plain cube %d","model":"%s","reference_handles":["@img1"]}]]' % (n, model)
    return '[[fake:tool:create_model_3d_from_views {"model":"%s","reference_handles":["@img1","@img2"],"reference_views":["front","left"]}]]' % model


def drain(api, sid):
    for _ in range(5):
        cards = pending(api, sid)
        if not cards:
            return
        for card in cards:
            api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
        time.sleep(1.5)


def run3d(api, sid, offering, n, parameters=None, seconds=150):
    before = {t["id"] for t in tasks(api, sid)}
    extra = {"creative_domain": "model-3d"}
    if parameters is not None:
        extra["parameters"] = parameters
    ok, why = turn(api, sid, f"做个模型 {n} " + marker(kind(offering), offering, n), seconds=seconds, **extra)
    drain(api, sid)
    end = time.time() + seconds
    while time.time() < end:
        new = [t for t in tasks(api, sid) if t["id"] not in before and t["capability"] in CAPABILITIES]
        if new:
            return wait_task(api, new[0]["id"], seconds), ""
        time.sleep(1)
        drain(api, sid)
    return None, f"turn_ok={ok} {why}; no 3D task; reply: {last_assistant(api, sid)[:120]!r}"


def frozen(task):
    return (task.get("input") or {}).get("parameters") or {}


def quote_total(task):
    try:
        return float((task.get("native_quote") or {}).get("total_amount"))
    except (TypeError, ValueError):
        return None


def texture_components(task):
    return [c["id"] for c in (task.get("native_quote") or {}).get("components") or [] if "texture" in c["id"]]


def model_glb(api, task):
    for asset in task.get("assets") or []:
        if asset.get("role") == "model" and str(asset.get("format", "")).upper() == "GLB":
            status, raw = api.raw("GET", asset["url"])
            if status == 200:
                return glb_counts(raw)
    return None


def one(api, r, oid, offering):
    p = {x["name"]: x for x in offering["parameters"]}
    toggle = next(name for name in TOGGLES if name in p)
    spec = p[toggle]
    presentation = spec.get("presentation") or {}
    r.check(f"{oid} S1 {toggle} 可编辑（surface=provider，无 advised）",
            presentation.get("surface") == "provider" and not spec.get("adjustment_policy") and not spec.get("adjustment_reason"),
            f"surface={presentation.get('surface')} adj={spec.get('adjustment_policy')}")

    def forced_false(name):
        return any((f.get("when") or {}).get("parameter") == toggle and f.get("value") is False
                   for f in (p.get(name) or {}).get("forced_when") or [])
    if toggle == "texture":
        for name in TRIPO_HIDDEN:
            if name in p:
                hidden = any(c.get("parameter") == "texture" and c.get("operator") == "truthy"
                             for c in (p[name].get("presentation") or {}).get("visible_when") or [])
                r.check(f"{oid} S2 关闭纹理时隐藏 {name}", hidden)
        r.check(f"{oid} S2 关闭纹理时 pbr 强制 false", forced_false("pbr"))
    elif "enable_pbr" in p:
        if "multiview" in oid:
            log(f"INFO {oid} enable_pbr forced by should_texture: {forced_false('enable_pbr')} (multiview declares no rule)")
        else:
            r.check(f"{oid} S2 关闭纹理时 enable_pbr 强制 false", forced_false("enable_pbr"))

    sid = new_session(api, f"rg-704-{oid.split('/')[1]}")
    set_mode(api, sid, "auto")
    for i in range({"text": 0, "image": 1, "views": 2}[kind(offering)]):
        turn(api, sid, f"画第{i + 1}张 " + '[[fake:tool:refine_image {"prompt":"a plain cube %d on white"}]]' % i, seconds=90)
        time.sleep(2)
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{sid}/model3d", {"explicit_model_3d": oid})
    r.check(f"{oid} 锁定 3D 模型", status == 200, f"HTTP {status}")
    params_path = f"/api/v1/agent/params?scope={sid}&offering_id={oid}"
    state = (api.json("GET", params_path)[1].get("parameter_states") or {}).get(toggle) or {}
    r.check(f"{oid} S3 默认 parameter_state 为 true", state.get("value") in (True, None), str(state))
    native = offering.get("price_mode") == "native"
    default_estimate = (offering.get("default_native_quote") or {}).get("total_amount")
    opened = api.json("GET", params_path)[1].get("native_quote")
    if native:
        r.check(f"{oid} E 新会话面板估算等于默认估算", (opened or {}).get("total_amount") == default_estimate,
                f"{(opened or {}).get('total_amount')} vs {default_estimate}")
    else:
        r.check(f"{oid} E 无原生报价时不返回 native_quote", opened is None, str(opened))

    def delivered(label, task, why, textures=None):
        if not task:
            r.check(f"{oid} {label} 建出 3D 任务", False, why)
            return None
        r.check(f"{oid} {label} SUCCEEDED", task.get("state") == "SUCCEEDED", str(task.get("state")))
        count, roles, problems = check_delivery(api, task)
        r.check(f"{oid} {label} 产物字节一致且含 model", count > 0 and not problems and "model" in roles, f"assets={count} roles={roles} {problems[:2]}")
        counts = model_glb(api, task)
        log(f"INFO {oid} {label} GLB counts {counts}")
        if textures is not None:
            r.check(f"{oid} {label} G GLB 贴图数为 {textures}", bool(counts) and counts.get("textures") == textures, str(counts))
        return roles

    default, why = run3d(api, sid, offering, 1)
    default_roles = delivered("D 默认", default, why, textures=3 if oid.startswith("tripo/") else None)
    default_total = quote_total(default) if default else None
    if default:
        r.check(f"{oid} D 冻结 {toggle}=true", frozen(default).get(toggle) is True, f"{toggle}={frozen(default).get(toggle)!r}")

    def switched_off(label, task):
        f = frozen(task)
        r.check(f"{oid} {label} 冻结 {toggle}=false", f.get(toggle) is False, f"{toggle}={f.get(toggle)!r}")
        pbr = "pbr" if "pbr" in f else ("enable_pbr" if "enable_pbr" in f else None)
        if pbr and (toggle == "texture" or "multiview" not in oid):
            r.check(f"{oid} {label} {pbr} 强制 false", f.get(pbr) is False, f"{pbr}={f.get(pbr)!r}")
        if toggle == "texture":
            r.check(f"{oid} {label} 报价没有 texture* 组件", not texture_components(task), str(texture_components(task)))
            if quote_total(task) is not None and default_total is not None:
                r.check(f"{oid} {label} 报价低于默认", quote_total(task) < default_total, f"{quote_total(task)} < {default_total}")
        else:
            log(f"INFO {oid} {label} Meshy quote total={quote_total(task)} estimated={task.get('estimated_credits')}")

    message, why = run3d(api, sid, offering, 2, parameters={toggle: False})
    roles = delivered("M 消息参数", message, why, textures=0)
    if message:
        switched_off("M 消息参数", message)
        r.check(f"{oid} M 必需产物角色与默认相同", roles == default_roles, f"{roles} vs {default_roles}")

    status, _ = api.json("PUT", params_path, {"params": {toggle: False}})
    state = (api.json("GET", params_path)[1].get("parameter_states") or {}).get(toggle) or {}
    r.check(f"{oid} P 保存会话参数 200、读回 false/user", status == 200 and state.get("value") is False and state.get("source") == "user",
            f"PUT {status} {state}")
    estimate = api.json("GET", params_path)[1].get("native_quote")
    if native:
        estimate_textures = [c["id"] for c in (estimate or {}).get("components") or [] if "texture" in c["id"]]
        r.check(f"{oid} E 关纹理后面板估算没有 texture* 组件", bool(estimate) and not estimate_textures, str(estimate_textures))
        r.check(f"{oid} E 关纹理后面板估算低于默认", bool(estimate) and default_estimate is not None
                and float(estimate["total_amount"]) < float(default_estimate), f"{(estimate or {}).get('total_amount')} < {default_estimate}")
    else:
        r.check(f"{oid} E 关纹理后仍不返回 native_quote", estimate is None, str(estimate))
    advised = next((n for n, x in p.items() if x.get("adjustment_policy") == "advised"), None)
    if advised:
        value = p[advised].get("default")
        if p[advised].get("options"):
            other = next((o["value"] for o in p[advised]["options"] if o["value"] != value), None)
        elif isinstance(value, bool):
            other = not value
        elif isinstance(value, (int, float)):
            other = value + 1
        else:
            other = None
        status, _ = api.json("PUT", params_path, {"params": {advised: other}})
        r.check(f"{oid} P 其他 advised 参数 {advised} 仍返回 400", status == 400, f"HTTP {status}")
    saved, why = run3d(api, sid, offering, 3)
    roles = delivered("P 会话参数", saved, why, textures=0)
    if saved:
        switched_off("P 会话参数", saved)
        if native and estimate:
            r.check(f"{oid} E 面板估算与冻结报价一致", quote_total(saved) == float(estimate["total_amount"]),
                    f"{estimate['total_amount']} vs {quote_total(saved)}")
        r.check(f"{oid} P 必需产物角色与默认相同", roles == default_roles, f"{roles} vs {default_roles}")


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--offering", action="append", default=[], help="only this Offering id (repeatable)")
    parser.add_argument("--live", action="store_true", help=argparse.SUPPRESS)  # refused by require_fake()
    arguments = parser.parse_args()
    require_fake()
    api = Api().login()
    report = Report("texture-switch-fake")
    chosen = select(api, set(arguments.offering))
    report.check("找到带「生成纹理」开关的 3D Offering", bool(chosen), json.dumps(sorted(chosen)))
    for oid, offering in chosen.items():
        log(f"--- {oid}")
        try:
            one(api, report, oid, offering)
        except Exception as error:  # a crash is a failure, never a silent pass
            report.check(f"{oid}: script error", False, f"{type(error).__name__}: {str(error)[:160]}")
    return report.finish()


if __name__ == "__main__":
    sys.exit(main())
