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
  /** When the task started (epoch milliseconds or an ISO time); absent on old projections. */
  startedAt?: number | string;
  /** Known once the server's task detail has arrived. */
  offeringId?: string;
}

export interface PlanStepProgress {
  step: ProductionPlanStep;
  /** The task that ran this step; null while the step has not started. */
  task: PlanTask | null;
}

function startedAtMilliseconds(task: PlanTask): number {
  if (typeof task.startedAt === 'number') return Number.isFinite(task.startedAt) ? task.startedAt : Number.NaN;
  if (typeof task.startedAt === 'string' && task.startedAt) return Date.parse(task.startedAt);
  return Number.NaN;
}

/**
 * Pairs plan steps with session tasks: same capability, and the pinned Offering
 * when both sides know it. A task is claimed by one step only; when the exact
 * Offering has no task yet the step takes the next task of its capability.
 *
 * #103: `decidedAt` excludes tasks that started before the person approved the
 * plan. Historical tasks of the same capability (for example, an older 3D
 * version made before this plan existed) must not fill the first step. When
 * `decidedAt` is empty or unusable, every task stays eligible as before.
 */
export function planStepProgress(
  plan: ProductionPlan,
  tasks: readonly PlanTask[],
  decidedAt = '',
): PlanStepProgress[] {
  const threshold = decidedAt ? Date.parse(decidedAt) : Number.NaN;
  const eligible = !Number.isFinite(threshold)
    ? tasks
    : tasks.filter((task) => {
      const started = startedAtMilliseconds(task);
      return Number.isFinite(started) && started >= threshold;
    });
  const used = new Set<number>();
  return plan.steps.map((step) => {
    let index = -1;
    if (step.offeringId) {
      index = eligible.findIndex((task, at) => !used.has(at) && task.capability === step.capability && task.offeringId === step.offeringId);
    }
    if (index < 0) {
      index = eligible.findIndex((task, at) => !used.has(at) && task.capability === step.capability);
    }
    if (index >= 0) used.add(index);
    return { step, task: index >= 0 ? eligible[index] : null };
  });
}

/** How many steps have a task that reached a successful, terminal state. */
export function completedPlanSteps(
  progress: readonly PlanStepProgress[],
  isComplete: (task: PlanTask) => boolean,
): number {
  return progress.filter((entry) => entry.task !== null && isComplete(entry.task)).length;
}
