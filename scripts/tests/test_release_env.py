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
                mock.patch.object(release_env, "release_version", return_value="v0.0.0"), \
                mock.patch.object(release_env, "agent_credentials", return_value=agent), \
                mock.patch.object(release_env, "smoke", fake_smoke), \
                mock.patch.object(release_env, "post_release_smoke", return_value=0) as post_release, \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            args = mock.Mock(revision=None, acceptance_note=None, live_upstream=False)
            code = release_env.release(document, name, args)
        return code, calls, output.getvalue(), post_release

    def test_a_successful_release_ensures_the_agent_account(self):
        code, calls, _, _ = self.release(0, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"})
        self.assertEqual(code, 0)
        self.assertEqual(calls, [("alone", ["bash", str(release_env.AGENT_ACCOUNT_SCRIPT)])])

    def test_a_failed_release_does_not_touch_the_account(self):
        code, calls, _, post_release = self.release(3, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"})
        self.assertEqual((code, calls), (3, []))
        post_release.assert_not_called()

    def test_without_the_account_file_the_release_only_says_so(self):
        code, calls, output, _ = self.release(0, {})
        self.assertEqual((code, calls), (0, []))
        self.assertIn("agent-test.env", output)

    def test_an_account_failure_is_reported_after_the_release_is_deployed(self):
        code, calls, output, _ = self.release(0, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"}, smoke_code=1)
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 1)
        self.assertIn("deployed", output)

    def test_the_post_release_smoke_runs_after_the_account_step(self):
        # #411: the release ends with the scripted smoke layers, not with the account step.
        code, calls, _, post_release = self.release(0, {"AGENT_TEST_USERNAME": "a", "AGENT_TEST_PASSWORD": "b"})
        self.assertEqual((code, post_release.call_count), (0, 1))
        self.assertEqual(post_release.call_args[0][2], "live")  # the persistent "alone" stack

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
    def add_prod(environments, **extra):
        environments["prod"] = {
            "ssh": "root@host-four", "root": "/srv/prod", "base_url": "http://host-four:8080",
            "compose_project": "prod", "single_stack": {"image_keep": 1}, "protected_paths": [], **extra}

    def test_prod_full_backup_retention(self):
        document = changed(lambda e: self.add_prod(e, backup={"scope": "full", "keep": 1}))
        load(document)
        words = release_env.release_variables(document["environments"]["prod"], None)["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_BACKUP_SCOPE=full", words)
        self.assertIn("RELEASE_BACKUP_KEEP=1", words)

    def test_prod_database_backup_is_refused(self):
        with self.assertRaisesRegex(release_env.EnvironmentError_, "dev-test"):
            load(changed(lambda e: self.add_prod(e, backup={"scope": "database", "keep": 2})))

    def test_dev_test_database_backup_cannot_be_promoted(self):
        def promoted(environments):
            environments["dev-test"]["promote_from"] = "shared-a"
        with self.assertRaisesRegex(release_env.EnvironmentError_, "promote_from"):
            load(changed(promoted))

    def test_full_backup_retention_is_limited_to_dev_test_and_prod(self):
        def mutate(environments):
            environments["shared-a"]["backup"] = {"scope": "full", "keep": 2}
        with self.assertRaisesRegex(release_env.EnvironmentError_, "dev-test and prod environments"):
            load(changed(mutate))

    def test_prod_full_backup_retention_rejects_bad_keep(self):
        for keep in (0, 3):
            with self.subTest(keep=keep), self.assertRaisesRegex(release_env.EnvironmentError_, "keep"):
                load(changed(lambda e: self.add_prod(e, backup={"scope": "full", "keep": keep})))

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


class McpListenerLayoutTest(unittest.TestCase):
    """#332: the external MCP listener's host side is an optional pair of layout keys."""

    @staticmethod
    def with_layout(layout):
        return changed(lambda environments: environments["alone"].update(layout=layout))

    @staticmethod
    def words(document, name="alone"):
        return release_env.release_variables(document["environments"][name], None)["RELEASE_SERVER_ENV"].split()

    @staticmethod
    def compose_env(document, name="alone"):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "environments.json"
            path.write_text(json.dumps(document))
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                assert release_env.main(["--file", str(path), "compose-env", name]) == 0
        return output.getvalue().splitlines()

    def test_environments_that_name_neither_key_are_unchanged(self):
        document = load(BASE)  # none of the base environments has a layout at all
        for name in document["environments"]:
            with self.subTest(name=name):
                self.assertFalse([word for word in self.words(document, name) if word.startswith("MCP_PUBLIC_")])
                self.assertFalse([line for line in self.compose_env(document, name) if line.startswith("MCP_PUBLIC_")])
        # A layout with the other keys, as the existing stacks have, validates and sends only those.
        document = load(self.with_layout({"GO_WEB_BIND_IP": "10.0.0.9", "GO_WEB_HOST_PORT": "13333",
                                          "GO_PROD_DB_DIR": "/srv/alone/production/data"}))
        words = self.words(document)
        self.assertIn("GO_WEB_HOST_PORT=13333", words)
        self.assertFalse([word for word in words if word.startswith("MCP_PUBLIC_")])
        self.assertFalse([line for line in self.compose_env(document) if line.startswith("MCP_PUBLIC_")])

    def test_both_keys_reach_the_server_and_manual_runs(self):
        document = load(self.with_layout({"GO_WEB_HOST_PORT": "13333", "MCP_PUBLIC_BIND_IP": "10.0.0.9",
                                          "MCP_PUBLIC_HOST_PORT": "13344"}))
        words = self.words(document)
        self.assertIn("MCP_PUBLIC_BIND_IP=10.0.0.9", words)
        self.assertIn("MCP_PUBLIC_HOST_PORT=13344", words)
        self.assertIn("GO_WEB_HOST_PORT=13333", words)
        lines = self.compose_env(document)
        self.assertIn("MCP_PUBLIC_BIND_IP=10.0.0.9", lines)
        self.assertIn("MCP_PUBLIC_HOST_PORT=13344", lines)

    def test_one_key_alone_is_enough(self):
        for key, value in (("MCP_PUBLIC_BIND_IP", "0.0.0.0"), ("MCP_PUBLIC_HOST_PORT", "13344")):
            with self.subTest(key=key):
                words = self.words(load(self.with_layout({key: value})))
                self.assertIn(f"{key}={value}", words)
                self.assertEqual([word for word in words if word.startswith("MCP_PUBLIC_")], [f"{key}={value}"])

    def test_bad_values_are_refused(self):
        for key, value in (("MCP_PUBLIC_BIND_IP", "999.1.1.1"), ("MCP_PUBLIC_BIND_IP", "10.0.0"), ("MCP_PUBLIC_BIND_IP", "::1"),
                           ("MCP_PUBLIC_BIND_IP", "listener.example"), ("MCP_PUBLIC_BIND_IP", "10.0.0.9:13344"),
                           ("MCP_PUBLIC_BIND_IP", "01.2.3.4"), ("MCP_PUBLIC_HOST_PORT", "0"), ("MCP_PUBLIC_HOST_PORT", "65536"),
                           ("MCP_PUBLIC_HOST_PORT", "13344x"), ("MCP_PUBLIC_HOST_PORT", "013344"), ("MCP_PUBLIC_HOST_PORT", "-1"),
                           ("MCP_PUBLIC_HOST_PORT", "")):
            with self.subTest(key=key, value=value), self.assertRaisesRegex(release_env.EnvironmentError_, key):
                load(self.with_layout({key: value}))
        with self.assertRaisesRegex(release_env.EnvironmentError_, "MCP_PUBLIC_HOST_PORT"):
            load(self.with_layout({"MCP_PUBLIC_HOST_PORT": 13344}))

    def test_the_mcp_port_cannot_take_the_web_port_of_the_same_address(self):
        # The same port on the same address (or the wildcard) is one socket.
        for layout in ({"MCP_PUBLIC_HOST_PORT": "13333"},
                       {"GO_WEB_BIND_IP": "10.0.0.9", "GO_WEB_HOST_PORT": "8080", "MCP_PUBLIC_BIND_IP": "10.0.0.9", "MCP_PUBLIC_HOST_PORT": "8080"},
                       {"GO_WEB_BIND_IP": "10.0.0.9", "GO_WEB_HOST_PORT": "8080", "MCP_PUBLIC_BIND_IP": "0.0.0.0", "MCP_PUBLIC_HOST_PORT": "8080"}):
            with self.subTest(layout=layout), self.assertRaisesRegex(release_env.EnvironmentError_, "differ from GO_WEB_HOST_PORT"):
                load(self.with_layout(layout))
        # Different addresses are different sockets.
        load(self.with_layout({"GO_WEB_BIND_IP": "10.0.0.9", "GO_WEB_HOST_PORT": "8080",
                               "MCP_PUBLIC_BIND_IP": "127.0.0.1", "MCP_PUBLIC_HOST_PORT": "8080"}))
        # A stack that names no MCP key is not checked: its layout is what it always was.
        load(self.with_layout({"GO_WEB_HOST_PORT": "13343"}))

    def test_the_example_environments_file_names_the_optional_keys(self):
        example = json.loads((ROOT / "deploy/environments.example.json").read_text())
        layout = example["environments"]["prod"]["layout"]
        self.assertIn("MCP_PUBLIC_BIND_IP", layout)
        self.assertIn("MCP_PUBLIC_HOST_PORT", layout)
        self.assertIn("MCP_PUBLIC_BIND_IP", example["environments"]["prod"]["description"])
        self.assertNotIn("MCP_PUBLIC_BIND_IP", example["environments"]["dev-test"]["layout"])
        self.assertLessEqual(set(layout), set(release_env.LAYOUT_KEYS))


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

    def test_live_chat_is_the_half_open_mixed_shape(self):
        # #416: real chat connections, fake generation. It needs the same
        # acknowledgement as a fully live release and cannot be combined with it.
        environment = ephemeral_document()["environments"]["dev-test"]
        for environ in ({}, {"ART_AI_LIVE_ACK": "0"}):
            with self.subTest(environ=environ), self.assertRaisesRegex(release_env.EnvironmentError_, "ART_AI_LIVE_ACK=1"):
                release_env.upstream_shape(environment, live_chat=True, environ=environ)
        self.assertEqual(release_env.upstream_shape(environment, live_chat=True, environ={"ART_AI_LIVE_ACK": "1"}), "mixed")
        words = release_env.release_variables(environment, None, "mixed")["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_UPSTREAM_SHAPE=mixed", words)
        self.assertIn("RELEASE_DATA_MODE=ephemeral", words)
        with self.assertRaisesRegex(release_env.EnvironmentError_, "cannot be combined"):
            release_env.upstream_shape(environment, live_upstream=True, live_chat=True, environ={"ART_AI_LIVE_ACK": "1"})
        # The acknowledgement alone keeps the fake default.
        self.assertEqual(release_env.upstream_shape(environment, environ={"ART_AI_LIVE_ACK": "1"}), "fake")

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
        for values, want in (({"RELEASE_UPSTREAM_SHAPE": "fake"}, "fake"), ({"RELEASE_UPSTREAM_SHAPE": "mixed"}, "mixed"),
                             ({"RELEASE_UPSTREAM_SHAPE": "live"}, "live"), ({}, "live")):
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


class ReleaseVersionTest(unittest.TestCase):
    """#676: each release links a release string into the build, next to the commit."""

    def test_an_explicit_release_version_is_used_as_given(self):
        with mock.patch.object(release_env, "run") as run:
            self.assertEqual(release_env.release_version("v1.9.5", "a" * 40), "v1.9.5")
        run.assert_not_called()

    def test_a_release_version_must_be_a_plain_name(self):
        for bad in ("", "v1.9.5 x", "-v1", "v1;rm", "v1/2", "v" + "1" * 64):
            with self.subTest(bad=bad), self.assertRaisesRegex(release_env.EnvironmentError_, "--release-version"):
                release_env.release_version(bad, None)

    def test_default_is_git_describe_of_the_released_revision(self):
        calls = []

        def fake_run(*args, **kwargs):
            calls.append(args)
            return "v1.9.4-12-gfbf5000\n" if args[1] == "describe" else ""

        with mock.patch.object(release_env, "run", fake_run):
            self.assertEqual(release_env.release_version(None, "b" * 40), "v1.9.4-12-gfbf5000")
        # A promoted revision may be missing from this clone: it is fetched first too.
        self.assertEqual(calls, [("git", "fetch", "-q", "origin"),
                                 ("git", "describe", "--tags", "--always", "b" * 40 + "^{commit}")])
        calls.clear()
        # Without a promotion revision the release is origin/main, fetched first.
        with mock.patch.object(release_env, "run", fake_run):
            self.assertEqual(release_env.release_version(None, None), "v1.9.4-12-gfbf5000")
        self.assertEqual(calls, [("git", "fetch", "-q", "origin"),
                                 ("git", "describe", "--tags", "--always", "origin/main^{commit}")])

    def test_an_unknown_revision_says_how_to_go_on(self):
        def describe_fails(*args, **kwargs):
            if args[1] == "describe":
                raise release_env.EnvironmentError_("git failed (exit 128)")
            return ""

        with mock.patch.object(release_env, "run", describe_fails), \
                self.assertRaisesRegex(release_env.EnvironmentError_, "c{40}.*--release-version"):
            release_env.release_version(None, "c" * 40)

        def fetch_fails(*args, **kwargs):
            raise release_env.EnvironmentError_("git failed (exit 128)")

        with mock.patch.object(release_env, "run", fetch_fails), \
                self.assertRaisesRegex(release_env.EnvironmentError_, "fetch origin failed.*--release-version"):
            release_env.release_version(None, "c" * 40)

    def test_the_release_string_rides_in_the_server_environment(self):
        environment = load(BASE)["environments"]["alone"]
        words = release_env.release_variables(environment, None, "live", "v1.9.5")["RELEASE_SERVER_ENV"].split()
        self.assertIn("RELEASE_VERSION=v1.9.5", words)
        words = release_env.release_variables(environment, None)["RELEASE_SERVER_ENV"]
        self.assertNotIn("RELEASE_VERSION", words)

    def test_release_passes_the_asked_or_described_version_to_the_server_steps(self):
        document = load(BASE)
        seen = []

        class Done:
            returncode = 1

        def fake_run(command, env):
            seen.append(env["RELEASE_SERVER_ENV"].split())
            return Done()

        for requested, expected in (("v1.9.5", "v1.9.5"), (None, "v1.9.4-3-gabcdef0")):
            args = release_env.argparse.Namespace(revision=None, acceptance_note=None, live_upstream=False,
                                                  release_version=requested)
            with self.subTest(requested=requested), \
                    mock.patch.object(release_env, "run", return_value="v1.9.4-3-gabcdef0\n"), \
                    mock.patch.object(release_env.subprocess, "run", fake_run), \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(release_env.release(document, "alone", args), 1)
                self.assertIn(f"RELEASE_VERSION={expected}", seen[-1])

    def test_main_accepts_release_version(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "environments.json"
            path.write_text(json.dumps(BASE))
            with mock.patch.object(release_env, "release", return_value=0) as release:
                self.assertEqual(release_env.main(["--file", str(path), "release", "alone", "--release-version", "v1.9.5"]), 0)
        self.assertEqual(release.call_args.args[2].release_version, "v1.9.5")


class PostReleaseSmokeTest(unittest.TestCase):
    """#411: a successful fake-shape release runs L0/L1/L1 Agent and leaves a JSON report."""

    SHA = "a" * 40

    class Done:
        def __init__(self, stdout=b"", code=0):
            self.stdout, self.stderr, self.returncode = stdout, b"", code

    def passing(self, name="L1"):
        return {"status": "pass", "passed": 3, "failed": 0, "skipped": 0, "soft_failed": 0,
                "script": f"{name}.sh", "started_at": "2026-11-22T00:00:00Z", "finished_at": "2026-11-22T00:01:00Z"}

    def test_a_layer_reads_its_own_counters(self):
        output = b"INFO: upstream shape fake\nPASS: healthz\n=== SMOKE-L1 PASS=3 FAIL=0 ===\n"
        with mock.patch.object(release_env.subprocess, "run", return_value=self.Done(output)):
            check, kept = release_env.smoke_layer("smoke-l1-free.sh", "L1", {})
        self.assertEqual((check["status"], check["passed"], check["failed"], check["skipped"], check["soft_failed"]),
                         ("pass", 3, 0, 0, 0))
        self.assertEqual(kept.count("PASS: healthz"), 1)
        self.assertEqual(check["script"], "smoke-l1-free.sh")
        self.assertNotIn("ADMIN_PASSWORD", kept)

    def test_a_skipped_soft_failed_or_silent_layer_is_not_a_pass(self):
        cases = [(b"=== SMOKE-L1 PASS=3 FAIL=1 ===\n", 1, "fail"),
                 (b"=== SMOKE-L1-AGENT-MODE PASS=3 FAIL=0 SKIP=1 ===\n", 0, "fail"),
                 (b"SOFT-FAIL: approval card not reached\n=== SMOKE-L1 PASS=3 FAIL=0 ===\n", 0, "fail"),
                 (b"no summary at all\n", 0, "fail"),
                 (b"", 0, "fail"),
                 (b"=== SMOKE-L1 PASS=3 FAIL=0 SKIP=0 ===\n", 0, "pass")]
        for output, code, want in cases:
            with self.subTest(output=output, code=code):
                with mock.patch.object(release_env.subprocess, "run", return_value=self.Done(output, code)):
                    check, _ = release_env.smoke_layer("smoke-l1-free.sh", "L1", {})
                self.assertEqual(check["status"], want)
        # The soft-fail line is counted, so the report says why the layer is not a pass.
        with mock.patch.object(release_env.subprocess, "run", return_value=self.Done(cases[2][0])):
            check, _ = release_env.smoke_layer("smoke-l1-free.sh", "L1", {})
        self.assertEqual((check["status"], check["passed"], check["soft_failed"]), ("fail", 3, 1))

    def test_a_timeout_is_a_failed_layer_not_an_error(self):
        with mock.patch.object(release_env.subprocess, "run",
                               side_effect=release_env.subprocess.TimeoutExpired("bash", 1, output=b"PASS: a")):
            check, output = release_env.smoke_layer("smoke-l1-free.sh", "L1", {})
        self.assertEqual(check["status"], "fail")
        self.assertIn("timed out", output)

    def test_the_report_holds_one_check_per_layer_and_writes_one_log_each(self):
        document = ephemeral_document()
        written = {}

        def fake_write(env, path, content, directory_mode=None):
            written[path] = (content, directory_mode)

        with mock.patch.object(release_env, "smoke_variables", return_value={"ART_AI_UPSTREAM_SHAPE": "fake",
                                                                            "ADMIN_PASSWORD": "p"}), \
                mock.patch.object(release_env, "smoke_layer",
                                  side_effect=lambda script, layer, variables: (self.passing(layer), "PASS=3 FAIL=0\n")), \
                mock.patch.object(release_env, "write_host_file", fake_write), \
                contextlib.redirect_stdout(io.StringIO()):
            report, path = release_env.smoke_report(document, "dev-test", self.SHA)
        directory = f"/srv/dev-test/production/reports/smoke-{self.SHA}"
        self.assertEqual(path, f"{directory}.json")
        self.assertEqual(report["schema"], "plinth.smoke-report")
        self.assertEqual((report["version"], report["revision"], report["environment"], report["upstream_shape"]),
                         (1, self.SHA, "dev-test", "fake"))
        self.assertEqual(sorted(report["checks"]), ["L0", "L1", "L1_agent"])
        for layer in ("L0", "L1", "L1_agent"):
            self.assertEqual(report["checks"][layer]["log"], f"{directory}/{layer}.log")
            self.assertIn("PASS=3 FAIL=0", written[f"{directory}/{layer}.log"][0])
        self.assertEqual(written[f"{directory}/L0.log"][1], "700")
        stored = json.loads(written[f"{directory}.json"][0])
        self.assertEqual(stored["checks"]["L1_agent"]["status"], "pass")
        self.assertNotIn("ADMIN_PASSWORD", written[f"{directory}.json"][0])

    def test_a_failed_layer_fails_the_release_after_it_is_deployed(self):
        document = load(BASE)
        report = {"checks": {"L0": self.passing("L0"), "L1": {**self.passing("L1"), "status": "fail"}, "L1_agent": self.passing("L1_agent")}}
        output = io.StringIO()
        with mock.patch.object(release_env, "running_revision", return_value=self.SHA), \
                mock.patch.object(release_env, "smoke_report", return_value=(report, "/srv/dev-test/report.json")), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            code = release_env.post_release_smoke(document, "dev-test", "fake")
        text = output.getvalue()
        self.assertEqual(code, 1)
        self.assertIn("deployed", text)
        self.assertIn("L1", text)

    def test_a_passing_report_leaves_the_release_at_zero(self):
        document = load(BASE)
        report = {"checks": {name: self.passing(name) for name in ("L0", "L1", "L1_agent")}}
        with mock.patch.object(release_env, "running_revision", return_value=self.SHA), \
                mock.patch.object(release_env, "smoke_report", return_value=(report, "/srv/dev-test/report.json")), \
                contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(release_env.post_release_smoke(document, "dev-test", "fake"), 0)

    def test_a_live_release_keeps_the_manual_protocol(self):
        # L1 reaches a real chat model on a live stack, which needs the user's consent per run.
        document = load(BASE)
        with mock.patch.object(release_env, "smoke_report") as smoke_report, \
                contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(release_env.post_release_smoke(document, "dev-test", "live"), 0)
        smoke_report.assert_not_called()

    def test_a_release_without_a_report_says_so_and_fails(self):
        document = load(BASE)
        output = io.StringIO()
        with mock.patch.object(release_env, "running_revision", return_value=self.SHA), \
                mock.patch.object(release_env, "smoke_report",
                                  side_effect=release_env.EnvironmentError_("ssh failed")), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            self.assertEqual(release_env.post_release_smoke(document, "dev-test", "fake"), 1)
        self.assertIn("deployed", output.getvalue())

    def test_a_promotion_reads_the_source_smoke_report_when_it_exists(self):
        report = {"schema": "plinth.smoke-report", "version": 1, "revision": self.SHA,
                  "checks": {name: {"status": "pass", "passed": 3, "failed": 0, "skipped": 0, "soft_failed": 0}
                             for name in ("L0", "L1", "L1_agent")}}
        _, commands, _ = self.run_promotion(report)
        rules = release_env.promotion_rules()
        self.assertTrue([command for command in commands
                         if command.endswith(f"cat /srv/dev-test/{rules.smoke_report_path(self.SHA)}")])

    def test_a_promotion_refuses_a_source_report_that_is_not_passing(self):
        for report in ({"schema": "plinth.smoke-report", "version": 1, "revision": self.SHA,
                        "checks": {name: {"status": "fail", "passed": 1, "failed": 2}
                                   for name in ("L0", "L1", "L1_agent")}},
                       {"schema": "plinth.smoke-report", "version": 1, "revision": "b" * 40,
                        "checks": {name: {"status": "pass", "passed": 1, "failed": 0}
                                   for name in ("L0", "L1", "L1_agent")}},
                       {"schema": "plinth.smoke-report", "version": 1, "revision": self.SHA,
                        "checks": {name: {"status": "pass", "passed": 1, "failed": 0, "skipped": 1}
                                   for name in ("L0", "L1", "L1_agent")}}):
            with self.subTest(report=report["checks"]["L0"]):
                with self.assertRaisesRegex(release_env.EnvironmentError_, "smoke report refused"):
                    self.run_promotion(report)

    def test_a_promotion_without_a_source_report_falls_back_to_the_note(self):
        _, commands, output = self.run_promotion(None, present=False)
        self.assertFalse([command for command in commands if "smoke-" in command])
        self.assertIn("no machine smoke report", output)

    # A promoted environment: dev-test is the source, prod takes its accepted revisions.
    PROMOTED = {"ssh": "root@host-four", "root": "/srv/prod", "base_url": "http://host-four:8080",
                "compose_project": "prod", "single_stack": {"image_keep": 1}, "protected_paths": [],
                "promote_from": "dev-test"}
    TAG = "aaaaaaa-20261019T010000Z"
    EVIDENCE = "https://forge.example.invalid/owner/project/issues/63#c1"

    GATE_EVIDENCE = "https://forge.example.invalid/owner/project/issues/63#issuecomment-9"
    GOOD_SMOKE = {"schema": "plinth.smoke-report", "version": 1, "revision": "a" * 40,
                  "checks": {name: {"status": "pass", "passed": 3, "failed": 0, "skipped": 0, "soft_failed": 0}
                             for name in ("L0", "L1", "L1_agent")}}

    def gated_note(self, **gate):
        note = {"prod_promotion": 2, "revision": self.SHA, "image_tag": self.TAG,
                "checks": {name: {"status": "pass", "failed": 0, "passed": 1, "evidence": self.EVIDENCE}
                           for name in ("L0", "L1", "L1_agent", "related")},
                "release_gate": {"status": "pass", "evidence": self.GATE_EVIDENCE}}
        note["release_gate"].update(gate)
        return note

    def gate_receipt(self, **changes):
        receipt = {"release_gate": 1, "revision": self.SHA, "status": "pass", "rules_version": 1,
                   "base": "v1.9.5", "changed_issues": [704], "checked_at": "2026-11-28"}
        receipt.update(changes)
        return receipt

    def test_a_promotion_reads_the_gate_receipt_named_by_the_acceptance_report(self):
        _, _, _, calls = self.run_promotion(self.GOOD_SMOKE, with_calls=True)
        self.assertEqual(calls, [("owner/project", 63, 5), ("owner/project", 63, 9)])

    def test_a_format_one_acceptance_report_is_refused_for_a_new_promotion(self):
        note = self.gated_note()
        note["prod_promotion"] = 1
        del note["release_gate"]
        with self.assertRaisesRegex(release_env.EnvironmentError_, "prod_promotion 2"):
            self.run_promotion(self.GOOD_SMOKE, note=note)

    def test_a_missing_failed_or_foreign_gate_record_is_refused_before_any_host_is_read(self):
        notes = []
        note = self.gated_note(); del note["release_gate"]; notes.append(note)
        notes.append(self.gated_note(status="fail"))
        notes.append(self.gated_note(evidence="https://example.com/owner/project/issues/63#issuecomment-9"))
        for note in notes:
            with self.subTest(gate=note.get("release_gate")):
                commands = []
                with self.assertRaisesRegex(release_env.EnvironmentError_, "acceptance refused"):
                    self.run_promotion(self.GOOD_SMOKE, note=note, commands=commands)
                self.assertFalse([command for command in commands if "manifest" in command or "smoke-" in command])

    def test_a_gate_receipt_for_another_revision_or_not_passing_is_refused(self):
        for receipt in (self.gate_receipt(revision="b" * 40), self.gate_receipt(status="fail"),
                        self.gate_receipt(release_gate=2), None):
            with self.subTest(receipt=receipt):
                commands = []
                with self.assertRaisesRegex(release_env.EnvironmentError_, "release gate refused"):
                    self.run_promotion(self.GOOD_SMOKE, receipt=receipt, commands=commands)
                self.assertFalse([command for command in commands if "manifest" in command or "smoke-" in command])
                self.assertFalse([command for command in commands if "host-four" in command])

    def run_promotion(self, smoke_report, present=True, note=None, receipt="default", commands=None,
                      with_calls=False):
        """Drive promotion_revision with the source side stubbed; return (revision, commands, output)."""
        document = copy.deepcopy(BASE)
        document["acceptance"] = {"forge_repo": "owner/project", "issue": 63,
                                  "forge_url": "https://forge.example.invalid/owner/project"}
        document["environments"]["prod"] = dict(self.PROMOTED)
        document = load(document)
        rules = release_env.promotion_rules()
        note = self.gated_note() if note is None else note
        receipt = self.gate_receipt() if receipt == "default" else receipt
        commands = [] if commands is None else commands
        calls = []

        def fake_run(*args, input=None, timeout=120):
            command = " ".join(str(arg) for arg in args)
            commands.append(command)
            if command.endswith("manifest.txt"):
                return f"{self.TAG} {self.SHA} 2026-11-19T01:00:00Z\n"
            if "smoke-" in command:
                return json.dumps(smoke_report) if smoke_report is not None else ""
            return ""

        def fake_comment(repo, issue, comment_id):
            calls.append((repo, issue, comment_id))
            if comment_id == 5:
                return {"body": "```json\n" + json.dumps(note) + "\n```"}
            return {"body": "门禁回执\n```json\n" + json.dumps(receipt) + "\n```" if receipt else "无回执"}

        output = io.StringIO()
        with mock.patch.object(release_env, "promotion_rules", return_value=rules), \
                mock.patch.object(rules, "read_acceptance_comment", side_effect=fake_comment), \
                mock.patch.object(release_env, "running_revision", return_value=self.SHA), \
                mock.patch.object(release_env, "host_file_state", return_value=present), \
                mock.patch.object(release_env, "run", fake_run), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            revision = release_env.promotion_revision(document, "prod", self.SHA, 5)
        if with_calls:
            return revision, commands, output.getvalue(), calls
        return revision, commands, output.getvalue()


if __name__ == "__main__":
    unittest.main()
