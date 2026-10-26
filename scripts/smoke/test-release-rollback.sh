#!/usr/bin/env bash
# Offline fault injection for the single-instance test-env release rollback.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")/.." && pwd)
RELEASE_SCRIPT=${1:-"$SCRIPT_DIR/release-server-steps.sh"}
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT
MOCK_REAL_MKDIR=$(command -v mkdir)
MOCK_REAL_TAR=$(command -v tar)
export MOCK_REAL_MKDIR MOCK_REAL_TAR
mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/root/app/scripts" "$TEST_ROOT/root/production"
cp "$SCRIPT_DIR/release-capacity.py" "$TEST_ROOT/root/app/scripts/release-capacity.py"

cat >"$TEST_ROOT/bin/docker" <<'MOCK_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
echo "$1" >>"$MOCK_TRACE"
case "$1" in
  build)
    # #259: the test stack must keep the image that carries the migration tools.
    [[ " $* " == *" --target runtime-with-migration-tools "* ]] || { echo 'mock build: internal target missing' >&2; exit 1; }
    touch "$MOCK_BUILD_DONE"; echo 'mock build complete' ;;
  run)
    case "$*" in
      *'id -u artai'*) echo '100 101' ;;
      *write-check*) [ "${MOCK_FAILURE:-}" != log-dir-unwritable ] || exit 1 ;;
      *) exit 1 ;;
    esac
    ;;
  info)
    [ "$*" = 'info --format {{.DockerRootDir}}' ] || exit 1
    [ "${MOCK_FAILURE:-}" != docker-info-failed ] || exit 1
    printf '%s\n' "$MOCK_ROOT/docker"
    ;;
  inspect)
    if [ "$2" = --format ]; then
      [ "$3" = '{{json .Mounts}}' ] || exit 1
      if [ "${MOCK_FAILURE:-}" = mount-output-oversized ]; then
        python3 -c 'print("x" * 100000)'
        exit 0
      fi
      python3 - <<'PY'
import json,os
r=os.environ['MOCK_ROOT']
mounts=[{'Type':'bind','Source':r+'/production/'+source,'Destination':'/app/'+destination,'RW':destination!='web-data'}
        for source,destination in [('data','data'),('storage-go','storage'),('web-data','web-data')]]
mounts += [{'Type':'bind','Source':r+'/legacy/'+name,'Destination':'/app/legacy-'+name,'RW':name in ['cache','thumbnails']}
           for name in ['generated','cache','thumbnails','assets']]
mounts.append({'Type':'tmpfs','Source':'','Destination':'/tmp','RW':True})
if os.environ.get('MOCK_FAILURE')=='mount-mismatch':mounts[0]['Source']=r+'/legacy/cache'
print(json.dumps(mounts))
PY
    else
      echo 'art-ai-go-production:previous'
    fi
    ;;
  compose)
    case " $* " in
      *' stop '*)
        echo stop >>"$MOCK_TRACE"
        echo stopped >"$MOCK_STATE"
        if [ "${MOCK_FAILURE:-}" = stop-failed ]; then exit 1; fi
        ;;
      *' up '*)
        if [ "$GO_IMAGE_TAG" = candidate ]; then
          echo candidate-up >>"$MOCK_TRACE"
          echo candidate >"$MOCK_STATE"
          for target in data storage-go web-data; do
            echo changed >"$MOCK_ROOT/production/$target/marker"
          done
          if [ "${MOCK_UP_FAIL:-0}" = 1 ]; then
            echo 'mock candidate up failure' >&2
            exit 1
          fi
        else
          echo previous-up >>"$MOCK_TRACE"
          echo previous >"$MOCK_STATE"
        fi
        ;;
    esac
    ;;
  *) echo "unexpected docker call: $*" >&2; exit 1 ;;
esac
MOCK_DOCKER
cat >"$TEST_ROOT/bin/curl" <<'MOCK_CURL'
#!/usr/bin/env bash
set -euo pipefail
case " $* " in
  *' --connect-timeout 3 --max-time 5 '*) ;;
  *) echo 'health probe is unbounded' >&2; exit 1 ;;
esac
if [ "${MOCK_FAILURE:-}" = network-failed ] && [ "$(cat "$MOCK_STATE")" = candidate ]; then
  exit 7
fi
case "${MOCK_FAILURE:-}" in
  unhealthy|up-failed|network-failed|backup-setup|backup-exists|backup-failed|stop-failed|report-failed|backup-and-report-failed|manifest-failed) ;;
  *) printf 200; exit 0 ;;
esac
if [ "$(cat "$MOCK_STATE")" = previous ] ||
  { [ "${MOCK_FAILURE:-}" = manifest-failed ] && [ "$(cat "$MOCK_STATE")" = candidate ]; }; then
  printf 200
else
  printf 503
