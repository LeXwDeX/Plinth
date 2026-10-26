import contextlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("hook", Path(__file__).parents[1] / "install-local-verification-hook.py")
hook = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(hook)


class HookTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.path = self.root / "pre-push"
        self.prefix = '#!/usr/bin/env bash\n# Unrelated existing setup\n'
        self.suffix = '\nprintf "tail" >> "$EVENTS"\ncat > "$TAIL_INPUT"\n'
        self.original = self.prefix + hook.LEGACY + self.suffix
        self.path.write_text(self.original)
        self.path.chmod(0o751)

    def apply(self, **kwargs):
        with contextlib.redirect_stdout(io.StringIO()) as out:
            result = hook.install(self.path, **kwargs)
        return result, out.getvalue()

    def environment(self):
        bin_dir = self.root / "bin"
        bin_dir.mkdir(exist_ok=True)
        for tool in ["pnpm", "go"]:
            path = bin_dir / tool
            path.write_text('''#!/usr/bin/env bash
name="$(basename "$0") $*"
printf '%s\n' "$name" >> "$EVENTS"
if [ "$name" = "${FAIL_GATE:-}" ]; then exit 7; fi
''')
            path.chmod(0o755)
        return {**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
                "EVENTS": str(self.root / "events"), "TAIL_INPUT": str(self.root / "tail-input"),
                "TMPDIR": str(self.root)}

    def run_hook(self, env, data="refs/heads/test abc refs/heads/test def\n"):
        return subprocess.run([str(self.path)], cwd=self.root, env=env,
                              input=data, text=True, capture_output=True)

    def test_preview_idempotence_and_restore_preserve_other_bytes_and_mode(self):
        changed, preview = self.apply()
        self.assertTrue(changed)
        self.assertIn("-pnpm exec tsc --noEmit", preview)
        self.assertIn("+    python3 scripts/verify-local.py --pre-push", preview)
        self.assertEqual(self.path.read_text(), self.original)
        self.apply(apply=True)
        installed = self.path.read_text()
        self.assertTrue(installed.startswith(self.prefix))
        self.assertTrue(installed.endswith(self.suffix))
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o751)
        self.assertFalse(self.apply(apply=True)[0])
        self.apply(apply=True, uninstall=True)
        self.assertEqual(self.path.read_text(), self.original)
        self.assertFalse(self.apply(apply=True, uninstall=True)[0])

    def test_old_worktree_executes_all_seven_and_replays_input_to_tail(self):
        self.apply(apply=True)
        result = self.run_hook(self.environment())
        self.assertEqual(result.returncode, 0, result.stderr)
        events = (self.root / "events").read_text().splitlines()
        self.assertEqual(events, ["pnpm exec tsc --noEmit", "pnpm test", "pnpm build", "go vet ./...",
                                  "go test -count=1 ./...", "go test -race -count=1 ./...",
                                  "go build -trimpath -o /dev/null ./cmd/art-ai", "tail"])
        self.assertEqual((self.root / "tail-input").read_text(), "refs/heads/test abc refs/heads/test def\n")
        self.assertEqual(list(self.root.glob("artai-pre-push.*")), [])

    def test_every_failed_legacy_gate_stops_push_and_tail(self):
        self.apply(apply=True)
        env = self.environment()
        gates = ["pnpm exec tsc --noEmit", "pnpm test", "pnpm build", "go vet ./...",
                 "go test -count=1 ./...", "go test -race -count=1 ./...",
                 "go build -trimpath -o /dev/null ./cmd/art-ai"]
        for index, gate in enumerate(gates):
            with self.subTest(gate=gate):
                (self.root / "events").unlink(missing_ok=True)
                result = self.run_hook({**env, "FAIL_GATE": gate})
                self.assertEqual(result.returncode, 7, result.stderr)
                self.assertEqual((self.root / "events").read_text().splitlines(), gates[:index + 1])
                self.assertFalse((self.root / "tail-input").exists())
                self.assertEqual(list(self.root.glob("artai-pre-push.*")), [])

    def test_runner_receives_refs_without_consuming_unrelated_stdin(self):
        (self.root / "scripts").mkdir()
        (self.root / "scripts/verify-local.py").write_text('''import os, pathlib, sys
HOOK_PROTOCOL = 1
assert sys.argv[1:] == ["--pre-push"]
pathlib.Path("runner-input").write_bytes(sys.stdin.buffer.read())
sys.exit(int(os.environ.get("RUNNER_EXIT", "0")))
''')
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        subprocess.run(["git", "-C", str(self.root), "add", "scripts/verify-local.py"], check=True)
        self.apply(apply=True)
        env = self.environment()
        data = "refs/heads/a a refs/heads/a b\nrefs/heads/c c refs/heads/c d\n"
        self.assertEqual(self.run_hook(env, data).returncode, 0)
        self.assertEqual((self.root / "runner-input").read_text(), data)
        self.assertEqual((self.root / "tail-input").read_text(), data)
        (self.root / "events").unlink()
        self.assertEqual(self.run_hook({**env, "RUNNER_EXIT": "9"}, data).returncode, 9)
        self.assertFalse((self.root / "events").exists())

    def test_untracked_or_unsupported_draft_keeps_legacy_checks(self):
        (self.root / "scripts").mkdir()
        script = self.root / "scripts/verify-local.py"
        script.write_text('HOOK_PROTOCOL = 1\nraise RuntimeError("must not run an untracked draft")\n')
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.apply(apply=True)
        env = self.environment()
        result = self.run_hook(env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len((self.root / "events").read_text().splitlines()), 8)
        script.write_text('raise RuntimeError("must not run an unsupported draft")\n')
        subprocess.run(["git", "-C", str(self.root), "add", "scripts/verify-local.py"], check=True)
        (self.root / "events").unlink()
        result = self.run_hook(env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len((self.root / "events").read_text().splitlines()), 8)

    def test_cancellation_exits_nonzero_without_more_gates_or_tail(self):
        self.apply(apply=True)
        env = self.environment()
        first = self.root / "bin/pnpm"
        first.write_text('''#!/usr/bin/env bash
printf 'first\n' >> "$EVENTS"
kill -"$CANCEL_SIGNAL" "$PPID"
exit 0
''')
        for sig, code in [("HUP", 129), ("INT", 130), ("TERM", 143)]:
            with self.subTest(signal=sig):
                (self.root / "events").unlink(missing_ok=True)
                result = self.run_hook({**env, "CANCEL_SIGNAL": sig})
                self.assertEqual(result.returncode, code, result.stderr)
                self.assertEqual((self.root / "events").read_text(), "first\n")
                self.assertFalse((self.root / "tail-input").exists())
                self.assertEqual(list(self.root.glob("artai-pre-push.*")), [])

    def test_existing_guard_rejection_still_stops_every_gate(self):
        # Unknown/SpecGit-owned prefix is retained byte for byte and executed first.
        self.path.write_text(self.prefix + 'exit 23\n' + hook.LEGACY + self.suffix)
        self.apply(apply=True)
        result = self.run_hook(self.environment())
        self.assertEqual(result.returncode, 23)
        self.assertFalse((self.root / "events").exists())

    def test_active_open_hook_keeps_original_inode(self):
        with self.path.open() as running:
            self.apply(apply=True)
            self.assertEqual(running.read(), self.original)

    def test_unrelated_mixed_newlines_are_byte_exact_through_install_and_restore(self):
        prefix = b'#!/usr/bin/env bash\n# unrelated CRLF\r\n'
        suffix = b'# unrelated trailing CRLF\r\n'
        original = prefix + hook.LEGACY.encode() + suffix
        self.path.write_bytes(original)
        self.apply(apply=True)
        self.assertEqual(self.path.read_bytes(), prefix + hook.BLOCK.encode() + suffix)
        self.apply(apply=True, uninstall=True)
        self.assertEqual(self.path.read_bytes(), original)

    def test_conflicts_are_not_replaced(self):
        bad = ["#!/bin/sh\n" + hook.LEGACY, self.original + hook.LEGACY,
               self.prefix + hook.START, self.prefix + hook.END,
               self.prefix + hook.BLOCK + hook.BLOCK,
               self.prefix + hook.BLOCK.replace("--pre-push", "--unknown"),
               self.prefix + hook.BLOCK + hook.LEGACY]
        for original in bad:
            with self.subTest(original=original[:80]):
                self.path.write_text(original)
                with self.assertRaises(ValueError):
                    self.apply(apply=True)
                self.assertEqual(self.path.read_text(), original)

    def test_custom_hooks_path_and_symlink_refusal(self):
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        subprocess.run(["git", "-C", str(self.root), "config", "core.hooksPath", "custom-hooks"], check=True)
        self.assertEqual(hook.effective_hook(self.root), self.root / "custom-hooks/pre-push")
        target = self.root / "target"
        target.write_text(self.original)
        self.path.unlink()
        self.path.symlink_to(target)
        with self.assertRaises(ValueError):
            self.apply(apply=True)
        self.assertEqual(target.read_text(), self.original)


if __name__ == "__main__":
    unittest.main()
