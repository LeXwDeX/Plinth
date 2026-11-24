"""Shared logic for the Agent output-quality evaluation (#238).

The fixed request set and the visual rubric are data files under
`scripts/smoke/agent-quality/`. Two runners use this module:

  * the fake-stack trajectory runner (free, deterministic; part of
    `scripts/smoke/smoke-fake-stack.py --only agent-quality`), and
  * the live output runner (`scripts/smoke/eval-agent-output.py`), which runs
    only after the person approves the paid upstream requests.

Everything that decides a result is a plain function so
`scripts/tests/test_agent_quality.py` can cover it without a server.
"""

import decimal
import hashlib
import json
import os
import time

import l5_common as common

REQUESTS_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "agent-quality", "requests.json")
RUBRIC_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "agent-quality", "rubric.json")
BASELINES_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "agent-quality", "baselines")

SUCCESS = common.SUCCESS_STATE
FAILED_STATES = common.FAILED_STATES
CHECK_PASS, CHECK_FAIL, CHECK_SKIP = "pass", "fail", "skip"
# #498: a fact that is recorded for calibration but never decides PASS/FAIL.
# The request set's budget thresholds are the case: a healthy live run of the
# fixed set exceeds them (13/13 over at version 5), so counting them as failures
# drowns the real regressions the report exists to show. The value stays in the
# check detail, where it is still readable.
CHECK_INFO = "info"

# #444: the #407 baseline took Meshy's 3MF delivery for the model and the GLB
# inspector refused it, so its output:model verdicts say nothing about the
# delivered model. A baseline check whose detail carries one of these markers is
# listed as incomparable instead of a regression, because the candidate that
# inspects the real GLB cannot be compared against a broken download.
UNCOMPARABLE_DETAIL_MARKERS = ("bad GLB magic",)

# Project steps set up the #240 fixture between tool steps; they are not tool
# calls and never count against the tool-call budget.
PROJECT_SETUP_ACTIONS = ("promote_first_image", "new_session")


# ---------------------------------------------------------------- request set

def file_sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def load_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def validate_request_set(data, purposes=None):
    """Problems that would make a report incomparable or a run meaningless.

    purposes optionally maps a modality to the purpose ids the Product Schema
    declares ("2d" from chat.image2d_usage.profiles, "3d" from
    chat.model3d_usage.profiles); a request whose purpose is outside that set
    is refused.
    """
    problems = []
    if not isinstance(data, dict):
        return ["the request set must be a JSON object"]
    if not isinstance(data.get("version"), int) or data["version"] <= 0:
        problems.append("version must be a positive integer")
    requests = data.get("requests")
    if not isinstance(requests, list) or not requests:
        return problems + ["requests must be a non-empty list"]
    seen = set()
    for index, request in enumerate(requests):
        label = f"requests[{index}]"
        if not isinstance(request, dict):
            problems.append(f"{label} must be an object")
            continue
        for field in ("id", "title", "modality", "prompt", "deliverables", "expected"):
            if not request.get(field):
                problems.append(f"{label}: missing {field}")
        request_id = request.get("id")
        if request_id in seen:
            problems.append(f"duplicate request id {request_id}")
        seen.add(request_id)
        if request.get("modality") not in ("2d", "3d"):
            problems.append(f"{request_id}: modality must be 2d or 3d")
        if request.get("modality") == "3d" and not request.get("purpose"):
            problems.append(f"{request_id}: a 3D request needs a purpose")
        references = request.get("reference_images")
        if references is not None and not isinstance(references, list):
            problems.append(f"{request_id}: reference_images must be a list")
        for reference in references or []:
            if not isinstance(reference, str) and not (isinstance(reference, dict) and reference.get("filename")):
                problems.append(f"{request_id}: every reference image needs a filename")
        if purposes is not None and request.get("purpose"):
            allowed = purposes.get(request.get("modality")) or set()
            if request["purpose"] not in allowed:
                problems.append(
                    f"{request_id}: purpose {request['purpose']} is not declared for {request.get('modality')} work")
        project = request.get("project")
        if project is not None:
            if not isinstance(project, dict) or not str(project.get("name") or "").strip():
                problems.append(f"{request_id}: project needs a name")
            elif project.get("conventions") is not None and not isinstance(project["conventions"], dict):
                problems.append(f"{request_id}: project.conventions must be an object")
            elif project.get("style_guide") is not None and not isinstance(project["style_guide"], dict):
                problems.append(f"{request_id}: project.style_guide must be an object")
        steps = request.get("steps") or []
        if not isinstance(steps, list) or not steps:
            problems.append(f"{request_id}: steps must be a non-empty list")
        else:
            for step_index, step in enumerate(steps):
                if not isinstance(step, dict) or not step.get("user"):
                    problems.append(f"{request_id}.steps[{step_index}]: missing user or tool.name")
                    continue
                tool = step.get("tool") or {}
                tools = step.get("tools")
                setup = step.get("project_setup")
                if tools is not None:
                    if setup is not None or tool.get("name"):
                        problems.append(f"{request_id}.steps[{step_index}]: a step is tools[], a tool or a project_setup, not more")
                    elif not isinstance(tools, list) or not tools or any(
                            not isinstance(item, dict) or not item.get("name") for item in tools):
                        problems.append(f"{request_id}.steps[{step_index}]: tools[] needs a name on every call")
                elif setup is not None and setup not in PROJECT_SETUP_ACTIONS:
                    problems.append(f"{request_id}.steps[{step_index}]: unknown project_setup {setup}")
                elif setup is not None and tool.get("name"):
                    problems.append(f"{request_id}.steps[{step_index}]: a step is a tool or a project_setup, not both")
                elif setup is None and not tool.get("name") and not step.get("feedback"):
                    problems.append(f"{request_id}.steps[{step_index}]: missing user or tool.name")
                elif setup == "promote_first_image":
                    asset = step.get("asset")
                    if asset is not None and (not isinstance(asset, dict) or not str(asset.get("name") or "").strip()):
                        problems.append(f"{request_id}.steps[{step_index}]: a promoted asset needs a name")
                feedback = step.get("feedback")
                if feedback is not None:
                    if not isinstance(feedback, list) or not feedback:
                        problems.append(f"{request_id}.steps[{step_index}]: feedback must be a non-empty list")
                    else:
                        for action_index, action in enumerate(feedback):
                            if not isinstance(action, dict) or action.get("action") not in PREFERENCE_ACTIONS:
                                problems.append(f"{request_id}.steps[{step_index}].feedback[{action_index}]: unknown action")
                                continue
                            if action["action"] in ("pick", "reject"):
                                index = action.get("task")
                                prompt = action.get("prompt")
                                if (not isinstance(index, int) or index < 0) and not (isinstance(prompt, str) and prompt):
                                    problems.append(f"{request_id}.steps[{step_index}].feedback[{action_index}]: needs a task index or prompt")
                            if action["action"] == "reject" and action.get("reasonPreset") is not None and not isinstance(action.get("reasonPreset"), str):
                                problems.append(f"{request_id}.steps[{step_index}].feedback[{action_index}]: reasonPreset must be a string")
                            if action["action"] == "edit" and not any(key in action for key in ("direction", "reasonPreset", "reasonText", "active")):
                                problems.append(f"{request_id}.steps[{step_index}].feedback[{action_index}]: edit changes nothing")
        expected = request.get("expected") or {}
        if not expected.get("capabilities"):
            problems.append(f"{request_id}: expected.capabilities is empty")
        for capability, count in (expected.get("min_tasks") or {}).items():
            if capability not in (expected.get("capabilities") or []) or not isinstance(count, int) or count < 1:
                problems.append(f"{request_id}: min_tasks.{capability} must be a positive count of a declared capability")
        for name, count in (expected.get("min_tool_counts") or {}).items():
            if not isinstance(name, str) or not name.strip() or not isinstance(count, int) or count < 1:
                problems.append(f"{request_id}: min_tool_counts.{name} must be a positive count of a named tool")
        for group in expected.get("required_tool_groups") or []:
            if not group.get("id") or not group.get("any"):
                problems.append(f"{request_id}: a required_tool_group needs id and any")
        budgets = expected.get("budgets") or {}
        for name in ("max_paid_calls", "max_tool_calls", "max_interruptions"):
            value = budgets.get(name)
            if not isinstance(value, int) or value < 0:
                problems.append(f"{request_id}: budgets.{name} must be a non-negative integer")
        preference_checks = expected.get("preferences")
        if preference_checks is not None:
            if not isinstance(preference_checks, dict):
                problems.append(f"{request_id}: expected.preferences must be an object")
            else:
                for name, value in preference_checks.items():
                    if name not in PREFERENCE_CHECKS or not isinstance(value, bool):
                        problems.append(f"{request_id}: expected.preferences.{name} is not a boolean check")
    return problems


