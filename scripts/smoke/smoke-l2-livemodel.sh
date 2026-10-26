#!/usr/bin/env bash
# L2 真实模型链路探针（含小额付费生成；发布后按需跑，非每次必跑）。
#
# 批准方式（会话级，2026-10 起）：手工方式下只有文生 3D、图生 3D、换贴图等待人工
# 批准；2D、重拓扑、绑骨、动画、转格式由策略直接批准（grant.decided_by 为
# "policy:manual"）。本脚本按这条规则分两部分，每个会话开头都显式设为手工方式：
#
#   第一部分  2D 手工方式 = 策略批准（一张小额文生图，与改造前费用相同）
#             不出现待批准同意；任务 SUCCEEDED；批准来源为 policy；显示积分；
#             有 DB 读权限时核对 grant.decided_by = policy:manual。
#   第二部分  3D 手工方式 = 人工批准（AGT-12）
#             A、B 各自挂起在待批准同意，C 不冻结；拒绝 B 并断言零任务。
#             A 默认停止本轮（零费用、零任务）；设 L2_APPROVE_3D=1 才批准 A，
#             产生一次真实 3D 生成（约 25 积分）并断言 SUCCEEDED。
#
# LLM 噪声兜底：同意/任务未出现时重发消息最多两次再等。
# 用法（服务器上）: ART_AI_SERVER_ROOT=<发布根> bash smoke-l2-livemodel.sh
#                   L2_APPROVE_3D=1 bash smoke-l2-livemodel.sh   # 含付费 3D 生成
# 可选环境变量: BASE、IMAGE_MODEL（默认 gpt-image-2.5-sunburst）、
#   SMOKE_3D_MODEL（默认取 /api/v1/offerings 中第一个可用推荐的文生 3D 模型）、
#   ART_AI_DB（SQLite 路径，用于核对 decided_by；服务器上缺省为 $ART_AI_SERVER_ROOT/production/data/art-ai.db）。
# 凭据：服务器上读 production/runtime.env；本地用 ADMIN_* 环境变量。数据保留不清理。
set -u
BASE="${BASE:-http://127.0.0.1:13333}"
IMAGE_MODEL="${IMAGE_MODEL:-gpt-image-2.5-sunburst}"
ART_AI_DB="${ART_AI_DB:-${ART_AI_SERVER_ROOT:+$ART_AI_SERVER_ROOT/production/data/art-ai.db}}"
PASS=0; FAIL=0
ok()  { echo "PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
req() { local out="$1"; shift; curl -s -o "$out" -w "%{http_code}" "$@"; }

# 服务器上运行时以 ART_AI_SERVER_ROOT=<该环境发布根> 读取本栈 runtime.env（仓库不写死路径）。
if [ -n "${ART_AI_SERVER_ROOT:-}" ] && [ -f "$ART_AI_SERVER_ROOT/production/runtime.env" ] && [ -z "${ADMIN_USERNAME:-}" ]; then
  set -a; . "$ART_AI_SERVER_ROOT/production/runtime.env"; set +a
fi
C=$(req /tmp/s2.json -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN_USERNAME:-}\",\"password\":\"${ADMIN_PASSWORD:-}\"}" -c /tmp/s2-cookies.txt)
[ "$C" = 200 ] && ok "login" || { bad "login ($C)"; exit 1; }
J=/tmp/s2-cookies.txt

