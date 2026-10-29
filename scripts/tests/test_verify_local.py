import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('verify_local', Path(__file__).parents[1] / 'verify-local.py')
v = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v)

# The native input monitor and the fingerprint binding use macOS FSEvents (dev_test.md section 2); the
# gate "scripts tests" also runs on Linux (CI, host CI container), where these classes are skipped.
requires_macos = unittest.skipUnless(sys.platform == 'darwin', 'macOS FSEvents-based monitor and fingerprint tests')


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.git('init', '-q')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('config', 'user.name', 'Test')
        (self.root / '.gitignore').write_text('ignored\n')
        (self.root / 'source').write_text('one')
        self.git('add', '.')
        self.git('commit', '-qm', 'initial')
    def tearDown(self):
        self.temp.cleanup()
    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root).decode().strip()
    def test_other_tip_same_tree_rejected(self):
        old = self.git('rev-parse', 'HEAD')
        self.git('commit', '--allow-empty', '-qm', 'new tip')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, f'refs/heads/other {old} refs/heads/other {old}')
    def test_ignored_extensionless_bound(self):
        before = v.source_identity(self.root)
        (self.root / 'ignored').write_text('embedded bytes')
        self.assertNotEqual(before, v.source_identity(self.root))


class ToolCheckTests(unittest.TestCase):
    def test_reports_every_missing_tool(self):
        with patch.object(v.shutil, 'which', return_value=None):
            self.assertEqual(v.missing_tools(), v.REQUIRED_TOOLS)
            self.assertEqual(len(v.missing_tools(race=True)), len(v.REQUIRED_TOOLS) + 1)
            with self.assertRaisesRegex(v.GateError, 'jq'):
                v.require_tools()

    def test_race_needs_a_c_compiler_only_with_race(self):
        only_cc = lambda name: '/bin/cc' if name == 'cc' else '/bin/x'
        with patch.object(v.shutil, 'which', side_effect=only_cc):
            self.assertEqual(v.missing_tools(race=True), [])
        no_cc = lambda name: None if name in ('gcc', 'cc') else '/bin/x'
        with patch.object(v.shutil, 'which', side_effect=no_cc):
            self.assertEqual(v.missing_tools(), [])
            self.assertEqual(len(v.missing_tools(race=True)), 1)


class ReceiptTests(VerificationTests):
    def setUp(self):
        super().setUp()
        v.receipt_dir(self.root).mkdir()
        self.calls = 0
    def fake_identity(self, root):
        snapshot = v.Snapshot()
        v.source_snapshot(root, snapshot)
        return {'content': snapshot.digest(), 'worktree': v.digest_json(str(root)),
                'gates': v.digest_json([*v.GATES, v.RACE_GATE]), 'environment': 'test'}, snapshot
    def gates(self, root, gates=None):
        self.calls += 1
        self.ran = v.gate_names(gates)
    def verify(self, **kwargs):
        with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', self.gates):
            return v.verify(self.root, **kwargs)
    @property
    def receipt(self):
        return v.receipt_dir(self.root) / 'latest.json'
    def test_success_and_same_content_commit_reuse(self):
        self.verify()
        self.git('commit', '--allow-empty', '-qm', 'same content')
        self.verify()
        self.assertEqual(self.calls, 1)
    def test_change_requires_complete_run(self):
        self.verify()
        (self.root / 'source').write_text('new')
        self.verify()
        self.assertEqual(self.calls, 2)
    def test_default_run_is_the_local_set_and_race_is_opt_in(self):
        self.verify()
        self.assertEqual(self.ran, v.gate_names(v.GATES))
        self.assertNotIn('go test race', self.ran)
        self.verify(force=True, race=True)
        self.assertEqual(self.ran, ['frontend typecheck', 'frontend test', 'frontend build', 'go vet',
                                    'go test', 'go test race', 'go build', 'public tree', 'scripts tests'])
        self.assertEqual(v.selected_gates(True)[5], v.RACE_GATE)
        self.assertEqual(v.RACE_GATE[2], {'CGO_ENABLED': '1'})
    def test_fast_receipt_never_covers_race(self):
        import json
        self.verify()
        self.assertNotIn('go test race', json.loads(self.receipt.read_text())['gates'])
        self.assertFalse(self.verify(check=True, race=True))
        self.verify(race=True)
        self.assertEqual(self.calls, 2)
        self.assertIn('go test race', json.loads(self.receipt.read_text())['gates'])
    def test_race_receipt_covers_fast_run(self):
        self.verify(race=True)
        self.assertTrue(self.verify(check=True))
        self.verify()
        self.verify(race=True)
        self.assertEqual(self.calls, 1)
    def test_changed_race_definition_invalidates_fast_receipt(self):
        self.verify()
        with patch.object(v, 'RACE_GATE', ('go test race', ['go', 'test', '-race'], {})):
            self.verify()
        self.assertEqual(self.calls, 2)
    def test_changed_gate_definition_requires_full_run(self):
        self.verify()
        with patch.object(v, 'GATES', v.GATES + [('other', ['true'], {})]):
            self.verify()
        self.assertEqual(self.calls, 2)
    def test_latest_failure_cannot_revive_old_success(self):
        self.verify()
        with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', side_effect=v.GateError('failed')):
            with self.assertRaises(v.GateError):
                v.verify(self.root, force=True)
        self.assertFalse(self.receipt.exists())
        self.verify()
        self.assertEqual(self.calls, 2)
    def test_interruption_invalidates_previous_success(self):
        self.verify()
        with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                v.verify(self.root, force=True)
        self.assertFalse(self.receipt.exists())
    def test_aba_modification_rejected(self):
        def mutate(root, gates=None):
            path = root / 'source'
            original = path.read_bytes()
            path.write_bytes(b'other')
            path.write_bytes(original)
        with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', mutate):
            with self.assertRaises(v.GateError):
                v.verify(self.root)
        self.assertFalse(self.receipt.exists())
    def test_new_input_during_run_rejected(self):
        with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', lambda root, gates=None: (root / 'new').write_text('added')):
            with self.assertRaises(v.GateError):
                v.verify(self.root)
    def test_stale_invalid_partial_future_receipts(self):
        import json
        self.verify()
        good = json.loads(self.receipt.read_text())
        for key, value in [('completed_at', 0), ('completed_at', v.time.time() + 100),
                           ('gates', []), ('gates', None), ('gates', v.gate_names(v.GATES)[:-1]),
                           ('result', 'failed'), ('version', -1)]:
            data = {**good, key: value}
            self.receipt.write_text(json.dumps(data))
            self.assertFalse(self.verify(check=True))
        self.receipt.write_text('invalid JSON')
        self.assertFalse(self.verify(check=True))
    def test_different_worktree_does_not_share_receipt(self):
        import json
        self.verify()
        other = self.root.parent / (self.root.name + '-worktree')
        try:
            self.git('worktree', 'add', '--detach', str(other), 'HEAD')
            self.assertNotEqual(v.receipt_dir(self.root), v.receipt_dir(other))
            v.receipt_dir(other).mkdir()
            (v.receipt_dir(other) / 'latest.json').write_bytes(self.receipt.read_bytes())
            with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', self.gates):
                self.assertFalse(v.verify(other, check=True))
        finally:
            self.git('worktree', 'remove', '--force', str(other))
    def test_unsupported_fingerprint_runs_existing_gates_without_receipt(self):
        self.verify()
        with patch.object(v, 'identity', side_effect=v.Unbindable('unsupported')), patch.object(v, 'run_gates', self.gates):
            self.assertTrue(v.verify(self.root))
        self.assertFalse(self.receipt.exists())
        self.assertEqual(self.calls, 2)
    def test_reuse_checks_stat_again(self):
        self.verify()
        with patch.object(v, 'identity', self.fake_identity), patch.object(v.Snapshot, 'unchanged', return_value=False):
            with self.assertRaises(v.GateError):
                v.verify(self.root)