fi
MOCK_CURL
cat >"$TEST_ROOT/bin/sleep" <<'MOCK_SLEEP'
#!/usr/bin/env bash
exit 0
MOCK_SLEEP
chmod +x "$TEST_ROOT/bin/docker" "$TEST_ROOT/bin/curl" "$TEST_ROOT/bin/sleep"
cat >"$TEST_ROOT/bin/mkdir" <<'MOCK_MKDIR'
#!/usr/bin/env bash
set -euo pipefail
case "${MOCK_FAILURE:-}:$*" in
  backup-setup:*'/backups/release-candidate') exit 1 ;;
  report-failed:*'/reports/release-failures') exit 1 ;;
  backup-and-report-failed:*'/reports/release-failures') exit 1 ;;
  manifest-failed:*'/releases') exit 1 ;;
esac
exec "$MOCK_REAL_MKDIR" "$@"
MOCK_MKDIR
cat >"$TEST_ROOT/bin/tar" <<'MOCK_TAR'
#!/usr/bin/env bash
set -euo pipefail
if [[ " ${*} " == *' -cf '* ]] &&
  [[ "${MOCK_FAILURE:-}" = backup-failed || "${MOCK_FAILURE:-}" = backup-and-report-failed ]]; then
  exit 1
fi
if [[ " ${*} " == *' -cf '* ]]; then
  exec "$MOCK_REAL_TAR" --format=ustar "$@"
fi
exec "$MOCK_REAL_TAR" "$@"
MOCK_TAR
chmod +x "$TEST_ROOT/bin/mkdir" "$TEST_ROOT/bin/tar"
cat >"$TEST_ROOT/root/app/scripts/cutover-go-production.sh" <<'MOCK_PREFLIGHT'
#!/usr/bin/env bash
echo 'mock preflight complete'
MOCK_PREFLIGHT
cat >"$TEST_ROOT/root/app/scripts/retain-release-backups.py" <<'MOCK_BACKUP_RETENTION'
import os, sys
open(os.environ["MOCK_TRACE"], "a").write("backup-retention " + " ".join(sys.argv[1:]) + "\n")
MOCK_BACKUP_RETENTION
cp "$SCRIPT_DIR/release-lock.py" "$TEST_ROOT/root/app/scripts/release-lock.py"
chmod +x "$TEST_ROOT/root/app/scripts/cutover-go-production.sh"
touch "$TEST_ROOT/root/app/docker-compose.go-production.yml" "$TEST_ROOT/root/production/runtime.env"

export PATH="$TEST_ROOT/bin:$PATH"
export MOCK_ROOT="$TEST_ROOT/root"
export MOCK_STATE="$TEST_ROOT/state"
export MOCK_TRACE="$TEST_ROOT/trace"
export MOCK_BUILD_DONE="$TEST_ROOT/build-done"
export TMPDIR="$TEST_ROOT"
# The repository names no environment's Compose project (#261); the caller passes it.
export RELEASE_COMPOSE_PROJECT=test-stack
mkdir -p "$MOCK_ROOT/docker"
for directory in generated cache thumbnails assets; do
  mkdir -p "$MOCK_ROOT/legacy/$directory"
  echo sample >"$MOCK_ROOT/legacy/$directory/sample"
done

cat >"$TEST_ROOT/bin/du" <<'MOCK_DU'
#!/usr/bin/env python3
import os,stat,sys
assert set(sys.argv[1:-1]) == {'--summarize','--apparent-size','--count-links','--block-size=1','--'}
failure=os.environ.get('MOCK_FAILURE','')
if failure=='du-failed':sys.exit(1)
if failure=='du-output-oversized':print('x'*100000);sys.exit(0)
path=sys.argv[-1]
if failure=='du-malformed':print('unknown\t'+path);sys.exit(0)
if failure=='arithmetic-overflow':print(str((1<<63)-1)+'\t'+path);sys.exit(0)
total=os.lstat(path).st_size
for root,dirs,files in os.walk(path,followlinks=False):
 for name in dirs+files:total+=os.lstat(os.path.join(root,name)).st_size
print(str(total)+'\t'+path)
MOCK_DU
cat >"$TEST_ROOT/bin/df" <<'MOCK_DF'
#!/usr/bin/env python3
import os,sys
assert sys.argv[1] in ['--output=avail','--output=iavail'] and sys.argv[2:4]==['--block-size=1','--']
assert os.environ['LC_ALL']=='C'
failure=os.environ.get('MOCK_FAILURE','')
if failure=='df-failed':sys.exit(1)
header='Avail' if sys.argv[1]=='--output=avail' else 'IFree'
value=1_000_000_000_000
if failure=='df-malformed':value='unknown'
if failure=='df-negative':value=-1
if failure=='df-overflow':value=1<<64
if failure=='bytes-zero' and header=='Avail':value=0
if failure=='bytes-low' and header=='Avail':value=1
if failure=='inodes-zero' and header=='IFree':value=0
built=os.path.exists(os.environ['MOCK_BUILD_DONE'])
stopped=open(os.environ['MOCK_STATE']).read().strip()=='stopped'
backed=all(os.path.exists(os.environ['MOCK_ROOT']+'/backups/release-candidate/'+n+'.tar') for n in ['data','storage-go','web-data'])
if failure=='after-build-drop' and built and header=='Avail':value=0
if failure=='inode-overhead' and built and header=='IFree':value=4096
if failure=='archive-overhead' and built and header=='Avail':
 total=0
 for name in ['data','storage-go','web-data']:
  path=os.environ['MOCK_ROOT']+'/production/'+name
  total+=os.lstat(path).st_size
  for root,dirs,files in os.walk(path):
   for entry in dirs+files:total+=os.lstat(root+'/'+entry).st_size
 value=(1<<30)+2*total
