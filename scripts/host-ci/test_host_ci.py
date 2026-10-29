"""Offline checks of the host CI driver's pure logic (#97): python3 -m unittest scripts/host-ci/test_host_ci.py

No SSH, Docker or network. The end-to-end proof is a real run on the CI host (dev_test.md section 2).
"""
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f'{name}.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


run = load('run')
host_run = load('host_run')


class ToolchainHash(unittest.TestCase):
    FILES = [('a', b'one'), ('b', b'two')]

    def test_stable_and_twelve_hex_digits(self):
        self.assertEqual(run.toolchain_hash(self.FILES), run.toolchain_hash(list(self.FILES)))
        self.assertRegex(run.toolchain_hash(self.FILES), r'^[0-9a-f]{12}$')

    def test_any_change_of_name_or_content_changes_the_tag(self):
        base = run.toolchain_hash(self.FILES)
        self.assertNotEqual(base, run.toolchain_hash([('a', b'one'), ('b', b'twp')]))
        self.assertNotEqual(base, run.toolchain_hash([('a', b'one'), ('c', b'two')]))
        # Moving bytes between neighbours must not collide.
        self.assertNotEqual(run.toolchain_hash([('a', b'on'), ('b', b'etwo')]), base)

    def test_context_tar_holds_exactly_the_hashed_files(self):
        with tarfile.open(fileobj=io.BytesIO(run.context_tar(self.FILES))) as archive:
            self.assertEqual([m.name for m in archive.getmembers()], ['a', 'b'])
            self.assertEqual(archive.extractfile('b').read(), b'two')
        self.assertEqual(run.context_tar(self.FILES), run.context_tar(self.FILES))

    def test_image_inputs_are_the_documented_files(self):
        self.assertEqual(run.TOOL_FILES, ('scripts/host-ci/Dockerfile', 'scripts/host-ci/entry.sh'))
        self.assertEqual(run.MANIFEST_FILES, ('package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', 'go.mod', 'go.sum'))
        dockerfile = (HERE / 'Dockerfile').read_text()
        for path in run.MANIFEST_FILES:
            self.assertIn(path, dockerfile, f'{path} decides the image tag but the Dockerfile does not copy it')


class EnvironmentLookup(unittest.TestCase):
    def load(self, environments):
        with tempfile.NamedTemporaryFile('w', suffix='.json') as handle:
            json.dump({'environments': environments}, handle)
            handle.flush()
            with mock.patch.object(run.local_environments, 'environments_file', return_value=Path(handle.name)):
                return run.load_environment('dev-test')

    def test_default_directory_is_separate_from_the_release_root(self):
        target, directory = self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth'}})
        self.assertEqual((target, directory), ('root@host.example', run.DEFAULT_CI_DIR))

    def test_directory_overlapping_a_release_root_or_protected_path_is_refused(self):
        for configured in ('/srv/plinth', '/srv/plinth/ci', '/srv', '/data/other'):
            with self.subTest(configured), self.assertRaises(run.UsageError):
                self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth', 'protected_paths': ['/data/other'],
                                        'host_ci': {'dir': configured}}})

    def test_unsafe_directory_text_is_refused(self):
        for configured in ('relative', '/a/../b', '/a b', '/a;rm', '/trailing/'):
            with self.subTest(configured), self.assertRaises(run.UsageError):
                self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth', 'host_ci': {'dir': configured}}})


class HostSide(unittest.TestCase):
    def test_verdict_requires_every_condition(self):
        passing = {'container_exit_code': 0, 'oom_killed': False, 'timed_out': False,
                   'entry': {'status': 'passed', 'cgroup': {'oom_kill': 0}, 'gates': [{'name': 'go vet', 'result': 'pass'}]}}
        self.assertEqual(host_run.verdict(passing), [])
        for change, expected in (
                ({'container_exit_code': 137}, 'container exit code 137'),
                ({'oom_killed': True}, 'container OOMKilled'),
                ({'timed_out': True}, 'timeout')):
            self.assertIn(expected, host_run.verdict({**passing, **change}))
        oom = json.loads(json.dumps(passing))
        oom['entry']['cgroup']['oom_kill'] = 1
        self.assertIn('cgroup oom_kill counter is not zero', host_run.verdict(oom))
        skipped = json.loads(json.dumps(passing))
        skipped['entry']['gates'].append({'name': 'go test race', 'result': 'not_run'})
        self.assertIn('gate go test race: not_run', host_run.verdict(skipped))
        self.assertIn('no gate results', host_run.verdict({**passing, 'entry': {'status': 'passed'}}))

    def test_remove_tree_refuses_anything_not_directly_below_its_base(self):
        with tempfile.TemporaryDirectory() as base:
            nested = Path(base) / 'a' / 'b'
            nested.mkdir(parents=True)
            with self.assertRaises(RuntimeError):
                host_run.remove_tree(nested, base)
            with self.assertRaises(RuntimeError):
                host_run.remove_tree(Path(base), base)
            host_run.remove_tree(Path(base) / 'a', base)
            self.assertFalse((Path(base) / 'a').exists())


if __name__ == '__main__':
    unittest.main()
