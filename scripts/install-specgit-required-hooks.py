#!/usr/bin/env python3
"""Preview/install the repository-owned SpecGit-required hook blocks (#242); never edit SpecGit blocks.

Git hooks are shared by every worktree of a clone, but SpecGit guard enforces checkpoints only
in worktrees that have a local .specgit.yaml. These blocks report that gap at commit/push
(warn mode) or refuse it (enforce mode), and initialize/configure after `git worktree add`.
Nothing is written without --apply; see docs/specgit-project-integration.md for the rollout.

  --status                 read-only: installed mode and every worktree's SpecGit state
  --mode warn|enforce      preview installing or switching the blocks; --apply writes them
  --uninstall              preview removing only these blocks; --apply removes them
  --init-worktrees         preview init/setup/doctor for ready and initialized worktrees; --apply runs them
"""
import argparse
import difflib
import fcntl
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile

START = "# >>> artai-specgit-required:v1:start >>>\n"
END = "# <<< artai-specgit-required:v1:end <<<\n"
MODES = ("warn", "enforce")
REQUIRED_HOOKS = ("pre-commit", "pre-push")
POST_CHECKOUT_SHEBANG = "#!/bin/sh\n"
CONFIG = "docs/specgit-project.yaml"
GUIDANCE = re.compile(r"<!-- specgit:project:v2:start -->\n.*?<!-- specgit:project:v2:end -->\n", re.S)
# Git exports these to hooks; a CLI started for another worktree must not inherit them.
GIT_HOOK_ENV = ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_COMMON_DIR")
GUARD_MARKERS = {
    name: (f"# >>> specgit-v2:{name}:start >>>", f"# <<< specgit-v2:{name}:end <<<")
    for name in REQUIRED_HOOKS
}

REQUIRE_TEMPLATE = r'''# Mode: @MODE@. Every worktree must be SpecGit-initialized; see docs/specgit-project-integration.md.
artai_sg_root=$(git rev-parse --show-toplevel 2>/dev/null) || artai_sg_root=
if [ -n "$artai_sg_root" ] && [ ! -f "$artai_sg_root/.specgit.yaml" ]; then
  artai_sg_config="$artai_sg_root/docs/specgit-project.yaml"
  [ -f "$artai_sg_config" ] ||
    artai_sg_config="$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")/docs/specgit-project.yaml"
  {
  echo "[specgit-required] $artai_sg_root has no .specgit.yaml (SpecGit not initialized); @VERDICT@"
  echo "[specgit-required] fix: specgit init --cwd '$artai_sg_root' --config-file '$artai_sg_config' --manual-observe"
  echo "[specgit-required] if init reports ownership_conflict on AGENTS.md, rebase onto origin/main first."
  } >&2
@EXIT@fi
unset artai_sg_root artai_sg_config
'''
VERDICTS = {
    "warn": ("allowed for now (warn mode); enforce mode will refuse it.", ""),
    "enforce": ("refused.", "  exit 1\n"),
}
REQUIRE = {mode: START + REQUIRE_TEMPLATE.replace("@MODE@", mode).replace("@VERDICT@", verdict)
           .replace("@EXIT@", exit_line) + END for mode, (verdict, exit_line) in VERDICTS.items()}
