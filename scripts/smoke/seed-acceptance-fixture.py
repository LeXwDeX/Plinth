#!/usr/bin/env python3
"""Copy a quiescent Plinth database and selected genuine source assets.

Read-only/dry-run by default. The copy retains actual Task/Attempt/Grant rows;
it never manufactures lifecycle state. The database is private evidence and
must not be included in a sanitized report archive.
"""

from contextlib import closing
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import sqlite3
import sys


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def inspect(database, storage, sources, max_bytes):
    guards = [
        ("generation_tasks", "state NOT IN ('SUCCEEDED','FAILED','CANCELED') OR next_action_at IS NOT NULL"),
        ("processing_plans", "state NOT IN ('SUCCEEDED','FAILED')"),
        ("go_chat_turns", "state NOT IN ('done','error','stopped')"),
    ]
    for table, predicate in guards:
        count = database.execute(f"SELECT COUNT(*) FROM {table} WHERE {predicate}").fetchone()[0]
        if count:
            raise ValueError(f"snapshot contains {count} active rows in {table}")
    selected, files = [], {}
    for task_id in sources:
        row = database.execute(
            "SELECT owner_id,session_id,state,offering_json FROM generation_tasks WHERE id=?",
            (task_id,),
        ).fetchone()
        if not row or row[2] != "SUCCEEDED":
            raise ValueError(f"source {task_id} is not a persisted successful Task")
        offering = json.loads(row[3])
        assets = []
        for asset in database.execute(
            "SELECT id,storage_key,size_bytes,sha256,role,variant FROM generation_assets WHERE task_id=?",
            (task_id,),
        ):
            asset_id, key, size, expected, role, variant = asset
            relative = PurePosixPath(key)
            if not key or relative.is_absolute() or ".." in relative.parts or "\\" in key:
                raise ValueError(f"source asset {asset_id} has an invalid storage key")
            path = (storage / key).resolve()
            if not path.is_relative_to(storage) or not path.is_file():
                raise ValueError(f"source asset {asset_id} has an invalid storage path")
            if not expected or path.stat().st_size != size or digest(path) != expected:
                raise ValueError(f"source asset {asset_id} byte identity mismatch")
            previous = files.get(key)
            if previous and previous[1:] != (size, expected):
                raise ValueError("conflicting asset metadata for one storage key")
            files[key] = (path, size, expected)
            assets.append({"id": asset_id, "role": role, "variant": variant,
                           "bytes": size, "sha256": expected})
        if not assets:
            raise ValueError(f"source {task_id} has no materialized assets")
        selected.append({"task_id": task_id, "owner_id": row[0], "session_id": row[1],
                         "offering_id": offering["id"], "assets": assets})
    total = sum(size for _, size, _ in files.values())
    if total > max_bytes:
        raise ValueError(f"selected source assets exceed the {max_bytes}-byte limit")
    return {"sources": selected, "asset_files": len(files), "asset_bytes": total,
            "task_count": database.execute("SELECT COUNT(*) FROM generation_tasks").fetchone()[0],
            "pending_grants": database.execute(
                "SELECT COUNT(*) FROM execution_grants WHERE status='pending'"
            ).fetchone()[0]}, files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-db", type=Path, required=True)
    parser.add_argument("--storage-root", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--source-task", action="append", required=True)
    parser.add_argument("--max-asset-bytes", type=int, default=2 * 1024**3)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    source, storage, destination = (p.resolve() for p in
                                    (args.source_db, args.storage_root, args.destination))
    if not source.is_file() or not storage.is_dir() or args.max_asset_bytes <= 0:
        parser.error("existing database/storage and a positive byte bound are required")
    if destination.exists() or destination.is_relative_to(storage) or storage.is_relative_to(destination):
        parser.error("destination must be new and separate from the source storage")
    if source.is_relative_to(destination) or destination.is_relative_to(source.parent):
        parser.error("destination must be separate from the source database directory")
    sources = list(dict.fromkeys(args.source_task))
    if len(sources) > 5:
        parser.error("at most five existing source Tasks may be copied")
    os.umask(0o077)
    with closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True)) as live:
        live.execute("PRAGMA query_only=ON")
        if not args.apply:
            live.execute("BEGIN")
            report, _ = inspect(live, storage, sources, args.max_asset_bytes)
            report["mode"] = "dry-run"
            print(json.dumps(report, ensure_ascii=False))
            return
        # SQLite backup establishes a consistent snapshot. Recheck that exact
        # snapshot, not an earlier read of the changing source database.
        destination.mkdir(mode=0o700, parents=False)
        copied_db = destination / "acceptance.db"
        try:
            with closing(sqlite3.connect(copied_db)) as isolated:
                live.backup(isolated)
                report, files = inspect(isolated, storage, sources, args.max_asset_bytes)
            for key, (path, _, expected) in files.items():
                assets_root = (destination / "assets").resolve()
                target = (assets_root / key).resolve()
                if not target.is_relative_to(assets_root):
                    raise ValueError("copied asset escaped the private storage root")
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                # A real copy prevents either process from changing the other's
                # bytes; do not use hard links to the running asset store.
                shutil.copyfile(path, target)
                if digest(target) != expected:
                    raise ValueError("copied source asset byte identity mismatch")
            report.update({"mode": "applied", "database_sha256": digest(copied_db),
                           "database_is_private": True,
                           "lifecycle_rows_modified": False})
            (destination / "seed-report.json").write_text(
                json.dumps(report, ensure_ascii=False, indent=2) + "\n")
            print(json.dumps(report, ensure_ascii=False))
        except Exception:
            # Preserve the incomplete private copy for diagnosis, never run it.
            (destination / "SEED_FAILED").write_text("Do not start this incomplete fixture.\n")
            raise


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, sqlite3.Error) as error:
        print(f"fixture seed failed: {error}", file=sys.stderr)
        sys.exit(1)
