#!/usr/bin/env python3
"""#689/#693/#611 三视图、四视图续跑回归（dev-test，fake 上游，零费用；只走 API）。

期望：
  window  自动方式，先占 98 个授权再出源图（第 99 个），三视图/四视图续跑跨过第 100 个窗口点也不打断：
          所有视角 succeeded、没有 awaiting_approval、待批准为空，image-to-image 任务数 = 视角数（3 或 4）且全部 SUCCEEDED
          （续跑只计数）；把计数补到 200 后 Agent 单独发起的 2D 出卡、不建任务；拒绝后下一个仍问；批准后执行成功；
          Agent 接着不再出卡；窗口从批准处重新计 100（批准 1 + Agent 1 + 直接 98 后再问，允许 97–99 的边界误差）。
  marks   #611：手选「三视图」「四视图」Skill，在 auto / budget / manual 三种方式下各发一次（共 6 次）：最终回复的
          used_skills 含该 Skill 且 source=user（严格/审批下脚本代为批准正面的卡）。
  strict  严格方式：Agent 发起的正面经人工批准后，用户在卡片上点左侧「继续生成」（views/left start）直接执行，
          不再出第二张卡（image-to-image 任务 +1）。
界面一侧（「稍后自动生成」只在 auto 且回合在跑时出现）见 scripts/regression/three-view-card.mjs。

用法（地址与 Agent 专用账号由 release-env.py 注入）：
  python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/three-view-continuation.py [--only window,marks,strict]
造数：Agent 专用账号下标题带 rg-tv 的若干会话，约 450 个 fake 2D 授权与二十几张 fake 视角图；约 10 分钟。
目标不是 fake 形态或给了 --live 时退出码 2。最后一行 `=== three-view-continuation: PASS=n FAIL=n ===`；有 FAIL 时退出码 1。
"""
import argparse
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lib import (SOURCE_PICTURE, TERMINAL, Api, Report, agent2d, decision_body, deny_all, groups, log,  # noqa: E402
                 new_session, pending, pre_grants, replies, require_fake, set_mode, slot_states, slots_of,
                 submit2d, tasks, three_view_marker, turn, wait_group, wait_pending, wait_task)


def i2i(api, sid):
    return [t for t in tasks(api, sid) if t.get("capability") == "image-to-image"]


def window(api, r):
    for label, four in (("三视图", False), ("四视图", True)):
        want = ("front", "left", "back", "right") if four else ("front", "left", "back")
        sid = new_session(api, f"rg-tv-window-{len(want)}")
        set_mode(api, sid, "auto")
        pre_grants(api, sid, 98)
        turn(api, sid, "画一个角色 " + SOURCE_PICTURE, seconds=90)
        time.sleep(2)
        turn(api, sid, f"做{label} " + three_view_marker(four), seconds=200)
        wait_group(api, sid, lambda g: g.get("complete") or "awaiting_approval" in slot_states(g).values(), 150)
        time.sleep(3)
        found = groups(api, sid)
        states = slot_states(found[0] if found else None)
        r.check(f"window {label}: 跨过窗口点，所有视角 succeeded", all(states.get(k) == "succeeded" for k in want), str(states))
        r.check(f"window {label}: 待批准为空", not pending(api, sid), f"cards={len(pending(api, sid))}")
        views = i2i(api, sid)
        r.check(f"window {label}: image-to-image 任务 {len(want)} 个且全部 SUCCEEDED（续跑只计数）",
                len(views) == len(want) and all(t["state"] == "SUCCEEDED" for t in views), f"n={len(views)}")
        turn(api, sid, "再画一张 " + agent2d(7), seconds=90)
        time.sleep(2)
        single_asked = bool(pending(api, sid))
        log(f"INFO window {label}: Agent single 2D right after the series asked={single_asked}")
        if single_asked:
            deny_all(api, sid)
        counted = 98 + 1 + len(want) + (0 if single_asked else 1)
        pre_grants(api, sid, 200 - counted, tag="w200")
        time.sleep(2)
        before = len(tasks(api, sid))
        turn(api, sid, "再画一张 " + agent2d(8), seconds=60)
        cards = wait_pending(api, sid, 25)
        r.check(f"window {label}: 计数到 200 时 Agent 单独发起的 2D 出卡、不建任务", len(cards) == 1 and len(tasks(api, sid)) == before,
                f"cards={len(cards)} tasks {before}->{len(tasks(api, sid))}")
        if not cards:
            continue
        status, _ = api.json("PUT", f"/api/v1/tasks/grants/{cards[0]['request_id']}", decision_body(api, "deny"))
        turn(api, sid, "再试一次 " + agent2d(9), seconds=60)
        cards = wait_pending(api, sid, 25)
        r.check(f"window {label}: 拒绝后下一个仍问", status == 200 and len(cards) == 1, f"deny={status} cards={len(cards)}")
        if not cards:
            continue
        status, body = api.json("PUT", f"/api/v1/tasks/grants/{cards[0]['request_id']}", decision_body(api, "approve"))
        task = wait_task(api, body.get("task_id")) if body.get("task_id") else {}
        r.check(f"window {label}: 批准后执行成功", status == 200 and task.get("state") == "SUCCEEDED", f"{status} {task.get('state')}")
        time.sleep(2)
        ok, why = turn(api, sid, "继续画 " + agent2d(10), seconds=90)
        time.sleep(2)
        r.check(f"window {label}: 批准后 Agent 接着不出卡", ok and not pending(api, sid), why)
        statuses = []
        for i in range(200):
            step = submit2d(api, sid, 500 + i)
            statuses.append(step.get("grant_status"))
            if step.get("grant_status") != "approved":
                break
            if i % 8 == 0:
                time.sleep(0.2)
        r.check(f"window {label}: 窗口从批准处重新计 100", statuses[-1] == "pending" and len(statuses) - 1 in (97, 98, 99),
                f"direct-before-ask={len(statuses) - 1}")
        deny_all(api, sid)


