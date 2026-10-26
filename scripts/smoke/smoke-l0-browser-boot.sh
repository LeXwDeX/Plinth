#!/usr/bin/env bash
# L0 真实浏览器启动检查（部署后必跑，零费用、不产生任何生成任务）。
# 用法（服务器上）: ART_AI_SERVER_ROOT=<发布根> bash smoke-l0-browser-boot.sh
#   或（本地）:     python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l0-browser-boot.sh
# 有 ADMIN_USERNAME/ADMIN_PASSWORD 时先登录再校验工作区首页，否则只校验公开登录页。
# 依赖：playwright 浏览器驱动（不进发布依赖，按 PLAYWRIGHT_MODULE_PATH 或本地安装解析）。
#   npx playwright install chromium webkit   # 首次需要
set -u
BASE="${BASE:-http://127.0.0.1:13333}"
HERE="$(cd "$(dirname "$0")" && pwd)"

if { [ -n "${ADMIN_USERNAME:-}" ] && [ -z "${ADMIN_PASSWORD:-}" ]; } || { [ -z "${ADMIN_USERNAME:-}" ] && [ -n "${ADMIN_PASSWORD:-}" ]; }; then
  echo "ERROR: ADMIN_USERNAME and ADMIN_PASSWORD must be supplied together" >&2; exit 2
fi
case "$BASE" in
  http://127.0.0.1:13333) ;;
  *)
    if [ -z "${ADMIN_USERNAME:-}" ] || [ -z "${ADMIN_PASSWORD:-}" ]; then
      echo "ERROR: ADMIN_USERNAME and ADMIN_PASSWORD are required for non-default BASE" >&2; exit 2
    fi
    ;;
esac

# 服务器上运行时以 ART_AI_SERVER_ROOT=<该环境发布根> 读取本栈 runtime.env（仓库不写死路径）。
if [ -n "${ART_AI_SERVER_ROOT:-}" ] && [ -f "$ART_AI_SERVER_ROOT/production/runtime.env" ] && [ -z "${ADMIN_USERNAME:-}" ]; then
  set -a; . "$ART_AI_SERVER_ROOT/production/runtime.env"; set +a
fi

BASE="$BASE" node "$HERE/browser-boot.mjs"
