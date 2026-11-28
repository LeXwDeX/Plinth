#!/usr/bin/env python3
"""#702 批准方式回归（dev-test，fake 上游，零费用；只走 API）。

批准方式：auto（自动）、budget（审批）、manual（严格）。期望：
  window  自动：用户路径前 100 个 2D 授权直接通过、无卡；第 101 个出卡；拒绝后下一个仍问；人工批准的那一步执行成功；
          之后第 102..200 个（共 99 个）直接执行；第 201 个再问（批准一次后再计 100）。
  agent   自动：先占满 100 个授权，Agent 发起的第 101 个 2D 出批准卡且不建任务；批准后执行成功；Agent 下一步不再出卡。
  budget  审批：会话读回 approvalMode=budget；Agent 的每个 3D 步骤都出卡（第 2 个也问），批准后 SUCCEEDED；
          Agent 的 2D 自动执行、不出卡；多步 3D 请求不出计划卡；100 个 2D 直接执行后第 101 个（Agent 2D）出卡。
  manual  严格：Agent 的 2D 也出卡、批准后执行；Agent 的 3D 出卡；用户自己发起的 2D（面板/API 路径）不再出第二张卡、
          直接 SUCCEEDED；用户自己发起的 3D 仍要批准（grant pending），批准后 SUCCEEDED。
  chain   三种方式各跑一遍 Meshy 7.1 文生 3D → 图生 3D → remesh → rigging → 格式转换：每步 SUCCEEDED、产物逐个下载、
          字节数与 sha256 与清单一致；是否出卡只记录（auto 全不问；budget 只问文生/图生 3D；manual 除转换外都问）。

用法（地址与 Agent 专用账号由 release-env.py 注入）：
  python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/approval-modes.py [--only window,agent,budget,manual,chain]
造数：Agent 专用账号下标题带 rg-702 的若干会话，约 450 个 fake 2D 授权与十几个 fake 3D 任务；约 10–15 分钟。
目标不是 fake 形态或给了 --live 时退出码 2。最后一行 `=== approval-modes: PASS=n FAIL=n ===`；有 FAIL 时退出码 1。
"""
import argparse
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from lib import (M3T, T2I, Api, Report, agent2d, decide, decision_body, deny_all, direct, make_png,  # noqa: E402
                 new_session, pending, pre_grants, require_fake, set_mode, submit2d, tasks, turn, upload,
                 wait_pending, wait_task, wait_tasks_settled, check_delivery, last_assistant, log)

MARK3D = '[[fake:tool:create_model_3d {"prompt":"a plain cube","model":"meshy-7.1"}]]'


def agent3d(api, sid, text="make a cube"):
    return turn(api, sid, f"{text} {MARK3D}", seconds=60, creative_domain="model-3d", ai_model=M3T,
                domainModels={"model-3d": M3T})


def approve_card(api, card):
    status, body = api.json("PUT", f"/api/v1/tasks/grants/{card['request_id']}", decision_body(api, "approve"))
    task = wait_task(api, body["task_id"]) if body.get("task_id") else {}
    return status, task


def window(api, r):
    sid = new_session(api, "rg-702-window")
    set_mode(api, sid, "auto")
    statuses = []
    for i in range(1, 101):
        step = submit2d(api, sid, i)
        if "err" in step:
            return r.check(f"window: step {i}", False, step["err"])
        statuses.append(step["grant_status"])
        if step["grant_status"] == "pending":
            break
        if i % 8 == 0:
            time.sleep(0.3)
    r.check("window: 前 100 个 2D 授权直接通过、无卡", statuses.count("approved") == 100 and not pending(api, sid),
            f"approved={statuses.count('approved')} cards={len(pending(api, sid))}")
    wait_tasks_settled(api, sid, 100)
    step = submit2d(api, sid, 101)
    r.check("window: 第 101 个出批准卡", step.get("grant_status") == "pending" and len(pending(api, sid)) == 1,
            f"grant={step.get('grant_status')} cards={len(pending(api, sid))}")
    status, _ = api.json("PUT", f"/api/v1/tasks/grants/{step['rid']}", decision_body(api, "deny"))
    step = submit2d(api, sid, 102)
    r.check("window: 拒绝后下一个仍问", status == 200 and step.get("grant_status") == "pending",
            f"deny={status} next={step.get('grant_status')}")
    decide(api, step)
    task = wait_task(api, step["task_id"]) if step.get("task_id") else {}
    r.check("window: 人工批准的这一步执行成功", step.get("decision_status") == 200 and task.get("state") == "SUCCEEDED",
            f"decision={step.get('decision_status')} task={task.get('state')}")
    after = []
    for i in range(103, 202):
        step = submit2d(api, sid, i)
        if "err" in step:
            return r.check(f"window: step {i}", False, step["err"])
        after.append(step["grant_status"])
        if step["grant_status"] == "pending":
            break
        if i % 8 == 0:
            time.sleep(0.3)
    r.check("window: 批准后接下来 99 个直接执行", len(after) == 99 and set(after) == {"approved"},
            f"count={len(after)} statuses={sorted(set(after))}")
    time.sleep(3)
    step = submit2d(api, sid, 201)
    r.check("window: 第 201 个再问", step.get("grant_status") == "pending", f"grant={step.get('grant_status')}")
    deny_all(api, sid)


