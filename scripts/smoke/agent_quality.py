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

SUCCESS = common.SUCCESS_STATE
FAILED_STATES = common.FAILED_STATES
CHECK_PASS, CHECK_FAIL, CHECK_SKIP = "pass", "fail", "skip"


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
        steps = request.get("steps") or []
        if not isinstance(steps, list) or not steps:
            problems.append(f"{request_id}: steps must be a non-empty list")
        else:
            for step_index, step in enumerate(steps):
                tool = (step or {}).get("tool") or {}
                if not step.get("user") or not tool.get("name"):
                    problems.append(f"{request_id}.steps[{step_index}]: missing user or tool.name")
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
    return problems


def load_requests(path=REQUESTS_PATH):
    data = load_json(path)
    problems = validate_request_set(data)
    if problems:
        raise ValueError("; ".join(problems))
    return data, path, file_sha256(path)


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

def evaluate_case(request, run):
    """Hard checks and recorded metrics for one finished request.

    `run` is built by the runners: messages, tasks, cards, final_reply,
    elapsed_ms and (live only) output_metrics.
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
    check("budget:paid_calls", paid <= budgets.get("max_paid_calls", 10 ** 9), f"{paid} task(s)")
    check("budget:tool_calls", len(names) <= budgets.get("max_tool_calls", 10 ** 9), f"{len(names)} call(s)")
    check("budget:interruptions", len(cards) <= budgets.get("max_interruptions", 10 ** 9), f"{len(cards)} card(s)")
    checks.extend(run.get("output_checks") or [])

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


def summarize(cases, mode, revision, dirty, request_set, rubric=None, started_at=None):
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
    lines = [
        f"# Agent 产出质量评测（{report.get('mode')}）",
        "",
        f"- revision: `{report.get('revision')}`{' (dirty)' if report.get('dirty') else ''}",
        f"- 请求集: version {report.get('request_set', {}).get('version')}, sha256 `{report.get('request_set', {}).get('sha256')}`",
    ]
    if report.get("rubric"):
        lines.append(f"- 量表: version {report['rubric'].get('version')}, sha256 `{report['rubric'].get('sha256')}`")
    lines += [
        f"- 用例: {summary.get('cases', 0)}；硬失败: {summary.get('hard_failures', 0)}；成功率: {metrics.get('success_rate', 0)}%",
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
        if case_metrics.get("output"):
            lines.append(f"- 产出指标：`{json.dumps(case_metrics['output'], ensure_ascii=False)}`")
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


def compare_reports(baseline, candidate, tolerance=0):
    """Regressions, improvements and changed metric deltas between two reports."""
    problems = []
    if baseline.get("request_set", {}).get("version") != candidate.get("request_set", {}).get("version"):
        problems.append("the two reports used different request-set versions")
    if baseline.get("request_set", {}).get("sha256") != candidate.get("request_set", {}).get("sha256"):
        problems.append("the two reports used different request-set contents")
    if baseline.get("mode") != candidate.get("mode"):
        problems.append(f"the two reports used different modes: {baseline.get('mode')} vs {candidate.get('mode')}")

    base_cases = {case.get("id"): case for case in baseline.get("cases") or []}
    cand_cases = {case.get("id"): case for case in candidate.get("cases") or []}
    regressions, improvements, changed = [], [], []
    for case_id in sorted(set(base_cases) | set(cand_cases)):
        if case_id not in cand_cases:
            regressions.append({"case": case_id, "check": "case", "detail": "missing from the candidate report"})
            continue
        if case_id not in base_cases:
            improvements.append({"case": case_id, "check": "case", "detail": "new case in the candidate report"})
            continue
        base_checks = {check.get("id"): check for check in base_cases[case_id].get("checks") or []}
        cand_checks = {check.get("id"): check for check in cand_cases[case_id].get("checks") or []}
        for check_id in sorted(set(base_checks) | set(cand_checks)):
            before = (base_checks.get(check_id) or {}).get("status")
            after = (cand_checks.get(check_id) or {}).get("status")
            if before == after:
                continue
            entry = {"case": case_id, "check": check_id, "before": before, "after": after}
            if before == CHECK_PASS and after != CHECK_PASS:
                regressions.append(entry)
            elif after == CHECK_PASS:
                improvements.append(entry)
            else:
                changed.append(entry)

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
        "metrics": deltas,
        "markdown": compare_markdown(baseline, candidate, problems, regressions, improvements, changed, deltas),
    }


def compare_markdown(baseline, candidate, problems, regressions, improvements, changed, deltas):
    lines = [
        "# Agent 产出质量评测对比",
        "",
        f"- 基线: `{baseline.get('revision')}`",
        f"- 候选: `{candidate.get('revision')}`",
        f"- 结论: {'无回退' if not regressions and not problems else '存在回退或不可比项'}",
        "",
    ]
    if problems:
        lines += ["## 不可比", ""] + [f"- {problem}" for problem in problems] + [""]
    if regressions:
        lines += ["## 回退", "", "| 用例 | 检查 | 基线 | 候选 |", "|---|---|---|---|"]
        lines += [f"| {item.get('case')} | {item.get('check')} | {item.get('before', '-')} | {item.get('after', '-')} |" for item in regressions]
        lines.append("")
    if improvements:
        lines += ["## 改善", ""] + [f"- `{item.get('case')}` / `{item.get('check')}`: {item.get('before')} → {item.get('after')}" for item in improvements] + [""]
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