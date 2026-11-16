#!/usr/bin/env python3
"""Offline tests of the fault pictures and checks of #301 (no stack, no network).

scripts/smoke/fake_faults.py builds the pictures that choose the matting fake's per-file faults from
their own bytes and holds the public Task against the Schema's sentences; smoke-fake-faults.py runs
that on a fake-shaped dev-test and smoke-fake-stack.py as its "matting-faults" scenario. Here:

  * the pictures are valid PNGs whose marker is the text the Go fake reads (the words and the marker
    are compared with the Go source, so a change on either side fails here, not on a deployed stack);
  * the notice sentence is built from Schema messages the way the server builds it;
  * every rule of the checks fails when the public projection is wrong;
  * run() submits every batch before it waits for any, approves a pending grant with the Schema's
    decision member, and reports a refusal instead of hiding it;
  * the script refuses a target that is not the fake before any request, and prints no credentials.
"""
import hashlib
import importlib.util
import inspect
import io
import json
import os
import re
import struct
import sys
import unittest
import zlib
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock

sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parents[2]
SMOKE = ROOT / "scripts" / "smoke"
GO_FAKE = ROOT / "internal" / "testutil" / "fakeupstream" / "matting" / "api-v1"
sys.path.insert(0, str(SMOKE))
import fake_faults as ff  # noqa: E402


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, SMOKE / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


script = load("smoke_fake_faults", "smoke-fake-faults.py")
stack = load("smoke_fake_stack", "smoke-fake-stack.py")

with open(ROOT / "internal" / "catalog" / "default_catalog.json", encoding="utf-8") as handle:
    MESSAGES = json.load(handle)["application"]["content"]["messages"]


def chunks(data):
    """The chunks of a PNG as (type, data); every CRC is checked."""
    assert data[:8] == ff.PNG_SIGNATURE, "not a PNG"
    found, at = [], 8
    while at < len(data):
        (length,) = struct.unpack(">I", data[at:at + 4])
        kind, body = data[at + 4:at + 8], data[at + 8:at + 8 + length]
        (crc,) = struct.unpack(">I", data[at + 8 + length:at + 12 + length])
        assert crc == zlib.crc32(kind + body) & 0xFFFFFFFF, f"bad CRC of {kind!r}"
        found.append((kind, body))
        at += 12 + length
    return found


class PictureTest(unittest.TestCase):
    def test_a_plain_picture_is_a_valid_png_of_the_asked_size(self):
        found = chunks(ff.solid_png(48, 32))
        self.assertEqual([kind for kind, _ in found], [b"IHDR", b"IDAT", b"IEND"])
        width, height = struct.unpack(">II", found[0][1][:8])
        self.assertEqual((width, height), (48, 32))
        self.assertEqual(len(zlib.decompress(found[1][1])), 32 * (1 + 3 * 48))

    def test_a_marked_picture_carries_the_marker_in_a_text_chunk_right_after_the_header(self):
        found = chunks(ff.solid_png(16, 16, ("failed", "gpu_oom")))
        self.assertEqual([kind for kind, _ in found], [b"IHDR", b"tEXt", b"IDAT", b"IEND"])
        self.assertEqual(found[1][1], b"Comment\x00fake-file-faults:failed,gpu_oom;")

    def test_the_marker_lies_within_the_head_the_fake_reads(self):
        data = ff.solid_png(2048, 2048, ("gpu_oom",))
        self.assertLess(data.index(b"fake-file-faults:"), 1 << 10)
        self.assertLess(len(data), 200_000, "a picture of the incident must stay small")

    def test_faults_must_be_words_the_fake_knows(self):
        for faults in ((), ("exploded",), ("failed", "exploded")):
            with self.subTest(faults=faults), self.assertRaises(ValueError):
                ff.fault_mark(faults)
        self.assertEqual(ff.fault_mark(("rejected", "renamed")), "fake-file-faults:rejected,renamed;")

    def test_the_contract_is_the_one_the_go_fake_reads(self):
        marker = (GO_FAKE / "faultmark.go").read_text(encoding="utf-8")
        self.assertIn(f'const FileFaultMarker = "{ff.MARKER}"', marker)
        self.assertIn(f"const fileFaultMarkerEnd = '{ff.MARKER_END}'", marker)
        fake = (GO_FAKE / "fake.go").read_text(encoding="utf-8")
        words = re.findall(r'^\s*Fault\w+\s*=\s*"(\w+)"$', fake, re.MULTILINE)
        self.assertEqual(tuple(words), ff.FILE_FAULTS)
        self.assertIn('const FileFaultsHeader = "X-Fake-File-Faults"', fake)


