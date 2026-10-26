#!/usr/bin/env python3
"""Read-only, fail-closed capacity checks for the ordinary Plinth release.

This measures headroom; it does not reserve it against concurrent host writes.
GNU du/df and the local Docker daemon are required. No cleanup or skip switch.
"""
import json
import os
from pathlib import Path
import posixpath
import selectors
import signal
import stat
import subprocess
import sys
import tarfile
import time

LIMIT = (1 << 63) - 1
HEADROOM = 1 << 30
HEADROOM_INODES = 4096
BUILD_HEADROOM = 8 << 30
BUILD_INODES = 1_000_000
MAX_ENTRIES = 1_000_000
MAX_DEPTH = 128
MAX_PATH = 4096


class CapacityError(Exception):
    pass


def number(value):
    text = str(value)
    if not text.isascii() or not text.isdecimal() or len(text) > 19:
        raise CapacityError("invalid capacity measurement")
    value = int(text)
    if value > LIMIT:
        raise CapacityError("capacity arithmetic overflow")
    return value


def add(*values):
    total = 0
    for value in values:
        total += number(value)
        if total > LIMIT:
            raise CapacityError("capacity arithmetic overflow")
    return total


def multiply(value, factor):
    value, factor = number(value), number(factor)
    if factor and value > LIMIT // factor:
        raise CapacityError("capacity arithmetic overflow")
    return value * factor


def command(*args):
    process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, start_new_session=True,
                               env={**os.environ, "LC_ALL": "C", "LANG": "C"})
    output = bytearray()
    deadline = time.monotonic() + 10
    completed = False
    try:
        with selectors.DefaultSelector() as pending:
            pending.register(process.stdout, selectors.EVENT_READ)
            while pending.get_map():
                left = deadline - time.monotonic()
                if left <= 0 or not pending.select(left):
                    raise CapacityError("capacity measurement timed out")
                chunk = os.read(process.stdout.fileno(), min(8192, 65537 - len(output)))
                if not chunk:
                    pending.unregister(process.stdout)
                output.extend(chunk)
                if len(output) > 65536:
                    raise CapacityError("capacity measurement output exceeded its bound")
        process.wait(timeout=max(0, deadline - time.monotonic()))
        if process.returncode:
            raise CapacityError("capacity measurement failed")
        completed = True
        return output.decode(sys.getfilesystemencoding(), "surrogateescape")
    except subprocess.TimeoutExpired:
        raise CapacityError("capacity measurement timed out") from None
    finally:
        if not completed:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        process.stdout.close()
        process.wait()


def absolute(value):
    path = Path(value)
    if not path.is_absolute() or any(c in value for c in "\n\r\x00") or len(os.fsencode(value)) > MAX_PATH:
        raise CapacityError("invalid capacity path")
    return path


def ancestor(path):
    while not path.exists():
        if path.is_symlink() or path == path.parent:
            raise CapacityError("capacity path is unavailable")
        path = path.parent
    return (path if path.is_dir() else path.parent).resolve(strict=True)


def tree(path, apparent=True):
    if not path.is_dir() or path.is_symlink():
        raise CapacityError("backup or sample directory is unavailable")
    size = 0
    if apparent:
        output = command("du", "--summarize", "--apparent-size", "--count-links", "--block-size=1", "--", str(path))
        fields = output.removesuffix("\n").split("\t")
        if len(fields) != 2 or fields[1] != str(path):
            raise CapacityError("invalid apparent-size measurement")
        size = number(fields[0])
    count, largest = 1, 0
    stack = [(path, 0)]
    started = time.monotonic()
    while stack:
        directory, depth = stack.pop()
        if depth > MAX_DEPTH or time.monotonic() - started > 20:
            raise CapacityError("capacity tree measurement exceeded its bound")
        with os.scandir(directory) as entries:
            for entry in entries:
                count += 1
                if count > MAX_ENTRIES or len(os.fsencode(entry.path)) > MAX_PATH:
                    raise CapacityError("capacity tree measurement exceeded its bound")
                info = entry.stat(follow_symlinks=False)
                if stat.S_ISDIR(info.st_mode):
                    stack.append((Path(entry.path), depth + 1))
                elif stat.S_ISREG(info.st_mode):
                    largest = max(largest, number(info.st_size))
                elif not stat.S_ISLNK(info.st_mode):
                    raise CapacityError("unsupported backup tree entry")
    return size, count, largest


