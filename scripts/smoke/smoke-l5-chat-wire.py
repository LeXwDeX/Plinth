#!/usr/bin/env python3
"""L5: one conversation-model wire round, the chat model only (#536, I30).

Drives the four turns whose wire the fake mirrors, through the public chat API, so
the deployment's upstream recorder (UPSTREAM_CAPTURE_DIR, scripts/smoke/README.md
"Opt-In Wire Capture") can take their samples:

  1. text-stream  a one-sentence answer, no tools: a streamed text completion, the
                  usage and the reasoning_content fragments of the model's family;
  2. tool-round   the same session, a question that needs the read-only
                  list_offerings tool: the tool round after a plain text round, with
                  the reasoning of the first round passed back (DeepSeek's rule, I28);
  3. image-read   a fresh session on a model that reads pictures: one small picture
                  and a question about it (the normal read-image wire, I27);
  4. title        the first turn's session title (the non-stream completion).

Every assertion is a hard one: a turn that fails, a failure text in the reply, a tool
error, a generation Task created (this script costs chat tokens only) or a missing
title is FAIL; a model the deployment does not offer is SKIP with the offered ids. The
bad-picture refusal (a strip too thin for the model) needs a request Plinth's own
minimum-size check would stop, so it is a raw read-only probe of the real round, not
a turn here.

The upstream gate is fake-default (upstream_shape.py): against a fake target the script
runs without switches (the fake chat model; --live is refused there); against a mixed
or live target both --live and ART_AI_LIVE_ACK=1 are needed, and each real run needs the
user's consent in the conversation first (AGENTS.md "上游接口与 Fake"). It never calls an
upstream itself: every request goes to Plinth.
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import upstream_shape  # noqa: E402

TEXT_PROMPT = "只用一句中文回答：你是谁？不要调用任何工具，不要生成图片。"
TOOL_PROMPT = ("查一下当前可用的 3D 模型，告诉我其中最便宜的是哪一个。"
               "只查资料回答，不要生成图片或 3D 模型，也不要调用任何生成工具。")
IMAGE_PROMPT = "这张图里最大的色块是什么颜色？只用一句中文回答，不要调用任何工具，不要生成图片。"
READ_ONLY_TOOL = "list_offerings"
# The fake chat model answers fixed text unless a marker asks for a call; a real model
# decides by itself, so the marker is added only for a fake target.
FAKE_TOOL_MARKER = "[[fake:tool:list_offerings]]"
DEFAULT_VISION_MODEL = "qwen3.8-omni-flash"
PICTURE_SIDE = 96


# ---------------------------------------------------------------- pure logic

def prompts(shape):
    """The four prompts; the tool turn carries the fake's marker only against a fake target."""
    tool = TOOL_PROMPT + (" " + FAKE_TOOL_MARKER if shape == "fake" else "")
    return {"text": TEXT_PROMPT, "tool": tool, "image": IMAGE_PROMPT}


def pick_vision_model(offered, preferred):
    """The model for the image turn: the preferred one when offered, else None (SKIP).

    No other model is guessed: whether a model reads pictures is the deployment's declaration, and
    a text-only model would only answer with the refusal that strips the picture."""
    return preferred if preferred in (offered or []) else None


def probe_picture(side=PICTURE_SIDE):
    """A side x side PNG: a red block on a white ground, large enough for every vision model's
    minimum size (Plinth refuses a thinner one itself, #535)."""
    red, white = (200, 40, 40), (245, 245, 245)
    low, high = side // 4, side - side // 4
    rows = [bytes(sum(((red if low <= x < high and low <= y < high else white) for x in range(side)), ()))
            for y in range(side)]
    return common.encode_png(side, side, rows)


def turn_problems(reply, messages, tasks, expected_tool=None):
    """What is wrong with a finished turn (empty list: nothing). Hard checks only, never the model's
    wording."""
    problems = []
    if reply is None:
        return ["no final reply (timed out)"]
    if not (reply.get("content") or "").strip():
        problems.append("empty reply")
    if not common.reply_is_clean(reply):
        problems.append("failure text in the reply: " + common.one_line(reply.get("content") or "", 80))
    errors = common.tool_errors(messages)
    if errors:
        problems.append(f"tool error: {errors}")
    names = [call.get("name") for message in messages for call in common.parse_tool_calls(message) if call.get("name")]
    if expected_tool and expected_tool not in names:
        problems.append(f"no {expected_tool} call (calls: {sorted(set(names)) or 'none'})")
    if tasks:
        problems.append(f"generation task created: {common.describe_tasks(tasks)}")
    return problems


def extra_arguments(parser):
    parser.add_argument("--vision-model", default=os.environ.get("L5_CHAT_VISION_MODEL", DEFAULT_VISION_MODEL),
                        help=f"chat model for the image turn (default {DEFAULT_VISION_MODEL}); SKIP when not offered")
    parser.add_argument("--title-timeout", type=int, default=90, help="seconds to wait for the session title")


# ---------------------------------------------------------------- live part

