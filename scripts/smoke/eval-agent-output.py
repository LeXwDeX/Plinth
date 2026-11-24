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

The report states the upstream shape, which metric groups it can compare and
which it cannot (a fake generation side delivers placeholder artifacts), and
whether a stored baseline exists (#498). The preference checks are skipped here
because this runner never sends feedback actions, and the request set's budget
thresholds are recorded without deciding PASS/FAIL.

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

# #498: this runner sends the fixed request set's user prompts and answers the
# cards the Agent asks about; it never sends the feedback actions (pick, reject,
# compact, edit, delete) the #241 preference checks measure, because the live
# request set has no feedback step. Every run says so, so those checks are
# skipped instead of failing for something the runner never exercised.
DRIVES_FEEDBACK = False


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


def answer_choice(live, session, card, purpose, domain, model, stats):
    options = [option for option in card.get("options") or [] if isinstance(option, dict) and option.get("id")]
    pick = next((option for option in options if purpose and option["id"] == purpose), None)
    pick = pick or next((option for option in options if option.get("recommended")), options[0] if options else None)
    if pick is None:
        return False
    status, _ = send_message(live, session, "按这个用途继续。", domain, model, stats,
                             extra={"choiceAnswer": {"choice_id": card["id"], "option_id": pick["id"]}})
    return status in (200, 201, 202)


def record_card(cards, seen, kind, card):
    key = card.get("request_id") or card.get("id")
    if not key or key in seen:
        return False
    seen.add(key)
    cards.append({"kind": kind, "id": key, "tool": card.get("tool"), "card_kind": card.get("kind"),
                  "capability": card.get("capability"), "state": "approved" if kind != "choice" else "answered"})
    return True


# --------------------------------------------------------- sending messages

# The server refuses a chat message while the session runs a turn (409, the
# catalog's `chat.presentation.messages.turn_active`). A terminal Task does not
# mean the turn ended: the Agent keeps writing after its last Task, so the
# evaluation's follow-ups and card answers were refused (#444). The refused
# message was never accepted, so waiting and sending the same body again
# duplicates nothing; every wait and retry is counted so the report shows what
# the waiting cost instead of hiding it in a sleep.
IDLE_POLL_SECONDS = 5
IDLE_WAIT_SECONDS = 300
BUSY_RETRY_SECONDS = 15
BUSY_RETRY_LIMIT = 12
BUSY_MESSAGE_MARKERS = ("仍有任务正在处理", "still has a task in progress")


def sending():
    """The send counters of one case: waits for the turn, busy retries, rejections."""
    return {"sends": 0, "idle_waits": 0, "idle_wait_ms": 0, "busy_retries": 0, "busy_wait_ms": 0, "rejected": 0}


def session_busy(status, data):
    """Whether a 409 means "the session still runs a turn" and is worth retrying.

    A card answer can also be refused with 409 (`choice_answered`, "这个选择已经
    作答或已失效"); retrying that one only burns the turn budget, so the retry
    keys on the turn-active text in both locales (or on a 409 with no body).
    """
    if status != 409:
        return False
    text = json.dumps(data or {}, ensure_ascii=False)
    if any(marker in text for marker in BUSY_MESSAGE_MARKERS):
        return True
    return text.strip() in ("", "{}")


def turn_running(live, session, probe=IDLE_POLL_SECONDS):
    """Whether the session still runs a turn: no final reply yet, or the stream is still open.

    A pending choice or approval card is how a turn ends, so a card alone does
    not count as running.
    """
    if not common.reply_is_final(live.messages(session)):
        return True
    return live.client.read_chat_stream(session, probe) != "done"


def wait_turn_end(live, session, stats, timeout=IDLE_WAIT_SECONDS, poll=IDLE_POLL_SECONDS, sleep=time.sleep):
    """Wait (bounded) until the session's turn is over; False when the wait timed out.

    A timeout is not an error: the caller still sends the message and a 409 is
    retried, so a turn that never ends cannot silently drop the follow-up.
    """
    if not turn_running(live, session):
        return True
    started = time.time()
    stats["idle_waits"] += 1
    while turn_running(live, session):
        elapsed = time.time() - started
        if elapsed >= timeout:
            stats["idle_wait_ms"] += int(elapsed * 1000)
            return False
        sleep(poll)
    stats["idle_wait_ms"] += int((time.time() - started) * 1000)
    return True


def send_message(live, session, content, domain, model, stats, extra=None,
                 idle_timeout=IDLE_WAIT_SECONDS, retry_seconds=BUSY_RETRY_SECONDS,
                 retry_limit=BUSY_RETRY_LIMIT, poll=IDLE_POLL_SECONDS, sleep=time.sleep):
    """Send one message once the running turn ends, retrying while the session is busy.

    Before the first message of a session there is no turn to wait for. A final
    409 is returned as-is so the caller records a lost turn rather than
    pretending the message landed.
    """
    extra = dict(extra or {})
    if "choiceAnswer" not in extra:
        # Same bookkeeping as common.Live.send: wait_tasks answers a later card
        # with the request fields of the last message.
        live._last_send[session] = (domain, model, dict(extra))
    if stats["sends"]:
        wait_turn_end(live, session, stats, timeout=idle_timeout, poll=poll, sleep=sleep)
    body = {"content": content, "creative_domain": domain, "ai_model": model}
    body.update(extra)
    status, data = 0, {}
    for attempt in range(retry_limit + 1):
        status, data = live.client.call("POST", f"/api/chat/sessions/{session}/messages", body)
        if not session_busy(status, data):
            break
        stats["busy_retries"] += 1
        if attempt >= retry_limit:
            break
        sleep(retry_seconds)
        stats["busy_wait_ms"] += int(retry_seconds * 1000)
    stats["sends"] += 1
    if status not in (200, 201, 202):
        stats["rejected"] += 1
        print(f"message rejected: HTTP {status} {common.one_line(json.dumps(data, ensure_ascii=False), 160)}",
              flush=True)
    return status, data


def drive_turn(live, session, content, domain, model, purpose, cards, seen, args, stats):
    """Send one message, answer cards, and wait for the turn to carry a final reply."""
    status, _ = send_message(live, session, content, domain, model, stats)
    if status not in (200, 201, 202):
        return False
    end = time.time() + args.turn_timeout
    while time.time() < end:
        changed = False
        for card in choices_pending(live, session):
            if record_card(cards, seen, "choice", card):
                answer_choice(live, session, card, purpose, domain, model, stats)
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
    sends = sending()
    followups = list(request.get("live_followups") or [])
    turns = 0
    content = request["prompt"]
    counts = expected_counts(request)
    for attempt in range(len(followups) + 1):
        turns += 1
        turn_ok = drive_turn(live, session, content, domain, model, request.get("purpose"), cards, seen, args,
                             sends)
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
        "sends": sends,
        "elapsed_ms": int((time.time() - started) * 1000),
        "output_metrics": output_metrics,
        "output_checks": output_checks,
        "renders": renders,
        # #498: see DRIVES_FEEDBACK — evaluate_case turns this into skipped
        # preference checks instead of failures.
        "preference_supported": DRIVES_FEEDBACK,
    }


