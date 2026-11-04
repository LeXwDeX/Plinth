import { describe, expect, it } from 'vitest';

import { budgetIsSpendable, budgetStates, parseProductionPlan, parseProductionPlanStep, parseSessionBudget, planEstimatedTotal } from '@/lib/chat/budget';
import type { ProductSchema } from '@/lib/product-schema';

function schemaWith(states: string[] | undefined): ProductSchema {
  return { chat: { approval_policy: states === undefined ? {} : { budget_states: states } } } as unknown as ProductSchema;
}

describe('the production plan', () => {
  it('reads the title, each step and the correction capabilities', () => {
    const plan = parseProductionPlan({
      title: '木椅',
      steps: [
        { capability: 'text-to-3d', offering_id: 'tripo-v3', purpose: '主体', estimated_credits: 80 },
        { capability: 'retexture', purpose: '材质', estimated_credits: 20 },
      ],
      correction_capabilities: ['remesh', 'retexture'],
    });
    expect(plan).toEqual({
      title: '木椅',
      steps: [
        { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimatedCredits: 80 },
        { capability: 'retexture', offeringId: '', purpose: '材质', estimatedCredits: 20 },
      ],
      correctionCapabilities: ['remesh', 'retexture'],
    });
  });

  it('drops a step without a capability and reads a plan with a title only', () => {
    expect(parseProductionPlan({ title: '空计划', steps: [{ offering_id: 'x' }] })).toEqual({ title: '空计划', steps: [], correctionCapabilities: [] });
    expect(parseProductionPlanStep({ capability: '' })).toBeNull();
  });

  it('is absent when there is neither a title nor a valid step', () => {
    expect(parseProductionPlan({ steps: [{ offering_id: 'x' }] })).toBeNull();
    expect(parseProductionPlan(null)).toBeNull();
    expect(parseProductionPlan({ steps: [] })).toBeNull();
  });

  it('sums the step quotes', () => {
    const plan = parseProductionPlan({ title: 'p', steps: [
      { capability: 'a', estimated_credits: 80 },
      { capability: 'b', estimated_credits: 20 },
    ] })!;
    expect(planEstimatedTotal(plan)).toBe(100);
  });
});

describe('the session budget', () => {
  const plan = { title: 'p', steps: [{ capability: 'a', estimated_credits: 10 }] };
  const schema = schemaWith(['pending', 'active', 'exhausted', 'closed', 'denied']);

  it('reads the public fields and drops malformed numbers to zero', () => {
    expect(parseSessionBudget(schema, {
      id: 'b1', state: 'active', limit_credits: 300, spent_credits: 120, reserved_credits: 'x', remaining_credits: 180,
      plan, created_at: '2026-10-01T00:00:00Z', decided_at: '2026-10-01T00:01:00Z',
    })).toMatchObject({
      id: 'b1', state: 'active', limitCredits: 300, spentCredits: 120, reservedCredits: 0, remainingCredits: 180,
      createdAt: '2026-10-01T00:00:00Z', decidedAt: '2026-10-01T00:01:00Z',
    });
  });

  it('keeps a state this build does not know instead of folding it into pending', () => {
    expect(parseSessionBudget(schema, { id: 'b1', state: 'refunded', plan })?.state).toBe('refunded');
    expect(parseSessionBudget(schema, { id: 'b1', plan })?.state).toBe('pending');
    expect(parseSessionBudget(schema, { state: 'active', plan })).toBeNull();
    expect(parseSessionBudget(schema, { id: 'b1', plan: { steps: [] } })).toBeNull();
  });

  it('reads the declared budget states from the Schema and falls back to none', () => {
    expect(budgetStates(schemaWith(['pending', 'active']))).toEqual(['pending', 'active']);
    expect(budgetStates(schemaWith(undefined))).toEqual([]);
  });

  it('is spendable only while approved, unfinished and with credits left', () => {
    const budget = parseSessionBudget(schema, { id: 'b1', state: 'active', remaining_credits: 1, plan })!;
    expect(budgetIsSpendable(budget)).toBe(true);
    expect(budgetIsSpendable({ ...budget, remainingCredits: 0 })).toBe(false);
    expect(budgetIsSpendable({ ...budget, state: 'exhausted' })).toBe(false);
    expect(budgetIsSpendable({ ...budget, state: 'pending' })).toBe(false);
    expect(budgetIsSpendable(null)).toBe(false);
    expect(budgetIsSpendable(undefined)).toBe(false);
  });
});
