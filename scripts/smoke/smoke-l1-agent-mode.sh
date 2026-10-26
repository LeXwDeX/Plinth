#!/usr/bin/env bash
# L1 Agent 批准方式 / 笔记 / 选定 / 版本链 / 3D 交付格式 验收冒烟（零费用）。
#
# 覆盖：会话批准方式（迁移 0043）、会话设定摘要（0045）、选定对象（0046）、版本链、
# 任务重试 / 格式清单 / 格式补齐 / 打包 ZIP 的错误路径，以及已有 3D 任务的只读验收
# 和已发布 Agent 配置里的新工具。全程不提交任何生成：不创建 Task，不批准同意，
# 不调用 LLM，不扣积分。会写入的只有新建空会话上的批准方式、设定摘要和选定对象。
#
# 何时跑：涉及本功能集（批准方式、Agent 3D 工具、格式补齐/ZIP、设定摘要、选定、
# 版本链、重试）的每次发布后都要跑，与 smoke-l1-free.sh 一起跑。
#
# 用法（服务器上）: EXPECT_SHA=<完整 origin/main SHA> bash smoke-l1-agent-mode.sh
#   或（本地）:     python3 scripts/release-env.py smoke dev-test -- \
#                   bash scripts/smoke/smoke-l1-agent-mode.sh
# 凭据：服务器上自动读 production/runtime.env；本地用 ADMIN_* 环境变量，不打印密码。
# 可选环境变量:
#   EXPECT_SHA          期望的构建版本（完整 SHA）。设置后必须一致，否则 FAIL。
#   RELEASE_CONTAINER   服务器上的容器名（release-env.py smoke 自动设置；服务器上手工运行时
#                       设为 `release-env.py show <环境>` 的 container；未设则不读容器 revision）。
#   SMOKE_CATALOG       default_catalog.json 路径，用于读取 7 种交付格式的 ID。
#   ARCHIVE_MAX_BYTES   已知 ZIP 总大小低于此值时做全量检查（默认 200 MiB）。
#   ARCHIVE_PARTIAL_TIMEOUT  ZIP 前 4 MiB 读取超时（默认 59 秒，不用 req 的 60 秒）。
# 退出码：0 = 无 FAIL（可含 SKIP）；1 = 有 FAIL。
# 数据保留：产生的空会话不清理（协议约定保留于 dev-test）。
set -u
BASE="${BASE:-http://127.0.0.1:13333}"
EXPECT_SHA="${EXPECT_SHA:-}"
RELEASE_CONTAINER="${RELEASE_CONTAINER:-}"
ARCHIVE_MAX_BYTES="${ARCHIVE_MAX_BYTES:-209715200}"
ARCHIVE_PARTIAL_TIMEOUT="${ARCHIVE_PARTIAL_TIMEOUT:-59}"
export ARCHIVE_MAX_BYTES ARCHIVE_PARTIAL_TIMEOUT
# 服务器上运行时设 ART_AI_SERVER_ROOT=<该环境发布根>；仓库不写死路径。
SERVER_ROOT="${ART_AI_SERVER_ROOT:-}"
PASS=0; FAIL=0; SKIP=0
ok()   { echo "PASS: $1"; PASS=$((PASS+1)); }
bad()  { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
skip() { echo "SKIP: $1"; SKIP=$((SKIP+1)); }
HERE="$(cd "$(dirname "$0")" && pwd)"
T="$(mktemp -d)" || { echo "FAIL: mktemp"; exit 1; }
trap 'rm -rf "${T:?}"' EXIT
JAR="$T/cookies.txt"
req() { local out="$1"; shift; curl -s --connect-timeout 5 --max-time 60 -o "$out" -w "%{http_code}" "$@"; }
# jget FILE EXPR：对 JSON 文件求值（d 为解析结果）；失败输出空串。
jget() { python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    v = eval(sys.argv[2])
    print("" if v is None else v)
except Exception:
    print("")' "$1" "$2" 2>/dev/null; }
is4xx() { case "$1" in 4[0-9][0-9]) return 0 ;; *) return 1 ;; esac; }

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

if [ -n "$SERVER_ROOT" ] && [ -f "$SERVER_ROOT/production/runtime.env" ] && [ -z "${ADMIN_USERNAME:-}" ]; then
  set -a; . "$SERVER_ROOT/production/runtime.env"; set +a
