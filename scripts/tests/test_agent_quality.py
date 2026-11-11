#!/usr/bin/env python3
"""Offline tests for the request set, trajectory checks and report comparison of #238. No network."""
import importlib.util
import json
import pathlib
import sys
import unittest

sys.dont_write_bytecode = True
SMOKE = pathlib.Path(__file__).resolve().parents[1] / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))


def load(name):
    spec = importlib.util.spec_from_file_location(name, SMOKE / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


quality = load("agent_quality")
CATALOG = json.loads((pathlib.Path(__file__).resolve().parents[2] / "internal" / "catalog" / "default_catalog.json").read_text(encoding="utf-8"))
PURPOSES = quality.schema_purposes(CATALOG)


def tool_call(name, arguments=None):
    return json.dumps([{"name": name, "arguments": json.dumps(arguments or {})}])


def message(*calls):
    return {"role": "assistant", "content": "", "toolCalls": "[]" if not calls else json.dumps(calls)}


def succeeded(capability):
    return {"id": capability, "capability": capability, "state": "SUCCEEDED"}


def base_request(**overrides):
    request = {
        "id": "case",
        "title": "case",
        "modality": "3d",
        "purpose": "game_character",
        "prompt": "make it",
        "deliverables": ["model"],
        "steps": [{"user": "go", "tool": {"name": "create_model_3d"}}],
        "expected": {
            "capabilities": ["text-to-3d"],
            "required_tool_groups": [
                {"id": "usage", "any": ["ask_choice"]},
                {"id": "brief", "any": ["update_production_brief"]},
                {"id": "generate", "any": ["create_model_3d"]},
            ],
            "order": [{"before": ["ask_choice", "update_production_brief"], "after": ["create_model_3d"]}],
            "soft_tools": ["inspect_model"],
            "budgets": {"max_paid_calls": 1, "max_tool_calls": 10, "max_interruptions": 2},
        },
    }
    request.update(overrides)
    return request


class RequestSetTest(unittest.TestCase):
    def test_the_shipped_request_set_is_valid(self):
        data, path, digest = quality.load_requests()
        self.assertEqual(data["version"], 3)
        self.assertEqual(len(data["requests"]), 13)
        self.assertTrue(path.endswith("requests.json"))
        self.assertEqual(len(digest), 64)
        self.assertEqual(len({request["id"] for request in data["requests"]}), 13)
        reference = next(request for request in data["requests"] if request["id"] == "3d-image-reference")
        self.assertEqual(reference["modality"], "3d")
        self.assertEqual(reference["expected"]["capabilities"], ["image-to-3d"])
        self.assertEqual([step["tool"]["name"] for step in reference["steps"]],
                         ["create_model_3d_from_image", "inspect_model", "compare_model_reference", "record_self_check"])
        concept = next(request for request in data["requests"] if request["id"] == "2d-3d-input-concept")
        self.assertEqual(concept["modality"], "2d")
        self.assertEqual(concept["expected"]["capabilities"], ["text-to-image"])
        multistep = next(request for request in data["requests"] if request["id"] == "3d-multistep-rig-checkpoint")
        self.assertEqual(multistep["modality"], "3d")
        self.assertEqual(multistep["expected"]["min_tasks"], {"text-to-3d": 1, "remesh": 2, "rigging": 1})
        self.assertEqual([step["tool"]["name"] for step in multistep["steps"]],
                         ["ask_choice", "update_production_brief", "propose_production_plan", "create_model_3d",
                          "plan_model_processing", "revise_processing_plan", "inspect_model"])
        self.assertEqual(multistep["steps"][5]["tool"]["arguments"]["operation"], "insert")
        project = next(request for request in data["requests"] if request["id"] == "2d-series-project")
        self.assertEqual(project["category"], "series_consistency")
        self.assertEqual(project["project"]["name"], "红发剑士系列")
        self.assertEqual([step.get("project_setup") for step in project["steps"] if step.get("project_setup")],
                         ["promote_first_image", "new_session"])
        self.assertEqual({request["modality"] for request in data["requests"]}, {"2d", "3d"})
        self.assertEqual(quality.validate_request_set(data, PURPOSES), [])
        for request in data["requests"]:
            if request["modality"] == "2d":
                self.assertIn(request["purpose"], PURPOSES["2d"])
            else:
                self.assertIn(request["purpose"], PURPOSES["3d"])

    def test_duplicate_ids_and_missing_fields_are_reported(self):
        data = {"version": 1, "requests": [base_request(), base_request()]}
        problems = quality.validate_request_set(data)
        self.assertTrue(any("duplicate request id" in problem for problem in problems))
        data["requests"][0]["expected"]["capabilities"] = []
        problems = quality.validate_request_set(data)
        self.assertTrue(any("capabilities is empty" in problem for problem in problems))

    def test_a_3d_request_needs_a_purpose(self):
        data = {"version": 1, "requests": [base_request(purpose=None)]}
        self.assertTrue(any("needs a purpose" in problem for problem in quality.validate_request_set(data)))

    def test_a_2d_request_takes_a_2d_purpose_only(self):
        request = base_request(modality="2d", purpose="icon_prop", expected={
            "capabilities": ["text-to-image"],
            "required_tool_groups": [{"id": "generate", "any": ["refine_image"]}],
            "budgets": {"max_paid_calls": 1, "max_tool_calls": 4, "max_interruptions": 1},
        })
        self.assertEqual(quality.validate_request_set({"version": 2, "requests": [request]}, PURPOSES), [])
        request["purpose"] = "game_character"
        problems = quality.validate_request_set({"version": 2, "requests": [request]}, PURPOSES)
        self.assertTrue(any("not declared for 2d" in problem for problem in problems))

    def test_project_setup_steps_are_accepted_but_never_both_tool_and_setup(self):
        request = base_request(
            modality="2d", purpose="illustration",
            project={"name": "series", "conventions": {"image_use": "illustration"}},
            steps=[
                {"user": "go", "tool": {"name": "refine_image"}},
                {"user": "promote", "project_setup": "promote_first_image", "asset": {"name": "role A"}},
                {"user": "new session", "project_setup": "new_session"},
            ],
            expected={
                "capabilities": ["text-to-image"],
                "required_tool_groups": [{"id": "generate", "any": ["refine_image"]}],
                "budgets": {"max_paid_calls": 1, "max_tool_calls": 4, "max_interruptions": 1},
            },
        )
        self.assertEqual(quality.validate_request_set({"version": 3, "requests": [request]}, PURPOSES), [])
        request["steps"][1]["tool"] = {"name": "refine_image"}
        problems = quality.validate_request_set({"version": 3, "requests": [request]}, PURPOSES)
        self.assertTrue(any("not both" in problem for problem in problems))
        del request["steps"][1]["tool"]
        request["steps"][2]["project_setup"] = "delete_everything"
        problems = quality.validate_request_set({"version": 3, "requests": [request]}, PURPOSES)
        self.assertTrue(any("unknown project_setup" in problem for problem in problems))
        request["steps"][2]["project_setup"] = "new_session"
        request["project"] = {"conventions": {}}
        problems = quality.validate_request_set({"version": 3, "requests": [request]}, PURPOSES)
        self.assertTrue(any("project needs a name" in problem for problem in problems))


class TrajectoryTest(unittest.TestCase):
    def test_tools_are_read_in_wire_order(self):
        messages = [
            message({"name": "ask_choice", "arguments": json.dumps({"kind": "usage_profile"})}),
            {"role": "assistant", "content": "ok", "toolCalls": "[]"},
            message({"name": "create_model_3d", "arguments": json.dumps({"prompt": "cube"}), "taskId": "t1"}),
        ]
        tools = quality.collect_tools(messages)
        self.assertEqual([tool["name"] for tool in tools], ["ask_choice", "create_model_3d"])
        self.assertEqual(tools[1]["arguments"], {"prompt": "cube"})
        self.assertEqual(tools[1]["task_id"], "t1")

    def test_a_paid_call_persisted_twice_is_counted_once(self):
        pending = {"name": "create_model_3d", "arguments": json.dumps({"prompt": "cube"})}
        accepted = {"name": "create_model_3d", "arguments": json.dumps({"prompt": "cube"}), "taskId": "t1"}
        tools = quality.collect_tools([message(pending), message(accepted)])
        self.assertEqual(len(tools), 1)
        self.assertEqual(tools[0]["task_id"], "t1")
        tools = quality.collect_tools([message(accepted), message(pending)])
        self.assertEqual(len(tools), 1)
        self.assertEqual(tools[0]["task_id"], "t1")

    def test_a_read_only_result_merges_into_its_pending_row(self):
        pending = {"name": "inspect_model", "arguments": json.dumps({"model": "t1"})}
        result = {"name": "inspect_model", "arguments": json.dumps({"model": "t1"}),
                  "summary": {"triangles": 72}}
        tools = quality.collect_tools([message(pending), message(result)])
        self.assertEqual(len(tools), 1)
        self.assertEqual(tools[0]["summary"], {"triangles": 72})

    def test_order_rule_needs_the_before_tools_first(self):
        ok, _ = quality.order_satisfied(["update_production_brief", "ask_choice", "create_model_3d"],
                                        {"before": ["ask_choice", "update_production_brief"], "after": ["create_model_3d"]})
        self.assertTrue(ok)
        ok, detail = quality.order_satisfied(["create_model_3d", "ask_choice"],
                                             {"before": ["ask_choice"], "after": ["create_model_3d"]})
        self.assertFalse(ok)
        self.assertIn("came after", detail)

    def test_evaluate_case_passes_a_clean_trajectory(self):
        run = {
            "session_id": "s1",
            "messages": [
                message({"name": "ask_choice"}),
                message({"name": "update_production_brief"}),
                message({"name": "create_model_3d", "taskId": "t1", "estimatedCredits": 40}),
                message({"name": "inspect_model"}),
                message({"name": "render_model_views"}),
            ],
            "tasks": [succeeded("text-to-3d")],
            "cards": [{"kind": "choice"}, {"kind": "approval"}],
            "final_reply": {"content": "完成，使用了 Tripo。"},
            "elapsed_ms": 1200,
            "image_count": 3,
        }
        case = quality.evaluate_case(base_request(), run)
        self.assertEqual(quality.case_hard_failures(case), [])
        self.assertEqual(case["metrics"]["tool_call_count"], 5)
        self.assertEqual(case["metrics"]["paid_call_count"], 1)
        self.assertEqual(case["metrics"]["interruption_count"], 2)
        self.assertEqual(case["metrics"]["self_inspection"],
                         {"inspect_model": 1, "render_model_views": 1, "compare_model_reference": 0,
                          "self_check_records": 0, "self_check_passed": 0, "self_check_issues": 0})
        self.assertEqual(case["metrics"]["credits"], {"legacy.credits": "40"})

    def test_evaluate_case_counts_reference_checks_and_recorded_conclusions(self):
        run = {
            "messages": [
                message({"name": "create_model_3d_from_image", "taskId": "t1", "estimatedCredits": 40}),
                message({"name": "inspect_model"}),
                message({"name": "compare_model_reference"}),
                message({"name": "record_self_check",
                         "summary": {"tool": "record_self_check", "task_id": "t1", "status": "passed", "status_role": "passed"}}),
            ],
            "tasks": [succeeded("image-to-3d")],
            "cards": [{"kind": "approval"}],
            "final_reply": {"content": "完成，已对照参考图。"},
        }
        request = base_request()
        request["expected"] = {
            "capabilities": ["image-to-3d"],
            "required_tool_groups": [
                {"id": "generate", "any": ["create_model_3d_from_image"]},
                {"id": "reference", "any": ["compare_model_reference"]},
                {"id": "record", "any": ["record_self_check"]},
            ],
            "order": [{"before": ["create_model_3d_from_image"], "after": ["compare_model_reference", "record_self_check"]}],
            "soft_tools": [],
            "budgets": {"max_paid_calls": 1, "max_tool_calls": 10, "max_interruptions": 1},
        }
        case = quality.evaluate_case(request, run)
        self.assertEqual(quality.case_hard_failures(case), [])
        inspection = case["metrics"]["self_inspection"]
        self.assertEqual(inspection["compare_model_reference"], 1)
        self.assertEqual(inspection["self_check_records"], 1)
        self.assertEqual(inspection["self_check_passed"], 1)
        self.assertEqual(inspection["self_check_issues"], 0)

    def test_evaluate_case_records_rework_revisions_and_checkpoints(self):
        run = {
            "messages": [
                message({"name": "create_model_3d", "taskId": "t1"}),
                message({"name": "plan_model_processing"}),
                message({"name": "revise_processing_plan", "arguments": json.dumps({"operation": "insert"})}),
                message({"name": "revise_processing_plan", "arguments": json.dumps({"operation": "skip"})}),
            ],
            "tasks": [succeeded("text-to-3d")],
            "plans": [{"id": "p1", "state": "SUCCEEDED", "steps": [
                {"capability": "remesh", "state": "SUCCEEDED", "checkpoint": {"state": "failed", "results": []}},
                {"capability": "resize", "state": "SUCCEEDED"},
                {"capability": "rigging", "state": "SUCCEEDED", "checkpoint": {"state": "passed", "results": []}},
            ]}],
            "cards": [{"kind": "plan"}],
            "final_reply": {"content": "完成。"},
        }
        request = base_request()
        request["expected"]["capabilities"] = ["text-to-3d"]
        request["expected"]["budgets"] = {"max_paid_calls": 3, "max_tool_calls": 10, "max_interruptions": 1}
        case = quality.evaluate_case(request, run)
        metrics = case["metrics"]
        self.assertEqual(metrics["rework"]["count"], 2)
        self.assertEqual(metrics["rework"]["revision_count"], 2)
        self.assertEqual(metrics["rework"]["revision_operations"], {"insert": 1, "skip": 1})
        self.assertEqual(metrics["checkpoints"], {"total": 2, "passed": 1, "failed": 1, "waived": 0, "unknown": 0})

    def test_evaluate_case_reports_each_kind_of_failure(self):
        run = {
            "messages": [message({"name": "create_model_3d"})],
            "tasks": [{"id": "t1", "capability": "text-to-3d", "state": "FAILED"}],
            "cards": [],
            "final_reply": {"content": "（错误 ID：abc）"},
            "elapsed_ms": 10,
        }
        case = quality.evaluate_case(base_request(), run)
        failed = {check["id"] for check in quality.case_hard_failures(case)}
        self.assertIn("final_reply", failed)
        self.assertIn("capability:text-to-3d", failed)
        self.assertIn("tool_group:usage", failed)
        self.assertIn("order:ask_choice+update_production_brief<create_model_3d", failed)
        self.assertIn("no_failed_tasks", failed)

    def test_budgets_are_enforced(self):
        request = base_request()
        request["expected"]["budgets"] = {"max_paid_calls": 0, "max_tool_calls": 1, "max_interruptions": 0}
        run = {
            "messages": [message({"name": "ask_choice"}), message({"name": "create_model_3d"})],
            "tasks": [succeeded("text-to-3d")],
            "cards": [{"kind": "choice"}],
            "final_reply": {"content": "done"},
        }
        failed = {check["id"] for check in quality.case_hard_failures(quality.evaluate_case(request, run))}
        self.assertIn("budget:paid_calls", failed)
        self.assertIn("budget:tool_calls", failed)
        self.assertIn("budget:interruptions", failed)


class OutputMetricsTest(unittest.TestCase):
    def test_an_rgb_png_reports_its_subject_coverage(self):
        rows = [bytes([255, 255, 255] * 8) for _ in range(8)]
        rows[3] = bytes([255, 255, 255] * 3 + [255, 0, 0] * 2 + [255, 255, 255] * 3)
        data = quality.common.encode_png(8, 8, rows)
        metrics = quality.image_metrics(data)
        self.assertEqual((metrics["width"], metrics["height"], metrics["format"]), (8, 8, "png"))
        self.assertFalse(metrics["alpha"])
        self.assertTrue(metrics["nonblank"])
        self.assertAlmostEqual(metrics["opaque_ratio"], 2 / 64, places=3)
        self.assertFalse(metrics["edge_contact"])

    def test_the_purpose_budget_is_read_from_the_schema(self):
        schema = {"chat": {"model3d_usage": {"profiles": [
            {"id": "print", "budget": {"require_watertight": True, "max_triangles": 100}},
        ]}}}
        budget = quality.purpose_budget(schema, "print")
        self.assertTrue(budget["require_watertight"])
        self.assertIsNone(quality.purpose_budget(schema, "missing"))

    def test_budget_checks_compare_the_inspect_report(self):
        report = {
            "triangles": 120,
            "materials": [{"slots": [{"present": True, "width": 4096, "height": 4096}]}],
            "uv": {"all_have_uv": True},
            "topology": {"watertight": False, "debris_components": 2},
            "skins": [],
            "bbox": {"size": [0.5, 0.5, 0.5]},
        }
        budget = {"min_triangles": 100, "max_triangles": 200, "max_texture_size": 2048,
                  "require_uv": True, "require_watertight": True, "require_skin": True,
                  "scale_range_m": {"min": 0.3, "max": 1.0}}
        checks = {check["id"]: check["status"] for check in quality.model_budget_checks(report, budget)}
        self.assertEqual(checks["output:triangles"], "pass")
        self.assertEqual(checks["output:uv"], "pass")
        self.assertEqual(checks["output:scale"], "pass")
        self.assertEqual(checks["output:texture_size"], "fail")
        self.assertEqual(checks["output:watertight"], "fail")
        self.assertEqual(checks["output:skin"], "fail")
        self.assertEqual(checks["output:debris"], "fail")


class ReportTest(unittest.TestCase):
    def report(self, revision="a", statuses=("pass",), paid=1):
        cases = [{
            "id": "case",
            "title": "case",
            "metrics": {
                "tool_call_count": 3, "paid_call_count": paid, "interruption_count": 1,
                "self_inspection": {"inspect_model": 1, "render_model_views": 1},
                "elapsed_ms": 10,
            },
            "checks": [{"id": "final_reply", "status": statuses[0], "detail": ""}],
            "tasks": [],
        }]
        return quality.summarize(cases, "trajectory-fake", revision, False,
                                 {"version": 1, "sha256": "x"})

    def test_summary_counts_hard_failures(self):
        report = self.report(statuses=("fail",))
        self.assertEqual(report["summary"]["hard_failures"], 1)
        self.assertEqual(report["summary"]["failed_cases"], ["case"])
        self.assertIn("case", quality.report_markdown(report))

    def test_summary_records_success_rate_rework_and_checkpoints(self):
        cases = [{
            "id": "case", "title": "case",
            "metrics": {"tool_call_count": 1, "paid_call_count": 1, "interruption_count": 0,
                        "self_inspection": {"inspect_model": 0, "render_model_views": 0}, "elapsed_ms": 1,
                        "rework": {"count": 2, "failed_tasks": 0, "revision_count": 2},
                        "checkpoints": {"total": 1, "passed": 0, "failed": 1, "waived": 1, "unknown": 0}},
            "checks": [{"id": "final_reply", "status": "pass", "detail": ""}],
            "tasks": [],
        }]
        report = quality.summarize(cases, "trajectory-fake", "a", False, {"version": 3, "sha256": "x"})
        metrics = report["summary"]["metrics"]
        self.assertEqual(metrics["success_rate"], 100.0)
        self.assertEqual(metrics["rework_count"], 2)
        self.assertEqual(metrics["revision_count"], 2)
        self.assertEqual(metrics["checkpoint_count"], 1)
        self.assertEqual(metrics["checkpoint_failed"], 1)
        self.assertIn("成功率", quality.report_markdown(report))
        compared = quality.compare_reports(report, report)
        self.assertIn("质量指标", compared["markdown"])

    def test_compare_flags_a_pass_to_fail_regression(self):
        result = quality.compare_reports(self.report(), self.report(revision="b", statuses=("fail",)))
        self.assertFalse(result["ok"])
        self.assertTrue(any(item["check"] == "final_reply" for item in result["regressions"]))
        self.assertIn("回退", result["markdown"])

    def test_compare_flags_more_paid_calls(self):
        result = quality.compare_reports(self.report(paid=1), self.report(revision="b", paid=2))
        self.assertFalse(result["ok"])
        self.assertTrue(any(item["check"] == "metric:paid_calls" for item in result["regressions"]))

    def test_compare_accepts_identical_reports(self):
        result = quality.compare_reports(self.report(), self.report())
        self.assertTrue(result["ok"])

    def test_compare_refuses_different_request_sets(self):
        candidate = self.report()
        candidate["request_set"] = {"version": 1, "sha256": "y"}
        result = quality.compare_reports(self.report(), candidate)
        self.assertFalse(result["ok"])
        self.assertTrue(any("request-set" in problem for problem in result["problems"]))

    def test_compare_accepts_an_explicit_new_request_set(self):
        baseline = self.report()
        candidate = self.report()
        candidate["request_set"] = {"version": 2, "sha256": "y"}
        added = json.loads(json.dumps(candidate["cases"][0]))
        added["id"] = "new-case"
        candidate["cases"].append(added)
        result = quality.compare_reports(baseline, candidate, new_request_set=True)
        self.assertTrue(result["ok"], result["problems"])
        self.assertTrue(any(item.get("case") == "new-case" for item in result["improvements"]))
        self.assertTrue(result["new_request_set"])
        # Only the cases both reports ran feed the tail metrics.
        self.assertEqual(result["metrics"]["tool_calls"]["before"], result["metrics"]["tool_calls"]["after"])

    def test_a_new_request_set_reports_metric_growth_without_blocking(self):
        baseline = self.report()
        candidate = self.report(paid=2)
        candidate["request_set"] = {"version": 2, "sha256": "y"}
        result = quality.compare_reports(baseline, candidate, new_request_set=True)
        self.assertTrue(result["ok"], result["regressions"])
        self.assertEqual(result["metrics"]["paid_calls"]["delta"], 1)

    def test_a_new_request_set_still_blocks_a_common_case_regression(self):
        baseline = self.report()
        candidate = self.report()
        candidate["request_set"] = {"version": 2, "sha256": "y"}
        candidate["cases"][0]["checks"][0]["status"] = "fail"
        result = quality.compare_reports(baseline, candidate, new_request_set=True)
        self.assertFalse(result["ok"])
        self.assertTrue(any(item["check"] == "final_reply" for item in result["regressions"]))


if __name__ == "__main__":
    unittest.main()