"""Shared helpers for the L5 live-capability smoke scripts (smoke-l5-*.py).

Everything that decides something (waiting rules, tool-call parsing, argument
validation, redaction) is a plain function so scripts/tests/test_l5_common.py
can cover it without a server. The HTTP client and the report writer are thin.

The scripts spend real money, so main() refuses to run without ART_AI_LIVE_ACK=1.
They never print credentials, cookies, signed-URL queries or full prompts.
"""

import argparse
import decimal
import http.cookiejar
import json
import math
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ACK_VARIABLE = "ART_AI_LIVE_ACK"
TERMINAL_TASK_STATES = ("SUCCEEDED", "FAILED", "CANCELED", "CANCELLED")
GENERATING_PLACEHOLDER = "__GENERATING__"
DEFAULT_SESSION_TITLE = "新对话"
SUCCESS_STATE = "SUCCEEDED"


# ---------------------------------------------------------------- pure logic

def ack_error(environ):
    """Return the refusal message when the paid-run acknowledgement is absent."""
    if environ.get(ACK_VARIABLE) == "1":
        return None
    return (f"refusing to run: this script sends real, paid model requests; "
            f"set {ACK_VARIABLE}=1 to acknowledge the cost")


def missing_environment(environ, names=("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD")):
    return [name for name in names if not environ.get(name)]


def validate_base(base):
    parsed = urllib.parse.urlparse(base or "")
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.path not in ("", "/"):
        return "BASE must be an http(s) origin without a path"
    if parsed.query or parsed.fragment or parsed.username or parsed.password:
        return "BASE must not carry credentials, a query or a fragment"
    return None


def parse_arguments(argv, description=""):
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("--report", help="directory for the report and downloaded artifacts")
    parser.add_argument("--turn-timeout", type=int, default=1800, help="seconds to wait for one chat turn")
    parser.add_argument("--task-timeout", type=int, default=2400, help="seconds to wait for expected tasks")
    arguments = parser.parse_args(argv)
    for name in ("turn_timeout", "task_timeout"):
        if getattr(arguments, name) < 30:
            parser.error(f"--{name.replace('_', '-')} must be at least 30 seconds")
    return arguments


def redact_url(text):
    """Drop query strings from any URL inside text so signed parameters never print."""
    return re.sub(r"(https?://[^\s?\"']+|/api/[^\s?\"']+)\?[^\s\"']*", r"\1?<redacted>", text or "")


def one_line(text, limit=80):
    return redact_url(" ".join((text or "").split()))[:limit]


def parse_tool_calls(message):
    """Tool calls of one chat message. The API serialises them as a JSON string."""
    raw = (message or {}).get("toolCalls")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else []
        except ValueError:
            return []
    if not isinstance(raw, list):
        return []
    return [call for call in raw if isinstance(call, dict)]


def tool_names(messages):
    return [call.get("name") for message in messages for call in parse_tool_calls(message) if call.get("name")]


def tool_errors(messages):
    """Names of tool calls that carry an error. The messages wire has no per-call result; the extra
    fields are checked only in case a later wire adds them. A failed turn is detected by reply_failed."""
    failed = []
    for message in messages:
        for call in parse_tool_calls(message):
            if call.get("error"):
                failed.append(call.get("name"))
    return failed


def tool_arguments(call):
    """Arguments of one tool call; the API serialises them as a JSON string."""
    raw = (call or {}).get("arguments")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else {}
        except ValueError:
            return {}
    return raw if isinstance(raw, dict) else {}


def is_multiview_task(task, model_id):
    """The task ran on the multi-view Offering: its offering id names multiview and its model is the chosen one."""
    return "multiview" in str(task.get("offering_id", "")) and task.get("model_id") == model_id


def image_handles(messages):
    """Handles of the image attachments over all messages, in order of first appearance."""
    found = []
    for message in messages:
        for attachment in message.get("attachments") or []:
            if isinstance(attachment, dict) and attachment.get("kind") == "image":
                handle = attachment.get("handle")
                if handle and handle not in found:
                    found.append(handle)
    return found


def reference_targets(call):
    """References one image-tool call passes: handles as given, and 1-based indices as "#<n>"."""
    arguments = tool_arguments(call)
    handles = [str(item) for item in arguments.get("reference_handles") or []]
    indices = [f"#{item}" for item in arguments.get("reference_image_indices") or []]
    return handles + indices


def calls_named(messages, *names):
    return [call for message in messages for call in parse_tool_calls(message) if call.get("name") in names]


def image_dimensions(data):
    """(width, height) of PNG, JPEG or WebP bytes, or None."""
    if len(data) >= 24 and data[:8] == b"\x89PNG\r\n\x1a\n":
        return int.from_bytes(data[16:20], "big"), int.from_bytes(data[20:24], "big")
    if data[:2] == b"\xff\xd8":
        index = 2
        while index + 9 < len(data):
            if data[index] != 0xFF:
                index += 1
                continue
            marker = data[index + 1]
            if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
                index += 2
                continue
            length = int.from_bytes(data[index + 2:index + 4], "big")
            if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
                return int.from_bytes(data[index + 7:index + 9], "big"), int.from_bytes(data[index + 5:index + 7], "big")
            index += 2 + length
        return None
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP" and len(data) >= 30:
        kind = data[12:16]
        if kind == b"VP8X":
            return 1 + int.from_bytes(data[24:27], "little"), 1 + int.from_bytes(data[27:30], "little")
        if kind == b"VP8L":
            bits = int.from_bytes(data[21:25], "little")
            return 1 + (bits & 0x3FFF), 1 + ((bits >> 14) & 0x3FFF)
        if kind == b"VP8 ":
            return int.from_bytes(data[26:28], "little") & 0x3FFF, int.from_bytes(data[28:30], "little") & 0x3FFF
    return None


def option_ratio(value):
    """Width/height of a size option such as '1536x1024' or '16:9'; None when it is not a ratio."""
    match = re.fullmatch(r"\s*(\d+)\s*[x:X*]\s*(\d+)\s*", str(value))
    if not match or int(match.group(2)) == 0:
        return None
    return int(match.group(1)) / int(match.group(2))


