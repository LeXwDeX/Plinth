#!/usr/bin/env python3
"""Read-only probes of the matting service's own contract, sent straight to it, not through Plinth (#540).

What Plinth's L5 batch (smoke-l5-matting.py) cannot show, because Plinth's Catalog and Adapter stand in front
of it: the service's own OpenAPI document, its validation answers and its authentication answers. Every probe
is free and none needs a picture beyond a 64x64 one; they cost the shared GPU nothing but the validation:

  health            GET /health
  openapi           GET /openapi.json: three endpoints present; the enums and the PipelineParams bounds are
                    listed; the whole document goes to the private directory (it is the source of the
                    committed, sanitized snapshot and its drift test)
  range-*           POST /api/v1/jobs with birefnet_process_res=511, expand=1001 and sensitivity=1.5 (one 64x64
                    picture each): 422 with the validation list (type, loc, msg)
  wrong-key         POST /api/v1/jobs with a non-empty wrong key: status, content type, WWW-Authenticate, body
  unknown-batch     GET /api/v1/batches/<random uuid>: 404
  unknown-task      GET /api/v1/tasks/<random uuid>/download with the key: 404; without a key: 401

A request that the service accepts when it should have refused it creates a batch of one 64x64 picture. That is
recorded as a finding (the bound is not enforced) and is the only way a probe leaves anything behind; nothing is
deleted or replayed.

Environment (read from the process environment only; never printed): MATTING_BASE_URL is the service's origin as
Plinth is configured with it (the API is under /api/v1), MATTING_API_KEY the key, sent as Authorization: Bearer.

Gate (upstream_shape.py "fake-default"): on a fake target (ART_AI_UPSTREAM_SHAPE=fake, MATTING_BASE_URL at the
fake's /matting) it runs with no --live; against any other target it needs BOTH --live and ART_AI_LIVE_ACK=1,
or it exits 2 before any request. The fake does not serve /openapi.json or /health and knows only two of the
parameter bounds: on a fake target those deviations are recorded as INFO (not modelled by the fake yet), on a
real target they fail the probe.

Raw answers go to a private 0700 directory outside the working tree (--private-dir, or a temporary one) and are
removed at the end unless --keep-private. --report DIR writes the PASS/FAIL/SKIP report and
matting-probe-evidence.json (statuses, content types, the validation lists and the OpenAPI summary).

  ART_AI_LIVE_ACK=1 MATTING_BASE_URL=... MATTING_API_KEY=... python3 scripts/smoke/matting-probe.py --live --report DIR
"""
import argparse
import collections
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402
import l5_direct as direct  # noqa: E402
import upstream_shape  # noqa: E402

Response = collections.namedtuple("Response", "status headers body")
WRONG_KEY = "l5-probe-wrong-key"
ENDPOINTS = (
    ("POST", re.compile(r"/api/v1/jobs$")),
    ("GET", re.compile(r"/api/v1/batches/\{[^}/]+\}$")),
    ("GET", re.compile(r"/api/v1/tasks/\{[^}/]+\}/download$")),
)
# The PipelineParams fields of the Catalog's Offering; the OpenAPI document is the authority for the real set.
CATALOG_FIELDS = ("birefnet_process_res", "birefnet_mask_blur", "expand", "tapered_corners", "blur_radius",
                  "sdmatte_process_res", "transparent_object", "mask_refine", "sensitivity")
# (field, out-of-range value, the bound the service documents)
RANGE_PROBES = (("birefnet_process_res", 511, "512..4096"), ("expand", 1001, "0..1000"), ("sensitivity", 1.5, "0..1"))


# ------------------------------------------------------------------ pure logic

def base_url_problem(base):
    parsed = urllib.parse.urlparse(base or "")
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        return "MATTING_BASE_URL must be an http(s) URL"
    if parsed.query or parsed.fragment or parsed.username or parsed.password:
        return "MATTING_BASE_URL must not carry credentials, a query or a fragment"
    return None


def parse_json(raw):
    try:
        return json.loads(raw)
    except (TypeError, ValueError):
        return None


def missing_endpoints(document):
    """The endpoints of ENDPOINTS the OpenAPI document does not declare, as 'METHOD pattern'."""
    paths = (document or {}).get("paths") or {}
    missing = []
    for method, pattern in ENDPOINTS:
        found = any(pattern.search(path) and method.lower() in (operations or {}) for path, operations in paths.items())
        if not found:
            missing.append(f"{method} {pattern.pattern}")
    return missing


def schema_type(schema):
    """The type words of a JSON schema node, looking through anyOf/oneOf (nullable fields)."""
    if not isinstance(schema, dict):
        return None
    if "type" in schema:
        return schema["type"]
    options = schema.get("anyOf") or schema.get("oneOf") or []
    words = [option.get("type") for option in options if isinstance(option, dict) and option.get("type") not in (None, "null")]
    return "|".join(sorted(set(str(word) for word in words))) or None


