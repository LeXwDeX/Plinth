"""Offline tests of the release gate check in scripts/release-gate.py (#707).

The decision is a pure function of plain data (evaluate); the git side is exercised once on a
throwaway repository. The sample matrices follow the docs/regression-matrix.md format.
"""
import contextlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

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


DEFAULT_AREAS = (("2D（fake）", "R-001"), ("3D（fake）", "R-002"), ("真实补漏", "R-003"))


def areas(entries=DEFAULT_AREAS):
    """A release coverage list section (#738); entries are (area, row IDs)."""
    return ("\n### 2.1 发布覆盖清单（门禁硬检查）\n\n| 覆盖面 | 行 | 说明 |\n|---|---|---|\n"
            + "".join(f"| {area} | {ids} | 说明 |\n" for area, ids in entries))


def matrix(rows, diffs="| 无新增差异（核对于 {head8} 2026-11-28） | — | — | 2026-11-28 | — |", head8=HEAD8,
           defects="", coverage=None):
    """A sample matrix; a row is (id, issues, state, last, evidence[, layer]) with layer L1 by default."""
    header = ("| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 |\n"
              "|---|---|---|---|---|---|---|---|---|\n")
    body = "".join(f"| {row[0]} | 行为 | {row[1]} | {row[5] if len(row) > 5 else 'L1'} | `scripts/x.sh` | 通过 "
                   f"| {row[2]} | {row[3]} | {row[4]} |\n" for row in rows)
    return ("# 回归测试表\n\n## 主表\n\n" + header + body
            + "\n## 每次发布必跑\n\n- R-001\n\n## Fake 与真实的已知差异\n\n"
            + "| 差异 | 状态 | Issue | 记录日期 | 备注 |\n|---|---|---|---|---|\n"
            + diffs.format(head8=head8) + "\n" + defects + (areas() if coverage is None else coverage))


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
        self.assertIn(f"最后验证于 {OLD8}，无法解析为本仓库的提交", reasons)
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

    def test_every_table_of_the_main_section_is_read(self):
        header = ("| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |\n"
                  "|---|---|---|---|---|---|---|---|---|---|\n")
        row = "| {0} | 行为 | {1} | unit | `scripts/x.sh` | 通过 | OK | {2} 2026-11-28 | E1 | |\n"
        text = ("# 回归测试表\n\n## 2. 每次发布必跑\n\n| 顺序 | 时机 | 行 | 命令 |\n|---|---|---|---|\n"
                "| 1 | 合并前 | G1 | `scripts/x.sh` |\n\n## 3. 主表\n\n### 门禁\n\n" + header + row.format("G1", "#86", HEAD8)
                + "\n### 按功能的行为\n\n" + header + row.format("R-1", "#704、#705", HEAD8)
                + row.format("R-2", "#699, #691", HEAD8)
                + "\n### prod 发布后回归\n\n" + header + row.format("P1", "#2", HEAD8)
                + "\n## 4. Fake 与真实的已知差异\n\n| 差异 | 状态 | Issue | 记录日期 | 备注 |\n|---|---|---|---|---|\n"
                + "| 差异甲 | FIXED | #704 | 2026-11-28 | 已回灌 |\n")
        parsed = gate.parse_matrix(text)
        self.assertTrue(parsed["main_found"])
        self.assertEqual([row["id"] for row in parsed["rows"]], ["G1", "R-1", "R-2", "P1"])
        self.assertEqual(parsed["rows"][1]["issues"], [704, 705])
        self.assertEqual(parsed["rows"][2]["issues"], [691, 699])
        self.assertEqual([row["state"] for row in parsed["diff_rows"]], ["FIXED"])

    def test_the_committed_matrix_reads_every_main_table(self):
        text = (ROOT / gate.MATRIX_PATH).read_text(encoding="utf-8")
        parsed = gate.parse_matrix(text)
        main = text.split("## 3. 主表", 1)[1].split("\n## 4.", 1)[0]
        expected = len(re.findall(r"(?m)^\| (?!ID \||---)[^|]+\|", main))
        self.assertEqual(len(parsed["rows"]), expected)
        self.assertGreater(expected, 5)
        self.assertTrue(parsed["diff_rows"], "the difference section must be a table the gate reads")

    def test_a_table_inside_a_code_fence_is_not_a_heading_boundary(self):
        text = "## 豁免清单\n\n```\n# not a heading\n```\n\n| Issue | 理由 | 用户同意日期 |\n|---|---|---|\n| #1 | r | 2026-11-28 |\n"
        self.assertEqual(gate.parse_rules(text)["exemptions"][0]["issue"], 1)


