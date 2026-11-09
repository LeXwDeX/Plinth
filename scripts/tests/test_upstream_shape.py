"""Offline tests of the smoke scripts' upstream shape gate (#219).

Scripts default to the fake upstream shape; a real upstream needs both --live
and ART_AI_LIVE_ACK=1, and anything else refuses before a request is sent.
"""
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts" / "smoke"


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, SMOKE / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


shape = load("upstream_shape", "upstream_shape.py")
seed = load("seed_fake_3d_task", "seed-fake-3d-task.py")


class TargetShapeTest(unittest.TestCase):
    def test_unknown_target_counts_as_live(self):
        self.assertEqual(shape.target_shape({}), "live")
        self.assertEqual(shape.target_shape({"ART_AI_UPSTREAM_SHAPE": "mock"}), "live")

    def test_shape_comes_from_the_variable_or_the_host_compose_env(self):
        self.assertEqual(shape.target_shape({"ART_AI_UPSTREAM_SHAPE": "fake"}), "fake")
        with tempfile.TemporaryDirectory() as root:
            os.makedirs(os.path.join(root, "production"))
            path = os.path.join(root, "production", "compose.env")
            with open(path, "w") as handle:
                handle.write("# no secrets\nRELEASE_DATA_MODE=ephemeral\nRELEASE_UPSTREAM_SHAPE=fake\n")
            self.assertEqual(shape.target_shape({"ART_AI_SERVER_ROOT": root}), "fake")
            # The explicit variable wins over the host file.
            self.assertEqual(shape.target_shape({"ART_AI_SERVER_ROOT": root, "ART_AI_UPSTREAM_SHAPE": "live"}), "live")
            with open(path, "w") as handle:
                handle.write("RELEASE_DATA_MODE=ephemeral\n")
            self.assertEqual(shape.target_shape({"ART_AI_SERVER_ROOT": root}), "live")


class GateTest(unittest.TestCase):
    FAKE = {"ART_AI_UPSTREAM_SHAPE": "fake"}
    LIVE = {"ART_AI_UPSTREAM_SHAPE": "live"}

    def test_fake_default_runs_on_a_fake_target_without_switches(self):
        self.assertEqual(shape.resolve("fake-default", False, self.FAKE), ("fake", None))
        self.assertEqual(shape.resolve("fake-default", False, {**self.FAKE, "ART_AI_LIVE_ACK": "1"}), ("fake", None))
        _, refusal = shape.resolve("fake-default", True, self.FAKE)
        self.assertIn("fake upstream", refusal)

    def test_live_target_needs_both_switches(self):
        for kind in ("fake-default", "live-only"):
            for live_flag, environ in ((False, self.LIVE), (True, self.LIVE), (False, {**self.LIVE, "ART_AI_LIVE_ACK": "1"}),
                                       (True, {**self.LIVE, "ART_AI_LIVE_ACK": "true"}), (False, {})):
                with self.subTest(kind=kind, live_flag=live_flag, environ=environ):
                    _, refusal = shape.resolve(kind, live_flag, environ)
                    self.assertIsNotNone(refusal)
            self.assertEqual(shape.resolve(kind, True, {**self.LIVE, "ART_AI_LIVE_ACK": "1"}), ("live", None))

    def test_live_only_refuses_a_fake_target_even_with_both_switches(self):
        _, refusal = shape.resolve("live-only", True, {**self.FAKE, "ART_AI_LIVE_ACK": "1"})
        self.assertIn("--live-upstream", refusal)

    def test_cli_exit_codes(self):
        def run(argv, environ):
            out, err = io.StringIO(), io.StringIO()
            with redirect_stdout(out), redirect_stderr(err):
                code = shape.main(argv, environ)
            return code, out.getvalue().strip()
        self.assertEqual(run(["fake-default"], self.FAKE), (0, "fake"))
        self.assertEqual(run(["live-only"], self.LIVE)[0], 2)
        self.assertEqual(run(["live-only", "--live"], {**self.LIVE, "ART_AI_LIVE_ACK": "1"}), (0, "live"))
        self.assertEqual(run(["shape"], self.FAKE), (0, "fake"))
        self.assertEqual(run(["live-only", "--yes"], self.LIVE)[0], 2)
        self.assertEqual(run([], self.LIVE)[0], 2)


class ScriptDefaultsTest(unittest.TestCase):
    """The paid scripts refuse without both switches before they read credentials or send a request."""

    def run_script(self, argv, extra=None):
        environ = {"PATH": os.environ["PATH"], "HOME": os.environ.get("HOME", ""), "ART_AI_UPSTREAM_SHAPE": "live",
                   "BASE": "http://127.0.0.1:9", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p", **(extra or {})}
        return subprocess.run(argv, env=environ, capture_output=True, text=True, timeout=30, cwd=ROOT)

    def test_live_scripts_refuse_without_both_switches(self):
        commands = (
            ["bash", str(SMOKE / "smoke-l2-livemodel.sh")],
            ["bash", str(SMOKE / "smoke-l3-batch.sh")],
            ["bash", str(SMOKE / "smoke-l5-all.sh")],
            [sys.executable, str(SMOKE / "smoke-l4-3d-isolation.py")],
            [sys.executable, str(SMOKE / "smoke-l5-chat-models.py")],
            ["bash", str(SMOKE / "smoke-l1-free.sh")],
        )
        for command in commands:
            for argv, extra in (([], {}), (["--live"], {}), ([], {"ART_AI_LIVE_ACK": "1"}),
                                (["--live"], {"ART_AI_LIVE_ACK": "1", "ART_AI_UPSTREAM_SHAPE": "fake"})):
                if command[-1].endswith("smoke-l1-free.sh") and "ART_AI_UPSTREAM_SHAPE" in extra:
                    continue  # L1 on a fake target with --live is covered by the gate test.
                with self.subTest(command=command[-1], argv=argv, extra=extra):
                    result = self.run_script(command + argv, extra)
                    self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                    self.assertIn("refusing to run", result.stderr)
                    self.assertNotIn("PASS:", result.stdout)


class SeedTest(unittest.TestCase):
    def test_seed_refuses_unless_the_target_runs_the_fake(self):
        for environ in ({}, {"ART_AI_UPSTREAM_SHAPE": "live"}):
            with self.subTest(environ=environ), redirect_stderr(io.StringIO()), \
                    mock.patch.object(seed, "seed") as run:
                self.assertEqual(seed.main({**environ, "BASE": "http://x", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}), 2)
                run.assert_not_called()

    def test_model_choice_avoids_extra_credit_ceilings(self):
        offerings = [
            {"model_id": "a", "available": False, "recommended": True},
            {"model_id": "b", "available": True, "recommended": True, "estimated_credits": 10, "maximum_estimated_credits": 40},
            {"model_id": "c", "available": True, "recommended": True, "estimated_credits": 10, "maximum_estimated_credits": 10},
        ]
        self.assertEqual(seed.pick_model(offerings), "c")
        self.assertEqual(seed.pick_model([]), "")

    def test_marker_names_the_3d_tool(self):
        self.assertTrue(seed.MARKER.startswith("[[fake:tool:create_model_3d {"))


if __name__ == "__main__":
    unittest.main()
