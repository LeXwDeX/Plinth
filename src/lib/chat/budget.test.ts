import { describe, expect, it } from 'vitest';

import {
  budgetAwaitsDecision, budgetIsShown, budgetIsSpendable, budgetStateRole, budgetStates, budgetStateWithRole,
  parseProductionPlan, parseProductionPlanStep, parseSessionBudget, planEstimatedTotal,
} from '@/lib/chat/budget';
import type { ProductSchema } from '@/lib/product-schema';

type StateDeclaration = { id: string; role: string };

function schemaWith(states: StateDeclaration[] | undefined): ProductSchema {
  return { chat: { approval_policy: states === undefined ? {} : { budget_states: states } } } as unknown as ProductSchema;
}

/** The shipped declaration: state ids with the roles the browser acts on. */
const shippedStates: StateDeclaration[] = [
  { id: 'pending', role: 'pending' },
  { id: 'active', role: 'active' },
  { id: 'exhausted', role: 'ended' },
  { id: 'closed', role: 'ended' },
  { id: 'denied', role: 'denied' },
];

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
  const schema = schemaWith(shippedStates);

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

  it('reads the declared budget states and roles from the Schema and falls back to none', () => {
    expect(budgetStates(schemaWith([{ id: 'pending', role: 'pending' }, { id: 'active', role: 'active' }])))
      .toEqual([{ id: 'pending', role: 'pending' }, { id: 'active', role: 'active' }]);
    expect(budgetStates(schemaWith(undefined))).toEqual([]);
    // A declaration without a known role says nothing about its state.
    expect(budgetStates(schemaWith([{ id: 'paused', role: 'sleeping' }, { id: '', role: 'active' }]))).toEqual([]);
    expect(budgetStateRole(schema, 'exhausted')).toBe('ended');
    expect(budgetStateRole(schema, 'refunded')).toBeNull();
    expect(budgetStateWithRole(schema, 'pending')).toBe('pending');
    expect(budgetStateWithRole(schemaWith(undefined), 'pending')).toBe('');
  });

  it('is spendable only while its state has the active role and credits are left', () => {
    const budget = parseSessionBudget(schema, { id: 'b1', state: 'active', remaining_credits: 1, plan })!;
    expect(budgetIsSpendable(schema, budget)).toBe(true);
    expect(budgetIsSpendable(schema, { ...budget, remainingCredits: 0 })).toBe(false);
    expect(budgetIsSpendable(schema, { ...budget, state: 'exhausted' })).toBe(false);
    expect(budgetIsSpendable(schema, { ...budget, state: 'pending' })).toBe(false);
    expect(budgetIsSpendable(schema, null)).toBe(false);
    expect(budgetIsSpendable(schema, undefined)).toBe(false);
  });

  // #120: the browser acts on the Schema role, so a renamed or new state id needs no client change.
  it('follows the Schema role of a state, not its id', () => {
    const renamed = schemaWith([
      { id: 'awaiting', role: 'pending' }, { id: 'running', role: 'active' },
      { id: 'spent', role: 'ended' }, { id: 'refused', role: 'denied' },
    ]);
    const budget = parseSessionBudget(renamed, { id: 'b1', state: 'running', remaining_credits: 5, plan })!;
    expect(budgetIsSpendable(renamed, budget)).toBe(true);
    expect(budgetIsShown(renamed, budget)).toBe(true);
    expect(budgetIsShown(renamed, { ...budget, state: 'refused' })).toBe(false);
    expect(budgetIsShown(renamed, { ...budget, state: 'denied' })).toBe(true);
    expect(budgetAwaitsDecision(renamed, { ...budget, state: 'awaiting' })).toBe(true);
    expect(budgetAwaitsDecision(renamed, { ...budget, state: 'pending' })).toBe(false);
    expect(budgetIsShown(renamed, { ...budget, state: 'spent' })).toBe(true);
    // A missing state falls back to the Schema's waiting state.
    expect(parseSessionBudget(renamed, { id: 'b1', plan })?.state).toBe('awaiting');
    // A state the Schema does not declare is shown, never hidden or spent.
    expect(budgetIsShown(renamed, { ...budget, state: 'refunded' })).toBe(true);
    expect(budgetIsSpendable(renamed, { ...budget, state: 'refunded' })).toBe(false);
    expect(budgetIsShown(renamed, null)).toBe(false);
  });
});