mk() { # 创建会话，失败重试一次
  local sid=""
  for attempt in 1 2; do
    local status
    status=$(req /tmp/s2-s.json -X POST "$BASE/api/chat/sessions" -H 'Content-Type: application/json' -H "Origin: $BASE" \
      -b "$J" -d "{\"title\":\"$1-$attempt\"}")
    case "$status" in 2[0-9][0-9]) ;; *) continue ;; esac
    sid=$(python3 -c "import json;print(json.load(open('/tmp/s2-s.json')).get('id',''))" 2>/dev/null)
    [ -n "$sid" ] && break
    sleep 2
  done
  echo "$sid"
}
set_mode() { # $1=session $2=auto|manual；返回 0 仅当服务端回读与请求一致
  local status mode
  status=$(req /tmp/s2-mode.json -X PUT "$BASE/api/v1/chat/sessions/$1/approval" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" -d "{\"approvalMode\":\"$2\"}")
  mode=$(python3 -c "import json;print(json.load(open('/tmp/s2-mode.json')).get('approvalMode',''))" 2>/dev/null)
  [ "$status" = 200 ] && [ "$mode" = "$2" ]
}
send() { # $1=session $2=content $3=domain(image|model-3d) $4=model
  local status
  status=$(req /tmp/s2-m.json -X POST "$BASE/api/chat/sessions/$1/messages" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" \
    -d "{\"content\":\"$2\",\"creative_domain\":\"$3\",\"ai_model\":\"$4\"}")
  case "$status" in 2[0-9][0-9]) ;; *) return 1 ;; esac
  python3 -c "import json;print(json.load(open('/tmp/s2-m.json')).get('messageId',''))" 2>/dev/null
}
# 批准卡片（2026-10 起）独立于轮次保存，Stop 后仍可决定；旧的 /api/v1/agent/consent
# 轮次挂起状态不再反映它。这里读会话的待批准卡片，决定走任务授权接口。
pending_list() { # $1=session → /tmp/s2-cs.json；读取失败返回非零
  [ "$(req /tmp/s2-cs.json "$BASE/api/v1/chat/sessions/$1/pending-approvals" -b "$J")" = 200 ]
}
consent_state() { # $1=session → pending|none|error
  pending_list "$1" || { echo error; return; }
  python3 -c "
import json
items=(json.load(open('/tmp/s2-cs.json')) or {}).get('items') or []
print('pending' if any(i.get('status') == 'pending' for i in items) else 'none')
" 2>/dev/null || echo error
}
pending_cards() { # $1=session → 每行 "request_id capability"（服务端按最新在前）
  pending_list "$1" || return 0
  python3 -c "
import json
for i in (json.load(open('/tmp/s2-cs.json')) or {}).get('items') or []:
    if i.get('status') == 'pending':
        print(i.get('request_id',''), i.get('capability',''))" 2>/dev/null
}
approve_consent() { # session request_id true|false：只决定这一张卡片，先确认它仍待批准
  local scope="$1" request_id="$2" approved="$3" decision=deny cap
  [ -n "$request_id" ] || return 1
  cap=$(pending_cards "$scope" | awk -v id="$request_id" '$1 == id { print $2 }')
  [ -n "$cap" ] || return 1
  if [ "$approved" = true ]; then
    # 只批准文生 3D / 图生 3D；别的能力一律不批准。
    case "$cap" in text-to-3d|image-to-3d) decision=approve ;; *) return 1 ;; esac
  fi
  local status
  status=$(req /tmp/s2-decision.json -X PUT "$BASE/api/v1/tasks/grants/$request_id" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" \
    -d "{\"decision\":\"$decision\"}")
  case "$status" in 2[0-9][0-9]) return 0 ;; *) return 1 ;; esac
}
deny_all() { # $1=session：拒绝会话里所有待批准卡片（免费）；全部拒绝成功返回 0
  local rc=0 r
  for r in $(pending_cards "$1" | cut -d' ' -f1); do approve_consent "$1" "$r" false || rc=1; done
  return $rc
}

task_state() { # $1=session → 最新任务状态（无任务为 none）
  req /tmp/s2-t.json "$BASE/api/v1/tasks?session_id=$1&limit=5" -b "$J" >/dev/null
  python3 -c "
import json
d=json.load(open('/tmp/s2-t.json'))
ts=d.get('tasks') or d.get('data') or []
print(ts[0].get('state','none') if ts else 'none')" 2>/dev/null
}
task_count() { # $1=session
  req /tmp/s2-t.json "$BASE/api/v1/tasks?session_id=$1&limit=5" -b "$J" >/dev/null
  python3 -c "
import json
d=json.load(open('/tmp/s2-t.json'))
print(len(d.get('tasks') or d.get('data') or []))" 2>/dev/null
}

