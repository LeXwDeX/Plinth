#!/usr/bin/env python3
"""Build or check the clean public snapshot of Plinth (#261).

The public repository starts from one root commit that holds only the tracked
files of an exact source revision, never the internal history. This tool
builds that commit in a new local directory and NEVER pushes: publishing (and
any force-replacement of the public repository) is a separate, confirmed step.

  make-public-snapshot.py check [--worktree | --rev REV | --tree DIR]
  make-public-snapshot.py make --version v1.0.0 --out DIR [--rev REV] [--tag]

Both modes scan every file the snapshot would contain (text and binary bytes)
for internal identifiers:

  * the built-in patterns below (private network prefix, internal hosts and
    domain, server and temporary paths, environment Compose project prefix,
    organization names, local user directories, private key headers and
    credential-shaped tokens) -- written so that this file does not match itself;
  * every literal value of the untracked local environments file
    (deploy/local/environments.json, see scripts/local_environments.py): hosts,
    addresses, roots, layout paths, Compose projects and mirror hosts;
  * extra regular expressions from --pattern, from --patterns-file, and from
    the untracked deploy/local/public-snapshot-patterns.txt when present (one
    per line, # comments), so private identifiers never need to be committed.

Any match fails the run with a non-zero exit. Paths matching the exclusion
manifest scripts/public-snapshot.exclude never enter the snapshot.
"""
import argparse
import fnmatch
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

SCRIPTS = Path(__file__).resolve().parent
REPOSITORY = SCRIPTS.parent
EXCLUDE_FILE = SCRIPTS / "public-snapshot.exclude"
LOCAL_PATTERNS = Path("deploy/local/public-snapshot-patterns.txt")
# Character classes and groups keep each pattern from matching its own source text
# (bytes regex: multi-byte characters use a group, never a class).
BUILTIN_PATTERNS = (
    r"192\.168\.",
    r"33\.110",
    r"33\.146",
    r"(?i:yc[g]ame)",
    r"(?i:yun[c]han)",
    r"云(?:蟾)",
    r"/D[A]TA/",
    r"/private/t[m]p",
    r"meshyai_g[o]_",
    # #334: local user directories (the public CI runner's home is not one), private
    # keys and credential-shaped tokens (GitLab, OpenAI-style, GitHub, Meshy, Tripo).
    # Lengths stay above the short fake secrets that test fixtures use.
    r"/(?:U[s]ers|h[o]me)/(?!runner/)[A-Za-z0-9._-]+",
    r"-----BEGIN [A-Z ]*PRIV[A]TE KEY-----",
    r"glp[a]t-[0-9A-Za-z_-]{20,}",
    r"(?<![A-Za-z0-9_-])s[k]-[A-Za-z0-9_-]{32,}",
    r"gh[pousr]_[A-Za-z0-9]{36,}",
    r"(?<![A-Za-z0-9])ms[y]_[A-Za-z0-9_-]{20,}",
    r"(?<![A-Za-z0-9])ts[k]_[A-Za-z0-9_-]{20,}",
)
VERSION = re.compile(r"v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?")
CONTEXT = 40


class SnapshotError(Exception):
    """A refusal with a fixed, readable reason."""


def git(*args, cwd=None, binary=False):
    result = subprocess.run(["git", *args], cwd=cwd or REPOSITORY, capture_output=True, check=False)
    if result.returncode:
        message = result.stderr.decode("utf-8", "replace").strip().splitlines()
        raise SnapshotError(f"git {args[0]} failed: {message[-1] if message else result.returncode}")
    return result.stdout if binary else result.stdout.decode("utf-8").strip()


def load_exclusions(path=EXCLUDE_FILE):
    patterns = []
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            patterns.append(line)
    return patterns


def excluded(relative, patterns):
    """gitignore-like subset: 'dir/' excludes a directory, otherwise fnmatch on the path or any parent."""
    parts = relative.split("/")
    for pattern in patterns:
        if pattern.endswith("/"):
            directory = pattern.rstrip("/")
            if any(fnmatch.fnmatchcase("/".join(parts[:index]), directory) for index in range(1, len(parts))):
                return True
            if "/" not in directory and any(fnmatch.fnmatchcase(part, directory) for part in parts[:-1]):
                return True
        elif fnmatch.fnmatchcase(relative, pattern) or ("/" not in pattern and fnmatch.fnmatchcase(parts[-1], pattern)):
            return True
    return False


def local_roots():
    helper = importlib.util.spec_from_file_location("snapshot_local_environments", SCRIPTS / "local_environments.py")
    module = importlib.util.module_from_spec(helper)
    helper.loader.exec_module(module)
    roots = [REPOSITORY]
    main = module.main_checkout(REPOSITORY)
    if main is not None and main.resolve() != REPOSITORY.resolve():
        roots.append(main)
    return module, roots