def safe_name(text, fallback="asset"):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", str(text or fallback)).strip("-")[:80] or fallback


def download(live, url, path):
    status, size = live.client.download(url, path)
    return status == 200 and size > 0, size


# ----------------------------------------------------------- model selection

# #444: a 3D task carries several required model files, and Meshy lists the 3MF
# first (3MF, FBX, GLB, OBJ, STL, USDZ). Media types do not tell them apart
# (`application/x-tgif` for OBJ, `application/zip` for USDZ), so the public
# `format` name of the task projection (#327) decides. Only binary glTF (GLB)
# can be inspected; the JSON form is the second choice, and a mislabeled GLB
# still gets inspected because its container decides.
GLTF_FORMATS = ("glb", "gltf")
KNOWN_MODEL_FORMATS = ("glb", "gltf", "fbx", "obj", "stl", "usdz", "3mf")
FORMAT_EXTENSIONS = {"glb": ".glb", "gltf": ".gltf", "fbx": ".fbx", "obj": ".obj",
                     "stl": ".stl", "usdz": ".usdz", "3mf": ".3mf"}
FORMAT_LABELS = {"glb": "GLB", "gltf": "glTF", "fbx": "FBX", "obj": "OBJ",
                 "stl": "STL", "usdz": "USDZ", "3mf": "3MF"}
MEDIA_TYPE_FORMATS = {"model/gltf-binary": "glb", "model/gltf+json": "gltf"}
GLB_MAGIC = b"glTF"


