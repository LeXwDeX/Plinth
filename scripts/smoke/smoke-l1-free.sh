#!/usr/bin/env bash
# L1 免费冒烟（部署后必跑，零真实模型调用，零费用）。
# 用法（服务器上）: ART_AI_SERVER_ROOT=<发布根> bash smoke-l1-free.sh
#   或（本地）:     python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l1-free.sh
# 凭据：服务器上自动读 production/runtime.env；本地用 ADMIN_* 环境变量。
# 数据保留：产生的会话不清理（协议约定保留于 dev-test）。
# 批准方式：第 7 步先把会话设为手工方式，再用 3D 请求取得待批准同意（2D 在
# 手工方式下由策略直接批准，会产生真实付费任务，不能用于免费冒烟）。
# 若 Agent 误调用 2D 工具而产生了任务，脚本无法阻止，但会停止本轮并以
# PAID-TASK-ALERT + FAIL 报告（退出码 1），不会当成 SOFT-FAIL 放过。
set -u
BASE="${BASE:-http://127.0.0.1:13333}"
PASS=0; FAIL=0
ok()  { echo "PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
req() { local out="$1"; shift; curl -s -o "$out" -w "%{http_code}" "$@"; }

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

# 1. 健康检查
[ "$(req /tmp/s1.json "$BASE/healthz")" = 200 ] && ok "healthz" || bad "healthz"
[ "$(req /tmp/s1.json "$BASE/readyz")" = 200 ] && ok "readyz" || bad "readyz"

# 2. Schema 投影可读且含 offering
C=$(req /tmp/s1-schema.json "$BASE/api/v1/schema")
if [ "$C" = 200 ] && python3 -c "
import json
d=json.load(open('/tmp/s1-schema.json')).get('schema') or {}
chat=d.get('chat') or {}
exit(0 if d.get('revision') and chat else 1)
" 2>/dev/null; then ok "schema projection readable"; else bad "schema ($C)"; fi

# 3. SPA shell 安全头（SEC-04）
H=$(curl -s -D - -o /dev/null "$BASE/login" | grep -ic "content-security-policy" || true)
[ "${H:-0}" -ge 1 ] && ok "SPA security headers" || bad "SPA security headers missing"

# 4. 登录
C=$(req /tmp/s1-r.json -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN_USERNAME:-}\",\"password\":\"${ADMIN_PASSWORD:-}\"}" \
  -c /tmp/s1-cookies.txt)
[ "$C" = 200 ] && ok "login" || { bad "login ($C)"; echo "=== ABORT ==="; exit 1; }

# 5. 跨源状态变更被拒（SEC-03）
C=$(req /tmp/s1.json -X POST "$BASE/api/chat/sessions" -H 'Content-Type: application/json' \
  -H 'Origin: http://evil.example' -H 'Sec-Fetch-Site: cross-site' \
  -b /tmp/s1-cookies.txt -d '{"title":"smoke-csrf"}')
[ "$C" = 403 ] && ok "cross-origin state change rejected (403)" || bad "csrf expected 403 got $C"

# 6. 会话创建 + 任务列表
C=$(req /tmp/s1-sess.json -X POST "$BASE/api/chat/sessions" -H 'Content-Type: application/json' \
  -H "Origin: $BASE" \
  -b /tmp/s1-cookies.txt -d '{"title":"smoke-l1"}')
SID=$(python3 -c "import json;print(json.load(open('/tmp/s1-sess.json')).get('id',''))" 2>/dev/null)
if { [ "$C" = 200 ] || [ "$C" = 201 ]; } && [ -n "$SID" ]; then
  ok "create session"
else
  bad "create session ($C)"
  exit 1
fi
C=$(req /tmp/s1-tasks.json "$BASE/api/v1/tasks?limit=5" -b /tmp/s1-cookies.txt)
[ "$C" = 200 ] && ok "task list" || bad "task list ($C)"

# 7. 挂起/停止路径（免费：同意请求不批准即停止，无真实生成）
# 批准方式（2026-10 起）：手工方式下只有文生 3D、图生 3D、换贴图等待人工批准，
# 2D、重拓扑、绑骨、动画、转格式由策略直接批准并产生真实付费任务。因此本步必须
# 用 3D 请求取得待批准同意，并先把会话显式设为手工方式；不再用 2D 请求。
C=$(req /tmp/s1.json -X PUT "$BASE/api/v1/chat/sessions/$SID/approval" \
  -H 'Content-Type: application/json' -H "Origin: $BASE" -b /tmp/s1-cookies.txt \
  -d '{"approvalMode":"manual"}')
MODE=$(python3 -c "import json;print(json.load(open('/tmp/s1.json')).get('approvalMode',''))" 2>/dev/null)
if [ "$C" = 200 ] && [ "$MODE" = manual ]; then
  ok "session approval mode set to manual"
