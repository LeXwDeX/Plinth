#!/usr/bin/env python3
"""Offline tests for the endpoint-chain L5 scripts (#539, #567): l5_endpoints.py, smoke-l5-meshy-endpoints.py,
smoke-l5-hyper3d.py and smoke-l5-tripo-endpoints.py. No network beyond a loopback listener, no server, no real upstream.

The chains themselves run end to end against the local fake stack (smoke-fake-stack.py builds it); these tests
cover every decision the scripts make: the gate, the single submission per step, the budget, the plan, the
checks and the clean-up.
"""
import contextlib
import hashlib
import http.server
import importlib.util
import io
import json
import os
import pathlib
import struct
import subprocess
import sys
import tempfile
import threading
import types
import unittest

sys.dont_write_bytecode = True
ROOT = pathlib.Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts" / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))

import l5_common as common  # noqa: E402
import l5_endpoints as engine  # noqa: E402
import upstream_shape  # noqa: E402


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, SMOKE / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


meshy = load("smoke_l5_meshy_endpoints", "smoke-l5-meshy-endpoints.py")
hyper3d = load("smoke_l5_hyper3d", "smoke-l5-hyper3d.py")
tripo = load("smoke_l5_tripo_endpoints", "smoke-l5-tripo-endpoints.py")

D = engine.Decimal
LIVE = {"ART_AI_UPSTREAM_SHAPE": "live"}
FAKE = {"ART_AI_UPSTREAM_SHAPE": "fake"}
ENV = {"BASE": "http://127.0.0.1:1", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p"}


def catalog_offerings():
    with open(ROOT / "internal" / "catalog" / "default_catalog.json", encoding="utf-8") as handle:
        document = json.load(handle)
    found = {}

    def walk(node):
        if isinstance(node, dict):
            if isinstance(node.get("id"), str) and "capability" in node and "/" in node["id"]:
                found.setdefault(node["id"], node)
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)
    walk(document)
    return found


def png():
    return common.encode_png(2, 2, [bytes([1, 2, 3] * 2)] * 2)


def glb(triangles=12, meshes=1, with_image=True, height=2.0):
    """A GLB 2.0 whose JSON says what glb_summary reads: triangles, meshes, an embedded PNG, a bounding box."""
    image = png()
    document = {
        "asset": {"version": "2.0"},
        "meshes": [{"primitives": [{"attributes": {"POSITION": 0}, "indices": 1}]} for _ in range(meshes)],
        "accessors": [{"count": 3, "min": [0, 0, 0], "max": [1, height, 1]}, {"count": triangles * 3}],
        "bufferViews": [{"byteOffset": 0, "byteLength": len(image)}],
        "materials": [{}],
    }
    if with_image:
        document["images"] = [{"bufferView": 0, "mimeType": "image/png"}]
    body = json.dumps(document).encode()
    body += b" " * (-len(body) % 4)
    binary = image + b"\x00" * (-len(image) % 4)
    total = 12 + 8 + len(body) + 8 + len(binary)
    return (b"glTF" + struct.pack("<II", 2, total) + struct.pack("<I4s", len(body), b"JSON") + body
            + struct.pack("<I4s", len(binary), b"BIN\x00") + binary)


class Directory:
    def __enter__(self):
        self.manager = tempfile.TemporaryDirectory()
        return self.manager.__enter__()

    def __exit__(self, *exc):
        return self.manager.__exit__(*exc)


def write(directory, name, data):
    path = os.path.join(directory, name)
    with open(path, "wb") as handle:
        handle.write(data)
    return path


# ------------------------------------------------------------------ the gate