OLD = OLD8 + "0" * 32


def revision(ancestor=True, runtime=(), sha=OLD, error=None):
    return {"sha": sha, "ancestor": ancestor, "runtime": list(runtime), "error": error}


class RevisionValidityTests(unittest.TestCase):
    """#718: a row verified at an ancestor stays current while only docs, scripts and CI changed since."""

    def evaluate(self, revisions, rows=None):
        rows = rows or passing_rows(last=f"{OLD8} 2026-11-27")
        return gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(rows), rules(), revisions=revisions)

    def test_an_ancestor_with_only_docs_and_scripts_changes_since_is_current(self):
        result = self.evaluate({OLD8: revision()})
        self.assertEqual(result["status"], "pass", result["reasons"])

    def test_an_ancestor_with_runtime_changes_since_is_stale(self):
        result = self.evaluate({OLD8: revision(runtime=["src/components/studio/card.tsx"])})
        self.assertEqual(result["status"], "fail")
        self.assertIn(f"R-003 最后验证于 {OLD8}，此后到候选 {HEAD8} 改了运行产物：src/components/studio/card.tsx",
                      "\n".join(result["reasons"]))

    def test_a_revision_that_is_not_an_ancestor_or_does_not_resolve_is_stale(self):
        result = self.evaluate({OLD8: revision(ancestor=False)})
        self.assertIn(f"最后验证于 {OLD8}，不是候选 {HEAD8} 的祖先", "\n".join(result["reasons"]))
        result = self.evaluate({OLD8: revision(sha=None, ancestor=False, error="ambiguous or unknown")})
        self.assertIn(f"最后验证于 {OLD8}，无法解析为本仓库的提交：ambiguous or unknown", "\n".join(result["reasons"]))
        self.assertEqual(self.evaluate({})["status"], "fail")

    def test_the_candidate_itself_needs_no_lookup(self):
        self.assertEqual(self.evaluate({}, rows=passing_rows())["status"], "pass")

    def test_runtime_files_is_the_exemption_rule(self):
        files = ["docs/a.md", "README.md", "scripts/x.py", ".github/w.yml", ".claude/a.md", "src/a.ts",
                 "internal/x/README.md", "go.mod"]
        self.assertEqual(gate.runtime_files(files), ["go.mod", "internal/x/README.md", "src/a.ts"])


class UnitCITests(unittest.TestCase):
    """#718: a unit row may cite CI; the host-ci/gates status of its revision must then be success."""

    def evaluate(self, ci, last=f"{HEAD8} 2026-11-28", revisions=None, layer="unit"):
        rows = passing_rows()[:2] + [("R-003", "#691", "OK", last, "CI", layer)]
        return gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(rows), rules(), revisions=revisions, ci=ci)

    def test_ci_evidence_passes_on_a_successful_gates_status(self):
        self.assertEqual(self.evaluate({HEAD: {"state": "success", "error": None}})["status"], "pass")
        result = self.evaluate({OLD: {"state": "success", "error": None}}, last=f"{OLD8} 2026-11-27",
                               revisions={OLD8: revision()})
        self.assertEqual(result["status"], "pass", result["reasons"])

    def test_ci_evidence_fails_on_a_failed_missing_or_unreadable_status(self):
        cases = {"host-ci/gates 是 failure": {HEAD: {"state": "failure", "error": None}},
                 "没有 host-ci/gates 状态": {HEAD: {"state": None, "error": None}},
                 "gh 不可用": {HEAD: {"state": None, "error": "gh 不可用"}},
                 "未查询": {}}
        for expected, ci in cases.items():
            with self.subTest(expected=expected):
                result = self.evaluate(ci)
                self.assertEqual(result["status"], "fail")
                self.assertIn(f"R-003 的证据是 CI，但 {HEAD8}", "\n".join(result["reasons"]))
                self.assertIn(expected, "\n".join(result["reasons"]))

    def test_ci_rows_name_the_revisions_to_query(self):
        rows = passing_rows()[:2] + [("R-003", "#691", "OK", f"{OLD8} 2026-11-27", "CI", "unit"),
                                     ("R-004", "#691", "OK", f"{OLD8} 2026-11-27", "CI", "unit"),
                                     ("R-005", "#691", "OK", f"{HEAD8} 2026-11-27", "CI", "L1")]
        parsed = gate.parse_matrix(matrix(rows))
        self.assertEqual(gate.ci_revisions(parsed, HEAD, {OLD8: revision()}), [OLD])
        self.assertEqual(gate.revision_refs(parsed, HEAD), [OLD8])

    def test_the_status_file_replaces_the_network_and_each_revision_is_read_once(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder, "statuses.json")
            path.write_text(json.dumps({HEAD: "success", OLD8: "failure"}), encoding="utf-8")
            states = gate.ci_states([HEAD, OLD, "c" * 40], {"RELEASE_GATE_STATUS_FILE": str(path)})
        self.assertEqual(states[HEAD], {"state": "success", "error": None})
        self.assertEqual(states[OLD], {"state": "failure", "error": None})
        self.assertEqual(states["c" * 40], {"state": None, "error": None})
        calls = []
        states = gate.ci_states([HEAD, HEAD, OLD], {}, fetch=lambda sha: calls.append(sha) or "success")
        self.assertEqual(calls, [HEAD, OLD])
        self.assertEqual(states[OLD]["state"], "success")

    def test_without_a_status_file_and_without_gh_the_state_is_an_error(self):
        with mock.patch.object(gate.shutil, "which", return_value=None):
            states = gate.ci_states([HEAD], {})
        self.assertIsNone(states[HEAD]["state"])
        self.assertIn("gh 不可用", states[HEAD]["error"])

    def test_the_latest_gates_status_is_taken_from_the_statuses_list(self):
        listing = [{"context": "host-ci/workflows", "state": "success"},
                   {"context": "host-ci/gates", "state": "failure"},
                   {"context": "host-ci/gates", "state": "success"}]
        self.assertEqual(gate.latest_gates_state(listing), "failure")
        self.assertIsNone(gate.latest_gates_state([]))
        self.assertEqual(gate.repository_slug("https://github.com/LeXwDeX/Plinth.git"), "LeXwDeX/Plinth")
        self.assertEqual(gate.repository_slug("git@github.com:owner/name.git"), "owner/name")