class PushAndFilesystemTests(VerificationTests):
    def push_line(self, tip=None, ref='refs/heads/test'):
        tip = tip or self.git('rev-parse', 'HEAD')
        return f'{ref} {tip} {ref} {"0" * 40}'
    def test_every_ref_checked(self):
        head = self.git('rev-parse', 'HEAD')
        self.git('commit', '--allow-empty', '-qm', 'new')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, self.push_line() + '\n' + self.push_line(head))
    def test_clean_current_tip_accepted(self):
        self.assertTrue(v.verify_push(self.root, self.push_line()))
    def test_deletion_only_safe_with_dirty_worktree(self):
        (self.root / 'source').write_text('dirty')
        self.assertFalse(v.verify_push(self.root, self.push_line('0' * 40)))
    def test_dirty_and_non_branch_and_bad_stdin_rejected(self):
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, self.push_line(ref='refs/tags/tag'))
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, 'invalid')
        (self.root / 'source').write_text('dirty')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, self.push_line())
    def test_external_symlink_actual_bytes_bound(self):
        external = self.root.parent / (self.root.name + '-external')
        try:
            external.mkdir()
            (external / 'input').write_text('one')
            (self.root / 'linked').symlink_to(external, target_is_directory=True)
            before = v.source_identity(self.root)
            (external / 'input').write_text('two')
            self.assertNotEqual(before, v.source_identity(self.root))
        finally:
            import shutil
            shutil.rmtree(external)
    def test_symlink_cycle_terminates(self):
        (self.root / 'cycle').symlink_to(self.root, target_is_directory=True)
        self.assertTrue(v.source_identity(self.root))
    def test_generated_output_excluded_but_tracked_output_bound(self):
        (self.root / 'build/go-web').mkdir(parents=True)
        output = self.root / 'build/go-web/index.html'
        output.write_text('one')
        before = v.source_identity(self.root)
        output.write_text('two')
        self.assertEqual(before, v.source_identity(self.root))
        self.git('add', 'build/go-web/index.html')
        before = v.source_identity(self.root)
        output.write_text('three')
        self.assertNotEqual(before, v.source_identity(self.root))
    def test_source_fingerprint_detects_file_mutation(self):
        snapshot = v.Snapshot()
        snapshot.add(self.root / 'source')
        (self.root / 'source').write_text('different')
        self.assertFalse(snapshot.unchanged())

