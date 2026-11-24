#!/usr/bin/env python3
"""Offline tests for the live output evaluation's model selection and send waiting (#444).

No network and no server: the runner is loaded as a module and driven with
fake clients, so the 3MF-before-GLB delivery order and the 409 turn-active
retry are checked without a paid request.
"""
import contextlib
import importlib.util
import io
import os
import pathlib
import sys
import tempfile
import unittest
import unittest.mock as mock

sys.dont_write_bytecode = True
SMOKE = pathlib.Path(__file__).resolve().parents[1] / "smoke"
if str(SMOKE) not in sys.path:
    sys.path.insert(0, str(SMOKE))


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), SMOKE / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


run = load("eval-agent-output")

BUSY = {"error": "当前会话仍有任务正在处理，请等待完成后重试", "request_id": "req_busy"}
BUSY_EN = {"error": "This session still has a task in progress. Please wait for it to finish and retry"}
CHOICE_CLOSED = {"error": "这个选择已经作答或已失效，请刷新后查看"}
FINAL = {"role": "assistant", "content": "完成。"}
PENDING = {"role": "assistant", "content": "", "isPending": True}


def asset(role="model", format_name="", media="", url="https://assets.test/model"):
    item = {"role": role, "required": True, "url": url}
    if format_name:
        item["format"] = format_name
    if media:
        item["media_type"] = media
    return item


def meshy_delivery():
    """Meshy's 3D delivery order from #444: the 3MF comes first, the GLB third."""
    return [
        asset(format_name="3MF", media="model/3mf", url="https://assets.test/a-3mf"),
        asset(format_name="FBX", media="application/octet-stream", url="https://assets.test/a-fbx"),
        asset(format_name="GLB", media="model/gltf-binary", url="https://assets.test/a-glb"),
        asset(format_name="OBJ", media="application/x-tgif", url="https://assets.test/a-obj"),
        asset(format_name="STL", media="model/stl", url="https://assets.test/a-stl"),
        asset(format_name="USDZ", media="application/zip", url="https://assets.test/a-usdz"),
        {"role": "thumbnail", "required": True, "media_type": "image/png", "url": "https://assets.test/a-png"},
    ]


class FakeClient:
    """The runner's HTTP client: fixed responses and a fixed chat-stream outcome."""

    def __init__(self, responses, stream="done"):
        self.responses = list(responses)
        self.stream = stream
        self.calls = []
        self.stream_calls = 0

    def call(self, method, path, body=None):
        self.calls.append({"method": method, "path": path, "body": body})
        return self.responses.pop(0) if len(self.responses) > 1 else self.responses[0]

    def read_chat_stream(self, session, timeout):
        self.stream_calls += 1
        return self.stream


class FakeLive:
    """The message and stream reads the send path needs, plus Live's card bookkeeping."""

    def __init__(self, client, replies):
        self.client = client
        self.replies = list(replies)
        self.messages_calls = 0
        self._last_send = {}

    def messages(self, session):
        index = min(self.messages_calls, len(self.replies) - 1)
        self.messages_calls += 1
        return [self.replies[index]]


def no_sleep(_seconds):
    return None


