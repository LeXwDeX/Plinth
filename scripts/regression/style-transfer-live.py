#!/usr/bin/env python3
"""#699/#723 画风迁移：风格图没有全身时先出比例选择卡、答完只提交一次 2D 生成；风格图有全身时不出卡、直接提交（真实对话模型，live）。

层：dev-test-live-chat（`release-env.py release dev-test --live-chat` 的 mixed 形态：对话模型真实，生成 Provider 全走
fake，所以这一形态下 2D 出图是 fake 的；卡片、提交次数与 Agent 写的提示词这些判定与真实 2D 相同）。要真实 2D 出图时
加 --real-2d：它要求目标以 `--live-upstream` 发布（live 形态，生成也走真实上游，含 3D 通道），否则退出码 2。
两种形态下 3D 都不会触发：会话用严格方式（manual），Agent 发起的每一步都停在批准卡上，脚本只批准 2D
（text-to-image / image-to-image）的批准卡，其余一律拒绝。
花费：每个场景两到三轮真实对话加一次 2D 生成（dev-test 上的 2D 与对话 token 不限量，见 AGENTS.md；--live-upstream
的发布本身仍须当次同意）。须同时给出 --live 与 ART_AI_LIVE_ACK=1，否则退出码 2；目标是 fake 对话模型时也拒绝（chat-live 闸门）。

素材完全由脚本现场合成（几何图形的 RGB PNG，不用真实人物或受版权素材），经 /api/assets/upload 上传，作为本轮附图
（image_urls 加 imageSlot）发出：图 1 是风格图，图 2 是我们的角色（Q 版、约 2 头身，同 #699 的报告场景）。
两个场景各一个新会话，同一句话「图 1 只当画风参考，图 2 是我们的角色，只当角色设计参考。用图 1 的画风重绘这个角色，
保留衣服、发型、动作和表情。」：
  S1 风格图没有全身（半身像，身体被画框截断）：期望本轮调用 ask_choice 开出一张比例卡（选项的步骤是 refine_image），
     卡上是 Agent 写的整句问题（不是默认的「接下来想怎么做？」），本轮不提交生成；脚本按推荐项（否则第一项）回答
     「选择：<选项>」后，只提交一次 2D 生成，且这次生成完成。
  S2 风格图有全身（约 4 头身站姿）：期望不出比例卡，本轮直接提交一次 2D 生成（refine_image），且这次生成完成。
风格图里有没有全身由对话模型自己看图判断（refine_image 的工具描述写明：没有风格图显示站立全身时先开比例卡），
没有服务端检查项。每个场景打印工具调用、卡片、批准卡、任务与回复摘要，以及 refine_image 的提示词摘要（INFO）；
真实模型的回答会波动，结论以多次运行为准（#723 的验收是每个场景 3 次）。

用法（Agent 专用账号经 AGENT_TEST_* 注入；脚本把它交给 scripts/smoke/l5_common 登录）：
  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/style-transfer-live.py --live
选项：--repeat N（每个场景跑 N 次，缺省取 STYLE_TRANSFER_REPEAT，否则 1）、--only S1,S2（缺省取 STYLE_TRANSFER_ONLY）、
--real-2d（见上）。STYLE_TRANSFER_DUMP=1 时逐条打印本轮存下的消息（调试用）。
输出：PASS/FAIL/INFO 行与 `=== style-transfer-live: PASS=n FAIL=n SKIP=n ===`，有 FAIL 时退出码 1。
"""
import json
import os
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(HERE, "..", "smoke"))
import l5_common as common  # noqa: E402
from lib import use_agent_account_for_l5  # noqa: E402

PROMPT = "图 1 只当画风参考，图 2 是我们的角色，只当角色设计参考。用图 1 的画风重绘这个角色，保留衣服、发型、动作和表情。"
# (label, style picture, expect a proportion card)
CASES = (
    ("S1 style picture without a full body", "bust", True),
    ("S2 style picture with a full body", "full", False),
)
CHOICE = "ask_choice"
RESTYLE = "refine_image"
TWO_D = ("text-to-image", "image-to-image")
# The next_step kind's own question (internal/catalog/default_catalog.json); the card must carry the Agent's instead (#699).
DEFAULT_QUESTION = "接下来想怎么做？"

