#!/usr/bin/env python3
"""Offline tests for the #536 conversation-wire script (smoke-l5-chat-wire.py).

No network, no server, no upstream: the gate tests run the script as a subprocess and stop at
its refusal or at the missing environment (BASE is never set), before any request exists.
"""
import importlib.util
import os
import pathlib
import subprocess
import sys
import unittest

sys.dont_write_bytecode = True
ROOT = pathlib.Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts" / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))


def load():
    spec = importlib.util.spec_from_file_location("smoke_l5_chat_wire", SMOKE / "smoke-l5-chat-wire.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


m = load()
import l5_common as c  # noqa: E402


def message(*calls, **extra):
    return {"role": "assistant", "content": "ok", "toolCalls": [dict(call) for call in calls], **extra}


class Prompts(unittest.TestCase):
    def test_the_fake_tool_marker_is_added_only_for_a_fake_target(self):
        fake, live, mixed = m.prompts("fake"), m.prompts("live"), m.prompts("mixed")
        self.assertIn(m.FAKE_TOOL_MARKER, fake["tool"])
        self.assertTrue(m.FAKE_TOOL_MARKER.startswith("[[fake:tool:" + m.READ_ONLY_TOOL))
        for shape in (live, mixed):
            self.assertNotIn("[[fake", shape["tool"])
        for kind in ("text", "image"):
            self.assertEqual(fake[kind], live[kind])
            self.assertNotIn("[[fake", live[kind])

    def test_no_prompt_asks_for_a_generation(self):
        for shape in ("fake", "live"):
            for text in m.prompts(shape).values():
                self.assertTrue("不要" in text and "工具" in text or "不要调用任何生成工具" in text, text)


class Model(unittest.TestCase):
    def test_vision_model_is_never_guessed(self):
        self.assertEqual(m.pick_vision_model(["a", "qwen3.8-omni-flash"], "qwen3.8-omni-flash"), "qwen3.8-omni-flash")
        self.assertIsNone(m.pick_vision_model(["deepseek", "gpt-astra"], "qwen3.8-omni-flash"))
        self.assertIsNone(m.pick_vision_model([], "x"))
        self.assertIsNone(m.pick_vision_model(None, "x"))


class Picture(unittest.TestCase):
    def test_probe_picture_is_a_png_above_every_minimum_side(self):
        width, height, rows = c.decode_png(m.probe_picture())
        self.assertEqual((width, height), (m.PICTURE_SIDE, m.PICTURE_SIDE))
        self.assertGreater(min(width, height), 64)  # Plinth's own minimum_side_pixels (#535)
        self.assertEqual(len(rows), height)
        centre, corner = rows[height // 2][3 * (width // 2):3 * (width // 2) + 3], rows[0][:3]
        self.assertNotEqual(bytes(centre), bytes(corner))  # a colour block on a ground, not one flat colour


class TurnProblems(unittest.TestCase):
    clean = {"role": "assistant", "content": "一句话回答。"}

    def test_a_clean_turn_has_no_problem(self):
        self.assertEqual(m.turn_problems(self.clean, [self.clean], []), [])
        call = message({"name": "list_offerings", "arguments": {}, "result": {"ok": 1}})
        self.assertEqual(m.turn_problems(self.clean, [call], [], "list_offerings"), [])

    def test_every_failure_is_named(self):
        self.assertEqual(m.turn_problems(None, [], []), ["no final reply (timed out)"])
        self.assertIn("empty reply", m.turn_problems({"content": "  "}, [], []))
        failed = {"content": "未知错误（错误 ID：abc）"}
        self.assertTrue(any("failure text" in p for p in m.turn_problems(failed, [], [])))
        errored = message({"name": "list_offerings", "error": "boom"})
        self.assertTrue(any("tool error" in p for p in m.turn_problems(self.clean, [errored], [], "list_offerings")))
        self.assertTrue(any("no list_offerings call" in p for p in m.turn_problems(self.clean, [self.clean], [], "list_offerings")))
        task = {"id": "t1", "capability": "text-to-image", "state": "SUCCEEDED"}
        self.assertTrue(any("generation task" in p for p in m.turn_problems(self.clean, [self.clean], [task])))


class Gate(unittest.TestCase):
    """The script uses the fake-default gate: no switches on a fake target, both on a live or mixed one."""

    def run_script(self, argv, **environment):
        env = {"PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", ""), "PYTHONDONTWRITEBYTECODE": "1", **environment}
        return subprocess.run([sys.executable, str(SMOKE / "smoke-l5-chat-wire.py"), *argv], env=env,
                              capture_output=True, text=True, timeout=30, cwd=ROOT)

    def test_a_live_or_mixed_target_needs_both_switches(self):
        for shape in ("live", "mixed", ""):  # an unknown shape counts as live
            environment = {"ART_AI_UPSTREAM_SHAPE": shape} if shape else {}
            for argv, extra in (([], {}), (["--live"], {}), ([], {"ART_AI_LIVE_ACK": "1"})):
                with self.subTest(shape=shape, argv=argv, extra=extra):
                    result = self.run_script(argv, **environment, **extra)
                    self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                    self.assertIn("refusing to run", result.stderr)
                    self.assertIn("ART_AI_LIVE_ACK=1", result.stderr)

    def test_a_fake_target_refuses_the_live_switch(self):
        for extra in ({}, {"ART_AI_LIVE_ACK": "1"}):
            result = self.run_script(["--live"], ART_AI_UPSTREAM_SHAPE="fake", **extra)
            self.assertEqual(result.returncode, 2)
            self.assertIn("--live was given but the target runs the fake upstream", result.stderr)

    def test_the_gates_that_pass_stop_at_the_missing_environment_before_any_request(self):
        # fake with no switches, and live/mixed with both: past the gate, so the next refusal is the
        # missing BASE and credentials. No request exists to send.
        for shape, argv, extra in (("fake", [], {}), ("mixed", ["--live"], {"ART_AI_LIVE_ACK": "1"}),
                                   ("live", ["--live"], {"ART_AI_LIVE_ACK": "1"})):
            with self.subTest(shape=shape):
                result = self.run_script(argv, ART_AI_UPSTREAM_SHAPE=shape, **extra)
                self.assertEqual(result.returncode, 2)
                self.assertIn("missing environment", result.stderr)
                self.assertNotIn("PASS:", result.stdout)


class Shared(unittest.TestCase):
    def test_other_scripts_keep_the_live_only_gate(self):
        import inspect
        self.assertEqual(inspect.signature(c.execute).parameters["gate"].default, "live-only")
        # a fake target is refused by every live-only script (astra-tools stands for them)
        result = subprocess.run([sys.executable, str(SMOKE / "smoke-l5-astra-tools.py")],
                                env={"PATH": os.environ["PATH"], "ART_AI_UPSTREAM_SHAPE": "fake",
                                     "PYTHONDONTWRITEBYTECODE": "1"},
                                capture_output=True, text=True, timeout=30, cwd=ROOT)
        self.assertEqual(result.returncode, 2)
        self.assertIn("does not call real, paid upstreams", result.stderr)



class ChatWireTitleTests(unittest.TestCase):
    def test_the_text_session_keeps_the_default_title_so_the_first_turn_names_it(self):
        """A custom title marks the session as user-titled and the first-turn title request is never made,
        so the title check would pass on a title the script itself set."""
        source = (pathlib.Path(__file__).resolve().parents[1] / "smoke" / "smoke-l5-chat-wire.py").read_text(encoding="utf-8")
        self.assertNotIn('new_session("l5-chat-wire-text"', source)

if __name__ == "__main__":
    unittest.main()