class GateTest(unittest.TestCase):
    def test_a_fake_target_runs_without_a_switch(self):
        self.assertEqual(engine.gate([], FAKE), ("fake", None))

    def test_a_fake_target_refuses_live(self):
        _, refusal = engine.gate(["--live"], {**FAKE, "ART_AI_LIVE_ACK": "1"})
        self.assertIn("--live was given but the target runs the fake upstream", refusal)

    def test_any_other_target_needs_both_switches(self):
        for shape in ("live", "mixed", "nonsense"):
            environ = {"ART_AI_UPSTREAM_SHAPE": shape}
            self.assertIsNotNone(engine.gate([], environ)[1], shape)
            self.assertIn("ART_AI_LIVE_ACK=1", engine.gate(["--live"], environ)[1])
            self.assertIn("--live", engine.gate([], {**environ, "ART_AI_LIVE_ACK": "1"})[1])
            self.assertIsNone(engine.gate(["--live"], {**environ, "ART_AI_LIVE_ACK": "1"})[1])

    def test_an_unknown_shape_counts_as_live(self):
        self.assertEqual(upstream_shape.target_shape({}), "live")
        self.assertIsNotNone(engine.gate([], {})[1])

    def test_execute_refuses_before_any_request(self):
        for module in (meshy, hyper3d, tripo):
            for argv, environ in (([], LIVE), (["--live"], LIVE), ([], {**LIVE, "ART_AI_LIVE_ACK": "1"}),
                                  (["--live"], {**FAKE, "ART_AI_LIVE_ACK": "1"})):
                err = io.StringIO()
                with contextlib.redirect_stderr(err):
                    code = module.main(argv, {**ENV, **environ})
                self.assertEqual(code, 2, (module.NAME, argv, environ))
                self.assertIn("refusing to run", err.getvalue())

    def test_execute_needs_the_environment_and_a_plain_base(self):
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(meshy.main([], dict(FAKE)), 2)
        self.assertIn("missing environment", err.getvalue())
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(meshy.main([], {**FAKE, **ENV, "BASE": "http://host/path"}), 2)
        self.assertIn("without a path", err.getvalue())

    def test_both_switches_still_stop_at_a_failed_login_without_running_a_step(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = hyper3d.main(["--live", "--poll", "0.1"], {**ENV, **LIVE, "ART_AI_LIVE_ACK": "1"})
        self.assertEqual(code, 1)
        self.assertIn("FAIL: login", out.getvalue())
        self.assertNotIn("submitted", out.getvalue())

    def test_the_scripts_refuse_as_processes(self):
        base_env = {"PATH": os.environ.get("PATH", ""), **ENV}
        for script in ("smoke-l5-meshy-endpoints.py", "smoke-l5-hyper3d.py", "smoke-l5-tripo-endpoints.py"):
            for extra, argv in ((LIVE, []), (LIVE, ["--live"]), ({**LIVE, "ART_AI_LIVE_ACK": "1"}, [])):
                result = subprocess.run([sys.executable, str(SMOKE / script), *argv], env={**base_env, **extra},
                                        capture_output=True, text=True, timeout=60)
                self.assertEqual(result.returncode, 2, (script, argv, extra))
                self.assertIn("refusing to run", result.stderr)
                self.assertEqual(result.stdout, "")


# ------------------------------------------------------------------ arguments and plan

class ArgumentsTest(unittest.TestCase):
    def parse(self, argv):
        return engine.parse_arguments(argv, "d", 160, "credits")

    def test_defaults(self):
        arguments = self.parse([])
        self.assertEqual(arguments.ceiling, D(160))
        self.assertEqual(arguments.only_steps, [])
        self.assertEqual(arguments.seeds, {})
        self.assertFalse(arguments.keep_raw)

    def test_ceiling_only_and_seeds(self):
        arguments = self.parse(["--max-credits", "12.5", "--only", "a, b", "--seed", "x=task_1", "--seed", "y=task_2"])
        self.assertEqual(arguments.ceiling, D("12.5"))
        self.assertEqual(arguments.only_steps, ["a", "b"])
        self.assertEqual(arguments.seeds, {"x": "task_1", "y": "task_2"})

    def test_misuse_exits_2(self):
        for argv in (["--max-credits", "0"], ["--max-credits", "-1"], ["--max-credits", "many"], ["--seed", "no-equals"],
                     ["--seed", "a=1", "--seed", "a=2"], ["--task-timeout", "5"], ["--poll", "0"], ["--unknown"]):
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as stop:
                self.parse(argv)
            self.assertEqual(stop.exception.code, 2, argv)


class PlanTest(unittest.TestCase):
    def setUp(self):
        self.steps = meshy.steps()

    def test_every_chain_is_a_valid_plan_with_unique_names_and_earlier_sources(self):
        for steps in (meshy.steps(), hyper3d.steps(), tripo.steps()):
            names = [step.name for step in steps]
            self.assertEqual(len(names), len(set(names)))
            seen = set()
            for step in steps:
                self.assertTrue(set(step.needs) <= seen, f"{step.name} needs a later step")
                seen.add(step.name)
            self.assertEqual(engine.plan_problems(steps, [], {}), [])

    def test_the_meshy_chain_is_the_audited_one(self):
        names = [step.name for step in self.steps]
        self.assertEqual(names, ["t2i", "i2i", "t25_text", "t25_image", "t25_image_untextured", "multiview", "multiview_2k", "remesh", "convert",
                                 "retexture", "rigging", "animation", "uv_unwrap", "resize", "analyze", "multi_color", "motion"])
        needs = {step.name: step.needs for step in self.steps}
        for name in ("convert", "retexture", "rigging", "uv_unwrap", "resize", "analyze", "multi_color"):
            self.assertEqual(needs[name], ("remesh",), name)
        self.assertEqual(needs["animation"], ("rigging",))
        self.assertEqual(needs["remesh"], ("t25_image",))
        self.assertEqual(needs["motion"], ())

    def test_the_hyper3d_chain_is_the_audited_one(self):
        steps = hyper3d.steps()
        self.assertEqual([step.name for step in steps], ["text", "bang_task", "bang_upload", "texture", "image_short"])
        self.assertEqual({step.name: step.needs for step in steps},
                         {"text": (), "bang_task": ("text",), "bang_upload": ("text",), "texture": ("text",), "image_short": ()})

    def test_the_tripo_chain_covers_every_enabled_tripo_offering(self):
        # #567: one step per Tripo Offering the default Catalog enables, each source the one the Catalog allows.
        steps = tripo.steps()
        with open(ROOT / "internal" / "catalog" / "default_catalog.json", encoding="utf-8") as handle:
            catalog = {item["id"]: item for item in json.load(handle)["offerings"] if item["id"].startswith("tripo/")}
        enabled = {offering_id for offering_id, item in catalog.items() if item.get("enabled")}
        self.assertEqual({step.offering for step in steps}, enabled)
        named = {step.name: step for step in steps}
        for step in steps:
            self.assertEqual(step.capability, catalog[step.offering]["capability"], step.name)
            for need in step.needs:
                source = next((parameter for parameter in catalog[step.offering]["parameters"]
                               if parameter.get("type") == "task"), None)
                if source and source.get("task_offerings"):
                    self.assertIn(named[need].offering, source["task_offerings"], step.name)
        self.assertEqual(named["retarget"].needs, ("rig_v3",))
        self.assertEqual(named["quick_cap"].needs, ("segment",))
        self.assertEqual(named["edit_mv"].needs, ("i2mv",))

    def test_a_selection_needs_its_sources_or_a_seed(self):
        problems = engine.plan_problems(self.steps, ["retexture"], {})
        self.assertEqual(len(problems), 1)
        self.assertIn("--seed remesh=TASK_ID", problems[0])
        self.assertEqual(engine.plan_problems(self.steps, ["retexture"], {"remesh": "task_1"}), [])
        self.assertEqual(engine.plan_problems(self.steps, ["remesh", "retexture"], {"t25_image": "task_1"}), [])
        self.assertIn("unknown step", engine.plan_problems(self.steps, ["nope"], {})[0])
        self.assertIn("unknown step", engine.plan_problems(self.steps, [], {"nope": "t"})[0])

    def test_selected_steps_keep_chain_order(self):
        self.assertEqual([s.name for s in engine.selected_steps(self.steps, ["resize", "remesh"])], ["remesh", "resize"])
        self.assertEqual(len(engine.selected_steps(self.steps, [])), len(self.steps))

    def test_a_seeded_step_is_never_selected_to_run_again(self):
        """--seed NAME=TASK_ID reuses the source: NAME must not be sent again (that would be a second paid
        generation that also overwrites the seed), whether or not --only names it."""
        seeded = self.steps[0].name
        everything = [s.name for s in engine.selected_steps(self.steps, [], [seeded])]
        self.assertNotIn(seeded, everything)
        self.assertEqual(len(everything), len(self.steps) - 1)
        named = [s.name for s in engine.selected_steps(self.steps, [seeded, self.steps[1].name], [seeded])]
        self.assertEqual(named, [self.steps[1].name])

    def test_cleanup_order_is_reverse_run_order_without_unsent_steps(self):
        results = [{"task_id": "a"}, {"task_id": None}, {"task_id": "c"}, {"task_id": "d"}]
        self.assertEqual(engine.cleanup_order(results), ["d", "c", "a"])


# ------------------------------------------------------------------ parameters and prices

class ParameterTest(unittest.TestCase):
    OFFERING = {"extra_credit_consent_parameter": "authorize_extra_credits", "estimated_credits": 20, "credits_metered": True,
                "parameters": [
                    {"name": "prompt", "required": True},
                    {"name": "formats", "required": True, "default": ["glb"], "adjustment_policy": "advised"},
                    {"name": "res", "default": "8k", "adjustment_policy": "advised"},
                    {"name": "pose", "default": ""},
                    {"name": "authorize_extra_credits", "default": False}]}

    def test_required_defaults_and_consent_are_filled(self):
        filled = engine.fill_parameters(self.OFFERING, {"prompt": "x"})
        self.assertEqual(filled, {"prompt": "x", "formats": ["glb"], "authorize_extra_credits": True})
        plain = engine.fill_parameters({"parameters": [{"name": "p", "required": True, "default": 1}]}, {})
        self.assertEqual(plain, {"p": 1})

    def test_reasons_only_for_changed_advised_parameters(self):
        parameters = {"prompt": "x", "formats": ["glb"], "res": "2k", "pose": "t-pose"}
        self.assertEqual(engine.advised_reasons(self.OFFERING, parameters, "why"), {"res": "why"})
        self.assertEqual(engine.advised_reasons(self.OFFERING, {"res": "8k"}, "why"), {})

    def test_price_is_the_frozen_quote_then_the_estimate_then_free_then_unknown(self):
        quote = {"native_quote": {"unit": "meshy.credit", "total_amount": "40"}}
        self.assertEqual(engine.step_price(quote, self.OFFERING), (D(40), "meshy.credit"))
        self.assertEqual(engine.step_price({}, self.OFFERING), (D(20), engine.LEGACY_UNIT))
        self.assertEqual(engine.step_price({}, {"credits_metered": True}, free=True), (D(0), None))
        self.assertIsNone(engine.step_price({}, {"credits_metered": True}))

    def test_budget_reserves_until_the_ceiling_then_stays_exhausted(self):
        budget = engine.Budget(10)
        self.assertTrue(budget.reserve(D(6)))
        self.assertTrue(budget.fits(D(4)))
        self.assertFalse(budget.reserve(D(5)))
        self.assertTrue(budget.exhausted)
        self.assertEqual(budget.spent, D(6))

    def test_balance_and_reconciliation(self):
        panel = {"accounts": [{"provider": "meshy", "native_balance": {"unit": "meshy.credit", "amount": "1000"}},
                              {"provider": "tripo", "native_balance": None}]}
        self.assertEqual(engine.balance_of(panel, "meshy"), D(1000))
        self.assertIsNone(engine.balance_of(panel, "tripo"))
        self.assertIsNone(engine.balance_of(panel, "hyper3d"))
        self.assertIsNone(engine.balance_of({}, "meshy"))
        matched = engine.reconcile(D(1000), D(858), D(142))
        self.assertTrue(matched["readable"] and matched["matched"] and not matched["negative"])
        self.assertEqual(matched["delta"], "142")
        off = engine.reconcile(D(1000), D(850), D(142))
        self.assertFalse(off["matched"])
        self.assertEqual(off["difference"], "8")
        self.assertTrue(engine.reconcile(D(1), D(2), D(0))["negative"])
        self.assertFalse(engine.reconcile(None, D(2), D(0))["readable"])

    def test_upload_limit_comes_from_the_offering(self):
        offering = {"parameters": [{"name": "source_asset", "max_bytes": 100}]}
        self.assertEqual(engine.parameter_limit(offering, "source_asset", 5), 100)
        self.assertEqual(engine.parameter_limit(offering, "other", 5), 5)
        self.assertEqual(engine.parameter_limit(None, "x", 7), 7)

    def test_the_synthetic_picture_is_a_png_with_the_asked_size(self):
        data = engine.gradient_png(40, 30)
        self.assertEqual(common.image_dimensions(data), (40, 30))
        self.assertNotEqual(engine.gradient_png(40, 30, (1, 2, 3)), data)


# ------------------------------------------------------------------ files and assets

class GlbTest(unittest.TestCase):
    def read(self, data):
        with Directory() as directory:
            return engine.glb_summary(write(directory, "m.glb", data))

    def test_summary_reads_triangles_textures_meshes_and_height(self):
        summary = self.read(glb(triangles=21800, meshes=3, height=1.5))
        self.assertEqual(summary["triangles"], 21800 * 3)
        self.assertEqual((summary["meshes"], summary["materials"], summary["images"]), (3, 1, 1))
        self.assertEqual(summary["texture_edges"], [2])
        self.assertAlmostEqual(summary["height"], 1.5)
        self.assertTrue(summary["length_ok"])

    def test_an_untextured_model_and_bad_bytes(self):
        self.assertEqual(self.read(glb(with_image=False))["images"], 0)
        self.assertIsNone(self.read(b"not a glb at all, but long enough to be read"))
        self.assertIsNone(self.read(glb()[:30]))
        self.assertIsNone(self.read(b"glTF" + struct.pack("<II", 1, 24) + b"\x00" * 20))
        with Directory() as directory:
            self.assertIsNone(engine.glb_summary(os.path.join(directory, "missing.glb")))

    def test_a_wrong_declared_length_is_noticed(self):
        data = bytearray(glb())
        data[8:12] = struct.pack("<I", len(data) + 4)
        self.assertFalse(self.read(bytes(data))["length_ok"])

    def test_triangle_window(self):
        self.assertEqual(engine.triangle_window(10000, 2.18, 0.35), (14170, 29430))
        low, high = engine.triangle_window(10000, 2.18, 0.35)
        self.assertTrue(low <= 21800 <= high)
        self.assertFalse(low <= 10000 <= high)


class AssetTest(unittest.TestCase):
    TASK = {"assets": [{"role": "model", "variant": "glb", "id": "a1", "required": True},
                       {"role": "thumbnail", "variant": "primary", "id": "a2", "required": False}]}

    def test_required_slots(self):
        offering = {"outputs": [{"role": "model", "variant": "glb", "delivery": "required", "minimum": 1},
                                {"role": "model", "variant": "obj", "delivery": "required", "minimum": 1},
                                {"role": "model", "variant": "mtl", "delivery": "required", "minimum": 0},
                                {"role": "thumbnail", "delivery": "background", "minimum": 0}]}
        self.assertEqual(engine.slot_problems(offering, self.TASK), ["missing model/obj"])
        self.assertEqual(engine.slot_problems({"outputs": []}, self.TASK), [])
        self.assertTrue(engine.required_slots_declared(offering))
        self.assertFalse(engine.required_slots_declared({"outputs": [{"role": "model", "delivery": "on-demand", "minimum": 1}]}))

    def test_conditional_slots_are_the_tasks(self):
        # #567: an edited view is required only when its prompt is present; the task freezes which views it owes.
        edit = {"outputs": [{"role": "image", "variant": view, "delivery": "required", "minimum": 1,
                             "required_when": {"parameter": f"{view}_prompt", "operator": "present"}}
                            for view in ("front", "left")]}
        task = {"assets": [{"role": "image", "variant": "front", "required": True}]}
        self.assertEqual(engine.slot_problems(edit, task), [])

    def test_find_asset(self):
        self.assertEqual(engine.find_asset(self.TASK, "model")["id"], "a1")
        self.assertEqual(engine.find_asset(self.TASK, "thumbnail", "primary")["id"], "a2")
        self.assertIsNone(engine.find_asset(self.TASK, "model", "obj"))

    def test_magic_numbers(self):
        self.assertEqual(engine.magic_problem({"variant": "glb"}, b"glTF"), "")
        self.assertEqual(engine.magic_problem({"variant": "glb"}, b"nope"), "not a GLB")
        self.assertEqual(engine.magic_problem({"media_type": "image/png"}, b"\x89PNG\r\n\x1a\n"), "")
        self.assertEqual(engine.magic_problem({"media_type": "image/png"}, b"JFIF"), "not a PNG")
        self.assertEqual(engine.magic_problem({"variant": "3mf"}, b"PK\x03\x04"), "")
        self.assertEqual(engine.magic_problem({"variant": "usdz"}, b"zzzz"), "not a ZIP archive")
        self.assertEqual(engine.magic_problem({"variant": "fbx"}, b"zzzz"), "")

    def test_evidence_never_carries_a_url_or_detail(self):
        task = {"assets": [{"role": "model", "variant": "glb", "required": True, "media_type": "model/gltf-binary", "size": 9,
                            "sha256": "ab" * 32, "url": "/api/v1/tasks/t/assets/a?sig=secret"}],
                "failure": {"class": "invalid_input", "retryable": False, "detail": "the prompt said a private thing"}}
        text = json.dumps([engine.asset_evidence(task), engine.failure_evidence(task)])
        self.assertNotIn("secret", text)
        self.assertNotIn("private", text)
        self.assertIn("abababababab", text)
        self.assertIsNone(engine.failure_evidence({}))

    def test_private_directory_is_0700_and_outside_the_checkout(self):
        path = engine.private_directory()
        try:
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o700)
            self.assertTrue(engine.outside_repository(path))
        finally:
            os.rmdir(path)
        self.assertFalse(engine.outside_repository(str(ROOT / "scripts")))
        self.assertFalse(engine.outside_repository(str(ROOT)))


