#!/usr/bin/env python3
"""Offline guards for the private acceptance snapshot copier."""

from contextlib import closing
import hashlib
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("seed-acceptance-fixture.py")


class SeedTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "original").mkdir()
        self.db = self.root / "original/source.db"
        self.storage = self.root / "storage"
        self.storage.mkdir()
        self.asset = self.storage / "model.glb"
        self.asset.write_bytes(b"genuine fixture bytes")
        with closing(sqlite3.connect(self.db)) as conn, conn:
            conn.executescript("""
                CREATE TABLE generation_tasks(id,owner_id,session_id,state,offering_json,next_action_at);
                CREATE TABLE generation_assets(id,task_id,storage_key,size_bytes,sha256,role,variant);
                CREATE TABLE processing_plans(state);
                CREATE TABLE go_chat_turns(state);
                CREATE TABLE execution_grants(status);
            """)
            conn.execute("INSERT INTO generation_tasks VALUES(?,?,?,?,?,?)",
                         ("source", "owner", "session", "SUCCEEDED", '{"id":"original"}', None))
            conn.execute("INSERT INTO generation_assets VALUES(?,?,?,?,?,?,?)",
                         ("asset", "source", "model.glb", self.asset.stat().st_size,
                          hashlib.sha256(self.asset.read_bytes()).hexdigest(), "model", "glb"))
            conn.execute("INSERT INTO go_chat_turns VALUES('done')")
        self.destination = self.root / "isolated"

    def run_seed(self, *args):
        return subprocess.run([sys.executable, str(SCRIPT), "--source-db", str(self.db),
                               "--storage-root", str(self.storage), "--source-task", "source",
                               "--destination", str(self.destination), *args],
                              capture_output=True, text=True, check=False)

    def test_dry_run_has_no_copy(self):
        result = self.run_seed()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["mode"], "dry-run")
        self.assertFalse(self.destination.exists())

    def test_applied_snapshot_preserves_rows_and_owns_independent_bytes(self):
        before = hashlib.sha256(self.db.read_bytes()).hexdigest()
        result = self.run_seed("--apply")
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertFalse(report["lifecycle_rows_modified"])
        with closing(sqlite3.connect(self.destination / "acceptance.db")) as copied:
            self.assertEqual(copied.execute("SELECT state FROM generation_tasks").fetchone()[0],
                             "SUCCEEDED")
        target = self.destination / "assets/model.glb"
        self.assertEqual(target.read_bytes(), self.asset.read_bytes())
        target.write_bytes(b"isolated overwrite")
        self.assertEqual(self.asset.read_bytes(), b"genuine fixture bytes")
        self.assertEqual(hashlib.sha256(self.db.read_bytes()).hexdigest(), before)
        self.assertNotEqual(self.run_seed("--apply").returncode, 0)

    def test_active_snapshot_never_becomes_runnable(self):
        with closing(sqlite3.connect(self.db)) as conn, conn:
            conn.execute("UPDATE generation_tasks SET state='RUNNING'")
        result = self.run_seed("--apply")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("active rows", result.stderr)
        self.assertTrue((self.destination / "SEED_FAILED").exists())
        self.assertFalse((self.destination / "seed-report.json").exists())

    def test_terminal_post_success_schedule_is_not_quiescent(self):
        with closing(sqlite3.connect(self.db)) as conn, conn:
            conn.execute("UPDATE generation_tasks SET next_action_at='2026-10-14T00:00:00Z'")
        result = self.run_seed()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("active rows", result.stderr)
        self.assertFalse(self.destination.exists())

    def test_bad_bytes_are_rejected(self):
        self.asset.write_bytes(b"unexpected bytes")
        result = self.run_seed()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("byte identity mismatch", result.stderr)
        self.assertFalse(self.destination.exists())

    def test_symlink_escape_is_rejected(self):
        outside = self.root / "outside.glb"
        self.asset.rename(outside)
        self.asset.symlink_to(outside)
        self.assertIn("invalid storage path", self.run_seed().stderr)

    def test_storage_key_cannot_escape_and_reenter_source(self):
        for key in ("../storage/model.glb", str(self.asset), "nested/../../storage/model.glb"):
            with self.subTest(key=key):
                with closing(sqlite3.connect(self.db)) as conn, conn:
                    conn.execute("UPDATE generation_assets SET storage_key=?", (key,))
                result = self.run_seed()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("invalid storage key", result.stderr)
                self.assertFalse(self.destination.exists())

    def test_copy_size_is_bounded(self):
        result = self.run_seed("--max-asset-bytes", "1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("byte limit", result.stderr)


if __name__ == "__main__":
    unittest.main()
