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
            "managed_by": "release-go-formal.sh",
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
                mock.patch.object(release_env, "running_revision", return_value="0" * 40), \
                mock.patch.object(release_env, "container", return_value="c"), \
                mock.patch.object(release_env.subprocess, "run", fake_run):
            name = next(iter(document["environments"]))
            self.assertEqual(release_env.smoke(document, name, ["true"]), 0)
        self.assertEqual(seen["RELEASE_ENV"], name)
        self.assertEqual(seen["BASE"], document["environments"][name]["base_url"])


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

    def test_obsolete_npm_registry_is_refused_with_a_removal_hint(self):
        # #257: builds use the public registry; a leftover field must not be
        # silently ignored, or the local file would keep claiming a mirror.
        document = changed(lambda environments: environments["shared-a"].update(
            build={"npm_registry": "https://npm.internal.test/npm/"}))
        with self.assertRaisesRegex(release_env.EnvironmentError_, "build.npm_registry was removed"):
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

    def test_dev_test_backup_cannot_be_managed_or_promoted(self):
        for field, value in (("managed_by", "release-go-formal.sh"), ("promote_from", "shared-a")):
            def mutate(environments, field=field, value=value):
                environments["dev-test"][field] = value
            with self.subTest(field=field), self.assertRaisesRegex(release_env.EnvironmentError_, "promote_from or managed_by"):
                load(changed(mutate))

    def test_backup_settings_are_strict(self):
        for backup in ({"scope": "full", "keep": 3}, {"scope": "database"}, {"scope": "database", "keep": 0},
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


if __name__ == "__main__":
    unittest.main()