# #241: the feedback actions a trajectory step may perform between turns.
PREFERENCE_ACTIONS = ("pick", "reject", "compact", "edit", "delete")
PREFERENCE_CHECKS = ("captured", "injected", "after_compaction", "edited", "deleted")


def load_requests(path=REQUESTS_PATH):
    data = load_json(path)
    problems = validate_request_set(data)
    if problems:
        raise ValueError("; ".join(problems))
    return data, path, file_sha256(path)


def find_baseline(mode, request_set_version=None, directory=BASELINES_PATH, kind="agent-quality"):
    """The stored report this run can be compared with, or the reason there is none.

    #498: the output baselines were never recorded from a live run (all nine
    stored reports are `trajectory-fake`), so a live candidate has nothing to be
    compared against. A report that does not say so reads like one that passed
    the comparison. A stored report matches when its kind and mode are this
    run's and, when a version is given, it was written for the same request set.
    """
    label = os.path.basename(os.path.normpath(directory)) + "/"
    try:
        names = sorted(name for name in os.listdir(directory) if name.endswith(".json"))
    except OSError as error:
        return {"available": False, "path": "", "revision": "",
                "note": f"无法与基线对比：读不到基线目录 {label}（{error}）"}
    reports = []
    for name in names:
        try:
            data = load_json(os.path.join(directory, name))
        except (OSError, ValueError):
            continue
        if isinstance(data, dict):
            reports.append((name, data))
    modes = sorted({(str(data.get("mode") or "?"), str((data.get("request_set") or {}).get("version") or "?"))
                    for _, data in reports if data.get("kind") == kind})
    candidates = [(name, data) for name, data in reports
                  if data.get("kind") == kind and data.get("mode") == mode
                  and (request_set_version is None
                       or (data.get("request_set") or {}).get("version") == request_set_version)]
    if candidates:
        name, data = max(candidates, key=lambda item: str(item[1].get("generated_at") or ""))
        revision = str(data.get("revision") or "未知")
        return {"available": True, "path": name, "revision": revision,
                "note": f"基线 {name}（revision {revision}，mode {mode}）；用 eval-agent-compare.py 对比"}
    wanted = f"mode={mode}" + ("" if request_set_version is None else f"、请求集 v{request_set_version}")
    detail = f"{label}下没有 {wanted} 的 {kind} 报告"
    if modes:
        detail += "（现有：" + "、".join(f"mode {name} 请求集 v{version}" for name, version in modes) + "）"
    return {"available": False, "path": "", "revision": "",
            "note": f"无法与基线对比：{detail}；没有可比基线，本次结果不能说明是否有回退"}


# ------------------------------------------------------------------- tools

def collect_tools(messages):
    """Tool calls in wire order, each as {name, arguments, task_id}.

    An accepted paid call is persisted twice: once while it waits for approval
    (no task id) and once with its task. The public wire drops the call id, so the
    pair is recognised by the same name and arguments and counted once; the richer
    row (the one naming its task) wins.
    """
    tools, by_key = [], {}
    for message in messages or []:
        for call in common.parse_tool_calls(message):
            name = call.get("name")
            if not name:
                continue
            arguments = common.tool_arguments(call)
            task_id = call.get("taskId")
            key = (name, json.dumps(arguments, sort_keys=True, ensure_ascii=False))
            summary = call.get("summary")
            prior = by_key.get(key)
            if prior is not None:
                if not task_id and not summary and (prior["task_id"] or prior["summary"]):
                    continue
                if (task_id or summary) and not prior["task_id"] and not prior["summary"]:
                    prior["task_id"] = task_id
                    prior["summary"] = summary
                    continue
                if (task_id and not prior["task_id"]) or (summary and not prior["summary"]):
                    prior["task_id"] = task_id or prior["task_id"]
                    prior["summary"] = summary or prior["summary"]
                    continue
            entry = {"name": name, "arguments": arguments, "task_id": task_id, "summary": summary}
            tools.append(entry)
            by_key[key] = entry
    return tools


def tool_names(tools):
    return [tool["name"] for tool in tools]


def first_index(names, wanted):
    for index, name in enumerate(names):
        if name in wanted:
            return index
    return None


def group_satisfied(names, group):
    return any(name in group.get("any", []) for name in names)


def order_satisfied(names, rule):
    """Every `before` tool appears, and each first appears before the first `after` tool."""
    after = first_index(names, set(rule.get("after") or []))
    if after is None:
        return False, f"none of {rule.get('after')} was called"
    for name in rule.get("before") or []:
        at = first_index(names, {name})
        if at is None:
            return False, f"{name} was not called"
        if at > after:
            return False, f"{name} came after {names[after]}"
    return True, ""


def task_capabilities(tasks):
    return [task.get("capability") for task in tasks or []]


def succeeded_capabilities(tasks):
    return {task.get("capability") for task in tasks or [] if task.get("state") == SUCCESS}


def failed_tasks(tasks):
    return [task for task in tasks or [] if task.get("state") in FAILED_STATES]


def interruption_cards(cards):
    """Choice, approval and plan cards the person had to answer or dismiss."""
    return [card for card in cards or [] if card.get("kind") in ("choice", "approval", "plan")]


# --------------------------------------------------------------- evaluation

