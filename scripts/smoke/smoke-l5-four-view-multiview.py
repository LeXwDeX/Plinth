#!/usr/bin/env python3
"""L5: reproduce the art report of #417, four views -> Meshy 7.1 multiview 3D (#417).

The person reported that, with the workspace 3D picker on one model, four
views finished but the plan's fifth step ("image to 3D, Meshy 7.1 multiview")
stayed waiting and the turn did not end. This script runs the same shape with a
real Agent and real models:

  1. A fresh session in the budget approval mode (a new session defaults to
     auto since #406), the 3D picker locked to meshy-7.1 (the single-image
     model the person had picked) and the 2D picker locked to a 6-credit image
     model. One character picture (--image, or --generate-image for one extra
     paid image) is uploaded and sent with a natural-language four-view
     request.
  2. The pending production plan card is recorded (budgets, estimated totals,
     steps) and approved on the Schema's chat_session_budget route exactly as
     the browser does. The right view is optional in the recipe, so the server
     never starts it by itself: once the front view is accepted and the right
     slot says it can start, the script POSTs the same start request the card's
     button sends (429 retried). A per-step approval card outside the plan is
     recorded; an image-to-image one is approved as the person would, while an
     image-to-3D one is left pending, because that is the reported symptom.
  3. After the four views the plan's 3D step is awaited. When the turn ended
     without running it, one plain model-3D message asks the Agent to continue
     (no structured multiview request is sent), so the tool call the Agent
     chooses -- create_model_3d_from_views with or without a model argument --
     is the evidence.
  4. --control (default on) runs the same picture in a fresh auto-mode session
     locked to the single-image meshy-7.1 and requires one image-to-3D Task to
     SUCCEED.

The script reports a verdict instead of treating the reproduction as a crash:
"reproduced" (four views done, no multi-view Task), "not_reproduced" (a
multi-view Task SUCCEEDED) or "inconclusive" (the flow could not reach that
decision: no plan, a view failed, a Task ran and failed, ...). --report writes
the usual PASS/FAIL/SKIP report plus <name>-evidence.json with the tool calls
and their arguments, the plan/card facts, every Task, the per-step plan quote
next to each Task's native quote (per unit), the group's view states and the
timings. The server-side refusal codes ("agent tool call refused") are read
from the stack log by the caller; the public wire keeps no tool result today.

Cost: four views (4 x one 6-credit image) + one multi-view 3D + one control 3D
(+ one image when --generate-image is used). --max-paid bounds the Task count
and the script stops before a phase that would exceed it; it never replays a
consumed request. Needs both --live and ART_AI_LIVE_ACK=1 and a live release;
see README.md (L5).
"""
import argparse
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

REQUEST = ("先用这张图做四视图（front/left/back/right）：按正面、左侧、背面、右侧的顺序各出一张，"
           "左右按角色自己的身体算；四张都完成后，再用这组四视图生成一个 3D 模型。")
FOLLOW_UP = "四视图已经全部完成。请用这组四视图（正面、左侧、背面、右侧）继续生成一个 3D 模型。"
CONTROL = "用这张角色图生成一个 3D 模型。"
CHARACTER = ("生成一张Q版小狐狸角色的正面全身立绘，白色背景。小狐狸的右手（它自己的右手）高举一面红色小旗，"
             "左手空着垂在身侧，左耳戴一朵黄色小花。")
VIEW_KEYS = ("front", "left", "back", "right")
VIEW_TERMINAL = ("succeeded", "failed", "canceled", "denied", "rejected")
POLL_SECONDS = 10
RIGHT_RETRY_SECONDS = 20
RIGHT_MAX_REQUESTS = 30
MAX_REFERENCE_BYTES = 20_000_000
DEFAULT_MAX_PAID = 6  # four views + one multi-view 3D + one control 3D


# ------------------------------------------------------------------ pure logic

def extra_arguments(parser):
    parser.add_argument("--image", default=os.environ.get("L5_FOUR_VIEW_IMAGE", ""),
                        help="local character picture (PNG/JPEG/WebP) uploaded as the source")
    parser.add_argument("--generate-image", action="store_true",
                        help="generate the character picture instead (one extra paid image, counts against --max-paid)")
    parser.add_argument("--max-paid", type=int, default=int(os.environ.get("L5_FOUR_VIEW_MAX_PAID", DEFAULT_MAX_PAID)),
                        help=f"paid Task ceiling of the whole run (default {DEFAULT_MAX_PAID}; see the docstring)")
    parser.add_argument("--control", action=argparse.BooleanOptionalAction, default=True,
                        help="run the single-view image-to-3D control group (default on)")
    parser.add_argument("--model3d", default=os.environ.get("L5_FOUR_VIEW_MODEL", "meshy-7.1"),
                        help="model_id the workspace 3D picker is locked to (default meshy-7.1)")
    parser.add_argument("--multiview-model", default=os.environ.get("L5_MULTIVIEW_MODEL", "meshy-7.1-multiview"),
                        help="model_id of the expected multi-view Offering (recorded, not forced)")


