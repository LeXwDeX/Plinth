import { describe, expect, it } from 'vitest';

import {
  budgetAwaitsDecision, budgetIsShown, budgetIsSpendable, budgetStateRole, budgetStates, budgetStateWithRole,
  budgetUnitTotals, budgetUnitsWith, budgetUsageText, parseBudgetUnits,
  parseProductionPlan, parseProductionPlanStep, parseSessionBudget, stepEstimate,
} from '@/lib/chat/budget';
import type { ProductSchema } from '@/lib/product-schema';

type StateDeclaration = { id: string; role: string };

function schemaWith(states: StateDeclaration[] | undefined): ProductSchema {
  return { chat: { approval_policy: states === undefined ? {} : { budget_states: states } } } as unknown as ProductSchema;
}

const tripo = (amount: string) => ({ unit: 'tripo.credit', unit_label: 'Tripo credits', amount });
const legacy = (amount: string) => ({ unit: 'legacy.credits', unit_label: 'legacy.credits', amount });

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
        { capability: 'text-to-3d', offering_id: 'tripo-v3', purpose: '主体', estimate: tripo('80'), purpose_budget_note: '面数超出用途预算' },
        { capability: 'retexture', purpose: '材质', estimate: legacy('20') },
      ],
      correction_capabilities: ['remesh', 'retexture'],
    });
    expect(plan).toEqual({
      title: '木椅',
      steps: [
        { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimate: tripo('80'), purposeBudgetNote: '面数超出用途预算' },
        { capability: 'retexture', offeringId: '', purpose: '材质', estimate: legacy('20'), purposeBudgetNote: '' },
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

  // #120: a step quote keeps its own unit; a malformed or zero quote shows nothing.
  it('keeps each step quote in its own unit and never sums them', () => {
    const plan = parseProductionPlan({ title: 'p', steps: [
      { capability: 'a', estimate: tripo('80') },
      { capability: 'b', estimate: { unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '0.5' } },
      { capability: 'c', estimate: legacy('2.5') },
      { capability: 'd', estimate: legacy('0') },
      { capability: 'e', estimated_credits: 20 },
    ] })!;
    expect(plan.steps.map(stepEstimate)).toEqual([
      tripo('80'), { unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '0.5' }, null, null, null,
    ]);
  });
});

describe('the per-unit budget (#120)', () => {
  const schema = { content: { messages: {
    'ui.pricing.legacy_estimate': '历史积分估算', 'ui.pricing.unknown': '报价未知', 'ui.studio.plan.unit_usage': '{spent} / {limit} {unit}',
  } } } as unknown as ProductSchema;
  const units = parseBudgetUnits([
    { unit: 'tripo.credit', unit_label: 'Tripo credits', limit: '1200', spent: '120', reserved: '0', remaining: '1080' },
    { unit: 'legacy.credits', unit_label: 'legacy.credits', limit: '40', spent: '0', reserved: '5', remaining: '35' },
    { unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', limit: 3, spent: '0', reserved: '0', remaining: '3' },
    { unit: 'tripo.credit', unit_label: 'Tripo credits', limit: '1', spent: '0', reserved: '0', remaining: '1' },
  ]);

  it('reads every well-formed unit once and leaves out the malformed ones', () => {
    expect(units).toEqual([
      { unit: 'tripo.credit', unitLabel: 'Tripo credits', limit: '1200', spent: '120', reserved: '0', remaining: '1080' },
      { unit: 'legacy.credits', unitLabel: 'legacy.credits', limit: '40', spent: '0', reserved: '5', remaining: '35' },
    ]);
    expect(parseBudgetUnits(null)).toEqual([]);
  });

  it('lists the figures per unit, side by side and never summed', () => {
    expect(budgetUsageText(schema, units)).toBe('120 / 1,200 Tripo credits · 0 / 40 历史积分估算');
    expect(budgetUsageText(schema, [])).toBe('报价未知');
    expect(budgetUnitTotals(units, 'remaining')).toEqual([
      { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '1080' },
      { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '35' },
    ]);
    expect(budgetUnitsWith(units, 'reserved').map((unit) => unit.unit)).toEqual(['legacy.credits']);
  });
});

describe('the session budget', () => {
  const plan = { title: 'p', steps: [{ capability: 'a', estimate: legacy('10') }] };
  const schema = schemaWith(shippedStates);
  const unit = (remaining: string) => ({ unit: 'legacy.credits', unit_label: 'legacy.credits', limit: '300', spent: '120', reserved: '0', remaining });

  it('reads the public fields with one entry per credit unit', () => {
    expect(parseSessionBudget(schema, {
      id: 'b1', state: 'active', units: [unit('180'), { unit: 'tripo.credit', unit_label: 'Tripo credits', limit: 'x' }],
      plan, created_at: '2026-10-01T00:00:00Z', decided_at: '2026-10-01T00:01:00Z',
    })).toMatchObject({
      id: 'b1', state: 'active',
      units: [{ unit: 'legacy.credits', unitLabel: 'legacy.credits', limit: '300', spent: '120', reserved: '0', remaining: '180' }],
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
    const budget = parseSessionBudget(schema, { id: 'b1', state: 'active', units: [unit('1')], plan })!;
    expect(budgetIsSpendable(schema, budget)).toBe(true);
    expect(budgetIsSpendable(schema, parseSessionBudget(schema, { id: 'b1', state: 'active', units: [unit('0')], plan }))).toBe(false);
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
    const budget = parseSessionBudget(renamed, { id: 'b1', state: 'running', units: [unit('5')], plan })!;
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