def agent(api, r):
    sid = new_session(api, "rg-702-agent")
    set_mode(api, sid, "auto")
    pre_grants(api, sid, 100)
    before = len(tasks(api, sid))
    turn(api, sid, "画 " + agent2d(1), seconds=60)
    cards = wait_pending(api, sid, 25)
    r.check("agent: 自动方式下 Agent 的第 101 个 2D 出卡、不建任务", len(cards) == 1 and len(tasks(api, sid)) == before,
            f"cards={len(cards)} tasks {before}->{len(tasks(api, sid))}")
    if not cards:
        return
    status, task = approve_card(api, cards[0])
    r.check("agent: 批准后这一步执行成功", status == 200 and task.get("state") == "SUCCEEDED", f"PUT {status} {task.get('state')}")
    time.sleep(2)
    ok, why = turn(api, sid, "继续 " + agent2d(2), seconds=90)
    time.sleep(2)
    r.check("agent: 批准后 Agent 下一步不再出卡", ok and not pending(api, sid), f"{why} cards={len(pending(api, sid))}")


def budget(api, r):
    sid = new_session(api, "rg-702-budget")
    status, _ = set_mode(api, sid, "budget")
    _, session = api.json("GET", f"/api/chat/sessions/{sid}")
    r.check("budget: 会话读回 approvalMode=budget", status == 200 and session.get("approvalMode") == "budget",
            f"PUT {status} GET {session.get('approvalMode')}")
    agent3d(api, sid)
    cards = wait_pending(api, sid, 20)
    r.check("budget: Agent 的 3D 步骤出卡（不是计划卡）",
            len(cards) == 1 and cards[0].get("capability") == "text-to-3d" and cards[0].get("kind") != "plan",
            str([(c.get("capability"), c.get("kind")) for c in cards]))
    if cards:
        status, task = approve_card(api, cards[0])
        r.check("budget: 批准后 3D SUCCEEDED", status == 200 and task.get("state") == "SUCCEEDED", f"PUT {status} {task.get('state')}")
    agent3d(api, sid, "make another cube")
    cards = wait_pending(api, sid, 20)
    r.check("budget: 第 2 个 3D 步骤也问", len(cards) == 1, f"cards={len(cards)}")
    if cards:
        approve_card(api, cards[0])
    before = len(tasks(api, sid))
    ok, why = turn(api, sid, "画一个 " + agent2d(1), seconds=90, creative_domain="image")
    time.sleep(2)
    r.check("budget: Agent 的 2D 自动执行、不出卡", ok and not pending(api, sid) and len(tasks(api, sid)) == before + 1,
            f"{why} tasks {before}->{len(tasks(api, sid))}")
    plain = new_session(api, "rg-702-budget-plain")
    set_mode(api, plain, "budget")
    ok, _ = turn(api, plain, "帮我做一个完整的游戏角色：先出概念图，再生成 3D 模型，最后绑骨并做一个走路动画", seconds=60,
                 creative_domain="model-3d")
    r.check("budget: 多步 3D 请求不出计划卡（fake 对话模型）", ok and not any(c.get("kind") == "plan" for c in pending(api, plain)),
            last_assistant(api, plain)[:60])
    win = new_session(api, "rg-702-budget-window")
    set_mode(api, win, "budget")
    failed = None
    for i in range(1, 101):
        step = submit2d(api, win, i)
        if step.get("grant_status") != "approved":
            failed = (i, step.get("grant_status") or step.get("err"))
            break
        if i % 8 == 0:
            time.sleep(0.3)
    r.check("budget: 100 个 2D 直接执行、无卡", failed is None and not pending(api, win), f"failed={failed}")
    time.sleep(4)
    turn(api, win, "再画 " + agent2d(101), seconds=60)
    r.check("budget: 第 101 个（Agent 2D）出卡", len(wait_pending(api, win, 20)) == 1)
    deny_all(api, win)


