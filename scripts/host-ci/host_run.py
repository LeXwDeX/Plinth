#!/usr/bin/env python3
"""Runs one gate check in a disposable container on the CI host (#97).

Uploaded and started by scripts/host-ci/run.py; it is not meant to be run by hand.
It needs only Python 3, Docker and flock-style locking on the host, and it knows
nothing about the release roots of other stacks. Everything it creates lives under
the CI root it is given (<root>/run/<id>, <root>/results/<id>, <root>/.lock), plus
containers labelled plinth.host-ci=1 and images named plinth-ci:<12 hex digits>.

Exit status: 0 gates passed; 1 gates failed or the run was not clean; 70 the host
failed the resource preflight; 75 another run holds the lock.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import threading
import time

LABEL = 'plinth.host-ci=1'
CONTAINER_USER = 10001
IMAGE_TAG = re.compile(r'[0-9a-f]{12}')
RUN_ID = re.compile(r'\d{8}T\d{6}Z-[0-9a-f]{12}-[0-9a-f]{4}')
GIB = 1 << 30
STALE_RUN_SECONDS = 24 * 3600
RESULTS_KEPT = 10
# Defaults of every tunable limit (dev_test.md section 2, "Host CI"). scripts/host-ci/run.py passes
# all of them explicitly, so these only matter for a hand run; test_host_ci.py keeps both copies equal.
DEFAULTS = {'cpus': 4, 'memory_gib': 12, 'min_mem_gib': 15, 'min_disk_gib': 10, 'timeout_min': 100, 'build_timeout_min': 40}
# MemAvailable must exceed the container's memory limit by at least this much (host and other
# stacks keep running while the container may use its whole limit). Smaller thresholds are refused.
MIN_MEMORY_MARGIN_GIB = 1
# Host swap with less than this fraction free is reported as a warning; it does not block the run
# (the container has no swap, and MemAvailable does not count swap).
SWAP_WARN_FREE_FRACTION = 0.10


class Stop(Exception):
    """Ends the run with a verdict and an exit status."""

    def __init__(self, status, code):
        super().__init__(status)
        self.status = status
        self.code = code


def say(message):
    print(f'[host-ci] {message}', flush=True)


def docker(*args, check=True, timeout=120, stdin=None):
    result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=timeout, stdin=stdin)
    if check and result.returncode:
        raise RuntimeError(f"docker {' '.join(args[:2])} failed: {result.stderr.strip()[:500]}")
    return result


def meminfo():
    values = {}
    for line in Path('/proc/meminfo').read_text().splitlines():
        key, _, rest = line.partition(':')
        values[key] = int(rest.split()[0]) * 1024
    return values


def free_bytes(path):
    stat = os.statvfs(path)
    return stat.f_bavail * stat.f_frsize


def acquire_lock(root):
    handle = open(root / '.lock', 'a')
    try:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        handle.close()
        return None
    return handle


def remove_tree(path, base):
    """rm -rf of a directory that must sit directly below base (never follows links)."""
    path = Path(path)
    if path.parent != Path(base) or path.is_symlink() or not path.name:
        raise RuntimeError(f'refusing to remove {path}')
    shutil.rmtree(path, ignore_errors=True)


def sweep(root, own_id):
    """Under the lock nothing of ours is running: clear leftovers of crashed runs."""
    stale = docker('ps', '-aq', '--filter', f'label={LABEL}', check=False).stdout.split()
    for container in stale:
        if re.fullmatch(r'[0-9a-f]{12}', container):
            say(f'removing leftover container {container}')
            docker('rm', '-f', container, check=False)
    runs = root / 'run'
    for entry in runs.iterdir() if runs.is_dir() else []:
        if entry.name != own_id and RUN_ID.fullmatch(entry.name) and time.time() - entry.stat().st_mtime > STALE_RUN_SECONDS:
            say(f'removing stale run directory {entry.name}')
            remove_tree(entry, runs)


def limit_problems(args):
    """Reasons the requested limits are not acceptable; a threshold must never weaken the gate silently."""
    problems = []
    if not 1 <= args.cpus <= 64:
        problems.append('cpus must be between 1 and 64')
    if not 1 <= args.memory_gib <= 256:
        problems.append('memory-gib must be between 1 and 256')
    if args.min_mem_gib < args.memory_gib + MIN_MEMORY_MARGIN_GIB:
        problems.append(f'min-mem-gib must be at least memory-gib + {MIN_MEMORY_MARGIN_GIB} '
                        f'({args.memory_gib + MIN_MEMORY_MARGIN_GIB} GiB)')
    if args.min_disk_gib < 1:
        problems.append('min-disk-gib must be at least 1')
    if not 1 <= args.timeout_min <= 24 * 60:
        problems.append('timeout-min must be between 1 and 1440')
    if not 1 <= args.build_timeout_min <= 24 * 60:
        problems.append('build-timeout-min must be between 1 and 1440')
    return problems


def preflight(root, args):
    mem = meminfo()
    docker_root = Path(docker('info', '--format', '{{.DockerRootDir}}').stdout.strip())
    facts = {
        'mem_available_bytes': mem['MemAvailable'],
        'swap_used_bytes': mem['SwapTotal'] - mem['SwapFree'],
        'ci_disk_free_bytes': free_bytes(root),
        'docker_disk_free_bytes': free_bytes(docker_root),
        'load_average': os.getloadavg()[0],
        'required': {'mem_available_bytes': int(args.min_mem_gib * GIB), 'disk_free_bytes': int(args.min_disk_gib * GIB)},
    }
    say(f"preflight: MemAvailable {facts['mem_available_bytes'] / GIB:.1f} GiB, swap used "
        f"{facts['swap_used_bytes'] / GIB:.1f} GiB, CI disk free {facts['ci_disk_free_bytes'] / GIB:.0f} GiB, "
        f"Docker disk free {facts['docker_disk_free_bytes'] / GIB:.0f} GiB, load {facts['load_average']:.1f}")
    problems = []
    if facts['mem_available_bytes'] < facts['required']['mem_available_bytes']:
        problems.append('available memory')
    for key in ('ci_disk_free_bytes', 'docker_disk_free_bytes'):
        if facts[key] < facts['required']['disk_free_bytes']:
            problems.append(key.replace('_', ' '))
    facts['problems'] = problems
    swap_total = mem['SwapTotal']
    facts['warnings'] = (['swap nearly full'] if swap_total and mem['SwapFree'] < swap_total * SWAP_WARN_FREE_FRACTION else [])
    for warning in facts['warnings']:
        say(f'warning: {warning} (not a failure: the container has no swap)')
    return facts


def ensure_image(run_dir, tag, args):
    name = f'plinth-ci:{tag}'
    found = docker('image', 'inspect', '--format', '{{.Id}}', name, check=False)
    if found.returncode == 0:
        say(f'image {name} exists; reusing it')
        return {'name': name, 'id': found.stdout.strip(), 'built': False, 'build_seconds': 0}
    say(f'building {name} (toolchain or lockfile changed; this needs network and takes a few minutes)')
    started = time.monotonic()
    with open(run_dir / 'context.tar', 'rb') as context:
        build = subprocess.run(
            ['docker', 'build', '--tag', name, '--label', LABEL, '--file', 'Dockerfile.ci', '-'],
            stdin=context, timeout=args.build_timeout_min * 60)
    if build.returncode:
        raise Stop('image_build_failed', 1)
    return {'name': name, 'id': docker('image', 'inspect', '--format', '{{.Id}}', name).stdout.strip(),
            'built': True, 'build_seconds': round(time.monotonic() - started, 1)}


def container_command(name, run_id, image_id, bundle, attempt_dir, args, sha, tree):
    return [
        'create', '--name', name, '--label', LABEL, '--label', f'plinth.host-ci.run={run_id}',
        '--init', '--user', f'{CONTAINER_USER}:{CONTAINER_USER}', '--read-only', '--cap-drop', 'ALL',
        '--security-opt', 'no-new-privileges', '--network', 'none',
        '--cpus', str(args.cpus), '--memory', f'{args.memory_gib}g', '--memory-swap', f'{args.memory_gib}g',
        '--pids-limit', '4096', '--shm-size', '512m',
        # /work and /tmp are tmpfs, not bind mounts: the wirefixture tests refuse a worktree or
        # evidence directory under a bind mount. Their pages count against the memory limit.
        '--tmpfs', f'/work:rw,exec,nosuid,nodev,size=6g,uid={CONTAINER_USER},gid={CONTAINER_USER},mode=0755',
        '--tmpfs', '/tmp:rw,exec,nosuid,nodev,size=4g,mode=1777',
        '--mount', f'type=bind,source={bundle},target=/in/source.bundle,readonly',
        '--mount', f'type=bind,source={attempt_dir}/scratch,target=/scratch',
        '--mount', f'type=bind,source={attempt_dir}/out,target=/out',
        '--env', f'CI_SHA={sha}', '--env', f'CI_TREE={tree}',
        '--log-driver', 'local', '--log-opt', 'max-size=50m', '--log-opt', 'max-file=2',
        image_id]


def configuration_problems(inspected, image_id, bundle, attempt_dir, args):
    """What `docker create` actually produced, against what this tool asked for."""
    host = inspected['HostConfig']
    wanted = {
        'network none': host.get('NetworkMode') == 'none',
        'memory': host.get('Memory') == args.memory_gib * GIB,
        'swap equals memory': host.get('MemorySwap') == args.memory_gib * GIB,
        'cpus': host.get('NanoCpus') == int(args.cpus * 1e9),
        'read-only root': host.get('ReadonlyRootfs') is True,
        'not privileged': host.get('Privileged') is False,
        'caps dropped': host.get('CapDrop') == ['ALL'],
        'no-new-privileges': any(str(item).startswith('no-new-privileges') for item in host.get('SecurityOpt') or []),
        'user': inspected['Config'].get('User') == f'{CONTAINER_USER}:{CONTAINER_USER}',
        'image': inspected.get('Image') == image_id,
        'tmpfs': sorted(host.get('Tmpfs') or {}) == ['/tmp', '/work'],
        'mounts': sorted((m['Type'], m['Source'], m['Destination'], m['RW']) for m in inspected.get('Mounts') or []) == sorted([
            ('bind', str(bundle), '/in/source.bundle', False),
            ('bind', f'{attempt_dir}/scratch', '/scratch', True),
            ('bind', f'{attempt_dir}/out', '/out', True)]),
    }
    return [name for name, ok in wanted.items() if not ok]


def run_attempt(number, ctx):
    args, root, run_dir = ctx['args'], ctx['root'], ctx['run_dir']
    attempt_dir = run_dir / f'a{number}'
    for sub in ('scratch', 'out'):
        (attempt_dir / sub).mkdir(parents=True, mode=0o700)
        os.chown(attempt_dir / sub, CONTAINER_USER, CONTAINER_USER)
    evidence = root / 'results' / args.run_id / f'attempt-{number}'
    evidence.mkdir(parents=True)
    name = f'plinth-ci-{args.run_id}-a{number}'
    ctx['containers'].append(name)
    record = {'attempt': number, 'container': name}

    docker(*container_command(name, args.run_id, ctx['image']['id'], run_dir / 'source.bundle', attempt_dir, args,
                              args.sha, args.tree))
    inspected = json.loads(docker('inspect', name).stdout)[0]
    (evidence / 'created-config.json').write_text(json.dumps(inspected, indent=2, sort_keys=True))
    problems = configuration_problems(inspected, ctx['image']['id'], run_dir / 'source.bundle', attempt_dir, args)
    if problems:
        say(f"attempt {number}: created container differs from the request ({', '.join(problems)})")
        docker('rm', '-f', name, check=False)
        record.update(status='created_config_mismatch', problems=problems)
        return record, False

    say(f'attempt {number}: starting {name} (network none, {args.cpus:g} CPU, {args.memory_gib} GiB, no swap, timeout {args.timeout_min:g} min)')
    started = time.monotonic()
    timed_out = False
    def stop_on_timeout():
        nonlocal timed_out
        timed_out = True
        say(f'attempt {number}: timeout after {args.timeout_min:g} minutes; stopping the container')
        docker('stop', '--time', '20', name, check=False, timeout=60)

    timer = threading.Timer(args.timeout_min * 60, stop_on_timeout)
    timer.start()
    try:
        with open(evidence / 'container.log', 'wb') as log:
            child = subprocess.Popen(['docker', 'start', '--attach', name], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            for chunk in iter(lambda: child.stdout.read1(65536), b''):
                log.write(chunk)
                sys.stdout.buffer.write(chunk)
                sys.stdout.buffer.flush()
            child.wait()
    finally:
        timer.cancel()
    seconds = round(time.monotonic() - started, 1)

    state = json.loads(docker('inspect', name).stdout)[0]['State']
    result = {}
    result_file = attempt_dir / 'out' / 'result.json'
    if result_file.is_file():
        result = json.loads(result_file.read_text())
    for produced in (attempt_dir / 'out').iterdir():
        if produced.is_file() and produced.stat().st_size < 64 * 1024 * 1024:
            shutil.copy2(produced, evidence / produced.name)
    record.update(
        seconds=seconds, timed_out=timed_out, container_exit_code=state['ExitCode'], oom_killed=state['OOMKilled'],
        docker_error=state.get('Error') or None, entry=result,
        status=result.get('status') or ('timeout' if timed_out else 'no_result'))
    docker('rm', '-f', name, check=False)
    # A stage identity mismatch is the known first-launch flake; anything else is a verdict.
    return record, state['ExitCode'] == 20 and not timed_out


def verdict(record):
    entry = record.get('entry') or {}
    reasons = []
    if record.get('container_exit_code') != 0:
        reasons.append(f"container exit code {record.get('container_exit_code')}")
    if record.get('oom_killed'):
        reasons.append('container OOMKilled')
    if (entry.get('cgroup') or {}).get('oom_kill'):
        reasons.append('cgroup oom_kill counter is not zero')
    if record.get('timed_out'):
        reasons.append('timeout')
    if entry.get('status') != 'passed':
        reasons.append(f"entry status {entry.get('status') or 'missing'}")
    for gate in entry.get('gates') or []:
        if gate['result'] != 'pass':
            reasons.append(f"gate {gate['name']}: {gate['result']}")
    if not entry.get('gates'):
        reasons.append('no gate results')
    return reasons


def retain_images(current, keep):
    """Keep the image of this run plus the newest others, at most `keep` in all."""
    names = [line for line in docker('image', 'ls', 'plinth-ci', '--format', '{{.Repository}}:{{.Tag}}').stdout.split()
             if re.fullmatch(r'plinth-ci:[0-9a-f]{12}', line)]
    dated = []
    for name in names:
        created = docker('image', 'inspect', '--format', '{{.Created}}', name, check=False).stdout.strip()
        dated.append((created, name))
    dated.sort(reverse=True)
    others = [name for _, name in dated if name != current]
    removed = []
    for name in others[max(keep - 1, 0):]:
        outcome = docker('rmi', name, check=False)
        if outcome.returncode:
            say(f'warning: could not remove {name}: {outcome.stderr.strip()[:200]}')
        else:
            removed.append(name)
    return removed


def trim_results(root, own_id):
    results = root / 'results'
    entries = sorted((p for p in results.iterdir() if RUN_ID.fullmatch(p.name) and p.is_dir()), key=lambda p: p.name)
    for entry in entries[:-RESULTS_KEPT]:
        if entry.name != own_id:
            remove_tree(entry, results)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--run-id', required=True)
    parser.add_argument('--sha', required=True)
    parser.add_argument('--tree', required=True)
    parser.add_argument('--image-tag', required=True)
    parser.add_argument('--cpus', type=float, default=DEFAULTS['cpus'])
    parser.add_argument('--memory-gib', type=int, default=DEFAULTS['memory_gib'])
    parser.add_argument('--min-mem-gib', type=float, default=DEFAULTS['min_mem_gib'], help='preflight: required MemAvailable in GiB')
    parser.add_argument('--min-disk-gib', type=float, default=DEFAULTS['min_disk_gib'], help='preflight: free GiB on the CI directory and Docker root disks')
    parser.add_argument('--timeout-min', type=float, default=DEFAULTS['timeout_min'], help='the container is stopped after this many minutes')
    parser.add_argument('--build-timeout-min', type=float, default=DEFAULTS['build_timeout_min'])
    parser.add_argument('--keep-images', type=int, default=2)
    args = parser.parse_args()

    root = Path(args.root)
    run_dir = root / 'run' / args.run_id
    if not (re.fullmatch(r'/[A-Za-z0-9_./-]+', args.root) and RUN_ID.fullmatch(args.run_id)
            and re.fullmatch(r'[0-9a-f]{40}', args.sha) and re.fullmatch(r'[0-9a-f]{40}', args.tree)
            and IMAGE_TAG.fullmatch(args.image_tag) and Path(__file__).resolve() == (run_dir / 'host_run.py').resolve()):
        print('host_run: invalid arguments or location', file=sys.stderr)
        return 2
    problems = limit_problems(args)
    if problems:
        print(f"host_run: unacceptable limits: {'; '.join(problems)}", file=sys.stderr)
        remove_tree(run_dir, root / 'run')
        return 2

    def interrupted(number, frame):
        raise Stop('interrupted', 130)

    for sig in (signal.SIGHUP, signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, interrupted)

    def watch_client():
        # run.py keeps our stdin open for the whole run: EOF means the SSH session (and so the
        # client) is gone, and nobody is waiting for this run any more.
        try:
            while sys.stdin.buffer.read(65536):
                pass
        except (OSError, ValueError):
            pass
        os.kill(os.getpid(), signal.SIGHUP)

    threading.Thread(target=watch_client, daemon=True).start()
    (root / 'results').mkdir(exist_ok=True)
    lock = acquire_lock(root)
    if lock is None:
        print('[host-ci] another gate run holds the lock on this host; try again when it ends', flush=True)
        remove_tree(run_dir, root / 'run')
        return 75

    ctx = {'args': args, 'root': root, 'run_dir': run_dir, 'containers': []}
    summary = {'run_id': args.run_id, 'sha': args.sha, 'tree': args.tree, 'command': 'GOFLAGS=-p=4 python3 scripts/verify-local.py --race --force',
               'limits': {'cpus': args.cpus, 'memory_gib': args.memory_gib, 'swap': 'none', 'network': 'none'},
               'started_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    code = 1
    began = time.monotonic()
    try:
        (root / 'results' / args.run_id).mkdir(parents=True, exist_ok=True)
        sweep(root, args.run_id)
        summary['preflight'] = facts = preflight(root, args)
        if facts['problems']:
            summary.update(verdict='not_run', reasons=['preflight failed: ' + ', '.join(facts['problems'])])
            say(summary['reasons'][0] + '; nothing was started')
            raise Stop('preflight_failed', 70)
        summary['image'] = ctx['image'] = ensure_image(run_dir, args.image_tag, args)
        os.chmod(run_dir / 'source.bundle', 0o444)
        attempts = []
        for number in (1, 2):
            record, retry = run_attempt(number, ctx)
            attempts.append(record)
            if record['status'] == 'created_config_mismatch':
                retry = True
            if not retry or number == 2:
                break
            say('identity or configuration mismatch; retrying once with a fresh staging directory (evidence of attempt 1 is kept)')
            remove_tree(run_dir / f'a{number}', run_dir)
        summary['attempts'] = attempts
        reasons = verdict(attempts[-1])
        summary.update(verdict='pass' if not reasons else 'fail', reasons=reasons)
        code = 0 if not reasons else 1
    except Stop as stop:
        summary.setdefault('verdict', 'not_run' if stop.code == 70 else 'fail')
        summary.setdefault('reasons', [stop.status])
        summary['stop'] = stop.status
        code = stop.code
    except Exception as error:  # noqa: BLE001 - the verdict must record any host failure
        summary.update(verdict='fail', reasons=[f'host error: {error}'][:1])
        code = 1
    finally:
        for name in ctx['containers']:
            docker('rm', '-f', name, check=False)
        summary['finished_at'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        summary['total_seconds'] = round(time.monotonic() - began, 1)
        try:
            summary['mem_available_after_bytes'] = meminfo()['MemAvailable']
            if summary.get('image'):
                summary['images_removed'] = retain_images(summary['image']['name'], args.keep_images)
        except Exception as error:  # noqa: BLE001 - cleanup problems are warnings
            say(f'warning: cleanup step failed: {error}')
        (root / 'results' / args.run_id / 'result.json').write_text(json.dumps(summary, indent=2, sort_keys=True) + '\n')
        remove_tree(run_dir, root / 'run')
        try:
            trim_results(root, args.run_id)
        except Exception as error:  # noqa: BLE001
            say(f'warning: result retention failed: {error}')
    say(f"verdict {summary['verdict'].upper()}" + (f": {'; '.join(summary['reasons'])}" if summary.get('reasons') else ''))
    return code


if __name__ == '__main__':
    status = main()
    sys.stdout.flush()
    # The stdin watcher thread blocks in read(); leave without interpreter shutdown so it cannot abort the exit status.
    os._exit(status)
