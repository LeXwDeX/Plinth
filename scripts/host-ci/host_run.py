#!/usr/bin/env python3
"""Runs one gate check in a disposable container on the CI host (#97).

Uploaded and started by scripts/host-ci/run.py; it is not meant to be run by hand.
It needs only Python 3, Docker and flock-style locking on the host, and it knows
nothing about the release roots of other stacks. Everything it creates lives under
the CI root it is given (<root>/run/<id>, <root>/results/<id>, the slot locks <root>/.lock and
<root>/.lock-<k>, <root>/.maint.lock), plus containers labelled plinth.host-ci=1 (and
plinth.host-ci.slot=<k>) and images named plinth-ci:<12 hex digits>. Up to `--slots` runs go in
parallel, one per slot lock; image builds, image retention and results retention serialize on
the short maintenance lock.

Two modes share the slot lock, the preflight, the sweep and the result layout: `gates` (default) runs
scripts/verify-local.py in the offline image; `workflows` replays .github/workflows with a pinned
nektos/act in throwaway containers (not a GitHub runner; dev_test.md section 2, "Host CI").

Exit status: 0 gates passed; 1 gates failed or the run was not clean; 70 the host
failed the resource preflight or MemAvailable fell below the runtime floor and the container was
stopped (host_memory_low); 75 every slot is held by another run.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tarfile
import threading
import time
import urllib.request

LABEL = 'plinth.host-ci=1'
SLOT_LABEL_KEY = 'plinth.host-ci.slot'
MAINT_LOCK = '.maint.lock'
MAINT_POLL_SECONDS = 1
PEAK_POLL_SECONDS = 2
MEM_GUARD_POLL_SECONDS = 5
CGROUP_ROOT = Path('/sys/fs/cgroup')
CONTAINER_USER = 10001
IMAGE_TAG = re.compile(r'[0-9a-f]{12}')
RUN_ID = re.compile(r'\d{8}T\d{6}Z-[0-9a-f]{12}-[0-9a-f]{4}')
GIB = 1 << 30
STALE_RUN_SECONDS = 24 * 3600
RESULTS_KEPT = 10
# Defaults of every tunable limit (dev_test.md section 2, "Host CI"). scripts/host-ci/run.py passes
# all of them explicitly, so these only matter for a hand run; test_host_ci.py keeps both copies equal.
DEFAULTS = {'cpus': 4, 'memory_gib': 10, 'min_mem_gib': 11, 'min_disk_gib': 10, 'timeout_min': 100, 'build_timeout_min': 40, 'slots': 1, 'min_mem_floor_gib': 2}
# MemAvailable must exceed the container's memory limit by at least this much (host and other
# stacks keep running while the container may use its whole limit). Smaller thresholds are refused.
# The preflight adds the headroom other host-ci containers may still claim on top of the threshold.
MIN_MEMORY_MARGIN_GIB = 1
# Host swap with less than this fraction free is reported as a warning; it does not block the run
# (the container has no swap, and MemAvailable does not count swap).
SWAP_WARN_FREE_FRACTION = 0.10
MAX_SLOTS = 4


class Stop(Exception):
    """Ends the run with a verdict and an exit status."""

    def __init__(self, status, code):
        super().__init__(status)
        self.status = status
        self.code = code


EVENTS = ('pull_request', 'push')
ACT_VERSION = re.compile(r'v\d+\.\d+\.\d+')
RUNNER_IMAGE = re.compile(r'[a-z0-9./_-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}')
REPO_SLUG = re.compile(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+')
ANSI = re.compile(r'\x1b\[[0-9;]*[A-Za-z]')
JOB_RESULT = re.compile(r'^\[(?P<job>[^\]]+)\].*\bJob (?P<result>succeeded|failed)\b', re.M)


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


def slot_label(slot):
    return f'{SLOT_LABEL_KEY}={slot}'


def acquire_lock(root, slots=1):
    """(handle, slot) of the first free run slot, or None when all `slots` are held.

    Slot 0 keeps the historic <root>/.lock name, so a client from before slots existed still
    excludes slot 0; slot k > 0 uses <root>/.lock-<k>. The lock lives as long as the handle."""
    for slot in range(slots):
        handle = open(root / ('.lock' if slot == 0 else f'.lock-{slot}'), 'a')
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            handle.close()
            continue
        return handle, slot
    return None


@contextlib.contextmanager
def maintenance_lock(root, timeout_s):
    """Short blocking lock for work that changes shared host state (image builds and retention,
    the act download, results retention). Never held across a gate run."""
    handle = open(root / MAINT_LOCK, 'a')
    deadline = time.monotonic() + timeout_s
    while True:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except BlockingIOError:
            if time.monotonic() >= deadline:
                handle.close()
                raise Stop('maintenance_lock_timeout', 1) from None
            time.sleep(MAINT_POLL_SECONDS)
    try:
        yield
    finally:
        handle.close()


def remove_tree(path, base):
    """rm -rf of a directory that must sit directly below base (never follows links)."""
    path = Path(path)
    if path.parent != Path(base) or path.is_symlink() or not path.name:
        raise RuntimeError(f'refusing to remove {path}')
    shutil.rmtree(path, ignore_errors=True)


def slot_containers(slot):
    """Short ids of labelled containers that belong to `slot`. Slot 0 also owns containers with no
    slot label: those come from a client older than slots, which only ever ran under the slot 0 lock."""
    rows = docker('ps', '-a', '--filter', f'label={LABEL}', '--format', '{{.ID}} {{.Label "' + SLOT_LABEL_KEY + '"}}', check=False).stdout.splitlines()
    owned = []
    for row in rows:
        parts = row.split()
        if not parts or not re.fullmatch(r'[0-9a-f]{12}', parts[0]):
            continue
        label = parts[1] if len(parts) > 1 else ''
        if label == str(slot) or (slot == 0 and not label):
            owned.append(parts[0])
    return owned


def sweep(root, own_id, slot=0):
    """Under the slot lock nothing of this slot is running: clear its leftovers of crashed runs.
    Containers of other slots belong to runs that may be alive and are never touched."""
    for container in slot_containers(slot):
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
    if not 1 <= args.min_mem_floor_gib <= args.min_mem_gib:
        problems.append('min-mem-floor-gib must be between 1 and min-mem-gib')
    if not 1 <= args.slots <= MAX_SLOTS:
        problems.append(f'slots must be between 1 and {MAX_SLOTS}')
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


def other_run_reserves(own_run_id):
    """Headroom the other running host-ci containers may still claim: limit minus current usage.

    A container without a limit contributes nothing (this tool always sets one); one whose usage
    cannot be read is assumed to be at zero, so its whole limit is reserved."""
    ids = [i for i in docker('ps', '-q', '--filter', f'label={LABEL}', check=False).stdout.split() if re.fullmatch(r'[0-9a-f]{12}', i)]
    if not ids:
        return []
    try:
        inspected = json.loads(docker('inspect', *ids, check=False).stdout or '[]')
    except ValueError:
        inspected = []
    stats = docker('stats', '--no-stream', '--format', '{{.ID}} {{.MemUsage}}', *ids, check=False, timeout=30).stdout.splitlines()
    usage = {}
    for row in stats:
        short, _, used = row.partition(' ')
        usage[short] = parse_size(used.split('/')[0])
    reserves = []
    for item in inspected:
        labels = (item.get('Config') or {}).get('Labels') or {}
        if labels.get('plinth.host-ci.run') == own_run_id:
            continue
        limit = (item.get('HostConfig') or {}).get('Memory') or 0
        short = str(item.get('Id', ''))[:12]
        used = usage.get(short, 0)
        reserves.append({'container': short, 'run': labels.get('plinth.host-ci.run'), 'slot': labels.get(SLOT_LABEL_KEY),
                         'limit_bytes': limit, 'usage_bytes': used, 'reserve_bytes': max(0, limit - used)})
    return reserves


def preflight(root, args):
    mem = meminfo()
    others = other_run_reserves(args.run_id)
    reserved = sum(item['reserve_bytes'] for item in others)
    docker_root = Path(docker('info', '--format', '{{.DockerRootDir}}').stdout.strip())
    facts = {
        'mem_available_bytes': mem['MemAvailable'],
        'swap_used_bytes': mem['SwapTotal'] - mem['SwapFree'],
        'ci_disk_free_bytes': free_bytes(root),
        'docker_disk_free_bytes': free_bytes(docker_root),
        'load_average': os.getloadavg()[0],
        'reserved_for_other_runs_bytes': reserved,
        'other_runs': others,
        'required': {'base_mem_available_bytes': int(args.min_mem_gib * GIB),
                     'mem_available_bytes': int(args.min_mem_gib * GIB) + reserved, 'disk_free_bytes': int(args.min_disk_gib * GIB)},
    }
    say(f"preflight: MemAvailable {facts['mem_available_bytes'] / GIB:.1f} GiB, swap used "
        f"{facts['swap_used_bytes'] / GIB:.1f} GiB, CI disk free {facts['ci_disk_free_bytes'] / GIB:.0f} GiB, "
        f"Docker disk free {facts['docker_disk_free_bytes'] / GIB:.0f} GiB, load {facts['load_average']:.1f}, "
        f"reserved for {len(others)} other container(s) {reserved / GIB:.1f} GiB, required MemAvailable {facts['required']['mem_available_bytes'] / GIB:.1f} GiB")
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


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def ensure_act(root, version, sha256):
    """The pinned act binary under <root>/tools; the release tarball must match the pinned digest."""
    target = root / 'tools' / f'act-{version}'
    binary = target / 'act'
    if binary.is_file() and os.access(binary, os.X_OK):
        return binary
    target.mkdir(parents=True, exist_ok=True)
    tarball = target / 'act.tar.gz'
    url = f'https://github.com/nektos/act/releases/download/{version}/act_Linux_x86_64.tar.gz'
    say(f'downloading act {version}')
    with urllib.request.urlopen(url, timeout=120) as response, open(tarball, 'wb') as out:
        shutil.copyfileobj(response, out)
    if sha256_file(tarball) != sha256:
        tarball.unlink()
        raise Stop('act_checksum_mismatch', 1)
    with tarfile.open(tarball) as archive:
        member = archive.getmember('act')
        if not member.isfile():
            raise Stop('act_archive_invalid', 1)
        with archive.extractfile(member) as source, open(binary, 'wb') as out:
            shutil.copyfileobj(source, out)
    os.chmod(binary, 0o755)
    tarball.unlink()
    return binary


def checkout_source(run_dir, args):
    """Fresh checkout of the exact commit from the uploaded bundle, with an origin act can read the slug from."""
    src = run_dir / 'src'
    def git(*cmd):
        return subprocess.run(['git', '-C', str(src), *cmd], capture_output=True, text=True, timeout=300)
    src.mkdir()
    for cmd in (('init', '-q'), ('fetch', '-q', '--no-tags', str(run_dir / 'source.bundle'), '+refs/plinth-host-ci/*:refs/remotes/bundle/*'),
                ('checkout', '-q', '-B', 'main', args.sha), ('remote', 'add', 'origin', f'https://github.com/{args.repo_slug}.git')):
        outcome = git(*cmd)
        if outcome.returncode:
            raise RuntimeError(f"git {cmd[0]} failed: {outcome.stderr.strip()[:300]}")
    if git('rev-parse', 'HEAD').stdout.strip() != args.sha or git('rev-parse', 'HEAD^{tree}').stdout.strip() != args.tree:
        raise Stop('stage_mismatch', 20)
    return src


def event_payload(event, sha):
    if event == 'pull_request':
        return {'action': 'synchronize', 'number': 1, 'pull_request': {
            'number': 1, 'head': {'sha': sha, 'ref': 'plinth-host-ci'}, 'base': {'ref': 'main'}}}
    return {'ref': 'refs/heads/main', 'after': sha, 'before': '0' * 40}


def parse_size(text):
    match = re.fullmatch(r'([0-9.]+)\s*([KMGT]?i?B)', text.strip())
    if not match:
        return 0
    scale = {'B': 1, 'KB': 1e3, 'MB': 1e6, 'GB': 1e9, 'TB': 1e12, 'KiB': 1 << 10, 'MiB': 1 << 20, 'GiB': 1 << 30, 'TiB': 1 << 40}
    return int(float(match.group(1)) * scale[match.group(2)])


def parse_act_output(text):
    """Per-job results from act's log: {job: 'success'|'failure'}; the last line for a job wins."""
    results = {}
    for match in JOB_RESULT.finditer(ANSI.sub('', text)):
        results[match.group('job').split('/')[-1].strip()] = 'success' if match.group('result') == 'succeeded' else 'failure'
    return results