def image_media_type(data):
    """(media type, file extension) of PNG, JPEG or WebP bytes, or (None, None)."""
    if data[:8] == b"\x89PNG\r\n\x1a\n":
        return "image/png", "png"
    if data[:2] == b"\xff\xd8":
        return "image/jpeg", "jpg"
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return "image/webp", "webp"
    return None, None


def image_model_for_cost(offerings, credits=6, preferred="openai/gpt-image-2/text-to-image"):
    """A 6-credit text-to-image Offering: the preferred id when it is still that price, else the
    first available one at that price. The picker is locked to its model_id."""
    available = [item for item in offerings if item.get("available") and item.get("model_id")]
    priced = [item for item in available if item.get("estimated_credits") == credits]
    for item in priced:
        if item.get("id") == preferred:
            return item
    return priced[0] if priced else None


def single_view_offering(offerings, model_id):
    """The available image-to-3D Offering of a single-image model (not a multi-view one)."""
    for item in offerings:
        if (item.get("available") and item.get("capability") == "image-to-3d"
                and item.get("model_id") == model_id and "multiview" not in str(item.get("id", ""))):
            return item
    return None


def multiview_offering(offerings, model_id):
    """The expected multi-view Offering: the named model when it is an available multi-view
    Offering, else the first available one (recorded; the Agent decides what it calls)."""
    multiview = [item for item in offerings
                 if item.get("available") and "multiview" in str(item.get("id", ""))]
    for item in multiview:
        if item.get("model_id") == model_id:
            return item
    return multiview[0] if multiview else None


def plan_view(card):
    """The plan card facts kept in the evidence: what the person saw before approving (#417)."""
    card = card if isinstance(card, dict) else {}
    steps = ((card.get("plan") or {}).get("steps") or []) if isinstance(card.get("plan"), dict) else []
    return {
        "request_id": card.get("request_id") or card.get("id"),
        "kind": card.get("kind"),
        "tool": card.get("tool"),
        "title": (card.get("plan") or {}).get("title"),
        "budgets": card.get("budgets"),
        "estimated_totals": card.get("estimated_totals"),
        "steps": [{"capability": step.get("capability"), "offering_id": step.get("offering_id"),
                   "purpose": step.get("purpose"), "estimate": step.get("estimate")}
                  for step in steps if isinstance(step, dict)],
        "problems": common.plan_card_problems(card),
    }


def quote_of_task(task, prices):
    """The Task's own frozen native quote, or its accepting tool call's price, as
    {unit, unit_label, total_amount, source}; None when nothing public prices it."""
    quote = (task or {}).get("native_quote")
    if isinstance(quote, dict) and quote.get("unit"):
        return {"unit": quote.get("unit"), "unit_label": quote.get("unit_label"),
                "total_amount": str(quote.get("total_amount")), "source": "task"}
    price = (prices or {}).get((task or {}).get("id"))
    if price:
        return {"unit": price[0], "unit_label": None, "total_amount": str(price[1]), "source": "tool_call"}
    return None


def task_view_refs(task):
    """(pictures, view labels) the frozen input of an image-to-3D Task carries, when the wire has
    them: `reference_assets` and the labelled `reference_views`. (0, None) when the input carries
    none (for example another offering's input shape)."""
    parameters = common.frozen_parameters(task)
    assets = parameters.get("reference_assets")
    if isinstance(assets, str):
        assets = [assets]
    pictures = len(assets) if isinstance(assets, list) else 0
    views = parameters.get("reference_views")
    return pictures, views if isinstance(views, list) else None


def plan_quote_rows(card, tasks, prices=None):
    """Pair each plan step with the Task that ran it, in order, and compare the plan's estimate
    with that Task's native quote per unit. A plan step without a Task stays in the rows; a Task
    no step claims is listed separately. Returns (rows, unmatched_task_ids)."""
    if isinstance(card, dict):
        plan = card.get("plan") if isinstance(card.get("plan"), dict) else {}
        steps = plan.get("steps") if isinstance(plan.get("steps"), list) else []
    else:
        steps = []
    used, rows = set(), []
    for index, step in enumerate(steps):
        step = step if isinstance(step, dict) else {}
        match = None
        for task in tasks or []:
            if task.get("id") in used or task.get("capability") != step.get("capability"):
                continue
            if step.get("offering_id") and task.get("offering_id") != step.get("offering_id"):
                continue
            match = task
            break
        estimate = step.get("estimate") if isinstance(step.get("estimate"), dict) else None
        row = {
            "step": index + 1,
            "capability": step.get("capability"),
            "plan_offering_id": step.get("offering_id"),
            "plan_estimate": estimate,
            "task_id": None,
            "task_offering_id": None,
            "task_model_id": None,
            "task_native_quote": None,
            "delta": None,
            "comparable": False,
        }
        if match is not None:
            used.add(match.get("id"))
            quote = quote_of_task(match, prices)
            row.update({"task_id": match.get("id"), "task_offering_id": match.get("offering_id"),
                        "task_model_id": match.get("model_id"), "task_native_quote": quote})
            if estimate and quote and estimate.get("unit") == quote.get("unit"):
                planned = common.native_amount(estimate.get("amount"))
                actual = common.native_amount(quote.get("total_amount"))
                if planned is not None and actual is not None:
                    row["delta"] = str(actual - planned)
                    row["comparable"] = True
        rows.append(row)
    unmatched = [task.get("id") for task in tasks or [] if task.get("id") not in used]
    return rows, unmatched


