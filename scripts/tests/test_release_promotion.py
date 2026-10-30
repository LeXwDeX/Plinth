"""Offline tests of the promotion rules in scripts/release-promotion.py (#74).

These are the checks release-env.py applies before an environment with promote_from
(formal-test) receives a revision: exact revision, native acceptance report, manifest.
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
    return {"formal_promotion": 1, "revision": SHA, "image_tag": TAG,
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


if __name__ == "__main__":
    unittest.main()
