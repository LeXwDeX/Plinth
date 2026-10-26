#!/usr/bin/env python3
"""Offline contract for the L1 Agent latest-task archive checks.

Run the real section 9 of smoke-l1-agent-mode.sh against stubbed HTTP responses.
"""
import json
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("smoke-l1-agent-mode.sh")
FORMATS = [{"id": name, "status": "available"} for name in ("glb", "gltf", "fbx", "obj", "stl", "usdz", "3mf")]
CAPABILITIES = ["text-to-3d", "image-to-3d", "uv-unwrap", "remesh", "retexture", "rigging"]
PREAMBLE = r'''
set -u
T="$1"; HERE="$2"
BASE="stub://offline"; JAR="$T/cookies"; PASS=0; FAIL=0; SKIP=0
SMOKE_CATALOG="$T/catalog.json"
ok() { PASS=$((PASS+1)); printf 'PASS: %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf 'FAIL: %s\n' "$1"; }
skip() { SKIP=$((SKIP+1)); printf 'SKIP: %s\n' "$1"; }
jget() { python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    v = eval(sys.argv[2])
    print("" if v is None else v)
except Exception:
    print("")' "$1" "$2" 2>/dev/null; }
req() {
  local out="$1"; local url="$2"
  printf '%s\n' "$url" >> "$T/requests"
  if [ "$url" = "$BASE/api/chat/sessions?limit=100" ]; then
    cp "$T/sessions.json" "$out"; cat "$T/sessions-status"
  elif [[ "$url" == "$BASE/api/v1/tasks?capability="*"&state=SUCCEEDED&limit=100" ]]; then
    cp "$T/tasks.json" "$out"; cat "$T/tasks-status"
  elif [[ "$url" == "$BASE/api/v1/tasks/"*"/formats" ]]; then
    local id="${url#"$BASE/api/v1/tasks/"}"; id="${id%/formats}"
    if [ -f "$T/format-status-$id" ]; then
      if [ -f "$T/format-$id.json" ]; then cp "$T/format-$id.json" "$out"; else : > "$out"; fi
      cat "$T/format-status-$id"
    else
      : > "$out"; printf 404
    fi
  elif [[ "$url" == "$BASE/api/v1/tasks/"*"/archive" ]]; then
    local id="${url#"$BASE/api/v1/tasks/"}"; id="${id%/archive}"
    if [ -f "$T/archive-status-$id" ]; then
      if [ -f "$T/archive-$id.json" ]; then cp "$T/archive-$id.json" "$out"; else : > "$out"; fi
      cat "$T/archive-status-$id"
    else
      : > "$out"; printf 500
    fi
  else
    : > "$out"; printf 99
  fi
}
'''
READ_ARCHIVE = r'''#!/usr/bin/env bash
set -u
url="$1"; out="$3"
printf '%s\n' "$url" >> "$out/zip-requests"
id="${url##*/tasks/}"; id="${id%/archive}"
if [ -f "$out/zip-status-$id" ]; then
  if [ -f "$out/zip-output-$id" ]; then cat "$out/zip-output-$id"; fi
  exit "$(cat "$out/zip-status-$id")"
fi
echo "stub archive"
exit 0
'''


def coverage(applicable=True, archive=True, with_field=True):
    value = {"applicable": applicable, "formats": FORMATS}
    if with_field:
        value["archive_available"] = archive
    return value


def task_list(*tasks):
    return {"tasks": [
        {"created_at": f"2026-10-23T00:00:{index:02d}Z", "id": task, "session_id": "session-1"}
        for index, task in enumerate(tasks)
    ]}