# #498: what a run's upstream shape can and cannot compare. Only the shape
# decides, never the environment: the generation side may be fake while the chat
# side is a real gateway (#416), and placeholder artifacts then feed back into
# the chat side, because the model refines and inspects what it was given.
# metric group -> what it covers, so the report names what is lost.
COMPARABILITY = {
    "live": (["chat", "artifacts"], [],
             "上游形态 live：生成侧与 chat 侧都是真实上游，chat 与产物指标都可与 live 基线比较"),
    "mixed": (["chat"], ["artifacts"],
              "上游形态 mixed：生成侧是 fake（占位图、夹具模型），产物指标不可比；"
              "占位产物还会反过来影响模型的 refine/inspect/提问，chat 指标只能与另一个 mixed 运行比较，"
              "不能与 live 基线比较"),
}
COMPARABILITY_GROUPS = {
    "chat": "chat 指标（回合、工具调用、打断、返工、检查点）",
    "artifacts": "产物指标（output 的 2D 尺寸/背景/主体覆盖、3D 面数/拓扑/UV/贴图/缩放，以及 output:* 检查）",
}


def comparability(shape):
    """(comparable groups, incomparable groups, note) for one upstream shape."""
    if shape in COMPARABILITY:
        comparable, incomparable, note = COMPARABILITY[shape]
        return list(comparable), list(incomparable), note
    return ([], ["chat", "artifacts"],
            f"上游形态 {shape}：生成侧与 chat 侧都不是真实上游，产物是占位物，"
            "chat 与产物指标都不能与 live/mixed 运行比较（只能与同形态的轨迹运行比较）")


def group_labels(groups):
    return "；".join(f"{COMPARABILITY_GROUPS.get(group, group)}" for group in groups)


def evaluate_case(request, run):
    """Hard checks and recorded metrics for one finished request.

    `run` is built by the runners: messages, tasks, cards, final_reply,
    elapsed_ms, (live only) output_metrics and the live send counters `sends`
    (waits for the turn and busy-session retries, #444).
    """
    expected = request.get("expected") or {}
    tools = collect_tools(run.get("messages") or [])
    names = tool_names(tools)
    tasks = run.get("tasks") or []
    cards = interruption_cards(run.get("cards") or [])
    budgets = expected.get("budgets") or {}
    checks = []

    def check(check_id, ok, detail=""):
        checks.append({"id": check_id, "status": CHECK_PASS if ok else CHECK_FAIL, "detail": detail})
        return ok

    def record(check_id, detail=""):
        """#498: a fact that is recorded for calibration but never decides PASS/FAIL."""
        checks.append({"id": check_id, "status": CHECK_INFO, "detail": detail})

    reply = run.get("final_reply") or {}
    content = (reply.get("content") or "").strip()
    clean = (bool(content) and content != common.GENERATING_PLACEHOLDER
             and not common.reply_failed(reply) and not reply.get("error"))
    check("final_reply", clean, f"{len(content)} chars" + ("" if clean else f"; {content[:120]!r}"))

    finished = succeeded_capabilities(tasks)
    minimums = expected.get("min_tasks") or {}
    for capability in expected.get("capabilities") or []:
        minimum = minimums.get(capability, 1)
        count = sum(1 for task in tasks if task.get("capability") == capability and task.get("state") == SUCCESS)
        check(f"capability:{capability}", count >= minimum,
              f"{count}/{minimum} succeeded; succeeded={sorted(finished)}; tasks={task_capabilities(tasks)}")

    failures = failed_tasks(tasks)
    check("no_failed_tasks", not failures,
          "; ".join(f"{task.get('capability')}:{task.get('state')}" for task in failures))

    for group in expected.get("required_tool_groups") or []:
        check(f"tool_group:{group['id']}", group_satisfied(names, group),
              f"any of {group['any']}; called={names}")

    # #242: an open concept request must actually draw several directions, and
    # a candidate plan must submit every candidate; a single call is not enough.
    for name, minimum in (expected.get("min_tool_counts") or {}).items():
        actual = sum(1 for called in names if called == name)
        check(f"min_tool:{name}", actual >= minimum, f"{actual}/{minimum} call(s)")

    for rule in expected.get("order") or []:
        ok, detail = order_satisfied(names, rule)
        check(f"order:{'+'.join(rule.get('before') or [])}<{'+'.join(rule.get('after') or [])}", ok, detail)

    paid = len(tasks)
    # #498: recorded, never enforced. The thresholds come from the fixed request
    # set and are calibration values: the 2026-11-24 live run (and the recorded
    # baseline) is over all three, so a fail here says nothing about the system.
    record("budget:paid_calls", f"{paid} task(s), 阈值 {budgets.get('max_paid_calls', 10 ** 9)}")
    record("budget:tool_calls", f"{len(names)} call(s), 阈值 {budgets.get('max_tool_calls', 10 ** 9)}")
    record("budget:interruptions", f"{len(cards)} card(s), 阈值 {budgets.get('max_interruptions', 10 ** 9)}")
    checks.extend(run.get("output_checks") or [])

    # #241: the feedback facts and the prompts the fake-only convention
    # produced from them at each stage.
    preference_expectations = expected.get("preferences") or {}
    if preference_expectations and run.get("preference_supported") is False:
        # #498: only a runner that sends the feedback actions (pick, reject,
        # compact, edit, delete) can measure these; the live output runner sends
        # the request set's user prompts alone. The runner states what it does —
        # not the environment — so an unexercised preference is skipped rather
        # than reported as a failure of the system under test.
        for name in PREFERENCE_CHECKS:
            if preference_expectations.get(name):
                checks.append({"id": f"preference:{name}", "status": CHECK_SKIP,
                               "detail": "该 runner 不发送 feedback（pick/reject/compact/edit/delete），"
                                         "preference 未被驱动"})
    elif preference_expectations:
        facts = run.get("preference_facts") or {}
        captured = run.get("preference_captured") or []
        kinds = {preference.get("kind") for preference in captured}
        prompts = {record.get("stage"): record.get("prompt") or "" for record in run.get("preference_prompts") or []}
        selected = facts.get("selected_direction") or ""
        rejected = facts.get("rejected_direction") or ""
        rejected_reason = facts.get("rejected_reason") or ""
        edited = facts.get("edited_direction") or ""
        edited_reason = facts.get("edited_reason") or ""

        def staged(stage):
            return prompts.get(stage, "")

        if preference_expectations.get("captured"):
            check("preference:captured", {"selected", "rejected"} <= kinds,
                  f"kinds={sorted(kind for kind in kinds if kind)}")
        if preference_expectations.get("injected"):
            prompt = staged("captured")
            check("preference:injected",
                  bool(selected and rejected and rejected_reason and selected in prompt
                       and rejected in prompt and rejected_reason in prompt),
                  f"selected={'yes' if selected and selected in prompt else 'no'} "
                  f"rejected={'yes' if rejected and rejected in prompt else 'no'} "
                  f"reason={'yes' if rejected_reason and rejected_reason in prompt else 'no'}")
        if preference_expectations.get("after_compaction"):
            prompt = staged("after_compaction")
            check("preference:after_compaction",
                  bool(selected and selected in prompt and rejected and rejected in prompt),
                  f"compacted_prompt={'found' if prompt else 'missing'}")
        if preference_expectations.get("edited"):
            prompt = staged("edited")
            check("preference:edited",
                  bool(edited and edited in prompt and (not edited_reason or edited_reason in prompt)
                       and (not rejected or rejected not in prompt)),
                  f"edited={'yes' if edited and edited in prompt else 'no'} "
                  f"old_gone={'yes' if rejected and rejected not in prompt else 'no'}")
        if preference_expectations.get("deleted"):
            prompt = staged("deleted")
            check("preference:deleted",
                  bool(prompt and (not edited or edited not in prompt)
                       and (not edited_reason or edited_reason not in prompt)
                       and (not selected or selected in prompt)),
                  f"edited_gone={'yes' if edited and edited not in prompt else 'no'} "
                  f"selected_kept={'yes' if selected and selected in prompt else 'no'}")

    counts = {}
    revision_operations = {}
    for tool in tools:
        name = tool.get("name")
        counts[name] = counts.get(name, 0) + 1
        if name == "revise_processing_plan":
            operation = str((tool.get("arguments") or {}).get("operation") or "unknown")
            revision_operations[operation] = revision_operations.get(operation, 0) + 1
    revision_count = counts.get("revise_processing_plan", 0)
    # #251: rework is recorded, never used as a tail regression gate: a base
    # without the feature may rework less simply because it does fewer steps.
    rework_count = len(failures) + revision_count
    checkpoint_states = []
    for plan in run.get("plans") or []:
        for step in plan.get("steps") or []:
            checkpoint = step.get("checkpoint")
            if isinstance(checkpoint, dict):
                checkpoint_states.append(checkpoint.get("state") or "")
    checkpoints = {
        "total": len(checkpoint_states),
        "passed": sum(1 for state in checkpoint_states if state == "passed"),
        "failed": sum(1 for state in checkpoint_states if state == "failed"),
        "waived": sum(1 for state in checkpoint_states if state == "waived"),
        "unknown": sum(1 for state in checkpoint_states if state == "unknown"),
    }
    self_check_records = 0
    self_check_status = {}
    for tool in tools:
        if tool.get("name") != "record_self_check":
            continue
        summary = tool.get("summary")
        if isinstance(summary, dict):
            self_check_records += 1
            role = summary.get("status_role")
            if role:
                self_check_status[role] = self_check_status.get(role, 0) + 1
    prices = common.call_prices(run.get("messages") or [])
    credits = common.spend_by_unit(prices.values())
    metrics = {
        "tool_call_count": len(names),
        "credits": {unit: str(amount) for unit, amount in sorted(credits.items())},
        "paid_call_count": paid,
        "interruption_count": len(cards),
        "choice_card_count": sum(1 for card in cards if card.get("kind") == "choice"),
        "approval_card_count": sum(1 for card in cards if card.get("kind") == "approval"),
        "plan_card_count": sum(1 for card in cards if card.get("kind") == "plan"),
        "tool_counts": counts,
        "rework": {
            "count": rework_count,
            "failed_tasks": len(failures),
            "revision_count": revision_count,
            "revision_operations": revision_operations,
        },
        "checkpoints": checkpoints,
        "self_inspection": {
            "inspect_model": counts.get("inspect_model", 0),
            "render_model_views": counts.get("render_model_views", 0),
            "compare_model_reference": counts.get("compare_model_reference", 0),
            "self_check_records": self_check_records,
            "self_check_passed": self_check_status.get("passed", 0),
            "self_check_issues": self_check_status.get("issues", 0),
        },
        "image_count": run.get("image_count", 0),
        "final_reply_chars": len(content),
        "elapsed_ms": run.get("elapsed_ms"),
    }
    if run.get("output_metrics"):
        metrics["output"] = run["output_metrics"]
    result = {
        "id": request["id"],
        "title": request["title"],
        "modality": request.get("modality"),
        "category": request.get("category"),
        "purpose": request.get("purpose"),
        "session_id": run.get("session_id"),
        "turns": run.get("turns"),
        "sends": run.get("sends") or {},
        "tool_calls": tools,
        "cards": cards,
        "tasks": [
            {
                "id": task.get("id"),
                "capability": task.get("capability"),
                "model_id": task.get("model_id"),
                "offering_id": task.get("offering_id"),
                "state": task.get("state"),
                "credits_charged": task.get("credits_charged"),
            }
            for task in tasks
        ],
        "final_reply": {"content": content, "failed": not clean},
        "assets": run.get("assets") or [],
        "metrics": metrics,
        "checks": checks,
    }
    return result


