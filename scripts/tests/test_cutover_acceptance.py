"""Exercise the real cutover scripts with isolated files and process-only mocks."""
import hashlib
from contextlib import closing
import json
import os
from pathlib import Path
import shutil
import sqlite3
import importlib.util
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SHA = "0123456789012345678901234567890123456789"
DOMAINS = ["users", "asset_registry", "usage_ledger", "balance_snapshots", "chat_sessions", "chat_messages", "task_cache", "task_input_snapshot", "audit_logs", "canvas_projects", "canvas_nodes", "generation_tasks", "task_outputs"]

MOCK = r'''
import json,os,pathlib,shutil,sys
args=sys.argv[1:]; tool=pathlib.Path(sys.argv[0]).name
root=pathlib.Path(os.environ["FIXTURE_ROOT"]); mode=os.environ.get("FIXTURE_MODE","")
with (root/"calls.jsonl").open("a") as out: out.write(json.dumps({"tool":tool,"args":args,"bind_ip":os.environ.get("GO_WEB_BIND_IP"),"host_port":os.environ.get("GO_WEB_HOST_PORT")})+"\n")
def value(flag): return args[args.index(flag)+1]
if tool=="hostname": print("fixture-host"); sys.exit()
if tool=="curl":
    from urllib.parse import urlsplit
    url=urlsplit(args[-1])
    if url.port==int(os.environ.get("GO_WEB_HOST_PORT","0")):
        expected=os.environ.get("FIXTURE_HEALTH_HOST","127.0.0.1")
        if url.hostname!=expected: sys.exit(71)
    elif url.hostname!="127.0.0.1": sys.exit(72)
    sys.exit()
if tool=="mv":
    paths=[a for a in args if a!="--"]
    if mode=="partial-archive" and paths[0].endswith("production/storage-go") and "/previous/" in paths[1]: sys.exit(49)
    if mode=="partial-promote" and "/cutover-staging/" in paths[0] and paths[1].endswith("production/storage-go"): sys.exit(50)
    shutil.move(*paths)
    if paths[1].endswith("production/migration-ready.txt") and "/cutover-staging/" in paths[0]:
        marker=pathlib.Path(paths[1])
        if mode=="unknown-ready-field": marker.write_text(marker.read_text()+"unknown=value\n")
        if mode=="legacy-ready": marker.write_text("\n".join(line for line in marker.read_text().splitlines() if not line.startswith("manifest_"))+"\n")
        if mode in ["tamper-manifest","forged-manifest"]:
            import hashlib
            manifest=next((marker.parent/"reports").glob("*.manifest.json"))
            doc=json.loads(manifest.read_text()); doc["domains"][0]["source_count"]=99
            manifest.write_text(json.dumps(doc))
            if mode=="forged-manifest":
                lines=marker.read_text().splitlines()
                marker.write_text("\n".join("manifest_sha256="+hashlib.sha256(manifest.read_bytes()).hexdigest() if line.startswith("manifest_sha256=") else line for line in lines)+"\n")
    sys.exit()
if tool=="acceptance": sys.exit(43 if mode=="acceptance-failure" else 0)
if tool=="backup":
    if args[0]=="verify": sys.exit()
    dest=pathlib.Path(value("--destination")); (dest/"storage/data").mkdir(parents=True)
    # The real backup contract is CLI online backup plus DELETE normalization;
    # copying the active main file would omit WAL and preserve another writer's
    # header version when Python and the SQLite CLI differ.
    import subprocess
    subprocess.run(["sqlite3","-readonly",value("--legacy-database"),".backup prod.db"],cwd=dest/"storage/data",check=True)
    subprocess.run(["sqlite3",str(dest/"storage/data/prod.db"),"PRAGMA journal_mode=DELETE;"],check=True,stdout=subprocess.PIPE)
    if mode=="source-changed":
        import sqlite3
        with sqlite3.connect(dest/"storage/data/prod.db") as db: db.execute("INSERT INTO fixture VALUES (1)")
    sys.exit()
if tool!="docker": raise RuntimeError(tool)
if args[0]=="info": print("fixture-daemon"); sys.exit()
if args[:2]==["container","inspect"]:
    # The old immutable ID remains inspectable after its name is reused.
    if args[-1] not in ["formal-legacy","legacy-id","replacement-id"]: sys.exit(1)
    replacement=(root/"replacement").exists() and args[-1] in ["formal-legacy","replacement-id"]
    if mode=="missing-container": sys.exit(1)
    if "--format" not in args: sys.exit()
    fmt=value("--format")
    if "compose.project" in fmt: print("com.docker.compose.project="+("other-project" if mode=="wrong-project" else "formal-next"))
    elif "compose.service" in fmt: print("com.docker.compose.service="+("other-service" if mode=="wrong-service" else "next-service"))
    elif "State.Running" in fmt: print("true" if replacement and not (root/"replacement-stopped").exists() else ("false" if (root/"stopped").exists() else "true"))
    elif ".Name" in fmt: print("/wrong-legacy" if mode=="wrong-name" else "/formal-legacy")
    elif ".Id" in fmt: print("replacement-id" if replacement else "legacy-id")
    sys.exit()
if args[0]=="stop":
    (root/("replacement-stopped" if args[1]=="replacement-id" else "stopped")).touch(); sys.exit()
if args[0]=="start":
    if args[1] in ["formal-legacy","replacement-id"] and (root/"replacement").exists(): (root/"wrong-started").touch()
    else: (root/"stopped").unlink(missing_ok=True)
    sys.exit()
if args[:2]==["image","inspect"]:
    print("fixture-release"+("|"+os.environ["FIXTURE_SHA"] if "revision" in value("--format") else "")); sys.exit()
if args[0]=="compose":
    if "stop" in args and value("-p")=="formal-go" and mode in ["rollback-stop-failure", "direct-stop-failure"]: sys.exit(55)
    if "--images" in args: print("art-ai-go-production:fixture-release")
    if "up" in args and value("-p")=="formal-go" and mode in ["startup-failure", "direct-startup-failure", "direct-stop-failure"]: sys.exit(41)
    sys.exit()
if args[0]=="run":
    if "rebind-source-loss-acknowledgement" in args: sys.exit(37)
    mounts={}
    for i,a in enumerate(args):
        if a=="--volume":
            host,container,*_=args[i+1].split(":"); mounts[container]=pathlib.Path(host)
    (mounts["/target-data"]/"art-ai.db").write_bytes(b"new-db")
    report=mounts["/reports"]/pathlib.Path(value("--report")).name
    report.write_text("对账结论: 通过\n")
    manifest=json.loads((root/"manifest.json").read_text())
    if mode=="bad-manifest": manifest["overall"]["conclusion"]="fail"
    if mode=="wrong-baseline": manifest["domains"][0]["source_count"]=1
    pathlib.Path(str(report)+".manifest.json").write_text(json.dumps(manifest))
    if mode=="etl-failure": sys.exit(37)
    if mode=="replace-after-etl": (root/"replacement").touch()
    if mode=="legacy-restarted": (root/"stopped").unlink(missing_ok=True)
    if "--source-loss-template" in args: (mounts["/reports"]/pathlib.Path(value("--source-loss-template")).name).write_text("{}")
    sys.exit()
raise RuntimeError(args)
'''