fi

# 1. 健康检查
[ "$(req "$T/o.json" "$BASE/healthz")" = 200 ] && ok "healthz" || bad "healthz"
[ "$(req "$T/o.json" "$BASE/readyz")" = 200 ] && ok "readyz" || bad "readyz"

# 2. 构建版本（#229）。/api/version 返回链接进二进制的提交，与容器 OCI 标签
#    org.opencontainers.image.revision 同源。三者必须一致：/api/version = EXPECT_SHA
#    = 容器 revision（服务器上可读时）。不再有 SKIP：缺 EXPECT_SHA 或读不到即 FAIL。
C=$(req "$T/ver.json" "$BASE/api/version")
API_VER=$(jget "$T/ver.json" 'd["version"]')
if [ "$C" = 200 ] && [ -n "$API_VER" ]; then ok "/api/version readable (version=$API_VER)"; else bad "/api/version ($C)"; fi
IMG_REV=""
if [ -n "$RELEASE_CONTAINER" ] && command -v docker >/dev/null 2>&1; then
  IMG_REV=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$RELEASE_CONTAINER" 2>/dev/null || true)
  case "$IMG_REV" in "<no value>") IMG_REV="" ;; esac
fi
[ -n "$IMG_REV" ] && echo "INFO: container revision label=$IMG_REV"
if [ -z "$EXPECT_SHA" ]; then
  bad "EXPECT_SHA is required: set it to the full origin/main SHA being verified"
else
  EXPECT_LC=$(printf '%s' "$EXPECT_SHA" | tr 'A-F' 'a-f')
  [ "$API_VER" = "$EXPECT_LC" ] && ok "/api/version == EXPECT_SHA" || bad "/api/version '$API_VER' != EXPECT_SHA $EXPECT_SHA"
  if [ -n "$IMG_REV" ]; then
    [ "$IMG_REV" = "$EXPECT_LC" ] && ok "container revision == EXPECT_SHA" || bad "container revision $IMG_REV != EXPECT_SHA $EXPECT_SHA"
  else
    echo "INFO: container label not readable here (not on the server); /api/version alone was compared"
  fi
fi

# 2b. 两栈日志（#233）：服务器上能读到本栈 logs/art-ai.log 时，最近一次启动必须以 DEBUG 配置、
#     且文件可由服务写入（存在且非空）。不在服务器上运行时只提示，不计通过。
LOG_FILE="${ART_AI_LOG_FILE:-${SERVER_ROOT:+$SERVER_ROOT/production/logs/art-ai.log}}"
if [ -z "$LOG_FILE" ]; then
  # Run off the server (e.g. `release-env.py smoke`): no release root, so no log path.
  echo "INFO: log directory not visible here; run on the server to check #233 file logging"
elif [ -r "$LOG_FILE" ]; then
  LAST_LOG_CONFIG=$(grep '"msg":"日志已配置"' "$LOG_FILE" | tail -1)
  if [ -n "$LAST_LOG_CONFIG" ] && printf '%s' "$LAST_LOG_CONFIG" | grep -q '"log_level":"DEBUG"' && printf '%s' "$LAST_LOG_CONFIG" | grep -q '"directory":"/app/logs"'; then
    ok "application log file written at DEBUG ($LOG_FILE)"
  else
    bad "application log file lacks a DEBUG startup line ($LOG_FILE)"
  fi
elif [ -d "$(dirname "$LOG_FILE")" ]; then
  bad "application log file missing or unreadable: $LOG_FILE"
else
  echo "INFO: log directory not visible here; run on the server to check #233 file logging"
fi

# 3. 登录
C=$(req "$T/o.json" -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN_USERNAME:-}\",\"password\":\"${ADMIN_PASSWORD:-}\"}" -c "$JAR")
if [ "$C" = 200 ]; then ok "login"; else bad "login ($C)"; echo "=== ABORT ==="; echo "=== SMOKE-L1-AGENT-MODE PASS=$PASS FAIL=$FAIL SKIP=$SKIP ==="; exit 1; fi