class PictureCommandTest(unittest.TestCase):
    """`fake_faults.py picture` writes the same pictures run() uploads, for trying a path by hand."""

    def write(self, *args):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "out.png")
            out = io.StringIO()
            with redirect_stdout(out):
                code = ff.main(["picture", path, *args])
            with open(path, "rb") as handle:
                return code, handle.read(), out.getvalue()

    def test_it_writes_a_marker_picture_of_the_asked_size(self):
        code, data, out = self.write("--size", "2048x1024", "--fault", "gpu_oom", "--fault", "renamed")
        self.assertEqual(code, 0)
        found = chunks(data)
        self.assertEqual(struct.unpack(">II", found[0][1][:8]), (2048, 1024))
        self.assertEqual(found[1], (b"tEXt", b"Comment\x00fake-file-faults:gpu_oom,renamed;"))
        self.assertIn("fake-file-faults:gpu_oom,renamed;", out)

    def test_without_a_fault_it_writes_a_plain_picture(self):
        code, data, out = self.write()
        self.assertEqual(code, 0)
        self.assertEqual([kind for kind, _ in chunks(data)], [b"IHDR", b"IDAT", b"IEND"])
        self.assertIn("no marker", out)

    def test_a_size_or_a_fault_it_cannot_write_is_refused(self):
        for args in (("--size", "0x10"), ("--size", "10"), ("--size", "9000x10"), ("--fault", "exploded")):
            with self.subTest(args=args), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
                ff.main(["picture", os.devnull, *args])
            self.assertEqual(raised.exception.code, 2)


class NoticeTextTest(unittest.TestCase):
    def test_the_sentence_is_built_from_schema_messages_only(self):
        self.assertEqual(ff.compose_notice(MESSAGES, "partial_inputs", [2, 4]), "第 2 张、第 4 张没有处理成功，其余已完成。")
        self.assertEqual(ff.compose_notice(MESSAGES, "partial_inputs", [3]), "第 3 张没有处理成功，其余已完成。")

    def test_positions_that_cannot_be_named_use_the_general_sentence(self):
        self.assertEqual(ff.compose_notice(MESSAGES, "partial_inputs", []), "部分图片没有处理成功，其余已完成。")
        self.assertEqual(ff.compose_notice(MESSAGES, "partial_inputs", None), "部分图片没有处理成功，其余已完成。")

    def test_a_code_the_schema_has_no_sentence_for_says_nothing(self):
        self.assertEqual(ff.compose_notice(MESSAGES, "no_such_notice", [1]), "")
        self.assertEqual(ff.compose_notice(MESSAGES, "", [1]), "")
        self.assertEqual(ff.compose_notice({}, "partial_inputs", [1]), "")

    def test_without_the_named_sentence_the_general_one_is_used(self):
        general = {"task.notice.partial_inputs": "Some pictures were not processed."}
        self.assertEqual(ff.compose_notice(general, "partial_inputs", [2]), "Some pictures were not processed.")