def parse_job_list(text):
    """[(job id, job name)] from `act -l` (columns: Stage, Job ID, Job name, Workflow name, ...)."""
    jobs = []
    for line in ANSI.sub('', text).splitlines():
        parts = re.split(r'\s{2,}', line.strip())
        if len(parts) >= 3 and parts[0].isdigit():
            jobs.append((parts[1], parts[2]))
    return jobs


def act_list_jobs(act, src, event):
    outcome = subprocess.run([str(act), '-l', event, '-C', str(src)], capture_output=True, text=True, timeout=120)
    if outcome.returncode:
        raise RuntimeError(f'act -l failed: {outcome.stderr.strip()[:300]}')
    return parse_job_list(outcome.stdout)


def read_memory_peak(container_id):
    """cgroup v2 memory.peak of a running container in bytes, or None when it cannot be found.

    The cgroup disappears when the container process exits, so this is only readable during the run."""
    if not container_id or not re.fullmatch(r'[0-9a-f]{64}', container_id):
        return None
    for pattern in (f'system.slice/docker-{container_id}.scope', f'docker/{container_id}', f'*/docker-{container_id}.scope',
                    f'*/*/docker-{container_id}.scope'):
        for directory in CGROUP_ROOT.glob(pattern):
            try:
                return int((directory / 'memory.peak').read_text().strip())
            except (OSError, ValueError):
                continue
    return None