# ------------------------------------------------------------------ the runner

class ScriptedApi:
    """The public task API of one in-memory deployment: what Runner talks to, recording every call."""

    def __init__(self, tasks, pending=False, prepare_status=201, readable=None):
        self.tasks = tasks              # task id -> list of payloads returned by successive reads
        self.readable = readable or {}  # tasks that exist already (seeds): readable, never created
        self.reads = {}
        self.calls = []
        self.pending = pending
        self.prepare_status = prepare_status
        self.bytes = {}                 # url -> bytes
        self.created = 0
        self.quotes = {}

    def json(self, method, path, body=None, headers=None, timeout=60):
        self.calls.append((method, path, body, headers))
        if (method, path) == ("GET", "/api/v1/schema"):
            return 200, {"schema": {"runtime": {"http": {"routes": {"task_grant_decision": "/api/v1/tasks/grants/{requestID}"}}},
                                    "chat": {"api": {"fields": {"decision": "decision"}}}}}
        if (method, path) == ("POST", "/api/v1/tasks/prepare"):
            if self.prepare_status != 201:
                return self.prepare_status, {"code": "invalid_input"}
            result = {"prepared_input_id": f"p{len(self.calls)}"}
            quote = self.quotes.get(body["offering_id"])
            if quote:
                result["native_quote"] = {"unit": "meshy.credit", "total_amount": quote}
            return 201, result
        if (method, path) == ("POST", "/api/v1/tasks/grants"):
            return 201, {"request_id": "r1", "status": "pending" if self.pending else "approved"}
        if method == "PUT" and path.startswith("/api/v1/tasks/grants/"):
            return 200, {}
        if (method, path) == ("POST", "/api/v1/tasks"):
            task_id = list(self.tasks)[self.created]
            self.created += 1
            return 202, {"task_id": task_id}
        if method == "GET" and path.startswith("/api/v1/tasks/"):
            task_id = path.rsplit("/", 1)[1]
            payloads = self.tasks.get(task_id) or self.readable.get(task_id)
            if payloads is None:
                return 404, {}
            index = self.reads.get(task_id, 0)
            self.reads[task_id] = index + 1
            return 200, payloads[min(index, len(payloads) - 1)]
        return 404, {}

    def download(self, path, destination):
        data = self.bytes.get(path)
        if data is None:
            return 404, 0
        with open(destination, "wb") as handle:
            handle.write(data)
        return 200, len(data)

    def upload(self, filename, data, media_type):
        self.calls.append(("UPLOAD", filename, len(data), media_type))
        return "asset_" + filename

    def posts(self, path):
        return [call for call in self.calls if call[0] == "POST" and call[1] == path]


