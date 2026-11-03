import hashlib
import hmac
import json
import os
import importlib.util
import unittest
from pathlib import Path
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "runner_start", Path(__file__).parents[1] / "azure-runner" / "webhook" / "runner_start.py")
runner_start = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner_start)

SECRET = b"test-secret"
ENV = {
    "WEBHOOK_SECRET": SECRET.decode(),
    "RUNNER_REPOSITORY": "LeXwDeX/Plinth",
    "RUNNER_LABEL": "plinth-azure",
    "RUNNER_VM_ID": "/subscriptions/s/resourceGroups/g/providers/Microsoft.Compute/virtualMachines/v",
}


def job(action="queued", repo="LeXwDeX/Plinth", labels=("self-hosted", "plinth-azure")):
    return {"action": action, "repository": {"full_name": repo}, "workflow_job": {"labels": list(labels)}}


def signed(payload, event="workflow_job", secret=SECRET):
    body = json.dumps(payload).encode()
    sig = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
    return {"x-hub-signature-256": sig, "x-github-event": event}, body


@mock.patch.dict(os.environ, ENV)
class HandleTest(unittest.TestCase):
    def run_handle(self, headers, body):
        calls = []
        result = runner_start.handle(headers, body, start=lambda vm: calls.append(vm) or 202)
        return result, calls

    def test_queued_plinth_azure_job_starts_vm(self):
        (status, _), calls = self.run_handle(*signed(job()))
        self.assertEqual(status, 202)
        self.assertEqual(calls, [ENV["RUNNER_VM_ID"]])

    def test_rejects_bad_or_missing_signature(self):
        headers, body = signed(job(), secret=b"other")
        self.assertEqual(self.run_handle(headers, body), ((401, "invalid signature"), []))
        headers.pop("x-hub-signature-256")
        self.assertEqual(self.run_handle(headers, body)[1], [])

    def test_rejects_tampered_body(self):
        headers, _ = signed(job())
        _, body = signed(job(labels=("ubuntu-latest",)))
        self.assertEqual(self.run_handle(headers, body)[0][0], 401)

    def test_empty_secret_rejects_everything(self):
        with mock.patch.dict(os.environ, {"WEBHOOK_SECRET": ""}):
            headers, body = signed(job(), secret=b"")
            self.assertEqual(self.run_handle(headers, body)[0][0], 401)

    def test_ignores_other_jobs(self):
        for payload, event in [
            (job(action="in_progress"), "workflow_job"),
            (job(action="completed"), "workflow_job"),
            (job(labels=("ubuntu-latest",)), "workflow_job"),
            (job(repo="someone/fork"), "workflow_job"),
            (job(), "workflow_run"),
        ]:
            (status, message), calls = self.run_handle(*signed(payload, event))
            self.assertEqual((status, message, calls), (200, "ignored", []), (payload, event))

    def test_ping(self):
        self.assertEqual(self.run_handle(*signed({"zen": "x"}, "ping")), ((200, "pong"), []))


if __name__ == "__main__":
    unittest.main()