def view_states(group):
    return common.slot_states(group)


def task_evidence(task):
    """The facts of one Task kept in the evidence file."""
    pictures, views = task_view_refs(task)
    return {"id": task.get("id"), "capability": task.get("capability"),
            "offering_id": task.get("offering_id"), "model_id": task.get("model_id"),
            "state": task.get("state"), "native_quote": task.get("native_quote"),
            "failure": task.get("failure"), "view_pictures": pictures, "view_labels": views}


def views_done(group):
    """Every view of the group reached a terminal state (a failed one is still an answer)."""
    states = view_states(group)
    return bool(group) and all(states.get(key) in VIEW_TERMINAL for key in VIEW_KEYS)


def views_succeeded(group):
    states = view_states(group)
    return bool(group) and all(states.get(key) == "succeeded" for key in VIEW_KEYS)


def decide_verdict(plan_seen, group_seen, group, calls, new_tasks, step_cards, turn_ended):
    """The #417 verdict from the facts the run collected. Returns (verdict, reason).

    reproduced: the four views all succeeded but no multi-view Task ran (the reported "step 5
    waits"), whether the Agent called the tool or a per-step 3D card was left pending.
    not_reproduced: a multi-view image-to-3D Task SUCCEEDED.
    inconclusive: the flow did not reach the reported point (no plan or group, a view failed, a
    Task ran on another Offering or failed, ...), so the report says what happened instead.
    """
    names = [call.get("name") for call in calls or []]
    succeeded_multiview = [task for task in new_tasks or []
                           if task.get("capability") == "image-to-3d"
                           and task.get("state") == common.SUCCESS_STATE
                           and "multiview" in str(task.get("offering_id", ""))]
    if succeeded_multiview:
        counts = []
        for task in succeeded_multiview:
            pictures, views = task_view_refs(task)
            counts.append(str(task.get("id")) + (f" ({pictures} view picture(s))" if pictures else ""))
        return "not_reproduced", "multi-view 3D task SUCCEEDED: " + ", ".join(counts)
    if not plan_seen:
        return "inconclusive", "no production plan card appeared, so the budget-mode chain did not start"
    if not group_seen:
        return "inconclusive", "no three-view group appeared"
    states = view_states(group)
    if not views_succeeded(group):
        return "inconclusive", "the four views did not all succeed: " + json.dumps(states, ensure_ascii=False)
    terminal_tasks = [task for task in new_tasks or []
                      if task.get("capability") == "image-to-3d"
                      and task.get("state") in common.TERMINAL_TASK_STATES]
    if terminal_tasks:
        return "inconclusive", ("the 3D step ran but did not deliver a multi-view result: "
                                + ", ".join(f"{task.get('id')} {task.get('offering_id')} {task.get('state')}"
                                            for task in terminal_tasks))
    pending_3d = [card for card in step_cards or [] if card.get("capability") == "image-to-3d"]
    from_views = "create_model_3d_from_views" in names
    if from_views or pending_3d:
        detail = []
        if from_views:
            call = next(call for call in calls if call.get("name") == "create_model_3d_from_views")
            arguments = call.get("arguments") if isinstance(call.get("arguments"), dict) else {}
            detail.append("create_model_3d_from_views " + ("with model" if arguments.get("model") else "without model"))
        if pending_3d:
            detail.append(f"{len(pending_3d)} pending image-to-3D approval card(s)")
        if not turn_ended:
            detail.append("the turn did not end")
        return "reproduced", "four views succeeded and no multi-view Task ran: " + "; ".join(detail)
    if not turn_ended:
        return "reproduced", "four views succeeded, the turn did not end and no 3D step started"
    return "inconclusive", "the four views succeeded but the Agent neither called the multi-view tool nor left a 3D card"


def compact_arguments(arguments, limit=200):
    """Tool arguments cut to short values for the evidence file (no full prompts)."""
    if not isinstance(arguments, dict):
        return arguments
    compacted = {}
    for key, value in arguments.items():
        if isinstance(value, str):
            compacted[key] = common.redact_url(value)[:limit]
        elif isinstance(value, list):
            compacted[key] = [common.redact_url(str(item))[:limit] if isinstance(item, str) else item
                              for item in value[:10]]
        elif isinstance(value, dict):
            compacted[key] = compact_arguments(value, limit)
        else:
            compacted[key] = value
    return compacted


def tool_calls_evidence(messages):
    """Every tool call the session wire shows, in order, with its (short) arguments."""
    found = []
    for message in messages or []:
        for call in common.parse_tool_calls(message):
            found.append({
                "message_id": (message or {}).get("id"),
                "name": call.get("name"),
                "arguments": compact_arguments(common.tool_arguments(call)),
                "task_id": call.get("taskId"),
                "native_quote": call.get("native_quote"),
                "estimated_credits": call.get("estimatedCredits"),
                "approval_source": call.get("approval_source") or call.get("approvalSource"),
            })
    return found


# ------------------------------------------------------------------- live flow

