"""Offline checks of the host CI driver (#97, #86): python3 scripts/host-ci/test_host_ci.py

No SSH, Docker or network: Docker, the container child process and the host files are stubbed, so the
OOM, timeout, preflight and lock paths run as regressions. This is the dev_test.md section 2 gate
"host ci test". The end-to-end proof is still a real run on the CI host.
"""
import argparse
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import threading
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
        self.assertEqual(run.TOOL_FILES, ('Dockerfile.ci', 'scripts/host-ci/entry.sh'))
        self.assertEqual(run.MANIFEST_FILES, ('package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', 'go.mod', 'go.sum'))
        dockerfile = (HERE.parent.parent / 'Dockerfile.ci').read_text()
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
        target, directory, host_ci = self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth'}})
        self.assertEqual((target, directory, host_ci), ('root@host.example', run.DEFAULT_CI_DIR, {}))

    def test_directory_overlapping_a_release_root_or_protected_path_is_refused(self):
        for configured in ('/srv/plinth', '/srv/plinth/ci', '/srv', '/data/other'):
            with self.subTest(configured), self.assertRaises(run.UsageError):
                self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth', 'protected_paths': ['/data/other'],
                                        'host_ci': {'dir': configured}}})

    def test_unsafe_directory_text_is_refused(self):
        for configured in ('relative', '/a/../b', '/a b', '/a;rm', '/trailing/'):
            with self.subTest(configured), self.assertRaises(run.UsageError):
                self.load({'dev-test': {'ssh': 'root@host.example', 'root': '/srv/plinth', 'host_ci': {'dir': configured}}})


class Limits(unittest.TestCase):
    """The preflight thresholds and timeouts are configurable and validated (#86)."""

    def args(self, **given):
        values = {key: None for key in run.LIMIT_DEFAULTS}
        values.update(given)
        return argparse.Namespace(**values)

    def test_the_two_copies_of_the_defaults_agree(self):
        self.assertEqual(run.LIMIT_DEFAULTS, host_run.DEFAULTS)

    def test_command_line_beats_environment_file_beats_default(self):
        self.assertEqual(run.resolve_limits(self.args(), {}), run.LIMIT_DEFAULTS)
        self.assertEqual(run.resolve_limits(self.args(), {'min_mem_gib': 20, 'timeout_min': 30})['min_mem_gib'], 20)
        limits = run.resolve_limits(self.args(min_mem_gib=18), {'min_mem_gib': 20, 'timeout_min': 30})
        self.assertEqual((limits['min_mem_gib'], limits['timeout_min'], limits['min_disk_gib']), (18, 30, 10))

    def test_non_numbers_in_the_environment_file_are_refused(self):
        for bad in ('15', True, None, [15], float('nan')):
            with self.subTest(bad), self.assertRaises(run.UsageError):
                run.resolve_limits(self.args(), {'min_mem_gib': bad} if bad is not None else {'min_mem_gib': {}})

    def test_environment_file_host_ci_must_be_an_object(self):
        with tempfile.NamedTemporaryFile('w', suffix='.json') as handle:
            json.dump({'environments': {'dev-test': {'ssh': 'root@host.example', 'root': '/srv/p', 'host_ci': 'x'}}}, handle)
            handle.flush()
            with mock.patch.object(run.local_environments, 'environments_file', return_value=Path(handle.name)), \
                    self.assertRaises(run.UsageError):
                run.load_environment('dev-test')

    def test_the_host_refuses_limits_that_weaken_the_gate(self):
        def problems(**given):
            values = {**host_run.DEFAULTS, **given}
            return host_run.limit_problems(argparse.Namespace(**values))
        self.assertEqual(problems(), [])
        self.assertEqual(problems(min_mem_gib=13, memory_gib=12), [])
        for given in ({'min_mem_gib': 12}, {'min_mem_gib': 0}, {'min_disk_gib': 0}, {'timeout_min': 0},
                      {'timeout_min': 24 * 60 + 1}, {'cpus': 0}, {'memory_gib': 0}, {'build_timeout_min': 0}):
            with self.subTest(given):
                self.assertTrue(problems(**given))


def preflight_args(**given):
    return argparse.Namespace(**{**host_run.DEFAULTS, **given})


