"""docs/regression-matrix.md must not drift from the repository (#705).

The main table (section 3) points at scripts and tests by repository path; every such path must exist, every
status must be one of OK, MANUAL, GAP, OBSOLETE, a GAP row must say what is missing, and the "every release"
list (section 2) may only name rows and scripts the main table carries. Known defects sit in their own section
(5, outside the release gate, #718): header 缺陷 | Issue | 记录日期 | 备注, a note on every row, Issue cells made
of #N references only, and no defect still in the main table. AGENTS.md must point at the table so a new session
finds it. The fake gate of scripts/regression/lib.py is checked offline here too.
"""
import importlib.util
import os
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MATRIX = "docs/regression-matrix.md"
STATUSES = {"OK", "MANUAL", "GAP", "OBSOLETE"}
COLUMNS = ["ID", "功能 / 行为", "来源 Issue", "验证层", "命令或脚本", "期望", "状态", "最后验证", "证据", "备注"]
PATH = re.compile(r"(?<![\w/.-])(?:\./)?((?:scripts|docs|internal|src|web|\.github)/[\w./-]*[\w-])")
VERIFIED = re.compile(r"^(—|[0-9a-f]{8} \d{4}-\d{2}-\d{2})$")
DEFECT_HEADING = "5. 已知缺陷（不进门禁）"
DEFECT_COLUMNS = ["缺陷", "Issue", "记录日期", "备注"]
ISSUES = re.compile(r"^#\d+(\s*[、,]\s*#\d+)*$")
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
ROW_ID = re.compile(r"^`?([A-Z]-?[A-Za-z0-9-]*\d[A-Za-z0-9]*)(?![\w-])", re.A)


def section(text, start, end):
    match = re.search(rf"^## {re.escape(start)}.*?(?=^## {re.escape(end)})", text, re.M | re.S)
    return match.group(0) if match else ""


def cells(line):
    return [cell.strip() for cell in line.strip().strip("|").split("|")]


def table_rows(block):
    """(header, rows) of every table in the block; separator lines are skipped."""
    header, rows = None, []
    for line in block.splitlines():
        if not line.startswith("|"):
            header = None if not line.strip() else header
            continue
        row = cells(line)
        if all(re.fullmatch(r":?-{3,}:?", c) for c in row):
            continue
        if header is None:
            header = row
            continue
        rows.append((header, row))
    return rows


def code_paths(cell):
    return [p for span in re.findall(r"`([^`]+)`", cell) for p in PATH.findall(span)]


def problems(text, root):
    """Every inconsistency of the matrix text against the tree at root, as readable strings."""
    found = []
    main = section(text, "3.", "4.")
    must = section(text, "2.", "3.")
    rows = table_rows(main)
    if not rows:
        return ["no main table rows under section 3"]
    ids, main_paths = set(), set()
    for header, row in rows:
        if header != COLUMNS:
            found.append(f"unexpected header {header}")
            continue
        if len(row) != len(COLUMNS):
            found.append(f"{row[0]}: {len(row)} cells, want {len(COLUMNS)}")
            continue
        entry = dict(zip(COLUMNS, row))
        rid = entry["ID"]
        if rid in ids:
            found.append(f"{rid}: duplicate ID")
        ids.add(rid)
        if entry["状态"] not in STATUSES:
            found.append(f"{rid}: status {entry['状态']!r} not in {sorted(STATUSES)}")
        if entry["状态"] == "GAP" and not entry["备注"]:
            found.append(f"{rid}: GAP row without a note")
        if not VERIFIED.match(entry["最后验证"]):
            found.append(f"{rid}: last verified {entry['最后验证']!r} is not '—' or '<8 hex> <YYYY-MM-DD>'")
        for cell in row:
            for path in code_paths(cell):
                if not (root / path).exists():
                    found.append(f"{rid}: {path} does not exist")
        main_paths.update(code_paths(entry["命令或脚本"]))
    found.extend(defect_problems(text, ids))
    for header, row in table_rows(must):
        entry = dict(zip(header, row))
        for rid in re.findall(r"\b[A-Z]-?[\w-]*\d\b", entry.get("行", "")):
            if rid not in ids:
                found.append(f"every-release row {rid} is not in the main table")
        for path in code_paths(entry.get("命令", "")):
            if path not in main_paths:
                found.append(f"every-release script {path} is not a command in the main table")
    return found


def defect_problems(text, main_ids):
    """The known-defect section (#718): it exists, has the fixed header, and every row is well formed."""
    if not re.search(rf"^## {re.escape(DEFECT_HEADING)}\s*$", text, re.M):
        return [f"no section '## {DEFECT_HEADING}'"]
    block = section(text, "5.", "6.")
    found = []
    if not re.search(r"^\| " + r" \| ".join(map(re.escape, DEFECT_COLUMNS)) + r" \|$", block, re.M):
        found.append(f"defect section has no table with header {DEFECT_COLUMNS}")
    for header, row in table_rows(block):
        if header != DEFECT_COLUMNS:
            found.append(f"defect table header {header}, want {DEFECT_COLUMNS}")
            continue
        if len(row) != len(DEFECT_COLUMNS):
            found.append(f"defect {row[0]!r}: {len(row)} cells, want {len(DEFECT_COLUMNS)}")
            continue
        entry = dict(zip(DEFECT_COLUMNS, row))
        name = entry["缺陷"][:24] or "(empty)"
        if not entry["缺陷"]:
            found.append("defect row without a description")
        if not ISSUES.match(entry["Issue"]):
            found.append(f"defect {name}: Issue {entry['Issue']!r} is not '#N' references")
        if not DATE.match(entry["记录日期"]):
            found.append(f"defect {name}: date {entry['记录日期']!r} is not YYYY-MM-DD")
        if not entry["备注"] or entry["备注"] == "—":
            found.append(f"defect {name}: no note")
        rid = ROW_ID.match(entry["缺陷"])
        if rid and rid.group(1) in main_ids:
            found.append(f"defect {rid.group(1)} is still a main table row")
    return found


