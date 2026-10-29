#!/usr/bin/env python3
"""Runs the dev_test.md section 2 gates for one exact commit in a clean container on the CI host (#97).

    python3 scripts/host-ci/run.py HEAD          # or any commit-ish; it is resolved to a full SHA

It bundles that commit, uploads the bundle to the host named by the `dev-test` entry of
the untracked environments file, runs `GOFLAGS=-p=4 python3 scripts/verify-local.py --race
--force` plus the external-link check in a one-off container (no network, 4 CPUs, 12 GiB,
no swap, non-root, no capabilities, input bundle read-only) and brings result.json and the
logs back to build/host-ci/<run id>/. Exit status: 0 pass, 1 fail, 70 host resources too
low, 75 another run is active, 2 usage or environment error. The resource preflight
thresholds, the container limits and the timeouts are options (and optional `host_ci`
fields of the environment entry); the host refuses values that would weaken the gate.

With --workflows it instead replays .github/workflows (the pull_request and push events) for that
commit with a pinned nektos/act in throwaway containers on the same host, under the same lock,
preflight and limits (not a GitHub runner; dev_test.md section 2). After a run, the verdict is written
as a commit status (`host-ci/gates` or `host-ci/workflows`) with the local `gh` login; --no-status skips it.

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
# Workflow replay (#86): act is pinned by release tag and by the sha256 of its release tarball
# (checksums.txt of the release); the runner image is pinned by index digest.
ACT_VERSION = 'v0.2.89'
ACT_SHA256 = '0191d6f1f3b716b5c55820032605d05fc3c1cdbf581ebeff655019e5dd1524c0'
RUNNER_IMAGE = 'catthehacker/ubuntu:act-24.04@sha256:c58e2b364da03b0c804c7d660f2ecbedf2f221a382b9baa0b344b0144780ff43'
STATUS_CONTEXTS = {'gates': 'host-ci/gates', 'workflows': 'host-ci/workflows'}
# Tunable limits. Precedence: command line, then the optional `host_ci` object of the environment
# entry (same names), then these defaults, which host_run.DEFAULTS repeats (test_host_ci.py keeps them equal).
# The host validates the final values again (host_run.limit_problems).
LIMIT_DEFAULTS = {'cpus': 4, 'memory_gib': 12, 'min_mem_gib': 13, 'min_disk_gib': 10, 'timeout_min': 100, 'build_timeout_min': 40}
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
    host_ci = environment.get('host_ci') or {}
    if not isinstance(host_ci, dict):
        raise UsageError(f'environment {name}: host_ci must be an object')
    ci_dir = host_ci.get('dir', DEFAULT_CI_DIR)
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
    return ssh, ci_dir, host_ci


def resolve_limits(args, host_ci):
    """Final numeric limits: command line over environment file over defaults; non-numbers are refused."""
    limits = {}
    for key, default in LIMIT_DEFAULTS.items():
        chosen = getattr(args, key)
        if chosen is None:
            chosen = host_ci.get(key, default)
        if isinstance(chosen, bool) or not isinstance(chosen, (int, float)) or chosen != chosen:
            raise UsageError(f'limit {key} must be a number, got {chosen!r}')
        limits[key] = chosen
    return limits


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
    if result.get('mode') == 'workflows':
        report = result.get('workflows') or {}
        lines.append(f"  act {report.get('act_version', '?')}  runner image {(report.get('runner_image') or '-')[:60]}")
        for event in report.get('events') or []:
            lines.append(f"  {event['event']:<14} {event['seconds']:.0f}s  exit {event['exit_code']}  peak memory {event.get('peak_memory_bytes', 0) / (1 << 30):.1f} GiB")
            for job in event['jobs']:
                lines.append(f"    {job['name']:<20} {job['result']}")
        lines.append(f"  total {result.get('total_seconds', '?')}s  peak memory {report.get('peak_memory_bytes', 0) / (1 << 30):.1f} GiB")
        lines.extend(f'  reason: {reason}' for reason in result.get('reasons') or [])
        return '\n'.join(lines)
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


def repo_slug(repo):
    url = git(repo, 'remote', 'get-url', 'origin')
    match = re.search(r'github\.com[:/]([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?$', url)
    if not match:
        raise UsageError('origin is not a github.com remote; cannot name the repository')
    return match.group(1)


def status_of(result):
    """(state, description) for a commit status, or None when the run says nothing about the commit."""
    verdict = result.get('verdict')
    if verdict not in ('pass', 'fail'):
        return None
    state = 'success' if verdict == 'pass' else 'failure'
    if result.get('mode') == 'workflows':
        report = result.get('workflows') or {}
        jobs = [job for event in report.get('events') or [] for job in event['jobs']]
        passed = sum(job['result'] == 'success' for job in jobs)
        scope = f" (only job {report['filter_job']})" if report.get('filter_job') else ''
        text = f"{passed}/{len(jobs)} workflow jobs passed in {len(report.get('events') or [])} events{scope}, act {report.get('act_pinned', '?')}"
    else:
        entry = (result.get('attempts') or [{}])[-1].get('entry') or {}
        gates = [gate for gate in entry.get('gates') or [] if gate['name'] != 'external links']
        passed = sum(gate['result'] == 'pass' for gate in gates)
        text = f'{passed}/{len(gates)} gates passed (verify-local --race, clean container)'
    if state == 'failure' and result.get('reasons'):
        text += f"; {result['reasons'][0]}"
    return state, text[:140]


def write_status(repo, sha, result, target_url=None):
    """Commit status through the local gh login (no token from project files); never touches branch protection."""
    outcome = status_of(result)
    if outcome is None:
        print('[host-ci] no commit status written (the run did not reach a pass/fail verdict)')
        return False
    state, description = outcome
    context = STATUS_CONTEXTS[result.get('mode') or 'gates']
    command = ['gh', 'api', '-X', 'POST', f'repos/{repo_slug(repo)}/statuses/{sha}',
               '-f', f'state={state}', '-f', f'context={context}', '-f', f'description={description}']
    if target_url:
        command += ['-f', f'target_url={target_url}']
    written = subprocess.run(command, capture_output=True, text=True, cwd=str(repo))
    if written.returncode:
        print(f'[host-ci] could not write the commit status: {written.stderr.strip()[:300]}', file=sys.stderr)
        return False
    print(f'[host-ci] commit status {context} = {state} on {sha[:12]}: {description}')
    return True


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('ref', nargs='?', default='HEAD', help='commit-ish to check (default HEAD)')
    parser.add_argument('--repo', default=str(TOOL_DIR.parent.parent), help='repository holding the commit (default: this checkout)')
    parser.add_argument('--env', default='dev-test', help='environment name in the untracked environments file')
    parser.add_argument('--out', default=None, help='where results are stored (default: build/host-ci)')
    parser.add_argument('--timeout-min', type=float, default=None, help=f"container timeout in minutes (default {LIMIT_DEFAULTS['timeout_min']})")
    parser.add_argument('--min-mem-gib', type=float, default=None, help=f"host preflight: required MemAvailable in GiB (default {LIMIT_DEFAULTS['min_mem_gib']}: the 12 GiB container limit plus margin)")
    parser.add_argument('--min-disk-gib', type=float, default=None, help=f"host preflight: free GiB on the CI directory and Docker root disks (default {LIMIT_DEFAULTS['min_disk_gib']})")
    parser.add_argument('--memory-gib', type=int, default=None, help=f"container memory and swap limit in GiB (default {LIMIT_DEFAULTS['memory_gib']})")
    parser.add_argument('--cpus', type=float, default=None, help=f"container CPU limit (default {LIMIT_DEFAULTS['cpus']})")
    parser.add_argument('--build-timeout-min', type=float, default=None, help=f"image build timeout in minutes (default {LIMIT_DEFAULTS['build_timeout_min']})")
    parser.add_argument('--workflows', action='store_true', help='replay .github/workflows (pull_request and push) with pinned act instead of the gate image')
    parser.add_argument('--act-job', default='', help='with --workflows: only this job (diagnosis; the status says so)')
    parser.add_argument('--no-status', action='store_true', help='do not write the commit status')
    parser.add_argument('--target-url', default=None, help='target_url of the commit status (default none)')
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
        target, ci_dir, host_ci = load_environment(args.env)
        limits = resolve_limits(args, host_ci)
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
                   f'--image-tag {tag} --timeout-min {limits["timeout_min"]:g} --min-mem-gib {limits["min_mem_gib"]:g} '
                   f'--min-disk-gib {limits["min_disk_gib"]:g} --memory-gib {limits["memory_gib"]:g} '
                   f'--cpus {limits["cpus"]:g} --build-timeout-min {limits["build_timeout_min"]:g}')
        if args.workflows:
            command += (f' --mode workflows --act-version {ACT_VERSION} --act-sha256 {ACT_SHA256} --runner-image {RUNNER_IMAGE}'
                        f' --repo-slug {repo_slug(repo)}' + (f' --act-job {args.act_job}' if args.act_job else ''))
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
            if not args.no_status and result.get('sha') == sha:
                write_status(repo, sha, result, args.target_url)
        else:
            print('[host-ci] could not fetch the result files', file=sys.stderr)
        return code
    except UsageError as error:
        print(f'run.py: {error}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