# Schema 里的批准策略：docs/product.md 规定这四种能力在手工模式需要人工批准
C=$(req "$T/schema.json" "$BASE/api/v1/schema")
SCHEMA_STATUS="$C"
POLICY=$(jget "$T/schema.json" '(lambda p: "%s|%s" % (p["default_mode"], ",".join(sorted(p["manual_approval_capabilities"]))))(d["schema"]["chat"]["approval_policy"])')
if [ "$C" = 200 ] && [ "$POLICY" = "manual|image-to-3d,image-to-splat,retexture,text-to-3d" ]; then
  ok "schema approval policy: default manual; manual only for text-to-3d, image-to-3d, image-to-splat, retexture"
else
  bad "schema approval policy unexpected ($C '$POLICY')"
fi

# 4. 新会话与批准方式
C=$(req "$T/sess.json" -X POST "$BASE/api/chat/sessions" -H 'Content-Type: application/json' \
  -H "Origin: $BASE" -b "$JAR" -d '{"title":"smoke-agent-mode"}')
SID=$(jget "$T/sess.json" 'd["id"]')
if { [ "$C" = 200 ] || [ "$C" = 201 ]; } && [ -n "$SID" ]; then
  ok "create session"
else
  bad "create session ($C)"; echo "=== SMOKE-L1-AGENT-MODE PASS=$PASS FAIL=$FAIL SKIP=$SKIP ==="; exit 1
fi
APPROVAL="$BASE/api/v1/chat/sessions/$SID/approval"
[ "$(jget "$T/sess.json" 'd["approvalMode"]')" = manual ] && ok "new session response carries approvalMode=manual" || bad "new session approvalMode != manual"
C=$(req "$T/o.json" "$APPROVAL" -b "$JAR")
[ "$C" = 200 ] && [ "$(jget "$T/o.json" 'd["approvalMode"]')" = manual ] && ok "GET approval: default manual" || bad "GET approval default ($C mode=$(jget "$T/o.json" 'd["approvalMode"]'))"

put_mode() { # $1=mode → HTTP 状态；响应在 $T/mode.json
  req "$T/mode.json" -X PUT "$APPROVAL" -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$JAR" -d "{\"approvalMode\":\"$1\"}"
}
C=$(put_mode auto)
[ "$C" = 200 ] && [ "$(jget "$T/mode.json" 'd["approvalMode"]')" = auto ] && ok "PUT approval auto" || bad "PUT approval auto ($C)"
C=$(req "$T/o.json" "$APPROVAL" -b "$JAR")
[ "$C" = 200 ] && [ "$(jget "$T/o.json" 'd["approvalMode"]')" = auto ] && ok "GET approval: auto" || bad "GET approval after auto ($C)"
C=$(put_mode manual)
[ "$C" = 200 ] && [ "$(jget "$T/mode.json" 'd["approvalMode"]')" = manual ] && ok "PUT approval manual" || bad "PUT approval manual ($C)"
C=$(req "$T/o.json" -X PUT "$APPROVAL" -H 'Content-Type: application/json' \
  -H 'Origin: http://evil.example' -H 'Sec-Fetch-Site: cross-site' -b "$JAR" -d '{"approvalMode":"auto"}')
[ "$C" = 403 ] && ok "cross-origin PUT approval rejected (403)" || bad "cross-origin PUT approval expected 403 got $C"
C=$(put_mode bogus)
is4xx "$C" && ok "invalid approval mode rejected ($C)" || bad "invalid approval mode expected 4xx got $C"
C=$(req "$T/o.json" "$APPROVAL" -b "$JAR")
[ "$(jget "$T/o.json" 'd["approvalMode"]')" = manual ] && ok "mode unchanged by the rejected requests" || bad "mode changed by a rejected request ($C)"
UNKNOWN_SESSION="session_$(python3 -c 'import secrets;print(secrets.token_urlsafe(18))')"
C=$(req "$T/o.json" "$BASE/api/v1/chat/sessions/$UNKNOWN_SESSION/approval" -b "$JAR")
[ "$C" = 404 ] && ok "approval of an unknown session -> 404" || bad "approval of an unknown session expected 404 got $C"

# 5. 设定摘要（上限 2000 字符）
NOTES="$BASE/api/v1/chat/sessions/$SID/notes"
put_notes_file() { req "$T/notes.json" -X PUT "$NOTES" -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$JAR" --data-binary "@$1"; }
python3 -c 'import json;print(json.dumps({"notes": "角色：红衣少女\n风格：水彩 smoke"}, ensure_ascii=False))' > "$T/n-small.json"
C=$(put_notes_file "$T/n-small.json")
[ "$C" = 200 ] && ok "PUT notes" || bad "PUT notes ($C)"
C=$(req "$T/o.json" "$NOTES" -b "$JAR")
python3 - "$T/o.json" "$T/n-small.json" <<'PYEOF' && ok "GET notes round-trips" || bad "GET notes does not round-trip ($C)"
import json, sys
got = json.load(open(sys.argv[1]))["notes"]
want = json.load(open(sys.argv[2]))["notes"]
sys.exit(0 if got == want else 1)
PYEOF
python3 -c 'import json;print(json.dumps({"notes": "中" * 2000}, ensure_ascii=False))' > "$T/n-2000.json"
python3 -c 'import json;print(json.dumps({"notes": "中" * 2001}, ensure_ascii=False))' > "$T/n-2001.json"
C=$(put_notes_file "$T/n-2000.json")
[ "$C" = 200 ] && ok "2000-character notes accepted" || bad "2000-character notes expected 200 got $C"
C=$(put_notes_file "$T/n-2001.json")
is4xx "$C" && ok "2001-character notes rejected ($C)" || bad "2001-character notes expected 4xx got $C"
C=$(req "$T/o.json" "$NOTES" -b "$JAR")
N_LEN=$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["notes"]))' "$T/o.json" 2>/dev/null)
[ "$C" = 200 ] && [ "$N_LEN" = 2000 ] && ok "rejected notes were not stored or truncated (length still 2000)" || bad "notes after rejection: length=$N_LEN ($C)"
C=$(put_notes_file "$T/n-small.json")
[ "$C" = 200 ] && ok "notes restored to a short value" || bad "restore notes ($C)"

