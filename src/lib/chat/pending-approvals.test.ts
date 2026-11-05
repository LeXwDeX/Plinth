import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

import {
  countPending,
  DecisionLedger,
  decidePendingApproval,
  decidePendingCard,
  decideSessionBudget,
  displayedApprovals,
  hasExpired,
  parsePendingApprovals,
  PendingApprovalError,
  retainExpired,
  type PendingApproval,
} from '@/lib/chat/pending-approvals';
import type { ProductSchema } from '@/lib/product-schema';

function approval(requestId: string, expiresAt: string, status: PendingApproval['status'] = 'pending'): PendingApproval {
  return {
    requestId, status, kind: 'step', plan: null, budgets: [],
    tool: 'create_model_3d', capability: 'text-to-3d', model: 'meshy-7.1', provider: 'Meshy AI', providerId: 'meshy',
    estimatedCredits: 20, baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [], prompt: 'a chair', inputs: [], messageId: '', createdAt: '', expiresAt,
  };
}

const now = Date.parse('2026-10-09T10:00:00Z');
const past = '2026-10-09T09:59:00Z';
const future = '2026-10-10T09:00:00Z';

describe('expired approval cards', () => {
  it('reads the server expiry instant', () => {
    expect(hasExpired(past, now)).toBe(true);
    expect(hasExpired(future, now)).toBe(false);
    expect(hasExpired('', now)).toBe(false);
  });

  it('keeps a card the server dropped after it expired, as expired', () => {
    const seen = [approval('r1', past), approval('r2', future)];
    const retained = retainExpired(seen, [approval('r2', future)], [], now);
    expect(retained).toEqual([{ ...approval('r1', past), status: 'expired' }]);
    const items = displayedApprovals([approval('r2', future)], retained);
    expect(items.map((item) => [item.requestId, item.status])).toEqual([['r2', 'pending'], ['r1', 'expired']]);
    expect(countPending(items, now)).toBe(1);
  });

  it('drops a card that vanished while still valid: it was decided or cancelled', () => {
    expect(retainExpired([approval('r1', future)], [], [], now)).toEqual([]);
  });

  it('keeps retained cards across polls without duplicating them', () => {
    const first = retainExpired([approval('r1', past)], [], [], now);
    const second = retainExpired([], [], first, now + 60_000);
    expect(second).toHaveLength(1);
    expect(second[0]).toBe(first[0]);
    expect(displayedApprovals([], second)).toHaveLength(1);
  });

  it('does not count a listed card whose expiry has passed on this page', () => {
    const items = displayedApprovals([approval('r1', past), approval('r2', future)], []);
    expect(countPending(items, now)).toBe(1);
    expect(items).toHaveLength(2);
  });
});

describe('a card kind this client does not render yet (#103)', () => {
  it('skips a production plan card instead of showing it as a step', () => {
    const items = parsePendingApprovals({ items: [
      { request_id: 'plan-1', status: 'pending', kind: 'plan', budgets: [{ unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '100' }] },
      { request_id: 'step-1', status: 'pending', capability: 'text-to-3d' },
    ] });
    expect(items.map((item) => item.requestId)).toEqual(['step-1']);
  });
});

describe('the extra credits of a pending step', () => {
  it('reads the split between base and extra credits and what the extra pays for', () => {
    const [item] = parsePendingApprovals({ items: [{
      request_id: 'r1', status: 'pending', estimated_credits: 50, base_credits: 20, extra_credits: 30,
      extra_label: '附加导出格式', extra_items: ['FBX', 'USDZ', 7],
    }] });
    expect(item).toMatchObject({ estimatedCredits: 50, baseCredits: 20, extraCredits: 30, extraLabel: '附加导出格式', extraItems: ['FBX', 'USDZ'] });
  });

  it('has no extra when the server reports none', () => {
    const [item] = parsePendingApprovals({ items: [{ request_id: 'r2', status: 'pending', estimated_credits: 20 }] });
    expect(item).toMatchObject({ baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [] });
  });
});

describe('a step that repeats a charged attempt whose result was not stored (#294)', () => {
  it('carries the earlier task to the card', () => {
    const [item] = parsePendingApprovals({ items: [{ request_id: 'r3', status: 'pending', recharge_task_id: 'task_old' }] });
    expect(item.rechargeTaskId).toBe('task_old');
  });

  it('has no recharge mark when the server sends none', () => {
    const [item] = parsePendingApprovals({ items: [{ request_id: 'r4', status: 'pending' }] });
    expect(item.rechargeTaskId).toBeUndefined();
  });
});