class ArchiveContractTests(unittest.TestCase):
    def run_case(self, tasks, coverage_by_task, archive_by_task=None,
                 zip_status=None, sessions_status=200, tasks_status=200,
                 format_status=None):
        archive_by_task = archive_by_task or {}
        zip_status = zip_status or {}
        format_status = format_status or {}
        source = SCRIPT.read_text()
        self.assertIn("task_candidates() {", source)
        self.assertIn("MAX_TASK_CANDIDATES=500", source)
        block = source.split("# 9. 已有 3D 任务", 1)[1].split("\n", 1)[1].split(
            "# 10. 已发布的 Agent 配置", 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            here = root / "bin"
            here.mkdir()
            (here / "read-archive.sh").write_text(READ_ARCHIVE)
            (root / "catalog.json").write_text(json.dumps(
                {"application": {"model_delivery": {"formats": FORMATS, "capabilities": CAPABILITIES}}}))
            (root / "sessions.json").write_text(json.dumps({"sessions": [{"id": "session-1"}]}))
            (root / "sessions-status").write_text(str(sessions_status))
            (root / "tasks.json").write_text(json.dumps(tasks))
            (root / "tasks-status").write_text(str(tasks_status))
            for task, value in coverage_by_task.items():
                (root / f"format-{task}.json").write_text(
                    value if isinstance(value, str) else json.dumps(value))
                (root / f"format-status-{task}").write_text(str(format_status.get(task, 200)))
            for task, (status, body) in archive_by_task.items():
                (root / f"archive-status-{task}").write_text(str(status))
                (root / f"archive-{task}.json").write_text(
                    body if isinstance(body, str) else json.dumps(body))
            for task, status in zip_status.items():
                (root / f"zip-status-{task}").write_text(str(status))
            (root / "zip-output-task-old").write_text("full ZIP check: 2 entries")
            result = subprocess.run(
                ["bash", "-s", "--", str(root), str(here)],
                input=PREAMBLE + block +
                '\nprintf "COUNTS:%s:%s:%s\\n" "$PASS" "$FAIL" "$SKIP"\n[ "$FAIL" = 0 ]\n',
                text=True, capture_output=True, timeout=30,
            )
            counts = next((line for line in result.stdout.splitlines()
                           if line.startswith("COUNTS:")), "COUNTS:0:1:0")
            passed, failed, skipped = (int(x) for x in counts.split(":")[1:])
            requests = (root / "requests").read_text() if (root / "requests").exists() else ""
            zips = (root / "zip-requests").read_text() if (root / "zip-requests").exists() else ""
            return result, passed, failed, skipped, requests, zips

    def test_latest_applicable_skips_a_newer_non_applicable_task(self):
        result, _, failed, _, requests, _ = self.run_case(
            task_list("task-old", "task-new"),
            {"task-new": coverage(applicable=False), "task-old": coverage(archive=True)},
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(failed, 0, result.stdout)
        self.assertIn("/tasks/task-old/formats", requests)
        self.assertIn("newest applicable SUCCEEDED 3D task in the bounded candidate set: task-old", result.stdout)
        self.assertIn("capability=uv-unwrap&state=SUCCEEDED", requests)

    def test_task_list_read_error_fails_without_a_skip(self):
        for name, kwargs in (("sessions", {"sessions_status": 503}), ("tasks", {"tasks_status": 503})):
            with self.subTest(name=name):
                result, _, failed, skipped, requests, zips = self.run_case(
                    task_list("task-new"), {"task-new": coverage(archive=True)}, **kwargs,
                )
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertGreaterEqual(failed, 1, result.stdout)
                self.assertEqual(skipped, 0, result.stdout)
                self.assertNotIn("/tasks/task-new/formats", requests)
                self.assertEqual(zips, "")

    def test_formats_read_error_on_the_newest_candidate_fails_without_fallback(self):
        result, _, failed, _, requests, zips = self.run_case(
            task_list("task-old", "task-new"),
            {"task-new": coverage(archive=True), "task-old": coverage(archive=True)},
            format_status={"task-new": 503},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertNotIn("/tasks/task-old/formats", requests)
        self.assertEqual(zips, "")

    def test_newest_oversized_falls_back_to_an_eligible_older_task(self):
        result, passed, failed, skipped, requests, zips = self.run_case(
            task_list("task-old", "task-new"),
            {"task-new": coverage(archive=False), "task-old": coverage(archive=True)},
            {"task-new": (413, {"code": "too_large", "detail": "over the limit"})},
            {"task-old": 0},
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(failed, 0, result.stdout)
        self.assertEqual(skipped, 0, result.stdout)
        self.assertGreaterEqual(passed, 3, result.stdout)
        self.assertIn("too_large", result.stdout)
        self.assertIn("/tasks/task-old/archive", zips)
        self.assertNotIn("/tasks/task-new/archive", zips)

    def test_latest_oversized_older_candidate_malformed_coverage_fails(self):
        bad_formats = {
            "missing": {"applicable": True, "archive_available": True},
            "list type": {"applicable": True, "archive_available": True, "formats": {"id": "glb"}},
            "bad item": {"applicable": True, "archive_available": True, "formats": ["glb"]},
            "bad status": {"applicable": True, "archive_available": True,
                           "formats": FORMATS[:1] + [dict(FORMATS[1], status=1)] + FORMATS[2:]},
            "duplicate ids": {"applicable": True, "archive_available": True,
                              "formats": FORMATS + [FORMATS[0]]},
            "missing id": {"applicable": True, "archive_available": True,
                           "formats": FORMATS[:-1]},
            "extra id": {"applicable": True, "archive_available": True,
                         "formats": FORMATS + [{"id": "extra", "status": "available"}]},
        }
        for name, value in bad_formats.items():
            with self.subTest(name=name):
                result, _, failed, _, requests, zips = self.run_case(
                    task_list("task-old", "task-new"),
                    {"task-new": coverage(archive=False), "task-old": value},
                    {"task-new": (413, {"code": "too_large"})},
                )
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertGreaterEqual(failed, 1, result.stdout)
                self.assertIn("/tasks/task-old/formats", requests)
                self.assertEqual(zips, "")

    def test_latest_malformed_formats_fails_without_zip(self):
        result, _, failed, _, requests, zips = self.run_case(
            task_list("task-new"),
            {"task-new": {"applicable": True, "archive_available": True}},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertIn("/tasks/task-new/formats", requests)
        self.assertEqual(zips, "")

    def test_latest_oversized_empty_archive_409_fails(self):
        result, _, failed, _, requests, zips = self.run_case(
            task_list("task-new"),
            {"task-new": coverage(archive=False)},
            {"task-new": (409, {"code": "empty"})},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertEqual(zips, "")

    def test_available_coverage_but_413_is_a_failure(self):
        # read-archive.sh exits non-zero when the real response is 413.
        result, _, failed, _, _, zips = self.run_case(
            task_list("task-new"),
            {"task-new": coverage(archive=True)},
            zip_status={"task-new": 1},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertIn("/tasks/task-new/archive", zips)
        self.assertNotIn("/tasks/task-old/archive", zips)

    def test_false_coverage_with_a_200_archive_is_a_failure(self):
        result, _, failed, _, requests, zips = self.run_case(
            task_list("task-old", "task-new"),
            {"task-new": coverage(archive=False), "task-old": coverage(archive=True)},
            {"task-new": (200, {"detail": "unexpected 200"})},
            {"task-old": 0},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertNotIn("/tasks/task-old/formats", requests)
        self.assertEqual(zips, "")

    def test_missing_or_malformed_archive_available_is_a_failure(self):
        cases = {
            "missing": coverage(with_field=False),
            "string": coverage() | {"archive_available": "false"},
            "null": coverage() | {"archive_available": None},
            "number": coverage() | {"archive_available": 0},
            "applicable string": coverage(applicable="yes"),
        }
        for name, value in cases.items():
            with self.subTest(name=name):
                result, _, failed, _, requests, zips = self.run_case(
                    task_list("task-old", "task-new"),
                    {"task-new": value, "task-old": coverage(archive=True)},
                    {"task-new": (200, {"code": "too_large"})},
                    {"task-old": 0},
                )
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertGreaterEqual(failed, 1, result.stdout)
                self.assertNotIn("/tasks/task-old/formats", requests)
                self.assertEqual(zips, "")

    def test_wrong_413_code_or_body_is_a_failure(self):
        for body in ({"code": "conflict"}, {"detail": "missing code"}, "not json"):
            with self.subTest(body=body):
                result, _, failed, _, _, zips = self.run_case(
                    task_list("task-new"),
                    {"task-new": coverage(archive=False)},
                    {"task-new": (413, body)},
                )
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertGreaterEqual(failed, 1, result.stdout)
                self.assertEqual(zips, "")

    def test_no_eligible_archive_fails(self):
        result, _, failed, _, _, zips = self.run_case(
            task_list("task-old", "task-new"),
            {"task-new": coverage(archive=False), "task-old": coverage(archive=False)},
            {"task-new": (413, {"code": "too_large"}),
             "task-old": (413, {"code": "too_large"})},
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertGreaterEqual(failed, 1, result.stdout)
        self.assertIn("no eligible", result.stdout)
        self.assertEqual(zips, "")

    def test_no_3d_task_still_skips(self):
        result, _, failed, skipped, _, zips = self.run_case(task_list(), {})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(failed, 0, result.stdout)
        self.assertEqual(skipped, 1, result.stdout)
        self.assertIn("no SUCCEEDED 3D task", result.stdout)
        self.assertEqual(zips, "")

    def test_candidate_list_is_bounded_newest_first_and_deduplicated(self):
        source = SCRIPT.read_text()
        function = "task_candidates() {\n" + source.split("task_candidates() {", 1)[1].split("\n}", 1)[0] + "\n}\n"
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "sessions").write_text("own\n")
            rows = [f"2026-10-23T00:{index // 60:02d}:{index % 60:02d}Z\ttask-{index:03d}\town"
                    for index in range(501)]
            rows.append("2026-10-23T00:08:20Z\ttask-500\tstranger")
            rows.append("2026-10-23T00:00:59Z\ttask-500\town")
            (root / "tasks").write_text("\n".join(rows) + "\n")
            result = subprocess.run(
                ["bash", "-s", "--", str(root / "sessions"), str(root / "tasks"), "500"],
                input=function + '\ntask_candidates "$1" "$2" "$3"\n',
                text=True, capture_output=True, timeout=10,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            listed = result.stdout.splitlines()
            self.assertEqual(len(listed), 500)
            self.assertEqual(len(set(listed)), 500)
            self.assertEqual(listed[0].split("\t")[0], "task-500")
            self.assertNotIn("stranger", listed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
