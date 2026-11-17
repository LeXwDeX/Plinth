#!/usr/bin/env python3
"""Minimal client for the external MCP listener (#332), standard library only.

The listener speaks the stateless 2026-07-28 JSON-RPC protocol: every request
carries the protocol version and the method in headers, plus the client
metadata in ``params._meta``; there is no initialize and no session id. This
module builds that envelope and the headers, calls tools, downloads asset
bytes and checks a downloaded asset against its declared size and SHA-256.

Only the smoke script and its offline tests import it. Nothing here prints a
key, a cookie or a signed download address (a signed token is a credential
until it expires).
"""
import hashlib
import json
import urllib.error
import urllib.request

PROTOCOL_VERSION = "2026-07-28"
CLIENT_NAME = "plinth-fake-stack-smoke"
CLIENT_VERSION = "1"


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def envelope(method, params=None, name=""):
    """One JSON-RPC request of the stateless protocol. Pure: ``params`` is copied.

    ``name`` is the tool of a ``tools/call``; it also becomes the ``Mcp-Name``
    header (see ``headers_for``). The request id is fixed because the listener
    keeps no state between requests and never correlates them.
    """
    message = {"jsonrpc": "2.0", "id": 1, "method": method}
    copied = dict(params or {})
    if name:
        copied["name"] = name
    copied["_meta"] = {
        "io.modelcontextprotocol/protocolVersion": PROTOCOL_VERSION,
        "io.modelcontextprotocol/clientInfo": {"name": CLIENT_NAME, "version": CLIENT_VERSION},
        "io.modelcontextprotocol/clientCapabilities": {},
    }
    message["params"] = copied
    return message


def headers_for(method, name="", key=""):
    """The headers every request must carry; Authorization only with a key."""
    headers = {
        "Content-Type": "application/json",
        "MCP-Protocol-Version": PROTOCOL_VERSION,
        "Mcp-Method": method,
    }
    if name:
        headers["Mcp-Name"] = name
    if key:
        headers["Authorization"] = "Bearer " + key
    return headers


def header(headers, name):
    """One header's value, case-insensitively (urllib's names vary in case)."""
    for key, value in (headers or {}).items():
        if key.lower() == name.lower():
            return value
    return ""


def required_slots(offering, submitted=None):
    """The (role, variant) pairs the Offering declares as required outputs.

    Same reading as smoke-fake-stack.py's check_delivery: delivery defaults to
    required, a minimum of 0 means "when delivered", an output without a variant
    is met by any asset of its role, and only the "present" condition is
    evaluated (any other counts as due).
    """
    slots = []
    for output in offering.get("outputs") or []:
        if output.get("delivery", "required") != "required" or output.get("minimum", 1) == 0:
            continue
        condition = output.get("required_when")
        if condition and condition.get("operator") == "present" and not (submitted or {}).get(condition.get("parameter")):
            continue
        slots.append((output.get("role"), output.get("variant")))
    return slots


def asset_problems(assets, blobs):
    """Check downloaded bytes against the asset declarations, without URLs.

    ``assets`` is the ``get_task_assets`` list; ``blobs`` maps an asset id to
    ``{"status": int, "bytes": bytes, "headers": dict}``. An asset passes when
    the download answered 200, the byte count and SHA-256 match the declaration
    and the response carries the sandboxed delivery headers. Problems name the
    role/variant only: a download URL may be a signed credential.
    """
    problems = []
    for asset in assets:
        label = "%s/%s" % (asset.get("role") or "?", asset.get("variant") or asset.get("format") or "?")
        blob = blobs.get(str(asset.get("id") or ""))
        if blob is None:
            problems.append(f"{label}: no downloaded bytes")
            continue
        if blob.get("status") != 200:
            problems.append(f"{label}: download answered {blob.get('status')}")
            continue
        data = blob.get("bytes") or b""
        if len(data) != asset.get("size"):
            problems.append(f"{label}: {len(data)} bytes, declared {asset.get('size')}")
            continue
        if hashlib.sha256(data).hexdigest() != asset.get("sha256"):
            problems.append(f"{label}: SHA-256 differs from the declaration")
        headers = blob.get("headers") or {}
        if header(headers, "X-Content-Type-Options") != "nosniff" or "sandbox" not in header(headers, "Content-Security-Policy"):
            problems.append(f"{label}: lacks the sandboxed delivery headers")
    return problems


class McpClient:
    """One listener origin and one access key (empty for the keyless checks)."""

    def __init__(self, base, key=""):
        self.base = base.rstrip("/")
        self.key = key

    def call(self, method, params=None, name="", timeout=60):
        """POST one request; returns (status, headers, body). Connection problems
        are a status of 0, a test failure the caller records, never a crash."""
        body = json.dumps(envelope(method, params, name)).encode()
        request = urllib.request.Request(self.base + "/mcp", data=body, method="POST",
                                         headers=headers_for(method, name, self.key))
        opener = urllib.request.build_opener(_NoRedirect())
        try:
            response = opener.open(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except Exception:
            return 0, {}, b""
        raw = response.read()
        return getattr(response, "status", None) or response.code, dict(response.headers), raw

    def tool(self, name, arguments=None):
        """Call one tool; a business error is the result's is_error, not a raise."""
        status, headers, raw = self.call("tools/call", {"name": name, "arguments": arguments or {}}, name=name)
        payload = {}
        try:
            payload = json.loads(raw)
        except (TypeError, ValueError):
            pass
        result = payload.get("result") if isinstance(payload, dict) else None
        result = result if isinstance(result, dict) else {}
        structured = result.get("structuredContent")
        text = [block.get("text", "") for block in result.get("content") or [] if isinstance(block, dict)]
        return {
            "http_status": status,
            "headers": headers,
            "is_error": result.get("isError") is True,
            "structured": structured if isinstance(structured, dict) else {},
            "text": text,
            "rpc_error": payload.get("error") if isinstance(payload, dict) else None,
        }

    def get(self, url, authorization=True, timeout=60):
        """GET a download address; no key when this client has none or
        ``authorization`` is false. Returns (status, headers, body)."""
        if not url.startswith(("http://", "https://")):
            url = self.base + url
        headers = {}
        if authorization and self.key:
            headers["Authorization"] = "Bearer " + self.key
        request = urllib.request.Request(url, method="GET", headers=headers)
        opener = urllib.request.build_opener(_NoRedirect())
        try:
            response = opener.open(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except Exception:
            return 0, {}, b""
        raw = response.read()
        return getattr(response, "status", None) or response.code, dict(response.headers), raw