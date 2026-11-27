#!/usr/bin/env python3
"""#688 retest: the character is only on the canvas, the person asks for three views.

Runs only against dev-test released in the mixed shape (release-env.py release dev-test --live-chat,
#416): the chat model is real, every generation Provider is the fake one. The only cost is chat tokens
(two turns); nothing is generated for money, and the approval card of turn 2 is denied, not approved.

Scenario (fresh data, one new session in strict approval mode so no step runs without a person):
  1. a character picture is uploaded and placed on the session's canvas only (scene object), never
     attached to the chat; the picture is --image PATH or a small synthesized figure;
  2. turn 1, not a canvas turn, with the hand-picked three-view Skill: 「把这个角色的三视图图片都生成出来。」
     The Agent cannot see the canvas here (#644). Expected after the #688 Catalog fix: it looks first
     (find_history_images and/or list_project_assets), then says it saw no picture and points to the
     canvas route (「以画板为主」 switch or @画布), instead of asking the person to describe the character;
     it starts no generation;
  3. turn 2, a canvas turn (inputSource canvas plus the flattened board as selection, as the browser
     sends it): 「看画板」. Expected: create_three_view is called and the front view stops at the
     approval card, which the script denies (no generation).
Each turn prints a summary of its tool calls (names and shortened arguments).

  ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- \
    python3 scripts/smoke/smoke-live-chat-three-view-route.py --live [--image PATH] [--report DIR]

Refuses (exit 2) unless: both live switches (--live and ART_AI_LIVE_ACK=1), the target shape is mixed
(fake or live are refused: a fake chat model cannot answer the question, a live target would bill the
generation), RELEASE_ENV is dev-test, and BASE matches dev-test's base_url in deploy/local/environments.json
when that file exists. Each run needs the user's consent in the conversation first (AGENTS.md).
Exit 0 = no FAIL (SKIP is not a pass), 1 = a FAIL. Never prints credentials, cookies or signed URLs.
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

NAME = "smoke-live-chat-three-view-route"
THREE_VIEW_PICK = {"id": "system-three-view", "name": "三视图"}
ASK_VIEWS = "把这个角色的三视图图片都生成出来。"
LOOK_AT_BOARD = "看画板"
LOOKUPS = ("find_history_images", "list_project_assets")
VIEW_TOOLS = ("create_three_view",)
BOARD_ROUTE = re.compile(r"以画板为主|@画布|@画板")
DESCRIBE_FIRST = re.compile(r"(请|先|麻烦)[^。！？\n]{0,8}描述[^。！？\n]{0,6}(角色|人物|形象|外观)")
ENVIRONMENTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "deploy", "local",
                            "environments.json")


def dev_test_only(arguments, shape, environ):
    """The refusal when the target is not dev-test in the mixed shape, else None."""
    if shape != "mixed":
        return (f"refusing to run: the target shape is {shape}; this script needs dev-test released with "
                "--live-chat (real chat model, fake generation)")
    if environ.get("RELEASE_ENV") != "dev-test":
        return "refusing to run: RELEASE_ENV is not dev-test (run through scripts/release-env.py smoke dev-test)"
    if os.path.exists(ENVIRONMENTS):
        try:
            with open(ENVIRONMENTS, encoding="utf-8") as handle:
                expected = json.load(handle)["environments"]["dev-test"]["base_url"]
        except (OSError, ValueError, KeyError, TypeError):
            return "refusing to run: deploy/local/environments.json has no readable dev-test base_url"
        if expected.rstrip("/") != environ.get("BASE", "").rstrip("/"):
            return "refusing to run: BASE is not dev-test's base_url"
    if arguments.image and not os.path.isfile(arguments.image):
        return "refusing to run: --image is not a readable file"
    return None


def add_arguments(parser):
    parser.add_argument("--image", help="PNG of the character to place on the canvas (default: a synthesized figure)")


def figure_png(size=512):
    """A plain standing figure (head, body, arms, legs) on a light background, standard library only."""
    background, ink = (236, 236, 230), (60, 90, 150)
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            dx, cy = x - size // 2, y
            head = dx * dx + (cy - 110) ** 2 <= 45 ** 2
            body = abs(dx) <= 55 and 160 <= cy <= 330
            arms = 160 <= cy <= 185 and abs(dx) <= 130
            legs = 330 < cy <= 470 and 15 <= abs(dx) <= 45
            row.extend(ink if head or body or arms or legs else background)
        rows.append(row)
    return common.encode_png(size, size, rows)


def summarize(label, messages):
    calls = [call for message in messages if message.get("role") == "assistant"
             for call in common.parse_tool_calls(message)]
    print(f"INFO: {label} tool calls ({len(calls)}):", flush=True)
    for call in calls:
        arguments = json.dumps(common.tool_arguments(call), ensure_ascii=False)
        print(f"INFO:   {call.get('name')} {common.one_line(arguments, 120)}", flush=True)
    return [call.get("name") for call in calls]


def turn(live, session, content, model, **extra):
    """Send one message and wait for its final reply; returns (reply, the turn's new messages)."""
    before = len(live.messages(session))
    status, _ = live.send(session, content, "image", model, **extra)
    if status not in (200, 201, 202):
        live.report.failed(f"message accepted: {content}", f"HTTP {status}")
        return None, []
    reply = live.wait_reply(session)
    if reply is None:
        live.report.failed(f"final reply: {content}", "timed out waiting for the turn to finish")
    return reply, live.messages(session)[before:]


def body(live, report):
    model = common.pick_model_id(live.offerings("text-to-image"))
    if not model:
        return report.failed("an available 2D model")
    session = live.new_session(title="#688 三视图路由复测", approval="manual")
    picture = open(live.arguments.image, "rb").read() if live.arguments.image else figure_png()
    asset = live.client.upload_image(picture)
    if not report.check("character picture uploaded", bool(asset)):
        return
    status, _ = live.client.call("POST", f"/api/v1/scenes/{session}/objects", {
        "rect": {"x": 0, "y": 0, "width": 512, "height": 512}, "anchor": {"x": 0, "y": 0},
        "initial": {"kind": "bitmap", "asset_id": asset, "metadata_schema_revision": "v1"}})
    if not report.check("character placed on the canvas only (not in the chat)", status == 201, f"HTTP {status}"):
        return

    reply, messages = turn(live, session, ASK_VIEWS, model, _skill=THREE_VIEW_PICK)
    if reply is None:
        return
    names = summarize("turn 1", messages)
    text = (reply or {}).get("content") or ""
    print(f"INFO: turn 1 reply: {common.one_line(text, 200)}", flush=True)
    report.check("turn 1: looks at history/project assets before answering",
                 any(name in LOOKUPS for name in names), f"tools={names}")
    report.check("turn 1: starts no three-view (no picture in the chat)",
                 not any(name in VIEW_TOOLS for name in names) and not live.pending_approvals(session), f"tools={names}")
    report.check("turn 1: points to the canvas route (以画板为主 / @画布), not only 「请发图」",
                 bool(BOARD_ROUTE.search(text)), common.one_line(text, 120))
    report.check("turn 1: does not ask the person to describe the character first",
                 not DESCRIBE_FIRST.search(text), common.one_line(text, 120))

    snapshot = live.client.upload_image(picture)  # the browser uploads the flattened board at send time
    if not report.check("board snapshot uploaded", bool(snapshot)):
        return
    reply, messages = turn(live, session, LOOK_AT_BOARD, model, _skill=THREE_VIEW_PICK, inputSource="canvas",
                           selection=[{"kind": "image", "assetId": snapshot, "canvasLayers": 1}])
    if reply is None:
        return
    names = summarize("turn 2", messages)
    print(f"INFO: turn 2 reply: {common.one_line((reply or {}).get('content'), 200)}", flush=True)
    report.check("turn 2 (canvas turn): create_three_view called", any(name in VIEW_TOOLS for name in names),
                 f"tools={names}")
    cards = live.pending_approvals(session)
    report.check("turn 2: the front view waits on an approval card (strict mode)", bool(cards), f"{len(cards)} card(s)")
    for card in cards:  # deny: nothing is generated, not even on the fake
        status, _ = live.decide_grant(card.get("request_id"), approve=False)
        report.check("approval card denied", status == 200, f"HTTP {status}")


if __name__ == "__main__":
    sys.exit(common.execute(NAME, body, __doc__, add_arguments=add_arguments, gate="chat-live",
                            precheck=dev_test_only))
