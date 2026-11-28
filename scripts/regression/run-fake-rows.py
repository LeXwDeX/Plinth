#!/usr/bin/env python3
"""Run the dev-test fake rows of the regression matrix one after another (#718).

  run-fake-rows.py [--dry-run] [--only ID,...] [--matrix PATH]

Reads the main tables of docs/regression-matrix.md (the parser of scripts/release-gate.py) and
picks the rows whose layer is dev-test-fake, whose state is OK or MANUAL, and whose
"命令或脚本" cell is exactly one command in backticks starting with python3, bash or node
(optionally after NAME=value assignments, which go into the environment). Each one runs as
"python3 scripts/release-env.py smoke dev-test -- <command>", like the README examples: no
--live, no ART_AI_LIVE_ACK, so a fake-only script still refuses a target that is not fake.

Prints "PASS|FAIL <ID> <seconds>" per row and a "=== run-fake-rows: PASS=n FAIL=n ===" summary,
headed by the local HEAD revision and the UTC date for the evidence comment. Rows sharing one
command run once. It never writes the matrix back. Exit 0 = all passed, 1 = a row failed,
2 = usage error (unknown --only ID, unreadable matrix).
"""
import argparse
import datetime
import importlib.util
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
MATRIX = "docs/regression-matrix.md"
LAYER = "dev-test-fake"
STATES = ("OK", "MANUAL")
PROGRAMS = ("python3", "bash", "node")
ENVIRONMENT = "dev-test"
ASSIGNMENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=.*", re.S)

_spec = importlib.util.spec_from_file_location("release_gate", ROOT / "scripts" / "release-gate.py")
gate = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gate)


class UsageError(Exception):
    pass


def single_command(cell):
    """({NAME: value}, argv) when the cell is one backticked python3/bash/node command, else None."""
    match = re.fullmatch(r"\s*`([^`]+)`\s*", cell or "")
    if not match:
        return None
    try:
        words = shlex.split(match[1])
    except ValueError:
        return None
    env = {}
    while words and ASSIGNMENT.fullmatch(words[0]):
        name, value = words.pop(0).split("=", 1)
        env[name] = value
    if not words or words[0] not in PROGRAMS or len(words) < 2:
        return None
    return env, words


def select(text, only=None):
    """(rows to run, dev-test-fake rows left out with the reason), in matrix order."""
    selected, skipped = [], []
    for row in gate.parse_matrix(text)["rows"]:
        if row["layer"] != LAYER:
            continue
        command = single_command(row["command"])
        if row["state"] not in STATES:
            skipped.append({"id": row["id"], "reason": f"状态 {row['state'] or '空'}"})
        elif command is None:
            skipped.append({"id": row["id"], "reason": "不是单条 python3/bash/node 命令"})
        else:
            selected.append({"id": row["id"], "env": command[0], "argv": command[1]})
    if only:
        known = {item["id"] for item in selected}
        unknown = [rid for rid in only if rid not in known]
        if unknown:
            raise UsageError("--only 里的行不在可运行的 dev-test-fake 行中：" + ", ".join(unknown))
        selected = [item for item in selected if item["id"] in only]
    return selected, skipped


def wrap(argv):
    return ["python3", "scripts/release-env.py", "smoke", ENVIRONMENT, "--", *argv]


def shown(item):
    assignments = " ".join(f"{name}={shlex.quote(value)}" for name, value in item["env"].items())
    return (assignments + " " if assignments else "") + shlex.join(wrap(item["argv"]))


def subprocess_run(command, extra):
    return subprocess.run(command, cwd=ROOT, env={**os.environ, **extra}, check=False).returncode


def head_revision():
    result = subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"], capture_output=True, check=False)
    return result.stdout.decode().strip() if result.returncode == 0 else "unknown"


def utc_today():
    return datetime.datetime.now(datetime.timezone.utc).date().isoformat()


def main(argv=None, matrix_text=None, run=subprocess_run, revision=head_revision, today=utc_today,
         clock=time.monotonic):
    parser = argparse.ArgumentParser(description="Run the dev-test fake rows of the regression matrix (#718).")
    parser.add_argument("--dry-run", action="store_true", help="list the rows and commands, run nothing")
    parser.add_argument("--only", help="comma-separated row IDs to run")
    parser.add_argument("--matrix", default=str(ROOT / MATRIX), help=f"the matrix file (default: {MATRIX})")
    args = parser.parse_args(argv)
    only = [rid.strip() for rid in args.only.split(",") if rid.strip()] if args.only else None
    try:
        text = matrix_text if matrix_text is not None else Path(args.matrix).read_text(encoding="utf-8")
        selected, skipped = select(text, only)
    except (OSError, UsageError) as error:
        print(f"[run-fake-rows] {error}", file=sys.stderr)
        return 2
    print(f"run-fake-rows revision={revision()} date={today()} environment={ENVIRONMENT}"
          f"{' (dry-run)' if args.dry_run else ''}")
    if args.dry_run:
        for item in selected:
            print(f"{item['id']}  {shown(item)}")
        for item in skipped:
            print(f"跳过 {item['id']}：{item['reason']}")
        return 0
    results, done = [], {}
    for item in selected:
        key = (tuple(sorted(item["env"].items())), tuple(item["argv"]))
        if key in done:
            first, passed, seconds = done[key]
            note = f"（同 {first}，只运行一次）"
        else:
            print(f"--- {item['id']}: {shown(item)}", flush=True)
            started = clock()
            passed = run(wrap(item["argv"]), item["env"]) == 0
            seconds = clock() - started
            done[key] = (item["id"], passed, seconds)
            note = ""
        results.append((item["id"], passed))
        print(f"{'PASS' if passed else 'FAIL'} {item['id']} {seconds:.1f}s{note}", flush=True)
    failed = [rid for rid, passed in results if not passed]
    print(f"=== run-fake-rows: PASS={len(results) - len(failed)} FAIL={len(failed)} ===")
    if failed:
        print("失败：" + ", ".join(failed))
    if skipped:
        print("未运行（不是可运行的 dev-test-fake 行）：" + ", ".join(item["id"] for item in skipped))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