# 6. 选定对象
PICK="$BASE/api/v1/chat/sessions/$SID/pick"
C=$(req "$T/o.json" "$PICK" -b "$JAR")
python3 - "$T/o.json" <<'PYEOF' && [ "$C" = 200 ] && ok "GET pick: empty" || bad "GET pick not empty ($C)"
import json, sys
d = json.load(open(sys.argv[1]))
sys.exit(0 if "pick" in d and d["pick"] is None else 1)
PYEOF
FAKE_TASK="task_$(python3 -c 'import secrets;print(secrets.token_hex(16))')"
FAKE_ASSET="asset_$(python3 -c 'import secrets;print(secrets.token_hex(16))')"
C=$(req "$T/o.json" -X PUT "$PICK" -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$JAR" \
  -d "{\"kind\":\"image\",\"taskId\":\"$FAKE_TASK\",\"assetId\":\"$FAKE_ASSET\"}")
is4xx "$C" && ok "PUT pick with an unknown task rejected ($C)" || bad "PUT pick with an unknown task expected 4xx got $C"
C=$(req "$T/o.json" -X DELETE "$PICK" -H "Origin: $BASE" -b "$JAR")
[ "$C" = 200 ] && ok "DELETE pick" || bad "DELETE pick ($C)"
C=$(req "$T/o.json" "$PICK" -b "$JAR")
[ "$C" = 200 ] && [ -z "$(jget "$T/o.json" 'd["pick"]')" ] && ok "pick still empty after the rejected PUT and the DELETE" || bad "pick not empty after DELETE ($C)"

# 7. 版本链
C=$(req "$T/o.json" "$BASE/api/v1/chat/sessions/$SID/lineage" -b "$JAR")
if [ "$C" = 200 ] && [ "$(jget "$T/o.json" 'isinstance(d["nodes"], list) and isinstance(d["edges"], list)')" = True ]; then
  ok "GET lineage (new session)"
else
  bad "GET lineage ($C)"
fi

# 8. 未知任务：格式清单 / 打包 / 重试 / 补齐 都是 404（补齐与重试是状态变更，带合法 Origin）
GHOST="task_$(python3 -c 'import secrets;print(secrets.token_hex(16))')"
C=$(req "$T/o.json" "$BASE/api/v1/tasks/$GHOST/formats" -b "$JAR")
[ "$C" = 404 ] && ok "formats of an unknown task -> 404" || bad "formats of an unknown task expected 404 got $C"
C=$(req "$T/o.json" "$BASE/api/v1/tasks/$GHOST/archive" -b "$JAR")
[ "$C" = 404 ] && ok "archive of an unknown task -> 404" || bad "archive of an unknown task expected 404 got $C"
C=$(req "$T/o.json" -X POST "$BASE/api/v1/tasks/$GHOST/retry" -H "Origin: $BASE" -b "$JAR")
[ "$C" = 404 ] && ok "retry of an unknown task -> 404" || bad "retry of an unknown task expected 404 got $C"
C=$(req "$T/o.json" -X POST "$BASE/api/v1/tasks/$GHOST/formats/complete" -H "Origin: $BASE" -b "$JAR")
[ "$C" = 404 ] && ok "formats/complete of an unknown task -> 404 (nothing started)" || bad "formats/complete of an unknown task expected 404 got $C"
C=$(req "$T/o.json" -X POST "$BASE/api/v1/tasks/$GHOST/formats/complete" -H 'Origin: http://evil.example' \
  -H 'Sec-Fetch-Site: cross-site' -b "$JAR")
[ "$C" = 403 ] && ok "cross-origin formats/complete rejected (403)" || bad "cross-origin formats/complete expected 403 got $C"

# 9. 已有 3D 任务的只读验收：在已获取的有界候选集内最新的 applicable SUCCEEDED 3D
# 任务。能力列表和 7 种格式都以 Schema 声明为准；任务列表每能力最多 100 条，本段
# 最多检查 500 个去重后的自有候选，不做全量会话扫描，也不保证是全局最新任务。
# 覆盖契约（formats 的 applicable/archive_available）必须严格：formats 必须是
# Schema 格式 ID 的精确集合（字符串 id/status、无重复），缺失、类型错误或读取失败
# 都 FAIL，不静默改用另一个任务。archive_available=false 时必须由 archive 返回
# 413/too_large；随后在同一有界候选列表里选一个 archive_available=true 的既有任务
# 完成真实 ZIP（200/206、README）检查；超过 ARCHIVE_MAX_BYTES 时只做首项检查。
# 没有可下载归档时 FAIL；只有完全没有自有 SUCCEEDED 3D 任务时才 SKIP。
MAX_TASK_CANDIDATES=500
CAT=""
for c in "${SMOKE_CATALOG:-}" "$HERE/../../internal/catalog/default_catalog.json" "${SERVER_ROOT:+$SERVER_ROOT/app/internal/catalog/default_catalog.json}"; do
  [ -n "$c" ] && [ -f "$c" ] && { CAT="$c"; break; }
done
EXPECT_FORMATS=""
[ -n "$CAT" ] && EXPECT_FORMATS=$(jget "$CAT" '" ".join(f["id"] for f in d["application"]["model_delivery"]["formats"])')
if [ -z "$EXPECT_FORMATS" ]; then
  EXPECT_FORMATS="glb gltf fbx obj stl usdz 3mf"
  echo "INFO: catalog formats not found; using the built-in 7-format list"
fi
DELIVERY_CAPS=""
[ -n "$CAT" ] && DELIVERY_CAPS=$(jget "$CAT" '" ".join(d["application"]["model_delivery"]["capabilities"])')
if [ -z "$DELIVERY_CAPS" ]; then
  DELIVERY_CAPS="text-to-3d image-to-3d remesh retexture rigging"
  echo "INFO: catalog capabilities not found; using the built-in 3D capability list"
fi
task_candidates() { # sessions, tasks, cap → newest-first "id<TAB>created_at"
  python3 - "$1" "$2" "$3" <<'PYEOF'
import sys
own = {line.strip() for line in open(sys.argv[1]) if line.strip()}
rows = {}
for line in open(sys.argv[2]):
    row = line.rstrip("\n").split("\t")
    if len(row) == 3 and row[0] and row[1] and row[2] in own:
        rows.setdefault(row[1], row[0])
for task in sorted(rows, key=lambda task: (rows[task], task), reverse=True)[:int(sys.argv[3])]:
    print("%s\t%s" % (task, rows[task]))
PYEOF
}
coverage_state() { # formats JSON, expected formats → skip|available|oversized|malformed
  python3 - "$1" "$2" <<'PYEOF'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    print("malformed"); raise SystemExit
if not isinstance(d, dict) or type(d.get("applicable")) is not bool:
    print("malformed"); raise SystemExit
if not d["applicable"]:
    print("skip"); raise SystemExit
want = sorted(sys.argv[2].split())
formats = d.get("formats")
if not isinstance(formats, list) or not formats:
    print("malformed"); raise SystemExit
ids = []
for item in formats:
    if (not isinstance(item, dict) or type(item.get("id")) is not str
            or type(item.get("status")) is not str):
        print("malformed"); raise SystemExit
    ids.append(item["id"])
if len(ids) != len(set(ids)) or sorted(ids) != want:
    print("malformed"); raise SystemExit
if type(d.get("archive_available")) is not bool:
    print("malformed"); raise SystemExit
print("available" if d["archive_available"] else "oversized")
PYEOF
}
LIST_ERROR=0
: > "$T/sessions.txt"
CURSOR=""
for _ in 1 2 3 4; do
  C=$(req "$T/sess-page.json" "$BASE/api/chat/sessions?limit=100${CURSOR:+&cursor=$CURSOR}" -b "$JAR")
  if [ "$C" != 200 ]; then LIST_ERROR=1; break; fi
  python3 -c 'import json,sys
for s in json.load(open(sys.argv[1])).get("sessions") or []: print(s["id"])' "$T/sess-page.json" >> "$T/sessions.txt" 2>/dev/null || LIST_ERROR=1
  CURSOR=$(jget "$T/sess-page.json" 'd.get("next_cursor") or ""')
  [ -n "$CURSOR" ] || break
done
: > "$T/tasks.txt"
for CAP in $DELIVERY_CAPS; do
  C=$(req "$T/tp.json" "$BASE/api/v1/tasks?capability=$CAP&state=SUCCEEDED&limit=100" -b "$JAR")
  if [ "$C" != 200 ]; then LIST_ERROR=1; continue; fi
  python3 -c 'import json,sys
for t in json.load(open(sys.argv[1])).get("tasks") or []: print("%s\t%s\t%s" % (t.get("created_at", ""), t["id"], t.get("session_id", "")))' "$T/tp.json" >> "$T/tasks.txt" 2>/dev/null || LIST_ERROR=1
done
: > "$T/candidates.txt"
if [ "$LIST_ERROR" = 1 ]; then
  bad "SUCCEEDED 3D task list read failed; latest archive checks not run"
elif ! task_candidates "$T/sessions.txt" "$T/tasks.txt" "$MAX_TASK_CANDIDATES" > "$T/candidates.txt"; then
  bad "SUCCEEDED 3D task candidates read failed"
elif [ ! -s "$T/candidates.txt" ]; then
  skip "no SUCCEEDED 3D task of this user — formats / archive read-only checks not run"
else
  LATEST=""; LATEST_STATE=""
  while IFS=$'\t' read -r TASK _; do
    [ -n "$TASK" ] || continue
    C=$(req "$T/fmt-probe.json" "$BASE/api/v1/tasks/$TASK/formats" -b "$JAR")
    if [ "$C" != 200 ]; then
      bad "formats of candidate $TASK ($C); latest archive checks not run"
      LATEST_STATE=error; break
    fi
    STATE=$(coverage_state "$T/fmt-probe.json" "$EXPECT_FORMATS")
    case "$STATE" in
      skip) continue ;;
      available|oversized)
        LATEST="$TASK"; LATEST_STATE="$STATE"; cp "$T/fmt-probe.json" "$T/latest-formats.json"; break ;;
      *)
        bad "formats of candidate $TASK: malformed coverage"
        LATEST_STATE=error; break ;;
    esac
  done < "$T/candidates.txt"
  if [ "$LATEST_STATE" = error ]; then
    :
  elif [ -z "$LATEST" ]; then
    bad "no applicable SUCCEEDED 3D task within $MAX_TASK_CANDIDATES own candidates"
  else
    echo "INFO: newest applicable SUCCEEDED 3D task in the bounded candidate set: $LATEST (archive_available=$([ "$LATEST_STATE" = available ] && echo true || echo false))"
    ok "GET formats: 200, the Schema format set listed ($EXPECT_FORMATS)"
    ARCHIVE_TASK="$LATEST"
    ARCHIVE_OK=1
    if [ "$LATEST_STATE" = oversized ]; then
      ARCHIVE_STATUS=$(req "$T/archive-probe.json" "$BASE/api/v1/tasks/$LATEST/archive" \
        -H 'Range: bytes=0-0' -b "$JAR")
      ARCHIVE_CODE=$(jget "$T/archive-probe.json" 'd.get("code") if isinstance(d, dict) else ""')
      if [ "$ARCHIVE_STATUS" = 413 ] && [ "$ARCHIVE_CODE" = too_large ]; then
        ok "GET archive of the newest applicable task: 413 too_large (Schema size bound)"
      else
        bad "GET archive of the newest applicable task expected 413 too_large got $ARCHIVE_STATUS code='$ARCHIVE_CODE'"
        ARCHIVE_OK=0
      fi
      if [ "$ARCHIVE_OK" = 1 ]; then
        ELIGIBLE=""
        while IFS=$'\t' read -r TASK _; do
          [ -n "$TASK" ] || continue
          [ "$TASK" = "$LATEST" ] && continue
          C=$(req "$T/fmt-eligible.json" "$BASE/api/v1/tasks/$TASK/formats" -b "$JAR")
          if [ "$C" != 200 ]; then
            bad "formats of candidate $TASK ($C) while selecting an archive; no fallback"
            ELIGIBLE=error; break
          fi
          STATE=$(coverage_state "$T/fmt-eligible.json" "$EXPECT_FORMATS")
          case "$STATE" in
            available) ELIGIBLE="$TASK"; break ;;
            oversized|skip) ;;
            *)
              bad "formats of candidate $TASK: malformed coverage while selecting an archive"
              ELIGIBLE=error; break ;;
          esac
        done < "$T/candidates.txt"
        if [ "$ELIGIBLE" = error ]; then
          ARCHIVE_OK=0
        elif [ -z "$ELIGIBLE" ]; then
          bad "no eligible SUCCEEDED 3D task with a downloadable archive within $MAX_TASK_CANDIDATES own candidates"
          ARCHIVE_OK=0
        else
          ARCHIVE_TASK="$ELIGIBLE"
          echo "INFO: checking the archive of an older eligible task: $ELIGIBLE"
        fi
      fi
    fi
    if [ "$ARCHIVE_OK" = 1 ]; then
      # ZIP：即使服务端忽略 Range 或没有 Content-Length，也只读取前 4 MiB。
      ZIP_RESULT=$(bash "$HERE/read-archive.sh" "$BASE/api/v1/tasks/$ARCHIVE_TASK/archive" "$JAR" "$T" 2>&1)
      ZIP_RC=$?
      if [ "$ZIP_RC" -eq 0 ]; then
        ok "GET archive ($ARCHIVE_TASK): $ZIP_RESULT"
      else
        bad "GET archive ($ARCHIVE_TASK): $ZIP_RESULT"
      fi
    fi
  fi
