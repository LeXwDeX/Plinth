// @vitest-environment jsdom
import React from 'react';
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PlanCard } from '@/components/studio/plan-card';
import type { ProductionPlan, SessionBudget } from '@/lib/chat/budget';
import type { PlanTask } from '@/lib/chat/plan-progress';
import { schemaText, taskStatePresentation, type ProductSchema } from '@/lib/product-schema';

// #403: a plan step that has no task read "waiting to run" for ever, even after the turn that should have submitted it
// had failed or ended. Once nothing can still submit the step the card says it was not run.

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
vi.mock('swr', () => ({
  default: () => ({ data: { 'text-to-3d': [{ id: 'tripo-v3', model_name: 'Tripo v3' }] } }),
}));

const text = (key: string) => schemaText(schema, key);
const estimate = { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '80' };
const plan: ProductionPlan = {
  title: '木椅',
  steps: [
    { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '主体', estimate, purposeBudgetNote: '' },
    { capability: 'retexture', offeringId: '', purpose: '材质', estimate, purposeBudgetNote: '' },
  ],
  correctionCapabilities: [],
};
const decided: SessionBudget = {
  id: 'budget-1', state: 'active',
  units: [{ unit: 'tripo.credit', unitLabel: 'Tripo credits', limit: '300', spent: '80', reserved: '0', remaining: '220' }],
  plan, createdAt: '2026-10-01T00:00:00Z', decidedAt: '2026-10-01T00:01:00Z',
};
const firstStepOnly: PlanTask[] = [
  { taskId: 't1', capability: 'text-to-3d', state: 'SUCCEEDED', offeringId: 'tripo-v3', startedAt: Date.parse('2026-10-01T00:02:00Z') },
];

afterEach(cleanup);

describe('the steps of an approved plan that have no task (#403)', () => {
  it('has a Schema text of its own for a step that was not run, different from waiting', () => {
    expect(text('ui.studio.plan.step_not_run')).not.toBe('');
    expect(text('ui.studio.plan.step_not_run')).not.toBe(text('ui.studio.plan.step_pending'));
  });

  it('says a step was not run once the turn that should have submitted it has ended', () => {
    const view = render(<PlanCard plan={plan} tasks={[]} progress={decided} stepsMayStillRun={false} />);
    const steps = view.getAllByTestId('studio-plan-step');
    for (const step of steps) {
      expect(step.getAttribute('data-state')).toBe('not_run');
      expect(step.textContent).toContain(text('ui.studio.plan.step_not_run'));
      expect(step.textContent).not.toContain(text('ui.studio.plan.step_pending'));
    }
  });

  it('keeps saying a step waits while the turn still runs', () => {
    for (const props of [{ stepsMayStillRun: true }, {}]) {
      const view = render(<PlanCard plan={plan} tasks={[]} progress={decided} {...props} />);
      for (const step of view.getAllByTestId('studio-plan-step')) {
        expect(step.getAttribute('data-state')).toBe('pending');
        expect(step.textContent).toContain(text('ui.studio.plan.step_pending'));
        expect(step.textContent).not.toContain(text('ui.studio.plan.step_not_run'));
      }
      cleanup();
    }
  });

  it('leaves a step that has a task as it is, and only the step without one is not run', () => {
    const view = render(<PlanCard plan={plan} tasks={firstStepOnly} progress={decided} stepsMayStillRun={false} />);
    const [first, second] = view.getAllByTestId('studio-plan-step');
    expect(first.getAttribute('data-state')).toBe('SUCCEEDED');
    expect(first.textContent).toContain(taskStatePresentation(schema, 'SUCCEEDED').label);
    expect(first.textContent).not.toContain(text('ui.studio.plan.step_not_run'));
    expect(second.getAttribute('data-state')).toBe('not_run');
    expect(second.textContent).toContain(text('ui.studio.plan.step_not_run'));
  });

  it('does not count a step that was not run as done', () => {
    const view = render(<PlanCard plan={plan} tasks={firstStepOnly} progress={decided} stepsMayStillRun={false} />);
    expect(view.getByTestId('studio-plan-progress').textContent).toContain('1/2');
  });

  it('keeps a plan that still waits for approval at waiting, since no turn has to be running for that', () => {
    const view = render(<PlanCard plan={plan} budgets={[estimate]} tasks={[]} onDecide={() => {}} stepsMayStillRun={false} />);
    for (const step of view.getAllByTestId('studio-plan-step')) {
      expect(step.getAttribute('data-state')).toBe('pending');
      expect(step.textContent).toContain(text('ui.studio.plan.step_pending'));
    }
  });

  it('words the candidates of a parallel group the same way', () => {
    const candidatePlan: ProductionPlan = {
      title: '两个候选',
      steps: [
        { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '', estimate, purposeBudgetNote: '', candidateGroup: 1, candidateIndex: 1 },
        { capability: 'text-to-3d', offeringId: 'tripo-v3', purpose: '', estimate, purposeBudgetNote: '', candidateGroup: 1, candidateIndex: 2 },
      ],
      correctionCapabilities: [],
    };
    const view = render(<PlanCard plan={candidatePlan} tasks={firstStepOnly} progress={{ ...decided, plan: candidatePlan }} stepsMayStillRun={false} />);
    const [first, second] = view.getAllByTestId('studio-plan-candidate');
    expect(first.getAttribute('data-state')).toBe('SUCCEEDED');
    expect(second.getAttribute('data-state')).toBe('not_run');
    expect(second.textContent).toContain(text('ui.studio.plan.step_not_run'));
    expect(first.textContent).not.toContain(text('ui.studio.plan.step_not_run'));
  });
});