def asset(role, variant, data, url):
    return {"role": role, "variant": variant, "required": True, "media_type": "model/gltf-binary" if variant == "glb" else "application/octet-stream",
            "size": len(data), "sha256": hashlib.sha256(data).hexdigest(), "url": url, "id": "id" + url[-2:]}, (url, data)


def succeeded(task_id, *assets):
    return {"id": task_id, "state": "SUCCEEDED", "assets": list(assets)}


class RunnerTest(unittest.TestCase):
    OFFERING = {"id": "x/y/z", "available": True, "estimated_credits": 5, "credits_metered": True,
                "outputs": [{"role": "model", "variant": "glb", "delivery": "required", "minimum": 1}]}

    def setUp(self):
        self.directory = tempfile.mkdtemp(prefix="l5-test-")
        self.addCleanup(lambda: __import__("shutil").rmtree(self.directory, ignore_errors=True))

    def run_steps(self, api, steps, ceiling=100, timeout=5, seeds=None, offering=None):
        arguments = types.SimpleNamespace(task_timeout=timeout, poll=0.001, seeds=seeds or {}, only_steps=[],
                                          multiview_texture="8k", no_texture=False)
        ctx = engine.Context(api, self.directory, arguments, "fake")
        report = common.Report("t")
        offerings = {"x/y/z": offering or self.OFFERING}
        with contextlib.redirect_stdout(io.StringIO()):
            runner = engine.run_chain(ctx, report, steps, offerings, "session", "key", engine.Budget(ceiling))
        return ctx, report, runner

    def step(self, name, needs=(), build=None, **kwargs):
        return engine.Step(name, "x/y/z", "text-to-3d", build or (lambda ctx: {"prompt": name}), needs=needs, **kwargs)

    def good_task(self, task_id, data=None):
        data = data or glb()
        item, binary = asset("model", "glb", data, f"/api/v1/tasks/{task_id}/assets/a1")
        return succeeded(task_id, item), binary

    def test_a_step_price_bounds_the_budget_and_each_submitted_step_is_reported(self):
        # #567: a processing step with fixed export formats is bounded by its whole price, not the base estimate.
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]})
        api.bytes[binary[0]] = binary[1]
        seen = []
        steps = [self.step("a", price=lambda offering, prepared: (engine.Decimal(7), engine.LEGACY_UNIT)),
                 self.step("b", price=lambda offering, prepared: (engine.Decimal(50), engine.LEGACY_UNIT))]
        arguments = types.SimpleNamespace(task_timeout=5, poll=0.001, seeds={}, only_steps=[])
        ctx = engine.Context(api, self.directory, arguments, "fake")
        report = common.Report("t")
        with contextlib.redirect_stdout(io.StringIO()):
            engine.run_chain(ctx, report, steps, {"x/y/z": self.OFFERING}, "s", "k", engine.Budget(40),
                             on_step=lambda ctx, report, record, task: seen.append((record["step"], record["quote"]["amount"])))
        self.assertEqual(seen, [("a", "7")])
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)
        self.assertEqual(ctx.results["b"]["state"], "NOT_SUBMITTED")

    def test_one_submission_per_step_and_the_deliverables_are_checked(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [{"id": "t1", "state": "QUEUED", "assets": []}, {"id": "t1", "state": "RUNNING", "assets": []}, task]})
        api.bytes[binary[0]] = binary[1]
        _, report, runner = self.run_steps(api, [self.step("a")])
        self.assertEqual(report.exit_code(), 0)
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)
        self.assertEqual(len(api.posts("/api/v1/tasks/grants")), 1)
        self.assertEqual(runner.evidence[0]["states"], ["QUEUED", "RUNNING", "SUCCEEDED"])
        self.assertNotIn("url", json.dumps(runner.evidence))
        body = api.posts("/api/v1/tasks")[0][2]
        self.assertEqual(set(body), {"prepared_input_id", "authorization_request_id"})
        self.assertTrue(api.posts("/api/v1/tasks")[0][3]["Idempotency-Key"].startswith("key-"))

    def test_a_pending_grant_is_approved_once_with_the_schema_member(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]}, pending=True)
        api.bytes[binary[0]] = binary[1]
        _, report, _ = self.run_steps(api, [self.step("a")])
        self.assertEqual(report.exit_code(), 0)
        puts = [call for call in api.calls if call[0] == "PUT"]
        self.assertEqual(len(puts), 1)
        self.assertEqual(puts[0][1], "/api/v1/tasks/grants/r1")
        self.assertEqual(puts[0][2], {"decision": "approve"})

    def test_a_failed_task_is_never_sent_again_and_its_dependents_are_skipped(self):
        api = ScriptedApi({"t1": [{"id": "t1", "state": "FAILED", "failure": {"class": "upstream_unavailable", "retryable": True}}]})
        ctx, report, _ = self.run_steps(api, [self.step("a"), self.step("b", needs=("a",))])
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)
        self.assertEqual(report.exit_code(), 1)
        self.assertEqual(report.counts(), {"PASS": 1, "FAIL": 1, "SKIP": 1})
        self.assertEqual(ctx.results["a"]["state"], "FAILED")

    def test_a_task_that_does_not_finish_is_only_observed(self):
        api = ScriptedApi({"t1": [{"id": "t1", "state": "RUNNING", "assets": []}]})
        _, report, runner = self.run_steps(api, [self.step("a")], timeout=0.05)
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)
        self.assertEqual(report.exit_code(), 1)
        self.assertEqual(runner.evidence[0]["state"], "OBSERVING")
        self.assertGreaterEqual(api.reads["t1"], 2)

    def test_a_refused_prepare_sends_nothing_else(self):
        api = ScriptedApi({}, prepare_status=400)
        _, report, runner = self.run_steps(api, [self.step("a")])
        self.assertEqual((api.posts("/api/v1/tasks/grants"), api.posts("/api/v1/tasks")), ([], []))
        self.assertEqual(report.exit_code(), 1)
        self.assertEqual(runner.evidence[0]["state"], "NOT_SUBMITTED")

    def test_the_ceiling_stops_a_step_before_its_grant_and_every_later_step(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task], "t2": [task], "t3": [task]})
        api.bytes[binary[0]] = binary[1]
        ctx, report, runner = self.run_steps(api, [self.step("a"), self.step("b"), self.step("c")], ceiling=7)
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)        # a: 5 credits fits, b would make 10 over 7
        self.assertEqual(len(api.posts("/api/v1/tasks/grants")), 1)
        self.assertEqual(report.counts()["SKIP"], 1)               # c is skipped, not sent
        self.assertTrue(any("budget" in item["detail"] for item in report.items if item["status"] == "FAIL"))
        self.assertEqual(runner.budget.spent, D(5))

    def test_a_native_quote_replaces_the_estimate(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]})
        api.bytes[binary[0]] = binary[1]
        api.quotes["x/y/z"] = "40"
        _, report, runner = self.run_steps(api, [self.step("a")], ceiling=39)
        self.assertEqual(api.posts("/api/v1/tasks"), [])
        self.assertEqual(report.exit_code(), 1)

    def test_a_step_nothing_prices_is_not_sent(self):
        api = ScriptedApi({"t1": []})
        offering = {**self.OFFERING}
        del offering["estimated_credits"]
        _, report, _ = self.run_steps(api, [self.step("a")], offering=offering)
        self.assertEqual(api.posts("/api/v1/tasks/grants"), [])
        self.assertEqual(report.exit_code(), 1)

    def test_a_free_step_runs_without_a_quote(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]})
        api.bytes[binary[0]] = binary[1]
        offering = {**self.OFFERING}
        del offering["estimated_credits"]
        _, report, _ = self.run_steps(api, [self.step("a", free=True)], offering=offering)
        self.assertEqual(report.exit_code(), 0)

    def test_bytes_that_do_not_match_the_declaration_fail_the_deliverables(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]})
        api.bytes[binary[0]] = binary[1] + b"tampered"
        _, report, _ = self.run_steps(api, [self.step("a")])
        failed = [item for item in report.items if item["status"] == "FAIL"]
        self.assertEqual(len(failed), 1)
        self.assertIn("deliverables", failed[0]["item"])

    def test_a_missing_required_slot_fails_the_deliverables(self):
        item, binary = asset("model", "obj", b"o", "/api/v1/tasks/t1/assets/a1")
        api = ScriptedApi({"t1": [succeeded("t1", item)]})
        api.bytes[binary[0]] = binary[1]
        _, report, _ = self.run_steps(api, [self.step("a")])
        self.assertEqual(report.exit_code(), 1)
        self.assertIn("missing model/glb", report.items[-1]["detail"])

    def test_verify_results_become_checks(self):
        task, binary = self.good_task("t1")
        api = ScriptedApi({"t1": [task]})
        api.bytes[binary[0]] = binary[1]
        step = self.step("a", verify=lambda ctx, task: [("shape", True, "fine"), ("size", False, "too big")])
        _, report, _ = self.run_steps(api, [step])
        self.assertEqual(report.counts()["FAIL"], 1)
        self.assertEqual([item["status"] for item in report.items if item["item"] in ("a shape", "a size")], ["PASS", "FAIL"])

    def test_a_step_that_cannot_build_its_input_is_skipped_with_the_reason(self):
        def build(ctx):
            ctx.skip_reason = "no picture"
            return None
        api = ScriptedApi({})
        _, report, _ = self.run_steps(api, [self.step("a", build=build, optional=True)])
        self.assertEqual(report.counts(), {"PASS": 0, "FAIL": 0, "SKIP": 1})
        self.assertEqual(api.posts("/api/v1/tasks/prepare"), [])

    def test_an_unavailable_offering_fails_a_required_step_and_skips_an_optional_one(self):
        api = ScriptedApi({})
        offering = {**self.OFFERING, "available": False}
        _, report, _ = self.run_steps(api, [self.step("a")], offering=offering)
        self.assertEqual(report.counts()["FAIL"], 1)
        _, report, _ = self.run_steps(api, [self.step("a", optional=True)], offering=offering)
        self.assertEqual(report.counts()["SKIP"], 1)

    def test_seeded_sources_are_read_not_run(self):
        task, binary = self.good_task("seed1")
        api = ScriptedApi({"t2": [self.good_task("t2")[0]]}, readable={"seed1": [task]})
        api.bytes[binary[0]] = binary[1]
        second, binary2 = self.good_task("t2")
        api.bytes[binary2[0]] = binary2[1]
        build = lambda ctx: {"source_task": ctx.task_id("a")}  # noqa: E731
        steps = [self.step("a"), self.step("b", needs=("a",), build=build)]
        arguments_seeds = {"a": "seed1"}
        arguments = types.SimpleNamespace(task_timeout=5, poll=0.001, seeds=arguments_seeds, only_steps=["b"],
                                          multiview_texture="8k", no_texture=False)
        ctx = engine.Context(api, self.directory, arguments, "fake")
        report = common.Report("t")
        with contextlib.redirect_stdout(io.StringIO()):
            engine.run_chain(ctx, report, steps, {"x/y/z": self.OFFERING}, "s", "k", engine.Budget(100))
        self.assertEqual(report.exit_code(), 0)
        self.assertEqual(len(api.posts("/api/v1/tasks")), 1)
        prepared = api.posts("/api/v1/tasks/prepare")[0][2]
        self.assertEqual(prepared["input"]["source_task"], "seed1")
        self.assertEqual(engine.cleanup_order([r for r in ctx.results.values() if not r.get("seeded")]), ["t2"])

    def test_a_seed_that_did_not_succeed_is_reported(self):
        api = ScriptedApi({"bad": [{"id": "bad", "state": "FAILED"}]})
        arguments = types.SimpleNamespace(task_timeout=5, poll=0.001, seeds={"a": "bad"}, only_steps=[], multiview_texture="8k", no_texture=False)
        ctx = engine.Context(api, self.directory, arguments, "fake")
        report = common.Report("t")
        with contextlib.redirect_stdout(io.StringIO()):
            engine.run_chain(ctx, report, [], {}, "s", "k", engine.Budget(1))
        self.assertEqual(report.exit_code(), 1)


