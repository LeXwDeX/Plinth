#!/usr/bin/env python3
"""Runs the dev_test.md section 2 gates for one exact commit in a clean container on the CI host (#97).

    python3 scripts/host-ci/run.py HEAD          # or any commit-ish; it is resolved to a full SHA

It bundles that commit, uploads the bundle to the host named by the `dev-test` entry of
the untracked environments file, runs `GOFLAGS=-p=4 python3 scripts/verify-local.py --race
--force` plus the external-link check in a one-off container (no network, 4 CPUs, 12 GiB,
no swap, non-root, no capabilities, input bundle read-only) and brings result.json and the
logs back to build/host-ci/<run id>/. Exit status: 0 pass, 1 fail, 70 host resources too
low, 75 another run is active, 2 usage or environment error.

The gate logic is not in this tool: it is `scripts/verify-local.py` plus the image built
from `Dockerfile.ci`. Any other runner can build that same image and run the same entry
point (scripts/host-ci/entry.sh) against the same mounts. This tool is the local / test-host
wrapper around it. See dev_test.md section 2.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import time

TOOL_DIR = Path(__file__).resolve().parent
SCRIPTS_DIR = TOOL_DIR.parent
sys.path.insert(0, str(SCRIPTS_DIR))
import local_environments  # noqa: E402

DEFAULT_CI_DIR = '/var/lib/plinth-ci'
# Files that decide the image. The first two come from the tool's own checkout (the
# image definition), the rest from the commit under test (the dependencies it needs).
TOOL_FILES = ('Dockerfile.ci', 'scripts/host-ci/entry.sh')
MANIFEST_FILES = ('package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', 'go.mod', 'go.sum')
SSH_OPTIONS = ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=30']
SAFE_PATH = re.compile(r'/[A-Za-z0-9_./-]+')


class UsageError(Exception):
    pass


def git(repo, *args, data=False):
    result = subprocess.run(['git', '-C', str(repo), *args], capture_output=True)
    if result.returncode:
        raise UsageError(f"git {' '.join(args[:2])} failed: {result.stderr.decode(errors='replace').strip()[:300]}")
    return result.stdout if data else result.stdout.decode().strip()


def toolchain_inputs(repo, sha):
    """(path, bytes) of every file that decides the image, in a fixed order."""
    files = [(path, (TOOL_DIR.parent.parent / path).read_bytes()) for path in TOOL_FILES]
    for path in MANIFEST_FILES:
        files.append((path, git(repo, 'show', f'{sha}:{path}', data=True)))
    return files


def toolchain_hash(files):
    """12 hex digits naming plinth-ci:<hash>; changes only with the files above."""
    digest = hashlib.sha256()
    for path, content in files:
        digest.update(f'{path}\0{len(content)}\0'.encode())
        digest.update(content)
    return digest.hexdigest()[:12]


def context_tar(files):
    """Deterministic build context holding exactly the hashed files."""
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w') as archive:
        for path, content in files:
            info = tarfile.TarInfo(path)
            info.size = len(content)
            info.mode = 0o755 if path.endswith('.sh') else 0o644
            archive.addfile(info, io.BytesIO(content))
    return buffer.getvalue()


def load_environment(name):
    path = local_environments.environments_file()
    try:
        document = json.loads(path.read_text())
        environment = document['environments'][name]
    except (OSError, ValueError, KeyError):
        raise UsageError(f'environment {name} is not defined in the untracked environments file '
                         f'(template: deploy/environments.example.json)') from None
    ssh = environment.get('ssh')
    if not isinstance(ssh, str) or not re.fullmatch(r'[a-z_][a-z0-9_-]*@[A-Za-z0-9.-]+', ssh):
        raise UsageError(f'environment {name}: ssh must be user@host')
    ci_dir = (environment.get('host_ci') or {}).get('dir', DEFAULT_CI_DIR)
    if not SAFE_PATH.fullmatch(ci_dir) or '..' in ci_dir or ci_dir.rstrip('/') != ci_dir:
        raise UsageError(f'environment {name}: host_ci.dir must be a plain absolute path')
    # The CI root must stay clear of every release root and protected path of every environment.
    taken = [other.get('root') for other in document['environments'].values() if isinstance(other, dict)]
    taken += [path for other in document['environments'].values() if isinstance(other, dict)
              for path in other.get('protected_paths') or []]
    for used in filter(None, taken):
        used = used.rstrip('/')
        if ci_dir == used or ci_dir.startswith(used + '/') or used.startswith(ci_dir + '/'):
            raise UsageError(f'host_ci.dir overlaps a release root or protected path of {name}; pick a separate directory')
    return ssh, ci_dir


def ssh_run(target, command, **kwargs):
    return subprocess.run(['ssh', *SSH_OPTIONS, target, command], **kwargs)


def make_bundle(repo, sha, destination):
    ref = f'refs/plinth-host-ci/{sha}'
    git(repo, 'update-ref', ref, sha)
    try:
        git(repo, 'bundle', 'create', str(destination), ref)
    finally:
        subprocess.run(['git', '-C', str(repo), 'update-ref', '-d', ref], capture_output=True)


def upload(target, ci_dir, run_id, stage):
    run_dir = f'{ci_dir}/run/{run_id}'
    # COPYFILE_DISABLE keeps macOS bsdtar from adding AppleDouble/xattr headers the host's GNU tar warns about.
    tar = subprocess.Popen(['tar', '-C', str(stage), '-cf', '-', '.'], stdout=subprocess.PIPE,
                           env={**os.environ, 'COPYFILE_DISABLE': '1'})
    remote = (f'umask 077 && mkdir -p {ci_dir}/run {ci_dir}/results && mkdir {run_dir} && '
              f'tar -xf - -C {run_dir} --no-same-owner')
    outcome = ssh_run(target, remote, stdin=tar.stdout)
    tar.stdout.close()
    if tar.wait() or outcome.returncode:
        raise UsageError('upload to the CI host failed')
    return run_dir


def fetch_results(target, ci_dir, run_id, destination):
    destination.mkdir(parents=True, exist_ok=True)
    remote = ssh_run(target, f'tar -C {ci_dir}/results/{run_id} -cf - .', stdout=subprocess.PIPE)
    extract = subprocess.run(['tar', '-xf', '-', '-C', str(destination)], input=remote.stdout)
    return remote.returncode == 0 and extract.returncode == 0


def summarize(result):
    attempt = (result.get('attempts') or [{}])[-1]
    entry = attempt.get('entry') or {}
    lines = [f"verdict: {result.get('verdict', '?').upper()}  sha {result['sha'][:12]}  tree {result['tree'][:12]}"]
    for gate in entry.get('gates') or []:
        seconds = f"{gate['seconds']:.1f}s" if 'seconds' in gate else '-'
        lines.append(f"  {gate['name']:<20} {gate['result']:<8} {seconds}")
    cgroup = entry.get('cgroup') or {}
    peak = cgroup.get('memory_peak_bytes')
    lines.append(f"  total {result.get('total_seconds', '?')}s  attempts {len(result.get('attempts') or [])}  "
                 f"image {(result.get('image') or {}).get('name', '-')}"
                 f"{' (built)' if (result.get('image') or {}).get('built') else ''}  "
                 f"peak memory {peak / (1 << 30):.1f} GiB  OOM {attempt.get('oom_killed')}  "
                 f"oom_kill {cgroup.get('oom_kill')}  exit {attempt.get('container_exit_code')}" if peak is not None else
                 f"  total {result.get('total_seconds', '?')}s  exit {attempt.get('container_exit_code')}")
    for reason in result.get('reasons') or []:
        lines.append(f'  reason: {reason}')
    return '\n'.join(lines)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('ref', nargs='?', default='HEAD', help='commit-ish to check (default HEAD)')
    parser.add_argument('--repo', default=str(TOOL_DIR.parent.parent), help='repository holding the commit (default: this checkout)')
    parser.add_argument('--env', default='dev-test', help='environment name in the untracked environments file')
    parser.add_argument('--out', default=None, help='where results are stored (default: build/host-ci)')
    parser.add_argument('--timeout-min', type=int, default=100)
    parser.add_argument('--min-mem-gib', type=float, default=15, help='host preflight: required MemAvailable in GiB (12 GiB container plus margin)')
    parser.add_argument('--print-image-tag', action='store_true', help='print plinth-ci:<hash> for the commit and exit')
    args = parser.parse_args(argv)
    repo = Path(args.repo).resolve()
    try:
        sha = git(repo, 'rev-parse', '--verify', f'{args.ref}^{{commit}}')
        tree = git(repo, 'rev-parse', f'{sha}^{{tree}}')
        files = toolchain_inputs(repo, sha)
        tag = toolchain_hash(files)
        if args.print_image_tag:
            print(f'plinth-ci:{tag}')
            return 0
        if args.ref == 'HEAD' and git(repo, 'status', '--porcelain', '--untracked-files=no'):
            print('warning: uncommitted changes are not part of HEAD and are not tested', file=sys.stderr)
        target, ci_dir = load_environment(args.env)
        run_id = f"{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}-{sha[:12]}-{os.urandom(2).hex()}"
        out = Path(args.out) if args.out else TOOL_DIR.parent.parent / 'build' / 'host-ci'
        print(f'[host-ci] {run_id}: commit {sha}, image plinth-ci:{tag}', flush=True)
        with tempfile.TemporaryDirectory(prefix='plinth-host-ci.') as stage_name:
            stage = Path(stage_name)
            make_bundle(repo, sha, stage / 'source.bundle')
            (stage / 'context.tar').write_bytes(context_tar(files))
            (stage / 'host_run.py').write_bytes((TOOL_DIR / 'host_run.py').read_bytes())
            run_dir = upload(target, ci_dir, run_id, stage)
        command = (f'python3 -I {run_dir}/host_run.py --root {ci_dir} --run-id {run_id} --sha {sha} --tree {tree} '
                   f'--image-tag {tag} --timeout-min {args.timeout_min} --min-mem-gib {args.min_mem_gib:g}')
        # The open stdin pipe tells host_run.py that this client is alive; it stops the run when it closes.
        remote = subprocess.Popen(['ssh', *SSH_OPTIONS, target, command], stdin=subprocess.PIPE)
        try:
            code = remote.wait()
        finally:
            remote.stdin.close()
        if code == 255:
            print('[host-ci] the SSH connection failed or dropped; the host cleans up after itself', file=sys.stderr)
            return 1
        if code == 75:
            return code
        if fetch_results(target, ci_dir, run_id, out / run_id):
            result = json.loads((out / run_id / 'result.json').read_text())
            print(summarize(result))
            print(f'results: {out / run_id}')
        else:
            print('[host-ci] could not fetch the result files', file=sys.stderr)
        return code
    except UsageError as error:
        print(f'run.py: {error}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
