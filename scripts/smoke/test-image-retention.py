#!/usr/bin/env python3
"""Offline Docker contract tests. No real Docker or SSH is invoked."""
import contextlib
import fcntl
import importlib.util
import io
import json
import pathlib
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
PATH = pathlib.Path(__file__).resolve().parents[1] / "retain-art-ai-images.py"
spec = importlib.util.spec_from_file_location("retention", PATH)
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)


def identity(number):
    return f"sha256:{number:064x}"


# Placeholder names: real projects live only in the untracked environments file (#261).
TEST_PROJECT, CANDIDATE_PROJECT, FORMAL_PROJECT = "test-stack", "candidate-stack", "formal-stack"
SHARED = ("--project", TEST_PROJECT, "--protect-project", CANDIDATE_PROJECT, "--protect-project", FORMAL_PROJECT)


class RetentionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        saved = {name: getattr(retention, name) for name in
                 ("ROOT", "PROJECTS", "RUNNING_PROJECT", "RUNNING_CONTAINER", "KEEP")}
        self.addCleanup(lambda: [setattr(retention, k, v) for k, v in saved.items()])
        retention.configure_stack(str(self.root), TEST_PROJECT, None, (CANDIDATE_PROJECT, FORMAL_PROJECT))
        self.calls = []
        self.rows = {
            identity(n): {"Id": identity(n), "Created": f"2026-10-{n:02d}T00:00:00.123456789Z",
                "RepoTags": [f"art-ai-go-production:tag{n}"]} for n in range(1, 11)
        }
        self.refs = {"current": 2, "stopped": 1, "candidate": 3}
        self.fail_remove = False
        self.running = True
        self.new_reference = False
        self.formal_refs = {}

    def docker(self, *args):
        self.calls.append(args)
        if args[:2] == ("image", "ls"):
            # Unsorted, duplicated tags, and a different repository in output.
            ids = [5, 2, 8, 1, 10, 4, 7, 3, 9, 6, 6]
            return "\n".join(json.dumps({"ID": identity(n), "Repository": "art-ai-go-production"}) for n in ids) + \
                "\n" + json.dumps({"ID": identity(11), "Repository": "other-service"})
        if args[0] == "ps":
            if args[-1].endswith("=" + FORMAL_PROJECT):
                return " ".join(self.formal_refs)
            if args[-1].endswith("=" + CANDIDATE_PROJECT):
                return "candidate"
            self.assertTrue(args[-1].endswith("=" + TEST_PROJECT), args)
            return "current stopped" + (" new" if self.new_reference else "")
        if args[:2] == ("inspect", "--type=container"):
            name = "current" if args[2] == retention.RUNNING_CONTAINER else args[2]
            project = CANDIDATE_PROJECT if name == "candidate" else TEST_PROJECT
            n = 4 if name == "new" else self.refs[name]
            if name in self.formal_refs:
                project, n = FORMAL_PROJECT, self.formal_refs[name]
            return json.dumps([{"Image": identity(n), "Config": {"Labels": {
                "com.docker.compose.project": project, "com.docker.compose.service": "art-ai-go"}},
                "State": {"Running": self.running if name == "current" else name == "formal-running"}}])
        if args[:2] == ("inspect", "--type=image"):
            value = identity(3) if args[2] == "art-ai-go-production:previous" else args[2]
            return json.dumps([self.rows[value]])
        if args[0] == "rmi":
            if self.fail_remove:
                raise ValueError("docker rmi failed (exit 1)")
            if self.new_reference is False and args[1] == identity(5):
                self.new_reference = True
            return "deleted"
        self.fail(f"unexpected Docker call {args!r}")

    def run_retention(self, apply=False):
        with patch.object(retention, "docker", self.docker), contextlib.redirect_stdout(io.StringIO()) as stdout:
            result = retention.retain("art-ai-go-production:previous", apply)
        return result, stdout.getvalue()

    def removals(self):
        return [call for call in self.calls if call[0] == "rmi"]

    def pins(self, **values):
        folder = self.root / "formal" / "reports"
        folder.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = folder / "image-pins.json"
        path.write_text(json.dumps({"version": 1, "current": identity(2), "rollback": None, "pending": None, **values}))
        path.chmod(0o600)
        return path

    def test_formal_running_and_stopped_containers_are_protected(self):
        self.pins()
        self.formal_refs = {"formal-running": 4, "formal-stopped": 5}
        self.refs.update(self.formal_refs)
        _, output = self.run_retention(True)
        self.assertEqual(self.removals(), [])
        self.assertIn("KEEP " + identity(4), output)
        self.assertIn("KEEP " + identity(5), output)

    def test_formal_current_rollback_pending_pins_protected_and_dry_run_readonly(self):
        path = self.pins(current=identity(4), rollback=identity(5), pending=identity(1))
        before = path.read_bytes()
        _, output = self.run_retention()
        self.assertEqual(self.removals(), [])
        for n in (1, 4, 5):
            line = next(line for line in output.splitlines() if line.startswith("KEEP " + identity(n)))
            self.assertIn("formal-image-pin", line)
        self.assertEqual(path.read_bytes(), before)
        self.assertFalse((self.root / "releases").exists())

    def test_formal_missing_or_invalid_pins_fail_before_deleting(self):
        (self.root / "formal").mkdir()
        with self.assertRaises((ValueError, OSError)):
            self.run_retention(True)
        self.assertEqual(self.removals(), [])
        invalid = [
            [], {"version": True, "current": identity(4), "rollback": None, "pending": None},
            {"version": 1, "current": None, "rollback": None, "pending": None},
            {"version": 1, "current": "art-ai-go-production:tag4", "rollback": None, "pending": None},
            {"version": 1, "current": identity(4), "rollback": None},
            {"version": 1, "current": identity(4), "rollback": None, "pending": None, "extra": 1},
        ]
        path = self.pins()
        for value in invalid:
            with self.subTest(value=value):
                path.write_text(json.dumps(value))
                with self.assertRaises(ValueError):
                    self.run_retention(True)
                self.assertEqual(self.removals(), [])
        path.write_text('{"version":1,"version":1,"current":"' + identity(4) + '","rollback":null,"pending":null}')
        with self.assertRaises(ValueError):
            self.run_retention(True)
        self.assertEqual(self.removals(), [])

    def test_pin_unknown_to_exact_repository_inventory_fails_closed(self):
        self.pins(pending=identity(99))
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.run_retention(True)
        self.assertEqual(self.removals(), [])

    def test_pins_are_reloaded_immediately_before_each_removal(self):
        path = self.pins()
        original = self.docker
        def change_before_second_removal(*args):
            result = original(*args)
            if args == ("rmi", identity(5)):
                self.new_reference = False
                path.write_text(json.dumps({"version": 1, "current": identity(2), "rollback": identity(4), "pending": None}))
            return result
        with patch.object(retention, "docker", change_before_second_removal), contextlib.redirect_stdout(io.StringIO()):
            retention.retain("art-ai-go-production:previous", True)
        self.assertEqual(self.removals(), [("rmi", identity(5))])

    def test_pin_becoming_unreadable_stops_remaining_cleanup(self):
        path = self.pins()
        original = self.docker
        def corrupt_after_first_removal(*args):
            result = original(*args)
            if args == ("rmi", identity(5)):
                path.write_text("broken")
            return result
        with patch.object(retention, "docker", corrupt_after_first_removal), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(ValueError):
                retention.retain("art-ai-go-production:previous", True)
        self.assertEqual(self.removals(), [("rmi", identity(5))])

    def test_formal_pin_reader_rejects_symlinks_and_writable_files(self):
        path = self.pins()
        path.chmod(0o666)
        with self.assertRaises(ValueError):
            retention.formal_image_pins(self.root)
        path.unlink()
        outside = self.root / "outside.json"
        outside.write_text('{"version":1,"current":null,"rollback":null,"pending":null}')
        path.symlink_to(outside)
        with self.assertRaises((ValueError, OSError)):
            retention.formal_image_pins(self.root)

    def test_formal_absence_is_readonly_and_pin_write_is_atomic_private(self):
        self.assertEqual(retention.formal_image_pins(self.root), set())
        self.assertEqual(list(self.root.iterdir()), [])
        with retention.image_lock(self.root):
            retention.write_formal_pins(self.root, current=None, rollback=None, pending=identity(4))
        path = self.root / "formal/reports/image-pins.json"
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(retention.formal_image_pins(self.root), {identity(4)})
        before = path.read_bytes()
        with patch.object(retention.os, "replace", side_effect=OSError("write failed")):
            with self.assertRaises(OSError):
                retention.write_formal_pins(self.root, current=identity(5), rollback=None, pending=None)
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual(list(path.parent.iterdir()), [path])

    def test_image_lock_serializes_other_file_descriptors_and_is_private(self):
        with retention.image_lock(self.root):
            lock = self.root / "releases/image-retention.lock"
            self.assertEqual(stat.S_IMODE(lock.parent.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE(lock.stat().st_mode), 0o600)
            with lock.open("r+") as other:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with lock.open("r+") as other:
            fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fcntl.flock(other, fcntl.LOCK_UN)

    def test_main_apply_holds_shared_lock_and_dry_run_does_not_create_one(self):
        def assert_locked(_rollback, apply):
            self.assertTrue(apply)
            with (self.root / "releases/image-retention.lock").open("r+") as other:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return 0
        with patch.object(sys, "argv", ["retention", "--rollback-image", "art-ai-go-production:previous", "--dry-run", *SHARED]), patch.object(retention, "retain", return_value=0):
            self.assertEqual(retention.main(), 0)
        self.assertFalse((self.root / "releases").exists())
        with patch.object(sys, "argv", ["retention", "--rollback-image", "art-ai-go-production:previous", "--apply", *SHARED]), patch.object(retention, "retain", side_effect=assert_locked):
            self.assertEqual(retention.main(), 0)

    def test_dry_run_unique_ids_newest_running_rollback_stopped_and_exact_repository(self):
        result, output = self.run_retention()
        self.assertEqual(result, 0)
        self.assertEqual(output.count("WOULD REMOVE"), 2)
        self.assertIn("WOULD REMOVE " + identity(4), output)
        self.assertIn("WOULD REMOVE " + identity(5), output)
        self.assertIn("images=10 newest=5", output)
        self.assertNotIn(identity(11), output)
        self.assertEqual(self.removals(), [])

    def test_apply_plain_rmi_and_refresh_references(self):
        result, output = self.run_retention(True)
        self.assertEqual(result, 0)
        self.assertEqual(self.removals(), [("rmi", identity(5))])
        self.assertIn("reference/repository changed", output)
        self.assertFalse(any("prune" in call or "-f" in call for call in self.calls))

    def test_foreign_repository_alias_protected(self):
        self.rows[identity(4)]["RepoTags"].append("other-service:keep")
        _, output = self.run_retention()
        self.assertIn("other-repository-alias", output)
        self.assertNotIn("WOULD REMOVE " + identity(4), output)

    def test_foreign_repository_digest_protected(self):
        self.rows[identity(4)]["RepoDigests"] = ["other-service@" + identity(4)]
        _, output = self.run_retention()
        self.assertNotIn("WOULD REMOVE " + identity(4), output)

    def test_removal_failure_nonfatal_and_continues(self):
        self.fail_remove = True
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            result, _ = self.run_retention(True)
        self.assertEqual(result, 0)
        self.assertEqual(len(self.removals()), 2)
        self.assertEqual(stderr.getvalue().count("WARNING: not removed"), 2)

    def test_unknown_inventory_and_stopped_current_fail_closed(self):
        self.running = False
        with self.assertRaisesRegex(ValueError, "not running"):
            self.run_retention(True)
        self.assertEqual(self.removals(), [])
        with patch.object(retention, "docker", side_effect=ValueError("inventory unavailable")):
            with self.assertRaises(ValueError):
                retention.retain("art-ai-go-production:previous", True)

    def test_wrong_rollback_repository_rejected(self):
        with patch.object(retention, "docker", self.docker):
            with self.assertRaisesRegex(ValueError, "rollback must"):
                retention.retain("other-service:previous", True)
        self.assertEqual(self.calls, [])

    def test_docker_failure_and_timeout(self):
        with patch.object(retention.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "secret")):
            with self.assertRaisesRegex(ValueError, "exit 1") as error:
                retention.docker("rmi", identity(4))
            self.assertNotIn("secret", str(error.exception))
        with patch.object(retention.subprocess, "run", side_effect=subprocess.TimeoutExpired("docker", 30)):
            with self.assertRaises(subprocess.TimeoutExpired):
                retention.docker("image", "ls")


