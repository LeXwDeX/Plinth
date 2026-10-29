#!/usr/bin/env python3
"""Run scoped recovery acceptance against the test-only loopback application.

Dry-run is read-only. --execute starts the source-bound test binary, exercises
normal HTTP APIs, and reads the private SQLite snapshot. Runtime credentials
are inherited in memory; sanitized evidence contains identities and hashes.
"""
import argparse
from contextlib import closing
import hashlib
import http.cookiejar
import json
import math
import os
from pathlib import Path
import shlex
import signal
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request


class AcceptanceError(RuntimeError):
    pass


FORMAT_COMPLETION_OFFERING = "meshy/processing/convert"
# Per-step input of the recovery chain. The Remesh step names a small geometry
# on purpose: the chain is paid, and 10000 triangles keeps it cheap and inside
# Rigging's source-model limit. The Schema default (quad, 300000 faces, about
# 600000 triangles) is not usable here: the Processing Plan refuses it when the
# plan is created, before any step is paid (source_model_limit and
# output_triangles, #121). The values stay explicit and may be overridden.
CHAIN_INPUTS = {"remesh": {"topology": "triangle", "target_polycount": 10000}, "animation": {"action_id": 10}}


def require(condition, message):
    if not condition:
        raise AcceptanceError(message)


def digest(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def stable_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), default=lambda item: {"type": "sqlite-blob", "size": len(item), "sha256": hashlib.sha256(item).hexdigest()} if isinstance(item, bytes) else str(item)).encode()).hexdigest()


def plan_id(owner, key):
    return hashlib.sha256((owner + ":" + key).encode()).hexdigest()


def missing_completion_formats(coverage, offering_id):
    """Formats that the completion offering can still supply for a source.

    Coverage lists a completion step only for formats the source is missing
    and the offering provides, so an empty result means there is nothing left
    to complete.
    """
    return [format_id for step in coverage.get("steps") or []
            if step.get("offering_id") == offering_id
            for format_id in step.get("formats") or []]


def write_private(path, value):
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    temporary.chmod(0o600)
    temporary.replace(path)


