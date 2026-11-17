#!/usr/bin/env python3
"""The fake stack's MCP client (#332) must speak the stateless 2026-07-28 wire.

Offline: it checks the envelope and headers, the asset byte verification and
the required-slot reading. No stack, no network, no listener.
"""
import importlib.util
import os
import sys
import unittest

sys.dont_write_bytecode = True

SMOKE = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "smoke")
spec = importlib.util.spec_from_file_location("fakestack_mcp", os.path.join(SMOKE, "smoke-fake-stack.py"))
fs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fs)
mcp = fs.mcp_client


def blob(data, status=200, headers=None):
    return {"status": status, "bytes": data,
            "headers": headers if headers is not None else {
                "X-Content-Type-Options": "nosniff",
                "Content-Security-Policy": "sandbox; default-src 'none'",
            }}


class Envelope(unittest.TestCase):
    def test_tools_call_carries_the_protocol_and_the_tool(self):
        message = mcp.envelope("tools/call", {"arguments": {"task_id": "t-1"}}, "get_task")
        self.assertEqual(message["jsonrpc"], "2.0")
        self.assertEqual(message["method"], "tools/call")
        self.assertEqual(message["params"]["name"], "get_task")
        self.assertEqual(message["params"]["arguments"], {"task_id": "t-1"})
        meta = message["params"]["_meta"]
        self.assertEqual(meta["io.modelcontextprotocol/protocolVersion"], "2026-07-28")
        self.assertTrue(meta["io.modelcontextprotocol/clientInfo"]["name"])
        self.assertEqual(meta["io.modelcontextprotocol/clientCapabilities"], {})

    def test_the_envelope_is_pure(self):
        params = {"arguments": {}}
        mcp.envelope("tools/list", params)
        self.assertEqual(params, {"arguments": {}})

    def test_headers_name_the_method_and_the_tool(self):
        headers = mcp.headers_for("tools/call", "create_model", "plk_example")
        self.assertEqual(headers["Content-Type"], "application/json")
        self.assertEqual(headers["MCP-Protocol-Version"], "2026-07-28")
        self.assertEqual(headers["Mcp-Method"], "tools/call")
        self.assertEqual(headers["Mcp-Name"], "create_model")
        self.assertEqual(headers["Authorization"], "Bearer plk_example")

    def test_a_keyless_list_has_no_authorization_and_no_tool_name(self):
        headers = mcp.headers_for("tools/list")
        self.assertNotIn("Authorization", headers)
        self.assertNotIn("Mcp-Name", headers)
        self.assertEqual(headers["Mcp-Method"], "tools/list")

    def test_headers_are_read_case_insensitively(self):
        self.assertEqual(mcp.header({"Www-Authenticate": 'Bearer realm="Plinth"'}, "WWW-Authenticate"), 'Bearer realm="Plinth"')


class AssetProblems(unittest.TestCase):
    ASSET = {"id": "a-1", "role": "model", "variant": "glb", "size": 3, "sha256": "computed-below"}

    def asset(self):
        import hashlib
        item = dict(self.ASSET)
        item["sha256"] = hashlib.sha256(b"abc").hexdigest()
        return item

    def test_a_matching_download_passes(self):
        self.assertEqual(mcp.asset_problems([self.asset()], {"a-1": blob(b"abc")}), [])

    def test_a_size_mismatch_fails(self):
        self.assertIn("declared 3", mcp.asset_problems([self.asset()], {"a-1": blob(b"ab")})[0])

    def test_a_hash_mismatch_fails(self):
        self.assertIn("SHA-256", mcp.asset_problems([self.asset()], {"a-1": blob(b"abd")})[0])

    def test_a_missing_download_fails(self):
        self.assertIn("no downloaded bytes", mcp.asset_problems([self.asset()], {})[0])

    def test_a_non_200_download_fails(self):
        self.assertIn("answered 404", mcp.asset_problems([self.asset()], {"a-1": blob(b"abc", status=404)})[0])

    def test_missing_delivery_headers_fail(self):
        problems = mcp.asset_problems([self.asset()], {"a-1": blob(b"abc", headers={})})
        self.assertIn("sandboxed delivery headers", problems[0])


class RequiredSlots(unittest.TestCase):
    def test_required_output_zero_minimum_and_background_are_read_like_delivery(self):
        offering = {"outputs": [
            {"role": "model", "variant": "glb", "minimum": 1},
            {"role": "model", "variant": "obj", "delivery": "required", "minimum": 1},
            {"role": "model", "variant": "mtl", "delivery": "required", "minimum": 0},
            {"role": "texture", "delivery": "required", "minimum": 0},
            {"role": "thumbnail", "variant": "primary", "delivery": "background", "minimum": 0},
        ]}
        self.assertEqual(mcp.required_slots(offering), [("model", "glb"), ("model", "obj")])

    def test_a_present_condition_is_honoured(self):
        offering = {"outputs": [
            {"role": "image", "variant": "left", "minimum": 1,
             "required_when": {"parameter": "left_prompt", "operator": "present"}},
        ]}
        self.assertEqual(mcp.required_slots(offering), [])
        self.assertEqual(mcp.required_slots(offering, {"left_prompt": "x"}), [("image", "left")])

    def test_an_unknown_condition_counts_as_due(self):
        offering = {"outputs": [{"role": "image", "variant": "back", "minimum": 1,
                                 "required_when": {"parameter": "back_prompt", "operator": "other"}}]}
        self.assertEqual(mcp.required_slots(offering), [("image", "back")])


if __name__ == "__main__":
    unittest.main()