if failure=='after-stop-drop' and stopped and header=='Avail':value=1<<30
if failure=='after-backup-drop' and backed and header=='Avail':value=1<<30
if failure=='sparse' and header=='Avail':value=12<<30
print(header);print(value)
MOCK_DF
chmod +x "$TEST_ROOT/bin/du" "$TEST_ROOT/bin/df"

for failure in unhealthy up-failed network-failed backup-setup backup-exists backup-failed stop-failed report-failed backup-and-report-failed manifest-failed; do
  export MOCK_FAILURE="$failure"
  : >"$MOCK_TRACE"
  rm -f "$MOCK_BUILD_DONE"
  rm -rf "$MOCK_ROOT/backups" "$MOCK_ROOT/production/reports" "$MOCK_ROOT/releases"
  if [ "$failure" = backup-exists ]; then
    mkdir -p "$MOCK_ROOT/backups/release-candidate"
    echo retained >"$MOCK_ROOT/backups/release-candidate/marker"
  fi
  echo previous >"$MOCK_STATE"
  for target in data storage-go web-data; do
    mkdir -p "$MOCK_ROOT/production/$target"
    echo original >"$MOCK_ROOT/production/$target/marker"
  done
  if [ "$failure" = up-failed ]; then
    export MOCK_UP_FAIL=1
  else
    unset MOCK_UP_FAIL || true
  fi
  set +e
  bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/$failure.log" 2>&1
  result=$?
  set -e
  [ "$result" = 1 ] || { echo "$failure: expected release failure, got $result" >&2; exit 1; }
  [ "$(cat "$MOCK_STATE")" = previous ] || { echo "$failure: previous service not running" >&2; exit 1; }
  if [ "$failure" = backup-exists ]; then
    [ "$(cat "$MOCK_ROOT/backups/release-candidate/marker")" = retained ] || {
      echo "$failure: earlier backup was changed" >&2; exit 1;
    }
  fi
  for target in data storage-go web-data; do
    [ "$(cat "$MOCK_ROOT/production/$target/marker")" = original ] || {
      echo "$failure: $target was not restored" >&2; exit 1;
    }
  done
  [ ! -e "$MOCK_ROOT/releases/manifest.txt" ] || {
    echo "$failure: failed candidate was recorded as deployed" >&2; exit 1;
  }
  case "$failure" in
    unhealthy|network-failed) stage=unhealthy-after-up ;;
    up-failed) stage=candidate-up-failed ;;
    backup-setup|backup-exists) stage=backup-setup-failed ;;
    backup-failed) stage=backup-failed ;;
    stop-failed) stage=previous-stop-failed ;;
    manifest-failed) stage=manifest-write-failed ;;
    report-failed|backup-and-report-failed) stage='' ;;
  esac
  if [ -n "$stage" ]; then
    [ -f "$MOCK_ROOT/production/reports/release-failures/candidate-$stage.txt" ] || {
      echo "$failure: failure report missing" >&2; exit 1;
    }
  fi
  echo "PASS existing/$failure"
done

for failure in capacity-adequate bytes-zero bytes-low inodes-zero du-failed du-malformed df-failed df-malformed df-negative df-overflow arithmetic-overflow after-build-drop archive-overhead inode-overhead sparse after-stop-drop after-backup-drop du-output-oversized mount-output-oversized docker-info-failed mount-mismatch; do
  export MOCK_FAILURE="$failure"
  unset MOCK_UP_FAIL || true
  rm -rf "$MOCK_ROOT/backups" "$MOCK_ROOT/releases" "$MOCK_ROOT/production/reports"
  mkdir -p "$MOCK_ROOT/backups/earlier"
  echo retained >"$MOCK_ROOT/backups/earlier/marker"
  rm -f "$MOCK_BUILD_DONE"
  : >"$MOCK_TRACE"
  echo previous >"$MOCK_STATE"
  for target in data storage-go web-data; do
    rm -rf "$MOCK_ROOT/production/$target"
    mkdir -p "$MOCK_ROOT/production/$target"
    echo original >"$MOCK_ROOT/production/$target/marker"
  done
  if [ "$failure" = sparse ]; then
    python3 - <<'PY'