SIZE = 320
OUTLINE = (25, 20, 20)
STYLE_BACKGROUND = (250, 236, 200)
CHARACTER_BACKGROUND = (255, 255, 255)
SKIN, SKIN_SHADE = (250, 214, 180), (222, 170, 140)
EYE = (30, 25, 40)

# (kind, (x0, y0, x1, y1) as fractions of the picture, fill, hard-edge shade or None), painted in order.
STYLE_SHAPES = {
    # Head and shoulders only: the jacket runs past the bottom edge, so no body proportion can be read.
    "bust": (
        ("rect", (0.18, 0.72, 0.82, 1.05), (200, 40, 40), (150, 25, 30)),
        ("rect", (0.44, 0.62, 0.56, 0.76), SKIN, SKIN_SHADE),
        ("ellipse", (0.27, 0.18, 0.73, 0.70), SKIN, SKIN_SHADE),
        ("ellipse", (0.25, 0.10, 0.75, 0.40), (45, 35, 35), (20, 15, 15)),
        ("ellipse", (0.36, 0.44, 0.44, 0.53), EYE, None),
        ("ellipse", (0.56, 0.44, 0.64, 0.53), EYE, None),
    ),
    # The same look standing, about four heads tall, everything inside the frame.
    "full": (
        ("rect", (0.31, 0.29, 0.38, 0.52), (200, 40, 40), (150, 25, 30)),
        ("rect", (0.62, 0.29, 0.69, 0.52), (200, 40, 40), (150, 25, 30)),
        ("rect", (0.41, 0.55, 0.48, 0.88), (60, 60, 90), (40, 40, 65)),
        ("rect", (0.52, 0.55, 0.59, 0.88), (60, 60, 90), (40, 40, 65)),
        ("ellipse", (0.38, 0.86, 0.49, 0.93), OUTLINE, None),
        ("ellipse", (0.51, 0.86, 0.62, 0.93), OUTLINE, None),
        ("rect", (0.38, 0.27, 0.62, 0.57), (200, 40, 40), (150, 25, 30)),
        ("ellipse", (0.40, 0.06, 0.60, 0.28), SKIN, SKIN_SHADE),
        ("ellipse", (0.39, 0.04, 0.61, 0.17), (45, 35, 35), (20, 15, 15)),
        ("ellipse", (0.445, 0.16, 0.475, 0.20), EYE, None),
        ("ellipse", (0.525, 0.16, 0.555, 0.20), EYE, None),
    ),
}
# Our character: a chibi (about two heads tall) in a blue cap and a green hoodie, waving.
CHARACTER_SHAPES = (
    ("rect", (0.63, 0.34, 0.71, 0.60), (60, 160, 90), (40, 120, 65)),
    ("rect", (0.30, 0.55, 0.38, 0.70), (60, 160, 90), (40, 120, 65)),
    ("rect", (0.41, 0.76, 0.48, 0.92), (70, 70, 80), (50, 50, 60)),
    ("rect", (0.52, 0.76, 0.59, 0.92), (70, 70, 80), (50, 50, 60)),
    ("rect", (0.37, 0.52, 0.63, 0.78), (60, 160, 90), (40, 120, 65)),
    ("ellipse", (0.28, 0.10, 0.72, 0.54), SKIN, SKIN_SHADE),
    ("ellipse", (0.26, 0.06, 0.74, 0.30), (40, 90, 200), (25, 60, 150)),
    ("ellipse", (0.38, 0.32, 0.45, 0.40), EYE, None),
    ("ellipse", (0.55, 0.32, 0.62, 0.40), EYE, None),
)


