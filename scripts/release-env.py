#!/usr/bin/env python3
"""Release Plinth to a named environment from the untracked local deployment folder.

The environments file is local (gitignored, deploy/local/environments.json; see
scripts/local_environments.py for the lookup, including the main-checkout
fallback for worktrees): it holds hosts, roots, ports and Compose names, never
secrets. deploy/environments.example.json is its template.

  release-env.py list
  release-env.py show <env>
  release-env.py release <env> [--revision SHA] [--acceptance-note ID] [--release-version NAME] [--live-upstream]
  release-env.py release <env> [--revision SHA] [--acceptance-note ID] [--release-version NAME] [--live-chat]
  release-env.py smoke <env> -- <command...>      # BASE, ADMIN_*, AGENT_TEST_*, EXPECT_SHA, RELEASE_ENV,
                                                  # ART_AI_UPSTREAM_SHAPE set
  release-env.py compose-env <env>                # GO_* layout for manual compose and
                                                  # ART_AI_PROTECTED_PATHS for the migration tools

An environment with "promote_from" only receives an exact origin/main revision
that the source environment released successfully (its manifest) AND that has a
passing acceptance report (L0, L1, L1_agent, related) in a native Issue note.
The release itself is scripts/release-go-testenv.sh with the environment's
parameters; nothing about the server flow is duplicated here.

After a successful release in the fake upstream shape, the three scripted smoke
layers (L0, L1, L1 Agent) run automatically and leave a machine-written report
under the release root (production/reports/, untracked): one JSON with a check per
layer and one log per layer. An environment with "promote_from" also checks that
report in the source environment instead of trusting a hand-written claim (#411).

Upstream shape (#219): the disposable "data": "ephemeral" environment (dev-test)
releases in the fake shape by default: a fake upstream service runs in the same
Compose project and every Provider and chat connection points at it, so no real
upstream is called and no real key is used. A live dev-test release needs both
--live-upstream and ART_AI_LIVE_ACK=1 (each live release also needs the user's
consent in the conversation, AGENTS.md). Other environments are always live.

Mixed shape (#416): --live-chat releases the ephemeral stack with the chat
connections of the runtime environment (a real model deciding on Plinth's tool
table) and every generation Provider still on the fake upstream. It needs
ART_AI_LIVE_ACK=1 for the same reason a live release does: the chat calls cost
tokens. The two switches are exclusive.

Release string (#676): every release links a release string into the binary, shown
next to the brand and as "release" in /api/version (the "version" field stays the
commit). --release-version names it (the formal-test release that becomes a GitHub
Release passes the tag it will be tagged with, e.g. v1.9.5); without it the string is
`git describe --tags --always` of the released revision (e.g. v1.9.4-12-gfbf5000).
It travels with the commit: server steps -> GO_RELEASE_VERSION build argument -> ldflags.
"""
import argparse
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys

SCRIPTS = Path(__file__).resolve().parent
SERVICE = "art-ai-go"
LIVE_ACK = "ART_AI_LIVE_ACK"
# Every key is optional: a layout names only the ones its stack sets, and the server
# step takes the compose default for the rest. The external MCP listener's host side
# (#332) is such a pair: neither key means the compose defaults (loopback, port 13343).
LAYOUT_KEYS = (
    "GO_WEB_BIND_IP", "GO_WEB_HOST_PORT", "GO_PROD_DB_DIR", "GO_PROD_STORAGE_DIR",
    "GO_PROD_WEB_DATA_DIR", "GO_PROD_LOG_DIR", "GO_LEGACY_GENERATED_IMAGES_DIR",
    "GO_LEGACY_ASSET_CACHE_DIR", "GO_LEGACY_THUMBNAIL_CACHE_DIR", "GO_LEGACY_INPUT_ASSETS_DIR",
    "MCP_PUBLIC_BIND_IP", "MCP_PUBLIC_HOST_PORT",
)
IPV4 = re.compile(r"(?:(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])")
PORT = re.compile(r"[1-9][0-9]{0,4}")
WORD = re.compile(r"[A-Za-z0-9_./:-]+")
ADD_HOST = re.compile(r"[A-Za-z0-9][A-Za-z0-9.-]{0,252}:[0-9A-Fa-f.:]+")
COMPOSE_PROJECT = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
URL = re.compile(r"https?://[A-Za-z0-9.:/_-]+")
URL_LIST = re.compile(r"https?://[A-Za-z0-9.:/_-]+(,(https?://[A-Za-z0-9.:/_-]+|direct|off))*")


class EnvironmentError_(Exception):
    """Fixed, credential-free diagnostics only."""


