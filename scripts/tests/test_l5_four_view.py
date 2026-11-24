#!/usr/bin/env python3
"""Offline tests for the #417 four-view reproduction script. No network, no server."""
import importlib.util
import pathlib
import sys
import unittest

sys.dont_write_bytecode = True
SMOKE = pathlib.Path(__file__).resolve().parents[1] / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))


def load():
    spec = importlib.util.spec_from_file_location("smoke_l5_four_view_multiview",
                                                 SMOKE / "smoke-l5-four-view-multiview.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


m = load()

GROUP = {"slots": [
    {"key": "front", "status": "succeeded", "task_id": "t1", "output_asset_id": "a1"},
    {"key": "left", "status": "succeeded", "task_id": "t2", "output_asset_id": "a2"},
    {"key": "back", "status": "succeeded", "task_id": "t3", "output_asset_id": "a3"},
    {"key": "right", "status": "succeeded", "task_id": "t4", "output_asset_id": "a4"},
]}


def view_group(**statuses):
    slots = [{"key": key, "status": statuses.get(key, "succeeded")} for key in ("front", "left", "back", "right")]
    return {"run_id": "run-1", "slots": slots}


def offering(offering_id, model_id, **extra):
    return {"id": offering_id, "model_id": model_id, "available": True, **extra}


class ModelsTest(unittest.TestCase):
    def test_a_6_credit_image_model_prefers_the_known_offering(self):
        offerings = [
            offering("openai/gpt-image-2/text-to-image", "gpt-image-2.6-sunburst", estimated_credits=6),
            offering("meshy/nano-banana-2/text-to-image", "nano-banana-2", estimated_credits=6),
        ]
        self.assertEqual(m.image_model_for_cost(offerings)["id"], "openai/gpt-image-2/text-to-image")
        offerings[0]["estimated_credits"] = 9
        self.assertEqual(m.image_model_for_cost(offerings)["id"], "meshy/nano-banana-2/text-to-image")
        self.assertIsNone(m.image_model_for_cost([offering("x/y/text-to-image", "y", estimated_credits=9)]))

    def test_unavailable_or_priceless_offerings_are_not_chosen(self):
        offerings = [
            {**offering("a/b/text-to-image", "b", estimated_credits=6), "available": False},
            offering("c/d/text-to-image", "d"),
        ]
        self.assertIsNone(m.image_model_for_cost(offerings))
        self.assertIsNone(m.single_view_offering(offerings, "d"))

    def test_the_locked_and_expected_models_are_selected_by_shape(self):
        offerings = [
            offering("meshy/meshy-7.1/image-to-3d", "meshy-7.1", capability="image-to-3d"),
            offering("meshy/meshy-7.1-multiview/image-to-3d", "meshy-7.1-multiview", capability="image-to-3d"),
            offering("tripo/tripo-v3.1-multiview/image-to-3d", "tripo-v3.1-multiview", capability="image-to-3d"),
        ]
        self.assertEqual(m.single_view_offering(offerings, "meshy-7.1")["id"], "meshy/meshy-7.1/image-to-3d")
        self.assertIsNone(m.single_view_offering(offerings, "meshy-7.1-multiview"))
        self.assertEqual(m.multiview_offering(offerings, "meshy-7.1-multiview")["id"],
                         "meshy/meshy-7.1-multiview/image-to-3d")
        self.assertEqual(m.multiview_offering(offerings, "missing")["id"],
                         "meshy/meshy-7.1-multiview/image-to-3d")
        self.assertIsNone(m.multiview_offering([], "x"))


class PlanTest(unittest.TestCase):
    def test_the_plan_view_keeps_budgets_quotes_and_problems(self):
        card = {
            "request_id": "pb-1", "kind": "plan", "tool": "propose_production_plan",
            "budgets": [{"unit": "legacy.credits", "amount": "24"}],
            "estimated_totals": [{"unit": "legacy.credits", "amount": "24"},
                                 {"unit": "meshy.credit", "amount": "20"}],
            "plan": {"title": "four views", "steps": [
                {"capability": "image-to-image", "offering_id": "img", "estimate": {"unit": "legacy.credits", "amount": "6"}},
                {"capability": "image-to-3d", "offering_id": "mv", "estimate": {"unit": "meshy.credit", "amount": "20"}},
            ]},
        }
        view = m.plan_view(card)
        self.assertEqual(view["request_id"], "pb-1")
        self.assertEqual([step["capability"] for step in view["steps"]], ["image-to-image", "image-to-3d"])
        # The meshy.credit step is not covered by the legacy.credits limit.
        self.assertTrue(any("meshy.credit" in problem for problem in view["problems"]))

    def test_plan_rows_pair_each_step_with_its_task_and_compare_per_unit(self):
        card = {"plan": {"steps": [
            {"capability": "image-to-image", "offering_id": "img", "estimate": {"unit": "legacy.credits", "amount": "6"}},
            {"capability": "image-to-3d", "offering_id": "mv", "estimate": {"unit": "meshy.credit", "amount": "20"}},
        ]}}
        tasks = [
            {"id": "v1", "capability": "image-to-image", "offering_id": "img", "model_id": "image",
             "native_quote": {"unit": "legacy.credits", "total_amount": "6"}},
            {"id": "m1", "capability": "image-to-3d", "offering_id": "mv", "model_id": "multiview",
             "native_quote": {"unit": "meshy.credit", "total_amount": "40"}},
        ]
        rows, unmatched = m.plan_quote_rows(card, tasks)
        self.assertEqual(unmatched, [])
        self.assertEqual(rows[0]["delta"], "0")
        self.assertTrue(rows[0]["comparable"])
        self.assertEqual(rows[1]["delta"], "20")
        self.assertEqual(rows[1]["task_native_quote"]["source"], "task")

    def test_a_task_without_a_native_quote_uses_its_tool_call_price(self):
        card = {"plan": {"steps": [
            {"capability": "image-to-3d", "offering_id": "mv", "estimate": {"unit": "meshy.credit", "amount": "20"}},
        ]}}
        tasks = [{"id": "m1", "capability": "image-to-3d", "offering_id": "mv"}]
        rows, _ = m.plan_quote_rows(card, tasks, {"m1": ("meshy.credit", __import__("decimal").Decimal("40"))})
        self.assertEqual(rows[0]["task_native_quote"]["source"], "tool_call")
        self.assertEqual(rows[0]["delta"], "20")

    def test_a_unit_mismatch_is_not_compared_and_a_missing_task_stays_in_the_rows(self):
        card = {"plan": {"steps": [
            {"capability": "image-to-image", "offering_id": "img", "estimate": {"unit": "legacy.credits", "amount": "6"}},
            {"capability": "image-to-3d", "offering_id": "mv", "estimate": {"unit": "meshy.credit", "amount": "20"}},
        ]}}
        tasks = [{"id": "v1", "capability": "image-to-image", "offering_id": "img",
                  "native_quote": {"unit": "tripo.credit", "total_amount": "10"}}]
        rows, unmatched = m.plan_quote_rows(card, tasks)
        self.assertFalse(rows[0]["comparable"])
        self.assertIsNone(rows[0]["delta"])
        self.assertIsNone(rows[1]["task_id"])
        self.assertEqual(unmatched, [])

    def test_an_unclaimed_task_is_reported(self):
        rows, unmatched = m.plan_quote_rows({"plan": {"steps": []}}, [{"id": "x", "capability": "text-to-image"}])
        self.assertEqual(rows, [])
        self.assertEqual(unmatched, ["x"])


class ViewsTest(unittest.TestCase):
    def test_terminal_and_succeeded_views(self):
        self.assertTrue(m.views_done(GROUP))
        self.assertTrue(m.views_succeeded(GROUP))
        self.assertTrue(m.views_done(view_group(right="failed")))
        self.assertFalse(m.views_succeeded(view_group(right="failed")))
        self.assertFalse(m.views_done(view_group(right="running")))
        self.assertFalse(m.views_succeeded(None))

    def test_a_task_reports_the_view_pictures_of_its_frozen_input(self):
        task = {"id": "m1", "input": {"parameters": {
            "reference_assets": ["a", "b", "c", "d"],
            "reference_views": ["front", "left", "back", "right"]}}}
        self.assertEqual(m.task_view_refs(task), (4, ["front", "left", "back", "right"]))
        self.assertEqual(m.task_view_refs({"id": "x"}), (0, None))
        self.assertEqual(m.task_view_refs({"id": "y", "input": {"parameters": {"reference_assets": "a"}}}), (1, None))

    def test_a_view_in_flight_keeps_the_plan_watch_alive(self):
        self.assertTrue(m.group_in_flight({"slots": [{"key": "right", "status": "running"}]}))
        approval = {"slots": [{"key": "right", "status": "awaiting_approval", "request_id": "r1"}]}
        self.assertTrue(m.group_in_flight(approval, {"r1"}))
        self.assertFalse(m.group_in_flight(approval, set()), "no card is pending any more")
        self.assertFalse(m.group_in_flight(approval, {"r1"}, {"r1"}), "the decision was refused")
        self.assertFalse(m.group_in_flight(GROUP))


class VerdictTest(unittest.TestCase):
    def test_a_succeeded_multiview_task_is_not_reproduced(self):
        verdict, reason = m.decide_verdict(
            True, True, GROUP, [], [{"id": "m1", "capability": "image-to-3d", "state": "SUCCEEDED",
                                     "offering_id": "meshy/meshy-7.1-multiview/image-to-3d"}], [], True)
        self.assertEqual(verdict, "not_reproduced")
        self.assertIn("SUCCEEDED", reason)

    def test_a_multiview_call_without_a_task_is_reproduced(self):
        calls = [{"name": "create_model_3d_from_views", "arguments": {"three_view_group": "run-1"}}]
        verdict, reason = m.decide_verdict(True, True, GROUP, calls, [], [], True)
        self.assertEqual(verdict, "reproduced")
        self.assertIn("without model", reason)

    def test_a_pending_3d_card_is_reproduced_even_without_a_call(self):
        verdict, reason = m.decide_verdict(True, True, GROUP, [], [], [{"capability": "image-to-3d"}], True)
        self.assertEqual(verdict, "reproduced")
        self.assertIn("pending image-to-3D approval", reason)

    def test_a_turn_that_never_ended_without_a_3d_step_is_reproduced(self):
        verdict, _ = m.decide_verdict(True, True, GROUP, [], [], [], False)
        self.assertEqual(verdict, "reproduced")

    def test_missing_pieces_are_inconclusive(self):
        self.assertEqual(m.decide_verdict(False, False, None, [], [], [], False)[0], "inconclusive")
        self.assertEqual(m.decide_verdict(True, False, None, [], [], [], True)[0], "inconclusive")
        self.assertEqual(m.decide_verdict(True, True, view_group(back="failed"), [], [], [], True)[0], "inconclusive")
        single = [{"id": "s1", "capability": "image-to-3d", "state": "SUCCEEDED",
                   "offering_id": "meshy/meshy-7.1/image-to-3d"}]
        self.assertEqual(m.decide_verdict(True, True, GROUP, [], single, [], True)[0], "inconclusive")
        failed = [{"id": "m1", "capability": "image-to-3d", "state": "FAILED",
                   "offering_id": "meshy/meshy-7.1-multiview/image-to-3d"}]
        verdict, reason = m.decide_verdict(True, True, GROUP, [], failed, [], True)
        self.assertEqual(verdict, "inconclusive")
        self.assertIn("FAILED", reason)
        self.assertEqual(m.decide_verdict(True, True, GROUP, [], [], [], True)[0], "inconclusive")


class EvidenceTest(unittest.TestCase):
    def test_arguments_are_shortened_and_redacted(self):
        compacted = m.compact_arguments({
            "prompt": "x" * 400,
            "reference_handles": ["@img1", "https://example.test/a.png?token=secret"],
            "nested": {"group": "run-1", "note": "y" * 300},
        })
        self.assertEqual(len(compacted["prompt"]), 200)
        self.assertIn("<redacted>", compacted["reference_handles"][1])
        self.assertEqual(compacted["nested"]["group"], "run-1")
        self.assertEqual(len(compacted["nested"]["note"]), 200)

    def test_the_paid_ceiling_counts_each_task_once(self):
        paid = m.PaidTasks(2)
        paid.observe([{"id": "t1", "state": "RUNNING"}])
        paid.observe([{"id": "t1", "state": "SUCCEEDED"}, {"id": "t2", "state": "RUNNING"}])
        self.assertEqual(paid.created(), 2)
        self.assertFalse(paid.exceeded())
        paid.observe([{"id": "t3", "capability": "image-to-3d"}])
        self.assertTrue(paid.exceeded())
        self.assertEqual(paid.seen["t1"]["state"], "SUCCEEDED")

    def test_media_types(self):
        self.assertEqual(m.image_media_type(b"\x89PNG\r\n\x1a\n" + b"0" * 8), ("image/png", "png"))
        self.assertEqual(m.image_media_type(b"\xff\xd8\xff\xe0" + b"0" * 8), ("image/jpeg", "jpg"))
        self.assertEqual(m.image_media_type(b"RIFF\x00\x00\x00\x00WEBP" + b"0" * 8), ("image/webp", "webp"))
        self.assertEqual(m.image_media_type(b"GIF89a"), (None, None))


if __name__ == "__main__":
    unittest.main()