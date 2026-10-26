#!/usr/bin/env bash
# Bounded, authenticated ZIP check. Arguments: URL cookie-jar output-directory.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
URL="$1"; JAR="$2"; OUT="$3"
PARTIAL_BYTES=4194304
ARCHIVE_MAX_BYTES=${ARCHIVE_MAX_BYTES:-209715200}
# Issue #159 requires the first 4 MiB in under 60s. Backend isolation now
# prefilters impossible regex matches; the deadline still fails stalled reads.
ARCHIVE_PARTIAL_TIMEOUT=${ARCHIVE_PARTIAL_TIMEOUT:-59}
[[ "$ARCHIVE_MAX_BYTES" =~ ^[1-9][0-9]*$ ]] || { echo 'invalid ARCHIVE_MAX_BYTES' >&2; exit 1; }

bounded_read() { # destination, headers, byte cap, timeout, optional curl arguments
  local body="$1" headers="$2" cap="$3" timeout="$4"
  local -a rc
  shift 4
  # head deliberately closes the pipe at the cap. Inspect BOTH exit codes.
  set +e
  curl -sS --connect-timeout 5 --max-time "$timeout" -b "$JAR" -D "$headers" "$@" "$URL" \
    2>"$OUT/archive-curl.err" | head -c "$cap" >"$body"
  rc=("${PIPESTATUS[@]}")
  set -e
  [ "${rc[1]}" -eq 0 ] || return 1
  if [ "${rc[0]}" -ne 0 ]; then
    # curl 23 (write error) / 141 (SIGPIPE) is expected only at the byte cap.
    if { [ "${rc[0]}" -eq 23 ] || [ "${rc[0]}" -eq 141 ]; } &&
      [ "$(wc -c <"$body")" -eq "$cap" ]; then
      return 0
    fi
    cat "$OUT/archive-curl.err" >&2
    return 1
  fi
}

bounded_read "$OUT/archive-prefix.zip" "$OUT/archive-prefix.hdr" "$PARTIAL_BYTES" \
  "$ARCHIVE_PARTIAL_TIMEOUT" --range "0-$((PARTIAL_BYTES-1))"
python3 "$HERE/check-archive.py" partial "$OUT/archive-prefix.zip" "$OUT/archive-prefix.hdr"
TOTAL=$(python3 "$HERE/check-archive.py" metadata "$OUT/archive-prefix.zip" "$OUT/archive-prefix.hdr")
if [ "$TOTAL" = unknown ] || [ "$TOTAL" -ge "$ARCHIVE_MAX_BYTES" ]; then
  echo "INFO: archive total=$TOTAL; partial check only (full ZIP cap=$ARCHIVE_MAX_BYTES)"
elif [ "$(wc -c <"$OUT/archive-prefix.zip")" -eq "$TOTAL" ]; then
  python3 "$HERE/check-archive.py" full "$OUT/archive-prefix.zip" "$OUT/archive-prefix.hdr"
else
  bounded_read "$OUT/archive.zip" "$OUT/archive.hdr" "$ARCHIVE_MAX_BYTES" 60
  [ "$(wc -c <"$OUT/archive.zip")" -eq "$TOTAL" ] || { echo 'full archive size mismatch' >&2; exit 1; }
  python3 "$HERE/check-archive.py" full "$OUT/archive.zip" "$OUT/archive.hdr"
fi
