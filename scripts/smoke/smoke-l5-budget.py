#!/usr/bin/env python3
"""L5: budget approval mode (#103), the default of a new session.

Two chains, each in a fresh session that keeps its default approval mode:
  approve  The session reads back "budget". A priced text-to-3D model is locked
           (L5_BUDGET_MODEL=<model_id>, else Tripo P2, else another Tripo
           Offering). A multi-step request (generate a game prop, inspect it,
           optimise if needed) must bring a pending production plan card with a
           budget ID, a limit per credit unit and steps; choice cards on the way
           are answered with their default. The card is approved on the Schema's
           chat_session_budget route exactly as the browser does, and the server
           starts the turn that runs the plan by itself (#134): nobody sends
           another message. Pass: the approval names that turn, no per-step paid
           approval card appears while the plan runs, the tasks' prices stay
           within the plan limit of their own unit (and the server's per-unit
           ledger agrees), and at least one 3D task is SUCCEEDED with its
           required artifacts READY.
  deny     Same request; the plan card is denied. Pass: no task, nothing
           charged, no card left pending, and the reply says nothing ran.
L5_BUDGET_ONLY=approve,deny limits the run. Real model generations; needs
ART_AI_LIVE_ACK=1.
"""
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import l5_common as common  # noqa: E402

CAPABILITY = "text-to-3d"
THREE_D = ("text-to-3d", "image-to-3d")
REQUEST = ("做一个低面数的游戏道具 3D 模型：一个带金属包边的木制宝箱，面数控制在 5000 面以内。"
           "这是多步制作：先生成模型，再检查模型，面数或结构不合格就优化。"
           "请先把制作计划和按积分单位的预算发给我批准，批准后再按计划执行。")
# Nothing starts a turn after a denial; this is how long the deny chain watches for stray work.
DENY_SETTLE_SECONDS = 30


def selected():
    value = os.environ.get("L5_BUDGET_ONLY", "")
    return {part.strip() for part in value.split(",") if part.strip()} or {"approve", "deny"}


def per_unit(entries):
    amounts = common.unit_amounts(entries) or {}
    return {unit: str(amount) for unit, amount in sorted(amounts.items())}


def describe_card(card):
    plan = card.get("plan") or {}
    return (f"budget {card.get('request_id')} limits {per_unit(card.get('budgets'))} "
            f"quote {per_unit(card.get('estimated_totals'))} "
            f"steps {common.plan_capabilities(card)} title {common.one_line(plan.get('title'), 40)!r}")


def propose(live, report, label, model):
    """Fresh session in its default mode, model locked, request sent; returns (session, plan card) or None."""
    session = live.new_session(approval=None)
    if not report.check(f"{label}: session created", bool(session)):
        return None
    mode = live.approval_mode(session)
    if not report.check(f"{label}: new session defaults to budget", mode == "budget", f"approvalMode={mode!r}"):
        return None
    live.lock_model_3d(session, model)
    status, _ = live.send(session, REQUEST, "model-3d", model)
    if not report.check(f"{label}: request accepted", status in (200, 201, 202), f"HTTP {status}"):
        return None
    reply = live.wait_reply(session)
    if not report.check(f"{label}: first turn finished", reply is not None,
                        "" if reply is not None else "timed out waiting for the turn"):
        return None
    card, steps = live.wait_plan_card(session)
    if not report.check(f"{label}: no per-step approval card before the plan", not steps,
                        str([(item.get("capability"), item.get("tool")) for item in steps])):
        return None
    last = common.last_assistant(live.messages(session)) or {}
    if not report.check(f"{label}: plan card pending", card is not None,
                        describe_card(card) if card else f"reply: {common.one_line(last.get('content'), 120)}"):
        return None
    problems = common.plan_card_problems(card)
    if not report.check(f"{label}: plan card carries budget id, per-unit limits and steps", not problems,
                        "; ".join(problems) or describe_card(card)):
        return None
    names = common.tool_names(live.messages(session))
    report.check(f"{label}: Agent proposed the plan with {card.get('tool') or 'the proposal tool'}",
                 bool(card.get("tool")) and card.get("tool") in names, str(sorted(set(names))))
    route = live.plan_route(session, card["request_id"])
    if not report.check(f"{label}: plan decision route filled from the Schema", route is not None,
                        common.redact_url(route or "chat_session_budget missing or a placeholder left")):
        return None
    return session, card


def decide(live, report, label, session, card, approve):
    status, data = live.decide_plan(session, card["request_id"], approve)
    want = "active" if approve else "denied"
    ok = status == 200 and data.get("budget_id") == card["request_id"] and data.get("state") == want
    report.check(f"{label}: plan {'approved' if approve else 'denied'} on the budget route", ok,
                 f"HTTP {status} state={data.get('state')!r}")
    return ok, data


def priced_tasks(live, session, tasks):
    """(prices of the tasks that may have spent, IDs of those tasks nothing public prices)."""
    prices = common.call_prices(live.messages(session))
    priced, unpriced = [], []
    for task in tasks:
        if not common.may_be_charged(task):
            continue
        price = common.task_price(task, prices)
        if price is None:
            price = common.task_price(task, prices, live.task(task["id"]))
        if price is None:
            unpriced.append(task.get("id"))
        else:
            priced.append(price)
    return priced, unpriced


