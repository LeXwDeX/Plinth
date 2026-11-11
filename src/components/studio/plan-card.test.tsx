// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PlanCard, PlanReloadNotice } from '@/components/studio/plan-card';
import type { ProductionPlan, SessionBudget } from '@/lib/chat/budget';
import { parseProductionPlan } from '@/lib/chat/budget';
import type { PlanTask } from '@/lib/chat/plan-progress';
import { formatSchemaText, schemaText, taskStatePresentation, type ProductSchema } from '@/lib/product-schema';

function tripo(amount: string) {
  return { unit: 'tripo.credit', unit_label: 'Tripo credits', amount };
}

function legacy(amount: string) {
  return { unit: 'legacy.credits', unit_label: 'legacy.credits', amount };
}

const raw = defaultCatalog.application;
// The public projection names each plan state with its role (#120); the raw Catalog keeps them under budget.states.
const schema = {
  ...raw,
  chat: { ...raw.chat, approval_policy: { ...raw.chat.approval_policy,
    budget_states: raw.chat.approval_policy.budget.states.map(({ id, role }) => ({ id, role })) } },
} as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('swr', () => ({
  default: () => ({ data: { 'text-to-3d': [{ id: 'tripo-v3', model_name: 'Tripo v3' }] } }),
}));

const plan: ProductionPlan = {
  title: '木椅',
  steps: [
    { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimate: tripo('80'), purposeBudgetNote: '', check: '' },
    { capability: 'retexture', offeringId: '', purpose: '材质', estimate: legacy('20'), purposeBudgetNote: '', check: '' },
  ],
  correctionCapabilities: ['remesh'],
};
const text = (key: string) => schemaText(schema, key);
const words = (key: string, values: Record<string, string>) => formatSchemaText(text(key), values);
// #120: amounts keep their own unit; the legacy bucket reads as the historical-estimate label.
const legacyLabel = text('ui.pricing.legacy_estimate');
const budgets = [tripo('120'), legacy('40')];
const capability = (id: string) => schema.capabilities.find((item) => item.id === id)!.label_cn!;

afterEach(cleanup);

