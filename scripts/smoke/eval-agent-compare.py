#!/usr/bin/env python3
"""Compare two Agent output-quality reports (#238).

    python3 scripts/smoke/eval-agent-compare.py baseline.json candidate.json [--json out.json]

Prints a Markdown comparison and exits 1 when a check regressed or a budget
metric grew. A pass-to-fail check, a missing case, or a higher tool/paid/
interruption count is a regression; more self-inspection is an improvement.
"""
import argparse
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import agent_quality as quality  # noqa: E402


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("baseline")
    parser.add_argument("candidate")
    parser.add_argument("--tolerance", type=int, default=0, help="allowed growth of a lower-is-better metric")
    parser.add_argument("--json", default="", help="write the comparison as JSON here")
    parser.add_argument("--no-fail", action="store_true", help="always exit 0 (report only)")
    args = parser.parse_args(argv)
    baseline = quality.load_json(args.baseline)
    candidate = quality.load_json(args.candidate)
    result = quality.compare_reports(baseline, candidate, tolerance=args.tolerance)
    print(result["markdown"], end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as handle:
            json.dump({key: value for key, value in result.items() if key != "markdown"}, handle,
                      ensure_ascii=False, indent=2)
    return 0 if args.no_fail or result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())