def environment_literals(path):
    """Every identifying string of the local environments file, as literals."""
    try:
        document = json.loads(Path(path).read_text(encoding="utf-8"))
    except FileNotFoundError:
        return []
    except ValueError:
        raise SnapshotError(f"{path} is not valid JSON") from None
    values = set()

    def add(value):
        if isinstance(value, str) and len(value) >= 4 and not value.startswith("<"):
            values.add(value)

    def host_of(url):
        match = re.match(r"[a-z]+://([^/:]+)", url or "")
        return match.group(1) if match else None

    acceptance = document.get("acceptance") or {}
    add(host_of(acceptance.get("forge_url")))
    for environment in (document.get("environments") or {}).values():
        if not isinstance(environment, dict):
            continue
        ssh = environment.get("ssh") or ""
        add(ssh.rpartition("@")[2])
        add(environment.get("root"))
        add(host_of(environment.get("base_url")))
        add(environment.get("compose_project"))
        for other in environment.get("protect_projects") or []:
            add(other)
        add(environment.get("extra_compose_file"))
        for key, value in (environment.get("layout") or {}).items():
            if key != "GO_WEB_HOST_PORT":
                add(value)
        build = environment.get("build") or {}
        # release-env.py refuses this removed field (#257); read the literal anyway so a
        # stale local file cannot leak its address into a snapshot.
        add(host_of(build.get("npm_registry")))
        for proxy in (build.get("goproxy") or "").split(","):
            add(host_of(proxy))
        for entry in build.get("add_hosts") or []:
            host, _, address = entry.partition(":")
            add(host)
            add(address)
    return sorted(values)


def read_patterns(path):
    patterns = []
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            patterns.append(line)
    return patterns


def compile_patterns(extra=(), patterns_files=(), use_local=True):
    """Return [(label, compiled bytes regex)]; labels of private patterns are not their text."""
    compiled = [(pattern, pattern) for pattern in BUILTIN_PATTERNS]
    compiled += [(pattern, pattern) for pattern in extra]
    for path in patterns_files:
        compiled += [(f"{Path(path).name}:{index}", pattern) for index, pattern in enumerate(read_patterns(path), 1)]
    if use_local:
        module, roots = local_roots()
        for literal in environment_literals(module.environments_file()):
            compiled.append(("local environments value", re.escape(literal)))
        for root in roots:
            candidate = root / LOCAL_PATTERNS
            if candidate.is_file():
                compiled += [(f"{LOCAL_PATTERNS}:{index}", pattern)
                             for index, pattern in enumerate(read_patterns(candidate), 1)]
                break
    result = []
    for label, pattern in compiled:
        try:
            result.append((label, re.compile(pattern.encode("utf-8"))))
        except re.error as error:
            raise SnapshotError(f"invalid pattern {label}: {error}") from None
    return result


def snapshot_files(tree):
    tree = Path(tree)
    for path in sorted(tree.rglob("*")):
        relative = path.relative_to(tree).as_posix()
        if relative == ".git" or relative.startswith(".git/"):
            continue
        if path.is_symlink() or path.is_file():
            yield relative, path


def worktree_files(repository=None):
    """Tracked files as they are in the working tree (the CI and pre-push view of HEAD)."""
    repository = Path(repository or REPOSITORY)
    for name in git("ls-files", "-z", cwd=repository, binary=True).decode("utf-8").split("\0"):
        path = repository / name
        if name and (path.is_symlink() or path.is_file()):
            yield name, path


def scan(tree, patterns, exclusions=(), files=None):
    """Return a list of 'path:line: label: context' findings for files that would be published."""
    findings = []
    for relative, path in (snapshot_files(tree) if files is None else files):
        if exclusions and excluded(relative, exclusions):
            continue
        data = os.readlink(path).encode("utf-8") if path.is_symlink() else path.read_bytes()
        for label, regex in patterns:
            for match in regex.finditer(relative.encode("utf-8") + b"\n" + data):
                offset = match.start() - len(relative) - 1
                if offset < 0:
                    findings.append(f"{relative}: path: {label}")
                    continue
                line = data.count(b"\n", 0, offset) + 1
                start, end = max(offset - CONTEXT, 0), min(match.end() - len(relative) - 1 + CONTEXT, len(data))
                context = data[start:end].decode("utf-8", "replace").replace("\n", " ")
                findings.append(f"{relative}:{line}: {label}: {context}")
    return findings


def resolve(rev):
    return git("rev-parse", "--verify", f"{rev}^{{commit}}")


def export(revision, destination):
    """Extract the tracked files of an exact revision (git archive) into destination."""
    archive = git("archive", "--format=tar", revision, binary=True)
    with tempfile.TemporaryFile() as handle:
        handle.write(archive)
        handle.seek(0)
        with tarfile.open(fileobj=handle) as tar:
            tar.extractall(destination, filter="data")


def remove_excluded(tree, exclusions):
    removed = []
    for relative, path in list(snapshot_files(tree)):
        if excluded(relative, exclusions):
            path.unlink()
            removed.append(relative)
    for directory in sorted((p for p in Path(tree).rglob("*") if p.is_dir() and not p.is_symlink()),
                            key=lambda p: len(p.parts), reverse=True):
        if not any(directory.iterdir()):
            directory.rmdir()
    return removed