class Client:
    def __init__(self, base, owner):
        self.base, self.owner = base.rstrip("/"), owner
        self.cookies = http.cookiejar.CookieJar()
        self.open = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))

    def request(self, method, path, body=None, statuses=(200,), key=None):
        headers = {"Content-Type": "application/json", "Origin": self.base}
        if key:
            headers["Idempotency-Key"] = key
        request = urllib.request.Request(self.base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
        try:
            response = self.open.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            payload = response.read(8 * 1024 * 1024)
            status = response.status
        require(status in statuses, f"{method} {path}: expected {statuses}, received {status}")
        try:
            return status, json.loads(payload)
        except ValueError:
            raise AcceptanceError(f"{method} {path}: response is not JSON") from None

    def login(self, routes, other=False):
        _, result = self.request("POST", routes["auth_mock"], {"name": "Other acceptance", "email": "other@example.invalid"} if other else {})
        identity = result.get("user", {}).get("id")
        require(identity and (identity != self.owner if other else identity == self.owner), "mock login did not issue the intended independent principal")
        require(bool(list(self.cookies)), "mock login did not issue a signed browser cookie")
        return identity


class Runner:
    def __init__(self, args):
        require(args.only != "lost-response" or getattr(args, "source_lost_task", None),
                "--only lost-response requires --source-lost-task before starting the fixture")
        require(not getattr(args, "source_lost_task", None) or args.only == "lost-response",
                "--source-lost-task is only accepted with --only lost-response; all uses its verified chain Remesh")
        self.args = args
        self.root = args.root.resolve()
        require(self.root.is_dir() and self.root.stat().st_mode & 0o077 == 0, "root must be an existing private directory")
        require(not (self.root / "SEED_FAILED").exists(), "seed is incomplete")
        self.manifest_path = self.root / "manifest.json"
        self.manifest = json.loads(self.manifest_path.read_text())
        self.owner = self.manifest["owner_id"]
        require(self.manifest["address"].startswith("127.0.0.1:") and self.manifest["address"] != "127.0.0.1:13333", "runner must target the dedicated loopback fixture")
        self.base = "http://" + self.manifest["address"]
        self.binary = args.binary.resolve()
        require(self.binary.is_file(), "source-bound test binary is missing")
        self.catalog_a = Path(self.manifest["catalog_file"]).resolve()
        require(self.catalog_a.is_relative_to(self.root), "catalog must be a private copy")
        self.catalog = json.loads(self.catalog_a.read_text())
        self.offerings = {o["id"]: o for o in self.catalog["offerings"]}
        schema = self.catalog["application"]
        self.routes = dict(schema["runtime"]["http"]["routes"])
        self.routes.update({name:path for name,path in schema["runtime"]["http"]["legacy_routes"].items() if name not in self.routes})
        self.client, self.other = Client(self.base, self.owner), Client(self.base, self.owner)
        self.process = self.process_log = None
        self.report = {"source_sha": self.manifest["source_sha"], "mode": "offline-mock" if args.offline_mock else "live", "base_url": self.base, "scenarios": {}, "browser_stages": [], "selected_scope": args.only or "all"}
        self.sources = {"humanoid": self.task(args.source_humanoid), "format": self.task(args.source_format)}
        require(args.source_humanoid != args.source_format, "the two format fault scenarios require distinct genuine source Tasks")
        for source in self.sources.values():
            require(source["owner_id"] == self.owner and source["state"] == "SUCCEEDED", "seed source is not the owner's successful Task")
        self.initial = self.counts()
        self.standalone_lost_source = None
        if args.only == "lost-response":
            # Validate standalone input and actual bytes during read-only
            # construction, before start() can write a manifest or spawn workers.
            self.standalone_lost_source = self.lost_response_source()

    def db(self):
        connection = sqlite3.connect((self.root / "acceptance.db").as_uri() + "?mode=ro", uri=True)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA query_only=ON")
        return connection

    def query(self, query, values=()):
        with closing(self.db()) as db:
            return [dict(row) for row in db.execute(query, values)]

    def task(self, task_id):
        rows = self.query("SELECT * FROM generation_tasks WHERE id=?", (task_id,))
        require(len(rows) == 1, f"Task {task_id} is missing")
        return rows[0]

    def counts(self):
        with closing(self.db()) as db:
            return {table: db.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0] for table in ("generation_tasks", "generation_attempts", "generation_prepared_inputs", "execution_grants", "generation_usage_ledger")}

    def source_asset(self, source):
        assets = self.query("SELECT * FROM generation_assets WHERE task_id=? AND role='model' AND variant='glb' ORDER BY ordinal,id", (source["id"],))
        require(bool(assets), "source Task has no saved GLB")
        return assets[0]

    def aggregate(self, task_id, verify_bytes=False):
        task = self.task(task_id)
        attempts = self.query("SELECT * FROM generation_attempts WHERE task_id=? ORDER BY ordinal", (task_id,))
        assets = self.query("SELECT * FROM generation_assets WHERE task_id=? ORDER BY id", (task_id,))
        if verify_bytes:
            for asset in assets:
                path = (self.root / "assets" / asset["storage_key"]).resolve()
                require(path.is_relative_to(self.root / "assets") and path.is_file(), "asset escaped the isolated storage")
                require(path.stat().st_size == asset["size_bytes"] and digest(path) == asset["sha256"], "actual asset bytes differ from the saved digest")
        return stable_digest({"task": task, "attempts": attempts, "assets": assets})

    def verify_delivery(self, task_id):
        task=self.task(task_id)
        require(task["state"]=="SUCCEEDED","delivery requires a successful public Task")
        offering=json.loads(task["offering_json"])
        _,public=self.client.request("GET",self.api_path("task_item",taskID=task_id))
        require(public["id"]==task_id and public["state"]=="SUCCEEDED","public Task projection disagrees with the kernel")
        assets=self.query("SELECT * FROM generation_assets WHERE task_id=? ORDER BY id",(task_id,))
        public_assets={a["id"]:a for a in public["assets"]}
        for output in offering["outputs"]:
            minimum=output.get("minimum",1)
            if output.get("delivery")=="background" or minimum==0: continue
            matching=[a for a in assets if a["required"] and a["role"]==output["role"] and (not output.get("variant") or a["variant"]==output["variant"]) and (not output.get("variant_prefix") or a["variant"].startswith(output["variant_prefix"]))]
            require(len(matching)>=minimum,"frozen Offering mandatory output slot is incomplete")
        for resource in public.get("resources",[]):
            if resource.get("delivery")=="required":
                require(resource["state"]=="READY","mandatory public resource is not READY")
        verified=[]
        for asset in assets:
            if not asset["required"]: continue
            projected=public_assets.get(asset["id"])
            require(projected and projected["required"] and projected["sha256"]==asset["sha256"] and projected["size"]==asset["size_bytes"],"mandatory asset is absent or differs in public projection")
            path=self.api_path("task_asset",taskID=task_id,assetID=asset["id"])
            downloads=[]
            for _ in range(2):
                request=urllib.request.Request(self.base+path,headers={"Cache-Control":"no-cache"})
                with self.client.open.open(request,timeout=120) as response:
                    require(response.status==200,"authenticated full asset download failed")
                    h=hashlib.sha256();size=0
                    for block in iter(lambda:response.read(1024*1024),b""):
                        size+=len(block);h.update(block)
                    require(size==asset["size_bytes"] and h.hexdigest()==asset["sha256"],"authenticated delivery bytes differ from saved asset identity")
                    downloads.append({"status":response.status,"size":size,"sha256":h.hexdigest()})
            self.other.request("GET",path,statuses=(404,))
            verified.append({"asset_id":asset["id"],"role":asset["role"],"variant":asset["variant"],"downloads":downloads,"cross_owner_status":404})
        require(bool(verified),"successful Task has no mandatory delivered assets")
        attempts=self.query("SELECT id,ordinal,upstream_provider_id,upstream_job_id FROM generation_attempts WHERE task_id=? ORDER BY ordinal",(task_id,))
        if not self.args.offline_mock:
            require(attempts and attempts[-1]["upstream_job_id"],"successful live step has no accepted upstream job identity")
        grants=self.query("SELECT request_id,status,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,plan_step_id FROM execution_grants WHERE task_id=?",(task_id,))
        require(len(grants)==1 and grants[0]["status"]=="bound","successful Task is not bound to one exact execution Grant")
        ledger=self.query("SELECT task_status,estimated_credits,retry_count FROM generation_usage_ledger WHERE task_id=?",(task_id,))
        self.report.setdefault("delivery",{})[task_id]={"frozen_offering_id":offering["id"],"frozen_offering_revision":offering["revision"],"assets":verified,"resources":public.get("resources",[]),"grant":grants[0],"attempts_sha256":stable_digest(attempts),"attempt_count":len(attempts),"ledger_projection":ledger,"aggregate_sha256":self.aggregate(task_id,True)}

    def api_path(self, route, **values):
        path = self.routes[route]
        for name, value in values.items():
            path = path.replace("{" + name + "}", value)
        return path

    def start(self, catalog=None):
        require(self.process is None, "fixture already running")
        self.manifest["catalog_file"] = str(catalog or self.catalog_a)
        write_private(self.manifest_path, self.manifest)
        (self.root / "ready.json").unlink(missing_ok=True)
        env = dict(os.environ, ART_AI_ACCEPTANCE_ACK="isolated-test-only", ART_AI_ACCEPTANCE_ROOT=str(self.root), ART_AI_ACCEPTANCE_MANIFEST=str(self.manifest_path))
        if self.args.offline_mock:
            env.update(PROVIDER_MODE="mock", AGENT_MODE="mock", ART_AI_ACCEPTANCE_MODE="offline-mock")
        else:
            require(env.get("PROVIDER_MODE") == "live", "live --execute requires the target runtime PROVIDER_MODE=live")
            env.pop("ART_AI_ACCEPTANCE_MODE", None)
        self.process_log = (self.root / "server.log").open("a")
        self.process = subprocess.Popen([str(self.binary), "-test.run", "^TestAcceptanceServer$", "-test.timeout", "0"], cwd=self.root, env=env, stdout=self.process_log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            require(self.process.poll() is None, "fixture exited before readiness; inspect its private server.log")
            if (self.root / "ready.json").exists():
                ready = json.loads((self.root / "ready.json").read_text())
                require(ready["pid"] == self.process.pid and ready["source_sha"] == self.manifest["source_sha"], "readiness receipt does not identify this binary process")
                require(ready["provider_mode"] == ("mock" if self.args.offline_mock else "live"), "readiness Provider mode does not match the requested acceptance")
                require(ready["test_binary_sha256"] == digest(self.binary), "test binary receipt digest changed")
                require(ready["catalog_sha256"] == digest(self.manifest["catalog_file"]), "running catalog receipt digest changed")
                self.report.setdefault("receipts", []).append(ready)
                self.client.login(self.routes)
                self.other.login(self.routes, other=True)
                return
            time.sleep(0.1)
        raise AcceptanceError("fixture did not become ready")

    def stop(self):
        if self.process is None:
            return
        self.process.send_signal(signal.SIGTERM)
        try:
            code = self.process.wait(timeout=30)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()
            raise AcceptanceError("fixture graceful shutdown timed out") from None
        finally:
            self.process_log.close()
            self.process, self.process_log = None, None
        require(code == 0, "fixture shutdown failed; inspect private server.log")

    def register(self, scenario):
        require(not any(s["id"] == scenario["id"] for s in self.manifest["scenarios"]), "scenario was already registered; use a fresh private seed for a new run")
        self.manifest["scenarios"].append(scenario)
        write_private(self.manifest_path, self.manifest)
        return scenario

    def get_plan(self, plan):
        return self.client.request("GET", self.api_path("processing_plan_item", planID=plan))[1]

    def wait_plan(self, plan, predicate, description):
        deadline = time.monotonic() + self.args.timeout
        last = None
        while time.monotonic() < deadline:
            last = self.get_plan(plan)
            if predicate(last):
                return last
            if last.get("failure") == "plan_consent_required":
                self.approve_plan(last)
            elif last["state"] in ("FAILED", "PAUSED") and last.get("failure") not in ("", "plan_consent_required"):
                raise AcceptanceError(f"{description}: unexpected {last['state']} {last.get('failure')}")
            time.sleep(0.5 if self.args.offline_mock else 2)
        raise AcceptanceError(f"{description}: timed out at {last and last['state']}")

    def approve_plan(self, plan):
        step = next(s for s in plan["steps"] if s.get("failure") == "plan_consent_required")
        _, grant = self.client.request("POST", self.routes["task_grants"], {"prepared_input_id": step["prepared_input_id"], "plan_id": plan["id"], "plan_step_id": step["plan_step_id"]}, (201,), step["idempotency_key"])
        if grant["status"] == "pending":
            self.client.request("PUT", self.api_path("task_grant_decision", requestID=grant["request_id"]), {"decision": "approve"})
        body = {"expected_plan_version": plan["version"], "expected_offering_revision": step["revision"], "authorization_request_id": grant["request_id"]}
        self.client.request("POST", self.api_path("processing_plan_resume", planID=plan["id"]), body, (202,))

    def frozen(self, plan, index=0):
        step = plan["steps"][index]
        fields = ("prepared_input_id", "prepared_input_hash", "authorization_request_id", "idempotency_key", "operation_id", "plan_step_id", "retry")
        identity = {k: step.get(k) for k in fields}
        if step.get("prepared_input_id"):
            inputs = self.query("SELECT * FROM generation_prepared_inputs WHERE id=?", (step["prepared_input_id"],))
            require(len(inputs) == 1, "frozen PreparedInput missing")
            identity["prepared_row_sha256"] = stable_digest(inputs[0])
        if step.get("authorization_request_id"):
            grants = self.query("SELECT * FROM execution_grants WHERE request_id=?", (step["authorization_request_id"],))
            require(len(grants) == 1, "frozen Grant missing")
            grant = grants[0]
            require(grant["owner_id"] == self.owner and grant["session_id"] == plan["session_id"] and grant["idempotency_key"] == step["idempotency_key"] and grant["operation_id"] == plan["id"], "Grant scope differs from the plan's frozen identity")
            identity["grant_scope_sha256"] = stable_digest({k: grant[k] for k in ("request_id", "owner_id", "session_id", "offering_id", "offering_revision", "prepared_input_id", "prepared_input_hash", "idempotency_key", "operation_id", "plan_step_id")})
        return identity

    def act(self, action, plan, index=0, status=(202,)):
        step = plan["steps"][index]
        body = {"expected_plan_version": plan["version"]}
        if action != "reconcile":
            body["expected_offering_revision"] = step["revision"]
        return self.client.request("POST", self.api_path("processing_plan_" + action, planID=plan["id"]), body, status)

    def browser(self, stage, source, plan, expected_status):
        context = {"stage": stage, "base_url": self.base, "source_sha": self.manifest["source_sha"], "session_id": source["session_id"], "source_task_id": source["id"], "plan_id": plan["id"], "expected_plan_version": plan["version"], "expected_offering_revision": next(s for s in plan["steps"] if s["state"] in ("FAILED", "PAUSED"))["revision"], "expected_http_status": expected_status}
        context["frozen_offering_revision"]=context["expected_offering_revision"]
        if stage=="format_frozen_catalog_b":
            current=json.loads(Path(self.manifest["catalog_file"]).read_text())
            context["expected_offering_revision"]=next(o["revision"] for o in current["offerings"] if o["id"]=="meshy/processing/remesh")
        write_private(self.root / "browser-stage.json", context)
        entry={"stage":stage,"executed":False}
        self.report["browser_stages"].append(entry)
        if self.args.browser_command:
            subprocess.run(shlex.split(self.args.browser_command) + ["--context", str(self.root / "browser-stage.json")], cwd=self.root, check=True, timeout=self.args.timeout)
            evidence_path=self.root/"browser-evidence"/(stage+".json")
            require(evidence_path.is_file(),"browser helper did not retain its stage evidence")
            evidence=json.loads(evidence_path.read_text())
            require(evidence.get("result")=="PASS" and evidence.get("source_sha")==self.manifest["source_sha"] and evidence.get("stage")==stage,"browser evidence did not prove this source-bound stage")
            entry["executed"]=True;entry["evidence_sha256"]=digest(evidence_path)
            return True
        return False

    def chain(self):
        source = self.sources["humanoid"]
        key = "controlled-acceptance-chain-" + self.manifest["source_sha"]
        plan = plan_id(self.owner, key)
        self.register({"id": "middle-rigging", "owner_id": self.owner, "session_id": source["session_id"], "plan_id": plan, "step_index": 1, "capability": "rigging", "offering_id": "meshy/processing/rigging", "fault": "rigging_start_failed", "max_provider_starts": 3})
        steps = [{"offering_id": "meshy/processing/" + cap, "expected_offering_revision": self.offerings["meshy/processing/" + cap]["revision"], "input": CHAIN_INPUTS.get(cap, {})} for cap in ("remesh", "rigging", "animation")]
        self.client.request("POST", self.routes["processing_plans"], {"session_id": source["session_id"], "source_task_id": source["id"], "source_asset_id": self.source_asset(source)["id"], "steps": steps}, (201,), key)
        failed = self.wait_plan(plan, lambda p: p["state"] == "FAILED", "middle step failure")
        require(failed["steps"][0]["state"] == "SUCCEEDED" and failed["steps"][1]["state"] == "FAILED" and not failed["steps"][2].get("task_id"), "failure did not isolate the middle step")
        remesh = failed["steps"][0]["task_id"]
        preserved = self.aggregate(remesh, True)
        if not self.browser("processing_retry", source, failed, 202):
            self.act("retry", failed, 1)
        final = self.wait_plan(plan, lambda p: p["state"] == "SUCCEEDED", "explicit middle-step retry")
        require(final["steps"][0]["task_id"] == remesh and self.aggregate(remesh, True) == preserved, "Retry changed the completed Remesh Task/Attempt/assets")
        require(final["steps"][1]["retry"] == 1 and final["steps"][1]["task_id"] != failed["steps"][1]["task_id"], "Retry did not preserve its step attempt identity")
        animation = self.task(final["steps"][2]["task_id"])
        require(json.loads(animation["input_json"])["source_task"] == final["steps"][1]["task_id"], "Animation did not consume recovered Rigging")
        for step in final["steps"]:
            self.verify_delivery(step["task_id"])
        self.report["scenarios"]["middle_rigging"] = {"plan": final, "remesh_aggregate_sha256": preserved}

    def lost_response_source(self):
        if self.args.only == "lost-response":
            source_id = getattr(self.args, "source_lost_task", None)
            require(source_id, "standalone lost-response requires an explicit Remesh Task")
        else:
            chain = self.report.get("scenarios", {}).get("middle_rigging", {}).get("plan", {})
            require(chain.get("state") == "SUCCEEDED" and chain.get("steps"),
                    "lost-response requires the current run's verified successful chain")
            require(chain.get("session_id") == self.sources["humanoid"]["session_id"],
                    "verified chain belongs to a different source session")
            first = chain["steps"][0]
            require(first.get("state") == "SUCCEEDED" and first.get("task_id"),
                    "verified chain has no successful Remesh source")
            source_id = first["task_id"]
        source = self.task(source_id)
        require(source["id"] == source_id and source["owner_id"] == self.owner and
                source["session_id"] == self.sources["humanoid"]["session_id"] and
                source["state"] == "SUCCEEDED" and source["capability"] == "remesh",
                "lost-response source must be the same owner's successful Remesh in the selected session")
        frozen = json.loads(source["offering_json"])
        rigging = self.offerings["meshy/processing/rigging"]
        require(frozen.get("id") == "meshy/processing/remesh" and
                frozen.get("capability") == "remesh" and
                frozen.get("provider_id") == rigging["provider_id"],
                "lost-response source frozen Offering is not the selected Meshy Remesh")
        frozen_input = json.loads(source["input_json"])
        require(frozen_input.get("convert_format_only", False) is False,
                "lost-response source must reduce geometry, not only convert formats")
        polycount_parameters = [parameter for parameter in frozen.get("parameters", [])
                                if parameter.get("semantic") == "polycount" and parameter.get("type") == "number"]
        require(len(polycount_parameters) == 1,
                "lost-response source frozen Offering has no unique polycount contract")
        parameter = polycount_parameters[0]
        polycount = frozen_input.get(parameter["name"])
        minimum, maximum = parameter.get("minimum", 0), parameter.get("maximum")
        bounded_numbers = (polycount, minimum, maximum)
        require(all(isinstance(value, (int, float)) and not isinstance(value, bool) and
                    math.isfinite(value) for value in bounded_numbers) and
                0 <= minimum <= polycount <= maximum and maximum > 0,
                "lost-response source polycount is outside its frozen Offering bounds")
        compatibility = {"frozen_polycount_parameter": parameter["name"],
                         "target_polycount": polycount, "frozen_minimum": minimum,
                         "frozen_maximum": maximum}
        if self.args.only != "lost-response":
            require(len(chain["steps"]) > 1 and chain["steps"][1].get("state") == "SUCCEEDED" and
                    chain["steps"][1].get("task_id"),
                    "verified chain has no successful Rigging consumer")
            rigged = self.task(chain["steps"][1]["task_id"])
            require(rigged["owner_id"] == self.owner and rigged["session_id"] == source["session_id"] and
                    rigged["state"] == "SUCCEEDED" and rigged["capability"] == "rigging" and
                    json.loads(rigged["input_json"]).get("source_task") == source_id,
                    "verified chain Rigging did not consume this Remesh source")
            compatibility["successful_rigging_consumer_task_id"] = rigged["id"]
        self.lost_source_compatibility = compatibility
        asset = self.source_asset(source)
        require(asset.get("required") and asset["size_bytes"] > 0 and
                len(asset.get("sha256", "")) == 64,
                "lost-response source has no required saved GLB identity")
        snapshot = self.aggregate(source_id, True)
        return source, asset, snapshot

    def lost_response(self):
        source, asset, source_before = self.lost_response_source()
        if self.standalone_lost_source is not None:
            require(source_before == self.standalone_lost_source[2],
                    "standalone lost-response source changed after read-only preflight")
        key = f"controlled-acceptance-lost:{self.manifest['source_sha']}:{source['id']}"
        plan = plan_id(self.owner, key)
        offering = self.offerings["meshy/processing/rigging"]
        self.register({"id": "lost-response:" + source["id"], "owner_id": self.owner, "session_id": source["session_id"], "plan_id": plan, "step_index": 0, "capability": offering["capability"], "offering_id": offering["id"], "fault": "submit_response_lost", "max_provider_starts": 1})
        self.client.request("POST", self.routes["processing_plans"], {"session_id": source["session_id"], "source_task_id": source["id"], "source_asset_id": asset["id"], "steps": [{"offering_id": offering["id"], "expected_offering_revision": offering["revision"], "input": {}}]}, (201,), key)
        paused = self.wait_plan(plan, lambda p: p.get("failure") == "plan_submit_unknown", "lost response")
        frozen = self.frozen(paused)
        if not self.browser("processing_unknown_resume",source,paused,202):
            self.act("resume", paused)
        unresolved = self.get_plan(plan)
        require(unresolved["failure"] == "plan_submit_unreconciled", "Resume did not expose the unreconciled scope")
        before = self.counts()
        self.other.request("POST", self.api_path("processing_plan_reconcile", planID=plan), {"expected_plan_version": unresolved["version"]}, (404,))
        if not self.browser("processing_unreconciled", source, unresolved, 200):
            self.act("reconcile", unresolved, status=(200,))
        linked = self.get_plan(plan)
        require(linked["steps"][0].get("task_id") and self.frozen(linked) == frozen, "Reconcile did not bind the accepted Task's original frozen identity")
        self.act("reconcile", unresolved, status=(409,))
        self.act("reconcile", linked, status=(200,))
        after = self.counts()
        require(after == before, f"reconciliation allocated a Task/Attempt/Grant/ledger row: {before} -> {after}")
        self.wait_plan(plan, lambda p: p["state"] == "SUCCEEDED", "accepted Task observation")
        self.verify_delivery(linked["steps"][0]["task_id"])
        source_after = self.aggregate(source["id"], True)
        require(source_after == source_before, "lost-response recovery changed its verified Remesh source")
        self.report["scenarios"]["lost_response_source"] = {
            "task_id": source["id"], "session_id": source["session_id"],
            "asset_id": asset["id"], "asset_size": asset["size_bytes"],
            "asset_sha256": asset["sha256"], "compatibility": self.lost_source_compatibility,
            "aggregate_before_sha256": source_before,
            "aggregate_after_sha256": source_after,
        }
        self.report["scenarios"]["lost_response"] = {"plan_id": plan, "task_id": linked["steps"][0]["task_id"], "frozen": frozen, "read_only_reconciliation_counts": before}

    def format_plan(self, which, fault):
        source = self.sources[which]
        # Meshy's own format completion is the Convert Offering since #330; the Remesh entry of the
        # Schema's conversion list is retired (convert_format_only).
        offering = self.offerings[FORMAT_COMPLETION_OFFERING]
        key = f"format-completion:{source['id']}:{offering['id']}:{offering['revision']}:all"
        plan = plan_id(self.owner, key)
        _,initial_coverage=self.client.request("GET",self.api_path("model_formats",taskID=source["id"]))
        missing=missing_completion_formats(initial_coverage,offering["id"])
        require(initial_coverage["can_complete"] and missing,"genuine source lists no missing format the Meshy convert completion can supply; pass --source-format with a genuine SUCCEEDED source that lacks one, and do not create a replacement paid plan")
        self.register({"id": fault, "owner_id": self.owner, "session_id": source["session_id"], "plan_id": plan, "step_index": 0, "capability": offering["capability"], "offering_id": offering["id"], "fault": fault, "max_provider_starts": 1})
        self.client.request("POST", self.api_path("model_formats_complete", taskID=source["id"]), {}, (202,))
        reason = "plan_prepare_unavailable" if fault == "prepare_unavailable" else "plan_submit_unknown"
        paused = self.wait_plan(plan, lambda p: p.get("failure") == reason, fault)
        self.client.request("POST", self.api_path("model_formats_complete", taskID=source["id"]), {}, (409,))
        return source, paused

    def formats(self):
        source, paused = self.format_plan("format", "prepare_unavailable")
        require(not paused["steps"][0].get("prepared_input_id") and not paused["steps"][0].get("authorization_request_id"), "preparation failure produced a frozen grant")
        if not self.browser("format_prepare_paused", source, paused, 202):
            self.act("resume", paused)
        final = self.wait_plan(paused["id"], lambda p: p["state"] == "SUCCEEDED", "format prepare recovery")
        self.verify_delivery(final["steps"][0]["task_id"])
        self.report["scenarios"]["format_prepare"] = {"plan": final}
        source, paused = self.format_plan("humanoid", "submit_before_acceptance")
        frozen = self.frozen(paused)
        self.act("resume", paused)
        unresolved = self.get_plan(paused["id"])
        require(unresolved["failure"] == "plan_submit_unreconciled", "pre-acceptance recovery did not become unreconciled")
        self.act("reconcile", unresolved, status=(200,))
        absent = self.get_plan(paused["id"])
        require(absent["failure"] == "plan_reconciled_absent" and self.frozen(absent) == frozen and not absent["steps"][0].get("task_id"), "absence reconciliation changed the frozen Grant")
        self.stop()
        catalog_b = json.loads(self.catalog_a.read_text())
        changed = next(o for o in catalog_b["offerings"] if o["id"] == FORMAT_COMPLETION_OFFERING)
        changed["revision"] += ".acceptance-b"
        changed["estimated_credits"] += 1
        catalog_b_path = self.root / "catalog-b.json"
        write_private(catalog_b_path, catalog_b)
        self.start(catalog_b_path)
        before = self.counts()
        _, coverage = self.client.request("GET", self.api_path("model_formats", taskID=source["id"]))
        require(not coverage["can_complete"] and any(s.get("plan_id") == absent["id"] for s in coverage["steps"]), "new revision lost the active old-revision format lock")
        self.client.request("POST", self.api_path("model_formats_complete", taskID=source["id"]), {}, (409,))
        displayed = self.get_plan(absent["id"])
        if not self.browser("format_frozen_catalog_b", source, displayed, 409):
            self.client.request("POST", self.api_path("processing_plan_resume", planID=absent["id"]), {"expected_plan_version": displayed["version"], "expected_offering_revision": changed["revision"]}, (409,))
        require(self.counts() == before and self.frozen(self.get_plan(absent["id"])) == frozen, "changed frozen price allocated or replaced execution identity")
        self.stop()
        self.start(self.catalog_a)
        restored = self.get_plan(absent["id"])
        if not self.browser("format_frozen_catalog_a", source, restored, 202):
            self.act("resume", restored)
        final = self.wait_plan(absent["id"], lambda p: p["state"] == "SUCCEEDED", "original-price format recovery")
        require(self.frozen(final) == frozen, "original-price resume replaced the frozen scope")
        self.verify_delivery(final["steps"][0]["task_id"])
        self.client.request("POST", self.api_path("model_formats_complete", taskID=source["id"]), {}, (409,))
        self.report["scenarios"]["format_frozen"] = {"plan": final, "frozen": frozen, "catalog_b_sha256": digest(catalog_b_path), "old_revision_locked": True, "changed_price_http_status": 409}

    def image(self):
        source = self.sources["format"]
        connection = next(c for c in self.catalog["application"]["runtime"]["provider_connections"] if c["protocol"] == "image-sync" and "insufficient_quota" in c.get("error_mapping", {}).get("upstream_code_map", {}))
        offering = next(o for o in self.offerings.values() if o["adapter_key"] == connection["adapter_key"] and o["capability"] == "text-to-image" and o["enabled"])
        key = "controlled-acceptance-image-" + self.manifest["source_sha"]
        self.register({"id": "image-balance", "owner_id": self.owner, "session_id": source["session_id"], "step_index": 0, "capability": offering["capability"], "offering_id": offering["id"], "idempotency_key": key, "fault": "image_insufficient_quota", "max_provider_starts": 1})
        _, prepared = self.client.request("POST", self.routes["task_prepare"], {"session_id": source["session_id"], "offering_id": offering["id"], "capability": offering["capability"], "input": {"prompt": "isolated wire quota acceptance"}}, (201,))
        _, grant = self.client.request("POST", self.routes["task_grants"], {"prepared_input_id": prepared["prepared_input_id"]}, (201,), key)
        if grant["status"] == "pending":
            self.client.request("PUT", self.api_path("task_grant_decision", requestID=grant["request_id"]), {"decision": "approve"})
        _, accepted = self.client.request("POST", self.routes["tasks"], {"prepared_input_id": prepared["prepared_input_id"], "authorization_request_id": grant["request_id"]}, (202,), key)
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            task = self.task(accepted["task_id"])
            if task["state"] == "FAILED":
                break
            time.sleep(0.2)
        require(task["state"] == "FAILED", "image wire quota Task did not fail")
        failure = json.loads(task["failure_json"])
        require(failure["class"] == "insufficient_balance" and not failure["retryable"] and not failure.get("ambiguous"), "wire quota did not map to definitive insufficient balance")
        before = self.counts()
        _, problem = self.client.request("POST", self.api_path("task_retry", taskID=task["id"]), {}, (409,))
        require(problem["code"] == "task_not_retryable" and self.counts() == before, "balance retry did not reject before allocating execution")
        write_private(self.root / "browser-stage.json", {"stage": "image_insufficient_balance", "base_url": self.base, "source_sha": self.manifest["source_sha"], "session_id": source["session_id"], "source_task_id": source["id"], "task_id": task["id"], "expected_http_status": 409})
        if self.args.browser_command:
            subprocess.run(shlex.split(self.args.browser_command) + ["--context", str(self.root / "browser-stage.json")], cwd=self.root, check=True, timeout=self.args.timeout)
        image_entry={"stage":"image_insufficient_balance","executed":False}
        if self.args.browser_command:
            evidence_path=self.root/"browser-evidence"/"image_insufficient_balance.json"
            require(evidence_path.is_file(),"image browser evidence missing")
            evidence=json.loads(evidence_path.read_text())
            require(evidence.get("result")=="PASS" and evidence.get("source_sha")==self.manifest["source_sha"],"image browser evidence failed")
            image_entry.update(executed=True,evidence_sha256=digest(evidence_path))
        self.report["browser_stages"].append(image_entry)
        require(self.counts()==before,"balance browser inspection allocated execution rows")
        self.report["scenarios"]["image_balance"] = {"task_id": task["id"], "session_id": source["session_id"], "failure_class": failure["class"], "upstream_code": failure.get("upstream_code"), "retry_http_status": 409, "browser_executed": bool(self.args.browser_command)}

    def execute(self):
        require(not self.manifest["scenarios"], "runner requires a fresh seed with no registered scenarios")
        try:
            source_before = {name: self.aggregate(source["id"], True) for name, source in self.sources.items()}
            self.start()
            if self.args.only in (None,"chain"): self.chain()
            if self.args.only in (None,"lost-response"): self.lost_response()
            if self.args.only in (None,"formats"): self.formats()
            if self.args.only in (None,"image"): self.image()
            for name, source in self.sources.items():
                require(self.aggregate(source["id"], True) == source_before[name], "acceptance changed a genuine seed Task/Attempt/asset")
            events = [json.loads(line) for line in (self.root / "events.jsonl").read_text().splitlines()]
            starts = {s["id"]: sum(e["kind"] == "real_start" and e["scenario"] == s["id"] for e in events) for s in self.manifest["scenarios"]}
            for scenario in self.manifest["scenarios"]:
                require(starts[scenario["id"]] <= scenario["max_provider_starts"], "fixture real Start budget exceeded")
            expected_stages={None:["processing_retry","processing_unknown_resume","processing_unreconciled","format_prepare_paused","format_frozen_catalog_b","format_frozen_catalog_a","image_insufficient_balance"],"chain":["processing_retry"],"lost-response":["processing_unknown_resume","processing_unreconciled"],"formats":["format_prepare_paused","format_frozen_catalog_b","format_frozen_catalog_a"],"image":["image_insufficient_balance"]}[self.args.only]
            actual_stages=[entry["stage"] for entry in self.report["browser_stages"] if entry["executed"]]
            self.report.update({"selected_scope":self.args.only or "all","expected_browser_stages":expected_stages,"completed_browser_stages":actual_stages,"result": "checks_passed", "initial_counts": self.initial, "final_counts": self.counts(), "real_starts": starts, "seed_aggregates_sha256": source_before, "browser_coverage_complete": set(expected_stages)==set(actual_stages)})
        except BaseException:
            self.report["result"] = "failed"
            raise
        finally:
            try:
                self.stop()
            except BaseException:
                self.report["result"]="failed"
                write_private(self.root / "controlled-acceptance-report.json",self.report)
                raise
            if self.report.get("result")=="checks_passed": self.report["result"]="passed"
            write_private(self.root / "controlled-acceptance-report.json", self.report)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--source-humanoid", default="task_1e9ff405bc4ea1fbd0f76d1fc95c92d4")
    parser.add_argument("--source-format", default="task_2d4c7c7e0acad06603a92fabfbbcb393")
    parser.add_argument("--source-lost-task", help="required with --only lost-response: an existing verified Meshy Remesh Task in the humanoid source session")
    parser.add_argument("--execute", action="store_true")
    parser.add_argument("--only", choices=("chain","lost-response","formats","image"), help="run one isolated scenario group")
    parser.add_argument("--offline-mock", action="store_true", help="explicitly mark the run as offline mock evidence, never live acceptance")
    parser.add_argument("--browser-command", help="isolated browser helper command, called with --context <private JSON>")
    parser.add_argument("--timeout", type=int, default=1800)
    args = parser.parse_args()
    require(args.timeout > 0, "timeout must be positive")
    runner = Runner(args)
    if not args.execute:
        print(json.dumps({"mode": "dry-run", "source_sha": runner.manifest["source_sha"], "base_url": runner.base, "provider_mode_expected": "mock" if args.offline_mock else "live", "source_tasks": [s["id"] for s in runner.sources.values()], "lost_response_source": runner.standalone_lost_source[0]["id"] if runner.standalone_lost_source else "current_run_verified_chain_remesh", "counts": runner.initial, "maximum_paid_provider_starts": {None:6,"chain":3,"lost-response":1,"formats":2,"image":0}[args.only], "local_wire_image_starts": 1, "browser_helper_configured": bool(args.browser_command)}))
        return
    runner.execute()
    print(json.dumps({"result": runner.report["result"], "mode": runner.report["mode"], "report": str(runner.root / "controlled-acceptance-report.json"), "real_starts": runner.report["real_starts"], "browser_coverage_complete": runner.report["browser_coverage_complete"]}))


if __name__ == "__main__":
    try:
        main()
    except (AcceptanceError, OSError, ValueError, KeyError, sqlite3.Error, subprocess.SubprocessError) as error:
        print(f"controlled acceptance failed: {error}", file=sys.stderr)
        sys.exit(1)
