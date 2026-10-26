#!/usr/bin/env bash
# L3 批量回归：每模型 5 张真实生成，零人工（脚本代批同意，产品零改动）。
# 协议（dev_test.md §11.3 / 用户 2026-09-28 确认）：
#   - 每模型每轮 IMAGES_PER_MODEL 张（默认 5，协议上限 5），满一轮即止，不循环。
#   - 断言：终态 SUCCEEDED；required 资产 READY；资产 URL 200；media_type 与
#     字节魔数一致；sha256 与服务端声明一致（字节级一致）；跨用户 403/404。
#   - 批准方式：每个会话先显式设为手工方式。2D 在手工方式下由策略直接批准，不再出现待批准同意，
#     脚本因此不等待同意：若同意恰好待批准，仍由脚本 GET pending-approvals 取得 request_id 后产品外 PUT /api/v1/tasks/grants/{id} 批准
#     （产品内严禁自动批准）；否则直接等待任务。
# 用法（服务器上）: ART_AI_SERVER_ROOT=<发布根> MODELS=gpt-image-2.5-sunburst bash smoke-l3-batch.sh
#   MODELS 缺省 = schema 全部可用 text-to-image 模型（逗号分隔可过滤）。
# 凭据：读 production/runtime.env。数据保留不清理。
set -u
BASE="${BASE:-http://127.0.0.1:13333}"
PASS=0; FAIL=0
ok()  { echo "PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
req() { local out="$1"; shift; curl -s -o "$out" -w "%{http_code}" "$@"; }
TAG="${ROUND_TAG:-manual}"
IMAGES="${IMAGES_PER_MODEL:-5}"
[ "$IMAGES" -gt 5 ] 2>/dev/null && IMAGES=5

# 服务器上运行时以 ART_AI_SERVER_ROOT=<该环境发布根> 读取本栈 runtime.env（仓库不写死路径）。
if [ -n "${ART_AI_SERVER_ROOT:-}" ] && [ -f "$ART_AI_SERVER_ROOT/production/runtime.env" ] && [ -z "${ADMIN_USERNAME:-}" ]; then
  set -a; . "$ART_AI_SERVER_ROOT/production/runtime.env"; set +a
fi
C=$(req /tmp/s3.json -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ADMIN_USERNAME\",\"password\":\"$ADMIN_PASSWORD\"}" -c /tmp/s3-cookies.txt)
[ "$C" = 200 ] && ok "login" || { bad "login"; exit 1; }
J=/tmp/s3-cookies.txt

# 模型清单：schema 可用 text-to-image offering 的 model_id（去重）
req /tmp/s3-off.json "$BASE/api/v1/offerings?capability=text-to-image" -b "$J" >/dev/null
ALL_MODELS=$(python3 -c "
import json
d=json.load(open('/tmp/s3-off.json'))
seen=[]
for o in d.get('offerings', []):
    if not o.get('available', True): continue
    m=o.get('model_id','')
    if m and m not in seen: seen.append(m)
print(','.join(seen))" 2>/dev/null)
MODELS="${MODELS:-$ALL_MODELS}"
[ -n "$MODELS" ] && ok "models: $MODELS" || { bad "no models discovered"; exit 1; }

mk() {
  local status
  status=$(req /tmp/s3-s.json -X POST "$BASE/api/chat/sessions" -H 'Content-Type: application/json' -H "Origin: $BASE" \
    -b "$J" -d "{\"title\":\"$1\"}")
  case "$status" in 2[0-9][0-9]) ;; *) return 1 ;; esac
  python3 -c "import json;print(json.load(open('/tmp/s3-s.json')).get('id',''))" 2>/dev/null
}
set_manual() { # $1=session；服务端回读 approvalMode=manual 才算成功
  local status mode
  status=$(req /tmp/s3-mode.json -X PUT "$BASE/api/v1/chat/sessions/$1/approval" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" -d '{"approvalMode":"manual"}')
  mode=$(python3 -c "import json;print(json.load(open('/tmp/s3-mode.json')).get('approvalMode',''))" 2>/dev/null)
  [ "$status" = 200 ] && [ "$mode" = manual ]
}
send() { # $1=session $2=model $3=content
  local status
  status=$(req /tmp/s3-m.json -X POST "$BASE/api/chat/sessions/$1/messages" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" \
    -d "{\"content\":\"$3\",\"creative_domain\":\"image\",\"ai_model\":\"$2\"}")
  case "$status" in 2[0-9][0-9]) return 0 ;; *) return 1 ;; esac
}
# 批准卡片（2026-10 起）独立于轮次保存；旧的 /api/v1/agent/consent 不再反映它。
consent_state() { # $1=session → pending|none|error
  [ "$(req /tmp/s3-cs.json "$BASE/api/v1/chat/sessions/$1/pending-approvals" -b "$J")" = 200 ] || { echo error; return; }
  python3 -c "
import json
items=(json.load(open('/tmp/s3-cs.json')) or {}).get('items') or []
print('pending' if any(i.get('status') == 'pending' for i in items) else 'none')" 2>/dev/null || echo error
}
approve() { # $1=session；先从服务端待批准卡片读取确切 request_id 再决定
  local request_id capability
  [ "$(consent_state "$1")" = pending ] || return 1
  read -r request_id capability <<EOF2
$(python3 -c "
import json
items=[i for i in (json.load(open('/tmp/s3-cs.json')) or {}).get('items') or [] if i.get('status') == 'pending']
print(items[0].get('request_id',''), items[0].get('capability','')) if items else print('')" 2>/dev/null)
EOF2
  [ -n "$request_id" ] || return 1
  # 本脚本只为 2D 图片步骤代批。其他能力（3D、换贴图、处理、转格式、去背景或未知）
  # 绝不代批，而是拒绝：那些费用不在 L3 协议内。
  case "$capability" in
    text-to-image|image-to-image) ;;
    *)
      echo "REFUSED: pending approval is '$capability', not the 2D image step — denied, not approved"
      req /tmp/s3.json -X PUT "$BASE/api/v1/tasks/grants/$request_id" \
        -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" -d '{"decision":"deny"}' >/dev/null
      return 1 ;;
  esac
  local status
  status=$(req /tmp/s3.json -X PUT "$BASE/api/v1/tasks/grants/$request_id" \
    -H 'Content-Type: application/json' -H "Origin: $BASE" -b "$J" \
    -d '{"decision":"approve"}')
  case "$status" in 2[0-9][0-9]) return 0 ;; *) return 1 ;; esac
}
newest_task_id() { # $1=session → 最新任务 ID（无任务为空）
  req /tmp/s3-t.json "$BASE/api/v1/tasks?session_id=$1&limit=1" -b "$J" >/dev/null 2>&1
  python3 -c "
import json
ts=json.load(open('/tmp/s3-t.json')).get('tasks') or []
print(ts[0].get('id','') if ts else '')" 2>/dev/null
}
# 推进一步到“已有新任务”：同意待批准则批准（脚本代批，仅测试操作授权）；策略已批准则直接看到新任务。
# LLM 噪声：未出现新任务也无待批准同意时，最多重发两次提示再等。$1=session $2=model $3=上一个任务 ID
drive_step() {
  local dl=$((SECONDS+180)); local nudges=0 tid
  while [ $SECONDS -lt $dl ]; do
    if [ "$(consent_state "$1")" = "pending" ]; then
      if approve "$1"; then echo "consent was pending -> approved by smoke operator"; return 0; fi
      return 1
    fi
    tid=$(newest_task_id "$1")
    if [ -n "$tid" ] && [ "$tid" != "$3" ]; then echo "no consent needed (policy-approved) -> task $tid"; return 0; fi
    sleep 3
    if [ $nudges -lt 2 ] && [ $((SECONDS - dl + 180)) -gt $((nudges*60+45)) ]; then
      nudges=$((nudges+1))
      send "$1" "$2" "请立即使用已选择的 $2 模型调用 refine_image，生成指定的测试图。直接调用生成工具；不要调用 request_consent。"
    fi
  done
  return 1
}
newest_task() { # $1=session → newest task JSON 到 /tmp/s3-t.json
  req /tmp/s3-t.json "$BASE/api/v1/tasks?session_id=$1&limit=1" -b "$J" >/dev/null 2>&1
}
wait_succeeded() { # $1=session $2=prev_task_id 600s → NEW_TASK_ID
  local dl=$((SECONDS+600))
  while [ $SECONDS -lt $dl ]; do
    newest_task "$1"
    python3 - "$2" <<'PYEOF'
import json, sys
prev = sys.argv[1]
ts = json.load(open('/tmp/s3-t.json')).get('tasks') or []
tid = ts[0].get('id', 'none') if ts else 'none'
st = ts[0].get('state', 'none') if ts else 'none'
if tid != prev and st == 'SUCCEEDED':
    open('/tmp/s3-newtask.txt', 'w').write(tid)
    raise SystemExit(0)
if tid != prev and st in ('FAILED', 'CANCELED'):
    raise SystemExit(2)
raise SystemExit(1)
PYEOF
    case $? in
      0) NEW_TASK_ID=$(cat /tmp/s3-newtask.txt); return 0 ;;
      2) return 1 ;;
    esac
    sleep 6
  done
  return 1
}
# 断言单张：终态/资产/字节一致。$1=session $2=标签
assert_image() {
  newest_task "$1"
  python3 - "$2" <<'PYEOF'
import json, sys
label = sys.argv[1]
d = json.load(open('/tmp/s3-t.json'))
ts = d.get('tasks') or []
if not ts:
    print(f"FAIL: {label} no task"); raise SystemExit(1)
t = ts[0]
if t.get('state') != 'SUCCEEDED':
    print(f"FAIL: {label} state={t.get('state')}"); raise SystemExit(1)
assets = [a for a in (t.get('assets') or []) if a.get('required') and a.get('role')=='image']
if not assets or not assets[0].get('url'):
    print(f"FAIL: {label} no required image asset url"); raise SystemExit(1)
a = assets[0]
meta = '|'.join([t.get('id',''), a['url'], a.get('media_type',''),
                 str(a.get('size') or 0), (a.get('sha256') or '')])
open('/tmp/s3-meta.txt', 'w').write(meta)
PYEOF
  [ $? -eq 0 ] || return 1
  local META TID AURL
  META=$(cat /tmp/s3-meta.txt)
  TID=$(echo "$META" | cut -d'|' -f1)
  AURL=$(echo "$META" | cut -d'|' -f2)
  req /tmp/s3-asset.bin -b "$J" "$BASE$AURL" >/dev/null 2>&1
  python3 - "$2" /tmp/s3-meta.txt <<'PYEOF'
import hashlib, sys
meta = (open(sys.argv[2]).read().strip().split('|') + [''] * 5)[:5]
tid, url, mt, size, declared = meta
label = sys.argv[1]
body = open('/tmp/s3-asset.bin', 'rb').read()
if not body:
    print(f"FAIL: {label} asset body empty"); raise SystemExit(1)
magic = {'image/png': b'\x89PNG', 'image/jpeg': b'\xff\xd8\xff', 'image/gif': b'GIF8'}
if mt in magic:
    if not body.startswith(magic[mt]):
        print(f"FAIL: {label} magic mismatch media_type={mt} head={body[:8].hex()}"); raise SystemExit(1)
elif mt == 'image/webp':
    if body[:4] != b'RIFF' or body[8:12] != b'WEBP':
        print(f"FAIL: {label} webp magic mismatch"); raise SystemExit(1)
else:
    if not any(body.startswith(m) for m in magic.values()):
        print(f"FAIL: {label} unknown media_type={mt} no known magic"); raise SystemExit(1)
sha = hashlib.sha256(body).hexdigest()
if declared and sha != declared.lower():
    print(f"FAIL: {label} sha mismatch declared={declared[:12]} actual={sha[:12]}"); raise SystemExit(1)
if size and int(size) != len(body):
    print(f"FAIL: {label} size mismatch declared={size} actual={len(body)}"); raise SystemExit(1)
print(f"VERIFIED: {label} task={tid} bytes={len(body)} sha={sha[:12]}")
PYEOF
}

