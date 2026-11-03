#!/usr/bin/env python3
"""L5: 3D parameters and special requests, one run per available 3D provider.

For the first available text-to-3d Offering of every provider (read from the
Schema, no names hard-coded) the script sets Schema-declared options through the
message `parameters` field and checks the frozen task input and the delivery:
  generate_parts (multi-component) when declared, otherwise a texture switch
  (`texture` / `enable_pbr`) when declared, otherwise defaults;
  the declared multi-format list (`output_formats` / `target_formats`) is
  requested in full and every requested format must be delivered as an artifact.
Then, on the first provider's model, a format request ("export STL") must create
a format-conversion task that SUCCEEDS (only for a model that lacks STL). L5_3D_PROVIDERS=hyper3d,tripo limits the providers. Extra-credit consent flags are set
by the script where the Schema asks for them. Needs ART_AI_LIVE_ACK=1.
"""
import concurrent.futures
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

PROMPT = "生成一个简单的低多边形陶罐 3D 模型，带盖子和两个把手。"
FORMAT_REQUEST = "这个模型没有 STL 格式，请帮我转换并导出一份 STL。"
FORMAT_PARAMETERS = ("output_formats", "target_formats")


def plan_parameters(offering):
    """Parameters to send and a description, chosen only from what the Schema declares."""
    declared = {p["name"]: p for p in offering.get("parameters") or []}
    send, notes, asks = {}, [], []
    if "generate_parts" in declared:
        send["generate_parts"] = True
        notes.append("generate_parts")
        asks.append(f"「{declared['generate_parts'].get('label')}」选「分部件」")
    else:
        for name in ("texture", "enable_pbr"):
            if name in declared and declared[name].get("type") == "boolean":
                send[name] = False
                notes.append(f"{name}=false")
                asks.append(f"把「{declared[name].get('label')}」关闭")
                break
    for name in FORMAT_PARAMETERS:
        if name in declared and declared[name].get("options"):
            send[name] = [str(item["value"]) for item in declared[name]["options"]]
            notes.append(f"{len(send[name])} formats")
            asks.append(f"「{declared[name].get('label')}」选全部格式：" + "、".join(send[name]))
    if "authorize_extra_credits" in declared:
        send["authorize_extra_credits"] = True
        asks.append("同意为此产生的额外积分")
    return send, notes, asks


def provider_offerings(live):
    chosen = {}
    only = {p.strip() for p in os.environ.get("L5_3D_PROVIDERS", "").split(",") if p.strip()}
    for offering in live.offerings("text-to-3d"):
        if only and (offering.get("provider_id") or offering["id"].split("/")[0]) not in only:
            continue
        if offering.get("available") and offering.get("model_id"):
            chosen.setdefault(offering.get("provider_id") or offering["id"].split("/")[0], offering)
    return chosen


def run_provider(live, report, provider, offering):
    parameters, notes, asks = plan_parameters(offering)
    label = f"{provider} ({', '.join(notes) or 'defaults'})"
    session = live.new_session()
    prompt = PROMPT + ("请求：" + "；".join(asks) + "。" if asks else "")
    reply, tasks, settled = live.run_turn(session, prompt, "model-3d", offering["model_id"],
                                          {"text-to-3d": 1}, parameters=parameters)
    done = [t for t in tasks if t.get("capability") == "text-to-3d" and t.get("state") == common.SUCCESS_STATE]
    if not report.check(f"{label} 3D task SUCCEEDED", settled and bool(done), str(common.describe_tasks(tasks))):
        return None, set()
    task = live.task(done[0]["id"])
    frozen = (task.get("input") or {}).get("parameters") or {}
    for name in ("generate_parts", "texture", "enable_pbr"):
        if name in parameters:
            report.check(f"{provider} {name} reached the frozen task input", frozen.get(name) == parameters[name],
                         f"frozen={frozen.get(name)}")
    live.verify_task_artifacts(done[0]["id"], f"{provider} 3D")
    for name in FORMAT_PARAMETERS:
        if name in parameters:
            delivered = {str(a.get("format", "")).upper() for a in task.get("assets") or []}
            delivered |= {str(a.get("variant", "")).upper().replace("CONVERTED.", "") for a in task.get("assets") or []}
            if "GLB" in delivered:
                delivered.add("GLTF")  # glTF binary is the glTF delivery
            missing = [f for f in parameters[name] if str(f).upper() not in delivered]
            report.check(f"{provider} requested formats delivered", not missing,
                         f"missing {missing}" if missing else f"{len(parameters[name])} format(s)")
    delivered = {str(a.get("format", "")).upper() for a in task.get("assets") or []}
    delivered |= {str(a.get("variant", "")).upper().replace("CONVERTED.", "") for a in task.get("assets") or []}
    return session, delivered


def format_case(live, report, session, offering, wanted):
    before = {t.get("id") for t in live.tasks(session)}
    reply, tasks, settled = live.run_turn(session, f"这个模型没有 {wanted} 格式，请帮我转换并导出一份 {wanted}。",
                                          "model-3d", offering["model_id"], {})
    made = [t for t in tasks if t.get("id") not in before and t.get("capability") == "format-conversion"]
    if not made:
        return report.skipped(f"format request {wanted}", "agent created no conversion task (not convertible): "
                              + common.one_line((reply or {}).get("content"), 70))
    done = [t for t in made if t.get("state") == common.SUCCESS_STATE]
    if report.check(f"format request {wanted}: format-conversion SUCCEEDED", settled and bool(done),
                    str(common.describe_tasks(made))):
        live.verify_task_artifacts(done[0]["id"], "format conversion")


def body(live, report):
    providers = provider_offerings(live)
    if not providers:
        return report.failed("text-to-3d Offerings available")
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(providers)) as pool:
        futures = {name: pool.submit(run_provider, live, report, name, offering)
                   for name, offering in providers.items()}
        sessions = {name: future.result() for name, future in futures.items()}
        sessions = {name: value for name, value in sessions.items() if value and value[0]}
    # a conversion request only makes sense for a format the model does not already deliver literally
    for name in providers:
        if not sessions.get(name):
            continue
        missing = [f for f in ("GLTF", "STL") if f not in sessions[name][1]]
        if missing:
            return format_case(live, report, sessions[name][0], providers[name], missing[0])
    report.skipped("format request", "every provider already delivered the candidate formats (or none produced a model)")

if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-3d-params", body, __doc__))