fi

# 10. 已发布的 Agent 配置含新工具（管理员接口 /api/admin/agent-configuration）
NEW_TOOLS="update_session_notes retexture_model process_model list_session_models plan_model_processing get_processing_plan find_history_images"
# 静态工具目录不证明可执行；先查 Schema 声明的实际 Offering discovery。
if ! CONVERSION_ROUTE=$(python3 - "$T/schema.json" "$SCHEMA_STATUS" <<'PYEOF' 2>/dev/null
import json, re, sys
if sys.argv[2] != "200":
    raise ValueError("invalid schema status")
schema = json.load(open(sys.argv[1]))["schema"]
route = schema["runtime"]["http"]["routes"]["offerings"]
if not isinstance(route, str) or not re.fullmatch(r"/api/[A-Za-z0-9_/-]+", route):
    raise ValueError("invalid offerings route")
tools = schema["chat"]["tools"]
if not isinstance(tools, list) or any(
    not isinstance(t, dict) or not isinstance(t.get("name"), str)
    or not re.fullmatch(r"[A-Za-z0-9_.:-]+", t["name"])
    or not isinstance(t.get("capability"), str) for t in tools
) or len({t["name"] for t in tools}) != len(tools):
    raise ValueError("invalid schema tool mapping")
print(route)
PYEOF
); then
  bad "Agent tool availability: invalid public Schema response"