import os
path=os.environ['MOCK_ROOT']+'/production/storage-go/sparse'
with open(path,'wb') as f:f.truncate(3<<30)
assert os.stat(path).st_blocks*512 < 1<<20
PY
  fi
  set +e
  bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/$failure.log" 2>&1
  result=$?
  set -e
  if [ "$failure" = capacity-adequate ]; then
    [ "$result" = 0 ] && [ "$(cat "$MOCK_STATE")" = candidate ] && [ -e "$MOCK_ROOT/releases/manifest.txt" ] || {
      cat "$TEST_ROOT/$failure.log" >&2; echo "$failure: adequate release did not reach candidate" >&2; exit 1;
    }
  else
    [ "$result" = 1 ] && [ "$(cat "$MOCK_STATE")" = previous ] || {
      cat "$TEST_ROOT/$failure.log" >&2; echo "$failure: capacity failure did not preserve the prior service" >&2; exit 1;
    }
    rg -q '\[capacity\] refused:' "$TEST_ROOT/$failure.log" || { cat "$TEST_ROOT/$failure.log" >&2; exit 1; }
    ! rg -q '^candidate-up$' "$MOCK_TRACE" || { echo "$failure: candidate was started" >&2; exit 1; }
    if [[ "$failure" != after-stop-drop && "$failure" != after-backup-drop ]]; then
      ! rg -q '^stop$' "$MOCK_TRACE" || { echo "$failure: old service was stopped" >&2; exit 1; }
    fi
    if [[ "$failure" = after-build-drop || "$failure" = archive-overhead || "$failure" = inode-overhead ]]; then
      rg -q '^build$' "$MOCK_TRACE" || { echo "$failure: final gate was not exercised" >&2; exit 1; }
    fi
    for target in data storage-go web-data; do
      [ "$(cat "$MOCK_ROOT/production/$target/marker")" = original ] || { echo "$failure: live data changed" >&2; exit 1; }
    done
    [ ! -e "$MOCK_ROOT/releases/manifest.txt" ] || { echo "$failure: failed release was recorded" >&2; exit 1; }
  fi
  [ "$(cat "$MOCK_ROOT/backups/earlier/marker")" = retained ] || { echo "$failure: prior backup changed" >&2; exit 1; }
  echo "PASS capacity/$failure"
done