class ModelAssetTest(unittest.TestCase):
    def test_the_glb_wins_over_the_3mf_that_comes_first(self):
        chosen, name = run.model_asset({"assets": meshy_delivery()})
        self.assertEqual(name, "glb")
        self.assertEqual(chosen["format"], "GLB")
        self.assertEqual(chosen["url"], "https://assets.test/a-glb")

    def test_the_gltf_json_file_is_the_second_choice(self):
        assets = [asset(format_name="3MF", media="model/3mf"),
                  asset(role="converted_model", format_name="glTF", media="model/gltf+json"),
                  asset(format_name="FBX", media="application/octet-stream")]
        chosen, name = run.model_asset({"assets": assets})
        self.assertEqual(name, "gltf")
        self.assertEqual(chosen["media_type"], "model/gltf+json")

    def test_the_media_type_selects_the_glb_when_the_format_name_is_missing(self):
        assets = [asset(media="application/zip"),
                  asset(media="model/gltf-binary"),
                  asset(media="model/3mf")]
        chosen, name = run.model_asset({"assets": assets})
        self.assertEqual(name, "glb")
        self.assertEqual(chosen["media_type"], "model/gltf-binary")

    def test_a_task_delivering_only_3mf_names_that_format_instead_of_inspecting_it(self):
        assets = [asset(format_name="3MF", media="model/3mf")]
        chosen, name = run.model_asset({"assets": assets})
        self.assertEqual(name, "3mf")
        detail = run.unsupported_model_detail(chosen, name)
        self.assertIn("unsupported format: 3MF (model/3mf)", detail)
        self.assertNotIn("bad GLB magic", detail)

    def test_the_meshy_formats_are_read_from_the_task_projection(self):
        self.assertEqual(run.asset_format({"format": "GLB", "media_type": "model/3mf"}), "glb")
        self.assertEqual(run.asset_format({"format": "glTF", "media_type": "model/gltf+json"}), "gltf")
        self.assertEqual(run.asset_format({"media_type": "model/gltf-binary"}), "glb")
        self.assertEqual(run.asset_format({"media_type": "application/x-tgif", "url": "https://a/model.obj"}), "obj")
        self.assertEqual(run.asset_format({"media_type": "application/zip"}), "")

    def test_assets_that_are_not_model_files_are_ignored(self):
        assets = [{"role": "thumbnail", "required": True, "media_type": "image/png", "url": "https://a/p.png"},
                  {"role": "model", "required": False, "format": "GLB", "url": "https://a/p.glb"}]
        self.assertEqual(run.model_asset({"assets": assets}), (None, ""))
        self.assertEqual(run.model_asset({}), (None, ""))

    def test_the_glb_container_magic_decides_for_a_gltf_labelled_file(self):
        with tempfile.TemporaryDirectory() as directory:
            glb = pathlib.Path(directory) / "a.glb"
            glb.write_bytes(b"glTF\x02\x00\x00\x00")
            json_file = pathlib.Path(directory) / "b.gltf"
            json_file.write_bytes(b'{"asset":{"version":"2.0"}}')
            zip_file = pathlib.Path(directory) / "c.3mf"
            zip_file.write_bytes(b"PK\x03\x04")
            self.assertTrue(run.glb_container(glb))
            self.assertFalse(run.glb_container(json_file))
            self.assertFalse(run.glb_container(zip_file))
            self.assertFalse(run.glb_container(pathlib.Path(directory) / "missing.glb"))


