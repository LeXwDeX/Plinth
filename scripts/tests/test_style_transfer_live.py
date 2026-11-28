#!/usr/bin/env python3
"""Offline tests for scripts/regression/style-transfer-live.py (#723, #699).

No network, no server, no real upstream: the gate is driven with a loopback BASE that is never reached (every
refusal happens before login), and the scenario table, the synthetic pictures, the card detection and the verdicts
are checked as plain functions.
"""
import importlib.util
import os
import struct
import subprocess
import sys
import unittest
import zlib
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "regression" / "style-transfer-live.py"


def load():
    spec = importlib.util.spec_from_file_location("style_transfer_live", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def png_rows(data):
    """(width, height, colour type, bit depth, rows) of a non-interlaced, filter-0 PNG as encode_png writes it."""
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("not a PNG")
    offset, idat, header = 8, b"", None
    while offset < len(data):
        length, kind = struct.unpack(">I4s", data[offset:offset + 8])
        payload = data[offset + 8:offset + 8 + length]
        crc = struct.unpack(">I", data[offset + 8 + length:offset + 12 + length])[0]
        if zlib.crc32(kind + payload) & 0xFFFFFFFF != crc:
            raise ValueError(f"bad CRC in {kind!r}")
        if kind == b"IHDR":
            header = struct.unpack(">IIBBBBB", payload)
        elif kind == b"IDAT":
            idat += payload
        offset += 12 + length
    width, height, depth, colour = header[0], header[1], header[2], header[3]
    raw = zlib.decompress(idat)
    stride = 1 + width * 3
    if len(raw) != height * stride:
        raise ValueError("IDAT size does not match an RGB image")
    rows = [raw[y * stride + 1:(y + 1) * stride] for y in range(height)]
    return width, height, colour, depth, rows


class ScenarioTable(unittest.TestCase):
    def setUp(self):
        self.script = load()

    def test_two_scenarios_one_with_and_one_without_a_full_body(self):
        cases = self.script.CASES
        self.assertEqual([label[:2] for label, _, _ in cases], ["S1", "S2"])
        self.assertEqual([(style, expect) for _, style, expect in cases], [("bust", True), ("full", False)])

    def test_the_prompt_splits_the_roles_like_the_699_session(self):
        prompt = self.script.PROMPT
        for words in ("画风参考", "角色设计参考", "保留衣服、发型、动作和表情"):
            self.assertIn(words, prompt)

    def test_only_2d_capabilities_count_as_2d(self):
        self.assertTrue(self.script.is_2d("text-to-image"))
        self.assertTrue(self.script.is_2d("image-to-image"))
        for capability in ("image-to-3d", "text-to-3d", "multiview-to-3d", None, ""):
            self.assertFalse(self.script.is_2d(capability))


class Gate(unittest.TestCase):
    def run_script(self, argv, **environ):
        env = {key: value for key, value in os.environ.items() if not key.startswith(("ART_AI_", "AGENT_TEST_"))}
        env.update({"BASE": "http://127.0.0.1:9", "ADMIN_USERNAME": "nobody", "ADMIN_PASSWORD": "unused",
                    "PYTHONDONTWRITEBYTECODE": "1"})
        env.update(environ)
        return subprocess.run([sys.executable, str(SCRIPT), *argv], env=env, capture_output=True, text=True,
                              timeout=60)

    def test_refuses_without_both_live_switches(self):
        for argv, ack in (([], None), (["--live"], None), ([], "1"), (["--live"], "0")):
            extra = {"ART_AI_UPSTREAM_SHAPE": "mixed"}
            if ack is not None:
                extra["ART_AI_LIVE_ACK"] = ack
            with self.subTest(argv=argv, ack=ack):
                result = self.run_script(argv, **extra)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertIn("ART_AI_LIVE_ACK", result.stderr)

    def test_refuses_a_fake_chat_target(self):
        result = self.run_script(["--live"], ART_AI_UPSTREAM_SHAPE="fake", ART_AI_LIVE_ACK="1")
        self.assertEqual(result.returncode, 2)
        self.assertIn("fake chat model", result.stderr)

    def test_real_2d_needs_the_live_upstream_shape(self):
        result = self.run_script(["--live", "--real-2d"], ART_AI_UPSTREAM_SHAPE="mixed", ART_AI_LIVE_ACK="1")
        self.assertEqual(result.returncode, 2)
        self.assertIn("--live-upstream", result.stderr)

    def test_precheck_accepts_real_2d_on_a_live_target_and_mixed_without_it(self):
        script = load()

        class Arguments:
            real_2d = True
            repeat = 1
            only = ""

        self.assertIsNone(script.precheck(Arguments, "live", {}))
        self.assertIsNotNone(script.precheck(Arguments, "mixed", {}))
        Arguments.real_2d = False
        self.assertIsNone(script.precheck(Arguments, "mixed", {}))
        Arguments.repeat = 0
        self.assertIsNotNone(script.precheck(Arguments, "mixed", {}))
        Arguments.repeat, Arguments.only = 1, "S3"
        self.assertIsNotNone(script.precheck(Arguments, "mixed", {}))


class Pictures(unittest.TestCase):
    def setUp(self):
        self.script = load()

    def test_every_picture_is_a_valid_rgb_png(self):
        for name, data in (("bust", self.script.style_png("bust")), ("full", self.script.style_png("full")),
                           ("character", self.script.character_png())):
            with self.subTest(picture=name):
                width, height, colour, depth, rows = png_rows(data)
                self.assertEqual((width, height), (self.script.SIZE, self.script.SIZE))
                self.assertEqual((colour, depth), (2, 8))
                self.assertGreater(len(set(bytes(row) for row in rows)), 10, "the picture is not blank")

    def test_the_bust_is_cut_by_the_frame_and_the_full_body_stands_inside_it(self):
        background = bytes(self.script.STYLE_BACKGROUND)
        def bottom_has_figure(data):
            _, _, _, _, rows = png_rows(data)
            last = rows[-1]
            return any(last[x:x + 3] != background for x in range(0, len(last), 3))
        self.assertTrue(bottom_has_figure(self.script.style_png("bust")))
        self.assertFalse(bottom_has_figure(self.script.style_png("full")))

    def test_pictures_are_deterministic(self):
        self.assertEqual(self.script.style_png("bust"), self.script.style_png("bust"))
        self.assertNotEqual(self.script.style_png("bust"), self.script.style_png("full"))


def card(kind="next_step", tools=("refine_image",), question="风格图里看不到全身，想按哪种身体比例来画？"):
    return {"id": "choice-1", "kind": kind, "question": question, "state": "pending",
            "options": [{"id": f"o{i}", "label": f"选项 {i}", "step": {"tool": tool, "capability": "image-to-image"}}
                        for i, tool in enumerate(tools)]}


class Cards(unittest.TestCase):
    def setUp(self):
        self.script = load()

    def test_a_proportion_card_has_refine_image_steps(self):
        self.assertEqual(len(self.script.ratio_cards([card()])), 1)
        self.assertEqual(self.script.ratio_cards([card(kind="concept_quantity", tools=("create_sketch",))]), [])
        self.assertEqual(self.script.ratio_cards([{"id": "x", "kind": "next_step", "options": [{"id": "a"}]}]), [])
        self.assertEqual(self.script.ratio_cards([]), [])

    def test_tally_counts_2d_tasks_and_denied_2d_cards_once_each(self):
        tasks = [{"id": "t1", "capability": "image-to-image"}]
        decided = [("image-to-image", True), ("image-to-image", False), ("image-to-3d", False)]
        self.assertEqual(self.script.tally(tasks, decided), (2, 1))
        self.assertEqual(self.script.tally([{"id": "t", "capability": "text-to-3d"}], []), (0, 1))
        self.assertEqual(self.script.tally([], []), (0, 0))


class Verdicts(unittest.TestCase):
    def setUp(self):
        self.script = load()

    def verdict(self, checks):
        return [ok for _, ok, _ in checks]

    def test_no_full_body_passes_with_one_card_and_no_submission(self):
        checks = self.script.judge_first_turn(True, ["ask_choice"], [card()], 0, 0)
        self.assertEqual(self.verdict(checks), [True, True, True])

    def test_no_full_body_fails_when_the_agent_draws_first_or_skips_the_card(self):
        self.assertIn(False, self.verdict(self.script.judge_first_turn(True, ["ask_choice", "refine_image"],
                                                                       [card()], 1, 0)))
        self.assertIn(False, self.verdict(self.script.judge_first_turn(True, ["refine_image"], [], 1, 0)))
        self.assertIn(False, self.verdict(self.script.judge_first_turn(True, ["ask_choice"], [card()], 0, 1)))

    def test_the_card_must_carry_the_agents_own_question(self):
        default = self.script.DEFAULT_QUESTION
        for question in (default, "", "  "):
            checks = self.script.judge_first_turn(True, ["ask_choice"], [card(question=question)], 0, 0)
            self.assertEqual(self.verdict(checks), [True, False, True], question)

    def test_full_body_passes_with_no_card_and_one_direct_submission(self):
        checks = self.script.judge_first_turn(False, ["inspect_image", "refine_image"], [], 1, 0)
        self.assertEqual(self.verdict(checks), [True, True])

    def test_full_body_fails_on_a_card_two_submissions_or_a_3d_step(self):
        self.assertEqual(self.verdict(self.script.judge_first_turn(False, ["ask_choice"], [card()], 0, 0)),
                         [False, False])
        self.assertIn(False, self.verdict(self.script.judge_first_turn(False, ["refine_image", "refine_image"],
                                                                       [], 2, 0)))
        self.assertIn(False, self.verdict(self.script.judge_first_turn(False, ["refine_image"], [], 1, 1)))

    def test_answering_the_card_submits_exactly_one_2d_generation(self):
        self.assertEqual(self.verdict(self.script.judge_answer(1, 0)), [True])
        for two_d, other in ((0, 0), (2, 0), (1, 1)):
            self.assertEqual(self.verdict(self.script.judge_answer(two_d, other)), [False], (two_d, other))


if __name__ == "__main__":
    unittest.main()
