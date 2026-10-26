#!/usr/bin/env python3
"""Retain five recent Plinth test images plus protected image identities.

Default is read-only. Only --apply removes images, using plain docker rmi ID.
No container, volume, build cache, database or backup is removed.
Every run names the deployment --root and the running Plinth Compose --project
(both from the environment's entry in the untracked deploy/local/environments.json;
scripts/release-env.py supplies them). A shared host also names every other
Compose project whose containers keep their images with --protect-project; a
single-stack host names none and usually lowers --keep. The repository names no
environment's project (#261).
"""
import argparse
from contextlib import contextmanager
import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

REPOSITORY = "art-ai-go-production"
ROOT = None
PROJECTS = ()
RUNNING_PROJECT = None
RUNNING_CONTAINER = None
COMPOSE_PROJECT = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
KEEP = 5


def private_directory(path):
    """Create only the named directory, never follow a deployment-path symlink."""
    path.mkdir(mode=0o700, exist_ok=True)
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fchmod(descriptor, 0o700)
    finally:
        os.close(descriptor)


@contextmanager
def image_lock(root=ROOT):
    """Serialize formal pin updates and image removal; callers retain the lock file."""
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError("image lock root is not a directory")
    directory = root / "releases"
    private_directory(directory)
    descriptor = os.open(directory / "image-retention.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise ValueError("image lock must be an unshared regular file")
        os.fchmod(descriptor, 0o600)
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(descriptor, fcntl.LOCK_UN)
    finally:
        os.close(descriptor)


def pin_identities(document):
    if (not isinstance(document, dict)
            or set(document) != {"version", "current", "rollback", "pending"}
            or type(document["version"]) is not int or document["version"] != 1):
        raise ValueError("invalid formal image pin document")
    identities = {image_id(document[key]) for key in ("current", "rollback", "pending")
                  if document[key] is not None}
    if not identities:
        raise ValueError("formal image pins must protect at least one image")
    return identities


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate formal image pin field")
        result[key] = value
    return result


def formal_image_pins(root=ROOT):
    """Read-only. An existing formal stack without trustworthy pins stops cleanup."""
    formal = Path(root) / "formal"
    if formal.is_symlink():
        raise ValueError("formal image pin directory must not be a symlink")
    if not formal.exists():
        return set()
    reports = formal / "reports"
    if not formal.is_dir() or reports.is_symlink() or not reports.is_dir():
        raise ValueError("formal image pin directory is unavailable")
    descriptor = os.open(reports / "image-pins.json", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
                or info.st_nlink != 1 or info.st_size > 4096):
            raise ValueError("formal image pins require a bounded private regular file")
        raw = source.read(4097)
    if len(raw) > 4096:
        raise ValueError("formal image pin document exceeds its bound")
    try:
        return pin_identities(json.loads(raw, object_pairs_hook=unique_object))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("invalid formal image pin JSON") from error


def write_formal_pins(root, current, rollback, pending):
    """Atomically publish pins (0600); caller must hold image_lock(root).

    Validate IDs here; retain separately checks their exact repository inventory.
    Never remove the file when a release fails: keep its protected identities.
    """
    document = {"version": 1, "current": current, "rollback": rollback, "pending": pending}
    pin_identities(document)
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError("formal image pin root is not a directory")
    formal = root / "formal"
    private_directory(formal)
    reports = formal / "reports"
    private_directory(reports)
    target = reports / "image-pins.json"
    if target.is_symlink():
        raise ValueError("formal image pin file must not be a symlink")
    descriptor, name = tempfile.mkstemp(prefix=".image-pins-", dir=reports)
    staged = Path(name)
    try:
        with os.fdopen(descriptor, "w") as output:
            os.fchmod(output.fileno(), 0o600)
            json.dump(document, output, sort_keys=True)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(staged, target)
        directory = os.open(reports, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        staged.unlink(missing_ok=True)


def docker(*args):
    result = subprocess.run(["docker", *args], capture_output=True, text=True, timeout=30)
    if result.returncode:
        # Do not log arbitrary inspect output (which could include runtime secrets).
        raise ValueError(f"docker {args[0]} failed (exit {result.returncode})")
    return result.stdout


def inspect(kind, reference):
    rows = json.loads(docker("inspect", f"--type={kind}", reference))
    if not isinstance(rows, list) or len(rows) != 1:
        raise ValueError("invalid docker inspect result")
    return rows[0]


def image_id(value):
    if not isinstance(value, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", value):
        raise ValueError("invalid Docker image ID")
    return value


def foreign_alias(row):
    return (any(not tag.startswith(REPOSITORY + ":") for tag in row.get("RepoTags") or [])
            or any(not digest.startswith(REPOSITORY + "@") for digest in row.get("RepoDigests") or []))


def container_images():
    protected = set()
    for project in PROJECTS:
        ids = docker("ps", "-aq", "--filter", f"label=com.docker.compose.project={project}").split()
        for container in ids:
            row = inspect("container", container)
            if row["Config"].get("Labels", {}).get("com.docker.compose.project") != project:
                raise ValueError("container project changed; cleanup skipped")
            protected.add(image_id(row["Image"]))
    running = inspect("container", RUNNING_CONTAINER)
    labels = running["Config"].get("Labels", {})
    if (labels.get("com.docker.compose.project") != RUNNING_PROJECT
            or labels.get("com.docker.compose.service") != "art-ai-go"
            or not running["State"]["Running"]):
        raise ValueError("Plinth service container is not running with expected project/service labels")
    protected.add(image_id(running["Image"]))
    return protected, image_id(running["Image"])


def candidates():
    ids = set()
    # Docker's repository filter can match broadly. Check exact Repository too.
    for line in docker("image", "ls", "--no-trunc", "--format", "{{json .}}", REPOSITORY).splitlines():
        row = json.loads(line)
        if row["Repository"] == REPOSITORY:
            ids.add(image_id(row["ID"]))
    images = []
    for identity in ids:
        row = inspect("image", identity)
        if image_id(row["Id"]) != identity:
            raise ValueError("image identity changed")
        tags = row.get("RepoTags") or []
        if not any(tag.startswith(REPOSITORY + ":") for tag in tags):
            raise ValueError("image repository changed; cleanup skipped")
        created = datetime.datetime.fromisoformat(row["Created"].replace("Z", "+00:00"))
        if created.tzinfo is None:
            raise ValueError("image creation time has no timezone")
        images.append((created, identity, tags, foreign_alias(row)))
    return sorted(images, key=lambda row: (row[0], row[1]), reverse=True)


def retain(rollback, apply=False):
    """Calculate/execute retention; apply callers must hold image_lock(ROOT)."""
    if not (rollback.startswith(REPOSITORY + ":") or re.fullmatch(r"sha256:[0-9a-f]{64}", rollback)):
        raise ValueError("rollback must be an art-ai-go-production tag or full image ID")
    previous = inspect("image", rollback)
    previous_id = image_id(previous["Id"])
    if not any(tag.startswith(REPOSITORY + ":") for tag in previous.get("RepoTags") or []):
        raise ValueError("rollback image is not in the Plinth repository")
    protected, running = container_images()
    protected.add(previous_id)
    formal_pins = formal_image_pins(ROOT)
    images = candidates()
    identities = {row[1] for row in images}
    if not {running, previous_id}.issubset(identities):
        raise ValueError("running/rollback image missing from exact repository inventory")
    if not formal_pins.issubset(identities):
        raise ValueError("formal image pin missing from exact repository inventory")
    newest = {row[1] for row in images[:KEEP]}
    print(f"[retention] {'APPLY' if apply else 'DRY-RUN'} repository={REPOSITORY} images={len(images)} newest={KEEP}")
    for _, identity, tags, foreign in images:
        reasons = []
        if identity in newest:
            reasons.append("newest-five" if KEEP == 5 else f"newest-{KEEP}")
        if identity in protected:
            reasons.append("running/rollback/container-reference")
        if identity in formal_pins:
            reasons.append("formal-image-pin")
        # ID-based removal must not affect aliases in other repositories.
        if foreign:
            reasons.append("other-repository-alias")
        if reasons:
            print(f"KEEP {identity} {','.join(tags)} ({';'.join(reasons)})")
            continue
        if not apply:
            print(f"WOULD REMOVE {identity} {','.join(tags)}")
            continue
        # Refresh pins and references under the shared lock immediately before rmi.
        fresh, _ = container_images()
        fresh_pins = formal_image_pins(ROOT)
        if not fresh_pins.issubset({row[1] for row in candidates()}):
            raise ValueError("formal image pin missing from exact repository inventory")
        current = inspect("image", identity)
        if image_id(current["Id"]) != identity:
            raise ValueError("image identity changed")
        current_tags = current.get("RepoTags") or []
        if identity in fresh_pins:
            print(f"KEEP {identity} (formal-image-pin refreshed)")
            continue
        if identity in fresh or not current_tags or foreign_alias(current):
            print(f"KEEP {identity} (reference/repository changed)")
            continue
        try:
            docker("rmi", identity)
            print(f"REMOVED {identity} {','.join(tags)}")
        except (ValueError, subprocess.TimeoutExpired) as error:
            # Multi-tag images / concurrent container references can refuse rmi.
            print(f"[retention] WARNING: not removed {identity}: {error}", file=sys.stderr)
    return 0


def configure_stack(root, project, keep, protect=()):
    """Name the running project and every other project whose containers keep images.

    Validates everything before changing the module settings."""
    global ROOT, PROJECTS, RUNNING_PROJECT, RUNNING_CONTAINER, KEEP
    if root is not None and not os.path.isabs(root):
        raise ValueError("retention root must be an absolute path")
    for name in (project, *protect):
        if name is not None and not COMPOSE_PROJECT.fullmatch(name):
            raise ValueError("invalid Compose project name")
    if protect and project is None:
        raise ValueError("--protect-project needs --project")
    if keep is not None and keep < 1:
        raise ValueError("--keep must protect at least the running image")
    if (ROOT if root is None else root) is None:
        raise ValueError("--root is required")
    if (RUNNING_PROJECT if project is None else project) is None:
        raise ValueError("--project is required")
    if root is not None:
        ROOT = Path(root)
    if project is not None:
        PROJECTS = tuple(dict.fromkeys((project, *protect)))
        RUNNING_PROJECT = project
        RUNNING_CONTAINER = project + "-art-ai-go-1"
    if keep is not None:
        KEEP = keep


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rollback-image", required=True, help="actual previous release tag or full image ID")
    parser.add_argument("--root", help="deployment root holding releases/ and formal/ (required)")
    parser.add_argument("--project", help="the running Plinth Compose project (required)")
    parser.add_argument("--protect-project", action="append", default=[],
                        help="another Compose project on this host whose container images are kept (repeatable)")
    parser.add_argument("--keep", type=int, help="newest distinct image IDs to keep (default 5)")
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--dry-run", action="store_true", help="print plan only (default)")
    modes.add_argument("--apply", action="store_true", help="remove unprotected images with docker rmi, no force")
    args = parser.parse_args()
    try:
        configure_stack(args.root, args.project, args.keep, tuple(args.protect_project))
        if args.apply:
            with image_lock(ROOT):
                return retain(args.rollback_image, True)
        return retain(args.rollback_image)
    except (ValueError, KeyError, TypeError, OSError, subprocess.TimeoutExpired) as error:
        print(f"[retention] WARNING: cleanup stopped: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