def case_hard_failures(case):
    return [check for check in case.get("checks") or [] if check.get("status") == CHECK_FAIL]


def summarize(cases, mode, revision, dirty, request_set, rubric=None, started_at=None, shape="live", baseline=None):
    hard = [check for case in cases for check in case_hard_failures(case)]
    total_cards = sum(case["metrics"]["interruption_count"] for case in cases)
    total_paid = sum(case["metrics"]["paid_call_count"] for case in cases)
    total_tools = sum(case["metrics"]["tool_call_count"] for case in cases)
    inspect = sum(case["metrics"]["self_inspection"]["inspect_model"] for case in cases)
    renders = sum(case["metrics"]["self_inspection"]["render_model_views"] for case in cases)
    references = sum(case["metrics"]["self_inspection"].get("compare_model_reference", 0) for case in cases)
    self_check_records = sum(case["metrics"]["self_inspection"].get("self_check_records", 0) for case in cases)
    self_check_passed = sum(case["metrics"]["self_inspection"].get("self_check_passed", 0) for case in cases)
    rework_count = sum((case["metrics"].get("rework") or {}).get("count", 0) for case in cases)
    revision_count = sum((case["metrics"].get("rework") or {}).get("revision_count", 0) for case in cases)
    checkpoint_totals = {"total": 0, "passed": 0, "failed": 0, "waived": 0, "unknown": 0}
    for case in cases:
        checkpoints = case["metrics"].get("checkpoints") or {}
        for name in checkpoint_totals:
            checkpoint_totals[name] += checkpoints.get(name, 0)
    passed_cases = len(cases) - len({case["id"] for case in cases if case_hard_failures(case)})
    success_rate = round(100.0 * passed_cases / len(cases), 1) if cases else 0.0
    recorded_checks = sum(1 for case in cases for check in case.get("checks") or []
                          if check.get("status") == CHECK_INFO)
    skipped_checks = sum(1 for case in cases for check in case.get("checks") or []
                         if check.get("status") == CHECK_SKIP)
    comparable, incomparable, note = comparability(shape)
    if baseline is None:
        baseline = find_baseline(mode, (request_set or {}).get("version"))
    credits = {}
    for case in cases:
        for unit, amount in (case["metrics"].get("credits") or {}).items():
            credits[unit] = credits.get(unit, decimal.Decimal(0)) + decimal.Decimal(str(amount))
    report = {
        "schema_version": 1,
        "kind": "agent-quality",
        "mode": mode,
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "revision": revision,
        "dirty": bool(dirty),
        "request_set": request_set,
        # The shape decides which metrics are comparable with a run on another
        # shape: only the artifacts need real generation, while the chat metrics
        # (turns, refusals, questions, tool calls) depend on the chat side alone
        # (#407, #416 mixed shape). #498: the report states both lists and names
        # the metric groups that are lost, so a run on a non-real generation side
        # cannot pass for a comparable one.
        "upstream_shape": shape,
        "comparable_metrics": comparable,
        "incomparable_metrics": incomparable,
        "comparability_note": note,
        # #498: a report with no stored baseline to compare against must say so;
        # "no regressions" is a statement about a comparison that did not happen.
        "baseline": baseline,
        "cases": cases,
        "summary": {
            "cases": len(cases),
            "hard_failures": len(hard),
            "failed_cases": sorted({case["id"] for case in cases if case_hard_failures(case)}),
            "metrics": {
                "tool_calls": total_tools,
                "paid_calls": total_paid,
                "interruptions": total_cards,
                "inspect_model": inspect,
                "render_model_views": renders,
                "compare_model_reference": references,
                "self_check_records": self_check_records,
                "self_check_passed": self_check_passed,
                "rework_count": rework_count,
                "revision_count": revision_count,
                "checkpoint_count": checkpoint_totals["total"],
                "checkpoint_passed": checkpoint_totals["passed"],
                "checkpoint_failed": checkpoint_totals["failed"],
                "checkpoint_waived": checkpoint_totals["waived"],
                "checkpoint_unknown": checkpoint_totals["unknown"],
                "success_rate": success_rate,
                # #498: recorded and skipped checks, so a reader of the JSON sees
                # how much of the case verdict was not measured.
                "recorded_checks": recorded_checks,
                "skipped_checks": skipped_checks,
                "credits": {unit: str(amount) for unit, amount in sorted(credits.items())},
                "elapsed_ms": sum((case["metrics"].get("elapsed_ms") or 0) for case in cases),
            },
        },
    }
    if rubric is not None:
        report["rubric"] = rubric
    if started_at is not None:
        report["started_at"] = started_at
    return report


