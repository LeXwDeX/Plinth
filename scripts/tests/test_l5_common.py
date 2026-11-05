#!/usr/bin/env python3
"""Offline tests for the pure logic in scripts/smoke/l5_common.py. No network."""
import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
SMOKE = pathlib.Path(__file__).resolve().parents[1] / "smoke"


def load():
    spec = importlib.util.spec_from_file_location("l5_common", SMOKE / "l5_common.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


c = load()


def task(capability, state, **extra):
    return {"capability": capability, "state": state, **extra}


class AckAndArguments(unittest.TestCase):
    def test_ack_required(self):
        self.assertIsNotNone(c.ack_error({}))
        self.assertIsNotNone(c.ack_error({"ART_AI_LIVE_ACK": "0"}))
        self.assertIsNotNone(c.ack_error({"ART_AI_LIVE_ACK": "true"}))
        self.assertIsNone(c.ack_error({"ART_AI_LIVE_ACK": "1"}))

    def test_missing_environment(self):
        self.assertEqual(c.missing_environment({"BASE": "x"}), ["ADMIN_USERNAME", "ADMIN_PASSWORD"])
        self.assertEqual(c.missing_environment({"BASE": "x", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}), [])

    def test_validate_base(self):
        self.assertIsNone(c.validate_base("https://example.test"))
        self.assertIsNone(c.validate_base("http://127.0.0.1:13333/"))
        for bad in ("", "example.test", "ftp://x", "https://u:p@x", "https://x/path", "https://x?q=1"):
            self.assertIsNotNone(c.validate_base(bad), bad)

    def test_parse_arguments(self):
        arguments = c.parse_arguments(["--report", "/tmp/r"])
        self.assertEqual(arguments.report, "/tmp/r")
        self.assertEqual(arguments.turn_timeout, 1800)
        with self.assertRaises(SystemExit):
            c.parse_arguments(["--turn-timeout", "5"])
        with self.assertRaises(SystemExit):
            c.parse_arguments(["--unknown"])

    def test_scripts_refuse_without_ack(self):
        # A bare environment, but still no bytecode: the gates must leave the checkout untouched.
        env = {"PATH": "/usr/bin:/bin", "BASE": "https://example.test", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p",
               "PYTHONDONTWRITEBYTECODE": "1"}
        for name in ("chat-models", "2d-to-3d", "parallel", "three-view", "split", "2d-params", "canvas", "3d-params",
                     "2d-split", "agent-hard"):
            result = subprocess.run([sys.executable, str(SMOKE / f"smoke-l5-{name}.py")], env=env,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 2, name)
            self.assertIn("ART_AI_LIVE_ACK=1", result.stderr)
        result = subprocess.run(["bash", str(SMOKE / "smoke-l5-all.sh")], env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("ART_AI_LIVE_ACK=1", result.stderr)


class ToolCalls(unittest.TestCase):
    def test_json_string(self):
        message = {"toolCalls": json.dumps([{"name": "refine_image"}, {"name": "create_model_3d_from_views"}])}
        self.assertEqual([call["name"] for call in c.parse_tool_calls(message)],
                         ["refine_image", "create_model_3d_from_views"])

    def test_list_empty_and_garbage(self):
        self.assertEqual(c.parse_tool_calls({"toolCalls": [{"name": "a"}, "x"]}), [{"name": "a"}])
        for value in (None, "", "   ", "not json", "{}", 5):
            self.assertEqual(c.parse_tool_calls({"toolCalls": value}), [])
        self.assertEqual(c.parse_tool_calls(None), [])

    def test_names_and_errors(self):
        messages = [{"toolCalls": json.dumps([{"name": "a"}, {"name": "b", "error": "boom"}])},
                    {"toolCalls": [{"name": "c", "result": {"ok": 1}}, {"name": "d", "result": {"ok": 1}}]}]
        self.assertEqual(c.tool_names(messages), ["a", "b", "c", "d"])
        self.assertEqual(c.tool_errors(messages), ["b"])


class ChoiceClient:
    """Records calls; answers the choices list with the given cards and the stream as done."""

    def __init__(self, cards):
        self.cards = cards
        self.calls = []

    def call(self, method, path, body=None, **_):
        self.calls.append((method, path, body))
        if path.endswith("/choices"):
            return 200, {"choices": self.cards}
        return 202, {}

    def read_chat_stream(self, session_id, timeout):
        return "done"


class ChoiceCards(unittest.TestCase):
    def live(self, cards):
        client = ChoiceClient(cards)
        live = c.Live(client, report=None, arguments=None)
        live._last_send["s1"] = ("model-3d", "m1")
        return live, client

    def posted(self, client):
        return [body for method, path, body in client.calls if method == "POST"]

    def test_answers_a_pending_card_once_with_the_default(self):
        card = {"id": "ch1", "tool": "ask_choice", "state": "pending", "allow_default": True,
                "default_option_id": "render", "options": [{"id": "render"}, {"id": "print"}]}
        live, client = self.live([card])
        self.assertTrue(live.answer_pending_choices("s1"))
        self.assertFalse(live.answer_pending_choices("s1"))
        posted = self.posted(client)
        self.assertEqual(len(posted), 1)
        self.assertEqual(posted[0]["choiceAnswer"], {"choice_id": "ch1", "use_default": True})
        self.assertEqual((posted[0]["creative_domain"], posted[0]["ai_model"]), ("model-3d", "m1"))

    def test_picks_the_recommended_option_without_a_default(self):
        card = {"id": "ch2", "state": "pending", "allow_default": False,
                "options": [{"id": "a"}, {"id": "b", "recommended": True}]}
        live, client = self.live([card])
        self.assertTrue(live.answer_pending_choices("s1"))
        self.assertEqual(self.posted(client)[0]["choiceAnswer"], {"choice_id": "ch2", "option_id": "b"})

    def test_ignores_answered_cards_and_sessions_without_a_message(self):
        live, client = self.live([{"id": "ch3", "state": "answered", "options": [{"id": "a"}]}])
        self.assertFalse(live.answer_pending_choices("s1"))
        self.assertFalse(live.answer_pending_choices("other-session"))
        self.assertEqual(self.posted(client), [])


class Waiting(unittest.TestCase):
    def test_reply_final(self):
        done = {"role": "assistant", "content": "好的"}
        self.assertTrue(c.reply_is_final([{"role": "user", "content": "x"}, done]))
        # the projected pending message (ProjectMessage): content blanked, isPending true
        self.assertFalse(c.reply_is_final([{"role": "assistant", "content": "", "isPending": True}]))
        self.assertFalse(c.reply_is_final([{"role": "assistant", "content": "__GENERATING__"}]))
        self.assertFalse(c.reply_is_final([{"role": "assistant", "content": "  "}]))
        self.assertFalse(c.reply_is_final([{"role": "user", "content": "x"}]))
        failed = {"role": "assistant", "content": "未知错误（错误 ID：m-1）"}
        self.assertTrue(c.reply_is_final([failed]))

    # Wire of GET /api/chat/sessions/{id}/stream (writeChatEvent: "id: N\ndata: {json}\n\n", retry and
    # heartbeat frames). Planning text is persisted before tools run, so only the done event ends a turn.
    STREAM = [b"retry: 86400000\n", b"\n", b'id: 1\n', b'data: {"type":"msg_created","msgId":"m1"}\n', b"\n",
              b'id: 2\n', b'data: {"type":"text","content":"\xe5\xa5\xbd\xe7\x9a\x84\xef\xbc\x8c\xe6\x88\x91\xe6\x9d\xa5\xe7\x94\x9f\xe6\x88\x90"}\n', b"\n",
              b": heartbeat\n", b"\n"]

    def test_stream_running_until_done_event(self):
        events = c.parse_sse_events(self.STREAM)
        self.assertEqual([e["type"] for e in events], ["msg_created", "text"])
        self.assertFalse(c.stream_finished(events))
        events = c.parse_sse_events(self.STREAM + [b"id: 3\n", b'data: {"type":"done"}\n', b"\n"])
        self.assertTrue(c.stream_finished(events))

    def test_stream_parser_ignores_garbage(self):
        self.assertEqual(c.parse_sse_events(["data: not json\n", "data: [1]\n", "data:\n", ": x\n"]), [])
        self.assertTrue(c.stream_finished(c.parse_sse_events(["data: {\"type\": \"done\"}"])))
        self.assertFalse(c.stream_finished([{"type": "error"}]))

    def test_reply_clean(self):
        self.assertTrue(c.reply_is_clean({"content": "hi"}))
        self.assertFalse(c.reply_is_clean({"content": "hi", "error": "e"}))

    def test_failure_text_is_not_clean(self):
        # Orchestrator.fail: Schema failure copy + error_reference "（错误 ID：{id}）" (en: " (Error ID: {id})")
        for content in ("未知错误（错误 ID：abc123）", "Unknown error (Error ID: abc123)",
                        "Agent 未配置：部署缺少对话模型设置（LLM_API_KEY 或 LLM_MODEL），暂时不能对话。",
                        "请求被上游模型服务的内容安全审核拦截：本轮的图片或文字可能被判定为不当内容。",
                        "⚠️ 未返回结果，请重试。（错误 ID：m2）"):
            reply = {"role": "assistant", "content": content}
            self.assertTrue(c.reply_failed(reply), content)
            self.assertFalse(c.reply_is_clean(reply), content)
        for content in ("我是 Plinth 的美术助手。", "错误 ID 是用来排查问题的编号。"):
            self.assertFalse(c.reply_failed({"content": content}), content)
            self.assertTrue(c.reply_is_clean({"content": content}), content)
        self.assertFalse(c.reply_failed(None))
        self.assertFalse(c.reply_is_clean({"content": "__GENERATING__"}))
        self.assertFalse(c.reply_is_clean(None))

    def test_not_settled_before_expected_tasks_exist(self):
        settled, reason = c.tasks_settled([], {"text-to-image": 1})
        self.assertFalse(settled)
        # the original misjudgement: one image finished, the 3D task is not created yet
        settled, reason = c.tasks_settled([task("text-to-image", "SUCCEEDED")],
                                          {"text-to-image": 1, "image-to-3d": 1})
        self.assertFalse(settled)
        self.assertIn("image-to-3d", reason)

    def test_settled_needs_all_terminal(self):
        expected = {"text-to-image": 2}
        self.assertFalse(c.tasks_settled([task("text-to-image", "SUCCEEDED"), task("text-to-image", "RUNNING")],
                                         expected)[0])
        self.assertFalse(c.tasks_settled([task("text-to-image", "SUCCEEDED")], expected)[0])
        self.assertTrue(c.tasks_settled([task("text-to-image", "SUCCEEDED"), task("text-to-image", "FAILED")],
                                        expected)[0])
        self.assertTrue(c.tasks_settled([task("text-to-image", "SUCCEEDED")], {})[0])
        self.assertFalse(c.tasks_settled([task("a", "PENDING_APPROVAL")], {})[0])

    def test_counts(self):
        tasks = [task("a", "SUCCEEDED"), task("a", "FAILED"), task("b", "SUCCEEDED")]
        self.assertEqual(c.succeeded_count(tasks, "a"), 1)
        self.assertEqual(c.tasks_by_capability(tasks), {"a": 2, "b": 1})

    def test_title(self):
        self.assertFalse(c.title_generated(""))
        self.assertFalse(c.title_generated("新对话"))
        self.assertTrue(c.title_generated("仙侠山林"))

    def test_views_settled(self):
        group = {"slots": [{"key": "front", "status": "succeeded"}, {"key": "left", "status": "running"},
                           {"key": "back", "status": "not_started"}]}
        self.assertFalse(c.views_settled(group))
        group["slots"][1]["status"] = "succeeded"
        self.assertFalse(c.views_settled(group))
        group["slots"][2]["status"] = "failed"
        self.assertTrue(c.views_settled(group))
        self.assertFalse(c.views_settled(None))


class FakeClient:
    def __init__(self, status, body):
        self.status, self.body = status, body

    def call(self, method, path, body=None, **kwargs):
        return self.status, self.body


class LiveStatus(unittest.TestCase):
    def live(self, status, body):
        return c.Live(FakeClient(status, body), None, None)

    def test_ok_statuses_return_data(self):
        self.assertEqual(self.live(200, {"tasks": [{"id": "t"}]}).tasks("s"), [{"id": "t"}])
        self.assertEqual(self.live(200, {"messages": [{"id": "m"}]}).messages("s"), [{"id": "m"}])

    def test_error_statuses_raise(self):
        for status in (401, 404, 500):
            live = self.live(status, {"error": "x"})
            for call in (lambda: live.tasks("s"), lambda: live.messages("s"), lambda: live.offerings("text-to-image"),
                         lambda: live.pending_approvals("s")):
                with self.assertRaises(RuntimeError):
                    call()


class References(unittest.TestCase):
    def test_image_handles_in_order(self):
        messages = [{"attachments": [{"kind": "image", "handle": "@img1"}, {"kind": "model", "handle": "@model1"}]},
                    {"attachments": [{"kind": "image", "handle": "@img2"}, {"kind": "image", "handle": "@img1"}]}]
        self.assertEqual(c.image_handles(messages), ["@img1", "@img2"])

    def test_reference_targets(self):
        call = {"arguments": json.dumps({"reference_handles": ["@img2"], "reference_image_indices": [2]})}
        self.assertEqual(c.reference_targets(call), ["@img2", "#2"])
        self.assertEqual(c.reference_targets({}), [])

    def test_multiview_task(self):
        self.assertTrue(c.is_multiview_task({"offering_id": "tripo/v3.1-multiview", "model_id": "m"}, "m"))
        self.assertFalse(c.is_multiview_task({"offering_id": "tripo/v3.1", "model_id": "m"}, "m"))
        self.assertFalse(c.is_multiview_task({"offering_id": "tripo/v3.1-multiview", "model_id": "x"}, "m"))


class Artifacts(unittest.TestCase):
    def asset(self, **extra):
        return {"role": "model", "required": True, "url": "/api/v1/tasks/t/assets/a", "size": 10, **extra}

    def test_ready(self):
        ok, _ = c.required_assets_ready({"state": "SUCCEEDED", "assets": [self.asset(), {"required": False}]})
        self.assertTrue(ok)

    def test_optional_resources_pending_does_not_fail(self):
        # e.g. Tripo v3.1 thumbnails: delivery background, minimum 0
        ok, reason = c.required_assets_ready({"state": "SUCCEEDED", "resources_pending": True,
                                              "assets": [self.asset(), {"role": "thumbnail", "required": False,
                                                                       "status": "PENDING"}]})
        self.assertTrue(ok)
        self.assertIn("optional", reason)

    def test_not_ready(self):
        cases = [
            {"state": "FAILED", "assets": [self.asset()]},
            {"state": "SUCCEEDED", "assets": []},
            {"state": "SUCCEEDED", "assets": [{"required": False}]},
            {"state": "SUCCEEDED", "assets": [self.asset(size=0)]},
            {"state": "SUCCEEDED", "assets": [self.asset(url="")]},
            {"state": "SUCCEEDED", "assets": [self.asset(status="PENDING")]},
        ]
        for payload in cases:
            self.assertFalse(c.required_assets_ready(payload)[0], payload)
        self.assertTrue(c.required_assets_ready({"state": "SUCCEEDED", "assets": [self.asset(status="ready")]})[0])


class Models(unittest.TestCase):
    OFFERINGS = [
        {"id": "openai/gpt-image-2/text-to-image", "model_id": "gpt-image-2.5-sunburst", "available": True},
        {"id": "meshy/nano-banana-pro/text-to-image", "model_id": "nano-banana-pro", "available": False},
        {"id": "gemini/x/text-to-image", "model_id": "gemini-3", "available": True},
    ]

    def test_model_id_not_offering_id(self):
        self.assertEqual(c.pick_model_id(self.OFFERINGS), "gpt-image-2.5-sunburst")
        self.assertEqual(c.pick_model_id(self.OFFERINGS, ("gemini",)), "gemini-3")
        self.assertEqual(c.pick_model_id(self.OFFERINGS, ("nano-banana-pro",)), "gpt-image-2.5-sunburst")
        self.assertIsNone(c.pick_model_id([]))
        self.assertIsNone(c.pick_model_id([{"id": "a", "available": True}]))

    def test_offering_model_ids(self):
        self.assertEqual(c.offering_model_ids(self.OFFERINGS),
                         {"openai/gpt-image-2/text-to-image": "gpt-image-2.5-sunburst",
                          "gemini/x/text-to-image": "gemini-3"})

    def test_multiview_request(self):
        slots = [
            {"key": "front", "status": "succeeded", "task_id": "t1", "output_asset_id": "a1"},
            {"key": "left", "status": "succeeded", "task_id": "t2", "output_asset_id": "a2"},
            {"key": "back", "status": "failed", "task_id": "t3", "output_asset_id": None},
            {"key": "top", "status": "succeeded", "task_id": "t4", "output_asset_id": "a4"},
        ]
        request = c.multiview_request("run1", "tripo-v3.1-multiview", slots)
        self.assertEqual(request["group"], "run1")
        self.assertEqual(request["model"], "tripo-v3.1-multiview")
        self.assertEqual(request["views"], [{"view": "front", "task_id": "t1", "asset_id": "a1"},
                                            {"view": "left", "task_id": "t2", "asset_id": "a2"}])


class Images(unittest.TestCase):
    def test_png(self):
        header = b"\x89PNG\r\n\x1a\n" + b"\x00\x00\x00\rIHDR" + (896).to_bytes(4, "big") + (1200).to_bytes(4, "big")
        self.assertEqual(c.image_dimensions(header), (896, 1200))

    def test_jpeg_skips_other_segments(self):
        data = (b"\xff\xd8\xff\xe0\x00\x04ab" + b"\xff\xc0\x00\x0b\x08" + (300).to_bytes(2, "big")
                + (200).to_bytes(2, "big") + b"\x03\x00\x00\x00")
        self.assertEqual(c.image_dimensions(data), (200, 300))

    def test_webp_lossless_and_garbage(self):
        bits = (99) | (49 << 14)
        data = b"RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f" + bits.to_bytes(4, "little") + b"\x00" * 8
        self.assertEqual(c.image_dimensions(data), (100, 50))
        self.assertIsNone(c.image_dimensions(b"nope"))

    def test_ratios_and_shapes(self):
        self.assertAlmostEqual(c.option_ratio("1536x1024"), 1.5)
        self.assertAlmostEqual(c.option_ratio("16:9"), 16 / 9)
        self.assertIsNone(c.option_ratio("auto"))
        self.assertTrue(c.ratio_matches((1500, 1000), 1.5))
        self.assertFalse(c.ratio_matches((1000, 1000), 1.5))
        self.assertFalse(c.ratio_matches(None, 1.5))
        shapes = c.pick_shapes(["1024x1024", "3840x2160", "1536x1024", "1024x1536", "1536x1152"], "1024x1024")
        self.assertEqual((shapes["square"], shapes["landscape"], shapes["portrait"]),
                         ("1024x1024", "1536x1024", "1024x1536"))
        self.assertIn("other", shapes)

    def test_size_options(self):
        offering = {"parameters": [{"name": "prompt", "semantic": "prompt"},
                                   {"name": "size", "semantic": "output-size", "options": [{"value": "1:1"}]}]}
        self.assertEqual(c.size_options(offering), ("size", ["1:1"]))
        self.assertIsNone(c.size_options({"parameters": []}))

    def test_tool_arguments(self):
        self.assertEqual(c.tool_arguments({"arguments": '{"prompt": "x"}'}), {"prompt": "x"})
        self.assertEqual(c.tool_arguments({"arguments": "bad"}), {})
        self.assertEqual(len(c.calls_named([{"toolCalls": [{"name": "a"}, {"name": "b"}]}], "b")), 1)


def png(width, height, colour, depth, rows, filters, palette=b""):
    """A PNG whose rows (raw samples, one bytes per row) are stored with the given per-row filter types."""
    import struct
    import zlib

    def chunk(kind, payload):
        body = kind + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    step = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[colour] * depth // 8
    raw, prior = b"", bytes(len(rows[0]))
    for row, kind in zip(rows, filters):
        out = bytearray()
        for i, value in enumerate(row):
            a, b, c = (row[i - step], prior[i], prior[i - step]) if i >= step else (0, prior[i], 0)
            p = a + b - c
            paeth = a if abs(p - a) <= abs(p - b) and abs(p - a) <= abs(p - c) else b if abs(p - b) <= abs(p - c) else c
            out.append((value - (0, a, b, (a + b) >> 1, paeth)[kind]) & 0xFF)
        raw += bytes([kind]) + bytes(out)
        prior = row
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, depth, colour, 0, 0, 0))
            + (chunk(b"PLTE", palette) if palette else b"") + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