class CLIIntegrationTests(VerificationTests):
    def setUp(self):
        super().setUp()
        self.tools = tempfile.TemporaryDirectory()
        self.bin = Path(self.tools.name)
        self.log = self.bin / 'calls'
        # 'bash' fakes the public tree gate (bash scripts/check-public-tree.sh, #261), 'python3' the scripts tests gate (#86).
        for name in ('go', 'pnpm', 'bash', 'python3'):
            command = self.bin / name
            command.write_text('#!/bin/sh\nif [ "$1" = env ]; then exit 1; fi\nprintf "%s\\n" "$*" >> "$GATE_TEST_LOG"\nexit "${GATE_TEST_EXIT:-0}"\n')
            command.chmod(0o755)
    def tearDown(self):
        self.tools.cleanup()
        super().tearDown()
    def run_cli(self, args=(), data=None, extra=None):
        import os
        import sys
        env = {**os.environ, 'PATH': str(self.bin) + os.pathsep + os.environ['PATH'],
               'GATE_TEST_LOG': str(self.log), **(extra or {})}
        return subprocess.run([sys.executable, str(Path(v.__file__).resolve()), *args], cwd=self.root,
                              env=env, input=data, text=True, capture_output=True)
    def test_fallback_executes_exact_local_gates_without_race(self):
        result = self.run_cli()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        calls = self.log.read_text().splitlines()
        self.assertEqual(calls, [' '.join(command[1:]) for _, command, _ in v.GATES])
        self.assertFalse(any('-race' in call for call in calls))
        self.assertIn(f'full: {len(v.GATES)} gates passed (', result.stdout)
        self.assertIn('go test race runs in CI only (--race adds it)', result.stdout)
        self.assertFalse((v.receipt_dir(self.root) / 'latest.json').exists())
    def test_race_flag_adds_race_gate_after_go_test(self):
        result = self.run_cli(['--race'])
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        calls = self.log.read_text().splitlines()
        self.assertEqual(len(calls), len(v.GATES) + 1)
        self.assertEqual(calls[5], 'test -race -count=1 -timeout=30m ./...')
        self.assertIn(f'full: {len(v.GATES) + 1} gates passed (', result.stdout)
        self.assertNotIn('CI only', result.stdout)
    def test_gate_failure_propagates(self):
        result = self.run_cli(extra={'GATE_TEST_EXIT': '8'})
        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(self.log.read_text().splitlines()), 1)
    def test_deletion_cli_does_not_run_gates(self):
        data = f'(delete) {"0" * 40} refs/heads/test {self.git("rev-parse", "HEAD")}\n'
        result = self.run_cli(['--pre-push'], data)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.log.exists())
    def test_wrong_tip_cli_does_not_run_gates(self):
        old = self.git('rev-parse', 'HEAD')
        self.git('commit', '--allow-empty', '-qm', 'new')
        result = self.run_cli(['--pre-push'], f'refs/heads/test {old} refs/heads/test {old}\n')
        self.assertEqual(result.returncode, 1)
        self.assertFalse(self.log.exists())
    def test_existing_lock_blocks_concurrent_run(self):
        import fcntl
        directory = v.receipt_dir(self.root)
        directory.mkdir()
        with (directory / 'run.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.run_cli()
        self.assertEqual(result.returncode, 1)
        self.assertIn('another local gate run', result.stderr)
        self.assertFalse(self.log.exists())
    def test_cli_waits_for_other_worktree_then_runs_local_gates(self):
        import fcntl
        import os
        import select
        import sys
        directory = v.queue_dir(self.root)
        directory.mkdir(parents=True)
        holder = (directory / 'slot-1.lock').open('a')
        fcntl.flock(holder, fcntl.LOCK_EX | fcntl.LOCK_NB)
        holder.write('{"pid": 1, "worktree": "other-worktree"}')
        holder.flush()
        env = {**os.environ, 'PATH': str(self.bin) + os.pathsep + os.environ['PATH'],
               'GATE_TEST_LOG': str(self.log)}
        env.pop(v.SLOTS_ENV, None)
        process = subprocess.Popen([sys.executable, str(Path(v.__file__).resolve())], cwd=self.root, env=env,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            waiting = ''
            deadline = v.time.monotonic() + 60
            while 'gate queue' not in waiting and v.time.monotonic() < deadline:
                readable, _, _ = select.select([process.stdout], [], [], 1)
                if readable:
                    line = process.stdout.readline()
                    if not line:
                        break
                    waiting = line
            self.assertIn("waiting for another worktree's gates", waiting)
            self.assertIn('other-worktree', waiting)
            self.assertFalse(self.log.exists())
        finally:
            holder.close()
        process.wait(timeout=120)
        output, error = process.stdout.read(), process.stderr.read()
        process.stdout.close()
        process.stderr.close()
        self.assertEqual(process.returncode, 0, error + output)
        self.assertIn('gate queue: acquired slot 1/1', output)
        self.assertEqual(len(self.log.read_text().splitlines()), len(v.GATES))

@requires_macos
class FingerprintTests(VerificationTests):
    def setUp(self):
        super().setUp()
        self.external_temp = tempfile.TemporaryDirectory()
        self.external = Path(self.external_temp.name)
        (self.root / 'node_modules/.pnpm/package').mkdir(parents=True)
        (self.root / 'node_modules/.pnpm/package/code.js').write_text('dependency')
        (self.root / 'node_modules/package').symlink_to('.pnpm/package', target_is_directory=True)
        self.sdk = self.external / 'sdk'
        self.sdk.mkdir()
        (self.sdk / 'header').write_text('sdk')
        self.toolchain = self.external / 'test.xctoolchain'
        self.clang = self.toolchain / 'usr/bin/clang'
        self.clang.parent.mkdir(parents=True)
        self.clang.write_text('clang')
        self.goroot = self.external / 'goroot'
        self.goroot.mkdir()
        (self.goroot / 'compiler').write_text('compiler')
        self.module = self.external / 'replacement'
        self.module.mkdir()
        (self.module / 'code.go').write_text('module')
        self.tool = self.external / 'tool'
        self.tool.write_bytes(b'\xcf\xfa\xed\xfetool bytes')
        self.tool.chmod(0o755)
        self.manager_store = self.external / 'manager-store'
        self.manager_store.mkdir()
        self.manager_runtime = self.manager_store / 'runtime'
        self.manager_runtime.write_text('manager runtime')
    def tearDown(self):
        self.external_temp.cleanup()
        super().tearDown()
    def capture(self, args, root, input_data=None, env_overrides=None, env_remove=()):
        import json
        if args[-1] == '--exec-path':
            return str(self.external / 'git-exec').encode()
        if args[:2] == ['git', 'ls-files']:
            return subprocess.check_output(args, cwd=root)
        if args[:2] == ['go', 'env']:
            return json.dumps({'GOROOT': str(self.goroot), 'CC': 'cc', 'CXX': 'c++'}).encode()
        if args[:2] == ['go', 'list'] and '-deps' in args:
            return b'{}'
        if args[:2] == ['go', 'list']:
            return json.dumps({'Path': 'example.test/module', 'Replace': {'Dir': str(self.module)}}).encode()
        if args[0] == 'otool':
            return b'tool:\n'
        if args == ['pnpm', 'config', 'get', 'package-manager-store-dir']:
            return str(self.manager_store).encode()
        if args[:3] == ['pnpm', 'config', 'get']:
            return b'undefined'
        if args == ['xcrun', '--show-sdk-path']:
            return str(self.sdk).encode()
        if args == ['xcrun', '--find', 'clang']:
            return str(self.clang).encode()
        return b'test version'
    def fingerprint(self, identity=None):
        with patch.object(v, 'capture', self.capture), patch.object(v.shutil, 'which', side_effect=lambda name, path=None: '/usr/bin/' + name if name in ('cc', 'c++') else str(self.tool)), patch.object(v.sys, 'platform', 'darwin'), patch.object(v.Path, 'home', return_value=self.external):
            return (identity or v.identity)(self.root)
    def test_dependency_modules_sdk_toolchain_and_user_config_bytes_bound(self):
        paths = [self.root / 'node_modules/.pnpm/package/code.js', self.module / 'code.go',
                 self.sdk / 'header', self.goroot / 'compiler', self.clang, self.external / '.npmrc', self.tool]
        for path in paths:
            with self.subTest(path=path):
                before, _ = self.fingerprint()
                path.write_bytes((b'\xcf\xfa\xed\xfe' if path == self.tool else b'') + ('changed-' + path.name).encode())
                after, _ = self.fingerprint()
                self.assertNotEqual(before, after)
    def test_hook_bookkeeping_ignored_real_flags_bound(self):
        before, _ = self.fingerprint()
        with patch.dict(v.os.environ, {'GIT_PREFIX': ''}):
            after, _ = self.fingerprint()
        self.assertEqual(before, after)
        with patch.dict(v.os.environ, {v.SLOTS_ENV: '3'}):
            queued, _ = self.fingerprint()
        self.assertEqual(before, queued)
        with patch.dict(v.os.environ, {'GOEXPERIMENT': 'changed'}):
            changed, _ = self.fingerprint()
        self.assertNotEqual(before, changed)
    def test_generated_vite_cache_excluded(self):
        cache = self.root / 'node_modules/.vite'
        cache.mkdir()
        (cache / 'derived').write_text('one')
        before, _ = self.fingerprint()
        (cache / 'derived').write_text('two')
        after, _ = self.fingerprint()
        self.assertEqual(before, after)
    def test_runtime_preload_disables_reuse(self):
        with patch.dict(v.os.environ, {'NODE_OPTIONS': '--require /unknown/module'}):
            with self.assertRaises(v.Unbindable):
                self.fingerprint()
    def test_missing_dependency_disables_reuse(self):
        import shutil
        shutil.rmtree(self.root / 'node_modules')
        with self.assertRaises(v.Unbindable):
            self.fingerprint()

class PnpmManagerStorageTests(FingerprintTests):
    def unknown_store(self, result):
        original = self.capture
        def capture(args, root, input_data=None, env_overrides=None, env_remove=()):
            if args == ['pnpm', 'config', 'get', 'package-manager-store-dir']:
                return result
            return original(args, root, input_data, env_overrides, env_remove)
        return patch.object(self, 'capture', capture)
    def test_unknown_manager_store_does_not_guess_home_cache(self):
        guessed = self.external / 'Library/pnpm/package-manager-store/tmp/engine-locks'
        guessed.mkdir(parents=True)
        for result in (b'', b'undefined', b'null', b' \n'):
            with self.subTest(result=result), self.unknown_store(result):
                with self.assertRaisesRegex(v.Unbindable, 'pnpm package-manager storage'):
                    self.fingerprint()
    def test_declared_store_bytes_and_transient_metadata_still_bound(self):
        before, snapshot = self.fingerprint()
        self.assertIn(str(self.manager_store), snapshot.entries)
        self.manager_runtime.write_text('changed runtime')
        self.assertFalse(snapshot.unchanged())
        after, _ = self.fingerprint()
        self.assertNotEqual(before, after)
        _, snapshot = self.fingerprint()
        transient = self.manager_store / 'transient-runtime'
        transient.write_text('transient')
        transient.unlink()
        self.assertFalse(snapshot.unchanged())
    def exercise_unknown(self, *, check=False, push=False, failure=False):
        import os
        import types
        directory = v.receipt_dir(self.root)
        directory.mkdir()
        receipt = directory / 'latest.json'
        # Even an existing success must not be accepted without provable inputs.
        receipt.write_text('{"result":"passed"}')
        guessed = self.external / 'Library/pnpm/package-manager-store/tmp/engine-locks'
        guessed.mkdir(parents=True)
        calls = []
        real_identity, real_gates = v.identity, v.run_gates
        def identity(root):
            return self.fingerprint(real_identity)
        def gates(root, selection=None):
            def execute(command, **kwargs):
                calls.append((command, kwargs.get('env', {}).get('CGO_ENABLED')))
                return types.SimpleNamespace(returncode=9 if failure else 0)
            # The gate queue resolves its directory with Git before this mock.
            with patch.object(v, 'queue_dir', return_value=v.queue_dir(root)), \
                    patch.object(v, 'normalized_environment', return_value={'PATH': os.environ['PATH']}), \
                    patch.object(v.subprocess, 'run', side_effect=execute):
                real_gates(root, selection)
            # Reproduce unrelated manager cache churn without modifying a byte
            # input. Unknown storage must not become an assumed input tree.
            transient = guessed / 'lock'
            transient.write_text('lock')
            transient.unlink()
        if push:
            (self.root / '.gitignore').write_text('ignored\nnode_modules\nbuild/\n')
            self.git('add', '.gitignore')
            self.git('commit', '-qm', 'declare generated fixture directories')
        head = self.git('rev-parse', 'HEAD')
        push_data = f'refs/heads/test {head} refs/heads/test {"0" * 40}' if push else None
        with self.unknown_store(b'undefined'), patch.object(v, 'identity', identity), patch.object(v, 'run_gates', gates):
            if failure:
                with self.assertRaisesRegex(v.GateError, 'frontend typecheck failed'):
                    v.verify(self.root, check=check, push_data=push_data)
                result = None
            else:
                result = v.verify(self.root, check=check, push_data=push_data)
        if check:
            self.assertFalse(result)
            self.assertEqual(calls, [])
        else:
            self.assertFalse(receipt.exists())
            self.assertEqual([c[0] for c in calls], [gate[1] for gate in v.GATES[:1 if failure else len(v.GATES)]])
            if not failure:
                self.assertTrue(result)
                go_build = next(gate[1] for gate in v.GATES if gate[0] == 'go build')
                self.assertEqual(next(cgo for command, cgo in calls if list(command) == go_build), '0')
        return calls
    def test_unknown_store_full_local_set_ignores_unrelated_cache_churn_and_deletes_old_receipt(self):
        self.exercise_unknown()
    def test_unknown_store_check_never_runs_gates_or_accepts_old_receipt(self):
        self.exercise_unknown(check=True)
    def test_unknown_store_push_keeps_full_local_set_and_head_guards(self):
        self.exercise_unknown(push=True)
        (self.root / 'source').write_text('dirty')
        head = self.git('rev-parse', 'HEAD')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, f'refs/heads/test {head} refs/heads/test {"0" * 40}')
    def test_unknown_store_gate_failure_propagates_and_cannot_keep_old_receipt(self):
        self.exercise_unknown(failure=True)

class PushShortcutTests(PushAndFilesystemTests):
    def test_assume_unchanged_and_skip_worktree_cannot_hide_dirty_bytes(self):
        for flag in ('--assume-unchanged', '--skip-worktree'):
            with self.subTest(flag=flag):
                self.git('update-index', flag, 'source')
                (self.root / 'source').write_text('hidden dirty')
                self.assertEqual(self.git('status', '--porcelain'), '')
                with self.assertRaises(v.GateError):
                    v.verify_push(self.root, self.push_line())
                (self.root / 'source').write_text('one')
                self.git('update-index', '--no-assume-unchanged', '--no-skip-worktree', 'source')
    def test_clean_conversion_semantics_accept_crlf_checkout(self):
        (self.root / '.gitattributes').write_text('source text eol=crlf\n')
        (self.root / 'source').write_bytes(b'first\nsecond\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'conversion')
        (self.root / 'source').write_bytes(b'first\r\nsecond\r\n')
        self.assertTrue(v.verify_push(self.root, self.push_line()))
    def test_changed_symlink_and_executable_mode_rejected(self):
        (self.root / 'link').symlink_to('source')
        self.git('add', 'link')
        self.git('commit', '-qm', 'link')
        self.assertTrue(v.verify_push(self.root, self.push_line()))
        (self.root / 'link').unlink()
        (self.root / 'link').symlink_to('.gitignore')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, self.push_line())

class ExternalFlagTests(FingerprintTests):
    def test_persistent_goflags_and_external_c_paths_require_full_checks(self):
        original = self.capture
        for key, flag in [('GOFLAGS', '-overlay=/tmp/overlay.json'),
                          ('GOFLAGS', '-toolexec=/tmp/wrapper'),
                          ('CGO_CFLAGS', '-I/tmp/headers'), ('CGO_LDFLAGS', '-L/tmp/libs')]:
            def configured(args, root, input_data=None, env_overrides=None, env_remove=()):
                import json
                raw = original(args, root, input_data, env_overrides, env_remove)
                if args[:2] == ['go', 'env']:
                    data = json.loads(raw)
                    data[key] = flag
                    return json.dumps(data).encode()
                return raw
            with self.subTest(key=key, flag=flag), patch.object(self, 'capture', configured):
                with self.assertRaises(v.Unbindable):
                    self.fingerprint()
        with patch.dict(v.os.environ, {'CPATH': '/tmp/headers'}):
            with self.assertRaises(v.Unbindable):
                self.fingerprint()
    def test_creation_and_removal_of_missing_configuration_detected(self):
        before, snapshot = self.fingerprint()
        config = self.external / '.npmrc'
        config.write_text('config')
        config.unlink()
        self.assertFalse(snapshot.unchanged())

class GitReplaceTests(PushAndFilesystemTests):
    def test_replace_object_cannot_substitute_pushed_tree(self):
        original = self.git('rev-parse', 'HEAD')
        (self.root / 'source').write_text('replacement')
        self.git('add', 'source')
        self.git('commit', '-qm', 'replacement')
        replacement = self.git('rev-parse', 'HEAD')
        self.git('replace', original, replacement)
        self.git('update-ref', 'HEAD', original)
        self.assertEqual(self.git('status', '--porcelain'), '')
        with self.assertRaises(v.GateError):
            v.verify_push(self.root, self.push_line(original))


@requires_macos
class NativeMonitorTests(VerificationTests):
    def test_real_native_monitor_catches_transient_added_deleted_source(self):
        monitor = v.InputMonitor(self.root)
        transient = self.root / 'temporary.go'
        transient.write_text('package test')
        transient.unlink()
        self.assertFalse(monitor.stop())
    def test_native_monitor_catches_modify_restore(self):
        monitor = v.InputMonitor(self.root)
        path = self.root / 'source'
        path.write_text('changed')
        path.write_text('one')
        self.assertFalse(monitor.stop())
    def test_native_monitor_accepts_proven_pnpm_probes(self):
        monitor = v.InputMonitor(self.root)
        probe = self.root / '_tmp_1234_abcdef'
        probe.write_text('')
        probe.unlink()
        self.assertTrue(monitor.stop())
    def test_monitor_early_exit_and_error_fail_closed(self):
        monitor = v.InputMonitor(self.root)
        monitor.process.kill()
        monitor.process.wait()
        self.assertFalse(monitor.stop())


@requires_macos
class RootEventTests(VerificationTests):
    def test_root_basename_transient_child_is_not_directory_metadata(self):
        monitor = v.InputMonitor(self.root.resolve())
        child = self.root / self.root.name
        child.write_text('real input')
        child.unlink()
        self.assertFalse(monitor.stop())
    def test_existing_root_basename_file_modify_restore_is_dirty(self):
        child = self.root / self.root.name
        child.write_text('real input')
        monitor = v.InputMonitor(self.root.resolve())
        child.write_text('modified')
        child.write_text('real input')
        self.assertFalse(monitor.stop())


class CgoEnumerationTests(FingerprintTests):
    def test_test_only_and_race_only_cgo_dependencies_disable_receipt(self):
        import json
        original = self.capture
        for scenario in ('test-only', 'race-only', 'normal-not-race'):
            seen = []
            def dependencies(args, root, input_data=None, env_overrides=None, env_remove=()):
                if args[:2] == ['go', 'list'] and '-deps' in args:
                    seen.append((list(args), env_overrides))
                    selected = ('-test' in args and
                                ((scenario == 'test-only') or
                                 (scenario == 'race-only' and '-race' in args and env_overrides == {'CGO_ENABLED': '1'}) or
                                 (scenario == 'normal-not-race' and '-race' not in args)))
                    if selected:
                        return json.dumps({'ImportPath': 'external.test/cgo', 'CgoFiles': ['code.go'], 'Standard': False}).encode()
                    return b'{}'
                return original(args, root, input_data, env_overrides, env_remove)
            with self.subTest(scenario=scenario), patch.dict(v.os.environ, {'CGO_ENABLED': '0'}), patch.object(self, 'capture', dependencies):
                with self.assertRaises(v.Unbindable):
                    self.fingerprint()
            self.assertEqual(len(seen), 2)
            self.assertTrue(all('-test' in command for command, _ in seen))
            self.assertIn('-race', seen[1][0])
            self.assertEqual(seen[1][1], {'CGO_ENABLED': '1'})
    def test_capture_fixed_cgo_environment_overrides_ambient(self):
        import sys
        with patch.dict(v.os.environ, {'CGO_ENABLED': '0'}):
            result = v.capture([sys.executable, '-c', 'import os; print(os.environ["CGO_ENABLED"])'],
                               self.root, env_overrides={'CGO_ENABLED': '1'})
        self.assertEqual(result.strip(), b'1')


@requires_macos
class MissingConfigurationMonitorTests(VerificationTests):
    def setUp(self):
        super().setUp()
        self.config_temp = tempfile.TemporaryDirectory()
        self.parent = Path(self.config_temp.name).resolve()
    def tearDown(self):
        self.config_temp.cleanup()
        super().tearDown()
    def observe(self, target):
        monitor = v.InputMonitor(self.root.resolve())
        snapshot = v.Snapshot()
        snapshot.monitor = monitor
        snapshot.add(target)
        # FSEvents drops events for changes made while its stream is still starting
        # (#135), which would make the detection tests below race the OS, not the monitor.
        v.time.sleep(0.3)
        return monitor, snapshot
    def test_unrelated_preferences_style_churn_does_not_change_input(self):
        target = self.parent / 'pnpm/config.yaml'
        monitor, snapshot = self.observe(target)
        unrelated = self.parent / 'desktop.plist'
        unrelated.write_text('unrelated')
        unrelated.unlink()
        self.assertTrue(snapshot.unchanged())
        self.assertTrue(monitor.stop())
    def test_missing_config_creation_and_removal_rejects(self):
        target = self.parent / '.npmrc'
        monitor, snapshot = self.observe(target)
        target.write_text('config')
        target.unlink()
        self.assertTrue(snapshot.unchanged())  # precise native guard owns parent mtime
        self.assertFalse(monitor.stop())
    def test_missing_intermediate_directory_creation_removal_rejects(self):
        target = self.parent / 'pnpm/config.yaml'
        monitor, snapshot = self.observe(target)
        target.parent.mkdir()
        target.write_text('config')
        target.unlink()
        target.parent.rmdir()
        self.assertFalse(monitor.stop())
    def test_parent_inode_replacement_rejected(self):
        target = self.parent / '.npmrc'
        monitor, snapshot = self.observe(target)
        self.parent.rmdir()
        self.parent.mkdir()
        self.assertFalse(snapshot.unchanged())
        self.assertFalse(monitor.stop())
    def test_read_only_system_parent_needs_no_write_marker(self):
        target = Path('/private/etc/artai-test-config-never-present-203')
        self.assertFalse(target.exists())
        monitor, snapshot = self.observe(target)
        self.assertTrue(snapshot.unchanged())
        self.assertTrue(monitor.stop())


class MissingConfigurationBoundaryTests(MissingConfigurationMonitorTests):
    def test_actual_directory_input_keeps_full_stat_guard(self):
        monitor = v.InputMonitor(self.root.resolve())
        snapshot = v.Snapshot()
        snapshot.monitor = monitor
        snapshot.add(self.parent)
        snapshot.add(self.parent / '.npmrc')
        actual_input = self.parent / 'source.go'
        actual_input.write_text('real source')
        actual_input.unlink()
        self.assertFalse(snapshot.unchanged())
        monitor.stop()
    def test_system_parent_symlink_path_supported(self):
        target = Path('/etc/artai-test-config-never-present-203')
        monitor, snapshot = self.observe(target)
        self.assertTrue(snapshot.unchanged())
        self.assertTrue(monitor.stop())
    def test_missing_config_through_external_symlink_is_precisely_watched(self):
        alias = self.root / 'config-alias'
        alias.symlink_to(self.parent, target_is_directory=True)
        monitor, snapshot = self.observe(alias / '.npmrc')
        actual_config = self.parent / '.npmrc'
        actual_config.write_text('config')
        actual_config.unlink()
        self.assertFalse(monitor.stop())


class MissingConfigurationOrderingTests(MissingConfigurationMonitorTests):
    def test_missing_first_then_actual_directory_keeps_full_guard(self):
        monitor, snapshot = self.observe(self.parent / '.npmrc')
        snapshot.add(self.parent)
        actual = self.parent / 'source.go'
        actual.write_text('real input')
        actual.unlink()
        self.assertFalse(snapshot.unchanged())
        monitor.stop()
    def test_multiple_missing_targets_share_parent_without_overwrite(self):
        monitor, snapshot = self.observe(self.parent / '.npmrc')
        second = self.parent / '.pnpmrc'
        snapshot.add(second)
        first = self.parent / '.npmrc'
        first.write_text('first target')
        first.unlink()
        self.assertFalse(monitor.stop())
    def test_symlink_ancestor_redirection_rejected(self):
        alias = self.root / 'config-alias'
        alias.symlink_to(self.parent, target_is_directory=True)
        monitor, snapshot = self.observe(alias / '.npmrc')
        alias.unlink()
        alias.symlink_to(self.root, target_is_directory=True)
        self.assertFalse(snapshot.unchanged())
        monitor.stop()


class HookPathNormalizationTests(VerificationTests):
    def test_custom_git_exec_path_rejected(self):
        with patch.dict(v.os.environ, {'GIT_EXEC_PATH': str(self.root / 'custom-helper')}):
            with self.assertRaises(v.Unbindable):
                v.normalized_environment(self.root)
    def test_default_exec_prefix_normalizes_but_real_tool_shadow_does_not(self):
        import os
        import shutil
        real_git = shutil.which('git')
        prefix = self.root / 'Cellar/git/fixture'
        binary = prefix / 'bin/git'
        binary.parent.mkdir(parents=True)
        helpers = prefix / 'libexec/git-core'
        helpers.mkdir(parents=True)
        binary.write_text('#!/bin/sh\nif [ "$1" = --exec-path ]; then echo "' + str(helpers) + '"; exit 0; fi\nexec "' + real_git + '" "$@"\n')
        binary.chmod(0o755)
        (helpers / 'git').symlink_to(binary)
        baseline = str(binary.parent) + os.pathsep + os.environ['PATH']
        with patch.dict(v.os.environ, {'PATH': str(helpers) + os.pathsep + baseline, 'GIT_EXEC_PATH': str(helpers)}):
            self.assertEqual(v.normalized_environment(self.root)['PATH'], baseline)
            for tool in ('pnpm', 'go', 'node', 'cc', 'xcrun'):
                with self.subTest(tool=tool):
                    shadow = helpers / tool
                    shadow.write_text('#!/bin/sh\nexit 0\n')
                    shadow.chmod(0o755)
                    with self.assertRaises(v.Unbindable):
                        v.normalized_environment(self.root)
                    shadow.unlink()
    def test_actual_gate_process_uses_same_canonical_path(self):
        canonical = '/safe/canonical/path'
        queue = v.queue_dir(self.root)
        with patch.object(v, 'queue_dir', return_value=queue), \
                patch.object(v, 'normalized_environment', return_value={'PATH': canonical}), patch.object(v.subprocess, 'run', return_value=type('Result', (), {'returncode': 0})()) as run:
            v.run_gates(self.root)
        self.assertEqual(run.call_count, len(v.GATES))
        self.assertTrue(all(call.kwargs['env']['PATH'] == canonical for call in run.call_args_list))

@requires_macos
class RealHookReuseTests(VerificationTests):
    def test_manual_same_content_commit_real_push_reuses_once(self):
        import os
        import sys
        base = Path(self.temp.name).parent / (self.root.name + '-hook-proof')
        base.mkdir()
        try:
            bare = base / 'remote.git'
            subprocess.run(['git', 'init', '--bare', '-q', str(bare)], check=True)
            hooks = base / 'hooks'
            hooks.mkdir()
            self.git('config', 'core.hooksPath', str(hooks))
            self.git('remote', 'add', 'origin', str(bare))
            counter = base / 'gates.log'
            wrapper = base / 'wrapper.py'
            wrapper.write_text('''import importlib.util, os, sys
from pathlib import Path
spec = importlib.util.spec_from_file_location('v', ''' + repr(str(Path(v.__file__).resolve())) + ''')
v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v)
def lightweight_identity(root):
    env = v.normalized_environment(root)
    snapshot = v.Snapshot(); v.source_snapshot(root, snapshot)
    # Keep the real Git lookup alias/target bytes in this integration proof.
    snapshot.add(v.shutil.which('git', path=env['PATH']))
    return {'content': snapshot.digest(), 'environment': v.digest_json(env),
            'gates': v.digest_json([*v.GATES, v.RACE_GATE]), 'runner': v.file_digest(Path(v.__file__)),
            'worktree': v.digest_json(str(root.resolve()))}, snapshot
def cheap_gates(root, gates=None):
    with Path(''' + repr(str(counter)) + ''').open('a') as stream:
        for name, _, _ in gates: stream.write(name + '\\n')
v.identity = lightweight_identity; v.run_gates = cheap_gates
try: sys.exit(v.main())
except v.GateError as error: print(error, file=sys.stderr); sys.exit(1)
''')
            hook = hooks / 'pre-push'
            hook.write_text('#!/usr/bin/env bash\nset -euo pipefail\npython3 -B "' + str(wrapper) + '" --pre-push\n')
            hook.chmod(0o755)
            manual = subprocess.run([sys.executable, '-B', str(wrapper), '--force'], cwd=self.root, text=True, capture_output=True)
            self.assertEqual(manual.returncode, 0, manual.stdout + manual.stderr)
            self.git('commit', '--allow-empty', '-qm', 'same content new tip')
            pushed = subprocess.run(['git', 'push', 'origin', self.git('symbolic-ref', 'HEAD') + ':refs/heads/test'], cwd=self.root, text=True, capture_output=True)
            self.assertEqual(pushed.returncode, 0, pushed.stdout + pushed.stderr)
            self.assertIn('[verify] reuse:', pushed.stdout + pushed.stderr)
            self.assertEqual(counter.read_text().splitlines(), v.gate_names(v.GATES))
            self.assertEqual(self.git('ls-remote', 'origin', 'refs/heads/test').split()[0], self.git('rev-parse', 'HEAD'))
        finally:
            import shutil
            shutil.rmtree(base)


class MonitorDiagnosticTests(unittest.TestCase):
    def test_fixed_reason_codes_preserve_clean_and_failure_classification(self):
        self.assertIsNone(v.InputMonitor.reason_for_output('clean\n', 0))
        self.assertEqual(v.InputMonitor.reason_for_output('dirty:source-change\n', 0), 'source-change')
        self.assertEqual(v.InputMonitor.reason_for_output('dirty:marker-missing,watch-error\n', 0), 'marker-missing,watch-error')
        self.assertEqual(v.InputMonitor.reason_for_output('clean\n', 1), 'process-exit')
    def test_unknown_output_is_not_echoed_or_accepted(self):
        for output in ('', '/secret/path', 'TOKEN=secret', 'dirty:/private/path', 'dirty:source-change\nTOKEN=secret'):
            with self.subTest(output=output):
                self.assertEqual(v.InputMonitor.reason_for_output(output, 0), 'protocol-output')
    def test_native_dirty_reason_is_fixed_without_filename(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            monitor = v.InputMonitor(root)
            (root / 'private-name.go').write_text('source')
            self.assertFalse(monitor.stop())
            self.assertEqual(monitor.reason, 'source-change')
            self.assertNotIn(str(root), monitor.reason)
            self.assertNotIn('private-name', monitor.reason)


class StdoutTransportTests(unittest.TestCase):
    def monitor_with_transport(self, root, body):
        # A pipe is allowed to buffer writes. Delay only the final verdict, after
        # the real native watcher and end-marker have performed their checks.
        script = v.InputMonitor.SCRIPT.replace('function finish(failed) {',
                    'function finish(failed) {\n' + body)
        with patch.object(v.InputMonitor, 'SCRIPT', script):
            return v.InputMonitor(root)
    def test_clean_verdict_flushes_before_exit(self):
        body = '''const originalWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (chunk, ...args) => {
  setTimeout(() => originalWrite(chunk, ...args), 25); return true;
};'''
        with tempfile.TemporaryDirectory() as directory:
            monitor = self.monitor_with_transport(Path(directory).resolve(), body)
            self.assertTrue(monitor.stop())
            self.assertIsNone(monitor.reason)
    def test_real_dirty_verdict_survives_deferred_transport(self):
        body = '''const originalWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (chunk, ...args) => {
  setTimeout(() => originalWrite(chunk, ...args), 25); return true;
};'''
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            monitor = self.monitor_with_transport(root, body)
            (root / 'changed.go').write_text('real changed input')
            self.assertFalse(monitor.stop())
            self.assertEqual(monitor.reason, 'source-change')
    def test_transport_error_rejects_even_if_clean_text_reached_pipe(self):
        body = '''const originalWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (chunk, callback) => {
  originalWrite(chunk, () => { if (callback) callback(new Error('transport failed')); });
  return true;
};'''
        with tempfile.TemporaryDirectory() as directory:
            monitor = self.monitor_with_transport(Path(directory).resolve(), body)
            self.assertFalse(monitor.stop())
            self.assertEqual(monitor.reason, 'process-exit')
    def test_stalled_transport_is_bounded_and_rejected(self):
        body = '''process.stdout.write = () => {
  setInterval(() => {}, 1000); return true;
};'''
        with tempfile.TemporaryDirectory() as directory:
            monitor = self.monitor_with_transport(Path(directory).resolve(), body)
            self.assertFalse(monitor.stop())
            self.assertIn(monitor.reason, ('monitor-timeout', 'protocol-output'))
    def test_exit_before_final_verdict_is_rejected(self):
        body = "process.stdout.write = () => { process.exit(9); return false; };"
        with tempfile.TemporaryDirectory() as directory:
            monitor = self.monitor_with_transport(Path(directory).resolve(), body)
            self.assertFalse(monitor.stop())
            self.assertEqual(monitor.reason, 'process-exit')


class GateQueueTests(VerificationTests):
    def setUp(self):
        super().setUp()
        v.receipt_dir(self.root).mkdir()
        self.calls = 0
    fake_identity = ReceiptTests.fake_identity
    receipt = ReceiptTests.receipt
    def hold(self, index=1):
        import fcntl
        directory = v.queue_dir(self.root)
        directory.mkdir(parents=True, exist_ok=True)
        stream = (directory / f'slot-{index}.lock').open('a')
        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.addCleanup(stream.close)
        return stream
    def acquire_in_thread(self, environ):
        import threading
        entered = threading.Event()
        def target():
            with patch.dict(v.os.environ, environ), v.gate_slot(self.root):
                entered.set()
        thread = threading.Thread(target=target, daemon=True)
        thread.start()
        return thread, entered
    def test_slot_count_parsing(self):
        for raw, expected in [(None, 1), ('', 1), ('3', 3), ('0', 0)]:
            environ = {} if raw is None else {v.SLOTS_ENV: raw}
            with patch.dict(v.os.environ, environ, clear=False):
                if raw is None:
                    v.os.environ.pop(v.SLOTS_ENV, None)
                self.assertEqual(v.gate_slots(), expected)
        for raw in ('-1', 'two', '1.5'):
            with patch.dict(v.os.environ, {v.SLOTS_ENV: raw}), self.assertRaises(v.GateError):
                v.gate_slots()
    def test_queue_is_shared_by_worktrees_of_one_clone(self):
        other = self.root.parent / (self.root.name + '-queue-worktree')
        try:
            self.git('worktree', 'add', '--detach', str(other), 'HEAD')
            self.assertEqual(v.queue_dir(self.root), v.queue_dir(other))
            self.assertNotEqual(v.receipt_dir(self.root), v.receipt_dir(other))
        finally:
            self.git('worktree', 'remove', '--force', str(other))
    def test_free_slot_used_without_waiting(self):
        self.hold(1)
        thread, entered = self.acquire_in_thread({v.SLOTS_ENV: '2'})
        self.assertTrue(entered.wait(10))
        thread.join(10)
    def test_busy_slot_waits_until_released(self):
        holder = self.hold(1)
        with patch.object(v, 'QUEUE_POLL_SECONDS', 0.05):
            thread, entered = self.acquire_in_thread({v.SLOTS_ENV: '1'})
            self.assertFalse(entered.wait(0.5))
            holder.close()
            self.assertTrue(entered.wait(10))
            thread.join(10)
    def test_zero_slots_disables_queue(self):
        self.hold(1)
        thread, entered = self.acquire_in_thread({v.SLOTS_ENV: '0'})
        self.assertTrue(entered.wait(10))
        thread.join(10)
    def test_slot_released_after_gate_failure(self):
        import fcntl
        with patch.object(v, 'run_gates_unqueued', side_effect=v.GateError('failed')):
            with self.assertRaises(v.GateError):
                v.run_gates(self.root)
        with (v.queue_dir(self.root) / 'slot-1.lock').open('a') as stream:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
    def test_receipt_reuse_never_waits_for_queue(self):
        def queued_gates(root, gates=None):
            with v.gate_slot(root):
                self.calls += 1
        def verify():
            with patch.object(v, 'identity', self.fake_identity), patch.object(v, 'run_gates', queued_gates):
                return v.verify(self.root)
        self.assertTrue(verify())
        self.hold(1)
        import threading
        done = threading.Event()
        thread = threading.Thread(target=lambda: (verify(), done.set()), daemon=True)
        thread.start()
        self.assertTrue(done.wait(30), 'receipt reuse waited for a gate slot')
        self.assertEqual(self.calls, 1)
    def test_change_while_queued_blocks_receipt(self):
        holder = self.hold(1)
        def edit_and_release():
            (self.root / 'source').write_text('edited while queued')
            holder.close()
        def queued_gates(root, gates=None):
            import threading
            threading.Timer(0.3, edit_and_release).start()
            with v.gate_slot(root):
                self.calls += 1
        with patch.object(v, 'QUEUE_POLL_SECONDS', 0.05), patch.object(v, 'identity', self.fake_identity), \
                patch.object(v, 'run_gates', queued_gates):
            with self.assertRaises(v.GateError):
                v.verify(self.root)
        self.assertEqual(self.calls, 1)
        self.assertFalse(self.receipt.exists())

if __name__ == '__main__':
    unittest.main()