class SingleStackRetentionTests(unittest.TestCase):
    """Single-stack host: one Compose project, keep the running image plus one rollback."""

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        saved = {name: getattr(retention, name) for name in
                 ("ROOT", "PROJECTS", "RUNNING_PROJECT", "RUNNING_CONTAINER", "KEEP")}
        self.addCleanup(lambda: [setattr(retention, k, v) for k, v in saved.items()])
        self.rows = {identity(n): {"Id": identity(n), "Created": f"2026-10-{n:02d}T00:00:00Z",
                                   "RepoTags": [f"art-ai-go-production:tag{n}"]} for n in range(1, 5)}
        self.calls = []

    def docker(self, *args):
        self.calls.append(args)
        if args[:2] == ("image", "ls"):
            return "\n".join(json.dumps({"ID": i, "Repository": "art-ai-go-production"}) for i in self.rows)
        if args[0] == "ps":
            self.assertEqual(args[-1], "label=com.docker.compose.project=formal-project")
            return "formal-project-art-ai-go-1"
        if args[:2] == ("inspect", "--type=container"):
            self.assertEqual(args[2], "formal-project-art-ai-go-1")
            return json.dumps([{"Image": identity(4), "State": {"Running": True}, "Config": {"Labels": {
                "com.docker.compose.project": "formal-project", "com.docker.compose.service": "art-ai-go"}}}])
        if args[:2] == ("inspect", "--type=image"):
            value = identity(3) if args[2] == "art-ai-go-production:tag3" else args[2]
            return json.dumps([self.rows[value]])
        if args[0] == "rmi":
            return "deleted"
        self.fail(f"unexpected Docker call {args!r}")

    def test_keep_one_removes_everything_but_running_and_rollback(self):
        retention.configure_stack(str(self.root), "formal-project", 1)
        with patch.object(retention, "docker", self.docker), contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(retention.retain("art-ai-go-production:tag3", True), 0)
        removed = [call[1] for call in self.calls if call[0] == "rmi"]
        self.assertEqual(sorted(removed), [identity(1), identity(2)])
        self.assertIn("newest=1", out.getvalue())
        self.assertEqual({call[-1] for call in self.calls if call[0] == "ps"},
                         {"label=com.docker.compose.project=formal-project"})

    def test_main_arguments_configure_stack_and_lock_under_its_root(self):
        argv = ["retention", "--rollback-image", "art-ai-go-production:tag3", "--apply",
                "--root", str(self.root), "--project", "formal-project", "--keep", "1"]
        def locked(_rollback, apply):
            self.assertTrue(apply)
            self.assertEqual((retention.ROOT, retention.PROJECTS, retention.RUNNING_CONTAINER, retention.KEEP),
                             (self.root, ("formal-project",), "formal-project-art-ai-go-1", 1))
            self.assertTrue((self.root / "releases/image-retention.lock").exists())
            return 0
        with patch.object(sys, "argv", argv), patch.object(retention, "retain", side_effect=locked):
            self.assertEqual(retention.main(), 0)

    def test_invalid_stack_arguments_are_refused(self):
        for root, project, keep in (("relative", None, None), (None, "Bad Name", None),
                                    (None, "x;rm", None), (None, None, 0)):
            with self.subTest(root=root, project=project, keep=keep), self.assertRaises(ValueError):
                retention.configure_stack(root, project, keep)

    def test_root_and_project_are_required_and_name_no_environment(self):
        retention.ROOT, retention.PROJECTS, retention.RUNNING_PROJECT, retention.RUNNING_CONTAINER = None, (), None, None
        with self.assertRaises(ValueError):
            retention.configure_stack(None, "formal-project", None)
        with self.assertRaises(ValueError):
            retention.configure_stack(str(self.root), None, None)
        with self.assertRaises(ValueError):
            retention.configure_stack(str(self.root), None, None, ("other",))
        with self.assertRaises(ValueError):
            retention.configure_stack(str(self.root), "test-stack", None, ("Bad Name",))
        retention.configure_stack(str(self.root), "test-stack", None, ("candidate-stack", "test-stack"))
        self.assertEqual((retention.ROOT, retention.PROJECTS, retention.RUNNING_PROJECT, retention.RUNNING_CONTAINER,
                          retention.KEEP), (self.root, ("test-stack", "candidate-stack"), "test-stack",
                                            "test-stack-art-ai-go-1", 5))

    def test_main_passes_protected_projects(self):
        argv = ["retention", "--rollback-image", "art-ai-go-production:tag3", "--dry-run",
                "--root", str(self.root), *SHARED]
        with patch.object(sys, "argv", argv), patch.object(retention, "retain", return_value=0):
            self.assertEqual(retention.main(), 0)
        self.assertEqual(retention.PROJECTS, (TEST_PROJECT, CANDIDATE_PROJECT, FORMAL_PROJECT))
        with patch.object(sys, "argv", argv[:-6]), patch.object(retention, "retain", return_value=0), \
                contextlib.redirect_stderr(io.StringIO()):
            retention.RUNNING_PROJECT = None
            self.assertEqual(retention.main(), 1)

    def test_retention_source_names_no_environment_project(self):
        source = PATH.read_text()
        self.assertNotRegex(source, r"PROJECTS = \(\"|RUNNING_PROJECT = \"")

if __name__ == "__main__":
    unittest.main(verbosity=2)