def archive_size(path, target, block):
    """Read bounded tar headers, seek over payload, never cache file content."""
    if not path.is_file() or path.is_symlink():
        raise CapacityError("rollback archive is unavailable")
    length = number(path.stat().st_size)
    if length % 512:
        raise CapacityError("invalid rollback archive framing")
    restored, count, headers, metadata_bytes, name_bytes = 0, 0, 0, 0, 0
    directories, names, files, pending = set(), set(), set(), {}
    extended, offset = 0, 0

    def safe(name):
        if not name or name.startswith("/") or ".." in name.split("/") or len(os.fsencode(name)) > MAX_PATH:
            raise CapacityError("unsafe rollback archive path")
        normalized = posixpath.normpath(name)
        if normalized != target and not normalized.startswith(target + "/"):
            raise CapacityError("unexpected rollback archive root")
        return normalized

    with path.open("rb") as raw:
        while True:
            raw.seek(offset)
            header = raw.read(512)
            if len(header) != 512:
                raise CapacityError("invalid rollback archive framing")
            if header == bytes(512):
                # Require two end records and consume all bounded padding.
                left = length - raw.tell()
                if extended or pending or left < 512 or left > 65536 or raw.read(left).strip(b"\0"):
                    raise CapacityError("invalid rollback archive end records")
                break
            headers = add(headers, 1)
            if headers > MAX_ENTRIES:
                raise CapacityError("archive header count exceeded its bound")
            # Public stdlib header decoder verifies checksum and numeric wire
            # fields. Explicit framing avoids version-specific TarFile caches.
            member = tarfile.TarInfo.frombuf(header, "utf-8", "surrogateescape")
            number(member.size)
            if member.type in (tarfile.GNUTYPE_LONGNAME, tarfile.GNUTYPE_LONGLINK, tarfile.XHDTYPE):
                extended += 1
                metadata_bytes = add(metadata_bytes, member.size)
                if extended > 4 or member.size > 65536 or metadata_bytes > 64 << 20:
                    raise CapacityError("archive metadata exceeded its bound")
                payload = raw.read(member.size)
                if len(payload) != member.size:
                    raise CapacityError("invalid rollback archive metadata framing")
                if member.type == tarfile.XHDTYPE:
                    at = 0
                    while at < len(payload):
                        space = payload.find(b" ", at, at + 8)
                        if space < 0:
                            raise CapacityError("invalid rollback PAX record")
                        size = number(payload[at:space].decode("ascii"))
                        finish = add(at, size)
                        if finish <= space + 1 or finish > len(payload) or payload[finish - 1] != 10:
                            raise CapacityError("invalid rollback PAX record")
                        fields = payload[space + 1:finish - 1].split(b"=", 1)
                        if len(fields) != 2:
                            raise CapacityError("invalid rollback PAX record")
                        key = fields[0].decode("ascii")
                        if key not in ("path", "linkpath", "size", "uid", "gid", "uname", "gname", "mtime", "atime", "ctime", "hdrcharset") or key in pending:
                            raise CapacityError("unsupported rollback archive metadata")
                        pending[key] = fields[1].decode("utf-8", "surrogateescape")
                        at = finish
                else:
                    key = "path" if member.type == tarfile.GNUTYPE_LONGNAME else "linkpath"
                    if not payload.endswith(b"\0") or b"\0" in payload[:-1] or key in pending:
                        raise CapacityError("invalid rollback longname record")
                    pending[key] = payload[:-1].decode("utf-8", "surrogateescape")
            else:
                extended = 0
                if member.type not in (tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE, tarfile.SYMTYPE, tarfile.LNKTYPE):
                    raise CapacityError("unsupported rollback archive entry")
                member.name = pending.get("path", member.name)
                member.linkname = pending.get("linkpath", member.linkname)
                member.size = number(pending.get("size", member.size))
                pending.clear()
                name = safe(member.name)
                name_bytes = add(name_bytes, len(os.fsencode(name)))
                if name_bytes > 64 << 20 or name in names:
                    raise CapacityError("archive path inventory exceeded its bound")
                if name != target and posixpath.dirname(name) not in directories:
                    raise CapacityError("archive parent directory is missing or unsafe")
                if member.issym():
                    safe(posixpath.join(posixpath.dirname(name), member.linkname))
                if member.islnk() and safe(member.linkname) not in files:
                    raise CapacityError("archive hardlink source is missing")
                names.add(name)
                if member.isdir():
                    directories.add(name)
                if member.isfile() or member.islnk():
                    files.add(name)
                if not member.isfile() and member.size:
                    raise CapacityError("invalid rollback archive entry length")
                count = add(count, 1)
                restored = add(restored, member.size, max(16384, multiply(block, 2)))
            padded = multiply(add(member.size, 511) // 512, 512)
            offset = add(offset, 512, padded)
            if offset > length - 1024:
                raise CapacityError("invalid rollback archive payload length")
    if target not in directories:
        raise CapacityError("rollback archive root is missing")
    return restored, count


class Capacity:
    def __init__(self):
        self.devices = {}
        self.paths = {}

    def filesystem(self, path):
        probe = ancestor(path)
        if probe in self.paths:
            return self.paths[probe]
        info, usage = probe.stat(), os.statvfs(probe)
        def available(field, header):
            output = command("df", "--output=" + field, "--block-size=1", "--", str(probe)).split()
            if len(output) != 2 or output[0] != header:
                raise CapacityError("invalid filesystem availability measurement")
            return number(output[1])
        block = number(usage.f_frsize)
        if not block:
            raise CapacityError("invalid filesystem allocation unit")
        bytes_left = min(available("avail", "Avail"), multiply(usage.f_bavail, block))
        inodes_left = min(available("iavail", "IFree"), number(usage.f_favail))
        device = number(info.st_dev)
        if device not in self.devices:
            if len(self.devices) >= 64:
                raise CapacityError("too many release filesystems")
            self.devices[device] = {"bytes": bytes_left, "inodes": inodes_left,
                                    "need_bytes": HEADROOM, "need_inodes": HEADROOM_INODES, "block": block}
        fs = self.devices[device]
        # Different paths may be bind mounts or observe concurrent writes.
        # Never use a more optimistic measurement for the same device.
        fs["bytes"] = min(fs["bytes"], bytes_left)
        fs["inodes"] = min(fs["inodes"], inodes_left)
        fs["block"] = max(fs["block"], block)
        self.paths[probe] = device
        return device

    def require(self, path, byte_count=0, inode_count=0):
        device = self.filesystem(path)
        fs = self.devices[device]
        fs["need_bytes"] = add(fs["need_bytes"], byte_count)
        fs["need_inodes"] = add(fs["need_inodes"], inode_count)
        return device

    def verify(self):
        for device, fs in self.devices.items():
            print("[capacity] device=%s available_bytes=%s required_bytes=%s available_inodes=%s required_inodes=%s" %
                  (device, fs["bytes"], fs["need_bytes"], fs["inodes"], fs["need_inodes"]))
            if fs["bytes"] < fs["need_bytes"] or fs["inodes"] < fs["need_inodes"]:
                raise CapacityError("insufficient unreserved bytes or inodes")


def require_build(capacity, root, docker_root):
    context_size, context_entries, _ = tree(root / "app")
    block = capacity.devices[capacity.filesystem(docker_root)]["block"]
    context = add(context_size, multiply(context_entries, block))
    capacity.require(docker_root, add(BUILD_HEADROOM, multiply(context, 2)), add(BUILD_INODES, multiply(context_entries, 2)))
    temporary = absolute(os.environ.get("TMPDIR", "/tmp"))
    if not temporary.is_dir():
        raise CapacityError("preflight temporary directory is unavailable")
    return temporary


def check(root, container, phase, release_backup, scope="full"):
    if phase not in ("before-build", "before-stop", "before-backup", "before-up", "restore", "bootstrap"):
        raise CapacityError("invalid release capacity phase")
    if scope not in ("full", "database"):
        raise CapacityError("invalid release backup scope")
    targets = ("data",) if scope == "database" else ("data", "storage-go", "web-data")
    capacity = Capacity()
    production, backups = root / "production", root / "backups"
    if release_backup.parent.resolve() != backups.resolve():
        raise CapacityError("rollback archive path differs from release backups")
    for path in (root, root / "app", production, backups, production / "reports/release-failures", root / "releases"):
        capacity.require(path)
    docker_root = absolute(command("docker", "info", "--format", "{{.DockerRootDir}}").strip())
    if not docker_root.is_dir():
        raise CapacityError("local Docker data directory is unavailable")
    capacity.require(docker_root)
    if phase == "bootstrap":
        # First deployment of an empty stack: no container, data or archive to
        # measure yet. Only the build, the empty preflight sample and headroom.
        temporary = require_build(capacity, root, docker_root)
        capacity.require(temporary, multiply(64, capacity.devices[capacity.filesystem(temporary)]["block"]), 64)
        capacity.verify()
        return
    mounts = json.loads(command("docker", "inspect", "--format", "{{json .Mounts}}", container))
    if not isinstance(mounts, list) or len(mounts) > 64:
        raise CapacityError("invalid Docker mount measurement")
    destinations = {}
    for mount in mounts:
        destination = str(absolute(mount["Destination"]))
        if destination in destinations or type(mount["RW"]) is not bool:
            raise CapacityError("invalid Docker mount measurement")
        if mount["Type"] == "tmpfs":
            if mount["Source"]:
                raise CapacityError("invalid Docker tmpfs measurement")
            continue  # In-memory mount; it does not consume host disk capacity.
        if mount["Type"] not in ("bind", "volume"):
            raise CapacityError("unsupported Docker mount measurement")
        source = absolute(mount["Source"])
        if not source.exists():
            raise CapacityError("Docker mount source is unavailable")
        destinations[destination] = source
        if mount["RW"]:
            capacity.require(source)
    for destination, target in (("/app/data", "data"), ("/app/storage", "storage-go"), ("/app/web-data", "web-data")):
        if destination not in destinations or destinations[destination].resolve() != (production / target).resolve():
            raise CapacityError("active mount differs from the release backup source")
    if phase == "before-build":
        temporary = require_build(capacity, root, docker_root)
        sample_bytes = 0
        for name in ("generated", "cache", "thumbnails", "assets"):
            source = destinations.get("/app/legacy-" + name)
            if source is None:
                raise CapacityError("preflight sample source is unavailable")
            _, _, largest = tree(source, apparent=False)
            sample_bytes = add(sample_bytes, largest)
        capacity.require(temporary, add(sample_bytes, multiply(64, capacity.devices[capacity.filesystem(temporary)]["block"])), 64)
    backup_device = capacity.filesystem(backups)
    archive_bytes, entries = 0, 0
    sources = []
    for name in targets:
        path = production / name
        block = capacity.devices[backup_device]["block"]
        if phase in ("before-up", "restore"):
            size, count = archive_size(release_backup / (name + ".tar"), name, block)
            estimate = size
        else:
            size, count, _ = tree(path)
            # At most 4096-byte names: longname/link headers, 512-byte content
            # padding, and allocation fit this per-entry bound. Sparse files
            # count their full apparent size. Each tar has end-record padding.
            estimate = add(size, multiply(count, max(16384, multiply(block, 2))), 65536)
        archive_bytes, entries = add(archive_bytes, estimate), add(entries, count)
        sources.append((path, size, count))
    copies = 1 if phase in ("before-up", "restore") else 2
    capacity.require(backups, multiply(archive_bytes, copies), add(entries, 16))
    for path, size, count in sources:
        device = capacity.filesystem(path)
        if device != backup_device:
            # Cross-filesystem mv copies before removing the restore staging
            # files. Its destination therefore needs a separate full copy.
            block = capacity.devices[device]["block"]
            capacity.require(path, add(size, multiply(count, max(16384, multiply(block, 2)))), count)
    capacity.verify()


def main():
    signal.signal(signal.SIGALRM, lambda *_: (_ for _ in ()).throw(CapacityError("capacity measurement deadline exceeded")))
    signal.alarm(60)
    try:
        if len(sys.argv) not in (5, 6):
            raise CapacityError("expected root, container (or - for bootstrap), phase, release archive path and optional backup scope")
        check(absolute(sys.argv[1]), sys.argv[2], sys.argv[3], absolute(sys.argv[4]),
              sys.argv[5] if len(sys.argv) == 6 else "full")
    except (CapacityError, OSError, ValueError, KeyError, TypeError, tarfile.TarError) as error:
        # Measurement diagnostics contain no Docker env/config or file payload.
        message = str(error) if isinstance(error, CapacityError) else "capacity measurement unavailable"
        print("[capacity] refused: " + message, file=sys.stderr)
        return 1
    finally:
        signal.alarm(0)
    return 0


if __name__ == "__main__":
    sys.exit(main())
