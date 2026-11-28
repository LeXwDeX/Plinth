"""Offline tests of the promotion rules in scripts/release-promotion.py (#74).

These are the checks release-env.py applies before an environment with promote_from
(prod) receives a revision: exact revision, native acceptance report, manifest.
"""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("release_promotion", ROOT / "scripts/release-promotion.py")
promotion = importlib.util.module_from_spec(spec)
spec.loader.exec_module(promotion)

SHA = "a" * 40
TAG = "aaaaaaa-20261019T010000Z"
FORGE = "https://forge.example.invalid/owner/project"
EVIDENCE = FORGE + "/issues/63#issuecomment-123"
COMMENT_ISSUE = "https://api.forge.example.invalid/repos/owner/project/issues/63"


def acceptance():
    return {"prod_promotion": 1, "revision": SHA, "image_tag": TAG,
            "checks": {name: {"status": "pass", "failed": 0, "passed": 1,
                              "evidence": EVIDENCE} for name in ("L0", "L1", "L1_agent", "related")}}


def note(report):
    return "```json\n" + json.dumps(report) + "\n```"


class PromotionRuleTests(unittest.TestCase):
    def test_acceptance_comment_is_read_from_the_configured_github_issue(self):
        comment = {"body": "accepted", "issue_url": COMMENT_ISSUE}
        with patch.object(promotion, "run", return_value=json.dumps(comment)) as run:
            self.assertEqual(promotion.read_acceptance_comment("owner/project", 63, 123), comment)
            run.assert_called_once_with("gh", "api", "repos/owner/project/issues/comments/123")
        for issue_url in ("https://api.forge.example.invalid/repos/owner/project/issues/7",
                          "https://api.forge.example.invalid/repos/other/project/issues/63", None):
            with patch.object(promotion, "run", return_value=json.dumps({"body": "accepted", "issue_url": issue_url})):
                with self.assertRaises(promotion.ReleaseError):
                    promotion.read_acceptance_comment("owner/project", 63, 123)

    def test_exact_revision_and_native_acceptance_required(self):
        report = acceptance()
        self.assertEqual(promotion.acceptance_from_note(note(report), SHA, FORGE), report)
        for bad in ("main", "a" * 7, "a" * 39 + ";", "A" * 40, " " + SHA):
            with self.subTest(bad=bad), self.assertRaises(promotion.ReleaseError):
                promotion.validate_revision(bad)
        for body in (EVIDENCE, "{}", "```json\n{}\n```", "```json\n{broken}\n```"):
            with self.assertRaises(promotion.ReleaseError):
                promotion.acceptance_from_note(body, SHA, FORGE)

    def test_two_reports_in_one_note_are_refused(self):
        with self.assertRaises(promotion.ReleaseError):
            promotion.acceptance_from_note(note(acceptance()) + "\n" + note(acceptance()), SHA, FORGE)

    def test_wrong_revision_failed_missing_or_soft_failed_check_refused(self):
        cases = []
        report = acceptance(); report["revision"] = "b" * 40; cases.append(report)
        report = acceptance(); del report["checks"]["L1_agent"]; cases.append(report)
        for field, value in (("status", "soft-fail"), ("failed", 1), ("passed", 0), ("evidence", "https://example.com/pass")):
            report = acceptance(); report["checks"]["L0"][field] = value; cases.append(report)
        report = acceptance(); report["checks"]["L1"]["skipped"] = 1; cases.append(report)
        report = acceptance(); report["checks"]["related"]["soft_failed"] = 1; cases.append(report)
        report = acceptance(); report["image_tag"] = "not-a-tag"; cases.append(report)
        for report in cases:
            with self.subTest(report=report), self.assertRaises(promotion.ReleaseError):
                promotion.acceptance_from_note(note(report), SHA, FORGE)

    def test_acceptance_needs_a_configured_forge_and_evidence_under_it(self):
        with self.assertRaises(promotion.ReleaseError):
            promotion.acceptance_from_note(note(acceptance()), SHA, None)
        with self.assertRaises(promotion.ReleaseError):
            promotion.acceptance_from_note(note(acceptance()), SHA, "https://other.example.invalid/group/project")

    def test_acceptance_cannot_replace_the_test_release_manifest(self):
        promotion.validate_manifest(f"{TAG} {SHA} 2026-10-19T01:00:00Z\n", SHA, TAG)
        for text in ("", f"other {SHA} 2026-10-19T01:00:00Z", f"{TAG} {'b'*40} 2026-10-19T01:00:00Z"):
            with self.assertRaises(promotion.ReleaseError):
                promotion.validate_manifest(text, SHA, TAG)

    def test_command_failures_report_only_the_command_name(self):
        with self.assertRaises(promotion.ReleaseError) as caught:
            promotion.run("false")
        self.assertEqual(str(caught.exception), "release command failed: false")


