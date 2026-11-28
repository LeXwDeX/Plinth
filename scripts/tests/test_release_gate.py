"""Offline tests of the release gate check in scripts/release-gate.py (#707).

The decision is a pure function of plain data (evaluate); the git side is exercised once on a
throwaway repository. The sample matrices follow the docs/regression-matrix.md format.
"""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("release_gate", ROOT / "scripts/release-gate.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

HEAD = "f5bc5e10f3d9ae0a52593ec521e3af41537a97c3"
HEAD8 = HEAD[:8]
OLD8 = "a5176f3a"


def commit(sha, message, files=(), parents=1):
    return {"sha": sha * 40 if len(sha) == 1 else sha, "parents": ["p"] * parents,
            "message": message, "files": list(files)}


def v196_changes(extra_files=()):
    """v1.9.5..v1.9.6 in miniature: four Issues, one integration PR, a Catalog change."""
    commits = [
        commit("1", "test: 生成纹理开关\n\nIssue: #704", ["internal/catalog/texture_toggle_704_test.go"]),
        commit("2", "chore(catalog): 升 revision\n\nIssue: #704", ["internal/catalog/default_catalog.json"]),
        commit("3", "fix(canvas): 蒙版\n\nIssue: #705", ["src/components/studio/mask.tsx"]),
        commit("4", "fix(chat): 选择卡\n\nIssue: #699", ["internal/chat/choice.go"]),
        commit("5", "fix(studio): 缩略图\n\nIssue: #691", ["src/components/studio/card.tsx"]),
        commit("6", "Merge branch 'feat/196-b' into integration/196", parents=2),
        commit("7", "Merge pull request #706 from owner/integration/196\n\nfeat: 1.9.6 集成", parents=2),
    ]
    files = sorted({path for item in commits for path in item["files"]} | set(extra_files))
    return {"base": "v1.9.5", "base_revision": "e" * 40, "head": HEAD, "commits": commits,
            "pr_commits": {706: [item["sha"] for item in commits[:6]]}, "files": files}


def matrix(rows, diffs="| 无新增差异（核对于 {head8} 2026-11-28） | — | — | 2026-11-28 | — |", head8=HEAD8):
    header = ("| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 |\n"
              "|---|---|---|---|---|---|---|---|---|\n")
    body = "".join(f"| {row_id} | 行为 | {issues} | L1 | `scripts/x.sh` | 通过 | {state} | {last} | {evidence} |\n"
                   for row_id, issues, state, last, evidence in rows)
    return ("# 回归测试表\n\n## 主表\n\n" + header + body
            + "\n## 每次发布必跑\n\n- R-001\n\n## Fake 与真实的已知差异\n\n"
            + "| 差异 | 状态 | Issue | 记录日期 | 备注 |\n|---|---|---|---|---|\n"
            + diffs.format(head8=head8) + "\n")


RULES = """# 发布门禁

## 豁免清单

| Issue | 类别 | 理由 | 用户同意日期 |
|---|---|---|---|
{exemptions}

## 变更记录

| 日期 | 内容 | 原因 | 同意依据 |
|---|---|---|---|
| 2026-11-28 | 创建 | v1.9.6 带着已知 fake 差异发布 | — |
"""


def rules(exemptions=""):
    return RULES.format(exemptions=exemptions)


def passing_rows(last=f"{HEAD8} 2026-11-28"):
    return [("R-001", "#704", "OK", last, "[L1](https://forge.example.invalid/issues/2#issuecomment-1)"),
            ("R-002", "#699, #705", "MANUAL", last, "评论链接"),
            ("R-003", "#691", "OK", last, "评论链接")]


class V196TrialTests(unittest.TestCase):
    """The acceptance cases of #707 on a sample matrix shaped like v1.9.6."""

    def test_rows_present_but_not_verified_at_the_candidate_and_an_open_difference_fail(self):
        text = matrix(passing_rows(last=f"{OLD8} 2026-11-27"),
                      diffs="| Tripo texture=false 仍交付带贴图 GLB | OPEN | #704 | 2026-11-28 | 回灌未合并 |")
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), text, rules())
        self.assertEqual(result["status"], "fail")
        self.assertEqual(result["changed_issues"], [691, 699, 704, 705])
        reasons = "\n".join(result["reasons"])
        for number in (691, 699, 704, 705):
            self.assertIn(f"#{number}：", reasons)
        self.assertIn(f"最后验证于 {OLD8}，不是候选 {HEAD8}", reasons)
        self.assertIn("已知差异仍是 OPEN：Tripo texture=false", reasons)
        self.assertIn("fake 登记表没有修改", reasons)

    def test_everything_in_place_passes(self):
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(passing_rows()), rules())
        self.assertEqual(result["reasons"], [])
        self.assertEqual(result["status"], "pass")
        self.assertEqual(result["rules_version"], 1)
        self.assertEqual([item["result"] for item in result["coverage"]], ["ok"] * 4)
        self.assertEqual(result["pull_requests"], [{"pr": 706, "issues": [691, 699, 704, 705]}])
        self.assertEqual(result["areas"], ["catalog"])

    def test_a_register_change_replaces_the_no_new_difference_line(self):
        changes = v196_changes([gate.MATRIX_PATH, "docs/fake-register.md"])
        result = gate.evaluate(changes, matrix(passing_rows(), diffs=""), rules())
        self.assertEqual(result["status"], "pass", result["reasons"])

    def test_a_stale_no_new_difference_line_does_not_count(self):
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(passing_rows(), head8=OLD8), rules())
        self.assertEqual(result["status"], "fail")
        self.assertIn("fake 登记表没有修改", "\n".join(result["reasons"]))


