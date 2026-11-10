#!/usr/bin/env python3
"""Agent output-quality evaluation against real upstreams (#238).

This is the paid half of the evaluation: it sends the fixed request set to a
real Agent and real generation providers, collects the delivered artifacts,
computes deterministic facts, and writes a report plus a visual-scoring packet.
It only runs when the person has agreed to the paid requests for this run:

    BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... ART_AI_LIVE_ACK=1 \
      python3 scripts/smoke/eval-agent-output.py --live --report <dir>

Without both --live and ART_AI_LIVE_ACK=1 it refuses (exit 2), and it refuses a
target released in the fake upstream shape. The free trajectory evaluation is
`smoke-fake-stack.py --only agent-quality` instead.

The visual rubric lives in scripts/smoke/agent-quality/rubric.json; scores are
filled by a person or a reviewer model and can be merged with --scores. The
script never calls a reviewer model itself and never chooses one.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)
import agent_quality as quality  # noqa: E402
import l5_common as common  # noqa: E402
import upstream_shape  # noqa: E402

HELPER = os.path.join(HERE, "inspect_model_eval.go")
SUCCESS = common.SUCCESS_STATE


def parse_arguments(argv):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--report", required=True, help="directory for the report, artifacts and scoring packet")
    parser.add_argument("--live", action="store_true", help="confirm real, paid upstream requests (also needs ART_AI_LIVE_ACK=1)")
    parser.add_argument("--turn-timeout", type=int, default=1800, help="seconds to wait for one chat turn")
    parser.add_argument("--task-timeout", type=int, default=2400, help="seconds to wait for expected tasks")
    parser.add_argument("--only", default="", help="comma-separated request ids (default: the whole set)")
    parser.add_argument("--scores", default="", help="JSON file of visual scores to merge into the report")
    return parser.parse_args(argv)


def offerings_by_id(live):
    items = live.offerings("text-to-image") + live.offerings("image-to-image")
    items += live.offerings("text-to-3d") + live.offerings("image-to-3d")
    return {item["id"]: item for item in items if item.get("id")}


def purpose_profile(schema, purpose):
    usage = ((schema or {}).get("chat") or {}).get("model3d_usage") or {}
    for profile in usage.get("profiles") or []:
        if profile.get("id") == purpose:
            return profile
    return {}


def pick_model(offerings, schema, purpose, capability):
    """A model the purpose recommends, or the first available one of the capability."""
    profile = purpose_profile(schema, purpose)
    recommended = (profile.get("recommended_offerings") or {}).get(capability) or []
    for offering_id in recommended:
        item = offerings.get(offering_id)
        if item and item.get("available") and item.get("model_id"):
            return item["model_id"]
    for item in offerings.values():
        if item.get("available") and item.get("capability") == capability and item.get("model_id"):
            return item["model_id"]
    return ""


def expected_counts(request):
    expected = request.get("expected") or {}
    minimums = expected.get("min_tasks") or {}
    return {capability: minimums.get(capability, 1) for capability in expected.get("capabilities") or []}


def choices_pending(live, session):
    return [card for card in live.pending_choices(session) if card.get("state") == "pending"]


def answer_choice(live, session, card, purpose, domain, model):
    options = [option for option in card.get("options") or [] if isinstance(option, dict) and option.get("id")]
    pick = next((option for option in options if purpose and option["id"] == purpose), None)
    pick = pick or next((option for option in options if option.get("recommended")), options[0] if options else None)
    if pick is None:
        return False
    status, _ = live.send(session, "按这个用途继续。", domain, model,
                          choiceAnswer={"choice_id": card["id"], "option_id": pick["id"]})
    return status in (200, 201, 202)


def record_card(cards, seen, kind, card):
    key = card.get("request_id") or card.get("id")
    if not key or key in seen:
        return False
    seen.add(key)
    cards.append({"kind": kind, "id": key, "tool": card.get("tool"), "card_kind": card.get("kind"),
                  "capability": card.get("capability"), "state": "approved" if kind != "choice" else "answered"})
    return True


def drive_turn(live, session, content, domain, model, purpose, cards, seen, args):
    """Send one message, answer cards, and wait for the turn to carry a final reply."""
    status, _ = live.send(session, content, domain, model)
    if status not in (200, 201, 202):
        return False
    end = time.time() + args.turn_timeout
    while time.time() < end:
        changed = False
        for card in choices_pending(live, session):
            if record_card(cards, seen, "choice", card):
                answer_choice(live, session, card, purpose, domain, model)
                changed = True
        for card in live.pending_approvals(session):
            kind = "plan" if card.get("kind") == "plan" else "approval"
            if record_card(cards, seen, kind, card):
                if kind == "plan":
                    live.decide_plan(session, card["request_id"], True)
                else:
                    live.decide_grant(card["request_id"], True)
                changed = True
        if changed:
            end = time.time() + args.turn_timeout
            continue
        outcome = live.client.read_chat_stream(session, min(30, max(1, end - time.time())))
        if outcome == "done":
            messages = live.messages(session)
            if common.reply_is_final(messages) and not choices_pending(live, session) and not live.pending_approvals(session):
                return True
        elif outcome == "error":
            time.sleep(2)
    return False


def run_case(live, request, schema, offerings, args):
    started = time.time()
    session = live.new_session(approval="auto")
    domain = "image" if request.get("modality") == "2d" else "model-3d"
    if request.get("modality") == "2d":
        capability = "text-to-image"
    elif any(step["tool"]["name"] in ("create_model_3d_from_image", "create_model_3d_from_views")
             for step in request.get("steps") or []):
        capability = "image-to-3d"
    else:
        capability = "text-to-3d"
    model = pick_model(offerings, schema, request.get("purpose"), capability)
    if request.get("modality") == "3d" and model:
        live.lock_model_3d(session, model)
    cards, seen = [], set()
    followups = list(request.get("live_followups") or [])
    turns = 0
    content = request["prompt"]
    counts = expected_counts(request)
    for attempt in range(len(followups) + 1):
        turns += 1
        turn_ok = drive_turn(live, session, content, domain, model, request.get("purpose"), cards, seen, args)
        # A turn that never finished is unlikely to start Tasks; do not wait the full task budget for it.
        tasks, _ = live.wait_tasks(session, counts, timeout=args.task_timeout if turn_ok else min(args.task_timeout, 120))
        messages = live.messages(session)
        tools = quality.collect_tools(messages)
        missing = [cap for cap, minimum in counts.items()
                   if sum(1 for task in tasks if task.get("capability") == cap and task.get("state") == SUCCESS) < minimum]
        need_render = request.get("modality") == "3d" and not any(tool["name"] == "render_model_views" for tool in tools)
        if not missing and not need_render:
            break
        if attempt >= len(followups):
            break
        content = followups[-1] if need_render and not missing else followups[attempt]
    messages = live.messages(session)
    tasks = live.tasks(session)
    tools = quality.collect_tools(messages)
    artifacts = os.path.join(args.report, "artifacts", request["id"])
    os.makedirs(artifacts, exist_ok=True)
    output_metrics, output_checks, renders = collect_outputs(live, request, tasks, messages, tools, artifacts,
                                                              schema, args.report)
    images = sum(1 for message in messages for attachment in message.get("attachments") or []
                 if isinstance(attachment, dict) and attachment.get("kind") == "image")
    return {
        "session_id": session,
        "messages": messages,
        "tasks": tasks,
        "cards": cards,
        "final_reply": next((message for message in reversed(messages)
                             if message.get("role") == "assistant" and (message.get("content") or "").strip()), {}),
        "image_count": images,
        "turns": turns,
        "elapsed_ms": int((time.time() - started) * 1000),
        "output_metrics": output_metrics,
        "output_checks": output_checks,
        "renders": renders,
    }


def safe_name(text, fallback="asset"):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", str(text or fallback)).strip("-")[:80] or fallback


def download(live, url, path):
    status, size = live.client.download(url, path)
    return status == 200 and size > 0, size


def model_asset(task):
    for asset in task.get("assets") or []:
        if not asset.get("required") or not asset.get("url"):
            continue
        media = str(asset.get("media_type") or "").lower()
        role = str(asset.get("role") or "").lower()
        if "gltf" in media or "glb" in media or media.startswith("model/") or role in ("model", "primary"):
            return asset
        if str(asset.get("url") or "").lower().split("?")[0].endswith((".glb", ".gltf")):
            return asset
    return None


def inspect_model(path):
    try:
        result = subprocess.run(["go", "run", HELPER, path], cwd=REPO, capture_output=True, text=True, timeout=600)
    except (OSError, subprocess.TimeoutExpired) as error:
        return None, str(error)
    if result.returncode != 0:
        return None, (result.stderr or result.stdout).strip()[:300]
    try:
        return json.loads(result.stdout), ""
    except ValueError:
        return None, result.stdout[:300]


def image_metrics_for_task(live, task, directory, report_root):
    found = []
    for asset in task.get("assets") or []:
        if not asset.get("required") or not asset.get("url"):
            continue
        media = str(asset.get("media_type") or "")
        if not media.startswith("image/"):
            continue
        status, data = live.client.fetch(asset["url"])
        if status != 200 or not data:
            continue
        extension = "png" if "png" in media else "jpg" if "jpeg" in media or "jpg" in media else "webp" if "webp" in media else "img"
        path = os.path.join(directory, f"{safe_name(task.get('id'))}-{safe_name(asset.get('role'))}.{extension}")
        with open(path, "wb") as handle:
            handle.write(data)
        found.append({"task_id": task.get("id"), "role": asset.get("role"),
                      "path": os.path.relpath(path, report_root), "metrics": quality.image_metrics(data)})
    return found


def collect_renders(live, messages, directory, report_root):
    """The rendered views the reply attached (role=render), as files under the report."""
    found = []
    for message in messages or []:
        for attachment in message.get("attachments") or []:
            if not isinstance(attachment, dict) or attachment.get("kind") != "image" or attachment.get("role") != "render":
                continue
            asset_id = attachment.get("asset_id")
            if not asset_id:
                continue
            path = os.path.join(directory, f"render-{safe_name(attachment.get('handle') or len(found) + 1)}.png")
            ok, _ = download(live, f"/api/v1/assets/{asset_id}", path)
            if ok:
                found.append({"label": attachment.get("handle") or f"view-{len(found) + 1}",
                              "asset_id": asset_id, "path": os.path.relpath(path, report_root)})
    return found


def collect_outputs(live, request, tasks, messages, tools, directory, schema, report_root):
    output = {"tasks": [], "renders": []}
    checks = []
    renders = collect_renders(live, messages, directory, report_root)
    output["renders"] = [{"label": render["label"], "path": render["path"]} for render in renders]
    inspect_summary = next((tool.get("summary") for tool in tools if tool["name"] == "inspect_model" and tool.get("summary")), None)
    if request.get("modality") == "2d":
        images = []
        for task in tasks:
            if task.get("state") == SUCCESS:
                images.extend(image_metrics_for_task(live, task, directory, report_root))
        output["images"] = images
        output["renders"] = [{"label": f"image-{index + 1}", "path": image["path"]} for index, image in enumerate(images)]
        background = (request.get("output") or {}).get("background")
        for image in images:
            metrics = image["metrics"]
            checks.append({"id": f"output:image:{image['task_id']}", "status": quality.CHECK_PASS,
                           "detail": f"{metrics.get('width')}x{metrics.get('height')} {metrics.get('format')}"
                                     f", subject {metrics.get('opaque_ratio')}, edge {metrics.get('edge_contact')}"})
            if background == "transparent":
                ok = bool(metrics.get("alpha")) and bool(metrics.get("nonblank")) and (metrics.get("opaque_ratio") or 0) < 0.98
                checks.append({"id": f"output:background:{image['task_id']}", "status": quality.CHECK_PASS if ok else quality.CHECK_FAIL,
                               "detail": f"alpha={metrics.get('alpha')}, subject={metrics.get('opaque_ratio')}"})
        return output, checks, output["renders"]
    budget = quality.purpose_budget(schema, request.get("purpose")) or {}
    for task in tasks:
        if task.get("state") != SUCCESS or task.get("capability") not in ("text-to-3d", "image-to-3d", "multiview-to-3d"):
            continue
        asset = model_asset(task)
        if not asset:
            checks.append({"id": f"output:model:{task.get('id')}", "status": quality.CHECK_FAIL, "detail": "no model asset"})
            continue
        path = os.path.join(directory, f"{safe_name(task.get('id'))}-{safe_name(asset.get('role'))}.glb")
        ok, size = download(live, asset["url"], path)
        if not ok:
            checks.append({"id": f"output:model:{task.get('id')}", "status": quality.CHECK_FAIL, "detail": "model download failed"})
            continue
        facts, error = inspect_model(path)
        if facts is None:
            checks.append({"id": f"output:model:{task.get('id')}", "status": quality.CHECK_FAIL, "detail": f"inspect failed: {error}"})
            continue
        output["model"] = {"task_id": task.get("id"), "path": os.path.relpath(path, report_root), "bytes": size,
                           "facts": facts, "inspect_summary": inspect_summary}
        output["tasks"].append({"task_id": task.get("id"), "capability": task.get("capability"),
                                "model_id": task.get("model_id"), "bytes": size})
        checks.extend(quality.model_budget_checks(facts, budget))
    return output, checks, output["renders"]


def rubric_context(rubric, request, case):
    axis_ids = rubric.get("per_request_axes", {}).get(request["id"], [])
    axes = [rubric["axes"][axis] for axis in axis_ids if axis in rubric.get("axes", {})]
    scale = "\n".join(f"{score}: {label}" for score, label in (rubric.get("scale") or {}).get("labels", {}).items())
    renders = case.get("metrics", {}).get("output", {}).get("renders") or []
    return {
        "title": request["title"],
        "prompt": request["prompt"],
        "purpose": request.get("purpose") or "（2D，无 3D 用途）",
        "deliverables": "、".join(request.get("deliverables") or []),
        "reference_images": "无" if not request.get("reference_images") else "\n".join(request["reference_images"]),
        "rendered_views": "\n".join(f"- {render.get('label')}: `{render['path']}`" for render in renders)
                          or "（未收集到渲染图）",
        "axes": "\n".join(f"- {axis['label']}（{axis_id}）：{axis['description']}" for axis_id, axis in zip(axis_ids, axes)),
        "scale": scale,
    }


def write_scoring(report, rubric_path, requests, report_root, scores_path=""):
    rubric = quality.load_json(rubric_path)
    scores = quality.load_json(scores_path) if scores_path else {}
    packet = {"rubric_version": rubric.get("version"), "rubric_sha256": quality.file_sha256(rubric_path),
              "prompt_template": rubric.get("prompt_template"), "cases": []}
    for case in report.get("cases") or []:
        request = requests.get(case["id"])
        if not request:
            continue
        context = rubric_context(rubric, request, case)
        try:
            prompt = rubric["prompt_template"].format(**context)
        except KeyError as error:
            prompt = f"（量表缺少占位符：{error}）"
        axis_ids = rubric.get("per_request_axes", {}).get(request["id"], [])
        packet["cases"].append({
            "id": case["id"], "title": request["title"], "prompt": request["prompt"],
            "purpose": request.get("purpose"), "deliverables": request.get("deliverables") or [],
            "axes": axis_ids, "rendered_views": case.get("metrics", {}).get("output", {}).get("renders") or [],
            "scoring_prompt": prompt, "scores": scores.get(case["id"]),
        })
        if scores.get(case["id"]):
            case["visual_scores"] = scores[case["id"]]
    packet_path = os.path.join(report_root, "scoring-packet.json")
    with open(packet_path, "w", encoding="utf-8") as handle:
        json.dump(packet, handle, ensure_ascii=False, indent=2)
    sheet_path = os.path.join(report_root, "scoring-sheet.md")
    with open(sheet_path, "w", encoding="utf-8") as handle:
        handle.write(scoring_markdown(packet))
    return packet_path, sheet_path


def scoring_markdown(packet):
    lines = [f"# Agent 产出质量视觉评分（量表 v{packet.get('rubric_version')}）", "",
             "按每个用例的 `scoring_prompt` 评分，把结果写入 `scores.json` 后用 `--scores` 合并。", ""]
    for case in packet.get("cases") or []:
        lines += [f"## {case['id']} — {case['title']}", "", f"**用户话术**：{case['prompt']}", ""]
        for render in case.get("rendered_views") or []:
            lines += [f"![{render.get('label')}]({render.get('path')})", ""]
        lines += ["**评分提示词**：", "", "```", case.get("scoring_prompt") or "", "```", ""]
    return "\n".join(lines) + "\n"


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    _shape, refusal = upstream_shape.resolve("live-only", "--live" in argv, os.environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    missing = common.missing_environment(os.environ)
    if missing:
        print("missing environment: " + ", ".join(missing), file=sys.stderr)
        return 2
    problem = common.validate_base(os.environ["BASE"])
    if problem:
        print(problem, file=sys.stderr)
        return 2
    args = parse_arguments(argv)
    for name in ("turn_timeout", "task_timeout"):
        if getattr(args, name) < 30:
            print(f"--{name.replace('_', '-')} must be at least 30 seconds", file=sys.stderr)
            return 2
    data, path, digest = quality.load_requests()
    requests = {request["id"]: request for request in data["requests"]}
    selected = [request for request in data["requests"] if not args.only or request["id"] in args.only.split(",")]
    if not selected:
        print("no request selected", file=sys.stderr)
        return 2
    report = common.Report("agent-quality-output-run", args.report)
    live = common.Live(common.Client(os.environ["BASE"]), report, args)
    if not live.login():
        return 2
    schema = live.public_schema()
    offerings = offerings_by_id(live)
    cases = []
    try:
        for request in selected:
            run = run_case(live, request, schema, offerings, args)
            case = quality.evaluate_case(request, run)
            cases.append(case)
            failed = quality.case_hard_failures(case)
            report.check(f"agent-quality-output {request['id']}", not failed,
                         f"tools={case['metrics']['tool_call_count']} paid={case['metrics']['paid_call_count']} "
                         f"cards={case['metrics']['interruption_count']}" + ("" if not failed else "; " + "; ".join(
                             f"{check['id']}: {check['detail']}" for check in failed)))
    except KeyboardInterrupt:
        report.failed("interrupted")
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {common.one_line(str(error), 120)}")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO, text=True).strip())
    rubric_path = os.path.join(HERE, "agent-quality", "rubric.json")
    rubric = {"version": quality.load_json(rubric_path).get("version"), "sha256": quality.file_sha256(rubric_path)}
    summary = quality.summarize(cases, "output-live", revision, dirty,
                                {"path": os.path.relpath(path, REPO), "version": data["version"], "sha256": digest},
                                rubric=rubric)
    os.makedirs(args.report, exist_ok=True)
    packet_path, sheet_path = write_scoring(summary, rubric_path, requests, args.report, args.scores)
    json_path, markdown_path = quality.write_report(summary, args.report, "agent-quality-output")
    print(f"# output report: {json_path} and {markdown_path}", flush=True)
    print(f"# scoring packet: {packet_path} and {sheet_path}", flush=True)
    return report.finish()


if __name__ == "__main__":
    sys.exit(main())