GATE_EVIDENCE = FORGE + "/issues/2#issuecomment-456"


def gated_acceptance():
    report = acceptance()
    report["prod_promotion"] = 2
    report["release_gate"] = {"status": "pass", "evidence": GATE_EVIDENCE}
    return report


def gate_receipt(**changes):
    receipt = {"release_gate": 1, "revision": SHA, "status": "pass", "rules_version": 1,
               "base": "v1.9.5", "changed_issues": [704], "checked_at": "2026-11-28"}
    receipt.update(changes)
    return receipt


class ReleaseGateReferenceTests(unittest.TestCase):
    """#707: a format-2 acceptance report points at a passing release gate receipt for the revision."""

    def test_format_two_carries_a_passing_gate_reference(self):
        report = gated_acceptance()
        self.assertEqual(promotion.acceptance_from_note(note(report), SHA, FORGE), report)
        self.assertEqual(promotion.gate_reference(report, FORGE), (2, 456))

    def test_format_one_still_reads_but_has_no_gate_reference(self):
        report = acceptance()
        self.assertEqual(promotion.acceptance_from_note(note(report), SHA, FORGE), report)
        with self.assertRaises(promotion.ReleaseError):
            promotion.gate_reference(report, FORGE)

    def test_format_two_without_or_with_a_bad_gate_record_is_refused(self):
        cases = []
        report = gated_acceptance(); del report["release_gate"]; cases.append(report)
        report = gated_acceptance(); report["release_gate"] = "pass"; cases.append(report)
        for status in ("fail", "PASS", None):
            report = gated_acceptance(); report["release_gate"]["status"] = status; cases.append(report)
        for evidence in (None, "", "https://example.com/issues/2#issuecomment-1", FORGE + "/issues/2",
                         FORGE + "/pull/2#issuecomment-1", FORGE + "/issues/2#issuecomment-1 extra"):
            report = gated_acceptance(); report["release_gate"]["evidence"] = evidence; cases.append(report)
        report = gated_acceptance(); report["prod_promotion"] = 3; cases.append(report)
        for report in cases:
            with self.subTest(report=report.get("release_gate"), version=report["prod_promotion"]):
                with self.assertRaises(promotion.ReleaseError):
                    promotion.acceptance_from_note(note(report), SHA, FORGE)
                with self.assertRaises(promotion.ReleaseError):
                    promotion.gate_reference(report, FORGE)

    def test_the_gate_comment_holds_one_passing_receipt_for_the_exact_revision(self):
        receipt = gate_receipt()
        self.assertEqual(promotion.gate_receipt_from_note("门禁通过\n" + note(receipt), SHA), receipt)
        # The acceptance report itself names release_gate; it is not a receipt.
        self.assertEqual(promotion.gate_receipt_from_note(note(gated_acceptance()) + "\n" + note(receipt), SHA),
                         receipt)

    def test_a_missing_doubled_mismatched_or_failing_receipt_is_refused(self):
        bodies = ["", "no json", note(gated_acceptance()), note(gate_receipt()) + "\n" + note(gate_receipt()),
                  "```json\n{broken}\n```", note(gate_receipt(revision="b" * 40)),
                  note(gate_receipt(revision=SHA[:8])), note(gate_receipt(status="fail")),
                  note(gate_receipt(status=None)), note(gate_receipt(release_gate=2)),
                  note(gate_receipt(release_gate=True)), note(gate_receipt(release_gate="1"))]
        for body in bodies:
            with self.subTest(body=body[:60]), self.assertRaises(promotion.ReleaseError):
                promotion.gate_receipt_from_note(body, SHA)