class CoverageTests(unittest.TestCase):
    def evaluate(self, rows, extra_files=(gate.MATRIX_PATH,), rules_text=None):
        return gate.evaluate(v196_changes(extra_files), matrix(rows), rules() if rules_text is None else rules_text)

    def test_a_missing_row_gap_state_empty_evidence_or_bad_date_fails(self):
        cases = {
            "没有「来源 Issue」含它": passing_rows()[:2],
            "状态是 GAP": passing_rows()[:2] + [("R-003", "#691", "GAP", f"{HEAD8} 2026-11-28", "链接")],
            "「证据」为空": passing_rows()[:2] + [("R-003", "#691", "OK", f"{HEAD8} 2026-11-28", "—")],
            "「最后验证」不是": passing_rows()[:2] + [("R-003", "#691", "OK", "—", "链接")],
        }
        for expected, rows in cases.items():
            with self.subTest(expected=expected):
                result = self.evaluate(rows)
                self.assertEqual(result["status"], "fail")
                self.assertIn(expected, "\n".join(r for r in result["reasons"] if r.startswith("#691")))

    def test_every_live_row_of_a_changed_issue_must_pass_but_obsolete_rows_are_ignored(self):
        stale = passing_rows() + [("R-004", "#704", "OK", f"{OLD8} 2026-11-20", "链接")]
        self.assertEqual(self.evaluate(stale)["status"], "fail")
        obsolete = passing_rows() + [("R-004", "#704", "OBSOLETE", "—", "—")]
        self.assertEqual(self.evaluate(obsolete)["status"], "pass")

    def test_the_matrix_must_change_in_the_range(self):
        result = self.evaluate(passing_rows(), extra_files=())
        self.assertIn("范围内没有被修改", "\n".join(result["reasons"]))

    def test_a_missing_matrix_or_rules_document_fails(self):
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), None, rules(), "docs/regression-matrix.md@f5bc5e10")
        self.assertIn("回归表缺失：docs/regression-matrix.md@f5bc5e10", result["reasons"])
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(passing_rows()), None)
        self.assertIn("门禁规则文档缺失：docs/release-gate.md", result["reasons"])
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), "# 空表\n", rules())
        self.assertTrue([r for r in result["reasons"] if r.startswith("回归表没有主表")])

    def test_a_commit_without_an_issue_trailer_fails(self):
        changes = v196_changes([gate.MATRIX_PATH])
        changes["commits"].insert(0, commit("8", "fix: 顺手改一下", ["src/a.ts"]))
        result = gate.evaluate(changes, matrix(passing_rows()), rules())
        self.assertEqual(result["untracked_commits"], ["88888888"])
        self.assertIn("没有 `Issue: #N` 引用", "\n".join(result["reasons"]))

    def test_a_pull_request_without_any_issue_is_its_own_change_point(self):
        changes = v196_changes([gate.MATRIX_PATH])
        changes["commits"].append(commit("9", "Merge pull request #710 from owner/x", parents=2))
        changes["commits"].append(commit("a", "Merge pull request #711 from owner/y\n\nCloses #712", parents=2))
        points, prs, _ = gate.change_points(changes)
        self.assertEqual(points[710]["kind"], "pr")
        self.assertEqual(prs[711], [712])
        self.assertIn(712, points)
        result = gate.evaluate(changes, matrix(passing_rows()), rules())
        self.assertIn("#710：回归表主表没有「来源 Issue」含它的有效行", result["reasons"])