# ----------------------------------------------------------------- reporting

def write_report(report, directory, name="agent-quality"):
    os.makedirs(directory, exist_ok=True)
    json_path = os.path.join(directory, name + ".json")
    markdown_path = os.path.join(directory, name + ".md")
    with open(json_path, "w", encoding="utf-8") as handle:
        json.dump(report, handle, ensure_ascii=False, indent=2)
    with open(markdown_path, "w", encoding="utf-8") as handle:
        handle.write(report_markdown(report))
    return json_path, markdown_path


def report_markdown(report):
    summary = report.get("summary") or {}
    metrics = summary.get("metrics") or {}
    comparable = report.get("comparable_metrics") or []
    incomparable = report.get("incomparable_metrics") or []
    baseline = report.get("baseline") or {}
    lines = [
        f"# Agent 产出质量评测（{report.get('mode')}）",
        "",
        f"- revision: `{report.get('revision')}`{' (dirty)' if report.get('dirty') else ''}",
        f"- 请求集: version {report.get('request_set', {}).get('version')}, sha256 `{report.get('request_set', {}).get('sha256')}`",
        # #498: the shape and what it costs go at the top: a run whose generation
        # side is fake produces placeholder artifacts, and a reader who only sees
        # the case table cannot tell that from a comparable run.
        f"- 上游形态: `{report.get('upstream_shape')}`；可比指标: {group_labels(comparable) or '（无）'}",
        f"- 不可比指标: {group_labels(incomparable) or '（无）'}",
    ]
    if incomparable:
        lines[-1] = f"- **不可比指标: {group_labels(incomparable)}**"
    if report.get("comparability_note"):
        lines.append(f"- 可比性: {report['comparability_note']}")
    if baseline:
        lines.append(f"- 基线: {baseline['note']}" if baseline.get("available")
                     else f"- **{baseline.get('note') or '无法与基线对比'}**")
    if report.get("rubric"):
        lines.append(f"- 量表: version {report['rubric'].get('version')}, sha256 `{report['rubric'].get('sha256')}`")
    lines += [
        f"- 用例: {summary.get('cases', 0)}；硬失败: {summary.get('hard_failures', 0)}；成功率: {metrics.get('success_rate', 0)}%",
        f"- 仅记录（不参与通过判定）: {metrics.get('recorded_checks', 0)} 项；已跳过（不参与通过判定）: {metrics.get('skipped_checks', 0)} 项",
        f"- 合计: 工具调用 {metrics.get('tool_calls', 0)}，付费调用 {metrics.get('paid_calls', 0)}，打断 {metrics.get('interruptions', 0)}，inspect {metrics.get('inspect_model', 0)}，render {metrics.get('render_model_views', 0)}，对照 {metrics.get('compare_model_reference', 0)}，自检记录 {metrics.get('self_check_records', 0)}，计费 {metrics.get('credits', {})}",
        f"- 返工: {metrics.get('rework_count', 0)}（修订 {metrics.get('revision_count', 0)}）；检查点 {metrics.get('checkpoint_count', 0)}（失败 {metrics.get('checkpoint_failed', 0)}，放弃 {metrics.get('checkpoint_waived', 0)}，无法判断 {metrics.get('checkpoint_unknown', 0)}）",
        "",
        "| 用例 | 结果 | 工具 | 付费 | 打断 | inspect | render | 对照 | 自检 | 返工 | 修订 | 检查点 | 耗时 |",
        "|---|---|---|---|---|---|---|---|---|---|---|---|---|",
    ]
    for case in report.get("cases") or []:
        case_metrics = case.get("metrics") or {}
        status = "FAIL" if case_hard_failures(case) else "PASS"
        elapsed = case_metrics.get("elapsed_ms")
        lines.append(
            "| {id} | {status} | {tools} | {paid} | {cards} | {inspect} | {render} | {reference} | {check} | {rework} | {revisions} | {checkpoints} | {elapsed} |".format(
                id=case.get("id"), status=status,
                tools=case_metrics.get("tool_call_count", 0),
                paid=case_metrics.get("paid_call_count", 0),
                cards=case_metrics.get("interruption_count", 0),
                inspect=case_metrics.get("self_inspection", {}).get("inspect_model", 0),
                render=case_metrics.get("self_inspection", {}).get("render_model_views", 0),
                reference=case_metrics.get("self_inspection", {}).get("compare_model_reference", 0),
                check=case_metrics.get("self_inspection", {}).get("self_check_records", 0),
                rework=(case_metrics.get("rework") or {}).get("count", 0),
                revisions=(case_metrics.get("rework") or {}).get("revision_count", 0),
                checkpoints=(case_metrics.get("checkpoints") or {}).get("total", 0),
                elapsed=f"{elapsed / 1000:.1f}s" if isinstance(elapsed, (int, float)) else "-",
            )
        )
    failures = [(case, check) for case in report.get("cases") or [] for check in case_hard_failures(case)]
    if failures:
        lines += ["", "## 硬失败", ""]
        for case, check in failures:
            lines.append(f"- `{case['id']}` / `{check['id']}`: {check.get('detail') or ''}")
    lines += ["", "## 各用例指标", ""]
    for case in report.get("cases") or []:
        case_metrics = case.get("metrics") or {}
        lines.append(f"### {case['id']} — {case['title']}")
        lines.append("")
        lines.append(f"- 工具：{case_metrics.get('tool_counts')}")
        lines.append(f"- 自检：{case_metrics.get('self_inspection')}")
        lines.append(f"- 返工：{case_metrics.get('rework')}")
        lines.append(f"- 检查点：{case_metrics.get('checkpoints')}")
        lines.append(f"- 任务：{[(task.get('capability'), task.get('state')) for task in case.get('tasks') or []]}")
        lines.append(f"- 最终回复：{case_metrics.get('final_reply_chars', 0)} 字符")
        sends = case.get("sends") or {}
        if sends.get("idle_waits") or sends.get("busy_retries") or sends.get("rejected"):
            lines.append(f"- 发送等待：回合结束 {sends.get('idle_waits', 0)} 次/{sends.get('idle_wait_ms', 0) / 1000:.1f}s，"
                         f"会话忙重试 {sends.get('busy_retries', 0)} 次/{sends.get('busy_wait_ms', 0) / 1000:.1f}s，"
                         f"被拒 {sends.get('rejected', 0)} 次")
        if case_metrics.get("output"):
            lines.append(f"- 产出指标：`{json.dumps(case_metrics['output'], ensure_ascii=False)}`")
        # #498: the recorded and skipped checks keep their values in the report;
        # they just do not decide the verdict.
        case_recorded = [check for check in case.get("checks") or [] if check.get("status") == CHECK_INFO]
        if case_recorded:
            lines.append("- 仅记录（不参与通过判定）：" + "；".join(
                f"`{check['id']}` {check.get('detail') or ''}" for check in case_recorded))
        case_skipped = [check for check in case.get("checks") or [] if check.get("status") == CHECK_SKIP]
        if case_skipped:
            reasons = {check.get("detail") or "" for check in case_skipped}
            lines.append("- 已跳过（不参与通过判定）：" + "；".join(f"`{check['id']}`" for check in case_skipped)
                         + (f"（{case_skipped[0]['detail']}）" if len(reasons) == 1 else ""))
        lines.append("")
    return "\n".join(lines) + "\n"


