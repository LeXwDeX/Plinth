"""Offline tests of scripts/regression/run-fake-rows.py (#718).

Row selection is read from sample matrices and the committed one; the runs go through a stub
runner, so nothing reaches dev-test or the network.
"""
import contextlib
import importlib.util
import io
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("run_fake_rows", ROOT / "scripts/regression/run-fake-rows.py")
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)

HEADER = ("| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |\n"
          "|---|---|---|---|---|---|---|---|---|---|\n")


def matrix(rows):
    body = "".join(f"| {rid} | 行为 | #1 | {layer} | {command} | 通过 | {state} | — | | |\n"
                   for rid, layer, command, state in rows)
    return "# 回归测试表\n\n## 3. 主表\n\n### 甲\n\n" + HEADER + body + "\n## 4. Fake 与真实的已知差异\n"


SAMPLE = matrix([
    ("B1", "dev-test-fake", "`node scripts/a.mjs`", "OK"),
    ("B2", "dev-test-fake", "`python3 scripts/b.py --only x,y`", "MANUAL"),
    ("B3", "dev-test-fake", "`A=1 B=two bash scripts/c.sh dev-test`", "OK"),
    ("B4", "dev-test-fake", "`node scripts/a.mjs`（N5）；单测 `src/x.test.ts`", "OK"),
    ("B5", "dev-test-fake", "`scripts/smoke/s.sh`", "OK"),
    ("B6", "dev-test-fake", "`node scripts/old.mjs`", "OBSOLETE"),
    ("B7", "dev-test-fake", "—", "GAP"),
    ("B8", "dev-test-fake", "`node scripts/a.mjs`", "OK"),
    ("U1", "unit", "`python3 -m unittest scripts.tests.x`", "OK"),
    ("P1", "prod-paid", "`python3 scripts/live.py --live`", "OK"),
])


class Run:
    """A stub runner: records (command, environment additions) and fails the listed programs."""

    def __init__(self, failing=()):
        self.calls, self.failing = [], failing

    def __call__(self, command, extra):
        self.calls.append((command, extra))
        return 1 if any(name in " ".join(command) for name in self.failing) else 0


class SelectionTests(unittest.TestCase):
    def test_a_single_command_cell(self):
        self.assertEqual(runner.single_command("`node scripts/a.mjs`"), ({}, ["node", "scripts/a.mjs"]))
        self.assertEqual(runner.single_command("`A=1 bash scripts/c.sh dev-test`"),
                         ({"A": "1"}, ["bash", "scripts/c.sh", "dev-test"]))
        for cell in ("`node a.mjs`；单测 `b.ts`", "`scripts/s.sh`", "—", "", "`go test ./...`",
                     "node scripts/a.mjs", "`node scripts/a.mjs` 加说明", "`A=1`"):
            with self.subTest(cell=cell):
                self.assertIsNone(runner.single_command(cell))

    def test_dev_test_fake_rows_with_one_script_command_are_selected(self):
        selected, skipped = runner.select(SAMPLE)
        self.assertEqual([item["id"] for item in selected], ["B1", "B2", "B3", "B8"])
        self.assertEqual(selected[2]["env"], {"A": "1", "B": "two"})
        self.assertEqual(selected[1]["argv"], ["python3", "scripts/b.py", "--only", "x,y"])
        self.assertEqual([item["id"] for item in skipped], ["B4", "B5", "B6", "B7"])

    def test_only_narrows_the_selection_and_rejects_unknown_ids(self):
        selected, _ = runner.select(SAMPLE, only=["B3", "B1"])
        self.assertEqual([item["id"] for item in selected], ["B1", "B3"])
        with self.assertRaises(runner.UsageError):
            runner.select(SAMPLE, only=["B1", "U1"])
        with self.assertRaises(runner.UsageError):
            runner.select(SAMPLE, only=["Z9"])

    def test_the_committed_matrix_selects_the_fake_scripts(self):
        selected, skipped = runner.select((ROOT / runner.MATRIX).read_text(encoding="utf-8"))
        ids = [item["id"] for item in selected]
        for rid in ("B1", "B5", "B7", "R-681a", "R-702a", "R-704b", "R-704i"):
            self.assertIn(rid, ids)
        b5 = next(item for item in selected if item["id"] == "B5")
        self.assertIn("B3_ITEMS", b5["env"])
        self.assertEqual(b5["argv"][0], "bash")
        for rid in ("S1", "B8", "R-695", "G1", "R-688b"):
            self.assertNotIn(rid, ids)
        self.assertIn("B8", [item["id"] for item in skipped])

    def test_commands_go_through_release_env_smoke_without_live_switches(self):
        command = runner.wrap(["node", "scripts/a.mjs"])
        self.assertEqual(command, ["python3", "scripts/release-env.py", "smoke", "dev-test", "--",
                                   "node", "scripts/a.mjs"])
        self.assertNotIn("--live", command)


class RunTests(unittest.TestCase):
    def main(self, args, run):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            code = runner.main(args, matrix_text=SAMPLE, run=run, revision=lambda: "f" * 40,
                               today=lambda: "2026-11-28", clock=iter(range(0, 100, 2)).__next__)
        return code, output.getvalue()

    def test_dry_run_lists_rows_and_commands_without_running(self):
        run = Run()
        code, output = self.main(["--dry-run"], run)
        self.assertEqual(code, 0)
        self.assertEqual(run.calls, [])
        self.assertIn("f" * 40, output)
        self.assertIn("2026-11-28", output)
        self.assertIn("B3  A=1 B=two python3 scripts/release-env.py smoke dev-test -- bash scripts/c.sh dev-test", output)
        self.assertIn("跳过 B4", output)

    def test_every_row_prints_pass_or_fail_and_a_failure_makes_the_exit_code_non_zero(self):
        run = Run(failing=("scripts/b.py",))
        code, output = self.main([], run)
        self.assertEqual(code, 1)
        lines = [line for line in output.splitlines() if line.startswith(("PASS ", "FAIL "))]
        self.assertEqual([line.split()[:2] for line in lines],
                         [["PASS", "B1"], ["FAIL", "B2"], ["PASS", "B3"], ["PASS", "B8"]])
        self.assertIn("2.0s", lines[0])
        self.assertIn("同 B1", lines[3])
        self.assertEqual(len(run.calls), 3, "the same command runs once")
        self.assertEqual(run.calls[2], (runner.wrap(["bash", "scripts/c.sh", "dev-test"]), {"A": "1", "B": "two"}))
        self.assertIn("=== run-fake-rows: PASS=3 FAIL=1 ===", output)
        self.assertIn("失败：B2", output)

    def test_all_passing_exits_zero(self):
        code, output = self.main(["--only", "B1,B3"], Run())
        self.assertEqual(code, 0)
        self.assertIn("=== run-fake-rows: PASS=2 FAIL=0 ===", output)

    def test_a_usage_error_exits_two(self):
        with contextlib.redirect_stderr(io.StringIO()):
            code, _ = self.main(["--only", "Z9"], Run())
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