POST_CHECKOUT = START + r'''# `git worktree add` checks out from the null revision: initialize and wire the new worktree.
# The helper initializes the project and wires the supported host integrations.
# A failure never fails the checkout; it prints a repair command for the Agent.
if [ "${3:-}" = 1 ] && [ -n "${1:-}" ] && [ -z "$(printf '%s' "$1" | tr -d 0)" ]; then
  artai_sg_root=$(git rev-parse --show-toplevel 2>/dev/null) || artai_sg_root=
  if [ -n "$artai_sg_root" ]; then
    artai_sg_helper="$artai_sg_root/scripts/specgit-new-worktree-setup.py"
    if [ -f "$artai_sg_helper" ]; then
      if artai_sg_out=$(env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_PREFIX -u GIT_COMMON_DIR \
          python3 "$artai_sg_helper" --cwd "$artai_sg_root" 2>&1); then
        printf '%s\n' "$artai_sg_out" >&2
      else
        {
        echo "[specgit-required] automatic initialization/setup failed in $artai_sg_root:"
        printf '%s\n' "$artai_sg_out" | head -c 3000; echo
        echo "[specgit-required] fix: python3 '$artai_sg_helper' --cwd '$artai_sg_root'"
        } >&2
      fi
    else
      {
      echo "[specgit-required] setup helper is missing in $artai_sg_root; worktree remains usable."
      echo "[specgit-required] fix: rebase onto origin/main, then run specgit init and the project setup instructions."
      } >&2
    fi
  fi
  unset artai_sg_root artai_sg_helper artai_sg_out
fi
''' + END
PREVIOUS_POST_CHECKOUT = START + r'''# `git worktree add` checks out from the null revision: run `specgit init` in the new worktree.
# A failure never fails the checkout; commit/push then print the same fix.
if [ "${3:-}" = 1 ] && [ -n "${1:-}" ] && [ -z "$(printf '%s' "$1" | tr -d 0)" ]; then
  artai_sg_root=$(git rev-parse --show-toplevel 2>/dev/null) || artai_sg_root=
  if [ -n "$artai_sg_root" ] && [ ! -f "$artai_sg_root/.specgit.yaml" ]; then
    artai_sg_config="$artai_sg_root/docs/specgit-project.yaml"
    [ -f "$artai_sg_config" ] ||
      artai_sg_config="$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")/docs/specgit-project.yaml"
    artai_sg_bin=$(command -v specgit 2>/dev/null) || artai_sg_bin="$HOME/.local/bin/specgit"
    if artai_sg_out=$(env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_PREFIX -u GIT_COMMON_DIR \
        "$artai_sg_bin" init --cwd "$artai_sg_root" --config-file "$artai_sg_config" \
        --manual-observe --json 2>&1); then
      echo "[specgit-required] SpecGit initialized in $artai_sg_root" >&2
    else
      {
      echo "[specgit-required] automatic SpecGit init failed in $artai_sg_root:"
      printf '%s\n' "$artai_sg_out" | head -c 2000; echo
      echo "[specgit-required] fix: specgit init --cwd '$artai_sg_root' --config-file '$artai_sg_config' --manual-observe"
      } >&2
    fi
  fi
  unset artai_sg_root artai_sg_config artai_sg_bin artai_sg_out
fi
''' + END


def proposed(original, wanted, known):
    """Return the hook with `wanted` as its only block (after the shebang), or without it if None.

    An existing block is replaced only when it is byte-identical to one of `known`.
    """
    first_line = original.split("\n", 1)[0]
    if not supported_shell_shebang(first_line):
        raise ValueError("expected an existing shell hook; preserve unsupported hooks")
    starts, ends = original.count(START), original.count(END)
    if starts or ends:
        if (starts, ends) != (1, 1):
            raise ValueError("duplicate or incomplete SpecGit-required block; no hook changed")
        first, last = original.index(START), original.index(END) + len(END)
        if last <= first or original[first:last] not in known:
            raise ValueError("SpecGit-required block changed; review it before any replacement")
        return original[:first] + (wanted or "") + original[last:]
    if "artai-specgit-required:" in original:
        raise ValueError("unrecognized SpecGit-required marker; no hook changed")
    if wanted is None:
        return original
    shebang, rest = original.split("\n", 1) if "\n" in original else (original, "")
    return shebang + "\n" + wanted + rest


def supported_shell_shebang(line):
    """Accept only explicitly recognized POSIX sh/bash/dash interpreter forms."""
    if not line.startswith("#!"):
        return False
    try:
        parts = shlex.split(line[2:].strip())
    except ValueError:
        return False
    if not parts:
        return False
    executable = Path(parts[0]).name
    if executable == "env":
        args = parts[1:]
        if args[:1] == ["-S"]:
            args = args[1:]
        if len(args) != 1:
            return False
        executable = Path(args[0]).name
    elif len(parts) != 1:
        return False
    return executable in {"sh", "bash", "dash"}


def validate_guard(name, path, original):
    start, end = GUARD_MARKERS[name]
    if original.count(start) != 1 or original.count(end) != 1:
        raise ValueError(f"{name} must contain one complete unique SpecGit guard; no hook changed")
    if original.index(end) <= original.index(start) or not original.split(start, 1)[1].split(end, 1)[0].strip():
        raise ValueError(f"{name} has an incomplete SpecGit guard; no hook changed")
    guard_body = original.split(start, 1)[1].split(end, 1)[0]
    if not re.search(rf"\bguard\s+--stage\s+{re.escape(name)}(?:\s|$)", guard_body):
        raise ValueError(f"{name} does not invoke its SpecGit guard stage; no hook changed")
    if not os.access(path, os.X_OK):
        raise ValueError(f"{name} SpecGit guard is not executable by Git; no hook changed")


