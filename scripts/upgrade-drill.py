#!/usr/bin/env python3
"""Rehearse a release's migrations on a copy of an existing database (#435).

The release preflight starts the candidate image in an empty directory, so the
migrations only ever meet a brand-new database there. This script runs the same
image against a *copy* of a database that already holds data, inside a one-shot
directory: start the container, let it apply every migration, check health, then
read the migrated database back without writing to it.

Before the start it publishes, in the copy only, one Agent configuration version
in the shape a v1.8.0 build stored (#500, #525): a frozen tool list that still
enables the tools the Catalog has retired and has one tool turned off by an
administrator, and the default prompts of that release. The candidate must start
over it; its startup reconcile must publish a version without the retired tools,
in the current format, that turns off exactly the one tool (a tool added after
v1.8.0 must not be turned off) and leaves the prompts following the default
instead of freezing the old text.

It never writes to the source and never talks to a running instance. By default
it refuses to read a path that belongs to an environment (a release root from the
environments file, or a database that still has -wal/-shm sidecars); pointing it
at such a path needs both --allow-real-path and ART_AI_LIVE_ACK=1, the same
half-open rule the live upstream shape uses.

Usage:
    python3 scripts/upgrade-drill.py --image art-ai-go-production:<tag> \
        --database /tmp/drill-copies/art-ai.db [--report report.md] [--keep]

Exit codes: 0 the drill passed, 1 it ran and a check failed, 2 usage, guard or
environment error (nothing was started).
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

LIVE_ACK = "ART_AI_LIVE_ACK"
READY_TIMEOUT = 120.0
CONTAINER_PORT = 13333
# Representative tables: a migration must not lose rows, and every one of them
# must exist after the upgrade. The same set the Go upgrade test reads back.
SMOKE_TABLES = (
    "authentication_users",
    "go_chat_sessions",
    "go_chat_messages",
    "go_chat_turns",
    "processing_plans",
    "production_budgets",
    "budget_reservations",
    "skill_runs",
    "skill_run_steps",
    "generation_tasks",
    "generation_attempts",
    "generation_assets",
    "execution_grants",
    "agent_configuration_versions",
    "asset_lists",
    "asset_list_items",
)
# Agent tools the Catalog removed (#491). A configuration published before then
# still enables them; the candidate must read it rather than fail to start (#500).
RETIRED_AGENT_TOOLS = ("observe_task", "request_consent")
DRILL_ACTOR = "upgrade-drill"
# The tool the seeded version turns off, as an administrator would have (#525).
DRILL_DISABLED_TOOL = "create_motion_clip"
# A tool the v1.8.0 Schema did not have: it must stay on after the upgrade.
DRILL_ADDED_TOOL = "analyze_model"
# The tool names a version without the current format speaks for: the v1.8.0 set.
LEGACY_DEFAULTS = Path(__file__).resolve().parent.parent / "internal" / "agentconfiguration" / "legacy_defaults.json"
# The default prompts v1.8.0 shipped, kept as the Go tests' fixtures.
V180_PROMPT_DIR = Path(__file__).resolve().parent.parent / "internal" / "agentconfiguration" / "testdata"
FOLLOWED_PROMPT_PARTS = ["domain_context.image", "domain_context.model-3d", "system_prompt"]


def v180_default_prompts():
    try:
        system = (V180_PROMPT_DIR / "v1.8.0-system-prompt.txt").read_text(encoding="utf-8")
        contexts = {key: (V180_PROMPT_DIR / ("v1.8.0-domain-%s.txt" % key)).read_text(encoding="utf-8")
                    for key in ("image", "model-3d")}
    except OSError as error:
        raise DrillError("读不到 v1.8.0 默认提示词 %s：%s" % (V180_PROMPT_DIR, error))
    return system, contexts


def legacy_tool_baseline():
    try:
        names = json.loads(LEGACY_DEFAULTS.read_text(encoding="utf-8"))["tool_baseline"]
    except (OSError, ValueError, KeyError) as error:
        raise DrillError("读不到 v1.8.0 工具名清单 %s：%s" % (LEGACY_DEFAULTS, error))
    if not names or DRILL_DISABLED_TOOL not in names or DRILL_ADDED_TOOL in names:
        raise DrillError("v1.8.0 工具名清单 %s 与演练假设不符" % LEGACY_DEFAULTS)
    return names


class DrillError(Exception):
    """A usage, guard or environment problem: nothing was started."""


def environments_roots(path):
    """Release roots of every environment in the environments file."""
    if not path:
        return []
    try:
        document = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return []
    environments = document.get("environments") or {}
    roots = []
    for entry in environments.values():
        root = (entry or {}).get("root")
        if root:
            roots.append(Path(root).expanduser().resolve())
    return roots


def live_source_reason(source, roots):
    """Why this database must not be read without an explicit acknowledgement."""
    for sidecar in (source.with_name(source.name + "-wal"),
                    source.with_name(source.name + "-shm")):
        if sidecar.exists():
            return "旁边还有 %s，是正在使用的库" % sidecar.name
    for root in roots:
        if source == root or root in source.parents:
            return "位于环境发布根 %s 下" % root
    return None


def sha256_of(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def copy_database(source, data_dir):
    """Copy the database (and its sidecars) into the drill directory."""
    target = data_dir / "art-ai.db"
    shutil.copyfile(source, target)
    notes = []
    for suffix in ("-wal", "-shm"):
        sidecar = source.with_name(source.name + suffix)
        if sidecar.exists():
            shutil.copyfile(sidecar, target.with_name(target.name + suffix))
            notes.append("同时复制了 %s" % sidecar.name)
    return target, notes


def seed_legacy_agent_configuration(db_path):
    """Publish, in the drill copy, a version in the v1.8.0 shape.

    It extends the copy's latest version (or a minimal one when nothing was
    published) the way the service appends one, replacing the tool list with the
    v1.8.0 set (retired tools included) minus one tool an administrator turned
    off. Returns the seeded revision, or None and why it could not be seeded.
    """
    baseline = legacy_tool_baseline()
    system_prompt, domain_context = v180_default_prompts()
    try:
        connection = sqlite3.connect(str(db_path))
    except sqlite3.Error as error:
        raise DrillError("演练副本打不开，无法注入旧 Agent 配置（%s）：%s" % (db_path, error))
    try:
        present = connection.execute(
            "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
            ("agent_configuration_versions",)).fetchone()[0]
        if not present:
            return None, "副本没有 agent_configuration_versions 表（早于 0041），无法注入"
        row = connection.execute(
            "SELECT revision, configuration_json FROM agent_configuration_versions"
            " ORDER BY revision DESC LIMIT 1").fetchone()
        if row:
            revision, configuration = row[0], json.loads(row[1])
        else:
            revision, configuration = 0, {
                "system_prompt": "升级演练（#500）", "domain_context": {}, "skills": [],
                "enabled_tool_names": [], "enabled_mcp_preset_ids": []}
        configuration["enabled_tool_names"] = [name for name in baseline if name != DRILL_DISABLED_TOOL]
        configuration["system_prompt"], configuration["domain_context"] = system_prompt, domain_context
        for key in ("format", "disabled_tool_names", "follows_schema"):
            configuration.pop(key, None)
        created = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")
        connection.execute(
            "INSERT INTO agent_configuration_versions"
            "(revision, configuration_json, actor_id, source, created_at) VALUES (?, ?, ?, ?, ?)",
            (revision + 1, json.dumps(configuration, ensure_ascii=False), DRILL_ACTOR, "publish", created))
        connection.commit()
    except (sqlite3.Error, ValueError) as error:
        raise DrillError("向演练副本注入旧 Agent 配置失败：%s" % error)
    finally:
        connection.close()
    return revision + 1, "基于 revision %d 注入 revision %d：v1.8.0 工具清单（含 %s），关闭 %s，v1.8.0 默认提示词" % (
        revision, revision + 1, "、".join(RETIRED_AGENT_TOOLS), DRILL_DISABLED_TOOL)


def check_legacy_agent_configuration(db_path, seeded):
    """After startup the seeded version is intact and a newer one drops the
    retired tools. Read-only; returns (name, ok, detail) entries."""
    connection = connect_readonly(db_path)
    try:
        row = connection.execute(
            "SELECT configuration_json FROM agent_configuration_versions WHERE revision = ?",
            (seeded,)).fetchone()
        kept = bool(row) and all(name in json.loads(row[0]).get("enabled_tool_names", [])
                                 for name in RETIRED_AGENT_TOOLS)
        latest = connection.execute(
            "SELECT revision, configuration_json, source FROM agent_configuration_versions"
            " ORDER BY revision DESC LIMIT 1").fetchone()
    finally:
        connection.close()
    results = [("旧 Agent 配置保留", kept,
                "revision %d 原样保留" % seeded if kept else "revision %d 缺失或被改写" % seeded)]
    if not latest or latest[0] <= seeded:
        results.append(("旧 Agent 配置可读", False, "启动后没有发布清理版本（最新 revision %s）"
                        % (latest[0] if latest else "无")))
        return results
    left = [name for name in RETIRED_AGENT_TOOLS
            if name in json.loads(latest[1]).get("enabled_tool_names", [])]
    results.append(("旧 Agent 配置可读", not left,
                    "启动对账发布 revision %d（%s），不含已退役工具" % (latest[0], latest[2])
                    if not left else "revision %d 仍启用 %s" % (latest[0], "、".join(left))))
    stored = json.loads(latest[1])
    disabled = stored.get("disabled_tool_names") or []
    converted = stored.get("format") == 2 and disabled == [DRILL_DISABLED_TOOL]
    results.append(("v1.8.0 配置工具换算", converted,
                    "revision %d 为 format 2，只关闭 %s，其余（含 %s）默认开启" % (latest[0], DRILL_DISABLED_TOOL, DRILL_ADDED_TOOL)
                    if converted else "revision %d 的 format=%r、disabled_tool_names=%r，应为 format 2 且只含 %s"
                    % (latest[0], stored.get("format"), disabled, DRILL_DISABLED_TOOL)))
    followed = sorted(stored.get("follows_schema") or [])
    results.append(("v1.8.0 配置提示词跟随", followed == FOLLOWED_PROMPT_PARTS,
                    "revision %d 的系统提示词与两个领域上下文都跟随默认" % latest[0]
                    if followed == FOLLOWED_PROMPT_PARTS else "revision %d 的 follows_schema=%r，应为 %r"
                    % (latest[0], followed, FOLLOWED_PROMPT_PARTS)))
    return results


def connect_readonly(path):
    return sqlite3.connect("%s?mode=ro" % Path(path).as_uri(), uri=True)


def read_state(db_path):
    """Row counts and migration head of a database, read-only."""
    state = {"counts": {}, "head": 0}
    try:
        connection = connect_readonly(db_path)
    except sqlite3.Error as error:
        raise DrillError("演练副本不是可用的 SQLite 数据库（%s）：%s" % (db_path, error))
    try:
        for table in SMOKE_TABLES:
            try:
                state["counts"][table] = connection.execute(
                    "SELECT COUNT(*) FROM %s" % table).fetchone()[0]
            except sqlite3.Error as error:
                state["counts"][table] = "缺失（%s）" % error
        try:
            row = connection.execute(
                "SELECT COALESCE(MAX(version), 0) FROM go_schema_migrations").fetchone()
            state["head"] = row[0] if row else 0
        except sqlite3.Error:
            state["head"] = 0
    finally:
        connection.close()
    return state


def smoke_readonly(db_path):
    """Read-only checks on the migrated database. Returns (name, ok, detail)."""
    results = []
    connection = connect_readonly(db_path)
    try:
        result = connection.execute("PRAGMA integrity_check").fetchone()
        results.append(("integrity_check", result[0] == "ok", str(result[0])))
        dangling = connection.execute("PRAGMA foreign_key_check").fetchall()
        results.append(("foreign_key_check", not dangling, "空" if not dangling else repr(dangling[:5])))
        versions = [row[0] for row in connection.execute(
            "SELECT version FROM go_schema_migrations ORDER BY version")]
        contiguous = versions == list(range(1, len(versions) + 1))
        results.append(("migration 台账", contiguous,
                        "1..%d 共 %d 条" % (versions[-1], len(versions)) if contiguous else repr(versions[:10])))
        for table in SMOKE_TABLES:
            present = connection.execute(
                "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
                (table,)).fetchone()[0]
            results.append(("表 %s" % table, present == 1, "存在" if present else "缺失"))
    finally:
        connection.close()
    return results


def compare_counts(before, after):
    """Fail when a migration lost rows the source database held."""
    problems = []
    for table, count in before.items():
        if table not in after:
            problems.append("%s 演练前可读、演练后消失" % table)
            continue
        if isinstance(count, int) and isinstance(after[table], int) and after[table] < count:
            problems.append("%s 行数 %d → %d" % (table, count, after[table]))
    return problems


def free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def run_command(command, timeout=60):
    return subprocess.run(command, capture_output=True, text=True, timeout=timeout, check=False)


def wait_ready(url, timeout):
    """Poll a URL until it answers 200; True when it did."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if response.status == 200:
                    return True
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(1)
    return False


