#!/usr/bin/env python3
"""Seed one SUCCEEDED 3D task on a fake-shaped dev-test for L1 Agent (#219).

dev-test starts from empty data on every release, and the L1 Agent read-only
checks (formats, archive) need an own SUCCEEDED 3D task. In the fake upstream
shape this costs nothing: a new manual-mode session asks for a 3D model with
the fake chat model's tool marker, the approval card is approved, and the fake
Tripo/Meshy/Hyper3D answers with its fixed model. The script refuses (exit 2)
unless the target runs the fake upstream (upstream_shape.py), so it can never
start a real, paid generation.

  BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... python3 scripts/smoke/seed-fake-3d-task.py

Prints the SUCCEEDED task id on the last line; exit 1 on any failure. Never
prints credentials, cookies or signed URLs.
"""
import http.cookiejar
import json
import os
import sys
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import upstream_shape  # noqa: E402

MARKER = '[[fake:tool:create_model_3d {"prompt":"a simple wooden stool"}]]'


class Api:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def json(self, method, path, body=None):
        headers = {"Origin": self.base}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(self.base + path, data=data, method=method, headers=headers)
        try:
            response = self.opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        except OSError:
            return 0, {}
        raw = response.read()
        try:
            return getattr(response, "status", None) or response.code, json.loads(raw or b"{}")
        except ValueError:
            return getattr(response, "status", None) or response.code, {}


def pick_model(offerings):
    """The L1 choice: available, recommended, no extra-credit ceiling above the estimate."""
    available = [o for o in offerings if o.get("available")]
    safe = [o for o in available if o.get("recommended")
            and (o.get("maximum_estimated_credits") or 0) <= (o.get("estimated_credits") or 0)]
    return ((safe or available or [{}])[0]).get("model_id", "")


def wait(check, seconds, step=2):
    end = time.time() + seconds
    while time.time() < end:
        value = check()
        if value:
            return value
        time.sleep(step)
    return None


def seed(api, environ):
    status, _ = api.json("POST", "/api/auth/login",
                         {"username": environ["ADMIN_USERNAME"], "password": environ["ADMIN_PASSWORD"]})
    if status != 200:
        return None, f"login {status}"
    status, schema = api.json("GET", "/api/v1/schema")
    schema = schema.get("schema", schema) if isinstance(schema, dict) else {}
    member = (((schema.get("chat") or {}).get("api") or {}).get("fields") or {}).get("decision")
    if not member:
        return None, "the public Schema declares no approval decision member"
    status, offerings = api.json("GET", "/api/v1/offerings?capability=text-to-3d")
    model = pick_model(offerings.get("offerings", []) if isinstance(offerings, dict) else [])
    if not model:
        return None, "no available text-to-3d model"
    status, session = api.json("POST", "/api/chat/sessions", {"title": ""})
    session_id = session.get("id") if isinstance(session, dict) else None
    if status not in (200, 201) or not session_id:
        return None, f"create session {status}"
    status, _ = api.json("PUT", f"/api/v1/chat/sessions/{session_id}/approval", {"approvalMode": "manual"})
    if status != 200:
        return None, f"set manual approval {status}"
    status, _ = api.json("POST", f"/api/chat/sessions/{session_id}/messages", {
        "content": "做一个简单的木凳 3D 模型 " + MARKER, "creative_domain": "model-3d", "ai_model": model})
    if status not in (200, 201, 202):
        return None, f"send message {status}"

    def pending():
        _, body = api.json("GET", f"/api/v1/chat/sessions/{session_id}/pending-approvals")
        items = [item for item in (body.get("items") or []) if item.get("status") == "pending"] if isinstance(body, dict) else []
        return items[0] if items else None
    card = wait(pending, 90)
    if not card:
        return None, "the fake tool call produced no approval card"
    status, decided = api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", {member: "approve"})
    task_id = decided.get("task_id") if isinstance(decided, dict) else None
    if status != 200 or not task_id:
        return None, f"approve {status}"

    def finished():
        _, task = api.json("GET", f"/api/v1/tasks/{task_id}")
        return task if isinstance(task, dict) and task.get("state") in ("SUCCEEDED", "FAILED", "CANCELED") else None
    task = wait(finished, 300)
    if not task or task.get("state") != "SUCCEEDED":
        return None, f"task {task_id} ended {task.get('state') if task else 'not in time'}"
    return task_id, f"{model} task SUCCEEDED in session {session_id}"


def main(environ=None):
    environ = os.environ if environ is None else environ
    if upstream_shape.target_shape(environ) != "fake":
        print("refusing to run: the target does not run the fake upstream; seeding would start a real, paid 3D "
              "generation", file=sys.stderr)
        return 2
    missing = [name for name in ("BASE", "ADMIN_USERNAME", "ADMIN_PASSWORD") if not environ.get(name)]
    if missing:
        print("missing environment: " + ", ".join(missing), file=sys.stderr)
        return 2
    task_id, detail = seed(Api(environ["BASE"]), environ)
    print(detail)
    if not task_id:
        return 1
    print(task_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())
