// @vitest-environment jsdom
import React from 'react';
import { cleanup, render, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PlanCard } from '@/components/studio/plan-card';
import type { ProductionPlan, SessionBudget } from '@/lib/chat/budget';
import type { PlanTask } from '@/lib/chat/plan-progress';
import { formatSchemaText, schemaCountMessage, schemaText, taskStatePresentation, type ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;
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
    { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimatedCredits: 80 },
    { capability: 'retexture', offeringId: '', purpose: '材质', estimatedCredits: 20 },
  ],
  correctionCapabilities: ['remesh'],
};
const text = (key: string) => schemaText(schema, key);
const count = (key: string, value: number, values: Record<string, number>) => schemaCountMessage(schema, key, value, values);
const capability = (id: string) => schema.capabilities.find((item) => item.id === id)!.label_cn!;

afterEach(cleanup);

describe('a production plan waiting for approval (#103)', () => {
  it('lists every step with its capability, model and quote, the corrections, and the total budget', () => {
    const view = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]} />);
    const card = view.getByTestId('studio-plan-card');
    expect(card.getAttribute('data-state')).toBe('pending');
    expect(card.textContent).toContain('木椅');
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      count('ui.studio.plan.budget', 120, { credits: 120 }),
    );
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps).toHaveLength(2);
    expect(steps[0].getAttribute('data-capability')).toBe('text-to-3d');
    expect(steps[0].getAttribute('data-state')).toBe('pending');
    expect(steps[0].textContent).toContain(capability('text-to-3d'));
    expect(steps[0].textContent).toContain('Tripo v3');
    expect(steps[0].textContent).toContain(count('ui.studio.plan.estimated', 80, { credits: 80 }));
    expect(steps[0].textContent).toContain(text('ui.studio.plan.step_pending'));
    expect(steps[1].textContent).toContain(text('ui.studio.plan.model_unspecified'));
    expect(steps[1].textContent).toContain(count('ui.studio.plan.estimated', 20, { credits: 20 }));
    const corrections = view.getByTestId('studio-plan-corrections');
    expect(corrections.textContent).toContain(text('ui.studio.plan.corrections'));
    expect(corrections.textContent).toContain(capability('remesh'));
    // A decided plan's state badge is absent while the plan waits.
    expect(view.queryByTestId('studio-plan-state')).toBeNull();
    expect(view.queryByTestId('studio-plan-progress')).toBeNull();
  });

  it('words the plan estimate when the server sends no separate budget, and says when no correction is allowed', () => {
    const view = render(<PlanCard plan={{ ...plan, correctionCapabilities: [] }} budgetCredits={0} tasks={[]} />);
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      count('ui.studio.plan.budget', 100, { credits: 100 }),
    );
    expect(view.getByTestId('studio-plan-corrections').textContent).toContain(text('ui.studio.plan.corrections_none'));
  });

  it('approves and declines through the card, and disables both while a decision is on its way', () => {
    const decide = vi.fn();
    const view = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]} onDecide={decide} />);
    view.getByTestId('studio-plan-approve').click();
    expect(decide).toHaveBeenLastCalledWith(true);
    view.getByTestId('studio-plan-deny').click();
    expect(decide).toHaveBeenLastCalledWith(false);
    expect(view.getByTestId('studio-plan-approve').textContent).toBe(text('ui.studio.plan.approve'));
    expect(view.getByTestId('studio-plan-deny').textContent).toBe(text('ui.studio.plan.deny'));
    cleanup();

    const busy = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]} onDecide={decide} busy />);
    expect(busy.getByTestId('studio-plan-approve').hasAttribute('disabled')).toBe(true);
    expect(busy.getByTestId('studio-plan-deny').hasAttribute('disabled')).toBe(true);
    expect(busy.getByTestId('studio-plan-approve').getAttribute('aria-busy')).toBe('true');
  });

  it('shows why the server refused a decision', () => {
    const view = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]} onDecide={vi.fn()} error="余额不足" />);
    expect(within(view.getByTestId('studio-plan-card')).getByRole('alert').textContent).toBe('余额不足');
  });
});

describe('an approved production plan (#103)', () => {
  const progress: SessionBudget = {
    id: 'budget-1', state: 'active', limitCredits: 300, spentCredits: 120, reservedCredits: 10, remainingCredits: 170,
    plan, createdAt: '2026-10-01T00:00:00Z', decidedAt: '2026-10-01T00:01:00Z',
  };
  const tasks: PlanTask[] = [
    { taskId: 't1', capability: 'text-to-3d', state: 'SUCCEEDED', offeringId: 'tripo-v3' },
    { taskId: 't2', capability: 'retexture', state: 'RUNNING' },
  ];

  it('reads the live budget and each step state from the Task projection', () => {
    const view = render(<PlanCard plan={plan} budgetCredits={120} tasks={tasks} progress={progress} />);
    const card = view.getByTestId('studio-plan-card');
    expect(card.getAttribute('data-state')).toBe('active');
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.active'));
    expect(view.getByTestId('studio-plan-budget').textContent).toBe(
      count('ui.studio.plan.progress', 120, { spent: 120, limit: 300 }),
    );
    const summary = view.getByTestId('studio-plan-progress').textContent ?? '';
    expect(summary).toContain(count('ui.studio.plan.remaining', 170, { credits: 170 }));
    expect(summary).toContain(count('ui.studio.plan.reserved', 10, { credits: 10 }));
    expect(summary).toContain(formatSchemaText(text('ui.studio.plan.steps_done'), { done: 1, total: 2 }));
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps[0].getAttribute('data-state')).toBe('SUCCEEDED');
    expect(steps[0].textContent).toContain(taskStatePresentation(schema, 'SUCCEEDED').label);
    expect(steps[1].getAttribute('data-state')).toBe('RUNNING');
    expect(steps[1].textContent).toContain(taskStatePresentation(schema, 'RUNNING').label);
    // An approved plan offers no decision buttons.
    expect(view.queryByTestId('studio-plan-approve')).toBeNull();
  });

  it('hides the reserved line when nothing is reserved and words every other budget state', () => {
    const view = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]}
      progress={{ ...progress, reservedCredits: 0, state: 'exhausted', remainingCredits: 0 }} />);
    expect(view.getByTestId('studio-plan-progress').textContent).not.toContain(text('ui.studio.plan.reserved').replace('{credits}', '0'));
    expect(view.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.exhausted'));
    cleanup();
    const denied = render(<PlanCard plan={plan} budgetCredits={120} tasks={[]} progress={{ ...progress, state: 'denied' }} />);
    expect(denied.getByTestId('studio-plan-state').textContent).toBe(text('ui.studio.plan.state.denied'));
  });
});