def used_skills(message):
    return message.get("used_skills") or []


def marks(api, r):
    for mode in ("auto", "budget", "manual"):
        for skill, name, four in (("system-three-view", "三视图", False), ("system-four-view", "四视图", True)):
            sid = new_session(api, f"rg-tv-mark-{mode}-{len(name)}")
            set_mode(api, sid, mode)
            turn(api, sid, "画一个角色 " + SOURCE_PICTURE, seconds=90)
            for card in wait_pending(api, sid, 8):
                _, body = api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
                if body.get("task_id"):
                    wait_task(api, body["task_id"])
            time.sleep(3)
            before = len(replies(api, sid)[0])
            body = {"content": "做图 " + three_view_marker(four), "creative_domain": "image", "_skill": {"id": skill, "name": name}}
            for _ in range(60):
                status, _ = api.json("POST", f"/api/chat/sessions/{sid}/messages", body)
                if status != 409:
                    break
                time.sleep(1)
            end, seen = time.time() + 200, set()
            while time.time() < end and len(replies(api, sid)[0]) <= before:
                for card in pending(api, sid):
                    api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
                for group in groups(api, sid):
                    for slot in slots_of(group).values():
                        rid = slot.get("request_id")
                        if slot.get("status") == "awaiting_approval" and rid and rid not in seen:
                            seen.add(rid)
                            api.json("PUT", f"/api/v1/tasks/grants/{rid}", decision_body(api, "approve"))
                time.sleep(1)
            done = replies(api, sid)[0]
            if len(done) <= before:
                r.check(f"marks {mode} {name}", False, "no reply")
                continue
            used = used_skills(done[-1])
            r.check(f"marks {mode} {name}: 回复的 used_skills 含该 Skill 且 source=user",
                    any(u.get("id") == skill and u.get("source") == "user" for u in used), json.dumps(used, ensure_ascii=False)[:160])


def strict(api, r):
    sid = new_session(api, "rg-tv-strict")
    set_mode(api, sid, "manual")
    turn(api, sid, "画一个角色 " + SOURCE_PICTURE, seconds=60)
    for card in wait_pending(api, sid, 20):
        _, body = api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
        if body.get("task_id"):
            wait_task(api, body["task_id"])
    time.sleep(3)
    send_end = time.time() + 150
    status = 0
    for _ in range(60):
        status, _ = api.json("POST", f"/api/chat/sessions/{sid}/messages", {"content": "做三视图 " + three_view_marker(), "creative_domain": "image"})
        if status != 409:
            break
        time.sleep(1)
    group = None
    while time.time() < send_end:
        for card in pending(api, sid):
            api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
        found = groups(api, sid)
        group = found[0] if found else None
        front = slots_of(group).get("front") or {}
        if front.get("status") == "awaiting_approval" and front.get("request_id"):  # the slot card is not always listed
            api.json("PUT", f"/api/v1/tasks/grants/{front['request_id']}", decision_body(api, "approve"))
        if group and slot_states(group).get("front") == "succeeded":
            break
        time.sleep(1)
    group = wait_group(api, sid, lambda g: slot_states(g).get("front") == "succeeded" and slot_states(g).get("left") in ("not_started",) + TERMINAL, 60)
    if not r.check("strict: 正面经人工批准后 succeeded", group is not None and slot_states(group).get("front") == "succeeded",
                   str(slot_states(group))):
        return
    before = len(i2i(api, sid))
    status, body = api.json("POST", f"/api/v1/chat/sessions/{sid}/three-views/{group['run_id']}/views/left", {"action": "start", "attempt": 0})
    group = wait_group(api, sid, lambda g: slot_states(g).get("left") in TERMINAL + ("awaiting_approval",), 90)
    states = slot_states(group)
    r.check("strict: 用户点左侧「继续生成」直接执行、不出第二张卡",
            status in (200, 201, 202) and states.get("left") == "succeeded" and not pending(api, sid) and len(i2i(api, sid)) == before + 1,
            f"start={status} {states} i2i {before}->{len(i2i(api, sid))}")


SECTIONS = {"window": window, "marks": marks, "strict": strict}


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--only", default=",".join(SECTIONS), help="comma-separated: " + ",".join(SECTIONS))
    parser.add_argument("--live", action="store_true", help=argparse.SUPPRESS)  # refused by require_fake()
    arguments = parser.parse_args()
    require_fake()
    api = Api().login()
    report = Report("three-view-continuation")
    for name in [n.strip() for n in arguments.only.split(",") if n.strip()]:
        if name not in SECTIONS:
            report.check(f"unknown section {name}", False)
            continue
        log(f"--- {name}")
        try:
            SECTIONS[name](api, report)
        except Exception as error:  # a crash is a failure, never a silent pass
            report.check(f"{name}: script error", False, f"{type(error).__name__}: {str(error)[:160]}")
    return report.finish()


if __name__ == "__main__":
    sys.exit(main())