# ------------------------------------------------------------------ the Meshy chain

class FakeContext:
    """What a build function reads: earlier steps' tasks and assets, uploads, files, the run's arguments."""

    def __init__(self, **options):
        self.arguments = types.SimpleNamespace(multiview_texture=options.get("multiview_texture", "8k"), no_texture=False)
        self.results = options.get("results", {})
        self.uploads = {}
        self.offerings = options.get("offerings", {})
        self.skip_reason = ""
        self.fake = True
        self.files = options.get("files", {})
        self.api = types.SimpleNamespace(upload=lambda filename, data, media_type: "asset_" + filename)

    def task_id(self, name):
        return "task_" + name

    def task(self, name):
        return (self.results.get(name) or {}).get("task") or {}

    def asset_id(self, name, role, variant=None):
        return f"asset_{name}_{role}"

    def file(self, name, role, variant=None):
        return self.files.get((name, role, variant)) or self.files.get((name, role, None))


class MeshyChainTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.catalog = catalog_offerings()

    def test_every_step_targets_a_declared_enabled_offering_with_declared_parameters(self):
        for step in meshy.steps():
            offering = self.catalog.get(step.offering)
            self.assertIsNotNone(offering, step.offering)
            self.assertTrue(offering.get("enabled"), step.offering)
            self.assertEqual(offering["capability"], step.capability, step.name)
            declared = {parameter["name"]: parameter for parameter in offering["parameters"]}
            parameters = step.build(FakeContext())
            for name, value in parameters.items():
                self.assertIn(name, declared, f"{step.name}: {name}")
                options = [option["value"] if isinstance(option, dict) else option for option in declared[name].get("options") or []]
                if options and not isinstance(value, list):
                    self.assertTrue(value in options or str(value) in options, f"{step.name}.{name}")
                if isinstance(value, list) and declared[name].get("type") == "string-array":
                    self.assertTrue(set(value) <= set(options), f"{step.name}.{name}")
                if declared[name].get("minimum") is not None and isinstance(value, (int, float)):
                    self.assertGreaterEqual(value, declared[name]["minimum"], f"{step.name}.{name}")
                if declared[name].get("maximum") is not None and isinstance(value, (int, float)):
                    self.assertLessEqual(value, declared[name]["maximum"], f"{step.name}.{name}")
            for parameter in offering["parameters"]:
                if parameter.get("required") and parameter.get("default") is None:
                    self.assertIn(parameter["name"], parameters, f"{step.name} leaves {parameter['name']} out")

    def test_the_sources_are_the_audited_ones(self):
        by_name = {step.name: step for step in meshy.steps()}
        ctx = FakeContext()
        self.assertEqual(by_name["i2i"].build(ctx)["reference_assets"], ["asset_t2i_image"])
        self.assertEqual(by_name["t25_image"].build(ctx)["reference_assets"], ["asset_t2i_image"])
        self.assertEqual(by_name["multiview"].build(ctx)["reference_assets"], ["asset_t2i_image", "asset_i2i_image"])
        self.assertEqual(by_name["remesh"].build(ctx)["source_task"], "task_t25_image")
        for name in ("convert", "retexture", "rigging", "uv_unwrap", "resize", "analyze", "multi_color"):
            self.assertEqual(by_name[name].build(ctx)["source_task"], "task_remesh", name)
        self.assertEqual(by_name["animation"].build(ctx)["source_task"], "task_rigging")
        self.assertNotIn("source_task", by_name["motion"].build(ctx))

    def test_the_requests_are_the_audited_ones(self):
        by_name = {step.name: step for step in meshy.steps()}
        ctx = FakeContext()
        self.assertEqual(by_name["t25_image"].build(ctx)["texture_resolution"], "2k")
        self.assertEqual(by_name["t25_image"].build(ctx)["target_polycount"], 15000)
        self.assertEqual(by_name["remesh"].build(ctx)["target_polycount"], 10000)
        self.assertEqual(by_name["remesh"].build(ctx)["topology"], "triangle")
        self.assertEqual(by_name["convert"].build(ctx)["target_formats"], ["stl", "usdz"])
        self.assertEqual(by_name["resize"].build(ctx)["resize_height"], 1.0)
        self.assertEqual(by_name["analyze"].build(ctx)["intended_print_height_mm"], 50)
        self.assertEqual(by_name["multiview"].build(ctx)["geometry_resolution"], "2k")
        self.assertEqual(by_name["multiview"].build(ctx)["texture_resolution"], "8k")
        self.assertEqual(by_name["multiview"].build(FakeContext(multiview_texture="2k"))["texture_resolution"], "2k")
        self.assertEqual(by_name["multiview_2k"].build(FakeContext())["texture_resolution"], "2k")
        self.assertEqual(by_name["motion"].build(ctx)["duration"], "2")
        self.assertTrue(by_name["analyze"].free)

    def test_the_default_ceiling_covers_the_estimates_with_room_and_not_the_fallback(self):
        total = sum(self.catalog[step.offering].get("estimated_credits") or 0 for step in meshy.steps()
                    if step.offering != meshy.MULTIVIEW and not step.optional)
        total += 40  # the multi-image frozen quote
        self.assertLessEqual(total, meshy.DEFAULT_CEILING)
        self.assertGreater(meshy.DEFAULT_CEILING, total)
        self.assertLess(meshy.DEFAULT_CEILING, total + 40 + 35)

    def test_the_2k_fallback_only_follows_a_definite_refusal(self):
        by_name = {step.name: step for step in meshy.steps()}
        fallback = by_name["multiview_2k"]
        self.assertTrue(fallback.optional)

        def ctx_with(state, failure):
            return FakeContext(results={"multiview": {"state": state, "task": {"failure": failure} if failure else {}}})

        self.assertTrue(fallback.only_if(ctx_with("FAILED", {"class": "invalid_input", "ambiguous": False})))
        self.assertTrue(fallback.only_if(ctx_with("FAILED", {"class": "rejected"})))
        self.assertFalse(fallback.only_if(ctx_with("FAILED", {"class": "invalid_input", "ambiguous": True})))
        self.assertFalse(fallback.only_if(ctx_with("FAILED", {"class": "upstream_unavailable"})))
        self.assertFalse(fallback.only_if(ctx_with("FAILED", {"class": "submission_unknown", "ambiguous": True})))
        self.assertFalse(fallback.only_if(ctx_with("SUCCEEDED", None)))
        self.assertFalse(fallback.only_if(FakeContext()))
        asked_2k = ctx_with("FAILED", {"class": "invalid_input"})
        asked_2k.arguments.multiview_texture = "2k"
        self.assertFalse(fallback.only_if(asked_2k))

    def test_checks(self):
        with Directory() as directory:
            ctx = FakeContext(files={("remesh", "model", "glb"): write(directory, "r.glb", glb(triangles=20000))})
            ctx.fake = False
            checks = {label: ok for label, ok, _ in meshy.glb_checks(ctx, "remesh", bound=10000, textured=False)}
            self.assertTrue(checks["GLB readable"])
            self.assertFalse(checks["GLB triangles"])           # a real upstream is held to the bound
            ctx.fake = True
            checks = {label: ok for label, ok, _ in meshy.glb_checks(ctx, "remesh", bound=10000, textured=False)}
            self.assertTrue(checks["GLB triangles"])            # the fake's model is one fixed fixture
            ctx.files[("resize", "model", "glb")] = write(directory, "z.glb", glb(height=1.02))
            ctx.fake = False
            self.assertTrue(dict((l, o) for l, o, _ in meshy.glb_checks(ctx, "resize", textured=False, height=1.0))["GLB height"])
            ctx.files[("resize", "model", "glb")] = write(directory, "z.glb", glb(height=2.0))
            self.assertFalse(dict((l, o) for l, o, _ in meshy.glb_checks(ctx, "resize", textured=False, height=1.0))["GLB height"])
            self.assertFalse(meshy.glb_checks(FakeContext(), "missing")[0][1])
        task = {"assets": [{"role": "model", "variant": "stl"}, {"role": "model", "variant": "usdz"}]}
        self.assertTrue(meshy.check_formats(task, ("stl", "usdz"))[1])
        self.assertFalse(meshy.check_formats(task, ("stl", "glb"))[1])
        self.assertEqual(meshy.model_formats(task), ["stl", "usdz"])
        self.assertTrue(meshy.verify_rigging(None, {"assets": [{"variant": "rigged_character_glb_url"}, {"variant": "rigged_character_fbx_url"}]})[0][1])
        self.assertFalse(meshy.verify_rigging(None, {"assets": [{"variant": "rigged_character_glb_url"}]})[0][1])

    def test_the_evidence_lists_the_cleanup_with_upstream_paths(self):
        evidence = {"steps": [{"step": "t25_text", "task_id": "tk1", "offering": meshy.T25_TEXT},
                              {"step": "remesh", "task_id": "tk2", "offering": meshy.REMESH},
                              {"step": "x", "task_id": "tk3", "offering": "unknown/offering"}],
                    "cleanup_order": ["tk3", "tk2", "tk1"]}
        meshy.add_cleanup(None, None, evidence)
        self.assertEqual(evidence["cleanup"], [{"task_id": "tk3", "path": ""}, {"task_id": "tk2", "path": "/openapi/v1/remesh"},
                                               {"task_id": "tk1", "path": "/openapi/v2/text-to-3d"}])

    def test_every_chain_offering_has_an_upstream_path(self):
        for step in meshy.steps():
            self.assertIn(step.offering, meshy.UPSTREAM_PATH)


