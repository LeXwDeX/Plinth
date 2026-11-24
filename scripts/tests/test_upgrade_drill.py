#!/usr/bin/env python3
"""Offline tests for the upgrade rehearsal script. No Docker, no environment.

`docker` and the readiness probe are replaced by module-level stubs; everything
else (argument guards, the database copy, the read-only smoke, the report) runs
against real temporary files.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
SCRIPT = Path(__file__).resolve().parents[1] / "upgrade-drill.py"


def load():
    spec = importlib.util.spec_from_file_location("upgrade_drill", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


AGENT_CONFIGURATION = {"system_prompt": "p", "domain_context": {}, "skills": [],
                       "enabled_tool_names": ["get_task"], "enabled_mcp_preset_ids": []}


def make_database(path, head=93, rows=2, tables=None):
    """A database shaped like an older release: a ledger and the key tables."""
    connection = sqlite3.connect(path)
    connection.execute(
        "CREATE TABLE go_schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL,"
        " checksum TEXT NOT NULL, applied_at TEXT NOT NULL)")
    for version in range(1, head + 1):
        connection.execute(
            "INSERT INTO go_schema_migrations (version, name, checksum, applied_at)"
            " VALUES (?, 'm', 'c', 't')", (version,))
    for table in tables or load().SMOKE_TABLES:
        if table == "agent_configuration_versions":
            # The shape of migration 0041: the drill appends a version to it.
            connection.execute(
                "CREATE TABLE agent_configuration_versions (revision INTEGER PRIMARY KEY,"
                " configuration_json TEXT NOT NULL, actor_id TEXT NOT NULL, source TEXT NOT NULL,"
                " created_at TEXT NOT NULL)")
            for index in range(rows):
                connection.execute(
                    "INSERT INTO agent_configuration_versions VALUES (?, ?, 'admin', 'publish', 't')",
                    (index + 1, json.dumps(AGENT_CONFIGURATION)))
            continue
        connection.execute("CREATE TABLE %s (id TEXT PRIMARY KEY)" % table)
        for index in range(rows):
            connection.execute("INSERT INTO %s (id) VALUES (?)" % table, ("%s-%d" % (table, index),))
    connection.commit()
    connection.close()


def reconcile_like_startup(image, workdir, port):
    """Stand-in for the candidate's startup reconcile (#500, #525, #554): append
    a version in the current format that turns off the baseline tools the seeded
    version did not list, leaves out the tools the Catalog retired and stores the
    Schema's prompts."""
    database = Path(workdir) / "data" / "art-ai.db"
    connection = sqlite3.connect(database)
    revision, encoded = connection.execute(
        "SELECT revision, configuration_json FROM agent_configuration_versions"
        " ORDER BY revision DESC LIMIT 1").fetchone()
    configuration = json.loads(encoded)
    baseline = load().legacy_tool_baseline()
    enabled = configuration["enabled_tool_names"]
    configuration["format"] = 2
    configuration["system_prompt"] = "Schema 系统提示词"
    configuration["domain_context"] = {"image": "Schema 2D", "model-3d": "Schema 3D"}
    configuration["disabled_tool_names"] = [name for name in baseline if name not in enabled]
    configuration["enabled_tool_names"] = [
        name for name in enabled if name not in ("observe_task", "request_consent")]
    connection.execute(
        "INSERT INTO agent_configuration_versions VALUES (?, ?, 'system',"
        " 'retire_undeclared_selections', 't')", (revision + 1, json.dumps(configuration)))
    connection.commit()
    connection.close()


