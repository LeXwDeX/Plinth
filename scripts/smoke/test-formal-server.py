#!/usr/bin/env python3
"""Offline tests of real formal server filesystem operations; no Docker or SSH."""
import http.cookiejar
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

PATH = Path(__file__).resolve().parents[1] / "release-formal.py"
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("formal_release_server", PATH)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
FORGE = "https://forge.example.invalid/group/project"
release.configure("/srv/unused", forge=FORGE, project="formal_project", test_project="test_project")

REVISION = "a" * 40
OLD_REVISION = "b" * 40
TAG = "aaaaaaa-20261019T010000Z"
OLD_TAG = "bbbbbbb-20261018T010000Z"
OLD_IMAGE = "sha256:" + "1" * 64
CANDIDATE_IMAGE = "sha256:" + "2" * 64
OLD_ROLLBACK_IMAGE = "sha256:" + "3" * 64
TEST_SNAPSHOT = {"id": "test-container", "image_id": "sha256:" + "4" * 64,
                 "started_at": "2026-10-19T00:00:00Z", "revision": "c" * 40, "restarts": 0}


def payload():
    evidence = FORGE + "/-/issues/232#note_123"
    return {"acceptance": {"formal_promotion": 1, "revision": REVISION, "image_tag": TAG,
            "checks": {name: {"status": "pass", "failed": 0, "passed": 1,
                              "evidence": evidence}
                       for name in ("L0", "L1", "L1_agent", "related")}},
            "acceptance_note": 123, "native_note_sha256": "c" * 64,
            "credentials": {"username": "formal_admin", "password": "p" * 48,
                            "session_secret": "s" * 48}}


def old_state(formal):
    return {"version": 1, "revision": OLD_REVISION, "image_tag": OLD_TAG,
            "image_id": OLD_IMAGE, "code": str(formal / "code" / OLD_REVISION),
            "rollback_image_id": OLD_ROLLBACK_IMAGE}


def formal_container(formal, *, image=OLD_IMAGE, revision=OLD_REVISION, mounts=None,
                     env=("SESSION_SECRET=" + "s" * 48, "SESSION_COOKIE_NAME=" + release.COOKIE)):
    if mounts is None:
        mounts = [{"Type": "bind", "Source": str(formal / name), "Destination": destination}
                  for name, destination in (("data", "/app/data"), ("storage-go", "/app/storage"),
                                            ("web-data", "/app/web-data"), ("logs", "/app/logs"))]
    return {"Id": "formal-container", "Image": image,
            "Config": {"Labels": {"com.docker.compose.project": release.PROJECT,
                                   "com.docker.compose.service": "art-ai-go",
                                   "org.opencontainers.image.revision": revision},
                       "Env": list(env)},
            "State": {"Running": True}, "Mounts": mounts,
            "HostConfig": {"ReadonlyRootfs": True}}


def test_container():
    return {"Id": TEST_SNAPSHOT["id"], "Image": TEST_SNAPSHOT["image_id"],
            "State": {"Running": True, "StartedAt": TEST_SNAPSHOT["started_at"],
                      "Health": {"Status": "healthy"}},
            "RestartCount": TEST_SNAPSHOT["restarts"],
            "Config": {"Labels": {"com.docker.compose.project": release.TEST_PROJECT,
                                   "com.docker.compose.service": "art-ai-go",
                                   "org.opencontainers.image.revision": TEST_SNAPSHOT["revision"]}}}


class ServerFilesystemTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "releases").mkdir()
        (self.root / "releases/manifest.txt").write_text(
            f"{TAG} {REVISION} 2026-10-19T01:00:00Z\n")
        self.formal = self.root / "formal"
        self.formal.mkdir(mode=0o700)
        for name in (*release.DATA_DIRS, "logs", "backups", "reports", "code"):
            (self.formal / name).mkdir(mode=0o700)
        self.ops = None
        root_patch = patch.object(release, "ROOT", self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)

    def make_ops(self):
        self.ops = release.ServerOperations(REVISION, payload())
        self.ops.uid, self.ops.gid = os.getuid(), os.getgid()
        self.ops.test_before = dict(TEST_SNAPSHOT)
        return self.ops

    def test_requested_rollback_accepts_an_identical_but_stopped_candidate(self):
        ops = self.make_ops()
        ops.code.mkdir(parents=True)
        (ops.code / "docker-compose.go-formal.yml").write_text("compose fixture\n")
        source = payload()
        state = {"version": 1, "status": "success", "revision": REVISION, "image_tag": TAG,
                 "image_id": CANDIDATE_IMAGE, "code": str(ops.code), "backup": None,
                 "acceptance": source["acceptance"], "acceptance_note": 123,
                 "native_note_sha256": source["native_note_sha256"]}
        release.atomic_json(ops.state_path, state)
        candidate = formal_container(self.formal, image=CANDIDATE_IMAGE, revision=REVISION)
        candidate["State"]["Running"] = False
        commands = []
        def fake_inspect(kind, reference):
            return test_container() if reference == release.TEST_CONTAINER else candidate
        with patch.object(release.os, "geteuid", return_value=0), patch.object(release.signal, "signal"), \
                patch.object(release, "inspect", side_effect=fake_inspect), patch.object(release, "healthy", return_value=True), \
                patch.object(release, "run", side_effect=lambda *args, **_: commands.append(args) or ""):
            release.rollback_server(SimpleNamespace(revision=REVISION, root=str(self.root), forge=FORGE,
                                                     project=release.PROJECT, test_project=release.TEST_PROJECT))
        self.assertEqual(json.loads(ops.state_path.read_text())["status"], "stopped-no-previous-version")
        self.assertTrue(commands)
        self.assertTrue(all(command[-2:] == ("stop", "art-ai-go") for command in commands))

    def write_old_deployment(self, ops):
        previous = old_state(self.formal)
        old_code = Path(previous["code"])
        old_code.mkdir(parents=True)
        (old_code / "docker-compose.go-formal.yml").write_text("old compose config\n")
        for name in release.DATA_DIRS:
            (self.formal / name / "payload.txt").write_text("old-" + name)
        env = {"ADMIN_USERNAME": "formal_admin", "SESSION_SECRET": "s" * 48,
               "SESSION_COOKIE_NAME": release.COOKIE}
        release.write_environment(self.formal / "runtime.env", env)
        release.atomic_json(ops.state_path, previous)
        with ops.retention.image_lock(self.root):
            ops.retention.write_formal_pins(self.root, OLD_IMAGE, OLD_ROLLBACK_IMAGE, None)
        ops.previous = previous
        ops.image_id = CANDIDATE_IMAGE
        return previous, env

    def test_real_tar_backup_and_recovery_restore_old_data_env_code_image_and_keep_failed_data(self):
        ops = self.make_ops()
        previous, old_env = self.write_old_deployment(ops)
        ops.check_capacity = lambda backup=None: None
        backup = ops.backup(previous)
        backup_path = Path(backup["path"])
        self.assertTrue(all((backup_path / (name + ".tar")).is_file() for name in release.DATA_DIRS))
        self.assertEqual(release.load_environment(backup_path / "runtime.env"), old_env)

        for name in release.DATA_DIRS:
            (self.formal / name / "payload.txt").write_text("failed-" + name)
        release.write_environment(self.formal / "runtime.env", {
            "ADMIN_USERNAME": "candidate_admin", "ADMIN_PASSWORD": "candidate-bootstrap-secret",
            "SESSION_SECRET": "n" * 48, "SESSION_COOKIE_NAME": release.COOKIE})
        (ops.code / "docker-compose.go-formal.yml").parent.mkdir(parents=True)
        (ops.code / "docker-compose.go-formal.yml").write_text("candidate compose config\n")

        commands = []
        original_run = release.run
        def fake_run(*args, **kwargs):
            if args[0] == "tar":
                return original_run(*args, **kwargs)
            commands.append((tuple(map(str, args)), kwargs.get("env")))
            return ""

        def inspect(kind, reference):
            if kind == "container" and reference == release.CONTAINER:
                return formal_container(self.formal)
            if kind == "container" and reference == release.TEST_CONTAINER:
                return test_container()
            raise AssertionError((kind, reference))

        with patch.object(release, "run", side_effect=fake_run), \
             patch.object(release, "inspect", side_effect=inspect), \
             patch.object(release, "wait_healthy"), patch.object(release, "healthy", return_value=True):
            ops.recover(previous, backup, activated=True)

        self.assertEqual([ (self.formal / name / "payload.txt").read_text()
                           for name in release.DATA_DIRS], ["old-" + name for name in release.DATA_DIRS])
        failed = self.formal / "backups" / (ops.release_id + "-failed")
        self.assertTrue(all((failed / name / "payload.txt").read_text() == "failed-" + name
                            for name in release.DATA_DIRS))
        self.assertEqual(release.load_environment(self.formal / "runtime.env"), old_env)
        self.assertTrue((Path(previous["code"]) / "docker-compose.go-formal.yml").is_file())
        self.assertEqual(json.loads(ops.state_path.read_text()), previous)
        pins = ops.retention.formal_image_pins(self.root)
        self.assertEqual(pins, {OLD_IMAGE, OLD_ROLLBACK_IMAGE, CANDIDATE_IMAGE})
        self.assertTrue(any(command[:5] == ("docker", "compose", "--env-file", str(ops.runtime), "-p")
                            and command[-5:] == ("up", "-d", "--no-build", "--force-recreate", "art-ai-go")
                            and str(Path(previous["code"]) / "docker-compose.go-formal.yml") in command
                            and environment["FORMAL_IMAGE_TAG"] == previous["image_tag"]
                            for command, environment in commands), commands)

    def test_first_bootstrap_recovery_stops_candidate_and_preserves_all_evidence(self):
        ops = self.make_ops()
        ops.previous = None
        ops.image_id = CANDIDATE_IMAGE
        for name in release.DATA_DIRS:
            (self.formal / name / "first-bootstrap-data").write_text("preserve-" + name)
        release.write_environment(self.formal / "runtime.env", {
            "ADMIN_USERNAME": "formal_admin", "ADMIN_PASSWORD": "bootstrap-secret",
            "SESSION_SECRET": "s" * 48, "SESSION_COOKIE_NAME": release.COOKIE})
        receipt = self.formal / "reports" / "bootstrap-evidence.json"
        receipt.write_text('{"keep":true}\n')
        commands = []
        with patch.object(release, "run", side_effect=lambda *args, **kwargs: commands.append(tuple(map(str, args))) or ""):
            ops.recover(None, None, activated=True)
        self.assertEqual(commands, [("docker", "compose", "--env-file", str(ops.runtime), "-p",
                                    release.PROJECT, "-f", str(ops.code / "docker-compose.go-formal.yml"),
                                    "stop", "art-ai-go")])
        self.assertTrue(all((self.formal / name / "first-bootstrap-data").read_text() == "preserve-" + name
                            for name in release.DATA_DIRS))
        self.assertEqual(release.load_environment(ops.runtime)["ADMIN_PASSWORD"], "bootstrap-secret")
        self.assertEqual(receipt.read_text(), '{"keep":true}\n')
        self.assertFalse(ops.state_path.exists())
        self.assertEqual(ops.retention.formal_image_pins(self.root), {CANDIDATE_IMAGE})

    def test_first_publish_failure_cannot_leave_current_marked_success(self):
        ops = self.make_ops()
        ops.image_id = CANDIDATE_IMAGE
        release.atomic_json(ops.state_path, {"revision": REVISION, "image_id": CANDIDATE_IMAGE, "status": "success"})
        with patch.object(release, "run", return_value=""):
            ops.recover(None, None, activated=True)
        self.assertEqual(json.loads(ops.state_path.read_text())["status"], "stopped-no-previous-version")

    def test_finish_bootstrap_atomically_removes_password_recreates_verifies_and_relogs_in(self):
        ops = self.make_ops()
        ops.image_id = CANDIDATE_IMAGE
        release.write_environment(self.formal / "runtime.env", {
            "ADMIN_USERNAME": "formal_admin", "ADMIN_PASSWORD": "bootstrap-secret",
            "SESSION_SECRET": "s" * 48, "SESSION_COOKIE_NAME": release.COOKIE})
        initial_inode = ops.runtime.stat().st_ino
        class FakeClient:
            def __init__(self, jar): self.jar = jar

        login_urls = []
        def fake_http(base, path, request_client=None, method="GET", payload=None):
            if path == "/api/auth/login":
                login_urls.append(base)
                request_client.jar.set_cookie(http.cookiejar.Cookie(
                    version=0, name=release.COOKIE, value="session", port=None, port_specified=False,
                    domain="127.0.0.1", domain_specified=False, domain_initial_dot=False,
                    path="/", path_specified=True, secure=False, expires=None, discard=True,
                    comment=None, comment_url=None, rest={}, rfc2109=False))
                return {}
            if path == "/api/auth/session" and method == "GET": return {"isAdmin": True}
            if path == "/api/auth/session" and method == "DELETE": return {}
            raise AssertionError((base, path, method))

        def fake_inspect(kind, reference):
            self.assertEqual((kind, reference), ("container", release.CONTAINER))
            self.assertNotIn("ADMIN_PASSWORD", release.load_environment(ops.runtime))
            return formal_container(self.formal, image=CANDIDATE_IMAGE, revision=REVISION)

        compose_calls = []
        def fake_compose(code, tag, *args, **kwargs):
            compose_calls.append((code, tag, args, kwargs))
        ops.compose = fake_compose
        def fake_build_opener(processor):
            return FakeClient(processor.cookiejar)

        with patch.object(release.urllib.request, "build_opener", side_effect=fake_build_opener), \
             patch.object(release, "http_json", side_effect=fake_http), \
             patch.object(release, "inspect", side_effect=fake_inspect), \
             patch.object(release, "wait_healthy") as wait_healthy, \
             patch.object(ops, "check_formal_container") as check_container, \
             patch.object(ops, "write_probe") as write_probe:
            ops.finish_bootstrap()

        self.assertEqual(login_urls, ["http://127.0.0.1:13334"] * 2)
        self.assertNotIn("ADMIN_PASSWORD", release.load_environment(ops.runtime))
        self.assertNotEqual(ops.runtime.stat().st_ino, initial_inode)
        self.assertEqual(compose_calls[0][0], ops.code)
        self.assertEqual(compose_calls[0][1], TAG)
        self.assertEqual(compose_calls[0][2], ("up", "-d", "--no-build", "--force-recreate", "art-ai-go"))
        wait_healthy.assert_called_once_with("http://127.0.0.1:13334")
        check_container.assert_called_once_with({"revision": REVISION, "image_id": CANDIDATE_IMAGE})
        write_probe.assert_called_once_with(self.formal / "logs")

    def test_finish_bootstrap_rejects_container_that_still_has_bootstrap_password(self):
        ops = self.make_ops()
        release.write_environment(self.formal / "runtime.env", {
            "ADMIN_USERNAME": "formal_admin", "ADMIN_PASSWORD": "bootstrap-secret",
            "SESSION_SECRET": "s" * 48, "SESSION_COOKIE_NAME": release.COOKIE})
        with patch.object(ops, "login_check"), patch.object(ops, "activate"), \
             patch.object(ops, "verify"), patch.object(release, "inspect", return_value=formal_container(
                 self.formal, env=("ADMIN_PASSWORD=still-present",))):
            with self.assertRaisesRegex(release.ReleaseError, "bootstrap password remains"):
                ops.finish_bootstrap()
        self.assertNotIn("ADMIN_PASSWORD", release.load_environment(ops.runtime))

    def test_formal_mount_and_test_stack_identity_changes_are_rejected(self):
        ops = self.make_ops()
        invalid_mounts = [{"Type": "bind", "Source": str(self.root / "test-data"),
                           "Destination": "/app/data"}]
        with patch.object(release, "inspect", return_value=formal_container(self.formal, mounts=invalid_mounts)):
            with self.assertRaisesRegex(release.ReleaseError, "mounts differ"):
                ops.check_formal_container(old_state(self.formal))

        ops.test_before = dict(TEST_SNAPSHOT)
        changed = dict(TEST_SNAPSHOT, id="different-container")
        row = test_container()
        row["Id"] = changed["id"]
        with patch.object(release, "inspect", return_value=row), patch.object(release, "healthy", return_value=True):
            with self.assertRaisesRegex(release.ReleaseError, "test stack identity changed"):
                ops.verify_test_unchanged()


if __name__ == "__main__":
    unittest.main(verbosity=2)