def public_task(case, messages=MESSAGES):
    """The public Task a correct stack answers for a case."""
    task = {"id": "task_1", "state": case.state, "assets": [
        {"role": "image", "size": 3, "sha256": "x", "url": f"/a/{index}"} for index in range(case.delivered)]}
    if case.state == "SUCCEEDED" and case.missing:
        task["notices"] = [{"code": "partial_inputs", "inputs": list(case.missing),
                            "detail": ff.compose_notice(messages, "partial_inputs", list(case.missing))}]
    if case.state == "FAILED":
        task["failure"] = {"class": case.failure_class, "retryable": False,
                           "detail": messages["failure." + case.failure_class]}
    return task


class CaseProblemsTest(unittest.TestCase):
    def problems(self, case, mutate=lambda task: None, messages=MESSAGES):
        task = public_task(case, messages)
        mutate(task)
        return ff.case_problems(case, task, messages)

    def test_the_cases_cover_the_paths_of_284_and_299(self):
        cases = ff.cases()
        self.assertEqual(len(cases), 4)
        partial, incident, oom, plain = cases
        self.assertEqual((partial.state, partial.missing, partial.delivered), ("SUCCEEDED", (2, 4), 2))
        self.assertEqual((incident.state, incident.missing, incident.delivered), ("SUCCEEDED", (1,), 1))
        self.assertEqual((oom.state, oom.failure_class, oom.delivered), ("FAILED", "upstream_unavailable", 0))
        self.assertEqual((plain.state, plain.missing, plain.delivered), ("SUCCEEDED", (), 2))
        # The pictures ask for exactly the faults the case expects.
        self.assertEqual([faults for _, _, faults in partial.pictures], [(), ("failed",), (), ("rejected",)])
        self.assertEqual([faults for _, _, faults in incident.pictures], [("gpu_oom",), ()])
        self.assertEqual([(w, h) for w, h, _ in incident.pictures][0], (2048, 2048))
        self.assertEqual([faults for _, _, faults in oom.pictures], [("gpu_oom",)])
        self.assertTrue(all(faults == () for _, _, faults in plain.pictures))

    def test_a_correct_public_task_has_no_problem(self):
        for case in ff.cases():
            with self.subTest(case=case.label):
                self.assertEqual(self.problems(case), [])

    def test_every_rule_of_a_partial_success_fails_when_it_is_broken(self):
        partial = ff.cases()[0]
        for name, mutate, needle in (
            ("no notice", lambda t: t.pop("notices"), "0 notices"),
            ("two notices", lambda t: t["notices"].append(t["notices"][0]), "2 notices"),
            ("wrong positions", lambda t: t["notices"][0].update(inputs=[2]), "names the pictures"),
            ("wrong code", lambda t: t["notices"][0].update(code="other"), "notice code"),
            ("wrong sentence", lambda t: t["notices"][0].update(detail="x"), "Schema's sentence"),
            ("a failure on a success", lambda t: t.update(failure={"class": "rejected"}), "carries a failure"),
            ("missing assets", lambda t: t["assets"].pop(), "1 assets delivered, want 2"),
            ("wrong state", lambda t: t.update(state="FAILED"), "state FAILED, want SUCCEEDED"),
        ):
            with self.subTest(name):
                self.assertTrue(any(needle in problem for problem in self.problems(partial, mutate)), self.problems(partial, mutate))

    def test_a_task_that_delivered_everything_must_say_nothing(self):
        plain = ff.cases()[3]
        problems = self.problems(plain, lambda t: t.update(notices=[{"code": "partial_inputs", "inputs": [1], "detail": "x"}]))
        self.assertTrue(any("notices on a Task that delivered everything" in problem for problem in problems), problems)

    def test_every_rule_of_the_capacity_failure_fails_when_it_is_broken(self):
        oom = ff.cases()[2]
        for name, mutate, needle in (
            ("a retryable failure", lambda t: t["failure"].update(retryable=True), "retryable"),
            ("a missing retryable member", lambda t: t["failure"].pop("retryable"), "retryable"),
            ("another class", lambda t: t["failure"].update(**{"class": "rejected"}), "failure class"),
            ("another sentence", lambda t: t["failure"].update(detail="oops"), "Schema's sentence"),
            ("notices on a failure", lambda t: t.update(notices=[{"code": "partial_inputs"}]), "notices on a failed Task"),
            ("delivered pictures", lambda t: t["assets"].append({"role": "image"}), "assets delivered"),
            ("a success", lambda t: t.update(state="SUCCEEDED"), "state SUCCEEDED, want FAILED"),
        ):
            with self.subTest(name):
                self.assertTrue(any(needle in problem for problem in self.problems(oom, mutate)), self.problems(oom, mutate))

    def test_the_services_own_text_must_not_reach_the_public_projection(self):
        oom = ff.cases()[2]
        for text in ("GPU 显存不足，无法处理该图片", "降低 BIREFNET_PROCESS_RES", "SDMATTE_PROCESS_RES", "（图片尺寸: 2048×2048）"):
            with self.subTest(text=text):
                problems = self.problems(oom, lambda t: t["failure"].update(detail=t["failure"]["detail"] + text))
                self.assertTrue(any("the service's own text" in problem for problem in problems), problems)
        partial = ff.cases()[0]
        problems = self.problems(partial, lambda t: t["notices"][0].update(detail="显存不足"))
        self.assertTrue(any("the service's own text" in problem for problem in problems), problems)

    def test_a_schema_without_the_sentences_fails_instead_of_passing_on_empty_text(self):
        partial, _, oom, _ = ff.cases()
        problems = self.problems(partial, messages={})
        self.assertTrue(any("no sentence for the partial_inputs notice" in problem for problem in problems), problems)
        task = public_task(oom)
        problems = ff.case_problems(oom, task, {})
        self.assertTrue(any("no sentence for failure.upstream_unavailable" in problem for problem in problems), problems)

    def test_the_task_list_must_agree_with_the_task(self):
        task = {"state": "SUCCEEDED", "notices": [{"code": "partial_inputs"}], "failure": None}
        self.assertEqual(ff.listed_problems(dict(task), task), [])
        self.assertEqual(ff.listed_problems(None, task), ["the Task is missing from the session's task list"])
        self.assertTrue(ff.listed_problems({**task, "notices": []}, task))
        self.assertTrue(ff.listed_problems({**task, "state": "FAILED"}, task))
        self.assertTrue(ff.listed_problems({**task, "failure": {"class": "rejected"}}, task))