else
  CONVERSION_STATUS=$(req "$T/conversion-offerings.json" "$BASE$CONVERSION_ROUTE?capability=format-conversion" -b "$JAR")
  if ! REQUIRED_TOOLS=$(python3 - "$T/schema.json" "$T/conversion-offerings.json" "$CONVERSION_STATUS" $NEW_TOOLS <<'PYEOF' 2>/dev/null
import json, sys
if sys.argv[3] != "200":
    raise ValueError("invalid offerings status")
offerings = json.load(open(sys.argv[2]))["offerings"]
if not isinstance(offerings, list) or any(
    not isinstance(o, dict) or type(o.get("available")) is not bool
    or o.get("capability") != "format-conversion" for o in offerings
):
    raise ValueError("invalid offering availability")
required = list(sys.argv[4:])
if any(o["available"] for o in offerings):
    tools = json.load(open(sys.argv[1]))["schema"]["chat"]["tools"]
    conversion_tools = [t["name"] for t in tools if t["capability"] == "format-conversion"]
    if not conversion_tools:
        raise ValueError("available conversion has no tool mapping")
    required.extend(n for n in conversion_tools if n not in required)
print(" ".join(required))
PYEOF
  ); then
    bad "Agent tool availability: invalid offerings or tool mapping response"
  else
    NEW_TOOLS="$REQUIRED_TOOLS"