# 3D 模型取自运行时 Offering 清单（Schema 声明 + 可用性）：可用、推荐、无额外
# 计费上限的第一个文生 3D 模型；SMOKE_3D_MODEL 可覆盖。
M3D="${SMOKE_3D_MODEL:-}"
if [ -z "$M3D" ]; then
  req /tmp/s2-off.json "$BASE/api/v1/offerings?capability=text-to-3d" -b "$J" >/dev/null
  M3D=$(python3 - <<'PYEOF' 2>/dev/null
import json
offs = [o for o in json.load(open('/tmp/s2-off.json')).get('offerings', []) if o.get('available')]
safe = [o for o in offs if o.get('recommended') and (o.get('maximum_estimated_credits') or 0) <= (o.get('estimated_credits') or 0)]
print(((safe or offs or [{}])[0]).get('model_id', ''))
PYEOF
)
fi

# ---------------------------------------------------------------------------
# 第一部分：2D 在手工方式下由策略批准（一张小额文生图）
# ---------------------------------------------------------------------------
echo "--- part 1: 2D in manual mode is approved by policy"
S2D=$(mk "smoke2-2D")
[ -n "$S2D" ] || { bad "2D session"; exit 1; }
set_mode "$S2D" manual && ok "2D session set to manual ($S2D)" || { bad "set manual mode (2D)"; exit 1; }
send "$S2D" "请使用已选择的 $IMAGE_MODEL 模型调用 refine_image，生成一只橘猫简笔画；直接调用生成工具，不要调用 request_consent。" image "$IMAGE_MODEL" >/dev/null \
  || { bad "2D message request failed"; exit 1; }

SAW_PENDING=0; P1=timeout; nudges=0; dl=$((SECONDS+300)); t0=$SECONDS
while [ $SECONDS -lt $dl ]; do
  [ "$(consent_state "$S2D")" = "pending" ] && { SAW_PENDING=1; P1=pending; break; }
  ST=$(task_state "$S2D")
  case "$ST" in
    SUCCEEDED) P1=ok; break ;;
    FAILED|CANCELED) P1="task-$ST"; break ;;
  esac
  # LLM 噪声：长时间既无任务也无同意 → 重发一次（最多两次）
  if [ "$ST" = none ] && [ $nudges -lt 2 ] && [ $((SECONDS - t0)) -gt $(((nudges+1)*70)) ]; then
    nudges=$((nudges+1))
    send "$S2D" "请立即使用已选择的 $IMAGE_MODEL 模型调用 refine_image，生成一只橘猫简笔画；直接调用生成工具。" image "$IMAGE_MODEL" >/dev/null
  fi
  sleep 4
done
[ "$SAW_PENDING" = 0 ] && ok "2D: no pending consent in manual mode" \
  || bad "2D: a consent became pending in manual mode (2D must be approved by policy)"
case "$P1" in
  ok) ok "2D: task SUCCEEDED without a person" ;;
  pending) bad "2D: waiting on a human instead of running — 策略未批准 2D" ;;
  *) bad "2D: task not SUCCEEDED ($P1)" ;;
esac

# 批准来源与积分：消息工具调用的 approvalSource / estimatedCredits，会话累计积分
if [ "$P1" = ok ]; then
  req /tmp/s2-msgs.json "$BASE/api/chat/sessions/$S2D/messages?limit=20" -b "$J" >/dev/null
  SRC=$(python3 - <<'PYEOF' 2>/dev/null
import json
msgs = json.load(open('/tmp/s2-msgs.json'))
msgs = msgs.get('messages', []) if isinstance(msgs, dict) else msgs  # limit 返回 {messages, next_cursor}
src, credits = '', 0
for m in msgs if isinstance(msgs, list) else []:
    try:
        calls = json.loads(m.get('toolCalls') or '[]')
    except ValueError:
        continue
    for c in calls:
        if c.get('approvalSource'):
            src = c['approvalSource']
            credits = c.get('estimatedCredits') or credits
print(f"{src}|{credits}")
PYEOF
)
  [ "${SRC%%|*}" = policy ] && ok "2D: approval source = policy" || bad "2D: approval source '${SRC%%|*}' (expected policy)"
  [ "${SRC##*|}" -gt 0 ] 2>/dev/null && ok "2D: step credits shown (${SRC##*|})" || bad "2D: step credits missing"
  req /tmp/s2-ap.json "$BASE/api/v1/chat/sessions/$S2D/approval" -b "$J" >/dev/null
  CRED=$(python3 -c "import json;d=json.load(open('/tmp/s2-ap.json'));print(d.get('approvalMode',''),d.get('estimatedCredits',0))" 2>/dev/null)
  case "$CRED" in
    "manual "[1-9]*) ok "2D: session still manual, accumulated credits=${CRED#manual }" ;;
    *) bad "2D: session approval state '$CRED' (expected manual with credits > 0)" ;;
  esac
  # grant.decided_by 只在库里；有读权限就核对，没有就明说跳过
  DECIDED=$(python3 - "$ART_AI_DB" "$S2D" <<'PYEOF' 2>/dev/null
import sqlite3, sys
try:
    con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True, timeout=3)
    rows = con.execute("SELECT decided_by FROM execution_grants WHERE session_id=?", (sys.argv[2],)).fetchall()
    print(",".join(sorted({r[0] or '' for r in rows})) if rows else "NOROWS")
