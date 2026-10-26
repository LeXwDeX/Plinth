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