# Run the real release process with permissive inherited masks. Only the new
# artifacts must become private; an earlier backup must keep its original mode.
for inherited_mask in 022 002; do
  for failure in capacity-adequate unhealthy; do
    export MOCK_FAILURE="$failure"
    unset MOCK_UP_FAIL || true
    rm -rf "$MOCK_ROOT/backups" "$MOCK_ROOT/releases" "$MOCK_ROOT/production/reports"
    mkdir -p "$MOCK_ROOT/backups/earlier"
    echo retained >"$MOCK_ROOT/backups/earlier/marker"
    chmod 755 "$MOCK_ROOT/backups/earlier"
    chmod 644 "$MOCK_ROOT/backups/earlier/marker"
    rm -f "$MOCK_BUILD_DONE"
    : >"$MOCK_TRACE"
    echo previous >"$MOCK_STATE"
    for target in data storage-go web-data; do
      rm -rf "$MOCK_ROOT/production/$target"
      mkdir -p "$MOCK_ROOT/production/$target"
      echo original >"$MOCK_ROOT/production/$target/marker"
    done
    set +e
    (umask "$inherited_mask"; bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT") >"$TEST_ROOT/permissions-$inherited_mask-$failure.log" 2>&1
    result=$?
    set -e
    if [ "$failure" = capacity-adequate ]; then
      [ "$result" = 0 ] && [ "$(cat "$MOCK_STATE")" = candidate ] || {
        echo "permissions/$inherited_mask/$failure: release did not succeed" >&2; exit 1;
      }
    else
      [ "$result" = 1 ] && [ "$(cat "$MOCK_STATE")" = previous ] || {
        echo "permissions/$inherited_mask/$failure: rollback failed" >&2; exit 1;
      }
      for target in data storage-go web-data; do
        [ "$(cat "$MOCK_ROOT/production/$target/marker")" = original ] || {
          echo "permissions/$inherited_mask/$failure: data was not restored" >&2; exit 1;
        }
      done
    fi
    python3 - "$MOCK_ROOT" "$failure" <<'PY_PERMISSIONS'
import pathlib,stat,sys
root=pathlib.Path(sys.argv[1])
def mode(path,wanted):
 actual=stat.S_IMODE(path.stat().st_mode)
 if actual!=wanted:raise SystemExit(f'{path.relative_to(root)}: expected {wanted:04o}, got {actual:04o}')
backup=root/'backups/release-candidate'
mode(backup,0o700)
for target in ['data','storage-go','web-data']:mode(backup/(target+'.tar'),0o600)
mode(root/'backups/earlier',0o755)
mode(root/'backups/earlier/marker',0o644)
if (root/'backups/earlier/marker').read_text()!='retained\n':raise SystemExit('earlier backup changed')
if sys.argv[2]=='capacity-adequate':
 mode(root/'releases',0o700)
 mode(root/'releases/manifest.txt',0o600)
else:
 reports=root/'production/reports/release-failures'
 mode(root/'production/reports',0o700)
 mode(reports,0o700)
 mode(reports/'candidate-unhealthy-after-up.txt',0o600)
 if (root/'releases/manifest.txt').exists():raise SystemExit('failed candidate entered manifest')
PY_PERMISSIONS
    echo "PASS permissions/$inherited_mask/$failure"
  done
done

# Database-only scope: archive/replace only data; candidate CAS and web-data
# writes stay as orphaned bytes (this is not a full rollback).
for failure in capacity-adequate unhealthy up-failed; do
  export MOCK_FAILURE="$failure"
  export RELEASE_BACKUP_SCOPE=database RELEASE_BACKUP_KEEP=2
  unset MOCK_UP_FAIL || true
  rm -rf "$MOCK_ROOT/backups" "$MOCK_ROOT/releases" "$MOCK_ROOT/production/reports"
  mkdir -p "$MOCK_ROOT/backups/earlier"
  echo retained >"$MOCK_ROOT/backups/earlier/marker"
  rm -f "$MOCK_BUILD_DONE"
  : >"$MOCK_TRACE"
  echo previous >"$MOCK_STATE"
  for target in data storage-go web-data; do
    rm -rf "$MOCK_ROOT/production/$target"
    mkdir -p "$MOCK_ROOT/production/$target"
    echo original >"$MOCK_ROOT/production/$target/marker"
  done
  if [ "$failure" = up-failed ]; then
    export MOCK_UP_FAIL=1
  fi
  set +e
  bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/database-$failure.log" 2>&1
  result=$?
  set -e
  [ -f "$MOCK_ROOT/backups/release-candidate/data.tar" ] || { echo "database/$failure: data.tar missing" >&2; exit 1; }
  [ ! -e "$MOCK_ROOT/backups/release-candidate/storage-go.tar" ] &&
    [ ! -e "$MOCK_ROOT/backups/release-candidate/web-data.tar" ] ||
    { echo "database/$failure: full archive created" >&2; exit 1; }
  grep -qx 'database' "$MOCK_ROOT/backups/release-candidate/scope" || {
    echo "database/$failure: scope metadata missing" >&2; exit 1;
  }
  if [ "$failure" = capacity-adequate ]; then
    [ "$result" = 0 ] && [ "$(cat "$MOCK_STATE")" = candidate ] &&
      rg -q '^backup-retention .*--keep 2 --apply$' "$MOCK_TRACE" || {
      cat "$TEST_ROOT/database-$failure.log" >&2
      echo "database/$failure: successful release did not retain backups" >&2; exit 1;
    }
  else
    [ "$result" = 1 ] && [ "$(cat "$MOCK_STATE")" = previous ] || {
      cat "$TEST_ROOT/database-$failure.log" >&2
      echo "database/$failure: rollback failed" >&2; exit 1;
    }
    [ "$(cat "$MOCK_ROOT/production/data/marker")" = original ] || {
      echo "database/$failure: database was not restored" >&2; exit 1;
    }
    for target in storage-go web-data; do
      [ "$(cat "$MOCK_ROOT/production/$target/marker")" = changed ] || {
        echo "database/$failure: candidate $target bytes were not kept" >&2; exit 1;
      }
    done
    ! rg -q '^backup-retention' "$MOCK_TRACE" || {
      echo "database/$failure: failed release ran backup retention" >&2; exit 1;
    }
    rg -q '^backup_scope=database$' "$MOCK_ROOT/production/reports/release-failures" || {
      echo "database/$failure: failure report lacks the backup scope" >&2; exit 1;
    }
    rg -q '^backup_targets=data$' "$MOCK_ROOT/production/reports/release-failures" || {
      echo "database/$failure: failure report lacks the backup targets" >&2; exit 1;
    }
  fi
  echo "PASS database/$failure"
done
unset RELEASE_BACKUP_SCOPE RELEASE_BACKUP_KEEP MOCK_UP_FAIL

# Per-root release lock (#364): supported recovery and retention share one
# lock. A release must stop before build/data changes, an inherited descriptor
# from release-lock.py run must be accepted, and a forged descriptor refused.
rm -rf "$MOCK_ROOT/backups" "$MOCK_ROOT/releases" "$MOCK_ROOT/production/reports"
for target in data storage-go web-data; do
  mkdir -p "$MOCK_ROOT/production/$target"
  echo original >"$MOCK_ROOT/production/$target/marker"
done
: >"$MOCK_TRACE"
rm -f "$MOCK_BUILD_DONE"
echo previous >"$MOCK_STATE"
mkfifo "$TEST_ROOT/hold.fifo"
python3 "$TEST_ROOT/root/app/scripts/release-lock.py" hold --root "$MOCK_ROOT" \
  <"$TEST_ROOT/hold.fifo" >"$TEST_ROOT/hold.out" 2>&1 &
holder=$!
exec 9>"$TEST_ROOT/hold.fifo"
held=0
for _ in $(seq 1 200); do
  if ! python3 "$TEST_ROOT/root/app/scripts/release-lock.py" run --root "$MOCK_ROOT" -- true \
    >/dev/null 2>&1; then
    held=1
    break
  fi
  /bin/sleep 0.05
done
if [ "$held" != 1 ]; then
  exec 9>&-
  kill "$holder" 2>/dev/null || true
  wait "$holder" 2>/dev/null || true
  echo "holder never acquired the release lock" >&2
  exit 1
fi
if bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/lock-busy.log" 2>&1; then
  exec 9>&-
  kill "$holder" 2>/dev/null || true
  echo "release ran while the recovery lock was held" >&2
  exit 1
fi
exec 9>&-
kill "$holder" 2>/dev/null || true
wait "$holder" 2>/dev/null || true
grep -q 'release lock is busy' "$TEST_ROOT/lock-busy.log" || {
  cat "$TEST_ROOT/lock-busy.log" >&2; echo "held lock was not reported" >&2; exit 1;
}
[ ! -s "$MOCK_TRACE" ] || { cat "$MOCK_TRACE" >&2; echo "held lock ran release steps" >&2; exit 1; }
for target in data storage-go web-data; do
  [ "$(cat "$MOCK_ROOT/production/$target/marker")" = original ] || {
    echo "held lock changed $target" >&2; exit 1;
  }
done
echo "PASS release-lock/busy"

unset MOCK_FAILURE
: >"$MOCK_TRACE"
rm -f "$MOCK_BUILD_DONE"
echo previous >"$MOCK_STATE"
python3 "$TEST_ROOT/root/app/scripts/release-lock.py" run --root "$MOCK_ROOT" -- \
  bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/lock-inherited.log" 2>&1 || {
  cat "$TEST_ROOT/lock-inherited.log" >&2; echo "inherited release lock failed" >&2; exit 1;
}
[ -f "$MOCK_BUILD_DONE" ] || { echo "inherited lock did not build" >&2; exit 1; }
[ "$(cat "$MOCK_STATE")" = candidate ] || { echo "inherited lock did not release" >&2; exit 1; }
echo "PASS release-lock/inherited"

