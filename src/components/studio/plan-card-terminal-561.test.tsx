// @vitest-environment jsdom
import React from 'react';
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PlanCard } from '@/components/studio/plan-card';
import { parseSessionBudget } from '@/lib/chat/budget';
import { schemaText, type ProductSchema } from '@/lib/product-schema';

// #561: a one-step plan priced in Tripo credits whose step charged the whole
// limit. Given the server's settled projection the card shows the plan's own
// end state and the charged amount in that unit, never "in progress" with
// nothing spent.

const raw = defaultCatalog.application;
const schema = {
  ...raw,
  chat: { ...raw.chat, approval_policy: { ...raw.chat.approval_policy,
    budget_states: raw.chat.approval_policy.budget.states.map(({ id, role }) => ({ id, role })) } },
} as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('swr', () => ({ default: () => ({ data: {} }) }));

const text = (key: string) => schemaText(schema, key);

const projection = (state: string, units: unknown[]) => parseSessionBudget(schema, {
  id: 'budget-tripo', state, units,
  plan: { title: '四视图生成石雕', steps: [
    { capability: 'image-to-3d', offering_id: 'tripo-p2-multiview', purpose: '多视图低模',
      estimate: { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '110' } },
  ] },
  created_at: '2026-11-24T08:50:00Z', decided_at: '2026-11-24T08:51:00Z',
  steps: [{ index: 1, state: 'done', task_id: 'task-3d', task_state: 'SUCCEEDED', optional: false }],
})!;

const tripoSpent = [{ unit: 'tripo.credit', unit_label: 'Tripo credits', limit: '110', spent: '110', reserved: '0', remaining: '0' }];

afterEach(cleanup);

describe('a finished Tripo plan (#561)', () => {
  it.each(['exhausted', 'completed'])('reads its %s state and 110 / 110 Tripo credits spent', (state) => {
    const progress = projection(state, tripoSpent);
    const view = render(<PlanCard plan={progress.plan} tasks={[]} progress={progress} stepsMayStillRun={false} />);
    const badge = view.getByTestId('studio-plan-state').textContent;
    expect(badge).toBe(text(`ui.studio.plan.state.${state}`));
    expect(badge).not.toBe(text('ui.studio.plan.state.active'));
    expect(view.getByTestId('studio-plan-budget').textContent).toContain('110 / 110 Tripo credits');
    const progressLine = view.getByTestId('studio-plan-progress').textContent ?? '';
    expect(progressLine).toContain('0 Tripo credits');
    expect(progressLine).not.toContain('110 Tripo credits');
    expect(view.getAllByTestId('studio-plan-step')[0].getAttribute('data-state')).toBe('done');
    // A finished plan is neither stopped nor offered to continue.
    expect(view.queryByTestId('studio-plan-stopped')).toBeNull();
  });

  it('keeps a legacy-credit plan in its historical-estimate wording', () => {
    const progress = projection('completed', [
      { unit: 'legacy.credits', unit_label: 'legacy.credits', limit: '6', spent: '6', reserved: '0', remaining: '0' },
    ]);
    const view = render(<PlanCard plan={progress.plan} tasks={[]} progress={progress} stepsMayStillRun={false} />);
    expect(view.getByTestId('studio-plan-budget').textContent).toContain(`6 / 6 ${text('ui.pricing.legacy_estimate')}`);
  });
});