MARKER_PATTERN = re.compile(rb"fake-file-faults:([a-z_, ]*);")


class ScriptedApi:
    """A stand-in for a Plinth stack in front of the matting fake: it keeps what is uploaded, reads the
    marker the way the fake does and answers the public Task the way Plinth projects the batch's result."""

    def __init__(self, pending=False, drop_notices=False, leak=False, refuse_prepare=False):
        self.pending, self.drop_notices, self.leak, self.refuse_prepare = pending, drop_notices, leak, refuse_prepare
        self.log, self.assets, self.inputs, self.tasks, self.decisions = [], {}, {}, {}, []

    def faults_of(self, asset):
        words = []
        for found in MARKER_PATTERN.findall(self.assets[asset][:1 << 20]):
            words += [word.strip() for word in found.decode().split(",") if word.strip()]
        return words

    def project(self, task_id):
        assets = self.inputs[self.tasks[task_id]]
        missing = [position for position, asset in enumerate(assets, 1)
                   if {"failed", "rejected", "gpu_oom"} & set(self.faults_of(asset))]
        delivered = len(assets) - len(missing)
        picture = self.assets[assets[0]]
        task = {"id": task_id, "state": "SUCCEEDED" if delivered else "FAILED", "assets": [
            {"role": "image", "url": f"/api/v1/tasks/{task_id}/assets/{index}", "size": len(picture),
             "sha256": hashlib.sha256(picture).hexdigest()} for index in range(delivered)]}
        if not delivered:
            task["failure"] = {"class": "upstream_unavailable", "retryable": False,
                               "detail": MESSAGES["failure.upstream_unavailable"] + (" GPU 显存不足" if self.leak else "")}
        elif missing and not self.drop_notices:
            task["notices"] = [{"code": "partial_inputs", "inputs": missing,
                                "detail": ff.compose_notice(MESSAGES, "partial_inputs", missing)}]
        return task

    def upload(self, path, field, filename, data, mime):
        self.log.append(("UPLOAD", path))
        asset = f"asset_{len(self.assets) + 1}"
        self.assets[asset] = data
        return 201, {"id": asset}

    def call(self, method, path, body=None, headers=None):
        self.log.append((method, path))
        match = re.fullmatch(r"/api/v1/tasks/(task_\d+)/assets/(\d+)", path)
        if match:
            return 200, self.assets[self.inputs[self.tasks[match.group(1)]][0]], {}
        return 404, b"", {}

    def json(self, method, path, body=None, headers=None):
        self.log.append((method, path))
        if (method, path) == ("GET", "/api/v1/schema"):
            return 200, {"schema": {"content": {"messages": MESSAGES}, "chat": {"api": {"fields": {"decision": "decision"}}}}}
        if (method, path) == ("POST", "/api/chat/sessions"):
            return 201, {"id": "session_1"}
        if (method, path) == ("POST", "/api/v1/tasks/prepare"):
            if self.refuse_prepare:
                return 400, {"error": "bad input"}
            assert body["offering_id"] == ff.MATTING and body["capability"] == ff.CAPABILITY
            self.inputs[f"prep_{len(self.inputs) + 1}"] = body["input"]["source_assets"]
            return 200, {"prepared_input_id": f"prep_{len(self.inputs)}"}
        if (method, path) == ("POST", "/api/v1/tasks/grants"):
            return 200, {"request_id": "req_" + body["prepared_input_id"], "status": "pending" if self.pending else "approved"}
        if method == "PUT" and path.startswith("/api/v1/tasks/grants/"):
            self.decisions.append(body)
            return 200, {}
        if (method, path) == ("POST", "/api/v1/tasks"):
            task_id = f"task_{len(self.tasks) + 1}"
            self.tasks[task_id] = body["prepared_input_id"]
            return 202, {"task_id": task_id}
        if method == "GET" and re.fullmatch(r"/api/v1/tasks/task_\d+", path):
            return 200, self.project(path.rsplit("/", 1)[1])
        if method == "GET" and path.startswith("/api/v1/tasks?session_id=session_1"):
            return 200, {"tasks": [self.project(task_id) for task_id in self.tasks]}
        return 404, {}


