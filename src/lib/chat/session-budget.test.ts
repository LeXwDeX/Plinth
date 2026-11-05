import { describe, expect, it, vi } from 'vitest';

import type { SessionBudget } from '@/lib/chat/budget';
import { reloadSessionBudget } from '@/lib/chat/session-budget';

const plan = { title: 'p', steps: [{ capability: 'a', offeringId: '', purpose: '', estimate: null, purposeBudgetNote: '' }], correctionCapabilities: [] };
const budget: SessionBudget = {
  id: 'b1', state: 'active',
  units: [{ unit: 'legacy.credits', unitLabel: 'legacy.credits', limit: '10', spent: '2', reserved: '0', remaining: '8' }],
  plan, createdAt: '', decidedAt: '2026-10-01T00:00:00Z',
};

describe('reloading the session budget after a decision (#103)', () => {
  it('returns the budget and brief from the session read', async () => {
    const getSession = vi.fn(async () => ({ budget, brief: { style: '低模' } }));
    await expect(reloadSessionBudget({ getSession }, 's1')).resolves.toEqual({ budget, brief: { style: '低模' } });
    expect(getSession).toHaveBeenCalledTimes(1);
  });

  it('retries once after a failed read instead of keeping a stale budget', async () => {
    const getSession = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ budget });
    await expect(reloadSessionBudget({ getSession }, 's1')).resolves.toEqual({ budget });
    expect(getSession).toHaveBeenCalledTimes(2);
  });

  it('rejects when both reads fail, so the caller can show the reload notice', async () => {
    const getSession = vi.fn().mockRejectedValue(new Error('offline'));
    await expect(reloadSessionBudget({ getSession }, 's1')).rejects.toThrow('offline');
    expect(getSession).toHaveBeenCalledTimes(2);
  });

  it('returns a null budget when the session has none', async () => {
    const getSession = vi.fn(async () => ({}));
    await expect(reloadSessionBudget({ getSession }, 's1')).resolves.toEqual({ budget: null });
  });
});
