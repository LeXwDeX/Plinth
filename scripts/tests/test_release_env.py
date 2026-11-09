"""Offline tests of the environments file rules in scripts/release-env.py (#334)."""
import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("release_env", ROOT / "scripts/release-env.py")
release_env = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release_env)

# Placeholder names only: no real host, root or project.
BASE = {
    "version": 1,
    "environments": {
        "shared-a": {
            "ssh": "root@host-one",
            "root": "/srv/stack-a",
            "base_url": "http://host-one:8080",
            "compose_project": "stack_a",
            "protect_projects": ["stack_b"],
            "protected_paths": ["/srv/legacy-app"],
        },
        "shared-b": {
            "ssh": "root@host-one",
            "root": "/srv/stack-b",
            "base_url": "http://host-one:8081",
            "compose_project": "stack_b",
            "protect_projects": ["stack_a"],
            "protected_paths": [],
        },
        "alone": {
            "ssh": "root@host-two",
            "root": "/srv/alone",
            "base_url": "http://host-two:8080",
            "compose_project": "alone",
            "single_stack": {"image_keep": 1},
            "protected_paths": [],
        },
        "dev-test": {
            "ssh": "root@host-three",
            "root": "/srv/dev-test",
            "base_url": "http://host-three:8080",
            "compose_project": "dev_test",
            "single_stack": {"image_keep": 1},
            "protected_paths": [],
            "backup": {"scope": "database", "keep": 2},
        },
    },
}


def load(document):
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "environments.json"
        path.write_text(json.dumps(document))
        return release_env.load_environments(path)


def changed(mutate):
    document = copy.deepcopy(BASE)
    mutate(document["environments"])
    return document