def container_env(secret, password):
    return [
        "APP_ENV=test",
        "APP_ADDR=0.0.0.0:%d" % CONTAINER_PORT,
        "DATABASE_PATH=/app/data/art-ai.db",
        "STORAGE_ROOT=/app/storage",
        "WEB_ASSETS_ROOT=/app/web",
        "LOG_DIR=/app/logs",
        "LOG_LEVEL=info",
        "AUTH_MODE=session",
        "SESSION_SECRET=%s" % secret,
        "ADMIN_USERNAME=drill",
        "ADMIN_PASSWORD=%s" % password,
        "ADMIN_EMAIL=drill@example.test",
        "LEGACY_BACKEND_STARTUP_ENABLED=false",
    ]


def start_container(image, workdir, port):
    """Run the candidate image on a loopback port over the copied data."""
    command = [
        "docker", "run", "-d", "--name", workdir.name,
        "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges",
        "--tmpfs", "/tmp:size=64m,mode=1777",
        "-p", "127.0.0.1:%d:%d" % (port, CONTAINER_PORT),
        "-v", "%s:/app/data" % (workdir / "data"),
        "-v", "%s:/app/storage" % (workdir / "storage"),
        "-v", "%s:/app/web:ro" % (workdir / "web"),
        "-v", "%s:/app/logs" % (workdir / "logs"),
    ]
    for entry in container_env(os.urandom(24).hex(), os.urandom(16).hex()):
        command += ["-e", entry]
    command.append(image)
    result = run_command(command)
    if result.returncode:
        raise DrillError("docker run 失败: %s" % (result.stderr or result.stdout).strip())


