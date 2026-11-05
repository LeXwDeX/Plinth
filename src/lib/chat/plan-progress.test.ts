import { describe, expect, it } from 'vitest';

import type { ProductionPlan } from '@/lib/chat/budget';
import { completedPlanSteps, planStepProgress, type PlanTask } from '@/lib/chat/plan-progress';

const plan = (...steps: Array<[string, string]>): ProductionPlan => ({
  title: '制作计划',
  steps: steps.map(([capability, offeringId]) => ({ capability, offeringId, purpose: '', estimate: { unit: 'legacy.credits', unit_label: 'legacy.credits', amount: '10' }, purposeBudgetNote: '' })),
  correctionCapabilities: [],
});

const task = (taskId: string, capability: string, state = 'SUCCEEDED', offeringId?: string): PlanTask => ({
  taskId, capability, state, ...(offeringId ? { offeringId } : {}),
});

describe('pairing plan steps with session tasks', () => {
  it('takes the task of the pinned Offering even when another task shares its capability', () => {
    const progress = planStepProgress(plan(['text-to-3d', 'tripo-v3']), [
      task('t1', 'text-to-3d', 'RUNNING', 'meshy-7'),
      task('t2', 'text-to-3d', 'SUCCEEDED', 'tripo-v3'),
    ]);
    expect(progress.map((entry) => entry.task?.taskId)).toEqual(['t2']);
  });

  it('takes the next task of the capability when the pinned Offering has none', () => {
    const progress = planStepProgress(plan(['text-to-3d', 'tripo-v3']), [task('t1', 'text-to-3d', 'SUCCEEDED', 'meshy-7')]);
    expect(progress[0].task?.taskId).toBe('t1');
  });

  it('claims each task for one step only, in plan order', () => {
    const progress = planStepProgress(plan(['remesh', 'remesh'], ['remesh', 'remesh']), []);
    expect(progress.map((entry) => entry.task)).toEqual([null, null]);
    const shared = planStepProgress(plan(['remesh', ''], ['remesh', '']), [task('t1', 'remesh')]);
    expect(shared.map((entry) => entry.task?.taskId ?? null)).toEqual(['t1', null]);
  });

  it('never pairs a task of another capability', () => {
    const progress = planStepProgress(plan(['remesh', '']), [task('t1', 'retexture')]);
    expect(progress[0].task).toBeNull();
    expect(progress[0].step.capability).toBe('remesh');
  });

  it('counts no task for a plan without steps', () => {
    expect(planStepProgress(plan(), [task('t1', 'remesh')])).toEqual([]);
  });

  it('never claims a task that started before the plan was approved', () => {
    const decidedAt = '2026-10-01T00:00:10Z';
    const earlier = { ...task('old', 'text-to-3d', 'SUCCEEDED', 'tripo-v3'), startedAt: Date.parse('2026-10-01T00:00:00Z') };
    const later = { ...task('new', 'text-to-3d', 'RUNNING', 'tripo-v3'), startedAt: Date.parse('2026-10-01T00:00:20Z') };
    const progress = planStepProgress(plan(['text-to-3d', 'tripo-v3']), [earlier, later], decidedAt);
    expect(progress.map((entry) => entry.task?.taskId)).toEqual(['new']);
  });

  it('accepts ISO task start times when filtering by the decision', () => {
    const after = { ...task('new', 'remesh'), startedAt: '2026-10-01T00:00:20Z' };
    expect(planStepProgress(plan(['remesh', '']), [after], '2026-10-01T00:00:10Z')[0].task?.taskId).toBe('new');
  });

  it('does not claim a task without a start time once a decision time is known', () => {
    expect(planStepProgress(plan(['remesh', '']), [task('unknown', 'remesh')], '2026-10-01T00:00:10Z')[0].task).toBeNull();
  });

  it('keeps the old pairing when no usable decision time is known', () => {
    const startedAt = Date.parse('2026-10-01T00:00:00Z');
    const old = { ...task('old', 'remesh'), startedAt };
    expect(planStepProgress(plan(['remesh', '']), [old], '')[0].task?.taskId).toBe('old');
    expect(planStepProgress(plan(['remesh', '']), [old], 'not-a-time')[0].task?.taskId).toBe('old');
  });
});

describe('counting completed steps', () => {
  it('counts only steps whose task is complete, and ignores a step without a task', () => {
    const progress = planStepProgress(plan(['a', 'b'], ['c', 'd']), [
      task('t1', 'a', 'SUCCEEDED'),
      task('t2', 'b', 'FAILED'),
    ]);
    const isComplete = (entry: PlanTask) => entry.state === 'SUCCEEDED';
    expect(completedPlanSteps(progress, isComplete)).toBe(1);
    expect(completedPlanSteps(planStepProgress(plan(['a', 'b']), []), isComplete)).toBe(0);
  });
});