class GuardTests(unittest.TestCase):
    def setUp(self):
        self.drill = load()
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "copy.db"
        make_database(self.source)
        self.environments = self.root / "environments.json"

    def write_environments(self, root):
        self.environments.write_text(json.dumps({"environments": {"dev-test": {"root": str(root)}}}),
                                     encoding="utf-8")

    def run_main(self, extra, env=None):
        argv = ["--image", "art-ai-go-production:drill", "--database", str(self.source),
                "--workdir", str(self.root / "work"), "--environments", str(self.environments)] + extra
        with mock.patch.dict(os.environ, env or {}, clear=False):
            return self.drill.main(argv)

    def test_arguments_are_required(self):
        with self.assertRaises(SystemExit) as caught:
            self.drill.parse_args(["--image", "i"])
        self.assertEqual(caught.exception.code, 2)

    def test_plain_copy_runs_without_an_acknowledgement(self):
        self.write_environments(self.root / "release-root")
        calls = []
        with mock.patch.object(self.drill, "run_command",
                               return_value=mock.Mock(returncode=0, stdout="image-id", stderr="")), \
             mock.patch.object(self.drill.shutil, "which", return_value="/usr/bin/docker"), \
             mock.patch.object(self.drill, "start_container",
                               side_effect=lambda image, workdir, port: (
                                   calls.append((image, port)),
                                   reconcile_like_startup(image, workdir, port))), \
             mock.patch.object(self.drill, "wait_ready", return_value=True), \
             mock.patch.object(self.drill, "container_logs", return_value="log line"):
            self.assertEqual(self.run_main([]), 0)
        self.assertEqual(len(calls), 1)

    def test_release_root_needs_the_half_open_pair(self):
        self.write_environments(self.root / "release-root")
        inside = self.root / "release-root" / "backups" / "art-ai.db"
        inside.parent.mkdir(parents=True)
        inside.write_bytes(self.source.read_bytes())
        argv = ["--image", "art-ai-go-production:drill", "--database", str(inside),
                "--workdir", str(self.root / "work"), "--environments", str(self.environments)]
        with mock.patch.object(self.drill, "run_command",
                               return_value=mock.Mock(returncode=0, stdout="image-id", stderr="")), \
             mock.patch.object(self.drill.shutil, "which", return_value="/usr/bin/docker"), \
             mock.patch.object(self.drill, "start_container", side_effect=reconcile_like_startup), \
             mock.patch.object(self.drill, "wait_ready", return_value=True), \
             mock.patch.object(self.drill, "container_logs", return_value=""):
            with mock.patch.dict(os.environ, {}, clear=False):
                self.assertEqual(self.drill.main(argv), 2)
            with mock.patch.dict(os.environ, {"ART_AI_LIVE_ACK": "1"}, clear=False):
                self.assertEqual(self.drill.main(argv), 2)
            argv.append("--allow-real-path")
            with mock.patch.dict(os.environ, {}, clear=False):
                self.assertEqual(self.drill.main(argv), 2)
            with mock.patch.dict(os.environ, {"ART_AI_LIVE_ACK": "1"}, clear=False):
                self.assertEqual(self.drill.main(argv), 0)

    def test_wal_sidecar_marks_a_live_database(self):
        (self.root / "live.db-wal").write_bytes(b"")
        reason = self.drill.live_source_reason(self.root / "live.db", [])
        self.assertIn("wal", reason)

    def test_clean_copy_has_no_reason(self):
        self.assertIsNone(self.drill.live_source_reason(self.source, []))

    def test_environments_roots_reads_every_root(self):
        self.write_environments(self.root / "one")
        document = json.loads(self.environments.read_text(encoding="utf-8"))
        document["environments"]["formal-test"] = {"root": str(self.root / "two")}
        self.environments.write_text(json.dumps(document), encoding="utf-8")
        roots = self.drill.environments_roots(self.environments)
        self.assertEqual([Path(root).name for root in roots], ["one", "two"])

    def test_missing_environments_file_is_not_an_error(self):
        self.assertEqual(self.drill.environments_roots(self.root / "absent.json"), [])


class SmokeTests(unittest.TestCase):
    def setUp(self):
        self.drill = load()
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_copy_brings_sidecars_and_leaves_the_source_alone(self):
        source = self.root / "art-ai.db"
        make_database(source)
        (self.root / "art-ai.db-wal").write_bytes(b"wal-bytes")
        before = hashlib.sha256(source.read_bytes()).hexdigest()
        data = self.root / "work" / "data"
        data.mkdir(parents=True)
        target, notes = self.drill.copy_database(source, data)
        self.assertTrue(target.is_file())
        self.assertTrue(Path(str(target) + "-wal").is_file())
        self.assertEqual(len(notes), 1)
        self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(), before)

    def test_smoke_reports_a_missing_table(self):
        source = self.root / "partial.db"
        make_database(source, tables=("go_chat_sessions",))
        checks = dict((name, (ok, detail)) for name, ok, detail in self.drill.smoke_readonly(source))
        self.assertEqual(checks["integrity_check"][0], True)
        self.assertEqual(checks["foreign_key_check"][0], True)
        self.assertEqual(checks["表 execution_grants"][0], False)
        self.assertEqual(checks["表 go_chat_sessions"][0], True)

    def test_smoke_checks_the_ledger_is_contiguous(self):
        source = self.root / "gap.db"
        make_database(source)
        connection = sqlite3.connect(source)
        connection.execute("DELETE FROM go_schema_migrations WHERE version = 40")
        connection.commit()
        connection.close()
        checks = dict((name, (ok, detail)) for name, ok, detail in self.drill.smoke_readonly(source))
        self.assertEqual(checks["migration 台账"][0], False)

    def test_compare_counts_fails_when_rows_disappear(self):
        problems = self.drill.compare_counts({"go_chat_sessions": 3}, {"go_chat_sessions": 2})
        self.assertIn("go_chat_sessions 行数 3 → 2", problems)
        self.assertEqual(self.drill.compare_counts({"a": 1}, {"a": 1}), [])

    def test_report_states_the_verdict_and_the_failures(self):
        report = self.drill.render_report(
            [("候选镜像", "art-ai-go-production:drill")],
            [("integrity_check", True, "ok"), ("表 go_chat_sessions", False, "缺失")],
            ["表 go_chat_sessions 未通过：缺失"], "FAIL", "log line")
        self.assertIn("结论：FAIL", report)
        self.assertIn("PASS integrity_check：ok", report)
        self.assertIn("FAIL 表 go_chat_sessions：缺失", report)
        self.assertIn("log line", report)


