import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "required", Path(__file__).parents[1] / "install-specgit-required-hooks.py")
required = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(required)

# SpecGit is the maintainers' internal tool (AGENTS.md); CI, Dockerfile.ci and outside contributors do not
# have it, so the tests that drive the real CLI are skipped there and stay in the maintainer gate.
requires_specgit = unittest.skipIf(shutil.which("specgit") is None, "needs the real specgit CLI (maintainer environments only)")
REAL_CLI_TESTS = (
    "test_real_git_hooks_path_and_cli_guard_are_verified",
    "test_real_cli_setup_wires_all_supported_hosts_in_a_disposable_worktree",
    "test_global_absolute_foreign_hooks_path_cannot_block_or_init_other_repo",
)

PRE_COMMIT = '''#!/bin/sh
# >>> specgit-v2:pre-commit:start >>>
specgit guard --stage pre-commit || exit $?
echo guard-pre-commit >> "$EVENTS"
# <<< specgit-v2:pre-commit:end <<<
'''
PRE_PUSH = '''#!/usr/bin/env bash
# >>> specgit-v2:pre-push:start >>>
specgit guard --stage pre-push || exit $?
cat > /dev/null
echo guard-pre-push >> "$EVENTS"
# <<< specgit-v2:pre-push:end <<<
'''
BLOCK = '''<!-- specgit:project:v2:start -->
## SpecGit 2

Read the Skill ([installation](https://example.invalid/agent-install.md)).
<!-- specgit:project:v2:end -->
'''
# A stand-in CLI: logs its arguments, whether Git hook variables leaked into it and
# whether a GitHub token reached it (never the value);
# `init` writes the declaration into --cwd, as the real CLI does.
FAKE_SPECGIT = '''#!/bin/sh
cwd=.
if [ "$1" = --cwd ]; then cwd=$2; shift 2; fi
command=$1; shift
if [ "$command" != guard ]; then echo "specgit $command $* GIT_DIR=${GIT_DIR:-unset} GH_TOKEN=${GH_TOKEN:+set}" >> "$EVENTS"; fi
agent=generic
if [ "$command" = setup ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --agent ]; then agent=$2; shift 2; else shift; fi
  done
fi
if [ "${FAIL_SPECGIT:-}" = "$command" ]; then
  echo '{"ok":false,"diagnostics":[{"code":"ownership_conflict"}]}'; exit 3
fi
if [ "$command" = init ]; then
  echo "version: 2" > "$cwd/.specgit.yaml"
  echo '{"ok":true}'
fi
if [ "$command" = setup ]; then
  if [ "$agent" = generic ]; then
    printf '{"ok":true,"status":"installed","evidence":{"hook_purposes":{"generic":{"installed":["guard"],"declined":[{"purpose":"context","status":"unsupported"}]}}}}\n'
  elif [ "${FAKE_SETUP_MISSING_CONTEXT:-}" = 1 ]; then
    printf '{"ok":true,"status":"installed","evidence":{"hook_purposes":{"%s":{"installed":["guard"],"declined":[{"purpose":"context","status":"unsupported"}]}}}}\n' "$agent"
  else
    printf '{"ok":true,"status":"installed","evidence":{"hook_purposes":{"%s":{"installed":["guard","context"],"declined":[]}}}}\n' "$agent"
  fi
fi
if [ "$command" = doctor ]; then printf '{"ok":true,"status":"%s","evidence":{"health":{"status":"%s","host_behavior":"not_verified"}}}\n' "${FAKE_DOCTOR_STATUS:-offline_checked}" "${FAKE_DOCTOR_HEALTH:-healthy}"; fi
'''


class RequiredHookTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.repo = self.base / "repo"
        self.events = self.base / "events"
        bin_dir = self.base / "bin"
        bin_dir.mkdir()
        (bin_dir / "specgit").write_text(FAKE_SPECGIT)
        (bin_dir / "specgit").chmod(0o755)
        self.env = {**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
                    "EVENTS": str(self.events), "HOME": str(self.base),
                    "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
                    "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid",
                    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid"}
        for key in ("FAIL_SPECGIT", "GH_TOKEN", *required.GIT_HOOK_ENV):
            self.env.pop(key, None)
        self.git("init", "-q", "-b", "main", str(self.repo), cwd=self.base)
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "specgit-project.yaml").write_text("version: 2\n")
        (self.repo / "scripts").mkdir()
        shutil.copy(Path(__file__).parents[1] / "specgit-new-worktree-setup.py",
                    self.repo / "scripts" / "specgit-new-worktree-setup.py")
        (self.repo / "AGENTS.md").write_text("# Rules\n\n" + BLOCK)
        (self.repo / ".gitignore").write_text(".specgit.yaml\n")
        self.git("add", ".")
        self.git("commit", "-q", "--no-verify", "-m", "init")
        self.hooks = self.repo / ".git" / "hooks"
        for name, text, mode in (("pre-commit", PRE_COMMIT, 0o751), ("pre-push", PRE_PUSH, 0o755)):
            (self.hooks / name).write_text(text)
            (self.hooks / name).chmod(mode)

    def git(self, *args, cwd=None, check=True):
        return subprocess.run(["git", *args], cwd=cwd or self.repo, env=self.env,
                              text=True, capture_output=True, check=check)

    def run_quietly(self, function, *args, **kwargs):
        with contextlib.redirect_stdout(io.StringIO()) as out, mock.patch.dict(os.environ, self.env, clear=True):
            result = function(*args, **kwargs)
        return result, out.getvalue()

    def apply(self, **kwargs):
        return self.run_quietly(required.install, self.hooks, **kwargs)

    def events_log(self):
        return self.events.read_text().splitlines() if self.events.exists() else []

    def use_global_hooks_path(self, path):
        config = self.base / "isolated-global.gitconfig"
        subprocess.run(["git", "config", "--file", str(config), "core.hooksPath", str(path)],
                       check=True, text=True, capture_output=True)
        self.env["GIT_CONFIG_GLOBAL"] = str(config)

    def commit(self, cwd, name="change.txt"):
        (cwd / name).write_text(name + "\n")
        self.git("add", name, cwd=cwd)
        return self.git("commit", "-q", "-m", "change", cwd=cwd, check=False)

    def fix_line(self, root):
        return (f"[specgit-required] fix: specgit init --cwd '{root}' "
                f"--config-file '{root}/docs/specgit-project.yaml' --manual-observe")

    def test_preview_idempotence_and_uninstall_preserve_bytes_and_mode(self):
        changed, preview = self.apply(mode="enforce")
        self.assertTrue(changed)
        self.assertIn("+# >>> artai-specgit-required:v1:start >>>", preview)
        self.assertIn("dry-run only", preview)
        self.assertEqual((self.hooks / "pre-commit").read_text(), PRE_COMMIT)
        self.assertFalse((self.hooks / "post-checkout").exists())
        self.apply(mode="enforce", apply=True)
        installed = (self.hooks / "pre-commit").read_text()
        self.assertTrue(installed.startswith("#!/bin/sh\n" + required.REQUIRE["enforce"]))
        self.assertTrue(installed.endswith(PRE_COMMIT.split("\n", 1)[1]))
        self.assertTrue((self.hooks / "pre-push").read_text().startswith(
            "#!/usr/bin/env bash\n" + required.REQUIRE["enforce"]))
        self.assertEqual((self.hooks / "pre-commit").stat().st_mode & 0o777, 0o751)
        self.assertEqual((self.hooks / "post-checkout").stat().st_mode & 0o777, 0o755)
        self.assertFalse(self.apply(mode="enforce", apply=True)[0])
        self.apply(uninstall=True, apply=True)
        self.assertEqual((self.hooks / "pre-commit").read_text(), PRE_COMMIT)
        self.assertEqual((self.hooks / "pre-push").read_text(), PRE_PUSH)
        self.assertEqual((self.hooks / "pre-commit").stat().st_mode & 0o777, 0o751)
        self.assertEqual((self.hooks / "post-checkout").read_text(), required.POST_CHECKOUT_SHEBANG)
        self.assertFalse(self.apply(uninstall=True, apply=True)[0])
        with self.assertRaises(ValueError):
            self.apply(apply=True)  # Installing needs an explicit mode.

    def test_enforce_refuses_uninitialized_commit_and_push_before_guard(self):
        self.apply(mode="enforce", apply=True)
        result = self.commit(self.repo)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SpecGit not initialized); refused.", result.stderr)
        self.assertIn(self.fix_line(self.repo), result.stderr.splitlines())
        self.assertEqual(self.events_log(), [])
        (self.repo / ".specgit.yaml").write_text("version: 2\n")
        result = self.git("commit", "-q", "-m", "change", check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.events_log(), ["guard-pre-commit"])
        remote = self.base / "remote.git"
        self.git("init", "-q", "--bare", str(remote), cwd=self.base)
        self.git("remote", "add", "origin", str(remote))
        self.assertEqual(self.git("push", "-q", "origin", "main", check=False).returncode, 0)
        self.assertEqual(self.events_log(), ["guard-pre-commit", "guard-pre-push"])
        (self.repo / ".specgit.yaml").unlink()
        result = self.git("push", "-q", "origin", "HEAD:other", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(self.fix_line(self.repo), result.stderr.splitlines())
        self.assertEqual(len(self.events_log()), 2)

    def test_warn_mode_reports_but_allows_and_switches_to_enforce(self):
        self.apply(mode="warn", apply=True)
        result = self.commit(self.repo)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("allowed for now (warn mode)", result.stderr)
        self.assertIn(self.fix_line(self.repo), result.stderr.splitlines())
        self.assertEqual(self.events_log(), ["guard-pre-commit"])
        changed, preview = self.apply(mode="enforce")
        self.assertTrue(changed)
        self.assertIn("+  exit 1", preview)
        self.assertNotIn("post-checkout", preview)  # Mode switches never touch post-checkout.
        self.apply(mode="enforce", apply=True)
        self.assertNotEqual(self.commit(self.repo, "second.txt").returncode, 0)
        self.apply(mode="warn", apply=True)
        self.assertTrue((self.hooks / "pre-commit").read_text().startswith(
            "#!/bin/sh\n" + required.REQUIRE["warn"]))
        self.assertEqual(self.git("commit", "-q", "-m", "second", check=False).returncode, 0)

    def test_new_worktree_is_initialized_without_hook_environment(self):
        self.apply(mode="enforce", apply=True)
        worktree = self.base / "wt"
        result = self.git("worktree", "add", "-q", "-b", "feature", str(worktree))
        self.assertIn("initialized", result.stderr)
        self.assertTrue((worktree / ".specgit.yaml").exists())
        calls = self.events_log()
        self.assertEqual(len(calls), 10)
        self.assertTrue(calls[0].startswith(f"specgit init --config-file {worktree}/docs/specgit-project.yaml"))
        for agent in ("generic", "claude", "codex", "opencode"):
            setups = [line for line in calls if f"--agent {agent} " in line]
            self.assertEqual(len(setups), 2)  # explicit dry-run followed by apply
            for setup in setups:
                self.assertIn("--hook-purpose guard --hook-purpose context", setup)
                self.assertIn("GIT_DIR=unset", setup)
        opencode = [line for line in calls if "--agent opencode " in line]
        self.assertTrue(all("--opencode-claude-hooks" in line for line in opencode))
        self.assertTrue(calls[-1].startswith("specgit doctor --provider github --remote origin --offline --json"))
        self.assertEqual(self.commit(worktree).returncode, 0)
        # Ordinary branch switches are not new worktrees and never re-run initialization.
        self.events.unlink()
        self.git("checkout", "-q", "-b", "other", cwd=worktree)
        (worktree / ".specgit.yaml").unlink()
        self.git("checkout", "-q", "feature", cwd=worktree)
        self.assertEqual(self.events_log(), [])

    def test_new_worktree_does_not_read_github_token_from_project_env(self):
        # Authentication comes from the existing SpecGit/gh session. A project .env is
        # not an auth source: the helper must not read or inject its GH_TOKEN.
        (self.repo / ".env").write_text("OTHER=1\nexport GH_TOKEN='secret-token-value'\n")
        self.apply(mode="enforce", apply=True)
        worktree = self.base / "wt-token"
        result = self.git("worktree", "add", "-q", "-b", "token", str(worktree))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((worktree / ".specgit.yaml").exists())
        calls = self.events_log()
        self.assertTrue(calls, calls)
        self.assertEqual(len(calls), 10)
        self.assertTrue(all(line.endswith("GH_TOKEN=") for line in calls), calls)
        self.assertTrue(all("GIT_DIR=unset" in line for line in calls), calls)
        self.assertNotIn("secret-token-value", result.stdout + result.stderr)
        self.assertNotIn("secret-token-value", self.events.read_text())

        # A credential explicitly present in the process environment still passes through.
        self.events.unlink()
        self.env["GH_TOKEN"] = "from-env"
        explicit = self.base / "wt-explicit"
        self.git("worktree", "add", "-q", "-b", "explicit", str(explicit))
        calls = self.events_log()
        self.assertEqual(len(calls), 10)
        self.assertTrue(all(line.endswith("GH_TOKEN=set") for line in calls))

    def test_failed_initialization_keeps_worktree_and_prints_fix(self):
        self.apply(mode="enforce", apply=True)
        self.env["FAIL_SPECGIT"] = "init"
        worktree = self.base / "wt"
        result = self.git("worktree", "add", "-q", "-b", "feature", str(worktree))
        self.assertIn("automatic initialization/setup failed", result.stderr)
        self.assertIn("ownership_conflict", result.stderr)
        self.assertIn(f"[specgit-required] fix: python3 '{worktree}/scripts/specgit-new-worktree-setup.py' --cwd '{worktree}'",
                      result.stderr.splitlines())
        self.assertTrue((worktree / "AGENTS.md").exists())
        self.assertFalse((worktree / ".specgit.yaml").exists())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotEqual(self.commit(worktree).returncode, 0)

    def test_failed_host_setup_is_reported_without_failing_worktree_checkout(self):
        self.apply(mode="enforce", apply=True)
        self.env["FAIL_SPECGIT"] = "setup"
        worktree = self.base / "setup-failure"
        result = self.git("worktree", "add", "-q", "-b", "setup-failure", str(worktree))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((worktree / ".specgit.yaml").exists())
        self.assertIn("automatic initialization/setup failed", result.stderr)
        self.assertIn("specgit setup", result.stderr)
        self.assertIn("ownership_conflict", result.stderr)

    def test_init_worktrees_retries_initialized_worktree_after_setup_failure(self):
        self.apply(mode="enforce", apply=True)
        (self.repo / ".specgit.yaml").write_text("version: 2\n")
        self.env["FAIL_SPECGIT"] = "setup"
        worktree = self.base / "setup-retry"
        result = self.git("worktree", "add", "-q", "-b", "setup-retry", str(worktree))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((worktree / ".specgit.yaml").exists())
        self.assertEqual(required.classify({"worktree": str(worktree)}, BLOCK)[0], "initialized")

        self.events.unlink()
        self.env.pop("FAIL_SPECGIT")
        ok, preview = self.run_quietly(required.init_worktrees, self.repo)
        self.assertTrue(ok)
        self.assertIn("initialized; init only if declaration is absent", preview)
        self.assertIn(f"--cwd {worktree}", preview)
        self.assertEqual(self.events_log(), [])

        ok, output = self.run_quietly(required.init_worktrees, self.repo, apply=True)
        self.assertTrue(ok, output)
        self.assertIn(f"[init] setup+offline doctor healthy: {worktree}", output)
        calls = self.events_log()
        self.assertFalse(any(line.startswith("specgit init ") for line in calls))
        self.assertEqual(len(calls), 18)  # two initialized worktrees, each gets setup previews/writes and doctor
        self.assertEqual(sum(line.startswith("specgit doctor ") for line in calls), 2)

    def test_setup_reports_installed_and_declined_purposes_and_enforces_host_requirements(self):
        helper = Path(__file__).parents[1] / "specgit-new-worktree-setup.py"
        (self.repo / ".specgit.yaml").write_text("version: 2\n")
        result = subprocess.run(["python3", str(helper), "--cwd", str(self.repo)], cwd=self.repo,
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("setup generic: installed guard; declined context (unsupported)", result.stdout)
        for agent in ("claude", "codex", "opencode"):
            self.assertIn(f"setup {agent}: installed guard, context; declined none", result.stdout)
        self.assertIn("host_behavior=not_verified", result.stdout)

        env = {**self.env, "FAKE_SETUP_MISSING_CONTEXT": "1"}
        result = subprocess.run(["python3", str(helper), "--cwd", str(self.repo)], cwd=self.repo,
                                env=env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("specgit setup claude did not install required purpose(s) context", result.stderr)
        self.assertNotIn("specgit doctor", self.events_log()[-1])

    def test_doctor_must_report_offline_static_health_after_all_setup_calls(self):
        (self.repo / ".specgit.yaml").write_text("version: 2\n")
        helper = Path(__file__).parents[1] / "specgit-new-worktree-setup.py"
        for env_overrides, expected in (({"FAKE_DOCTOR_STATUS": "unknown"}, "status=unknown"),
                                        ({"FAKE_DOCTOR_HEALTH": "unhealthy"}, "health=unhealthy")):
            with self.subTest(env_overrides=env_overrides):
                self.events.unlink(missing_ok=True)
                env = {**self.env, **env_overrides}
                result = subprocess.run(["python3", str(helper), "--cwd", str(self.repo)], cwd=self.repo,
                                        env=env, text=True, capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("did not report offline static health", result.stderr)
                self.assertIn(expected, result.stderr)
                self.assertTrue(any(line.startswith("specgit doctor ") for line in self.events_log()))

        self.events.unlink()
        result = subprocess.run(["python3", str(helper), "--cwd", str(self.repo)], cwd=self.repo,
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("doctor: offline static healthy", result.stdout)
        calls = self.events_log()
        self.assertFalse(any(line.startswith("specgit init ") for line in calls))
        self.assertTrue(any(line.startswith("specgit doctor ") for line in calls))

    def test_existing_post_checkout_is_extended_and_restored(self):
        original = '#!/bin/sh\necho user-post-checkout >> "$EVENTS"\n'
        (self.hooks / "post-checkout").write_text(original)
        (self.hooks / "post-checkout").chmod(0o700)
        self.apply(mode="warn", apply=True)
        text = (self.hooks / "post-checkout").read_text()
        self.assertTrue(text.startswith("#!/bin/sh\n" + required.POST_CHECKOUT))
        self.assertTrue(text.endswith('echo user-post-checkout >> "$EVENTS"\n'))
        self.assertEqual((self.hooks / "post-checkout").stat().st_mode & 0o777, 0o700)
        self.apply(uninstall=True, apply=True)
        self.assertEqual((self.hooks / "post-checkout").read_text(), original)

    def test_previous_auto_init_block_upgrades_without_replacing_manual_hook_tail(self):
        path = self.hooks / "post-checkout"
        tail = 'echo user-post-checkout >> "$EVENTS"\n'
        path.write_text(required.POST_CHECKOUT_SHEBANG + required.PREVIOUS_POST_CHECKOUT + tail)
        path.chmod(0o755)
        self.apply(mode="warn", apply=True)
        updated = path.read_text()
        self.assertIn(required.POST_CHECKOUT, updated)
        self.assertNotIn(required.PREVIOUS_POST_CHECKOUT, updated)
        self.assertTrue(updated.endswith(tail))
        worktree = self.base / "legacy-init-only"
        result = self.git("worktree", "add", "-q", "-b", "legacy-init-only", str(worktree))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((worktree / ".specgit.yaml").exists())
        calls = self.events_log()
        self.assertEqual(len(calls), 11)  # includes the preserved user hook tail
        self.assertTrue(calls[0].startswith("specgit init "))
        self.assertTrue(any(line.startswith("specgit doctor ") for line in calls))

    def test_uninstall_keeps_preexisting_shebang_only_post_checkout(self):
        path = self.hooks / "post-checkout"
        path.write_text(required.POST_CHECKOUT_SHEBANG)
        path.chmod(0o755)
        self.apply(mode="warn", apply=True)
        self.apply(uninstall=True, apply=True)
        self.assertEqual(path.read_text(), required.POST_CHECKOUT_SHEBANG)
        self.assertEqual(path.stat().st_mode & 0o777, 0o755)

    def test_non_executable_or_incomplete_guard_is_refused_without_writes(self):
        pre_push = self.hooks / "pre-push"
        pre_push.chmod(0o644)
        with self.assertRaises(ValueError):
            self.apply(mode="warn", apply=True)
        self.assertFalse((self.hooks / "post-checkout").exists())
        pre_push.chmod(0o755)
        original = pre_push.read_text()
        pre_push.write_text(original.replace("# <<< specgit-v2:pre-push:end <<<\n", "", 1))
        with self.assertRaises(ValueError):
            self.apply(mode="warn", apply=True)
        self.assertEqual((self.hooks / "pre-commit").read_text(), PRE_COMMIT)
        self.assertFalse((self.hooks / "post-checkout").exists())

    def test_only_supported_shell_shebangs_are_accepted(self):
        for shebang in ("#!/usr/bin/fish", "#!/bin/csh", "#!/usr/bin/env fish",
                        "#!/usr/bin/env -S csh -f"):
            with self.subTest(shebang=shebang), self.assertRaises(ValueError):
                required.proposed(shebang + "\n", required.REQUIRE["warn"], ())
        for shebang in ("#!/bin/sh", "#!/bin/bash", "#!/usr/bin/dash",
                        "#!/usr/bin/env bash", "#!/usr/bin/env -S dash"):
            with self.subTest(shebang=shebang):
                self.assertTrue(required.proposed(shebang + "\n", "block", ()).endswith("block"))

    @requires_specgit
    def test_real_git_hooks_path_and_cli_guard_are_verified(self):
        real_repo = self.base / "real-repo"
        self.git("init", "-q", str(real_repo), cwd=self.base)
        guard_bin = Path(shutil.which("specgit"))
        guard_bin = guard_bin.resolve()
        setup = subprocess.run([str(guard_bin), "guard", "--install", "--cwd", str(real_repo), "--json"],
                               env=self.env, text=True, capture_output=True)
        self.assertEqual(setup.returncode, 0, setup.stderr)
        real_hooks = real_repo / ".git" / "hooks"
        for name in required.REQUIRED_HOOKS:
            path = real_hooks / name
            self.assertTrue(path.stat().st_mode & 0o111)
            text = path.read_text()
            self.assertEqual(text.count(f"# >>> specgit-v2:{name}:start >>>"), 1)
            self.assertEqual(text.count(f"# <<< specgit-v2:{name}:end <<<"), 1)
        changed, _ = self.run_quietly(required.install, required.hooks_directory(real_repo),
                                      mode="warn", apply=True)
        self.assertTrue(changed)

        custom_hooks = self.base / "shared-hooks"
        custom_hooks.mkdir()
        for name in required.REQUIRED_HOOKS:
            (custom_hooks / name).write_text((real_hooks / name).read_text())
            (custom_hooks / name).chmod(0o755)
        self.git("config", "core.hooksPath", str(custom_hooks), cwd=real_repo)
        before = {p.name: p.read_bytes() for p in custom_hooks.iterdir()}
        with self.assertRaises(ValueError):
            required.hooks_directory(real_repo)
        self.assertEqual({p.name: p.read_bytes() for p in custom_hooks.iterdir()}, before)

        # Git itself executes the CLI-generated block. A malformed local declaration
        # makes the guard return its parser error, proving the installed block ran.
        (real_repo / ".specgit.yaml").write_text("bad: [\n")
        self.git("config", "--unset", "core.hooksPath", cwd=real_repo)
        for name in required.REQUIRED_HOOKS:
            run = self.git("hook", "run", name, cwd=real_repo, check=False)
            self.assertEqual(run.returncode, 2, run.stderr)
            self.assertIn("Invalid v2 declaration", run.stderr)

    @requires_specgit
    def test_real_cli_setup_wires_all_supported_hosts_in_a_disposable_worktree(self):
        cli = Path(shutil.which("specgit")).resolve()
        root = self.base / "real-setup"
        self.git("init", "-q", "-b", "main", str(root), cwd=self.base)
        (root / "docs").mkdir()
        shutil.copy(Path(__file__).parents[2] / "docs" / "specgit-project.yaml",
                    root / "docs" / "specgit-project.yaml")
        shutil.copy(Path(__file__).parents[2] / "AGENTS.md", root / "AGENTS.md")
        self.git("add", ".", cwd=root)
        self.git("commit", "-q", "-m", "setup fixture", cwd=root)
        for agent in ("generic", "claude", "codex", "opencode"):
            args = ["setup", "--scope", "project", "--agent", agent,
                    "--hook-purpose", "guard", "--hook-purpose", "context"]
            if agent == "opencode":
                args.append("--opencode-claude-hooks")
            for extra in (("--dry-run", "--json"), ("--json",)):
                result = subprocess.run([str(cli), "--cwd", str(root), *args, *extra],
                                        env=self.env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr or result.stdout)
                report = json.loads(result.stdout)
                self.assertTrue(report.get("ok"), result.stdout)
                purposes = report["evidence"]["hook_purposes"][agent]
                self.assertIn("guard", purposes["installed"])
                if agent == "generic":
                    self.assertEqual(purposes["declined"][0]["purpose"], "context")
                    self.assertEqual(purposes["declined"][0]["status"], "unsupported")
                else:
                    self.assertIn("context", purposes["installed"])
        self.assertTrue((root / "CLAUDE.md").is_file())
        self.assertTrue((root / ".agents/skills/specgit-native/SKILL.md").is_file())
        for relative in (".claude/settings.json", ".codex/hooks.json", ".opencode/hooks.json"):
            content = (root / relative).read_text()
            self.assertIn("SessionStart", content)
            self.assertIn("PreToolUse", content)
        for name in required.REQUIRED_HOOKS:
            hook = root / ".git/hooks" / name
            self.assertTrue(hook.stat().st_mode & 0o111)
            self.assertIn(f"# >>> specgit-v2:{name}:start >>>", hook.read_text())

    def test_global_relative_hooks_path_is_rejected_for_main_and_linked_worktree(self):
        repo = self.base / "relative-hooks-repo"
        self.git("init", "-q", str(repo), cwd=self.base)
        (repo / "base.txt").write_text("base\n")
        self.git("add", "base.txt", cwd=repo)
        self.git("commit", "-q", "-m", "base", cwd=repo)
        hook = repo / ".git" / "hooks" / "pre-commit"
        hook.write_text('#!/bin/sh\necho invoked >> "$EVENTS"\nexit 41\n')
        hook.chmod(0o755)
        self.use_global_hooks_path(".git/hooks")

        main_commit = self.commit(repo, "main-change.txt")
        self.assertNotEqual(main_commit.returncode, 0)
        self.assertEqual(self.events_log(), ["invoked"])
        linked = self.base / "relative-hooks-linked"
        self.git("worktree", "add", "-q", "-b", "relative-feature", str(linked), cwd=repo)
        for root in (repo, linked):
            with self.subTest(root=root), self.assertRaises(ValueError):
                self.run_quietly(required.hooks_directory, root)
        linked_commit = self.commit(linked)
        self.assertEqual(linked_commit.returncode, 0, linked_commit.stderr)
        self.assertEqual(self.events_log(), ["invoked"])

    @requires_specgit
    def test_global_absolute_foreign_hooks_path_cannot_block_or_init_other_repo(self):
        host = self.base / "hooks-owner"
        self.git("init", "-q", str(host), cwd=self.base)
        (host / "docs").mkdir()
        (host / "docs" / "specgit-project.yaml").write_text("version: 2\n")
        (host / ".gitignore").write_text(".specgit.yaml\n")
        (host / "base.txt").write_text("base\n")
        self.git("add", ".", cwd=host)
        self.git("commit", "-q", "-m", "base", cwd=host)
        (host / ".specgit.yaml").write_text("version: 2\n")
        cli = Path(shutil.which("specgit")).resolve()
        install_guard = subprocess.run([str(cli), "guard", "--install", "--cwd", str(host), "--json"],
                                       env=self.env, text=True, capture_output=True)
        self.assertEqual(install_guard.returncode, 0, install_guard.stderr)
        host_hooks = host / ".git" / "hooks"
        self.run_quietly(required.install, host_hooks, mode="enforce", apply=True)

        foreign = self.base / "foreign-repo"
        self.git("init", "-q", str(foreign), cwd=self.base)
        (foreign / "docs").mkdir()
        (foreign / "docs" / "specgit-project.yaml").write_text("version: 2\n")
        (foreign / "AGENTS.md").write_text("# Rules\n\n" + BLOCK)
        (foreign / "scripts").mkdir()
        shutil.copy(Path(__file__).parents[1] / "specgit-new-worktree-setup.py",
                    foreign / "scripts" / "specgit-new-worktree-setup.py")
        (foreign / "base.txt").write_text("foreign\n")
        self.git("add", ".", cwd=foreign)
        self.git("commit", "-q", "-m", "base", cwd=foreign)
        self.use_global_hooks_path(host_hooks)
        for root in (host, foreign):
            with self.subTest(root=root), self.assertRaises(ValueError):
                self.run_quietly(required.hooks_directory, root)

        self.env["FAIL_SPECGIT"] = "init"
        linked = self.base / "foreign-linked"
        checkout = self.git("worktree", "add", "-q", "-b", "foreign-feature", str(linked), cwd=foreign)
        self.assertEqual(checkout.returncode, 0, checkout.stderr)
        self.assertTrue(any(line.startswith("specgit init --config-file ") and str(linked) in line
                            for line in self.events_log()))
        self.assertFalse((linked / ".specgit.yaml").exists())
        blocked = self.commit(linked)
        self.assertNotEqual(blocked.returncode, 0)
        self.assertIn("SpecGit not initialized", blocked.stderr)

    def test_non_executable_existing_post_checkout_rejects_entire_plan(self):
        path = self.hooks / "post-checkout"
        original = '#!/bin/sh\necho existing\n'
        path.write_text(original)
        path.chmod(0o644)
        with self.assertRaises(ValueError):
            self.apply(mode="warn", apply=True)
        self.assertEqual((self.hooks / "pre-commit").read_text(), PRE_COMMIT)
        self.assertEqual((self.hooks / "pre-push").read_text(), PRE_PUSH)
        self.assertEqual(path.read_text(), original)
        self.assertEqual(path.stat().st_mode & 0o777, 0o644)

    def test_changed_duplicate_or_foreign_content_is_refused(self):
        self.apply(mode="enforce", apply=True)
        path = self.hooks / "pre-commit"
        installed = path.read_text()
        for broken in (installed.replace("refused.", "skipped."),
                       installed + required.START,
                       installed.replace(required.END, "")):
            path.write_text(broken)
            for kwargs in ({"mode": "warn"}, {"uninstall": True}):
                with self.assertRaises(ValueError):
                    self.apply(apply=True, **kwargs)
            self.assertEqual(path.read_text(), broken)
        with self.assertRaises(ValueError):
            required.proposed("#!/usr/bin/env python3\n", required.REQUIRE["warn"], ())

    def test_missing_guard_hook_is_refused(self):
        (self.hooks / "pre-push").unlink()
        with self.assertRaises(ValueError):
            self.apply(mode="warn", apply=True)
        self.assertEqual((self.hooks / "pre-commit").read_text(), PRE_COMMIT)
        self.assertFalse((self.hooks / "post-checkout").exists())

    def test_status_and_init_worktrees_only_touch_ready_worktrees(self):
        ready = self.base / "ready"
        self.git("worktree", "add", "-q", "-b", "ready", str(ready))
        (self.repo / ".specgit.yaml").write_text("version: 2\n")
        # A branch whose AGENTS.md SpecGit block predates main's cannot be initialized yet.
        self.git("checkout", "-q", "-b", "stale")
        (self.repo / "AGENTS.md").write_text("# Rules\n\n" + BLOCK.replace("agent-install", "installation"))
        self.git("commit", "-q", "--no-verify", "-am", "stale block")
        self.git("checkout", "-q", "main")
        stale = self.base / "stale"
        self.git("worktree", "add", "-q", str(stale), "stale")
        missing = self.base / "missing"
        self.git("worktree", "add", "-q", "-b", "missing", str(missing))
        shutil.rmtree(missing)
        states = {Path(entry["worktree"]).name: state
                  for entry, state, _ in self.run_quietly(required.survey, self.repo)[0]}
        self.assertEqual(states, {"repo": "initialized", "ready": "ready",
                                  "stale": "stale-block", "missing": "missing"})
        self.apply(mode="warn", apply=True)
        _, report = self.run_quietly(required.status, self.repo, self.hooks)
        self.assertIn("pre-commit: warn", report)
        self.assertIn("post-checkout: auto-init+setup", report)
        self.assertIn("summary: initialized 1, missing 1, ready 1, stale-block 1 (total 4;", report)
        ok, preview = self.run_quietly(required.init_worktrees, self.repo)
        self.assertTrue(ok)
        self.assertIn(f"would run (ready; init only if declaration is absent): {__import__('sys').executable} {self.repo}/scripts/specgit-new-worktree-setup.py --cwd {ready}", preview)
        self.assertIn(f"skip stale-block: {stale}", preview)
        self.assertEqual(self.events_log(), [])
        ok, output = self.run_quietly(required.init_worktrees, self.repo, apply=True)
        self.assertTrue(ok, output)
        self.assertIn(f"[init] setup+offline doctor healthy: {ready}", output)
        self.assertTrue((ready / ".specgit.yaml").exists())
        self.assertFalse((stale / ".specgit.yaml").exists())
        self.assertEqual(len(self.events_log()), 19)
        self.assertTrue(all(" GIT_DIR=unset " in line for line in self.events_log()))
        self.env["FAIL_SPECGIT"] = "init"
        (ready / ".specgit.yaml").unlink()
        ok, output = self.run_quietly(required.init_worktrees, self.repo, apply=True)
        self.assertFalse(ok)
        self.assertIn(f"[init] FAILED (1; [specgit-worktree] specgit init", output)
        self.assertIn("ownership_conflict:", output)
        self.assertTrue(output.rstrip().endswith(f"): {ready}"))


class WithoutSpecgitTests(unittest.TestCase):
    """The gate must pass where specgit is absent: the CLI-driven tests skip instead of raising."""

    def test_cli_driven_tests_skip_when_specgit_is_not_on_path(self):
        path = os.pathsep.join(d for d in os.environ.get("PATH", "").split(os.pathsep)
                               if d and not os.access(os.path.join(d, "specgit"), os.X_OK))
        names = [f"test_install_specgit_required_hooks.RequiredHookTests.{name}" for name in REAL_CLI_TESTS]
        run = subprocess.run([sys.executable, "-m", "unittest", "-v", *names], cwd=Path(__file__).parent,
                             env={**os.environ, "PATH": path, "PYTHONDONTWRITEBYTECODE": "1"},
                             capture_output=True, text=True)
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertEqual(run.stderr.count("skipped"), len(REAL_CLI_TESTS) + 1, run.stderr)
        self.assertIn("OK (skipped=3)", run.stderr)


if __name__ == "__main__":
    unittest.main()
