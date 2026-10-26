#!/usr/bin/env python3
"""Execute the real L1 configuration block offline, with stubbed HTTP responses."""
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("smoke-l1-agent-mode.sh")
TOOLS = [
    "update_session_notes", "retexture_model", "process_model", "list_session_models",
    "plan_model_processing", "get_processing_plan", "find_history_images",
]
STUBS = r'''
set -u
T="$1"; ADMIN_STATUS="$2"; EFFECTIVE_STATUS="$3"
SCHEMA_STATUS="$4"; OFFERINGS_STATUS="$5"
BASE="stub://offline"; JAR="$T/cookies"; PASS=0; FAIL=0
ok() { printf 'PASS: %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf 'FAIL: %s\n' "$1"; FAIL=$((FAIL+1)); }
req() {
  local out="$1" url="$2"
  printf '%s\n' "$url" >> "$T/requests"
  case "$url" in
    "$BASE/api/from-public-schema?capability=format-conversion")
      cp "$T/offerings-fixture.json" "$out"; printf '%s' "$OFFERINGS_STATUS" ;;
    "$BASE/api/admin/agent-configuration")
      cp "$T/admin-fixture.json" "$out"; printf '%s' "$ADMIN_STATUS" ;;
    "$BASE/api/v1/agent-configuration/effective?creative_domain=model-3d")
      cp "$T/effective-fixture.json" "$out"; printf '%s' "$EFFECTIVE_STATUS" ;;
    *) printf 'Unexpected request: %s\n' "$url" >&2; exit 99 ;;
  esac
}
jget() {
  python3 - "$1" "$2" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
print(eval(sys.argv[2]))
PYEOF
}
'''


def admin_response(enabled=TOOLS, known=TOOLS):
    return {
        "revision": "2",
        "configuration": {"enabled_tool_names": enabled},
        "tools": [{"name": tool} for tool in known],
    }


def effective_response(tools=TOOLS):
    return {"tools": [{"name": tool} for tool in tools]}


def public_schema(tools=None):
    return {"schema": {"runtime": {"http": {"routes": {"offerings": "/api/from-public-schema"}}},
                       "chat": {"tools": tools if tools is not None else [{"name": "convert_model_format", "capability": "format-conversion"}]}}}


def conversion_offerings(available=False):
    return {"offerings": [{"id": "declared/route", "capability": "format-conversion", "available": available}]}


class ApprovalPolicyTests(unittest.TestCase):
    def test_manual_policy_includes_splat_and_rejects_missing_or_extra_capabilities(self):
        # Independent product policy: docs/product.md approval policy and #190.
        # Do not derive this oracle from the deployed Schema being checked.
        required = ["text-to-3d", "image-to-3d", "image-to-splat", "retexture"]
        source = SCRIPT.read_text()
        block = source.split("# Schema 里的批准策略", 1)[1].split("\n", 1)[1].split("# 4. 新会话", 1)[0]
        stub = r'''
set -u
T="$1"; BASE=stub://offline; PASS=0; FAIL=0
ok() { PASS=$((PASS+1)); }
bad() { FAIL=$((FAIL+1)); }
req() { printf 200; }
jget() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$1" "$2"; }
'''
        cases = [("complete", "manual", required, True), ("automatic default", "auto", required, False)]
        cases += [("missing " + name, "manual", [x for x in required if x != name], False) for name in required]
        cases += [("unexpected 2D approval", "manual", required + ["text-to-image"], False)]
        for name, mode, capabilities, valid in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                policy = {"default_mode": mode, "manual_approval_capabilities": capabilities}
                (pathlib.Path(directory) / "schema.json").write_text(json.dumps({"schema": {"chat": {"approval_policy": policy}}}))
                result = subprocess.run(["bash", "-s", "--", directory], input=stub + block + '\n[ "$PASS" = 1 ] && [ "$FAIL" = 0 ]\n', text=True, capture_output=True, timeout=10)
                self.assertEqual(result.stderr, "")
                self.assertEqual(result.returncode, 0 if valid else 1)


