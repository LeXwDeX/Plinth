#!/usr/bin/env python3
"""#688/#709 画板兜底：图只放在画板上（没有附到对话）时，Agent 是否查看画板并用画板的图（真实对话模型，live）。

层：dev-test-live-chat（`release-env.py release dev-test --live-chat` 的 mixed 形态：对话模型真实、生成全走 fake）。
花费：5 个新会话各一轮真实对话（只耗对话 token）。会话都用严格方式，Agent 发起的任何生成都停在批准卡上，脚本一律
拒绝，不产生生成费用。每次运行前须在当前对话取得用户同意，并同时给出 --live 与 ART_AI_LIVE_ACK=1，否则退出码 2；
目标是 fake 对话模型时也拒绝（chat-live 闸门）。

场景（每个一个新会话，脚本现场合成一张 512×512 的 RGB 人形图，经 /api/assets/upload 上传，再走浏览器用的画布保存接口
放上画板：POST /api/canvas/projects {sessionId} 取得会话的画布项目，PUT /api/canvas/projects/<项目>/nodes 写入一个图片图层）：
  S1「直接开始干活吧。」 S2「把这个角色做成三视图。」 S3「把它转成 3D 模型。」 S4「帮我优化一下这张图，让线条更干净。」
  S5 对照：S2 的同一句以画板轮发送（inputSource canvas 加画板快照作为选区）。
期望（#709）：S1 先调用 view_canvas，再出选择卡或在回复里提到画板；S2–S4 调用 view_canvas 并发起对应的
生成工具（严格方式下停在批准卡，脚本一律拒绝）；S5 作为显式画板轮是对照，判定不变。每个场景打印工具调用、批准卡数、
任务数与回复摘要（INFO）；真实模型的回答会波动，结论以多次运行为准。
同类的固定两轮场景见 scripts/smoke/smoke-live-chat-three-view-route.py。

用法（Agent 专用账号经 AGENT_TEST_* 注入；脚本把它交给 scripts/smoke/l5_common 登录）：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/board-agent.py --live
输出：PASS/FAIL/INFO 行与 `=== board-agent: PASS=n FAIL=n SKIP=n ===`，有 FAIL 时退出码 1。
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(HERE, "..", "smoke"))
import l5_common as common  # noqa: E402
from lib import use_agent_account_for_l5  # noqa: E402

# (label, prompt, canvas turn, generation tools that count as "used the board picture")
CASES = (
    ("S1 vague: start working", "直接开始干活吧。", False, ()),
    ("S2 three views", "把这个角色做成三视图。", False, ("create_three_view", "create_multiview_images")),
    ("S3 to 3D", "把它转成 3D 模型。", False, ("create_model_3d_from_image", "create_model_3d_from_views")),
    ("S4 improve the picture", "帮我优化一下这张图，让线条更干净。", False, ("refine_image",)),
    ("S5 control: same ask as a canvas turn", "把这个角色做成三视图。", True, ()),
)
BOARD = re.compile(r"画板|画布|@画板|@画布|以画板为主|board", re.IGNORECASE)
LOOK = "view_canvas"
CHOICE = "ask_choice"


def figure_png(size=512):
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            head = (x - size // 2) ** 2 + (y - size // 5) ** 2 < (size // 9) ** 2
            body = abs(x - size // 2) < size // 8 and size // 4 < y < size * 3 // 4
            legs = ((abs(x - size // 2 + size // 12) < size // 22 or abs(x - size // 2 - size // 12) < size // 22)
                    and size * 3 // 4 <= y < size * 9 // 10)
            row += bytes((40, 90, 200)) if (head or body or legs) else bytes((255, 255, 255))  # RGB: encode_png wants 3 bytes
        rows.append(bytes(row))
    return common.encode_png(size, size, rows)


def tool_names(messages):
    return [call.get("name") for message in messages if message.get("role") == "assistant"
            for call in common.parse_tool_calls(message)]


def place_on_board(live, session, asset, size=512):
    """Put one picture layer on the session's board through the browser's canvas save routes (#709)."""
    status, project = live.client.call("POST", "/api/canvas/projects", {"sessionId": session})
    if status not in (200, 201) or not isinstance(project, dict) or not project.get("id"):
        return status
    node = {"id": "node-board-agent", "type": "image", "x": 0, "y": 0, "width": size, "height": size, "zIndex": 1,
            "data": json.dumps({"imageUrl": f"/api/assets/{asset}"}), "visible": True, "locked": False}
    body = {"nodes": [node]}
    if project.get("revision"):
        body["revision"] = project["revision"]
    status, _ = live.client.call("PUT", f"/api/canvas/projects/{project['id']}/nodes", body)
    return status


def run_case(live, report, label, prompt, as_canvas_turn, generators):
    session = live.new_session(title=f"board-agent {label[:2]}", approval="manual")
    picture = figure_png()
    asset = live.client.upload_image(picture)
    status = place_on_board(live, session, asset) if asset else None
    if not report.check(f"[{label}] image placed on the canvas only", bool(asset) and status == 200, f"HTTP {status}"):
        return
    model = common.pick_model_id(live.offerings("text-to-image"))
    before = len(live.messages(session))
    extra = {}
    if as_canvas_turn:
        snapshot = live.client.upload_image(picture)
        extra = {"inputSource": "canvas", "selection": [{"kind": "image", "assetId": snapshot, "canvasLayers": 1}]}
    status, _ = live.send(session, prompt, "image", model, **extra)
    if status not in (200, 201, 202):
        return report.failed(f"[{label}] message accepted", f"HTTP {status}")
    reply = live.wait_reply(session, timeout=240)
    messages = live.messages(session)[before:]
    names = tool_names(messages)
    cards = live.pending_approvals(session)
    text = ((reply or {}).get("content") or "") if reply else ""
    if not text:
        texts = [m.get("content") or "" for m in messages if m.get("role") == "assistant" and m.get("content")]
        text = texts[-1] if texts else ""
    print(f"INFO: [{label}] tool calls: {names}; approval cards: {len(cards)}; tasks: {len(live.tasks(session))}", flush=True)
    print(f"INFO: [{label}] reply: {common.one_line(text, 260)}", flush=True)
    for card in cards:  # nothing is generated, not even by mistake
        status, _ = live.decide_grant(card.get("request_id"), approve=False)
        print(f"INFO: [{label}] approval card denied: HTTP {status}", flush=True)
    if as_canvas_turn:
        report.check(f"[{label}] control: the canvas turn uses the board", True, f"tools={names}")
    elif not generators:
        report.check(f"[{label}] looked at the board, then offered a choice or named the board",
                     LOOK in names and (CHOICE in names or bool(BOARD.search(text))), f"tools={names}")
    else:
        report.check(f"[{label}] looked at the board and started a generation from it",
                     LOOK in names and any(n in generators for n in names), f"tools={names}")


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False
    for label, prompt, canvas, generators in CASES:
        run_case(live, report, label, prompt, canvas, generators)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("board-agent", body, __doc__, gate="chat-live"))