describe('a step that uses only the first of the pictures given (#329 E)', () => {
  it('carries the server sentence to the card as it is', () => {
    const note = 'Meshy 7.1 一次只用 1 张参考图：这次共 2 张，只用第 1 张。要用多张不同角度的图，请改选支持多图的模型。';
    const [item] = parsePendingApprovals({ items: [{ request_id: 'r5', status: 'pending', reference_note: note }] });
    expect(item.referenceNote).toBe(note);
  });

  it('has no note when the server sends none', () => {
    const [item] = parsePendingApprovals({ items: [{ request_id: 'r6', status: 'pending' }] });
    expect(item.referenceNote).toBeUndefined();
  });
});

describe('multiview reference pictures', () => {
  it('parses the view key and label of each input, tolerating their absence', () => {
    const [item] = parsePendingApprovals({ items: [{ request_id: 'rv', status: 'pending', reference_note: '由 正面 / 左侧 视图生成', inputs: [
      { parameter: 'reference_assets', kind: 'asset', asset_id: 'a1', view: 'front', view_label: '正面' },
      { parameter: 'reference_assets', kind: 'asset', asset_id: 'a2', view: 'left', view_label: '左侧' },
      { parameter: 'reference_assets', kind: 'asset', asset_id: 'a3' },
    ] }] });
    expect(item.inputs.map((input) => [input.view, input.viewLabel])).toEqual([['front', '正面'], ['left', '左侧'], [undefined, undefined]]);
    expect(item.referenceNote).toBe('由 正面 / 左侧 视图生成');
  });
});

describe('a poll that was sent before a decision', () => {
  const card = approval('r1', future);
  const other = approval('r2', future);

  it('cannot bring a decided card back, whenever it returns', () => {
    const ledger = new DecisionLedger();
    // The poll leaves at t=100; the person approves and the server answers at t=150;
    // the poll's answer (read before the commit) only arrives at t=200.
    const stale = { startedAt: 100, items: [card, other] };
    ledger.markDecided('r1', 150);
    expect(ledger.visible(stale).map((item) => item.requestId)).toEqual(['r2']);
    // Reading it again does not change the answer.
    expect(ledger.visible(stale).map((item) => item.requestId)).toEqual(['r2']);
  });

  it('hides a card decided in the same millisecond the poll left', () => {
    const ledger = new DecisionLedger();
    ledger.markDecided('r1', 100);
    expect(ledger.visible({ startedAt: 100, items: [card] })).toEqual([]);
  });

  it('keeps hiding the card until a poll sent after the decision arrives, then trusts the server', () => {
    const ledger = new DecisionLedger();
    ledger.markDecided('r1', 150);
    const stale = { startedAt: 100, items: [card] };
    ledger.settle(stale);
    expect(ledger.visible(stale)).toEqual([]);
    const fresh = { startedAt: 160, items: [other] };
    expect(ledger.visible(fresh)).toEqual([other]);
    ledger.settle(fresh);
    // Forgotten: the server's list is the truth again, so a card it lists is shown.
    expect(ledger.visible({ startedAt: 170, items: [card] })).toEqual([card]);
  });

  it('leaves the cards nobody decided alone', () => {
    const ledger = new DecisionLedger();
    const polled = { startedAt: 100, items: [card, other] };
    expect(ledger.visible(polled)).toBe(polled.items);
    ledger.markDecided('r9', 500);
    expect(ledger.visible(polled)).toEqual([card, other]);
  });
});

describe('the pending-approval hook', () => {
  it('stamps each read with the moment it was sent and filters it through the ledger', () => {
    const hook = readFileSync(new URL('../../hooks/use-session-pending-approvals.ts', import.meta.url), 'utf8');
    expect(hook).toContain('const startedAt = Date.now();');
    expect(hook).toContain('ledger.current.visible(data)');
    expect(hook).toContain('ledger.current.markDecided(requestId, Date.now());');
    expect(hook).toContain('ledger.current.settle(data);');
  });
});

it('keeps only the server-frozen Agent adjustment facts on pending cards', () => {
 const items=parsePendingApprovals({items:[{request_id:'frozen',status:'pending',parameter_adjustments:[{name:'quality',label:'Quality',recommended_value:'extreme',value:'detailed',reason:'User requested an earlier draft',advice:'Not recommended',source:'agent'},{name:'bad',label:'Bad',recommended_value:1,value:2,source:'user'}]}]});
 expect(items[0].parameterAdjustments).toEqual([{name:'quality',label:'Quality',recommendedValue:'extreme',value:'detailed',reason:'User requested an earlier draft',advice:'Not recommended',source:'agent'}]);
});

