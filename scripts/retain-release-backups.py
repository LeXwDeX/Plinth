#!/usr/bin/env python3
"""Retain the newest release backups of one Plinth stack.

Read-only unless --apply. Every run names the deployment --root, the running
Compose --project and the previous image tag (for the record). Only
<root>/backups/release-<tag> directories are considered. Failed snapshots are
ordinary candidates: the newest --keep survive, older ones are removed once a
success manifest or failure report proves the attempt finished.

Safety: holds the per-root release lock shared with deployment backup/restore
and manual recovery, never follows symlinks, refuses any mount point inside a
snapshot tree (including same-device bind mounts), refuses when current
release, manifest, mounts or a backup tree do not match, and reports partial
deletions with a nonzero exit. A missing, partial or zero-byte archive is
deletable only when an independent finished proof exists: a success manifest
(which requires complete nonempty archives) or a failure report. Restore
validity is a separate question; a failed snapshot is not a restore point.
"""
import argparse
import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
from typing import NoReturn

IMAGE = "art-ai-go-production"
SERVICE = "art-ai-go"
PROJECT_RE = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
TAG_RE = re.compile(r"([0-9a-f]{7,40})-(\d{8}T\d{6}Z)")
DOCKER_TAG_RE = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}")
SHA_RE = re.compile(r"[0-9a-f]{40}")
UTC_RE = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z")
REPORT_RE = re.compile(r"(?P<tag>[0-9a-f]{7,40}-\d{8}T\d{6}Z)-(?P<stage>[a-z0-9][a-z0-9-]{0,63})\.txt")
ARCHIVES = ("data.tar", "storage-go.tar", "web-data.tar")
DESTINATIONS = (("/app/data", "data"), ("/app/storage", "storage-go"), ("/app/web-data", "web-data"))
MAX_MANIFEST = 1 << 20
MAX_SCOPE = 512
MAX_REPORT = 4096
MAX_ENTRIES = 100_000
MAX_DEPTH = 16


class RetentionError(ValueError):
    """A fixed, credential-free refusal reason."""


def fail(message: str) -> NoReturn:
    raise RetentionError(message)