# ------------------------------------------------------------------ the Meshy clean-up

class DeleteRecorder(http.server.BaseHTTPRequestHandler):
    log = []
    answers = {}

    def do_DELETE(self):
        DeleteRecorder.log.append((self.path, self.headers.get("Authorization")))
        self.send_response(DeleteRecorder.answers.get(self.path, 200))
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, *args):
        pass


class MeshyCleanupTest(unittest.TestCase):
    EVIDENCE = {"cleanup": [{"task_id": "child", "path": "/openapi/v1/remesh"}, {"task_id": "preview", "path": "/openapi/v2/text-to-3d"},
                            {"task_id": "lost", "path": "/openapi/v1/convert"}, {"task_id": "pathless", "path": ""}]}
    MAP = {"child": ["c1"], "preview": ["p1", "r1"], "pathless": ["x"]}

    def test_the_plan_deletes_dependents_first_and_the_refine_before_its_preview(self):
        plan, problems = meshy.cleanup_plan(self.EVIDENCE, self.MAP)
        self.assertEqual(plan, [("child", "/openapi/v1/remesh/c1"), ("preview", "/openapi/v2/text-to-3d/r1"),
                                ("preview", "/openapi/v2/text-to-3d/p1")])
        self.assertEqual(problems, ["lost: no upstream ID in the map", "pathless: no upstream path"])

    def test_ids_are_path_encoded(self):
        plan, _ = meshy.cleanup_plan({"cleanup": [{"task_id": "t", "path": "/p"}]}, {"t": ["a/b?c"]})
        self.assertEqual(plan, [("t", "/p/a%2Fb%3Fc")])

    def test_refusals(self):
        key = {"MESHY_API_KEY": "k"}
        self.assertEqual(meshy.cleanup_refusal("https://api.meshy.ai", False, {}), "MESHY_API_KEY is not set")
        self.assertIn("ART_AI_LIVE_ACK=1", meshy.cleanup_refusal("https://api.meshy.ai", True, key))
        self.assertIn("--live", meshy.cleanup_refusal("https://api.meshy.ai", False, {**key, "ART_AI_LIVE_ACK": "1"}))
        self.assertIsNotNone(meshy.cleanup_refusal("https://api.meshy.ai", False, key))
        self.assertIsNone(meshy.cleanup_refusal("https://api.meshy.ai", True, {**key, "ART_AI_LIVE_ACK": "1"}))
        self.assertIsNone(meshy.cleanup_refusal("http://127.0.0.1:19410/meshy", False, key))
        self.assertIn("loopback fake", meshy.cleanup_refusal("http://127.0.0.1:19410/meshy", True, key))
        self.assertIn("origin", meshy.cleanup_refusal("ftp://x", False, key))
        self.assertIn("origin", meshy.cleanup_refusal("https://u:p@api.meshy.ai", False, key))

    def serve(self):
        DeleteRecorder.log, DeleteRecorder.answers = [], {}
        server = http.server.HTTPServer(("127.0.0.1", 0), DeleteRecorder)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(lambda: (server.shutdown(), server.server_close()))
        return f"http://127.0.0.1:{server.server_address[1]}"

    def test_the_cleanup_command_sends_each_delete_once_in_order(self):
        base = self.serve()
        DeleteRecorder.answers = {"/openapi/v1/remesh/c1": 404}      # already gone is fine
        with Directory() as directory:
            evidence = write(directory, "e.json", json.dumps({"cleanup": self.EVIDENCE["cleanup"][:2]}).encode())
            remote = write(directory, "m.json", json.dumps(self.MAP).encode())
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = meshy.main(["--cleanup", evidence, "--remote-ids", remote], {"MESHY_API_KEY": "fake-key", "MESHY_BASE_URL": base})
        self.assertEqual(code, 0)
        self.assertEqual([path for path, _ in DeleteRecorder.log],
                         ["/openapi/v1/remesh/c1", "/openapi/v2/text-to-3d/r1", "/openapi/v2/text-to-3d/p1"])
        self.assertEqual({auth for _, auth in DeleteRecorder.log}, {"Bearer fake-key"})
        self.assertNotIn("fake-key", out.getvalue())

    def test_a_conflict_is_reported_and_not_retried(self):
        base = self.serve()
        DeleteRecorder.answers = {"/openapi/v1/remesh/c1": 409}
        with Directory() as directory:
            evidence = write(directory, "e.json", json.dumps({"cleanup": self.EVIDENCE["cleanup"][:1]}).encode())
            remote = write(directory, "m.json", json.dumps(self.MAP).encode())
            with contextlib.redirect_stdout(io.StringIO()):
                code = meshy.main(["--cleanup", evidence, "--remote-ids", remote], {"MESHY_API_KEY": "k", "MESHY_BASE_URL": base})
        self.assertEqual(code, 1)
        self.assertEqual(len(DeleteRecorder.log), 1)

    def test_a_real_base_without_the_switches_sends_nothing(self):
        with Directory() as directory:
            evidence = write(directory, "e.json", b"{}")
            remote = write(directory, "m.json", b"{}")
            err = io.StringIO()
            with contextlib.redirect_stderr(err):
                code = meshy.main(["--cleanup", evidence, "--remote-ids", remote], {"MESHY_API_KEY": "k"})
        self.assertEqual(code, 2)
        self.assertIn("refusing to run", err.getvalue())

    def test_the_cleanup_needs_its_map(self):
        base = self.serve()
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(meshy.main(["--cleanup", "e.json"], {"MESHY_API_KEY": "k", "MESHY_BASE_URL": base}), 2)
        self.assertIn("--remote-ids", err.getvalue())