class Snapshot(unittest.TestCase):
    def test_round_trip_through_every_filter(self):
        rows = [bytes((x * 37 + y * 11 + k * 5) % 256 for x in range(4) for k in range(4)) for y in range(5)]
        data = png(4, 5, 6, 8, rows, [0, 1, 2, 3, 4])
        width, height, decoded = c.decode_png(data)
        self.assertEqual((width, height), (4, 5))
        for row, rgb in zip(rows, decoded):
            self.assertEqual(rgb, bytes(v for i, v in enumerate(row) if i % 4 != 3))
        self.assertEqual(c.decode_png(c.encode_png(4, 5, decoded))[2], decoded)

    def test_grey_palette_and_sixteen_bit(self):
        self.assertEqual(c.decode_png(png(2, 1, 4, 8, [b"\x10\xff\x20\x00"], [1]))[2], [b"\x10" * 3 + b"\x20" * 3])
        palette = b"\x01\x02\x03\x04\x05\x06"
        self.assertEqual(c.decode_png(png(2, 1, 3, 8, [b"\x01\x00"], [0], palette))[2], [b"\x04\x05\x06\x01\x02\x03"])
        self.assertEqual(c.decode_png(png(1, 1, 2, 16, [b"\xaa\x01\xbb\x02\xcc\x03"], [0]))[2], [b"\xaa\xbb\xcc"])

    def test_unsupported_pictures_raise(self):
        with self.assertRaises(ValueError):
            c.decode_png(b"\xff\xd8\xff\xe0")
        with self.assertRaises(ValueError):
            c.decode_png(png(8, 1, 0, 1, [b"\x00"], [0]))

    def test_side_by_side_scales_to_one_height(self):
        red = c.encode_png(2, 2, [b"\xff\x00\x00" * 2] * 2)
        blue = c.encode_png(2, 1, [b"\x00\x00\xff" * 2])
        width, height, rows = c.decode_png(c.side_by_side_png([red, blue], height=4))
        self.assertEqual((width, height), (4 + 8, 4))
        self.assertEqual(rows, [b"\xff\x00\x00" * 4 + b"\x00\x00\xff" * 8] * 4)
        self.assertEqual(c.image_dimensions(c.side_by_side_png([red, blue], height=4)), (12, 4))

    def test_canvas_handles_and_user_handles(self):
        messages = [
            {"role": "user", "attachments": [{"kind": "image", "role": "canvas", "handle": "@img3"}]},
            {"role": "assistant", "attachments": [{"kind": "image", "handle": "@img4"}]},
            {"role": "user", "attachments": []},
            {"role": "user", "attachments": [{"kind": "image", "handle": "@img3"}, {"kind": "image", "handle": "@img5"}]},
        ]
        self.assertEqual(c.canvas_attachment_ids(messages), ["@img3"])
        self.assertEqual(c.user_handles(messages[:3]), ["@img3"])
        self.assertEqual(c.user_handles(messages), ["@img3", "@img5"])


