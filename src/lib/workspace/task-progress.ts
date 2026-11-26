/**
 * What the chat shows about tasks in flight: a progress card per running task
 * (percentage and time used) and, for a task that was seen running and then
 * failed, a failure notice. Pure selection and arithmetic; the wording is the
 * Schema's and the card component reads it.
 */

import type { TrackedTask } from '@/store/workspace-store';

/** A start time more than this far from now (clock skew, an old task) is not trusted. */
const TRUSTED_ELAPSED_SECONDS = 24 * 60 * 60;

export function isRunning(task: Pick<TrackedTask, 'terminal' | 'notFound'>): boolean {
  return !task.terminal && !task.notFound;
}

/** Percent of the task's own scale, whole numbers, clamped to 0..100. */
export function progressPercent(task: Pick<TrackedTask, 'progress' | 'progressMaximum'>, defaultMaximum: number): number {
  const maximum = task.progressMaximum > 0 ? task.progressMaximum : defaultMaximum;
  if (!(maximum > 0)) return 0;
  return Math.min(100, Math.max(0, Math.round((task.progress / maximum) * 100)));
}

/**
 * Whole seconds the task has been running. The start is the server's creation
 * time when it is known; a start in the future or more than a day back means
 * the clocks disagree, and the card falls back to when the page first saw it.
 */
export function elapsedSeconds(task: Pick<TrackedTask, 'startedAt' | 'updatedAt'>, now: number, seenAt?: number): number {
  const trusted = (at: number | undefined) => at !== undefined && at <= now && now - at <= TRUSTED_ELAPSED_SECONDS * 1000;
  const from = trusted(task.startedAt) ? task.startedAt : trusted(seenAt) ? seenAt : task.updatedAt;
  return Math.max(0, Math.floor((now - (from ?? now)) / 1000));
}

/** m:ss, or h:mm:ss past the hour. */
export function formatElapsed(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = String(total % 60).padStart(2, '0');
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${rest}` : `${minutes}:${rest}`;
}

export interface ChatTaskCards {
  running: TrackedTask[];
  failed: TrackedTask[];
}

/**
 * The tasks of the session in view that deserve a card in the conversation. A
 * failure gets a notice only if this page watched the task run first (`watched`),
 * so reopening a session does not replay every old failure; a dismissed notice
 * stays gone.
 */
export function chatTaskCards(
  tasks: readonly TrackedTask[],
  sessionId: string | null,
  watched: ReadonlySet<string>,
  dismissed: ReadonlySet<string>,
): ChatTaskCards {
  const mine = tasks.filter((task) => sessionId !== null && task.sessionId === sessionId);
  return {
    running: mine.filter(isRunning),
    failed: mine.filter((task) => task.terminal && !task.notFound && watched.has(task.taskId) && !dismissed.has(task.taskId)
      && (task.failure !== null || task.liveError !== '')),
  };
}

/** The once-a-second clock is for the running cards' elapsed time; the failure notices show none. */
export function elapsedClockNeeded(part: 'running' | 'failed', cards: ChatTaskCards): boolean {
  return part === 'running' && cards.running.length > 0;
}

/**
 * The tasks the compact float counts together: every task of the session seen running since the last moment none
 * ran. Parallel tasks started in one turn (or while others still run) form one batch; once all of them have ended
 * the batch is empty again and the next task starts a new one. Returns `previous` itself when nothing was added.
 */
export function nextTaskBatch(previous: ReadonlySet<string>, running: readonly Pick<TrackedTask, 'taskId'>[]): ReadonlySet<string> {
  if (running.length === 0) return previous.size === 0 ? previous : new Set();
  if (running.every((task) => previous.has(task.taskId))) return previous;
  const next = new Set(previous);
  for (const task of running) next.add(task.taskId);
  return next;
}

/**
 * #565: one batch per session, each grown and emptied by `nextTaskBatch` from that session's running tasks only, so
 * switching between two sessions that both run tasks neither counts nor lists the other session's tasks. A task with
 * no session belongs to no batch. Returns `previous` itself when no session's batch changed.
 */
export function nextSessionTaskBatches(
  previous: ReadonlyMap<string, ReadonlySet<string>>,
  tasks: readonly Pick<TrackedTask, 'taskId' | 'sessionId' | 'terminal' | 'notFound'>[],
): ReadonlyMap<string, ReadonlySet<string>> {
  const running = new Map<string, Pick<TrackedTask, 'taskId'>[]>();
  for (const task of tasks) {
    if (!task.sessionId || !isRunning(task)) continue;
    running.set(task.sessionId, [...(running.get(task.sessionId) ?? []), task]);
  }
  let changed = false;
  const next = new Map<string, ReadonlySet<string>>();
  for (const sessionId of new Set([...previous.keys(), ...running.keys()])) {
    const before = previous.get(sessionId) ?? new Set<string>();
    const after = nextTaskBatch(before, running.get(sessionId) ?? []);
    if (after !== before || !previous.has(sessionId)) changed = true;
    if (after.size > 0) next.set(sessionId, after);
  }
  return changed ? next : previous;
}

export interface TaskBatchProgress {
  /** Tasks of the batch that are no longer running (finished, failed or gone). */
  done: number;
  total: number;
  /** The batch as a whole, 0..100: an ended task counts as complete, a running one by its own percent. */
  percent: number;
}

/** "x/y" of the float (ended over all) and its overall bar. */
export function taskBatchProgress(
  batch: ReadonlySet<string>,
  running: readonly Pick<TrackedTask, 'taskId' | 'progress' | 'progressMaximum'>[],
  defaultMaximum: number,
): TaskBatchProgress {
  const ids = new Set([...batch, ...running.map((task) => task.taskId)]);
  const total = ids.size;
  if (total === 0) return { done: 0, total: 0, percent: 0 };
  const done = total - running.length;
  const runningPercent = running.reduce((sum, task) => sum + progressPercent(task, defaultMaximum), 0);
  return { done, total, percent: Math.round((done * 100 + runningPercent) / total) };
}

/**
 * #546: the conversation shows one expression of progress at a time. The "generating" status bubble is only for a
 * turn that is still thinking while no task runs; once a task runs, the task block in the conversation says it.
 */
export function statusBubbleShown(streaming: boolean, statusText: string, runningTasks: number): boolean {
  return streaming && statusText !== '' && runningTasks === 0;
}

/** #665: forgets when the page first saw a task once the task is no longer tracked, so the record stays bounded. */
export function forgetUntrackedTasks(seen: Map<string, number>, tracked: readonly Pick<TrackedTask, 'taskId'>[]): void {
  if (seen.size <= tracked.length) return;
  const ids = new Set(tracked.map((task) => task.taskId));
  for (const id of [...seen.keys()]) if (!ids.has(id)) seen.delete(id);
}
