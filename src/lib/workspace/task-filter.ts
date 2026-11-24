import type { TrackedTask } from '@/store/workspace-store';

/**
 * The task center's filter: everything, what is still running, what waits in a queue, what failed (#546 added the
 * queue). The drawer lists only public Tasks of the providers; the Agent's own turn is not one.
 */
export type TaskFilter = 'all' | 'active' | 'queued' | 'failed';

export const TASK_FILTERS: readonly TaskFilter[] = ['all', 'active', 'queued', 'failed'];

export function isTaskFilter(value: unknown): value is TaskFilter {
  return typeof value === 'string' && (TASK_FILTERS as readonly string[]).includes(value);
}

/** A task that ended without a result: a failure, an error seen while tracking, or one the server no longer knows. */
export function isFailedTask(task: Pick<TrackedTask, 'terminal' | 'notFound' | 'failure' | 'liveError'>): boolean {
  return task.notFound || (task.terminal && (task.failure !== null || task.liveError !== ''));
}

/** The Schema's outcome of a task state (`task_states[].outcome`), as `taskStatePresentation` reads it. */
export type TaskOutcomeOf = (state: string) => string;

/**
 * #546: a task waiting for its turn: its state's Schema outcome is `pending` (queued, waiting for a slot, waiting to
 * retry), or the server says why it still waits (`queue_notice`: the provider's own queue or its rate limit). No list
 * of states lives here; the Schema says which states wait.
 */
export function isQueuedTask(task: Pick<TrackedTask, 'terminal' | 'notFound' | 'state' | 'queueNotice'>, outcomeOf: TaskOutcomeOf): boolean {
  if (task.terminal || task.notFound) return false;
  return task.queueNotice !== undefined || (task.state !== '' && outcomeOf(task.state) === 'pending');
}

type FilterableTask = Pick<TrackedTask, 'terminal' | 'notFound' | 'failure' | 'liveError' | 'state' | 'queueNotice'>;

export function filterTasks<T extends FilterableTask>(tasks: readonly T[], filter: TaskFilter, outcomeOf: TaskOutcomeOf = () => ''): T[] {
  if (filter === 'active') return tasks.filter((task) => !task.terminal && !task.notFound);
  if (filter === 'queued') return tasks.filter((task) => isQueuedTask(task, outcomeOf));
  if (filter === 'failed') return tasks.filter(isFailedTask);
  return [...tasks];
}