# --------------------------------------------------------- output metrics

def png_rgba(data):
    """(width, height, rows of RGBA bytes) for an 8-bit non-interlaced PNG, or None."""
    import struct
    import zlib

    if data[:8] != b"\x89PNG\r\n\x1a\n":
        return None
    header, compressed, index = None, [], 8
    while index + 8 <= len(data):
        length, kind = struct.unpack(">I4s", data[index:index + 8])
        payload = data[index + 8:index + 8 + length]
        if kind == b"IHDR":
            header = struct.unpack(">IIBBBBB", payload)
        elif kind == b"IDAT":
            compressed.append(payload)
        elif kind == b"IEND":
            break
        index += 12 + length
    if not header:
        return None
    width, height, depth, colour, _, _, interlace = header
    if depth != 8 or colour != 6 or interlace:
        return None
    stride = width * 4
    raw = zlib.decompress(b"".join(compressed))
    prior, rows, position = bytearray(stride), [], 0
    for _ in range(height):
        kind, line = raw[position], bytearray(raw[position + 1:position + 1 + stride])
        position += 1 + stride
        if kind == 1:
            for i in range(4, stride):
                line[i] = (line[i] + line[i - 4]) & 0xFF
        elif kind == 2:
            line = bytearray((a + b) & 0xFF for a, b in zip(line, prior))
        elif kind == 3:
            for i in range(stride):
                left = line[i - 4] if i >= 4 else 0
                line[i] = (line[i] + ((left + prior[i]) >> 1)) & 0xFF
        elif kind == 4:
            for i in range(stride):
                a, b, c = (line[i - 4], prior[i], prior[i - 4]) if i >= 4 else (0, prior[i], 0)
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                line[i] = (line[i] + (a if pa <= pb and pa <= pc else b if pb <= pc else c)) & 0xFF
        elif kind != 0:
            return None
        rows.append(line)
        prior = line
    return width, height, rows