C=$(req "$T/agentcfg.json" "$BASE/api/admin/agent-configuration" -b "$JAR")
if [ "$C" = 200 ]; then
  if ! MISSING=$(python3 - "$T/agentcfg.json" $NEW_TOOLS <<'PYEOF' 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1]))
revision = d["revision"]
if not isinstance(revision, str) or not revision.strip():
    raise ValueError("invalid revision")
enabled = d["configuration"]["enabled_tool_names"]
if not isinstance(enabled, list) or any(not isinstance(n, str) or not n for n in enabled):
    raise ValueError("invalid enabled tools")
tools = d["tools"]
if not isinstance(tools, list) or any(not isinstance(t, dict) or not isinstance(t.get("name"), str) or not t["name"] for t in tools):
    raise ValueError("invalid tool definitions")
known = {t["name"] for t in tools}
print(" ".join(n for n in sys.argv[2:] if n not in enabled or n not in known))
PYEOF
  ); then
    bad "published Agent configuration: invalid configuration response"
  else
    if [ -z "$MISSING" ]; then
      ok "published Agent configuration enables: $NEW_TOOLS"
    else
      bad "published Agent configuration is missing tools: $MISSING — 保留现有自定义配置并补齐工具后重新发布；仅在明确需要恢复默认时重置"
    fi
  fi
