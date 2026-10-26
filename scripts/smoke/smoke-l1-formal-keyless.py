#!/usr/bin/env python3
"""Read-only, keyless formal-environment smoke check.

This intentionally does not create sessions, tasks, approvals, or LLM turns.
"""

import argparse
import http.cookiejar
import importlib.util
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request


# The formal entry is pinned to the legacy-formal base_url of the untracked
# environments file; the repository names no host (#249).
LEGACY_ENVIRONMENT = "legacy-formal"
LOCATOR = Path(__file__).resolve().parents[1] / "local_environments.py"
FORBIDDEN_SCHEMA_KEYS = {"configuration", "cookie", "cookie_name", "cookie_secret", "session_secret"}


class Client:
    def __init__(self, base, opener=None):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = opener or urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def request(self, method, path, payload=None, cookie=None):
        data = None if payload is None else json.dumps(payload).encode()
        headers = {"Accept": "application/json", "Origin": self.base}
        if cookie is not None:
            headers["Cookie"] = cookie
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=15) as response:
                raw = response.read()
                status = response.status
        except urllib.error.HTTPError as exc:
            status, raw = exc.code, exc.read()
            exc.close()
        try:
            body = json.loads(raw) if raw else None
        except (ValueError, UnicodeDecodeError):
            body = None
        return status, body


def _require(condition, message):
    if not condition:
        raise AssertionError(message)


def _walk_forbidden(value, parent=""):
    if isinstance(value, dict):
        for key, child in value.items():
            if key.lower() in FORBIDDEN_SCHEMA_KEYS:
                raise AssertionError(f"private schema field: {parent}{key}")
            _walk_forbidden(child, parent + key + ".")
    elif isinstance(value, list):
        for child in value:
            _walk_forbidden(child, parent)


def _items(body, *keys):
    if isinstance(body, list):
        return body
    if isinstance(body, dict):
        for key in keys:
            if isinstance(body.get(key), list):
                return body[key]
    return None