class RunTest(unittest.TestCase):
    def run_checks(self, api, wait=1):
        recorded = []
        ff.run(api, lambda name, ok, detail: recorded.append((name, ok, detail)), wait_seconds=wait)
        return recorded

    def test_a_stack_that_behaves_passes_every_case(self):
        api = ScriptedApi()
        recorded = self.run_checks(api)
        self.assertEqual([ok for _, ok, _ in recorded], [True] * 4, recorded)
        self.assertEqual([name for name, _, _ in recorded], [case.label for case in ff.cases()])

    def test_every_batch_is_submitted_before_any_is_waited_for(self):
        api = ScriptedApi()
        self.run_checks(api)
        submits = [index for index, entry in enumerate(api.log) if entry == ("POST", "/api/v1/tasks")]
        polls = [index for index, entry in enumerate(api.log) if re.fullmatch(r"/api/v1/tasks/task_\d+", entry[1]) and entry[0] == "GET"]
        self.assertEqual(len(submits), 4)
        self.assertLess(max(submits), min(polls), "the fake must hold the batches together")

    def test_the_pictures_that_are_uploaded_carry_the_cases_markers(self):
        api = ScriptedApi()
        self.run_checks(api)
        first = [api.faults_of(asset) for asset in api.inputs["prep_1"]]
        self.assertEqual(first, [[], ["failed"], [], ["rejected"]])
        second = [api.faults_of(asset) for asset in api.inputs["prep_2"]]
        self.assertEqual(second, [["gpu_oom"], []])
        self.assertEqual([api.faults_of(asset) for asset in api.inputs["prep_3"]], [["gpu_oom"]])
        self.assertEqual([api.faults_of(asset) for asset in api.inputs["prep_4"]], [[], []])
        for asset in api.assets.values():
            self.assertTrue(chunks(asset), "every upload is a valid PNG")

    def test_a_pending_grant_is_approved_with_the_schemas_decision_member(self):
        api = ScriptedApi(pending=True)
        recorded = self.run_checks(api)
        self.assertEqual(api.decisions, [{"decision": "approve"}] * 4)
        self.assertTrue(all(ok for _, ok, _ in recorded), recorded)

    def test_a_missing_notice_fails_the_partial_cases_only(self):
        recorded = self.run_checks(ScriptedApi(drop_notices=True))
        self.assertEqual([ok for _, ok, _ in recorded], [False, False, True, True], recorded)

    def test_the_services_own_text_in_the_public_failure_fails_the_capacity_case(self):
        recorded = self.run_checks(ScriptedApi(leak=True))
        self.assertEqual([ok for _, ok, _ in recorded], [True, True, False, True], recorded)
        self.assertIn("the service's own text", recorded[2][2])

    def test_a_refused_submission_is_reported_with_its_step(self):
        recorded = self.run_checks(ScriptedApi(refuse_prepare=True))
        self.assertEqual([ok for _, ok, _ in recorded], [False] * 4)
        self.assertTrue(all("prepare 400" in detail for _, _, detail in recorded), recorded)

    def test_a_stack_without_schema_messages_fails_before_it_submits_anything(self):
        api = ScriptedApi()
        api.json = lambda method, path, body=None, headers=None: (200, {"schema": {}}) if path == "/api/v1/schema" else (404, {})
        recorded = self.run_checks(api)
        self.assertEqual(len(recorded), 1)
        self.assertFalse(recorded[0][1])
        self.assertEqual(api.assets, {})


