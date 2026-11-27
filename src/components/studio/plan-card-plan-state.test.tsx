// @vitest-environment jsdom
import React from 'react';
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PlanCard } from '@/components/studio/plan-card';
import { parseSessionBudget, type ProductionPlan, type SessionBudget } from '@/lib/chat/budget';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

// #410: the server pairs every plan step with the Task that ran it and the card
// only renders that result: each step reads done / running / failed / not run /
// optional not run, a plan whose turn ended with steps left reads "stopped, N
// steps left", and the card offers "continue" to run the plan again.

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
const offeringsRead = vi.hoisted(() => ({
  value: { data: { 'text-to-image': [{ id: 'jimeng-4', model_name: '即梦 4.5' }] } } as { data?: unknown; error?: unknown },
}));
vi.mock('swr', () => ({ default: () => offeringsRead.value }));

const text = (key: string) => schemaText(schema, key);
const estimate = { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '7' };
const plan: ProductionPlan = {
  title: '四视图加总图',
  steps: [
    { capability: 'text-to-image', offeringId: '', purpose: '正面', estimate, purposeBudgetNote: '' },
    { capability: 'text-to-image', offeringId: '', purpose: '左侧', estimate, purposeBudgetNote: '' },
    { capability: 'text-to-image', offeringId: '', purpose: '右侧（可选）', estimate, purposeBudgetNote: '', optional: true },
  ],
  correctionCapabilities: [],
};

const budget = (state: string, steps: SessionBudget['steps']): SessionBudget => ({
  id: 'budget-1', state,
  units: [{ unit: 'legacy.credits', unitLabel: 'legacy.credits', limit: '30', spent: '14', reserved: '0', remaining: '16' }],
  plan, createdAt: '2026-11-21T00:00:00Z', decidedAt: '2026-11-21T00:01:00Z',
  ...(steps ? { steps } : {}),
});

afterEach(cleanup);

