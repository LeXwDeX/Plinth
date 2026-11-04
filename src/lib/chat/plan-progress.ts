import type { ProductionPlan, ProductionPlanStep } from '@/lib/chat/budget';

/**
 * #103: the plan card's progress, read from the session's Task projection. The
 * server owns task states; this module only pairs each plan step with the task
 * that ran it, in order, without guessing from charging or message order.
 */

export interface PlanTask {
  taskId: string;
  capability: string;
  state: string;
  /** Known once the server's task detail has arrived. */
  offeringId?: string;
}

export interface PlanStepProgress {
  step: ProductionPlanStep;
  /** The task that ran this step; null while the step has not started. */
  task: PlanTask | null;
}

/**
 * Pairs plan steps with session tasks: same capability, and the pinned Offering
 * when both sides know it. A task is claimed by one step only; when the exact
 * Offering has no task yet the step takes the next task of its capability.
 */
export function planStepProgress(plan: ProductionPlan, tasks: readonly PlanTask[]): PlanStepProgress[] {
  const used = new Set<number>();
  return plan.steps.map((step) => {
    let index = -1;
    if (step.offeringId) {
      index = tasks.findIndex((task, at) => !used.has(at) && task.capability === step.capability && task.offeringId === step.offeringId);
    }
    if (index < 0) {
      index = tasks.findIndex((task, at) => !used.has(at) && task.capability === step.capability);
    }
    if (index >= 0) used.add(index);
    return { step, task: index >= 0 ? tasks[index] : null };
  });
}

/** How many steps have a task that reached a successful, terminal state. */
export function completedPlanSteps(
  progress: readonly PlanStepProgress[],
  isComplete: (task: PlanTask) => boolean,
): number {
  return progress.filter((entry) => entry.task !== null && isComplete(entry.task)).length;
}
