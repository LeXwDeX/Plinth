#!/usr/bin/env python3
"""Cooperative per-root release exclusion for Plinth deployment operations.

One release root has one lock file, <root>/.release.lock. Deployment backup
and restore, release backup retention and the documented manual recovery all
run under a flock on that file, so a concurrent restore cannot move a tree
while retention removes it. The server steps acquire the lock before any
build or data change and pass the descriptor to the retention helper as
RELEASE_LOCK_FD/RELEASE_LOCK_ROOT; the helper validates the descriptor and
reuses it instead of taking a second lock. Missing or forged inherited
descriptors fail closed. After the root, regular-file and path-identity checks,
the inherited descriptor itself is flocked with LOCK_EX|LOCK_NB: a descriptor
that shares the holder's open file description keeps that lock, an independent
descriptor on a free file acquires it before any operation, and an independent
descriptor on a lock held by another process fails busy. A borrowed descriptor
is never unlocked by the child; the original holder releases it at its outer
end. This is a supported-operation protocol, not a defence against a hostile
root owner, and it uses no platform-specific lock table or environment bypass.
"""
import argparse
import contextlib
import errno
import fcntl
import os
from pathlib import Path
import stat
import subprocess
import sys
from typing import NoReturn

LOCK_NAME = ".release.lock"
INHERITED_FD = "RELEASE_LOCK_FD"
INHERITED_ROOT = "RELEASE_LOCK_ROOT"


class LockError(ValueError):
    """A fixed, credential-free refusal reason."""


def fail(message: str) -> NoReturn:
    raise LockError(message)


def root_path(root):
    if (not isinstance(root, str) or not root.startswith("/") or root.startswith("//")
            or "\0" in root or ".." in Path(root).parts):
        fail("root must be a normalized absolute directory")
    root = os.path.normpath(root)
    if root == "/":
        fail("root must be a normalized absolute directory")
    try:
        info = os.lstat(root)
    except OSError:
        fail("release lock root must be a real directory")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("release lock root must be a real directory")
    return root


def inspect(descriptor, label="release lock"):
    try:
        info = os.fstat(descriptor)
    except OSError:
        fail(f"{label} descriptor is invalid")
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        fail(f"{label} must be an unshared regular file")
    if info.st_uid != os.geteuid():
        fail(f"{label} is owned by another user")
    return info


def acquire(root):
    path = os.path.join(root_path(root), LOCK_NAME)
    try:
        descriptor = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    except OSError:
        fail("release lock is unavailable")
    try:
        info = inspect(descriptor)
        try:
            named = os.lstat(path)
        except OSError:
            fail("release lock identity changed")
        if (not stat.S_ISREG(named.st_mode)
                or (info.st_dev, info.st_ino) != (named.st_dev, named.st_ino)):
            fail("release lock identity changed")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            if error.errno in (errno.EACCES, errno.EAGAIN):
                fail("release lock is busy")
            fail("release lock is unavailable")
    except BaseException:
        os.close(descriptor)
        raise
    return descriptor


def inherited_descriptor(root, missing_ok=False):
    value = os.environ.get(INHERITED_FD)
    named_root = os.environ.get(INHERITED_ROOT)
    if value is None and named_root is None:
        if missing_ok:
            return None
        fail("inherited release lock needs both descriptor and root")
    if value is None or named_root is None:
        fail("inherited release lock needs both descriptor and root")
    try:
        descriptor = int(value)
    except ValueError:
        fail("inherited release lock descriptor is invalid")
    if descriptor < 3:
        fail("inherited release lock descriptor is invalid")
    if named_root != root:
        fail("inherited release lock root does not match")
    info = inspect(descriptor, "inherited release lock")
    try:
        named = os.lstat(os.path.join(root, LOCK_NAME))
    except OSError:
        fail("inherited release lock identity is unavailable")
    if (not stat.S_ISREG(named.st_mode)
            or (info.st_dev, info.st_ino) != (named.st_dev, named.st_ino)):
        fail("inherited release lock identity does not match")
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError as error:
        if error.errno in (errno.EACCES, errno.EAGAIN):
            fail("release lock is busy")
        fail("inherited release lock is unavailable")
    return descriptor


def validate_environment(root):
    root = root_path(root)
    inherited_descriptor(root, missing_ok=True)


@contextlib.contextmanager
def hold(root):
    root = root_path(root)
    inherited = inherited_descriptor(root, missing_ok=True)
    if inherited is not None:
        yield inherited
        return
    descriptor = acquire(root)
    try:
        yield descriptor
    finally:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_UN)
        finally:
            os.close(descriptor)


def run(root, command):
    if not command:
        fail("release lock run needs a command")
    root = root_path(root)
    with hold(root) as descriptor:
        environment = {**os.environ, INHERITED_FD: str(descriptor), INHERITED_ROOT: root}
        os.set_inheritable(descriptor, True)
        return subprocess.run(command, env=environment, pass_fds=(descriptor,)).returncode


def main(argv=None):
    parser = argparse.ArgumentParser(description="Coordinate one Plinth release root.")
    sub = parser.add_subparsers(dest="mode", required=True)
    sub.add_parser("hold").add_argument("--root", required=True)
    sub.add_parser("validate").add_argument("--root", required=True)
    runner = sub.add_parser("run")
    runner.add_argument("--root", required=True)
    runner.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = list(getattr(args, "command", []))
    if command[:1] == ["--"]:
        command = command[1:]
    try:
        if args.mode == "hold":
            with hold(args.root):
                print("locked", flush=True)
                sys.stdin.readline()
            return 0
        if args.mode == "validate":
            validate_environment(args.root)
            return 0
        if not command:
            fail("release lock run needs a command after --")
        return run(args.root, command)
    except LockError as error:
        print(f"release-lock: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