def ratio_matches(dimensions, expected, tolerance=0.03):
    if not dimensions or not expected or dimensions[1] == 0:
        return False
    return abs(dimensions[0] / dimensions[1] - expected) / expected <= tolerance


def option_area(value):
    match = re.fullmatch(r"\s*(\d+)\s*[x*X]\s*(\d+)\s*", str(value))
    return int(match.group(1)) * int(match.group(2)) if match else 0


def size_options(offering):
    """(parameter name, [option values]) of the output-size parameter an Offering declares, or None."""
    for parameter in offering.get("parameters") or []:
        if parameter.get("semantic") == "output-size" and parameter.get("options"):
            return parameter["name"], [str(item["value"]) for item in parameter["options"]]
    return None


def pick_shapes(options, default=None):
    """Cheapest option per shape: square, portrait, landscape, plus one non-default option of another ratio."""
    chosen = {}
    for value in sorted(options, key=lambda v: (option_area(v), options.index(v))):
        ratio = option_ratio(value)
        if ratio is None:
            continue
        shape = "square" if abs(ratio - 1) < 0.02 else ("portrait" if ratio < 1 else "landscape")
        chosen.setdefault(shape, value)
    taken = set(chosen.values())
    for value in sorted(options, key=lambda v: (option_area(v), options.index(v))):
        ratio = option_ratio(value)
        if value not in taken and value != default and ratio is not None and all(
                abs(ratio - option_ratio(other)) > 0.02 for other in taken):
            chosen["other"] = value
            break
    return chosen


def encode_png(width, height, rows):
    """An 8-bit RGB PNG of the given rows (each width * 3 bytes), built with the standard library."""
    import struct
    import zlib

    def chunk(kind, payload):
        body = kind + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    raw = b"".join(b"\x00" + bytes(row) for row in rows)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def decode_png(data):
    """(width, height, rows) of a non-interlaced 8- or 16-bit PNG, rows as 8-bit RGB bytes (alpha dropped).

    Raises ValueError for anything else (JPEG, WebP, interlaced or low-bit-depth PNG)."""
    import struct
    import zlib

    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("not a PNG")
    header, palette, compressed, index = None, b"", [], 8
    while index + 8 <= len(data):
        length, kind = struct.unpack(">I4s", data[index:index + 8])
        payload = data[index + 8:index + 8 + length]
        if kind == b"IHDR":
            header = struct.unpack(">IIBBBBB", payload)
        elif kind == b"PLTE":
            palette = payload
        elif kind == b"IDAT":
            compressed.append(payload)
        elif kind == b"IEND":
            break
        index += 12 + length
    if not header:
        raise ValueError("PNG without IHDR")
    width, height, depth, colour, _, _, interlace = header
    channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}.get(colour)
    if channels is None or depth not in (8, 16) or (colour == 3 and depth != 8) or interlace:
        raise ValueError(f"unsupported PNG (colour type {colour}, depth {depth}, interlace {interlace})")
    step = channels * depth // 8
    stride = width * step
    raw = zlib.decompress(b"".join(compressed))
    prior, rows = bytearray(stride), []
    for y in range(height):
        start = y * (stride + 1)
        kind, line = raw[start], bytearray(raw[start + 1:start + 1 + stride])
        if kind == 1:
            for i in range(step, stride):
                line[i] = (line[i] + line[i - step]) & 0xFF
        elif kind == 2:
            line = bytearray((a + b) & 0xFF for a, b in zip(line, prior))
        elif kind == 3:
            for i in range(stride):
                line[i] = (line[i] + (((line[i - step] if i >= step else 0) + prior[i]) >> 1)) & 0xFF
        elif kind == 4:
            for i in range(stride):
                a, b, c = (line[i - step], prior[i], prior[i - step]) if i >= step else (0, prior[i], 0)
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                line[i] = (line[i] + (a if pa <= pb and pa <= pc else b if pb <= pc else c)) & 0xFF
        elif kind != 0:
            raise ValueError(f"bad PNG filter {kind}")
        prior = line
        samples = line[::2] if depth == 16 else line  # 16-bit samples keep their high byte
        rgb = bytearray(width * 3)
        if colour in (0, 4):
            grey = samples[::channels]
            rgb[0::3], rgb[1::3], rgb[2::3] = grey, grey, grey
        elif colour == 3:
            rgb = bytearray(b"".join(palette[3 * value:3 * value + 3] for value in samples))
        else:
            for k in range(3):
                rgb[k::3] = samples[k::channels]
        rows.append(bytes(rgb))
    return width, height, rows


