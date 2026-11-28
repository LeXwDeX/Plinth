#!/usr/bin/env python3
"""#688 画板识别：图只放在画板上（没有附到对话）时，Agent 是否注意到画板（真实对话模型，live）。

层：dev-test-live-chat（`release-env.py release dev-test --live-chat` 的 mixed 形态：对话模型真实、生成全走 fake）。
花费：5 个新会话各一轮真实对话（只耗对话 token）。会话都用严格方式，Agent 发起的任何生成都停在批准卡上，脚本一律
拒绝，不产生生成费用。每次运行前须在当前对话取得用户同意，并同时给出 --live 与 ART_AI_LIVE_ACK=1，否则退出码 2；
目标是 fake 对话模型时也拒绝（chat-live 闸门）。

场景（每个一个新会话，脚本现场合成一张 512×512 的人形图，经 /api/v1/scenes/<会话>/objects 放上画板）：
  S1「直接开始干活吧。」 S2「把这个角色做成三视图。」 S3「把它转成 3D 模型。」 S4「帮我优化一下这张图，让线条更干净。」
  S5 对照：S2 的同一句以画板轮发送（inputSource canvas 加画板快照作为选区）。
期望（#644/#688）：非画板轮 Agent 看不到画板，但应先查看（find_history_images / list_project_assets），或在回复里
提到画板/画布路径（「以画板为主」或 @画布），而不是只让人「请发图」；S5 作为显式画板轮是对照。每个场景打印工具调用、
批准卡数、任务数与回复摘要（INFO），判定只看「注意到画板」这一条；真实模型的回答会波动，结论以多次运行为准。
同类的固定两轮场景见 scripts/smoke/smoke-live-chat-three-view-route.py。

用法（Agent 专用账号经 AGENT_TEST_* 注入；脚本把它交给 scripts/smoke/l5_common 登录）：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/board-agent.py --live
输出：PASS/FAIL/INFO 行与 `=== board-agent: PASS=n FAIL=n SKIP=n ===`，有 FAIL 时退出码 1。
"""
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(HERE, "..", "smoke"))
import l5_common as common  # noqa: E402
from lib import use_agent_account_for_l5  # noqa: E402

CASES = (
    ("S1 vague: start working", "直接开始干活吧。", False),
    ("S2 three views", "把这个角色做成三视图。", False),
    ("S3 to 3D", "把它转成 3D 模型。", False),
    ("S4 improve the picture", "帮我优化一下这张图，让线条更干净。", False),
    ("S5 control: same ask as a canvas turn", "把这个角色做成三视图。", True),
)
BOARD = re.compile(r"画板|画布|@画板|@画布|以画板为主")
LOOKS = ("find_history_images", "list_project_assets")


def figure_png(size=512):
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            head = (x - size // 2) ** 2 + (y - size // 5) ** 2 < (size // 9) ** 2
            body = abs(x - size // 2) < size // 8 and size // 4 < y < size * 3 // 4
            legs = ((abs(x - size // 2 + size // 12) < size // 22 or abs(x - size // 2 - size // 12) < size // 22)
                    and size * 3 // 4 <= y < size * 9 // 10)
            row += bytes((40, 90, 200, 255)) if (head or body or legs) else bytes((255, 255, 255, 255))
        rows.append(bytes(row))
    return common.encode_png(size, size, rows)


def tool_names(messages):
    return [call.get("name") for message in messages if message.get("role") == "assistant"
            for call in common.parse_tool_calls(message)]


def run_case(live, report, label, prompt, as_canvas_turn):
    session = live.new_session(title=f"board-agent {label[:2]}", approval="manual")
    picture = figure_png()
    asset = live.client.upload_image(picture)
    status, _ = live.client.call("POST", f"/api/v1/scenes/{session}/objects", {
        "rect": {"x": 0, "y": 0, "width": 512, "height": 512}, "anchor": {"x": 0, "y": 0},
        "initial": {"kind": "bitmap", "asset_id": asset, "metadata_schema_revision": "v1"}})
    if not report.check(f"[{label}] image placed on the canvas only", bool(asset) and status == 201, f"HTTP {status}"):
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
    report.check(f"[{label}] the Agent noticed the board (looked, or mentioned / used it)",
                 bool(BOARD.search(text)) or as_canvas_turn or any(n in LOOKS for n in names), f"tools={names}")


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False
    for label, prompt, canvas in CASES:
        run_case(live, report, label, prompt, canvas)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("board-agent", body, __doc__, gate="chat-live"))