def container_logs(name, tail=50):
    result = run_command(["docker", "logs", "--tail", str(tail), name])
    return (result.stdout + result.stderr).strip()


def render_report(fields, checks, problems, verdict, logs):
    lines = ["# 升级演练报告（#435）", "", "- 结论：%s" % verdict]
    lines += ["- %s：%s" % (key, value) for key, value in fields]
    lines += ["", "## 检查", ""]
    for name, ok, detail in checks:
        lines.append("- %s %s：%s" % ("PASS" if ok else "FAIL", name, detail))
    if problems:
        lines += ["", "## 失败原因", ""]
        lines += ["- %s" % problem for problem in problems]
    if logs:
        lines += ["", "## 容器日志尾部", "", "```", logs, "```"]
    return "\n".join(lines) + "\n"


def drill(args):
    source = Path(args.database).expanduser()
    if not source.is_file():
        raise DrillError("数据库不存在或不是文件：%s" % source)
    source = source.resolve()
    roots = environments_roots(args.environments)
    reason = live_source_reason(source, roots)
    acknowledged = args.allow_real_path and os.environ.get(LIVE_ACK) == "1"
    if not acknowledged:
        if args.allow_real_path:
            raise DrillError("半开拒绝：给了 --allow-real-path 但没有 %s=1" % LIVE_ACK)
        if os.environ.get(LIVE_ACK) == "1":
            raise DrillError("半开拒绝：给了 %s=1 但没有 --allow-real-path" % LIVE_ACK)
        if reason:
            raise DrillError(
                "拒绝把真实路径当作演练数据：%s（%s）。先复制一份再跑；"
                "确实要用真实路径时同时给 --allow-real-path 与 %s=1" % (source, reason, LIVE_ACK))
    if shutil.which("docker") is None:
        raise DrillError("docker 不在 PATH")
    image_check = run_command(["docker", "image", "inspect", "--format", "{{.Id}}", args.image])
    if image_check.returncode:
        raise DrillError("候选镜像不存在：%s" % args.image)

    workdir = Path(args.workdir).expanduser().resolve() if args.workdir else Path(
        tempfile.mkdtemp(prefix="plinth-upgrade-drill."))
    try:
        for name in ("data", "storage", "web", "logs"):
            (workdir / name).mkdir(parents=True, exist_ok=True)
        if hasattr(os, "geteuid") and os.geteuid() == 0:
            # The candidate image runs as the artai user; the release scripts
            # hand it the same two ids when they prepare a directory.
            for name in ("data", "storage", "web", "logs"):
                os.chown(workdir / name, 100, 101)
            os.chown(workdir, 100, 101)
        target, notes = copy_database(source, workdir / "data")
        seeded, seed_detail = seed_legacy_agent_configuration(target)
        notes.append("旧 Agent 配置：%s" % seed_detail)
        before = read_state(target)
        port = args.port or free_port()
        started = time.time()
        start_container(args.image, workdir, port)
        container = workdir.name
        ready = wait_ready("http://127.0.0.1:%d/readyz" % port, args.timeout)
        health = wait_ready("http://127.0.0.1:%d/healthz" % port, 5) if ready else False
        after = read_state(target) if ready else before
        checks = []
        if ready:
            checks.append(("容器启动", True, "%s 就绪" % args.image))
            checks.append(("/healthz", health, "200" if health else "无响应"))
            try:
                checks += smoke_readonly(target)
                if seeded is None:
                    checks.append(("旧 Agent 配置可读", False, seed_detail))
                else:
                    checks += check_legacy_agent_configuration(target, seeded)
            except (sqlite3.Error, ValueError) as error:
                checks.append(("只读冒烟", False, "读回失败：%s" % error))
        else:
            checks.append(("容器启动", False, "在 %.0fs 内没有就绪" % args.timeout))
        problems = []
        if ready:
            problems += compare_counts(before["counts"], after["counts"])
        fields = [
            ("候选镜像", "%s（%s）" % (args.image, image_check.stdout.strip() or "未知 ID")),
            ("数据库来源", "%s（%s，%d 字节）%s" % (
                source, sha256_of(source), source.stat().st_size,
                "；声明为真实路径" if acknowledged and reason else "")),
            ("真实路径判定", "%d 个环境发布根（%s）" % (len(roots), args.environments)
             if roots else "未读到环境文件，只能按 -wal/-shm 边车判定"),
            ("来源备注", "；".join(notes) or "无 -wal/-shm 边车"),
            ("演练目录", str(workdir)),
            ("监听地址", "http://127.0.0.1:%d" % port),
            ("容器", container),
            ("耗时", "%.1fs" % (time.time() - started)),
            ("迁移版本", "%s → %s" % (before["head"], after["head"])),
        ]
        problems += ["%s 未通过：%s" % (name, detail) for name, ok, detail in checks if not ok]
        logs = container_logs(container)
        verdict = "PASS" if ready and not problems else "FAIL"
        report = render_report(fields, checks, problems, verdict, logs)
        (workdir / "report.md").write_text(report, encoding="utf-8")
        print(report)
        if args.report:
            try:
                Path(args.report).expanduser().write_text(report, encoding="utf-8")
            except OSError as error:
                raise DrillError("报告写不出去（%s）：%s" % (args.report, error))
        return 0 if verdict == "PASS" else 1
    finally:
        run_command(["docker", "rm", "-f", workdir.name], timeout=30)
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)


