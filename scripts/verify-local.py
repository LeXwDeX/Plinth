#!/usr/bin/env python3
"""dev_test.md §2 local gates, with conservative worktree-private reuse.

The default (pre-push) run executes the gates §2 marks local. The race gate is
CI-only there (slow, and it repeats the go test suite); --race adds it here.
A receipt optimizes trusted local work; CI always executes its own gates. Only
one latest success is retained. Unbindable inputs fall back to full validation.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import select
import shutil
import stat
import subprocess
import sys
import tempfile
import time

# The gates dev_test.md §2 marks local, in §2 order; the default and pre-push run.
GATES = [
    ('frontend typecheck', ['pnpm', 'exec', 'tsc', '--noEmit', '--incremental', 'false'], {}),
    ('frontend test', ['pnpm', 'test'], {}),
    ('frontend build', ['pnpm', 'build'], {}),
    ('go vet', ['go', 'vet', './...'], {}),
    # The per-package test timeout is raised for the local gate only: parallel sessions on one
    # workstation can slow a package (httpapi, sqlite under -race) past Go's default 10m.
    ('go test', ['go', 'test', '-count=1', '-timeout=30m', './...'], {}),
    ('go build', ['go', 'build', '-trimpath', '-o', os.devnull, './cmd/art-ai'], {'CGO_ENABLED': '0'}),
    # #261: tracked files carry no internal identifier (about 2 seconds).
    ('public tree', ['bash', 'scripts/check-public-tree.sh'], {}),
    # #86: the release/gate scripts' own unit tests (no bytecode: the gate must not write into the tree it fingerprints).
    ('scripts tests', ['python3', '-m', 'unittest', 'discover', '-s', 'scripts/tests', '-p', 'test_*.py'], {'PYTHONDONTWRITEBYTECODE': '1'}),
]
# CI-only in dev_test.md §2: the slow gate, running the same tests as go test.
# --race opts in locally; it then runs right after go test, as in §2.
RACE_GATE = ('go test race', ['go', 'test', '-race', '-count=1', '-timeout=30m', './...'], {'CGO_ENABLED': '1'})
VERSION = 3
HOOK_PROTOCOL = 1
ACTIVE_MONITOR = None
MAX_AGE_SECONDS = 24 * 60 * 60
# Git injects these during hook dispatch; none selects compilation behavior.
ENV_BOOKKEEPING = {'_', 'SHLVL', 'OLDPWD', 'PWD', 'GIT_EXEC_PATH', 'GIT_PREFIX',
                   'GIT_DIR', 'GIT_COMMON_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE'}
# Repository-selecting variables Git sets for hooks; gates run with the worktree as cwd instead.
HOOK_GIT_ENV = {'GIT_DIR', 'GIT_COMMON_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_PREFIX'}
# Gate concurrency across all worktrees of this clone (shared Git common dir).
# Only scheduling: it selects no gate, command or compilation input, so it is
# excluded from the receipt environment digest. 0 disables queueing.
SLOTS_ENV = 'ART_AI_VERIFY_SLOTS'
DEFAULT_SLOTS = 1
QUEUE_POLL_SECONDS = 1.0
# Vite build regenerates this exact directory before any Go gate. Everything
# tracked is still bound, even under these exclusions. Agent metadata is outside
# the product/build contract. No suffix-based source exclusion is permitted.
SOURCE_EXCLUSIONS = {'.git', 'node_modules', 'build/go-web', 'tsconfig.tsbuildinfo', '.codex', '.claude',
                     '.opencode', '.memory', '.codegraph'}

class GateError(RuntimeError):
    pass

class Unbindable(GateError):
    pass


# Command-line tools the gates call directly (frontend tests shell out to bash and jq;
# git backs the public tree gate). The C compiler is needed only by the race gate (CGO).
REQUIRED_TOOLS = ['git', 'pnpm', 'node', 'go', 'bash', 'jq']
RACE_TOOLS = [('C compiler (gcc or cc, for CGO)', ('gcc', 'cc'))]


def missing_tools(race=False):
    """Names of required tools absent from PATH, so a missing tool is reported up front
    instead of failing deep inside a gate with `jq: command not found`."""
    missing = [name for name in REQUIRED_TOOLS if shutil.which(name) is None]
    if race:
        missing += [label for label, names in RACE_TOOLS if not any(shutil.which(n) for n in names)]
    return missing


def require_tools(race=False):
    missing = missing_tools(race)
    if missing:
        raise GateError('missing required tools: ' + ', '.join(missing)
                        + '; install them first (see CONTRIBUTING.md "Development setup")')


def selected_gates(race=False):
    if not race:
        return list(GATES)
    position = [name for name, _, _ in GATES].index('go test') + 1
    return [*GATES[:position], RACE_GATE, *GATES[position:]]


def gate_manifest():
    """The gate set as data: names, argv and extra environment, in run order."""
    def entry(gate):
        name, argv, env = gate
        return {'name': name, 'argv': argv, 'env': env}
    return {'gates': [entry(gate) for gate in GATES], 'race': entry(RACE_GATE)}


def gate_names(gates):
    return [name for name, _, _ in gates]


def capture(argv, root, input_data=None, env_overrides=None, env_remove=()):
    if argv[0] == 'git':
        argv = ['git', '--no-replace-objects', *argv[1:]]
    result = subprocess.run(argv, cwd=root, input=input_data, capture_output=True, check=False,
                            env={**{k: value for k, value in os.environ.items() if k not in env_remove},
                                 **(env_overrides or {})})
    if result.returncode:
        raise Unbindable(f'input inspection failed: {argv[0]} {argv[1]} (exit {result.returncode})')
    return result.stdout


def git(root, *args):
    return capture(['git', *args], root).decode().strip()


def digest_json(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def file_digest(path):
    result = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def signature(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_size,
            info.st_mtime_ns, info.st_ctime_ns)


class InputMonitor:
    """Node's native recursive filesystem events distinguish pnpm write probes
    from real transient source changes. Stat guards still bind external trees.
    If observation fails, full gates remain available but no receipt is saved.
    """
    SCRIPT = r"""
