#!/usr/bin/env python3
"""Release Plinth to a named environment from the untracked local deployment folder.

The environments file is local (gitignored, deploy/local/environments.json; see
scripts/local_environments.py for the lookup, including the main-checkout
fallback for worktrees): it holds hosts, roots, ports and Compose names, never
secrets. deploy/environments.example.json is its template.

  release-env.py list
  release-env.py show <env>
  release-env.py release <env> [--revision SHA] [--acceptance-note ID]
  release-env.py smoke <env> -- <command...>      # BASE, ADMIN_*, EXPECT_SHA, RELEASE_ENV set
  release-env.py compose-env <env>                # GO_* layout for manual compose and
                                                  # ART_AI_PROTECTED_PATHS for the migration tools

An environment with "promote_from" only receives an exact origin/main revision
that the source environment released successfully (its manifest) AND that has a
passing acceptance report (L0, L1, L1_agent, related) in a native Issue note.
The release itself is scripts/release-go-testenv.sh with the environment's
parameters; nothing about the server flow is duplicated here.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys

SCRIPTS = Path(__file__).resolve().parent
SERVICE = "art-ai-go"
LAYOUT_KEYS = (
    "GO_WEB_BIND_IP", "GO_WEB_HOST_PORT", "GO_PROD_DB_DIR", "GO_PROD_STORAGE_DIR",
    "GO_PROD_WEB_DATA_DIR", "GO_PROD_LOG_DIR", "GO_LEGACY_GENERATED_IMAGES_DIR",
    "GO_LEGACY_ASSET_CACHE_DIR", "GO_LEGACY_THUMBNAIL_CACHE_DIR", "GO_LEGACY_INPUT_ASSETS_DIR",
)
WORD = re.compile(r"[A-Za-z0-9_./:-]+")
ADD_HOST = re.compile(r"[A-Za-z0-9][A-Za-z0-9.-]{0,252}:[0-9A-Fa-f.:]+")
COMPOSE_PROJECT = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
URL_LIST = re.compile(r"https?://[A-Za-z0-9.:/_-]+(,(https?://[A-Za-z0-9.:/_-]+|direct|off))*")


class EnvironmentError_(Exception):
    """Fixed, credential-free diagnostics only."""


def load_helper(filename, name):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run(*args, input=None, timeout=120):
    try:
        result = subprocess.run([str(a) for a in args], input=input, capture_output=True,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise EnvironmentError_(f"{Path(str(args[0])).name} was unavailable or timed out") from None
    if result.returncode:
        raise EnvironmentError_(f"{Path(str(args[0])).name} failed (exit {result.returncode})")
    return result.stdout.decode("utf-8")


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
    if environment.get("managed_by") is None:
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
        # Only the disposable test stack may skip the asset trees (#364).
        need(name == "dev-test", "backup retention is only supported for the dev-test environment")
        need(environment.get("promote_from") is None and environment.get("managed_by") is None,
             "dev-test backup retention cannot be combined with promote_from or managed_by")
        need(isinstance(backup, dict) and set(backup) == {"scope", "keep"}, "backup needs only scope and keep")
        need(backup.get("scope") == "database", "backup scope must be database")
        need(type(backup.get("keep")) is int and 1 <= backup["keep"] <= 2, "backup keep must be 1-2")
    layout = environment.get("layout", {})
    need(isinstance(layout, dict) and set(layout) <= set(LAYOUT_KEYS), "layout has unknown keys")
    for key, value in layout.items():
        need(isinstance(value, str) and WORD.fullmatch(value) and ".." not in value, f"layout {key} is invalid")
    extra = environment.get("extra_compose_file")
    if extra is not None:
        need(isinstance(extra, str) and WORD.fullmatch(extra) and ".." not in extra
             and extra.startswith(environment["root"] + "/production/") and extra.endswith((".yml", ".yaml")),
             "extra_compose_file must be a .yml file under <root>/production/")
    build = environment.get("build", {})
    need(isinstance(build, dict), "build must be an object")
    # #257: image builds always use the public registry.npmjs.org. A leftover
    # field would silently mislead, so refuse it with a fixable message.
    need("npm_registry" not in build,
         "build.npm_registry was removed (#257); image builds use the public registry.npmjs.org, "
         "so delete this field from the local environment file")
    need(set(build) <= {"network", "goproxy", "add_hosts"}, "build has unknown fields")
    if "network" in build:
        need(isinstance(build["network"], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,62}", build["network"]),
             "build.network is invalid")
    if "goproxy" in build:
        need(isinstance(build["goproxy"], str) and URL_LIST.fullmatch(build["goproxy"]), "build.goproxy is invalid")
    if "add_hosts" in build:
        need(isinstance(build["add_hosts"], list) and build["add_hosts"] and all(
            isinstance(entry, str) and ADD_HOST.fullmatch(entry) for entry in build["add_hosts"]),
             "build.add_hosts must list host:address entries")
    need(environment.get("source_transfer", "pipe") in ("pipe", "scp"), "source_transfer must be pipe or scp")
    need(environment.get("managed_by") in (None, "release-go-formal.sh"),
         "managed_by may only name scripts/release-go-formal.sh")
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
    formal = load_helper("release-formal.py", "release_formal_rules")
    running = running_revision(source)
    revision = revision or running
    try:
        formal.validate_revision(revision)
    except formal.ReleaseError as error:
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
        note = formal.read_acceptance_comment(acceptance["forge_repo"], acceptance["issue"], note_id)
    except formal.ReleaseError as error:
        raise EnvironmentError_(f"{source_name} acceptance refused: {error}") from None
    manifest = run("ssh", source["ssh"], f"cat {source['root']}/releases/manifest.txt")
    try:
        report = formal.acceptance_from_note(note.get("body", ""), revision, acceptance["forge_url"])
        formal.validate_manifest(manifest, revision, report["image_tag"])
    except formal.ReleaseError as error:
        raise EnvironmentError_(f"{source_name} acceptance refused: {error}") from None
    if revision != running:
        print(f"[release-env] note: {source_name} now runs {running}; promoting accepted {revision}")
    return revision


def release_variables(environment, revision):
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
    words += [f"{key}={environment['layout'][key]}" for key in LAYOUT_KEYS if key in environment.get("layout", {})]
    if environment.get("extra_compose_file"):
        words.append(f"RELEASE_EXTRA_COMPOSE_FILE={environment['extra_compose_file']}")
    build = environment.get("build", {})
    for field, key in (("network", "RELEASE_BUILD_NETWORK"), ("goproxy", "RELEASE_GOPROXY")):
        if field in build:
            words.append(f"{key}={build[field]}")
    if "add_hosts" in build:
        words.append(f"RELEASE_BUILD_ADD_HOSTS={','.join(build['add_hosts'])}")
    # The migration tools refuse to run without it (#334); the server steps
    # record it in production/compose.env for manual runs on that host.
    words.append(f"ART_AI_PROTECTED_PATHS={protected_paths(environment)}")
    variables["RELEASE_SERVER_ENV"] = " ".join(words)
    if environment.get("source_transfer", "pipe") != "pipe":
        variables["RELEASE_SOURCE_TRANSFER"] = environment["source_transfer"]
    return variables


def release(document, name, args):
    environment = document["environments"][name]
    if environment.get("managed_by"):
        raise EnvironmentError_(f"{name} is released by scripts/{environment['managed_by']}, not release-env.py")
    revision = None
    if environment.get("promote_from"):
        revision = promotion_revision(document, name, args.revision, args.acceptance_note)
    elif args.revision or args.acceptance_note:
        raise EnvironmentError_(f"{name} always releases the pushed origin/main; --revision is for promotions")
    print(f"[release-env] {name}: {environment['base_url']}" + (f" revision={revision}" if revision else " origin/main"))
    environment_variables = {**os.environ, **release_variables(environment, revision)}
    return subprocess.run(["bash", str(SCRIPTS / "release-go-testenv.sh")], env=environment_variables).returncode


def admin_credentials(environment):
    reader = ("import json,sys\nvalues={}\nfor line in open(sys.argv[1]):\n k,s,v=line.rstrip('\\n').partition('=')\n"
              " if s and k in ('ADMIN_USERNAME','ADMIN_PASSWORD'):\n  values[k]=v[1:-1] if len(v)>1 and v[0]==v[-1] and v[0] in \"'\\\"\" else v\n"
              "print(json.dumps(values))\n")
    output = run("ssh", environment["ssh"], f"python3 - {environment['root']}/production/runtime.env", input=reader.encode())
    values = json.loads(output)
    if not values.get("ADMIN_USERNAME") or not values.get("ADMIN_PASSWORD"):
        raise EnvironmentError_("the environment runtime.env has no admin login for smoke runs")
    return values


def smoke(document, name, command):
    environment = document["environments"][name]
    if not command:
        raise EnvironmentError_("smoke needs a command after --")
    variables = {**os.environ, **admin_credentials(environment), "BASE": environment["base_url"],
                 "EXPECT_SHA": running_revision(environment), "RELEASE_CONTAINER": container(environment),
                 "RELEASE_ENV": name}
    return subprocess.run(command, env=variables).returncode


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