def run_checks(base, test_base, username, password, expect_empty=False, emit=print):
    _require(test_base and test_base.rstrip("/") != base.rstrip("/"),
             "TEST_BASE must be explicit and different from BASE")
    client = Client(base)

    for path in ("/healthz", "/readyz"):
        status, body = client.request("GET", path)
        _require(status == 200, f"{path} failed ({status})")
        _require(isinstance(body, dict), f"{path} returned invalid JSON")

    status, projection = client.request("GET", "/api/v1/schema")
    _require(status == 200 and isinstance(projection, dict), f"schema failed ({status})")
    schema = projection.get("schema", projection)
    _require(isinstance(schema, dict), "schema projection missing schema object")
    _walk_forbidden(schema)

    status, body = client.request("POST", "/api/auth/login", {"username": username, "password": password})
    _require(status == 200 and isinstance(body, dict), f"login failed ({status})")
    _require(body.get("success", True) is True, "login failed")

    status, session = client.request("GET", "/api/auth/session")
    _require(status == 200 and isinstance(session, dict), f"authenticated session check failed ({status})")
    _require(session.get("user") is not None, "login session has no user")
    _require(session.get("isAdmin") is True, "logged-in account is not admin")
    formal_cookie = next((cookie for cookie in client.jar if cookie.name == "art_ai_formal_session"), None)
    _require(formal_cookie is not None and formal_cookie.value,
             "formal login did not issue its isolated session cookie")
    test_client = Client(test_base, urllib.request.build_opener())
    status, test_session = test_client.request(
        "GET", "/api/auth/session", cookie=f"{formal_cookie.name}={formal_cookie.value}")
    _require(status == 200 and isinstance(test_session, dict)
             and test_session.get("user") is None and test_session.get("isAdmin") is False,
             "test stack accepted formal cookie")
    status, _ = test_client.request(
        "GET", "/api/v1/tasks?limit=1", cookie=f"{formal_cookie.name}={formal_cookie.value}")
    _require(status == 401, f"test stack accepted formal cookie for task access ({status})")
    status, wrong_name_session = test_client.request(
        "GET", "/api/auth/session", cookie=f"meshyai_session={formal_cookie.value}")
    _require(status == 200 and isinstance(wrong_name_session, dict)
             and wrong_name_session.get("user") is None and wrong_name_session.get("isAdmin") is False,
             "test stack accepted formal cookie under its default cookie name")
    status, _ = test_client.request(
        "GET", "/api/v1/tasks?limit=1", cookie=f"meshyai_session={formal_cookie.value}")
    _require(status == 401, f"test stack accepted renamed formal cookie for task access ({status})")

    status, offerings_body = client.request("GET", "/api/v1/offerings")
    _require(status == 200, f"offerings failed ({status})")
    offerings = _items(offerings_body, "offerings")
    _require(offerings is not None, "offerings response has no offering list")
    _require(all(isinstance(item, dict) and isinstance(item.get("provider_id"), str)
                 and item.get("provider_id") for item in offerings),
             "Offering missing provider_id")
    remote = [item for item in offerings if item["provider_id"] != "local"]
    local = [item for item in offerings if item.get("provider_id") == "local"]
    _require(remote and all(item.get("available") is False for item in remote), "remote Offering advertised as available")
    _require(local and any(item.get("available") is True for item in local), "no local Offering available")

    status, sessions_body = client.request("GET", "/api/chat/sessions")
    _require(status == 200, f"sessions list failed ({status})")
    status, tasks_body = client.request("GET", "/api/v1/tasks")
    _require(status == 200, f"tasks list failed ({status})")
    sessions = _items(sessions_body, "sessions", "data")
    tasks = _items(tasks_body, "tasks", "data")
    _require(sessions is not None and tasks is not None, "session/task list response malformed")
    if expect_empty:
        _require(not sessions and not tasks, "expected empty sessions and tasks")

    status, logout = client.request("DELETE", "/api/auth/session")
    _require(status in (200, 204), f"logout failed ({status})")
    status, anonymous = client.request("GET", "/api/auth/session")
    _require(status == 200 and isinstance(anonymous, dict) and anonymous.get("user") is None,
             f"session remained authenticated after logout ({status})")

    # Exercise rejection of a stale cookie without reusing the authenticated cookie jar.
    stale_client = Client(base, urllib.request.build_opener())
    old_cookie = f"{formal_cookie.name}={formal_cookie.value}"
    status, stale_session = stale_client.request("GET", "/api/auth/session", cookie=old_cookie)
    _require(status == 200 and isinstance(stale_session, dict) and stale_session.get("user") is None,
             f"revoked cookie still authenticated ({status})")
    status, _ = stale_client.request("GET", "/api/v1/tasks", cookie=old_cookie)
    _require(status == 401, f"revoked cookie task access expected 401, got {status}")
    emit("PASS: formal keyless read-only smoke; no task or LLM submission")
    return 0


def formal_base():
    spec = importlib.util.spec_from_file_location("local_environments", LOCATOR)
    locator = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(locator)
    path = locator.environments_file()
    try:
        base = json.loads(path.read_text())["environments"][LEGACY_ENVIRONMENT]["base_url"]
    except (OSError, ValueError, KeyError, TypeError):
        return None
    return base.rstrip("/") if isinstance(base, str) and base else None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expect-empty", action="store_true", help="require no sessions or tasks")
    args = parser.parse_args(argv)
    if "BASE" not in os.environ:
        print("ERROR: BASE must be explicitly set", file=sys.stderr)
        return 2
    base = os.environ["BASE"].rstrip("/")
    test_base = os.environ.get("TEST_BASE", "").rstrip("/")
    username, password = os.environ.get("ADMIN_USERNAME", ""), os.environ.get("ADMIN_PASSWORD", "")
    if not username or not password:
        print("ERROR: ADMIN_USERNAME and ADMIN_PASSWORD are required", file=sys.stderr)
        return 2
    if not test_base or test_base == base:
        print("ERROR: TEST_BASE must be explicit and different from BASE", file=sys.stderr)
        return 2
    expected = formal_base()
    if expected is None:
        print(f"ERROR: {LEGACY_ENVIRONMENT} base_url is not set in the environments file", file=sys.stderr)
        return 2
    if base != expected:
        print(f"ERROR: BASE must be the {LEGACY_ENVIRONMENT} base_url {expected}", file=sys.stderr)
        return 2
    try:
        return run_checks(base, test_base, username, password, expect_empty=args.expect_empty)
    except (AssertionError, OSError, urllib.error.URLError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