const fs = require('node:fs');
const root = process.argv[1];
const excluded = JSON.parse(process.argv[2]);
const marker = '_tmp_' + process.pid + '_' + require('node:crypto').randomBytes(16).toString('hex');
let dirty = false, stopping = false, finalizing = false;
const configurationWatches = new Map();
const reasons = new Set();
function markDirty(reason) { dirty = true; reasons.add(reason); }
function finish(failed) {
  watcher.close();
  for (const [directory, record] of configurationWatches) {
    try {
      const now = fs.lstatSync(directory);
      if (now.dev !== record.guard.dev || now.ino !== record.guard.ino || now.mode !== record.guard.mode || now.birthtimeMs !== record.guard.birthtimeMs) markDirty('directory-identity');
    } catch { markDirty('directory-unavailable'); }
    record.watcher.close();
  }
  if (failed) reasons.add('marker-missing');
  process.stdout.write(dirty || failed ? 'dirty:' + [...reasons].sort().join(',') + '\n' : 'clean\n',
    error => process.exit(error ? 1 : 0));
}
const watcher = fs.watch(root, {recursive: true}, (kind, name) => {
  const p = name ? name.toString().replaceAll('\\', '/') : '';
  if (p === marker) {
    if (stopping && !finalizing) {
      finalizing = true;
      try { fs.unlinkSync(root + '/' + marker); } catch (error) { if (error.code !== 'ENOENT') markDirty('marker-cleanup'); }
      // Let all callbacks in the filesystem event batch drain before closing.
      setImmediate(() => finish(false));
    }
    return;
  }
  // macOS reports the watched directory's own metadata as change:basename.
  // A real child of that name still has rename events (or exists and is dirty).
  if (kind === 'change' && p === require('node:path').basename(root) && !fs.existsSync(root + '/' + p)) return;
  if (/^_tmp_[0-9]+_[0-9a-f]+$/.test(p)) return; // pnpm write-permission probes
  if (excluded.some(x => p === x || p.startsWith(x + '/'))) return;
  markDirty(name ? 'source-change' : 'unknown-filename');
});
watcher.on('error', () => { markDirty('watch-error'); });
process.stdout.write('ready\n');
function watchMissing(directory, target) {
  let record = configurationWatches.get(directory);
  if (!record) {
    record = {targets: new Set(), watcher: null, guard: fs.lstatSync(directory)};
    record.watcher = fs.watch(directory, (kind, name) => {
      if (!name) { markDirty('unknown-filename'); return; }
      const first = name.toString().replaceAll('\\', '/').split('/')[0];
      // For a missing nested config, creating/removing its first missing
      // component is already a relevant change. Other desktop files are not.
      if (record.targets.has(first)) markDirty('configuration-target');
    });
    record.watcher.on('error', () => { markDirty('watch-error'); });
    configurationWatches.set(directory, record);
  }
  record.targets.add(target.split('/')[0]);
}
const input = require('node:readline').createInterface({input: process.stdin});
input.on('close', () => { if (!stopping) { watcher.close(); process.exit(1); } });
input.on('line', line => {
  let command;
  try { command = JSON.parse(line); } catch { markDirty('protocol-input'); return; }
  if (command.kind === 'watch') {
    try {
      watchMissing(command.directory, command.target);
      process.stdout.write('watched\n');
    } catch {
      markDirty('configuration-watch-error'); process.stdout.write('failed\n');
    }
    return;
  }
  if (command.kind !== 'stop') { markDirty('protocol-input'); return; }
  stopping = true;
  // FSEvents can drop an event for a file created right after the stream
  // started (observed ~5% of the time when the fingerprint finishes within
  // milliseconds), so the marker is re-created until its event arrives.
  const deadline = Date.now() + 3000;
  const probe = () => {
    if (finalizing) return;
    if (Date.now() >= deadline) {
      try { fs.unlinkSync(root + '/' + marker); } catch {}
      finish(true);
      return;
    }
    try { fs.unlinkSync(root + '/' + marker); } catch {}
    try { fs.writeFileSync(root + '/' + marker, '', {flag: 'wx'}); }
    catch { finish(true); return; }
    setTimeout(probe, 250);
  };
  // All watches use this Node loop. The marker and setImmediate drain its
  // native event batch before every watcher closes; external parents need no
  // writable marker/config files (including system /etc).
  probe();
});
"""
    def __init__(self, root):
        self.root = root.resolve()
        self.process = None
        self.clean = False
        self.reason = 'monitor-unavailable'
        self.finished = False
        self.configuration_directories = set()
        exclusions = sorted((SOURCE_EXCLUSIONS - {'node_modules'}) |
                            {'node_modules/.vite', 'node_modules/.vite-temp'})
        try:
            self.process = subprocess.Popen(['node', '-e', self.SCRIPT, str(self.root), json.dumps(exclusions)],
                                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                            stderr=subprocess.DEVNULL, text=True)
            readable, _, _ = select.select([self.process.stdout], [], [], 10)
            if not readable or self.process.stdout.readline().strip() != 'ready':
                self.stop()
                raise Unbindable('filesystem change monitor unavailable')
        except (OSError, ValueError) as error:
            raise Unbindable('filesystem change monitor unavailable') from error

    def watch_missing(self, path, parent):
        if self.finished:
            raise Unbindable('configuration change monitor already stopped')
        command = {'kind': 'watch', 'directory': str(parent),
                   'target': str(path.relative_to(parent))}
        try:
            self.process.stdin.write(json.dumps(command) + '\n')
            self.process.stdin.flush()
            readable, _, _ = select.select([self.process.stdout], [], [], 10)
            if not readable or self.process.stdout.readline().strip() != 'watched':
                raise Unbindable('configuration change monitor unavailable')
            self.configuration_directories.add(str(parent))
        except (OSError, ValueError) as error:
            raise Unbindable('configuration change monitor unavailable') from error

    @staticmethod
    def reason_for_output(output, returncode):
        # Never echo unexpected stdout, paths, errors or payloads. Only these
        # fixed diagnostic tokens may reach the user; the success test is unchanged.
        if returncode != 0:
            return 'process-exit'
        if output.strip() == 'clean':
            return None
        allowed = {'directory-identity', 'directory-unavailable', 'marker-missing',
                   'marker-cleanup', 'source-change', 'unknown-filename', 'watch-error',
                   'configuration-target', 'configuration-watch-error', 'protocol-input'}
        if output.strip().startswith('dirty:'):
            codes = output.strip()[6:].split(',')
            if codes and all(code in allowed for code in codes):
                return ','.join(sorted(set(codes)))
        return 'protocol-output'

    def stop(self):
        if self.finished:
            return self.clean
        self.finished = True
        if self.process is None:
            return False
        try:
            output, _ = self.process.communicate(json.dumps({'kind': 'stop'}) + '\n', timeout=5)
            self.clean = self.process.returncode == 0 and output.strip() == 'clean'
            self.reason = self.reason_for_output(output, self.process.returncode)
        except (OSError, subprocess.TimeoutExpired):
            self.reason = 'monitor-timeout'
            self.process.kill()
            self.process.communicate()
        return self.clean


class Snapshot:
    """Hash bytes in-process and retain stat signatures to catch in-run ABA.

    Directory signatures catch transient additions/removals. Symlink targets are
    traversed (including external package links), with cycles visited once.
    Signatures are compared in-memory only; receipts contain aggregate digests.
    """
    def __init__(self):
        self.entries = {}
        self.signatures = {}
        self.visited = set()
        self.monitor = ACTIVE_MONITOR

    def add(self, path, exclusions=()):
        path = Path(os.path.abspath(path))
        key = str(path)
        if key in self.visited:
            return
        self.visited.add(key)
        try:
            info = path.lstat()
        except FileNotFoundError:
            self.entries[key] = ['missing']
            self.signatures[key] = None
            parent = path.parent
            while not parent.exists() and parent != parent.parent:
                parent = parent.parent
            relative_target = path.relative_to(parent)
            for ancestor in path.parents:
                if ancestor.is_symlink():
                    self.entries.setdefault(str(ancestor), ['path-link', os.readlink(ancestor)])
                    self.signatures.setdefault(str(ancestor), signature(ancestor.lstat()))
            parent = parent.resolve()
            watched_path = parent / relative_target
            parent_signature = signature(parent.lstat())
            if self.monitor is not None and not watched_path.is_relative_to(self.monitor.root):
                self.monitor.watch_missing(watched_path, parent)
                if signature(parent.lstat()) != parent_signature:
                    raise Unbindable('configuration parent changed while registering observation')
            self.signatures.setdefault(str(parent), parent_signature)
            return
        before = signature(info)
        self.signatures[key] = before
        mode = stat.S_IMODE(info.st_mode)
        if stat.S_ISLNK(info.st_mode):
            self.entries[key] = ['link', os.readlink(path)]
            self.add(path.resolve())
        elif stat.S_ISREG(info.st_mode):
            self.entries[key] = ['file', mode, file_digest(path)]
        elif stat.S_ISDIR(info.st_mode):
            self.entries[key] = ['directory', mode]
            with os.scandir(path) as children:
                for child in sorted(children, key=lambda item: item.name):
                    child_path = Path(child.path)
                    if not any(child_path == item or item in child_path.parents for item in exclusions):
                        self.add(child_path, exclusions)
        else:
            raise Unbindable('special filesystem entry cannot be fingerprinted')
        if signature(path.lstat()) != before:
            raise Unbindable('input changed while being fingerprinted')

    def digest(self):
        return digest_json(self.entries)

    def unchanged(self):
        for name, before in self.signatures.items():
            try:
                after = signature(Path(name).lstat())
            except FileNotFoundError:
                after = None
            if (after != before and self.monitor is not None and before is not None and after is not None and
                    stat.S_ISDIR(before[2]) and
                    (Path(name).is_relative_to(self.monitor.root) or
                     (not self.monitor.finished and name not in self.entries and
                      name in self.monitor.configuration_directories))):
                # Native events guard directory additions/removals; ignore only
                # directory mtime/ctime, retaining inode/mode identity.
                if after[:3] == before[:3]:
                    continue
            if after != before:
                return False
        return True


def source_snapshot(root, snapshot):
    exclusions = tuple(root / name for name in SOURCE_EXCLUSIONS)
    snapshot.add(root, exclusions)
    # A tracked file must never disappear from the identity through an exclusion.
    for raw in capture(['git', 'ls-files', '-z', '--cached'], root).split(b'\0'):
        if raw:
            snapshot.add(root / os.fsdecode(raw))


def source_identity(root):
    snapshot = Snapshot()
    source_snapshot(root, snapshot)
    return snapshot.digest()


def json_objects(raw):
    decoder, cursor = json.JSONDecoder(), 0
    while cursor < len(raw):
        while cursor < len(raw) and raw[cursor].isspace():
            cursor += 1
        if cursor == len(raw):
            break
        value, cursor = decoder.raw_decode(raw, cursor)
        yield value


def installation_prefix(path):
    path = path.resolve()
    if '/Cellar/' in str(path):
        parts = path.parts
        cell = parts.index('Cellar')
        return Path(*parts[:cell + 3])
    if path.parent.name == 'bin' and (path.parent.parent / 'lib/node_modules').is_dir():
        return path.parent.parent / 'lib/node_modules'
    return path


def dynamic_libraries(path, root, snapshot, seen):
    """Bind Mach-O runtime libraries without invoking a tool per source file."""
    path = path.resolve()
    if str(path) in seen:
        return
    seen.add(str(path))
    raw = capture(['otool', '-L', str(path)], root).decode()
    for line in raw.splitlines()[1:]:
        library = line.strip().split(' (', 1)[0]
        if library.startswith(('/usr/lib/', '/System/Library/')):
            # These are sealed OS/shared-cache inputs, bound to OS build below.
            continue
        if library.startswith('@rpath/'):
            library = str(path.parent.parent / 'lib' / library[len('@rpath/'):])
        elif library.startswith('@loader_path/'):
            library = str(path.parent / library[len('@loader_path/'):])
        if not library.startswith('/') or not Path(library).is_file():
            raise Unbindable('unresolved compiler/runtime dynamic library')
        snapshot.add(library)
        dynamic_libraries(Path(library), root, snapshot, seen)


def normalized_environment(root):
    """Git prepends its own exec directory before dispatching a hook. Remove
    only that verified default prefix, with unchanged executable resolution.
    Every remaining PATH component and all genuine flags remain bound.
    """
    environment = {k: value for k, value in os.environ.items() if k not in ENV_BOOKKEEPING and k != SLOTS_ENV}
    original_path = environment.get('PATH', '')
    git_executable = shutil.which('git', path=original_path)
    if not git_executable:
        raise Unbindable('Git executable unavailable')
    installed = capture([str(Path(git_executable).resolve()), '--exec-path'], root,
                        env_remove=('GIT_EXEC_PATH',)).decode().strip()
    if not Path(installed).is_absolute():
        raise Unbindable('default Git exec directory unavailable')
    installed = Path(installed).resolve()
    configured = os.environ.get('GIT_EXEC_PATH')
    if configured is not None and (not configured or Path(configured).resolve() != installed):
        raise Unbindable('custom Git exec directory requires full checks')
    components = original_path.split(os.pathsep)
    count = 0
    while count < len(components) and components[count] and Path(components[count]).resolve() == installed:
        count += 1
    if not count:
        return environment
    prefix = installation_prefix(Path(git_executable))
    if not installed.is_dir() or not installed.is_relative_to(prefix):
        raise Unbindable('Git exec prefix is outside its bound installation')
    canonical_path = os.pathsep.join(components[count:])
    for name in ('git', 'go', 'node', 'pnpm', 'python3', 'cc', 'c++', 'clang', 'clang++',
                 'gcc', 'g++', 'ld', 'ar', 'ranlib', 'pkg-config', 'sh', 'bash', 'xcrun', 'otool', 'sw_vers'):
        before = shutil.which(name, path=original_path)
        after = shutil.which(name, path=canonical_path)
        if (before is None) != (after is None) or (before and Path(before).resolve() != Path(after).resolve()):
            raise Unbindable('Git exec prefix shadows a gate/toolchain executable')
    environment['PATH'] = canonical_path
    return environment


def collect_identity(root):
    snapshot = Snapshot()
    source_snapshot(root, snapshot)
    modules = root / 'node_modules'
    if not modules.is_dir():
        raise Unbindable('node_modules missing; full checks required')
    snapshot.add(modules, (modules / '.vite', modules / '.vite-temp'))
    go_env = json.loads(capture(['go', 'env', '-json'], root))
    # go env generates a fresh temporary -ffile-prefix-map in this derived
    # diagnostic field on each call. Effective CGO_* flags remain bound.
    go_env.pop('GOGCCFLAGS', None)
    # External Go module and local replace bytes, rather than go.sum or an eighth
    # `go mod verify` gate. Go's content-addressed generated GOCACHE is excluded;
    # this trusted-local optimization is not malicious cache attestation.
    module_records = list(json_objects(capture(['go', 'list', '-mod=readonly', '-m', '-json', 'all'], root).decode()))
    for module in module_records:
        effective = module.get('Replace', module)
        if effective.get('Dir'):
            snapshot.add(effective['Dir'])
        # Modules never downloaded/used may have no Dir. Their metadata stays
        # bound below; becoming an actual byte input changes the post-run list.
        if effective.get('GoMod'):
            snapshot.add(effective['GoMod'])
    # Custom cgo packages can name external headers/libraries in directives.
    # Avoid reconstructing compiler search rules: only standard-library cgo is
    # currently supported by reuse (bound GOROOT + SDK + compiler toolchain).
    packages = []
    for race in (False, True):
        command = ['go', 'list', '-mod=readonly', '-test', '-deps', '-json']
        if race:
            command.append('-race')
        command.append('./...')
        packages.extend(json_objects(capture(command, root,
                                             env_overrides={'CGO_ENABLED': '1'} if race else None).decode()))
    if any(package.get('CgoFiles') and not package.get('Standard') for package in packages):
        raise Unbindable('external cgo package requires full checks')
    if go_env.get('PKG_CONFIG', 'pkg-config') != 'pkg-config':
        raise Unbindable('custom pkg-config wrapper requires full checks')
    snapshot.add(go_env['GOROOT'])
    if go_env.get('GOENV', '') not in ('', 'off'):
        snapshot.add(go_env['GOENV'])
    if go_env.get('GOWORK', '') not in ('', 'off'):
        workspace = Path(go_env['GOWORK'])
        snapshot.add(workspace)
        snapshot.add(str(workspace) + '.sum')
    environment = normalized_environment(root)
    effective_flags = {**go_env, **environment}
    if go_env.get('GOFLAGS') or environment.get('GOFLAGS'):
        raise Unbindable('custom Go flags require full checks')
    for key in ('CGO_CFLAGS', 'CGO_CPPFLAGS', 'CGO_CXXFLAGS', 'CGO_FFLAGS', 'CGO_LDFLAGS'):
        flags = effective_flags.get(key, '')
        if flags not in ('', '-O2 -g'):
            raise Unbindable('custom C compiler/linker flags require full checks')
    if any(effective_flags.get(key) for key in ('CPATH', 'C_INCLUDE_PATH', 'CPLUS_INCLUDE_PATH',
                                               'OBJC_INCLUDE_PATH', 'LIBRARY_PATH', 'COMPILER_PATH',
                                               'GCC_EXEC_PREFIX', 'PKG_CONFIG_PATH', 'PKG_CONFIG_LIBDIR',
                                               'CGO_CFLAGS_ALLOW', 'CGO_CFLAGS_DISALLOW',
                                               'CGO_LDFLAGS_ALLOW', 'CGO_LDFLAGS_DISALLOW')):
        raise Unbindable('external compiler/header/library path configuration')
    # Arbitrary preload/wrapper flags may load unenumerated external code. Do not
    # guess: retain normal gates but disable receipt optimization for that run.
    if any(environment.get(k) for k in ('NODE_OPTIONS', 'NODE_PATH', 'LD_PRELOAD',
                                        'LD_LIBRARY_PATH', 'DYLD_LIBRARY_PATH',
                                        'DYLD_INSERT_LIBRARIES', 'PYTHONPATH')):
        raise Unbindable('runtime preload/search-path customization')
    tools = {}
    dynamic_seen = set()
    for name in ('git', 'go', 'node', 'pnpm', 'python3'):
        executable = shutil.which(name, path=environment.get('PATH'))
        if not executable:
            raise Unbindable(f'tool missing: {name}')
        path = Path(executable)
        snapshot.add(path)
        snapshot.add(installation_prefix(path))
        command = [name, 'version'] if name == 'go' else [name, '--version']
        tools[name] = capture(command, root).decode().strip()
        if sys.platform == 'darwin':
            with path.open('rb') as executable_stream:
                magic = executable_stream.read(4)
            if magic in (b'\xcf\xfa\xed\xfe', b'\xfe\xed\xfa\xcf', b'\xca\xfe\xba\xbe', b'\xbe\xba\xfe\xca'):
                dynamic_libraries(path, root, snapshot, dynamic_seen)
            elif name != 'pnpm':
                raise Unbindable('custom tool wrapper requires full checks')
    for compiler in (go_env.get('CC', 'cc'), go_env.get('CXX', 'c++')):
        executable = shutil.which(compiler, path=environment.get('PATH'))
        if (not executable or any(char.isspace() for char in compiler) or
                compiler not in ('cc', 'c++', 'clang', 'clang++', 'gcc', 'g++') or
                str(Path(executable).resolve()) not in ('/usr/bin/cc', '/usr/bin/c++', '/usr/bin/clang', '/usr/bin/clang++', '/usr/bin/gcc', '/usr/bin/g++')):
            raise Unbindable('compiler wrapper requires full checks')
        snapshot.add(executable)
    if sys.platform == 'darwin':
        sdk = capture(['xcrun', '--show-sdk-path'], root).decode().strip()
        clang = Path(capture(['xcrun', '--find', 'clang'], root).decode().strip())
        snapshot.add(sdk)
        if environment.get('SDKROOT'):
            if not Path(environment['SDKROOT']).is_absolute():
                raise Unbindable('non-path SDKROOT requires full checks')
            snapshot.add(environment['SDKROOT'])
        # Includes clang, linker, compiler resource headers and their libraries.
        toolchain = next((parent for parent in clang.parents if parent.name.endswith('.xctoolchain')), None)
        if toolchain is None:
            raise Unbindable('compiler toolchain root unavailable')
        snapshot.add(toolchain)
        tools['os_build'] = capture(['sw_vers'], root).decode()
        selection = Path('/var/db/xcode_select_link')
        if selection.is_symlink():
            snapshot.entries[str(selection)] = ['selection-link', os.readlink(selection)]
            snapshot.signatures[str(selection)] = signature(selection.lstat())
        else:
            snapshot.add(selection)
    else:
        # Linux compiler headers/libraries vary by distribution; until bounded
        # discovery is implemented, correctness comes from full checks only.
        raise Unbindable('receipt binding currently supports macOS toolchains')
    home = Path.home()
    for setting in ('node-options', 'script-shell', 'use-node-version', 'node-version'):
        configured = capture(['pnpm', 'config', 'get', setting], root).decode().strip()
        if configured not in ('', 'undefined', 'null'):
            raise Unbindable('custom pnpm runtime/shell configuration requires full checks')
    pnpm_global = capture(['pnpm', 'config', 'get', 'globalconfig'], root).decode().strip()
    if pnpm_global not in ('', 'undefined', 'null'):
        snapshot.add(pnpm_global)
    manager_store = capture(['pnpm', 'config', 'get', 'package-manager-store-dir'], root).decode().strip()
    if manager_store in ('', 'undefined', 'null'):
        raise Unbindable('pnpm package-manager storage is not declared; full checks without receipt')
    snapshot.add(manager_store)
    # pnpm uses npm-compatible global/user configuration; bind bytes, not values.
    for path in (home / '.npmrc', home / '.pnpmrc', home / '.config/pnpm/rc',
                 Path('/etc/npmrc'), Path('/opt/homebrew/etc/npmrc')):
        snapshot.add(path)
    for key in ('NPM_CONFIG_USERCONFIG', 'npm_config_userconfig',
                'NPM_CONFIG_GLOBALCONFIG', 'npm_config_globalconfig'):
        if environment.get(key):
            snapshot.add(environment[key])
    # Runner bytes are required even if invoked from outside this checkout.
    snapshot.add(Path(__file__).resolve())
    if not snapshot.unchanged():
        raise Unbindable('input changed while being fingerprinted')
    inputs = {'content': snapshot.digest(), 'environment': digest_json(environment),
              'toolchain': digest_json({'versions': tools, 'go_env': go_env, 'modules': module_records, 'platform': list(os.uname())}),
              # Every definition, selected or not: one digest serves both runs, and
              # the receipt's own gate list says which of them actually passed.
              'gates': digest_json({'version': VERSION, 'commands': [*GATES, RACE_GATE]}),
              'worktree': digest_json(str(root.resolve()))}
    return inputs, snapshot


def identity(root):
    global ACTIVE_MONITOR
    if ACTIVE_MONITOR is not None:
        return collect_identity(root)
    monitor = InputMonitor(root)
    ACTIVE_MONITOR = monitor
    try:
        result = collect_identity(root)
        if not monitor.stop():
            raise Unbindable(f'inputs changed while being fingerprinted ({monitor.reason})')
        return result
    finally:
        monitor.stop()
        ACTIVE_MONITOR = None


def receipt_dir(root):
    return Path(git(root, 'rev-parse', '--path-format=absolute', '--git-path', 'artai-verification'))


def receipt_missing(data, gates):
    """Requested gates a receipt does not record as passed. A fast run's receipt
    never covers race; a --race receipt covers the fast set as well."""
    covered = data.get('gates') if isinstance(data, dict) else None
    if not isinstance(covered, list):
        return gate_names(gates)
    return [name for name in gate_names(gates) if name not in covered]


def valid_receipt(path, inputs, gates=None):
    gates = GATES if gates is None else gates
    try:
        data = json.loads(path.read_text())
        age = time.time() - data['completed_at']
        if (data['version'] != VERSION or data['inputs'] != inputs or
                not 0 <= age <= MAX_AGE_SECONDS or data['result'] != 'passed' or
                not isinstance(data['gates'], list) or receipt_missing(data, gates)):
            return None
        return data
    except (OSError, ValueError, KeyError, TypeError):
        return None


def save_receipt(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix='receipt-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            json.dump(value, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def verify_head_bytes(root, head):
    # status alone trusts index stat shortcuts. Refuse assume-unchanged and
    # skip-worktree, then hash all ordinary tracked files in one Git subprocess.
    # hash-object applies Git's clean/eol conversion, so CRLF checkouts are valid.
    for raw in capture(['git', 'ls-files', '-v', '-z'], root).split(b'\0'):
        if raw and (raw[:1] == b'S' or raw[:1].islower()):
            raise GateError('push cannot validate assume-unchanged/skip-worktree entries')
    files, expected = [], []
    for raw in capture(['git', 'ls-tree', '-r', '-z', head], root).split(b'\0'):
        if not raw:
            continue
        metadata, name = raw.split(b'\t', 1)
        mode, kind, oid = metadata.split()
        path = root / os.fsdecode(name)
        if mode == b'120000':
            if not path.is_symlink():
                raise GateError('tracked symlink differs from pushed HEAD')
            target = os.fsencode(os.readlink(path))
            algorithm = 'sha256' if len(oid) == 64 else 'sha1'
            actual = hashlib.new(algorithm, b'blob ' + str(len(target)).encode() + b'\0' + target).hexdigest().encode()
            if actual != oid:
                raise GateError('tracked symlink differs from pushed HEAD')
        elif kind == b'blob' and mode in (b'100644', b'100755'):
            if not path.is_file() or path.is_symlink():
                raise GateError('tracked file differs from pushed HEAD')
            executable = bool(path.stat().st_mode & 0o111)
            if executable != (mode == b'100755'):
                raise GateError('tracked executable mode differs from pushed HEAD')
            files.append(json.dumps(os.fsdecode(name), ensure_ascii=False))
            expected.append(oid)
        else:
            raise GateError('unsupported tracked entry; validate push in a complete matching checkout')
    if files:
        actual = capture(['git', 'hash-object', '--stdin-paths'], root,
                         ('\n'.join(files) + '\n').encode()).splitlines()
        if actual != expected:
            raise GateError('tracked bytes differ from pushed HEAD')


def verify_push(root, data):
    head = git(root, 'rev-parse', 'HEAD')
    updates = False
    for line in data.splitlines():
        fields = line.split()
        if len(fields) != 4 or not all(re.fullmatch(r'[0-9a-f]{40}|[0-9a-f]{64}', sha) for sha in (fields[1], fields[3])):
            raise GateError('invalid pre-push input')
        ref, local_sha, _, _ = fields
        if set(local_sha) == {'0'}:
            continue
        if not ref.startswith('refs/heads/'):
            raise GateError('non-branch push must be validated in its matching checkout')
        if local_sha != head:
            raise GateError('pushed branch tip is not this worktree HEAD; use its matching worktree')
        updates = True
    if updates:
        verify_head_bytes(root, head)
        if (capture(['git', 'diff-index', '--cached', '--name-only', 'HEAD', '--'], root) or
                capture(['git', 'ls-files', '--others', '--exclude-standard', '-z'], root)):
            raise GateError('push gates require an unchanged index and no untracked source')
    return updates


def gate_slots():
    raw = os.environ.get(SLOTS_ENV, '').strip()
    if not raw:
        return DEFAULT_SLOTS
    if not re.fullmatch(r'[0-9]+', raw):
        raise GateError(f'{SLOTS_ENV} must be a non-negative integer')
    return int(raw)


def queue_dir(root):
    # Shared by every worktree of this clone, unlike the per-worktree receipts.
    return Path(git(root, 'rev-parse', '--path-format=absolute', '--git-common-dir')) / 'artai-verification-queue'


def slot_holder(path):
    try:
        data = json.loads(path.read_text() or 'null')
        return f"{data['worktree']} (pid {data['pid']})"
    except (OSError, ValueError, KeyError, TypeError):
        return 'unknown holder'


@contextlib.contextmanager
def gate_slot(root):
    """Hold one of N clone-wide slots while the heavy gates run.

    Parallel worktrees pushing at once overload the workstation and make the
    race gate fail from contention. Only gate execution queues: receipt reuse
    and --check never call this. The input monitor and stat guards stay active
    while waiting, so a change during the wait still blocks the receipt.
    flock is released on every exit path, including process death.
    """
    slots = gate_slots()
    if slots == 0:
        yield None
        return
    directory = queue_dir(root)
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    paths = [directory / f'slot-{index}.lock' for index in range(1, slots + 1)]
    start = time.monotonic()
    announced = False
    while True:
        for index, path in enumerate(paths, 1):
            stream = path.open('a+')
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                stream.close()
                continue
            except BaseException:
                stream.close()
                raise
            try:
                stream.truncate(0)
                stream.write(json.dumps({'pid': os.getpid(), 'worktree': str(root), 'started_at': time.time()}))
                stream.flush()
                if announced:
                    print(f'[verify] gate queue: acquired slot {index}/{slots} after '
                          f'{time.monotonic() - start:.0f}s', flush=True)
                yield index
            finally:
                try:
                    stream.truncate(0)
                finally:
                    stream.close()
            return
        if not announced:
            holders = '; '.join(f'slot {index}: {slot_holder(path)}' for index, path in enumerate(paths, 1))
            print(f'[verify] gate queue: all {slots} slot(s) busy ({SLOTS_ENV}={slots}); waiting for another '
                  f"worktree's gates to finish [{holders}]", flush=True)
            announced = True
        time.sleep(QUEUE_POLL_SECONDS)


def run_gates(root, gates=None):
    with gate_slot(root):
        run_gates_unqueued(root, gates)


def run_gates_unqueued(root, gates=None):
    gates = GATES if gates is None else gates
    try:
        canonical_path = normalized_environment(root).get('PATH', os.environ.get('PATH', ''))
    except (Unbindable, OSError, ValueError):
        # Unknown/custom prefix keeps normal full-check execution; no receipt
        # was considered reusable by identity inspection.
        canonical_path = os.environ.get('PATH', '')
    for name, command, overrides in gates:
        start = time.monotonic()
        print(f'[verify] {name}', flush=True)
        # Hook dispatch points Git at the pushed repository; the scripts tests create their own repositories.
        inherited = {k: value for k, value in os.environ.items() if k not in HOOK_GIT_ENV}
        result = subprocess.run(command, cwd=root, env={**inherited, 'PATH': canonical_path, **overrides})
        print(f'[verify] {name}: {time.monotonic() - start:.2f}s (exit {result.returncode})', flush=True)
        if result.returncode:
            raise GateError(f'{name} failed ({result.returncode}); no reusable receipt')


def coverage(gates, outcome):
    names = gate_names(gates)
    note = '' if RACE_GATE[0] in names else f'; {RACE_GATE[0]} runs in CI only (--race adds it)'
    return f"{len(names)} gates {outcome} ({', '.join(names)}){note}"


def verify_inner(root, force=False, check=False, push_data=None, gates=None):
    gates = GATES if gates is None else gates
    start = time.monotonic()
    path = receipt_dir(root) / 'latest.json'
    if not check:
        (root / 'build/go-web').mkdir(parents=True, exist_ok=True)
        if (root / 'node_modules').is_dir():
            for name in ('.vite', '.vite-temp'):
                (root / 'node_modules' / name).mkdir(exist_ok=True)
    try:
        inputs, snapshot = identity(root)
    except (Unbindable, OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
        inputs, snapshot = None, None
        print(f'[verify] full checks required: {error}', flush=True)
    if inputs is not None and not force and valid_receipt(path, inputs, gates):
        if not snapshot.unchanged():
            raise GateError('inputs changed during receipt validation')
        if push_data is not None:
            verify_push(root, push_data)
        if ACTIVE_MONITOR is not None and not ACTIVE_MONITOR.stop():
            raise GateError(f'inputs changed during receipt validation ({ACTIVE_MONITOR.reason})')
        print(f'[verify] reuse: {coverage(gates, "reused")}; total {time.monotonic() - start:.2f}s', flush=True)
        return True
    if check:
        print('[verify] no reusable receipt', flush=True)
        return False
    # Report why reuse missed before invalidating the sole success. Key names
    # and digests are safe diagnostics; never serialize environment values.
    previous = None
    try:
        previous = json.loads(path.read_text())
    except (OSError, ValueError, TypeError):
        pass
    changed = []
    if inputs is not None and isinstance(previous, dict) and isinstance(previous.get('inputs'), dict):
        changed = sorted(key for key in set(inputs) | set(previous['inputs'])
                         if inputs.get(key) != previous['inputs'].get(key))
        missing = receipt_missing(previous, gates)
        print('[verify] full required: changed input components: ' + (
            ', '.join(changed) or
            ('receipt does not cover ' + ', '.join(missing) if missing else 'receipt age/result/format')), flush=True)
    # Any new attempt invalidates the old success, including failures/interruption.
    path.unlink(missing_ok=True)
    save_receipt(path.parent / 'last-attempt.json',
                 {'kind': 'diagnostic-only', 'started_at': time.time(), 'inputs': inputs,
                  'changed_components': changed})
    run_gates(root, gates)
    if push_data is not None:
        verify_push(root, push_data)
    if snapshot is not None:
        if not snapshot.unchanged():
            raise GateError('inputs changed during verification; no reusable receipt')
        try:
            after, _ = identity(root)
        except (Unbindable, OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
            print(f'[verify] gates passed, receipt unavailable: {error}', flush=True)
            after = None
        if after is not None and after != inputs:
            raise GateError('inputs changed during verification; no reusable receipt')
        if after is not None and snapshot.unchanged():
            if ACTIVE_MONITOR is not None and not ACTIVE_MONITOR.stop():
                raise GateError(f'inputs changed during verification ({ACTIVE_MONITOR.reason}); no reusable receipt')
            save_receipt(path, {'version': VERSION, 'result': 'passed', 'inputs': inputs,
                                'head_at_run': git(root, 'rev-parse', 'HEAD'),
                                'gates': gate_names(gates), 'completed_at': time.time()})
            print('[verify] private worktree receipt saved', flush=True)
    print(f'[verify] full: {coverage(gates, "passed")}; total {time.monotonic() - start:.2f}s', flush=True)
    return True


def verify(root, force=False, check=False, push_data=None, race=False):
    global ACTIVE_MONITOR
    gates = selected_gates(race)
    if not check:
        (root / 'build/go-web').mkdir(parents=True, exist_ok=True)
        if (root / 'node_modules').is_dir():
            for name in ('.vite', '.vite-temp'):
                (root / 'node_modules' / name).mkdir(exist_ok=True)
    try:
        monitor = InputMonitor(root)
    except Unbindable as error:
        if check:
            return False
        print(f'[verify] {error}; full checks without receipt', flush=True)
        (receipt_dir(root) / 'latest.json').unlink(missing_ok=True)
        run_gates(root, gates)
        if push_data is not None:
            verify_push(root, push_data)
        return True
    ACTIVE_MONITOR = monitor
    try:
        return verify_inner(root, force=force, check=check, push_data=push_data, gates=gates)
    finally:
        monitor.stop()
        ACTIVE_MONITOR = None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--force', action='store_true')
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--pre-push', action='store_true')
    parser.add_argument('--race', action='store_true',
                        help='also run the CI-only go test race gate (dev_test.md §2)')
    parser.add_argument('--list-gates', action='store_true',
                        help='print the gate manifest (default set and the race gate) as JSON and exit; '
                             'src/__tests__/ci-gate-parity.test.ts compares it with dev_test.md §2 and ci.yml')
    args = parser.parse_args()
    if args.list_gates:
        print(json.dumps(gate_manifest(), indent=2))
        return 0
    if not args.check:
        require_tools(args.race)
    root = Path(git(Path.cwd(), 'rev-parse', '--show-toplevel')).resolve()
    push_data = sys.stdin.read() if args.pre_push else None
    if push_data is not None and not verify_push(root, push_data):
        print('[verify] deletion-only push; no code gates required')
        return 0
    directory = receipt_dir(root)
    if not args.check:
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    elif not directory.exists():
        return 2
    with (directory / 'run.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise GateError('another local gate run is active in this worktree') from None
        return 0 if verify(root, force=args.force, check=args.check, push_data=push_data,
                           race=args.race) else 2


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (GateError, OSError, ValueError) as error:
        print(f'[verify] {error}', file=sys.stderr)
        sys.exit(1)
