#!/usr/bin/env bash
# In-container entry point of the Plinth gate CI image (#97). Runs as the unprivileged
# user without network. It only knows mounted paths and the exact commit to check, so any
# runner that mounts the same paths gets the same behaviour:
#
#   /in/source.bundle  read-only Git bundle holding the commit (ref refs/plinth-host-ci/*)
#   /work              empty writable tmpfs (not a bind mount: the wirefixture tests refuse
#                      worktrees under bind mounts); the commit is checked out here
#   /tmp               writable tmpfs, also not a bind mount (test evidence directories)
#   /scratch           writable bind mount: HOME, Go build cache, pnpm store copy
#   /out               writable; receives result.json, gates.log and phases.tsv
#   CI_SHA, CI_TREE    expected commit and tree (40 hex digits each)
#
# Exit status: 0 all gates passed; 1 a gate failed; 20 staged checkout is not the
# expected commit/tree (retried once by the host); 21 the source changed while the gates
# ran; 22 the image does not match ci.yml/package.json or lacks dependencies.
set -uo pipefail
umask 022

SHA=${CI_SHA:-}
TREE=${CI_TREE:-}
[[ $SHA =~ ^[0-9a-f]{40}$ && $TREE =~ ^[0-9a-f]{40}$ ]] || { echo "entry: CI_SHA and CI_TREE must be 40 hex digits" >&2; exit 2; }
OUT=/out SCRATCH=/scratch WORK=/work BUNDLE=/in/source.bundle
mkdir -p "$SCRATCH/home" "$SCRATCH/gocache" "$SCRATCH/gopath" || exit 2
: >"$OUT/phases.tsv"

export HOME=$SCRATCH/home TMPDIR=/tmp GOCACHE=$SCRATCH/gocache GOPATH=$SCRATCH/gopath
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
export CI=true PYTHONDONTWRITEBYTECODE=1 NPM_CONFIG_USERCONFIG=/dev/null npm_config_userconfig=/dev/null
export GOFLAGS=-p=4

STATUS=failed
FAILED_PHASE=
EXPECTED_GATES=
EXIT=1

# phase NAME CMD...: run, time and record one step; returns the command's status.
phase() {
  local name=$1 start end rc elapsed
  shift
  printf '[host-ci] phase %s\n' "$name"
  start=${EPOCHREALTIME/./}
  "$@"
  rc=$?
  end=${EPOCHREALTIME/./}
  elapsed=$((end - start))
  printf '%s\t%d.%02d\t%d\n' "$name" $((elapsed / 1000000)) $((elapsed % 1000000 / 10000)) "$rc" >>"$OUT/phases.tsv"
  printf '[host-ci] phase %s: exit %d\n' "$name" "$rc"
  return "$rc"
}

report() {
  python3 - "$OUT" "$STATUS" "$FAILED_PHASE" "$EXIT" "$SHA" "$TREE" "$EXPECTED_GATES" <<'PY'
import json, re, sys
out, status, failed_phase, exit_code, sha, tree, expected = sys.argv[1:8]

def read(path):
    try:
        with open(path, encoding='utf-8', errors='replace') as handle:
            return handle.read()
    except OSError:
        return ''

phases = []
for line in read(f'{out}/phases.tsv').splitlines():
    name, seconds, code = line.split('\t')
    phases.append({'name': name, 'seconds': float(seconds), 'exit': int(code)})

ran = {}
for match in re.finditer(r'^\[verify\] (.+?): ([0-9.]+)s \(exit (-?\d+)\)$', read(f'{out}/verify.log'), re.M):
    ran[match.group(1)] = {'seconds': float(match.group(2)), 'exit': int(match.group(3))}
gates = []
for name in [gate for gate in expected.split('|') if gate]:
    if name in ran:
        gates.append({'name': name, 'result': 'pass' if ran[name]['exit'] == 0 else 'fail', **ran[name]})
    else:
        gates.append({'name': name, 'result': 'not_run'})
for phase in phases:
    if phase['name'] == 'external links':
        gates.append({'name': 'external links', 'result': 'pass' if phase['exit'] == 0 else 'fail',
                      'seconds': phase['seconds'], 'exit': phase['exit']})

def cgroup(name):
    value = read(f'/sys/fs/cgroup/{name}').strip()
    return value or None

events = {}
for line in (cgroup('memory.events') or '').splitlines():
    key, _, count = line.partition(' ')
    if count.isdigit():
        events[key] = int(count)
peak = cgroup('memory.peak')
result = {
    'sha': sha, 'tree': tree, 'status': status, 'failed_phase': failed_phase or None,
    'entry_exit': int(exit_code), 'phases': phases, 'gates': gates,
    'cgroup': {'memory_peak_bytes': int(peak) if peak and peak.isdigit() else None,
               'memory_max': cgroup('memory.max'), 'cpu_max': cgroup('cpu.max'),
               'events': events, 'oom_kill': events.get('oom_kill')},
}
with open(f'{out}/result.json', 'w', encoding='utf-8') as handle:
    json.dump(result, handle, indent=2, sort_keys=True)
    handle.write('\n')
PY
}

