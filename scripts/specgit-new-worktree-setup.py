#!/usr/bin/env python3
"""Initialize a new project worktree and install its SpecGit host integrations.

All managed files are written by SpecGit itself. This helper only constructs
validated CLI calls, previews each setup plan, and checks the resulting doctor
report. It is invoked by the repository-owned post-checkout hook.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

GIT_HOOK_ENV = ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_COMMON_DIR")
AGENTS = ("generic", "claude", "codex", "opencode")
REQUIRED_PURPOSES = {
    "generic": ("guard",),
    "claude": ("guard", "context"),
    "codex": ("guard", "context"),
    "opencode": ("guard", "context"),
}


def cli_path():
    found = shutil.which("specgit") or str(Path.home() / ".local/bin/specgit")
    return str(Path(found).resolve())


def main_checkout(root):
    common = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
                            cwd=root, env=clean_env(), text=True, capture_output=True, check=True).stdout.strip()
    return Path(common).parent


def clean_env():
    env = os.environ.copy()
    for name in GIT_HOOK_ENV:
        env.pop(name, None)
    return env


def invoke(binary, root, args):
    result = subprocess.run([binary, "--cwd", str(root), *args], cwd=root,
                            env=clean_env(), text=True, capture_output=True)
    try:
        report = json.loads(result.stdout)
    except (ValueError, TypeError):
        report = None
    if result.returncode or not isinstance(report, dict) or report.get("ok") is not True:
        diagnostics = report.get("diagnostics", []) if isinstance(report, dict) else []
        detail = "; ".join(
            f"{item.get('code', 'error')}: {item.get('message', '')}"
            for item in diagnostics if isinstance(item, dict)
        )
        if not detail:
            detail = (result.stderr or result.stdout or f"exit {result.returncode}").strip()
        raise RuntimeError(f"specgit {' '.join(args)} failed ({result.returncode}): {detail}")
    return report


def setup_args(agent):
    args = ["setup", "--scope", "project", "--agent", agent,
            "--hook-purpose", "guard", "--hook-purpose", "context"]
    if agent == "opencode":
        args.append("--opencode-claude-hooks")
    return args


def setup_purposes(report, agent):
    evidence = report.get("evidence")
    all_purposes = evidence.get("hook_purposes") if isinstance(evidence, dict) else None
    purposes = all_purposes.get(agent) if isinstance(all_purposes, dict) else None
    if not isinstance(purposes, dict):
        raise RuntimeError(f"specgit setup {agent} omitted evidence.hook_purposes.{agent}")
    installed = purposes.get("installed")
    declined = purposes.get("declined")
    if not isinstance(installed, list) or not isinstance(declined, list):
        raise RuntimeError(f"specgit setup {agent} returned malformed hook purpose evidence")
    required = REQUIRED_PURPOSES[agent]
    missing = [purpose for purpose in required if purpose not in installed]
    if missing:
        details = "; ".join(
            f"{item.get('purpose', 'unknown')}={item.get('status', 'unknown')}"
            for item in declined if isinstance(item, dict)
        ) or "no decline reason reported"
        raise RuntimeError(f"specgit setup {agent} did not install required purpose(s) {', '.join(missing)}: {details}")
    installed_text = ", ".join(purpose for purpose in installed if isinstance(purpose, str)) or "none"
    declined_text = ", ".join(
        f"{item.get('purpose', 'unknown')} ({item.get('status', 'unknown')})"
        for item in declined if isinstance(item, dict)
    ) or "none"
    return f"installed {installed_text}; declined {declined_text}"


def run(cwd, config_file=None):
    root = Path(cwd).resolve()
    if not (root / ".git").exists() and not (root / ".git").is_file():
        raise RuntimeError(f"not a Git worktree: {root}")
    config = Path(config_file).resolve() if config_file else root / "docs/specgit-project.yaml"
    if not config.is_file():
        config = main_checkout(root) / "docs/specgit-project.yaml"
    if not config.is_file():
        raise RuntimeError(f"project declaration source is missing: {config}")
    binary = cli_path()

    if not (root / ".specgit.yaml").is_file():
        invoke(binary, root, ["init", "--config-file", str(config), "--manual-observe", "--json"])
        print(f"[specgit-worktree] initialized {root}")
    else:
        print(f"[specgit-worktree] declaration exists in {root}; initialized does not mean host setup is ready; retrying setup and doctor")

    for agent in AGENTS:
        args = setup_args(agent)
        preview = invoke(binary, root, [*args, "--dry-run", "--json"])
        setup_purposes(preview, agent)
        result = invoke(binary, root, [*args, "--json"])
        print(f"[specgit-worktree] setup {agent}: {setup_purposes(result, agent)}")

    doctor = invoke(binary, root, ["doctor", "--provider", "github", "--remote", "origin",
                                   "--offline", "--json"])
    doctor_status = doctor.get("status")
    evidence = doctor.get("evidence")
    health = evidence.get("health") if isinstance(evidence, dict) else None
    health_status = health.get("status") if isinstance(health, dict) else None
    host_behavior = health.get("host_behavior") if isinstance(health, dict) else None
    if doctor_status != "offline_checked" or health_status != "healthy" or host_behavior != "not_verified":
        diagnostics = "; ".join(
            f"{item.get('code', 'error')}: {item.get('message', '')}"
            for item in doctor.get("diagnostics", []) if isinstance(item, dict)
        )
        raise RuntimeError(
            "specgit doctor did not report offline static health "
            f"(status={doctor_status or 'unknown'}, health={health_status or 'unknown'}, "
            f"host_behavior={host_behavior or 'unknown'}): {diagnostics}"
        )
    print("[specgit-worktree] doctor: offline static healthy; host_behavior=not_verified")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cwd", required=True, help="new Git worktree root")
    parser.add_argument("--config-file", help="explicit project declaration source")
    args = parser.parse_args()
    run(args.cwd, args.config_file)


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"[specgit-worktree] {error}", file=sys.stderr)
        sys.exit(1)