except Exception:
    print("UNREADABLE")
PYEOF
)
  case "$DECIDED" in
    policy:manual) ok "2D: grant decided_by = policy:manual" ;;
    UNREADABLE|"") echo "SKIP: grant decided_by not verified (database not readable at $ART_AI_DB)" ;;
    NOROWS) bad "2D: no execution grant row for the session" ;;
    *) bad "2D: grant decided_by '$DECIDED' (expected policy:manual)" ;;
  esac
fi

# ---------------------------------------------------------------------------
# 第二部分：3D 在手工方式下等待人工批准（AGT-12）
# ---------------------------------------------------------------------------
echo "--- part 2: 3D in manual mode waits for a human"
if [ -z "$M3D" ]; then
  bad "no available text-to-3d model in /api/v1/offerings — 3D 部分无法执行"
  echo "=== SMOKE-L2 PASS=$PASS FAIL=$FAIL ==="
  exit 1
fi
echo "3D model: $M3D"
SA=$(mk "smoke2-A"); SB=$(mk "smoke2-B"); SC=$(mk "smoke2-C")
[ -n "$SA" ] && [ -n "$SB" ] && [ -n "$SC" ] && ok "3 sessions (A=$SA B=$SB C=$SC)" || { bad "sessions"; exit 1; }
for s in "$SA" "$SB"; do
  set_mode "$s" manual || { bad "set manual mode ($s)"; exit 1; }
done
ok "A/B set to manual"

# Trigger the generation tool itself so the pending consent is bound to its exact prepared input.
wait_consent() { # $1=session $2=nudge content
  local dl=$((SECONDS+180)); local nudges=0
  while [ $SECONDS -lt $dl ]; do
    [ "$(consent_state "$1")" = "pending" ] && return 0
    sleep 3
    if [ $nudges -lt 2 ] && [ $((SECONDS - dl + 180)) -gt $((nudges*60+45)) ]; then
      nudges=$((nudges+1))
      send "$1" "$2" model-3d "$M3D" >/dev/null
    fi
  done
  [ "$(consent_state "$1")" = "pending" ]
}
consent_card() { # $1=session → 最新一张待批准卡片的 "request_id capability"
  pending_cards "$1" | head -n 1
}
NUDGE_A="请立即使用已选择的 $M3D 模型调用 create_model_3d，生成一个简单的木凳；直接调用生成工具以准备本次确切输入；不要调用 request_consent，也不要在未获批准前提交生成。"
NUDGE_B="请立即使用已选择的 $M3D 模型调用 create_model_3d，生成一个简单的花瓶；直接调用生成工具以准备本次确切输入；不要调用 request_consent，也不要在未获批准前提交生成。"

# 顺序化挂起（降低并发限流噪声）：A 挂起后再发 B，双挂起就绪后发 C
send "$SA" "$NUDGE_A" model-3d "$M3D" >/dev/null || { bad "A message request failed"; exit 1; }
A_CAP=""; A_REQ=""
if wait_consent "$SA" "$NUDGE_A"; then
  read -r A_REQ A_CAP <<< "$(consent_card "$SA")"
  case "$A_CAP" in
    text-to-3d|image-to-3d) ok "A suspended on a 3D consent" ;;
    *) bad "A pending consent is not a 3D generation ($A_CAP)" ;;
  esac
else
  bad "A consent pending"
fi
send "$SB" "$NUDGE_B" model-3d "$M3D" >/dev/null || { bad "B message request failed"; exit 1; }
if wait_consent "$SB" "$NUDGE_B"; then
  read -r B_REQ B_CAP <<< "$(consent_card "$SB")"
  case "$B_CAP" in
    text-to-3d|image-to-3d) ok "B suspended on a 3D consent" ;;
    *) bad "B pending consent is not a 3D generation ($B_CAP)" ;;
  esac