def load_lock():
    path = Path(__file__).resolve().with_name("release-lock.py")
    try:
        spec = importlib.util.spec_from_file_location("release_lock", path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
    except (OSError, AttributeError, ImportError):
        fail("release lock helper is unavailable")
    return module


LOCK = load_lock()


def release_tag(value):
    match = TAG_RE.fullmatch(value) if isinstance(value, str) else None
    if not match:
        fail("invalid release tag")
    try:
        datetime.datetime.strptime(match.group(2), "%Y%m%dT%H%M%SZ")
    except ValueError:
        fail("invalid release tag timestamp")
    return value


def docker(*args):
    try:
        result = subprocess.run(["docker", *args], capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        fail("docker inspect failed")
    if result.returncode:
        fail("docker inspect failed")
    return result.stdout


def regular_bytes(path, limit, label):
    """Read a bounded regular file without following a symlink."""
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError:
        fail(f"{label} is not a readable regular file")
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
            fail(f"{label} must be a bounded regular file")
        with os.fdopen(descriptor, "rb", closefd=False) as source:
            raw = source.read(limit + 1)
    finally:
        os.close(descriptor)
    if len(raw) > limit:
        fail(f"{label} exceeds its bound")
    try:
        return raw.decode("utf-8")
    except UnicodeDecodeError:
        fail(f"{label} is not UTF-8")


def read_manifest(root):
    directory = Path(root) / "releases"
    try:
        info = directory.lstat()
    except OSError:
        fail("manifest directory is unavailable")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("manifest directory is unsafe")
    path = directory / "manifest.txt"
    text = regular_bytes(path, MAX_MANIFEST, "manifest")
    records = []
    revisions = {}
    for line in text.splitlines():
        fields = line.split(" ")
        if len(fields) != 3 or not all(fields):
            fail("malformed manifest record")
        tag, sha, utc = fields
        release_tag(tag)
        if not SHA_RE.fullmatch(sha) or not sha.startswith(tag.split("-", 1)[0]):
            fail("malformed manifest revision")
        if not UTC_RE.fullmatch(utc):
            fail("malformed manifest timestamp")
        try:
            datetime.datetime.strptime(utc, "%Y-%m-%dT%H:%M:%SZ")
        except ValueError:
            fail("invalid manifest timestamp")
        # The history is append-only and may repeat a successful release with
        # a different success timestamp. Keep every row and the byte digest;
        # the same tag may never name a different revision.
        if tag in revisions and revisions[tag] != sha:
            fail("manifest tag has conflicting revisions")
        revisions[tag] = sha
        records.append((tag, sha, utc))
    if not records:
        fail("manifest needs at least one release record")
    try:
        before = path.lstat()
    except OSError:
        fail("manifest changed while reading")
    return records, hashlib.sha256(text.encode()).hexdigest(), (before.st_dev, before.st_ino)


def read_scope(path):
    text = regular_bytes(path, MAX_SCOPE, "backup scope")
    lines = text.splitlines()
    if len(lines) == 1 and lines[0] in ("database", "full"):
        return ("data.tar",) if lines[0] == "database" else ARCHIVES
    fail("backup scope is malformed")


def reports(root, tags):
    """Finished-failure reports for the candidate backup tags only.

    Failure reports are independent diagnostics and outlive the snapshots they
    describe. Reading every historical body would let an unrelated old format
    block retention, so entries whose parsed tag is not part of this operation
    are ignored without reading their bytes. A matching entry is still checked
    for type, safety and its own tag line; the filename alone is never proof.
    """
    production = Path(root) / "production"
    try:
        info = production.lstat()
    except FileNotFoundError:
        return set()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("failure report directory is unsafe")
    reports_dir = production / "reports"
    try:
        info = reports_dir.lstat()
    except FileNotFoundError:
        return set()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("failure report directory is unsafe")
    directory = reports_dir / "release-failures"
    try:
        info = directory.lstat()
    except FileNotFoundError:
        return set()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("failure report directory is unsafe")
    closed = set()
    with os.scandir(directory) as entries:
        for entry in entries:
            match = REPORT_RE.fullmatch(entry.name)
            if not match:
                continue
            tag = release_tag(match.group("tag"))
            if tag not in tags:
                continue
            if entry.is_symlink() or not entry.is_file(follow_symlinks=False):
                fail("failure report is unsafe")
            text = regular_bytes(Path(directory) / entry.name, MAX_REPORT, "failure report")
            named = {line.split(" ", 1)[0] for line in text.splitlines()}
            if f"tag={tag}" not in named:
                fail("failure report does not name its tag")
            closed.add(tag)
    return closed


def read_mount_points():
    """Every mount point visible to this process; deletion must stop at one.

    A mounted subtree keeps the enclosing st_dev, so a same-device bind mount
    inside a failed/restore tree can alias a live directory. Linux mountinfo is
    authoritative; a missing or malformed table fails closed.
    """
    try:
        with open("/proc/self/mountinfo", encoding="utf-8") as source:
            lines = source.read().splitlines()
    except OSError:
        fail("mount table is unavailable")
    points = set()
    for line in lines:
        fields = line.split(" ")
        if len(fields) < 5 or not fields[4]:
            fail("mount table is malformed")
        point = re.sub(r"\\([0-7]{3})", lambda match: chr(int(match.group(1), 8)), fields[4])
        if not point.startswith("/") or "\0" in point:
            fail("mount table is malformed")
        points.add(os.path.normpath(point))
    if "/" not in points:
        fail("mount table is malformed")
    return frozenset(points)


def check_mount(path, mounts):
    if os.path.normpath(path) in mounts:
        fail("backup staging path crosses a mount boundary")


def validate_tree(path, device, mounts):
    total = 0
    count = 0
    for current, directories, files in os.walk(path, followlinks=False):
        check_mount(current, mounts)
        if current[len(path):].count(os.sep) > MAX_DEPTH:
            fail("backup staging path is too deep")
        for name in directories + files:
            child_path = os.path.join(current, name)
            check_mount(child_path, mounts)
            child = os.lstat(child_path)
            if stat.S_ISLNK(child.st_mode) or child.st_dev != device:
                fail("backup staging path is unsafe")
            if not (stat.S_ISDIR(child.st_mode) or stat.S_ISREG(child.st_mode)):
                fail("backup staging path has a special file")
            count += 1
            if count > MAX_ENTRIES:
                fail("backup staging path is too large")
            if stat.S_ISREG(child.st_mode):
                total += child.st_size
    return total


def inspect_snapshot(directory, backups_device, mounts):
    check_mount(directory, mounts)
    entries = {}
    with os.scandir(directory) as scanned:
        scanned = list(scanned)
    for entry in scanned:
        if entry.is_symlink():
            fail("backup contains a symlink")
        info = entry.stat(follow_symlinks=False)
        if info.st_dev != backups_device:
            fail("backup crosses a filesystem")
        if entry.name in ("scope",) or entry.name in ARCHIVES:
            if not stat.S_ISREG(info.st_mode):
                fail("backup archive is not a regular file")
            entries[entry.name] = ("file", info.st_size, info.st_dev, info.st_ino)
        elif entry.name in ("restore", "failed"):
            if not stat.S_ISDIR(info.st_mode):
                fail(f"{entry.name} staging path is not a directory")
            if entry.name == "failed":
                with os.scandir(entry.path) as children:
                    children = list(children)
                for child in children:
                    if child.name not in ("data", "storage-go", "web-data", "compose.env"):
                        fail("failed backup layout is malformed")
                    if child.is_symlink():
                        fail("failed backup layout is unsafe")
                    child_info = child.stat(follow_symlinks=False)
                    if child.name == "compose.env":
                        if not stat.S_ISREG(child_info.st_mode):
                            fail("failed compose.env is not a regular file")
                    elif not stat.S_ISDIR(child_info.st_mode):
                        fail("failed backup layout is malformed")
            entries[entry.name] = ("dir", validate_tree(entry.path, backups_device, mounts),
                                   info.st_dev, info.st_ino)
        else:
            fail("backup contains an unknown entry")

    if "scope" in entries:
        expected = read_scope(Path(directory) / "scope")
        if any(name in entries for name in ARCHIVES if name not in expected):
            fail("backup archive does not match its scope")
        if entries.keys() - {"scope", "restore", "failed", *ARCHIVES}:
            fail("backup contains an unknown entry")
    else:
        expected = ARCHIVES
    # A finished failure may have missing, partial or zero-byte archives. Only
    # a success manifest may treat the snapshot as a restorable full backup.
    complete = all(name in entries and entries[name][1] > 0 for name in expected)
    return entries, expected, complete


def inspect_runtime(root, project):
    if not isinstance(project, str) or not PROJECT_RE.fullmatch(project):
        fail("project is invalid")
    container = f"{project}-{SERVICE}-1"
    try:
        rows = json.loads(docker("inspect", "--type=container", container))
    except (ValueError, TypeError):
        fail("invalid docker inspect result")
    if not isinstance(rows, list) or len(rows) != 1:
        fail("invalid docker inspect result")
    row = rows[0]
    if not isinstance(row, dict):
        fail("invalid docker inspect result")
    config = row.get("Config")
    state = row.get("State")
    if not isinstance(config, dict) or not isinstance(state, dict):
        fail("invalid docker inspect result")
    labels = config.get("Labels") or {}
    if not isinstance(labels, dict):
        fail("invalid docker inspect result")
    if (labels.get("com.docker.compose.project") != project
            or labels.get("com.docker.compose.service") != SERVICE
            or state.get("Running") is not True):
        fail("project or running service does not match")
    image = config.get("Image")
    if not isinstance(image, str) or not image.startswith(IMAGE + ":"):
        fail("running image is not the Plinth production image")
    tag = image[len(IMAGE) + 1:]
    release_tag(tag)
    revision = labels.get("org.opencontainers.image.revision")
    if not isinstance(revision, str) or not SHA_RE.fullmatch(revision):
        fail("running revision is missing or invalid")
    mounts = row.get("Mounts")
    if not isinstance(mounts, list):
        fail("production mount data is invalid")
    sources = {mount.get("Destination"): mount.get("Source") for mount in mounts
               if isinstance(mount, dict)}
    for destination, name in DESTINATIONS:
        expected = os.path.normpath(os.path.join(root, "production", name))
        if not isinstance(sources.get(destination), str) or os.path.normpath(sources[destination]) != expected:
            fail("production mount does not match the deployment root")
    return tag, revision


def check_root(root, mounts):
    if (not isinstance(root, str) or not root.startswith("/")
            or root.startswith("//") or os.path.normpath(root) != root or root == "/"):
        fail("root must be a normalized absolute directory")
    try:
        info = os.lstat(root)
    except OSError:
        fail("root must be a real directory")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("root must be a real directory")
    backups = Path(root) / "backups"
    try:
        backup_info = backups.lstat()
    except OSError:
        fail("backups must be a real directory")
    if stat.S_ISLNK(backup_info.st_mode) or not stat.S_ISDIR(backup_info.st_mode):
        fail("backups must be a real directory")
    if backup_info.st_dev != info.st_dev:
        fail("backups crosses a filesystem boundary")
    check_mount(str(backups), mounts)
    return backups, (backup_info.st_dev, backup_info.st_ino)


def build_plan(root, project, keep, rollback=None):
    if (not isinstance(root, str) or not root.startswith("/") or root.startswith("//")
            or "\0" in root or ".." in Path(root).parts):
        fail("root must be a normalized absolute directory")
    root = os.path.normpath(root)
    if root == "/":
        fail("root must be a normalized absolute directory")
    if type(keep) is not int or not 1 <= keep <= 2:
        fail("keep must be 1-2")
    if rollback is not None and (not isinstance(rollback, str)
                                or not rollback.startswith(IMAGE + ":")
                                or not DOCKER_TAG_RE.fullmatch(rollback[len(IMAGE) + 1:])):
        fail("rollback image is invalid")
    mounts = read_mount_points()
    backups, backups_identity = check_root(root, mounts)
    records, manifest_digest, manifest_identity = read_manifest(root)
    current, revision = inspect_runtime(root, project)
    if records[-1][0] != current:
        fail("running release does not match the success manifest")
    if records[-1][1] != revision:
        fail("running revision does not match the success manifest")
    successful = {record[0] for record in records}
    candidates = []
    preserved = []
    with os.scandir(backups) as scanned:
        directory_entries = list(scanned)
    backup_tags = set()
    for entry in directory_entries:
        match = re.fullmatch(r"release-(.+)", entry.name)
        if match and TAG_RE.fullmatch(match.group(1)):
            backup_tags.add(match.group(1))
    closed = reports(root, backup_tags)
    for entry in directory_entries:
        if entry.is_symlink():
            fail("backups contains a symlink")
        match = re.fullmatch(r"release-(.+)", entry.name)
        if not match or not TAG_RE.fullmatch(match.group(1)):
            preserved.append({"name": entry.name, "reason": "not a release backup"})
            continue
        tag = release_tag(match.group(1))
        info = entry.stat(follow_symlinks=False)
        if not stat.S_ISDIR(info.st_mode) or info.st_dev != backups_identity[0]:
            fail("release backup is not a directory on the backups filesystem")
        snapshot, expected, complete = inspect_snapshot(
            Path(entry.path), backups_identity[0], mounts)
        # Deletion eligibility needs an independent finished proof. A bare
        # failed/ tree is not proof; a success manifest requires real archives.
        if tag in successful:
            if not complete:
                fail("successful backup is missing its archives")
        elif tag not in closed:
            fail("release backup is not finalized; cleanup refused")
        if "restore" in snapshot and tag not in closed:
            fail("restore staging path is not finalized; cleanup refused")
        size = sum(value[1] for value in snapshot.values())
        candidates.append({"name": entry.name, "tag": tag, "bytes": size,
                           "entries": snapshot, "complete": complete,
                           "identity": (info.st_dev, info.st_ino)})
    candidates.sort(key=lambda item: datetime.datetime.strptime(
        TAG_RE.fullmatch(item["tag"]).group(2), "%Y%m%dT%H%M%SZ"), reverse=True)
    if current not in {item["tag"] for item in candidates}:
        fail("current release backup is missing; cleanup refused")
    if candidates[0]["tag"] != current:
        fail("current release is not the newest backup; cleanup refused")
    current_entry = next(item for item in candidates if item["tag"] == current)
    kept = candidates[:keep]
    deleting = candidates[keep:]
    for item in kept:
        item["reason"] = "current release" if item["tag"] == current else f"within newest {keep}"
    for item in deleting:
        item["reason"] = f"older than newest {keep}"
    public = lambda row: {key: row[key] for key in ("name", "tag", "bytes", "reason")}
    return {
        "mode": "dry-run",
        "root": root,
        "project": project,
        "current": current,
        "revision": revision,
        "keep": keep,
        "manifest": {"digest": manifest_digest, "identity": manifest_identity},
        "backups_identity": backups_identity,
        "candidates": [public(item) for item in kept + deleting],
        "kept": [public(item) for item in kept],
        "would_delete": [public(item) for item in deleting],
        "deleted": [],
        "preserved": preserved,
        "pending": deleting,
        "current_entry": current_entry,
    }


def unlink_checked(fd, name, expected, path, mounts):
    check_mount(path, mounts)
    kind, size, device, inode = expected
    info = os.stat(name, dir_fd=fd, follow_symlinks=False)
    if (kind != "file" or not stat.S_ISREG(info.st_mode) or info.st_size != size
            or info.st_dev != device or info.st_ino != inode):
        fail("backup changed before deletion")
    os.unlink(name, dir_fd=fd)


def remove_tree(fd, name, expected, mounts, path, depth=0):
    if depth > MAX_DEPTH:
        fail("backup staging path is too deep")
    check_mount(path, mounts)
    descriptor = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
    try:
        info = os.fstat(descriptor)
        if (not stat.S_ISDIR(info.st_mode) or info.st_dev != expected[2]
                or info.st_ino != expected[3]):
            fail("backup staging path changed before deletion")
        count = 0
        for entry in os.listdir(descriptor):
            child_path = os.path.join(path, entry)
            check_mount(child_path, mounts)
            child = os.stat(entry, dir_fd=descriptor, follow_symlinks=False)
            if child.st_dev != expected[2]:
                fail("backup staging path crosses a filesystem")
            if stat.S_ISDIR(child.st_mode):
                remove_tree(descriptor, entry, ("dir", 0, child.st_dev, child.st_ino),
                            mounts, child_path, depth + 1)
            elif stat.S_ISREG(child.st_mode):
                os.unlink(entry, dir_fd=descriptor)
            else:
                fail("backup staging path has a special file")
            count += 1
            if count > MAX_ENTRIES:
                fail("backup staging path is too large")
    finally:
        os.close(descriptor)
    check_mount(path, mounts)
    info = os.stat(name, dir_fd=fd, follow_symlinks=False)
    if info.st_dev != expected[2] or info.st_ino != expected[3]:
        fail("backup staging path changed before deletion")
    os.rmdir(name, dir_fd=fd)


def check_backups_path(backups, expected):
    try:
        info = backups.lstat()
    except OSError:
        fail("backups directory changed before deletion")
    if stat.S_ISLNK(info.st_mode) or (info.st_dev, info.st_ino) != expected:
        fail("backups directory changed before deletion")


def verify_current(fd, entry):
    try:
        directory = os.open(entry["name"], os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
    except OSError:
        fail("current release backup changed before deletion")
    try:
        info = os.fstat(directory)
        if (info.st_dev, info.st_ino) != entry["identity"]:
            fail("current release backup changed before deletion")
        if set(os.listdir(directory)) != set(entry["entries"]):
            fail("current release backup changed before deletion")
        for name, expected in entry["entries"].items():
            info = os.stat(name, dir_fd=directory, follow_symlinks=False)
            if expected[0] == "file":
                if (not stat.S_ISREG(info.st_mode) or info.st_size != expected[1]
                        or info.st_dev != expected[2] or info.st_ino != expected[3]):
                    fail("current release backup changed before deletion")
            elif not stat.S_ISDIR(info.st_mode) or info.st_dev != expected[2] or info.st_ino != expected[3]:
                fail("current release backup changed before deletion")
    finally:
        os.close(directory)


def apply_result(plan, deleted, failed, error=None):
    result = {
        "mode": "apply",
        "root": plan["root"],
        "project": plan["project"],
        "current": plan["current"],
        "revision": plan["revision"],
        "keep": plan["keep"],
        "candidates": plan["candidates"],
        "kept": plan["kept"],
        "deleted": deleted,
        "failed": failed,
        "preserved": plan["preserved"],
    }
    if error is not None:
        result["error"] = error
    return result


def execute(plan):
    if not isinstance(plan, dict) or plan.get("mode") != "dry-run":
        fail("invalid retention plan")
    with LOCK.hold(plan["root"]):
        return _execute(plan)


def _execute(plan):
    root = plan["root"]
    mounts = read_mount_points()
    if inspect_runtime(root, plan["project"]) != (plan["current"], plan["revision"]):
        fail("current release changed before deletion")
    records, digest, identity = read_manifest(root)
    if (records[-1][0] != plan["current"] or digest != plan["manifest"]["digest"]
            or identity != plan["manifest"]["identity"]):
        fail("manifest changed before deletion")
    report_tags = {item["tag"] for item in plan["pending"] + plan["kept"]}
    closed = reports(root, report_tags)
    successful = {record[0] for record in records}
    backups, backups_identity = check_root(root, mounts)
    if backups_identity != plan["backups_identity"]:
        fail("backups directory changed before deletion")
    check_backups_path(backups, backups_identity)
    fd = os.open(backups, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    deleted, failed = [], []
    active = None
    try:
        info = os.fstat(fd)
        if info.st_dev != backups_identity[0] or info.st_ino != backups_identity[1]:
            fail("backups directory changed before deletion")
        check_backups_path(backups, backups_identity)
        planned = {item["name"] for item in plan["pending"] + plan["kept"] + plan["preserved"]}
        for item in plan["pending"]:
            if item["tag"] in successful:
                if not item["complete"]:
                    fail("successful backup is missing its archives")
            elif item["tag"] not in closed:
                fail("release backup is no longer finalized; cleanup refused")
            if "restore" in item["entries"] and item["tag"] not in closed:
                fail("restore staging path is no longer finalized; cleanup refused")
            # Re-check every pending tree against the current mount table before
            # removing any file, so a mount that appeared after the plan cannot
            # be discovered only halfway through a snapshot.
            pending_path = os.path.join(root, "backups", item["name"])
            for entry_name, expected in item["entries"].items():
                entry_path = os.path.join(pending_path, entry_name)
                if expected[0] == "dir":
                    validate_tree(entry_path, backups_identity[0], mounts)
                else:
                    check_mount(entry_path, mounts)
        for name in os.listdir(fd):
            match = re.fullmatch(r"release-(.+)", name)
            if match and TAG_RE.fullmatch(match.group(1)) and name not in planned:
                fail("new release backup appeared before deletion")
        verify_current(fd, plan["current_entry"])
        try:
            for item in plan["pending"]:
                check_backups_path(backups, backups_identity)
                active = item
                name = item["name"]
                try:
                    directory = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                except OSError:
                    failed.append({"name": name, "reason": "backup changed before deletion"})
                    active = None
                    continue
                try:
                    info = os.fstat(directory)
                    if (info.st_dev, info.st_ino) != item["identity"]:
                        fail("backup changed before deletion")
                    if set(os.listdir(directory)) != set(item["entries"]):
                        fail("backup changed before deletion")
                    backup_path = os.path.join(root, "backups", name)
                    for entry_name, expected in item["entries"].items():
                        entry_path = os.path.join(backup_path, entry_name)
                        if expected[0] == "dir":
                            remove_tree(directory, entry_name, expected, mounts, entry_path)
                        else:
                            unlink_checked(directory, entry_name, expected, entry_path, mounts)
                except RetentionError:
                    raise
                except OSError:
                    failed.append({"name": name, "reason": "delete failed"})
                    active = None
                    continue
                finally:
                    os.close(directory)
                try:
                    info = os.stat(name, dir_fd=fd, follow_symlinks=False)
                    if (info.st_dev, info.st_ino) != item["identity"]:
                        fail("backup changed before deletion")
                    os.rmdir(name, dir_fd=fd)
                except OSError:
                    failed.append({"name": name, "reason": "delete failed"})
                    active = None
                    continue
                deleted.append({"name": item["name"], "tag": item["tag"],
                                "bytes": item["bytes"], "reason": item["reason"]})
                active = None
        except RetentionError as error:
            if not deleted and active is None:
                raise
            if active is not None:
                failed.append({"name": active["name"], "reason": "delete failed"})
            return apply_result(plan, deleted, failed, str(error))
        return apply_result(plan, deleted, failed)
    finally:
        os.close(fd)


def run(root, project, keep, apply=False, rollback=None):
    with LOCK.hold(root):
        plan = build_plan(root, project, keep, rollback)
        return _execute(plan) if apply else plan


def main(argv=None):
    parser = argparse.ArgumentParser(description="Retain the newest Plinth release backups; dry-run by default.")
    parser.add_argument("--root", required=True)
    parser.add_argument("--project", required=True)
    parser.add_argument("--rollback-image")
    parser.add_argument("--keep", type=int, required=True)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--apply", action="store_true")
    mode.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    try:
        result = run(args.root, args.project, args.keep, apply=args.apply,
                     rollback=args.rollback_image)
    except (RetentionError, OSError) as error:
        print(f"retain-release-backups: {error}", file=sys.stderr)
        return 1
    output = {key: value for key, value in result.items()
              if key not in ("pending", "current_entry")}
    print(json.dumps(output, indent=2, sort_keys=True))
    return 1 if result.get("failed") or "error" in result else 0


if __name__ == "__main__":
    raise SystemExit(main())
