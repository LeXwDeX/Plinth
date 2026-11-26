/**
 * Task IDs the person cleared with X (#651): remembered in this browser so the history hydration after a reload or a
 * session switch does not bring them back. Task IDs are unique across sessions. Storage may be missing or refuse
 * writes (private windows); then dismissed tasks simply return, as before.
 */
const KEY = 'plinth.dismissedTasks';
const LIMIT = 500;

function read(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : [];
  } catch {
    return [];
  }
}

export function isDismissedTask(taskId: string): boolean {
  return read().includes(taskId);
}

export function rememberDismissedTask(taskId: string): void {
  try {
    localStorage.setItem(KEY, JSON.stringify([...read().filter((id) => id !== taskId), taskId].slice(-LIMIT)));
  } catch { /* no storage: the task may come back after a reload */ }
}