class SendMessageTest(unittest.TestCase):
    def test_the_first_message_does_not_wait_for_a_turn(self):
        client = FakeClient([(202, {})])
        live = FakeLive(client, [PENDING])
        stats = run.sending()
        status, _ = run.send_message(live, "s1", "第一条", "image", "m", stats, sleep=no_sleep)
        self.assertEqual(status, 202)
        self.assertEqual(stats["sends"], 1)
        self.assertEqual(stats["idle_waits"], 0)
        self.assertEqual(live.messages_calls, 0)
        self.assertEqual(len(client.calls), 1)

    def test_a_follow_up_waits_for_the_running_turn_first(self):
        client = FakeClient([(202, {}), (202, {})])
        live = FakeLive(client, [PENDING, FINAL])
        stats = run.sending()
        run.send_message(live, "s1", "第一条", "model-3d", "m", stats, sleep=no_sleep)
        status, _ = run.send_message(live, "s1", "追问", "model-3d", "m", stats, sleep=no_sleep)
        self.assertEqual(status, 202)
        self.assertEqual(stats["sends"], 2)
        self.assertEqual(stats["idle_waits"], 1)
        self.assertEqual(live.messages_calls, 2)
        self.assertEqual([call["body"]["content"] for call in client.calls], ["第一条", "追问"])

    def test_a_busy_session_is_retried_with_the_same_body(self):
        client = FakeClient([(409, BUSY), (202, {})])
        live = FakeLive(client, [FINAL])
        stats = run.sending()
        sleeps = []
        status, _ = run.send_message(live, "s1", "追问", "model-3d", "m", stats, sleep=sleeps.append)
        self.assertEqual(status, 202)
        self.assertEqual(stats["busy_retries"], 1)
        self.assertEqual(stats["busy_wait_ms"], run.BUSY_RETRY_SECONDS * 1000)
        self.assertEqual(stats["rejected"], 0)
        self.assertEqual(sleeps, [run.BUSY_RETRY_SECONDS])
        self.assertEqual(client.calls[0]["body"], client.calls[1]["body"])

    def test_a_busy_session_reported_in_english_is_retried_too(self):
        client = FakeClient([(409, BUSY_EN), (202, {})])
        live = FakeLive(client, [FINAL])
        stats = run.sending()
        status, _ = run.send_message(live, "s1", "go", "model-3d", "m", stats, sleep=no_sleep)
        self.assertEqual(status, 202)
        self.assertEqual(stats["busy_retries"], 1)

    def test_a_closed_choice_card_is_not_retried(self):
        client = FakeClient([(409, CHOICE_CLOSED)])
        live = FakeLive(client, [FINAL])
        stats = run.sending()
        status, _ = run.send_message(live, "s1", "按这个用途继续。", "image", "m", stats,
                                     extra={"choiceAnswer": {"choice_id": "c1", "option_id": "o1"}},
                                     sleep=no_sleep)
        self.assertEqual(status, 409)
        self.assertEqual(stats["busy_retries"], 0)
        self.assertEqual(stats["rejected"], 1)
        self.assertEqual(len(client.calls), 1)

    def test_a_turn_that_never_ends_burns_the_bounded_retries_and_reports_the_rejection(self):
        client = FakeClient([(409, BUSY)])
        live = FakeLive(client, [FINAL])
        stats = run.sending()
        status, _ = run.send_message(live, "s1", "追问", "model-3d", "m", stats, retry_limit=2, sleep=no_sleep)
        self.assertEqual(status, 409)
        self.assertEqual(stats["busy_retries"], 3)
        self.assertEqual(stats["rejected"], 1)
        self.assertEqual(len(client.calls), 3)

    def test_the_wait_for_the_turn_is_bounded(self):
        client = FakeClient([(202, {})])
        live = FakeLive(client, [PENDING])
        stats = run.sending()
        self.assertFalse(run.wait_turn_end(live, "s1", stats, timeout=0, sleep=no_sleep))
        self.assertEqual(stats["idle_waits"], 1)

    def test_a_cached_card_answer_keeps_the_last_request_fields(self):
        client = FakeClient([(202, {})])
        live = FakeLive(client, [FINAL])
        stats = run.sending()
        run.send_message(live, "s1", "按默认选项继续。", "image", "m", stats,
                         extra={"choiceAnswer": {"choice_id": "c1", "option_id": "o1"}}, sleep=no_sleep)
        self.assertEqual(live._last_send, {})
        run.send_message(live, "s1", "追问", "image", "m", stats, extra={"parameters": {"a": 1}},
                         sleep=no_sleep)
        self.assertEqual(live._last_send["s1"], ("image", "m", {"parameters": {"a": 1}}))

    def test_session_busy_reads_the_turn_active_text_only(self):
        self.assertTrue(run.session_busy(409, BUSY))
        self.assertTrue(run.session_busy(409, BUSY_EN))
        self.assertTrue(run.session_busy(409, {}))
        self.assertFalse(run.session_busy(409, CHOICE_CLOSED))
        self.assertFalse(run.session_busy(202, BUSY))
        self.assertFalse(run.session_busy(500, {"error": "boom"}))


class StubClient:
    """A chat client whose turn ends at once: no stream is held open."""

    def call(self, method, path, body=None):
        return 202, {}

    def read_chat_stream(self, session, timeout):
        return "done"


