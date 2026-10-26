#!/usr/bin/env python3
"""Offline contract checks for the controlled acceptance runner (no server)."""
import argparse
from contextlib import closing
import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("controlled_acceptance", Path(__file__).with_name("run-controlled-acceptance.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ControlledAcceptanceTests(unittest.TestCase):
    def test_plan_identity_matches_owner_and_key_contract(self):
        expected = hashlib.sha256(b"owner:paid-step-identity").hexdigest()
        self.assertEqual(runner.plan_id("owner", "paid-step-identity"), expected)
        self.assertNotEqual(runner.plan_id("other", "paid-step-identity"), expected)

    def test_blob_hash_is_typed_and_never_serializes_original_payload(self):
        value = {"opaque": b"private-checkpoint"}
        self.assertEqual(runner.stable_digest(value), runner.stable_digest(value))
        self.assertNotEqual(runner.stable_digest(value), runner.stable_digest({"opaque": "private-checkpoint"}))
        self.assertNotEqual(runner.stable_digest(value), runner.stable_digest({"opaque": b"different"}))

    def test_private_json_write_is_atomic_and_private(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "manifest.json"
            runner.write_private(target, {"scenarios": []})
            self.assertEqual(json.loads(target.read_text()), {"scenarios": []})
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
            self.assertFalse(target.with_name("manifest.json.tmp").exists())

    def test_fresh_scope_refuses_duplicate_scenario(self):
        with tempfile.TemporaryDirectory() as directory:
            subject = object.__new__(runner.Runner)
            subject.manifest = {"scenarios": [{"id": "original"}]}
            subject.manifest_path = Path(directory) / "manifest.json"
            with self.assertRaises(runner.AcceptanceError):
                subject.register({"id": "original"})
            self.assertEqual(subject.manifest["scenarios"], [{"id": "original"}])
            self.assertFalse(subject.manifest_path.exists())

    def test_sqlite_reader_is_read_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with closing(sqlite3.connect(root / "acceptance.db")) as db, db:
                db.execute("CREATE TABLE example (id TEXT)")
            subject = object.__new__(runner.Runner)
            subject.root = root
            with closing(subject.db()) as db:
                with self.assertRaises(sqlite3.OperationalError):
                    db.execute("INSERT INTO example VALUES ('mutation')")

    def test_grant_decision_uses_real_put_method(self):
        # Exercise the human-consent helper through its HTTP port rather than
        # asserting a literal in the implementation source.
        subject = object.__new__(runner.Runner)
        subject.routes = {"task_grants": "/grants", "task_grant_decision": "/grants/{requestID}", "processing_plan_resume": "/plans/{planID}/resume"}
        calls = []
        class Client:
            def request(self, method, path, body, statuses=(200,), key=None):
                calls.append((method, path))
                return 201, {"request_id": "grant", "status": "pending"}
        subject.client = Client()
        subject.approve_plan({"id": "plan", "version": 2, "steps": [{"failure": "plan_consent_required", "prepared_input_id": "frozen", "plan_step_id": "0:0", "idempotency_key": "same-key", "revision": "A"}]})
        self.assertEqual(calls, [("POST", "/grants"), ("PUT", "/grants/grant"), ("POST", "/plans/plan/resume")])

    def test_missing_completion_formats_come_only_from_the_completion_offering(self):
        coverage = {"steps": [
            {"offering_id": "local/static-glb/to-gltf", "formats": ["gltf"]},
            {"offering_id": "meshy/processing/remesh", "formats": ["usdz", "fbx"]},
        ]}
        self.assertEqual(
            runner.missing_completion_formats(coverage, "meshy/processing/remesh"),
            ["usdz", "fbx"])

    def test_a_fully_delivered_source_offers_no_completion_formats(self):
        # After BLEND was withdrawn a six-format source has no missing format
        # the remesh offering can provide, so the guard must not key on any
        # specific format such as USDZ.
        self.assertEqual(runner.missing_completion_formats({"steps": []}, "meshy/processing/remesh"), [])
        self.assertEqual(runner.missing_completion_formats(
            {"steps": [{"offering_id": "meshy/processing/remesh", "formats": []}]},
            "meshy/processing/remesh"), [])


class LostSourceSelectionTests(unittest.TestCase):
    def subject(self):
        subject = object.__new__(runner.Runner)
        subject.args = argparse.Namespace(only=None, source_lost_task=None)
        subject.owner = "owner"
        subject.sources = {
            "humanoid": {"id": "raw-source", "owner_id": "owner", "session_id": "session", "state": "SUCCEEDED", "capability": "text-to-3d"},
            "format": {"id": "format-source", "owner_id": "owner", "session_id": "format-session", "state": "SUCCEEDED"},
        }
        subject.report = {"scenarios": {"middle_rigging": {"plan": {"state": "SUCCEEDED", "session_id": "session", "steps": [{"state": "SUCCEEDED", "task_id": "verified-remesh"}, {"state": "SUCCEEDED", "task_id": "verified-rigging"}]}}}}
        subject.manifest = {"source_sha": "a" * 40, "scenarios": []}
        subject.offerings = {"meshy/processing/rigging": {"id": "meshy/processing/rigging", "revision": "A", "provider_id": "meshy", "capability": "rigging"}}
        reduced = {"id": "verified-remesh", "owner_id": "owner", "session_id": "session", "state": "SUCCEEDED", "capability": "remesh", "offering_json": json.dumps({"id": "meshy/processing/remesh", "provider_id": "meshy", "capability": "remesh", "parameters": [{"name": "target_polycount", "type": "number", "semantic": "polycount", "minimum": 100, "maximum": 300000}]}), "input_json": json.dumps({"target_polycount": 300000, "convert_format_only": False})}
        subject.task = Mock(return_value=reduced)
        rigged = {"id": "verified-rigging", "owner_id": "owner", "session_id": "session", "state": "SUCCEEDED", "capability": "rigging", "input_json": json.dumps({"source_task": "verified-remesh"})}
        subject.task.side_effect = lambda task_id: rigged if task_id == "verified-rigging" else subject.task.return_value
        subject.standalone_lost_source = None
        subject.source_asset = Mock(return_value={"required": True, "id": "verified-glb", "sha256": "b" * 64, "size_bytes": 12})
        subject.aggregate = Mock(return_value="source-aggregate")
        subject.register = Mock()
        subject.client = Mock()
        subject.routes = {"processing_plans": "/plans"}
        subject.wait_plan = Mock(side_effect=RuntimeError("stop after observed submission"))
        return subject

    def test_all_submits_verified_chain_remesh_instead_of_raw_humanoid(self):
        subject = self.subject()
        originals = dict(subject.sources)
        with self.assertRaisesRegex(RuntimeError, "stop after observed submission"):
            subject.lost_response()
        body = subject.client.request.call_args.args[2]
        self.assertEqual(body["source_task_id"], "verified-remesh")
        self.assertEqual(body["source_asset_id"], "verified-glb")
        self.assertEqual(subject.sources, originals)

    def test_standalone_missing_source_rejects_before_start_or_manifest_write(self):
        with unittest.mock.patch.object(runner, "write_private") as writes, \
             unittest.mock.patch.object(runner.subprocess, "Popen") as starts:
            with self.assertRaisesRegex(runner.AcceptanceError, "requires --source-lost-task"):
                runner.Runner(argparse.Namespace(only="lost-response", source_lost_task=None))
            writes.assert_not_called()
            starts.assert_not_called()

    def test_all_refuses_a_standalone_override_before_any_side_effect(self):
        with self.assertRaisesRegex(runner.AcceptanceError, "all uses its verified chain Remesh"):
            runner.Runner(argparse.Namespace(only=None, source_lost_task="override"))

    def test_all_missing_verified_chain_never_falls_back_to_raw(self):
        subject = self.subject()
        subject.report["scenarios"] = {}
        with self.assertRaises(runner.AcceptanceError):
            subject.lost_response()
        subject.register.assert_not_called()
        subject.client.request.assert_not_called()

    def test_invalid_source_scope_rejects_before_registration_or_http(self):
        for field, value in (("id", "other-task"), ("owner_id", "other-owner"),
                             ("session_id", "other-session"), ("state", "FAILED"),
                             ("capability", "text-to-3d")):
            with self.subTest(field=field):
                subject = self.subject()
                source = dict(subject.task.return_value)
                source[field] = value
                subject.task.return_value = source
                with self.assertRaises(runner.AcceptanceError):
                    subject.lost_response()
                subject.register.assert_not_called()
                subject.client.request.assert_not_called()
                subject.aggregate.assert_not_called()

    def test_frozen_offering_provider_capability_and_identity_are_verified(self):
        for field, value in (("id", "other/remesh"), ("provider_id", "tripo"),
                             ("capability", "rigging")):
            with self.subTest(field=field):
                subject = self.subject()
                source = dict(subject.task.return_value)
                offering = json.loads(source["offering_json"])
                offering[field] = value
                source["offering_json"] = json.dumps(offering)
                subject.task.return_value = source
                with self.assertRaises(runner.AcceptanceError):
                    subject.lost_response()
                subject.register.assert_not_called()
                subject.client.request.assert_not_called()

    def test_format_only_remesh_is_not_a_reduced_source(self):
        subject = self.subject()
        subject.args.only = "lost-response"
        subject.args.source_lost_task = "verified-remesh"
        subject.task.return_value["input_json"] = json.dumps({"target_polycount": 300000, "convert_format_only": True})
        with self.assertRaisesRegex(runner.AcceptanceError, "not only convert formats"):
            subject.lost_response()
        subject.register.assert_not_called()
        subject.client.request.assert_not_called()

    def test_polycount_uses_frozen_bounds_and_rejects_invalid_values(self):
        for polycount in (None, True, 99, 300001, float("nan")):
            with self.subTest(polycount=polycount):
                subject = self.subject()
                subject.task.return_value["input_json"] = json.dumps({"target_polycount": polycount})
                with self.assertRaisesRegex(runner.AcceptanceError, "frozen Offering bounds"):
                    subject.lost_response()
                subject.register.assert_not_called()
                subject.client.request.assert_not_called()
        subject = self.subject()
        offering = json.loads(subject.task.return_value["offering_json"])
        offering["parameters"][0]["maximum"] = 200000
        subject.task.return_value["offering_json"] = json.dumps(offering)
        with self.assertRaisesRegex(runner.AcceptanceError, "frozen Offering bounds"):
            subject.lost_response()
        subject.register.assert_not_called()
        subject.client.request.assert_not_called()

    def test_missing_required_glb_rejects_before_registration(self):
        subject = self.subject()
        subject.source_asset.return_value["required"] = False
        with self.assertRaises(runner.AcceptanceError):
            subject.lost_response()
        subject.register.assert_not_called()
        subject.client.request.assert_not_called()

    def test_source_bound_key_and_scenario_preserve_previous_failed_scope(self):
        identities = []
        for source_id in ("verified-remesh", "other-verified-remesh"):
            subject = self.subject()
            subject.args.only = "lost-response"
            subject.args.source_lost_task = source_id
            source = dict(subject.task.return_value)
            source["id"] = source_id
            subject.task.return_value = source
            with self.assertRaisesRegex(RuntimeError, "stop after observed submission"):
                subject.lost_response()
            scope = subject.register.call_args.args[0]
            key = subject.client.request.call_args.args[-1]
            self.assertIn(source_id, key)
            self.assertEqual(scope["id"], "lost-response:" + source_id)
            self.assertEqual(scope["plan_id"], runner.plan_id("owner", key))
            identities.append((scope["id"], scope["plan_id"], key))
        self.assertNotEqual(identities[0], identities[1])
        with tempfile.TemporaryDirectory() as directory:
            subject = self.subject()
            old = {"id": "lost-response", "plan_id": "old-failed-plan", "max_provider_starts": 1}
            subject.manifest["scenarios"] = [dict(old)]
            subject.manifest_path = Path(directory) / "manifest.json"
            marker = Path(directory) / "consumed-marker"
            marker.write_bytes(b"old fault already consumed")
            subject.register = runner.Runner.register.__get__(subject)
            with self.assertRaisesRegex(RuntimeError, "stop after observed submission"):
                subject.lost_response()
            self.assertEqual(json.loads(subject.manifest_path.read_text())["scenarios"][0], old)
            self.assertEqual(marker.read_bytes(), b"old fault already consumed")

    def test_standalone_corrupt_asset_rejects_during_read_only_construction(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            catalog = Path(__file__).resolve().parents[2] / "internal/catalog/default_catalog.json"
            (root / "catalog-a.json").write_bytes(catalog.read_bytes())
            runner.write_private(root / "manifest.json", {
                "owner_id": "owner", "source_sha": "a" * 40,
                "address": "127.0.0.1:18333", "catalog_file": str(root / "catalog-a.json"),
                "web_assets_root": str(root / "web"), "scenarios": [],
            })
            (root / "assets").mkdir()
            asset = root / "assets" / "remesh.glb"
            original = b"glTF saved source evidence"
            asset.write_bytes(original)
            with closing(sqlite3.connect(root / "acceptance.db")) as db, db:
                db.execute("CREATE TABLE generation_tasks (id TEXT, owner_id TEXT, session_id TEXT, state TEXT, capability TEXT, offering_json TEXT, input_json TEXT)")
                for task_id, session, capability in (("raw-source", "session", "text-to-3d"),
                                                      ("format-source", "format-session", "text-to-3d"),
                                                      ("verified-remesh", "session", "remesh")):
                    db.execute("INSERT INTO generation_tasks VALUES (?,?,?,?,?,?,?)", (task_id, "owner", session, "SUCCEEDED", capability,
                        json.dumps({"id": "meshy/processing/remesh", "provider_id": "meshy", "capability": capability, "parameters": [{"name": "target_polycount", "type": "number", "semantic": "polycount", "minimum": 100, "maximum": 300000}]}), json.dumps({"target_polycount": 300000})))
                db.execute("CREATE TABLE generation_attempts (id TEXT, task_id TEXT, ordinal INTEGER)")
                db.execute("CREATE TABLE generation_assets (id TEXT, task_id TEXT, role TEXT, variant TEXT, required INTEGER, ordinal INTEGER, storage_key TEXT, size_bytes INTEGER, sha256 TEXT)")
                db.execute("INSERT INTO generation_assets VALUES (?,?,?,?,?,?,?,?,?)", ("source-glb", "verified-remesh", "model", "glb", 1, 0, "remesh.glb", len(original), hashlib.sha256(original).hexdigest()))
                for table in ("generation_prepared_inputs", "execution_grants", "generation_usage_ledger"):
                    db.execute(f"CREATE TABLE {table} (id TEXT)")
            args = argparse.Namespace(root=root, binary=Path(sys.executable), only="lost-response",
                source_lost_task="verified-remesh", source_humanoid="raw-source", source_format="format-source", offline_mock=True)
            before = runner.digest(root / "acceptance.db")
            snapshot = (root / "manifest.json").read_bytes()
            valid = runner.Runner(args)
            self.assertEqual(valid.standalone_lost_source[0]["id"], "verified-remesh")
            self.assertEqual(runner.digest(root / "acceptance.db"), before)
            self.assertEqual((root / "manifest.json").read_bytes(), snapshot)
            asset.write_bytes(b"changed source contents")
            with unittest.mock.patch.object(runner, "write_private") as writes, \
                 unittest.mock.patch.object(runner.subprocess, "Popen") as starts:
                with self.assertRaisesRegex(runner.AcceptanceError, "actual asset bytes"):
                    runner.Runner(args)
                writes.assert_not_called()
                starts.assert_not_called()
            self.assertEqual(runner.digest(root / "acceptance.db"), before)
            self.assertEqual((root / "manifest.json").read_bytes(), snapshot)

    def complete_subject(self):
        subject = self.subject()
        subject.wait_plan = Mock(side_effect=[{"state": "PAUSED"}, {"state": "SUCCEEDED"}])
        subject.frozen = Mock(return_value={"prepared_input_id": "original-frozen-input"})
        subject.browser = Mock(return_value=False)
        subject.act = Mock()
        subject.counts = Mock(return_value={"generation_tasks": 4, "execution_grants": 4})
        subject.other = Mock()
        subject.get_plan = Mock(side_effect=[{"failure": "plan_submit_unreconciled", "version": 2},
                                            {"steps": [{"task_id": "recovered-rigging"}], "version": 3}])
        subject.verify_delivery = Mock()
        subject.routes["processing_plan_reconcile"] = "/plans/{planID}/reconcile"
        return subject

    def test_completed_recovery_records_source_aggregate_before_and_after(self):
        subject = self.complete_subject()
        subject.lost_response()
        evidence = subject.report["scenarios"]["lost_response_source"]
        self.assertEqual(evidence["task_id"], "verified-remesh")
        self.assertEqual(evidence["compatibility"]["successful_rigging_consumer_task_id"], "verified-rigging")
        self.assertEqual(evidence["aggregate_before_sha256"], evidence["aggregate_after_sha256"])
        self.assertEqual(subject.aggregate.call_args_list,
                         [unittest.mock.call("verified-remesh", True), unittest.mock.call("verified-remesh", True)])
        subject.verify_delivery.assert_called_once_with("recovered-rigging")
        self.assertEqual(subject.sources["humanoid"]["id"], "raw-source")
        self.assertEqual(subject.sources["format"]["id"], "format-source")

    def test_changed_source_after_recovery_is_not_recorded_as_success(self):
        subject = self.complete_subject()
        subject.aggregate.side_effect = ["original", "changed"]
        with self.assertRaisesRegex(runner.AcceptanceError, "changed its verified Remesh source"):
            subject.lost_response()
        self.assertNotIn("lost_response", subject.report["scenarios"])


if __name__ == "__main__":
    unittest.main()