: >"$MOCK_TRACE"
rm -f "$MOCK_BUILD_DONE"
echo previous >"$MOCK_STATE"
if RELEASE_LOCK_FD=0 RELEASE_LOCK_ROOT="$MOCK_ROOT" \
  bash "$RELEASE_SCRIPT" candidate test-sha "$MOCK_ROOT" >"$TEST_ROOT/lock-invalid.log" 2>&1; then
  echo "forged inherited lock was accepted" >&2; exit 1
fi
grep -q 'inherited release lock' "$TEST_ROOT/lock-invalid.log" || {
  cat "$TEST_ROOT/lock-invalid.log" >&2; echo "forged lock was not reported" >&2; exit 1;
}
[ ! -s "$MOCK_TRACE" ] && [ ! -e "$MOCK_BUILD_DONE" ] || {
  echo "forged inherited lock changed state" >&2; exit 1;
}
echo "PASS release-lock/invalid-inherited"

python3 - "$SCRIPT_DIR/release-capacity.py" "$TEST_ROOT" <<'PY_CAPACITY_TEST'
import importlib.util,io,json,os,pathlib,signal,sys,tarfile,tempfile,tracemalloc,types,unittest
from unittest.mock import patch
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('release_capacity',sys.argv[1])
c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
base=pathlib.Path(sys.argv[2])