def sample_memory_peak(stop, container_id):
    """Highest memory.peak read until `stop` is set (the counter only grows), or None if never readable."""
    peak = None
    while True:
        value = read_memory_peak(container_id)
        if value is not None:
            peak = value if peak is None else max(peak, value)
        if stop.wait(PEAK_POLL_SECONDS):
            return peak


def guard_host_memory(stop, floor_bytes, container, state):
    """Runtime guard: while the gate container runs, kill it (and only it) when the host's MemAvailable
    drops below the floor. Other stacks on the host grow without a limit, so the start-time preflight
    is not enough. `state` collects the lowest reading ('min') and the first one under the floor ('low')."""
    while True:
        try:
            available = meminfo()['MemAvailable']
        except (OSError, KeyError, ValueError):
            available = None
        if available is not None:
            state['min'] = available if state.get('min') is None else min(state['min'], available)
            if available < floor_bytes and not state.get('killed'):
                state.setdefault('low', available)
                say(f'host MemAvailable {available / GIB:.2f} GiB is below the {floor_bytes / GIB:g} GiB floor; killing {container}')
                state['killed'] = docker('kill', container, check=False, timeout=60).returncode == 0
        if stop.wait(MEM_GUARD_POLL_SECONDS):
            return


def peak_container_memory(stop, label):
    """Highest summed MemUsage of labelled containers seen while `stop` is unset."""
    peak = 0
    while not stop.wait(5):
        ids = [i for i in docker('ps', '-q', '--filter', f'label={label}', check=False, timeout=30).stdout.split() if re.fullmatch(r'[0-9a-f]{12}', i)]
        if not ids:
            continue
        rows = docker('stats', '--no-stream', '--format', '{{.MemUsage}}', *ids, check=False, timeout=30).stdout.split('\n')
        peak = max(peak, sum(parse_size(row.split('/')[0]) for row in rows if row.strip()))
    return peak