class RegressionMatrixTests(unittest.TestCase):
    text = (ROOT / MATRIX).read_text(encoding="utf-8")

    def test_matrix_is_consistent(self):
        self.assertEqual(problems(self.text, ROOT), [])

    def test_agents_md_points_at_the_matrix(self):
        self.assertTrue(MATRIX in (ROOT / "AGENTS.md").read_text(encoding="utf-8"),
                        "AGENTS.md must reference docs/regression-matrix.md so a new session finds it")

    def test_checker_rejects_a_drifted_table(self):
        head = "| " + " | ".join(COLUMNS) + " |\n|" + "---|" * len(COLUMNS) + "\n"
        bad = ("## 2. 每次发布必跑\n\n| 顺序 | 行 | 命令 |\n|---|---|---|\n"
               "| 1 | Z9 | `scripts/regression/lib.py` |\n\n## 3. 主表\n\n" + head
               + "| A1 | x | #1 | unit | `scripts/no-such-script.py` | y | DONE | yesterday | | |\n"
               + "| A2 | x | #1 | unit | `scripts/regression/lib.mjs` | y | GAP | — | | |\n"
               + "| A2 | x | #1 | unit | — | y | OK | — | |\n\n## 4. 差异\n")
        found = "\n".join(problems(bad, ROOT))
        for needle in ("scripts/no-such-script.py does not exist", "status 'DONE'", "last verified 'yesterday'",
                       "A2: GAP row without a note", "A2: 9 cells", "every-release row Z9",
                       "every-release script scripts/regression/lib.py", f"no section '## {DEFECT_HEADING}'"):
            self.assertIn(needle, found)

    def test_the_defect_section_is_checked(self):
        # #725 fixed the four defects first recorded here; an empty table is valid.
        block = section(self.text, "5.", "6.")
        self.assertIn(f"## {DEFECT_HEADING}", self.text)
        self.assertEqual(table_rows(block), [])
        self.assertIn("当前没有登记的已知缺陷", block)
        self.assertEqual(defect_problems(self.text, set()), [])
        good = (f"## {DEFECT_HEADING}\n\n| 缺陷 | Issue | 记录日期 | 备注 |\n|---|---|---|---|\n"
                "| R-9a 甲缺陷 | #9 | 2026-11-28 | 来源 #8 |\n| 乙缺陷 | #10、#11 | 2026-11-29 | 待产品决定 |\n"
                "\n## 6. 维护规则\n")
        rows = [row for header, row in table_rows(section(good, "5.", "6.")) if header == DEFECT_COLUMNS]
        self.assertEqual([row[1] for row in rows], ["#9", "#10、#11"])
        self.assertEqual(defect_problems(good, {"R-1"}), [])
        self.assertIn("defect R-9a is still a main table row", "\n".join(defect_problems(good, {"R-9a"})))
        no_table = f"## {DEFECT_HEADING}\n\n（空）\n\n## 6. 维护规则\n"
        self.assertIn("has no table with header", "\n".join(defect_problems(no_table, set())))
        bad = (f"## {DEFECT_HEADING}\n\n| 缺陷 | Issue | 日期 | 备注 |\n|---|---|---|---|\n| x | #1 | 2026-11-28 | n |\n\n"
               "| 缺陷 | Issue | 记录日期 | 备注 |\n|---|---|---|---|\n"
               "| R-1 甲 | 见 Issue | 2026-11-28 | n |\n| R-2 乙 | #1、#2 | 昨天 | |\n| R-3 丙 | #3 | 2026-11-28 | n |\n"
               "\n## 6. 维护规则\n")
        found = "\n".join(defect_problems(bad, {"R-3"}))
        for needle in ("defect table header", "Issue '见 Issue'", "date '昨天'", "defect R-2 乙: no note",
                       "defect R-3 is still a main table row"):
            self.assertIn(needle, found)
        self.assertNotIn("R-2 乙: Issue", found)


class FakeGateTests(unittest.TestCase):
    """scripts/regression/lib.py refuses a non-fake target offline, before any request."""

    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("regression_lib", ROOT / "scripts" / "regression" / "lib.py")
        cls.lib = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.lib)

    def env(self, **extra):
        base = {"BASE": "http://127.0.0.1:9", "AGENT_TEST_USERNAME": "u", "AGENT_TEST_PASSWORD": "p",
                "ART_AI_UPSTREAM_SHAPE": "fake"}
        base.update(extra)
        return {k: v for k, v in base.items() if v is not None}

    def test_fake_target_runs(self):
        self.assertIsNone(self.lib.fake_refusal([], self.env()))

    def test_refuses_other_shapes_live_flag_and_missing_environment(self):
        for shape in ("live", "mixed", None):
            self.assertIn("not 'fake'", self.lib.fake_refusal([], self.env(ART_AI_UPSTREAM_SHAPE=shape)))
        self.assertIn("--live is not accepted", self.lib.fake_refusal(["--live"], self.env()))
        self.assertIn("missing environment", self.lib.fake_refusal([], self.env(AGENT_TEST_PASSWORD=None)))
        self.assertIn("origin without a path", self.lib.fake_refusal([], self.env(BASE="http://127.0.0.1:9/app")))


if __name__ == "__main__":
    unittest.main()
