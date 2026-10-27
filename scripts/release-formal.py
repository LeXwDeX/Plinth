#!/usr/bin/env python3
"""Promote an accepted test image; never build, import data, or configure a Provider.

Local: release-go-formal.sh FULL_SHA --acceptance-note ID --credentials-file PATH
The native acceptance comment (a GitHub Issue comment on acceptance.legacy_formal_issue) must
contain one JSON fenced formal_promotion report.
Server mode is an internal entry point executed from that exact revision archive.

The host, root and Compose project come from the "legacy-formal" entry of the untracked local
deployment folder (scripts/local_environments.py; or ART_AI_ENVIRONMENTS_FILE), the test stack's
Compose project from its "dev-test" entry, and the project forge from acceptance.forge_url and
acceptance.forge_repo; the repository names no host, path or environment project (#249, #261).
"""
import argparse
import contextlib
import hashlib
import http.cookiejar
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = None
HOST = None
LEGACY_ENVIRONMENT = "legacy-formal"
TEST_ENVIRONMENT = "dev-test"
PROJECT = None
CONTAINER = None
TEST_PROJECT = None
TEST_CONTAINER = None
FORGE_REPO = None
LEGACY_ISSUE = None
FORGE_REPO_NAME = re.compile(r"[A-Za-z0-9-]+/[A-Za-z0-9._-]+")
COMPOSE_PROJECT = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
REPOSITORY = "art-ai-go-production"
COOKIE = "art_ai_formal_session"
FORGE = None
FORGE_URL = re.compile(r"https://[A-Za-z0-9.:-]+(/[A-Za-z0-9._-]+)+")
DATA_DIRS = ("data", "storage-go", "web-data")


class ReleaseError(Exception):
    """Only fixed, credential-free diagnostics belong in this exception."""


def validate_revision(revision):
    if not isinstance(revision, str) or not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ReleaseError("an exact lowercase 40-character revision is required")
    return revision


def validate_tag(tag):
    if not isinstance(tag, str) or not re.fullmatch(r"[0-9a-f]{7,40}-[0-9]{8}T[0-9]{6}Z", tag):
        raise ReleaseError("invalid test image tag")
    return tag


def configure(root, host=None, forge=None, project=None, test_project=None):
    """Point this run at one deployment; every value comes from outside the repository."""
    global ROOT, HOST, FORGE, PROJECT, CONTAINER, TEST_PROJECT, TEST_CONTAINER
    if not isinstance(root, str) or not root.startswith("/") or ".." in root or not re.fullmatch(r"[A-Za-z0-9_./-]+", root):
        raise ReleaseError("the deployment root must be an absolute path")
    if host is not None and not re.fullmatch(r"[a-z_][a-z0-9_-]*@[A-Za-z0-9.-]+", host):
        raise ReleaseError("the deployment ssh target must be user@host")
    if forge is not None and not FORGE_URL.fullmatch(forge):
        raise ReleaseError("the project forge URL must be an https project URL")
    for name in (project, test_project):
        if name is not None and (not isinstance(name, str) or not COMPOSE_PROJECT.fullmatch(name)):
            raise ReleaseError("the Compose project names must be valid")
    if project is not None and project == test_project:
        raise ReleaseError("the formal and test stacks need different Compose projects")
    ROOT = Path(root)
    HOST = host
    FORGE = forge
    if project is not None:
        PROJECT, CONTAINER = project, project + "-art-ai-go-1"
    if test_project is not None:
        TEST_PROJECT, TEST_CONTAINER = test_project, test_project + "-art-ai-go-1"


def configure_from_environments(path=None):
    global FORGE_REPO, LEGACY_ISSUE
    path = load_helper("local_environments.py", "formal_local_environments").environments_file(path)
    try:
        document = json.loads(path.read_text())
        environment = document["environments"][LEGACY_ENVIRONMENT]
        acceptance = document["acceptance"]
        forge_repo, issue = acceptance["forge_repo"], acceptance["legacy_formal_issue"]
        if not isinstance(forge_repo, str) or not FORGE_REPO_NAME.fullmatch(forge_repo):
            raise ValueError("forge repo")
        if type(issue) is not int or issue < 1:
            raise ValueError("legacy formal issue")
        configure(environment["root"], environment["ssh"], acceptance["forge_url"],
                  environment["compose_project"], document["environments"][TEST_ENVIRONMENT]["compose_project"])
        FORGE_REPO, LEGACY_ISSUE = forge_repo, issue
    except (OSError, ValueError, KeyError, TypeError):
        raise ReleaseError(f"{LEGACY_ENVIRONMENT}, {TEST_ENVIRONMENT} and acceptance.forge_url/forge_repo/"
                           "legacy_formal_issue must be set in the environments file") from None