class PhaseTests(unittest.TestCase):
    """#718: before promotion the PROD rows are not yet verifiable; before the tag they are."""

    def evaluate(self, phase, state="OK", layer="prod-paid"):
        rows = passing_rows() + [("P-1", "#704", state, "—", "", layer)]
        return gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(rows), rules(), phase=phase)

    def test_the_prod_prefix_is_matched_case_insensitively(self):
        for layer in ("prod-readonly", "PROD-paid", "Prod-readonly"):
            with self.subTest(layer=layer):
                self.assertEqual(self.evaluate("promote", layer=layer)["status"], "pass")
        self.assertEqual(self.evaluate("promote", layer="dev-test-fake")["status"], "fail")

    def test_promote_does_not_require_current_prod_rows_but_tag_does(self):
        result = self.evaluate("promote")
        self.assertEqual(result["status"], "pass", result["reasons"])
        self.assertEqual(result["phase"], "promote")
        self.assertEqual(next(item for item in result["coverage"] if item["issue"] == 704)["deferred"], ["P-1"])
        result = self.evaluate("tag")
        self.assertEqual(result["status"], "fail")
        self.assertEqual(result["phase"], "tag")
        self.assertIn("P-1 的「最后验证」不是", "\n".join(result["reasons"]))
        self.assertEqual(gate.evaluate(v196_changes([gate.MATRIX_PATH]), matrix(passing_rows()), rules())["phase"], "tag")

    def test_promote_still_rejects_a_gap_prod_row(self):
        result = self.evaluate("promote", state="GAP")
        self.assertEqual(result["status"], "fail")
        self.assertIn("P-1 状态是 GAP", "\n".join(result["reasons"]))


class DefectSectionTests(unittest.TestCase):
    """#718: known defects live in their own section and never reach the gate."""

    def test_rows_of_the_defect_section_are_not_main_rows(self):
        defects = ("\n## 5. 已知缺陷（不进门禁）\n\n| 缺陷 | Issue | 记录日期 | 备注 |\n|---|---|---|---|\n"
                   "| R-705-1 批准卡预计消耗 | #719 | 2026-11-28 | 来源 #705 第 1 条 |\n\n"
                   "| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 |\n"
                   "|---|---|---|---|---|---|---|---|---|\n"
                   "| R-X | 行为 | #691 | L1 | — | — | GAP | — | |\n")
        text = matrix(passing_rows(), defects=defects)
        self.assertEqual([row["id"] for row in gate.parse_matrix(text)["rows"]], ["R-001", "R-002", "R-003"])
        self.assertEqual(gate.evaluate(v196_changes([gate.MATRIX_PATH]), text, rules())["status"], "pass")


