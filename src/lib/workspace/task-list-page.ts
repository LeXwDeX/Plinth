import type { TrackedTask } from '@/store/workspace-store';

/**
 * #546: the task drawer's order and pages. Newest first by when the task was created (the server's `created_at`
 * once the task detail has arrived, else when this page registered it); a change of state never moves a row. Ties
 * keep the later registration on top. Pure; the drawer pages what the filter kept.
 */
export function newestFirst<T extends Pick<TrackedTask, 'startedAt'>>(tasks: readonly T[]): T[] {
  return tasks
    .map((task, index) => ({ task, index }))
    .sort((left, right) => {
      const a = left.task.startedAt ?? Number.POSITIVE_INFINITY;
      const b = right.task.startedAt ?? Number.POSITIVE_INFINITY;
      if (a !== b) return b > a ? 1 : -1;
      return right.index - left.index;
    })
    .map(({ task }) => task);
}

export interface TaskListPage<T> {
  shown: T[];
  /** Tasks after the shown ones; the "show more" line opens the next page of them. */
  more: number;
}

/** The first `pages` pages of `size` rows (the Schema's `runtime.frontend.task_list_page_size`). */
export function taskListPage<T>(tasks: readonly T[], pages: number, size: number): TaskListPage<T> {
  const count = Math.max(1, Math.floor(pages)) * Math.max(1, Math.floor(size));
  const shown = tasks.slice(0, count);
  return { shown, more: tasks.length - shown.length };
}
