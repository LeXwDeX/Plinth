#!/usr/bin/env python3
"""#688/#709 画板兜底：图只放在画板上（没有附到对话）时，Agent 是否查看画板并用画板的图（真实对话模型，live）。

层：dev-test-live-chat（`release-env.py release dev-test --live-chat` 的 mixed 形态：对话模型真实、生成全走 fake）。
花费：8 个场景各至少一轮真实对话（只耗对话 token；dev-test 上的 2D 与对话 token 不限量，见 AGENTS.md）。--repeat N 每个场景跑 N 次。会话都用严格方式，Agent 发起的任何生成都停在批准卡上，脚本一律
拒绝，不产生生成费用。每次运行前须在当前对话取得用户同意，并同时给出 --live 与 ART_AI_LIVE_ACK=1，否则退出码 2；
目标是 fake 对话模型时也拒绝（chat-live 闸门）。

场景（每个一个新会话，脚本现场合成一张 512×512 的 RGB 人形图，经 /api/assets/upload 上传，再走浏览器用的画布保存接口
放上画板：POST /api/canvas/projects {sessionId} 取得会话的画布项目，PUT /api/canvas/projects/<项目>/nodes 写入一个图片图层）：
  S1「直接开始干活吧。」 S2「把这个角色做成三视图。」 S3「把它转成 3D 模型。」 S4「帮我优化一下这张图，让线条更干净。」
  S5 对照：S2 的同一句以画板轮发送（inputSource canvas 加画板快照作为选区）。
  S6 对话里已有一张图（第一轮作为附件发过），再往画板新放一张，说「把这张转成 3D 模型」：不得静默用历史图，
     必须先看画板（或出选择卡问）。
  S7 画板上有两张图加一处文字标注，说「开始干活吧。」：先看画板，再出选择卡，且选择卡含「以画板为主继续」。
  S8 本轮带附图（画板上另有一张图）：说「把这个角色做成三视图。」：不看画板（本轮有素材，服务端短路也不放行）。
期望（#709）：S1 先调用 view_canvas，再出选择卡或在回复里提到画板；S2–S4 调用 view_canvas 并发起对应的
生成工具（严格方式下停在批准卡，脚本一律拒绝）；S5 作为显式画板轮是对照，判定不变。每个场景打印工具调用、批准卡数、
任务数与回复摘要（INFO）；真实模型的回答会波动，结论以多次运行为准。
同类的固定两轮场景见 scripts/smoke/smoke-live-chat-three-view-route.py。

用法（Agent 专用账号经 AGENT_TEST_* 注入；脚本把它交给 scripts/smoke/l5_common 登录）：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/board-agent.py --live
可选环境变量：BOARD_AGENT_REPEAT=3（每个场景跑 3 次）、BOARD_AGENT_ONLY=S1,S6（只跑这些场景）。
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

# (label, prompt, kind, generation tools that count as "used the board picture")
# kind: plain | canvas | history_then_board | multi_layer | attached
CASES = (
    ("S1 vague: start working", "直接开始干活吧。", "plain", ()),
    ("S2 three views", "把这个角色做成三视图。", "plain", ("create_three_view", "create_multiview_images")),
    ("S3 to 3D", "把它转成 3D 模型。", "plain", ("create_model_3d_from_image", "create_model_3d_from_views")),
    ("S4 improve the picture", "帮我优化一下这张图，让线条更干净。", "plain", ("refine_image",)),
    ("S5 control: same ask as a canvas turn", "把这个角色做成三视图。", "canvas", ()),
    ("S6 history picture, then a new picture on the board", "把这张转成 3D 模型。", "history_then_board",
     ("create_model_3d_from_image", "create_model_3d_from_views")),
    ("S7 several layers and a note", "开始干活吧。", "multi_layer", ()),
    ("S8 the turn carries its own picture", "把这个角色做成三视图。", "attached", ()),
)
BOARD = re.compile(r"画板|画布|@画板|@画布|以画板为主|board", re.IGNORECASE)
LOOK = "view_canvas"
CHOICE = "ask_choice"
CONTINUE = "以画板为主继续"


def figure_png(size=512, color=(40, 90, 200)):
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            head = (x - size // 2) ** 2 + (y - size // 5) ** 2 < (size // 9) ** 2
            body = abs(x - size // 2) < size // 8 and size // 4 < y < size * 3 // 4
            legs = ((abs(x - size // 2 + size // 12) < size // 22 or abs(x - size // 2 - size // 12) < size // 22)
                    and size * 3 // 4 <= y < size * 9 // 10)
            row += bytes(color) if (head or body or legs) else bytes((255, 255, 255))  # RGB: encode_png wants 3 bytes
        rows.append(bytes(row))
    return common.encode_png(size, size, rows)


def tool_names(messages):
    return [call.get("name") for message in messages if message.get("role") == "assistant"
            for call in common.parse_tool_calls(message)]


def image_node(node_id, asset, x=0, y=0, size=512, z=1):
    return {"id": node_id, "type": "image", "x": x, "y": y, "width": size, "height": size, "zIndex": z,
            "data": json.dumps({"imageUrl": f"/api/assets/{asset}"}), "visible": True, "locked": False}


def text_node(node_id, text, x=0, y=520, z=3):
    return {"id": node_id, "type": "text", "x": x, "y": y, "width": 300, "height": 40, "zIndex": z,
            "data": json.dumps({"text": text}), "visible": True, "locked": False}


def place_on_board(live, session, nodes):
    """Put layers on the session's board through the browser's canvas save routes (#709)."""
    status, project = live.client.call("POST", "/api/canvas/projects", {"sessionId": session})
    if status not in (200, 201) or not isinstance(project, dict) or not project.get("id"):
        return status
    # The frame travels with the nodes, as the browser saves them (#246): the canvas size and the coordinate version.
    body = {"nodes": nodes, "canvasWidth": 2048, "canvasHeight": 2048, "coordinateVersion": 2}
    if project.get("revision"):
        body["revision"] = project["revision"]
    status, _ = live.client.call("PUT", f"/api/canvas/projects/{project['id']}/nodes", body)
    return status


def run_case(live, report, label, prompt, kind, generators, attempt=1):
    tag = f"{label} #{attempt}" if attempt > 1 else label
    session = live.new_session(title=f"board-agent {label[:2]}", approval="manual")
    picture = figure_png()
    asset = live.client.upload_image(picture)
    model = common.pick_model_id(live.offerings("text-to-image"))
    if not asset:
        return report.failed(f"[{tag}] picture uploaded", "no asset id")
    extra = {}
    if kind == "history_then_board":
        # Turn 1: the person attaches a picture and asks for nothing yet; it becomes @img1 of the conversation.
        status, _ = live.send(session, "这是第一张参考图，先记着，先不用做任何事。", "image", model,
                              image_urls=[f"/api/assets/{asset}"],
                              attachments=[{"kind": "image", "imageSlot": 1, "assetId": asset}])
        if status not in (200, 201, 202) or not live.wait_reply(session, timeout=240):
            return report.failed(f"[{tag}] first turn with an attachment", f"HTTP {status}")
        fresh = live.client.upload_image(figure_png(color=(200, 60, 60)))
        nodes = [image_node("node-board-new", fresh)] if fresh else []
    elif kind == "multi_layer":
        second = live.client.upload_image(figure_png(color=(60, 160, 80)))
        nodes = [image_node("node-board-a", asset), image_node("node-board-b", second or asset, x=560, z=2),
                 text_node("node-board-note", "线条再干净一点，配色保持不变")]
    else:  # plain, canvas, attached: the one picture only on the board
        nodes = [image_node("node-board-agent", asset)]
    status = place_on_board(live, session, nodes)
    if not report.check(f"[{tag}] layers placed on the canvas only", status == 200, f"HTTP {status}"):
        return
    before = len(live.messages(session))
    if kind == "canvas":
        snapshot = live.client.upload_image(picture)
        extra = {"inputSource": "canvas", "selection": [{"kind": "image", "assetId": snapshot, "canvasLayers": 1}]}
    elif kind == "attached":
        own = live.client.upload_image(figure_png(color=(180, 120, 30)))
        extra = {"image_urls": [f"/api/assets/{own}"], "attachments": [{"kind": "image", "imageSlot": 1, "assetId": own}]}
    status, _ = live.send(session, prompt, "image", model, **extra)
    if status not in (200, 201, 202):
        return report.failed(f"[{tag}] message accepted", f"HTTP {status}")
    reply = live.wait_reply(session, timeout=240)
    messages = live.messages(session)[before:]
    names = tool_names(messages)
    cards = live.pending_approvals(session)
    text = ((reply or {}).get("content") or "") if reply else ""
    if not text:
        texts = [m.get("content") or "" for m in messages if m.get("role") == "assistant" and m.get("content")]
        text = texts[-1] if texts else ""
    if os.environ.get("BOARD_AGENT_DUMP"):  # debugging aid: what the turn's messages carried, as stored
        for message in live.messages(session)[before:]:
            print("DUMP:", common.one_line(json.dumps(message, ensure_ascii=False), 1800), flush=True)
    print(f"INFO: [{tag}] tool calls: {names}; approval cards: {len(cards)}; tasks: {len(live.tasks(session))}", flush=True)
    print(f"INFO: [{tag}] reply: {common.one_line(text, 260)}", flush=True)
    for card in cards:  # nothing is generated, not even by mistake
        status, _ = live.decide_grant(card.get("request_id"), approve=False)
        print(f"INFO: [{tag}] approval card denied: HTTP {status}", flush=True)
    started = any(n in generators for n in names)
    if kind == "canvas":
        report.check(f"[{tag}] control: the canvas turn uses the board", True, f"tools={names}")
    elif kind == "attached":
        report.check(f"[{tag}] the turn has its own picture: the board is not read", LOOK not in names, f"tools={names}")
    elif kind == "multi_layer":
        asked = CHOICE in names and CONTINUE in json.dumps(messages, ensure_ascii=False)
        report.check(f"[{tag}] looked at the board and offered a choice with '{CONTINUE}'", LOOK in names and asked,
                     f"tools={names}")
    elif kind == "history_then_board":
        report.check(f"[{tag}] looked at the board (or asked) instead of silently using the history picture",
                     LOOK in names or CHOICE in names, f"tools={names}")
    elif not generators:
        report.check(f"[{tag}] looked at the board, then offered a choice or named the board",
                     LOOK in names and (CHOICE in names or bool(BOARD.search(text))), f"tools={names}")
    else:
        report.check(f"[{tag}] looked at the board and started a generation from it", LOOK in names and started,
                     f"tools={names}")


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False
    repeat = max(1, int(os.environ.get("BOARD_AGENT_REPEAT", "1")))
    only = set(filter(None, os.environ.get("BOARD_AGENT_ONLY", "").split(",")))
    for label, prompt, kind, generators in CASES:
        if only and label[:2] not in only:
            continue
        for attempt in range(1, repeat + 1):
            run_case(live, report, label, prompt, kind, generators, attempt)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("board-agent", body, __doc__, gate="chat-live"))
