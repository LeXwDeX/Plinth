#!/usr/bin/env python3
"""Verify a supplied source baseline against a consistent, WAL-aware snapshot."""
import argparse
from contextlib import closing
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate baseline field")
        result[key] = value
    return result


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def snapshot_sha256(database):
    database = Path(database)
    if database.is_symlink() or not database.is_file():
        raise ValueError("source database must be a regular file")
    with tempfile.TemporaryDirectory(prefix="art-ai-source-check-") as temporary:
        target = Path(temporary) / "snapshot.db"
        deadline = time.monotonic() + 60

        def sqlite(*arguments):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ValueError("consistent source snapshot timed out")
            try:
                result = subprocess.run(
                    ["sqlite3", "-cmd", f".timeout {max(1, int(remaining * 1000))}", *arguments], cwd=temporary,
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    timeout=remaining, check=False,
                )
            except subprocess.TimeoutExpired:
                raise ValueError("consistent source snapshot timed out") from None
            if result.returncode:
                raise ValueError("consistent source snapshot failed")
            return result.stdout.strip()

        # A raw hash of an active main file misses committed WAL pages. Match
        # backup-go-production.sh: the same SQLite CLI online backup, then
        # DELETE journal. A different library version changes header bytes even
        # when all rows match. Use a fixed private target to avoid dot-command
        # filename interpolation; the source path is a separate argv element.
        sqlite("-readonly", str(database.resolve()), ".backup snapshot.db")
        if sqlite(str(target), "PRAGMA journal_mode=DELETE;") != b"delete":
            raise ValueError("source snapshot journal mode is invalid")
        if sqlite(str(target), "PRAGMA quick_check;") != b"ok":
            raise ValueError("source snapshot is not consistent")
        return file_sha256(target)


def verify(database, baseline, *, snapshot=False):
    path = Path(baseline)
    if path.is_symlink() or not path.is_file() or stat.S_IMODE(path.stat().st_mode) != 0o600:
        raise ValueError("source baseline must be a regular 0600 file")
    document = json.loads(path.read_text(), object_pairs_hook=unique_object)
    if not isinstance(document, dict):
        raise ValueError("source baseline must be an object")
    if document.get("format") != "art-ai-production-source-baseline-v1":
        raise ValueError("source baseline format is invalid")
    for key in ("database_snapshot_sha256", "inventory_sha256"):
        if not isinstance(document.get(key), str) or not re.fullmatch("[0-9a-f]{64}", document[key]):
            raise ValueError("source baseline digest is invalid")
    # Inventory provenance is supplied/verified by its separately authorized
    # collection workflow. This check proves only the database snapshot binding.
    if snapshot:
        database = Path(database)
        if database.is_symlink() or not database.is_file() or Path(str(database)+"-wal").exists():
            raise ValueError("final snapshot must be a standalone regular file")
        with closing(sqlite3.connect(database.resolve().as_uri()+"?mode=ro", uri=True)) as connection:
            if connection.execute("PRAGMA journal_mode").fetchone()[0] != "delete" or connection.execute("PRAGMA quick_check").fetchall() != [("ok",)]:
                raise ValueError("final snapshot must be a consistent DELETE database")
        actual = file_sha256(database)
    else:
        actual = snapshot_sha256(database)
    if actual != document["database_snapshot_sha256"]:
        raise ValueError("source database differs from the supplied baseline")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True)
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--snapshot", action="store_true", help="Verify an already isolated DELETE snapshot without re-backing it up")
    args = parser.parse_args()
    try:
        verify(args.database, args.baseline, snapshot=args.snapshot)
    except (ValueError, OSError, sqlite3.Error):
        print("source baseline verification failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