describe('the steps of a plan, as the server projected them (#410)', () => {
  it('reads each step state from the Schema and ignores its own pairing', () => {
    const progress = budget('active', [
      { index: 1, state: 'done', taskId: 't1', taskState: 'SUCCEEDED', optional: false },
      { index: 2, state: 'running', taskId: 't2', taskState: 'RUNNING', optional: false },
      { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    const view = render(<PlanCard plan={plan} tasks={[]} progress={progress} stepsMayStillRun />);
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps.map((step) => step.getAttribute('data-state'))).toEqual(['done', 'running', 'optional_not_run']);
    // The label of the third step is the Schema's optional wording, not "not run".
    expect(text('ui.studio.plan.step_state.optional_not_run')).not.toBe(text('ui.studio.plan.step_state.not_run'));
    expect(steps[2].textContent).toContain(text('ui.studio.plan.step_state.optional_not_run'));
    // `stepsMayStillRun` no longer guesses: the server's status wins.
    expect(steps[1].textContent).toContain(text('ui.studio.plan.step_state.running'));
  });

  it('says a step failed in its own words', () => {
    const progress = budget('active', [
      { index: 1, state: 'failed', taskId: 't1', taskState: 'FAILED', optional: false },
      { index: 2, state: 'not_run', taskId: '', taskState: '', optional: false },
      { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    const view = render(<PlanCard plan={plan} tasks={[]} progress={progress} stepsMayStillRun={false} />);
    const steps = view.getAllByTestId('studio-plan-step');
    expect(steps[0].textContent).toContain(text('ui.studio.plan.step_state.failed'));
    expect(steps[1].textContent).toContain(text('ui.studio.plan.step_state.not_run'));
  });

  it('keeps the browser pairing when the server projected no steps', () => {
    const progress = budget('active', undefined);
    const view = render(<PlanCard plan={plan} tasks={[]} progress={progress} stepsMayStillRun={false} />);
    for (const step of view.getAllByTestId('studio-plan-step')) {
      expect(step.getAttribute('data-state')).toBe('not_run');
    }
  });

  it('offers to continue a stopped plan and counts only the required steps left', () => {
    const stopped = budget('active', [
      { index: 1, state: 'done', taskId: 't1', taskState: 'SUCCEEDED', optional: false },
      { index: 2, state: 'not_run', taskId: '', taskState: '', optional: false },
      { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    const onContinue = vi.fn();
    const view = render(
      <PlanCard plan={plan} tasks={[]} progress={stopped} stepsMayStillRun={false} onContinue={onContinue} />,
    );
    const banner = view.getByTestId('studio-plan-stopped');
    // Only the required step that never ran is counted: the optional view does
    // not keep the plan from being done.
    expect(banner.textContent).toContain(formatCount(1));
    view.getByTestId('studio-plan-continue').click();
    expect(onContinue).toHaveBeenCalledTimes(1);
  });

  // #670: English says "1 step left", through the Schema's singular text.
  it('words one step left in the singular', () => {
    const messages = raw.content.messages as Record<string, string>;
    const en = raw.localization.bundles.en.messages as Record<string, string>;
    const previous = { locale: schema.localization, plural: messages['ui.studio.plan.stopped'], one: messages['ui.studio.plan.stopped_one'] };
    messages['ui.studio.plan.stopped'] = en['ui.studio.plan.stopped'];
    messages['ui.studio.plan.stopped_one'] = en['ui.studio.plan.stopped_one'];
    schema.localization = { ...raw.localization, active_locale: 'en', default_locale: 'en' } as ProductSchema['localization'];
    try {
      const stopped = budget('active', [
        { index: 1, state: 'done', taskId: 't1', taskState: 'SUCCEEDED', optional: false },
        { index: 2, state: 'not_run', taskId: '', taskState: '', optional: false },
        { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
      ]);
      const view = render(<PlanCard plan={plan} tasks={[]} progress={stopped} stepsMayStillRun={false} />);
      expect(view.getByTestId('studio-plan-stopped').textContent).toContain('Stopped, 1 step left');
    } finally {
      schema.localization = previous.locale;
      messages['ui.studio.plan.stopped'] = previous.plural;
      messages['ui.studio.plan.stopped_one'] = previous.one;
    }
  });

  // #670: a chosen model reads as loading while the Offering names are read, never as its raw id.
  it('shows the loading text, not the offering id, while the model names are read', () => {
    const chosen: ProductionPlan = { ...plan, steps: [{ ...plan.steps[0], offeringId: 'jimeng-4' }] };
    const previous = offeringsRead.value;
    offeringsRead.value = {};
    try {
      const loading = render(<PlanCard plan={chosen} tasks={[]} />);
      expect(loading.getByTestId('studio-plan-card').textContent).toContain(text('ui.loading'));
      expect(loading.getByTestId('studio-plan-card').textContent).not.toContain('jimeng-4');
      cleanup();
      offeringsRead.value = previous;
      const loaded = render(<PlanCard plan={chosen} tasks={[]} />);
      expect(loaded.getByTestId('studio-plan-card').textContent).toContain('即梦 4.5');
    } finally {
      offeringsRead.value = previous;
    }
  });

  it('shows no continue button while the turn still runs or the plan is done', () => {
    const active = budget('active', [
      { index: 1, state: 'done', taskId: 't1', taskState: 'SUCCEEDED', optional: false },
      { index: 2, state: 'running', taskId: 't2', taskState: 'RUNNING', optional: false },
      { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    const running = render(<PlanCard plan={plan} tasks={[]} progress={active} stepsMayStillRun onContinue={vi.fn()} />);
    expect(running.queryByTestId('studio-plan-stopped')).toBeNull();
    cleanup();
    const completed = budget('completed', [
      { index: 1, state: 'done', taskId: 't1', taskState: 'SUCCEEDED', optional: false },
      { index: 2, state: 'done', taskId: 't2', taskState: 'SUCCEEDED', optional: false },
      { index: 3, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    const done = render(<PlanCard plan={plan} tasks={[]} progress={completed} stepsMayStillRun={false} onContinue={vi.fn()} />);
    expect(done.queryByTestId('studio-plan-stopped')).toBeNull();
    expect(done.getByTestId('studio-plan-state').textContent).toContain(text('ui.studio.plan.state.completed'));
  });

  it('reads the projected steps from a session payload', () => {
    const parsed = parseSessionBudget(schema, {
      id: 'budget-1', state: 'active',
      units: [{ unit: 'legacy.credits', unit_label: 'legacy.credits', limit: '30', spent: '14', reserved: '0', remaining: '16' }],
      plan: { title: '四视图加总图', steps: [
        { capability: 'text-to-image', purpose: '正面', optional: true },
      ] },
      created_at: '2026-11-21T00:00:00Z', decided_at: '2026-11-21T00:01:00Z',
      steps: [{ index: 1, state: 'optional_not_run', optional: true }],
    });
    expect(parsed?.steps).toEqual([
      { index: 1, state: 'optional_not_run', taskId: '', taskState: '', optional: true },
    ]);
    // A state this build does not know is dropped, so the card falls back to
    // its own pairing instead of showing another step's state.
    const unknown = parseSessionBudget(schema, {
      id: 'budget-1', state: 'active', units: [],
      plan: { title: '四视图加总图', steps: [{ capability: 'text-to-image', purpose: '正面' }] },
      steps: [{ index: 1, state: 'teleported' }],
    });
    expect(unknown?.steps).toBeUndefined();
  });
});

function formatCount(count: number): string {
  return formatSchemaText(text('ui.studio.plan.stopped'), { count });
}