class PaidTasks:
    """The run's paid Task ceiling: observe both sessions' task lists and stop on growth."""

    def __init__(self, limit):
        self.limit = limit
        self.seen = {}

    def observe(self, tasks):
        added = []
        for task in tasks or []:
            task_id = task.get("id")
            if task_id and task_id not in self.seen:
                self.seen[task_id] = {"id": task_id, "capability": task.get("capability"),
                                      "offering_id": task.get("offering_id"),
                                      "model_id": task.get("model_id"), "state": task.get("state")}
                added.append(task)
            elif task_id in self.seen:
                self.seen[task_id].update({"state": task.get("state"),
                                           "offering_id": task.get("offering_id"),
                                           "model_id": task.get("model_id")})
        return added

    def created(self):
        return len(self.seen)

    def exceeded(self):
        return len(self.seen) > self.limit


def evidence_write(directory, name, evidence):
    if not directory:
        return None
    os.makedirs(directory, exist_ok=True)
    path = os.path.join(directory, f"{name}-evidence.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(evidence, handle, ensure_ascii=False, indent=2, default=str)
    return path


def summary_line(report, evidence):
    report.record("INFO", "verdict", evidence.get("verdict") + ": " + evidence.get("verdict_reason", ""))
    report.record("INFO", "paid tasks", f"{evidence.get('paid_tasks', {}).get('created')} of "
                                        f"{evidence.get('paid_tasks', {}).get('limit')} used")


def latest_group(live, session):
    status, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/three-views")
    if status != 200:
        return None
    groups = data.get("items") or []
    return groups[-1] if groups else None


def start_right(live, session, group):
    """POST the right view's start as the three-view card's button does. Returns (status, attempts)."""
    slot = next((item for item in group.get("slots") or [] if item.get("key") == "right"), {})
    status = 0
    for attempt in range(RIGHT_MAX_REQUESTS):
        status, data = live.client.call(
            "POST", f"/api/v1/chat/sessions/{session}/three-views/{group.get('run_id')}/views/right",
            {"action": "start", "attempt": slot.get("attempt", 0)})
        if status != 429:
            return status, attempt + 1
        time.sleep(RIGHT_RETRY_SECONDS)
    return status, RIGHT_MAX_REQUESTS


def load_or_generate_image(live, report, evidence, models, paid):
    """(bytes, media type, source) of the character picture, or (None, None, reason)."""
    path = live.arguments.image
    if path:
        try:
            with open(path, "rb") as handle:
                data = handle.read(MAX_REFERENCE_BYTES + 1)
        except OSError as error:
            return None, None, f"--image {path}: {error}"
        media_type, _extension = image_media_type(data)
        if media_type is None:
            return None, None, f"--image {path}: not PNG, JPEG or WebP"
        if len(data) > MAX_REFERENCE_BYTES:
            return None, None, f"--image {path}: over {MAX_REFERENCE_BYTES} bytes"
        return data, media_type, "upload"
    if not live.arguments.generate_image:
        return None, None, "no --image given and --generate-image not set"
    session = live.new_session("l5-four-view-source", approval="auto")
    if not report.check("source image session created", bool(session)):
        return None, None, "session creation failed"
    reply, tasks, settled = live.run_turn(session, CHARACTER, "image", models["image"],
                                          {"text-to-image": 1})
    paid.observe(tasks)
    task = common.delivered_task(tasks, set(), models["image"])
    if not report.check("source character image generated", task is not None,
                        str(common.describe_tasks(tasks)) + " reply=" + common.one_line((reply or {}).get("content"), 60)):
        return None, None, "source generation did not deliver an image"
    asset = next((item for item in live.task(task["id"]).get("assets") or []
                  if item.get("required") and str(item.get("media_type", "")).startswith("image/")), None)
    if asset is None:
        return None, None, "the source Task has no required image artifact"
    status, data = live.client.fetch(asset["url"])
    if status != 200 or not data:
        return None, None, f"source image not readable (HTTP {status})"
    media_type, _extension = image_media_type(data)
    return (data, media_type, "generated") if media_type else (None, None, "generated image has an unknown format")


def upload(live, data, media_type):
    _type, extension = image_media_type(data)
    return live.client.upload_image(data, media_type=media_type, filename=f"four-view-source.{extension or 'png'}")


def propose_plan(live, report, evidence, models, image, session):
    """Send the four-view request and wait for the production plan card. Returns the card or None."""
    asset = upload(live, image[0], image[1])
    if not report.check("character image uploaded", bool(asset)):
        return None
    evidence["source_asset"] = asset
    status, _ = live.send(session, REQUEST, "model-3d", models["locked_3d"],
                          selection=[{"kind": "image", "assetId": asset}])
    if not report.check("four-view request accepted", status in (200, 201, 202), f"HTTP {status}"):
        return None
    reply = live.wait_reply(session)
    evidence["proposal_reply"] = common.one_line((reply or {}).get("content"), 160)
    evidence["proposal_turn_finished"] = reply is not None
    card, steps = live.wait_plan_card(session)
    evidence["plan_cards_seen"] = [plan_view(item) for item in common.plan_cards(live.pending_approvals(session))]
    evidence["step_cards_before_plan"] = [{"capability": item.get("capability"), "tool": item.get("tool"),
                                          "request_id": item.get("request_id")} for item in steps]
    if card is None:
        # The plan proposal may have been refused (an unpriceable step, a conflicting model); its
        # arguments are the evidence, so they are already in the tool calls collected at the end.
        report.record("INFO", "plan card", "none appeared; see the proposal tool calls and reply")
        return None
    evidence["plan"] = plan_view(card)
    report.record("INFO", "plan card", json.dumps({
        "budgets": card.get("budgets"), "estimated_totals": card.get("estimated_totals"),
        "steps": [(step.get("capability"), step.get("offering_id")) for step in evidence["plan"]["steps"]]},
        ensure_ascii=False))
    return card


def hydrate_tasks(live, tasks):
    """Task rows with the owner-only `input` merged in (the session list omits it), so the frozen
    reference pictures a 3D Task used are in the evidence and the verdict."""
    hydrated = []
    for task in tasks or []:
        if task.get("capability") == "image-to-3d" and not task.get("input") and task.get("id"):
            detail = live.task(task["id"])
            if isinstance(detail, dict) and detail:
                task = {**task, "input": detail.get("input"),
                        "credits_charged": detail.get("credits_charged")}
        hydrated.append(task)
    return hydrated


def wait_turn_started(live, session, message_id, timeout=90):
    """Wait for the assistant message of the plan's server-started turn (#134) to appear.

    The approval enqueues the turn; until it starts, the chat stream is not held and the idle rule
    of the plan watch would otherwise mistake "not started yet" for "ended". Returns whether the
    message appeared."""
    if not message_id:
        return False
    deadline = time.time() + timeout
    while time.time() < deadline:
        if any(message.get("id") == message_id for message in live.messages(session)):
            return True
        time.sleep(3)
    return False


def group_in_flight(group, pending_request_ids=None, abandoned_request_ids=None):
    """A view is still moving (a paid Task or a decidable approval) and may settle on its own.

    An `awaiting_approval` view counts only while its approval card is still pending and its
    approval was not refused; otherwise nobody will move it and the plan watch can stop."""
    pending_request_ids = pending_request_ids or set()
    abandoned_request_ids = abandoned_request_ids or set()
    for slot in (group or {}).get("slots") or []:
        status = slot.get("status")
        if status in ("starting", "running"):
            return True
        if (status == "awaiting_approval" and slot.get("request_id") in pending_request_ids
                and slot.get("request_id") not in abandoned_request_ids):
            return True
    return False


def follow_plan(live, report, evidence, session, budget, deadline):
    """Follow the plan's server-started turn: start the optional right view, record cards, keep the
    Task count. Returns (group, turn_ended)."""
    started_right = False
    step_cards, plan_cards = {}, {}
    group, turn_ended, idle = None, False, 0
    while time.time() < deadline:
        pending = live.pending_approvals(session)
        pending_ids = {item.get("request_id") or item.get("id") for item in pending}
        for item in common.plan_cards(pending):
            plan_cards.setdefault(item.get("request_id"), plan_view(item))
        for item in common.step_cards(pending):
            key = item.get("request_id") or item.get("id")
            entry = step_cards.setdefault(key, {
                "request_id": key, "kind": item.get("kind"), "capability": item.get("capability"),
                "tool": item.get("tool"), "estimated_credits": item.get("estimated_credits"),
                "native_quote": item.get("native_quote")})
            if entry.get("decision"):
                continue
            if item.get("capability") == "image-to-image":
                # A view the plan did not cover: the person would decide it on the card, and the
                # four views must finish for the reported situation. It is recorded either way.
                status, _ = live.decide_grant(key, True)
                entry["decision"] = {"decision": "approve", "status": status}
            elif item.get("capability") == "image-to-3d":
                # The reported symptom: a 3D step the approved plan did not cover waits for a
                # person. Leaving it pending keeps the reproduction faithful.
                entry["decision"] = {"decision": "left_pending"}
        group = latest_group(live, session) or group
        budget.observe(live.tasks(session))
        if budget.exceeded():
            report.failed("paid cap", f"exceeded: {budget.created()} > {budget.limit}; the plan watch stopped")
            break
        if group and not started_right:
            slot = next((item for item in group.get("slots") or [] if item.get("key") == "right"), None)
            if slot and slot.get("status") != "not_started":
                started_right = True
                evidence["right_started_by"] = "agent"
            elif slot and slot.get("startable"):
                status, attempts = start_right(live, session, group)
                started_right = True
                evidence["right_started_by"] = "script"
                evidence["right_start"] = {"http_status": status, "requests": attempts}
                report.record("INFO", "started right view", f"HTTP {status} after {attempts} request(s)")
        turn_ended = live.turn_finished(session, probe=4)
        group = latest_group(live, session) or group
        if group and views_done(group) and turn_ended:
            break
        # After the turn ended, nothing starts a view by itself (the right view is optional and the
        # Agent cannot start views). Two idle polls mean the remaining views will not move.
        abandoned = {key for key, entry in step_cards.items()
                     if (entry.get("decision") or {}).get("decision") == "approve"
                     and (entry.get("decision") or {}).get("status") not in (200, 201, 204)}
        if turn_ended and not group_in_flight(group, pending_ids, abandoned):
            idle += 1
            if idle >= 2:
                break
        else:
            idle = 0
        time.sleep(POLL_SECONDS)
    evidence["right_started_by"] = evidence.get("right_started_by", "not_needed")
    evidence["plan_cards_during_plan"] = list(plan_cards.values())
    evidence["step_cards_outside_plan"] = list(step_cards.values())
    return group, turn_ended


def wait_control_task(live, session, before, timeout):
    """The first new image-to-3D Task once terminal, or the first new terminal Task of another
    capability (the Agent took another route), so a wrong route does not wait out the 3D budget."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        tasks = live.tasks(session)
        new = [task for task in tasks if task.get("id") not in before]
        for task in new:
            if task.get("capability") == "image-to-3d" and task.get("state") in common.TERMINAL_TASK_STATES:
                return tasks, task
        settled = [task for task in new if task.get("state") in common.TERMINAL_TASK_STATES]
        if settled:
            return tasks, None
        time.sleep(5)
    return live.tasks(session), None


def control_group(live, report, evidence, models, image, budget):
    """The single-view control: a fresh auto-mode session, same image, one image-to-3D Task."""
    started = time.time()
    session = live.new_session("l5-four-view-control", approval="auto")
    if not report.check("control session created", bool(session)):
        return
    _before_scope, after_scope, _status = set_agent_scope(live, session)
    if not report.check("control session Agent scope is auto", after_scope == "auto", f"scope={after_scope!r}"):
        return
    live.lock_model_3d(session, models["control_3d"])
    live.lock_model_2d(session, models["image"])
    asset = upload(live, image[0], image[1])
    if not report.check("control image uploaded", bool(asset)):
        return
    before = {task.get("id") for task in live.tasks(session)}
    status, _ = live.send(session, CONTROL, "model-3d", models["control_3d"],
                          selection=[{"kind": "image", "assetId": asset}])
    if not report.check("control request accepted", status in (200, 201, 202), f"HTTP {status}"):
        return
    reply = live.wait_reply(session)
    tasks, task = wait_control_task(live, session, before, live.arguments.task_timeout)
    budget.observe(tasks)
    new = [item for item in tasks if item.get("id") not in before]
    evidence["control"] = {
        "session_id": session, "turn_finished": reply is not None,
        "reply": common.one_line((reply or {}).get("content"), 160),
        "tasks": [task_evidence(item) for item in new],
        "elapsed_ms": int((time.time() - started) * 1000),
    }
    ok = bool(task) and task.get("state") == common.SUCCESS_STATE and not common.reply_failed(reply)
    report.check("control: one image-to-3D task SUCCEEDED", ok,
                 str(common.describe_tasks(new)) + " reply=" + common.one_line((reply or {}).get("content"), 60))
    if task and task.get("state") == common.SUCCESS_STATE:
        live.verify_task_artifacts(task["id"], "control 3D")


def set_agent_scope(live, session, scope="auto"):
    """Make both 2D and 3D tools available, as the workspace's functional selection does.

    A new session defaults to the Schema's default mode (auto here), but an administrator may
    change it; the reported flow needs the 2D three-view tool and the 3D model tool in one
    session. Returns (before, after, http status)."""
    status, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/agent-scope")
    before = data.get("agentScope") if status == 200 and isinstance(data, dict) else None
    if status != 200:
        return None, None, status
    if before == scope:
        return before, before, status
    status, data = live.client.call("PUT", f"/api/v1/chat/sessions/{session}/agent-scope", {"agentScope": scope})
    after = data.get("agentScope") if isinstance(data, dict) else None
    return before, after, status


def run_chain(live, report, evidence, models, image, budget):
    """The four-view budget chain; returns the plan card or None. All findings are evidence/INFO:
    the reported failure is an expected outcome, only an unreachable flow is 'inconclusive'."""
    session = live.new_session("l5-four-view-budget", approval="budget")
    if not report.check("budget session created", bool(session)):
        evidence["verdict_reason"] = "session creation failed"
        return
    evidence["session_id"] = session
    evidence["approval_mode"] = live.approval_mode(session)
    if not report.check("the session reads back the budget mode", evidence["approval_mode"] == "budget",
                        f"approvalMode={evidence['approval_mode']!r}"):
        evidence["verdict_reason"] = "the session did not enter the budget approval mode"
        return
    before_scope, after_scope, scope_status = set_agent_scope(live, session)
    evidence["agent_scope"] = {"before": before_scope, "after": after_scope, "http_status": scope_status}
    if not report.check("the session Agent scope allows 2D and 3D tools", after_scope == "auto",
                        f"scope={after_scope!r} (HTTP {scope_status})"):
        evidence["verdict_reason"] = f"the session Agent scope is {after_scope!r}, not auto"
        return
    live.lock_model_3d(session, models["locked_3d"])
    live.lock_model_2d(session, models["image"])
    read = live.session(session) or {}
    evidence["session_read"] = {"explicit_model_3d": read.get("explicit_model_3d"),
                                "explicit_model_2d": read.get("explicit_model_2d")}

    card = propose_plan(live, report, evidence, models, image, session)
    if card is None:
        evidence["tool_calls"] = tool_calls_evidence(live.messages(session))
        evidence["verdict_reason"] = "no production plan card appeared"
        return
    evidence["plan_seen"] = True
    before = {task.get("id") for task in live.tasks(session)}
    status, decided = live.decide_plan(session, card["request_id"], True)
    evidence["plan_decision"] = {"http_status": status, "state": decided.get("state"),
                                 "turn_message_id": decided.get("turn_message_id")}
    if not report.check("plan approved on the session budget route",
                        status == 200 and decided.get("state") == "active"
                        and bool(decided.get("turn_message_id")),
                        f"HTTP {status} state={decided.get('state')!r}"):
        evidence["verdict_reason"] = "the plan decision did not start a turn"
        evidence["tool_calls"] = tool_calls_evidence(live.messages(session))
        return
    plan_started = time.time()
    turn_started = wait_turn_started(live, session, decided.get("turn_message_id"))
    group, turn_ended = follow_plan(live, report, evidence, session, budget,
                                    plan_started + live.arguments.turn_timeout)
    evidence["plan_turn"] = {"finished": turn_ended, "started": turn_started,
                             "elapsed_ms": int((time.time() - plan_started) * 1000)}
    tasks = live.tasks(session)
    budget.observe(tasks)
    # A 3D Task the plan started may still be running when the turn ended: its terminal state is
    # the difference between "delivered" and "not executed".
    if any(task.get("capability") == "image-to-3d" and task.get("state") not in common.TERMINAL_TASK_STATES
           for task in tasks):
        tasks, _ = live.wait_tasks(session, {"image-to-3d": 1}, timeout=live.arguments.task_timeout)
        budget.observe(tasks)
    new_tasks = [task for task in tasks if task.get("id") not in before]
    report.record("INFO", "four views", json.dumps(view_states(group) if group else {}, ensure_ascii=False))
    if group and views_done(group) and not any(
            task.get("capability") == "image-to-3d" for task in new_tasks) and turn_ended:
        # The plan's 3D step did not run. Ask in plain model-3D language; no structured multiview
        # request is sent, so the tool call the Agent chooses is the evidence (#417).
        follow_started = time.time()
        status, _ = live.send(session, FOLLOW_UP, "model-3d", models["locked_3d"])
        evidence["follow_up"] = {"http_status": status}
        if status in (200, 201, 202):
            reply = live.wait_reply(session)
            follow_tasks = live.tasks(session)
            # A refused or unanswered call creates no Task; only wait for a Task that exists and is
            # still running (the turn may time out while a real 3D job runs).
            if any(task.get("capability") == "image-to-3d"
                   and task.get("state") not in common.TERMINAL_TASK_STATES
                   and task.get("id") not in before for task in follow_tasks):
                follow_tasks, _ = live.wait_tasks(session, {"image-to-3d": 1},
                                                  timeout=live.arguments.task_timeout)
            budget.observe(follow_tasks)
            tasks = follow_tasks
            new_tasks = [task for task in follow_tasks if task.get("id") not in before]
            evidence["follow_up"].update({
                "turn_finished": reply is not None,
                "reply": common.one_line((reply or {}).get("content"), 160),
                "elapsed_ms": int((time.time() - follow_started) * 1000),
            })
            for item in common.step_cards(live.pending_approvals(session)):
                if item.get("capability") == "image-to-3d":
                    evidence.setdefault("step_cards_outside_plan", []).append(
                        {"request_id": item.get("request_id"), "capability": item.get("capability"),
                         "tool": item.get("tool"), "decision": "left_pending"})
        else:
            report.record("INFO", "follow-up 3D message", f"rejected with HTTP {status}; the session stayed busy")
    group = latest_group(live, session) or group
    evidence["view_states"] = view_states(group) if group else {}
    evidence["view_slots"] = [
        {"key": slot.get("key"), "status": slot.get("status"), "attempt": slot.get("attempt"),
         "task_id": slot.get("task_id"), "review": slot.get("review"), "optional": slot.get("optional")}
        for slot in ((group or {}).get("slots") or [])]
    messages = live.messages(session)
    evidence["tool_calls"] = tool_calls_evidence(messages)
    evidence["api_visible_refusals"] = common.visible_refusal_counts(messages)
    prices = common.call_prices(messages)
    new_ids = {item.get("id") for item in new_tasks}
    planned_tasks = hydrate_tasks(live, [task for task in tasks if task.get("id") in new_ids])
    rows, unmatched = plan_quote_rows(card, planned_tasks, prices)
    evidence["plan_quote_rows"] = rows
    evidence["unmatched_task_ids"] = unmatched
    report.record("INFO", "plan quote vs task quote", json.dumps(
        [{"step": row["step"], "plan": row["plan_estimate"], "task": row["task_native_quote"],
          "delta": row["delta"]} for row in rows], ensure_ascii=False))
    evidence["tasks_after_follow_up"] = [task_evidence(task) for task in planned_tasks]
    verdict, reason = decide_verdict(
        plan_seen=True, group_seen=bool(group), group=group, calls=evidence["tool_calls"],
        new_tasks=planned_tasks,
        step_cards=evidence.get("step_cards_outside_plan") or [], turn_ended=turn_ended)
    evidence["verdict"], evidence["verdict_reason"] = verdict, reason
    if verdict == "not_reproduced":
        report.passed("four-view multi-view 3D delivered", reason)
    else:
        report.record("INFO", "four-view chain outcome", reason)


def run(live, report, evidence):
    started = time.time()
    evidence.update({
        "issue": "417",
        "kind": "four-view-multiview",
        "base": os.environ.get("BASE", ""),
        "expect_sha": os.environ.get("EXPECT_SHA", ""),
        "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "request": REQUEST,
        "follow_up": FOLLOW_UP,
        "control_request": CONTROL,
        "verdict": "inconclusive",
        "verdict_reason": "the run did not reach a decision",
    })
    offerings_image = live.offerings("text-to-image")
    offerings_3d = live.offerings("image-to-3d")
    image_offering = image_model_for_cost(offerings_image)
    locked = single_view_offering(offerings_3d, live.arguments.model3d)
    multiview = multiview_offering(offerings_3d, live.arguments.multiview_model)
    evidence["models"] = {
        "image_2d": (image_offering or {}).get("model_id"),
        "image_2d_offering": (image_offering or {}).get("id"),
        "image_2d_credits": (image_offering or {}).get("estimated_credits"),
        "locked_3d": live.arguments.model3d,
        "locked_3d_offering": (locked or {}).get("id"),
        "expected_multiview": live.arguments.multiview_model,
        "multiview_offering": (multiview or {}).get("id"),
        "multiview_estimated_credits": (multiview or {}).get("estimated_credits"),
    }
    if live.arguments.image and live.arguments.generate_image:
        report.failed("--image and --generate-image are exclusive", "pass one of them")
        return
    if not live.arguments.image and not live.arguments.generate_image:
        report.skipped("character picture given",
                       "pass --image <local PNG/JPEG/WebP> (or --generate-image for one extra paid image)")
        evidence["verdict_reason"] = "no source picture given"
        return
    if not report.check("a 6-credit text-to-image Offering is available", image_offering is not None,
                        str([(item.get("id"), item.get("estimated_credits"))
                             for item in offerings_image if item.get("available")])):
        return
    if multiview is None:
        report.skipped("an available multi-view image-to-3D Offering", "none in this Catalog/runtime")
        evidence["verdict_reason"] = "no available multi-view Offering"
        return
    if locked is None:
        report.skipped("the locked 3D model is an available single-image Offering",
                       f"{live.arguments.model3d} is not an available single-image image-to-3D model")
        evidence["verdict_reason"] = f"locked 3D model {live.arguments.model3d} is not available"
        return
    control_on = bool(live.arguments.control)
    minimum = 5 + (1 if control_on else 0) + (1 if live.arguments.generate_image else 0)
    if live.arguments.max_paid < minimum:
        report.failed("--max-paid covers the chain", f"{live.arguments.max_paid} < {minimum} paid Task(s)")
        evidence["verdict_reason"] = f"--max-paid {live.arguments.max_paid} is below the chain's {minimum}"
        return
    models = {"image": image_offering["model_id"], "locked_3d": live.arguments.model3d,
              "control_3d": locked["model_id"]}
    budget = PaidTasks(live.arguments.max_paid)
    image, media_type, source = load_or_generate_image(live, report, evidence, models, budget)
    if image is None:
        report.failed("character picture available", source)
        evidence["verdict_reason"] = source
        return
    dimensions = common.image_dimensions(image)
    evidence["image"] = {"source": source, "media_type": media_type, "bytes": len(image),
                         "dimensions": list(dimensions) if dimensions else None}
    if source == "upload":
        report.passed("character picture loaded", f"{media_type} {len(image)} bytes")
    if budget.exceeded():
        report.failed("paid cap before the four-view chain", f"{budget.created()} > {budget.limit}")
        evidence["verdict_reason"] = "the source image alone exceeded --max-paid"
        return
    chain_completed = False
    try:
        run_chain(live, report, evidence, models, (image, media_type), budget)
        chain_completed = True
    finally:
        # The control is independent of how far the budget chain got, and runs whenever the paid
        # ceiling leaves room for one more Task. A crash inside the chain skips it.
        if chain_completed and control_on:
            if budget.created() + 1 > budget.limit:
                report.record("INFO", "control skipped",
                              f"would exceed --max-paid ({budget.created()} + 1 > {budget.limit})")
            else:
                control_group(live, report, evidence, models, (image, media_type), budget)
    evidence["paid_tasks"] = {"limit": budget.limit, "created": budget.created(),
                              "tasks": sorted(budget.seen.values(), key=lambda item: item["id"])}
    evidence["elapsed_ms"] = int((time.time() - started) * 1000)


def body(live, report):
    evidence = {}
    try:
        run(live, report, evidence)
    except Exception as error:  # the evidence must survive a crash; execute() records the FAIL
        evidence["script_error"] = f"{type(error).__name__}: {common.one_line(str(error), 160)}"
        raise
    finally:
        evidence.setdefault("elapsed_ms", None)
        path = evidence_write(live.arguments.report, "smoke-l5-four-view-multiview", evidence)
        if path:
            print(f"# evidence: {path}", flush=True)
        if evidence.get("verdict"):
            summary_line(report, evidence)


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-four-view-multiview", body, __doc__, extra_arguments))