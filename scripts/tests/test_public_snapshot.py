"""Offline tests of the public snapshot tool (#261); no network, no push."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("public_snapshot", ROOT / "scripts/make-public-snapshot.py")
snapshot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(snapshot)

# Samples are assembled from pieces so this file holds no identifier in plain text.
SAMPLES = {
    "private network": "http://192." + "168.7.8:13333/",
    "dev host octets": "host 10.0." + "33." + "110",
    "formal host octets": "x" + "33." + "146" + "y",
    "domain": "https://git." + "yc" + "game" + ".com/group",
    "organization": "Yun" + "chan".lower() + " Ltd",
    "organization zh": "云" + "蟾" + "游戏",
    "server path": "/" + "DATA" + "/AppData/app",
    "temporary path": "/private/" + "tmp/evidence",
    "compose project": "meshyai_" + "go_prod",
    # #334: local user directories, private keys and common credential prefixes.
    "macOS home": "/" + "Users" + "/alice/project",
    "linux home": "/" + "home" + "/alice/.ssh",
    "private key": "-----BEGIN " + "OPENSSH PRIVATE" + " KEY-----",
    "rsa private key": "-----BEGIN " + "RSA PRIVATE" + " KEY-----",
    "gitlab token": "glp" + "at-" + "A1b2C3d4E5f6G7h8I9j0",
    "openai key": "s" + "k-proj-" + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7",
    "github token": "gh" + "p_" + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8",
    "meshy key": "ms" + "y_" + "A1b2C3d4E5f6G7h8I9j0K1",
    "tripo key": "ts" + "k_" + "A1b2C3d4E5f6G7h8I9j0K1",
}


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


def repository(directory, files):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    git(directory, "init", "-q", "-b", "main")
    git(directory, "config", "user.name", "Snapshot Tester")
    git(directory, "config", "user.email", "tester@example.invalid")
    git(directory, "config", "commit.gpgsign", "false")
    for name, content in files.items():
        path = directory / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
    git(directory, "add", "-A")
    git(directory, "commit", "-q", "-m", "source")
    return git(directory, "rev-parse", "HEAD")


class PatternTests(unittest.TestCase):
    def setUp(self):
        self.patterns = snapshot.compile_patterns(use_local=False)

    def scan_text(self, text, name="file.txt"):
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / name).write_text(text, encoding="utf-8")
            return snapshot.scan(directory, self.patterns)

    def test_every_builtin_pattern_finds_its_sample(self):
        for label, sample in SAMPLES.items():
            with self.subTest(label=label):
                self.assertTrue(self.scan_text("before\n" + sample + "\nafter\n"), sample)

    def test_ordinary_text_and_unrelated_cjk_are_clean(self):
        # Bytes regex: a multi-byte character must not degrade into a byte class.
        self.assertEqual(self.scan_text("点云表示，10.0.0.1，/srv/app/data，art-ai-go-production\n"), [])

    def test_public_ci_paths_and_short_fake_secrets_are_clean(self):
        # A prebuilt binary may carry the public CI runner's home; test fixtures use
        # short, obviously fake secrets that are not credential-shaped (#334).
        clean = ["/" + "Users" + "/runner/work/x", "/" + "home" + "/runner/work/x",
                 "s" + "k-live-1234567890abcdef", "s" + "k-playwright-default-secret",
                 "task-" + "processing-plan-step-identifier-0123", "ms" + "y_short", "ts" + "k_short"]
        for text in clean:
            with self.subTest(text=text):
                self.assertEqual(self.scan_text("before " + text + " after\n"), [])

    def test_tool_files_do_not_match_their_own_patterns(self):
        with tempfile.TemporaryDirectory() as directory:
            for name in ("scripts/make-public-snapshot.py", "scripts/check-public-tree.sh",
                         "scripts/public-snapshot.exclude", "scripts/tests/test_public_snapshot.py"):
                target = Path(directory) / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((ROOT / name).read_bytes())
            self.assertEqual(snapshot.scan(directory, self.patterns), [])

    def test_escaped_check_rules_and_binary_screenshots_are_clean(self):
        # Escaped rules do not match the literal pattern, so no allowlist is needed.
        # Screenshots are scanned as bytes like every other file.
        files = sorted((ROOT / "docs/screenshots").glob("*.png"))
        files = [path for path in files if path.is_file()]
        if not files:
            self.skipTest("screenshots not in this checkout")
        with tempfile.TemporaryDirectory() as directory:
            for path in files:
                target = Path(directory) / path.relative_to(ROOT)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(path.read_bytes())
            self.assertEqual(snapshot.scan(directory, self.patterns), [])
        self.assertTrue(self.scan_text("grep '" + SAMPLES["private network"] + "'\n"))

    def test_findings_name_file_line_and_path_matches(self):
        findings = self.scan_text("a\nb\n" + SAMPLES["domain"] + "\n")
        self.assertTrue(findings[0].startswith("file.txt:3: "), findings)
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / ("notes-" + "yc" + "game.md")).write_text("clean\n")
            self.assertTrue(snapshot.scan(directory, self.patterns)[0].endswith(": path: (?i:yc[g]ame)"))

    def test_extra_and_file_patterns_apply(self):
        with tempfile.TemporaryDirectory() as directory:
            listing = Path(directory) / "patterns.txt"
            listing.write_text("# comment\ninternal-host\\.example\n")
            patterns = snapshot.compile_patterns(["secret-word"], [listing], use_local=False)
            (Path(directory) / "a.txt").write_text("secret-word and internal-host.example\n")
            labels = [finding.split(": ")[1] for finding in snapshot.scan(directory, patterns)]
            self.assertEqual(sorted(labels), ["patterns.txt:1", "secret-word"])
        with self.assertRaises(snapshot.SnapshotError):
            snapshot.compile_patterns(["("], use_local=False)

    def test_local_environment_values_become_literals(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "environments.json"
            path.write_text(json.dumps({"version": 1, "acceptance": {"forge_url": "https://forge.internal.test/g/p"},
                "environments": {"dev-test": {
                    "ssh": "root@build-host.internal.test", "root": "/srv/plinth-dev",
                    "base_url": "http://10.9.8.7:13333", "compose_project": "plinth_dev",
                    "protect_projects": ["plinth_candidate"],
                    "layout": {"GO_WEB_HOST_PORT": "13333", "GO_LEGACY_INPUT_ASSETS_DIR": "/srv/legacy/assets"},
                    "build": {"goproxy": "https://proxy.internal.test/go/,direct",
                              "add_hosts": ["npm.internal.test:10.9.8.6"]}},
                    "template": {"ssh": "root@<host>", "root": "<root>", "compose_project": "x"}}}))
            literals = snapshot.environment_literals(path)
        self.assertEqual(literals, sorted({
            "forge.internal.test", "build-host.internal.test", "/srv/plinth-dev", "10.9.8.7", "plinth_dev",
            "plinth_candidate", "/srv/legacy/assets", "npm.internal.test", "proxy.internal.test", "10.9.8.6"}))
        self.assertEqual(snapshot.environment_literals(Path(directory) / "missing.json"), [])

    def test_obsolete_npm_registry_address_stays_forbidden(self):
        # release-env.py refuses this removed field (#257); scanning stays independent of
        # release config, so a stale local file's address is still a forbidden literal.
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "environments.json"
            path.write_text(json.dumps({"version": 1, "environments": {"dev-test": {
                "ssh": "root@build-host.internal.test", "root": "/srv/plinth-dev",
                "base_url": "http://10.9.8.7:13333", "compose_project": "plinth_dev",
                "build": {"npm_registry": "https://npm.internal.test/npm/"}}}}))
            literals = snapshot.environment_literals(path)
            self.assertIn("npm.internal.test", literals)
            tree = Path(directory) / "tree"
            tree.mkdir()
            (tree / "leaked.txt").write_text("mirror at https://npm.internal.test/npm/\n")
            patterns = snapshot.compile_patterns(literals, [], use_local=False)
            self.assertEqual([finding.split(": ")[1] for finding in snapshot.scan(tree, patterns)],
                             ["npm.internal.test"])


class ExclusionTests(unittest.TestCase):
    def test_manifest_semantics(self):
        patterns = snapshot.load_exclusions()
        for path in ("deploy/local/environments.json", "deploy/environments.local.json", ".specgit.yaml",
                     ".agents/skills/x/SKILL.md", "handoff.md", "sub/handoff.md", ".env", "production/runtime.env",
                     ".claude/worktrees/x/file"):
            with self.subTest(path=path):
                self.assertTrue(snapshot.excluded(path, patterns))
        for path in (".env.example", "deploy/environments.example.json", "docs/deployment.md",
                     ".gitlab-ci.yml", "src/components/ui/focus-handoff.ts", ".claude/settings.json"):
            with self.subTest(path=path):
                self.assertFalse(snapshot.excluded(path, patterns))


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.source = self.base / "source"
        repository_patch = patch.object(snapshot, "REPOSITORY", self.source)
        repository_patch.start()
        self.addCleanup(repository_patch.stop)

    def run_tool(self, *argv):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = snapshot.main([*argv, "--no-local"])
        return code, out.getvalue(), err.getvalue()

    def test_make_builds_one_local_root_commit_without_remote(self):
        sha = repository(self.source, {"README.md": "Plinth\n", "src/app.ts": "export {};\n",
                                       "deploy/local/environments.json": "{}\n"})
        out = self.base / "public"
        code, stdout, stderr = self.run_tool("make", "--version", "v1.0.0", "--out", str(out), "--tag")
        self.assertEqual(code, 0, stderr)
        self.assertIn("not pushed", stdout)
        self.assertEqual(git(out, "rev-list", "--count", "HEAD"), "1")
        self.assertEqual(git(out, "remote"), "")
        self.assertEqual(git(out, "log", "-1", "--format=%an <%ae>|%cn <%ce>"),
                         "Snapshot Tester <tester@example.invalid>|Snapshot Tester <tester@example.invalid>")
        message = git(out, "log", "-1", "--format=%B")
        self.assertIn("Plinth v1.0.0", message)
        self.assertIn(sha, message)
        self.assertEqual(git(out, "tag", "--list"), "v1.0.0")
        self.assertEqual(sorted(git(out, "ls-files").splitlines()), ["README.md", "src/app.ts"])
        self.assertEqual(git(out, "rev-parse", "HEAD^{tree}") != "", True)
        self.assertEqual([p.name for p in self.base.iterdir() if ".partial-" in p.name], [])

    def test_forbidden_content_fails_and_leaves_nothing(self):
        repository(self.source, {"docs/ops.md": "deploy to " + SAMPLES["server path"] + "\n"})
        out = self.base / "public"
        code, _, stderr = self.run_tool("make", "--version", "v1.0.0", "--out", str(out))
        self.assertEqual(code, 1)
        self.assertIn("FORBIDDEN docs/ops.md:1", stderr)
        self.assertFalse(out.exists())
        self.assertEqual(sorted(p.name for p in self.base.iterdir()), ["source"])
        code, _, stderr = self.run_tool("check", "--rev", "HEAD")
        self.assertEqual(code, 1)
        self.assertIn("FORBIDDEN docs/ops.md:1", stderr)

    def test_check_scans_the_exact_revision_not_the_worktree(self):
        repository(self.source, {"a.txt": "clean\n"})
        (self.source / "untracked.txt").write_text(SAMPLES["domain"] + "\n")
        self.assertEqual(self.run_tool("check", "--rev", "HEAD")[0], 0)
        self.assertEqual(self.run_tool("check", "--tree", str(self.source))[0], 1)

    def test_worktree_mode_scans_tracked_files_as_edited(self):
        repository(self.source, {"a.txt": "clean\n"})
        (self.source / "untracked.txt").write_text(SAMPLES["domain"] + "\n")
        self.assertEqual(self.run_tool("check", "--worktree")[0], 0)
        (self.source / "a.txt").write_text(SAMPLES["server path"] + "\n")
        code, _, stderr = self.run_tool("check", "--worktree")
        self.assertEqual(code, 1)
        self.assertIn("FORBIDDEN a.txt:1", stderr)

    def test_make_refusals(self):
        repository(self.source, {"a.txt": "clean\n"})
        busy = self.base / "busy"
        busy.mkdir()
        (busy / "x").write_text("x")
        for argv in (["--version", "1.0", "--out", str(self.base / "new")],
                     ["--version", "v1.0.0", "--out", str(busy)],
                     ["--version", "v1.0.0", "--out", str(self.source / "nested")],
                     ["--version", "v1.0.0", "--out", str(self.base / "new"), "--rev", "no-such-revision"]):
            with self.subTest(argv=argv):
                self.assertEqual(self.run_tool("make", *argv)[0], 2)
        self.assertFalse((self.base / "new").exists())

    def test_check_public_tree_wrapper_uses_the_same_scan(self):
        repository(self.source, {"a.txt": "clean\n"})
        result = subprocess.run(["bash", str(ROOT / "scripts/check-public-tree.sh"), "HEAD", "--no-local"],
                                cwd=self.source, capture_output=True, text=True,
                                env={**os.environ, "ART_AI_ENVIRONMENTS_FILE": str(self.base / "none.json")})
        # The wrapper scans this checkout's revision; only the exit protocol is asserted here.
        self.assertIn(result.returncode, (0, 1), result.stderr)
        self.assertIn("[snapshot]", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