def read_acceptance_comment(repo, issue, comment_id):
    """Read one GitHub Issue comment; a comment that belongs to another Issue is refused."""
    comment = json.loads(run("gh", "api", f"repos/{repo}/issues/comments/{comment_id}"))
    if not str(comment.get("issue_url", "")).lower().endswith(f"/repos/{repo}/issues/{issue}".lower()):
        raise ReleaseError("the acceptance comment belongs to another Issue")
    return comment


def require_projects():
    if PROJECT is None or TEST_PROJECT is None:
        raise ReleaseError("the formal and test Compose projects must be configured")


def acceptance_from_note(body, revision, forge=None):
    validate_revision(revision)
    forge = forge or FORGE
    if not forge:
        raise ReleaseError("the project forge URL is not configured")
    candidates = []
    for block in re.findall(r"```json\s*\n(.*?)\n```", body, re.S):
        try:
            report = json.loads(block)
        except ValueError:
            raise ReleaseError("native acceptance JSON is invalid") from None
        if isinstance(report, dict) and "formal_promotion" in report:
            candidates.append(report)
    if len(candidates) != 1:
        raise ReleaseError("one native formal_promotion acceptance report is required")
    report = candidates[0]
    if type(report.get("formal_promotion")) is not int or report["formal_promotion"] != 1 or report.get("revision") != revision:
        raise ReleaseError("acceptance revision does not match the promotion")
    validate_tag(report.get("image_tag"))
    checks = report.get("checks", {})
    if not isinstance(checks, dict): raise ReleaseError("acceptance checks must be an object")
    for name in ("L0", "L1", "L1_agent", "related"):
        check = checks.get(name, {})
        if not isinstance(check, dict): raise ReleaseError("acceptance check must be an object")
        if (check.get("status") != "pass" or type(check.get("failed")) is not int
                or check["failed"] != 0 or type(check.get("passed")) is not int or check["passed"] < 1
                or check.get("skipped", 0) != 0 or check.get("soft_failed", 0) != 0):
            raise ReleaseError("test acceptance is missing, failed, skipped, or soft-failed")
        evidence = check.get("evidence", "")
        if not isinstance(evidence, str) or not evidence.startswith(forge + "/"):
            raise ReleaseError("native project evidence is required for every acceptance check")
    return report