REPORT="${L3_REPORT_DIR:-${ART_AI_SERVER_ROOT:+$ART_AI_SERVER_ROOT/production/reports/smoke-l3}}"
REPORT="${REPORT:-./reports/smoke-l3}"
mkdir -p "$REPORT"
REPORT_FILE="$REPORT/l3-$TAG-$(date +%Y%m%dT%H%M%SZ).txt"
exec > >(tee -a "$REPORT_FILE") 2>&1
echo "=== SMOKE-L3 round start tag=$TAG images_per_model=$IMAGES ==="

LAST_TASK_SESSION=""
for MODEL_ID in $(echo "$MODELS" | tr ',' ' '); do
  SID=$(mk "smoke-l3-$MODEL_ID-$TAG")
  [ -n "$SID" ] || { bad "$MODEL_ID session"; continue; }
  set_manual "$SID" || { bad "$MODEL_ID set manual approval mode"; continue; }
  ok "$MODEL_ID session=$SID (manual)"
  LAST_TASK_ID=""
  for i in $(seq 1 "$IMAGES"); do
    LABEL="$MODEL_ID#$i"
    if ! send "$SID" "$MODEL_ID" "请立即使用已选择的 $MODEL_ID 模型调用 refine_image，生成第 $i 张测试图：极简风格的小木船漂浮在平静湖面，晨雾。直接调用生成工具准备本次确切输入；不要调用 request_consent。"; then
      bad "$LABEL message request failed"; continue
    fi
    if ! drive_step "$SID" "$MODEL_ID" "$LAST_TASK_ID"; then
      if ! send "$SID" "$MODEL_ID" "继续第 $i 张：请调用 refine_image 准备并生成指定图片，不要调用 request_consent。"; then
        bad "$LABEL nudge request failed"; continue
      fi
      if ! drive_step "$SID" "$MODEL_ID" "$LAST_TASK_ID"; then bad "$LABEL no consent and no new task"; continue; fi
    fi
    if wait_succeeded "$SID" "$LAST_TASK_ID"; then
      LAST_TASK_ID="$NEW_TASK_ID"
      assert_image "$SID" "$LABEL" && ok "$LABEL generated+verified" || bad "$LABEL verify"
    else
      newest_task "$SID"
      ST=$(python3 -c "
import json
d=json.load(open('/tmp/s3-t.json'))
ts=d.get('tasks') or []
print(ts[0].get('state','none') if ts else 'none')" 2>/dev/null)
      bad "$LABEL not SUCCEEDED (state=$ST)"
    fi
    LAST_TASK_SESSION="$SID"
  done
done

# 跨用户：管理员建一次性账号 → 以其身份读最新任务与资产 → 必须 403/404
if [ -n "$LAST_TASK_SESSION" ]; then
  RU="smoke-l3-cross-$(date +%s)"
  RP="Cross$(date +%s)!"
  RC=$(req /tmp/s3.json -X POST "$BASE/api/admin/users" -H 'Content-Type: application/json' -H "Origin: $BASE" \
    -b "$J" -d "{\"username\":\"$RU\",\"password\":\"$RP\",\"email\":\"$RU@example.com\"}" -o /tmp/s3-u.json)
  if [ "$RC" = "200" ] || [ "$RC" = "201" ]; then
    ok "cross-user created ($RU)"
    RC2=$(req /tmp/s3.json -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$RU\",\"password\":\"$RP\"}" -c /tmp/s3-cross.txt)
    if [ "$RC2" = "200" ]; then
      newest_task "$LAST_TASK_SESSION"
      TID=$(python3 -c "
import json
d=json.load(open('/tmp/s3-t.json'))
ts=d.get('tasks') or []
print(ts[0].get('id','') if ts else '')" 2>/dev/null)
      AURL=$(python3 -c "
import json
d=json.load(open('/tmp/s3-t.json'))
ts=d.get('tasks') or []
a=(ts[0].get('assets') or [{}])[0] if ts else {}
print(a.get('url',''))" 2>/dev/null)
      CODE_T=$(req /dev/null "$BASE/api/v1/tasks/$TID" -b /tmp/s3-cross.txt)
      if [ -z "$AURL" ]; then
        echo "SKIP: newest task has no asset url — cross-user asset check skipped (task=$CODE_T)"
      else
        CODE_A=$(req /dev/null "$BASE$AURL" -b /tmp/s3-cross.txt)
        if { [ "$CODE_T" = 403 ] || [ "$CODE_T" = 404 ]; } && { [ "$CODE_A" = 403 ] || [ "$CODE_A" = 404 ]; }; then
          ok "cross-user denied (task=$CODE_T asset=$CODE_A url=$AURL)"
        else
          bad "cross-user leak (task=$CODE_T asset=$CODE_A url=$AURL)"
        fi
      fi
    else
      bad "cross-user login ($RC2)"
    fi
  else
    echo "SKIP: admin user create unsupported ($RC) — cross-user check skipped"
  fi
fi

echo "=== SMOKE-L3 PASS=$PASS FAIL=$FAIL tag=$TAG ==="
[ "$FAIL" = 0 ] && exit 0 || exit 1