describe('a production plan waiting for approval (#103)', () => {
  it('lists every step with its capability, model and quote, the corrections, and the total budget', () => {
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} />);
    const card = view.getByTestId('studio-plan-card');
    expect(card.getAttribute('data-state')).toBe('pending');
    expect(card.textContent).toContain('木椅');
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      words('ui.studio.plan.budget', { amounts: `120 Tripo credits · 40 ${legacyLabel}` }),
    );
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps).toHaveLength(2);
    expect(steps[0].getAttribute('data-capability')).toBe('text-to-3d');
    expect(steps[0].getAttribute('data-state')).toBe('pending');
    expect(steps[0].textContent).toContain(capability('text-to-3d'));
    expect(steps[0].textContent).toContain('Tripo v3');
    expect(steps[0].textContent).toContain(words('ui.studio.plan.estimated', { amount: '80 Tripo credits' }));
    expect(steps[0].textContent).toContain(text('ui.studio.plan.step_pending'));
    expect(steps[1].textContent).toContain(text('ui.studio.plan.model_unspecified'));
    expect(steps[1].textContent).toContain(words('ui.studio.plan.estimated', { amount: `20 ${legacyLabel}` }));
    const corrections = view.getByTestId('studio-plan-corrections');
    expect(corrections.textContent).toContain(text('ui.studio.plan.corrections'));
    expect(corrections.textContent).toContain(capability('remesh'));
    // A decided plan's state badge is absent while the plan waits.
    expect(view.queryByTestId('studio-plan-state')).toBeNull();
    expect(view.queryByTestId('studio-plan-progress')).toBeNull();
  });

  it('says the budget is unknown when the server sends none, never a sum of the steps, and says when no correction is allowed', () => {
    const view = render(<PlanCard plan={{ ...plan, correctionCapabilities: [] }} tasks={[]} />);
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      words('ui.studio.plan.budget', { amounts: text('ui.pricing.unknown') }),
    );
    expect(view.getByTestId('studio-plan-corrections').textContent).toContain(text('ui.studio.plan.corrections_none'));
  });

  // #129: a step whose values would be over the session purpose's budget says so before the plan is approved.
  it('shows the purpose budget note of a step and nothing for a step that fits', () => {
    const noted = { ...plan, steps: [{ ...plan.steps[0], purposeBudgetNote: '面数上限 1000 超过用途预算 500' }, plan.steps[1]] };
    const view = render(<PlanCard plan={noted} budgets={budgets} tasks={[]} />);
    const notes = view.getAllByTestId('studio-plan-step-budget-note');
    expect(notes).toHaveLength(1);
    expect(notes[0].textContent).toBe('面数上限 1000 超过用途预算 500');
    expect(within(view.getAllByTestId('studio-plan-step')[0]).getByTestId('studio-plan-step-budget-note')).toBeTruthy();
  });

  // #246: a step may declare a Schema checkpoint; the plan card repeats it.
  it('shows the declared checkpoint of a step', () => {
    const checked = { ...plan, steps: [{ ...plan.steps[0], check: 'self_check' }, plan.steps[1]] };
    const view = render(<PlanCard plan={checked} budgets={budgets} tasks={[]} />);
    const checks = view.getAllByTestId('studio-plan-step-check');
    expect(checks).toHaveLength(1);
    // The declared label, never the internal checkpoint id.
    const label = schema.chat.self_check?.checkpoints?.find((checkpoint) => checkpoint.id === 'self_check')?.label;
    expect(label).toBeTruthy();
    expect(checks[0].textContent).toBe(words('ui.studio.plan.checkpoint', { check: label! }));
    expect(within(view.getAllByTestId('studio-plan-step')[0]).getByTestId('studio-plan-step-check')).toBeTruthy();
  });

  it('approves and declines through the card, and disables both while a decision is on its way', () => {
    const decide = vi.fn();
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} onDecide={decide} />);
    view.getByTestId('studio-plan-approve').click();
    expect(decide).toHaveBeenLastCalledWith(true);
    view.getByTestId('studio-plan-deny').click();
    expect(decide).toHaveBeenLastCalledWith(false);
    expect(view.getByTestId('studio-plan-approve').textContent).toBe(text('ui.studio.plan.approve'));
    expect(view.getByTestId('studio-plan-deny').textContent).toBe(text('ui.studio.plan.deny'));
    cleanup();

    const busy = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} onDecide={decide} busy />);
    expect(busy.getByTestId('studio-plan-approve').hasAttribute('disabled')).toBe(true);
    expect(busy.getByTestId('studio-plan-deny').hasAttribute('disabled')).toBe(true);
    expect(busy.getByTestId('studio-plan-approve').getAttribute('aria-busy')).toBe('true');
  });

  it('shows why the server refused a decision', () => {
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} onDecide={vi.fn()} error="余额不足" />);
    expect(within(view.getByTestId('studio-plan-card')).getByRole('alert').textContent).toBe('余额不足');
  });
});

describe('a plan that needs reloading (#103)', () => {
  it('shows the failure and reloads on request', () => {
    const key = 'ui.studio.plan.reload';
    const had = Object.hasOwn(messages, key);
    const previous = messages[key];
    messages[key] = '重新加载';
    try {
      const reload = vi.fn();
      const view = render(<PlanReloadNotice message="计划状态读取失败" onReload={reload} />);
      expect(view.getByTestId('studio-plan-reload').getAttribute('role')).toBe('alert');
      expect(view.getByTestId('studio-plan-reload').textContent).toContain('计划状态读取失败');
      const button = view.getByTestId('studio-plan-reload-button');
      expect(button.textContent).toBe('重新加载');
      button.click();
      expect(reload).toHaveBeenCalledTimes(1);
    } finally {
      if (had) messages[key] = previous;
      else delete messages[key];
    }
  });

  it('disables the reload while one is in flight', () => {
    const view = render(<PlanReloadNotice message="无法读取计划状态" busy onReload={() => {}} />);
    expect(view.getByTestId('studio-plan-reload-button').hasAttribute('disabled')).toBe(true);
  });
});