# ------------------------------------------------------------------ the Hyper3D chain

class Hyper3dChainTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.catalog = catalog_offerings()

    def test_every_step_targets_a_declared_enabled_offering_with_declared_parameters(self):
        with Directory() as directory:
            model = write(directory, "model.glb", glb())
            ctx = FakeContext(files={("text", "model", "glb"): model})
            for step in hyper3d.steps():
                offering = self.catalog.get(step.offering)
                self.assertIsNotNone(offering, step.offering)
                self.assertTrue(offering.get("enabled"), step.offering)
                self.assertEqual(offering["capability"], step.capability, step.name)
                declared = {parameter["name"]: parameter for parameter in offering["parameters"]}
                parameters = step.build(ctx)
                self.assertIsNotNone(parameters, step.name)
                for name, value in parameters.items():
                    self.assertIn(name, declared, f"{step.name}: {name}")
                    options = [option["value"] if isinstance(option, dict) else option for option in declared[name].get("options") or []]
                    if options and not isinstance(value, list):
                        self.assertIn(value, options, f"{step.name}.{name}")
                    if declared[name].get("maximum") is not None and isinstance(value, (int, float)) and not isinstance(value, bool):
                        self.assertLessEqual(value, declared[name]["maximum"], f"{step.name}.{name}")
                for parameter in offering["parameters"]:
                    if parameter.get("required") and parameter.get("default") is None:
                        self.assertIn(parameter["name"], parameters, f"{step.name} leaves {parameter['name']} out")

    def test_the_requests_are_the_audited_ones(self):
        by_name = {step.name: step for step in hyper3d.steps()}
        text = by_name["text"].build(FakeContext())
        self.assertEqual({key: text[key] for key in ("mesh_mode", "quality_override", "texture_mode", "TAPose", "quad_normal", "seed")},
                         {"mesh_mode": "Quad", "quality_override": 10000, "texture_mode": "high", "TAPose": True, "quad_normal": True, "seed": 1})
        short = by_name["image_short"].build(FakeContext())
        self.assertEqual(short["reference_image_labels"], ["F"])
        self.assertEqual(len(short["reference_assets"]), 2)
        self.assertEqual(by_name["bang_task"].build(FakeContext())["source_task"], "task_text")

    def test_the_upload_steps_use_the_text_glb_and_skip_when_it_is_too_big_or_missing(self):
        with Directory() as directory:
            model = write(directory, "model.glb", glb())
            ctx = FakeContext(files={("text", "model", "glb"): model},
                              offerings={hyper3d.BANG_LOCAL: {"parameters": [{"name": "source_asset", "max_bytes": 10}]}})
            self.assertIsNone(hyper3d.build_bang_upload(ctx))
            self.assertIn("over the 10-byte upload limit", ctx.skip_reason)
            ctx = FakeContext(files={("text", "model", "glb"): model})
            built = hyper3d.build_bang_upload(ctx)
            self.assertEqual(built["source_asset"], "asset_model.glb")
            self.assertEqual(built["material"], "PBR")
            self.assertEqual(hyper3d.build_texture(ctx)["source_asset"], "asset_model.glb")
            self.assertEqual(ctx.uploads["picture"], "asset_reference.png")   # one picture, uploaded once, shared
            ctx = FakeContext()
            self.assertIsNone(hyper3d.build_texture(ctx))
            self.assertIn("no GLB", ctx.skip_reason)

    def test_texture_can_be_left_out(self):
        texture = {step.name: step for step in hyper3d.steps()}["texture"]
        ctx = FakeContext()
        ctx.arguments.no_texture = True
        self.assertFalse(texture.only_if(ctx))
        self.assertEqual(ctx.skip_reason, "--no-texture")
        ctx.arguments.no_texture = False
        self.assertTrue(texture.only_if(ctx))

    def test_the_text_glb_must_follow_the_request(self):
        with Directory() as directory:
            for triangles, expected in ((21800, True), (10000, False), (60000, False)):
                ctx = FakeContext(files={("text", "model", "glb"): write(directory, "m.glb", glb(triangles=triangles))})
                checks = {label: ok for label, ok, _ in hyper3d.verify_text(ctx, {})}
                self.assertEqual(checks["GLB triangles follow the request"], expected, triangles)
            self.assertFalse(hyper3d.verify_text(FakeContext(), {})[0][1])

    def test_a_segmented_model_needs_parts_on_a_real_upstream_only(self):
        with Directory() as directory:
            ctx = FakeContext(files={("bang_task", "model", "glb"): write(directory, "m.glb", glb(meshes=1))})
            check = hyper3d.verify_bang("bang_task")(ctx, {})[1]
            self.assertTrue(check[1])
            ctx.fake = False
            self.assertFalse(hyper3d.verify_bang("bang_task")(ctx, {})[1][1])
            ctx.files[("bang_task", "model", "glb")] = write(directory, "m2.glb", glb(meshes=4))
            self.assertTrue(hyper3d.verify_bang("bang_task")(ctx, {})[1][1])

    def test_the_default_ceiling_covers_the_chain(self):
        total = D("1.0") + D("0.5") + D("0.5") + D("2.0") + D("1.0")
        self.assertGreater(hyper3d.DEFAULT_CEILING, total)
        self.assertLessEqual(hyper3d.DEFAULT_CEILING, D("7"))


if __name__ == "__main__":
    unittest.main()
