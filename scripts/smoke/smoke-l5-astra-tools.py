#!/usr/bin/env python3
"""L5: one real gpt-astra Agent turn with a read-only tool (#417, #401).

The Responses Adapter for the dev-test chat model gpt-astra had never run a
whole Agent turn with tools against the real upstream (#401). This script
selects the model on a fresh session, sends a question that needs the read-only
"list offerings" tool and asserts:

  * the model is offered by this deployment and the session reads it back after
    PUT /api/v1/chat/sessions/{id}/model;
  * the turn ends (the chat stream's done event and a final reply) without a
    failure text and without a tool error;
  * the reply's toolCalls include list_offerings (the read-only tool the
    question needs);
  * the session created zero generation Tasks (chat tokens only).

If the deployment does not offer gpt-astra the run is SKIP, with the offered
model ids. If the model is offered but the turn fails, the failure text (with
its error reference) is recorded so the upstream error can be followed up.
Needs both --live and ART_AI_LIVE_ACK=1 and a live release; see README.md (L5).
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

PROMPT = ("查一下当前可用的 3D 模型，告诉我其中最便宜的是哪一个。"
          "只查资料回答，不要生成图片或 3D 模型，也不要调用任何生成工具。")
READ_ONLY_TOOL = "list_offerings"


def extra_arguments(parser):
    parser.add_argument("--chat-model", default=os.environ.get("L5_ASTRA_MODEL", "gpt-astra"),
                        help="chat model to select on the session (default gpt-astra)")


def offered_models(live, session):
    status, data = live.client.call("GET", f"/api/v1/chat/sessions/{session}/model")
    if status != 200:
        return None, [], data
    return data.get("chatModel"), [item.get("id") for item in data.get("chatModels") or [] if item.get("id")], data


def body(live, report):
    started = time.time()
    model = live.arguments.chat_model
    session = live.new_session(f"l5-astra-{common.safe_name(model)}", approval="auto")
    if not report.check("session created", bool(session)):
        return
    current, models, raw = offered_models(live, session)
    if not models:
        report.failed("chat model list readable", f"HTTP response without chatModels: {sorted(raw)}")
        return
    if model not in models:
        report.skipped(f"chat model {model} offered by this deployment", f"offered: {', '.join(sorted(models))}")
        return
    status, _ = live.client.call("PUT", f"/api/v1/chat/sessions/{session}/model", {"chatModel": model})
    if not report.check(f"{model} selected on the session", 200 <= status < 300, f"HTTP {status}"):
        return
    current, models, _ = offered_models(live, session)
    if not report.check(f"{model} reads back from the session", current == model,
                        f"chatModel={current!r} (default before the PUT: {raw.get('chatModel')!r})"):
        return
    turn_model = (common.pick_model_id(live.offerings("image-to-3d"))
                  or common.pick_model_id(live.offerings("text-to-3d")) or "")
    status, _ = live.send(session, PROMPT, "model-3d", turn_model)
    if not report.check("message accepted", status in (200, 201, 202), f"HTTP {status}"):
        return
    reply = live.wait_reply(session)
    messages = live.messages(session)
    calls = [call for message in messages for call in common.parse_tool_calls(message)]
    names = [call.get("name") for call in calls if call.get("name")]
    tasks = live.tasks(session)
    content = (reply or {}).get("content") or ""
    evidence = {
        "chat_model": model,
        "session_id": session,
        "default_before": raw.get("chatModel"),
        "tool_calls": [{"name": call.get("name"),
                        "arguments": common.redact_url(str(call.get("arguments")))[:200],
                        "task_id": call.get("taskId")}
                       for call in calls],
        "final_reply": common.one_line(content, 240),
        "tasks": [{"id": task.get("id"), "capability": task.get("capability"), "state": task.get("state")}
                  for task in tasks],
        "elapsed_ms": int((time.time() - started) * 1000),
    }
    directory = live.arguments.report
    if directory:
        os.makedirs(directory, exist_ok=True)
        path = os.path.join(directory, "smoke-l5-astra-tools-evidence.json")
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(evidence, handle, ensure_ascii=False, indent=2)
        print(f"# evidence: {path}", flush=True)
    if reply is None:
        report.failed("turn finished", "timed out waiting for the final reply")
        return
    report.check("final reply without a failure text", common.reply_is_clean(reply),
                 common.one_line(content, 120) or "empty reply")
    tool_errors = common.tool_errors(messages)
    report.check("no tool error", not tool_errors, str(tool_errors) or "none")
    report.check(f"toolCalls include {READ_ONLY_TOOL}", READ_ONLY_TOOL in names, str(sorted(set(names))) or "none")
    report.check("no generation task created (zero generation cost)", not tasks, str(common.describe_tasks(tasks)))
    cheapest = [str(item.get("model_id")) for item in live.offerings("image-to-3d")
                if item.get("available") and item.get("model_id") and str(item.get("model_id")) in content]
    report.record("INFO", "reply names an available 3D model", ", ".join(sorted(set(cheapest))) or "no model id found")


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-astra-tools", body, __doc__, extra_arguments))