def validate_manifest(text, revision, tag):
    validate_revision(revision)
    validate_tag(tag)
    if not any(len(parts := line.split()) == 3 and parts[:2] == [tag, revision]
               and re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", parts[2]) for line in text.splitlines()):
        raise ReleaseError("the exact image/revision is absent from the successful test release manifest")


def validate_environment(env):
    allowed = {"ADMIN_USERNAME", "ADMIN_PASSWORD", "ADMIN_DISPLAY_NAME", "ADMIN_EMAIL",
               "SESSION_SECRET", "SESSION_COOKIE_NAME"}
    if not isinstance(env, dict) or set(env) - allowed:
        raise ReleaseError("formal environment contains an unapproved setting")
    if (len(env.get("SESSION_SECRET", "")) < 32 or env.get("SESSION_COOKIE_NAME") != COOKIE
            or not re.fullmatch(r"[a-zA-Z0-9_]{3,64}", env.get("ADMIN_USERNAME", ""))):
        raise ReleaseError("formal authentication settings are invalid")
    if any(not isinstance(v, str) or any(c in v for c in "\n\r\0") for v in env.values()):
        raise ReleaseError("formal environment has an invalid value")


def promote(ops):
    previous, backup, stopped, activated = None, None, False, False
    stage = "prepare"
    try:
        previous = ops.prepare()
        stage = "preflight"
        print("[formal] preflight", flush=True)
        ops.preflight()
        if previous:
            stage, stopped = "stop", True
            print("[formal] stopping formal service for consistent backup", flush=True)
            ops.stop(previous)
            stage = "backup"
            backup = ops.backup(previous)
        stage, activated = "activate", True
        print("[formal] activating accepted image", flush=True)
        ops.activate()
        stage = "verify"
        ops.verify()
        stage = "finish_bootstrap"
        print("[formal] verifying bootstrap credentials are removed", flush=True)
        ops.finish_bootstrap()
        stage = "verify_test_unchanged"
        ops.verify_test_unchanged()
        stage = "publish"
        print("[formal] recording verified promotion", flush=True)
        return ops.publish(previous, backup)
    except BaseException:
        print("[formal] failed stage=" + stage, flush=True)
        recovered = False
        if stopped or activated:
            try:
                ops.recover(previous, backup, activated)
                recovered = True
            except BaseException:
                ops.failure(stage, False)
                raise ReleaseError("formal recovery failed; preserve evidence and stop") from None
        ops.failure(stage, recovered)
        raise


def run(*args, input=None, env=None, timeout=120):
    try:
        result = subprocess.run([str(arg) for arg in args], input=input, capture_output=True,
                                env=env, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise ReleaseError("a release command was unavailable or timed out") from None
    if result.returncode:
        # Never print compose/inspect output, argv, env, raw HTTP, or credentials.
        raise ReleaseError("release command failed: " + str(args[0]).split("/")[-1])
    return result.stdout.decode("utf-8")


def load_helper(filename, name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def atomic_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as output:
            json.dump(value, output, ensure_ascii=False, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)


def http_json(base, path, client=None, method="GET", payload=None):
    request = urllib.request.Request(base + path, method=method,
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={"Content-Type": "application/json", "Origin": base})
    try:
        response = client.open(request, timeout=8) if client else urllib.request.urlopen(request, timeout=8)
        with response:
            return json.load(response)
    except (OSError, ValueError, urllib.error.HTTPError):
        raise ReleaseError("formal HTTP verification failed") from None


def healthy(base):
    try:
        return all(http_json(base, path).get("status") == "ok" for path in ("/healthz", "/readyz"))
    except ReleaseError:
        return False


def wait_healthy(base):
    for _ in range(40):
        if healthy(base): return
        time.sleep(2)
    raise ReleaseError("formal service failed health/readiness verification")


def inspect(kind, reference):
    rows = json.loads(run("docker", "inspect", "--type=" + kind, reference))
    if not isinstance(rows, list) or len(rows) != 1:
        raise ReleaseError("Docker identity is ambiguous")
    return rows[0]


def load_environment(path):
    if path.is_symlink() or not path.is_file() or stat.S_IMODE(path.stat().st_mode) != 0o600:
        raise ReleaseError("formal runtime environment must be a private regular file")
    env = {}
    for line in path.read_text().splitlines():
        if not line or line.startswith("#"): continue
        key, separator, value = line.partition("=")
        if not separator or key in env: raise ReleaseError("formal environment syntax is invalid")
        env[key] = value
    validate_environment(env)
    return env


def write_environment(path, env):
    validate_environment(env)
    fd, temporary = tempfile.mkstemp(prefix=".runtime.", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as output:
            output.write("".join(f"{k}={v}\n" for k, v in env.items()))
            output.flush(); os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)


def port_unused(port):
    with socket.socket() as probe:
        try: probe.bind(("0.0.0.0", port))
        except OSError: raise ReleaseError("formal target or preflight port is occupied") from None


def credential_file(path):
    path = path.expanduser().absolute()
    if not path.exists():
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        # Exclusive creation also prevents replacing an existing user's secret.
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump({"username": "formal_admin", "password": secrets.token_urlsafe(36),
                       "session_secret": secrets.token_urlsafe(48)}, output)
    if path.is_symlink() or not path.is_file() or stat.S_IMODE(path.stat().st_mode) != 0o600:
        raise ReleaseError("admin credential file must be a private regular file (0600)")
    credentials = json.loads(path.read_text())
    if (set(credentials) != {"username", "password", "session_secret"}
            or not re.fullmatch(r"[a-zA-Z0-9_]{3,64}", credentials.get("username", ""))
            or not re.fullmatch(r"[a-zA-Z0-9_-]{32,128}", credentials.get("password", ""))
            or not re.fullmatch(r"[a-zA-Z0-9_-]{32,128}", credentials.get("session_secret", ""))):
        raise ReleaseError("admin credential file has an invalid format")
    return credentials


class ServerOperations:
    def __init__(self, revision, payload):
        require_projects()
        self.revision = validate_revision(revision)
        self.payload = payload
        self.report = acceptance_from_note("```json\n" + json.dumps(payload["acceptance"]) + "\n```", revision)
        self.tag = self.report["image_tag"]
        self.image = REPOSITORY + ":" + self.tag
        self.formal = ROOT / "formal"
        self.runtime = self.formal / "runtime.env"
        self.state_path = self.formal / "reports/current.json"
        self.code = self.formal / "code" / revision
        self.release_id = self.tag + "-" + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
        self.retention = load_helper("retain-art-ai-images.py", "formal_retention")
        self.capacity = load_helper("release-capacity.py", "formal_capacity")
        self.credentials = payload["credentials"]
        self.previous = None
        self.image_id = None
        self.test_before = None

    def compose(self, code, tag, *arguments, preflight=None):
        root = preflight or self.formal
        environment = {**os.environ, "FORMAL_ROOT": str(root), "FORMAL_IMAGE_TAG": tag,
                       "FORMAL_RUNTIME_ENV": str(self.runtime),
                       "FORMAL_BIND_IP": "127.0.0.1" if preflight else "0.0.0.0",
                       "FORMAL_PORT": "13342" if preflight else "13334"}
        return run("docker", "compose", "--env-file", self.runtime,
                   "-p", PROJECT + ("_preflight" if preflight else ""),
                   "-f", Path(code) / "docker-compose.go-formal.yml", *arguments, env=environment)

    def test_snapshot(self):
        row = inspect("container", TEST_CONTAINER)
        labels = row["Config"].get("Labels", {})
        if (labels.get("com.docker.compose.project") != TEST_PROJECT
                or labels.get("com.docker.compose.service") != "art-ai-go"
                or not row["State"]["Running"] or row["State"].get("Health", {}).get("Status") != "healthy"
                or not healthy("http://127.0.0.1:13333")):
            raise ReleaseError("the test stack must be healthy before and after promotion")
        return {"id": row["Id"], "image_id": row["Image"], "started_at": row["State"]["StartedAt"],
                "revision": labels.get("org.opencontainers.image.revision"), "restarts": row["RestartCount"]}

    def check_capacity(self, backup=None):
        try:
            self._check_capacity(backup)
        except self.capacity.CapacityError:
            raise ReleaseError("formal capacity measurement failed or available space is insufficient") from None

    def _check_capacity(self, backup=None):
        cap = self.capacity.Capacity()
        cap.require(ROOT)
        cap.require(self.formal, 64 << 20, 256)  # preflight DB and release metadata
        docker_root = self.capacity.absolute(run("docker", "info", "--format", "{{.DockerRootDir}}").strip())
        if not docker_root.is_dir(): raise ReleaseError("Docker data filesystem is unavailable")
        cap.require(docker_root, 512 << 20, 4096)  # json-file logs and container metadata
        backup_root = self.formal / "backups"
        block = cap.devices[cap.filesystem(backup_root)]["block"]
        archive_bytes, entries = 0, 0
        for name in DATA_DIRS:
            path = self.formal / name
            if backup:
                size, count = self.capacity.archive_size(Path(backup["path"]) / (name + ".tar"), name, block)
            elif path.exists():
                size, count, _ = self.capacity.tree(path)
            else:
                size, count = 0, 1
            amount = self.capacity.add(size, self.capacity.multiply(count, max(16384, block * 2)), 65536)
            archive_bytes = self.capacity.add(archive_bytes, amount)
            entries = self.capacity.add(entries, count)
            # Reserve a second on-filesystem copy for restore, including bind mounts.
            cap.require(path, amount, count)
        cap.require(backup_root, archive_bytes * (1 if backup else 2), entries + 32)
        cap.verify()

    def validate_state(self, state):
        if state.get("version") != 1:
            raise ReleaseError("formal release state is unsupported")
        validate_revision(state.get("revision"))
        validate_tag(state.get("image_tag"))
        self.retention.image_id(state.get("image_id", ""))
        if state.get("code") != str(self.formal / "code" / state["revision"]):
            raise ReleaseError("formal code state is outside its isolated directory")
        if not (Path(state["code"]) / "docker-compose.go-formal.yml").is_file():
            raise ReleaseError("previous formal compose is unavailable")

    def check_formal_container(self, state, require_running=True):
        row = inspect("container", CONTAINER)
        labels = row["Config"].get("Labels", {})
        if (labels.get("com.docker.compose.project") != PROJECT
                or labels.get("com.docker.compose.service") != "art-ai-go"
                or labels.get("org.opencontainers.image.revision") != state["revision"]
                or row["Image"] != state["image_id"] or (require_running and not row["State"]["Running"])):
            raise ReleaseError("formal container differs from recorded identity")
        mounts = {mount["Destination"]: mount for mount in row["Mounts"] if mount["Type"] != "tmpfs"}
        expected = {"/app/data": "data", "/app/storage": "storage-go", "/app/web-data": "web-data", "/app/logs": "logs"}
        if set(mounts) != set(expected) or any(mounts[dest]["Type"] != "bind"
                or Path(mounts[dest]["Source"]) != self.formal / name for dest, name in expected.items()):
            raise ReleaseError("formal mounts differ from isolated data paths")
        if not row["HostConfig"].get("ReadonlyRootfs"):
            raise ReleaseError("formal runtime filesystem is not read-only")
        return row

    def prepare(self):
        if ROOT.is_symlink() or not ROOT.is_dir(): raise ReleaseError("deployment root is unavailable")
        self.test_before = self.test_snapshot()
        validate_manifest((ROOT / "releases/manifest.txt").read_text(), self.revision, self.tag)
        image = inspect("image", self.image)
        if image.get("Config", {}).get("Labels", {}).get("org.opencontainers.image.revision") != self.revision:
            raise ReleaseError("accepted image OCI revision does not match")
        if self.image not in image.get("RepoTags", []): raise ReleaseError("accepted image repository differs")
        self.image_id = self.retention.image_id(image["Id"])
        if self.formal.exists() or self.formal.is_symlink():
            if self.formal.is_symlink() or not self.formal.is_dir() or stat.S_IMODE(self.formal.stat().st_mode) != 0o700:
                raise ReleaseError("formal root must be an isolated private directory")
            self.retention.formal_image_pins(ROOT)
            if not self.state_path.is_file():
                raise ReleaseError("existing incomplete formal bootstrap requires inspected recovery; no data was replaced")
            self.previous = json.loads(self.state_path.read_text())
            self.validate_state(self.previous)
            self.check_formal_container(self.previous)
            if not healthy("http://127.0.0.1:13334"): raise ReleaseError("previous formal service is unhealthy")
            env = load_environment(self.runtime)
            if env.get("ADMIN_PASSWORD"):
                raise ReleaseError("previous formal bootstrap password remains active")
        else:
            port_unused(13334)
            ids = run("docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=" + PROJECT).strip()
            if ids: raise ReleaseError("unrecorded formal containers already exist")
        self.check_capacity()
        rollback = self.previous.get("rollback_image_id") if self.previous else None
        self.retention.write_formal_pins(ROOT, self.previous["image_id"] if self.previous else None, rollback, self.image_id)
        for directory in ("backups", "code", "logs", *DATA_DIRS):
            path = self.formal / directory
            if path.is_symlink(): raise ReleaseError("formal subdirectory must not be a symlink")
            path.mkdir(mode=0o700, exist_ok=True)
        if not self.previous:
            write_environment(self.runtime, {"ADMIN_USERNAME": self.credentials["username"],
                "ADMIN_PASSWORD": self.credentials["password"], "SESSION_SECRET": self.credentials["session_secret"],
                "SESSION_COOKIE_NAME": COOKIE})
        if not self.code.exists():
            shutil.copytree(Path(__file__).resolve().parent.parent, self.code)
        if self.code.is_symlink(): raise ReleaseError("formal code directory must not be a symlink")
        identity = run("docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
                       "--entrypoint", "/bin/sh", self.image, "-c", "id -u artai; id -g artai").split()
        if len(identity) != 2 or not all(x.isdecimal() for x in identity):
            raise ReleaseError("runtime user identity is unavailable")
        self.uid, self.gid = map(int, identity)
        if self.uid == 0: raise ReleaseError("formal runtime must not run as root")
        for directory in ("logs", *DATA_DIRS):
            path = self.formal / directory
            if self.previous:
                info = path.stat()
                if (info.st_uid, info.st_gid) != (self.uid, self.gid):
                    raise ReleaseError("candidate runtime user differs from the existing formal data owner")
            else:
                os.chown(path, self.uid, self.gid)
                os.chmod(path, 0o750)
        self.write_probe(self.formal / "logs")
        return self.previous

    def write_probe(self, directory):
        run("docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges:true", "--user", f"{self.uid}:{self.gid}",
            "--mount", f"type=bind,src={directory},dst=/probe", "--entrypoint", "/bin/sh", self.image,
            "-ec", "f=$(mktemp /probe/.write-check.XXXXXX); printf ok > \"$f\"; test -s \"$f\"; rm \"$f\"")

    def login_check(self, base):
        jar = http.cookiejar.CookieJar()
        client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
        http_json(base, "/api/auth/login", client, "POST",
                  {"username": self.credentials["username"], "password": self.credentials["password"]})
        session = http_json(base, "/api/auth/session", client)
        if not session.get("isAdmin") or COOKIE not in {cookie.name for cookie in jar}:
            raise ReleaseError("formal admin or isolated session cookie is unavailable")
        http_json(base, "/api/auth/session", client, "DELETE")

    def preflight(self):
        self.check_capacity()
        port_unused(13342)
        preflight = Path(tempfile.mkdtemp(prefix="preflight-", dir=self.formal / "reports"))
        for name in ("logs", *DATA_DIRS):
            directory = preflight / name
            directory.mkdir(mode=0o750)
            os.chown(directory, self.uid, self.gid)
        try:
            self.compose(self.code, self.tag, "up", "-d", "--no-build", preflight=preflight)
            wait_healthy("http://127.0.0.1:13342")
            # Updates intentionally omit ADMIN_PASSWORD; preflight has an empty DB.
            if not self.previous: self.login_check("http://127.0.0.1:13342")
        finally:
            self.compose(self.code, self.tag, "down", "--timeout", "10", preflight=preflight)
        self.check_capacity()

    def stop(self, previous):
        self.check_capacity()
        self.compose(previous["code"], previous["image_tag"], "stop", "art-ai-go")

    def backup(self, previous):
        self.check_capacity()
        directory = self.formal / "backups" / self.release_id
        directory.mkdir(mode=0o700)
        for name in DATA_DIRS:
            run("tar", "-cf", directory / (name + ".tar"), "-C", self.formal, name)
        shutil.copy2(self.runtime, directory / "runtime.env")
        atomic_json(directory / "previous.json", previous)
        receipt = {"path": str(directory), "previous": previous}
        self.check_capacity(receipt)
        return receipt

    def activate(self):
        self.compose(self.code, self.tag, "up", "-d", "--no-build", "--force-recreate", "art-ai-go")

    def verify(self):
        wait_healthy("http://127.0.0.1:13334")
        self.check_formal_container({"revision": self.revision, "image_id": self.image_id})
        self.write_probe(self.formal / "logs")

    def finish_bootstrap(self):
        if not self.previous:
            self.login_check("http://127.0.0.1:13334")
            env = load_environment(self.runtime)
            env.pop("ADMIN_PASSWORD", None)
            write_environment(self.runtime, env)
            self.activate()
            self.verify()
            self.login_check("http://127.0.0.1:13334")
        row = inspect("container", CONTAINER)
        if any(value.startswith("ADMIN_PASSWORD=") and value != "ADMIN_PASSWORD=" for value in row["Config"]["Env"]):
            raise ReleaseError("bootstrap password remains in the running container")

    def verify_test_unchanged(self):
        if self.test_snapshot() != self.test_before:
            raise ReleaseError("test stack identity changed during formal promotion")

    def publish(self, previous, backup):
        state = {"version": 1, "status": "committing", "revision": self.revision, "image_tag": self.tag, "image_id": self.image_id,
            "code": str(self.code), "rollback_image_id": previous["image_id"] if previous else None,
            "backup": backup, "acceptance": self.report, "acceptance_note": self.payload["acceptance_note"],
            "native_note_sha256": self.payload["native_note_sha256"],
            "test_stack": self.test_before, "released_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "logs": {"directory_writable": True, "stdout_max_size": "50m", "stdout_max_file": 10,
                     "application_debug_file_writer": "pending-issue-233"}}
        atomic_json(self.formal / "reports" / (self.release_id + ".json"), state)
        self.retention.write_formal_pins(ROOT, self.image_id, state["rollback_image_id"], None)
        state["status"] = "success"
        atomic_json(self.state_path, state)
        atomic_json(self.formal / "reports" / (self.release_id + ".json"), state)
        return {"status": "success", "revision": self.revision, "image_tag": self.tag,
                "report": str(self.formal / "reports" / (self.release_id + ".json")),
                "rollback_image_id": state["rollback_image_id"], "test_stack_unchanged": True}

    def recover(self, previous, backup, activated):
        if activated:
            self.compose(self.code, self.tag, "stop", "art-ai-go")
        if not previous:
            # Keep the bootstrap DB/env/report for inspected recovery. Never wipe it.
            self.retention.write_formal_pins(ROOT, None, None, self.image_id)
            if self.state_path.exists():
                state = json.loads(self.state_path.read_text())
                if state.get("revision") != self.revision or state.get("image_id") != self.image_id:
                    raise ReleaseError("formal state changed during bootstrap recovery")
                state["status"] = "stopped-no-previous-version"
                atomic_json(self.state_path, state)
            return
        if activated and backup:
            self.validate_backup(backup, previous)
            self.check_capacity(backup)
            directory = Path(backup["path"])
            staging = Path(tempfile.mkdtemp(prefix="restore-", dir=self.formal / "backups"))
            failed = self.formal / "backups" / (self.release_id + "-failed")
            failed.mkdir(mode=0o700)
            for name in DATA_DIRS:
                run("tar", "-xf", directory / (name + ".tar"), "-C", staging)
            for name in DATA_DIRS:
                os.rename(self.formal / name, failed / name)
                os.rename(staging / name, self.formal / name)
            shutil.copy2(directory / "runtime.env", self.runtime)
            # Valid old deployments never contain bootstrap passwords.
            if load_environment(self.runtime).get("ADMIN_PASSWORD"):
                raise ReleaseError("rollback environment contains a bootstrap password")
        self.compose(previous["code"], previous["image_tag"], "up", "-d", "--no-build", "--force-recreate", "art-ai-go")
        wait_healthy("http://127.0.0.1:13334")
        self.check_formal_container(previous)
        self.retention.write_formal_pins(ROOT, previous["image_id"], previous.get("rollback_image_id"), self.image_id)
        atomic_json(self.state_path, previous)
        self.verify_test_unchanged()

    def validate_backup(self, backup, previous):
        directory = Path(backup.get("path", ""))
        if (directory.parent != self.formal / "backups" or directory.is_symlink() or not directory.is_dir()
                or backup.get("previous") != previous):
            raise ReleaseError("formal backup identity is invalid")
        if json.loads((directory / "previous.json").read_text()) != previous:
            raise ReleaseError("formal backup previous-state receipt differs")
        if load_environment(directory / "runtime.env").get("ADMIN_PASSWORD"):
            raise ReleaseError("formal backup has an active bootstrap password")

    def failure(self, stage, recovered):
        if not self.formal.is_dir(): return
        status = "rolled-back" if recovered and self.previous else "stopped-no-previous-version" if recovered else "failed"
        report = {"status": status, "stage": stage, "revision": self.revision, "recovered": recovered,
                  "previous_revision": self.previous["revision"] if self.previous else None}
        # Replace any partially committed candidate report, not only a separate error file.
        atomic_json(self.formal / "reports" / (self.release_id + ".json"), report)
        atomic_json(self.formal / "reports" / (self.release_id + "-failure.json"), report)


def server(args):
    if os.geteuid() != 0: raise ReleaseError("formal server operations require the authorized deployment account")
    configure(args.root, forge=args.forge, project=args.project, test_project=args.test_project)
    payload = json.load(sys.stdin)
    ops = ServerOperations(args.revision, payload)
    def interrupted(*_): raise ReleaseError("formal release interrupted")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    with ops.retention.image_lock(ROOT):
        receipt = promote(ops)
    print(json.dumps(receipt, ensure_ascii=False))


def rollback_server(args):
    if os.geteuid() != 0: raise ReleaseError("formal rollback requires the authorized deployment account")
    configure(args.root, forge=args.forge, project=args.project, test_project=args.test_project)
    revision = validate_revision(args.revision)
    retention = load_helper("retain-art-ai-images.py", "formal_rollback_retention")
    def interrupted(*_): raise ReleaseError("formal rollback interrupted")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    with retention.image_lock(ROOT):
        state_path = ROOT / "formal/reports/current.json"
        state = json.loads(state_path.read_text())
        if state.get("revision") != revision:
            raise ReleaseError("rollback refused because the current formal revision changed")
        payload = {"acceptance": state["acceptance"], "acceptance_note": state["acceptance_note"],
                   "native_note_sha256": state["native_note_sha256"], "credentials": {}}
        ops = ServerOperations(revision, payload)
        ops.image_id = state["image_id"]
        ops.test_before = ops.test_snapshot()
        ops.validate_state(state)
        ops.check_formal_container(state, require_running=False)
        backup = state.get("backup")
        previous = backup.get("previous") if backup else None
        if previous:
            ops.validate_state(previous)
            ops.validate_backup(backup, previous)
        ops.previous = previous
        try:
            ops.recover(previous, backup, True)
            ops.verify_test_unchanged()
            if not previous:
                state["status"] = "stopped-no-previous-version"
                atomic_json(state_path, state)
        except BaseException:
            ops.failure("requested-rollback", False)
            raise ReleaseError("requested formal rollback failed; preserve evidence and stop") from None
        receipt = {"status": "rolled-back" if previous else "stopped-no-previous-version",
                   "failed_revision": revision, "restored_revision": previous["revision"] if previous else None,
                   "test_stack_unchanged": True}
        atomic_json(ROOT / "formal/reports" / (ops.release_id + "-rollback.json"), receipt)
    print(json.dumps(receipt))


def remote_operation(command, payload=None):
    # The remote program prints only fixed stage/capacity metadata and receipts.
    # Stream those diagnostics; never send credentials in argv or print stdin.
    try:
        subprocess.run(["ssh", HOST, command], input=payload, check=True, timeout=1800)
    except (OSError, subprocess.SubprocessError):
        raise ReleaseError("formal remote operation failed; inspect the formal reports before retrying") from None


def local(args):
    revision = validate_revision(args.revision)
    if not args.rollback and (not args.acceptance_note or args.acceptance_note < 1 or not args.credentials_file):
        raise ReleaseError("promotion requires --acceptance-note and --credentials-file")
    run("git", "fetch", "-q", "origin")
    run("git", "merge-base", "--is-ancestor", revision, "origin/main")
    configure_from_environments(getattr(args, "file", None))
    target = (" --root " + shlex.quote(str(ROOT)) + " --forge " + shlex.quote(FORGE)
              + " --project " + PROJECT + " --test-project " + TEST_PROJECT)
    if args.rollback:
        command = "python3 " + str(ROOT / "formal/code" / revision / "scripts/release-formal.py")
        remote_operation(command + " rollback-server " + revision + target)
        return
    note = read_acceptance_comment(FORGE_REPO, LEGACY_ISSUE, args.acceptance_note)
    report = acceptance_from_note(note.get("body", ""), revision)
    credentials_path = Path(args.credentials_file).expanduser().resolve()
    if credentials_path.is_relative_to(Path(__file__).resolve().parent.parent):
        raise ReleaseError("admin credentials must be stored outside the source repository")
    credentials = credential_file(credentials_path)
    staging = run("ssh", HOST, "mktemp -d /tmp/art-ai-formal.XXXXXXXXXX").strip()
    if not re.fullmatch(r"/tmp/art-ai-formal\.[a-zA-Z0-9]{10}", staging):
        raise ReleaseError("remote staging path is invalid")
    # Archive the exact accepted commit, not local uncommitted files or test/app.
    archive = subprocess.run(["git", "archive", revision], capture_output=True, check=True).stdout
    run("ssh", HOST, "tar -x -C " + shlex.quote(staging), input=archive)
    payload = {"acceptance": report, "acceptance_note": args.acceptance_note,
               "credentials": credentials,
               "native_note_sha256": hashlib.sha256(note["body"].encode()).hexdigest()}
    command = "python3 " + shlex.quote(staging + "/scripts/release-formal.py") + " server " + revision + target
    remote_operation(command, json.dumps(payload).encode())
    print("[formal] admin credentials: " + str(Path(args.credentials_file).expanduser().absolute()))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="mode", required=True)
    entry = sub.add_parser("local")
    entry.add_argument("revision")
    entry.add_argument("--acceptance-note", type=int)
    entry.add_argument("--credentials-file")
    entry.add_argument("--rollback", action="store_true", help="recover the current formal revision after smoke failure")
    entry.add_argument("--file", help="environments file (default: ART_AI_ENVIRONMENTS_FILE or the local deployment folder)")
    server_parser = sub.add_parser("server")
    server_parser.add_argument("revision")
    rollback_parser = sub.add_parser("rollback-server")
    rollback_parser.add_argument("revision")
    for internal in (server_parser, rollback_parser):
        internal.add_argument("--root", required=True)
        internal.add_argument("--forge", required=True)
        internal.add_argument("--project", required=True)
        internal.add_argument("--test-project", required=True)
    args = parser.parse_args()
    try:
        if args.mode == "local": local(args)
        elif args.mode == "server": server(args)
        else: rollback_server(args)
        return 0
    except (ReleaseError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        message = str(error) if isinstance(error, ReleaseError) else "release input or state is unavailable"
        print("[formal] refused: " + message, file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