def load_helper(filename, name):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def promotion_rules():
    return load_helper("release-promotion.py", "release_promotion_rules")


def run(*args, input=None, timeout=120):
    try:
        result = subprocess.run([str(a) for a in args], input=input, capture_output=True,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise EnvironmentError_(f"{Path(str(args[0])).name} was unavailable or timed out") from None
    if result.returncode:
        raise EnvironmentError_(f"{Path(str(args[0])).name} failed (exit {result.returncode})")
    return result.stdout.decode("utf-8")


def host_file_state(environment, path):
    """True when the host has this file, False when it does not; an unreachable host raises."""
    try:
        result = subprocess.run(["ssh", environment["ssh"], f"test -f {shlex.quote(path)}"],
                                capture_output=True, timeout=120, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise EnvironmentError_("ssh was unavailable or timed out") from None
    if result.returncode == 0:
        return True
    if result.returncode == 1:
        return False
    raise EnvironmentError_("ssh failed")


def write_host_file(environment, path, content, directory_mode=None):
    """Write one report file under the release root (untracked, 0600)."""
    directory = shlex.quote(str(Path(path).parent))
    command = f"mkdir -p {directory}"
    if directory_mode:
        command += f" && chmod {directory_mode} {directory}"
    command += f" && cat > {shlex.quote(path)} && chmod 600 {shlex.quote(path)}"
    run("ssh", environment["ssh"], command, input=content.encode("utf-8"))


def load_environments(path):
    try:
        document = json.loads(Path(path).read_text())
    except FileNotFoundError:
        raise EnvironmentError_(f"{path} is missing; copy deploy/environments.example.json and fill it in") from None
    except ValueError:
        raise EnvironmentError_(f"{path} is not valid JSON") from None
    if not isinstance(document, dict) or document.get("version") != 1 or not isinstance(document.get("environments"), dict):
        raise EnvironmentError_("environments file needs version 1 and an environments object")
    for name, environment in document["environments"].items():
        validate_environment(name, environment, document)
    return document


def validate_environment(name, environment, document):
    def need(condition, message):
        if not condition:
            raise EnvironmentError_(f"environment {name}: {message}")
    need(re.fullmatch(r"[a-z][a-z0-9-]{0,31}", name), "name must be lowercase words")
    need(isinstance(environment, dict), "must be an object")
    for key in ("ssh", "root", "base_url", "compose_project"):
        need(isinstance(environment.get(key), str) and environment[key], f"{key} is required")
    need(re.fullmatch(r"[a-z_][a-z0-9_-]*@[A-Za-z0-9.-]+", environment["ssh"]), "ssh must be user@host")
    need(environment["root"].startswith("/") and WORD.fullmatch(environment["root"]) and ".." not in environment["root"],
         "root must be an absolute path")
    need(re.fullmatch(r"https?://[A-Za-z0-9.:-]+", environment["base_url"]), "base_url must be scheme://host[:port]")
    need(COMPOSE_PROJECT.fullmatch(environment["compose_project"]), "compose_project is invalid")
    single = environment.get("single_stack")
    protect = environment.get("protect_projects")
    if protect is not None:
        need(isinstance(protect, list) and protect and all(
            isinstance(other, str) and COMPOSE_PROJECT.fullmatch(other) and other != environment["compose_project"]
            for other in protect), "protect_projects must list other Compose project names")
        need(single is None, "a single_stack environment has no other projects to protect")
    # managed_by (stacks released by another tool) was removed with legacy-formal (#74);
    # a stale field would silently mislead, so refuse it with a fixable message.
    need("managed_by" not in environment,
         "managed_by was removed (#74, the legacy-formal stack is retired); delete this field")
    # #334: a stack released here must name the other Compose projects on
    # its host, so a release never touches them. Without single_stack the
    # host counts as shared even when this file lists nothing else on it.
    host = environment["ssh"].split("@", 1)[1]
    neighbours = [other.get("compose_project") for other_name, other in document["environments"].items()
                  if other_name != name and isinstance(other, dict) and isinstance(other.get("ssh"), str)
                  and other["ssh"].split("@", 1)[-1] == host]
    if single is not None:
        need(not neighbours, "single_stack cannot share its host with another environment; use protect_projects")
    else:
        need(protect is not None, "a shared host (no single_stack) needs protect_projects naming the other projects")
        missing = [str(project) for project in neighbours if project not in protect]
        need(not missing, f"protect_projects misses {', '.join(missing)} on the same host")
    # Directories the migration tools (backup-go-production.sh,
    # run-go-etl-container.sh) must never take as a target, besides the
    # root itself; [] when the host has none.
    paths = environment.get("protected_paths")
    need(isinstance(paths, list) and all(
        isinstance(path, str) and path.startswith("/") and WORD.fullmatch(path) and ".." not in path
        and ":" not in path for path in paths), "protected_paths must list absolute paths (may be empty)")
    if single is not None:
        need(isinstance(single, dict) and set(single) <= {"image_keep", "keep_source_snapshots", "allow_bootstrap"},
             "single_stack has unknown fields")
        need(type(single.get("image_keep", 1)) is int and 1 <= single.get("image_keep", 1) <= 9, "image_keep must be 1-9")
        need(type(single.get("keep_source_snapshots", 1)) is int and 1 <= single.get("keep_source_snapshots", 1) <= 9,
             "keep_source_snapshots must be 1-9")
        need(type(single.get("allow_bootstrap", False)) is bool, "allow_bootstrap must be boolean")
    backup = environment.get("backup")
    if backup is not None:
        need(isinstance(backup, dict) and set(backup) == {"scope", "keep"}, "backup needs only scope and keep")
        scope = backup.get("scope")
        need(scope in ("database", "full"), "backup scope must be database or full")
        if scope == "database":
            # Only the disposable test stack may skip the asset trees (#364).
            need(name == "dev-test", "backup scope database is only supported for the dev-test environment")
            need(environment.get("promote_from") is None,
                 "dev-test backup scope database cannot be combined with promote_from")
        else:
            # #66: a full backup keeps every tree; retention is allowed on both test stacks.
            need(name in ("dev-test", "formal-test"),
                 "backup retention is only supported for the dev-test and formal-test environments")
        need(type(backup.get("keep")) is int and 1 <= backup["keep"] <= 2, "backup keep must be 1-2")
    data = environment.get("data")
    if data is not None:
        # #28: only the disposable dev-test stack may run on throwaway data.
        need(name == "dev-test", "data is only supported for the dev-test environment")
        need(environment.get("promote_from") is None, "dev-test data mode cannot be combined with promote_from")
        need(data == "ephemeral", "data must be the string ephemeral")
        need(backup is None, "data ephemeral cannot be combined with backup (an ephemeral release takes no backup)")
    layout = environment.get("layout", {})
    need(isinstance(layout, dict) and set(layout) <= set(LAYOUT_KEYS), "layout has unknown keys")
    for key, value in layout.items():
        need(isinstance(value, str) and WORD.fullmatch(value) and ".." not in value, f"layout {key} is invalid")
    # The external MCP listener's host side (#332) is optional; when named it must be an
    # IPv4 address and a port, and not collide with the web port of the same stack.
    if "MCP_PUBLIC_BIND_IP" in layout:
        need(IPV4.fullmatch(layout["MCP_PUBLIC_BIND_IP"]), "layout MCP_PUBLIC_BIND_IP must be an IPv4 address")
    if "MCP_PUBLIC_HOST_PORT" in layout:
        need(PORT.fullmatch(layout["MCP_PUBLIC_HOST_PORT"]) and int(layout["MCP_PUBLIC_HOST_PORT"]) <= 65535,
             "layout MCP_PUBLIC_HOST_PORT must be a port number")
    if "MCP_PUBLIC_BIND_IP" in layout or "MCP_PUBLIC_HOST_PORT" in layout:
        web_ip, mcp_ip = layout.get("GO_WEB_BIND_IP", "0.0.0.0"), layout.get("MCP_PUBLIC_BIND_IP", "127.0.0.1")
        need(layout.get("GO_WEB_HOST_PORT", "13333") != layout.get("MCP_PUBLIC_HOST_PORT", "13343")
             or not (web_ip == mcp_ip or "0.0.0.0" in (web_ip, mcp_ip)),
             "layout MCP_PUBLIC_HOST_PORT must differ from GO_WEB_HOST_PORT on the same address")
    extra = environment.get("extra_compose_file")
    if extra is not None:
        need(isinstance(extra, str) and WORD.fullmatch(extra) and ".." not in extra
             and extra.startswith(environment["root"] + "/production/") and extra.endswith((".yml", ".yaml")),
             "extra_compose_file must be a .yml file under <root>/production/")
    build = environment.get("build", {})
    need(isinstance(build, dict), "build must be an object")
    # #257 keeps the repository free of internal sources: without npm_registry
    # the image build uses the public registry.npmjs.org. An internal stack may
    # name its own package cache here, only in its local environment file.
    need(set(build) <= {"network", "goproxy", "npm_registry", "add_hosts"}, "build has unknown fields")
    if "network" in build:
        need(isinstance(build["network"], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,62}", build["network"]),
             "build.network is invalid")
    if "goproxy" in build:
        need(isinstance(build["goproxy"], str) and URL_LIST.fullmatch(build["goproxy"]), "build.goproxy is invalid")
    if "npm_registry" in build:
        need(isinstance(build["npm_registry"], str) and URL.fullmatch(build["npm_registry"]),
             "build.npm_registry must be one http(s) registry URL")
    if "add_hosts" in build:
        need(isinstance(build["add_hosts"], list) and build["add_hosts"] and all(
            isinstance(entry, str) and ADD_HOST.fullmatch(entry) for entry in build["add_hosts"]),
             "build.add_hosts must list host:address entries")
    need(environment.get("source_transfer", "pipe") in ("pipe", "scp"), "source_transfer must be pipe or scp")
    source = environment.get("promote_from")
    if source is not None:
        need(source in document["environments"] and source != name, "promote_from must name another environment")
        need(document["environments"][source].get("promote_from") is None, "promotion chains are not supported")
        acceptance = document.get("acceptance")
        need(isinstance(acceptance, dict) and isinstance(acceptance.get("forge_repo"), str)
             and re.fullmatch(r"[A-Za-z0-9-]+/[A-Za-z0-9._-]+", acceptance["forge_repo"])
             and type(acceptance.get("issue")) is int and acceptance["issue"] > 0
             and isinstance(acceptance.get("forge_url"), str)
             and re.fullmatch(r"https://[A-Za-z0-9.:-]+(/[A-Za-z0-9._-]+)+", acceptance["forge_url"]),
             "promotion needs acceptance.forge_repo, acceptance.forge_url and acceptance.issue")


def protected_paths(environment):
    """ART_AI_PROTECTED_PATHS for this environment: its root, then protected_paths."""
    paths = [environment["root"]]
    for path in environment.get("protected_paths", []):
        if path.rstrip("/") not in (known.rstrip("/") for known in paths):
            paths.append(path)
    return ":".join(paths)


def container(environment):
    return f"{environment['compose_project']}-{SERVICE}-1"


def running_revision(environment):
    template = "{{index .Config.Labels \"org.opencontainers.image.revision\"}}"
    return run("ssh", environment["ssh"], f"docker inspect -f '{template}' {container(environment)}").strip()


def promotion_revision(document, name, revision, note_id):
    """Enforce: only a stably accepted source revision reaches this environment."""
    environment = document["environments"][name]
    source_name = environment["promote_from"]
    source = document["environments"][source_name]
    promotion = promotion_rules()
    running = running_revision(source)
    revision = revision or running
    try:
        promotion.validate_revision(revision)
    except promotion.ReleaseError as error:
        raise EnvironmentError_(str(error)) from None
    run("git", "fetch", "-q", "origin")
    try:
        run("git", "merge-base", "--is-ancestor", revision, "origin/main")
    except EnvironmentError_:
        raise EnvironmentError_(f"{revision} is not on origin/main") from None
    if not note_id:
        raise EnvironmentError_(f"{name} needs --acceptance-note: the {source_name} acceptance report for {revision}")
    acceptance = document["acceptance"]
    try:
        note = promotion.read_acceptance_comment(acceptance["forge_repo"], acceptance["issue"], note_id)
    except promotion.ReleaseError as error:
        raise EnvironmentError_(f"{source_name} acceptance refused: {error}") from None
    manifest = run("ssh", source["ssh"], f"cat {source['root']}/releases/manifest.txt")
    try:
        report = promotion.acceptance_from_note(note.get("body", ""), revision, acceptance["forge_url"])
        promotion.validate_manifest(manifest, revision, report["image_tag"])
    except promotion.ReleaseError as error:
        raise EnvironmentError_(f"{source_name} acceptance refused: {error}") from None
    # #411: the source environment's own smoke report is checked as well, so a note cannot claim
    # a layer the release script did not see pass. Older releases have no report: the note stays
    # the only evidence for them.
    report_path = f"{source['root']}/{promotion.smoke_report_path(revision)}"
    if host_file_state(source, report_path):
        try:
            promotion.smoke_checks_from_report(run("ssh", source["ssh"], f"cat {report_path}"), revision)
        except (EnvironmentError_, promotion.ReleaseError) as error:
            raise EnvironmentError_(f"{source_name} smoke report refused: {error}") from None
    else:
        print(f"[release-env] note: {source_name} has no machine smoke report for {revision}; "
              f"L0/L1/L1_agent rest on the acceptance note alone")
    if revision != running:
        print(f"[release-env] note: {source_name} now runs {running}; promoting accepted {revision}")
    return revision


# #676: the release string is a plain name; it rides in RELEASE_SERVER_ENV and in -ldflags.
RELEASE_VERSION_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}")


def release_version(requested, revision):
    """The release string to link into the build: the one asked for, or git describe of the revision."""
    if requested is not None:
        if not RELEASE_VERSION_NAME.fullmatch(requested):
            raise EnvironmentError_(f"--release-version {requested!r} may only hold letters, digits, dot, underscore "
                                    "and hyphen (at most 64), e.g. v1.9.5")
        return requested
    # Without a revision the release is the same origin/main release-go-testenv.sh resolves right
    # after its own fetch. A promoted revision may be missing from this clone too: fetch before
    # describing it, and say what to do instead of a bare git exit code.
    target = revision or "origin/main"
    try:
        run("git", "fetch", "-q", "origin")
    except EnvironmentError_:
        raise EnvironmentError_(f"git fetch origin failed, so the release string of {target} cannot be described; "
                                "check the network or pass --release-version") from None
    try:
        described = run("git", "describe", "--tags", "--always", f"{target}^{{commit}}").strip()
    except EnvironmentError_:
        raise EnvironmentError_(f"git describe cannot read {target} even after fetching origin (is it a commit of "
                                "this repository?); pass --release-version") from None
    if not RELEASE_VERSION_NAME.fullmatch(described):
        raise EnvironmentError_(f"git describe gave {described!r}, which is not a plain release name; pass --release-version")
    return described


def upstream_shape(environment, live_upstream=False, environ=None, live_chat=False):
    """The upstream shape of a release: fake for the ephemeral stack unless both live switches are set.

    --live-chat (#416) is the half-open mixed shape: the chat connections call the
    real gateway of the runtime environment while every generation Provider still
    points at the fake upstream, so a real model decides and generation stays free.
    It needs the same explicit acknowledgement as a fully live release.
    """
    environ = os.environ if environ is None else environ
    if environment.get("data") != "ephemeral":
        if live_upstream or live_chat:
            raise EnvironmentError_("--live-upstream and --live-chat only apply to the ephemeral dev-test stack; this environment is always live")
        return "live"
    if live_upstream and live_chat:
        raise EnvironmentError_("--live-upstream and --live-chat cannot be combined: --live-upstream releases every connection live, --live-chat only the chat ones")
    if not live_upstream and not live_chat:
        return "fake"
    if environ.get(LIVE_ACK) != "1":
        raise EnvironmentError_(f"a dev-test release with real chat or real upstreams calls paid services; set {LIVE_ACK}=1 together with "
                                f"{'--live-upstream' if live_upstream else '--live-chat'}")
    return "live" if live_upstream else "mixed"


def release_variables(environment, revision, shape="live", version=None):
    variables = {"RELEASE_HOST": environment["ssh"], "RELEASE_ROOT": environment["root"]}
    if revision:
        variables["RELEASE_REF"] = revision
    # The repository names no environment's Compose project (#261): always pass it.
    words = [f"RELEASE_COMPOSE_PROJECT={environment['compose_project']}"]
    single = environment.get("single_stack")
    if environment.get("protect_projects"):
        words.append(f"RELEASE_PROTECT_PROJECTS={','.join(environment['protect_projects'])}")
    if single is not None:
        words += ["RELEASE_SINGLE_STACK=1", f"RELEASE_IMAGE_KEEP={single.get('image_keep', 1)}"]
        if single.get("allow_bootstrap"):
            words.append("RELEASE_ALLOW_BOOTSTRAP=1")
        variables["RELEASE_KEEP_SOURCE_SNAPSHOTS"] = str(single.get("keep_source_snapshots", 1))
    backup = environment.get("backup")
    if backup is not None:
        words.append(f"RELEASE_BACKUP_SCOPE={backup['scope']}")
        words.append(f"RELEASE_BACKUP_KEEP={backup['keep']}")
    if environment.get("data") == "ephemeral":
        words.append("RELEASE_DATA_MODE=ephemeral")
        if shape in ("fake", "mixed"):
            words.append(f"RELEASE_UPSTREAM_SHAPE={shape}")
    words += [f"{key}={environment['layout'][key]}" for key in LAYOUT_KEYS if key in environment.get("layout", {})]
    if environment.get("extra_compose_file"):
        words.append(f"RELEASE_EXTRA_COMPOSE_FILE={environment['extra_compose_file']}")
    build = environment.get("build", {})
    for field, key in (("network", "RELEASE_BUILD_NETWORK"), ("goproxy", "RELEASE_GOPROXY"),
                       ("npm_registry", "RELEASE_NPM_REGISTRY")):
        if field in build:
            words.append(f"{key}={build[field]}")
    if "add_hosts" in build:
        words.append(f"RELEASE_BUILD_ADD_HOSTS={','.join(build['add_hosts'])}")
    # The migration tools refuse to run without it (#334); the server steps
    # record it in production/compose.env for manual runs on that host.
    words.append(f"ART_AI_PROTECTED_PATHS={protected_paths(environment)}")
    if version:
        words.append(f"RELEASE_VERSION={version}")
    variables["RELEASE_SERVER_ENV"] = " ".join(words)
    if environment.get("source_transfer", "pipe") != "pipe":
        variables["RELEASE_SOURCE_TRANSFER"] = environment["source_transfer"]
    return variables


def release(document, name, args):
    environment = document["environments"][name]
    revision = None
    if environment.get("promote_from"):
        revision = promotion_revision(document, name, args.revision, args.acceptance_note)
    elif args.revision or args.acceptance_note:
        raise EnvironmentError_(f"{name} always releases the pushed origin/main; --revision is for promotions")
    shape = upstream_shape(environment, live_upstream=getattr(args, "live_upstream", False) is True,
                           live_chat=getattr(args, "live_chat", False) is True)
    requested = getattr(args, "release_version", None)
    version = release_version(requested if isinstance(requested, str) else None, revision)
    print(f"[release-env] {name}: {environment['base_url']}" + (f" revision={revision}" if revision else " origin/main")
          + f" upstream={shape} release={version}")
    environment_variables = {**os.environ, **release_variables(environment, revision, shape, version)}
    code = subprocess.run(["bash", str(SCRIPTS / "release-go-testenv.sh")], env=environment_variables).returncode
    if code:
        return code
    code = ensure_agent_account(document, name)
    if code:
        return code
    return post_release_smoke(document, name, shape)


# Reads the asked KEY=value lines of one env file on the host as JSON; a missing file yields {}.
HOST_ENV_READER = ("import json,os,sys\nvalues={}\npath,keys=sys.argv[1],set(sys.argv[2:])\n"
                   "if os.path.exists(path):\n for line in open(path):\n  k,s,v=line.rstrip('\\n').partition('=')\n"
                   "  if s and k in keys:\n   values[k]=v[1:-1] if len(v)>1 and v[0]==v[-1] and v[0] in \"'\\\"\" else v\n"
                   "print(json.dumps(values))\n")
ADMIN_KEYS = ("ADMIN_USERNAME", "ADMIN_PASSWORD")
AGENT_KEYS = ("AGENT_TEST_USERNAME", "AGENT_TEST_PASSWORD")
AGENT_ACCOUNT_SCRIPT = SCRIPTS / "smoke" / "ensure-agent-account.sh"


def host_env(environment, filename, keys):
    command = f"python3 - {environment['root']}/production/{filename} {' '.join(keys)}"
    return json.loads(run("ssh", environment["ssh"], command, input=HOST_ENV_READER.encode()))


def admin_credentials(environment):
    values = host_env(environment, "runtime.env", ADMIN_KEYS)
    if not all(values.get(key) for key in ADMIN_KEYS):
        raise EnvironmentError_("the environment runtime.env has no admin login for smoke runs")
    return values


def agent_credentials(environment):
    """The Agent's own account (#215), kept in the untracked production/agent-test.env of the release root."""
    values = host_env(environment, "agent-test.env", AGENT_KEYS)
    return values if all(values.get(key) for key in AGENT_KEYS) else {}


def ensure_agent_account(document, name):
    """After a successful release, make sure the Agent's account exists and logs in (#215).

    dev-test starts from empty data on every release and gets it recreated; a kept database
    (formal-test) answers 409 and is only checked. The release itself is already deployed
    when this runs, so a failure here says so instead of looking like a refused release.
    """
    environment = document["environments"][name]
    try:
        if not agent_credentials(environment):
            print(f"[release-env] {name}: no production/agent-test.env on the host; the Agent account was not checked")
            return 0
        print(f"[release-env] {name}: ensuring the Agent account from production/agent-test.env")
        code = smoke(document, name, ["bash", str(AGENT_ACCOUNT_SCRIPT)])
    except EnvironmentError_ as error:
        print(f"[release-env] {name}: the release is deployed, but the Agent account step failed: {error}", file=sys.stderr)
        return 1
    if code:
        print(f"[release-env] {name}: the release is deployed, but the Agent account step failed (exit {code})", file=sys.stderr)
    return code


def running_upstream_shape(environment):
    """The shape the environment was last released in, from its production/compose.env.

    Only the ephemeral stack can run fake or mixed; anything unreadable counts as
    live, so a script that needs the double live switch refuses instead of guessing.
    """
    if environment.get("data") != "ephemeral":
        return "live"
    try:
        values = host_env(environment, "compose.env", ("RELEASE_UPSTREAM_SHAPE",))
    except (EnvironmentError_, ValueError):
        return "live"
    shape = values.get("RELEASE_UPSTREAM_SHAPE")
    return shape if shape in ("fake", "mixed") else "live"


def smoke_variables(document, name):
    """BASE, credentials, EXPECT_SHA and the shape for a smoke run against this environment."""
    environment = document["environments"][name]
    return {**os.environ, **admin_credentials(environment), **agent_credentials(environment),
            "BASE": environment["base_url"],
            "EXPECT_SHA": running_revision(environment), "RELEASE_CONTAINER": container(environment),
            "RELEASE_ENV": name, "ART_AI_UPSTREAM_SHAPE": running_upstream_shape(environment)}


def smoke(document, name, command):
    environment = document["environments"][name]
    if not command:
        raise EnvironmentError_("smoke needs a command after --")
    return subprocess.run(command, env=smoke_variables(document, name)).returncode


# #411: the layers a release runs by itself, each script printing its own PASS/FAIL/SKIP counters.
SMOKE_LAYERS = (
    ("L0", "smoke-l0-browser-boot.sh"),
    ("L1", "smoke-l1-free.sh"),
    ("L1_agent", "smoke-l1-agent-mode.sh"),
)
# The last "PASS=<n> FAIL=<n>[ SKIP=<n>]" line a script prints is its summary.
SMOKE_SUMMARY = re.compile(r"PASS=(\d+) FAIL=(\d+)(?:\s+SKIP=(\d+))?")
SMOKE_SOFT_FAIL = re.compile(r"^SOFT-FAIL", re.M)
SMOKE_TIMEOUT = 1800


def utc_now():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def smoke_layer(script, layer, variables):
    """Run one smoke layer, keep its output and read its own counters.

    A layer passes only when it exits 0 and its summary shows no failure, no skip, no soft-fail
    and at least one pass. No summary at all counts as a failure: the script never got far
    enough to report, and a missing report must not look like a pass.
    """
    started = utc_now()
    try:
        result = subprocess.run(["bash", str(SCRIPTS / "smoke" / script)], env=variables,
                                capture_output=True, timeout=SMOKE_TIMEOUT)
        output = (result.stdout + result.stderr).decode("utf-8", "replace")
        code = result.returncode
    except subprocess.TimeoutExpired as error:
        output = ((error.stdout or b"") + (error.stderr or b"")).decode("utf-8", "replace")
        output += f"\nFAIL: {layer} timed out after {SMOKE_TIMEOUT} seconds"
        code = 1
    except OSError:
        output, code = f"FAIL: {script} could not be started", 1
    summary = None
    for match in SMOKE_SUMMARY.finditer(output):
        summary = match
    passed = failed = skipped = 0
    if summary:
        passed, failed, skipped = int(summary.group(1)), int(summary.group(2)), int(summary.group(3) or 0)
    soft_failed = len(SMOKE_SOFT_FAIL.findall(output))
    check = {"status": "pass" if code == 0 and passed > 0 and not failed and not skipped and not soft_failed
             else "fail",
             "passed": passed, "failed": failed, "skipped": skipped, "soft_failed": soft_failed,
             "script": script, "started_at": started, "finished_at": utc_now()}
    return check, output


def smoke_report(document, name, revision):
    """Run L0, L1 and L1 Agent and write the machine report under the release root.

    One JSON holds a check per layer (its counters, its status and its log path) plus the
    revision and the run times; one log per layer sits next to it. The report is untracked
    evidence: it is written from the scripts' own counters, not from a claim about them.
    """
    environment = document["environments"][name]
    rules = promotion_rules()
    relative = rules.smoke_report_path(revision)
    report_file = Path(relative)
    directory = str(report_file.parent / report_file.stem)
    variables = smoke_variables(document, name)
    checks = {}
    for layer, script in SMOKE_LAYERS:
        check, output = smoke_layer(script, layer, variables)
        check["log"] = f"{environment['root']}/{directory}/{layer}.log"
        checks[layer] = check
        write_host_file(environment, check["log"], output, directory_mode="700")
        print(f"[release-env] {name}: smoke {layer} {check['status']} "
              f"(passed={check['passed']} failed={check['failed']} skipped={check['skipped']} "
              f"soft_failed={check['soft_failed']})")
    report = {"schema": rules.SMOKE_SCHEMA, "version": rules.SMOKE_VERSION, "environment": name,
              "revision": revision, "upstream_shape": variables["ART_AI_UPSTREAM_SHAPE"],
              "started_at": utc_now(), "finished_at": utc_now(), "checks": checks}
    write_host_file(environment, f"{environment['root']}/{relative}",
                    json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    return report, f"{environment['root']}/{relative}"


def post_release_smoke(document, name, shape):
    """After a successful release, run the release smoke layers and leave the report.

    Only the fake shape runs them by itself: on a live stack L1 reaches a real chat model, which
    needs the user's consent for every run (AGENTS.md "上游接口与 Fake"), so those releases keep
    the manual protocol of docs/deployment.md §6. The release itself stays deployed either way.
    """
    environment = document["environments"][name]
    if shape != "fake":
        print(f"[release-env] {name}: {shape} upstream shape; L0/L1/L1 Agent are not run "
              f"automatically, follow docs/deployment.md §6")
        return 0
    rules = promotion_rules()
    try:
        revision = running_revision(environment)
        report, path = smoke_report(document, name, revision)
    except (EnvironmentError_, rules.ReleaseError) as error:
        print(f"[release-env] {name}: the release is deployed, but the post-release smoke "
              f"produced no report: {error}", file=sys.stderr)
        return 1
    print(f"[release-env] {name}: post-release smoke report {path}")
    failed = [layer for layer, check in report["checks"].items() if check["status"] != "pass"]
    if failed:
        print(f"[release-env] {name}: the release is deployed, but the post-release smoke failed: "
              f"{', '.join(failed)}", file=sys.stderr)
        return 1
    return 0


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--file", help="environments file (default: ART_AI_ENVIRONMENTS_FILE or the local deployment folder)")
    sub = parser.add_subparsers(dest="mode", required=True)
    sub.add_parser("list")
    for mode in ("show", "compose-env"):
        sub.add_parser(mode).add_argument("environment")
    entry = sub.add_parser("release")
    entry.add_argument("environment")
    entry.add_argument("--revision")
    entry.add_argument("--acceptance-note", type=int)
    entry.add_argument("--release-version",
                       help="release string shown next to the brand (#676), e.g. v1.9.5 for the formal-test release that "
                            "will be tagged; default: git describe --tags --always of the released revision")
    entry.add_argument("--live-upstream", action="store_true",
                       help=f"release the ephemeral dev-test stack against real upstreams (needs {LIVE_ACK}=1)")
    entry.add_argument("--live-chat", action="store_true",
                       help=f"release the ephemeral dev-test stack with a real chat gateway and fake generation (#416, needs {LIVE_ACK}=1)")
    smoke_parser = sub.add_parser("smoke")
    smoke_parser.add_argument("environment")
    smoke_parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    try:
        document = load_environments(load_helper("local_environments.py", "local_environments").environments_file(args.file))
        if args.mode == "list":
            for name, environment in document["environments"].items():
                source = environment.get("promote_from")
                print(f"{name}\t{environment['base_url']}" + (f"\tpromoted from {source}" if source else ""))
            return 0
        if args.environment not in document["environments"]:
            raise EnvironmentError_(f"unknown environment {args.environment}")
        environment = document["environments"][args.environment]
        if args.mode == "show":
            print(json.dumps({"name": args.environment, **environment, "container": container(environment)},
                             ensure_ascii=False, indent=2))
            return 0
        if args.mode == "compose-env":
            for key in LAYOUT_KEYS:
                if key in environment.get("layout", {}):
                    print(f"{key}={environment['layout'][key]}")
            print(f"GO_RUNTIME_ENV_FILE={environment['root']}/production/runtime.env")
            print(f"ART_AI_PROTECTED_PATHS={protected_paths(environment)}")
            # Data directories derive from the root when the layout leaves them out.
            for key, name in (("GO_PROD_DB_DIR", "data"), ("GO_PROD_STORAGE_DIR", "storage-go"),
                              ("GO_PROD_WEB_DATA_DIR", "web-data"), ("GO_PROD_LOG_DIR", "logs")):
                if key not in environment.get("layout", {}):
                    print(f"{key}={environment['root']}/production/{name}")
            return 0
        if args.mode == "release":
            return release(document, args.environment, args)
        command = args.command[1:] if args.command[:1] == ["--"] else args.command
        return smoke(document, args.environment, command)
    except EnvironmentError_ as error:
        print(f"[release-env] refused: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