describe('an approved production plan (#103)', () => {
  const progress: SessionBudget = {
    id: 'budget-1', state: 'active',
    units: [
      { unit: 'tripo.credit', unitLabel: 'Tripo credits', limit: '300', spent: '120', reserved: '10', remaining: '170' },
      { unit: 'legacy.credits', unitLabel: 'legacy.credits', limit: '40', spent: '0', reserved: '0', remaining: '40' },
    ],
    plan, createdAt: '2026-10-01T00:00:00Z', decidedAt: '2026-10-01T00:01:00Z',
  };
  const tasks: PlanTask[] = [
    { taskId: 't1', capability: 'text-to-3d', state: 'SUCCEEDED', offeringId: 'tripo-v3', startedAt: Date.parse('2026-10-01T00:02:00Z') },
    { taskId: 't2', capability: 'retexture', state: 'RUNNING', startedAt: Date.parse('2026-10-01T00:03:00Z') },
  ];

  it('reads the live budget and each step state from the Task projection', () => {
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={tasks} progress={progress} />);
    const card = view.getByTestId('studio-plan-card');
    expect(card.getAttribute('data-state')).toBe('active');
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.active'));
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      words('ui.studio.plan.progress', { amounts: `120 / 300 Tripo credits · 0 / 40 ${legacyLabel}` }),
    );
    const summary = view.getByTestId('studio-plan-progress').textContent ?? '';
    expect(summary).toContain(words('ui.studio.plan.remaining', { amounts: `170 Tripo credits · 40 ${legacyLabel}` }));
    // Only a unit that holds a reservation is listed.
    expect(summary).toContain(words('ui.studio.plan.reserved', { amounts: '10 Tripo credits' }));
    expect(summary).toContain(formatSchemaText(text('ui.studio.plan.steps_done'), { done: 1, total: 2 }));
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps[0].getAttribute('data-state')).toBe('SUCCEEDED');
    expect(steps[0].textContent).toContain(taskStatePresentation(schema, 'SUCCEEDED').label);
    expect(steps[1].getAttribute('data-state')).toBe('RUNNING');
    expect(steps[1].textContent).toContain(taskStatePresentation(schema, 'RUNNING').label);
    // An approved plan offers no decision buttons.
    expect(view.queryByTestId('studio-plan-approve')).toBeNull();
  });

  it('leaves a step pending when the only task of its capability started before approval', () => {
    const historical: PlanTask = {
      taskId: 'old', capability: 'text-to-3d', state: 'SUCCEEDED', offeringId: 'tripo-v3',
      startedAt: Date.parse('2026-10-01T00:00:30Z'),
    };
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[historical]} progress={progress} />);
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps[0].getAttribute('data-state')).toBe('pending');
    expect(steps[0].textContent).toContain(text('ui.studio.plan.step_pending'));
    expect(view.getByTestId('studio-plan-progress').textContent).toContain(
      formatSchemaText(text('ui.studio.plan.steps_done'), { done: 0, total: 2 }),
    );
  });

  it('shows a budget state the Schema does not name as unknown, never as pending', () => {
    const key = 'ui.studio.plan.state.unknown';
    const had = Object.hasOwn(messages, key);
    const previous = messages[key];
    messages[key] = '未知状态';
    try {
      const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} progress={{ ...progress, state: 'refunded' }} />);
      expect(view.getByTestId('studio-plan-state').textContent).toBe('未知状态');
      expect(view.getByTestId('studio-plan-state').textContent).not.toBe(text('ui.studio.plan.state.pending'));
    } finally {
      if (had) messages[key] = previous;
      else delete messages[key];
    }
  });

  it('hides the reserved line when nothing is reserved and words every other budget state', () => {
    const usedUp = progress.units.map((unit) => ({ ...unit, spent: unit.limit, reserved: '0', remaining: '0' }));
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]}
      progress={{ ...progress, units: usedUp, state: 'exhausted' }} />);
    expect(view.getByTestId('studio-plan-progress').textContent).not.toContain(text('ui.studio.plan.reserved').split('{amounts}')[0]);
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.exhausted'));
    cleanup();
    const denied = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} progress={{ ...progress, state: 'denied' }} />);
    expect(denied.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.denied'));
  });
});

