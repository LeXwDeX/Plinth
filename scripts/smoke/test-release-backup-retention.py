#!/usr/bin/env python3
"""Offline contract tests for release backup retention. No Docker or SSH."""
import contextlib
import datetime
import hashlib
import importlib.util
import io
import json
import os
import pathlib
import sys
import tempfile
import threading
import unittest
from unittest.mock import mock_open, patch

sys.dont_write_bytecode = True
PATH = pathlib.Path(__file__).resolve().parents[1] / "retain-release-backups.py"
spec = importlib.util.spec_from_file_location("backup_retention", PATH)
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)

PROJECT = "test-stack"
OTHER_PROJECT = "other-stack"
IMAGE = "art-ai-go-production"
UTC = datetime.timezone.utc


class RetentionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name) / "stack"
        self.backups = self.root / "backups"
        self.manifest = self.root / "releases" / "manifest.txt"
        self.reports = self.root / "production" / "reports" / "release-failures"
        for directory in ("production/data", "production/storage-go", "production/web-data",
                          "releases", "backups", "production/reports/release-failures"):
            (self.root / directory).mkdir(parents=True, exist_ok=True)
        self.live = self.root / "production" / "data" / "live.txt"
        self.live.write_text("live bytes\n")
        self.releases = []
        self.counter = 0
        self.project = PROJECT
        self.project_label = PROJECT
        self.tag_override = None
        self.revision_override = None
        self.mount_override = None
        self.calls = []
        self.secret = "docker-secret-value"
        self.real_mount_points = retention.read_mount_points
        patches = patch.object(retention, "read_mount_points", return_value=frozenset())
        patches.start()
        self.addCleanup(patches.stop)

    def release(self, minute, kind="database", report=False, restore=False, manifest=True,
                sha=None, failed=False):
        self.counter += 1
        sha = sha or f"{self.counter:040x}"
        moment = datetime.datetime(2026, 10, 23, 12, minute, tzinfo=UTC)
        tag = self.tag_override or f"{sha[:7]}-{moment:%Y%m%dT%H%M%SZ}"
        path = self.backups / f"release-{tag}"
        path.mkdir()
        if kind in ("database", "partial"):
            (path / "data.tar").write_bytes(b"data" * 10)
            (path / "scope").write_text("database\n")
            if kind == "partial":
                (path / "data.tar").unlink()
        elif kind == "full":
            for name in ("data", "storage-go", "web-data"):
                (path / f"{name}.tar").write_bytes((name * 8).encode())
            (path / "scope").write_text("full\n")
        elif kind == "legacy":
            for name in ("data", "storage-go", "web-data"):
                (path / f"{name}.tar").write_bytes((name * 8).encode())
        else:
            self.fail(f"unknown fixture layout {kind}")
        if failed:
            staging = path / "failed"
            for name in ("data", "storage-go", "web-data"):
                (staging / name).mkdir(parents=True)
                (staging / name / "marker").write_bytes((name * 4).encode())
            (staging / "compose.env").write_text("GO_IMAGE_TAG=previous\n")
        if restore:
            nested = path / "restore" / "data" / "nested"
            nested.mkdir(parents=True)
            (nested / "recovered.bin").write_bytes(b"restore" * 5)
        if report:
            self.write_report(tag, sha)
        if manifest:
            self.releases.append((tag, sha, moment))
            self.write_manifest()
        return tag, sha, path

    def write_report(self, tag, sha):
        (self.reports / f"{tag}-unhealthy-after-up.txt").write_text(
            f"tag={tag} sha={sha} stage=unhealthy-after-up\n"
            f"backup={self.backups}/release-{tag}\n")

    def write_manifest(self):
        self.manifest.write_text(
            "".join(f"{tag} {sha} {moment:%Y-%m-%dT%H:%M:%SZ}\n"
                    for tag, sha, moment in self.releases))

    def docker(self, *args):
        self.calls.append(args)
        container = f"{self.project}-art-ai-go-1"
        if args[:2] != ("inspect", "--type=container") or args[2] != container:
            self.fail(f"unexpected Docker call {args!r}")
        tag = self.tag_override or (self.releases[-1][0] if self.releases else "")
        mounts = self.mount_override or [
            {"Type": "bind", "Source": str(self.root / "production" / name), "Destination": destination}
            for destination, name in (("/app/data", "data"), ("/app/storage", "storage-go"),
                                      ("/app/web-data", "web-data"))
        ]
        row = {
            "Config": {
                "Image": f"{IMAGE}:{tag}",
                "Labels": {"com.docker.compose.project": self.project_label,
                           "com.docker.compose.service": "art-ai-go",
                           "org.opencontainers.image.revision": self.revision_override
                           or (self.releases[-1][1] if self.releases else "0" * 40)},
                "Env": [f"SECRET={self.secret}"],
            },
            "State": {"Running": True},
            "Mounts": mounts,
        }
        return json.dumps([row])

    def run_retention(self, keep=2, apply=False):
        return self.run_retention_with(str(self.root), keep, apply)

    def run_retention_with(self, root, keep=2, apply=False):
        with patch.object(retention, "docker", self.docker):
            return retention.run(root, self.project, keep, apply=apply,
                                 rollback=f"{IMAGE}:previous-tag")

    def assert_unchanged(self, before):
        self.assertEqual(before, self.state())

    def state(self):
        result = {}
        for directory, directories, files in os.walk(self.root, followlinks=False):
            for name in directories + files:
                if name == ".release.lock":
                    continue
                path = pathlib.Path(directory) / name
                info = path.lstat()
                result[str(path.relative_to(self.root))] = (info.st_mode, info.st_size, info.st_mtime_ns)
        return result

    def test_default_dry_run_reports_newest_two_without_changes(self):
        old, _, _ = self.release(0, kind="legacy")
        second, _, _ = self.release(1)
        current, current_sha, current_path = self.release(2)
        # Legacy snapshots predate the scope file but still count as closed releases.
        preserved = self.backups / "migration-notes"
        preserved.mkdir()
        (preserved / "readme.txt").write_text("keep")
        (self.backups / "release-notes").mkdir()
        before = self.state()
        with contextlib.redirect_stdout(io.StringIO()) as output:
            with patch.object(retention, "docker", self.docker):
                code = retention.main(["--root", str(self.root), "--project", PROJECT,
                                       "--rollback-image", f"{IMAGE}:previous-tag",
                                       "--keep", "2"])
        document = json.loads(output.getvalue())
        self.assertEqual(code, 0)
        self.assertEqual(document["mode"], "dry-run")
        self.assertEqual(document["deleted"], [])
        self.assertEqual([item["name"] for item in document["kept"]],
                         [f"release-{current}", f"release-{second}"])
        self.assertEqual([item["name"] for item in document["would_delete"]],
                         [f"release-{old}"])
        self.assertIn("older than newest 2", document["would_delete"][0]["reason"])
        self.assertEqual(document["candidates"][0]["bytes"], current_path.joinpath("data.tar").stat().st_size
                         + current_path.joinpath("scope").stat().st_size)
        self.assertIn({"name": "migration-notes", "reason": "not a release backup"},
                      document["preserved"])
        self.assertIn({"name": "release-notes", "reason": "not a release backup"},
                      document["preserved"])
        self.assertNotIn(self.secret, output.getvalue())
        self.assert_unchanged(before)
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_apply_keep_one_deletes_only_the_older_release(self):
        old, _, old_path = self.release(0)
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_failed_and_restore_trees_expire_after_newest_two(self):
        failed, failed_sha, failed_path = self.release(0, report=True, restore=True, manifest=False)
        partial, partial_sha, partial_path = self.release(1, kind="partial", report=True, manifest=False)
        current, _, current_path = self.release(2)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual({item["name"] for item in result["deleted"]},
                         {f"release-{failed}", f"release-{partial}"})
        self.assertFalse(failed_path.exists())
        self.assertFalse(partial_path.exists())
        self.assertTrue(current_path.exists())
        # Failure reports are diagnostics outside backups and are not removed.
        self.assertTrue((self.reports / f"{failed}-unhealthy-after-up.txt").exists())
        self.assertTrue((self.reports / f"{partial}-unhealthy-after-up.txt").exists())

    def test_refuses_unfinalized_or_malformed_backup_metadata(self):
        current, _, current_path = self.release(1)
        in_progress, _, in_progress_path = self.release(2, manifest=False)
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention()
        self.assertTrue(in_progress_path.exists())

        (in_progress_path / "scope").write_text("other\n")
        (in_progress_path / "data.tar").unlink()
        self.write_report(in_progress, "0" * 40)
        with self.assertRaisesRegex(ValueError, "scope"):
            self.run_retention()
        self.assertTrue(current_path.exists())
        self.assertTrue(self.live.exists())

    def test_refuses_wrong_project_current_or_mount_binding(self):
        old, _, _ = self.release(0)
        current, _, current_path = self.release(1)

        self.project_label = OTHER_PROJECT
        with self.assertRaisesRegex(ValueError, "project"):
            self.run_retention()
        self.project_label = PROJECT

        self.mount_override = [
            {"Type": "bind", "Source": str(self.root / "production" / "data"), "Destination": "/app/data"},
            {"Type": "bind", "Source": str(self.root / "other" / "storage-go"), "Destination": "/app/storage"},
            {"Type": "bind", "Source": str(self.root / "production" / "web-data"), "Destination": "/app/web-data"},
        ]
        with self.assertRaisesRegex(ValueError, "mount"):
            self.run_retention()
        self.mount_override = None

        self.tag_override = old
        self.revision_override = self.releases[0][1]
        with self.assertRaisesRegex(ValueError, "manifest"):
            self.run_retention()
        self.tag_override = None
        self.revision_override = None
        self.assertTrue(current_path.exists())

    def test_refuses_mismatched_running_revision(self):
        self.release(0)
        self.release(1)
        self.revision_override = "f" * 40
        with self.assertRaisesRegex(ValueError, "revision"):
            self.run_retention()

    def test_refuses_symlink_and_path_replacement(self):
        old, _, old_path = self.release(0)
        current, _, current_path = self.release(1)
        original = old_path / "data.tar"
        original.unlink()
        original.symlink_to(self.live)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.run_retention(keep=1)
        self.assertTrue(self.live.exists())

        original.unlink()
        original.write_bytes(b"data" * 10)
        with patch.object(retention, "docker", self.docker):
            plan = retention.build_plan(str(self.root), PROJECT, 1,
                                        rollback=f"{IMAGE}:previous-tag")
            old_path.rename(self.backups / "moved-release")
            old_path.mkdir()
            (old_path / "data.tar").write_bytes(b"data" * 10)
            (old_path / "scope").write_text("database\n")
            result = retention.execute(plan)
        self.assertIn("changed", result["error"])
        self.assertEqual([item["name"] for item in result["failed"]], [f"release-{old}"])
        self.assertTrue(old_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_partial_delete_failure_is_reported_and_nonzero(self):
        first, _, first_path = self.release(0)
        second, _, second_path = self.release(1)
        current, _, current_path = self.release(2)
        real_unlink = retention.unlink_checked
        calls = []

        def flaky(*args):
            calls.append(args[1])
            if len(calls) == 1:
                raise OSError(13, "permission denied")
            return real_unlink(*args)

        with patch.object(retention, "docker", self.docker), \
                patch.object(retention, "unlink_checked", flaky):
            with contextlib.redirect_stdout(io.StringIO()) as output:
                code = retention.main(["--root", str(self.root), "--project", PROJECT,
                                       "--rollback-image", f"{IMAGE}:previous-tag",
                                       "--keep", "1", "--apply"])
        document = json.loads(output.getvalue())
        self.assertEqual(code, 1)
        self.assertTrue(document["deleted"])
        self.assertEqual(len(document["failed"]), 1)
        self.assertTrue(first_path.exists() or second_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_retention_error_after_deletion_reports_json(self):
        first, _, first_path = self.release(0)
        second, _, second_path = self.release(1)
        current, _, current_path = self.release(2)
        real_unlink = retention.unlink_checked
        calls = []

        def flaky(*args):
            calls.append(args[1])
            if len(calls) == 3:
                raise retention.RetentionError("synthetic identity change")
            return real_unlink(*args)

        with patch.object(retention, "docker", self.docker), \
                patch.object(retention, "unlink_checked", flaky):
            with contextlib.redirect_stdout(io.StringIO()) as output:
                code = retention.main(["--root", str(self.root), "--project", PROJECT,
                                       "--rollback-image", f"{IMAGE}:previous-tag",
                                       "--keep", "1", "--apply"])
        document = json.loads(output.getvalue())
        self.assertEqual(code, 1)
        self.assertEqual(document["error"], "synthetic identity change")
        self.assertEqual([item["name"] for item in document["deleted"]], [f"release-{second}"])
        self.assertEqual([item["name"] for item in document["failed"]], [f"release-{first}"])
        self.assertFalse(second_path.exists())
        self.assertTrue(first_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_refuses_unsafe_root_and_backups_symlink(self):
        self.release(0)
        self.release(1)
        with self.assertRaisesRegex(ValueError, "root"):
            self.run_retention_with("bad/../root")
        with self.assertRaisesRegex(ValueError, "root"):
            self.run_retention_with("//")
        real_backups = self.root / "backups"
        saved = self.root / "real-backups"
        real_backups.rename(saved)
        real_backups.symlink_to(saved)
        with self.assertRaisesRegex(ValueError, "backups"):
            self.run_retention()
        real_backups.unlink()
        saved.rename(real_backups)

    def test_current_backup_must_be_newest(self):
        current, _, current_path = self.release(0)
        newer_failure, _, newer_path = self.release(1, report=True, manifest=False)
        with self.assertRaisesRegex(ValueError, "newest"):
            self.run_retention(keep=2)
        self.assertTrue(current_path.exists())
        self.assertTrue(newer_path.exists())

    def test_refuses_keep_above_two(self):
        self.release(0)
        self.release(1)
        with self.assertRaisesRegex(ValueError, "keep must be 1-2"):
            self.run_retention(keep=3)

    def test_refuses_unfinalized_legacy(self):
        old, _, old_path = self.release(0, kind="legacy", manifest=False)
        self.release(1)
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention(keep=1)
        self.assertTrue(old_path.exists())

    def test_refuses_current_backup_change_before_apply(self):
        self.release(0)
        _, _, current_path = self.release(1)
        with patch.object(retention, "docker", self.docker):
            plan = retention.build_plan(str(self.root), PROJECT, 1,
                                        rollback=f"{IMAGE}:previous-tag")
        current_path.rename(self.backups / "moved-current")
        current_path.mkdir()
        (current_path / "data.tar").write_bytes(b"data" * 10)
        (current_path / "scope").write_text("database\n")
        with patch.object(retention, "docker", self.docker):
            with self.assertRaisesRegex(ValueError, "current release backup changed"):
                retention.execute(plan)

    def test_refuses_missing_current_backup(self):
        self.release(0)
        _, _, current_path = self.release(1)
        for child in current_path.iterdir():
            child.unlink()
        current_path.rmdir()
        with self.assertRaisesRegex(ValueError, "current release backup is missing"):
            self.run_retention()

    def test_refuses_successful_backup_missing_archive(self):
        current, _, current_path = self.release(1)
        (current_path / "data.tar").unlink()
        with self.assertRaisesRegex(ValueError, "missing"):
            self.run_retention()

    def test_refuses_new_backup_between_plan_and_apply(self):
        self.release(0)
        self.release(1)
        with patch.object(retention, "docker", self.docker):
            plan = retention.build_plan(str(self.root), PROJECT, 1,
                                        rollback=f"{IMAGE}:previous-tag")
        new, _, new_path = self.release(2, manifest=False)
        with patch.object(retention, "docker", self.docker):
            with self.assertRaisesRegex(ValueError, "appeared"):
                retention.execute(plan)
        self.assertTrue(new_path.exists())

    def test_orders_by_timestamp_not_sha(self):
        older = self.release(0, sha="f" * 40)
        newer = self.release(1, sha="0" * 40)
        result = self.run_retention(keep=1)
        self.assertEqual([item["name"] for item in result["kept"]], [f"release-{newer[0]}"])
        self.assertEqual([item["name"] for item in result["would_delete"]],
                         [f"release-{older[0]}"])

    def test_refuses_two_line_scope(self):
        current, _, current_path = self.release(1)
        (current_path / "scope").write_text("scope=database\ntargets=data\n")
        with self.assertRaisesRegex(ValueError, "scope"):
            self.run_retention()

    def test_full_scope_layout_can_be_deleted(self):
        old, _, old_path = self.release(0, kind="full")
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertTrue(current_path.exists())

    def test_unrelated_malformed_report_without_backup_is_ignored(self):
        old, _, old_path = self.release(0)
        current, _, current_path = self.release(1)
        stale = self.reports / "abcdef0-20261020T000000Z-unhealthy-after-up.txt"
        stale.write_bytes(b"historical diagnostic body without a tag line\n")
        before = stale.read_bytes()
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(stale.read_bytes(), before)

    def test_malformed_report_for_existing_snapshot_is_refused(self):
        old, _, old_path = self.release(0, failed=True, manifest=False)
        self.release(1)
        report = self.reports / f"{old}-unhealthy-after-up.txt"
        report.write_bytes(b"body without tag line\n")
        with self.assertRaisesRegex(ValueError, "does not name its tag"):
            self.run_retention(keep=1)
        self.assertTrue(old_path.exists())

    def test_unrelated_report_is_not_finalization_proof(self):
        old, _, old_path = self.release(0, failed=True, manifest=False)
        self.release(1)
        stale = self.reports / "abcdef0-20261020T000000Z-unhealthy-after-up.txt"
        stale.write_text("no tag line\n")
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention(keep=1)
        self.assertTrue(old_path.exists())

    def test_failed_layout_can_be_deleted(self):
        old, _, old_path = self.release(0, report=True, failed=True, manifest=False)
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_refuses_legacy_failed_layout_without_report(self):
        old, _, old_path = self.release(0, kind="legacy", failed=True, manifest=False)
        self.release(1)
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention(keep=1)
        self.assertTrue(old_path.exists())

    def test_refuses_unknown_failed_layout(self):
        current, _, current_path = self.release(1, failed=True)
        (current_path / "failed" / "unknown").mkdir()
        with self.assertRaisesRegex(ValueError, "failed backup layout"):
            self.run_retention()

    def test_refuses_failed_layout_symlink(self):
        current, _, current_path = self.release(1, failed=True)
        (current_path / "failed" / "data" / "link").symlink_to(self.live)
        with self.assertRaisesRegex(ValueError, "unsafe"):
            self.run_retention()
        self.assertTrue(self.live.exists())

    def test_legacy_failed_restore_expires_only_with_failure_report(self):
        failed, _, failed_path = self.release(0, kind="legacy", report=True, restore=True,
                                             manifest=False)
        self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{failed}"])
        self.assertFalse(failed_path.exists())

        current, _, current_path = self.release(3)
        unreported, _, unreported_path = self.release(2, kind="legacy", restore=True,
                                                      manifest=False)
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention(keep=1)
        self.assertTrue(unreported_path.exists())

    def test_refuses_unknown_backup_tree_entry(self):
        current, _, current_path = self.release(1)
        (current_path / "unexpected.txt").write_text("unexpected")
        with self.assertRaisesRegex(ValueError, "unknown"):
            self.run_retention()

    def test_refuses_current_change_before_apply(self):
        self.release(0)
        current = self.release(1)
        with patch.object(retention, "docker", self.docker):
            plan = retention.build_plan(str(self.root), PROJECT, 1,
                                        rollback=f"{IMAGE}:previous-tag")
        self.manifest.write_text(
            self.manifest.read_text() + f"{current[0]} {'0' * 40} 2026-10-23T12:02:00Z\n")
        with patch.object(retention, "docker", self.docker):
            with self.assertRaisesRegex(ValueError, "manifest"):
                retention.execute(plan)

    def test_refuses_missing_or_malformed_manifest(self):
        self.release(0)
        self.release(1)
        self.manifest.write_text("not a manifest line\n")
        with self.assertRaisesRegex(ValueError, "manifest"):
            self.run_retention()
        self.manifest.unlink()
        with self.assertRaisesRegex(ValueError, "manifest"):
            self.run_retention()

    def test_duplicate_tag_and_sha_rows_are_allowed_and_delete_oldest(self):
        oldest, _, oldest_path = self.release(0)
        duplicated, duplicate_sha, duplicated_path = self.release(1)
        self.releases.append((duplicated, duplicate_sha,
                              datetime.datetime(2026, 10, 23, 12, 5, tzinfo=UTC)))
        self.write_manifest()
        current, _, current_path = self.release(2)
        original = self.manifest.read_bytes()
        records, digest, _ = retention.read_manifest(str(self.root))
        self.assertEqual(len(records), 4)
        self.assertEqual(records[1], (duplicated, duplicate_sha, "2026-10-23T12:01:00Z"))
        self.assertEqual(records[2], (duplicated, duplicate_sha, "2026-10-23T12:05:00Z"))
        self.assertEqual(digest, hashlib.sha256(original).hexdigest())
        result = self.run_retention(keep=2, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{oldest}"])
        self.assertFalse(oldest_path.exists())
        self.assertTrue(duplicated_path.exists())
        self.assertTrue(current_path.exists())
        self.assertEqual(self.manifest.read_bytes(), original)

    def test_conflicting_revision_for_same_tag_is_refused(self):
        first, first_sha, _ = self.release(0)
        self.release(1)
        conflicting = first_sha[:7] + "b" * 33
        lines = self.manifest.read_text().splitlines()
        lines.insert(1, f"{first} {conflicting} 2026-10-23T12:03:00Z")
        self.manifest.write_text("\n".join(lines) + "\n")
        with self.assertRaisesRegex(ValueError, "conflicting"):
            self.run_retention(keep=1)

    def test_current_runtime_must_match_the_last_manifest_row(self):
        first, first_sha, _ = self.release(0)
        self.release(1)
        self.tag_override = first
        self.revision_override = first_sha
        with self.assertRaisesRegex(ValueError, "running release does not match"):
            self.run_retention(keep=1)

    def test_zero_byte_failed_archive_is_deletable_with_report(self):
        old, _, old_path = self.release(0, report=True, manifest=False)
        (old_path / "data.tar").write_bytes(b"")
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertTrue(current_path.exists())

    def test_zero_byte_successful_archive_is_refused(self):
        current, _, current_path = self.release(1)
        (current_path / "data.tar").write_bytes(b"")
        with self.assertRaisesRegex(ValueError, "missing its archives"):
            self.run_retention(keep=1)
        self.assertTrue(current_path.exists())

    def test_partial_legacy_failure_is_deletable_only_with_report(self):
        partial, _, partial_path = self.release(0, kind="legacy", report=True, manifest=False)
        (partial_path / "storage-go.tar").unlink()
        (partial_path / "web-data.tar").unlink()
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{partial}"])
        self.assertFalse(partial_path.exists())
        self.assertTrue(current_path.exists())

        current, _, current_path = self.release(3)
        unreported, _, unreported_path = self.release(2, kind="legacy", manifest=False)
        (unreported_path / "storage-go.tar").unlink()
        (unreported_path / "web-data.tar").unlink()
        with self.assertRaisesRegex(ValueError, "not finalized"):
            self.run_retention(keep=1)
        self.assertTrue(unreported_path.exists())

    def test_empty_legacy_failure_is_deletable_only_with_report(self):
        failed, _, failed_path = self.release(0, kind="legacy", report=True, manifest=False)
        for name in retention.ARCHIVES:
            (failed_path / name).unlink()
        current, _, current_path = self.release(1)
        result = self.run_retention(keep=1, apply=True)
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{failed}"])
        self.assertFalse(failed_path.exists())
        self.assertTrue(current_path.exists())

    def test_same_device_mount_alias_stops_recursive_removal(self):
        old, _, old_path = self.release(0, report=True, restore=True, manifest=False)
        self.release(1)
        nested = old_path / "restore" / "data" / "nested"
        alias = nested / "aliased"
        alias.mkdir()
        (alias / "live.txt").write_bytes(b"live-bytes\n")
        before = self.state()
        with patch.object(retention, "read_mount_points", return_value=frozenset({str(alias)})):
            with self.assertRaisesRegex(ValueError, "mount"):
                self.run_retention(keep=1)
            with self.assertRaisesRegex(ValueError, "mount"):
                self.run_retention(keep=1, apply=True)
        self.assert_unchanged(before)
        self.assertEqual((alias / "live.txt").read_bytes(), b"live-bytes\n")
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_mount_alias_in_failed_tree_stops_removal(self):
        old, _, old_path = self.release(0, report=True, failed=True, manifest=False)
        self.release(1)
        alias = old_path / "failed" / "storage-go"
        with patch.object(retention, "read_mount_points", return_value=frozenset({str(alias)})):
            with self.assertRaisesRegex(ValueError, "mount"):
                self.run_retention(keep=1, apply=True)
        self.assertTrue((alias / "marker").exists())

    def test_mount_boundary_discovered_before_apply_is_refused(self):
        old, _, old_path = self.release(0, report=True, restore=True, manifest=False)
        self.release(1)
        with patch.object(retention, "docker", self.docker):
            plan = retention.build_plan(str(self.root), PROJECT, 1,
                                        rollback=f"{IMAGE}:previous-tag")
        alias = old_path / "restore" / "data"
        before = self.state()
        with patch.object(retention, "read_mount_points", return_value=frozenset({str(alias)})), \
                patch.object(retention, "docker", self.docker):
            with self.assertRaisesRegex(ValueError, "mount"):
                retention.execute(plan)
        self.assert_unchanged(before)

    def test_mount_table_is_required_and_parsed(self):
        with patch.object(retention, "read_mount_points", self.real_mount_points):
            with patch("builtins.open", side_effect=FileNotFoundError):
                with self.assertRaisesRegex(ValueError, "mount table is unavailable"):
                    retention.read_mount_points()
            with patch("builtins.open", mock_open(read_data="1 2 3 4\n")):
                with self.assertRaisesRegex(ValueError, "mount table is malformed"):
                    retention.read_mount_points()
            table = ("1 0 8:1 / / rw - ext3 /dev/root rw\n"
                     "36 35 98:0 / /mnt\\040space rw - ext3 /dev/root rw\n")
            with patch("builtins.open", mock_open(read_data=table)):
                self.assertEqual(retention.read_mount_points(), frozenset({"/", "/mnt space"}))

    def test_backups_directory_mount_alias_is_refused(self):
        self.release(0)
        self.release(1)
        with patch.object(retention, "read_mount_points", return_value=frozenset({str(self.backups)})):
            with self.assertRaisesRegex(ValueError, "mount"):
                self.run_retention(keep=1)
            before = self.state()
            with self.assertRaisesRegex(ValueError, "mount"):
                self.run_retention(keep=1, apply=True)
        self.assert_unchanged(before)

    def test_retention_refuses_when_the_release_lock_is_held(self):
        self.release(0)
        self.release(1)
        with retention.LOCK.hold(str(self.root)):
            with self.assertRaisesRegex(ValueError, "busy"):
                self.run_retention(keep=1, apply=True)

    def test_retention_reuses_an_inherited_release_lock(self):
        old, _, old_path = self.release(0)
        self.release(1)
        with retention.LOCK.hold(str(self.root)) as descriptor:
            environment = {"RELEASE_LOCK_FD": str(descriptor),
                           "RELEASE_LOCK_ROOT": str(self.root)}
            with patch.dict(os.environ, environment), patch.object(retention, "docker", self.docker):
                result = retention.run(str(self.root), PROJECT, 1, apply=True,
                                       rollback=f"{IMAGE}:previous-tag")
        self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())

    def test_retention_acquires_an_unlocked_inherited_descriptor(self):
        old, _, old_path = self.release(0)
        self.release(1)
        descriptor = os.open(self.root / ".release.lock", os.O_RDWR | os.O_CREAT, 0o600)
        try:
            environment = {"RELEASE_LOCK_FD": str(descriptor),
                           "RELEASE_LOCK_ROOT": str(self.root)}
            with patch.dict(os.environ, environment), patch.object(retention, "docker", self.docker):
                result = retention.run(str(self.root), PROJECT, 1, apply=True,
                                       rollback=f"{IMAGE}:previous-tag")
            self.assertEqual([item["name"] for item in result["deleted"]], [f"release-{old}"])
            self.assertFalse(old_path.exists())
            # The borrowed descriptor still holds the lock after retention returns.
            with self.assertRaisesRegex(ValueError, "busy"):
                retention.run(str(self.root), PROJECT, 1, apply=False,
                              rollback=f"{IMAGE}:previous-tag")
        finally:
            os.close(descriptor)

    def test_retention_refuses_a_forged_inherited_lock(self):
        self.release(0)
        self.release(1)
        with patch.dict(os.environ, {"RELEASE_LOCK_FD": "0",
                                     "RELEASE_LOCK_ROOT": str(self.root)}):
            with self.assertRaisesRegex(ValueError, "descriptor"):
                self.run_retention(keep=1)
        descriptor = os.open(self.root / "not-a-lock", os.O_RDWR | os.O_CREAT, 0o600)
        try:
            with patch.dict(os.environ, {"RELEASE_LOCK_FD": str(descriptor),
                                         "RELEASE_LOCK_ROOT": str(self.root / "other")}):
                with self.assertRaisesRegex(ValueError, "root"):
                    self.run_retention(keep=1)
        finally:
            os.close(descriptor)

    def test_concurrent_retention_is_refused_while_removal_holds_the_lock(self):
        old, _, old_path = self.release(0, report=True, restore=True, manifest=False)
        self.release(1)
        real = retention.remove_tree
        started, finish = threading.Event(), threading.Event()
        results = []

        def slow(*args, **kwargs):
            started.set()
            if not finish.wait(10):
                raise AssertionError("removal did not finish")
            return real(*args, **kwargs)

        def first():
            results.append(self.run_retention(keep=1, apply=True))

        with patch.object(retention, "remove_tree", slow):
            worker = threading.Thread(target=first)
            worker.start()
            try:
                self.assertTrue(started.wait(10), "removal did not start")
                with self.assertRaisesRegex(ValueError, "busy"):
                    self.run_retention(keep=1, apply=True)
            finally:
                finish.set()
                worker.join(30)
        self.assertFalse(worker.is_alive())
        self.assertEqual([item["name"] for item in results[0]["deleted"]], [f"release-{old}"])
        self.assertFalse(old_path.exists())
        self.assertEqual(self.live.read_text(), "live bytes\n")

    def test_cli_rejects_apply_with_dry_run(self):
        with contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                retention.main(["--root", str(self.root), "--project", PROJECT,
                                "--keep", "2", "--apply", "--dry-run"])


if __name__ == "__main__":
    unittest.main()