class ExemptionTests(unittest.TestCase):
    def changes_with_docs_issue(self, files=("docs/release-gate.md", "scripts/release-gate.py")):
        changes = v196_changes([gate.MATRIX_PATH, *files])
        changes["commits"].insert(0, commit("b", "ci: 发布门禁\n\nIssue: #707", files))
        return changes

    def test_a_consented_exemption_covers_a_docs_and_scripts_issue(self):
        text = rules("| #707 | 脚本/文档 | 只改门禁脚本与文档 | 2026-11-28 |")
        result = gate.evaluate(self.changes_with_docs_issue(), matrix(passing_rows()), text)
        self.assertEqual(result["status"], "pass", result["reasons"])
        self.assertEqual(result["exemptions"]["used"], [{"issue": 707, "reason": "只改门禁脚本与文档",
                                                         "agreed": "2026-11-28"}])

    def test_an_exemption_without_consent_date_or_reason_is_invalid(self):
        for row in ("| #707 | 脚本/文档 | 只改门禁脚本与文档 | — |", "| #707 | 脚本/文档 | — | 2026-11-28 |",
                    "| #707, #708 | 脚本/文档 | 理由 | 2026-11-28 |"):
            with self.subTest(row=row):
                result = gate.evaluate(self.changes_with_docs_issue(), matrix(passing_rows()), rules(row))
                self.assertEqual(result["status"], "fail")
                self.assertTrue(result["exemptions"]["invalid"])
                self.assertIn("#707：回归表主表没有「来源 Issue」含它的有效行", result["reasons"])

    def test_an_issue_that_changed_runtime_files_cannot_be_exempted(self):
        changes = self.changes_with_docs_issue(("docs/release-gate.md", "internal/app/app.go"))
        result = gate.evaluate(changes, matrix(passing_rows()), rules("| #707 | 文档 | 理由 | 2026-11-28 |"))
        self.assertEqual(result["status"], "fail")
        self.assertIn("internal/app/app.go", json.dumps(result["exemptions"]["invalid"], ensure_ascii=False))

    def test_an_exemption_for_an_issue_outside_the_range_is_reported_unused(self):
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(passing_rows()),
                               rules("| #600 | 文档 | 理由 | 2026-11-01 |"))
        self.assertEqual(result["status"], "pass")
        self.assertEqual(result["exemptions"]["unused"], [{"issue": 600}])


class FakeDifferenceTests(unittest.TestCase):
    def evaluate(self, diffs, extra_files=(gate.MATRIX_PATH,)):
        return gate.evaluate(v196_changes(extra_files), matrix(passing_rows(), diffs=diffs), rules())

    def test_accepted_needs_a_consent_date_and_unknown_states_fail(self):
        marker = f"| 无新增差异（核对于 {HEAD8} 2026-11-28） | — | — | 2026-11-28 | — |\n"
        self.assertEqual(self.evaluate(marker + "| 差异甲 | ACCEPTED | #1 | 2026-11-20 | 用户 2026-11-21 明确接受 |")
                         ["status"], "pass")
        self.assertEqual(self.evaluate(marker + "| 差异甲 | FIXED | #1 | 2026-11-20 | 已回灌 |")["status"], "pass")
        for row, expected in (("| 差异甲 | ACCEPTED | #1 | 2026-11-20 | 用户接受 |", "没有用户明确接受的日期"),
                              ("| 差异甲 | WONTFIX | #1 | 2026-11-20 | — |", "不是 OPEN/FIXED/ACCEPTED")):
            with self.subTest(row=row):
                result = self.evaluate(marker + row)
                self.assertEqual(result["status"], "fail")
                self.assertIn(expected, "\n".join(result["reasons"]))

    def test_an_open_difference_fails_even_without_fake_changes(self):
        changes = v196_changes([gate.MATRIX_PATH])
        changes["files"].remove("internal/catalog/default_catalog.json")
        text = matrix(passing_rows(), diffs="| 差异乙 | OPEN | #1 | 2026-11-20 | — |")
        result = gate.evaluate(changes, text, rules())
        self.assertEqual(result["areas"], [])
        self.assertEqual(result["reasons"], ["fake：已知差异仍是 OPEN：差异乙"])

    def test_a_missing_difference_section_fails(self):
        text = matrix(passing_rows()).split("## Fake 与真实的已知差异")[0]
        result = gate.evaluate(v196_changes([gate.MATRIX_PATH, "docs/fake-register.md"]), text, rules())
        self.assertIn("fake：回归表缺少「Fake 与真实的已知差异」一节", result["reasons"])