class CapacityTests(unittest.TestCase):
 def setUp(self):
  self.temporary=tempfile.TemporaryDirectory(dir=base)
  self.addCleanup(self.temporary.cleanup)
  self.root=pathlib.Path(self.temporary.name)

 def archive(self,name='data',members=None,format=tarfile.USTAR_FORMAT):
  path=self.root/(name+'.tar')
  with tarfile.open(path,'w',format=format) as t:
   directory=tarfile.TarInfo(name);directory.type=tarfile.DIRTYPE;t.addfile(directory)
   for member in members if members is not None else [name+'/file']:
    if isinstance(member,str):
     parent=os.path.dirname(member)
     if parent != name:
      directory=tarfile.TarInfo(parent);directory.type=tarfile.DIRTYPE;t.addfile(directory)
     info=tarfile.TarInfo(member);info.size=128<<10;t.addfile(info,io.BytesIO(bytes(info.size)))
    else:t.addfile(member)
  return path

 def test_arithmetic_and_invalid_measurements(self):
  self.assertEqual(c.add(c.LIMIT,0),c.LIMIT)
  self.assertEqual(c.multiply(c.LIMIT,1),c.LIMIT)
  for value in [-1,'', 'unknown','１',c.LIMIT+1]:
   with self.subTest(value=str(value)),self.assertRaises(c.CapacityError):c.number(value)
  for function,args in [(c.add,(c.LIMIT,1)),(c.multiply,(c.LIMIT,2))]:
   with self.assertRaises(c.CapacityError):function(*args)

 def test_output_is_bounded_before_buffering(self):
  tracemalloc.start()
  try:
   with self.assertRaisesRegex(c.CapacityError,'output exceeded'):
    c.command(sys.executable,'-c','import sys,time;sys.stdout.buffer.write(b"x"*10000000);sys.stdout.flush();time.sleep(30)')
   _,peak=tracemalloc.get_traced_memory()
   self.assertLess(peak,2<<20)
   print('PASS bounded-output/peak-bytes='+str(peak))
  finally:tracemalloc.stop()

 def test_command_deadline_and_locale(self):
  with patch.dict(os.environ,{'LC_ALL':'synthetic-locale'}):
   self.assertEqual(c.command(sys.executable,'-c','import os;print(os.environ["LC_ALL"])').strip(),'C')
  with self.assertRaisesRegex(c.CapacityError,'timed out'):
   c.command(sys.executable,'-c','import time;time.sleep(30)')

 def test_per_filesystem_grouping_and_nonroot_availability(self):
  def device(p):return 1 if str(p) in ['/first','/alias'] else 2
  usage=types.SimpleNamespace(f_frsize=4096,f_bavail=(6<<30)//4096,f_bfree=(100<<30)//4096,f_favail=9000000)
  def df(*args):
   if args[1]=='--output=iavail':return 'IFree\n8000000\n'
   return 'Avail\n'+str((3 if args[-1]=='/alias' else 4)<<30)+'\n'
  with patch.object(c,'ancestor',lambda p:p),patch.object(pathlib.Path,'stat',lambda p:types.SimpleNamespace(st_dev=device(p))),patch.object(c.os,'statvfs',lambda p:usage),patch.object(c,'command',df):
   capacity=c.Capacity()
   capacity.require(pathlib.Path('/first'),100,2)
   capacity.require(pathlib.Path('/alias'),200,3)
   capacity.require(pathlib.Path('/second'),500,7)
   self.assertEqual(len(capacity.devices),2)
   self.assertEqual(capacity.devices[1]['bytes'],3<<30)
   self.assertEqual(capacity.devices[1]['need_bytes'],c.HEADROOM+300)
   self.assertEqual(capacity.devices[2]['need_bytes'],c.HEADROOM+500)
   capacity.verify()
   capacity.devices[2]['inodes']=0
   with self.assertRaises(c.CapacityError):capacity.verify()

 def test_unknown_df_and_unknown_statvfs_fail_closed(self):
  for output in ['Avail\nunknown\n','Avail\n-1\n','Wrong\n123\n','Avail\n1\n2\n']:
   with self.subTest(output=output),patch.object(c,'command',lambda *a:output),self.assertRaises(c.CapacityError):
    c.Capacity().filesystem(self.root)
  bad=types.SimpleNamespace(f_frsize=0,f_bavail=10,f_favail=10)
  with patch.object(c.os,'statvfs',lambda p:bad),self.assertRaises(c.CapacityError):c.Capacity().filesystem(self.root)

 def test_tree_measurement_bounds(self):
  (self.root/'nested').mkdir()
  for n in range(4):(self.root/str(n)).write_bytes(b'x')
  with patch.object(c,'command',lambda *a:'4\t'+str(self.root)+'\n'):
   with patch.object(c,'MAX_ENTRIES',3),self.assertRaises(c.CapacityError):c.tree(self.root)
   with patch.object(c,'MAX_DEPTH',0),self.assertRaises(c.CapacityError):c.tree(self.root)
   with patch.object(c,'MAX_PATH',1),self.assertRaises(c.CapacityError):c.tree(self.root)
   with patch.object(c.time,'monotonic',side_effect=[0,21]),self.assertRaises(c.CapacityError):c.tree(self.root)

 def test_actual_sparse_apparent_size(self):
  sparse=self.root/'sparse'
  with sparse.open('wb') as f:f.truncate(16<<20)
  self.assertLess(sparse.stat().st_blocks*512,1<<20)
  with patch.object(c,'command',lambda *a:str(sparse.stat().st_size)+'\t'+str(self.root)+'\n'):
   size,count,_=c.tree(self.root)
  self.assertEqual(size,16<<20);self.assertEqual(count,2)

 def test_standard_and_longname_archive_footprints(self):
  for format in [tarfile.USTAR_FORMAT,tarfile.GNU_FORMAT,tarfile.PAX_FORMAT]:
   with self.subTest(format=format):
    member='data/'+('a'*80)+'/'+('b'*80) if format==tarfile.USTAR_FORMAT else 'data/'+('a'*200)
    path=self.archive(members=[member],format=format)
    footprint,count=c.archive_size(path,'data',4096)
    self.assertGreaterEqual(footprint,128<<10);self.assertEqual(count,3 if format==tarfile.USTAR_FORMAT else 2)

 def test_safe_symlink_and_hardlink(self):
  link=tarfile.TarInfo('data/link');link.type=tarfile.SYMTYPE;link.linkname='file'
  hard=tarfile.TarInfo('data/hard');hard.type=tarfile.LNKTYPE;hard.linkname='data/file'
  path=self.archive(members=['data/file',link,hard])
  _,count=c.archive_size(path,'data',4096);self.assertEqual(count,4)

 def test_unsafe_archive_paths_types_and_extended_metadata(self):
  members=['/outside','data/../outside','other/file']
  link=tarfile.TarInfo('data/link');link.type=tarfile.SYMTYPE;link.linkname='/outside';members.append(link)
  fifo=tarfile.TarInfo('data/fifo');fifo.type=tarfile.FIFOTYPE;members.append(fifo)
  badlink=tarfile.TarInfo('data/hard');badlink.type=tarfile.LNKTYPE;badlink.linkname='data';members.append(badlink)
  for member in members:
   with self.subTest(kind=type(member).__name__),self.assertRaises((c.CapacityError,tarfile.TarError)):
    c.archive_size(self.archive(members=[member]),'data',4096)
  unknown=tarfile.TarInfo('data/file');unknown.pax_headers={'SCHILY.xattr.synthetic':'value'}
  with self.assertRaises(c.CapacityError):c.archive_size(self.archive(members=[unknown],format=tarfile.PAX_FORMAT),'data',4096)

 def test_archive_corruption_and_exact_end_records(self):
  path=self.archive();original=path.read_bytes()
  invalid=[]
  checksum=bytearray(original);checksum[0]^=1;invalid.append(checksum)
  invalid.append(original[:1024])
  # Root + file header + 128KiB payload then exactly one end marker.
  invalid.append(original[:1024+(128<<10)+512])
  invalid.append(original+bytes(512)+b'x'*512)
  for payload in invalid:
   path.write_bytes(payload)
   with self.subTest(bytes=len(payload)),self.assertRaises((c.CapacityError,tarfile.TarError)):c.archive_size(path,'data',4096)

 def test_archive_metadata_and_header_resource_bounds(self):
  path=self.root/'data.tar'
  info=tarfile.TarInfo('metadata');info.type=tarfile.XHDTYPE;info.size=1<<30
  path.write_bytes(info.tobuf()+bytes(1024))
  with self.assertRaises(c.CapacityError):c.archive_size(path,'data',4096)
  path=self.archive()
  with patch.object(c,'MAX_ENTRIES',1),self.assertRaises(c.CapacityError):c.archive_size(path,'data',4096)

 def test_saved_archives_not_empty_or_growing_live_trees(self):
  backup=self.root/'backups/release-test';backup.mkdir(parents=True)
  production=self.root/'production';production.mkdir()
  (self.root/'app').mkdir();(self.root/'docker').mkdir()
  mounts=[]
  for name,destination in [('data','data'),('storage-go','storage'),('web-data','web-data')]:
   path=production/name;path.mkdir()
   archive=self.archive(name=name)
   archive.rename(backup/(name+'.tar'))
   mounts.append({'Type':'bind','Source':str(path),'Destination':'/app/'+destination,'RW':destination!='web-data'})
  def docker(*args):
   self.assertEqual(args[0],'docker')
   return str(self.root/'docker') if args[1]=='info' else json.dumps(mounts)
  results=[]
  class Recording:
   def __init__(self):self.devices={1:{'block':4096}};self.need=c.HEADROOM;self.limit=2<<30
   def filesystem(self,path):return 1
   def require(self,path,byte_count=0,inode_count=0):self.need+=byte_count;return 1
   def verify(self):
    results.append(self.need)
    if self.need>self.limit:raise c.CapacityError('insufficient bytes')
  for grown in [False,True]:
   if grown:(production/'data/new').write_bytes(bytes(4<<20))
   with patch.object(c,'Capacity',Recording),patch.object(c,'command',docker),patch.object(c,'tree',side_effect=AssertionError('live trees must not determine rollback size')):
    c.check(self.root,'container','restore',backup)
  self.assertEqual(results[0],results[1]);self.assertGreater(results[0],c.HEADROOM+(3*128<<10))
  low=Recording();low.limit=c.HEADROOM+1
  with patch.object(c,'Capacity',lambda:low),patch.object(c,'command',docker),self.assertRaises(c.CapacityError):
   c.check(self.root,'container','before-up',backup)

 def test_database_scope_measures_only_data_and_keeps_mount_checks(self):
  backup=self.root/'backups/release-test';backup.mkdir(parents=True)
  production=self.root/'production';production.mkdir()
  (self.root/'app').mkdir();(self.root/'docker').mkdir()
  mounts=[]
  for name,destination in [('data','data'),('storage-go','storage'),('web-data','web-data')]:
   path=production/name;path.mkdir()
   mounts.append({'Type':'bind','Source':str(path),'Destination':'/app/'+destination,'RW':destination!='web-data'})
  self.archive(name='data').rename(backup/'data.tar')
  def docker(*args):
   self.assertEqual(args[0],'docker')
   return str(self.root/'docker') if args[1]=='info' else json.dumps(mounts)
  class Recording:
   def __init__(self):self.devices={1:{'block':4096}};self.need=c.HEADROOM
   def filesystem(self,path):return 1
   def require(self,path,byte_count=0,inode_count=0):self.need+=byte_count;return 1
   def verify(self):pass
  with patch.object(c,'Capacity',Recording),patch.object(c,'command',docker),patch.object(c,'tree',side_effect=AssertionError('live trees must not determine rollback size')):
   c.check(self.root,'container','restore',backup,'database')
   with self.assertRaises(c.CapacityError):
    c.check(self.root,'container','restore',backup,'full')
  mounts[0]['Source']=str(production/'storage-go')
  with patch.object(c,'Capacity',Recording),patch.object(c,'command',docker):
   with self.assertRaises(c.CapacityError):
    c.check(self.root,'container','restore',backup,'database')
  with self.assertRaises(c.CapacityError):
   c.check(self.root,'container','restore',backup,'invalid')

 def test_archive_source_and_release_phase_are_fixed(self):
  with self.assertRaises(c.CapacityError):c.check(self.root,'container','skip',self.root/'backups/release-test')
  with self.assertRaises(c.CapacityError):c.check(self.root,'container','restore',self.root/'different/release-test')

unittest.main(argv=['release capacity tests'],verbosity=2)
PY_CAPACITY_TEST

echo 'release rollback fault injection passed'
