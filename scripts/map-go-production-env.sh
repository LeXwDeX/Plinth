#!/usr/bin/env bash
set -Eeuo pipefail

envmap_root="$(
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &&
    pwd
)"

if [[ -n "${GO_ETL_BIN:-}" ]]; then
  exec "$GO_ETL_BIN" map-env "$@"
fi
if command -v art-ai-etl >/dev/null 2>&1; then
  exec art-ai-etl map-env "$@"
fi
if command -v go >/dev/null 2>&1; then
  cd "$envmap_root"
  exec go run ./cmd/art-ai-etl map-env "$@"
fi

printf '%s\n' \
  '无法执行环境映射：请安装 Go/art-ai-etl，或用 GO_ETL_BIN 指定迁移二进制。' \
  >&2
exit 1