describe('deciding an approval through the Schema-declared decision member (#103)', () => {
  function approvalSchema(decision?: string): ProductSchema {
    return {
      chat: { api: { fields: decision === undefined ? {} : { decision } } },
      runtime: { http: { routes: {
        task_grant_decision: '/api/grants/{requestID}/decision',
        chat_session_budget: '/api/v1/chat/sessions/{sessionID}/budgets/{budgetID}',
      } } },
    } as unknown as ProductSchema;
  }

  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

  function expectBody(call: [unknown, RequestInit | undefined], expected: unknown): void {
    expect(JSON.parse(String(call[1]?.body))).toEqual(expected);
  }

  it('sends the decision under the Schema member and reads the step answer', async () => {
    const fetcher = vi.fn(async () => response({
      request_id: 'r1', status: 'approved', task_id: 'task-1', message_id: 'm1',
    }));
    const outcome = await decidePendingApproval(approvalSchema('verdict'), 'r 1', true, fetcher as unknown as typeof fetch);
    const call = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(call[0]).toBe('/api/grants/r%201/decision');
    expect(call[1].method).toBe('PUT');
    expectBody(call, { verdict: 'approve' });
    expect(outcome).toEqual({ requestId: 'r1', status: 'approved', taskId: 'task-1', messageId: 'm1' });
  });

  it('sends a deny under the same member on the session budget route', async () => {
    const fetcher = vi.fn(async () => response({ budget_id: 'b1', state: 'denied', message_id: 'm2' }));
    const outcome = await decideSessionBudget(approvalSchema('verdict'), 's 1', 'b/1', false, fetcher as unknown as typeof fetch);
    const call = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(call[0]).toBe('/api/v1/chat/sessions/s%201/budgets/b%2F1');
    expectBody(call, { verdict: 'deny' });
    expect(outcome).toEqual({ requestId: 'b1', status: 'denied', taskId: '', messageId: 'm2' });
  });

  it('refuses to send an empty decision member when the Schema declares none', async () => {
    const fetcher = vi.fn();
    await expect(decidePendingApproval(approvalSchema(), 'r1', true, fetcher as unknown as typeof fetch))
      .rejects.toThrow('chat.api.fields.decision is required to decide an approval');
    await expect(decideSessionBudget(approvalSchema(), 's1', 'b1', true, fetcher as unknown as typeof fetch))
      .rejects.toThrow('chat.api.fields.decision is required to decide an approval');
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('keeps the server status and detail of a refused decision', async () => {
    const fetcher = vi.fn(async () => response({ detail: 'ui.pending_approval.expired' }, 409));
    await expect(decidePendingApproval(approvalSchema('decision'), 'r1', true, fetcher as unknown as typeof fetch))
      .rejects.toMatchObject({ name: PendingApprovalError.name, status: 409, detail: 'ui.pending_approval.expired' });
    const planFetcher = vi.fn(async () => response({ detail: 'ui.studio.plan.decide_failed' }, 409));
    await expect(decideSessionBudget(approvalSchema('decision'), 's1', 'b1', false, planFetcher as unknown as typeof fetch))
      .rejects.toMatchObject({ name: PendingApprovalError.name, status: 409, detail: 'ui.studio.plan.decide_failed' });
  });

  it('dispatches a plan card to the budget route and a step card to the grant route', async () => {
    const fetcher = vi.fn(async (url: string) => url.includes('/budgets/')
      ? response({ budget_id: 'b1', state: 'active' })
      : response({ request_id: 'r1', status: 'approved' }));
    await decidePendingCard(approvalSchema('decision'), 's1', { requestId: 'b1', kind: 'plan' }, true, fetcher as unknown as typeof fetch);
    await decidePendingCard(approvalSchema('decision'), 's1', { requestId: 'r1', kind: 'step' }, false, fetcher as unknown as typeof fetch);
    const calls = fetcher.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map((call) => call[0])).toEqual([
      '/api/v1/chat/sessions/s1/budgets/b1',
      '/api/grants/r1/decision',
    ]);
    // Every route placeholder must be filled; a literal {sessionID} 404s on the server.
    expect(calls.every((call) => !/[{}]/.test(decodeURIComponent(call[0])))).toBe(true);
    expectBody(calls[0], { decision: 'approve' });
    expectBody(calls[1], { decision: 'deny' });
  });
});
