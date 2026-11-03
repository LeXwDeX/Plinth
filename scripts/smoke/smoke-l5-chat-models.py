#!/usr/bin/env python3
"""L5: every available chat model x each of its reasoning levels, one agent turn each.

Pass criterion per cell: the level can be set, and the turn ends with final
content and no error. The turn asks for a one-sentence answer and no tools, so
the cost is chat tokens only. Needs ART_AI_LIVE_ACK=1; see README.md (L5).
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

PROMPT = "只用一句中文回答：你是谁？不要调用任何工具，不要生成图片。"


def body(live, report):
    client = live.client
    image_model = common.pick_model_id(live.offerings("text-to-image"))
    if not image_model:
        report.failed("text-to-image model available")
        return
    probe = live.new_session("l5-chat-models-probe")
    status, data = client.call("GET", f"/api/v1/chat/sessions/{probe}/model")
    models = [item.get("id") for item in data.get("chatModels") or [] if item.get("id")]
    report.check("chat models offered", status == 200 and len(models) >= 1, f"{len(models)} model(s)")
    for chat_model in models:
        session = live.new_session(f"l5-chat-{common.safe_name(chat_model)}")
        status, _ = client.call("PUT", f"/api/v1/chat/sessions/{session}/model", {"chatModel": chat_model})
        if status != 200:
            report.failed(f"{chat_model} select model", f"HTTP {status}")
            continue
        _, reasoning = client.call("GET", f"/api/v1/chat/sessions/{session}/reasoning")
        levels = reasoning.get("reasoningLevels") or []
        if not levels:
            report.skipped(f"{chat_model} reasoning levels", "model declares no reasoning levels")
            continue
        for level in levels:
            cell = f"{chat_model}/{level}"
            turn = live.new_session(f"l5-chat-{common.safe_name(chat_model)}-{level}")
            client.call("PUT", f"/api/v1/chat/sessions/{turn}/model", {"chatModel": chat_model})
            status, applied = client.call("PUT", f"/api/v1/chat/sessions/{turn}/reasoning", {"reasoningEffort": level})
            if status != 200 or applied.get("reasoningEffort") != level:
                report.failed(f"{cell} set level", f"HTTP {status}")
                continue
            sent, _ = live.send(turn, PROMPT, "image", image_model)
            if sent not in (200, 201, 202):
                report.failed(f"{cell} message accepted", f"HTTP {sent}")
                continue
            reply = live.wait_reply(turn, timeout=min(live.arguments.turn_timeout, 600))
            messages = live.messages(turn)
            clean = common.reply_is_clean(reply) and not common.tool_errors(messages)
            report.check(f"{cell} final reply without error", clean,
                         common.one_line((reply or {}).get("content") or (reply or {}).get("error") or "no reply", 50))


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-chat-models", body, __doc__))
