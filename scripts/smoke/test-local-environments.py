#!/usr/bin/env python3
"""Offline tests: the untracked environments file is found in this checkout or the main checkout."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

PATH = Path(__file__).resolve().parents[1] / "local_environments.py"
spec = importlib.util.spec_from_file_location("local_environments", PATH)
locator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(locator)


def git(*args, cwd):
    subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True)


class LocatorTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name).resolve()
        self.main = root / "main"
        self.main.mkdir()
        git("init", "-q", cwd=self.main)
        git("-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "x",
            cwd=self.main)
        self.worktree = root / "worktree"
        git("worktree", "add", "-q", str(self.worktree), cwd=self.main)
        environment = patch.dict(os.environ)
        environment.start()
        self.addCleanup(environment.stop)
        os.environ.pop("ART_AI_ENVIRONMENTS_FILE", None)

    def write(self, root, relative):
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("{}")
        return path

    def test_worktree_falls_back_to_the_main_checkout(self):
        expected = self.write(self.main, "deploy/local/environments.json")
        self.assertEqual(locator.environments_file(repository=self.worktree), expected)

    def test_own_copy_wins_and_new_folder_precedes_old_file(self):
        self.write(self.main, "deploy/local/environments.json")
        old = self.write(self.worktree, "deploy/environments.local.json")
        self.assertEqual(locator.environments_file(repository=self.worktree), old)
        new = self.write(self.worktree, "deploy/local/environments.json")
        self.assertEqual(locator.environments_file(repository=self.worktree), new)

    def test_explicit_path_and_missing_file(self):
        os.environ["ART_AI_ENVIRONMENTS_FILE"] = "/elsewhere/environments.json"
        self.assertEqual(locator.environments_file(repository=self.worktree), Path("/elsewhere/environments.json"))
        self.assertEqual(locator.environments_file("/given.json", repository=self.worktree), Path("/given.json"))
        del os.environ["ART_AI_ENVIRONMENTS_FILE"]
        self.assertEqual(locator.environments_file(repository=self.worktree),
                         self.worktree / "deploy/local/environments.json")

    def test_local_folder_is_ignored_by_git(self):
        ignored = subprocess.run(["git", "check-ignore", "-q", "deploy/local/environments.json"],
                                 cwd=PATH.parent.parent, check=False)
        self.assertEqual(ignored.returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