def approve_chain(live, report, model):
    proposed = propose(live, report, "approve", model)
    if not proposed:
        return
    session, card = proposed
    before = live.tasks(session)
    report.check("approve: no task before the plan was decided", not before, str(common.describe_tasks(before)))
    ok, decided = decide(live, report, "approve", session, card, approve=True)
    if not ok:
        return
    # #134: the approval itself starts the turn that runs the plan.
    if not report.check("approve: the approval started the turn that runs the plan",
                        bool(decided.get("turn_message_id")), f"response {sorted(decided)}"):
        return
    summary = (live.session(session) or {}).get("budget") or {}
    report.check("approve: session budget active with the card's per-unit limits",
                 summary.get("id") == card["request_id"] and summary.get("state") == "active"
                 and common.budget_limits(summary) == common.budget_limits(card),
                 f"state={summary.get('state')!r} limits={per_unit([{**unit, 'amount': unit.get('limit')} for unit in summary.get('units') or []])} "
                 f"card={per_unit(card.get('budgets'))}")

    known = {item.get("id") for item in before}
    step_seen, plan_seen = {}, {}

    def watch(session_id):
        # Inside an approved plan every planned step is approved by the budget; a step card, or a
        # new plan card, means the plan did not cover what the Agent did. Nobody will decide it.
        pending = live.pending_approvals(session_id)
        step_seen.update({item.get("request_id"): item for item in common.step_cards(pending)})
        plan_seen.update({item.get("request_id"): item for item in common.plan_cards(pending)})
        return bool(step_seen or plan_seen)

    reply, tasks, settled = live.follow_turn(session, {CAPABILITY: 1}, watch=watch)
    watch(session)
    report.check("approve: no per-step paid approval card while the plan ran", not step_seen,
                 str([(item.get("capability"), item.get("tool"), item.get("estimated_credits"))
                      for item in step_seen.values()]))
    report.check("approve: no further plan card while the plan ran", not plan_seen,
                 str([describe_card(item) for item in plan_seen.values()]))
    if reply is None:
        return
    new = [task for task in tasks if task.get("id") not in known]
    report.check("approve: planned tasks settled", settled, str(common.describe_tasks(new)))

    summary = (live.session(session) or {}).get("budget") or {}
    limits = common.budget_limits(summary) or common.budget_limits(card)
    priced, unpriced = priced_tasks(live, session, new)
    overruns = common.budget_overruns(priced, limits)
    spent = {unit: str(amount) for unit, amount in common.spend_by_unit(priced).items()}
    report.check("approve: task prices within the plan limit of their own unit", not overruns,
                 f"spent {spent} limits { {unit: str(amount) for unit, amount in limits.items()} }"
                 + (f" over {[(u, str(s), str(l)) for u, s, l in overruns]}" if overruns else "")
                 + (f" unpriced {unpriced}" if unpriced else ""))
    # "" means the session budget carries no readable per-unit ledger: that fails too.
    ledger = common.ledger_overrun(summary)
    detail = f"state={summary.get('state')} units={summary.get('units')}"
    if ledger:
        detail = ledger
    elif ledger == "":
        detail = "the session budget has no readable per-unit ledger"
    report.check("approve: plan ledger within the limit of every unit", ledger is None, detail)

    done = [task for task in new if task.get("capability") in THREE_D and task.get("state") == common.SUCCESS_STATE]
    if report.check("approve: a 3D task SUCCEEDED under the plan", bool(done), str(common.describe_tasks(new))):
        live.verify_task_artifacts(done[0]["id"], "approve: 3D task")


def deny_chain(live, report, model):
    proposed = propose(live, report, "deny", model)
    if not proposed:
        return
    session, card = proposed
    ok, data = decide(live, report, "deny", session, card, approve=False)
    if not ok:
        return
    report.check("deny: no turn started for a denied plan", not data.get("turn_message_id"),
                 f"turn_message_id={data.get('turn_message_id')!r}")
    time.sleep(DENY_SETTLE_SECONDS)
    tasks = live.tasks(session)
    report.check("deny: no task created", not tasks, str(common.describe_tasks(tasks)))
    read = live.session(session)
    charges = common.session_charges(read) if read is not None else ["session unreadable"]
    report.check("deny: nothing charged", not charges, "; ".join(charges))
    pending = live.pending_approvals(session)
    report.check("deny: no card left pending", not pending,
                 str([(item.get("kind") or "step", item.get("capability")) for item in pending]))
    messages = live.messages(session)
    notice = next((message for message in messages if message.get("id") == data.get("message_id")), None)
    reply = notice or common.last_assistant(messages) or {}
    report.check("deny: reply says nothing was executed", bool(common.NOT_EXECUTED.search(reply.get("content") or "")),
                 common.one_line(reply.get("content"), 120))


def body(live, report):
    model = common.budget_model(live.offerings(CAPABILITY), os.environ.get("L5_BUDGET_MODEL"))
    if not model:
        wanted = os.environ.get("L5_BUDGET_MODEL")
        return report.skipped("budget", f"L5_BUDGET_MODEL {wanted} is not an available, priced {CAPABILITY} Offering"
                              if wanted else f"no available, priced {CAPABILITY} Offering")
    print(f"budget chains use 3D model {model}", flush=True)
    chains = {"approve": approve_chain, "deny": deny_chain}
    wanted = selected()
    for name, chain in chains.items():
        if name in wanted:
            chain(live, report, model)


if __name__ == "__main__":
    sys.exit(common.execute("smoke-l5-budget", body, __doc__))
