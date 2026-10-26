#!/usr/bin/env python3
"""Offline promotion authorization and failure-transaction tests; no SSH/Docker."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

PATH = Path(__file__).resolve().parents[1] / "release-formal.py"
spec = importlib.util.spec_from_file_location("formal_release", PATH)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

SHA = "a" * 40
TAG = "aaaaaaa-20261019T010000Z"
FORGE = "https://forge.example.invalid/group/project"
EVIDENCE = FORGE + "/-/issues/232#note_123"
release.FORGE = FORGE
ENVIRONMENTS = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
json.dump({"version": 1, "acceptance": {"forge_project": "42", "forge_url": FORGE, "issue": 232},
           "environments": {"legacy-formal": {"ssh": "root@legacy.example.invalid", "root": "/srv/legacy",
                                              "base_url": "http://legacy.example.invalid:13334",
                                              "compose_project": "legacy_project",
                                              "managed_by": "release-go-formal.sh"},
                            "dev-test": {"ssh": "root@legacy.example.invalid", "root": "/srv/legacy",
                                         "base_url": "http://legacy.example.invalid:13333",
                                         "compose_project": "test_project"}}}, ENVIRONMENTS)
ENVIRONMENTS.close()
os.environ["ART_AI_ENVIRONMENTS_FILE"] = ENVIRONMENTS.name


def acceptance():
    return {"formal_promotion": 1, "revision": SHA, "image_tag": TAG,
            "checks": {name: {"status": "pass", "failed": 0, "passed": 1,
                              "evidence": EVIDENCE} for name in ("L0", "L1", "L1_agent", "related")}}


class AuthorizationTests(unittest.TestCase):
    def test_capacity_checks_docker_data_filesystem_separately(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker_root = root / "docker-data"
            docker_root.mkdir()
            cap = SimpleNamespace(devices={1: {"block": 4096}}, filesystem=lambda _: 1,
                                  require=unittest.mock.Mock(), verify=unittest.mock.Mock())
            ops = release.ServerOperations.__new__(release.ServerOperations)
            ops.formal = root / "formal"
            ops.capacity = release.load_helper("release-capacity.py", "capacity_test")
            with patch.object(release, "ROOT", root), patch.object(release, "run", return_value=str(docker_root)) as command, \
                    patch.object(ops.capacity, "Capacity", return_value=cap):
                ops.check_capacity()
                command.assert_called_once_with("docker", "info", "--format", "{{.DockerRootDir}}")
            self.assertTrue(any(call.args[0] == docker_root and call.args[1] >= 512 << 20 for call in cap.require.call_args_list))

    def test_cli_rejects_invalid_arguments_before_network_or_credentials(self):
        for revision, note in (("main", 1), (SHA, None), (SHA, -1)):
            args = SimpleNamespace(revision=revision, acceptance_note=note, credentials_file="/unused", rollback=False)
            with patch.object(release, "run") as run, patch.object(release, "credential_file") as secret:
                with self.assertRaises(release.ReleaseError): release.local(args)
                run.assert_not_called(); secret.assert_not_called()

    def test_non_main_revision_and_unaccepted_native_note_never_reach_ssh(self):
        args = SimpleNamespace(revision=SHA, acceptance_note=123, credentials_file="/unused", rollback=False)
        for results in (["", release.ReleaseError("not ancestor")], ["", "", '{"body":"no accepted tests"}']):
            with patch.object(release, "run", side_effect=results) as run, patch.object(release, "credential_file") as secret:
                with self.assertRaises(release.ReleaseError): release.local(args)
                self.assertFalse(any(call.args[0] == "ssh" for call in run.call_args_list))
                secret.assert_not_called()

    def test_exact_revision_and_native_acceptance_required(self):
        report = acceptance()
        self.assertEqual(release.acceptance_from_note("```json\n" + json.dumps(report) + "\n```", SHA), report)
        for bad in ("main", "a" * 7, "a" * 39 + ";", "A" * 40, " " + SHA):
            with self.subTest(bad=bad), self.assertRaises(release.ReleaseError):
                release.validate_revision(bad)
        for body in (EVIDENCE, "{}", "```json\n{}\n```", "```json\n{broken}\n```"):
            with self.assertRaises(release.ReleaseError):
                release.acceptance_from_note(body, SHA)

    def test_wrong_revision_failed_missing_or_soft_failed_check_refused(self):
        cases = []
        report = acceptance(); report["revision"] = "b" * 40; cases.append(report)
        report = acceptance(); del report["checks"]["L1_agent"]; cases.append(report)
        for field, value in (("status", "soft-fail"), ("failed", 1), ("passed", 0), ("evidence", "https://example.com/pass")):
            report = acceptance(); report["checks"]["L0"][field] = value; cases.append(report)
        report = acceptance(); report["checks"]["L1"]["skipped"] = 1; cases.append(report)
        for report in cases:
            with self.subTest(report=report), self.assertRaises(release.ReleaseError):
                release.acceptance_from_note("```json\n" + json.dumps(report) + "\n```", SHA)

    def test_native_acceptance_cannot_replace_test_release_manifest(self):
        release.validate_manifest(f"{TAG} {SHA} 2026-10-19T01:00:00Z\n", SHA, TAG)
        for text in ("", f"other {SHA} 2026-10-19T01:00:00Z", f"{TAG} {'b'*40} 2026-10-19T01:00:00Z"):
            with self.assertRaises(release.ReleaseError):
                release.validate_manifest(text, SHA, TAG)

    def test_formal_environment_rejects_keys_and_test_cookie(self):
        env = {"ADMIN_USERNAME": "formal_admin", "SESSION_SECRET": "a" * 48,
               "SESSION_COOKIE_NAME": "art_ai_formal_session"}
        release.validate_environment(env)
        for addition in ({"OPENAI_API_KEY": "never-log-this"}, {"LLM_API_KEY": "never-log-this"},
                         {"SESSION_COOKIE_NAME": "meshyai_session"}, {"PROVIDER_MODE": "mock"}):
            with self.assertRaises(release.ReleaseError) as caught:
                release.validate_environment({**env, **addition})
            self.assertNotIn("never-log-this", str(caught.exception))

    def test_server_rejects_missing_manifest_or_wrong_oci_without_creating_formal_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "releases").mkdir()
            manifest = root / "releases/manifest.txt"
            payload = {"acceptance": acceptance(), "acceptance_note": 123, "credentials": {}, "native_note_sha256": "c" * 64}
            for text, image in (("", {}), (f"{TAG} {SHA} 2026-10-19T01:00:00Z", {"Config": {"Labels": {"org.opencontainers.image.revision": "b" * 40}}})):
                manifest.write_text(text)
                with patch.object(release, "ROOT", root), patch.object(release, "inspect", return_value=image):
                    ops = release.ServerOperations(SHA, payload)
                    with patch.object(ops, "test_snapshot", return_value={"revision": "test"}):
                        with self.assertRaises(release.ReleaseError): ops.prepare()
                self.assertFalse((root / "formal").exists())


class FakeOperations:
    def __init__(self, previous=True, fail=None):
        self.previous = {"revision": "b" * 40} if previous else None
        self.fail = fail
        self.calls = []

    def action(self, name, result=None):
        self.calls.append(name)
        if self.fail == name:
            raise release.ReleaseError("injected")
        return result

    def prepare(self): return self.action("prepare", self.previous)
    def preflight(self): self.action("preflight")
    def stop(self, previous): self.action("stop")
    def backup(self, previous): return self.action("backup", "backup-receipt")
    def activate(self): self.action("activate")
    def verify(self): self.action("verify")
    def finish_bootstrap(self): self.action("finish_bootstrap")
    def verify_test_unchanged(self): self.action("verify_test_unchanged")
    def publish(self, previous, backup): return self.action("publish", {"status": "success"})
    def recover(self, previous, backup, activated):
        self.calls.append(("recover", copy.deepcopy(previous), backup, activated))
    def failure(self, stage, recovered): self.calls.append(("failure", stage, recovered))


class TransactionTests(unittest.TestCase):
    def test_failed_publish_marks_the_same_candidate_report_as_rolled_back(self):
        with tempfile.TemporaryDirectory() as directory:
            ops = release.ServerOperations.__new__(release.ServerOperations)
            ops.formal = Path(directory)
            ops.release_id = "candidate"
            ops.revision = SHA
            ops.previous = {"revision": "b" * 40}
            report = ops.formal / "reports/candidate.json"
            release.atomic_json(report, {"status": "committing", "revision": SHA})
            ops.failure("publish", True)
            result = json.loads(report.read_text())
            self.assertEqual(result["status"], "rolled-back")
            self.assertEqual(result["stage"], "publish")

    def test_preflight_failure_never_stops_old_service(self):
        ops = FakeOperations(fail="preflight")
        with self.assertRaises(release.ReleaseError): release.promote(ops)
        self.assertNotIn("stop", ops.calls)
        self.assertFalse(any(isinstance(x, tuple) and x[0] == "recover" for x in ops.calls))

    def test_backup_failure_restarts_old_service_without_restoring_partial_archive(self):
        ops = FakeOperations(fail="backup")
        with self.assertRaises(release.ReleaseError): release.promote(ops)
        self.assertIn(("recover", ops.previous, None, False), ops.calls)
        self.assertNotIn("activate", ops.calls)

    def test_every_post_cutover_failure_restores_old_data_and_configuration(self):
        for phase in ("activate", "verify", "finish_bootstrap", "verify_test_unchanged", "publish"):
            ops = FakeOperations(fail=phase)
            with self.subTest(phase=phase), self.assertRaises(release.ReleaseError): release.promote(ops)
            self.assertIn(("recover", ops.previous, "backup-receipt", True), ops.calls)

    def test_first_start_failure_stops_new_instance_without_invented_rollback(self):
        ops = FakeOperations(previous=False, fail="verify")
        with self.assertRaises(release.ReleaseError): release.promote(ops)
        self.assertNotIn("backup", ops.calls)
        self.assertIn(("recover", None, None, True), ops.calls)

    def test_success_is_published_only_after_bootstrap_and_test_stack_verified(self):
        ops = FakeOperations()
        self.assertEqual(release.promote(ops), {"status": "success"})
        self.assertLess(ops.calls.index("backup"), ops.calls.index("activate"))
        self.assertLess(ops.calls.index("finish_bootstrap"), ops.calls.index("publish"))
        self.assertLess(ops.calls.index("verify_test_unchanged"), ops.calls.index("publish"))


class DeploymentTargetTests(unittest.TestCase):
    """#249/#261: the legacy-formal host, root, forge and both Compose projects come from the environments file."""

    def tearDown(self):
        release.configure("/srv/unused", forge=FORGE)

    def test_targets_are_read_from_the_environments_file(self):
        release.configure_from_environments(ENVIRONMENTS.name)
        self.assertEqual((release.HOST, release.ROOT, release.FORGE, release.FORGE_PROJECT),
                         ("root@legacy.example.invalid", Path("/srv/legacy"), FORGE, "42"))
        self.assertEqual((release.PROJECT, release.CONTAINER, release.TEST_PROJECT, release.TEST_CONTAINER),
                         ("legacy_project", "legacy_project-art-ai-go-1", "test_project", "test_project-art-ai-go-1"))

    def test_server_operations_need_both_projects(self):
        saved = (release.PROJECT, release.CONTAINER, release.TEST_PROJECT, release.TEST_CONTAINER)
        self.addCleanup(lambda: [setattr(release, name, value) for name, value in
                                 zip(("PROJECT", "CONTAINER", "TEST_PROJECT", "TEST_CONTAINER"), saved)])
        release.PROJECT = release.TEST_PROJECT = None
        with self.assertRaises(release.ReleaseError):
            release.ServerOperations(SHA, {})
        for project, test_project in (("Bad Name", "t"), ("same", "same"), ("x;rm", "t")):
            with self.subTest(project=project), self.assertRaises(release.ReleaseError):
                release.configure("/srv/x", project=project, test_project=test_project)

    def test_environments_without_the_test_stack_project_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            partial = Path(directory) / "environments.json"
            document = json.loads(Path(ENVIRONMENTS.name).read_text())
            del document["environments"]["dev-test"]
            partial.write_text(json.dumps(document))
            with self.assertRaises(release.ReleaseError):
                release.configure_from_environments(partial)

    def test_missing_entry_or_unsafe_values_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            empty = Path(directory) / "environments.json"
            empty.write_text('{"version": 1, "environments": {}}')
            with self.assertRaises(release.ReleaseError):
                release.configure_from_environments(empty)
        for root, host, forge in (("relative", None, None), ("/srv/../etc", None, None),
                                  ("/srv/x", "root@a;b", None), ("/srv/x", None, "http://forge.example.invalid/p")):
            with self.subTest(root=root, host=host, forge=forge), self.assertRaises(release.ReleaseError):
                release.configure(root, host, forge)

    def test_acceptance_needs_a_configured_forge(self):
        release.FORGE = None
        body = "```json\n" + json.dumps(acceptance()) + "\n```"
        with self.assertRaises(release.ReleaseError):
            release.acceptance_from_note(body, SHA)
        self.assertEqual(release.acceptance_from_note(body, SHA, FORGE), acceptance())
        other = "https://other.example.invalid/group/project"
        with self.assertRaises(release.ReleaseError):
            release.acceptance_from_note(body, SHA, other)

    def test_rollback_passes_root_and_forge_to_the_server(self):
        args = SimpleNamespace(revision=SHA, acceptance_note=None, credentials_file=None, rollback=True)
        with patch.object(release, "run", return_value=""), patch.object(release, "remote_operation") as remote:
            release.local(args)
        command = remote.call_args.args[0]
        self.assertTrue(command.startswith("python3 /srv/legacy/formal/code/" + SHA + "/scripts/release-formal.py"))
        self.assertTrue(command.endswith(" rollback-server " + SHA + " --root /srv/legacy --forge " + FORGE
                                         + " --project legacy_project --test-project test_project"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
