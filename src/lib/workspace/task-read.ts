import type { TaskStateSchema } from '@/lib/product-schema';
import { projectNativeTaskDetail, type TrackedTaskProjection } from '@/lib/workspace/task-projection';
import { useWorkspaceStore } from '@/store/workspace-store';

/**
 * One read of a tracked task's native detail (runtime.http.routes.task_item). The task poller and the reads after a
 * turn's `done` (#605) share it, so one task has at most one read in flight: a caller that asks while a read runs
 * waits for that read. Each caller decides on its own whether the result is still wanted before writing it.
 */

export type TaskRead =
  | { kind: 'detail'; projection: TrackedTaskProjection }
  | { kind: 'not-found' }
  | { kind: 'unavailable' };

export interface TaskReadOptions {
  /** runtime.http.routes.task_item, with `{taskID}`. */
  route: string;
  taskStates: TaskStateSchema[];
  progressMaximum: number;
}

const inFlight = new Map<string, Promise<TaskRead>>();

export function readTask(taskId: string, options: TaskReadOptions): Promise<TaskRead> {
  const running = inFlight.get(taskId);
  if (running) return running;
  const read = (async (): Promise<TaskRead> => {
    try {
      const response = await fetch(options.route.replace('{taskID}', encodeURIComponent(taskId)));
      if (response.status === 404) return { kind: 'not-found' };
      if (!response.ok) return { kind: 'unavailable' };
      const projection = projectNativeTaskDetail(await response.json(), options.taskStates, options.progressMaximum);
      return projection ? { kind: 'detail', projection } : { kind: 'unavailable' };
    } catch {
      // A passing network error: the next read tries again.
      return { kind: 'unavailable' };
    }
  })().finally(() => {
    inFlight.delete(taskId);
  });
  inFlight.set(taskId, read);
  return read;
}

/** Writes a read into the store; a 404 ends the task. */
export function applyTaskRead(taskId: string, read: TaskRead, activeSessionId: string | null): void {
  if (read.kind === 'not-found') useWorkspaceStore.getState().markTaskNotFound(taskId);
  else if (read.kind === 'detail') useWorkspaceStore.getState().applyTaskDetail(taskId, read.projection, activeSessionId);
}

const unended = (sessionId: string) =>
  useWorkspaceStore.getState().trackedTasks.filter((task) => task.sessionId === sessionId && !task.notFound && !task.terminal);

/**
 * #605: reads once each task of the session the page still tracks as running and writes what it finds, unless
 * `isCurrent` says the session is no longer the one shown (or a new turn of it started). Tasks already ended are not
 * read. True when a task of the session is still running afterwards.
 */
export async function refreshSessionTasks(
  sessionId: string,
  options: TaskReadOptions & { isCurrent: () => boolean },
): Promise<boolean> {
  const tasks = unended(sessionId);
  if (tasks.length === 0) return false;
  await Promise.all(tasks.map(async (task) => {
    const read = await readTask(task.taskId, options);
    if (options.isCurrent()) applyTaskRead(task.taskId, read, sessionId);
  }));
  return unended(sessionId).length > 0;
}