class Redaction(unittest.TestCase):
    def test_url_query_dropped(self):
        text = "see https://host/a/b?X-Signature=abc&Expires=1 and /api/v1/tasks/t/assets/a?token=zzz done"
        redacted = c.redact_url(text)
        self.assertNotIn("abc", redacted)
        self.assertNotIn("zzz", redacted)
        self.assertIn("https://host/a/b?<redacted>", redacted)

    def test_one_line_limit(self):
        self.assertEqual(c.one_line("a\n b\tc", 80), "a b c")
        self.assertEqual(len(c.one_line("x" * 500, 50)), 50)
        self.assertEqual(c.safe_name("a/b c"), "a-b-c")


class ReportCounts(unittest.TestCase):
    def test_counts_and_exit_code(self):
        with tempfile.TemporaryDirectory() as directory:
            report = c.Report("demo", directory)
            report.passed("a")
            report.skipped("b", "reason")
            self.assertEqual(report.exit_code(), 0)
            report.check("c", False, "why")
            self.assertEqual(report.counts(), {"PASS": 1, "FAIL": 1, "SKIP": 1})
            self.assertEqual(report.exit_code(), 1)
            self.assertEqual(report.summary_line(), "=== demo: PASS=1 FAIL=1 SKIP=1 ===")
            self.assertEqual(report.finish(), 1)
            saved = json.loads((pathlib.Path(directory) / "demo.json").read_text())
            self.assertEqual(saved["counts"]["FAIL"], 1)


if __name__ == "__main__":
    unittest.main()