def installed_block(text, known):
    """Return the name of the installed block in `known` ({name: block}), 'absent' or 'changed'."""
    if text is None or START not in text and END not in text:
        return "absent"
    for name, block in known.items():
        if text.count(START) == 1 and block in text:
            return name
    return "changed"


def git(*args, cwd):
    return subprocess.run(["git", *args], cwd=cwd, text=True, capture_output=True, check=True).stdout


def hooks_directory(root):
    config = subprocess.run(["git", "config", "--get-all", "core.hooksPath"],
                            cwd=root, text=True, capture_output=True)
    if config.returncode not in (0, 1):
        raise ValueError("could not read the effective hooks path")
    if config.returncode == 0:
        raise ValueError("explicit core.hooksPath is unsupported; use Git's default common-dir/hooks")
    common_dir = Path(git("rev-parse", "--path-format=absolute", "--git-common-dir", cwd=root).strip())
    common_hooks = common_dir / "hooks"
    if common_hooks.is_symlink():
        raise ValueError("expected the clone's regular common-dir/hooks directory; no hook changed")
    expected = common_hooks.resolve()
    directory = expected
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("expected a regular hooks directory; no hook changed")
    return directory


def plan(directory, mode=None, uninstall=False):
    """Return [(path, original or None, replacement or None, mode bits)] for changed files only."""
    changes = []
    for name in REQUIRED_HOOKS:
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"expected an existing regular {name} hook with SpecGit guard; no hook changed")
        original = path.read_text()
        validate_guard(name, path, original)
        replacement = proposed(original, None if uninstall else REQUIRE[mode], tuple(REQUIRE.values()))
        if replacement != original:
            changes.append((path, original, replacement, stat.S_IMODE(path.stat().st_mode)))
    path = directory / "post-checkout"
    if path.is_symlink():
        raise ValueError("symlink post-checkout is unsupported; no hook changed")
    if path.exists():
        if not path.is_file():
            raise ValueError("expected a regular post-checkout hook; no hook changed")
        if not os.access(path, os.X_OK):
            raise ValueError("existing post-checkout hook is not executable by Git; no hook changed")
        original = path.read_text()
        replacement = proposed(original, None if uninstall else POST_CHECKOUT,
                               (POST_CHECKOUT, PREVIOUS_POST_CHECKOUT))
        if replacement != original:
            changes.append((path, original, replacement, stat.S_IMODE(path.stat().st_mode)))
    elif not uninstall:
        changes.append((path, None, POST_CHECKOUT_SHEBANG + POST_CHECKOUT, 0o755))
    return changes