def fake_meminfo(available_gib, swap_total_gib=8, swap_free_gib=0.2):
    gib = host_run.GIB
    return {'MemAvailable': int(available_gib * gib), 'SwapTotal': int(swap_total_gib * gib), 'SwapFree': int(swap_free_gib * gib)}


class Preflight(unittest.TestCase):
    def run_preflight(self, meminfo, ci_free_gib=100, docker_free_gib=100, **limits):
        frees = {'ci': ci_free_gib * host_run.GIB, 'docker': docker_free_gib * host_run.GIB}
        docker_info = subprocess.CompletedProcess([], 0, stdout='/var/lib/docker\n', stderr='')
        with mock.patch.object(host_run, 'meminfo', return_value=meminfo), mock.patch.object(host_run, 'say'), \
                mock.patch.object(host_run, 'docker', return_value=docker_info), \
                mock.patch.object(host_run, 'free_bytes', side_effect=lambda path: frees['docker' if 'docker' in str(path) else 'ci']):
            return host_run.preflight(Path('/ci'), preflight_args(**limits))

    def test_enough_memory_and_disk_passes(self):
        self.assertEqual(self.run_preflight(fake_meminfo(16.3))['problems'], [])

    def test_memory_just_under_the_threshold_blocks_and_just_over_passes(self):
        self.assertEqual(self.run_preflight(fake_meminfo(14.99))['problems'], ['available memory'])
        self.assertEqual(self.run_preflight(fake_meminfo(15.0))['problems'], [])

    def test_memory_threshold_is_configurable(self):
        self.assertEqual(self.run_preflight(fake_meminfo(14), min_mem_gib=13.5)['problems'], [])
        facts = self.run_preflight(fake_meminfo(14), min_mem_gib=20)
        self.assertEqual(facts['problems'], ['available memory'])
        self.assertEqual(facts['required']['mem_available_bytes'], 20 * host_run.GIB)

    def test_each_disk_is_checked_against_its_own_threshold(self):
        self.assertEqual(self.run_preflight(fake_meminfo(20), ci_free_gib=9)['problems'], ['ci disk free bytes'])
        self.assertEqual(self.run_preflight(fake_meminfo(20), docker_free_gib=9)['problems'], ['docker disk free bytes'])
        self.assertEqual(self.run_preflight(fake_meminfo(20), ci_free_gib=9, min_disk_gib=5)['problems'], [])

    def test_nearly_full_swap_is_a_warning_not_a_failure(self):
        facts = self.run_preflight(fake_meminfo(16.3, swap_total_gib=8, swap_free_gib=0.2))
        self.assertEqual((facts['problems'], facts['warnings']), ([], ['swap nearly full']))
        self.assertEqual(self.run_preflight(fake_meminfo(16.3, swap_free_gib=6))['warnings'], [])
        self.assertEqual(self.run_preflight(fake_meminfo(16.3, swap_total_gib=0, swap_free_gib=0))['warnings'], [])


class FakeDocker:
    """Records docker calls; `inspect` returns the configured container state."""

    def __init__(self, state, stopped=None):
        self.calls = []
        self.state = state
        self.stopped = stopped

    def __call__(self, *args, check=True, timeout=120, stdin=None):
        self.calls.append(args)
        if args[0] == 'inspect':
            return subprocess.CompletedProcess(args, 0, stdout=json.dumps([{'State': self.state}]), stderr='')
        if args[0] == 'stop' and self.stopped is not None:
            self.stopped.set()
        return subprocess.CompletedProcess(args, 0, stdout='', stderr='')


class FakeChild:
    """Stands in for `docker start --attach`; output ends at once, or when the container is stopped."""

    def __init__(self, wait_for=None):
        self.stdout = self
        self.wait_for = wait_for

    def read1(self, size):
        if self.wait_for is not None and not self.wait_for.wait(10):
            raise AssertionError('the container was never stopped')
        return b''

    def wait(self):
        return 0