class ParsingTests(unittest.TestCase):
    def test_cells_keep_escaped_pipes_and_strip_marks(self):
        self.assertEqual(gate.split_row(r"| A | `a \| b` | **OK** |"), ["A", "`a | b`", "**OK**"])
        self.assertEqual(gate.clean("**OK**"), "OK")
        self.assertEqual(gate.clean("`f5bc5e10 2026-11-28`"), "f5bc5e10 2026-11-28")

    def test_trailers_name_one_or_more_issues(self):
        self.assertEqual(gate.trailer_issues("x\n\nIssue: #704\nIssues: #1, #2\n提到 #9"), {704, 1, 2})

    def test_the_committed_rules_document_parses(self):
        parsed = gate.parse_rules((ROOT / gate.RULES_PATH).read_text(encoding="utf-8"))
        self.assertGreaterEqual(parsed["rules_version"], 1)
        self.assertTrue(all(entry["issue"] and entry["reason"] and entry["agreed"] for entry in parsed["exemptions"]))

    def test_a_table_inside_a_code_fence_is_not_a_heading_boundary(self):
        text = "## 豁免清单\n\n```\n# not a heading\n```\n\n| Issue | 理由 | 用户同意日期 |\n|---|---|---|\n| #1 | r | 2026-11-28 |\n"
        self.assertEqual(gate.parse_rules(text)["exemptions"][0]["issue"], 1)


class GitCollectionTests(unittest.TestCase):
    """collect() on a throwaway repository: trailers, a merged pull request and the diff."""

    def git(self, repo, *args):
        return subprocess.run(["git", "-C", repo, *args], check=True, capture_output=True,
                              env={"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid",
                                   "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid",
                                   "PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "HOME": repo}
                              ).stdout.decode().strip()

    def write(self, repo, path, text):
        target = Path(repo, path)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding="utf-8")
        self.git(repo, "add", path)

    def test_collect_and_main_over_a_real_history(self):
        with tempfile.TemporaryDirectory() as repo:
            self.git(repo, "init", "-q", "-b", "main")
            self.write(repo, "README.md", "x\n")
            self.git(repo, "commit", "-q", "-m", "init")
            self.git(repo, "tag", "v0")
            self.git(repo, "checkout", "-q", "-b", "feat")
            self.write(repo, "internal/catalog/default_catalog.json", "{}\n")
            self.git(repo, "commit", "-q", "-m", "feat: 目录\n\nIssue: #12")
            self.git(repo, "checkout", "-q", "main")
            self.git(repo, "merge", "-q", "--no-ff", "feat", "-m", "Merge pull request #13 from owner/feat")
            head = self.git(repo, "rev-parse", "HEAD")
            changes = gate.collect(repo, "v0", head)
            self.assertEqual(changes["files"], ["internal/catalog/default_catalog.json"])
            self.assertEqual(len(changes["pr_commits"][13]), 1)
            points, prs, untracked = gate.change_points(changes)
            self.assertEqual((sorted(points), prs, untracked), ([12], {13: [12]}, []))
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                code = gate.main(["--repo", repo, "--base", "v0", "--head", head, "--json"])
            result = json.loads(output.getvalue())
            self.assertEqual(code, 1)
            self.assertIn(f"回归表缺失：docs/regression-matrix.md@{head[:8]}", result["reasons"])
            self.assertEqual(result["changed_issues"], [12])
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(gate.main(["--repo", repo, "--base", "nope", "--head", head]), 2)
                self.assertEqual(gate.main(["--repo", repo, "--base", head, "--head", "v0"]), 2)


if __name__ == "__main__":
    unittest.main()