def parse_args(argv):
    parser = argparse.ArgumentParser(description="用存量数据库副本演练候选镜像的迁移（#435）")
    parser.add_argument("--image", required=True, help="候选镜像引用")
    parser.add_argument("--database", required=True, help="存量数据库副本（只读，脚本不会改动它）")
    parser.add_argument("--workdir", help="一次性演练目录；缺省为 mktemp -d")
    parser.add_argument("--port", type=int, help="回环端口；缺省自动选一个空闲端口")
    parser.add_argument("--report", help="把报告写到这个文件（同时始终打印到标准输出）")
    parser.add_argument("--timeout", type=float, default=READY_TIMEOUT, help="等待就绪的秒数")
    parser.add_argument("--keep", action="store_true",
                        help="保留演练目录与 report.md；容器总是清理")
    parser.add_argument("--allow-real-path", action="store_true",
                        help="允许 --database 指向真实环境路径（须同时设 %s=1）" % LIVE_ACK)
    parser.add_argument("--environments", help="环境文件；缺省用 local_environments 的查找顺序")
    return parser.parse_args(argv)


def main(argv=None):
    args = parse_args(argv)
    if not args.environments:
        # The environments file is untracked, so a worktree falls back to the
        # main checkout's copy; when there is none, no release root is known.
        scripts = Path(__file__).resolve().parent
        sys.path.insert(0, str(scripts))
        try:
            import local_environments
            args.environments = str(local_environments.environments_file())
        except ImportError:
            args.environments = None
        finally:
            sys.path.pop(0)
    try:
        return drill(args)
    except DrillError as error:
        print("DRILL FAIL: %s" % error, file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print("DRILL FAIL: 已中断", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())