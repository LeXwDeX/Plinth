#!/usr/bin/env python3
"""Offline contract tests for the formal keyless smoke runner."""

import importlib.util
import http.server
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("smoke-l1-formal-keyless.py")
sys.path.insert(0, str(SCRIPT.parent))


class FixtureState:
    def __init__(self):
        self.requests = []
        self.origins = []
        self.remote_available = False
        self.missing_provider_id = False
        self.login_status = 200
        self.private_schema = False
        self.test_stack = False
        self.accept_formal_cookie = False
        self.accept_default_cookie = False
        self.accept_stale_cookie = False
        self.session_active = True


class Handler(http.server.BaseHTTPRequestHandler):
    state = None

    def log_message(self, *_args):
        pass

    def do_GET(self):
        cookie = self.headers.get("Cookie", "")
        self.state.requests.append(("GET", self.path, cookie))
        self.state.origins.append(self.headers.get("Origin", ""))
        path = self.path.split("?", 1)[0]
        valid_session = ((self.state.session_active and ((not self.state.test_stack and "art_ai_formal_session=secret-cookie" in cookie)
                         or (self.state.test_stack and self.state.accept_formal_cookie
                             and "art_ai_formal_session=secret-cookie" in cookie)
                         or (self.state.test_stack and self.state.accept_default_cookie
                             and "meshyai_session=secret-cookie" in cookie)))
                         or (self.state.accept_stale_cookie and "art_ai_formal_session=secret-cookie" in cookie))
        if path in ("/healthz", "/readyz"):
            self.reply(200, {"status": "ok", "service": "fixture"})
        elif path == "/api/v1/schema":
            schema = {
                "revision": "fixture-revision",
                "runtime": {"http": {"routes": {"offerings": "/api/v1/offerings"}}},
                "authentication": {"api": {}, "login": {}},
            }
            if self.state.private_schema:
                schema["runtime"]["configuration"] = {"secret": "must-not-appear"}
            self.reply(200, {"schema": schema})
        elif path == "/api/v1/offerings":
            remote = [
                {"id": "meshy/model", "provider_id": "meshy", "available": self.state.remote_available},
                {"id": "openai/image", "provider_id": "openai", "available": False},
            ]
            if self.state.missing_provider_id:
                remote[0].pop("provider_id")
            self.reply(200, {"offerings": remote + [
                {"id": "local/conversion", "provider_id": "local", "available": True},
            ]})
        elif path == "/api/chat/sessions":
            self.reply(200, [])
        elif path == "/api/v1/tasks":
            self.reply(200 if valid_session else 401,
                       {"tasks": []} if valid_session else {"error": "unauthorized"})
        elif path == "/api/auth/session":
            if valid_session:
                self.reply(200, {"user": {"id": "admin-1", "name": "Admin", "email": "admin@example.test"}, "isAdmin": True})
            else:
                self.reply(200, {"user": None, "isAdmin": False})
        else:
            self.reply(404, {"error": "not found"})

    def do_POST(self):
        self.state.requests.append(("POST", self.path, self.headers.get("Cookie", "")))
        self.state.origins.append(self.headers.get("Origin", ""))
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.path == "/api/auth/login":
            if self.state.login_status != 200:
                self.reply(self.state.login_status, {"error": "invalid credentials"})
                return
            self.reply(200, {"success": True, "user": {"id": "admin-1", "name": "Admin", "email": "admin@example.test"}}, [
                ("Set-Cookie", "art_ai_formal_session=secret-cookie; Path=/; HttpOnly; SameSite=Lax")
            ])
            self.state.session_active = True
        else:
            self.reply(404, {"error": "not found"})

    def do_DELETE(self):
        self.state.requests.append(("DELETE", self.path, self.headers.get("Cookie", "")))
        self.state.origins.append(self.headers.get("Origin", ""))
        if self.path == "/api/auth/session":
            self.state.session_active = False
            self.reply(200, {"success": True}, [
                ("Set-Cookie", "art_ai_formal_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax")
            ])
        else:
            self.reply(404, {"error": "not found"})

    def reply(self, status, body, headers=()):
        raw = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Connection", "close")
        for name, value in headers:
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(raw)


