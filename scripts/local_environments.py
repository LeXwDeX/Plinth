"""Locate the untracked local deployment folder shared by every worktree.

Internal hosts, roots, ports and Compose names live only in an untracked
environments file (template: deploy/environments.example.json). Untracked files
do not follow `git worktree add`, so a worktree without its own copy falls back
to the main checkout's copy at the same relative path.

Order: an explicit path (--file / ART_AI_ENVIRONMENTS_FILE); then, in this
checkout and then in the main checkout, deploy/local/environments.json and the
older deploy/environments.local.json.
"""
import os
from pathlib import Path
import subprocess

REPOSITORY = Path(__file__).resolve().parent.parent
CANDIDATES = (Path("deploy/local/environments.json"), Path("deploy/environments.local.json"))


def main_checkout(repository=REPOSITORY):
    """The main checkout of this repository, or None when Git cannot tell."""
    try:
        result = subprocess.run(
            ["git", "-C", str(repository), "rev-parse", "--path-format=absolute", "--git-common-dir"],
            capture_output=True, text=True, timeout=10, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    common = result.stdout.strip()
    if result.returncode or not common:
        return None
    common = Path(common)
    return common.parent if common.name == ".git" else None


def environments_file(explicit=None, repository=REPOSITORY):
    """Path of the environments file to read; the first candidate when none exists."""
    explicit = explicit or os.environ.get("ART_AI_ENVIRONMENTS_FILE")
    if explicit:
        return Path(explicit)
    roots = [Path(repository)]
    main = main_checkout(repository)
    if main is not None and main.resolve() != Path(repository).resolve():
        roots.append(main)
    for root in roots:
        for candidate in CANDIDATES:
            if (root / candidate).is_file():
                return root / candidate
    return Path(repository) / CANDIDATES[0]