def write(path, original, replacement, mode):
    """Atomically replace one file after confirming it still matches the preview."""
    current = path.read_text() if path.exists() and not path.is_symlink() else None
    if path.is_symlink() or current != original:
        raise ValueError(f"{path.name} changed since preview; stopped")
    if replacement is None:
        path.unlink()
        return
    fd, name = tempfile.mkstemp(prefix=f".{path.name}-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(replacement)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, mode)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def install(directory, mode=None, uninstall=False, apply=False):
    if not uninstall and mode not in MODES:
        raise ValueError("choose --mode warn or --mode enforce")
    changes = plan(directory, mode, uninstall)
    if not changes:
        print("[hook] unchanged")
        return False
    for path, original, replacement, _ in changes:
        print("".join(difflib.unified_diff(
            (original or "").splitlines(True), (replacement or "").splitlines(True),
            fromfile=str(path) if original is not None else "/dev/null",
            tofile=str(path) if replacement is not None else "/dev/null")), end="")
    if not apply:
        print("[hook] dry-run only; add --apply to write exactly this diff")
        return True
    with (directory / ".artai-specgit-required.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        for change in changes:
            write(*change)
    print("[hook] removed SpecGit-required blocks" if uninstall else f"[hook] installed ({mode} mode)")
    return True


def worktrees(root):
    """Parse `git worktree list --porcelain` into dicts with path/branch/flags."""
    entries, entry = [], {}
    for line in git("worktree", "list", "--porcelain", cwd=root).splitlines() + [""]:
        if not line:
            if entry:
                entries.append(entry)
            entry = {}
            continue
        key, _, value = line.partition(" ")
        entry[key] = value or True
    return [entry for entry in entries if "bare" not in entry]


def guidance_block(path):
    text = path.read_text() if path.is_file() else ""
    blocks = GUIDANCE.findall(text)
    return blocks[0] if len(blocks) == 1 else None


def classify(entry, reference):
    """Return (state, reason) for one worktree; `reference` is the expected AGENTS.md SpecGit block."""
    path = Path(entry["worktree"])
    if not path.is_dir():
        return "missing", "path no longer exists; `git worktree prune` once it is no longer needed"
    if (path / ".specgit.yaml").is_file():
        return "initialized", "declaration exists; host setup and doctor readiness are unverified"
    block = guidance_block(path / "AGENTS.md")
    if block is None:
        return "no-block", "AGENTS.md has no single SpecGit block; rebase onto origin/main, then init"
    if block != reference:
        return "stale-block", "AGENTS.md SpecGit block differs from main; rebase onto origin/main, then init"
    return "ready", "specgit init can run here"


def survey(root):
    reference = guidance_block(root / "AGENTS.md")
    if reference is None:
        raise ValueError(f"{root / 'AGENTS.md'} has no single SpecGit block to compare against")
    return [(entry, *classify(entry, reference)) for entry in worktrees(root)]


def label(entry):
    branch = entry.get("branch")
    return branch.removeprefix("refs/heads/") if isinstance(branch, str) else "(detached)"


def status(root, directory):
    hooks = {name: (directory / name).read_text() if (directory / name).is_file() else None
             for name in (*REQUIRED_HOOKS, "post-checkout")}
    print(f"hooks: {directory}")
    for name in REQUIRED_HOOKS:
        print(f"  {name}: {installed_block(hooks[name], REQUIRE)}")
    print(f"  post-checkout: {installed_block(hooks['post-checkout'], {
        'auto-init+setup': POST_CHECKOUT, 'legacy-auto-init': PREVIOUS_POST_CHECKOUT})}")
    rows = survey(root)
    counts = {}
    for entry, state, reason in rows:
        counts[state] = counts.get(state, 0) + 1
        locked = " locked" if "locked" in entry else ""
        print(f"{state:<12} {entry['worktree']} [{label(entry)}]{locked}" + (f"  - {reason}" if reason and state != "ready" else ""))
    print("summary: " + ", ".join(f"{state} {count}" for state, count in sorted(counts.items()))
          + f" (total {len(rows)}; reference block from {root / 'AGENTS.md'})")


def init_worktrees(root, apply=False):
    rows = survey(root)
    actionable = [(entry, state) for entry, state, _ in rows if state in ("ready", "initialized")]
    for entry, state, reason in rows:
        if state not in ("ready", "initialized"):
            print(f"skip {state}: {entry['worktree']} [{label(entry)}] - {reason}")
    if not actionable:
        print("[init] no worktree is eligible for SpecGit init/setup")
        return True
    env = {key: value for key, value in os.environ.items() if key not in GIT_HOOK_ENV}
    failures = 0
    for entry, state in actionable:
        path = Path(entry["worktree"])
        command = [sys.executable, str(root / "scripts/specgit-new-worktree-setup.py"),
                   "--cwd", str(path), "--config-file", str(path / CONFIG)]
        if not apply:
            print(f"would run ({state}; init only if declaration is absent): " + " ".join(command))
            continue
        result = subprocess.run(command, cwd=path, env=env, text=True, capture_output=True)
        if result.returncode == 0:
            print(f"[init] setup+offline doctor healthy: {path}")
            continue
        failures += 1
        detail = (result.stderr or result.stdout).strip()
        print(f"[init] FAILED ({result.returncode}; {detail}): {path}")
    if not apply:
        print(f"[init] dry-run only; add --apply to init when needed, then configure and verify {len(actionable)} worktree(s)")
    return failures == 0


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument("--status", action="store_true", help="read-only report")
    action.add_argument("--mode", choices=MODES, help="install or switch the commit/push blocks")
    action.add_argument("--uninstall", action="store_true",
                        help="remove only the SpecGit-required blocks")
    action.add_argument("--init-worktrees", action="store_true",
                        help="init if needed, then run setup and doctor for every eligible worktree")
    write_mode = parser.add_mutually_exclusive_group()
    write_mode.add_argument("--apply", action="store_true")
    write_mode.add_argument("--dry-run", action="store_true", help="default; print the exact plan only")
    args = parser.parse_args()
    root = Path(git("rev-parse", "--show-toplevel", cwd=Path.cwd()).strip())
    if args.status:
        status(root, hooks_directory(root))
    elif args.init_worktrees:
        if not init_worktrees(root, apply=args.apply):
            raise SystemExit(1)
    else:
        install(hooks_directory(root), mode=args.mode, uninstall=args.uninstall, apply=args.apply)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"[hook] {error}")