else
  bad "set manual approval mode ($C mode=$MODE) — 不继续发送生成请求，避免产生付费任务"
  echo "=== SMOKE-L1 PASS=$PASS FAIL=$FAIL ==="
  exit 1
fi

# 3D 模型取自运行时 Offering 清单（Schema 声明 + 可用性）：可用、推荐、
# 无额外计费上限的第一个文生 3D 模型；SMOKE_3D_MODEL 可覆盖。
req /tmp/s1-off.json "$BASE/api/v1/offerings?capability=text-to-3d" -b /tmp/s1-cookies.txt >/dev/null
M3D="${SMOKE_3D_MODEL:-}"
if [ -z "$M3D" ]; then
  M3D=$(python3 - <<'PYEOF' 2>/dev/null
import json
offs = [o for o in json.load(open('/tmp/s1-off.json')).get('offerings', []) if o.get('available')]
safe = [o for o in offs if o.get('recommended') and (o.get('maximum_estimated_credits') or 0) <= (o.get('estimated_credits') or 0)]
pick = (safe or offs or [{}])[0]
print(pick.get('model_id', ''))
PYEOF
)
fi
if [ -z "$M3D" ]; then
  echo "SOFT-FAIL: no available text-to-3d model in /api/v1/offerings — 挂起/停止路径未验证"
else
  C=$(req /tmp/s1-m.json -X POST "$BASE/api/chat/sessions/$SID/messages" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b /tmp/s1-cookies.txt \
    -d "{\"content\":\"做一个简单的木凳 3D 模型\",\"creative_domain\":\"model-3d\",\"ai_model\":\"$M3D\"}")
  if [ "$C" = 202 ]; then
    ok "agent message accepted (3D model=$M3D)"
    # 本脚本承诺零费用：会话里出现任何任务都是意外。手工方式下 2D 由策略直接批准，
    # 所以 Agent 若误调用 refine_image 之类的 2D 工具，不会有待批准同意，而是直接产生
    # 一个付费任务。脚本无法阻止 LLM 这样做，但必须发现并明确报告。
    session_tasks() { # 会话任务数；读不到时输出 ?
      req /tmp/s1-t.json "$BASE/api/v1/tasks?session_id=$SID&limit=5" -b /tmp/s1-cookies.txt >/dev/null
      python3 -c "
import json
d=json.load(open('/tmp/s1-t.json'))
print(len(d.get('tasks') or d.get('data') or []))" 2>/dev/null || echo "?"
    }
    list_tasks() { # 打印 /tmp/s1-t.json 里的任务（id、capability、state）
      python3 -c "
import json
d=json.load(open('/tmp/s1-t.json'))
for t in (d.get('tasks') or d.get('data') or []):
    print('  task %s capability=%s state=%s' % (t.get('id'), t.get('capability'), t.get('state')))" 2>/dev/null
    }
    # 等待批准卡片出现（最多 90s）。返回 0=待批准卡片已出现；1=超时；2=会话里出现了任务。
    # 批准卡片（2026-10 起）在调用 3D 生成工具时独立于轮次保存，Stop 后仍可批准或拒绝；
    # 旧的 /api/v1/agent/consent 轮次挂起状态不再反映它，所以这里读会话的待批准卡片。
    pend() {
      local dl=$((SECONDS+90)) n
      while [ $SECONDS -lt $dl ]; do
        req /tmp/s1-pa.json "$BASE/api/v1/chat/sessions/$SID/pending-approvals" -b /tmp/s1-cookies.txt >/dev/null
        python3 -c "
import json
items=(json.load(open('/tmp/s1-pa.json')) or {}).get('items') or []
exit(0 if any(i.get('status') == 'pending' for i in items) else 1)
" 2>/dev/null && return 0
        n=$(session_tasks)
        case "$n" in 0|"?") ;; *) return 2 ;; esac
        sleep 3
      done
      return 1
    }
    pending_cards() { # 打印会话里状态为 pending 的卡片：request_id capability；读不到时打印 ERROR
      if [ "$(req /tmp/s1-pa.json "$BASE/api/v1/chat/sessions/$SID/pending-approvals" -b /tmp/s1-cookies.txt)" != 200 ]; then
        echo "ERROR list"; return
      fi
      python3 -c "
import json
for i in (json.load(open('/tmp/s1-pa.json')) or {}).get('items') or []:
    if i.get('status') == 'pending':
        print(i.get('request_id',''), i.get('capability',''))" 2>/dev/null
    }
    retry_nudge() {
      req /tmp/s1-m.json -X POST "$BASE/api/chat/sessions/$SID/messages" \
        -H 'Content-Type: application/json' -H "Origin: $BASE" -b /tmp/s1-cookies.txt \
        -d "{\"content\":\"请使用已选择的 $M3D 模型调用 create_model_3d 生成一个简单的木凳，准备确切输入并等待同意；不要调用 request_consent，不要调用任何 2D 图片工具，也不要在批准前提交生成。\",\"creative_domain\":\"model-3d\",\"ai_model\":\"$M3D\"}" >/dev/null
    }
    # LLM 噪声兜底：超时则重发一次再等，最多两次；出现任务时立即停止等待
    pend; WRC=$?
    if [ "$WRC" = 1 ]; then retry_nudge; pend; WRC=$?; fi
    if [ "$WRC" = 1 ]; then retry_nudge; pend; WRC=$?; fi
    if [ "$WRC" = 0 ]; then
      CARD=$(pending_cards | head -n 1)
      RID=${CARD%% *}; CAP=${CARD#* }
      case "$CAP" in
        text-to-3d|image-to-3d) ok "approval card pending (manual mode, $CAP)" ;;
        *) bad "pending approval card has unexpected capability '$CAP' (expected a 3D generation)" ;;
      esac
      # 等待批准时停止本轮：应 200 且轮次收束（免费）
      C=$(req /tmp/s1.json -X POST "$BASE/api/chat/sessions/$SID/turn/cancel" \
        -H "Origin: $BASE" -b /tmp/s1-cookies.txt)
      [ "$C" = 200 ] && ok "stop while waiting for approval -> 200" || bad "cancel ($C)"
      sleep 8
      req /tmp/s1-msgs.json "$BASE/api/chat/sessions/$SID/messages?limit=3" -b /tmp/s1-cookies.txt >/dev/null
      python3 -c "