# GitHub's ubuntu-latest has the sqlite3 command line; the pinned act image does not, and scripts/tests need it.
RUNNER_DOCKERFILE = "FROM {base}\nRUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends sqlite3 \\\n && rm -rf /var/lib/apt/lists/*\n"


def ensure_runner_image(args):
    """The pinned act runner image plus the few packages ubuntu-latest has that it lacks; tag derives from the recipe."""
    recipe = RUNNER_DOCKERFILE.format(base=args.runner_image)
    name = f'plinth-ci-act:{hashlib.sha256(recipe.encode()).hexdigest()[:12]}'
    if docker('image', 'inspect', name, check=False).returncode != 0:
        say(f'building {name} (pinned act runner image plus sqlite3)')
        build = subprocess.run(['docker', 'build', '--tag', name, '--label', LABEL, '-'], input=recipe.encode(),
                               timeout=args.build_timeout_min * 60)
        if build.returncode:
            raise Stop('runner_image_build_failed', 1)
    return name


def act_command(act, src, event, event_file, args):
    command = [str(act), event, '-C', str(src), '-e', str(event_file), '-P', f'ubuntu-latest={args.runner_name}',
               '--container-architecture', 'linux/amd64', '--concurrent-jobs', '1', '--container-daemon-socket', '-', '--rm', '--pull=false',
               '--action-cache-path', str(args.act_cache),
               '--container-options', f'--cpus {args.cpus:g} --memory {args.memory_gib}g --memory-swap {args.memory_gib}g --pids-limit 4096 '
                                      f'--security-opt no-new-privileges --label {LABEL} --label plinth.host-ci.run={args.run_id} '
                                      f'--label {slot_label(args.slot)}']
    if args.act_job:
        command += ['-j', args.act_job]
    return command