def report(findings):
    for finding in findings:
        print(f"[snapshot] FORBIDDEN {finding}", file=sys.stderr)
    print(f"[snapshot] {len(findings)} forbidden match(es); nothing may be published", file=sys.stderr)


def check(args, patterns, exclusions):
    if args.worktree:
        findings = scan(None, patterns, exclusions, files=worktree_files())
        label = f"tracked files of {REPOSITORY}"
    elif args.tree:
        tree = Path(args.tree)
        if not tree.is_dir():
            raise SnapshotError(f"{tree} is not a directory")
        findings = scan(tree, patterns, exclusions)
        label = str(tree)
    else:
        revision = resolve(args.rev)
        with tempfile.TemporaryDirectory(prefix="plinth-snapshot-check.") as directory:
            export(revision, directory)
            findings = scan(directory, patterns, exclusions)
        label = revision
    if findings:
        report(findings)
        return 1
    print(f"[snapshot] clean: {label}")
    return 0


def inside(path, parent):
    try:
        Path(path).resolve().relative_to(Path(parent).resolve())
        return True
    except ValueError:
        return False


def make(args, patterns, exclusions):
    if not VERSION.fullmatch(args.version):
        raise SnapshotError("--version must look like v1.0.0")
    out = Path(args.out).expanduser()
    if out.exists() and (not out.is_dir() or any(out.iterdir())):
        raise SnapshotError(f"{out} must not exist or must be an empty directory")
    if inside(out, git("rev-parse", "--show-toplevel")):
        raise SnapshotError("--out must be outside the source repository")
    revision = resolve(args.rev)
    name = git("config", "user.name")
    email = git("config", "user.email")
    if not name or not email:
        raise SnapshotError("git config user.name and user.email are required for the snapshot author")
    out.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=f".{out.name}.partial-", dir=out.parent))
    try:
        export(revision, staging)
        removed = remove_excluded(staging, exclusions)
        findings = scan(staging, patterns)
        if findings:
            report(findings)
            return 1
        environment = {**os.environ, "GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
                       "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email}
        def run(*command):
            result = subprocess.run(["git", *command], cwd=staging, env=environment, capture_output=True, check=False)
            if result.returncode:
                raise SnapshotError(f"git {command[0]} in the snapshot failed: "
                                    + result.stderr.decode("utf-8", "replace").strip())
            return result.stdout.decode("utf-8").strip()
        run("init", "-q", "-b", "main")
        run("add", "-A")
        message = (f"Plinth {args.version}\n\nPublic snapshot of source revision {revision}.\n"
                   "Single root commit; the internal development history is not included.\n")
        run("-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
        if args.tag:
            run("-c", "tag.gpgsign=false", "tag", "-a", args.version, "-m", f"Plinth {args.version}")
        commit = run("rev-parse", "HEAD")
        if run("remote"):
            raise SnapshotError("the snapshot repository must have no remote")
        if out.exists():
            out.rmdir()
        staging.rename(out)
    finally:
        # Failure leaves nothing behind; success has already renamed the staging directory.
        shutil.rmtree(staging, ignore_errors=True)
    print(f"[snapshot] {out}: commit {commit} from {revision} ({args.version}); "
          f"{len(removed)} excluded path(s); not pushed")
    for path in removed:
        print(f"[snapshot] excluded {path}")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="mode", required=True)
    check_parser = sub.add_parser("check", help="scan what a snapshot would contain")
    source = check_parser.add_mutually_exclusive_group()
    source.add_argument("--worktree", action="store_true",
                        help="scan the tracked files as they are in this working tree (CI and pre-push gate)")
    source.add_argument("--rev", default="HEAD", help="scan the tracked files of this revision (default HEAD)")
    source.add_argument("--tree", help="scan an existing directory (e.g. a built snapshot) instead")
    make_parser = sub.add_parser("make", help="build the single-commit snapshot repository locally")
    make_parser.add_argument("--rev", default="HEAD", help="source revision (default HEAD)")
    make_parser.add_argument("--version", required=True, help="release version for the commit message, e.g. v1.0.0")
    make_parser.add_argument("--out", required=True, help="new or empty directory outside this repository")
    make_parser.add_argument("--tag", action="store_true", help="also create an annotated tag named --version locally")
    for entry in (check_parser, make_parser):
        entry.add_argument("--pattern", action="append", default=[], help="extra forbidden regular expression")
        entry.add_argument("--patterns-file", action="append", default=[], help="file of extra forbidden expressions")
        entry.add_argument("--no-local", action="store_true",
                           help="skip the untracked local environments file and local pattern file")
        entry.add_argument("--exclude-file", default=str(EXCLUDE_FILE), help="exclusion manifest")
    args = parser.parse_args(argv)
    try:
        exclusions = load_exclusions(args.exclude_file)
        patterns = compile_patterns(args.pattern, args.patterns_file, use_local=not args.no_local)
        return check(args, patterns, exclusions) if args.mode == "check" else make(args, patterns, exclusions)
    except SnapshotError as error:
        print(f"[snapshot] refused: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