else
  # 非管理员账号读不了管理接口：退回「当前生效配置」，3D 领域应列出全部新工具
  C2=$(req "$T/effective.json" "$BASE/api/v1/agent-configuration/effective?creative_domain=model-3d" -b "$JAR")
  if [ "$C2" = 200 ]; then
    if ! MISSING=$(python3 - "$T/effective.json" $NEW_TOOLS <<'PYEOF' 2>/dev/null
import json, sys
tools = json.load(open(sys.argv[1]))["tools"]
if not isinstance(tools, list) or any(not isinstance(t, dict) or not isinstance(t.get("name"), str) or not t["name"] for t in tools):
    raise ValueError("invalid tool definitions")
names = {t["name"] for t in tools}
print(" ".join(n for n in sys.argv[2:] if n not in names))
PYEOF
    ); then
      bad "effective Agent configuration: invalid configuration response"
    elif [ -z "$MISSING" ]; then
      ok "effective Agent configuration (model-3d) lists: $NEW_TOOLS (admin API gave $C)"
    else
      bad "effective Agent configuration lacks tools: $MISSING — 保留现有自定义配置并补齐工具后重新发布；仅在明确需要恢复默认时重置"
    fi
  else
    bad "Agent configuration unreadable (admin $C, effective $C2)"
  fi
fi

  fi
fi

echo "=== SMOKE-L1-AGENT-MODE PASS=$PASS FAIL=$FAIL SKIP=$SKIP ==="
[ "$FAIL" = 0 ] && exit 0 || exit 1
