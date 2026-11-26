/**
 * Task IDs the person cleared with X (#651): remembered in this browser so the history hydration after a reload or a
 * session switch does not bring them back. Task IDs are unique across sessions, so one list serves every account on
 * the browser. Storage may be missing or refuse writes (private windows); then dismissed tasks stay cleared for this page and return after a reload.
 */
const KEY = 'plinth.dismissedTasks';
const LIMIT = 500;

// Parsed once per page: hydration asks about every restored task, and only this page's own dismissals change the list.
let cache: string[] | null = null;

function read(): string[] {
  if (cache) return cache;
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]');
    cache = Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : [];
  } catch {
    cache = [];
  }
  return cache;
}

export function isDismissedTask(taskId: string): boolean {
  return read().includes(taskId);
}

export function rememberDismissedTask(taskId: string): void {
  cache = [...read().filter((id) => id !== taskId), taskId].slice(-LIMIT);
  try {
    localStorage.setItem(KEY, JSON.stringify(cache));
  } catch { /* no storage: the task may come back after a reload */ }
}