class CoverageAreaTests(unittest.TestCase):
    """#738: 2D (fake), 3D (fake) and real-gap rows are checked on every release, whatever changed."""

    def evaluate(self, rows=None, coverage=None, phase="tag", changes=None, revisions=None, ci=None):
        text = matrix(passing_rows() if rows is None else rows, coverage=coverage)
        return gate.evaluate(changes or v196_changes([gate.MATRIX_PATH]), text, rules(), phase=phase,
                             revisions=revisions, ci=ci)

    def area(self, result, name):
        return next(item for item in result["coverage_areas"] if item["area"] == name)

    def test_every_area_with_current_rows_passes_and_is_summarised(self):
        rows = passing_rows() + [("R-010", "#600", "OK", f"{HEAD8} 2026-11-28", "评论链接")]
        result = self.evaluate(rows, areas([("2D（fake）", "R-001、R-010"), ("3D（fake）", "R-002"),
                                            ("真实补漏", "R-003")]))
        self.assertEqual(result["status"], "pass", result["reasons"])
        self.assertEqual([item["area"] for item in result["coverage_areas"]], list(gate.COVERAGE_AREAS))
        self.assertEqual(self.area(result, "2D（fake）"),
                         {"area": "2D（fake）", "rows": ["R-001", "R-010"], "passed": ["R-001", "R-010"],
                          "deferred": [], "failed": []})

    def test_rows_outside_the_changed_issues_are_still_checked(self):
        rows = passing_rows() + [("R-010", "#600", "OK", f"{OLD8} 2026-11-20", "评论链接")]
        result = self.evaluate(rows, areas([("2D（fake）", "R-001、R-010"), ("3D（fake）", "R-002"),
                                            ("真实补漏", "R-003")]))
        self.assertEqual(result["status"], "fail")
        self.assertNotIn(600, result["changed_issues"])
        self.assertIn(f"覆盖面「2D（fake）」：R-010 最后验证于 {OLD8}，无法解析为本仓库的提交", "\n".join(result["reasons"]))
        self.assertEqual([item["id"] for item in self.area(result, "2D（fake）")["failed"]], ["R-010"])

    def test_gap_obsolete_empty_evidence_and_unknown_rows_fail(self):
        rows = passing_rows() + [("R-011", "#600", "GAP", "—", ""), ("R-012", "#600", "OBSOLETE", "—", ""),
                                 ("R-013", "#600", "OK", f"{HEAD8} 2026-11-28", "—")]
        result = self.evaluate(rows, areas([("2D（fake）", "R-001, R-011"), ("3D（fake）", "R-002、R-012"),
                                            ("真实补漏", "R-003、R-013、R-099")]))
        reasons = "\n".join(result["reasons"])
        self.assertEqual(result["status"], "fail")
        for expected in ("覆盖面「2D（fake）」：R-011 状态是 GAP", "覆盖面「3D（fake）」：R-012 状态是 OBSOLETE",
                         "覆盖面「真实补漏」：R-013 的「证据」为空", "覆盖面「真实补漏」：R-099 不在主表"):
            self.assertIn(expected, reasons)

    def test_a_missing_section_area_or_an_unknown_area_fails(self):
        result = self.evaluate(coverage="")
        self.assertIn("回归表缺少「发布覆盖清单」一节", "\n".join(result["reasons"]))
        result = self.evaluate(coverage=areas(DEFAULT_AREAS[:2]))
        self.assertIn("发布覆盖清单缺少覆盖面「真实补漏」或它没有行", result["reasons"])
        result = self.evaluate(coverage=areas(DEFAULT_AREAS + (("真实 2D", "R-001"),)))
        self.assertIn("发布覆盖清单里的覆盖面「真实 2D」不是 2D（fake）、3D（fake）、真实补漏之一", result["reasons"])
        result = self.evaluate(coverage=areas([("2D（fake）", "—"), *DEFAULT_AREAS[1:]]))
        self.assertIn("发布覆盖清单缺少覆盖面「2D（fake）」或它没有行", result["reasons"])

    def test_promote_defers_prod_rows_and_tag_requires_them(self):
        rows = passing_rows() + [("P-9", "#600", "OK", "—", "", "prod-paid")]
        coverage = areas([("2D（fake）", "R-001"), ("3D（fake）", "R-002"), ("真实补漏", "R-003、P-9")])
        result = self.evaluate(rows, coverage, phase="promote")
        self.assertEqual(result["status"], "pass", result["reasons"])
        self.assertEqual(self.area(result, "真实补漏")["deferred"], ["P-9"])
        result = self.evaluate(rows, coverage, phase="tag")
        self.assertEqual(result["status"], "fail")
        self.assertIn("覆盖面「真实补漏」：P-9 的「最后验证」不是", "\n".join(result["reasons"]))

    def test_unit_rows_may_cite_ci(self):
        rows = passing_rows() + [("U-1", "#600", "OK", f"{HEAD8} 2026-11-28", "CI", "unit")]
        coverage = areas([("2D（fake）", "R-001、U-1"), *DEFAULT_AREAS[1:]])
        self.assertEqual(self.evaluate(rows, coverage, ci={HEAD: {"state": "success", "error": None}})["status"], "pass")
        result = self.evaluate(rows, coverage, ci={HEAD: {"state": "failure", "error": None}})
        self.assertIn("覆盖面「2D（fake）」：U-1 的证据是 CI", "\n".join(result["reasons"]))

    def test_the_list_parses_separators_and_the_committed_list_names_main_rows(self):
        parsed = gate.parse_matrix(matrix(passing_rows(), coverage=areas([("2D（fake）", "R-001、R-002, R-003"),
                                                                         *DEFAULT_AREAS[1:]])))
        self.assertEqual(parsed["coverage_list"][0], {"area": "2D（fake）", "ids": ["R-001", "R-002", "R-003"]})
        committed = gate.parse_matrix((ROOT / gate.MATRIX_PATH).read_text(encoding="utf-8"))
        ids = {row["id"] for row in committed["rows"]}
        self.assertEqual(sorted({entry["area"] for entry in committed["coverage_list"]}), sorted(gate.COVERAGE_AREAS))
        for entry in committed["coverage_list"]:
            self.assertTrue(entry["ids"] and set(entry["ids"]) <= ids, entry)


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

    def test_a_row_verified_before_a_docs_only_commit_passes_end_to_end(self):
        """#718: the matrix cannot name its own revision; it names the code revision before it."""
        with tempfile.TemporaryDirectory() as repo:
            self.git(repo, "init", "-q", "-b", "main")
            self.write(repo, "README.md", "x\n")
            self.git(repo, "commit", "-q", "-m", "init")
            self.git(repo, "tag", "v0")
            self.write(repo, "src/a.ts", "export {}\n")
            self.git(repo, "commit", "-q", "-m", "feat: a\n\nIssue: #12")
            code = self.git(repo, "rev-parse", "HEAD")
            row = ("CI", "unit")
            self.write(repo, gate.MATRIX_PATH, matrix([("R-12", "#12", "OK", f"{code[:8]} 2026-11-28", *row)],
                                                      coverage=areas([(a, "R-12") for a, _ in DEFAULT_AREAS])))
            self.write(repo, gate.RULES_PATH, rules())
            self.git(repo, "commit", "-q", "-m", "docs: 回归表\n\nIssue: #12")
            head = self.git(repo, "rev-parse", "HEAD")
            infos = gate.inspect_revisions(repo, head, [code[:8], "0123abcd"])
            self.assertEqual(infos[code[:8]], {"sha": code, "ancestor": True, "runtime": [], "error": None})
            self.assertIsNone(infos["0123abcd"]["sha"])
            self.assertTrue(infos["0123abcd"]["error"])
            status = Path(repo, "statuses.json")
            for state, expected in (("success", 0), ("failure", 1)):
                status.write_text(json.dumps({code: state}), encoding="utf-8")
                output = io.StringIO()
                with mock.patch.dict(os.environ, {"RELEASE_GATE_STATUS_FILE": str(status)}), \
                        contextlib.redirect_stdout(output):
                    result = gate.main(["--repo", repo, "--base", "v0", "--head", head, "--phase", "promote", "--json"])
                self.assertEqual(result, expected, output.getvalue())
                self.assertEqual(json.loads(output.getvalue())["phase"], "promote")
            # A later runtime change makes the same row stale.
            self.write(repo, "src/a.ts", "export const a = 1\n")
            self.git(repo, "commit", "-q", "-m", "fix: a\n\nIssue: #12")
            later = self.git(repo, "rev-parse", "HEAD")
            self.assertEqual(gate.inspect_revisions(repo, later, [code[:8]])[code[:8]]["runtime"], ["src/a.ts"])


if __name__ == "__main__":
    unittest.main()
