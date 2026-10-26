#!/usr/bin/env python3
"""Generate formal cutover baselines from one validated inventory and supplement."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import sys


TABLES = (
    "users", "asset_registry", "usage_ledger", "balance_snapshots",
    "chat_sessions", "chat_messages", "task_cache", "task_input_snapshot",
    "audit_logs", "canvas_projects", "canvas_nodes",
)
STORAGE = ("assets", "generated", "cache", "audit", "thumbnails", "task-inputs")
BASELINE_FIELDS = (
    "users", "generations", "usage_tasks", "usage_credits", "audit_minimum",
    "output_assets", "input_assets", "chat_sessions", "chat_messages",
    "canvas_projects", "canvas_nodes",
)
FORMAT = "art-ai-formal-production-inventory-v2"
SUPPLEMENT_FORMAT = "art-ai-formal-migration-artifact-supplement-v1"
SCRIPT_FORMAT = "art-ai-production-acceptance-instance-v1"


def fail(reason):
    raise ValueError(reason)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            fail("duplicate JSON field")
        result[key] = value
    return result


def reject_constant(_value):
    fail("non-standard JSON number")


def read_private_regular(path, *, require_private=True):
    path = Path(path)
    try:
        info = path.lstat()
    except OSError:
        fail("required input file is unavailable")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        fail("input must be a regular non-symlink file")
    if require_private and stat.S_IMODE(info.st_mode) != 0o600:
        fail("inventory and supplement must have mode 0600")
    try:
        return path.read_bytes()
    except OSError:
        fail("required input file is unreadable")


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def nonnegative_integer(value):
    return type(value) is int and value >= 0


def parse_inventory(raw):
    try:
        text = raw.decode("utf-8", errors="strict")
    except UnicodeError:
        fail("inventory encoding is invalid")
    fields = {}
    repeatable = {"legacy_mount"}
    for line in text.splitlines():
        if not line or "=" not in line:
            fail("inventory contains an invalid record")
        key, value = line.split("=", 1)
        if not re.fullmatch(r"[a-z][a-z0-9_-]*", key):
            fail("inventory contains an invalid field name")
        if key in repeatable:
            fields.setdefault(key, []).append(value)
        elif key in fields:
            fail("inventory contains duplicate fields")
        else:
            fields[key] = value
    expected = {
        "format", "collected_at_utc", "source_mutated", "secrets_read",
        "external_network_accessed", "hostname", "kernel", "operator_uid",
        "legacy_root", "legacy_storage", "legacy_database", "legacy_assets", "legacy_cache",
        "legacy_audit", "legacy_container", "legacy_container_name",
        "legacy_container_image", "legacy_container_image_id", "legacy_container_running",
        "legacy_compose_project", "legacy_compose_service", "legacy_compose_working_dir",
        "legacy_port_bindings", "legacy_mount", "database_snapshot_sha256",
        "database_snapshot_bytes", "database_quick_check", "database_foreign_key_violations",
        "filesystem_capacity_kb", "filesystem_available_kb",
    }
    expected.update(f"table_{table}_{suffix}" for table in TABLES for suffix in ("exists", "rows"))
    expected.update(f"storage_{directory}_{suffix}" for directory in STORAGE for suffix in ("files", "bytes"))
    expected.update(f"baseline_{field}" for field in (
        "users", "generations", "usage_tasks", "usage_credits", "audit_minimum",
        "input_assets", "chat_sessions", "chat_messages", "canvas_projects", "canvas_nodes",
    ))
    if set(fields) != expected:
        fail("inventory fields are missing or unrecognized")
    if fields["format"] != FORMAT:
        fail("inventory format is unsupported")
    for key, expected_value in (("source_mutated", "false"), ("secrets_read", "false"),
                                ("external_network_accessed", "false"),
                                ("database_quick_check", "ok"),
                                ("database_foreign_key_violations", "0"),
                                ("legacy_container_running", "true")):
        if fields[key] != expected_value:
            fail("inventory safety or consistency assertion failed")
    if not re.fullmatch(r"[0-9a-f]{64}", fields["database_snapshot_sha256"]):
        fail("inventory database digest is invalid")
    if not fields["legacy_root"].startswith("/") or not fields["legacy_database"].startswith("/"):
        fail("inventory source paths are not absolute")
    if fields["legacy_storage"] != fields["legacy_root"].rstrip("/") + "/storage":
        fail("inventory storage path does not match its source root")
    for key in ("hostname", "kernel", "legacy_container", "legacy_container_name",
                "legacy_container_image", "legacy_container_image_id", "legacy_compose_project",
                "legacy_compose_service", "legacy_compose_working_dir"):
        if not fields[key]:
            fail("inventory is missing a required topology value")
    if fields["legacy_container_name"].lstrip("/") != fields["legacy_container"]:
        fail("inventory container name and inspected identity disagree")
    try:
        port_bindings = json.loads(
            fields["legacy_port_bindings"],
            object_pairs_hook=unique_object,
            parse_constant=reject_constant,
        )
    except (ValueError, json.JSONDecodeError):
        fail("inventory port binding record is invalid")
    if port_bindings is not None and not isinstance(port_bindings, dict):
        fail("inventory port binding record has an unexpected shape")
    mounts = {}
    for record in fields["legacy_mount"]:
        parts = record.split("|")
        if len(parts) != 4:
            fail("inventory mount record is invalid")
        mount_type, source, destination, read_write = parts
        if (mount_type not in {"bind", "volume"} or not source.startswith("/") or
                destination not in {"/app/data", "/app/uploads", "/app/asset-cache", "/app/audit-assets"} or
                read_write not in {"true", "false"} or destination in mounts):
            fail("inventory mount records are incomplete or inconsistent")
        mounts[destination] = source
    if set(mounts) != {"/app/data", "/app/uploads", "/app/asset-cache", "/app/audit-assets"}:
        fail("inventory required container mounts are incomplete")
    if fields["legacy_database"] != mounts["/app/data"].rstrip("/") + "/prod.db":
        fail("inventory database path does not match its container mount")
    if (fields["legacy_assets"] != mounts["/app/uploads"] or
            fields["legacy_cache"] != mounts["/app/asset-cache"] or
            fields["legacy_audit"] != mounts["/app/audit-assets"]):
        fail("inventory asset paths do not match their container mounts")
    try:
        collected = datetime.fromisoformat(fields["collected_at_utc"].replace("Z", "+00:00"))
    except ValueError:
        fail("inventory timestamp is invalid")
    if collected.tzinfo is None or collected.utcoffset().total_seconds() != 0:
        fail("inventory timestamp must be UTC")
    for table in TABLES:
        if fields[f"table_{table}_exists"] != "1":
            fail("inventory is missing a required source table")
        parse_count(fields, f"table_{table}_rows")
    for directory in STORAGE:
        parse_count(fields, f"storage_{directory}_files")
        parse_count(fields, f"storage_{directory}_bytes")
    for key in expected:
        if key.startswith("baseline_") or key in {"database_snapshot_bytes", "filesystem_capacity_kb", "filesystem_available_kb", "operator_uid"}:
            parse_count(fields, key)
    if int(fields["database_snapshot_bytes"]) < 1 or int(fields["filesystem_available_kb"]) > int(fields["filesystem_capacity_kb"]):
        fail("inventory database size or filesystem counters are inconsistent")
    table_map = {
        "baseline_users": "table_users_rows",
        "baseline_usage_tasks": "table_usage_ledger_rows",
        "baseline_audit_minimum": "table_audit_logs_rows",
        "baseline_input_assets": "table_asset_registry_rows",
        "baseline_chat_sessions": "table_chat_sessions_rows",
        "baseline_chat_messages": "table_chat_messages_rows",
        "baseline_canvas_projects": "table_canvas_projects_rows",
        "baseline_canvas_nodes": "table_canvas_nodes_rows",
    }
    for baseline_key, table_key in table_map.items():
        if fields[baseline_key] != fields[table_key]:
            fail("inventory baseline aggregate conflicts with its table count")
    if int(fields["baseline_generations"]) > int(fields["baseline_usage_tasks"]):
        fail("inventory generation count exceeds ledger row count")
    return fields


def parse_count(fields, key):
    value = fields.get(key, "")
    if not re.fullmatch(r"0|[1-9][0-9]*", value):
        fail("inventory count is invalid")
    return int(value)


def validate_origin(value):
    if not isinstance(value, str) or not value or any(ord(char) < 32 for char in value):
        fail("supplement contains an invalid HTTP origin")
    if not (value.startswith("http://") or value.startswith("https://")) or any(char in value for char in "@?#"):
        fail("supplement URLs must be HTTP(S) origins without credentials or paths")
    authority = value.removeprefix("http://").removeprefix("https://")
    if not authority or "/" in authority:
        fail("supplement URLs must be HTTP(S) origins without credentials or paths")
    if authority.count(":") > 1:
        fail("supplement IPv6 hosts are incompatible with the acceptance template")
    if ":" in authority:
        host, port = authority.split(":", 1)
        if not re.fullmatch(r"[1-9][0-9]{0,4}", port) or int(port) > 65535:
            fail("supplement contains an invalid HTTP port")
    else:
        host = authority
    if not re.fullmatch(r"[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?", host) or ".." in host:
        fail("supplement contains an invalid HTTP host")
    return value


def validate_absolute_path(value):
    if not isinstance(value, str) or not value.startswith("/") or any(ord(char) < 32 for char in value):
        fail("supplement paths must be absolute POSIX paths")
    path = PurePosixPath(value)
    if (".." in path.parts or value.endswith("/") or "//" in value or
            not re.fullmatch(r"/[A-Za-z0-9_./-]+", value)):
        fail("supplement path contains traversal")
    return value


def parse_supplement(raw, inventory_sha):
    try:
        doc = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object, parse_constant=reject_constant)
    except (UnicodeError, json.JSONDecodeError):
        fail("supplement JSON is invalid")
    if not isinstance(doc, dict) or set(doc) != {"format", "inventory_sha256", "review", "acceptance"}:
        fail("supplement fields are incomplete or unrecognized")
    if doc["format"] != SUPPLEMENT_FORMAT or doc["inventory_sha256"] != inventory_sha:
        fail("supplement is not bound to this inventory")
    review = doc["review"]
    if not isinstance(review, dict) or set(review) != {"reviewer", "reviewed_at_utc", "evidence_sha256"}:
        fail("supplement review attestation is incomplete")
    if not isinstance(review["reviewer"], str) or not re.fullmatch(r"[A-Za-z0-9_.@-]{1,128}", review["reviewer"]):
        fail("supplement reviewer is invalid")
    if not isinstance(review["evidence_sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", review["evidence_sha256"]):
        fail("supplement evidence digest is invalid")
    try:
        timestamp = datetime.fromisoformat(review["reviewed_at_utc"].replace("Z", "+00:00"))
    except (AttributeError, ValueError):
        fail("supplement review timestamp is invalid")
    if timestamp.tzinfo is None or timestamp.utcoffset().total_seconds() != 0:
        fail("supplement review timestamp must be UTC")
    acceptance = doc["acceptance"]
    required_acceptance = {
        "output_assets", "acceptance_base_url", "external_webhook_url",
        "database_path", "env_path",
    }
    if not isinstance(acceptance, dict) or set(acceptance) != required_acceptance:
        fail("supplement is missing formal acceptance topology or output count")
    if not nonnegative_integer(acceptance["output_assets"]):
        fail("supplement output asset count must be a non-negative integer")
    acceptance["acceptance_base_url"] = validate_origin(acceptance["acceptance_base_url"])
    acceptance["external_webhook_url"] = validate_origin(acceptance["external_webhook_url"])
    acceptance["database_path"] = validate_absolute_path(acceptance["database_path"])
    acceptance["env_path"] = validate_absolute_path(acceptance["env_path"])
    return doc


def shell_quote(value):
    return "'" + value.replace("'", "'\\''") + "'"


def write_private(path, data, mode=0o600):
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd = os.open(path, flags, mode)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", required=True, help="0600 formal inventory report")
    parser.add_argument("--supplement", required=True, help="0600 reviewed structured topology supplement")
    parser.add_argument("--output-dir", required=True, help="new private directory; existing paths are refused")
    args = parser.parse_args(argv)
    output = Path(args.output_dir)
    created = False
    try:
        inventory_path = Path(args.inventory)
        supplement_path = Path(args.supplement)
        inventory_raw = read_private_regular(inventory_path)
        supplement_raw = read_private_regular(supplement_path)
        inventory_sha = sha256(inventory_raw)
        inventory = parse_inventory(inventory_raw)
        supplement = parse_supplement(supplement_raw, inventory_sha)
        script_template = Path(__file__).resolve().parents[1] / "scripts/post-cutover-acceptance.sh"
        template_info = script_template.lstat()
        if stat.S_ISLNK(template_info.st_mode) or not stat.S_ISREG(template_info.st_mode):
            fail("acceptance template is not a regular file")
        template_bytes = script_template.read_bytes()
        if not template_bytes.startswith(b"#!/usr/bin/env bash\n"):
            fail("acceptance template header is incompatible")
        if not output.name or output.name in {".", ".."}:
            fail("output directory is invalid")
        parent = output.parent
        parent_info = parent.lstat()
        if stat.S_ISLNK(parent_info.st_mode) or not stat.S_ISDIR(parent_info.st_mode):
            fail("output parent must be a real existing directory")
        output = parent.resolve() / output.name
        if output.exists() or output.is_symlink():
            fail("output directory already exists; refusing to overwrite")
        if any(path.resolve().is_relative_to(output) for path in (inventory_path, supplement_path, script_template)):
            fail("output directory overlaps an input")

        acceptance_values = {
            "users": parse_count(inventory, "baseline_users"),
            "generations": parse_count(inventory, "baseline_generations"),
            "usage_tasks": parse_count(inventory, "baseline_usage_tasks"),
            "usage_credits": parse_count(inventory, "baseline_usage_credits"),
            "audit_minimum": parse_count(inventory, "baseline_audit_minimum"),
            "output_assets": supplement["acceptance"]["output_assets"],
            "input_assets": parse_count(inventory, "baseline_input_assets"),
            "chat_sessions": parse_count(inventory, "baseline_chat_sessions"),
            "chat_messages": parse_count(inventory, "baseline_chat_messages"),
            "canvas_projects": parse_count(inventory, "baseline_canvas_projects"),
            "canvas_nodes": parse_count(inventory, "baseline_canvas_nodes"),
            "external_webhook_url": supplement["acceptance"]["external_webhook_url"],
            "inventory_sha256": inventory_sha,
            "database_snapshot_sha256": inventory["database_snapshot_sha256"],
            "supplement_sha256": sha256(supplement_raw),
        }
        source_baseline = {
            "format": "art-ai-production-source-baseline-v1",
            "inventory_sha256": inventory_sha,
            "database_snapshot_sha256": inventory["database_snapshot_sha256"],
        }
        outdir = output
        outdir.mkdir(mode=0o700)
        created = True
        source_path = outdir / "source-baseline.json"
        acceptance_path = outdir / "acceptance-baseline.json"
        script_path = outdir / "post-cutover-acceptance.sh"
        source_bytes = (json.dumps(source_baseline, sort_keys=True, indent=2) + "\n").encode()
        acceptance_bytes = (json.dumps(acceptance_values, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode()
        script_prefix = (
            "#!/usr/bin/env bash\n"
            "# Generated from a reviewed inventory-bound supplement.\n"
            f"export ACCEPTANCE_BASE_URL={shell_quote(supplement['acceptance']['acceptance_base_url'])}\n"
            f"export ACCEPTANCE_DATABASE={shell_quote(supplement['acceptance']['database_path'])}\n"
            f"export ACCEPTANCE_ENV={shell_quote(supplement['acceptance']['env_path'])}\n"
            f"export ACCEPTANCE_BASELINE_FILE={shell_quote(str(acceptance_path))}\n"
        ).encode()
        script_bytes = script_prefix + template_bytes.split(b"\n", 1)[1]
        write_private(source_path, source_bytes)
        write_private(acceptance_path, acceptance_bytes)
        write_private(script_path, script_bytes, 0o700)
        manifest = {
            "format": SCRIPT_FORMAT,
            "inventory_sha256": inventory_sha,
            "database_snapshot_sha256": inventory["database_snapshot_sha256"],
            "supplement_sha256": sha256(supplement_raw),
            "template_sha256": sha256(template_bytes),
            "artifacts": {
                "source-baseline.json": sha256(source_bytes),
                "acceptance-baseline.json": sha256(acceptance_bytes),
                "post-cutover-acceptance.sh": sha256(script_bytes),
            },
        }
        write_private(outdir / "artifact-manifest.json", (json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode())
        dir_fd = os.open(outdir, os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
        print(f"formal migration artifacts generated; inventory_sha256={inventory_sha}")
        return 0
    except (OSError, ValueError, KeyError, TypeError) as error:
        if created:
            shutil.rmtree(output, ignore_errors=True)
        reason = str(error)
        print(f"formal migration artifact generation failed: {reason or 'invalid input'}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