def asset_format(asset):
    """The model format of a task asset: its public `format` name, else the media type, else the URL suffix."""
    named = str(asset.get("format") or "").strip().lower()
    if named in KNOWN_MODEL_FORMATS:
        return named
    media = str(asset.get("media_type") or "").split(";")[0].strip().lower()
    if media in MEDIA_TYPE_FORMATS:
        return MEDIA_TYPE_FORMATS[media]
    suffix = os.path.splitext(str(asset.get("url") or "").split("?")[0])[1].lower()
    for name in KNOWN_MODEL_FORMATS:
        if suffix == FORMAT_EXTENSIONS[name]:
            return name
    return named


def is_model_asset(asset):
    """Whether the asset is one of the task's model files (the old selection rule)."""
    media = str(asset.get("media_type") or "").split(";")[0].strip().lower()
    role = str(asset.get("role") or "").strip().lower()
    if role in ("model", "primary") or media.startswith("model/") or "gltf" in media or "glb" in media:
        return True
    return str(asset.get("url") or "").lower().split("?")[0].endswith((".glb", ".gltf"))


def model_asset(task):
    """(asset, format) of the model file to inspect: the GLB first, then glTF (#444).

    The old rule took the first required `model` asset, which is Meshy's 3MF;
    `modelinspect` then refused that ZIP as "bad GLB magic" and every 3D case's
    output:model check was wrong. A task with no glTF asset returns its first
    model file with its own format, so the check can name what was delivered
    instead of handing a non-glTF file to the GLB parser.
    """
    models = [(asset_format(asset), asset) for asset in task.get("assets") or []
              if asset.get("required") and asset.get("url") and is_model_asset(asset)]
    for wanted in GLTF_FORMATS:
        for name, asset in models:
            if name == wanted:
                return asset, name
    return (models[0][1], models[0][0]) if models else (None, "")


def glb_container(path):
    """Whether the downloaded file starts with the GLB container magic."""
    try:
        with open(path, "rb") as handle:
            return handle.read(4) == GLB_MAGIC
    except OSError:
        return False


def unsupported_model_detail(asset, name):
    """Why a delivered model file cannot be inspected, or "" when it is glTF.

    #444: naming the delivered format keeps a 3MF ZIP from looking like a
    corrupt GLB; the GLB parser only gets glTF files.
    """
    if name in GLTF_FORMATS:
        return ""
    return (f"unsupported format: {asset.get('format') or name or 'unknown'}"
            f" ({asset.get('media_type') or 'no media type'});"
            " the task has no GLB or glTF asset to inspect")


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
    # The final deliverable is the last image-producing Task; the API may list newest first.
    tasks = sorted(tasks, key=lambda task: str(task.get("created_at") or ""))
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
        for index, image in enumerate(images):
            metrics = image["metrics"]
            checks.append({"id": f"output:image:{image['task_id']}", "status": quality.CHECK_PASS,
                           "detail": f"{metrics.get('width')}x{metrics.get('height')} {metrics.get('format')}"
                                     f", subject {metrics.get('opaque_ratio')}, edge {metrics.get('edge_contact')}"})
            # The deliverable is the last image (a background-removal step may follow the generation).
            if background == "transparent" and index == len(images) - 1:
                ok = bool(metrics.get("alpha")) and bool(metrics.get("nonblank")) and (metrics.get("opaque_ratio") or 0) < 0.98
                checks.append({"id": f"output:background:{image['task_id']}", "status": quality.CHECK_PASS if ok else quality.CHECK_FAIL,
                               "detail": f"alpha={metrics.get('alpha')}, subject={metrics.get('opaque_ratio')}"})
        return output, checks, output["renders"]
    budget = quality.purpose_budget(schema, request.get("purpose")) or {}
    for task in tasks:
        if task.get("state") != SUCCESS or task.get("capability") not in ("text-to-3d", "image-to-3d", "multiview-to-3d"):
            continue
        check_id = f"output:model:{task.get('id')}"
        asset, name = model_asset(task)
        if not asset:
            checks.append({"id": check_id, "status": quality.CHECK_FAIL, "detail": "no model asset"})
            continue
        unsupported = unsupported_model_detail(asset, name)
        if unsupported:
            checks.append({"id": check_id, "status": quality.CHECK_FAIL, "detail": unsupported})
            continue
        label = safe_name(f"{name}-{asset.get('role') or 'model'}")
        path = os.path.join(directory, f"{safe_name(task.get('id'))}-{label}{FORMAT_EXTENSIONS[name]}")
        ok, size = download(live, asset["url"], path)
        if not ok:
            checks.append({"id": check_id, "status": quality.CHECK_FAIL, "detail": "model download failed"})
            continue
        if name == "gltf" and not glb_container(path):
            checks.append({"id": check_id, "status": quality.CHECK_FAIL,
                           "detail": f"unsupported format: glTF JSON ({asset.get('media_type') or 'model/gltf+json'});"
                                     " the inspector reads GLB binary glTF only"})
            continue
        facts, error = inspect_model(path)
        if facts is None:
            checks.append({"id": check_id, "status": quality.CHECK_FAIL, "detail": f"inspect failed: {error}"})
            continue
        output["model"] = {"task_id": task.get("id"), "format": FORMAT_LABELS.get(name, name.upper()),
                           "path": os.path.relpath(path, report_root),
                           "bytes": size, "facts": facts, "inspect_summary": inspect_summary}
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