class ScriptGateTest(unittest.TestCase):
    ENV = {"ART_AI_UPSTREAM_SHAPE": "fake", "BASE": "http://stack.example.test:8080", "ADMIN_USERNAME": "admin",
           "ADMIN_PASSWORD": "never-printed-secret"}

    def run_main(self, argv, environ):
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = script.main(argv, environ)
        return code, out.getvalue(), err.getvalue()

    def test_a_target_that_is_not_the_fake_is_refused_before_any_request(self):
        for shape in ({"ART_AI_UPSTREAM_SHAPE": "live"}, {"ART_AI_UPSTREAM_SHAPE": "mock"}, {}):
            with self.subTest(shape=shape), mock.patch.object(ff, "Client") as client:
                environ = {key: value for key, value in self.ENV.items() if key != "ART_AI_UPSTREAM_SHAPE"}
                code, out, err = self.run_main([], {**environ, **shape})
                self.assertEqual(code, 2)
                self.assertIn("does not run the fake upstream", err)
                client.assert_not_called()

    def test_the_script_has_no_live_mode(self):
        for argv in (["--live"], ["x"]):
            with self.subTest(argv=argv), mock.patch.object(ff, "Client") as client:
                code, _, err = self.run_main(argv, {**self.ENV, "ART_AI_LIVE_ACK": "1"})
                self.assertEqual(code, 2)
                self.assertIn("no arguments", err)
                client.assert_not_called()

    def test_missing_or_malformed_environment_is_refused(self):
        for drop in ("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD"):
            with self.subTest(drop=drop), mock.patch.object(ff, "Client") as client:
                code, _, err = self.run_main([], {key: value for key, value in self.ENV.items() if key != drop})
                self.assertEqual(code, 2)
                self.assertIn(drop, err)
                client.assert_not_called()
        for base in ("ftp://x", "http://x/path", "http://user:pw@x", "http://x?query=1", "nonsense"):
            with self.subTest(base=base), mock.patch.object(ff, "Client") as client:
                code, _, err = self.run_main([], {**self.ENV, "BASE": base})
                self.assertEqual(code, 2)
                self.assertNotIn("pw", err)
                client.assert_not_called()

    def test_help_prints_the_description_and_runs_nothing(self):
        with mock.patch.object(ff, "Client") as client:
            code, out, _ = self.run_main(["--help"], {})
            self.assertEqual(code, 0)
            self.assertIn("#301", out)
            client.assert_not_called()

    def stubbed_client(self, login_status=200):
        client = mock.Mock()
        client.login.return_value = (login_status, {})
        return mock.patch.object(ff, "Client", return_value=client), client

    def test_every_check_passing_exits_zero_and_prints_no_credentials(self):
        patch_client, client = self.stubbed_client()
        with patch_client, mock.patch.object(ff, "run", side_effect=lambda api, record: [record("a", True, "ok"), record("b", True, "ok")]) as run:
            code, out, err = self.run_main([], self.ENV)
        self.assertEqual(code, 0)
        run.assert_called_once()
        client.login.assert_called_once_with("admin", "never-printed-secret")
        self.assertIn("RESULT: 2 passed, 0 failed", out)
        self.assertNotIn("never-printed-secret", out + err)

    def test_a_failed_check_exits_one(self):
        patch_client, _ = self.stubbed_client()
        with patch_client, mock.patch.object(ff, "run", side_effect=lambda api, record: [record("a", True, "ok"), record("b", False, "bad")]):
            code, out, _ = self.run_main([], self.ENV)
        self.assertEqual(code, 1)
        self.assertIn("FAIL: b | bad", out)
        self.assertIn("RESULT: 1 passed, 1 failed", out)

    def test_a_refused_login_exits_one_without_running_the_checks(self):
        patch_client, _ = self.stubbed_client(login_status=401)
        with patch_client, mock.patch.object(ff, "run") as run:
            code, out, err = self.run_main([], self.ENV)
        self.assertEqual(code, 1)
        run.assert_not_called()
        self.assertIn("FAIL: login | status 401", out)
        self.assertNotIn("never-printed-secret", out + err)

    def test_no_check_at_all_is_not_a_pass(self):
        patch_client, _ = self.stubbed_client()
        with patch_client, mock.patch.object(ff, "run"):
            code, _, _ = self.run_main([], self.ENV)
        self.assertEqual(code, 1)


class StackScenarioTest(unittest.TestCase):
    def test_the_scenario_runs_the_shared_checks_with_the_stacks_record(self):
        with mock.patch.object(stack.fake_faults, "run") as run:
            stack.scenario_matting_faults("api", {stack.MATTING: {}})
        run.assert_called_once_with("api", stack.record)

    def test_a_stack_without_the_matting_offering_fails_instead_of_skipping(self):
        stack.results.clear()
        with mock.patch.object(stack.fake_faults, "run") as run, redirect_stdout(io.StringIO()):
            stack.scenario_matting_faults("api", {})
        run.assert_not_called()
        self.assertEqual([ok for _, ok in stack.results], [False])
        stack.results.clear()

    def test_the_scenario_is_selectable_and_part_of_the_full_run(self):
        source = inspect.getsource(stack.main)
        # The default set (the full run), the group of scenarios that share the main stack, and the call.
        self.assertGreaterEqual(source.count('"matting-faults"'), 3)
        self.assertIn("images,matting-faults,tripo", source, "the --only help names it")
        self.assertIn("scenario_matting_faults(api, offerings)", source)


if __name__ == "__main__":
    unittest.main()