class CutoverAcceptance(unittest.TestCase):
    def setUp(self):
        if shutil.which("jq") is None:
            self.skipTest("jq is required by the real ETL gate")
        self.tmp = tempfile.TemporaryDirectory(prefix="artai-cutover-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for tool in ["docker", "curl", "backup", "acceptance", "mv", "hostname"]:
            path = self.bin / tool
            path.write_text(f"#!{sys.executable}\n" + MOCK)
            path.chmod(0o755)
        (self.bin / "acceptance").write_text("#!/usr/bin/env bash\nprintf '%s\\n' '{\"tool\":\"acceptance\",\"args\":[]}' >>\"$FIXTURE_ROOT/calls.jsonl\"\n[[ \"$FIXTURE_MODE\" != acceptance-failure && \"$FIXTURE_MODE\" != rollback-stop-failure ]]\n")
        self.legacy = self.root / "legacy"
        for directory in ["assets", "cache", "audit", "generated", "thumbnails", "task-inputs", "data"]:
            (self.legacy / "storage" / directory).mkdir(parents=True)
        with closing(sqlite3.connect(self.legacy / "storage/data/prod.db")) as database:
            database.execute("CREATE TABLE fixture (id INTEGER)")
            database.commit()

        self.go = self.root / "go"
        self.go.mkdir()
        self.prod = self.go / "production"
        for item in ["data", "storage-go", "reports", "web-data"]:
            (self.prod / item).mkdir(parents=True)
            (self.prod / item / "old").write_text(item)
        (self.prod / "migration-ready.txt").write_text("old-marker")
        self.original = self.tree(self.prod)
        for name, body in {"runtime.env": "", "approved.json": "{}", "baseline.json": json.dumps(dict.fromkeys(["users", "generations", "usage_tasks", "usage_credits", "audit_minimum", "output_assets", "input_assets", "chat_sessions", "chat_messages", "canvas_projects", "canvas_nodes"], 0) | {"external_webhook_url": "https://fixture-webhook.invalid"})}.items():
            path = self.root / name
            path.write_text(body)
            path.chmod(0o600)
        if os.getuid() == 0:
            os.chown(self.root / "approved.json", 100, 101)
        manifest = {"format": "art-ai-etl-completeness-manifest-v1", "overall": {"conclusion": "pass", "failure_count": 0}, "domains": [{"domain": n, "status": "pass", "source_count": 0, "loaded_count": 0, "imported_count": 0, "target_count": 0, "skipped_count": 0, "read_gap": 0, "unexplained_gap": 0} for n in DOMAINS], "credits": {"equal": True, "source_sum": 0, "loaded_sum": 0, "target_sum": 0}, "cas": {"sha256_mismatch": 0}, "source_loss": {"unacknowledged": 0}}
        (self.root / "manifest.json").write_text(json.dumps(manifest))
        spec = importlib.util.spec_from_file_location("source_check", ROOT / "scripts/verify-cutover-source.py")
        source_check = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(source_check)
        self.source_check = source_check
        source_baseline = self.root / "source-baseline.json"
        source_baseline.write_text(json.dumps({"format":"art-ai-production-source-baseline-v1", "database_snapshot_sha256":source_check.snapshot_sha256(self.legacy / "storage/data/prod.db"), "inventory_sha256":"a"*64}))
        source_baseline.chmod(0o600)
        self.env = os.environ.copy()
        self.env.update(PATH=str(self.bin)+os.pathsep+os.environ["PATH"], FIXTURE_ROOT=str(self.root), FIXTURE_SHA=SHA, FINALIZE_RUN_ID="fixture", GO_BACKUP_SCRIPT=str(self.bin / "backup"), GO_ACCEPTANCE_SCRIPT=str(self.bin / "acceptance"),
                        # #334: the migration tools need the protected paths stated; none here.
                        ART_AI_PROTECTED_PATHS="")
        for key in ["GO_ETL_SCRIPT", "GO_CUTOVER_SCRIPT", "GO_PROD_ROOT", "GO_PROD_PROJECT", "GO_IMAGE_NAME", "GO_PROD_DB_DIR", "GO_PROD_STORAGE_DIR", "GO_PROD_WEB_DATA_DIR", "GO_PREFLIGHT_PROJECT", "DOCKER_HOST", "DOCKER_CONTEXT"]:
            self.env.pop(key, None)
        self.args = ["--env-file", str(self.root / "runtime.env"), "--image-tag", "fixture-release", "--source-commit", SHA, "--approved-source-loss-acknowledgement", str(self.root / "approved.json"), "--legacy-root", str(self.legacy), "--legacy-database", str(self.legacy / "storage/data/prod.db"), "--legacy-container", "formal-legacy", "--legacy-compose-project", "formal-next", "--legacy-compose-service", "next-service", "--go-root", str(self.go), "--go-project", "formal-go", "--acceptance-base-url", "https://fixture-entry.invalid", "--acceptance-baseline", str(self.root / "baseline.json"), "--minimum-free-kib", "1"]
        self.args += ["--expected-hostname", "fixture-host", "--expected-docker-id", "fixture-daemon", "--source-baseline", str(self.root / "source-baseline.json"), "--go-bind-ip", "127.0.0.1", "--go-host-port", "24101", "--preflight-port", "24102"]
        for kind in ["assets", "cache", "audit"]:
            self.args += ["--legacy-"+kind, str(self.legacy / "storage" / kind)]

    @staticmethod
    def tree(root):
        return {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file()}

    def run_finalize(self, mode="", args=None):
        self.env["FIXTURE_MODE"] = mode
        result = subprocess.run(["bash", str(ROOT / "scripts/finalize-go-production.sh"), *(self.args if args is None else args)], env=self.env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        return result

    def calls(self):
        path = self.root / "calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_runner_ready_marker_is_accepted_by_actual_takeover(self):
        result = self.run_finalize()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual((self.prod / "data/art-ai.db").read_bytes(), b"new-db")
        marker = (self.prod / "migration-ready.txt").read_text()
        self.assertIn("manifest_sha256=", marker)
        for call in self.calls():
            self.assertNotIn("13333", " ".join(call["args"]))
            self.assertNotIn("13341", " ".join(call["args"]))
        self.assertTrue((self.root / "stopped").exists())
        self.assertFalse(any(c["args"][:1] == ["start"] for c in self.calls()))

    def test_explicit_ipv6_binding_is_formatted_for_compose(self):
        args = self.args.copy(); args[args.index("--go-bind-ip")+1] = "::1"
        self.env["FIXTURE_HEALTH_HOST"] = "::1"
        result = self.run_finalize(args=args)
        self.assertEqual(result.returncode, 0, result.stdout)
        actual = [c for c in self.calls() if c["tool"] == "docker" and "up" in c["args"] and "formal-go" in c["args"]]
        self.assertEqual(len(actual), 1)
        self.assertEqual(actual[0]["bind_ip"], "[::1]")
        self.assertEqual(actual[0]["host_port"], "24101")
        self.assertTrue(any(c["tool"] == "curl" and c["args"][-1] == "http://[::1]:24101/readyz" for c in self.calls()))

    def test_specific_ipv4_binding_probes_its_actual_address(self):
        args = self.args.copy(); args[args.index("--go-bind-ip")+1] = "192.0.2.25"
        self.env["FIXTURE_HEALTH_HOST"] = "192.0.2.25"
        result = self.run_finalize(args=args)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertTrue(any(c["tool"] == "curl" and c["args"][-1] == "http://192.0.2.25:24101/readyz" for c in self.calls()))

    def test_ipv6_wildcard_probes_ipv6_loopback(self):
        args = self.args.copy(); args[args.index("--go-bind-ip")+1] = "::"
        self.env["FIXTURE_HEALTH_HOST"] = "::1"
        result = self.run_finalize(args=args)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertTrue(any(c["tool"] == "curl" and c["args"][-1] == "http://[::1]:24101/readyz" for c in self.calls()))

    def test_ipv4_wildcard_probes_ipv4_loopback(self):
        args = self.args.copy(); args[args.index("--go-bind-ip")+1] = "0.0.0.0"
        result = self.run_finalize(args=args)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertTrue(any(c["tool"] == "curl" and c["args"][-1] == "http://127.0.0.1:24101/readyz" for c in self.calls()))

    def test_restarted_original_cannot_be_stopped_again_by_formal_child(self):
        result = self.run_finalize("legacy-restarted")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("不再保持停止", result.stdout)
        actions = [c["args"] for c in self.calls() if c["tool"] == "docker" and c["args"][0] in ["start", "stop"]]
        self.assertEqual(actions, [["stop", "legacy-id"], ["start", "legacy-id"]])
        self.assertEqual(self.tree(self.prod), self.original)
        self.assertFalse(any(c["tool"] == "docker" and "up" in c["args"] and "formal-go" in c["args"] for c in self.calls()))

    def direct_takeover_failure(self, mode):
        self.assertEqual(self.run_finalize().returncode, 0)
        (self.root / "calls.jsonl").unlink()
        (self.root / "stopped").unlink()
        env = self.env.copy()
        env.update(FIXTURE_MODE=mode, GO_PROD_ROOT=str(self.prod), GO_PROD_PROJECT="formal-go",
            GO_PROD_DB_DIR=str(self.prod / "data"), GO_PROD_STORAGE_DIR=str(self.prod / "storage-go"), GO_PROD_WEB_DATA_DIR=str(self.prod / "web-data"),
            CUTOVER_LEGACY_CONTAINER="formal-legacy", CUTOVER_LEGACY_PROJECT="formal-next", CUTOVER_LEGACY_SERVICE="next-service",
            GO_WEB_BIND_IP="127.0.0.1", GO_WEB_HOST_PORT="24101", CUTOVER_PREFLIGHT_PORT="24102")
        for key, directory in [("GO_LEGACY_GENERATED_IMAGES_DIR","generated"),("GO_LEGACY_ASSET_CACHE_DIR","cache"),("GO_LEGACY_THUMBNAIL_CACHE_DIR","thumbnails"),("GO_LEGACY_INPUT_ASSETS_DIR","assets")]:
            env[key] = str(self.legacy / "storage" / directory)
        return subprocess.run(["bash", str(ROOT / "scripts/cutover-go-production.sh"), "takeover", "--env-file", str(self.root / "runtime.env"), "--image-tag", "fixture-release", "--source-commit", SHA, "--no-build"], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    def test_cutover_names_no_environment_project_or_legacy_container(self):
        # #261: the repository names no environment's Compose project or old container.
        source = (ROOT / "scripts/cutover-go-production.sh").read_text()
        self.assertNotRegex(source, r'(GO_PROD_PROJECT|CUTOVER_LEGACY_[A-Z]+):-[A-Za-z]')
        base = self.env.copy()
        base.update(GO_PROD_ROOT=str(self.prod), GO_PROD_PROJECT="formal-go",
            CUTOVER_LEGACY_CONTAINER="formal-legacy", CUTOVER_LEGACY_PROJECT="formal-next", CUTOVER_LEGACY_SERVICE="next-service")
        for missing in ["GO_PROD_PROJECT", "CUTOVER_LEGACY_CONTAINER", "CUTOVER_LEGACY_PROJECT", "CUTOVER_LEGACY_SERVICE"]:
            with self.subTest(missing=missing):
                env = {key: value for key, value in base.items() if key != missing}
                result = subprocess.run(["bash", str(ROOT / "scripts/cutover-go-production.sh"), "takeover", "--env-file", str(self.root / "runtime.env"), "--image-tag", "fixture-release", "--source-commit", SHA, "--no-build"], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn(missing, result.stdout)
                self.assertFalse((self.root / "calls.jsonl").exists() and "docker" in (self.root / "calls.jsonl").read_text())

    def test_direct_takeover_restores_original_id_after_startup_failure(self):
        result = self.direct_takeover_failure("direct-startup-failure")
        self.assertNotEqual(result.returncode, 0)
        actions = [c["args"] for c in self.calls() if c["tool"] == "docker" and c["args"][0] in ["start", "stop"]]
        self.assertEqual(actions, [["stop", "legacy-id"], ["start", "legacy-id"]])
        self.assertFalse((self.root / "stopped").exists())

    def test_direct_takeover_does_not_restore_legacy_when_go_stop_fails(self):
        result = self.direct_takeover_failure("direct-stop-failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("回滚未完成", result.stdout)
        self.assertTrue((self.root / "stopped").exists())
        self.assertFalse(any(c["tool"] == "docker" and c["args"][0] == "start" for c in self.calls()))

    def test_name_replacement_after_etl_cannot_rebind_or_stop_replacement(self):
        result = self.run_finalize("replace-after-etl")
        self.assertNotEqual(result.returncode, 0, "formal child rebound a replaced container name and reported success")
        actions = [c["args"] for c in self.calls() if c["tool"] == "docker" and c["args"][0] in ["start", "stop"]]
        self.assertEqual(actions, [["stop", "legacy-id"], ["start", "legacy-id"]])
        self.assertFalse((self.root / "replacement-stopped").exists())
        self.assertFalse((self.root / "wrong-started").exists())
        self.assertEqual(self.tree(self.prod), self.original)
        self.assertFalse(any(c["tool"] == "docker" and "up" in c["args"] and "formal-go" in c["args"] for c in self.calls()))

    def test_partial_archive_restores_every_old_item(self):
        result = self.run_finalize("partial-archive")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.tree(self.prod), self.original, result.stdout)
        self.assertFalse((self.root / "stopped").exists())

    def test_partial_promotion_restores_old_and_keeps_both_new_fragments(self):
        result = self.run_finalize("partial-promote")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.tree(self.prod), self.original, result.stdout)
        self.assertFalse((self.root / "stopped").exists())
        self.assertEqual((self.go / "production-archives/fixture/failed-new/data/art-ai.db").read_bytes(), b"new-db")
        self.assertTrue((self.go / "cutover-staging/fixture/storage-go").is_dir())

    def test_etl_failure_keeps_old_data_and_restores_old_service(self):
        result = self.run_finalize("etl-failure")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.tree(self.prod), self.original, result.stdout)
        self.assertFalse((self.root / "stopped").exists())
        self.assertTrue((self.go / "cutover-staging/fixture/data/art-ai.db").exists())
        self.assertFalse(any(c["args"][:1] == ["compose"] and "up" in c["args"] and "formal-go" in c["args"] for c in self.calls()))

    def assert_promoted_failure(self, mode):
        # Separate instances prevent an earlier transaction's archive reuse.
        result = self.run_finalize(mode)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.tree(self.prod), self.original, result.stdout)
        self.assertFalse((self.root / "stopped").exists())
        self.assertEqual((self.go / "production-archives/fixture/failed-new/data/art-ai.db").read_bytes(), b"new-db")
        self.assertTrue(any(c["tool"] == "docker" and "up" in c["args"] and "formal-go" in c["args"] for c in self.calls()))

    def test_startup_failure_archives_new_and_restores_old(self):
        self.assert_promoted_failure("startup-failure")

    def test_acceptance_failure_archives_new_and_restores_old(self):
        self.assert_promoted_failure("acceptance-failure")
        self.assertTrue(any(c["tool"] == "acceptance" for c in self.calls()))

    def test_missing_identity_source_or_network_flags_refuse_before_commands(self):
        for flag in ["--expected-hostname", "--expected-docker-id", "--source-baseline", "--go-bind-ip", "--go-host-port", "--preflight-port"]:
            with self.subTest(flag=flag):
                args = self.args.copy(); index = args.index(flag); del args[index:index+2]
                result = self.run_finalize(args=args)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.calls(), [])


    def assert_no_mutations(self):
        for call in self.calls():
            self.assertNotIn(call["tool"], ["mv", "backup", "acceptance"])
            if call["tool"] == "docker":
                self.assertNotIn(call["args"][0], ["stop", "start", "run", "compose"])
        self.assertEqual(self.tree(self.prod), self.original)

    def test_wrong_host_daemon_project_and_service_refuse_before_stop(self):
        for mode, flag, value in [
            ("", "--expected-hostname", "different-host"),
            ("", "--expected-docker-id", "different-daemon"),
            ("wrong-project", None, None), ("wrong-service", None, None),
            ("missing-container", None, None), ("wrong-name", None, None),
        ]:
            with self.subTest(mode=mode, flag=flag):
                (self.root / "calls.jsonl").unlink(missing_ok=True)
                args = self.args.copy()
                if flag: args[args.index(flag)+1] = value
                result = self.run_finalize(mode, args)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assert_no_mutations()

    def test_invalid_network_and_project_parameters_refuse_before_commands(self):
        for flag, value in [("--go-bind-ip", "999.1.1.1"), ("--go-host-port", "65536"), ("--preflight-port", "0"), ("--preflight-port", "24101"), ("--go-project", "formal-next"), ("--legacy-compose-project", "formal-go_preflight")]:
            with self.subTest(flag=flag, value=value):
                args = self.args.copy(); args[args.index(flag)+1] = value
                result = self.run_finalize(args=args)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(self.calls(), [])

    def test_wrong_source_baseline_refuses_before_stop(self):
        path = self.root / "source-baseline.json"
        baseline = json.loads(path.read_text()); baseline["database_snapshot_sha256"] = "b"*64
        path.write_text(json.dumps(baseline))
        result = self.run_finalize()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("来源基线", result.stdout)
        self.assert_no_mutations()

    def test_source_changes_before_final_snapshot_restore_without_running_etl(self):
        result = self.run_finalize("source-changed")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("最终停写快照", result.stdout)
        self.assertEqual(self.tree(self.prod), self.original)
        self.assertFalse((self.root / "stopped").exists())
        self.assertFalse(any(c["tool"] == "docker" and c["args"][0] == "run" for c in self.calls()))

    def test_failed_go_stop_preserves_both_data_sets_and_never_restarts_legacy(self):
        result = self.run_finalize("rollback-stop-failure")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("回滚未完成", result.stdout)
        self.assertEqual((self.prod / "data/art-ai.db").read_bytes(), b"new-db")
        self.assertEqual(self.tree(self.go / "production-archives/fixture/previous"), self.original)
        self.assertTrue((self.root / "stopped").exists())
        self.assertFalse(any(c["tool"] == "docker" and c["args"][0] == "start" for c in self.calls()))

    def assert_takeover_rejects(self, mode, message):
        result = self.run_finalize(mode)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stdout)
        self.assertEqual(self.tree(self.prod), self.original)
        self.assertFalse((self.root / "stopped").exists())

    def test_unknown_ready_field_is_not_ignored(self):
        self.assert_takeover_rejects("unknown-ready-field", "就绪证明格式无效")

    def test_legacy_marker_without_manifest_is_not_accepted(self):
        self.assert_takeover_rejects("legacy-ready", "就绪证明格式无效")

    def test_changed_manifest_is_not_accepted(self):
        self.assert_takeover_rejects("tamper-manifest", "manifest 与就绪证明不一致")

    def test_matching_digest_cannot_hide_failed_manifest_counts(self):
        self.assert_takeover_rejects("forged-manifest", "manifest 未通过")

    def test_bad_manifest_cannot_promote(self):
        result = self.run_finalize("bad-manifest")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.tree(self.prod), self.original)
        self.assertFalse((self.root / "stopped").exists())
        self.assertFalse((self.go / "production-archives/fixture/previous").exists())