def paint(shapes, background, size=SIZE):
    """An RGB PNG (encode_png wants 3 bytes per pixel) of outlined, cel-shaded shapes, painted back to front."""
    rows = [bytearray(bytes(background) * size) for _ in range(size)]
    pad = max(2, size // 80)
    for kind, (x0, y0, x1, y1), fill, shade in shapes:
        left, top, right, bottom = x0 * size, y0 * size, x1 * size, y1 * size
        cx, cy, rx, ry = (left + right) / 2, (top + bottom) / 2, (right - left) / 2, (bottom - top) / 2
        for grow, outline in ((pad, True), (0, False)):
            for y in range(max(0, int(top - grow)), min(size, int(bottom + grow) + 1)):
                for x in range(max(0, int(left - grow)), min(size, int(right + grow) + 1)):
                    if kind == "ellipse":
                        inside = ((x - cx) / (rx + grow)) ** 2 + ((y - cy) / (ry + grow)) ** 2 <= 1
                    else:
                        inside = left - grow <= x <= right + grow and top - grow <= y <= bottom + grow
                    if not inside:
                        continue
                    colour = OUTLINE if outline else (shade if shade and x > cx + rx * 0.3 else fill)
                    rows[y][3 * x:3 * x + 3] = bytes(colour)
    return common.encode_png(size, size, rows)


def style_png(kind):
    return paint(STYLE_SHAPES[kind], STYLE_BACKGROUND)


def character_png():
    return paint(CHARACTER_SHAPES, CHARACTER_BACKGROUND)


# ---------------------------------------------------------------- pure verdicts

def is_2d(capability):
    return capability in TWO_D


def ratio_cards(choices):
    """The proportion cards among pending choice cards: options whose step is refine_image (#699)."""
    return [card for card in choices if isinstance(card, dict) and any(
        isinstance(option, dict) and (option.get("step") or {}).get("tool") == RESTYLE
        for option in card.get("options") or [])]


def tally(new_tasks, decided):
    """(2D submissions, other submissions): new tasks plus the approval cards that were denied (an approved card is
    one of the new tasks, so it is not counted twice). `decided` is [(capability, approved)]."""
    two_d = sum(1 for task in new_tasks if is_2d(task.get("capability")))
    other = sum(1 for task in new_tasks if not is_2d(task.get("capability")))
    for capability, approved in decided:
        if not approved:
            two_d, other = (two_d + 1, other) if is_2d(capability) else (two_d, other + 1)
    return two_d, other


def judge_first_turn(expect_card, names, cards, two_d, other):
    """[(item, ok, detail)] for the turn that sends the two pictures."""
    detail = f"tools={names} cards={len(cards)} 2d={two_d} other={other}"
    if expect_card:
        question = ((cards[0].get("question") or "").strip()) if cards else ""
        return [
            ("opened one proportion card (ask_choice, refine_image options)", CHOICE in names and len(cards) == 1, detail),
            ("the card carries the Agent's own question", bool(question) and question != DEFAULT_QUESTION,
             f"question={question!r}"),
            ("no generation submitted in the card's reply", two_d == 0 and other == 0, detail),
        ]
    return [
        ("no proportion card", not cards, detail),
        ("submitted exactly one 2D generation directly", RESTYLE in names and two_d == 1 and other == 0, detail),
    ]


def judge_answer(two_d, other):
    return [("answering the card submitted exactly one 2D generation", two_d == 1 and other == 0,
             f"2d={two_d} other={other}")]


# ---------------------------------------------------------------- live run

def tool_names(messages):
    return [call.get("name") for message in messages if message.get("role") == "assistant"
            for call in common.parse_tool_calls(message)]


def restyle_summaries(messages):
    for message in messages:
        if message.get("role") != "assistant":
            continue
        for call in common.parse_tool_calls(message):
            if call.get("name") == RESTYLE:
                arguments = common.tool_arguments(call)
                yield common.one_line(json.dumps({key: arguments.get(key) for key in (
                    "prompt", "reference_handles", "reference_purposes", "model") if key in arguments},
                    ensure_ascii=False), 600)


def settle_cards(live, session, tag, approve_2d, rounds=4):
    """Wait for the turn, then decide every new approval card: approve 2D only when asked, deny the rest (never 3D).
    An approval may let the turn go on, so look again until no new card appears. Returns [(capability, approved)]."""
    decided, seen = [], set()
    for _ in range(rounds):
        live.wait_reply(session, timeout=300)
        fresh = [card for card in live.pending_approvals(session) if card.get("request_id") not in seen]
        if not fresh:
            break
        for card in fresh:
            seen.add(card.get("request_id"))
            approve = approve_2d and is_2d(card.get("capability"))
            status, _ = live.decide_grant(card.get("request_id"), approve=approve)
            if status not in (200, 201, 202):
                approve = False  # an undecided card produced nothing; count it as not run
            decided.append((card.get("capability"), approve))
            print(f"INFO: [{tag}] approval card {card.get('capability')} {'approved' if approve else 'denied'}: "
                  f"HTTP {status}", flush=True)
        time.sleep(5)
    return decided


def wait_settled(live, session, ids, timeout=900, poll=10):
    deadline = time.time() + timeout
    tasks = []
    while time.time() < deadline:
        tasks = [task for task in live.tasks(session) if task.get("id") in ids]
        if all(task.get("state") in common.TERMINAL_TASK_STATES for task in tasks):
            break
        time.sleep(poll)
    return tasks


def turn(live, report, tag, session, before, approve_2d, known, **send):
    """Send one message, decide its approval cards, and return what the turn did."""
    status, _ = live.send(**send)
    if status not in (200, 201, 202):
        report.failed(f"[{tag}] message accepted", f"HTTP {status}")
        return None
    decided = settle_cards(live, session, tag, approve_2d)
    messages = live.messages(session)[before:]
    new = [task for task in live.tasks(session) if task.get("id") not in known]
    texts = [m.get("content") or "" for m in messages if m.get("role") == "assistant" and m.get("content")]
    names = tool_names(messages)
    print(f"INFO: [{tag}] tool calls: {names}; approval cards: {len(decided)}; new tasks: "
          f"{common.describe_tasks(new)}", flush=True)
    print(f"INFO: [{tag}] reply: {common.one_line(texts[-1] if texts else '', 260)}", flush=True)
    for summary in restyle_summaries(messages):
        print(f"INFO: [{tag}] {RESTYLE}: {summary}", flush=True)
    if os.environ.get("STYLE_TRANSFER_DUMP"):  # debugging aid: what the turn's messages carried, as stored
        for message in messages:
            print("DUMP:", common.one_line(json.dumps(message, ensure_ascii=False), 1800), flush=True)
    return names, new, decided


def check_generation(live, report, tag, session, new):
    ids = {task.get("id") for task in new if is_2d(task.get("capability"))}
    if not ids:
        return
    tasks = wait_settled(live, session, ids)
    report.check(f"[{tag}] the 2D generation SUCCEEDED ({'real' if live.shape == 'live' else 'fake'} 2D)",
                 bool(tasks) and all(task.get("state") == common.SUCCESS_STATE for task in tasks),
                 str(common.describe_tasks(tasks)))


def run_case(live, report, label, style, expect_card, attempt=1):
    tag = f"{label} #{attempt}" if attempt > 1 else label
    session = live.new_session(title=f"style-transfer {label[:2]}", approval="manual")
    style_asset = live.client.upload_image(style_png(style), filename="style.png")
    character_asset = live.client.upload_image(character_png(), filename="character.png")
    if not style_asset or not character_asset:
        return report.failed(f"[{tag}] pictures uploaded", f"style={style_asset} character={character_asset}")
    model = (common.pick_model_id(live.offerings("image-to-image"))
             or common.pick_model_id(live.offerings("text-to-image")))
    if not model:
        return report.failed(f"[{tag}] a 2D model is available", "no image-to-image or text-to-image offering")
    attached = {
        "image_urls": [f"/api/assets/{style_asset}", f"/api/assets/{character_asset}"],
        "attachments": [{"kind": "image", "imageSlot": 1, "assetId": style_asset},
                        {"kind": "image", "imageSlot": 2, "assetId": character_asset}],
    }
    known = {task.get("id") for task in live.tasks(session)}
    # S1 denies every card of the first turn: the Agent must not draw before the person answers.
    outcome = turn(live, report, tag, session, len(live.messages(session)), not expect_card, known,
                   session_id=session, content=PROMPT, domain="image", model=model, **attached)
    if outcome is None:
        return
    names, new, decided = outcome
    cards = ratio_cards(live.pending_choices(session))
    for card in cards:
        print(f"INFO: [{tag}] card {card.get('kind')}: {common.one_line(card.get('question') or '', 200)} options="
              f"{[option.get('label') for option in card.get('options') or []]}", flush=True)
    for item, ok, detail in judge_first_turn(expect_card, names, cards, *tally(new, decided)):
        report.check(f"[{tag}] {item}", ok, detail)
    if not expect_card:
        return check_generation(live, report, tag, session, new)
    if not cards:
        return
    answer, picked = common.choice_option(cards[0])
    if answer is None:
        return report.failed(f"[{tag}] the card has an option to answer", "no option")
    label_text = next((option.get("label") for option in cards[0].get("options") or []
                       if option.get("id") == picked), picked)
    print(f"INFO: [{tag}] answering with {picked}: {label_text}", flush=True)
    known |= {task.get("id") for task in new}
    # As the browser does, the answer turn carries no pictures of its own (chat-column: no input-box images on an answer).
    outcome = turn(live, report, f"{tag} answer", session, len(live.messages(session)), True, known,
                   session_id=session, content=f"选择：{label_text}", domain="image", model=model, choiceAnswer=answer)
    if outcome is None:
        return
    _, new, decided = outcome
    for item, ok, detail in judge_answer(*tally(new, decided)):
        report.check(f"[{tag}] {item}", ok, detail)
    check_generation(live, report, tag, session, new)


def add_arguments(parser):
    parser.add_argument("--repeat", type=int, default=os.environ.get("STYLE_TRANSFER_REPEAT", "1"),
                        help="run each scenario N times (default STYLE_TRANSFER_REPEAT, else 1)")
    parser.add_argument("--only", default=os.environ.get("STYLE_TRANSFER_ONLY", ""),
                        help="comma-separated scenario IDs, such as S1 (default STYLE_TRANSFER_ONLY, else all)")
    parser.add_argument("--real-2d", action="store_true",
                        help="require real 2D generation: the target must be released with --live-upstream")


def precheck(arguments, shape, environ):
    if arguments.repeat < 1:
        return "--repeat must be at least 1"
    known = {label[:2] for label, _, _ in CASES}
    unknown = sorted(set(filter(None, arguments.only.split(","))) - known)
    if unknown:
        return f"unknown scenario(s) {', '.join(unknown)}; known: {', '.join(sorted(known))}"
    if arguments.real_2d and shape != "live":
        return (f"refusing to run: --real-2d needs real 2D generation, but the target's generation side is fake "
                f"(shape {shape}); release dev-test with --live-upstream and ART_AI_LIVE_ACK=1 first (user consent required)")
    return None


def body(live, report):
    live.answer_pending_choices = lambda *a, **k: False  # this script answers the proportion card itself
    print(f"INFO: target shape {live.shape}: chat is real, 2D generation is "
          f"{'real' if live.shape == 'live' else 'fake'}; every non-2D approval card is denied", flush=True)
    only = set(filter(None, live.arguments.only.split(",")))
    for label, style, expect_card in CASES:
        if only and label[:2] not in only:
            continue
        for attempt in range(1, live.arguments.repeat + 1):
            run_case(live, report, label, style, expect_card, attempt)


if __name__ == "__main__":
    use_agent_account_for_l5()
    sys.exit(common.execute("style-transfer-live", body, __doc__, add_arguments=add_arguments, gate="chat-live",
                            precheck=precheck))