finish() {
  trap - EXIT
  report || echo "entry: could not write result.json" >&2
  exit "$EXIT"
}
trap finish EXIT
trap 'EXIT=130; STATUS=interrupted; exit 130' INT
trap 'EXIT=143; STATUS=interrupted; exit 143' TERM HUP

fail() { STATUS=$1; EXIT=$2; FAILED_PHASE=$3; exit "$2"; }

stage() {
  git init -q "$WORK" &&
    git -C "$WORK" fetch -q --no-tags "$BUNDLE" '+refs/plinth-host-ci/*:refs/remotes/bundle/*' &&
    git -C "$WORK" checkout -q --detach "$SHA"
}
check_stage() {
  [[ $(git -C "$WORK" rev-parse HEAD) == "$SHA" ]] &&
    [[ $(git -C "$WORK" rev-parse 'HEAD^{tree}') == "$TREE" ]] &&
    [[ -z $(git -C "$WORK" status --porcelain --untracked-files=all) ]]
}
phase stage stage || fail stage_mismatch 20 stage
phase stage-identity check_stage || fail stage_mismatch 20 stage-identity
cd "$WORK" || exit 2

workflow_version() { sed -n "s/^  $1: '\\([0-9.]*\\)'.*/\\1/p" .github/workflows/ci.yml; }
check_toolchain() {
  local node_want go_want pnpm_want
  node_want=$(workflow_version NODE_VERSION)
  go_want=$(workflow_version GO_VERSION)
  pnpm_want=$(node -p "require('./package.json').packageManager.replace(/^pnpm@/, '')")
  [[ -n $node_want && -n $go_want && -n $pnpm_want ]] || { echo "toolchain: versions not found in ci.yml/package.json" >&2; return 1; }
  [[ $(node --version) == v$node_want.* ]] || { echo "toolchain: node $(node --version), CI wants $node_want" >&2; return 1; }
  [[ $(go version) == *" go$go_want"[.\ ]* ]] || { echo "toolchain: $(go version), CI wants $go_want" >&2; return 1; }
  [[ $(pnpm --version) == "$pnpm_want" ]] || { echo "toolchain: pnpm $(pnpm --version), package.json wants $pnpm_want" >&2; return 1; }
  node --version; go version; pnpm --version; python3 --version; git --version
}
phase toolchain check_toolchain || fail toolchain_mismatch 22 toolchain

# pnpm keeps its store index in SQLite, which cannot be opened on the read-only image layer:
# work on a copy in the scratch directory.
copy_dependencies() { cp -a /opt/ci/pnpm-store "$SCRATCH/pnpm-store" && cp -a /opt/ci/cache "$SCRATCH/cache"; }
phase copy-dependencies copy_dependencies || fail dependencies_missing 22 copy-dependencies
export XDG_CACHE_HOME=$SCRATCH/cache
phase pnpm-install pnpm install --offline --frozen-lockfile --store-dir "$SCRATCH/pnpm-store" || fail dependencies_missing 22 pnpm-install
phase go-deps bash -c 'go list -deps -test ./... >/dev/null' || fail dependencies_missing 22 go-deps
mkdir -p build/go-web

source_identity() {
  python3 -c 'import importlib.util; from pathlib import Path
spec = importlib.util.spec_from_file_location("verify_local", "scripts/verify-local.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
print(module.source_identity(Path.cwd()))'
}
EXPECTED_GATES=$(python3 -c 'import importlib.util
spec = importlib.util.spec_from_file_location("verify_local", "scripts/verify-local.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
print("|".join(name for name, _, _ in module.selected_gates(race=True)))') || fail toolchain_mismatch 22 gate-list
BEFORE=$(source_identity)
[[ $BEFORE =~ ^[0-9a-f]{64}$ ]] || fail source_changed 21 source-identity

phase gates bash -c 'python3 scripts/verify-local.py --race --force 2>&1 | tee /out/verify.log; exit "${PIPESTATUS[0]}"'
GATES_RC=$?
if [[ $GATES_RC -eq 0 ]]; then
  phase 'external links' node scripts/check-build-external-links.mjs
  LINKS_RC=$?
else
  LINKS_RC=1
fi

AFTER=$(source_identity)
unchanged() {
  [[ $AFTER == "$BEFORE" && $(git rev-parse HEAD) == "$SHA" && $(git rev-parse 'HEAD^{tree}') == "$TREE" ]] &&
    [[ -z $(git status --porcelain --untracked-files=all) ]]
}
phase source-unchanged unchanged || fail source_changed 21 source-unchanged
if [[ $GATES_RC -ne 0 ]]; then fail failed 1 gates; fi
if [[ $LINKS_RC -ne 0 ]]; then fail failed 1 'external links'; fi
STATUS=passed
EXIT=0
exit 0
