#!/usr/bin/env python3
"""Release gate check (#707): the part of docs/release-gate.md that git alone can decide.

  release-gate.py --base <tag|sha> --head <sha> [--matrix PATH] [--rules PATH] [--json]

Reads only the local git history (git log / git diff / git show); it never touches the network,
GitHub or an environment. From base..head it lists the change points (the Issues named by
"Issue: #N" trailers, the merged pull requests and the Issues their commits name, the fake /
Provider / Catalog areas touched), then checks:

  1. coverage: every change point has passing rows in the main table of the regression matrix
     (docs/regression-matrix.md), verified at the candidate revision with evidence, and the matrix
     itself changed in the range; or a valid exemption in docs/release-gate.md;
  2. fake: a change to the fake, the Providers or the Catalog Offerings comes with a register
     change or a "无新增差异（核对于 <rev> <date>）" line, and no fake/real difference is OPEN.

The matrix and the rules are read from the head revision unless --matrix / --rules name a file.
The evidence checks (smoke report, #2 receipt, PROD regression links) need GitHub and are left to
the release-gate Agent (.claude/agents/release-gate.md). Exit 0 = PASS, 1 = FAIL, 2 = usage error.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

MATRIX_PATH = "docs/regression-matrix.md"
RULES_PATH = "docs/release-gate.md"
REGISTER_PATHS = ("docs/fake-register.md", "internal/testutil/fakeupstream/register/register.json")
# A change under one of these needs the fake register (or an explicit "no new difference" line).
FAKE_AREAS = (("fake", "internal/testutil/fakeupstream/"), ("providers", "internal/providers/"),
              ("catalog", "internal/catalog/default_catalog.json"))
# Only an Issue whose files all sit here (docs, scripts, CI, agent rules) may be exempted.
NON_RUNTIME_PREFIXES = ("docs/", "scripts/", ".github/", ".claude/")
MATRIX_COLUMNS = ("ID", "功能 / 行为", "来源 Issue", "验证层", "命令或脚本", "期望", "状态", "最后验证", "证据")
MATRIX_STATES = ("OK", "MANUAL", "GAP", "OBSOLETE")
PASSING_STATES = ("OK", "MANUAL")
DIFF_SECTION = "Fake 与真实的已知差异"
DIFF_COLUMNS = ("差异", "状态", "Issue", "记录日期", "备注")
DIFF_STATES = ("OPEN", "FIXED", "ACCEPTED")
EXEMPTION_SECTION = "豁免清单"
EXEMPTION_COLUMNS = ("Issue", "理由", "用户同意日期")
CHANGELOG_SECTION = "变更记录"
EMPTY = ("", "—", "-", "–", "无", "N/A", "n/a")
DATE = r"\d{4}-\d{2}-\d{2}"
NO_NEW_DIFF = re.compile(r"无新增差异[（(]\s*核对于\s*`?([0-9a-f]{8})`?\s+(" + DATE + r")\s*[）)]")
AGENT_CHECKS = (
    "候选 revision 在 dev-test 的机器冒烟报告 L0/L1/L1_agent 三层 pass（0 skip、0 soft-fail）",
    "#2 里有该候选 revision 的 dev-test 验收证据评论（L0/L1/L1_agent/related），链接真实指向该 revision",
    "晋级前（dev-test 回归）与晋级后（formal-test 回归，打 tag 前复核）的回归证据链接存在且对应候选 revision",
)


class UsageError(Exception):
    pass


# ---------------------------------------------------------------- git (the only side effects)

def git(repo, *args):
    try:
        result = subprocess.run(["git", "-C", str(repo), *args], capture_output=True, check=False, timeout=120)
    except (OSError, subprocess.TimeoutExpired):
        raise UsageError("git is unavailable or timed out") from None
    if result.returncode:
        raise UsageError(f"git {args[0]} failed: {result.stderr.decode('utf-8', 'replace').strip()[:200]}")
    return result.stdout.decode("utf-8")


def resolve(repo, ref):
    return git(repo, "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}").strip()


def read_at(repo, revision, path):
    """The file at a revision, or None when it does not exist there."""
    result = subprocess.run(["git", "-C", str(repo), "show", f"{revision}:{path}"], capture_output=True, check=False)
    return result.stdout.decode("utf-8") if result.returncode == 0 else None


def collect(repo, base, head):
    """Everything the checks need from git, as plain data (see evaluate)."""
    base_revision, head_revision = resolve(repo, base), resolve(repo, head)
    try:
        git(repo, "merge-base", "--is-ancestor", base_revision, head_revision)
    except UsageError:
        raise UsageError(f"{base} is not an ancestor of {head}") from None
    log = git(repo, "log", "--no-renames", "--name-only", "--format=%x1e%H%x1f%P%x1f%B%x1f",
              f"{base_revision}..{head_revision}")
    commits = []
    for chunk in log.split("\x1e")[1:]:
        sha, parents, body, files = chunk.split("\x1f", 3)
        commits.append({"sha": sha, "parents": parents.split(), "message": body.strip(),
                        "files": [line for line in files.splitlines() if line.strip()]})
    pr_commits = {}
    for commit in commits:
        number = pr_number(commit["message"])
        if number is not None and len(commit["parents"]) == 2:
            first, second = commit["parents"]
            pr_commits[number] = git(repo, "rev-list", f"{first}..{second}").split()
    files = git(repo, "diff", "--no-renames", "--name-only", base_revision, head_revision).split()
    return {"base": base, "base_revision": base_revision, "head": head_revision, "commits": commits,
            "pr_commits": pr_commits, "files": files}


# ---------------------------------------------------------------- pure parsing

def pr_number(message):
    match = re.match(r"Merge pull request #(\d+) ", message)
    return int(match[1]) if match else None


def trailer_issues(message):
    issues = set()
    for line in re.findall(r"(?mi)^Issues?:\s*(.+)$", message):
        issues.update(int(number) for number in re.findall(r"#(\d+)", line))
    return issues


def clean(cell):
    cell = cell.strip()
    for mark in ("**", "`"):
        if len(cell) >= 2 * len(mark) and cell.startswith(mark) and cell.endswith(mark):
            cell = cell[len(mark):-len(mark)].strip()
    return cell


def split_row(line):
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|") and not line.endswith("\\|"):
        line = line[:-1]
    return [cell.replace("\\|", "|").strip() for cell in re.split(r"(?<!\\)\|", line)]


def sections(text):
    """[(heading, level, lines)] with the text before the first heading under heading ""."""
    result = [("", 0, [])]
    fence = False
    for line in text.splitlines():
        if line.lstrip().startswith("```"):
            fence = not fence
        heading = None if fence else re.match(r"(#{1,6})\s+(.*?)\s*#*\s*$", line)
        if heading:
            result.append((heading[2], len(heading[1]), []))
        else:
            result[-1][2].append(line)
    return result


def tables(lines):
    """Markdown tables in a list of lines: [(header, [row dict])]."""
    found, index = [], 0
    while index + 1 < len(lines):
        if lines[index].strip().startswith("|") and re.fullmatch(r"\s*\|?[\s:|-]+\|?\s*", lines[index + 1]) \
                and "-" in lines[index + 1]:
            header = [clean(cell) for cell in split_row(lines[index])]
            rows, index = [], index + 2
            while index < len(lines) and lines[index].strip().startswith("|"):
                cells = split_row(lines[index])
                rows.append({name: (cells[position] if position < len(cells) else "")
                             for position, name in enumerate(header)})
                index += 1
            found.append((header, rows))
        else:
            index += 1
    return found


def section_lines(text, title):
    """Lines of the first section whose heading contains title, including its subsections."""
    parts = sections(text)
    for position, (heading, level, lines) in enumerate(parts):
        if title in heading:
            collected = list(lines)
            for _, deeper, more in parts[position + 1:]:
                if deeper <= level:
                    break
                collected.extend(more)
            return collected
    return None


def empty(cell):
    return clean(cell) in EMPTY


def parse_matrix(text):
    main = next((rows for header, rows in tables(text.splitlines())
                 if all(column in header for column in MATRIX_COLUMNS)), None)
    rows = []
    for row in main or []:
        last = re.fullmatch(r"([0-9a-f]{8})\s+(" + DATE + r")", clean(row["最后验证"]))
        rows.append({"id": clean(row["ID"]), "issues": sorted({int(n) for n in re.findall(r"#(\d+)", row["来源 Issue"])}),
                     "state": clean(row["状态"]), "last": (last[1], last[2]) if last else None,
                     "last_raw": clean(row["最后验证"]), "evidence": "" if empty(row["证据"]) else clean(row["证据"])})
    diff_lines = section_lines(text, DIFF_SECTION)
    diff_rows, markers = [], []
    if diff_lines is not None:
        markers = [(match[1], match[2]) for match in NO_NEW_DIFF.finditer("\n".join(diff_lines))]
        table = next((rows for header, rows in tables(diff_lines) if all(c in header for c in DIFF_COLUMNS)), [])
        diff_rows = [{"difference": clean(row["差异"]), "state": clean(row["状态"]), "note": clean(row["备注"])}
                     for row in table if not NO_NEW_DIFF.search(row["差异"])]
    return {"main_found": main is not None, "rows": rows, "diff_section": diff_lines is not None,
            "diff_rows": diff_rows, "no_new_diff": markers}


def parse_rules(text):
    exemptions = []
    lines = section_lines(text, EXEMPTION_SECTION) or []
    table = next((rows for header, rows in tables(lines) if all(c in header for c in EXEMPTION_COLUMNS)), [])
    for row in table:
        issues = re.findall(r"#(\d+)", row["Issue"])
        agreed = re.fullmatch(DATE, clean(row["用户同意日期"]))
        exemptions.append({"issue": int(issues[0]) if len(issues) == 1 else None, "raw": clean(row["Issue"]),
                           "reason": "" if empty(row["理由"]) else clean(row["理由"]),
                           "agreed": agreed[0] if agreed else None})
    changelog = section_lines(text, CHANGELOG_SECTION) or []
    entries = next((rows for header, rows in tables(changelog)), [])
    return {"exemptions": exemptions, "rules_version": len(entries)}


# ---------------------------------------------------------------- pure decisions

def change_points(changes):
    """Issues (and Issue-less pull requests) changed in the range, with where they came from."""
    by_sha = {commit["sha"]: commit for commit in changes["commits"]}
    points, prs, untracked = {}, {}, []

    def point(number, kind="issue"):
        return points.setdefault(number, {"kind": kind, "commits": [], "prs": [], "files": set()})

    for commit in changes["commits"]:
        if len(commit["parents"]) > 1:
            continue
        issues = trailer_issues(commit["message"])
        if not issues:
            untracked.append(commit)
        for number in issues:
            entry = point(number)
            entry["commits"].append(commit["sha"][:8])
            entry["files"].update(commit["files"])
    for commit in changes["commits"]:
        number = pr_number(commit["message"])
        if number is None:
            continue
        linked = set(int(n) for n in re.findall(r"(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)",
                                                  commit["message"]))
        for sha in changes["pr_commits"].get(number, []):
            if sha in by_sha and len(by_sha[sha]["parents"]) < 2:
                linked.update(trailer_issues(by_sha[sha]["message"]))
        prs[number] = sorted(linked)
        if not linked:
            point(number, "pr")["commits"].append(commit["sha"][:8])
        for issue in linked:
            entry = point(issue)
            if number not in entry["prs"]:
                entry["prs"].append(number)
    return points, prs, untracked


def changed_areas(files):
    return [name for name, prefix in FAKE_AREAS if any(path.startswith(prefix) for path in files)]


def runtime_files(files):
    return sorted(path for path in files
                  if not path.startswith(NON_RUNTIME_PREFIXES) and not (path.endswith(".md") and "/" not in path))


def evaluate(changes, matrix_text, rules_text, matrix_source=MATRIX_PATH):
    """The gate decision from plain data: changes (see collect), the matrix and rules texts or None."""
    head8 = changes["head"][:8]
    reasons = []
    points, prs, untracked = change_points(changes)
    for commit in untracked:
        reasons.append(f"提交 {commit['sha'][:8]} 没有 `Issue: #N` 引用：{commit['message'].splitlines()[0]}")
    matrix = parse_matrix(matrix_text) if matrix_text is not None else None
    rules = parse_rules(rules_text) if rules_text is not None else {"exemptions": [], "rules_version": 0}
    if rules_text is None:
        reasons.append(f"门禁规则文档缺失：{RULES_PATH}")
    elif rules["rules_version"] < 1:
        reasons.append(f"{RULES_PATH} 的「{CHANGELOG_SECTION}」没有记录")
    if matrix is None:
        reasons.append(f"回归表缺失：{matrix_source}")
    elif not matrix["main_found"]:
        reasons.append(f"回归表没有主表（列：{'｜'.join(MATRIX_COLUMNS)}）")
    if MATRIX_PATH not in changes["files"]:
        reasons.append(f"回归表 {MATRIX_PATH} 在 {changes['base']}..{head8} 范围内没有被修改")

    exemptions = {"used": [], "unused": [], "invalid": []}
    valid = {}
    for entry in rules["exemptions"]:
        problems = []
        if entry["issue"] is None:
            problems.append("Issue 必须恰好写一个 #N")
        if not entry["reason"]:
            problems.append("没有理由")
        if not entry["agreed"]:
            problems.append("没有用户在聊天里明确同意的日期")
        if entry["issue"] in points and not problems:
            touched = runtime_files(points[entry["issue"]]["files"])
            if touched:
                problems.append("该 Issue 改了运行产物，不能豁免：" + ", ".join(touched[:5]))
        if problems:
            exemptions["invalid"].append({"issue": entry["raw"], "problems": problems})
        elif entry["issue"] in points:
            valid[entry["issue"]] = entry
            exemptions["used"].append({"issue": entry["issue"], "reason": entry["reason"], "agreed": entry["agreed"]})
        else:
            exemptions["unused"].append({"issue": entry["issue"]})

    coverage = []
    for number in sorted(points):
        if number in valid:
            coverage.append({"issue": number, "result": "exempt", "rows": [], "problems": []})
            continue
        problems, ids = [], []
        if matrix is None or not matrix["main_found"]:
            problems.append("回归表不可用，无法核对")
        else:
            rows = [row for row in matrix["rows"] if number in row["issues"] and row["state"] != "OBSOLETE"]
            ids = [row["id"] for row in rows]
            if not rows:
                problems.append("回归表主表没有「来源 Issue」含它的有效行")
            for row in rows:
                if row["state"] not in PASSING_STATES:
                    problems.append(f"{row['id']} 状态是 {row['state'] or '空'}，不是 OK/MANUAL")
                if row["last"] is None:
                    problems.append(f"{row['id']} 的「最后验证」不是「<revision 前 8 位> <日期>」：{row['last_raw'] or '空'}")
                elif row["last"][0] != head8:
                    problems.append(f"{row['id']} 最后验证于 {row['last'][0]}，不是候选 {head8}")
                if not row["evidence"]:
                    problems.append(f"{row['id']} 的「证据」为空")
            if matrix["main_found"]:
                for problem in problems:
                    reasons.append(f"#{number}：{problem}")
        coverage.append({"issue": number, "result": "fail" if problems else "ok", "rows": ids, "problems": problems})

    areas = changed_areas(changes["files"])
    register = sorted(path for path in REGISTER_PATHS if path in changes["files"])
    fake = {"areas": areas, "register_changed": register, "no_new_diff": [], "problems": []}
    if matrix is not None:
        fake["no_new_diff"] = [f"{rev} {date}" for rev, date in matrix["no_new_diff"]]
        if not matrix["diff_section"]:
            fake["problems"].append(f"回归表缺少「{DIFF_SECTION}」一节")
        for row in matrix["diff_rows"]:
            if row["state"] == "OPEN":
                fake["problems"].append(f"已知差异仍是 OPEN：{row['difference']}")
            elif row["state"] == "ACCEPTED" and not re.search(DATE, row["note"]):
                fake["problems"].append(f"ACCEPTED 差异的备注没有用户明确接受的日期：{row['difference']}")
            elif row["state"] not in DIFF_STATES:
                fake["problems"].append(f"已知差异的状态 {row['state'] or '空'} 不是 OPEN/FIXED/ACCEPTED：{row['difference']}")
    if areas and not register:
        current = matrix is not None and any(rev == head8 for rev, _ in matrix["no_new_diff"])
        if not current:
            fake["problems"].append(f"改动了 {', '.join(areas)}，但 fake 登记表没有修改，回归表也没有"
                                    f"「无新增差异（核对于 {head8} <日期>）」一行")
    reasons.extend("fake：" + problem for problem in fake["problems"])

    return {"release_gate_check": 1, "status": "fail" if reasons else "pass", "base": changes["base"],
            "base_revision": changes["base_revision"], "head": changes["head"],
            "rules_version": rules["rules_version"], "matrix": matrix_source,
            "changed_issues": sorted(points),
            "change_points": [{"number": n, "kind": p["kind"], "commits": p["commits"], "prs": sorted(p["prs"])}
                              for n, p in sorted(points.items())],
            "pull_requests": [{"pr": n, "issues": issues} for n, issues in sorted(prs.items())],
            "untracked_commits": [commit["sha"][:8] for commit in untracked],
            "areas": areas, "coverage": coverage, "fake": fake, "exemptions": exemptions,
            "agent_checks": list(AGENT_CHECKS), "reasons": reasons}


def render(result):
    lines = [f"发布门禁检查 base={result['base']} ({result['base_revision'][:8]}) head={result['head']}",
             f"规则版本：{result['rules_version']}（{RULES_PATH}「{CHANGELOG_SECTION}」条数）", "修改点："]
    for point in result["change_points"]:
        source = f"提交 {', '.join(point['commits'])}" if point["commits"] else "无直接提交"
        if point["prs"]:
            source += f"；PR {', '.join('#' + str(n) for n in point['prs'])}"
        lines.append(f"  #{point['number']}{'（无关联 Issue 的 PR）' if point['kind'] == 'pr' else ''}  {source}")
    lines.append(f"fake/Provider/Catalog 改动区域：{', '.join(result['areas']) or '无'}")
    lines.append(f"回归表：{result['matrix']}")
    for item in result["coverage"]:
        rows = f"（{', '.join(item['rows'])}）" if item["rows"] else ""
        lines.append(f"  #{item['issue']}  {item['result'].upper()}{rows}")
        lines.extend(f"      - {problem}" for problem in item["problems"])
    fake = result["fake"]
    lines.append(f"fake 检查：登记表改动 {', '.join(fake['register_changed']) or '无'}；"
                 f"无新增差异核对行 {', '.join(fake['no_new_diff']) or '无'}")
    lines.extend(f"  - {problem}" for problem in fake["problems"])
    exemptions = result["exemptions"]
    lines.append("豁免：已用 " + (", ".join(f"#{e['issue']}（{e['agreed']}）" for e in exemptions["used"]) or "无")
                 + "；无效 " + (", ".join(f"{e['issue']}：{'；'.join(e['problems'])}" for e in exemptions["invalid"]) or "无")
                 + "；未用 " + (", ".join(f"#{e['issue']}" for e in exemptions["unused"]) or "无"))
    lines.append("需门禁 Agent 核对（脚本不判断）：")
    lines.extend(f"  - {check}" for check in result["agent_checks"])
    lines.append(f"结论：{result['status'].upper()}")
    if result["reasons"]:
        lines.append("原因：")
        lines.extend(f"  - {reason}" for reason in result["reasons"])
    return "\n".join(lines)


def main(argv=None):
    parser = argparse.ArgumentParser(description="Release gate check over git base..head (#707).")
    parser.add_argument("--base", required=True, help="the previous release tag or revision")
    parser.add_argument("--head", required=True, help="the candidate revision")
    parser.add_argument("--matrix", help=f"a regression matrix file (default: {MATRIX_PATH} at --head)")
    parser.add_argument("--rules", help=f"a gate rules file (default: {RULES_PATH} at --head)")
    parser.add_argument("--repo", default=".", help="the git repository (default: current directory)")
    parser.add_argument("--json", action="store_true", help="print the result as JSON")
    args = parser.parse_args(argv)
    try:
        changes = collect(args.repo, args.base, args.head)
    except UsageError as error:
        print(f"[release-gate] {error}", file=sys.stderr)
        return 2
    head8 = changes["head"][:8]
    if args.matrix:
        path = Path(args.matrix)
        matrix_text, source = (path.read_text(encoding="utf-8") if path.is_file() else None), str(path)
    else:
        matrix_text, source = read_at(args.repo, changes["head"], MATRIX_PATH), f"{MATRIX_PATH}@{head8}"
    if args.rules:
        path = Path(args.rules)
        rules_text = path.read_text(encoding="utf-8") if path.is_file() else None
    else:
        rules_text = read_at(args.repo, changes["head"], RULES_PATH)
    result = evaluate(changes, matrix_text, rules_text, source)
    print(json.dumps(result, ensure_ascii=False, indent=2) if args.json else render(result))
    return 0 if result["status"] == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