class FormalKeylessSmokeTests(unittest.TestCase):
    def setUp(self):
        self.state = FixtureState()
        self.test_state = FixtureState()
        self.test_state.test_stack = True
        handler = type("FixtureHandler", (Handler,), {"state": self.state})
        test_handler = type("TestFixtureHandler", (Handler,), {"state": self.test_state})
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.test_server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), test_handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.test_thread = threading.Thread(target=self.test_server.serve_forever, daemon=True)
        self.thread.start(); self.test_thread.start()
        self.base = f"http://127.0.0.1:{self.server.server_port}"
        self.test_base = f"http://127.0.0.1:{self.test_server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.test_server.shutdown()
        self.thread.join()
        self.test_thread.join()
        self.server.server_close()
        self.test_server.server_close()

    def run_smoke(self, *, username="formal-admin", password="passphrase", expect_empty=True):
        spec = importlib.util.spec_from_file_location("smoke_l1_formal_keyless", SCRIPT)
        smoke = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(smoke)
        return smoke.run_checks(
            self.base,
            self.test_base,
            username,
            password,
            expect_empty=expect_empty,
            emit=lambda _line: None,
        )

    def test_cli_rejects_missing_credentials_before_any_request(self):
        environment = os.environ | {"BASE": "http://legacy.example.invalid:13334", "TEST_BASE": "http://dev.example.invalid:13333"}
        environment.pop("ADMIN_USERNAME", None)
        environment.pop("ADMIN_PASSWORD", None)
        result = subprocess.run([sys.executable, str(SCRIPT)], env=environment, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ADMIN_USERNAME and ADMIN_PASSWORD are required", result.stderr)

    def test_cli_requires_distinct_explicit_test_base(self):
        environment = os.environ | {"BASE": "http://legacy.example.invalid:13334",
                                    "ADMIN_USERNAME": "formal-admin", "ADMIN_PASSWORD": "secret"}
        environment.pop("TEST_BASE", None)
        result = subprocess.run([sys.executable, str(SCRIPT)], env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("TEST_BASE must be explicit", result.stderr)
        environment["TEST_BASE"] = environment["BASE"]
        result = subprocess.run([sys.executable, str(SCRIPT)], env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("TEST_BASE must be explicit", result.stderr)

    def test_cli_pins_base_to_the_legacy_formal_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            environments = Path(directory) / "environments.json"
            environment = os.environ | {"BASE": "http://other.example.invalid:13334",
                                        "TEST_BASE": "http://dev.example.invalid:13333",
                                        "ADMIN_USERNAME": "formal-admin", "ADMIN_PASSWORD": "secret",
                                        "ART_AI_ENVIRONMENTS_FILE": str(environments)}
            result = subprocess.run([sys.executable, str(SCRIPT)], env=environment, capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn("legacy-formal base_url is not set", result.stderr)
            environments.write_text(json.dumps({"version": 1, "environments": {"legacy-formal": {
                "base_url": "http://legacy.example.invalid:13334"}}}))
            result = subprocess.run([sys.executable, str(SCRIPT)], env=environment, capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn("BASE must be the legacy-formal base_url http://legacy.example.invalid:13334", result.stderr)

    def test_remote_offering_must_be_unavailable(self):
        self.state.remote_available = True
        with self.assertRaisesRegex(AssertionError, "remote Offering advertised as available"):
            self.run_smoke()

    def test_every_non_local_provider_needs_identity_and_must_be_unavailable(self):
        self.state.missing_provider_id = True
        with self.assertRaisesRegex(AssertionError, "provider_id"):
            self.run_smoke()

    def test_login_failure_stops_before_protected_requests(self):
        self.state.login_status = 401
        with self.assertRaisesRegex(AssertionError, "login failed"):
            self.run_smoke()
        self.assertEqual([r[1] for r in self.state.requests], ["/healthz", "/readyz", "/api/v1/schema", "/api/auth/login"])
        self.assertEqual(self.test_state.requests, [])

    def test_normal_empty_database_closure_and_method_allowlist(self):
        self.assertEqual(self.run_smoke(), 0)
        methods = {(method, path.split("?", 1)[0]) for method, path, _cookie in self.state.requests}
        self.assertEqual(methods, {
            ("GET", "/healthz"), ("GET", "/readyz"), ("GET", "/api/v1/schema"),
            ("POST", "/api/auth/login"), ("GET", "/api/auth/session"),
            ("GET", "/api/v1/offerings"), ("GET", "/api/chat/sessions"),
            ("GET", "/api/v1/tasks"), ("DELETE", "/api/auth/session"),
        })
        self.assertFalse(any("/messages" in path or "/prepare" in path or "/grants" in path for _, path, _ in self.state.requests))
        test_requests = [r for r in self.state.requests if r[0] == "GET" and r[1] == "/api/auth/session"]
        self.assertIn("art_ai_formal_session=secret-cookie", [r[2] for r in test_requests])
        self.assertEqual([r[2] for r in test_requests].count("art_ai_formal_session=secret-cookie"), 2)
        self.assertTrue(all(method == "GET" for method, _path, _cookie in self.test_state.requests))
        self.assertTrue(all(origin == self.test_base for origin in self.test_state.origins))
        self.assertTrue(all(self.base == origin for (method, _path, _cookie), origin
                            in zip(self.state.requests, self.state.origins)
                            if method in ("POST", "DELETE")))
        self.assertEqual([path.split("?", 1)[0] for _method, path, _cookie in self.test_state.requests],
                         ["/api/auth/session", "/api/v1/tasks", "/api/auth/session", "/api/v1/tasks"])

    def test_test_fixture_that_accepts_formal_cookie_fails(self):
        self.test_state.accept_formal_cookie = True
        with self.assertRaisesRegex(AssertionError, "test stack accepted formal cookie"):
            self.run_smoke()

    def test_test_fixture_that_accepts_renamed_formal_cookie_fails(self):
        self.test_state.accept_default_cookie = True
        with self.assertRaisesRegex(AssertionError, "test stack accepted formal cookie under its default cookie name"):
            self.run_smoke()

    def test_formal_fixture_that_accepts_old_cookie_after_logout_fails(self):
        self.state.accept_stale_cookie = True
        with self.assertRaisesRegex(AssertionError, "revoked cookie still authenticated"):
            self.run_smoke()

    def test_missing_or_same_test_base_is_rejected_before_requests(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("smoke_l1_formal_keyless", SCRIPT)
        smoke = importlib.util.module_from_spec(spec); spec.loader.exec_module(smoke)
        with self.assertRaisesRegex(AssertionError, "TEST_BASE must be explicit"):
            smoke.run_checks(self.base, "", "formal-admin", "passphrase", emit=lambda _line: None)
        with self.assertRaisesRegex(AssertionError, "TEST_BASE must be explicit"):
            smoke.run_checks(self.base, self.base, "formal-admin", "passphrase", emit=lambda _line: None)
        self.assertEqual(self.state.requests, [])
        self.assertEqual(self.test_state.requests, [])

    def test_public_schema_cannot_contain_runtime_configuration(self):
        self.state.private_schema = True
        with self.assertRaisesRegex(AssertionError, "private schema field"):
            self.run_smoke()


if __name__ == "__main__":
    unittest.main()