def _png_subject_stats(data):
    """Alpha/background coverage, subject box and edge contact for an 8-bit PNG."""
    rgba = png_rgba(data)
    if rgba is not None:
        width, height, rows = rgba
        opaque = [(x, y) for y, row in enumerate(rows) for x in range(width) if row[4 * x + 3] > 8]
        coverage = len(opaque) / float(width * height) if width and height else 0.0
        if not opaque:
            return {"alpha": True, "opaque_ratio": 0.0, "subject_bbox": [0, 0, 0, 0],
                    "edge_contact": False, "nonblank": False}
        xs, ys = [x for x, _ in opaque], [y for _, y in opaque]
        edge = any(x == 0 or x == width - 1 or y == 0 or y == height - 1 for x, y in opaque)
        return {"alpha": True, "opaque_ratio": round(coverage, 4),
                "subject_bbox": [min(xs), min(ys), max(xs), max(ys)],
                "edge_contact": edge, "nonblank": coverage > 0.01}
    try:
        width, height, rows = common.decode_png(data)
    except ValueError:
        return None
    if not width or not height:
        return None
    # Opaque background sample from the four corners; a subject pixel differs from it.
    corners = [rows[0][:3], rows[0][-3:], rows[-1][:3], rows[-1][-3:]]
    background = [sum(pixel[channel] for pixel in corners) // 4 for channel in range(3)]
    subject = []
    for y, row in enumerate(rows):
        for x in range(width):
            pixel = row[3 * x:3 * x + 3]
            if sum(abs(pixel[channel] - background[channel]) for channel in range(3)) > 72:
                subject.append((x, y))
    coverage = len(subject) / float(width * height)
    if not subject:
        return {"alpha": False, "opaque_ratio": 0.0, "subject_bbox": [0, 0, 0, 0],
                "edge_contact": False, "nonblank": False}
    xs, ys = [x for x, _ in subject], [y for _, y in subject]
    edge = any(x == 0 or x == width - 1 or y == 0 or y == height - 1 for x, y in subject)
    return {"alpha": False, "opaque_ratio": round(coverage, 4),
            "subject_bbox": [min(xs), min(ys), max(xs), max(ys)],
            "edge_contact": edge, "nonblank": coverage > 0.01}


def image_metrics(data):
    """Deterministic 2D facts: size, format, and PNG subject/background coverage."""
    dimensions = common.image_dimensions(data)
    fmt = "png" if data[:8] == b"\x89PNG\r\n\x1a\n" else "jpeg" if data[:2] == b"\xff\xd8" else "webp" if data[:4] == b"RIFF" else "unknown"
    metrics = {"bytes": len(data), "format": fmt,
               "width": dimensions[0] if dimensions else None,
               "height": dimensions[1] if dimensions else None}
    if fmt == "png":
        stats = _png_subject_stats(data)
        if stats:
            metrics.update(stats)
    return metrics


def schema_purposes(schema):
    """The purpose ids each modality declares in a Product Schema document."""
    application = schema.get("application", schema) if isinstance(schema, dict) else {}
    chat = application.get("chat") or {}
    return {
        "2d": {profile.get("id") for profile in ((chat.get("image2d_usage") or {}).get("profiles") or [])},
        "3d": {profile.get("id") for profile in ((chat.get("model3d_usage") or {}).get("profiles") or [])},
    }


def purpose_budget(schema, purpose):
    """The Schema's production-purpose budget, or None."""
    usage = ((schema or {}).get("chat") or {}).get("model3d_usage") or {}
    for profile in usage.get("profiles") or []:
        if profile.get("id") == purpose:
            return profile.get("budget") or {}
    return None


def model_budget_checks(report, budget):
    """Checks of one inspect report against a purpose budget."""
    checks = []
    budget = budget or {}
    triangles = report.get("triangles")
    low, high = budget.get("min_triangles"), budget.get("max_triangles")
    ok = triangles is not None and (low is None or triangles >= low) and (high is None or triangles <= high)
    checks.append({"id": "output:triangles", "status": CHECK_PASS if ok else CHECK_FAIL,
                   "detail": f"{triangles} triangles, budget {low}..{high}"})
    max_texture = budget.get("max_texture_size")
    sizes = [slot.get("width") or 0 for material in report.get("materials") or []
             for slot in material.get("slots") or [] if slot.get("present")]
    sizes += [slot.get("height") or 0 for material in report.get("materials") or []
              for slot in material.get("slots") or [] if slot.get("present")]
    ok = max_texture is None or not sizes or max(sizes) <= max_texture
    checks.append({"id": "output:texture_size", "status": CHECK_PASS if ok else CHECK_FAIL,
                   "detail": f"max {max(sizes) if sizes else 0}, budget {max_texture}"})
    if budget.get("require_uv"):
        ok = bool((report.get("uv") or {}).get("all_have_uv"))
        checks.append({"id": "output:uv", "status": CHECK_PASS if ok else CHECK_FAIL,
                       "detail": f"all_have_uv={(report.get('uv') or {}).get('all_have_uv')}"})
    if budget.get("require_watertight"):
        ok = bool((report.get("topology") or {}).get("watertight"))
        checks.append({"id": "output:watertight", "status": CHECK_PASS if ok else CHECK_FAIL,
                       "detail": f"watertight={(report.get('topology') or {}).get('watertight')}"})
    if budget.get("require_skin"):
        ok = bool(report.get("skins"))
        checks.append({"id": "output:skin", "status": CHECK_PASS if ok else CHECK_FAIL,
                       "detail": f"{len(report.get('skins') or [])} skin(s)"})
    scale = budget.get("scale_range_m") or {}
    if scale.get("min") is not None or scale.get("max") is not None:
        size = (report.get("bbox") or {}).get("size") or [0, 0, 0]
        longest = max(size) if size else 0
        ok = ((scale.get("min") is None or longest >= scale["min"])
              and (scale.get("max") is None or longest <= scale["max"]))
        checks.append({"id": "output:scale", "status": CHECK_PASS if ok else CHECK_FAIL,
                       "detail": f"longest edge {longest:.3f} m, budget {scale.get('min')}..{scale.get('max')}"})
    debris = (report.get("topology") or {}).get("debris_components")
    if debris is not None:
        checks.append({"id": "output:debris", "status": CHECK_PASS if debris == 0 else CHECK_FAIL,
                       "detail": f"{debris} debris component(s)"})
    return checks


# ------------------------------------------------------------------ compare

TAIL_METRICS = {
    # metric -> "lower" means a larger value is a regression.
    "tool_calls": "lower",
    "paid_calls": "lower",
    "interruptions": "lower",
    "inspect_model": "higher",
    "render_model_views": "higher",
    "compare_model_reference": "higher",
    "self_check_records": "higher",
    "self_check_passed": "higher",
}


def compare_reports(baseline, candidate, tolerance=0, new_request_set=False, exclude_check_prefixes=()):
    """Regressions, improvements and changed metric deltas between two reports.

    new_request_set is the explicit report mode for a changed fixed request
    set (#240): the request-set version/content difference is recorded rather
    than treated as incomparable, cases missing from the baseline count as
    additions, and the tail metrics are compared over the common cases only.

    exclude_check_prefixes drops checks from the diff (#444): the caller names
    the checks whose baseline verdict is void, for example `output:model` after
    the baseline inspected a 3MF delivery as a GLB. Baseline checks whose
    detail is an UNCOMPARABLE_DETAIL_MARKERS match (the same GLB parse failure)
    are listed as incomparable automatically. Neither list affects `ok`.
    """
    prefixes = tuple(prefix for prefix in exclude_check_prefixes or () if prefix)
    problems = []
    if not new_request_set:
        if baseline.get("request_set", {}).get("version") != candidate.get("request_set", {}).get("version"):
            problems.append("the two reports used different request-set versions")
        if baseline.get("request_set", {}).get("sha256") != candidate.get("request_set", {}).get("sha256"):
            problems.append("the two reports used different request-set contents")
    if baseline.get("mode") != candidate.get("mode"):
        problems.append(f"the two reports used different modes: {baseline.get('mode')} vs {candidate.get('mode')}")

    base_cases = {case.get("id"): case for case in baseline.get("cases") or []}
    cand_cases = {case.get("id"): case for case in candidate.get("cases") or []}
    regressions, improvements, changed, excluded, incomparable = [], [], [], [], []
    recorded = []
    for case_id in sorted(set(base_cases) | set(cand_cases)):
        if case_id not in cand_cases:
            if new_request_set:
                changed.append({"case": case_id, "check": "case",
                                "detail": "removed from the candidate request set"})
                continue
            regressions.append({"case": case_id, "check": "case", "detail": "missing from the candidate report"})
            continue
        if case_id not in base_cases:
            improvements.append({"case": case_id, "check": "case", "detail": "new case in the candidate report"})
            continue
        base_checks = {check.get("id"): check for check in base_cases[case_id].get("checks") or []}
        cand_checks = {check.get("id"): check for check in cand_cases[case_id].get("checks") or []}
        for check_id in sorted(set(base_checks) | set(cand_checks)):
            if prefixes and str(check_id or "").startswith(prefixes):
                excluded.append({"case": case_id, "check": check_id, "reason": "按前缀排除"})
                continue
            before_detail = str((base_checks.get(check_id) or {}).get("detail") or "")
            marker = next((marker for marker in UNCOMPARABLE_DETAIL_MARKERS if marker in before_detail), "")
            if marker:
                incomparable.append({"case": case_id, "check": check_id,
                                     "reason": f"基线判定作废（detail 含「{marker}」）"})
                continue
            before = (base_checks.get(check_id) or {}).get("status")
            after = (cand_checks.get(check_id) or {}).get("status")
            if before == after:
                continue
            if after == CHECK_INFO:
                # #498: a recorded check (the budget thresholds) does not decide
                # the verdict, so its status change is neither a regression nor
                # an improvement; the values stay visible in the report.
                recorded.append({"case": case_id, "check": check_id, "before": before, "after": after,
                                 "reason": "候选按「仅记录」处理（不参与通过判定）"})
                continue
            entry = {"case": case_id, "check": check_id, "before": before, "after": after}
            if before == CHECK_PASS and after != CHECK_PASS:
                regressions.append(entry)
            elif after == CHECK_PASS:
                improvements.append(entry)
            else:
                changed.append(entry)

    if new_request_set:
        # The request set changed, so the report totals are not comparable.
        # Compare the same metrics over the cases both reports ran.
        common = set(base_cases) & set(cand_cases)
        base_metrics = case_metric_totals(base_cases, common)
        cand_metrics = case_metric_totals(cand_cases, common)
    else:
        base_metrics = (baseline.get("summary") or {}).get("metrics") or {}
        cand_metrics = (candidate.get("summary") or {}).get("metrics") or {}
    deltas = {}
    for metric, direction in TAIL_METRICS.items():
        before = base_metrics.get(metric)
        after = cand_metrics.get(metric)
        if before is None or after is None:
            continue
        delta = after - before
        deltas[metric] = {"before": before, "after": after, "delta": delta, "direction": direction}
        if new_request_set:
            # A changed request definition can add tools or cards to a common
            # case; the delta stays visible but never blocks a report-only run.
            continue
        if direction == "lower" and delta > tolerance:
            regressions.append({"case": "*", "check": f"metric:{metric}", "before": before, "after": after})
        elif direction == "higher" and delta < -tolerance:
            regressions.append({"case": "*", "check": f"metric:{metric}", "before": before, "after": after})
    return {
        "ok": not regressions and not problems,
        "problems": problems,
        "regressions": regressions,
        "improvements": improvements,
        "changed": changed,
        "excluded": excluded,
        "incomparable": incomparable,
        "recorded": recorded,
        "exclude_check_prefixes": list(prefixes),
        "metrics": deltas,
        "new_request_set": new_request_set,
        "markdown": compare_markdown(baseline, candidate, problems, regressions, improvements, changed, deltas,
                                     new_request_set=new_request_set, excluded=excluded,
                                     incomparable=incomparable, prefixes=prefixes, recorded=recorded),
    }


# CASE_METRIC_PATHS reads the per-case metrics a report summary flattens.
CASE_METRIC_PATHS = {
    "tool_calls": ("tool_call_count",),
    "paid_calls": ("paid_call_count",),
    "interruptions": ("interruption_count",),
    "inspect_model": ("self_inspection", "inspect_model"),
    "render_model_views": ("self_inspection", "render_model_views"),
    "compare_model_reference": ("self_inspection", "compare_model_reference"),
    "self_check_records": ("self_inspection", "self_check_records"),
    "self_check_passed": ("self_inspection", "self_check_passed"),
}


def case_metric_totals(cases, case_ids):
    """The tail metrics a candidate and a baseline share, summed per case."""
    totals = {}
    for metric, path in CASE_METRIC_PATHS.items():
        total = 0
        for case_id in case_ids:
            value = (cases.get(case_id) or {}).get("metrics") or {}
            for key in path:
                value = (value or {}).get(key) if isinstance(value, dict) else None
            if isinstance(value, int):
                total += value
        totals[metric] = total
    return totals


def compare_markdown(baseline, candidate, problems, regressions, improvements, changed, deltas,
                     new_request_set=False, excluded=(), incomparable=(), prefixes=(), recorded=()):
    lines = [
        "# Agent 产出质量评测对比",
        "",
        f"- 基线: `{baseline.get('revision')}`",
        f"- 候选: `{candidate.get('revision')}`",
        f"- 结论: {'无回退' if not regressions and not problems else '存在回退或不可比项'}",
    ]
    if baseline.get("upstream_shape") or candidate.get("upstream_shape"):
        before, after = baseline.get("upstream_shape"), candidate.get("upstream_shape")
        lines.append(f"- 上游形态: 基线 `{before}`，候选 `{after}`"
                     + ("" if before == after else "；**两侧形态不同，只有 comparable_metrics 里的指标组可比**"))
    if new_request_set:
        lines.append("- 请求集: 显式按新请求集对比（新增用例计为新增，汇总指标只比较两边共有的用例且仅作参考）")
    if prefixes:
        lines.append("- 排除检查: " + "、".join(f"`{prefix}*`" for prefix in prefixes) + "（不参与回退判定）")
    lines.append("")
    if problems:
        lines += ["## 不可比", ""] + [f"- {problem}" for problem in problems] + [""]
    if excluded or incomparable or recorded:
        lines += ["## 不可比检查（不参与回退判定）", "", "| 用例 | 检查 | 原因 |", "|---|---|---|"]
        lines += [f"| {item.get('case')} | {item.get('check')} | {item.get('reason')} |"
                  for item in list(excluded) + list(incomparable) + list(recorded)]
        lines.append("")
    if regressions:
        lines += ["## 回退", "", "| 用例 | 检查 | 基线 | 候选 |", "|---|---|---|---|"]
        lines += [f"| {item.get('case')} | {item.get('check')} | {item.get('before', '-')} | {item.get('after', '-')} |" for item in regressions]
        lines.append("")
    added = [item for item in improvements if item.get("check") == "case"]
    if added:
        lines += ["## 新增用例", ""] + [f"- `{item.get('case')}`: {item.get('detail')}" for item in added] + [""]
    kept = [item for item in improvements if item.get("check") != "case"]
    if kept:
        lines += ["## 改善", ""] + [f"- `{item.get('case')}` / `{item.get('check')}`: {item.get('before') or '未检查'} → {item.get('after')}" for item in kept] + [""]
    if changed:
        lines += ["## 其他变化", ""] + [f"- `{item.get('case')}` / `{item.get('check')}`: {item.get('before')} → {item.get('after')}" for item in changed] + [""]
    if deltas:
        lines += ["## 汇总指标", "", "| 指标 | 基线 | 候选 | 变化 |", "|---|---|---|---|"]
        for metric, delta in deltas.items():
            lines.append(f"| {metric} | {delta['before']} | {delta['after']} | {delta['delta']:+d} |")
    base_summary = (baseline.get("summary") or {}).get("metrics") or {}
    cand_summary = (candidate.get("summary") or {}).get("metrics") or {}
    quality_metrics = ("success_rate", "rework_count", "revision_count", "checkpoint_count",
                       "checkpoint_failed", "checkpoint_waived")
    if any(name in base_summary or name in cand_summary for name in quality_metrics):
        # #251: recorded for the #238 comparison. Rework is intentionally not
        # a TAIL_METRICS regression gate: a base without checkpoints may rework
        # less because it does fewer steps.
        lines += ["", "## 质量指标（仅记录，不参与回退判定）", "",
                  "| 指标 | 基线 | 候选 | 变化 |", "|---|---|---|---|"]
        for metric in quality_metrics:
            before = base_summary.get(metric, 0)
            after = cand_summary.get(metric, 0)
            try:
                delta = round(after - before, 1)
                shown = f"{delta:+}"
            except TypeError:
                delta, shown = "", ""
            lines.append(f"| {metric} | {before} | {after} | {shown} |")
    return "\n".join(lines) + "\n"