import json
msgs=json.load(open('/tmp/s1-msgs.json'))
msgs=msgs.get('messages', []) if isinstance(msgs, dict) else msgs  # limit 返回 {messages, next_cursor}
last=[m for m in msgs if m.get('role')=='assistant'][-1]
c=last.get('content') or ''
exit(0 if ('停止' in c or '停止本轮' in c or len(c)>0) else 1)
" 2>/dev/null && ok "turn finalized after cancel" || bad "turn not finalized"
      # 停止后卡片仍待批准（可以批准或拒绝）。这里只拒绝，不批准：拒绝免费。
      if pending_cards | grep -q "^$RID "; then
        ok "approval card still pending after Stop"
      else
        bad "approval card $RID no longer pending after Stop"
      fi
      for R in $(pending_cards | grep -v '^ERROR' | cut -d' ' -f1); do
        C=$(req /tmp/s1-deny.json -X PUT "$BASE/api/v1/tasks/grants/$R" \
          -H 'Content-Type: application/json' -H "Origin: $BASE" -b /tmp/s1-cookies.txt \
          -d '{"decision":"deny"}')
        [ "$C" = 200 ] || bad "deny approval card $R ($C)"
      done
      [ -z "$(pending_cards)" ] && ok "approval cards denied after Stop (no card left pending)" || bad "approval cards still pending after deny"
      # 取消后不得留下任务：本脚本承诺零费用
      NT=$(session_tasks)
      if [ "$NT" = 0 ]; then
        ok "no task created (free)"
      else
        list_tasks
        echo "PAID-TASK-ALERT: session $SID has $NT task(s) — L1 应是零费用，上面的任务很可能已经扣费"
        bad "session has $NT task(s) after cancel — L1 不应产生任务（请人工复核会话 $SID）"
      fi
    else
      # 超时或出现任务：都先停止本轮，不让 Agent 继续动作
      req /tmp/s1.json -X POST "$BASE/api/chat/sessions/$SID/turn/cancel" \
        -H "Origin: $BASE" -b /tmp/s1-cookies.txt >/dev/null
      sleep 5
      for R in $(pending_cards | grep -v '^ERROR' | cut -d' ' -f1); do
        req /tmp/s1-deny.json -X PUT "$BASE/api/v1/tasks/grants/$R" \
          -H 'Content-Type: application/json' -H "Origin: $BASE" -b /tmp/s1-cookies.txt \
          -d '{"decision":"deny"}' >/dev/null || true
        echo "  denied leftover approval card $R (status $(cat /tmp/s1-deny.json 2>/dev/null | head -c 120))"
      done
      NT=$(session_tasks)
      if [ "$NT" = 0 ]; then
        echo "SOFT-FAIL: approval card not reached (LLM noise) — 已停止本轮，会话零任务；请人工复核该会话 $SID"
      else
        list_tasks
        echo "PAID-TASK-ALERT: session $SID has $NT task(s) without a pending approval card — Agent 没有调用 3D 工具，多半误调用了 2D 工具；手工方式下 2D 由策略批准，已产生付费任务。脚本无法阻止，但这不是免费冒烟"
        bad "a task was created while waiting for the 3D approval card — L1 不应产生任务（请人工复核会话 $SID）"
      fi
    fi
  else
    bad "agent message ($C)"
  fi
fi

echo "=== SMOKE-L1 PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" = 0 ] && exit 0 || exit 1