class AcceptanceOriginContract(unittest.TestCase):
    def test_formal_wrapper_and_actual_acceptance_share_origin_bounds(self):
        for script, name in [("scripts/finalize-go-production.sh", "finalize_validate_http_origin"), ("scripts/post-cutover-acceptance.sh", "validate_http_origin")]:
            body = (ROOT / script).read_text()
            function = name + "() {" + body.split(name + "() {", 1)[1].split("\n}\n", 1)[0] + "\n}\n"
            # Execute the real shell function without sourcing credentials or
            # starting the surrounding acceptance transaction/network checks.
            command = "finalize_fail() { exit 1; }\n" + function + name + ' origin "$1"'
            for url, accepted in [("https://fixture.invalid", True), ("http://127.0.0.1:24101", True), ("https://fixture.invalid:", False), ("https://fixture.invalid:65536", False), ("https://user@fixture.invalid", False), ("https://fixture.invalid/path", False), ("https://fixture.invalid?q=1", False)]:
                with self.subTest(script=script, url=url):
                    result = subprocess.run(["bash", "-c", command, "origin-check", url], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                    self.assertEqual(result.returncode == 0, accepted)


class SourceBaselineSnapshot(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="artai-source-proof-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        spec = importlib.util.spec_from_file_location("source_check", ROOT / "scripts/verify-cutover-source.py")
        self.check = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.check)
        self.path = self.root / "source.db"
        self.source = sqlite3.connect(self.path)
        self.addCleanup(self.source.close)
        self.source.execute("PRAGMA journal_mode=WAL")
        self.source.execute("PRAGMA wal_autocheckpoint=0")
        self.source.execute("CREATE TABLE entries (id INTEGER)")
        self.source.commit()

    def baseline(self):
        path = self.root / "baseline.json"
        path.write_text(json.dumps({"format":"art-ai-production-source-baseline-v1", "database_snapshot_sha256":self.check.snapshot_sha256(self.path), "inventory_sha256":"a"*64}))
        path.chmod(0o600)
        return path

    def test_wal_commit_changes_consistent_proof_while_main_file_hash_stays_same(self):
        baseline = self.baseline()
        main_hash = hashlib.sha256(self.path.read_bytes()).hexdigest()
        self.check.verify(self.path, baseline)
        self.source.execute("INSERT INTO entries VALUES (1)")
        self.source.commit()
        self.assertEqual(hashlib.sha256(self.path.read_bytes()).hexdigest(), main_hash)
        with self.assertRaisesRegex(ValueError, "differs"):
            self.check.verify(self.path, baseline)
        current = self.baseline()
        self.check.verify(self.path, current)
        self.assertEqual(hashlib.sha256(self.path.read_bytes()).hexdigest(), main_hash)
        self.assertEqual(self.source.execute("SELECT COUNT(*) FROM entries").fetchone()[0], 1)

    def test_snapshot_digest_is_stable_and_matches_existing_sqlite_backup_protocol(self):
        first = self.check.snapshot_sha256(self.path)
        self.assertEqual(first, self.check.snapshot_sha256(self.path))
        sqlite = shutil.which("sqlite3")
        if sqlite is None: self.skipTest("SQLite CLI required for backup protocol comparison")
        backup = self.root / "existing-protocol.db"
        subprocess.run([sqlite, str(self.path), ".backup '"+str(backup)+"'"], check=True)
        subprocess.run([sqlite, str(backup), "PRAGMA journal_mode=DELETE;"], check=True, stdout=subprocess.PIPE)
        self.assertEqual(first, hashlib.sha256(backup.read_bytes()).hexdigest())
        baseline = self.baseline()
        self.check.verify(backup, baseline, snapshot=True)
        # Re-backing a WAL-normalized database can change SQLite header fields;
        # final backup evidence must be verified as the existing isolated bytes.
        self.assertEqual(first, hashlib.sha256(backup.read_bytes()).hexdigest())

    def test_invalid_permissions_duplicates_and_corrupt_database_fail_closed(self):
        baseline = self.baseline()
        baseline.chmod(0o644)
        with self.assertRaises(ValueError): self.check.verify(self.path, baseline)
        baseline.chmod(0o600)
        baseline.write_text('{"format":"x","format":"art-ai-production-source-baseline-v1"}')
        with self.assertRaises(ValueError): self.check.verify(self.path, baseline)
        self.baseline()
        corrupt = self.root / "corrupt.db"; corrupt.write_bytes(b"not a SQLite database")
        with self.assertRaises(ValueError): self.check.verify(corrupt, baseline)

    def test_quoted_source_path_and_snapshot_header_tampering(self):
        quoted = self.root / "source ' quoted.db"
        with closing(sqlite3.connect(quoted)) as target:
            self.source.backup(target)
            target.execute("PRAGMA journal_mode=DELETE")
        digest = self.check.snapshot_sha256(quoted)
        baseline = self.root / "quoted-baseline.json"
        baseline.write_text(json.dumps({"format":"art-ai-production-source-baseline-v1", "database_snapshot_sha256":digest, "inventory_sha256":"a"*64}))
        baseline.chmod(0o600)
        self.check.verify(quoted, baseline)
        final = self.root / "final.db"
        subprocess.run(["sqlite3", "-readonly", str(quoted), ".backup final.db"], cwd=self.root, check=True)
        subprocess.run(["sqlite3", str(final), "PRAGMA journal_mode=DELETE;"], check=True, stdout=subprocess.PIPE)
        self.check.verify(final, baseline, snapshot=True)
        # SQLite's writer-version field does not affect rows or integrity, but
        # changing it must still invalidate the full-byte baseline binding.
        data = bytearray(final.read_bytes())
        data[99] ^= 1
        final.write_bytes(data)
        with self.assertRaisesRegex(ValueError, "differs"):
            self.check.verify(final, baseline, snapshot=True)

    def test_sqlite_cli_failure_timeout_and_missing_executable_fail_closed(self):
        with mock.patch.object(self.check.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, b"", b"private database detail")):
            with self.assertRaisesRegex(ValueError, "^consistent source snapshot failed$"):
                self.check.snapshot_sha256(self.path)
        with mock.patch.object(self.check.subprocess, "run", side_effect=subprocess.TimeoutExpired("sqlite3", 60)):
            with self.assertRaisesRegex(ValueError, "timed out"):
                self.check.snapshot_sha256(self.path)
        with mock.patch.object(self.check.subprocess, "run", side_effect=FileNotFoundError("sqlite3")):
            with self.assertRaises(FileNotFoundError):
                self.check.snapshot_sha256(self.path)

    def test_snapshot_waits_for_short_source_lock_without_changing_source(self):
        locked = self.root / "locked.db"
        with closing(sqlite3.connect(locked)) as connection:
            connection.execute("CREATE TABLE entries (id INTEGER)")
            connection.commit()
        original = locked.read_bytes()
        ready = threading.Event()

        def hold_lock():
            with closing(sqlite3.connect(locked)) as connection:
                connection.execute("BEGIN EXCLUSIVE")
                ready.set()
                time.sleep(0.25)
                connection.rollback()

        worker = threading.Thread(target=hold_lock)
        worker.start()
        try:
            self.assertTrue(ready.wait(5), "fixture source lock not acquired")
            self.assertRegex(self.check.snapshot_sha256(locked), "^[0-9a-f]{64}$")
        finally:
            worker.join()
        self.assertEqual(locked.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()

