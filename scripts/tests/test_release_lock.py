#!/usr/bin/env python3
"""Offline contract tests for the per-root release lock. No Docker or SSH."""
import importlib.util
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "release-lock.py"


def load():
    spec = importlib.util.spec_from_file_location("release_lock", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class LockTests(unittest.TestCase):
    def setUp(self):
        self.lock = load()
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name) / "stack"
        self.root.mkdir()
        self.lockfile = self.root / ".release.lock"

    def inherited(self, descriptor, root=None):
        return patch.dict(os.environ, {
            "RELEASE_LOCK_FD": str(descriptor),
            "RELEASE_LOCK_ROOT": str(root or self.root),
        }, clear=False)

    def test_hold_is_exclusive_and_releases_cleanly(self):
        with self.lock.hold(str(self.root)) as descriptor:
            self.assertTrue(self.lockfile.is_file())
            self.assertEqual(os.fstat(descriptor).st_nlink, 1)
            with self.assertRaisesRegex(ValueError, "busy"):
                self.lock.acquire(str(self.root))
        with self.lock.hold(str(self.root)):
            pass

    def test_inherited_descriptor_is_validated_and_reused(self):
        with self.lock.hold(str(self.root)) as descriptor:
            with self.inherited(descriptor):
                self.lock.validate_environment(str(self.root))
                with self.lock.hold(str(self.root)) as reused:
                    self.assertEqual(reused, descriptor)
            # The borrowed descriptor must stay locked after the nested hold ends.
            with self.assertRaisesRegex(ValueError, "busy"):
                self.lock.acquire(str(self.root))
        with self.lock.hold(str(self.root)):
            pass

    def test_environment_requires_the_exact_root_and_a_held_descriptor(self):
        with self.lock.hold(str(self.root)) as descriptor:
            with self.inherited(descriptor, self.root / "other"):
                with self.assertRaisesRegex(ValueError, "root"):
                    self.lock.validate_environment(str(self.root))
            for name in ("RELEASE_LOCK_FD", "RELEASE_LOCK_ROOT"):
                environment = {"RELEASE_LOCK_FD": str(descriptor),
                               "RELEASE_LOCK_ROOT": str(self.root)}
                environment.pop(name)
                with patch.dict(os.environ, environment, clear=False):
                    with self.assertRaisesRegex(ValueError, "both"):
                        self.lock.validate_environment(str(self.root))
            other = os.open(self.root / "not-the-lock", os.O_RDWR | os.O_CREAT, 0o600)
            try:
                with self.inherited(other):
                    with self.assertRaisesRegex(ValueError, "identity"):
                        self.lock.validate_environment(str(self.root))
            finally:
                os.close(other)
            with patch.dict(os.environ, {"RELEASE_LOCK_FD": "not-a-number",
                                         "RELEASE_LOCK_ROOT": str(self.root)}):
                with self.assertRaisesRegex(ValueError, "descriptor"):
                    self.lock.validate_environment(str(self.root))

    def test_unlocked_descriptor_acquires_before_operation(self):
        self.lockfile.touch()
        descriptor = os.open(self.lockfile, os.O_RDWR)
        try:
            with self.inherited(descriptor):
                self.lock.validate_environment(str(self.root))
            # The inherited flock is now held until this descriptor closes.
            busy = subprocess.run([sys.executable, str(SCRIPT), "run", "--root", str(self.root),
                                   "--", sys.executable, "-c", "raise SystemExit(0)"],
                                  capture_output=True, text=True, timeout=20)
            self.assertNotEqual(busy.returncode, 0)
            self.assertIn("busy", busy.stderr)
        finally:
            os.close(descriptor)
        self.assertEqual(self.lock.run(str(self.root), [sys.executable, "-c", "raise SystemExit(0)"]), 0)

    def test_independent_descriptor_cannot_steal_an_other_process_lock(self):
        holder = subprocess.Popen(
            [sys.executable, str(SCRIPT), "hold", "--root", str(self.root)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertEqual(holder.stdout.readline().strip(), "locked")
            descriptor = os.open(self.lockfile, os.O_RDWR)
            try:
                with self.inherited(descriptor):
                    with self.assertRaisesRegex(ValueError, "busy"):
                        self.lock.validate_environment(str(self.root))
            finally:
                os.close(descriptor)
        finally:
            holder.terminate()
            holder.wait(timeout=20)
            for stream in (holder.stdin, holder.stdout, holder.stderr):
                stream.close()

    def test_symlink_and_unsafe_layout_are_refused(self):
        for root in ("relative", "//", "/"):
            with self.assertRaisesRegex(ValueError, "root"):
                with self.lock.hold(root):
                    pass
        real = self.root / "real.lock"
        real.write_text("x")
        self.lockfile.symlink_to(real)
        with self.assertRaisesRegex(ValueError, "lock"):
            with self.lock.hold(str(self.root)):
                pass

    def test_run_executes_under_the_lock_and_propagates_status(self):
        command = [sys.executable, "-c", "import os,sys; sys.exit(7 if os.environ.get('RELEASE_LOCK_FD') else 9)"]
        self.assertEqual(self.lock.run(str(self.root), command), 7)

    def test_run_refuses_when_another_process_holds_the_lock(self):
        holder = subprocess.Popen(
            [sys.executable, str(SCRIPT), "hold", "--root", str(self.root)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertEqual(holder.stdout.readline().strip(), "locked")
            result = subprocess.run([sys.executable, str(SCRIPT), "run", "--root", str(self.root),
                                     "--", sys.executable, "-c", "raise SystemExit(0)"],
                                    capture_output=True, text=True, timeout=20)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("busy", result.stderr)
        finally:
            holder.terminate()
            holder.wait(timeout=20)
            for stream in (holder.stdin, holder.stdout, holder.stderr):
                stream.close()

    def test_cli_run_reuses_an_inherited_lock_without_deadlock(self):
        with self.lock.hold(str(self.root)) as descriptor:
            environment = {**os.environ, "RELEASE_LOCK_FD": str(descriptor),
                           "RELEASE_LOCK_ROOT": str(self.root)}
            result = subprocess.run(
                [sys.executable, str(SCRIPT), "run", "--root", str(self.root), "--",
                 sys.executable, "-c",
                 "import os,sys; sys.exit(0 if os.environ.get('RELEASE_LOCK_FD') else 9)"],
                env=environment, capture_output=True, text=True, timeout=20, pass_fds=(descriptor,))
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_cli_rejects_invalid_invocations(self):
        for arguments in (["validate", "--root", "relative"], ["hold", "--root", "//"],
                          ["run", "--root", str(self.root)]):
            result = subprocess.run([sys.executable, str(SCRIPT), *arguments],
                                    capture_output=True, text=True, timeout=20)
            self.assertNotEqual(result.returncode, 0, arguments)


if __name__ == "__main__":
    unittest.main()