class SmokeInjectionTest(unittest.TestCase):
    def test_smoke_injects_the_environment_name_with_the_base_address(self):
        # api_free.py reads the server log of the same deployment it sent requests to (RELEASE_ENV + BASE).
        seen = {}

        class Done:
            returncode = 0

        def fake_run(command, env):
            seen.update(env)
            return Done()

        document = load(BASE)
        with mock.patch.object(release_env, "admin_credentials", return_value={"ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}), \
                mock.patch.object(release_env, "agent_credentials", return_value={}), \
                mock.patch.object(release_env, "running_revision", return_value="0" * 40), \
                mock.patch.object(release_env, "container", return_value="c"), \
                mock.patch.object(release_env.subprocess, "run", fake_run):
            name = next(iter(document["environments"]))
            self.assertEqual(release_env.smoke(document, name, ["true"]), 0)
        self.assertEqual(seen["RELEASE_ENV"], name)
        self.assertEqual(seen["BASE"], document["environments"][name]["base_url"])


class AgentAccountTest(unittest.TestCase):
    """#215: every release keeps the Agent's own account, read from production/agent-test.env on the host."""

    class Done:
        def __init__(self, code):
            self.returncode = code

    def release(self, release_code, agent, smoke_code=0, name="alone"):
        document = load(BASE)
        calls = []

        def fake_smoke(doc, env_name, command):
            calls.append((env_name, command))
            return smoke_code

        output = io.StringIO()
        with mock.patch.object(release_env.subprocess, "run", return_value=self.Done(release_code)), \
                mock.patch.object(release_env, "agent_credentials", return_value=agent), \
                mock.patch.object(release_env, "smoke", fake_smoke), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            args = mock.Mock(revision=None, acceptance_note=None)
            code = release_env.release(document, name, args)
        return code, calls, output.getvalue()

    def test_a_successful_release_ensures_the_agent_account(self):
        code, calls, _ = self.release(0, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"})
        self.assertEqual(code, 0)
        self.assertEqual(calls, [("alone", ["bash", str(release_env.AGENT_ACCOUNT_SCRIPT)])])

    def test_a_failed_release_does_not_touch_the_account(self):
        code, calls, _ = self.release(3, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"})
        self.assertEqual((code, calls), (3, []))

    def test_without_the_account_file_the_release_only_says_so(self):
        code, calls, output = self.release(0, {})
        self.assertEqual((code, calls), (0, []))
        self.assertIn("agent-test.env", output)

    def test_an_account_failure_is_reported_after_the_release_is_deployed(self):
        code, calls, output = self.release(0, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"}, smoke_code=1)
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 1)
        self.assertIn("deployed", output)

    def test_smoke_injects_the_agent_account(self):
        seen = {}

        def fake_run(command, env):
            seen.update(env)
            return self.Done(0)

        document = load(BASE)
        with mock.patch.object(release_env, "admin_credentials", return_value={"ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}), \
                mock.patch.object(release_env, "agent_credentials", return_value={"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"}), \
                mock.patch.object(release_env, "running_revision", return_value="0" * 40), \
                mock.patch.object(release_env, "container", return_value="c"), \
                mock.patch.object(release_env.subprocess, "run", fake_run):
            self.assertEqual(release_env.smoke(document, "alone", ["true"]), 0)
        self.assertEqual((seen["AGENT_TEST_USERNAME"], seen["AGENT_TEST_PASSWORD"]), ("a", "b"))

    def test_the_host_reader_returns_only_asked_keys_and_tolerates_a_missing_file(self):
        import subprocess
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "agent-test.env"
            path.write_text("AGENT_TEST_USERNAME='agent'\nAGENT_TEST_PASSWORD=\"secret\"\nOTHER=x\n")
            def read(target):
                result = subprocess.run(["python3", "-", str(target), *release_env.AGENT_KEYS],
                                        input=release_env.HOST_ENV_READER.encode(), capture_output=True, check=True)
                return json.loads(result.stdout)
            self.assertEqual(read(path), {"AGENT_TEST_USERNAME": "agent", "AGENT_TEST_PASSWORD": "secret"})
            self.assertEqual(read(Path(directory) / "missing.env"), {})


class EnvironmentRulesTest(unittest.TestCase):
    def test_base_document_is_valid(self):
        load(BASE)

    def test_shared_host_without_protect_projects_is_refused(self):
        document = changed(lambda environments: environments["shared-a"].pop("protect_projects"))
        with self.assertRaisesRegex(release_env.EnvironmentError_, "protect_projects"):
            load(document)

    def test_stack_without_protect_projects_or_single_stack_is_refused(self):
        # Not single_stack means the host is shared even when this file lists
        # no other environment on it.
        def mutate(environments):
            del environments["shared-b"]
            environments["shared-a"].pop("protect_projects")
        with self.assertRaisesRegex(release_env.EnvironmentError_, "protect_projects"):
            load(changed(mutate))

    def test_protect_projects_must_cover_every_project_on_the_host(self):
        document = changed(lambda environments: environments["shared-a"].update(protect_projects=["other"]))
        with self.assertRaisesRegex(release_env.EnvironmentError_, "stack_b"):
            load(document)

    def test_single_stack_cannot_share_its_host(self):
        def mutate(environments):
            environments["alone"]["ssh"] = "root@host-one"
            environments["shared-a"]["protect_projects"].append("alone")
            environments["shared-b"]["protect_projects"].append("alone")
        with self.assertRaisesRegex(release_env.EnvironmentError_, "single_stack"):
            load(changed(mutate))

    def test_released_environment_needs_protected_paths(self):
        document = changed(lambda environments: environments["alone"].pop("protected_paths"))
        with self.assertRaisesRegex(release_env.EnvironmentError_, "protected_paths"):
            load(document)

    def test_protected_paths_must_be_absolute_plain_paths(self):
        for bad in (["relative/dir"], ["/srv/a:/srv/b"], ["/srv/../etc"], "/srv/a", [""]):
            document = changed(lambda environments, bad=bad: environments["shared-a"].update(protected_paths=bad))
            with self.subTest(bad=bad), self.assertRaisesRegex(release_env.EnvironmentError_, "protected_paths"):
                load(document)

    def test_protected_paths_reach_the_server_and_manual_runs(self):
        document = load(BASE)
        words = release_env.release_variables(document["environments"]["shared-a"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("ART_AI_PROTECTED_PATHS=/srv/stack-a:/srv/legacy-app", words)
        words = release_env.release_variables(document["environments"]["alone"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("ART_AI_PROTECTED_PATHS=/srv/alone", words)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "environments.json"
            path.write_text(json.dumps(BASE))
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                self.assertEqual(release_env.main(["--file", str(path), "compose-env", "shared-a"]), 0)
        self.assertIn("ART_AI_PROTECTED_PATHS=/srv/stack-a:/srv/legacy-app\n", output.getvalue())

    def test_database_backup_reaches_the_server(self):
        document = load(BASE)
        words = release_env.release_variables(document["environments"]["dev-test"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_BACKUP_SCOPE=database", words)
        self.assertIn("RELEASE_BACKUP_KEEP=2", words)
        for name in ("shared-a", "shared-b", "alone"):
            words = release_env.release_variables(document["environments"][name], None)["RELEASE_SERVER_ENV"].split()
            self.assertFalse([word for word in words if word.startswith("RELEASE_BACKUP_")], name)

    def test_omitted_backup_keeps_full_scope_without_retention(self):
        document = changed(lambda environments: environments["dev-test"].pop("backup"))
        load(document)
        words = release_env.release_variables(document["environments"]["dev-test"], None)["RELEASE_SERVER_ENV"].split()
        self.assertFalse([word for word in words if word.startswith("RELEASE_BACKUP_")])

    def test_npm_registry_is_passed_to_the_build_and_must_be_one_url(self):
        # Internal stacks may point the image build at their package cache; the
        # repository default stays the public registry (#257).
        document = changed(lambda environments: environments["shared-a"].update(
            build={"npm_registry": "https://npm.internal.test/npm/"}))
        load(document)
        words = release_env.release_variables(document["environments"]["shared-a"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_NPM_REGISTRY=https://npm.internal.test/npm/", words)
        for bad in ("https://a.test/npm/,direct", "ftp://a.test/npm/", "https://a.test/npm/ x"):
            document = changed(lambda environments, bad=bad: environments["shared-a"].update(build={"npm_registry": bad}))
            with self.assertRaisesRegex(release_env.EnvironmentError_, "build.npm_registry"):
                load(document)

    def test_build_options_keep_network_proxy_and_add_hosts(self):
        document = changed(lambda environments: environments["shared-a"].update(
            build={"network": "host", "goproxy": "https://proxy.internal.test/go/,direct",
                   "add_hosts": ["mirror.internal.test:10.9.8.6"]}))
        load(document)
        words = release_env.release_variables(document["environments"]["shared-a"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_BUILD_NETWORK=host", words)
        self.assertIn("RELEASE_GOPROXY=https://proxy.internal.test/go/,direct", words)
        self.assertIn("RELEASE_BUILD_ADD_HOSTS=mirror.internal.test:10.9.8.6", words)
        self.assertFalse([word for word in words if word.startswith("ART_AI_NPM_REGISTRY=")])

    def test_database_backup_is_limited_to_dev_test(self):
        def mutate(environments):
            environments["shared-a"]["backup"] = {"scope": "database", "keep": 3}
        with self.assertRaisesRegex(release_env.EnvironmentError_, "dev-test"):
            load(changed(mutate))

    def test_dev_test_backup_cannot_be_promoted(self):
        def mutate(environments):
            environments["dev-test"]["promote_from"] = "shared-a"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "promote_from"):
            load(changed(mutate))

    def test_ephemeral_data_reaches_the_server_without_backup_words(self):
        # #28: dev-test may run on throwaway data; it excludes the backup policy.
        def mutate(environments):
            environments["dev-test"].pop("backup")
            environments["dev-test"]["data"] = "ephemeral"
        document = changed(mutate)
        load(document)
        words = release_env.release_variables(document["environments"]["dev-test"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_DATA_MODE=ephemeral", words)
        self.assertFalse([word for word in words if word.startswith("RELEASE_BACKUP_")])
        default = release_env.release_variables(load(BASE)["environments"]["shared-a"], None)["RELEASE_SERVER_ENV"]
        self.assertNotIn("RELEASE_DATA_MODE", default)

    def test_ephemeral_data_is_limited_to_dev_test(self):
        def mutate(environments):
            environments["shared-a"]["data"] = "ephemeral"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "data is only supported for the dev-test"):
            load(changed(mutate))

    def test_ephemeral_data_cannot_be_promoted_or_combined_with_backup(self):
        def promoted(environments):
            environments["dev-test"].pop("backup")
            environments["dev-test"]["data"] = "ephemeral"
            environments["dev-test"]["promote_from"] = "shared-a"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "promote_from"):
            load(changed(promoted))

        def with_backup(environments):
            environments["dev-test"]["data"] = "ephemeral"  # BASE keeps its backup policy
        with self.assertRaisesRegex(release_env.EnvironmentError_, "cannot be combined with backup"):
            load(changed(with_backup))

    def test_ephemeral_data_value_is_strict(self):
        for value in ("persistent", "", "Ephemeral", True, 1, ["ephemeral"], {"mode": "ephemeral"}):
            def mutate(environments, value=value):
                environments["dev-test"].pop("backup")
                environments["dev-test"]["data"] = value
            with self.subTest(value=value), self.assertRaisesRegex(release_env.EnvironmentError_, "data must be"):
                load(changed(mutate))

    def test_removed_managed_by_field_is_refused_with_a_fixable_message(self):
        # #74: the legacy-formal stack that used managed_by is retired.
        def mutate(environments):
            environments["shared-b"]["managed_by"] = "release-go-formal.sh"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "managed_by was removed"):
            load(changed(mutate))

    @staticmethod
    def add_formal(environments, **extra):
        environments["formal-test"] = {
            "ssh": "root@host-four", "root": "/srv/formal", "base_url": "http://host-four:8080",
            "compose_project": "formal_test", "single_stack": {"image_keep": 1}, "protected_paths": [], **extra}

    def test_formal_test_full_backup_retention(self):
        document = changed(lambda e: self.add_formal(e, backup={"scope": "full", "keep": 2}))
        load(document)
        words = release_env.release_variables(document["environments"]["formal-test"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_BACKUP_SCOPE=full", words)
        self.assertIn("RELEASE_BACKUP_KEEP=2", words)

    def test_formal_test_database_backup_is_refused(self):
        with self.assertRaisesRegex(release_env.EnvironmentError_, "dev-test"):
            load(changed(lambda e: self.add_formal(e, backup={"scope": "database", "keep": 2})))

    def test_dev_test_database_backup_cannot_be_promoted(self):
        def promoted(environments):
            environments["dev-test"]["promote_from"] = "shared-a"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "promote_from"):
            load(changed(promoted))

    def test_full_backup_retention_is_limited_to_test_stacks(self):
        def mutate(environments):
            environments["shared-a"]["backup"] = {"scope": "full", "keep": 2}
        with self.assertRaisesRegex(release_env.EnvironmentError_, "formal-test"):
            load(changed(mutate))

    def test_formal_test_full_backup_retention_rejects_bad_keep(self):
        for keep in (0, 3):
            with self.subTest(keep=keep), self.assertRaisesRegex(release_env.EnvironmentError_, "keep"):
                load(changed(lambda e: self.add_formal(e, backup={"scope": "full", "keep": keep})))

    def test_unknown_backup_scope_is_refused(self):
        def mutate(environments):
            environments["dev-test"]["backup"] = {"scope": "partial", "keep": 2}
        with self.assertRaisesRegex(release_env.EnvironmentError_, "scope must be database or full"):
            load(changed(mutate))

    def test_backup_settings_are_strict(self):
        for backup in ({"scope": "full", "keep": 3}, {"scope": "full", "keep": 0}, {"scope": "database"}, {"scope": "database", "keep": 0},
                       {"scope": "database", "keep": 3}, {"scope": "database", "keep": 10},
                       {"scope": "database", "keep": True}, {"scope": "database", "keep": "3"},
                       {"scope": "database", "keep": 3, "extra": 1}, "database"):
            def mutate(environments, backup=backup):
                environments["dev-test"]["backup"] = backup
            with self.subTest(backup=backup), self.assertRaisesRegex(release_env.EnvironmentError_, "backup"):
                load(changed(mutate))
        for keep in (1, 2):
            def set_keep(environments, keep=keep):
                environments["dev-test"]["backup"]["keep"] = keep
            load(changed(set_keep))


def ephemeral_document():
    def mutate(environments):
        environments["dev-test"].pop("backup")
        environments["dev-test"]["data"] = "ephemeral"
    return load(changed(mutate))


class UpstreamShapeTest(unittest.TestCase):
    """#219: the ephemeral dev-test stack defaults to the fake upstream; live needs two switches."""

    def test_ephemeral_release_defaults_to_the_fake_upstream(self):
        environment = ephemeral_document()["environments"]["dev-test"]
        shape = release_env.upstream_shape(environment, False, {})
        self.assertEqual(shape, "fake")
        words = release_env.release_variables(environment, None, shape)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_UPSTREAM_SHAPE=fake", words)
        self.assertIn("RELEASE_DATA_MODE=ephemeral", words)

    def test_live_dev_test_needs_both_switches(self):
        environment = ephemeral_document()["environments"]["dev-test"]
        for environ in ({}, {"ART_AI_LIVE_ACK": "0"}, {"ART_AI_LIVE_ACK": "yes"}):
            with self.subTest(environ=environ), self.assertRaisesRegex(release_env.EnvironmentError_, "ART_AI_LIVE_ACK=1"):
                release_env.upstream_shape(environment, True, environ)
        # The acknowledgement alone keeps the fake default.
        self.assertEqual(release_env.upstream_shape(environment, False, {"ART_AI_LIVE_ACK": "1"}), "fake")
        shape = release_env.upstream_shape(environment, True, {"ART_AI_LIVE_ACK": "1"})
        self.assertEqual(shape, "live")
        words = release_env.release_variables(environment, None, shape)["RELEASE_SERVER_ENV"]
        self.assertNotIn("RELEASE_UPSTREAM_SHAPE", words)

    def test_persistent_environments_are_always_live(self):
        document = load(BASE)
        for name, environment in document["environments"].items():
            with self.subTest(name=name):
                self.assertEqual(release_env.upstream_shape(environment, False, {}), "live")
                words = release_env.release_variables(environment, None)["RELEASE_SERVER_ENV"]
                self.assertNotIn("RELEASE_UPSTREAM_SHAPE", words)
                with self.assertRaisesRegex(release_env.EnvironmentError_, "always live"):
                    release_env.upstream_shape(environment, True, {"ART_AI_LIVE_ACK": "1"})

    def test_release_refuses_live_without_acknowledgement_before_any_release(self):
        document = ephemeral_document()
        args = release_env.argparse.Namespace(revision=None, acceptance_note=None, live_upstream=True)
        with mock.patch.dict(release_env.os.environ, {}, clear=False), \
                mock.patch.object(release_env.subprocess, "run") as run:
            release_env.os.environ.pop("ART_AI_LIVE_ACK", None)
            with self.assertRaises(release_env.EnvironmentError_):
                release_env.release(document, "dev-test", args)
        run.assert_not_called()

    def test_smoke_reports_the_running_shape(self):
        document = ephemeral_document()
        environment = document["environments"]["dev-test"]
        for values, want in (({"RELEASE_UPSTREAM_SHAPE": "fake"}, "fake"), ({"RELEASE_UPSTREAM_SHAPE": "live"}, "live"),
                             ({}, "live")):
            with self.subTest(values=values), mock.patch.object(release_env, "host_env", return_value=values):
                self.assertEqual(release_env.running_upstream_shape(environment), want)
        with mock.patch.object(release_env, "host_env", side_effect=release_env.EnvironmentError_("ssh failed")):
            self.assertEqual(release_env.running_upstream_shape(environment), "live")
        with mock.patch.object(release_env, "host_env") as host_env:
            self.assertEqual(release_env.running_upstream_shape(load(BASE)["environments"]["alone"]), "live")
        host_env.assert_not_called()

        seen = {}

        class Done:
            returncode = 0

        def fake_run(command, env):
            seen.update(env)
            return Done()

        with mock.patch.object(release_env, "admin_credentials", return_value={"ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}), \
                mock.patch.object(release_env, "agent_credentials", return_value={}), \
                mock.patch.object(release_env, "running_revision", return_value="0" * 40), \
                mock.patch.object(release_env, "running_upstream_shape", return_value="fake"), \
                mock.patch.object(release_env.subprocess, "run", fake_run):
            self.assertEqual(release_env.smoke(document, "dev-test", ["true"]), 0)
        self.assertEqual(seen["ART_AI_UPSTREAM_SHAPE"], "fake")


if __name__ == "__main__":
    unittest.main()
