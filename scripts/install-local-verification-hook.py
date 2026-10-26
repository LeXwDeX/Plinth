#!/usr/bin/env python3
"""Preview/install the repository-owned local gate block; never edit SpecGit blocks."""
import argparse
import difflib
import fcntl
import os
from pathlib import Path
import stat
import subprocess
import tempfile

START = "# >>> artai-local-verification:v1:start >>>\n"
END = "# <<< artai-local-verification:v1:end <<<\n"
LEGACY = '''# Local seven-gate checks run before pushes; remote CI remains required.
# Never bypass hooks. Follow the project's verification policy.
set -euo pipefail
echo "[pre-push] running local CI (seven gates)..."
pnpm exec tsc --noEmit
pnpm test
pnpm build
go vet ./...
go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...
CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/art-ai
echo "[pre-push] local CI green."
'''
BLOCK = START + '''# SpecGit guard above remains authoritative. CI always runs independently.
set -euo pipefail
artai_push_input=$(mktemp "${TMPDIR:-/tmp}/artai-pre-push.XXXXXX") || exit 1
cat > "$artai_push_input" || { rm -f "$artai_push_input"; exit 1; }
# Keep a separate open descriptor for later unrelated hook commands.
exec < "$artai_push_input"
(
  trap 'rm -f "$artai_push_input"' EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [ -f scripts/verify-local.py ] &&
     git ls-files --error-unmatch -- scripts/verify-local.py >/dev/null 2>&1 &&
     grep -qx 'HOOK_PROTOCOL = 1' scripts/verify-local.py; then
    python3 scripts/verify-local.py --pre-push < "$artai_push_input" || exit $?
  else
    # Older worktrees and untracked/unsupported drafts keep full checks.
    echo "[pre-push] running local CI (seven gates; legacy worktree)..."
    pnpm exec tsc --noEmit || exit $?
    pnpm test || exit $?
    pnpm build || exit $?
    go vet ./... || exit $?
    go test -count=1 ./... || exit $?
    CGO_ENABLED=1 go test -race -count=1 ./... || exit $?
    CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/art-ai || exit $?
    echo "[pre-push] local CI green."
  fi
) || exit $?
unset artai_push_input
''' + END


def proposed(original, uninstall=False):
    if not original.startswith(("#!/usr/bin/env bash\n", "#!/bin/bash\n")):
        raise ValueError("expected the existing Bash pre-push hook; preserve unsupported hooks")
    count_start, count_end = original.count(START), original.count(END)
    if count_start or count_end:
        if (count_start, count_end) != (1, 1):
            raise ValueError("duplicate or incomplete Plinth block; no hook changed")
        first, last = original.index(START), original.index(END) + len(END)
        if last <= first or original[first:last] != BLOCK:
            raise ValueError("Plinth block changed; review it before any replacement")
        if LEGACY in original:
            raise ValueError("both legacy and Plinth gates exist; no hook changed")
        return original[:first] + (LEGACY if uninstall else BLOCK) + original[last:]
    if "artai-local-verification:" in original:
        raise ValueError("unrecognized Plinth marker; no hook changed")
    if original.count(LEGACY) != 1:
        raise ValueError("expected one exact legacy gate block; no hook changed")
    return original if uninstall else original.replace(LEGACY, BLOCK, 1)


def effective_hook(root):
    config = subprocess.run(["git", "config", "--path", "--get", "core.hooksPath"],
                            cwd=root, text=True, capture_output=True)
    if config.returncode not in (0, 1):
        raise ValueError("could not read the effective hooks path")
    if config.returncode == 0:
        directory = Path(config.stdout.strip())
        if not directory.is_absolute():
            directory = root / directory
    else:
        directory = Path(subprocess.check_output(
            ["git", "rev-parse", "--path-format=absolute", "--git-path", "hooks"],
            cwd=root, text=True).strip())
    if directory.is_symlink():
        raise ValueError("symlink hooks directory is unsupported; no hook changed")
    return directory / "pre-push"


def install(path, uninstall=False, apply=False):
    if path.is_symlink() or not path.is_file():
        raise ValueError("expected an existing regular pre-push hook; install SpecGit guard first")
    before = path.stat()
    original_bytes = path.read_bytes()
    original = original_bytes.decode("utf-8")
    replacement = proposed(original, uninstall)
    if replacement == original:
        print("[hook] unchanged")
        return False
    print("".join(difflib.unified_diff(original.splitlines(True), replacement.splitlines(True),
                                     fromfile=str(path), tofile=str(path))), end="")
    if not apply:
        print("[hook] dry-run only; use --apply after compatibility checks")
        return True
    # Atomic replacement leaves an already-running hook on its original inode.
    # The lock serializes this installer; the content check rejects other writers.
    def same_file():
        current = path.stat()
        # Reading may update atime; it is not a concurrent content modification.
        fields = ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")
        return (not path.is_symlink() and
                all(getattr(current, field) == getattr(before, field) for field in fields) and
                path.read_bytes() == original_bytes)

    with (path.parent / ".artai-verification-install.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if not same_file():
            raise ValueError("hook changed since preview; no hook changed")
        fd, name = tempfile.mkstemp(prefix=".pre-push-", dir=path.parent)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(replacement.encode("utf-8"))
                stream.flush()
                os.fsync(stream.fileno())
            os.chmod(name, stat.S_IMODE(before.st_mode))
            if not same_file():
                raise ValueError("hook changed during preparation; no hook changed")
            os.replace(name, path)
        finally:
            if os.path.exists(name):
                os.unlink(name)
    print("[hook] installed" if not uninstall else "[hook] restored full checks")
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--apply", action="store_true")
    mode.add_argument("--dry-run", action="store_true", help="default; print the exact diff only")
    parser.add_argument("--uninstall", action="store_true", help="restore the original full gate block")
    args = parser.parse_args()
    root = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
    install(effective_hook(root), uninstall=args.uninstall, apply=args.apply)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"[hook] {error}")