class AgentConfigurationTests(unittest.TestCase):
    scenario_count = 0
    def check_response(self, admin, effective, errors=(), admin_status=200, effective_status=200,
                       schema=None, offerings=None, schema_status=200, offerings_status=200,
                       preflight_error=None):
        AgentConfigurationTests.scenario_count += 1
        source = SCRIPT.read_text()
        # Extract, rather than reimplement, the production checking algorithm.
        block = source.split("# 10. 已发布的 Agent 配置含新工具", 1)[1].split("\n", 1)[1].split(
            'echo "=== SMOKE-L1-AGENT-MODE', 1
        )[0]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for name, value in (("admin", admin), ("effective", effective),
                                ("schema", schema if schema is not None else public_schema()),
                                ("offerings", offerings if offerings is not None else conversion_offerings())):
                (root / f"{name}-fixture.json").write_text(
                    value if isinstance(value, str) else json.dumps(value)
                )
            (root / "schema.json").write_bytes((root / "schema-fixture.json").read_bytes())
            result = subprocess.run(
                ["bash", "-s", "--", directory, str(admin_status), str(effective_status),
                 str(schema_status), str(offerings_status)],
                input=STUBS + block + '\nprintf "COUNTS:%s:%s\\n" "$PASS" "$FAIL"\n[ "$FAIL" = 0 ]\n',
                text=True, capture_output=True, timeout=10,
            )
            self.assertEqual(result.stderr, "")
            self.assertEqual(result.returncode, int(bool(errors)), result.stdout)
            self.assertIn("COUNTS:0:1" if errors else "COUNTS:1:0", result.stdout)
            self.assertNotIn("private-response-sentinel", result.stdout)
            for error in errors:
                self.assertIn(error, result.stdout)
            routes = []
            if preflight_error != "schema":
                routes.append("stub://offline/api/from-public-schema?capability=format-conversion")
            if preflight_error is None:
                routes.append("stub://offline/api/admin/agent-configuration")
                if admin_status != 200:
                    routes.append("stub://offline/api/v1/agent-configuration/effective?creative_domain=model-3d")
            actual = (root / "requests").read_text().splitlines() if (root / "requests").exists() else []
            self.assertEqual(actual, routes)

    def test_required_tools_and_fallback(self):
        cases = [
            ("old admin", 200, 200, TOOLS[:4], TOOLS, TOOLS, TOOLS[4:]),
            ("old nonadmin", 403, 200, TOOLS, TOOLS[:4], TOOLS, TOOLS[4:]),
            ("complete admin", 200, 200, TOOLS, TOOLS, TOOLS, []),
            ("complete nonadmin", 403, 200, TOOLS, TOOLS, TOOLS, []),
            ("known catalog mismatch", 200, 200, TOOLS, TOOLS, TOOLS[:4], TOOLS[4:]),
            ("empty known catalog", 200, 200, TOOLS, TOOLS, [], TOOLS),
            ("unreadable fallback", 403, 503, TOOLS, TOOLS, TOOLS,
             ["Agent configuration unreadable"]),
        ]
        for missing in TOOLS[4:]:
            incomplete = [tool for tool in TOOLS if tool != missing]
            cases.extend([
                (f"admin missing {missing}", 200, 200, incomplete, TOOLS, TOOLS, [missing]),
                (f"nonadmin missing {missing}", 403, 200, TOOLS, incomplete, TOOLS, [missing]),
            ])
        for name, admin_status, effective_status, enabled, effective, known, errors in cases:
            with self.subTest(name=name):
                self.check_response(admin_response(enabled, known), effective_response(effective),
                                    errors, admin_status, effective_status)

    def test_invalid_admin_response(self):
        valid = admin_response()
        cases = [
            '{"private-response-sentinel":', [], {},
            {key: value for key, value in valid.items() if key != "configuration"},
            {key: value for key, value in valid.items() if key != "revision"},
            {key: value for key, value in valid.items() if key != "tools"},
            *({**valid, "revision": revision} for revision in (None, 2, True, "", " ")),
            *({**valid, "configuration": {"enabled_tool_names": enabled}}
              for enabled in (None, " ".join(TOOLS), {name: True for name in TOOLS})),
            *({**valid, "tools": tools} for tools in ({}, [{"name": 1}], [{}])),
        ]
        for index, response in enumerate(cases):
            with self.subTest(case=index):
                self.check_response(response, effective_response(), ["invalid configuration response"])

    def test_invalid_effective_response(self):
        cases = [
            '{"private-response-sentinel":', [], {},
            *({"tools": tools} for tools in (None, " ".join(TOOLS), {}, [{"name": 1}], [{}])),
        ]
        for index, response in enumerate(cases):
            with self.subTest(case=index):
                self.check_response(admin_response(), response,
                                    ["invalid configuration response"], admin_status=403)

    def test_conversion_availability_requires_mapped_tool_in_both_branches(self):
        complete = TOOLS + ["convert_model_format"]
        for name, status, enabled, effective, known, errors in [
            ("available admin missing", 200, TOOLS, complete, complete, ["convert_model_format"]),
            ("available admin complete", 200, complete, complete, complete, []),
            ("available effective missing", 403, complete, TOOLS, complete, ["convert_model_format"]),
            ("available effective complete", 403, complete, complete, complete, []),
            ("known catalog missing", 200, complete, complete, TOOLS, ["convert_model_format"]),
        ]:
            with self.subTest(case=name):
                self.check_response(admin_response(enabled, known), effective_response(effective), errors,
                                    admin_status=status, offerings=conversion_offerings(True))
        for status in (200, 403):
            with self.subTest(unavailable_status=status):
                self.check_response(admin_response(), effective_response(), admin_status=status,
                                    offerings=conversion_offerings(False))
        self.check_response(admin_response(), effective_response(), schema=public_schema([]))
        self.check_response(admin_response(), effective_response(), offerings={"offerings": []})

    def test_conversion_mapping_is_read_from_schema(self):
        renamed = "schema-declared-conversion-tool"
        complete = TOOLS + [renamed]
        self.check_response(admin_response(complete, complete), effective_response(complete),
                            schema=public_schema([{"name": renamed, "capability": "format-conversion"}]),
                            offerings=conversion_offerings(True))
        self.check_response(admin_response(), effective_response(), ["invalid offerings or tool mapping response"],
                            schema=public_schema([]), offerings=conversion_offerings(True), preflight_error="offerings")

    def test_invalid_conversion_discovery_fails_closed(self):
        cases = [
            '{"private-response-sentinel":', [], {}, {"offerings": None}, {"offerings": {}},
            {"offerings": ["private-response-sentinel"]},
            *({"offerings": [{"capability": "format-conversion", "available": value}]}
              for value in (None, "true", 1, {})),
            {"offerings": [{"capability": "format-conversion"}]},
            {"offerings": [{"capability": "text-to-3d", "available": True}]},
        ]
        for index, value in enumerate(cases):
            with self.subTest(case=index):
                self.check_response(admin_response(), effective_response(), ["invalid offerings or tool mapping response"],
                                    offerings=value, preflight_error="offerings")
        self.check_response(admin_response(), effective_response(), ["invalid offerings or tool mapping response"],
                            offerings_status=503, preflight_error="offerings")

    def test_invalid_public_schema_mapping_fails_closed(self):
        cases = ['{"private-response-sentinel":', {}, {"schema": {}},
                 public_schema([{"name": "bad name", "capability": "format-conversion"}]),
                 public_schema([{"name": "convert", "capability": None}]),
                 public_schema([{"name": "same", "capability": ""}, {"name": "same", "capability": "format-conversion"}])]
        for path in (None, "https://private-response-sentinel.invalid/api", "//other/api", "/api/options?unexpected=1"):
            value = public_schema(); value["schema"]["runtime"]["http"]["routes"]["offerings"] = path; cases.append(value)
        missing_tools = public_schema(); del missing_tools["schema"]["chat"]["tools"]; cases.append(missing_tools)
        invalid_tools = public_schema(); invalid_tools["schema"]["chat"]["tools"] = {}; cases.append(invalid_tools)
        for index, value in enumerate(cases):
            with self.subTest(case=index):
                self.check_response(admin_response(), effective_response(), ["invalid public Schema response"],
                                    schema=value, preflight_error="schema")
        self.check_response(admin_response(), effective_response(), ["invalid public Schema response"],
                            schema_status=503, preflight_error="schema")


if __name__ == "__main__":
    result = unittest.main(verbosity=2, exit=False).result
    print(f"Executed {AgentConfigurationTests.scenario_count} response scenarios")
    sys.exit(not result.wasSuccessful())
