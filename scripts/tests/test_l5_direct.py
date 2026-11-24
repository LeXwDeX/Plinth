#!/usr/bin/env python3
"""Offline tests of the direct L5 scripts of #540: smoke-l5-2d-direct.py, smoke-l5-matting.py, matting-probe.py
and their shared l5_direct.py. No network, no server: the HTTP side is replaced by scripted answers."""
import contextlib
import importlib.util
import io
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import types
import unittest

sys.dont_write_bytecode = True
ROOT = pathlib.Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts" / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, SMOKE / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


common = load("l5_common", "l5_common.py")
direct = load("l5_direct", "l5_direct.py")
two_d = load("smoke_l5_2d_direct", "smoke-l5-2d-direct.py")
matting = load("smoke_l5_matting", "smoke-l5-matting.py")
probe = load("matting_probe", "matting-probe.py")

CATALOG = json.loads((ROOT / "internal" / "catalog" / "default_catalog.json").read_text(encoding="utf-8"))
OFFERINGS = {offering["id"]: offering for offering in CATALOG["offerings"]}
ASSETS = {"ref_a": "asset_a", "ref_b": "asset_b", "mask": "asset_mask"}
SCRIPTS = ("smoke-l5-2d-direct.py", "smoke-l5-matting.py", "matting-probe.py")


class Scripted:
    """A stand-in for the l5_common Client: answers from a function and records every call."""

    def __init__(self, answer):
        self.answer = answer
        self.calls = []

    def call(self, method, path, body=None, timeout=60, headers=None):
        self.calls.append((method, path, body, headers))
        return self.answer(method, path, body, headers)

    def fetch(self, path, limit=0):
        self.calls.append(("GET", path, None, None))
        return self.answer("GET", path, None, None)


class StubLive:
    def __init__(self, answer, shape="live", timeout=30):
        self.client = Scripted(answer)
        self.shape = shape
        self.arguments = types.SimpleNamespace(task_timeout=timeout)
        self.approvals = []

    def decide_grant(self, request_id, approve):
        self.approvals.append(request_id)
        return 200, {}

    def tasks(self, session):
        status, data = self.client.call("GET", f"/api/v1/tasks?session_id={session}&limit=50")
        return data.get("tasks") or []

    def posts(self, path):
        return [call for call in self.client.calls if call[0] == "POST" and call[1] == path]


def accepting(task=None, grant_status="approved"):
    """A server that takes the prepare, grant and submit of one Task and serves the given Task."""
    def answer(method, path, body, headers):
        if path == "/api/v1/tasks/prepare":
            return 201, {"prepared_input_id": "prep_1", "native_quote": {"unit": "usd", "total_amount": "1"}}
        if path == "/api/v1/tasks/grants":
            return 201, {"request_id": "req_1", "status": grant_status}
        if path == "/api/v1/tasks":
            return 202, {"task_id": "task_1"}
        if path == "/api/v1/tasks/task_1":
            return 200, task or {"id": "task_1", "state": "SUCCEEDED", "assets": []}
        if path.startswith("/api/v1/tasks?"):
            return 200, {"tasks": []}
        return 404, {}
    return answer


# ------------------------------------------------------------------ gates

class GateTest(unittest.TestCase):
    """Every direct script is fake-default: nothing real without both live switches, nothing is sent first."""

    def run_script(self, name, args=(), **environment):
        env = {"PATH": "/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE": "1", "BASE": "https://example.test",
               "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p", "MATTING_BASE_URL": "https://example.test",
               "MATTING_API_KEY": "k"}
        env.update(environment)
        return subprocess.run([sys.executable, str(SMOKE / name), *args], env=env, capture_output=True, text=True,
                              timeout=60)

    def test_an_unknown_target_counts_as_live_and_needs_both_switches(self):
        for name in SCRIPTS:
            result = self.run_script(name)
            self.assertEqual(result.returncode, 2, name)
            self.assertIn("ART_AI_LIVE_ACK=1", result.stderr, name)
            self.assertIn("--live", result.stderr, name)

    def test_one_switch_alone_refuses(self):
        for name in SCRIPTS:
            only_flag = self.run_script(name, ["--live"])
            self.assertEqual(only_flag.returncode, 2, name)
            self.assertIn("ART_AI_LIVE_ACK=1", only_flag.stderr, name)
            only_ack = self.run_script(name, ART_AI_LIVE_ACK="1")
            self.assertEqual(only_ack.returncode, 2, name)
            self.assertIn("--live", only_ack.stderr, name)

    def test_live_on_a_fake_target_refuses(self):
        for name in SCRIPTS:
            result = self.run_script(name, ["--live"], ART_AI_UPSTREAM_SHAPE="fake", ART_AI_LIVE_ACK="1")
            self.assertEqual(result.returncode, 2, name)
            self.assertIn("fake upstream", result.stderr, name)

    def test_a_mixed_target_needs_both_switches_too(self):
        for name in SCRIPTS:
            result = self.run_script(name, ART_AI_UPSTREAM_SHAPE="mixed")
            self.assertEqual(result.returncode, 2, name)

    def test_the_content_probe_needs_the_third_switch(self):
        live = {"ART_AI_UPSTREAM_SHAPE": "live", "ART_AI_LIVE_ACK": "1"}
        with tempfile.NamedTemporaryFile("w", suffix=".txt") as prompt:
            prompt.write("a prompt the user chose")
            prompt.flush()
            args = ["--live", "--content-probe", "--provider", "openai", "--probe-prompt-file", prompt.name]
            result = self.run_script("smoke-l5-2d-direct.py", args, **live)
            self.assertEqual(result.returncode, 2)
            self.assertIn("ART_AI_CONTENT_PROBE_ACK=1", result.stderr)
            self.assertNotIn("a prompt the user chose", result.stderr + result.stdout)

    def test_the_content_probe_never_runs_on_a_fake_target(self):
        with tempfile.NamedTemporaryFile("w", suffix=".txt") as prompt:
            prompt.write("x")
            prompt.flush()
            args = ["--content-probe", "--provider", "gemini", "--probe-prompt-file", prompt.name]
            result = self.run_script("smoke-l5-2d-direct.py", args, ART_AI_UPSTREAM_SHAPE="fake",
                                     ART_AI_CONTENT_PROBE_ACK="1")
            self.assertEqual(result.returncode, 2)
            self.assertIn("never blocks content", result.stderr)

    def test_the_default_run_has_no_content_probe(self):
        # The probe options alone, without --content-probe, are refused rather than ignored.
        result = self.run_script("smoke-l5-2d-direct.py", ["--provider", "openai"], ART_AI_UPSTREAM_SHAPE="fake")
        self.assertEqual(result.returncode, 2)
        self.assertIn("only apply to --content-probe", result.stderr)

    def test_missing_environment_refuses_before_any_request(self):
        result = self.run_script("matting-probe.py", ART_AI_UPSTREAM_SHAPE="fake", MATTING_BASE_URL="")
        self.assertEqual(result.returncode, 2)
        self.assertIn("MATTING_BASE_URL", result.stderr)
        result = self.run_script("matting-probe.py", ART_AI_UPSTREAM_SHAPE="fake", MATTING_BASE_URL="ftp://x")
        self.assertEqual(result.returncode, 2)

    def test_the_shared_gate_is_the_default_of_every_other_script(self):
        # execute() keeps "live-only" for the scripts that drive the Agent: a fake target stays refused.
        env = {"PATH": "/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE": "1", "BASE": "https://example.test",
               "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p", "ART_AI_UPSTREAM_SHAPE": "fake"}
        result = subprocess.run([sys.executable, str(SMOKE / "smoke-l5-parallel.py")], env=env, capture_output=True,
                                text=True, timeout=60)
        self.assertEqual(result.returncode, 2)


