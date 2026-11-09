#!/usr/bin/env python3
"""check_delivery of smoke-fake-stack.py must evaluate conditional output slots (#96, #89).

A SUCCEEDED edit-multiview Task whose requested view is missing has to fail the smoke;
a view that was not requested may be absent. Offline: no stack, no network.
"""
import importlib.util
import os
import sys
import unittest

sys.dont_write_bytecode = True

SMOKE = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "smoke")
spec = importlib.util.spec_from_file_location("fakestack", os.path.join(SMOKE, "smoke-fake-stack.py"))
fs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fs)

VIEWS = ("front", "left", "back", "right")
OFFERING = {"outputs": [
    {"role": "image", "variant": view, "delivery": "required", "minimum": 1,
     "required_when": {"parameter": f"{view}_prompt", "operator": "present"}} for view in VIEWS]}


class NoAssetsApi:
    def call(self, *args, **kwargs):  # no asset is ever fetched when none is delivered
        raise AssertionError("unexpected fetch")


def task_with(views):
    return {"assets": [{"role": "image", "variant": view, "url": f"/x/{view}", "size": 1, "sha256": "0"} for view in views]}


class ConditionalSlots(unittest.TestCase):
    def test_a_succeeded_task_without_the_requested_view_fails(self):
        problems, _ = fs.check_delivery(NoAssetsApi(), task_with([]), OFFERING, {"front_prompt": "green"})
        self.assertIn("missing required slot ('image', 'front')", problems)

    def test_a_view_that_was_not_requested_may_be_absent(self):
        problems, _ = fs.check_delivery(NoAssetsApi(), task_with([]), OFFERING, {})
        self.assertEqual(problems, [])

    def test_only_the_requested_views_are_due(self):
        problems, _ = fs.check_delivery(NoAssetsApi(), task_with([]), OFFERING, {"left_prompt": "x", "back_prompt": "y"})
        self.assertEqual(sorted(problems), ["missing required slot ('image', 'back')", "missing required slot ('image', 'left')"])

    def test_an_unconditional_slot_is_always_due(self):
        offering = {"outputs": [{"role": "model", "variant": "glb", "delivery": "required", "minimum": 1}]}
        problems, _ = fs.check_delivery(NoAssetsApi(), task_with([]), offering, {})
        self.assertEqual(problems, ["missing required slot ('model', 'glb')"])


class ZeroRealUpstream(unittest.TestCase):
    """#218: one refusal line of the stack's loopback guard in any stack log fails the run."""

    def run_check(self, logs):
        import tempfile
        with tempfile.TemporaryDirectory() as work:
            for name, text in logs.items():
                os.makedirs(os.path.join(work, name))
                with open(os.path.join(work, name, "stack.log"), "w", encoding="utf-8") as handle:
                    handle.write(text)
            fs.results.clear()
            fs.check_no_real_upstream(work)
            return fs.results[-1][1]

    def test_clean_logs_pass(self):
        self.assertTrue(self.run_check({"main": "level=INFO msg=started\n", "fault-task-failed": ""}))

    def test_a_refused_request_fails(self):
        line = fs.REFUSAL_MARKER + ": connection tripo host api.example.com\n"
        self.assertFalse(self.run_check({"main": "ok\n", "fault-key-quota": line}))

    def test_no_stack_log_fails(self):
        self.assertFalse(self.run_check({}))


if __name__ == "__main__":
    unittest.main()
