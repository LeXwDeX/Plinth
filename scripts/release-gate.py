#!/usr/bin/env python3
"""Release gate check (#707, #718): the part of docs/release-gate.md that a script can decide.

  release-gate.py --base <tag|sha> --head <sha> [--phase promote|tag] [--matrix PATH] [--rules PATH] [--json]

Reads the local git history (git log / git diff / git show); the only other read is the commit
status "host-ci/gates" of the revisions that unit rows cite as "CI" evidence (gh api, or the JSON
file named by RELEASE_GATE_STATUS_FILE). It never touches an environment. From base..head it lists
the change points (the Issues named by "Issue: #N" trailers, the merged pull requests and the
Issues their commits name, the fake / Provider / Catalog areas touched), then checks:

  1. coverage: every change point has passing rows in the main tables (every table of section
     "3. 主表") of the regression matrix (docs/regression-matrix.md) with evidence, each verified
     at the candidate or at an ancestor with no runtime file changed since, and the matrix itself
     changed in the range; or a valid exemption in docs/release-gate.md. Phase "promote" leaves the
     current verification of the prod-* rows to the "tag" run;
  2. fake: a change to the fake, the Providers or the Catalog Offerings comes with a register
     change or a "无新增差异（核对于 <rev> <date>）" line, and no fake/real difference is OPEN.

The matrix and the rules are read from the head revision unless --matrix / --rules name a file.
The evidence checks (smoke report, #2 receipt, prod regression links) need GitHub and are left to
the release-gate Agent (.claude/agents/release-gate.md). Exit 0 = PASS, 1 = FAIL, 2 = usage error.

RELEASE_GATE_STATUS_FILE: a JSON object mapping a revision (full SHA, or a prefix of at least 8
characters) to the state of its latest host-ci/gates status ("success", "failure", ...); a
revision it does not name has no status. Set, it replaces gh entirely (tests, offline runs).
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
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
MAIN_SECTION = "主表"
MAIN_KEY_COLUMNS = ("ID", "来源 Issue", "状态", "最后验证")
MATRIX_STATES = ("OK", "MANUAL", "GAP", "OBSOLETE")
PASSING_STATES = ("OK", "MANUAL")
PHASES = ("promote", "tag")
# Rows of these layers (prod-readonly, prod-paid; matched case-insensitively) are verified on prod
# after the promotion, so only phase "tag" requires them.
POST_PROMOTION_LAYER = "prod-"
UNIT_LAYER = "unit"
CI_EVIDENCE = "CI"
CI_CONTEXT = "host-ci/gates"
STATUS_FILE_VARIABLE = "RELEASE_GATE_STATUS_FILE"
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
    "晋级前（dev-test 回归）与晋级后（prod 回归，打 tag 前复核）的回归证据链接存在且对应候选 revision",
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


def run_git(repo, *args):
    """(exit code, stdout, stderr) of a git command that may legitimately fail."""
    try:
        result = subprocess.run(["git", "-C", str(repo), *args], capture_output=True, check=False, timeout=120)
    except (OSError, subprocess.TimeoutExpired):
        return 128, "", "git is unavailable or timed out"
    return result.returncode, result.stdout.decode("utf-8"), result.stderr.decode("utf-8", "replace").strip()


def inspect_revisions(repo, head, revisions):
    """For each 8-character revision a row names: its full SHA, whether it is an ancestor of head,
    and the runtime files changed between it and head (#718). Each revision is inspected once."""
    found = {}
    for short in dict.fromkeys(revisions):
        code, out, err = run_git(repo, "rev-parse", "--verify", "--quiet", f"{short}^{{commit}}")
        if code:
            found[short] = {"sha": None, "ancestor": False, "runtime": [],
                            "error": err[:200] or "不存在或前缀不唯一"}
            continue
        sha = out.strip()
        ancestor = run_git(repo, "merge-base", "--is-ancestor", sha, head)[0] == 0
        runtime = []
        if ancestor:
            code, out, err = run_git(repo, "diff", "--no-renames", "--name-only", sha, head)
            if code:
                found[short] = {"sha": sha, "ancestor": False, "runtime": [], "error": err[:200]}
                continue
            runtime = runtime_files(out.split())
        found[short] = {"sha": sha, "ancestor": ancestor, "runtime": runtime, "error": None}
    return found


def repository_slug(url):
    """owner/name of a GitHub remote URL (https or ssh form)."""
    match = re.search(r"github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$", url.strip())
    if not match:
        raise UsageError(f"origin is not a GitHub repository: {url.strip()}")
    return f"{match[1]}/{match[2]}"


def latest_gates_state(statuses):
    """The state of the newest host-ci/gates status; GitHub lists commit statuses newest first."""
    return next((item.get("state") for item in statuses if item.get("context") == CI_CONTEXT), None)


def gh_fetch(repo):
    """A fetch(sha) for ci_states over gh api; an origin gh cannot read makes every call fail."""
    try:
        slug = repository_slug(git(repo, "remote", "get-url", "origin"))
    except UsageError as error:
        problem = str(error)

        def unavailable(sha):
            raise UsageError(problem)
        return unavailable

    def fetch(sha):
        result = subprocess.run(["gh", "api", f"repos/{slug}/commits/{sha}/statuses?per_page=100"],
                                capture_output=True, check=False, timeout=60)
        if result.returncode:
            raise UsageError("gh api 失败：" + result.stderr.decode("utf-8", "replace").strip()[:200])
        return latest_gates_state(json.loads(result.stdout.decode("utf-8")))
    return fetch


def ci_states(shas, env, fetch=None):
    """{full sha: {"state", "error"}} of the host-ci/gates status; each revision is read once.
    The file named by RELEASE_GATE_STATUS_FILE replaces gh; without it and without gh every
    revision gets an error instead of a state."""
    shas = list(dict.fromkeys(shas))
    path = env.get(STATUS_FILE_VARIABLE)
    if path:
        try:
            table = json.loads(Path(path).read_text(encoding="utf-8"))
            if not isinstance(table, dict):
                raise ValueError("not a JSON object")
        except (OSError, ValueError) as error:
            return {sha: {"state": None, "error": f"{STATUS_FILE_VARIABLE} 不可读：{error}"} for sha in shas}
        return {sha: {"state": next((state for key, state in table.items()
                                     if len(key) >= 8 and sha.startswith(key)), None), "error": None}
                for sha in shas}
    if fetch is None and shutil.which("gh") is None:
        return {sha: {"state": None, "error": f"gh 不可用，读不到 {CI_CONTEXT}（可用 {STATUS_FILE_VARIABLE} 注入）"}
                for sha in shas}
    states = {}
    for sha in shas:
        try:
            states[sha] = {"state": fetch(sha), "error": None}
        except (UsageError, OSError, ValueError, subprocess.TimeoutExpired) as error:
            states[sha] = {"state": None, "error": str(error)}
    return states


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
    # The main section is split into one table per area, all with the same header; read every one.
    main = [found for header, found in tables(section_lines(text, MAIN_SECTION) or [])
            if all(column in header for column in MAIN_KEY_COLUMNS)]
    rows = []
    for row in (row for found in main for row in found):
        last = re.fullmatch(r"([0-9a-f]{8})\s+(" + DATE + r")", clean(row["最后验证"]))
        evidence = row.get("证据", "")
        # "来源 Issue" may separate numbers with "," or "、"; every #N counts.
        rows.append({"id": clean(row["ID"]), "issues": sorted({int(n) for n in re.findall(r"#(\d+)", row["来源 Issue"])}),
                     "state": clean(row["状态"]), "last": (last[1], last[2]) if last else None,
                     "last_raw": clean(row["最后验证"]), "evidence": "" if empty(evidence) else clean(evidence),
                     "layer": clean(row.get("验证层", "")), "command": row.get("命令或脚本", "").strip()})
    diff_lines = section_lines(text, DIFF_SECTION)
    diff_rows, markers = [], []
    if diff_lines is not None:
        markers = [(match[1], match[2]) for match in NO_NEW_DIFF.finditer("\n".join(diff_lines))]
        table = next((rows for header, rows in tables(diff_lines) if all(c in header for c in DIFF_COLUMNS)), [])
        diff_rows = [{"difference": clean(row["差异"]), "state": clean(row["状态"]), "note": clean(row["备注"])}
                     for row in table if not NO_NEW_DIFF.search(row["差异"])]
    return {"main_found": bool(main), "rows": rows, "diff_section": diff_lines is not None,
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
    """The runtime files among files: the rule of the exemption list and of row staleness (#718)."""
    return sorted(path for path in files
                  if not path.startswith(NON_RUNTIME_PREFIXES) and not (path.endswith(".md") and "/" not in path))


def revision_refs(matrix, head):
    """The 8-character revisions the main rows name other than the candidate itself."""
    return sorted({row["last"][0] for row in matrix["rows"] if row["last"] and row["last"][0] != head[:8]})


def post_promotion(row):
    return row["layer"].lower().startswith(POST_PROMOTION_LAYER)


def cites_ci(row):
    return row["layer"] == UNIT_LAYER and row["evidence"] == CI_EVIDENCE


def ci_revisions(matrix, head, revisions):
    """Full SHAs whose host-ci/gates status the unit rows with "CI" evidence rely on."""
    found = []
    for row in matrix["rows"]:
        if row["state"] == "OBSOLETE" or not cites_ci(row) or not row["last"]:
            continue
        sha = head if row["last"][0] == head[:8] else (revisions.get(row["last"][0]) or {}).get("sha")
        if sha and sha not in found:
            found.append(sha)
    return found


def stale(row, head, revisions):
    """Why a row's "最后验证" does not count for the candidate, or None when it does (#718)."""
    revision, head8 = row["last"][0], head[:8]
    if revision == head8:
        return None
    info = revisions.get(revision)
    if not info or not info["sha"]:
        return (f"{row['id']} 最后验证于 {revision}，无法解析为本仓库的提交："
                f"{(info or {}).get('error') or '未核对'}")
    if info.get("error"):
        return f"{row['id']} 最后验证于 {revision}，无法核对与候选 {head8} 的差异：{info['error']}"
    if not info["ancestor"]:
        return f"{row['id']} 最后验证于 {revision}，不是候选 {head8} 的祖先"
    if info["runtime"]:
        return (f"{row['id']} 最后验证于 {revision}，此后到候选 {head8} 改了运行产物："
                + ", ".join(info["runtime"][:5]) + (" 等" if len(info["runtime"]) > 5 else ""))
    return None


def ci_problem(row, head, revisions, ci):
    revision = row["last"][0]
    sha = head if revision == head[:8] else (revisions.get(revision) or {}).get("sha")
    if not sha:
        return None  # the revision problem is already reported
    status = ci.get(sha)
    prefix = f"{row['id']} 的证据是 CI，但 {revision} 的 {CI_CONTEXT} "
    if status is None:
        return prefix + "未查询"
    if status["error"]:
        return prefix + "读取失败：" + status["error"]
    if status["state"] is None:
        return f"{row['id']} 的证据是 CI，但 {revision} 没有 {CI_CONTEXT} 状态"
    if status["state"] != "success":
        return f"{row['id']} 的证据是 CI，但 {revision} 的 {CI_CONTEXT} 是 {status['state']}，不是 success"
    return None


def evaluate(changes, matrix_text, rules_text, matrix_source=MATRIX_PATH, phase="tag", revisions=None, ci=None):
    """The gate decision from plain data: changes (see collect), the matrix and rules texts or None,
    the phase, what inspect_revisions found for the rows' revisions and the CI states (ci_states)."""
    if phase not in PHASES:
        raise UsageError(f"phase must be one of {', '.join(PHASES)}")
    revisions, ci = revisions or {}, ci or {}
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
        reasons.append(f"回归表没有主表（「{MAIN_SECTION}」一节下表头含 {'｜'.join(MAIN_KEY_COLUMNS)} 的表）")
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
            coverage.append({"issue": number, "result": "exempt", "rows": [], "deferred": [], "problems": []})
            continue
        problems, ids, deferred = [], [], []
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
                if phase == "promote" and post_promotion(row):
                    deferred.append(row["id"])  # verified on prod after the promotion
                    continue
                if row["last"] is None:
                    problems.append(f"{row['id']} 的「最后验证」不是「<revision 前 8 位> <日期>」：{row['last_raw'] or '空'}")
                else:
                    problem = stale(row, changes["head"], revisions)
                    if problem:
                        problems.append(problem)
                    elif cites_ci(row):
                        problem = ci_problem(row, changes["head"], revisions, ci)
                        if problem:
                            problems.append(problem)
                if not row["evidence"]:
                    problems.append(f"{row['id']} 的「证据」为空")
            if matrix["main_found"]:
                for problem in problems:
                    reasons.append(f"#{number}：{problem}")
        coverage.append({"issue": number, "result": "fail" if problems else "ok", "rows": ids, "deferred": deferred,
                         "problems": problems})

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

    return {"release_gate_check": 1, "status": "fail" if reasons else "pass", "phase": phase, "base": changes["base"],
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
    lines = [f"发布门禁检查 base={result['base']} ({result['base_revision'][:8]}) head={result['head']} "
             f"phase={result['phase']}",
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
        deferred = f"；晋级后验证 {', '.join(item['deferred'])}" if item.get("deferred") else ""
        lines.append(f"  #{item['issue']}  {item['result'].upper()}{rows}{deferred}")
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
    parser.add_argument("--phase", choices=PHASES, default="tag",
                        help="promote: before prod, prod-* rows verified later; tag: everything (default)")
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
    revisions, ci = {}, {}
    if matrix_text is not None:
        matrix = parse_matrix(matrix_text)
        revisions = inspect_revisions(args.repo, changes["head"], revision_refs(matrix, changes["head"]))
        shas = ci_revisions(matrix, changes["head"], revisions)
        if shas:
            fetch = gh_fetch(args.repo) if not os.environ.get(STATUS_FILE_VARIABLE) and shutil.which("gh") else None
            ci = ci_states(shas, os.environ, fetch)
    result = evaluate(changes, matrix_text, rules_text, source, args.phase, revisions, ci)
    print(json.dumps(result, ensure_ascii=False, indent=2) if args.json else render(result))
    return 0 if result["status"] == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
