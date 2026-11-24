#!/usr/bin/env python3
"""Compare two Agent output-quality reports (#238).

    python3 scripts/smoke/eval-agent-compare.py baseline.json candidate.json [--json out.json] [--new-request-set]

Prints a Markdown comparison and exits 1 when a check regressed or a budget
metric grew. A pass-to-fail check, a missing case, or a higher tool/paid/
interruption count is a regression; more self-inspection is an improvement.
#407 adds the refused tool calls (by code, from the API when visible or the
--refusals-log of the output runner) and the Agent round proxy to that
comparison: more refusals or more tool-using rounds are regressions; the text
question count is recorded without gating.
--new-request-set is the explicit report mode for an intentionally changed
fixed request set: new cases are recorded as additions, the request-set
version/content change no longer blocks, and the summary metrics compare the
common cases only.
--exclude-check-prefix names checks whose baseline verdict is void and must not
count either way, for example `--exclude-check-prefix output:model` after the
#407 baseline inspected Meshy's 3MF delivery as a GLB (#444). A baseline check
whose detail is a known broken-baseline marker (`bad GLB magic`) is listed as
incomparable automatically. The report names every excluded or incomparable
check; neither changes the exit code.
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
    parser.add_argument("--new-request-set", action="store_true",
                        help="report mode for an intentionally changed request set: new cases are additions, not regressions")
    parser.add_argument("--no-fail", action="store_true", help="always exit 0 (report only)")
    parser.add_argument("--exclude-check-prefix", action="append", default=[], metavar="PREFIX",
                        help="checks whose baseline verdict is void (repeatable), for example output:model")
    args = parser.parse_args(argv)
    baseline = quality.load_json(args.baseline)
    candidate = quality.load_json(args.candidate)
    result = quality.compare_reports(baseline, candidate, tolerance=args.tolerance,
                                     new_request_set=args.new_request_set,
                                     exclude_check_prefixes=args.exclude_check_prefix)
    print(result["markdown"], end="")
    if args.json:
        with open(args.json, "w", encoding="utf-8") as handle:
            json.dump({key: value for key, value in result.items() if key != "markdown"}, handle,
                      ensure_ascii=False, indent=2)
    return 0 if args.no_fail or result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())