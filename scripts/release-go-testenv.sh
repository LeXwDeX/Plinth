#!/usr/bin/env bash
# Plinth 按环境发布的执行段 —— 本地执行，Agent 控制。入口是
# `python3 scripts/release-env.py release <环境名>`（docs/deployment.md），它从未纳入 Git 的
# deploy/local/environments.json 解析该环境的主机、发布根与栈参数后调用本脚本。
#
# 协议（2026-09-27 用户确认）：
#   - 不走 CI 发布；CI 只做集成测试。发布由 Agent 在本地驱动。
#   - 部署凭据只存在于本地（环境文件中的 ssh 目标免密可用）。
#   - 本入口只更新目标环境的单个 Plinth 服务；其他栈通过各自入口发布。
#   - 健康检查失败 → 自动回滚到上一镜像标签并在服务器留下失败报告；
#     构建与 preflight 失败则中止（旧实例未动，无需回滚）。
#   - 边界：普通发布不重跑 ETL、不改写 migration-ready.txt，也不
#     调用 cutover takeover（那是正式生产切换入口）；见 dev_ops.md 与
#     README-go.md「dev-test 后续普通发布」。
#   - 线上无法在线解决的问题：记录问题（本脚本输出 + 服务器报告文件），
#     回滚，回到本地修复后再次发布。
#
# 用法（通常由 release-env.py 调用）：
#   scripts/release-go-testenv.sh [--skip-source-sync]
# 前提：
#   - 本地 origin/main 已是待发布内容（先 push / fetch）；
#   - RELEASE_HOST 的 ssh 免密可用。
#
# 变量（release-env.py 按环境文件设置；本脚本不写死任何主机或路径）：
#   RELEASE_HOST / RELEASE_ROOT    目标主机（user@host）与发布根（必填）
#   RELEASE_REF                    发布的提交（缺省 origin/main）
#   RELEASE_SERVER_ENV             传给服务器段的栈参数（KEY=VALUE 空格分隔）
#   RELEASE_KEEP_SOURCE_SNAPSHOTS  成功后只保留最新 N 份 app.prev-*（缺省全保留）
#   RELEASE_SOURCE_TRANSFER        scp = 先在本地生成源码 tar，scp 到 <根>/releases/ 后解包并删除
#                                  （缺省经 ssh 管道直接解包）
set -euo pipefail

HOST=${RELEASE_HOST:?RELEASE_HOST is required; release with scripts/release-env.py release <environment>}
REMOTE_ROOT=${RELEASE_ROOT:?RELEASE_ROOT is required; release with scripts/release-env.py release <environment>}
REF=${RELEASE_REF:-origin/main}
SERVER_ENV=${RELEASE_SERVER_ENV:-}
KEEP_SNAPSHOTS=${RELEASE_KEEP_SOURCE_SNAPSHOTS:-}
TRANSFER=${RELEASE_SOURCE_TRANSFER:-pipe}
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
case "$HOST" in
  *[!A-Za-z0-9_.@-]* | *@*@* | @* | *@) echo "[release] RELEASE_HOST must be user@host" >&2; exit 1 ;;
esac
case "$REMOTE_ROOT" in
  /*) ;;
  *) echo "[release] RELEASE_ROOT must be an absolute path" >&2; exit 1 ;;
esac
case "$REMOTE_ROOT" in
  *[!A-Za-z0-9_./-]* | *..*) echo "[release] RELEASE_ROOT may only hold path characters" >&2; exit 1 ;;
esac
case "$SERVER_ENV" in
  *[!A-Za-z0-9_=./:,\ -]*) echo "[release] RELEASE_SERVER_ENV may only hold KEY=VALUE words" >&2; exit 1 ;;
esac
case "$TRANSFER" in
  pipe | scp) ;;
  *) echo "[release] RELEASE_SOURCE_TRANSFER must be pipe or scp" >&2; exit 1 ;;
esac
case "$KEEP_SNAPSHOTS" in
  '' | [1-9]) ;;
  *) echo "[release] RELEASE_KEEP_SOURCE_SNAPSHOTS must be 1-9" >&2; exit 1 ;;
esac

git fetch -q origin
SHA=$(git rev-parse "$REF^{commit}")
TAG="$(git rev-parse --short=7 "$SHA")-$(date -u +%Y%m%dT%H%M%SZ)"
if [ "$REF" = origin/main ]; then
  echo "[release] main=$SHA tag=$TAG"
else
  echo "[release] host=$HOST revision=$SHA tag=$TAG"
fi

# 1) 同步源码（远端旧 app 目录保留为 app.prev-<时间戳>，供回查）
if [[ "${1:-}" != "--skip-source-sync" ]]; then
  if [ "$TRANSFER" = scp ]; then
    ARCHIVE=$(mktemp "${TMPDIR:-/tmp}/art-ai-source.XXXXXX")
    trap 'rm -f "$ARCHIVE"' EXIT
    git archive --format=tar -o "$ARCHIVE" "$SHA"
    ssh "$HOST" "mv $REMOTE_ROOT/app $REMOTE_ROOT/app.prev-\$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true; mkdir -p $REMOTE_ROOT/app $REMOTE_ROOT/releases"
    scp -q "$ARCHIVE" "$HOST:$REMOTE_ROOT/releases/source-$TAG.tar"
    ssh "$HOST" "tar -x -C $REMOTE_ROOT/app -f $REMOTE_ROOT/releases/source-$TAG.tar && rm -f $REMOTE_ROOT/releases/source-$TAG.tar"
  else
    ssh "$HOST" "mv $REMOTE_ROOT/app $REMOTE_ROOT/app.prev-\$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null || true; mkdir -p $REMOTE_ROOT/app"
    git archive "$SHA" | ssh "$HOST" "tar -x -C $REMOTE_ROOT/app"
  fi
fi

# 2) 服务器端：构建 → preflight → 备份 → Compose 更新 → 健康 → 镜像保留；失败自动回滚
# 同步同版 helper，--skip-source-sync 时也不依赖远端旧 app 中是否已有 helper。
scp -q "$SCRIPT_DIR/release-server-steps.sh" "$HOST:/tmp/art-ai-release-steps.sh"
scp -q "$SCRIPT_DIR/retain-art-ai-images.py" "$HOST:/tmp/art-ai-retain-images.py"
ssh "$HOST" "${SERVER_ENV:+$SERVER_ENV }RETENTION_SCRIPT=/tmp/art-ai-retain-images.py bash /tmp/art-ai-release-steps.sh '$TAG' '$SHA' '$REMOTE_ROOT'"

# 3) 单版本主机：成功后只留最新 N 份源码快照（失败时上面已退出，不清理）。
if [ -n "$KEEP_SNAPSHOTS" ]; then
  ssh "$HOST" "cd $REMOTE_ROOT && ls -1d app.prev-[0-9]*T[0-9]*Z 2>/dev/null | sort | head -n -$KEEP_SNAPSHOTS | xargs -r rm -rf --"
fi