class AttemptOutcomes(unittest.TestCase):
    """OOM and timeout regressions for #97's open item: run_attempt with stubbed Docker."""

    def attempt(self, state, entry_result=None, timeout_min=100, waits_for_stop=False):
        stopped = threading.Event() if waits_for_stop else None
        fake = FakeDocker(state, stopped)
        with tempfile.TemporaryDirectory() as base:
            root = Path(base)
            run_dir = root / 'run' / 'r1'
            run_dir.mkdir(parents=True)
            args = preflight_args(run_id='r1', sha='a' * 40, tree='b' * 40, timeout_min=timeout_min)
            ctx = {'args': args, 'root': root, 'run_dir': run_dir, 'containers': [], 'image': {'id': 'sha256:img'}}
            original_mkdir = Path.mkdir

            def mkdir_then_result(path, *a, **k):
                original_mkdir(path, *a, **k)
                if path.name == 'out' and entry_result is not None:
                    (path / 'result.json').write_text(json.dumps(entry_result))

            with mock.patch.object(host_run, 'docker', fake), \
                    mock.patch.object(host_run.os, 'chown'), \
                    mock.patch.object(host_run, 'configuration_problems', return_value=[]), \
                    mock.patch.object(Path, 'mkdir', mkdir_then_result), \
                    mock.patch.object(host_run.subprocess, 'Popen', return_value=FakeChild(stopped)), \
                    mock.patch.object(host_run, 'say'), mock.patch.object(host_run.sys, 'stdout', io.TextIOWrapper(io.BytesIO())):
                record, retry = host_run.run_attempt(1, ctx)
        return record, retry, fake

    PASSED = {'status': 'passed', 'cgroup': {'oom_kill': 0}, 'gates': [{'name': 'go vet', 'result': 'pass'}]}

    def test_a_clean_run_yields_no_failure_reasons(self):
        record, retry, _ = self.attempt({'ExitCode': 0, 'OOMKilled': False}, self.PASSED)
        self.assertEqual((record['status'], record['timed_out'], retry), ('passed', False, False))
        self.assertEqual(host_run.verdict(record), [])

    def test_a_container_killed_by_the_oom_killer_fails_and_is_not_retried(self):
        record, retry, _ = self.attempt({'ExitCode': 137, 'OOMKilled': True})
        self.assertTrue(record['oom_killed'])
        self.assertFalse(retry)
        reasons = host_run.verdict(record)
        self.assertIn('container OOMKilled', reasons)
        self.assertIn('container exit code 137', reasons)
        self.assertIn('entry status missing', reasons)

    def test_an_oom_inside_the_container_is_caught_from_the_cgroup_counter_even_with_exit_zero(self):
        entry = {**self.PASSED, 'cgroup': {'oom_kill': 2}}
        record, _, _ = self.attempt({'ExitCode': 0, 'OOMKilled': False}, entry)
        self.assertEqual(host_run.verdict(record), ['cgroup oom_kill counter is not zero'])

    def test_a_run_over_the_timeout_is_stopped_and_fails(self):
        record, retry, fake = self.attempt({'ExitCode': 143, 'OOMKilled': False}, timeout_min=0.0005, waits_for_stop=True)
        self.assertTrue(record['timed_out'])
        self.assertEqual(record['status'], 'timeout')
        self.assertFalse(retry)
        self.assertEqual([call for call in fake.calls if call[0] == 'stop'], [('stop', '--time', '20', 'plinth-ci-r1-a1')])
        self.assertIn('timeout', host_run.verdict(record))

    def test_no_timeout_stop_is_issued_when_the_run_ends_in_time(self):
        _, _, fake = self.attempt({'ExitCode': 0, 'OOMKilled': False}, self.PASSED)
        self.assertEqual([call for call in fake.calls if call[0] == 'stop'], [])

    def test_only_a_staging_identity_mismatch_is_retried(self):
        _, retry, _ = self.attempt({'ExitCode': 20, 'OOMKilled': False}, {'status': 'stage_mismatch'})
        self.assertTrue(retry)
        _, retry, _ = self.attempt({'ExitCode': 1, 'OOMKilled': False}, {'status': 'failed'})
        self.assertFalse(retry)


class Concurrency(unittest.TestCase):
    def test_a_second_run_cannot_take_the_lock_until_the_first_releases_it(self):
        with tempfile.TemporaryDirectory() as base:
            first = host_run.acquire_lock(Path(base))
            self.assertIsNotNone(first)
            self.assertIsNone(host_run.acquire_lock(Path(base)))
            first.close()
            again = host_run.acquire_lock(Path(base))
            self.assertIsNotNone(again)
            again.close()


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