def smoke_report(**changes):
    report = {"schema": promotion.SMOKE_SCHEMA, "version": promotion.SMOKE_VERSION, "environment": "dev-test",
              "revision": SHA, "upstream_shape": "fake", "started_at": "2026-11-22T00:00:00Z",
              "finished_at": "2026-11-22T00:05:00Z",
              "checks": {name: {"status": "pass", "passed": 3, "failed": 0, "skipped": 0, "soft_failed": 0,
                                "script": "smoke.sh", "log": f"/srv/dev-test/production/reports/smoke-{SHA}/{name}.log"}
                         for name in ("L0", "L1", "L1_agent")}}
    report.update(changes)
    return report


class MachineSmokeReportTests(unittest.TestCase):
    """#411: release-env.py writes the report; a promotion must find three passing layers in it."""

    def test_a_report_with_three_passing_layers_is_accepted(self):
        text = json.dumps(smoke_report())
        self.assertEqual(sorted(promotion.smoke_checks_from_report(text, SHA)),
                         ["L0", "L1", "L1_agent"])

    def test_the_report_path_is_pinned_to_the_revision_under_the_release_root(self):
        self.assertEqual(promotion.smoke_report_path(SHA), f"production/reports/smoke-{SHA}.json")

    def test_a_layer_that_failed_skipped_or_soft_failed_is_refused(self):
        for name, field, value in (("L0", "failed", 1), ("L1", "status", "soft-fail"), ("L1_agent", "passed", 0),
                                   ("L1_agent", "skipped", 1), ("L1_agent", "soft_failed", 2), ("L0", "passed", True)):
            with self.subTest(name=name, field=field, value=value):
                report = smoke_report()
                report["checks"][name][field] = value
                with self.assertRaises(promotion.ReleaseError):
                    promotion.smoke_checks_from_report(json.dumps(report), SHA)

    def test_a_missing_layer_is_refused(self):
        for name in ("L0", "L1", "L1_agent"):
            with self.subTest(name=name):
                report = smoke_report()
                del report["checks"][name]
                with self.assertRaises(promotion.ReleaseError):
                    promotion.smoke_checks_from_report(json.dumps(report), SHA)

    def test_another_revision_unknown_schema_or_broken_json_is_refused(self):
        cases = [json.dumps(smoke_report(revision="b" * 40)), json.dumps(smoke_report(schema="plinth.other")),
                 json.dumps(smoke_report(version=2)), "{}", "not json", json.dumps({"schema": "list"})]
        for text in cases:
            with self.subTest(text=text[:40]), self.assertRaises(promotion.ReleaseError):
                promotion.smoke_checks_from_report(text, SHA)

    def test_the_note_checks_use_the_same_passing_rule(self):
        # The note and the machine report must agree on what "pass" means.
        for field, value in (("failed", 1), ("passed", 0), ("skipped", 1), ("soft_failed", 1), ("status", "fail")):
            with self.subTest(field=field, value=value):
                report = acceptance()
                report["checks"]["L0"][field] = value
                self.assertFalse(promotion.check_passed(report["checks"]["L0"]))
                with self.assertRaises(promotion.ReleaseError):
                    promotion.acceptance_from_note(note(report), SHA, FORGE)


if __name__ == "__main__":
    unittest.main()