class DrillRunTests(unittest.TestCase):
    """The whole run with docker replaced by stubs."""

    def setUp(self):
        self.drill = load()
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "art-ai.db"
        make_database(self.source, head=93)
        self.environments = self.root / "environments.json"
        self.environments.write_text(json.dumps({"environments": {}}), encoding="utf-8")
        self.workdir = self.root / "work"
        self.report = self.root / "report.md"

    def run_drill(self, ready=True, keep=False, report=None, startup=reconcile_like_startup):
        argv = ["--image", "art-ai-go-production:drill", "--database", str(self.source),
                "--workdir", str(self.workdir), "--report", str(report or self.report),
                "--environments", str(self.environments)]
        if keep:
            argv.append("--keep")
        with mock.patch.object(self.drill, "run_command",
                               return_value=mock.Mock(returncode=0, stdout="image-id", stderr="")), \
             mock.patch.object(self.drill.shutil, "which", return_value="/usr/bin/docker"), \
             mock.patch.object(self.drill, "start_container", side_effect=startup), \
             mock.patch.object(self.drill, "wait_ready", return_value=ready), \
             mock.patch.object(self.drill, "container_logs", return_value="container log"):
            return self.drill.main(argv)

    def test_passing_drill_writes_a_report_and_cleans_up(self):
        self.assertEqual(self.run_drill(), 0)
        report = self.report.read_text(encoding="utf-8")
        self.assertIn("结论：PASS", report)
        self.assertIn("integrity_check：ok", report)
        self.assertIn("1..93", report)
        self.assertFalse(self.workdir.exists())

    def test_failing_readiness_fails_the_drill_and_keeps_nothing(self):
        self.assertEqual(self.run_drill(ready=False), 1)
        self.assertIn("结论：FAIL", self.report.read_text(encoding="utf-8"))
        self.assertFalse(self.workdir.exists())

    def test_keep_leaves_the_directory(self):
        self.assertEqual(self.run_drill(keep=True), 0)
        self.assertTrue((self.workdir / "report.md").is_file())
        self.assertTrue((self.workdir / "data" / "art-ai.db").is_file())

    def test_a_failing_check_fails_the_drill(self):
        with mock.patch.object(self.drill, "smoke_readonly",
                               return_value=[("integrity_check", False, "损坏")]):
            self.assertEqual(self.run_drill(), 1)
        self.assertIn("结论：FAIL", self.report.read_text(encoding="utf-8"))

    def test_lost_rows_fail_the_drill(self):
        before = {"counts": {"go_chat_sessions": 3}, "head": 93}
        after = {"counts": {"go_chat_sessions": 2}, "head": 112}
        with mock.patch.object(self.drill, "read_state", side_effect=[before, after]):
            self.assertEqual(self.run_drill(), 1)
        self.assertIn("go_chat_sessions 行数 3 → 2", self.report.read_text(encoding="utf-8"))

    def test_unwritable_report_fails_cleanly(self):
        target = self.root / "missing" / "report.md"
        self.assertEqual(self.run_drill(report=target), 2)
        self.assertFalse(target.exists())

    def test_the_copy_carries_a_version_in_the_v180_shape(self):
        self.assertEqual(self.run_drill(keep=True), 0)
        connection = sqlite3.connect(self.workdir / "data" / "art-ai.db")
        rows = connection.execute(
            "SELECT revision, configuration_json, actor_id FROM agent_configuration_versions"
            " ORDER BY revision").fetchall()
        connection.close()
        seeded = json.loads(rows[2][1])
        self.assertEqual(rows[2][2], "upgrade-drill")
        baseline = self.drill.legacy_tool_baseline()
        self.assertEqual(seeded["enabled_tool_names"], [n for n in baseline if n != "create_motion_clip"])
        self.assertNotIn("format", seeded)
        self.assertIn("observe_task", seeded["enabled_tool_names"])
        self.assertTrue(seeded["system_prompt"].startswith(self.drill.v180_default_prompts()[0]))
        self.assertIn(self.drill.DRILL_PROMPT_MARKER, seeded["system_prompt"])
        self.assertEqual(sorted(seeded["domain_context"]), ["image", "model-3d"])
        self.assertTrue(all(self.drill.DRILL_PROMPT_MARKER in text for text in seeded["domain_context"].values()))
        reconciled = json.loads(rows[3][1])
        self.assertNotIn("observe_task", reconciled["enabled_tool_names"])
        self.assertEqual(reconciled["disabled_tool_names"], ["create_motion_clip"])
        report = self.report.read_text(encoding="utf-8")
        self.assertIn("PASS 旧 Agent 配置可读", report)
        self.assertIn("PASS 旧 Agent 配置保留", report)
        self.assertIn("PASS v1.8.0 配置工具换算", report)
        self.assertIn("PASS v1.8.0 配置提示词取 Schema", report)

    def test_a_cleaned_version_that_keeps_the_stored_prompt_fails(self):
        def startup(image, workdir, port):
            reconcile_like_startup(image, workdir, port)
            connection = sqlite3.connect(Path(workdir) / "data" / "art-ai.db")
            revision, encoded = connection.execute(
                "SELECT revision, configuration_json FROM agent_configuration_versions"
                " ORDER BY revision DESC LIMIT 1").fetchone()
            configuration = json.loads(encoded)
            configuration["domain_context"]["model-3d"] = "冻结的旧文本" + load().DRILL_PROMPT_MARKER
            connection.execute("UPDATE agent_configuration_versions SET configuration_json = ?"
                               " WHERE revision = ?", (json.dumps(configuration), revision))
            connection.commit()
            connection.close()
        self.assertEqual(self.run_drill(startup=startup), 1)
        self.assertIn("FAIL v1.8.0 配置提示词取 Schema", self.report.read_text(encoding="utf-8"))

    def test_a_cleaned_version_that_turns_off_a_new_tool_fails(self):
        def startup(image, workdir, port):
            reconcile_like_startup(image, workdir, port)
            connection = sqlite3.connect(Path(workdir) / "data" / "art-ai.db")
            revision, encoded = connection.execute(
                "SELECT revision, configuration_json FROM agent_configuration_versions"
                " ORDER BY revision DESC LIMIT 1").fetchone()
            configuration = json.loads(encoded)
            configuration["disabled_tool_names"].append("analyze_model")
            connection.execute("UPDATE agent_configuration_versions SET configuration_json = ?"
                               " WHERE revision = ?", (json.dumps(configuration), revision))
            connection.commit()
            connection.close()
        self.assertEqual(self.run_drill(startup=startup), 1)
        self.assertIn("FAIL v1.8.0 配置工具换算", self.report.read_text(encoding="utf-8"))

    def test_a_start_that_keeps_retired_agent_tools_fails_the_drill(self):
        self.assertEqual(self.run_drill(startup=lambda image, workdir, port: None), 1)
        self.assertIn("FAIL 旧 Agent 配置可读", self.report.read_text(encoding="utf-8"))

    def test_a_cleaned_version_that_still_enables_a_retired_tool_fails(self):
        def startup(image, workdir, port):
            connection = sqlite3.connect(Path(workdir) / "data" / "art-ai.db")
            connection.execute(
                "INSERT INTO agent_configuration_versions SELECT revision + 1, configuration_json,"
                " 'system', 'publish', 't' FROM agent_configuration_versions"
                " ORDER BY revision DESC LIMIT 1")
            connection.commit()
            connection.close()
        self.assertEqual(self.run_drill(startup=startup), 1)
        self.assertIn("仍启用 observe_task、request_consent", self.report.read_text(encoding="utf-8"))

    def test_a_copy_without_the_configuration_table_fails_the_check(self):
        tables = tuple(t for t in self.drill.SMOKE_TABLES if t != "agent_configuration_versions")
        self.source.unlink()
        make_database(self.source, tables=tables)
        self.assertEqual(self.run_drill(startup=lambda image, workdir, port: None), 1)
        self.assertIn("早于 0041", self.report.read_text(encoding="utf-8"))

    def test_the_source_is_never_modified(self):
        before = hashlib.sha256(self.source.read_bytes()).hexdigest()
        self.run_drill()
        self.assertEqual(hashlib.sha256(self.source.read_bytes()).hexdigest(), before)
        self.assertEqual(sorted(entry.name for entry in self.root.iterdir() if entry.name.startswith("art-ai.db-")), [])


if __name__ == "__main__":
    unittest.main()