import type { TrackedTask } from '@/store/workspace-store';

/** The task center's filter: everything, what is still running, what failed. */
export type TaskFilter = 'all' | 'active' | 'failed';

export const TASK_FILTERS: readonly TaskFilter[] = ['all', 'active', 'failed'];

export function isTaskFilter(value: unknown): value is TaskFilter {
  return typeof value === 'string' && (TASK_FILTERS as readonly string[]).includes(value);
}

/** A task that ended without a result: a failure, an error seen while tracking, or one the server no longer knows. */
export function isFailedTask(task: Pick<TrackedTask, 'terminal' | 'notFound' | 'failure' | 'liveError'>): boolean {
  return task.notFound || (task.terminal && (task.failure !== null || task.liveError !== ''));
}

export function filterTasks<T extends Pick<TrackedTask, 'terminal' | 'notFound' | 'failure' | 'liveError'>>(tasks: readonly T[], filter: TaskFilter): T[] {
  if (filter === 'active') return tasks.filter((task) => !task.terminal && !task.notFound);
  if (filter === 'failed') return tasks.filter(isFailedTask);
  return [...tasks];
}
