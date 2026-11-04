import { describe, expect, it } from 'vitest';

import type { ProductionPlan } from '@/lib/chat/budget';
import { completedPlanSteps, planStepProgress, type PlanTask } from '@/lib/chat/plan-progress';

const plan = (...steps: Array<[string, string]>): ProductionPlan => ({
  title: '制作计划',
  steps: steps.map(([capability, offeringId]) => ({ capability, offeringId, purpose: '', estimatedCredits: 10 })),
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