// #251: acceptance criteria, checkpoints and contingencies are part of the
// approved contract; the card shows them and never makes the usual route look
// as expensive as the worst case.
describe('a production plan with acceptance and contingencies (#251)', () => {
  it('shows the acceptance criteria, the checkpoints and the separately estimated contingencies', () => {
    // Read the card from the server's wire shape: a contingency nests its
    // repair step, and the split totals live next to (not inside) the plan.
    const contracted = parseProductionPlan({
      title: '木椅',
      steps: plan.steps.map((step) => ({ capability: step.capability, offering_id: step.offeringId, purpose: step.purpose, estimate: step.estimate })),
      correction_capabilities: plan.correctionCapabilities,
      acceptance: [
        { id: 'purpose', statement: '用途「游戏/实时·角色」的交付标准：', check: 'game_character' },
        { id: 'triangles', statement: '三角面数在 5000–60000 之间', check: 'triangles' },
      ],
      checkpoints: [{ after_capability: 'remesh', checks: [{ check: 'triangles', maximum: 60000 }, { check: 'pivot_bottom_center', maximum_ratio: 0.01 }] }],
      contingencies: [{
        id: 'low_poly', trigger: ['triangles'],
        step: { capability: 'remesh', offering_id: 'meshy/processing/remesh', purpose: '再减面', estimate: legacy('5') },
      }],
    })!;
    const view = render(<PlanCard plan={contracted} budgets={budgets} stepTotals={[tripo('80'), legacy('20')]}
      contingencyTotals={[legacy('5')]} tasks={[]} />);
    const acceptance = view.getByTestId('studio-plan-acceptance');
    expect(acceptance.textContent).toContain(text('ui.studio.plan.acceptance'));
    expect(acceptance.textContent).toContain('三角面数在 5000–60000 之间');
    const checkpoints = view.getByTestId('studio-plan-checkpoints');
    expect(checkpoints.textContent).toContain(words('ui.studio.plan.checkpoint_item', {
      capability: capability('remesh'),
      checks: `${text('ui.chat.review.check_names.triangles')}、${text('ui.chat.review.check_names.pivot_bottom_center')}`,
    }));
    const contingencies = view.getByTestId('studio-plan-contingencies');
    // The split figures are the server's own totals, not a client-side sum.
    const estimates = view.getByTestId('studio-plan-estimates');
    expect(estimates.textContent).toContain(words('ui.studio.plan.step_total', { amounts: `80 Tripo credits · 20 ${legacyLabel}` }));
    expect(estimates.textContent).toContain(words('ui.studio.plan.contingency_total', { amounts: `5 ${legacyLabel}` }));
    expect(contingencies.textContent).toContain(words('ui.studio.plan.contingencies', { amounts: `5 ${legacyLabel}` }));
    expect(contingencies.textContent).toContain(words('ui.studio.plan.contingency_item', {
      trigger: text('ui.chat.review.check_names.triangles'),
      capability: capability('remesh'),
      amount: `5 ${legacyLabel}`,
    }));
  });

  it('renders a plan from an older server without the new sections', () => {
    const view = render(<PlanCard plan={plan} budgets={budgets} tasks={[]} />);
    expect(view.queryByTestId('studio-plan-acceptance')).toBeNull();
    expect(view.queryByTestId('studio-plan-checkpoints')).toBeNull();
    expect(view.queryByTestId('studio-plan-contingencies')).toBeNull();
  });
});