else
  bad "B consent pending"
fi
A_SUSPENDED=$( [ "$(consent_state "$SA")" = "pending" ] && echo 1 || echo 0 )

# 核心：双挂起下 C 立即响应
send "$SC" "1加1等于几？直接回答数字" image "$IMAGE_MODEL" >/dev/null
COK=0; dl=$((SECONDS+120))
while [ $SECONDS -lt $dl ]; do
  req /tmp/s2-msgs.json "$BASE/api/chat/sessions/$SC/messages?limit=3" -b "$J" >/dev/null
  python3 -c "
import json
msgs=json.load(open('/tmp/s2-msgs.json'))
msgs=msgs.get('messages', []) if isinstance(msgs, dict) else msgs  # limit 返回 {messages, next_cursor}
last=[m for m in msgs if m.get('role')=='assistant'][-1] if msgs else {}
exit(0 if (last.get('content') or '').strip() else 1)
" 2>/dev/null && { COK=1; break; }
  sleep 3
done
[ "$COK" = 1 ] && ok "C responded while A/B suspended" || bad "C frozen (A_suspended=$A_SUSPENDED)"

# A：L2_APPROVE_3D=1 才批准（一次真实 3D 生成）；否则停止本轮，零任务零费用。
# 只批准确认过是 text-to-3d / image-to-3d 的待批准同意；别的能力一律不批准。
if [ "$(consent_state "$SA")" = "pending" ]; then
  if [ "${L2_APPROVE_3D:-}" = 1 ] && { [ "$A_CAP" = text-to-3d ] || [ "$A_CAP" = image-to-3d ]; }; then
    approve_consent "$SA" "$A_REQ" true || { bad "A approval request failed"; exit 1; }
    deny_all "$SA"
    dl=$((SECONDS+1200)); A_OK=0
    while [ $SECONDS -lt $dl ]; do
      ST=$(task_state "$SA")
      [ "$ST" = SUCCEEDED ] && { A_OK=1; break; }
      case "$ST" in FAILED|CANCELED) break ;; esac
      sleep 8
    done
    [ "$A_OK" = 1 ] && ok "A 3D generation SUCCEEDED (L2_APPROVE_3D=1)" || bad "A 3D generation not SUCCEEDED (state=$ST)"
  else
    C=$(req /tmp/s2.json -X POST "$BASE/api/chat/sessions/$SA/turn/cancel" -H "Origin: $BASE" -b "$J")
    if [ "${L2_APPROVE_3D:-}" = 1 ]; then
      [ "$C" = 200 ] && ok "A canceled: pending consent is '$A_CAP', not a 3D generation, so it was not approved" || bad "A cancel ($C)"
    else
      [ "$C" = 200 ] && ok "A canceled instead of approved (set L2_APPROVE_3D=1 to run the paid 3D generation)" || bad "A cancel ($C)"
    fi
    deny_all "$SA"
    sleep 5
    NA=$(task_count "$SA")
    [ "$NA" = 0 ] && ok "A produced no task after cancel and deny" || bad "A produced $NA task(s) after cancel"
  fi
else
  bad "A not pending at decision time — skip A decision"
  # 没有待批准同意却有任务：Agent 多半误调用了 2D 工具（手工方式下由策略批准，已扣费）
  NA=$(task_count "$SA")
  [ "$NA" = 0 ] && ok "A produced no task" || bad "A produced $NA task(s) without a pending consent — 多半误调用了 2D 工具，已产生付费任务"
fi

# B 拒绝 → 零任务（仅当 B 挂起；否则 zero-task 断言自然成立但标记 skip）
if [ "$(consent_state "$SB")" = "pending" ]; then
  deny_all "$SB" || { bad "B denial request failed"; exit 1; }
else
  echo "SKIP: B was not suspended; deny skipped"
fi
sleep 20
NT=$(task_count "$SB")
[ "$NT" = "0" ] && ok "B produced no task after deny" || bad "B produced $NT task(s)"

echo "=== SMOKE-L2 PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" = 0 ] && exit 0 || exit 1
