import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { parseSessionBudget, type SessionBudget } from '@/lib/chat/budget';
import { budgetFollowsTasks, followSettledBudget } from '@/lib/chat/session-budget';
import type { ProductSchema } from '@/lib/product-schema';

// #561: a decided plan follows the session's Tasks. The settlement charges a
// step's reservation a moment after its Task ends and the plan then turns used
// up or completed; the page reads the budget again instead of keeping the
// figures of the moment it was approved.

const raw = defaultCatalog.application;
const schema = {
  ...raw,
  chat: { ...raw.chat, approval_policy: { ...raw.chat.approval_policy,
    budget_states: raw.chat.approval_policy.budget.states.map(({ id, role }) => ({ id, role })) } },
} as unknown as ProductSchema;

const tripoPlan = (state: string, spent: string, remaining: string): SessionBudget => parseSessionBudget(schema, {
  id: 'budget-tripo', state,
  units: [{ unit: 'tripo.credit', unit_label: 'Tripo credits', limit: '110', spent, reserved: '0', remaining }],
  plan: { title: '四视图生成石雕', steps: [
    { capability: 'image-to-3d', offering_id: 'tripo-p2-multiview', purpose: '多视图低模',
      estimate: { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '110' } },
  ] },
  created_at: '2026-11-24T08:50:00Z', decided_at: '2026-11-24T08:51:00Z',
})!;

afterEach(() => {
  vi.useRealTimers();
});

describe('which plan follows the Tasks (#561)', () => {
  it('follows a decided plan the session shows, by the Schema role of its state', () => {
    expect(budgetFollowsTasks(schema, tripoPlan('active', '0', '110'))).toBe(true);
    expect(budgetFollowsTasks(schema, tripoPlan('exhausted', '110', '0'))).toBe(true);
    expect(budgetFollowsTasks(schema, tripoPlan('completed', '110', '0'))).toBe(true);
  });

  it('does not follow a plan that waits for a decision, a refused one or none', () => {
    expect(budgetFollowsTasks(schema, tripoPlan('pending', '0', '110'))).toBe(false);
    expect(budgetFollowsTasks(schema, tripoPlan('denied', '0', '110'))).toBe(false);
    expect(budgetFollowsTasks(schema, null)).toBe(false);
  });
});

describe('reading the budget after the Tasks ended (#561)', () => {
  it('reads at each delay and writes back the settled Tripo plan', async () => {
    vi.useFakeTimers();
    const settled = tripoPlan('exhausted', '110', '0');
    const getSession = vi.fn()
      .mockResolvedValueOnce({ budget: tripoPlan('active', '0', '110') })
      .mockResolvedValue({ budget: settled });
    const apply = vi.fn();
    followSettledBudget({ getSession }, 's1', { delays: [0, 2_500], apply });

    await vi.advanceTimersByTimeAsync(0);
    expect(getSession).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2_500);
    expect(getSession).toHaveBeenCalledTimes(2);
    expect(apply).toHaveBeenLastCalledWith({ budget: settled });
    const unit = apply.mock.calls.at(-1)![0].budget.units[0];
    expect(unit).toMatchObject({ unit: 'tripo.credit', spent: '110', remaining: '0' });
  });

  it('stops reading and writing once cancelled, and leaves a failed read alone', async () => {
    vi.useFakeTimers();
    const getSession = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ budget: tripoPlan('completed', '110', '0') });
    const apply = vi.fn();
    const stop = followSettledBudget({ getSession }, 's1', { delays: [0, 2_500, 5_000], apply });

    await vi.advanceTimersByTimeAsync(0);
    // A failed background read neither retries at once nor writes anything.
    expect(getSession).toHaveBeenCalledTimes(1);
    expect(apply).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2_500);
    expect(apply).toHaveBeenCalledTimes(1);
    stop();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(getSession).toHaveBeenCalledTimes(2);
    expect(apply).toHaveBeenCalledTimes(1);
  });
});