def run_workflows(ctx):
    """Replays the workflows for each event; returns a report with per-job results."""
    args, root, run_dir = ctx['args'], ctx['root'], ctx['run_dir']
    with maintenance_lock(root, args.build_timeout_min * 60):
        act = ensure_act(root, args.act_version, args.act_sha256)
    version = subprocess.run([str(act), '--version'], capture_output=True, text=True, timeout=30).stdout.strip()
    docker('pull', '-q', args.runner_image, timeout=1800)
    with maintenance_lock(root, args.build_timeout_min * 60):
        args.runner_name = ensure_runner_image(args)
    src = checkout_source(run_dir, args)
    evidence = root / 'results' / args.run_id
    volumes_before = set(docker('volume', 'ls', '-q', '--filter', 'name=act-', check=False).stdout.split())
    networks_before = set(docker('network', 'ls', '-q', '--filter', 'name=act-', check=False).stdout.split())
    report = {'act_version': version, 'act_pinned': args.act_version, 'runner_image': args.runner_image, 'runner_derived': args.runner_name,
              'image_id': docker('image', 'inspect', '--format', '{{.Id}}', args.runner_name).stdout.strip(),
              'filter_job': args.act_job or None, 'events': [], 'peak_memory_bytes': 0}
    try:
        for event in EVENTS:
            listed = act_list_jobs(act, src, event)
            expected = [job for job in listed if not args.act_job or args.act_job in job]
            if not expected:
                raise RuntimeError(f'job {args.act_job} is not part of the {event} event')
            event_file = run_dir / f'event-{event}.json'
            event_file.write_text(json.dumps(event_payload(event, args.sha)))
            say(f'workflows: replaying {event} ({len(expected)} jobs: {", ".join(job_id for job_id, _ in expected)})')
            stop = threading.Event()
            peak = {'value': 0}
            sampler = threading.Thread(target=lambda: peak.__setitem__('value', peak_container_memory(stop, slot_label(args.slot))), daemon=True)
            sampler.start()
            started = time.monotonic()
            timed_out = False
            log_path = evidence / f'act-{event}.log'
            with open(log_path, 'wb') as log:
                child = subprocess.Popen(act_command(act, src, event, event_file, args), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                timer = threading.Timer(args.timeout_min * 60, lambda: (child.terminate(), ctx.__setitem__('timed_out', True)))
                timer.start()
                try:
                    for chunk in iter(lambda: child.stdout.read1(65536), b''):
                        log.write(chunk)
                        sys.stdout.buffer.write(chunk)
                        sys.stdout.buffer.flush()
                    child.wait()
                finally:
                    timer.cancel()
                    timed_out = bool(ctx.pop('timed_out', False))
            stop.set()
            sampler.join(10)
            results = parse_act_output(log_path.read_text(errors='replace'))
            report['peak_memory_bytes'] = max(report['peak_memory_bytes'], peak['value'])
            report['events'].append({
                'event': event, 'exit_code': child.returncode, 'timed_out': timed_out, 'seconds': round(time.monotonic() - started, 1),
                'jobs': [{'name': job_id, 'job_name': job_name, 'result': results.get(job_name, results.get(job_id, 'not_run'))}
                         for job_id, job_name in expected],
                'peak_memory_bytes': peak['value']})
            for name in slot_containers(args.slot):
                docker('rm', '-f', name, check=False)
            if timed_out or child.returncode == 143:
                break
    finally:
        for kind, before in (('volume', volumes_before), ('network', networks_before)):
            now = set(docker(kind, 'ls', '-q', '--filter', 'name=act-', check=False).stdout.split())
            # Another slot's act run may have created some of these; Docker refuses to remove a volume
            # or network that is still in use, which is what keeps this from hurting a parallel run.
            for item in sorted(now - before):
                docker(kind, 'rm', item, check=False)
    return report


def workflows_verdict(report):
    reasons = []
    if not report.get('events'):
        reasons.append('no events were replayed')
    for event in report.get('events') or []:
        label = event['event']
        if event.get('timed_out'):
            reasons.append(f'{label}: timeout')
        if event.get('exit_code') != 0:
            reasons.append(f"{label}: act exit code {event.get('exit_code')}")
        if not event.get('jobs'):
            reasons.append(f'{label}: no jobs')
        for job in event.get('jobs') or []:
            if job['result'] != 'success':
                reasons.append(f"{label}/{job['name']}: {job['result']}")
    if len(report.get('events') or []) != len(EVENTS):
        reasons.append('not every event was replayed')
    return reasons


IMAGE_IN_USE = 'image-in-use'


def ensure_image(run_dir, tag, args):
    """The gates image; the caller holds the maintenance lock. The marker file tells another run's
    retention that this run needs the image even before its container exists."""
    name = f'plinth-ci:{tag}'
    (run_dir / IMAGE_IN_USE).write_text(name)
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
        '--label', slot_label(args.slot),
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
    container_id = inspected.get('Id')
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
    stop_sampling = threading.Event()
    sampled = {'peak': None}
    sampler = threading.Thread(target=lambda: sampled.__setitem__('peak', sample_memory_peak(stop_sampling, container_id)), daemon=True)
    sampler.start()
    guard = {'min': None}
    guard_thread = threading.Thread(target=guard_host_memory, args=(stop_sampling, int(args.min_mem_floor_gib * GIB), name, guard), daemon=True)
    guard_thread.start()
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
        stop_sampling.set()
        sampler.join(10)
        guard_thread.join(10)
    seconds = round(time.monotonic() - started, 1)
    # The container's cgroup (and its memory.peak) is gone once it has exited, so the host's value is
    # the last one sampled while it ran; the entry script's own end-of-run reading covers the tail.
    final = read_memory_peak(container_id)

    state = json.loads(docker('inspect', name).stdout)[0]['State']
    result = {}
    result_file = attempt_dir / 'out' / 'result.json'
    if result_file.is_file():
        result = json.loads(result_file.read_text())
    candidates = [value for value in (sampled['peak'], final, (result.get('cgroup') or {}).get('memory_peak_bytes'))
                  if isinstance(value, int) and not isinstance(value, bool)]
    memory_peak = max(candidates) if candidates else None
    for produced in (attempt_dir / 'out').iterdir():
        if produced.is_file() and produced.stat().st_size < 64 * 1024 * 1024:
            shutil.copy2(produced, evidence / produced.name)
    record.update(
        seconds=seconds, timed_out=timed_out, container_exit_code=state['ExitCode'], oom_killed=state['OOMKilled'], memory_peak_bytes=memory_peak,
        docker_error=state.get('Error') or None, entry=result, mem_available_min_bytes=guard.get('min'),
        status=result.get('status') or ('timeout' if timed_out else 'no_result'))
    if guard.get('low') is not None:
        record.update(status='host_memory_low', host_memory_low={
            'observed_mem_available_bytes': guard['low'], 'floor_bytes': int(args.min_mem_floor_gib * GIB), 'container_killed': bool(guard.get('killed'))})
    docker('rm', '-f', name, check=False)
    # A stage identity mismatch is the known first-launch flake; anything else is a verdict.
    return record, state['ExitCode'] == 20 and not timed_out and guard.get('low') is None


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
    low = record.get('host_memory_low')
    if low:
        reasons.append(f"host memory low: MemAvailable {low['observed_mem_available_bytes'] / GIB:.2f} GiB under the "
                       f"{low['floor_bytes'] / GIB:g} GiB floor; the container was stopped")
    if entry.get('status') != 'passed':
        reasons.append(f"entry status {entry.get('status') or 'missing'}")
    for gate in entry.get('gates') or []:
        if gate['result'] != 'pass':
            reasons.append(f"gate {gate['name']}: {gate['result']}")
    if not entry.get('gates'):
        reasons.append('no gate results')
    return reasons


def images_in_use_by_runs(root):
    """Images that live runs (other slots included) have marked, before their container exists."""
    runs = Path(root) / 'run'
    names = set()
    for marker in runs.glob(f'*/{IMAGE_IN_USE}') if runs.is_dir() else []:
        try:
            names.add(marker.read_text().strip())
        except OSError:
            pass
    return names


def retain_images(current, keep, root=None):
    """Keep the image of this run plus the newest others, at most `keep` in all. Images another live
    run has marked are kept too, and the plain `docker rmi` (never -f) refuses any image a container
    still uses. The caller holds the maintenance lock."""
    protected = images_in_use_by_runs(root) if root is not None else set()
    names = [line for line in docker('image', 'ls', 'plinth-ci', '--format', '{{.Repository}}:{{.Tag}}').stdout.split()
             if re.fullmatch(r'plinth-ci:[0-9a-f]{12}', line)]
    dated = []
    for name in names:
        created = docker('image', 'inspect', '--format', '{{.Created}}', name, check=False).stdout.strip()
        dated.append((created, name))
    dated.sort(reverse=True)
    others = [name for _, name in dated if name != current and name not in protected]
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
        # A run still in progress (slots) has no result.json yet; only abandoned ones are trimmed.
        unfinished = not (entry / 'result.json').exists() and time.time() - entry.stat().st_mtime <= STALE_RUN_SECONDS
        if entry.name != own_id and not unfinished:
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
    parser.add_argument('--slots', type=int, default=DEFAULTS['slots'], help='parallel run slots on this host (1-4)')
    parser.add_argument('--min-mem-floor-gib', type=float, default=DEFAULTS['min_mem_floor_gib'],
                        help='runtime guard: the container is killed when host MemAvailable falls below this many GiB')
    parser.add_argument('--keep-images', type=int, default=2)
    parser.add_argument('--mode', choices=('gates', 'workflows'), default='gates')
    parser.add_argument('--act-version', default='')
    parser.add_argument('--act-sha256', default='')
    parser.add_argument('--runner-image', default='')
    parser.add_argument('--repo-slug', default='')
    parser.add_argument('--act-job', default='')
    args = parser.parse_args()
    args.act_cache = Path(args.root) / 'act-cache'

    root = Path(args.root)
    run_dir = root / 'run' / args.run_id
    if not (re.fullmatch(r'/[A-Za-z0-9_./-]+', args.root) and RUN_ID.fullmatch(args.run_id)
            and re.fullmatch(r'[0-9a-f]{40}', args.sha) and re.fullmatch(r'[0-9a-f]{40}', args.tree)
            and IMAGE_TAG.fullmatch(args.image_tag) and Path(__file__).resolve() == (run_dir / 'host_run.py').resolve()):
        print('host_run: invalid arguments or location', file=sys.stderr)
        return 2
    problems = limit_problems(args)
    if args.mode == 'workflows':
        if not (ACT_VERSION.fullmatch(args.act_version) and re.fullmatch(r'[0-9a-f]{64}', args.act_sha256)
                and RUNNER_IMAGE.fullmatch(args.runner_image) and REPO_SLUG.fullmatch(args.repo_slug)
                and (not args.act_job or re.fullmatch(r'[\w-]+', args.act_job))):
            problems.append('workflows mode needs a pinned act version and sha256, a digest-pinned runner image and a repo slug')
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
    held = acquire_lock(root, args.slots)
    if held is None:
        print(f'[host-ci] all {args.slots} run slot(s) on this host are busy; try again when one run ends', flush=True)
        remove_tree(run_dir, root / 'run')
        return 75
    lock, args.slot = held

    ctx = {'args': args, 'root': root, 'run_dir': run_dir, 'containers': []}
    summary = {'mode': args.mode, 'run_id': args.run_id, 'sha': args.sha, 'tree': args.tree, 'command': 'GOFLAGS=-p=4 python3 scripts/verify-local.py --race --force',
               'slot': args.slot, 'memory_peak_bytes': None, 'mem_available_min_bytes': None,
               'limits': {'cpus': args.cpus, 'memory_gib': args.memory_gib, 'slots': args.slots, 'min_mem_floor_gib': args.min_mem_floor_gib, 'swap': 'none', 'network': 'none'},
               'started_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    code = 1
    began = time.monotonic()
    try:
        (root / 'results' / args.run_id).mkdir(parents=True, exist_ok=True)
        sweep(root, args.run_id, args.slot)
        summary['preflight'] = facts = preflight(root, args)
        if facts['problems']:
            summary.update(verdict='not_run', reasons=['preflight failed: ' + ', '.join(facts['problems'])])
            say(summary['reasons'][0] + '; nothing was started')
            raise Stop('preflight_failed', 70)
        os.chmod(run_dir / 'source.bundle', 0o444)
        if args.mode == 'workflows':
            summary['workflows'] = report = run_workflows(ctx)
            reasons = workflows_verdict(report)
            summary['command'] = f'act {" ".join(EVENTS)} (pinned {args.act_version}) on .github/workflows'
        else:
            with maintenance_lock(root, args.build_timeout_min * 60):
                summary['image'] = ctx['image'] = ensure_image(run_dir, args.image_tag, args)
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
            summary['memory_peak_bytes'] = attempts[-1].get('memory_peak_bytes')
            summary['mem_available_min_bytes'] = attempts[-1].get('mem_available_min_bytes')
            reasons = verdict(attempts[-1])
            if attempts[-1].get('status') == 'host_memory_low':
                # The host, not the commit, ended the run: no pass/fail verdict, so no commit status either.
                summary.update(verdict='not_run', reasons=reasons)
                say(next(reason for reason in reasons if reason.startswith('host memory low')))
                raise Stop('host_memory_low', 70)
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
                with maintenance_lock(root, args.build_timeout_min * 60):
                    summary['images_removed'] = retain_images(summary['image']['name'], args.keep_images, root)
        except Exception as error:  # noqa: BLE001 - cleanup problems are warnings
            say(f'warning: cleanup step failed: {error}')
        (root / 'results' / args.run_id / 'result.json').write_text(json.dumps(summary, indent=2, sort_keys=True) + '\n')
        remove_tree(run_dir, root / 'run')
        try:
            with maintenance_lock(root, args.build_timeout_min * 60):
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
