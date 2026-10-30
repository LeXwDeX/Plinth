#!/usr/bin/env python3
"""Promotion rules shared by release-env.py: which source revision may reach a promoted environment.

An environment with "promote_from" (formal-test) only receives an exact origin/main revision that
its source environment (dev-test) released successfully AND that has a passing acceptance report
in a native GitHub Issue comment. This module holds the pure checks; release-env.py drives them.

The acceptance comment must contain exactly one JSON fenced report with "formal_promotion": 1,
the full revision, the source image tag and four passing checks (L0, L1, L1_agent, related), each
with evidence under the project URL. Skipped or soft-failed checks never count.

The legacy-formal stack that used to be promoted by release-formal.py was retired on 2026-10-30
(#74); only these shared checks remain.
"""
import json
import re
import subprocess


class ReleaseError(Exception):
    """Only fixed, credential-free diagnostics belong in this exception."""


def validate_revision(revision):
    if not isinstance(revision, str) or not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ReleaseError("an exact lowercase 40-character revision is required")
    return revision


def validate_tag(tag):
    if not isinstance(tag, str) or not re.fullmatch(r"[0-9a-f]{7,40}-[0-9]{8}T[0-9]{6}Z", tag):
        raise ReleaseError("invalid test image tag")
    return tag


def run(*args, input=None, env=None, timeout=120):
    try:
        result = subprocess.run([str(arg) for arg in args], input=input, capture_output=True,
                                env=env, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise ReleaseError("a release command was unavailable or timed out") from None
    if result.returncode:
        # Never print command output, argv, env, or credentials.
        raise ReleaseError("release command failed: " + str(args[0]).split("/")[-1])
    return result.stdout.decode("utf-8")


def read_acceptance_comment(repo, issue, comment_id):
    """Read one GitHub Issue comment; a comment that belongs to another Issue is refused."""
    comment = json.loads(run("gh", "api", f"repos/{repo}/issues/comments/{comment_id}"))
    if not str(comment.get("issue_url", "")).lower().endswith(f"/repos/{repo}/issues/{issue}".lower()):
        raise ReleaseError("the acceptance comment belongs to another Issue")
    return comment


def acceptance_from_note(body, revision, forge):
    validate_revision(revision)
    if not forge:
        raise ReleaseError("the project forge URL is not configured")
    candidates = []
    for block in re.findall(r"```json\s*\n(.*?)\n```", body, re.S):
        try:
            report = json.loads(block)
        except ValueError:
            raise ReleaseError("native acceptance JSON is invalid") from None
        if isinstance(report, dict) and "formal_promotion" in report:
            candidates.append(report)
    if len(candidates) != 1:
        raise ReleaseError("one native formal_promotion acceptance report is required")
    report = candidates[0]
    if type(report.get("formal_promotion")) is not int or report["formal_promotion"] != 1 or report.get("revision") != revision:
        raise ReleaseError("acceptance revision does not match the promotion")
    validate_tag(report.get("image_tag"))
    checks = report.get("checks", {})
    if not isinstance(checks, dict): raise ReleaseError("acceptance checks must be an object")
    for name in ("L0", "L1", "L1_agent", "related"):
        check = checks.get(name, {})
        if not isinstance(check, dict): raise ReleaseError("acceptance check must be an object")
        if (check.get("status") != "pass" or type(check.get("failed")) is not int
                or check["failed"] != 0 or type(check.get("passed")) is not int or check["passed"] < 1
                or check.get("skipped", 0) != 0 or check.get("soft_failed", 0) != 0):
            raise ReleaseError("test acceptance is missing, failed, skipped, or soft-failed")
        evidence = check.get("evidence", "")
        if not isinstance(evidence, str) or not evidence.startswith(forge + "/"):
            raise ReleaseError("native project evidence is required for every acceptance check")
    return report


def validate_manifest(text, revision, tag):
    validate_revision(revision)
    validate_tag(tag)
    if not any(len(parts := line.split()) == 3 and parts[:2] == [tag, revision]
               and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", parts[2]) for line in text.splitlines()):
        raise ReleaseError("the exact image/revision is absent from the successful test release manifest")