class ShapeTest(unittest.TestCase):
    def test_a_mixed_target_has_fake_generation_so_it_is_judged_leniently(self):
        self.assertTrue(direct.generation_is_fake("fake"))
        self.assertTrue(direct.generation_is_fake("mixed"))
        self.assertFalse(direct.generation_is_fake("live"))
        self.assertFalse(direct.generation_is_fake(None))


class ContentProbeRefusalTest(unittest.TestCase):
    def arguments(self, **override):
        values = {"content_probe": True, "provider": "openai", "probe_prompt_file": "", "large": False, "only": ""}
        values.update(override)
        return types.SimpleNamespace(**values)

    def test_every_missing_piece_refuses(self):
        with tempfile.NamedTemporaryFile("w") as prompt:
            ok = self.arguments(probe_prompt_file=prompt.name)
            environ = {direct.CONTENT_PROBE_ACK: "1"}
            providers = two_d.PROVIDERS
            self.assertIsNone(direct.content_probe_refusal(ok, "live", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(ok, "live", {}, providers))
            self.assertIsNotNone(direct.content_probe_refusal(ok, "fake", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(ok, "mixed", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(self.arguments(probe_prompt_file=prompt.name, provider=""),
                                                              "live", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(self.arguments(), "live", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(self.arguments(probe_prompt_file="/nope"), "live", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(self.arguments(probe_prompt_file=prompt.name, large=True),
                                                              "live", environ, providers))
            self.assertIsNotNone(direct.content_probe_refusal(self.arguments(probe_prompt_file=prompt.name, only="a"),
                                                              "live", environ, providers))

    def test_prompt_file_limits(self):
        with tempfile.TemporaryDirectory() as directory:
            empty = os.path.join(directory, "empty.txt")
            open(empty, "w").close()
            with self.assertRaises(ValueError):
                direct.load_probe_prompt(empty)
            big = os.path.join(directory, "big.txt")
            with open(big, "w") as handle:
                handle.write("x" * (direct.MAX_PROBE_PROMPT_BYTES + 1))
            with self.assertRaises(ValueError):
                direct.load_probe_prompt(big)
            good = os.path.join(directory, "good.txt")
            with open(good, "w") as handle:
                handle.write("  some words  \n")
            text, digest, length = direct.load_probe_prompt(good)
            self.assertEqual((text, length), ("some words", 10))
            self.assertEqual(len(digest), 64)


class ContentProbeRunTest(unittest.TestCase):
    PROMPT = "the-secret-probe-prompt-words"

    def test_the_prompt_is_sent_once_and_never_printed_or_stored(self):
        task = {"id": "task_1", "state": "FAILED", "failure": {"class": "content_blocked", "detail": "blocked"},
                "input": {"parameters": {"prompt": self.PROMPT, "size": "1024x1024"}}}
        live = StubLive(accepting(task))
        report = common.Report("probe")
        findings = direct.Findings(report, True)
        with tempfile.TemporaryDirectory() as directory:
            prompt_file = os.path.join(directory, "prompt.txt")
            with open(prompt_file, "w") as handle:
                handle.write(self.PROMPT)
            arguments = types.SimpleNamespace(provider="openai", probe_prompt_file=prompt_file, task_timeout=30)
            private = direct.PrivateDir()
            output = io.StringIO()
            try:
                with contextlib.redirect_stdout(output):
                    evidence = two_d.run_content_probe(live, report, findings, private, "session_1", arguments)
                kept = []
                for name in private.written:
                    with open(os.path.join(private.path, name), encoding="utf-8") as handle:
                        kept.append(handle.read())
            finally:
                private.close()
        self.assertEqual(len(live.posts("/api/v1/tasks")), 1)
        self.assertNotIn(self.PROMPT, output.getvalue())
        self.assertNotIn(self.PROMPT, json.dumps(evidence))
        # nothing on disk carries it either: the server echoes the frozen input, the script keeps a summary
        self.assertTrue(all(self.PROMPT not in text for text in kept))
        self.assertTrue(all(self.PROMPT not in json.dumps(item) for item in report.items))
        self.assertEqual(evidence["prompt_length"], len(self.PROMPT))
        self.assertEqual(len(evidence["prompt_sha256_prefix"]), 12)
        self.assertEqual(evidence["task"]["failure"]["class"], "content_blocked")
        self.assertEqual(report.counts()["FAIL"], 0)
        self.assertEqual(len(kept), 1)


# ------------------------------------------------------------------ cases and the Catalog

class Constraint:
    """The ImageSizeConstraint check of internal/schema/image_size.go."""

    def __init__(self, spec):
        self.spec = spec

    def problem(self, size):
        width, height = (int(part) for part in size.split("x"))
        spec = self.spec
        if spec.get("multiple") and (width % spec["multiple"] or height % spec["multiple"]):
            return "multiple"
        if spec.get("max_edge") and max(width, height) > spec["max_edge"]:
            return "edge"
        if not spec["min_pixels"] <= width * height <= spec["max_pixels"]:
            return "pixels"
        if max(width / height, height / width) > spec["max_ratio"]:
            return "ratio"
        return None


class CaseTableTest(unittest.TestCase):
    def test_the_seven_tasks_are_the_planned_ones(self):
        self.assertEqual([case["key"] for case in two_d.CASES],
                         ["openai-generate", "openai-edits", "gemini-generate", "gemini-edit", "jimeng-generate",
                          "jimeng-edit", "jimeng-sequence"])
        self.assertEqual([case["provider"] for case in two_d.CASES], ["openai", "openai", "gemini", "gemini", "jimeng", "jimeng", "jimeng"])

    def test_every_input_is_a_valid_input_of_its_catalog_offering(self):
        for large in (False, True):
            for case in two_d.CASES:
                with self.subTest(case=case["key"], large=large):
                    payload, reasons = two_d.build_input(case, ASSETS, large)
                    offering = OFFERINGS[case["offering"]]
                    self.assertEqual(offering["capability"], case["capability"])
                    self.check_against(offering, payload, reasons or {})

    def check_against(self, offering, payload, reasons):
        declared = {parameter["name"]: parameter for parameter in offering["parameters"]}
        for name in (n for n, parameter in declared.items() if parameter.get("required")):
            self.assertIn(name, payload, f"required {name} missing")
        for name, value in payload.items():
            self.assertIn(name, declared, f"{name} is not a parameter of {offering['id']}")
            parameter = declared[name]
            kind = parameter["type"]
            options = [option["value"] for option in parameter.get("options") or []]
            if kind == "select":
                self.assertIn(value, options, name)
            elif kind == "text" and parameter.get("max_length"):
                self.assertLessEqual(len(value), parameter["max_length"], name)
            elif kind == "text" and options:
                constraint = parameter.get("image_size")
                self.assertTrue(value in options or (constraint and Constraint(constraint).problem(value) is None),
                                f"{name}={value}")
            elif kind == "number":
                self.assertTrue(parameter.get("minimum", value) <= value <= parameter.get("maximum", value), name)
                self.assertIsInstance(value, int)
            elif kind == "asset-array":
                self.assertTrue(parameter.get("min_items", 0) <= len(value) <= parameter.get("max_items", 99), name)
            for rule in parameter.get("forced_when") or []:
                condition = rule["when"]
                if condition["parameter"] == name and condition["operator"] == "equals" and condition["value"] == value:
                    self.assertEqual(rule["value"], value, f"{name}={value} would be rewritten to {rule['value']}")
        # an advised parameter that differs from its default carries a reason, and only those do
        for name, value in payload.items():
            parameter = declared[name]
            if parameter.get("adjustment_policy") == "advised" and value != parameter.get("default"):
                self.assertIn(name, reasons, f"advised {name} changed without a reason")
        for name in reasons:
            self.assertEqual(declared[name].get("adjustment_policy"), "advised", f"reason for non-advised {name}")
            self.assertNotEqual(payload[name], declared[name].get("default"), f"reason for unchanged {name}")
        for rule in offering.get("input_rules") or []:
            total = sum(len(payload[name]) if isinstance(payload.get(name), list) else int(payload.get(name, 0))
                        for name in rule["parameters"] if name in payload)
            self.assertLessEqual(total, rule["max_total_items"])

    def test_the_large_variants_only_change_gemini_and_jimeng_sizes(self):
        small = {case["key"]: two_d.build_input(case, ASSETS, False)[0] for case in two_d.CASES}
        large = {case["key"]: two_d.build_input(case, ASSETS, True)[0] for case in two_d.CASES}
        self.assertEqual(small["gemini-generate"]["image_size"], "1K")
        self.assertEqual(large["gemini-generate"]["image_size"], "4K")
        self.assertEqual(small["jimeng-generate"]["size"], "1920x1920")
        self.assertEqual(large["jimeng-generate"]["size"], "4096x4096")
        self.assertEqual(large["jimeng-edit"]["size"], "4096x4096")
        for key in ("openai-generate", "openai-edits", "gemini-edit", "jimeng-sequence"):
            self.assertEqual(small[key], large[key], key)

    def test_the_default_run_never_asks_for_4k_or_4096(self):
        for case in two_d.CASES:
            payload, _ = two_d.build_input(case, ASSETS, False)
            self.assertNotEqual(payload.get("image_size"), "4K")
            self.assertNotEqual(payload.get("size"), "4096x4096")

    def test_the_reference_pictures_follow_the_case(self):
        for case in two_d.CASES:
            payload, _ = two_d.build_input(case, ASSETS, False)
            self.assertEqual(len(payload.get("reference_assets", [])), case.get("refs", 0))
        self.assertEqual(two_d.build_input(two_d.CASES[1], ASSETS, False)[0]["mask"], "asset_mask")

    def test_the_mask_matches_the_first_reference_size(self):
        self.assertEqual(direct.describe_image(direct.mask_png(two_d.REFERENCE_SIZE))["width"], two_d.REFERENCE_SIZE)
        self.assertEqual(direct.describe_image(direct.subject_png(two_d.REFERENCE_SIZE))["width"], two_d.REFERENCE_SIZE)

    def test_prompts_fit_the_offering_limits(self):
        for case in two_d.CASES:
            payload, _ = two_d.build_input(case, ASSETS, False)
            self.assertTrue(payload["prompt"].isascii())

    def test_probe_inputs_are_valid_offering_inputs(self):
        for provider in two_d.PROVIDERS:
            offering, payload, reasons = two_d.probe_input(provider)
            payload = dict(payload, prompt="x")
            CaseTableTest.check_against(self, OFFERINGS[offering], payload, reasons or {})

    def test_estimates_are_declared_by_the_catalog_unit(self):
        # the unit parameter of the Catalog's price is n or max_images: the cases that raise it say so
        self.assertEqual(OFFERINGS["openai/gpt-image-2/text-to-image"]["estimated_credits_unit_parameter"], "n")
        self.assertEqual(OFFERINGS["jimeng/seedream-4-5/text-to-image"]["estimated_credits_unit_parameter"], "max_images")


class PlanTest(unittest.TestCase):
    def test_the_default_round_fits_the_default_budget(self):
        run, skipped, spent = two_d.plan_run(two_d.CASES, False, two_d.DEFAULT_BUDGET_USD, two_d.DEFAULT_MAX_TASKS)
        self.assertEqual(len(run), 7)
        self.assertEqual(skipped, [])
        self.assertAlmostEqual(spent, 0.468, places=3)

    def test_large_costs_more_and_still_fits(self):
        _, _, small = two_d.plan_run(two_d.CASES, False, 10, 7)
        run, _, large = two_d.plan_run(two_d.CASES, True, two_d.DEFAULT_BUDGET_USD, 7)
        self.assertGreater(large, small)
        self.assertEqual(len(run), 7)

    def test_a_task_over_the_budget_is_skipped_and_never_run(self):
        run, skipped, spent = two_d.plan_run(two_d.CASES, False, 0.2, 7)
        self.assertLessEqual(spent, 0.2)
        self.assertTrue(skipped)
        self.assertTrue({case["key"] for case in run}.isdisjoint({case["key"] for case, _ in skipped}))
        self.assertIn("budget", skipped[0][1])

    def test_the_task_ceiling_stops_the_run(self):
        run, skipped, _ = two_d.plan_run(two_d.CASES, False, 10, 3)
        self.assertEqual(len(run), 3)
        self.assertEqual(len(skipped), 4)
        self.assertIn("ceiling", skipped[0][1])

    def test_only_selects_in_table_order_and_rejects_unknown_keys(self):
        self.assertEqual([case["key"] for case in two_d.select_cases("jimeng-edit, openai-generate")],
                         ["openai-generate", "jimeng-edit"])
        self.assertEqual(len(two_d.select_cases("")), 7)
        with self.assertRaises(ValueError):
            two_d.select_cases("nope")


# ------------------------------------------------------------------ pictures, directory, evidence

class PictureTest(unittest.TestCase):
    def test_png_builders_describe_themselves(self):
        subject = direct.describe_image(direct.subject_png(64))
        self.assertEqual((subject["format"], subject["width"], subject["height"], subject["alpha"]), ("png", 64, 64, False))
        translucent = direct.describe_image(direct.translucent_png(64))
        self.assertEqual((translucent["color_type"], translucent["alpha"]), (6, True))
        self.assertEqual(direct.describe_image(direct.mask_png(32))["color_type"], 6)

    def test_the_embedded_jpeg_is_a_256_square_jpeg(self):
        info = direct.describe_image(direct.jpeg_256())
        self.assertEqual((info["format"], info["width"], info["height"]), ("jpeg", 256, 256))

    def test_the_mask_is_transparent_in_the_centre_only(self):
        data = direct.mask_png(64)
        # decode the alpha channel through zlib: color type 6, one filter byte per row
        import zlib
        idat = b""
        index = 8
        while index < len(data):
            length = int.from_bytes(data[index:index + 4], "big")
            kind = data[index + 4:index + 8]
            if kind == b"IDAT":
                idat += data[index + 8:index + 8 + length]
            index += 12 + length
        raw = zlib.decompress(idat)
        stride = 64 * 4 + 1
        alpha = lambda x, y: raw[y * stride + 1 + x * 4 + 3]
        self.assertEqual(alpha(32, 32), 0)
        self.assertEqual(alpha(2, 2), 255)
        self.assertEqual(alpha(62, 32), 255)

    def test_the_bad_file_is_not_a_picture(self):
        self.assertEqual(direct.describe_image(direct.not_a_picture())["format"], None)

    def test_pictures_hold_no_text_or_scene(self):
        self.assertNotIn(b"tEXt", direct.subject_png(32))


class PrivateDirTest(unittest.TestCase):
    def test_a_temporary_directory_is_private_outside_the_tree_and_removed(self):
        private = direct.PrivateDir()
        path = private.path
        self.assertEqual(os.stat(path).st_mode & 0o777, 0o700)
        self.assertFalse(path.startswith(str(ROOT)))
        name = private.write("../escape.json", "{}")
        self.assertEqual(name, "escape.json")
        self.assertEqual(os.stat(os.path.join(path, name)).st_mode & 0o777, 0o600)
        private.close()
        self.assertFalse(os.path.exists(path))

    def test_keep_leaves_the_files(self):
        private = direct.PrivateDir(keep=True)
        private.write("a.bin", b"x")
        with contextlib.redirect_stdout(io.StringIO()):
            private.close()
        self.assertTrue(os.path.exists(os.path.join(private.path, "a.bin")))
        import shutil
        shutil.rmtree(private.path)

    def test_a_given_directory_loses_only_what_the_run_wrote(self):
        with tempfile.TemporaryDirectory() as parent:
            path = os.path.join(parent, "raw")
            os.mkdir(path, 0o700)
            with open(os.path.join(path, "other.txt"), "w") as handle:
                handle.write("mine")
            private = direct.PrivateDir(path)
            private.write("run.json", "{}")
            private.close()
            self.assertEqual(sorted(os.listdir(path)), ["other.txt"])

    def test_bad_directories_are_refused(self):
        with tempfile.TemporaryDirectory() as parent:
            open_dir = os.path.join(parent, "open")
            os.mkdir(open_dir, 0o755)
            self.assertIn("0700", direct.private_dir_problem(open_dir))
            self.assertIn("does not exist", direct.private_dir_problem(os.path.join(parent, "missing")))
            real = os.path.join(parent, "real")
            os.mkdir(real, 0o700)
            link = os.path.join(parent, "link")
            os.symlink(real, link)
            self.assertIn("link", direct.private_dir_problem(link))
            self.assertIsNone(direct.private_dir_problem(real, repo_root=os.path.join(parent, "elsewhere")))
            self.assertIn("outside the working tree", direct.private_dir_problem(real, repo_root=parent))
            inside = os.path.join(ROOT, "scripts")
            self.assertIsNotNone(direct.private_dir_problem(inside))
        with self.assertRaises(ValueError):
            direct.PrivateDir(os.path.join(tempfile.gettempdir(), "definitely-missing-l5-dir"))


class EvidenceTest(unittest.TestCase):
    TASK = {"id": "t1", "state": "SUCCEEDED", "offering_id": "o", "model_id": "m", "created_at": "2026-11-24T00:00:00Z",
            "input": {"parameters": {"prompt": "FREE TEXT PROMPT", "negative_prompt": "NEG", "size": "1024x1024"}},
            "native_quote": {"unit": "usd", "total_amount": "1"},
            "assets": [{"role": "image", "url": "/api/v1/tasks/t1/assets/a1?signature=SECRET", "size": 3,
                        "sha256": "ab", "status": "READY", "required": True, "media_type": "image/png"}],
            "failure": None, "secret_extra": "dropped"}

    def test_the_summary_keeps_structure_and_drops_text_and_urls(self):
        summary = direct.task_summary(self.TASK)
        text = json.dumps(summary)
        self.assertNotIn("FREE TEXT PROMPT", text)
        self.assertNotIn("NEG", text)
        self.assertNotIn("SECRET", text)
        self.assertNotIn("secret_extra", text)
        self.assertNotIn("/api/v1/tasks/t1/assets", text)
        self.assertEqual(summary["parameters"], {"size": "1024x1024"})
        self.assertEqual(summary["assets"][0]["sha256"], "ab")

    def test_scrub_value_cuts_strings_and_queries(self):
        scrubbed = direct.scrub_value({"a": "x" * 500, "u": "see https://host.example/p?token=SECRET end"})
        self.assertLessEqual(len(scrubbed["a"]), 160)
        self.assertNotIn("SECRET", scrubbed["u"])

    def test_evidence_is_written_next_to_the_report_only_with_a_directory(self):
        self.assertIsNone(direct.write_evidence(None, "x", {}))
        with tempfile.TemporaryDirectory() as directory:
            path = direct.write_evidence(directory, "x", {"a": 1})
            with open(path) as handle:
                self.assertEqual(json.load(handle), {"a": 1})
            self.assertTrue(path.endswith("x-evidence.json"))

    def test_findings_are_strict_on_a_real_target_and_recorded_on_the_fake(self):
        for strict, expected_fail in ((True, 1), (False, 0)):
            report = common.Report("t")
            findings = direct.Findings(report, strict)
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertFalse(findings.check("a", False, "why"))
                self.assertTrue(findings.check("b", True))
            self.assertEqual(report.counts()["FAIL"], expected_fail)
            self.assertEqual(len(findings.info), 0 if strict else 1)


# ------------------------------------------------------------------ the submission

class SubmissionTest(unittest.TestCase):
    def test_outcome_classes(self):
        for status in (200, 201, 202):
            self.assertEqual(direct.submit_outcome(status), "accepted")
        for status in (0, 500, 502, 503):
            self.assertEqual(direct.submit_outcome(status), "unknown")
        for status in (400, 401, 403, 404, 409, 422, 429):
            self.assertEqual(direct.submit_outcome(status), "refused")

    def test_a_clean_submission_sends_each_step_once(self):
        live = StubLive(accepting())
        result = direct.direct_submit(live, "s1", "o/m/text-to-image", "text-to-image", {"prompt": "p"}, {"quality": "r"})
        self.assertEqual((result.outcome, result.task_id), ("accepted", "task_1"))
        for path in ("/api/v1/tasks/prepare", "/api/v1/tasks/grants", "/api/v1/tasks"):
            self.assertEqual(len(live.posts(path)), 1, path)
        prepare = live.posts("/api/v1/tasks/prepare")[0][2]
        self.assertEqual(prepare["parameter_reasons"], {"quality": "r"})
        self.assertEqual(live.posts("/api/v1/tasks")[0][3]["Idempotency-Key"], "l5-direct-prep_1")
        self.assertEqual(result.quote["unit"], "usd")
        self.assertEqual(live.approvals, [])

    def test_a_pending_grant_is_approved_with_the_schema_member(self):
        live = StubLive(accepting(grant_status="pending"))
        result = direct.direct_submit(live, "s1", "o", "c", {})
        self.assertEqual(result.outcome, "accepted")
        self.assertEqual(live.approvals, ["req_1"])

    def test_a_refused_prepare_stops_before_anything_is_started(self):
        def answer(method, path, body, headers):
            return (400, {"error": "bad size"}) if path == "/api/v1/tasks/prepare" else (500, {})
        live = StubLive(answer)
        result = direct.direct_submit(live, "s1", "o", "c", {})
        self.assertEqual((result.outcome, result.step, result.status), ("refused", "prepare", 400))
        self.assertEqual(len(live.client.calls), 1)

    def test_an_unknown_submit_is_not_replayed_and_is_observed(self):
        def answer(method, path, body, headers):
            if path == "/api/v1/tasks":
                return 503, {}
            if path.startswith("/api/v1/tasks?"):
                return 200, {"tasks": [{"id": "new_task", "state": "QUEUED"}, {"id": "old"}]}
            return accepting()(method, path, body, headers)
        live = StubLive(answer)
        result = direct.direct_submit(live, "s1", "o", "c", {})
        self.assertEqual((result.outcome, result.step), ("unknown", "submit"))
        self.assertEqual(len(live.posts("/api/v1/tasks")), 1)
        observed = direct.observe_new_tasks(live, "s1", {"old"}, wait=0, sleep=lambda _: None)
        self.assertEqual([task["id"] for task in observed], ["new_task"])
        self.assertEqual(len(live.posts("/api/v1/tasks")), 1)

    def test_a_connection_failure_on_submit_is_unknown(self):
        def answer(method, path, body, headers):
            return (0, {}) if path == "/api/v1/tasks" else accepting()(method, path, body, headers)
        result = direct.direct_submit(StubLive(answer), "s1", "o", "c", {})
        self.assertEqual(result.outcome, "unknown")

    def test_a_submit_without_a_task_id_is_unknown(self):
        def answer(method, path, body, headers):
            return (202, {}) if path == "/api/v1/tasks" else accepting()(method, path, body, headers)
        self.assertEqual(direct.direct_submit(StubLive(answer), "s1", "o", "c", {}).outcome, "unknown")


class AwaitTest(unittest.TestCase):
    def test_states_are_recorded_until_terminal(self):
        sequence = iter(["QUEUED", "QUEUED", "RUNNING", "SUCCEEDED"])
        clock = {"now": 0.0}

        def answer(method, path, body, headers):
            return 200, {"state": next(sequence)}

        def sleep(seconds):
            clock["now"] += seconds

        live = StubLive(answer)
        task, states, timed_out = direct.await_task(live, "t", 100, interval=5, now=lambda: clock["now"], sleep=sleep)
        self.assertFalse(timed_out)
        self.assertEqual([state for state, _ in states], ["QUEUED", "RUNNING", "SUCCEEDED"])
        self.assertEqual(states[-1][1], 15.0)

    def test_the_timeout_ends_the_wait_without_a_terminal_state(self):
        clock = {"now": 0.0}
        live = StubLive(lambda *args: (200, {"state": "RUNNING"}))
        task, states, timed_out = direct.await_task(live, "t", 20, interval=5, now=lambda: clock["now"],
                                                    sleep=lambda s: clock.__setitem__("now", clock["now"] + s))
        self.assertTrue(timed_out)
        self.assertEqual(task["state"], "RUNNING")


# ------------------------------------------------------------------ judging the pictures

def image(index=1, width=1536, height=1152, fmt="png", sha=None, ok=True):
    return {"index": index, "width": width, "height": height, "format": fmt, "sha256": sha or f"sha{index}",
            "matches_declared": ok, "color_type": 6}


class PictureFindingsTest(unittest.TestCase):
    def verdicts(self, case_key, images, large=False):
        case = next(case for case in two_d.CASES if case["key"] == case_key)
        return two_d.picture_findings(case, large, images)

    def failing(self, findings):
        return [item for kind, item, ok, _ in findings if kind == "check" and not ok]

    def test_two_distinct_pictures_of_the_asked_size_pass(self):
        findings = self.verdicts("openai-generate", [image(1), image(2)])
        self.assertEqual(self.failing(findings), [])
        self.assertEqual([item for kind, item, *_ in findings if kind == "note"], [])

    def test_one_picture_for_n2_fails(self):
        self.assertTrue(any("delivers 2" in item for item in self.failing(self.verdicts("openai-generate", [image(1)]))))

    def test_identical_pictures_fail_the_distinct_check(self):
        findings = self.verdicts("openai-generate", [image(1, sha="same"), image(2, sha="same")])
        self.assertTrue(any("distinct" in item for item in self.failing(findings)))

    def test_a_wrong_ratio_fails_but_a_different_exact_size_is_a_note(self):
        wrong_ratio = self.verdicts("openai-generate", [image(1, 1024, 1024), image(2, 1024, 1024)])
        self.assertTrue(any("ratio" in item for item in self.failing(wrong_ratio)))
        scaled = self.verdicts("openai-generate", [image(1, 768, 576), image(2, 768, 576)])
        self.assertEqual(self.failing(scaled), [])
        self.assertTrue(any(kind == "note" and "deviates" in item for kind, item, *_ in scaled))

    def test_bytes_that_differ_from_the_declaration_fail(self):
        findings = self.verdicts("jimeng-generate", [image(1, 1920, 1920, "jpeg", ok=False)])
        self.assertTrue(any("declared" in item for item in self.failing(findings)))

    def test_a_format_that_differs_from_the_usual_is_only_a_note(self):
        findings = self.verdicts("jimeng-generate", [image(1, 1920, 1920, "png")])
        self.assertEqual(self.failing(findings), [])
        self.assertTrue(any(kind == "note" and "format" in item for kind, item, *_ in findings))

    def test_gemini_judges_the_ratio_not_the_exact_size(self):
        findings = self.verdicts("gemini-generate", [image(1, 1376, 768, "jpeg")])
        self.assertEqual(self.failing(findings), [])
        findings = self.verdicts("gemini-edit", [image(1, 1024, 1024)])
        self.assertTrue(any("ratio" in item for item in self.failing(findings)))

    def test_a_sequence_may_deliver_one_or_two(self):
        for count in (1, 2):
            images = [image(i + 1, 1920, 1920, "jpeg") for i in range(count)]
            self.assertEqual(self.failing(self.verdicts("jimeng-sequence", images)), [])
        self.assertTrue(self.failing(self.verdicts("jimeng-sequence", [])))

    def test_an_unreadable_size_fails_when_a_ratio_is_asked(self):
        unreadable = image(1)
        unreadable["width"] = unreadable["height"] = None
        self.assertTrue(any("readable" in item for item in self.failing(self.verdicts("gemini-generate", [unreadable]))))


class FetchImagesTest(unittest.TestCase):
    def test_bytes_are_verified_described_and_kept_privately(self):
        import hashlib
        data = direct.subject_png(16)

        def answer(method, path, body, headers):
            return 200, data

        live = StubLive(answer)
        task = {"assets": [{"role": "image", "media_type": "image/png", "url": "/a/1", "size": len(data),
                            "sha256": hashlib.sha256(data).hexdigest()},
                           {"role": "model", "media_type": "model/gltf-binary", "url": "/a/2", "size": 1}]}
        private = direct.PrivateDir()
        try:
            entries = direct.fetch_images(live, task, private, "case")
            self.assertEqual(len(entries), 1)
            self.assertTrue(entries[0]["matches_declared"])
            self.assertEqual((entries[0]["width"], entries[0]["format"]), (16, "png"))
            self.assertEqual(private.written, ["case-1.png"])
            self.assertNotIn("data", entries[0])
        finally:
            private.close()
        mismatch = direct.fetch_images(StubLive(answer), {"assets": [{"role": "image", "url": "/a", "size": 1, "sha256": "x"}]})
        self.assertFalse(mismatch[0]["matches_declared"])
        missing = direct.fetch_images(StubLive(lambda *a: (404, b"")), {"assets": [{"role": "image", "url": "/a", "size": 1}]})
        self.assertFalse(missing[0]["matches_declared"])


class EvidenceEntryTest(unittest.TestCase):
    def test_the_case_evidence_carries_no_prompt_or_url(self):
        case = two_d.CASES[0]
        submission = direct.Submission()
        submission.outcome, submission.task_id, submission.status = "accepted", "t1", 202
        submission.quote = {"unit": "usd", "total_amount": "0.02"}
        task = EvidenceTest.TASK
        entry = two_d.evidence_entry(case, False, submission, task, [["QUEUED", 0.0]],
                                     [dict(image(1), bytes=3, status=200, variant=None, media_type="image/png",
                                           alpha=False, sha256="abcdef0123456789abcdef")], [])
        text = json.dumps(entry)
        self.assertNotIn("FREE TEXT PROMPT", text)
        self.assertNotIn("SECRET", text)
        self.assertEqual(entry["pictures"][0]["sha256_prefix"], "abcdef012345")
        self.assertNotIn("sha256", entry["pictures"][0])


# ------------------------------------------------------------------ matting through Plinth

class MattingSourcesTest(unittest.TestCase):
    def test_the_real_batch_is_a_png_a_jpeg_and_a_file_that_is_not_a_picture(self):
        sources = matting.build_sources("live")
        self.assertEqual([source["media_type"] for source in sources], ["image/png", "image/jpeg", "image/png"])
        self.assertEqual([source["good"] for source in sources], [True, True, False])
        self.assertEqual(direct.describe_image(sources[0]["data"])["width"], 512)
        self.assertEqual(direct.describe_image(sources[1]["data"])["width"], 256)
        self.assertIsNone(direct.describe_image(sources[2]["data"])["format"])
        self.assertEqual(sources[2]["filename"], "third.png")

    def test_on_the_fake_the_third_file_is_a_marked_picture(self):
        third = matting.build_sources("fake")[2]
        self.assertEqual(direct.describe_image(third["data"])["format"], "png")
        self.assertIn(b"fake-file-faults:failed;", third["data"])
        self.assertFalse(third["good"])

    def test_a_mixed_target_gets_the_marked_picture_too(self):
        self.assertIn(b"fake-file-faults:failed;", matting.build_sources("mixed")[2]["data"])

    def test_positions_and_sizes_follow_the_accepted_files(self):
        sources = matting.build_sources("live")
        self.assertEqual(matting.missing_positions(sources), (3,))
        self.assertEqual(matting.expected_sizes(sources), [(512, 512), (256, 256)])
        # a refused bad file leaves a batch of two good pictures and no missing position
        self.assertEqual(matting.missing_positions(sources[:2]), ())

    def test_default_pipeline_check(self):
        self.assertEqual(matting.frozen_default_problems({"input": {"parameters": {}}}), [])
        self.assertEqual(matting.frozen_default_problems({"input": {"parameters": {"birefnet_process_res": 1024}}}), [])
        self.assertEqual(matting.frozen_default_problems({"input": {"parameters": {"sdmatte_process_res": 2048}}}),
                         ["sdmatte_process_res=2048"])


def result(index, width, height, fmt="png", color=6, ok=True):
    return {"index": index, "width": width, "height": height, "format": fmt, "color_type": color,
            "matches_declared": ok}


class MattingResultTest(unittest.TestCase):
    def verdicts(self, images, expected=((512, 512), (256, 256))):
        return {item: ok for _, item, ok, _ in matting.result_findings(images, list(expected))}

    def test_rgba_results_of_the_source_sizes_pass_everything(self):
        verdicts = self.verdicts([result(1, 512, 512), result(2, 256, 256)])
        self.assertTrue(all(verdicts.values()), verdicts)

    def test_an_opaque_white_square_fails_alpha_and_size_but_not_the_count(self):
        verdicts = self.verdicts([result(1, 64, 64, color=2), result(2, 64, 64, color=2)])
        self.assertTrue(verdicts["matting delivers one result per matted file"])
        self.assertFalse(verdicts["matting results carry an alpha channel (RGBA)"])
        self.assertFalse(verdicts["matting results have the pixel size of their sources"])

    def test_the_count_and_the_bytes_are_requirements(self):
        verdicts = self.verdicts([result(1, 512, 512)])
        self.assertFalse(verdicts["matting delivers one result per matted file"])
        verdicts = self.verdicts([result(1, 512, 512, ok=False), result(2, 256, 256)])
        self.assertFalse(verdicts["matting results match the declared size and hash"])

    def test_requirements_and_shape_checks_are_told_apart(self):
        kinds = {item: kind for kind, item, *_ in matting.result_findings([], [(1, 1)])}
        self.assertEqual(kinds["matting delivers one result per matted file"], "check")
        self.assertEqual(kinds["matting results carry an alpha channel (RGBA)"], "shape")

    def test_the_notice_must_name_the_missing_pictures_with_the_schema_sentence(self):
        messages = {"task.notice.partial_inputs": "有图片没有处理成功，其余已完成。",
                    "task.notice.partial_inputs_named": "第 {inputs}没有处理成功，其余已完成。",
                    "task.notice.input": "{ordinal} 张", "task.notice.input_separator": "、"}
        good = {"state": "SUCCEEDED", "assets": [{}, {}], "notices": [
            {"code": "partial_inputs", "inputs": [3], "detail": "第 3 张没有处理成功，其余已完成。"}]}
        self.assertEqual(matting.notice_problems(good, messages, (3,)), [])
        silent = {"state": "SUCCEEDED", "assets": [{}, {}], "notices": []}
        self.assertTrue(matting.notice_problems(silent, messages, (3,)))
        wrong = dict(good, notices=[dict(good["notices"][0], inputs=[2])])
        self.assertTrue(matting.notice_problems(wrong, messages, (3,)))
        self.assertEqual(matting.notice_problems(silent, messages, ()), [])


class MattingRunTest(unittest.TestCase):
    """The whole batch against a scripted Plinth: the library upload of the bad file is refused or accepted."""

    def server(self, reject_third=False, results=None):
        task = {"id": "task_1", "state": "SUCCEEDED", "assets": [], "notices": []}
        data = direct.subject_png(8)
        task["assets"] = [{"role": "image", "required": True, "media_type": "image/png", "url": f"/a/{i}",
                           "size": len(data)} for i in (1, 2)]

        def answer(method, path, body, headers):
            if path.startswith("/a/"):
                return 200, data
            return accepting(task)(method, path, body, headers)

        live = StubLive(answer, shape="fake")
        uploads = []

        def upload(content, media_type, filename):
            uploads.append(filename)
            if reject_third and filename == "third.png":
                return 400, None
            return 200, f"asset_{len(uploads)}"

        live.client.upload_library = upload
        live.arguments = types.SimpleNamespace(task_timeout=30, private_dir="", keep_private=False)
        live.new_session = lambda title="": "s1"
        live.offerings = lambda capability: [{"id": matting.MATTING, "available": True}]
        live.public_schema = lambda: {"content": {"messages": {}}}
        return live, uploads

    def test_a_refused_bad_file_leaves_a_two_file_batch(self):
        live, uploads = self.server(reject_third=True)
        report = common.Report("t")
        with contextlib.redirect_stdout(io.StringIO()):
            matting.body(live, report)
        self.assertEqual(uploads, ["subject-512.png", "subject-256.jpg", "third.png"])
        submitted = live.posts("/api/v1/tasks/prepare")[0][2]["input"]["source_assets"]
        self.assertEqual(len(submitted), 2)
        self.assertEqual(len(live.posts("/api/v1/tasks")), 1)

    def test_the_batch_is_submitted_once_with_default_parameters(self):
        live, _ = self.server()
        report = common.Report("t")
        with contextlib.redirect_stdout(io.StringIO()):
            matting.body(live, report)
        prepare = live.posts("/api/v1/tasks/prepare")
        self.assertEqual(len(prepare), 1)
        self.assertEqual(list(prepare[0][2]["input"]), ["source_assets"])
        self.assertEqual(len(prepare[0][2]["input"]["source_assets"]), 3)
        self.assertEqual(len(live.posts("/api/v1/tasks")), 1)


# ------------------------------------------------------------------ the matting service probe

OPENAPI = {
    "openapi": "3.1.0", "info": {"title": "matting service"},
    "paths": {"/api/v1/jobs": {"post": {}}, "/api/v1/batches/{batch_id}": {"get": {}},
              "/api/v1/tasks/{task_id}/download": {"get": {}}, "/health": {"get": {}}},
    "components": {"schemas": {
        "BatchStatus": {"enum": ["READY", "SUCCESS"]},
        "PipelineParams": {"properties": {
            "birefnet_process_res": {"anyOf": [{"type": "integer", "minimum": 512, "maximum": 4096}, {"type": "null"}]},
            "expand": {"type": "integer", "minimum": 0, "maximum": 1000, "default": 240},
            "sensitivity": {"type": "number", "minimum": 0, "maximum": 1, "default": 0.1},
            "extra_field": {"type": "boolean"}}},
        "TaskStatusItem": {"properties": {"status": {"type": "string", "enum": ["READY", "REJECTED"]}}}}},
}


class ProbeLogicTest(unittest.TestCase):
    def test_the_openapi_summary_lists_paths_enums_and_pipeline_bounds(self):
        summary = probe.summarize_openapi(OPENAPI)
        self.assertEqual(summary["paths"]["/api/v1/jobs"], ["post"])
        self.assertEqual(summary["enums"]["BatchStatus"], ["READY", "SUCCESS"])
        self.assertEqual(summary["enums"]["TaskStatusItem.status"], ["READY", "REJECTED"])
        fields = summary["pipeline_fields"]
        self.assertEqual((fields["birefnet_process_res"]["minimum"], fields["birefnet_process_res"]["maximum"]), (512, 4096))
        self.assertEqual(fields["birefnet_process_res"]["type"], "integer")
        self.assertEqual(summary["pipeline_fields_not_in_catalog"], ["extra_field"])
        self.assertIn("birefnet_mask_blur", summary["catalog_fields_not_in_pipeline"])
        self.assertEqual(probe.summarize_openapi({})["pipeline_fields"], {})

    def test_missing_endpoints(self):
        self.assertEqual(probe.missing_endpoints(OPENAPI), [])
        broken = json.loads(json.dumps(OPENAPI))
        del broken["paths"]["/api/v1/tasks/{task_id}/download"]
        self.assertEqual(len(probe.missing_endpoints(broken)), 1)
        self.assertEqual(len(probe.missing_endpoints({})), 3)

    def test_validation_summary_keeps_loc_type_and_message_but_not_input(self):
        body = json.dumps({"detail": [{"type": "greater_than_equal", "loc": ["body", "pipeline_params_json", "expand"],
                                       "msg": "Input should be >= 0", "input": "SECRET-INPUT"}]})
        summary = probe.validation_summary(json.loads(body))
        self.assertEqual(summary["detail"][0]["loc"][-1], "expand")
        self.assertNotIn("SECRET-INPUT", json.dumps(summary))
        self.assertEqual(probe.validation_summary({"detail": "Invalid API key"})["detail"], "Invalid API key")
        self.assertIsNone(probe.validation_summary("x")["keys"])

    def test_base_url_check(self):
        self.assertIsNone(probe.base_url_problem("http://matting.example.test:8002"))
        self.assertIsNone(probe.base_url_problem("http://127.0.0.1:19410/matting"))
        for bad in ("", "x", "ftp://h", "http://u:p@h", "http://h?q=1"):
            self.assertIsNotNone(probe.base_url_problem(bad), bad)


class FakeService:
    """A scripted matting service. `faithful` enforces every bound; otherwise only the first, like the fake."""

    def __init__(self, faithful=True):
        self.faithful = faithful
        self.requests = []

    def __call__(self, method, path, headers=None, body=None):
        headers = headers or {}
        self.requests.append((method, path, headers))
        auth = headers.get("Authorization", "")
        if path == "/health":
            return probe.Response(200, {"content-type": "application/json"}, b'{"status":"ok"}')
        if path == "/openapi.json":
            if not self.faithful:
                return probe.Response(404, {}, b"{}")
            return probe.Response(200, {"content-type": "application/json"}, json.dumps(OPENAPI).encode())
        if method == "POST" and path == "/api/v1/jobs":
            if auth != "Bearer good-key":
                return probe.Response(401, {"www-authenticate": "Bearer", "content-type": "application/json"},
                                      b'{"detail":"Invalid API key"}')
            pipeline = json.loads(re.search(rb'name="pipeline_params_json"\r\n\r\n(.*?)\r\n--', body, re.S).group(1))
            for field, bound in (("birefnet_process_res", (512, 4096)), ("expand", (0, 1000)), ("sensitivity", (0, 1))):
                if field in pipeline and not bound[0] <= pipeline[field] <= bound[1]:
                    if self.faithful or field == "birefnet_process_res":
                        detail = [{"type": "less_than_equal", "loc": ["body", "pipeline_params_json", field],
                                   "msg": "out of range", "input": pipeline[field]}]
                        return probe.Response(422, {"content-type": "application/json"}, json.dumps({"detail": detail}).encode())
            return probe.Response(200, {}, b'{"batch_id":"batch_x"}')
        if path.startswith("/api/v1/") and auth != "Bearer good-key":
            return probe.Response(401, {}, b'{"detail":"Not authenticated"}')
        return probe.Response(404, {"content-type": "application/json"}, b'{"detail":"Not Found"}')


class ProbeRunTest(unittest.TestCase):
    def run_probe(self, service, strict):
        report = common.Report("probe")
        findings = direct.Findings(report, strict)
        evidence = {}
        private = direct.PrivateDir()
        output = io.StringIO()
        try:
            with contextlib.redirect_stdout(output):
                probe.run_probe(service, "good-key", report, findings, private, evidence)
            written = list(private.written)
        finally:
            private.close()
        return report, findings, evidence, written, output.getvalue()

    def test_a_faithful_service_passes_everything(self):
        service = FakeService()
        report, findings, evidence, written, _ = self.run_probe(service, True)
        self.assertEqual(report.counts()["FAIL"], 0, report.items)
        self.assertIn("openapi.json", written)
        self.assertEqual(evidence["ranges"]["expand"]["status"], 422)
        self.assertEqual(evidence["wrong_key"]["www_authenticate"], "Bearer")
        self.assertEqual(evidence["unknown_batch"]["status"], 404)
        self.assertEqual(evidence["download_without_key"]["status"], 401)

    def test_a_lax_service_fails_on_a_real_target(self):
        report, findings, evidence, _, _ = self.run_probe(FakeService(faithful=False), True)
        self.assertGreater(report.counts()["FAIL"], 0)

    def test_the_same_gaps_on_a_fake_target_are_recorded_not_failed(self):
        report, findings, evidence, _, output = self.run_probe(FakeService(faithful=False), False)
        self.assertEqual(report.counts()["FAIL"], 0)
        self.assertTrue(findings.info)
        self.assertIn("not modelled by the fake yet", output)

    def test_every_probe_is_sent_once_and_the_right_key_never_leaves_the_authenticated_calls(self):
        service = FakeService()
        self.run_probe(service, True)
        paths = [(method, path) for method, path, _ in service.requests]
        self.assertEqual(paths.count(("GET", "/openapi.json")), 1)
        self.assertEqual(sum(1 for method, path in paths if (method, path) == ("POST", "/api/v1/jobs")), 4)
        wrong = [headers for method, path, headers in service.requests if headers.get("Authorization") == "Bearer " + probe.WRONG_KEY]
        self.assertEqual(len(wrong), 1)

    def test_nothing_secret_reaches_the_output(self):
        _, _, evidence, _, output = self.run_probe(FakeService(), True)
        self.assertNotIn("good-key", output + json.dumps(evidence))

    def test_a_probe_main_with_a_scripted_service_exits_cleanly(self):
        service = FakeService()
        with tempfile.TemporaryDirectory() as report_dir:
            environ = {"ART_AI_UPSTREAM_SHAPE": "fake", "MATTING_BASE_URL": "http://127.0.0.1:1", "MATTING_API_KEY": "good-key"}
            with contextlib.redirect_stdout(io.StringIO()):
                code = probe.main(["--report", report_dir], environ, call=service)
            self.assertEqual(code, 0)
            self.assertTrue(os.path.exists(os.path.join(report_dir, "matting-probe-evidence.json")))
            self.assertTrue(os.path.exists(os.path.join(report_dir, "matting-probe.json")))

    def test_a_probe_main_refuses_without_the_switches(self):
        environ = {"MATTING_BASE_URL": "http://127.0.0.1:1", "MATTING_API_KEY": "k"}
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            self.assertEqual(probe.main([], environ, call=FakeService()), 2)
        self.assertIn("ART_AI_LIVE_ACK=1", stderr.getvalue())


class MultipartTest(unittest.TestCase):
    def test_fields_and_files_are_framed(self):
        content_type, body = direct.multipart_body([("pipeline_params_json", "{}")], [("files", "a.png", "image/png", b"\x89PNG")])
        boundary = content_type.split("boundary=")[1]
        self.assertIn(f"--{boundary}--".encode(), body)
        self.assertIn(b'name="pipeline_params_json"\r\n\r\n{}', body)
        self.assertIn(b'name="files"; filename="a.png"', body)


class UploadLibraryTest(unittest.TestCase):
    def test_the_shared_client_has_a_library_upload(self):
        self.assertTrue(callable(common.Client.upload_library))


if __name__ == "__main__":
    unittest.main()