class StubLive:
    """Just enough of Live for run_case: one turn, no cards, no tasks."""

    def __init__(self):
        self.client = StubClient()
        self._last_send = {}

    def new_session(self, approval="auto"):
        return "s1"

    def lock_model_3d(self, session, model):
        return {}

    def pending_choices(self, session):
        return []

    def pending_approvals(self, session):
        return []

    def messages(self, session):
        return [{"role": "assistant", "content": "Fake upstream reply."}]

    def tasks(self, session):
        return []

    def wait_tasks(self, session, counts, timeout=None):
        return [], True


class RunnerReportingTest(unittest.TestCase):
    """#498: what the live runner can and cannot report."""

    def case(self, request):
        args = run.parse_arguments(["--report", tempfile.gettempdir()])
        return run.quality.evaluate_case(request, run.run_case(StubLive(), request, {}, {}, args))

    def test_the_live_run_declares_that_it_never_sends_feedback(self):
        data, _path, _digest = run.quality.load_requests()
        request = next(item for item in data["requests"] if item["id"] == "2d-preference-multiturn")
        case = self.case(request)
        checks = {check["id"]: check["status"] for check in case["checks"]}
        for name in ("preference:captured", "preference:injected", "preference:after_compaction",
                     "preference:edited", "preference:deleted"):
            self.assertEqual(checks[name], run.quality.CHECK_SKIP)
        self.assertNotIn("preference:captured",
                         {check["id"] for check in run.quality.case_hard_failures(case)})

    def test_an_unknown_request_id_is_refused_instead_of_running_a_subset(self):
        environ = {"BASE": "http://127.0.0.1:1", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p",
                   "ART_AI_UPSTREAM_SHAPE": "live", "ART_AI_LIVE_ACK": "1", "PATH": os.environ["PATH"]}
        with mock.patch.dict(os.environ, environ, clear=True):
            with contextlib.redirect_stderr(io.StringIO()) as err:
                code = run.main(["--live", "--report", tempfile.gettempdir(), "--only", "no-such-case"])
        self.assertEqual(code, 2)
        self.assertIn("unknown request id", err.getvalue())

    def test_an_interrupt_says_so_and_exits_nonzero(self):
        with mock.patch.object(run, "run", side_effect=KeyboardInterrupt):
            with contextlib.redirect_stderr(io.StringIO()) as err:
                code = run.main(["--live", "--report", tempfile.gettempdir()])
        self.assertEqual(code, 130)
        self.assertIn("interrupted", err.getvalue())

    def test_a_crash_says_what_happened_and_exits_nonzero(self):
        with mock.patch.object(run, "run", side_effect=RuntimeError("boom")):
            with contextlib.redirect_stderr(io.StringIO()) as err:
                code = run.main(["--live", "--report", tempfile.gettempdir()])
        self.assertEqual(code, 1)
        self.assertIn("boom", err.getvalue())


class CrashingLive:
    """A Live that cannot even open a session, to prove a half-run crash reports."""

    def __init__(self, client, report, arguments):
        self.arguments = arguments

    def login(self):
        return True

    def public_schema(self):
        return {}

    def offerings(self, capability):
        return []

    def new_session(self, approval="auto"):
        raise RuntimeError("boom")


class CrashReportingTest(unittest.TestCase):
    def test_a_crash_mid_run_still_writes_a_report_and_exits_nonzero(self):
        environ = {"BASE": "http://127.0.0.1:1", "ADMIN_USERNAME": "u", "ADMIN_PASSWORD": "p",
                   "ART_AI_UPSTREAM_SHAPE": "live", "ART_AI_LIVE_ACK": "1", "PATH": os.environ["PATH"]}
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.dict(os.environ, environ, clear=True):
                with mock.patch.object(run.common, "Live", CrashingLive):
                    code = run.main(["--live", "--report", directory, "--only", "2d-icon-set"])
            report = pathlib.Path(directory) / "agent-quality-output-run.json"
            self.assertEqual(code, 1)
            self.assertTrue(report.exists(), "a crash must leave a report behind")
            self.assertIn("script error", report.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()