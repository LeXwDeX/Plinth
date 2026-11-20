import { describe, expect, it } from 'vitest';

import type { ProductionPlan } from '@/lib/chat/budget';
import { planStepProgress, type PlanTask } from '@/lib/chat/plan-progress';

// #403: a plan step without a task read "waiting to run" for ever, even when the turn that should have submitted it
// had failed, been stopped or ended. A step the plan's turn can no longer submit is "not run"; one it still can
// stays waiting; a step that has a task shows that task whatever the turn does.

const plan = (...capabilities: string[]): ProductionPlan => ({
  title: '制作计划',
  steps: capabilities.map((capability) => ({
    capability, offeringId: '', purpose: '', estimate: { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '10' }, purposeBudgetNote: '',
  })),
  correctionCapabilities: [],
});
const task = (taskId: string, capability: string, state = 'SUCCEEDED'): PlanTask => ({ taskId, capability, state });

describe('plan steps without a task once the turn cannot submit them', () => {
  it('marks every step that has no task as not run when the turn no longer runs', () => {
    const progress = planStepProgress(plan('text-to-3d', 'retexture'), [], '', false);
    expect(progress.map((entry) => [entry.task, entry.notRun])).toEqual([[null, true], [null, true]]);
  });

  it('keeps such a step waiting while the turn still runs', () => {
    const progress = planStepProgress(plan('text-to-3d', 'retexture'), [], '', true);
    expect(progress.map((entry) => entry.notRun)).toEqual([false, false]);
  });

  it('keeps waiting when it is not said whether the turn runs', () => {
    expect(planStepProgress(plan('text-to-3d'), []).map((entry) => entry.notRun)).toEqual([false]);
  });

  it('never marks a step that has a task, whatever the turn does', () => {
    const progress = planStepProgress(plan('text-to-3d', 'retexture'), [task('t1', 'text-to-3d', 'FAILED')], '', false);
    expect(progress.map((entry) => [entry.task?.taskId ?? null, entry.notRun])).toEqual([['t1', false], [null, true]]);
  });

  it('judges a step by the pairing it already has: a task from before the approval does not rescue it', () => {
    const earlier = { ...task('old', 'text-to-3d'), startedAt: Date.parse('2026-10-01T00:00:00Z') };
    const progress = planStepProgress(plan('text-to-3d'), [earlier], '2026-10-01T00:00:10Z', false);
    expect(progress.map((entry) => [entry.task, entry.notRun])).toEqual([[null, true]]);
  });
});