def side_by_side_png(pictures, height=512):
    """The PNG pictures scaled to one height (nearest neighbour) and laid out left to right, as one PNG.

    Stands in for the browser's flattened artboard of the placed source pictures."""
    import operator

    strips = []
    for data in pictures:
        width, source_height, rows = decode_png(data)
        scaled = max(1, round(width * height / source_height))
        pick = operator.itemgetter(*[3 * (x * width // scaled) + k for x in range(scaled) for k in range(3)])
        strips.append([bytes(pick(rows[y * source_height // height])) for y in range(height)])
    total = sum(len(strip[0]) // 3 for strip in strips)
    return encode_png(total, height, [b"".join(strip[y] for strip in strips) for y in range(height)])


def canvas_attachment_ids(messages):
    """Handles of the canvas-role attachments of the user messages, oldest first."""
    found = []
    for message in messages:
        if message.get("role") != "user":
            continue
        for attachment in message.get("attachments") or []:
            if isinstance(attachment, dict) and (attachment.get("role") == "canvas" or attachment.get("canvas")):
                found.append(attachment.get("handle") or attachment.get("assetId") or attachment.get("asset_id") or "")
    return found


def user_handles(messages):
    """Handles of the attachments the user messages carry, in order of first appearance."""
    found = []
    for message in messages:
        if message.get("role") != "user":
            continue
        for attachment in message.get("attachments") or []:
            handle = attachment.get("handle") if isinstance(attachment, dict) else None
            if handle and handle not in found:
                found.append(handle)
    return found


def last_assistant(messages):
    for message in reversed(messages or []):
        if message.get("role") == "assistant":
            return message
    return None


# Orchestrator.fail writes the failure text into the assistant content and appends the Schema's
# error_reference ("（错误 ID：{id}）", English " (Error ID: {id})"). The other failure copies are the Schema's
# agent_not_configured / agent_content_rejected / agent_images_unsupported / no_result texts.
ERROR_REFERENCE = re.compile(r"[（(]\s*(?:错误 ID|Error ID)\s*[:：]")
FAILURE_PREFIXES = ("Agent 未配置", "请求被上游模型服务的内容安全审核拦截", "当前 Agent 模型拒绝了带图片的请求",
                    "⚠️ 未返回结果", "未知错误")


def reply_failed(reply):
    """The assistant message content is a failure text written by Orchestrator.fail."""
    content = ((reply or {}).get("content") or "").strip()
    if not content:
        return False
    return bool(ERROR_REFERENCE.search(content)) or content.startswith(FAILURE_PREFIXES)


def reply_is_final(messages):
    """The last assistant message holds real content, not the placeholder or an empty body.

    Content alone never proves the turn is over: Orchestrator persists planning text before it runs
    tools, and the session wire has no pending flag. Completion is the chat stream's done event
    (stream_finished); this only guards what to read after it. A failure text counts as content.
    """
    reply = last_assistant(messages)
    if not reply or reply.get("isPending"):
        return False
    content = (reply.get("content") or "").strip()
    return bool(content) and content != GENERATING_PLACEHOLDER


def parse_sse_events(lines):
    """Decode the JSON payloads of `data:` lines of a text/event-stream (id/retry/heartbeat lines ignored)."""
    events = []
    for raw in lines:
        line = raw.decode("utf-8", "replace") if isinstance(raw, bytes) else raw
        line = line.rstrip("\r\n")
        if not line.startswith("data:"):
            continue
        try:
            payload = json.loads(line[len("data:"):].strip())
        except ValueError:
            continue
        if isinstance(payload, dict):
            events.append(payload)
    return events


def stream_finished(events):
    """The chat stream carries the turn-done event (Schema chat.stream.event_types.done)."""
    return any(event.get("type") == "done" for event in events)


def reply_is_clean(reply):
    """Final content that is not a failure text."""
    if not reply:
        return False
    content = (reply.get("content") or "").strip()
    return bool(content) and content != GENERATING_PLACEHOLDER and not reply_failed(reply) and not reply.get("error")


def tasks_by_capability(tasks):
    counts = {}
    for task in tasks:
        counts[task.get("capability")] = counts.get(task.get("capability"), 0) + 1
    return counts


def tasks_settled(tasks, expected):
    """Decide whether the expected tasks exist and every task is terminal.

    expected maps capability -> minimum number of tasks. Never settled while an
    expected task has not been created yet, even if all existing tasks are terminal.
    Returns (settled, reason).
    """
    counts = tasks_by_capability(tasks)
    for capability, minimum in expected.items():
        if counts.get(capability, 0) < minimum:
            return False, f"waiting for {capability} task ({counts.get(capability, 0)}/{minimum})"
    running = [task for task in tasks if task.get("state") not in TERMINAL_TASK_STATES]
    if running:
        return False, f"{len(running)} task(s) still running"
    return True, "all expected tasks terminal"


def succeeded_count(tasks, capability):
    return sum(1 for task in tasks if task.get("capability") == capability and task.get("state") == SUCCESS_STATE)


def describe_tasks(tasks):
    described = []
    for task in tasks:
        state = task.get("state")
        failure = (task.get("failure") or {}).get("class")
        described.append((task.get("capability"), task.get("model_id"), f"{state}/{failure}" if failure else state))
    return described


def title_generated(title):
    return bool(title) and title != DEFAULT_SESSION_TITLE


def required_assets_ready(task):
    """Return (ok, reason) for the required artifacts of a terminal task payload."""
    if task.get("state") != SUCCESS_STATE:
        return False, f"state {task.get('state')}"
    # resources_pending reports any pending slot, including optional/background ones (a
    # thumbnail with minimum=0 may still be rendering): only required artifacts decide pass/fail.
    required = [asset for asset in task.get("assets") or [] if asset.get("required")]
    if not required:
        return False, "no required artifact declared"
    for asset in required:
        if not asset.get("url") or not isinstance(asset.get("size"), int) or asset["size"] <= 0:
            return False, f"required artifact {asset.get('role')} has no bytes"
        status = asset.get("status")
        if status is not None and str(status).upper() != "READY":
            return False, f"required artifact {asset.get('role')} is {status}"
    note = "; optional resources still pending" if task.get("resources_pending") else ""
    return True, f"{len(required)} required artifact(s) ready{note}"


def pick_model_id(offerings, preferred=()):
    """Model ID (not Offering ID) of the first available offering, preferring the given IDs/substrings."""
    available = [item for item in offerings if item.get("available") and item.get("model_id")]
    for wanted in preferred:
        for item in available:
            if wanted in (item["model_id"], item.get("id")) or wanted in item["id"]:
                return item["model_id"]
    return available[0]["model_id"] if available else None


def offering_model_ids(offerings):
    return {item["id"]: item["model_id"] for item in offerings if item.get("available") and item.get("model_id")}


def multiview_request(group, model, slots):
    """Structured multi-view request from succeeded front/left/back(/right) slots of a three-view group."""
    views = [{"view": slot["key"], "task_id": slot["task_id"], "asset_id": slot["output_asset_id"]}
             for slot in slots
             if slot.get("status") == "succeeded" and slot.get("key") in ("front", "left", "back", "right")
             and slot.get("task_id") and slot.get("output_asset_id")]
    return {"group": group, "model": model, "views": views}


def slot_states(group):
    return {slot["key"]: slot["status"] for slot in (group or {}).get("slots", [])}


def views_settled(group, keys=("front", "left", "back")):
    states = slot_states(group)
    return all(states.get(key) in ("succeeded", "failed", "canceled", "denied") for key in keys)


def safe_name(text):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", text or "x").strip("-")[:60] or "x"


# ------------------------------------------------- budget approval mode (#103)
#
# Wire facts (server side): GET pending-approvals lists {"items": [...]}; a production plan card has
# kind "plan", request_id = its budget ID, budget_credits = its limit, estimated_credits = the server's
# quote of all steps and plan = {title, steps[{capability, offering_id, purpose, estimated_credits}],
# correction_capabilities}. Every other pending item is a per-step approval card. The plan is decided
# on the Schema route chat_session_budget (PUT {sessionID}/{budgetID}, body {<chat.api.fields.decision>:
# "approve"|"deny"}), answered with {budget_id, state, message_id}. The session read carries the current
# (active or exhausted) plan as "budget": {id, state, limit_credits, spent_credits, reserved_credits,
# remaining_credits, plan}.

PLAN_KIND = "plan"
APPROVE, DENY = "approve", "deny"
# schema.PriceAccumulator's bucket for integer (non-native) credit estimates.
LEGACY_UNIT = "legacy.credits"
# Key of a single limit that bounds the sum over every unit (see budget_limits).
ANY_UNIT = "*"
# Members that may carry one limit per unit, each entry {"unit", "amount"} like the session's
# price_totals. #120 item 7 introduces per-unit limits; adjust the names here when its wire lands.
PER_UNIT_LIMIT_MEMBERS = ("limit_totals", "budget_totals", "limits")
FAILED_STATES = ("FAILED", "CANCELED", "CANCELLED")


def plan_cards(items):
    """The production plan cards among pending approvals (newest first, as the server lists them)."""
    return [item for item in items or [] if isinstance(item, dict) and item.get("kind") == PLAN_KIND]


def step_cards(items):
    """The per-step approval cards among pending approvals: every item that is not a plan card."""
    return [item for item in items or [] if isinstance(item, dict) and item.get("kind") != PLAN_KIND]


def plan_card_problems(card):
    """What a plan card lacks to be decided as the browser shows it: a budget ID, a positive limit that
    covers the server's quote, and at least one step, each naming its capability."""
    if not isinstance(card, dict):
        return ["no plan card"]
    problems = []
    if not card.get("request_id"):
        problems.append("no budget id")
    limit = card.get("budget_credits")
    if not isinstance(limit, int) or isinstance(limit, bool) or limit <= 0:
        problems.append(f"budget_credits {limit!r}")
    steps = (card.get("plan") or {}).get("steps") if isinstance(card.get("plan"), dict) else None
    if not isinstance(steps, list) or not steps:
        problems.append("no steps")
    elif any(not isinstance(step, dict) or not step.get("capability") for step in steps):
        problems.append("a step without capability")
    estimate = card.get("estimated_credits")
    if isinstance(limit, int) and isinstance(estimate, int) and estimate > limit:
        problems.append(f"limit {limit} below the quote {estimate}")
    return problems


def plan_capabilities(card):
    steps = ((card or {}).get("plan") or {}).get("steps") or []
    return [step.get("capability") for step in steps if isinstance(step, dict)]


def budget_route(template, session_id, budget_id):
    """The plan decision route built from the Schema template the way the browser does
    (decideSessionBudget): both placeholders filled, IDs path-encoded. None when the template is
    missing or a placeholder is left (the #105 review found the card route left one unreplaced)."""
    if not template or not session_id or not budget_id:
        return None
    route = (template.replace("{sessionID}", urllib.parse.quote(str(session_id), safe=""))
             .replace("{budgetID}", urllib.parse.quote(str(budget_id), safe="")))
    return None if "{" in route or "}" in route else route


def decision_body(fields, approve):
    """The decision request body with the Schema's member name (chat.api.fields.decision), or None
    when the Schema does not declare it (the browser refuses to decide then, too)."""
    member = (fields or {}).get("decision")
    return {member: APPROVE if approve else DENY} if member else None


def native_amount(value):
    """A non-negative native amount ("12.5", 10) as a Decimal, or None."""
    if isinstance(value, bool):
        return None
    try:
        amount = decimal.Decimal(str(value))
    except (decimal.InvalidOperation, ValueError, TypeError):
        return None
    return amount if amount.is_finite() and amount >= 0 else None


def quote_price(quote):
    """(unit, total) of a frozen native quote, or None."""
    if isinstance(quote, dict) and quote.get("unit"):
        amount = native_amount(quote.get("total_amount"))
        if amount is not None:
            return quote["unit"], amount
    return None


def offering_priced(offering):
    """The Offering has a positive server quote, or is not metered: a metered step quoted at zero
    cannot enter a production plan (the proposal tool refuses it)."""
    if not offering.get("credits_metered"):
        return True
    legacy = offering.get("estimated_credits")
    if isinstance(legacy, int) and not isinstance(legacy, bool) and legacy > 0:
        return True
    price = quote_price(offering.get("default_native_quote"))
    return bool(price and price[1] > 0)


def budget_model(offerings, wanted=None):
    """model_id of an available, priced Offering for the budget chain: `wanted` when it is one, else
    Tripo P2 (low poly), else another Tripo Offering, else any. None when none qualifies."""
    priced = [item for item in offerings if item.get("available") and item.get("model_id") and offering_priced(item)]
    if wanted:
        return wanted if any(item.get("model_id") == wanted for item in priced) else None
    tripo = [item for item in priced if str(item.get("id", "")).startswith("tripo/")]
    return pick_model_id(tripo, ("tripo-p2",)) or pick_model_id(priced)


def call_prices(messages):
    """Price of each task an Agent tool call accepted, by task ID: (unit, amount). A native quote is
    priced in its own unit (total_amount); a legacy step in legacy credits (estimatedCredits, which the
    server omits when it is zero)."""
    prices = {}
    for message in messages or []:
        for call in parse_tool_calls(message):
            task_id = call.get("taskId")
            if not task_id or task_id in prices:
                continue
            price = quote_price(call.get("native_quote"))
            if price is None:
                credits = native_amount(call.get("estimatedCredits"))
                price = (LEGACY_UNIT, credits) if credits is not None else None
            if price is not None:
                prices[task_id] = price
    return prices


def task_price(task, prices, detail=None):
    """(unit, amount) of one task: the accepting tool call's price, else the task's own frozen native
    quote, else the owner's credits_charged (legacy pricing, SUCCEEDED only). None when nothing public
    prices it (a step that is not metered)."""
    price = prices.get(task.get("id")) or quote_price(task.get("native_quote"))
    if price is None and detail:
        credits = native_amount(detail.get("credits_charged"))
        price = (LEGACY_UNIT, credits) if credits is not None else None
    return price


def may_be_charged(task):
    """The public side of the server's settlement rule: every task may have spent its reservation,
    except a FAILED/CANCELED one whose failure is not marked ambiguous."""
    if task.get("state") not in FAILED_STATES:
        return True
    return bool((task.get("failure") or {}).get("ambiguous"))


def budget_limits(payload):
    """Credit limits of a plan card or a session budget, by unit.

    #120 item 7 moves the limit to one amount per unit (a list of {"unit", "amount"} under one of
    PER_UNIT_LIMIT_MEMBERS). Until then the single limit_credits (budget_credits on a plan card) bounds
    the sum over every unit, keyed ANY_UNIT: budget.EstimateNativeTotal counts one native unit as one
    credit when it reserves a step. Empty when the payload has no readable limit."""
    payload = payload if isinstance(payload, dict) else {}
    for member in PER_UNIT_LIMIT_MEMBERS:
        entries = payload.get(member)
        if not isinstance(entries, list) or not entries:
            continue
        limits = {}
        for entry in entries:
            if not isinstance(entry, dict) or not entry.get("unit"):
                return {}
            amount = native_amount(entry.get("amount", entry.get("limit")))
            if amount is None:
                return {}
            limits[entry["unit"]] = amount
        return limits
    for member in ("limit_credits", "budget_credits"):
        limit = native_amount(payload.get(member))
        if limit is not None and limit > 0:
            return {ANY_UNIT: limit}
    return {}


def spend_by_unit(prices):
    totals = {}
    for unit, amount in prices:
        totals[unit] = totals.get(unit, decimal.Decimal(0)) + amount
    return totals


def budget_overruns(prices, limits):
    """Where the priced steps exceed the plan's limits, as (unit, spent, limit) triples; empty when
    every unit stays within its own limit.

    Per-unit limits are checked per unit, never summed across units; a unit that spent without a limit
    of its own is an overrun with limit None. A single limit (ANY_UNIT) bounds the sum over all units,
    each step rounded up to whole credits as the server reserves it."""
    if not limits:
        return [(ANY_UNIT, sum((amount for _, amount in prices), decimal.Decimal(0)), None)]
    if ANY_UNIT in limits:
        spent = decimal.Decimal(sum(math.ceil(amount) for _, amount in prices))
        return [] if spent <= limits[ANY_UNIT] else [(ANY_UNIT, spent, limits[ANY_UNIT])]
    overruns = []
    for unit, spent in sorted(spend_by_unit(prices).items()):
        limit = limits.get(unit)
        if (limit is None and spent > 0) or (limit is not None and spent > limit):
            overruns.append((unit, spent, limit))
    return overruns


def ledger_overrun(summary):
    """The server's own plan ledger: spent plus reserved must stay within limit_credits. Returns a
    reason when it does not, None when it does; "" when the summary has no single limit to check."""
    if not isinstance(summary, dict) or not isinstance(summary.get("limit_credits"), int):
        return ""
    spent = (summary.get("spent_credits") or 0) + (summary.get("reserved_credits") or 0)
    if spent > summary["limit_credits"]:
        return f"spent {summary.get('spent_credits')} + reserved {summary.get('reserved_credits')} > limit {summary['limit_credits']}"
    return None


def session_charges(session):
    """Non-zero spending a session read reports: its legacy estimate, its per-unit price_totals and
    the spent/reserved credits of its current plan. Empty when nothing was charged."""
    session = session if isinstance(session, dict) else {}
    found = []
    credits = native_amount(session.get("estimatedCredits"))
    if credits:
        found.append(f"estimatedCredits {credits}")
    for entry in session.get("price_totals") or []:
        amount = native_amount((entry or {}).get("amount"))
        if amount:
            found.append(f"{entry.get('unit')} {amount}")
    plan = session.get("budget") if isinstance(session.get("budget"), dict) else {}
    for member in ("spent_credits", "reserved_credits"):
        if plan.get(member):
            found.append(f"budget {member} {plan[member]}")
    return found


# The plan decision notice the server posts (approval_policy.budget.messages.denied), or an Agent
# reply in the same sense: the plan was not approved and no step ran.
NOT_EXECUTED = re.compile(r"未执行|没有执行|不会执行|未获批准|没有获批|not (?:been )?(?:run|executed|approved)", re.I)


# --------------------------------------------------------------- report

class Report:
    def __init__(self, name, directory=None):
        self.name = name
        self.directory = directory
        self.items = []
        if directory:
            os.makedirs(directory, exist_ok=True)

    def record(self, status, item, detail=""):
        detail = one_line(detail, 200)
        self.items.append({"status": status, "item": item, "detail": detail})
        print(f"{status}: {item}" + (f" - {detail}" if detail else ""), flush=True)

    def passed(self, item, detail=""):
        self.record("PASS", item, detail)

    def failed(self, item, detail=""):
        self.record("FAIL", item, detail)

    def skipped(self, item, detail=""):
        self.record("SKIP", item, detail)

    def check(self, item, ok, detail=""):
        self.record("PASS" if ok else "FAIL", item, detail)
        return ok

    def counts(self):
        return {key: sum(1 for entry in self.items if entry["status"] == key) for key in ("PASS", "FAIL", "SKIP")}

    def summary_line(self):
        counts = self.counts()
        return f"=== {self.name}: PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']} ==="

    def exit_code(self):
        return 1 if self.counts()["FAIL"] else 0

    def finish(self):
        print(self.summary_line(), flush=True)
        if self.directory:
            with open(os.path.join(self.directory, f"{self.name}.json"), "w", encoding="utf-8") as handle:
                json.dump({"script": self.name, "counts": self.counts(), "items": self.items}, handle,
                          ensure_ascii=False, indent=2)
        return self.exit_code()


# ---------------------------------------------------------------- client

class Client:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None, timeout=60, headers=None):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Origin", self.base)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        for key, value in (headers or {}).items():
            request.add_header(key, value)
        try:
            with self.opener.open(request, timeout=timeout) as response:
                payload = response.read()
                return response.status, (json.loads(payload) if payload else {})
        except urllib.error.HTTPError as error:
            payload = error.read()
            try:
                return error.code, json.loads(payload)
            except ValueError:
                return error.code, {}
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, {}

    def read_chat_stream(self, session_id, timeout):
        """Follow the session's chat stream from its first event for at most `timeout` seconds.

        Returns "done" (turn-done event seen, or HTTP 204: no stream is held any more because the
        turn ended and its cleanup window passed), "open" (still running when time ran out) or
        "error" (connection failure or an unexpected status).
        """
        request = urllib.request.Request(f"{self.base}/api/chat/sessions/{session_id}/stream?fromId=0",
                                         headers={"Accept": "text/event-stream"})
        request.add_header("Origin", self.base)
        deadline = time.time() + timeout
        events = []
        try:
            with self.opener.open(request, timeout=min(max(timeout, 1), 30)) as response:
                if response.status == 204:
                    return "done"
                for raw in response:
                    events.extend(parse_sse_events([raw]))
                    if stream_finished(events):
                        return "done"
                    if time.time() >= deadline:
                        return "open"
        except urllib.error.HTTPError as error:
            return "done" if error.code == 204 else "error"
        except (urllib.error.URLError, TimeoutError, OSError):
            return "open" if time.time() >= deadline - 1 else "error"
        return "error"  # the server closed the stream without a done event

    def probe_bytes(self, path, count=16):
        """First bytes of a protected asset (status, bytes), without downloading the whole file."""
        request = urllib.request.Request(self.base + path, headers={"Range": f"bytes=0-{count - 1}"})
        try:
            with self.opener.open(request, timeout=120) as response:
                return response.status, response.read(count)
        except urllib.error.HTTPError as error:
            return error.code, b""
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, b""

    def upload_image(self, data, media_type="image/png", filename="snapshot.png"):
        """Upload picture bytes as an input asset (multipart, field `file`); returns the asset ID or None."""
        boundary = "l5boundary7f3a9c"
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
                f"Content-Type: {media_type}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(self.base + "/api/assets/upload", data=body, method="POST")
        request.add_header("Origin", self.base)
        request.add_header("Content-Type", "multipart/form-data; boundary=" + boundary)
        try:
            with self.opener.open(request, timeout=120) as response:
                return json.loads(response.read()).get("asset_id")
        except (urllib.error.URLError, TimeoutError, OSError, ValueError):
            return None

    def fetch(self, path, limit=40 << 20):
        request = urllib.request.Request(self.base + path)
        try:
            with self.opener.open(request, timeout=300) as response:
                return response.status, response.read(limit)
        except urllib.error.HTTPError as error:
            return error.code, b""
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, b""

    def download(self, path, destination):
        request = urllib.request.Request(self.base + path)
        try:
            with self.opener.open(request, timeout=300) as response, open(destination, "wb") as handle:
                size = 0
                while True:
                    chunk = response.read(1 << 20)
                    if not chunk:
                        return response.status, size
                    handle.write(chunk)
                    size += len(chunk)
        except urllib.error.HTTPError as error:
            return error.code, 0
        except (urllib.error.URLError, TimeoutError, OSError):
            return 0, 0

    def login(self, username, password):
        status, _ = self.call("POST", "/api/auth/login", {"username": username, "password": password})
        return status == 200


# ------------------------------------------------------------ live helpers

def require_ok(status, what):
    """Raise on a non-200 status so execute() records a FAIL instead of reading an error body as empty data."""
    if status != 200:
        raise RuntimeError(f"{what}: HTTP {status}")


class Live:
    """Session-level operations used by the scripts."""

    def __init__(self, client, report, arguments):
        self.client = client
        self.report = report
        self.arguments = arguments
        # The domain, model and extra fields (for example structured
        # parameters) of each session's last message, reused when a choice card
        # is answered; and the cards whose answer the server accepted.
        self._last_send = {}
        self._answered_choices = set()
        self._schema = None

    # sessions
    def new_session(self, title=DEFAULT_SESSION_TITLE, approval="auto"):
        status, data = self.client.call("POST", "/api/chat/sessions", {"title": title})
        session_id = data.get("id")
        if status not in (200, 201) or not session_id:
            return None
        if approval:
            self.client.call("PUT", f"/api/v1/chat/sessions/{session_id}/approval", {"approvalMode": approval})
        return session_id

    def send(self, session_id, content, domain, model, **extra):
        if "choiceAnswer" not in extra:
            self._last_send[session_id] = (domain, model, dict(extra))
        body = {"content": content, "creative_domain": domain, "ai_model": model}
        body.update(extra)
        # 409 means the session has not released the previous turn yet; the rejected message was not
        # accepted, so waiting and sending it again does not duplicate anything.
        for _ in range(12):
            status, data = self.client.call("POST", f"/api/chat/sessions/{session_id}/messages", body)
            if status != 409:
                break
            time.sleep(15)
        if status not in (200, 201, 202):
            print(f"message rejected: HTTP {status} {one_line(json.dumps(data, ensure_ascii=False), 160)}", flush=True)
        return status, data

    def lock_model_3d(self, session_id, model):
        """Pick a 3D model in the session's model picker, as a person does: since #103 only this
        explicit pick binds the 3D tools; the per-message ai_model is only the picker's display."""
        status, data = self.client.call("PUT", f"/api/v1/chat/sessions/{session_id}/model3d",
                                        {"explicit_model_3d": model})
        require_ok(status, "lock 3D model")
        return data

    def set_brief(self, session_id, brief):
        """Fill the session's production brief, as a person does in the brief panel (#103)."""
        status, data = self.client.call("PUT", f"/api/v1/chat/sessions/{session_id}/brief", {"brief": brief})
        require_ok(status, "set production brief")
        return data

    def session(self, session_id):
        status, data = self.client.call("GET", f"/api/chat/sessions/{session_id}")
        return data if status == 200 else None

    def messages(self, session_id):
        status, data = self.client.call("GET", f"/api/chat/sessions/{session_id}/messages?limit=100")
        require_ok(status, "list messages")
        return (data.get("messages") if isinstance(data, dict) else data) or []

    def tasks(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/tasks?session_id={session_id}&limit=50")
        require_ok(status, "list tasks")
        return data.get("tasks") or data.get("data") or []

    def task(self, task_id):
        status, data = self.client.call("GET", f"/api/v1/tasks/{task_id}")
        return data if status == 200 else {}

    def offerings(self, capability):
        status, data = self.client.call("GET", f"/api/v1/offerings?capability={capability}")
        require_ok(status, "list offerings")
        return data.get("offerings") or data.get("data") or (data if isinstance(data, list) else [])

    # waiting: the turn must be over (chat stream done event) first, then the expected tasks must exist
    # and be terminal. Message content is not a completion signal: planning text is persisted before
    # tools run.
    def turn_finished(self, session_id, probe=3):
        return self.client.read_chat_stream(session_id, probe) == "done"

    def wait_reply(self, session_id, timeout=None, poll=4):
        """The final assistant message once the turn is over, or None when it never finished in time."""
        deadline = time.time() + (timeout or self.arguments.turn_timeout)
        while time.time() < deadline:
            outcome = self.client.read_chat_stream(session_id, min(max(deadline - time.time(), 1), 60))
            if outcome == "done":
                for _ in range(5):  # the done event is written after the final message is stored
                    messages = self.messages(session_id)
                    if reply_is_final(messages):
                        return last_assistant(messages)
                    time.sleep(1)
                return None
            if outcome == "error":
                time.sleep(poll)
        return None

    def wait_tasks(self, session_id, expected, timeout=None, poll=15, watch=None):
        """Wait until the expected tasks exist and all tasks are terminal. `watch(session_id)` is called
        on every poll; a true result stops the wait early (unsettled), for example on a card nobody
        will decide."""
        deadline = time.time() + (timeout or self.arguments.task_timeout)
        tasks = []
        while time.time() < deadline:
            tasks = self.tasks(session_id)
            settled, _ = tasks_settled(tasks, expected)
            if settled:
                return tasks, True
            if watch is not None and watch(session_id):
                return tasks, False
            # #103: the Agent may stop at a choice card (for example the 3D usage
            # card) before the expected step; answer it as a person would.
            self.answer_pending_choices(session_id)
            time.sleep(poll)
        return tasks, False

    def pending_choices(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/chat/sessions/{session_id}/choices")
        if status != 200 or not isinstance(data, dict):
            return []
        return [card for card in data.get("choices") or []
                if isinstance(card, dict) and card.get("state") == "pending"
                and card.get("id") not in self._answered_choices]

    def answer_pending_choices(self, session_id):
        """Answer each pending choice card once, after its turn has finished, with the default
        (or else the recommended, or else the first) option. Returns whether a card was answered."""
        cards = self.pending_choices(session_id)
        if not cards or session_id not in self._last_send or not self.turn_finished(session_id, probe=2):
            return False
        domain, model, extra = self._last_send[session_id]
        for card in cards:
            options = [option for option in card.get("options") or [] if isinstance(option, dict) and option.get("id")]
            if card.get("allow_default") and card.get("default_option_id"):
                answer, label = {"choice_id": card["id"], "use_default": True}, "default"
            elif options:
                pick = next((option for option in options if option.get("recommended")), options[0])
                answer, label = {"choice_id": card["id"], "option_id": pick["id"]}, pick["id"]
            else:
                continue
            # The answer turn carries the original request's fields (for example
            # its structured parameters): the server reads them from this turn only.
            status, _ = self.send(session_id, "按默认选项继续。", domain, model, **extra, choiceAnswer=answer)
            if status not in (200, 201, 202):
                # The card stays pending on the server unless the answer landed; the
                # next poll reads its state again and retries only if still pending.
                print(f"choice card {card.get('id')} answer not accepted: HTTP {status}", flush=True)
                return False
            self._answered_choices.add(card["id"])
            print(f"answered choice card {card.get('tool', '')} with {label}", flush=True)
            return True
        return False

    def run_turn(self, session_id, content, domain, model, expected, watch=None, **extra):
        """Send a message, wait for the final reply, then for the expected tasks (see wait_tasks for
        `watch`). Returns (reply, tasks, ok)."""
        status, data = self.send(session_id, content, domain, model, **extra)
        if status not in (200, 201, 202):
            self.report.failed("message accepted", f"HTTP {status}")
            return None, [], False
        reply = self.wait_reply(session_id)
        if reply is None:
            self.report.failed("final reply", "timed out waiting for the turn to finish")
            return None, self.tasks(session_id), False
        # A turn that stopped at a choice card (for example the #103 usage card) has not done the
        # request yet, even when no task is expected; answer it and wait for the turn it starts.
        for _ in range(3):
            if not self.answer_pending_choices(session_id):
                break
            reply = self.wait_reply(session_id)
            if reply is None:
                self.report.failed("final reply", "timed out waiting for the turn after the choice answer")
                return None, self.tasks(session_id), False
        answered = len(self._answered_choices)
        tasks, settled = self.wait_tasks(session_id, expected, watch=watch)
        if len(self._answered_choices) > answered:
            # Answering a choice card started a turn of its own, which may still be
            # reviewing the result after the tasks settled; the next message would be
            # rejected until it ends, and its final message is this turn's real reply.
            reply = self.wait_reply(session_id)
            if reply is None:
                self.report.failed("final reply", "timed out waiting for the turn after the choice answer")
                return None, self.tasks(session_id), False
        return reply, tasks, settled

    def login(self):
        environ = os.environ
        if not self.client.login(environ["ADMIN_USERNAME"], environ["ADMIN_PASSWORD"]):
            self.report.failed("login")
            return False
        self.report.passed("login")
        return True

    def verify_task_artifacts(self, task_id, label):
        """Terminal task payload: SUCCEEDED, required artifacts present, bytes readable through the API."""
        payload = self.task(task_id)
        ok, reason = required_assets_ready(payload)
        if not ok:
            self.report.failed(f"{label} required artifacts READY", reason)
            return False
        for asset in payload["assets"]:
            if not asset.get("required"):
                continue
            status, head = self.client.probe_bytes(asset["url"])
            if status not in (200, 206) or not head:
                self.report.failed(f"{label} required artifact readable", f"{asset.get('role')} HTTP {status}")
                return False
        self.report.passed(f"{label} required artifacts READY", reason)
        return True

    def first_required_image(self, task_id):
        """(width, height) of the first required image artifact of a task, or None."""
        for asset in self.task(task_id).get("assets") or []:
            if asset.get("required") and str(asset.get("media_type", "")).startswith("image/"):
                status, data = self.client.fetch(asset["url"])
                return common_dimensions(data) if status == 200 else None
        return None

    def pending_approvals(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/chat/sessions/{session_id}/pending-approvals")
        require_ok(status, "list pending approvals")
        items = data.get("approvals") or data.get("items") or data.get("data") or (data if isinstance(data, list) else [])
        return [item for item in items if item.get("status") == "pending"]

    # budget approval mode (#103)
    def approval_mode(self, session_id):
        status, data = self.client.call("GET", f"/api/v1/chat/sessions/{session_id}/approval")
        require_ok(status, "read approval mode")
        return data.get("approvalMode")

    def public_schema(self):
        """The public Product Schema (GET /api/v1/schema answers {"schema": {...}}), read once."""
        if self._schema is None:
            status, data = self.client.call("GET", "/api/v1/schema")
            require_ok(status, "read product schema")
            self._schema = data.get("schema") if isinstance(data.get("schema"), dict) else data
        return self._schema

    def plan_route(self, session_id, budget_id):
        """The Schema's chat_session_budget route for this plan card, or None (see budget_route)."""
        routes = ((self.public_schema().get("runtime") or {}).get("http") or {}).get("routes") or {}
        return budget_route(routes.get("chat_session_budget"), session_id, budget_id)

    def decide_plan(self, session_id, budget_id, approve):
        """Approve or deny a production plan card as the browser does (decideSessionBudget). Returns
        (status, payload); status 0 when the Schema lacks the route or the decision member."""
        route = self.plan_route(session_id, budget_id)
        body = decision_body(((self.public_schema().get("chat") or {}).get("api") or {}).get("fields"), approve)
        if route is None or body is None:
            return 0, {}
        return self.client.call("PUT", route, body)

    def wait_plan_card(self, session_id, timeout=None, poll=5, quiet=3):
        """After a finished turn: wait for the session's pending plan card, answering choice cards on
        the way. Returns (plan card or None, per-step cards pending at that moment). Gives up when the
        turn has ended and `quiet` polls in a row find no card of any kind."""
        deadline = time.time() + (timeout or self.arguments.turn_timeout)
        idle = 0
        while time.time() < deadline:
            pending = self.pending_approvals(session_id)
            plans, steps = plan_cards(pending), step_cards(pending)
            if plans or steps:
                return (plans[0] if plans else None), steps
            if self.answer_pending_choices(session_id):
                idle = 0
                if self.wait_reply(session_id, timeout=max(deadline - time.time(), 1)) is None:
                    return None, []
                continue
            if self.turn_finished(session_id, probe=2):
                idle += 1
                if idle >= quiet:
                    return None, []
            time.sleep(poll)
        return None, []

    def save_asset(self, task_id, asset_id, destination):
        status, size = self.client.download(f"/api/v1/tasks/{task_id}/assets/{asset_id}", destination)
        return status == 200 and size > 0


def single_image_3d_model(live):
    """model_id for single-image 3D: L5_3D_MODEL when set (a model_id the Schema lists), else the first
    available Tripo single-image Offering, else the first available image-to-3d Offering."""
    offerings = live.offerings("image-to-3d")
    wanted = os.environ.get("L5_3D_MODEL")
    if wanted:
        return wanted if any(o.get("model_id") == wanted and o.get("available") for o in offerings) else None
    plain = [o for o in offerings if "multiview" not in o.get("id", "") and "segment" not in o.get("id", "")]
    return pick_model_id([o for o in plain if o["id"].startswith("tripo/")]) or pick_model_id(plain)


def multiview_3d_model(live):
    """model_id for multi-view 3D: L5_MULTIVIEW_MODEL when set, else the first available Tripo multiview Offering."""
    offerings = [o for o in live.offerings("image-to-3d") if "multiview" in o.get("id", "")]
    wanted = os.environ.get("L5_MULTIVIEW_MODEL")
    if wanted:
        return wanted if any(o.get("model_id") == wanted and o.get("available") for o in offerings) else None
    return pick_model_id([o for o in offerings if o["id"].startswith("tripo/")] or offerings)


def common_dimensions(data):
    return image_dimensions(data)


def execute(name, body, description=""):
    """Entry point shared by every script: ACK gate, environment, login, body(live, report), summary."""
    refusal = ack_error(os.environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    missing = missing_environment(os.environ)
    if missing:
        print("missing environment: " + ", ".join(missing) + " (run through scripts/release-env.py smoke)",
              file=sys.stderr)
        return 2
    problem = validate_base(os.environ["BASE"])
    if problem:
        print(problem, file=sys.stderr)
        return 2
    arguments = parse_arguments(sys.argv[1:], description)
    report = Report(name, arguments.report)
    live = Live(Client(os.environ["BASE"]), report, arguments)
    try:
        if live.login():
            body(live, report)
    except KeyboardInterrupt:
        report.failed("interrupted")
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {one_line(str(error), 120)}")
    return report.finish()