def chat_model(live, session):
    status, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/model")
    if status != 200:
        return None, []
    return data.get("chatModel"), [item.get("id") for item in data.get("chatModels") or [] if item.get("id")]


def run_turn(live, report, session, label, prompt, expected_tool=None):
    """Send one turn, wait for its final reply and judge it. Returns (ok, reply, messages, elapsed_ms)."""
    started = time.time()
    model_3d = (common.pick_model_id(live.offerings("image-to-3d"))
                or common.pick_model_id(live.offerings("text-to-3d")) or "")
    status, _ = live.send(session, prompt, "model-3d", model_3d)
    if not report.check(f"{label}: message accepted", status in (200, 201, 202), f"HTTP {status}"):
        return False, None, [], 0
    reply = live.wait_reply(session)
    messages = live.messages(session)
    problems = turn_problems(reply, messages, live.tasks(session), expected_tool)
    elapsed = int((time.time() - started) * 1000)
    report.check(f"{label}: final reply, no failure text, no tool error" + (f", {expected_tool} called" if expected_tool else "")
                 + ", no generation task", not problems,
                 "; ".join(problems) or common.one_line((reply or {}).get("content") or "", 80))
    return not problems, reply, messages, elapsed


def body(live, report):
    shape = upstream_shape.target_shape(os.environ)
    asked = prompts(shape)
    evidence = {"shape": shape, "turns": []}

    session = live.new_session("l5-chat-wire-text", approval="auto")
    if not report.check("session created", bool(session)):
        return
    model, offered = chat_model(live, session)
    evidence["chat_model"] = model
    evidence["offered"] = sorted(offered)
    report.check("session reads its chat model back", bool(model), f"chatModel={model!r}")
    ok, reply, _, elapsed = run_turn(live, report, session, "text-stream", asked["text"])
    evidence["turns"].append({"turn": "text-stream", "ok": ok, "elapsed_ms": elapsed,
                              "reply": common.one_line((reply or {}).get("content") or "", 160)})
    if ok:
        ok, reply, messages, elapsed = run_turn(live, report, session, "tool-round", asked["tool"], READ_ONLY_TOOL)
        evidence["turns"].append({"turn": "tool-round", "ok": ok, "elapsed_ms": elapsed,
                                  "tools": sorted({c.get("name") for m in messages for c in common.parse_tool_calls(m)
                                                   if c.get("name")}),
                                  "reply": common.one_line((reply or {}).get("content") or "", 160)})
    else:
        report.skipped("tool-round", "the text round failed first")

    # The title is a non-stream completion of the first turn's text.
    deadline = time.time() + live.arguments.title_timeout
    title = None
    while time.time() < deadline:
        title = (live.session(session) or {}).get("title")
        if common.title_generated(title):
            break
        time.sleep(3)
    report.check("title: the session title is generated", common.title_generated(title), common.one_line(title or "", 60))
    evidence["title"] = common.one_line(title or "", 60)

    vision = pick_vision_model(offered, live.arguments.vision_model)
    if vision is None:
        report.skipped(f"image-read: chat model {live.arguments.vision_model} offered by this deployment",
                       f"offered: {', '.join(sorted(offered))}")
    else:
        picture_session = live.new_session("l5-chat-wire-image", approval="auto")
        status, _ = live.client.call("PUT", f"/api/v1/chat/sessions/{picture_session}/model", {"chatModel": vision})
        if report.check(f"image-read: {vision} selected", 200 <= status < 300, f"HTTP {status}"):
            asset = live.client.upload_image(probe_picture(), filename="chat-wire-probe.png")
            if report.check("image-read: probe picture uploaded", bool(asset)):
                fields = {"selection": [{"kind": "image", "assetId": asset, "url": f"/api/v1/assets/{asset}"}],
                          "inputSource": "canvas"}
                started = time.time()
                model_3d = (common.pick_model_id(live.offerings("image-to-3d")) or "")
                status, _ = live.send(picture_session, asked["image"], "model-3d", model_3d, **fields)
                if report.check("image-read: message accepted", status in (200, 201, 202), f"HTTP {status}"):
                    reply = live.wait_reply(picture_session)
                    problems = turn_problems(reply, live.messages(picture_session), live.tasks(picture_session))
                    report.check("image-read: final reply, no failure text (the picture was read, not stripped)",
                                 not problems, "; ".join(problems) or common.one_line((reply or {}).get("content") or "", 80))
                    evidence["turns"].append({"turn": "image-read", "ok": not problems, "model": vision,
                                              "elapsed_ms": int((time.time() - started) * 1000),
                                              "reply": common.one_line((reply or {}).get("content") or "", 160)})

    directory = live.arguments.report
    if directory:
        os.makedirs(directory, exist_ok=True)
        path = os.path.join(directory, "smoke-l5-chat-wire-evidence.json")
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(evidence, handle, ensure_ascii=False, indent=2)
        print(f"# evidence: {path}", flush=True)


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-chat-wire", body, __doc__, extra_arguments, gate="fake-default"))