def run(argv):
    shape, refusal = upstream_shape.resolve("chat-live", "--live" in argv, os.environ)
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
    asked = [name.strip() for name in args.only.split(",") if name.strip()] if args.only else []
    unknown = [name for name in asked if name not in requests]
    if unknown:
        # #498: an unknown id used to be dropped silently, so a typo ran a subset
        # (or nothing) and still exited 0.
        print("unknown request id: " + ", ".join(unknown) + "; known: " + ", ".join(sorted(requests)), file=sys.stderr)
        return 2
    selected = [request for request in data["requests"] if not asked or request["id"] in asked]
    if not selected:
        print("no request selected", file=sys.stderr)
        return 2
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO, text=True).strip())
    report = common.Report("agent-quality-output-run", args.report)
    cases = []
    try:
        live = common.Live(common.Client(os.environ["BASE"]), report, args)
        if not live.login():
            print("error: login failed; check BASE, ADMIN_USERNAME and ADMIN_PASSWORD", file=sys.stderr)
            return 2
        schema = live.public_schema()
        offerings = offerings_by_id(live)
        for request in selected:
            case_run = run_case(live, request, schema, offerings, args)
            case = quality.evaluate_case(request, case_run)
            cases.append(case)
            failed = quality.case_hard_failures(case)
            skipped = [check for check in case["checks"] if check.get("status") == quality.CHECK_SKIP]
            recorded = [check for check in case["checks"] if check.get("status") == quality.CHECK_INFO]
            report.check(f"agent-quality-output {request['id']}", not failed,
                         f"tools={case['metrics']['tool_call_count']} paid={case['metrics']['paid_call_count']} "
                         f"cards={case['metrics']['interruption_count']}"
                         + (f" skipped={len(skipped)}" if skipped else "")
                         + (f" recorded={len(recorded)}" if recorded else "")
                         + ("" if not failed else "; " + "; ".join(
                             f"{check['id']}: {check['detail']}" for check in failed)))
    except KeyboardInterrupt:
        report.failed("interrupted")
        print("error: interrupted before every case ran", file=sys.stderr)
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {common.one_line(str(error), 120)}")
        print(f"error: {type(error).__name__}: {common.one_line(str(error), 120)}", file=sys.stderr)
    rubric_path = os.path.join(HERE, "agent-quality", "rubric.json")
    rubric = {"version": quality.load_json(rubric_path).get("version"), "sha256": quality.file_sha256(rubric_path)}
    summary = quality.summarize(cases, "output-live", revision, dirty,
                                {"path": os.path.relpath(path, REPO), "version": data["version"], "sha256": digest},
                                rubric=rubric, shape=shape)
    os.makedirs(args.report, exist_ok=True)
    packet_path, sheet_path = write_scoring(summary, rubric_path, requests, args.report, args.scores)
    json_path, markdown_path = quality.write_report(summary, args.report, "agent-quality-output")
    # #498: the shape and the baseline go to the terminal too, so a run whose
    # generation side was fake or that has no stored baseline does not read like
    # a comparable PASS=13 run.
    print(f"# upstream shape: {summary['upstream_shape']}; "
          f"comparable: {', '.join(summary['comparable_metrics']) or '-'}; "
          f"incomparable: {', '.join(summary['incomparable_metrics']) or '-'}", flush=True)
    print(f"# baseline: {summary['baseline']['note']}", flush=True)
    print(f"# output report: {json_path} and {markdown_path}", flush=True)
    print(f"# scoring packet: {packet_path} and {sheet_path}", flush=True)
    return report.finish()


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        return run(argv)
    except KeyboardInterrupt:
        # #498: a silent ^C leaves no summary and no line saying what happened.
        print("error: interrupted; the run did not finish", file=sys.stderr)
        return 130
    except Exception as error:  # a crash is a failure, never a silent exit
        print(f"error: {type(error).__name__}: {common.one_line(str(error), 200)}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())