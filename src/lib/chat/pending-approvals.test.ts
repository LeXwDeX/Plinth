import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { countPending, DecisionLedger, displayedApprovals, hasExpired, parsePendingApprovals, retainExpired, type PendingApproval } from '@/lib/chat/pending-approvals';

function approval(requestId: string, expiresAt: string, status: PendingApproval['status'] = 'pending'): PendingApproval {
  return {
    requestId, status, tool: 'create_model_3d', capability: 'text-to-3d', model: 'meshy-7.1', provider: 'Meshy AI', providerId: 'meshy',
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