def bounds_of(schema):
    """Minimum, maximum and default of a property, looking through anyOf/oneOf."""
    found = {}
    nodes = [schema] + [option for option in (schema.get("anyOf") or schema.get("oneOf") or []) if isinstance(option, dict)]
    for node in nodes:
        for key in ("minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "default", "enum"):
            if key in node and key not in found:
                found[key] = node[key]
    return found


def pipeline_fields(document):
    """{field: {type, minimum, maximum, default}} of the schema that holds birefnet_process_res (the pipeline
    parameters), or {} when the document has none."""
    schemas = ((document or {}).get("components") or {}).get("schemas") or {}
    for schema in schemas.values():
        properties = schema.get("properties") if isinstance(schema, dict) else None
        if isinstance(properties, dict) and "birefnet_process_res" in properties:
            return {name: {"type": schema_type(node), **bounds_of(node)} for name, node in properties.items()
                    if isinstance(node, dict)}
    return {}


def enum_values(document):
    """{schema.property: [values]} for every enum of the document's schemas (the batch and task status words)."""
    schemas = ((document or {}).get("components") or {}).get("schemas") or {}
    found = {}
    for name, schema in schemas.items():
        if not isinstance(schema, dict):
            continue
        if isinstance(schema.get("enum"), list):
            found[name] = schema["enum"]
        for prop, node in (schema.get("properties") or {}).items():
            if isinstance(node, dict) and isinstance(node.get("enum"), list):
                found[f"{name}.{prop}"] = node["enum"]
    return found


def summarize_openapi(document):
    """What the evidence keeps of the OpenAPI document: version, title, paths with methods, enums and the
    pipeline field bounds. Never the servers or any example value."""
    document = document if isinstance(document, dict) else {}
    paths = {path: sorted(method for method in operations if method in ("get", "post", "put", "delete", "patch"))
             for path, operations in (document.get("paths") or {}).items() if isinstance(operations, dict)}
    fields = pipeline_fields(document)
    return {"openapi": document.get("openapi"), "title": (document.get("info") or {}).get("title"),
            "paths": paths, "enums": enum_values(document), "pipeline_fields": fields,
            "pipeline_fields_not_in_catalog": sorted(set(fields) - set(CATALOG_FIELDS)),
            "catalog_fields_not_in_pipeline": sorted(set(CATALOG_FIELDS) - set(fields)) if fields else []}


def validation_summary(parsed):
    """The reusable shape of an error body: for a FastAPI validation list the (type, loc, msg) of each item,
    otherwise the top-level keys and a short detail string. Inputs are not kept."""
    if isinstance(parsed, dict) and isinstance(parsed.get("detail"), list):
        return {"detail": [{"type": item.get("type"), "loc": item.get("loc"), "msg": common.one_line(str(item.get("msg")), 100)}
                           for item in parsed["detail"][:6] if isinstance(item, dict)]}
    if isinstance(parsed, dict):
        detail = parsed.get("detail")
        return {"keys": sorted(parsed)[:12], "detail": common.one_line(detail, 100) if isinstance(detail, str) else None}
    return {"keys": None, "detail": None}


def range_problem(response, field):
    """None when the answer is the 422 validation list that names the field, else why it is not."""
    if response.status != 422:
        return f"HTTP {response.status}, want 422"
    items = (validation_summary(parse_json(response.body)).get("detail") or [])
    if not any(item.get("loc") and item["loc"][-1] == field for item in items):
        return f"422 does not name {field} in a loc"
    return None


def answer_record(response):
    """The scrubbed record of one answer: status, content type, WWW-Authenticate and the body's shape."""
    return {"status": response.status, "content_type": response.headers.get("content-type"),
            "www_authenticate": response.headers.get("www-authenticate"),
            "body": validation_summary(parse_json(response.body))}


# ------------------------------------------------------------------ HTTP

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def make_call(base, timeout=60):
    """A call(method, path, headers, body) bound to the service's origin: no redirects, no cookies; a
    connection failure is status 0."""
    opener = urllib.request.build_opener(NoRedirect())

    def call(method, path, headers=None, body=None):
        request = urllib.request.Request(base.rstrip("/") + path, data=body, method=method, headers=headers or {})
        try:
            response = opener.open(request, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except (urllib.error.URLError, TimeoutError, OSError):
            return Response(0, {}, b"")
        raw = response.read(8 << 20)
        return Response(getattr(response, "status", None) or response.code,
                        {name.lower(): value for name, value in response.headers.items()}, raw)

    return call


# ------------------------------------------------------------------ the run

def jobs_request(call, key, pipeline, picture=None):
    """POST /api/v1/jobs with one 64x64 picture and the given pipeline JSON, as Plinth's Adapter does
    (multipart: pipeline_params_json and files)."""
    content_type, body = direct.multipart_body(
        [("pipeline_params_json", json.dumps(pipeline))],
        [("files", "input_1.png", "image/png", picture or direct.subject_png(64))])
    headers = {"Content-Type": content_type}
    if key:
        headers["Authorization"] = "Bearer " + key
    return call("POST", "/api/v1/jobs", headers, body)


def run_probe(call, key, report, findings, private, evidence):
    """Send every probe once, check the answers and fill the evidence."""
    health = call("GET", "/health")
    evidence["health"] = {"status": health.status, "content_type": health.headers.get("content-type")}
    findings.check("health answers 200", health.status == 200, f"HTTP {health.status}")

    document = call("GET", "/openapi.json")
    parsed = parse_json(document.body) if document.status == 200 else None
    evidence["openapi_status"] = document.status
    if findings.check("openapi.json is served as JSON", isinstance(parsed, dict) and "paths" in parsed,
                      f"HTTP {document.status}"):
        private.write("openapi.json", document.body)
        summary = summarize_openapi(parsed)
        evidence["openapi"] = summary
        missing = missing_endpoints(parsed)
        report.check("openapi declares the jobs, batches and download endpoints", not missing,
                     ", ".join(missing) or f"{len(summary['paths'])} paths")
        findings.note("openapi pipeline fields",
                      f"{len(summary['pipeline_fields'])} fields; not in the Catalog: {summary['pipeline_fields_not_in_catalog']}; "
                      f"Catalog fields not in the service: {summary['catalog_fields_not_in_pipeline']}")
        findings.note("openapi enums", ", ".join(f"{name}={values}" for name, values in sorted(summary["enums"].items())))

    evidence["ranges"] = {}
    for field, value, bound in RANGE_PROBES:
        response = jobs_request(call, key, {field: value})
        private.write(f"range-{field}.json", response.body or b"{}")
        problem = range_problem(response, field)
        evidence["ranges"][field] = dict(answer_record(response), sent=value, documented_bound=bound)
        findings.check(f"{field}={value} (bound {bound}) is refused with a 422 that names it", problem is None,
                       problem or "422")

    wrong = jobs_request(call, WRONG_KEY, {})
    evidence["wrong_key"] = answer_record(wrong)
    findings.check("a wrong non-empty key is refused", wrong.status in (401, 403),
                   f"HTTP {wrong.status}, www-authenticate {wrong.headers.get('www-authenticate')!r}")
    findings.note("wrong key answer", f"HTTP {wrong.status}, content type {wrong.headers.get('content-type')}, "
                                      f"www-authenticate {wrong.headers.get('www-authenticate')!r}")

    authenticated = {"Authorization": "Bearer " + key}
    batch = call("GET", f"/api/v1/batches/{uuid.uuid4()}", authenticated)
    evidence["unknown_batch"] = answer_record(batch)
    findings.check("an unknown batch is 404", batch.status == 404, f"HTTP {batch.status}")
    task = call("GET", f"/api/v1/tasks/{uuid.uuid4()}/download", authenticated)
    evidence["unknown_task_download"] = answer_record(task)
    findings.check("the download of an unknown task is 404", task.status == 404, f"HTTP {task.status}")
    anonymous = call("GET", f"/api/v1/tasks/{uuid.uuid4()}/download")
    evidence["download_without_key"] = answer_record(anonymous)
    findings.check("a download without a key is refused", anonymous.status in (401, 403), f"HTTP {anonymous.status}")


def parse_arguments(argv):
    parser = argparse.ArgumentParser(description="Read-only probes of the matting service's own contract (#540).")
    parser.add_argument("--live", action="store_true",
                        help="confirm requests to a real service (also needs ART_AI_LIVE_ACK=1)")
    parser.add_argument("--report", help="directory for the report and the evidence")
    parser.add_argument("--private-dir", default="",
                        help="existing 0700 directory outside the working tree for raw answers (default: a temporary one)")
    parser.add_argument("--keep-private", action="store_true", help="keep the raw answers instead of removing them")
    return parser.parse_args(argv)


def main(argv=None, environ=None, call=None):
    argv = sys.argv[1:] if argv is None else argv
    environ = os.environ if environ is None else environ
    if argv in (["-h"], ["--help"]):
        print(__doc__)
        return 0
    shape, refusal = upstream_shape.resolve("fake-default", upstream_shape.LIVE_FLAG in argv, environ)
    if refusal:
        print(refusal, file=sys.stderr)
        return 2
    arguments = parse_arguments(argv)
    missing = [name for name in ("MATTING_BASE_URL", "MATTING_API_KEY") if not environ.get(name)]
    if missing:
        print("missing environment: " + ", ".join(missing), file=sys.stderr)
        return 2
    problem = base_url_problem(environ["MATTING_BASE_URL"])
    if problem:
        print(problem, file=sys.stderr)
        return 2
    try:
        private = direct.PrivateDir(arguments.private_dir or None, arguments.keep_private)
    except ValueError as error:
        print(str(error), file=sys.stderr)
        return 2
    report = common.Report("matting-probe", arguments.report)
    findings = direct.Findings(report, not direct.generation_is_fake(shape))
    evidence = {"script": "matting-probe", "shape": shape}
    try:
        run_probe(call or make_call(environ["MATTING_BASE_URL"]), environ["MATTING_API_KEY"], report, findings, private, evidence)
    except Exception as error:  # a crash is a failure, never a silent pass
        report.failed("script error", f"{type(error).__name__}: {common.one_line(str(error), 120)}")
    finally:
        evidence["info"] = findings.info
        direct.write_evidence(report.directory, "matting-probe", evidence)
        private.close()
    return report.finish()


if __name__ == "__main__":
    sys.exit(main())