def manual(api, r):
    sid = new_session(api, "rg-702-manual")
    set_mode(api, sid, "manual")
    before = len(tasks(api, sid))
    turn(api, sid, "画一个 " + agent2d(1), seconds=60, creative_domain="image")
    cards = wait_pending(api, sid, 20)
    r.check("manual: Agent 的 2D 也出卡、不建任务",
            len(cards) == 1 and cards[0].get("capability") == "text-to-image" and len(tasks(api, sid)) == before,
            f"cards={[c.get('capability') for c in cards]}")
    if cards:
        status, task = approve_card(api, cards[0])
        r.check("manual: 批准后这一步执行", status == 200 and task.get("state") == "SUCCEEDED", f"{status} {task.get('state')}")
    agent3d(api, sid)
    cards = wait_pending(api, sid, 20)
    r.check("manual: Agent 的 3D 出卡", len(cards) == 1, f"cards={len(cards)}")
    if cards:
        approve_card(api, cards[0])
    step = direct(api, sid, T2I, "text-to-image", {"prompt": "user panel 2d"})
    r.check("manual: 用户自己发起的 2D 不再出第二张卡、直接执行",
            step.get("grant_status") == "approved" and (step.get("task") or {}).get("state") == "SUCCEEDED" and not pending(api, sid),
            f"grant={step.get('grant_status')} task={(step.get('task') or {}).get('state')} {step.get('err', '')}")
    step = direct(api, sid, M3T, "text-to-3d", {"prompt": "user panel 3d"}, wait=False)
    r.check("manual: 用户自己发起的 3D 仍要批准", step.get("grant_status") == "pending", f"grant={step.get('grant_status')}")
    if step.get("rid"):
        decide(api, step)
        task = wait_task(api, step["task_id"]) if step.get("task_id") else {}
        r.check("manual: 批准用户的 3D 后执行成功", step.get("decision_status") == 200 and task.get("state") == "SUCCEEDED",
                f"{step.get('decision_status')} {task.get('state')}")


def chain(api, r):
    picture = upload(api, "rg-ref.png", make_png())

    def step(mode, sid, label, offering, capability, base):
        s = direct(api, sid, offering, capability, base, wait=False)
        if s.get("err"):
            r.check(f"chain {mode} {label}", False, s["err"])
            return None
        asked = s.get("grant_status") == "pending"
        if asked:
            decide(api, s)
        if not s.get("task_id"):
            r.check(f"chain {mode} {label}", False, f"no task: {s.get('decision_status')} {s.get('task_status')}")
            return None
        task = wait_task(api, s["task_id"], 120)
        count, _, problems = check_delivery(api, task) if task.get("state") == "SUCCEEDED" else (0, [], ["not succeeded"])
        r.check(f"chain {mode} {label} SUCCEEDED、产物字节一致", task.get("state") == "SUCCEEDED" and count > 0 and not problems,
                f"asked={asked} assets={count} {problems[:2]}")
        return task

    for mode in ("auto", "budget", "manual"):
        sid = new_session(api, f"rg-702-chain-{mode}")
        set_mode(api, sid, mode)
        text = step(mode, sid, "text-to-3d", "meshy/meshy-7.1/text-to-3d", "text-to-3d", {"prompt": "a plain cube"})
        step(mode, sid, "image-to-3d", "meshy/meshy-7.1/image-to-3d", "image-to-3d", {"reference_assets": [picture]})
        if text:
            remesh = step(mode, sid, "remesh", "meshy/processing/remesh", "remesh",
                          {"source_task": text["id"], "topology": "triangle", "target_polycount": 10000})
            if remesh and remesh.get("state") == "SUCCEEDED":
                step(mode, sid, "rigging", "meshy/processing/rigging", "rigging", {"source_task": remesh["id"]})
            step(mode, sid, "convert", "meshy/processing/convert", "format-conversion", {"source_task": text["id"]})


SECTIONS = {"window": window, "agent": agent, "budget": budget, "manual": manual, "chain": chain}


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--only", default=",".join(SECTIONS), help="comma-separated: " + ",".join(SECTIONS))
    parser.add_argument("--live", action="store_true", help=argparse.SUPPRESS)  # refused by require_fake()
    arguments = parser.parse_args()
    require_fake()
    api = Api().login()
    report = Report("approval-modes")
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
