import type { CanvasProject } from './browser-client';

const sessionSaves = new Map<string, Promise<void>>();
const sessionCreates = new Map<string, Promise<CanvasProject>>();

/** Share creation across effect cleanup/remount. Keep a new-project receipt until a board consumes it. */
export function beginSessionCanvasCreate(sessionID: string, create: () => Promise<CanvasProject>): Promise<CanvasProject> {
  const pending = sessionCreates.get(sessionID);
  if (pending) return pending;
  const result = create();
  sessionCreates.set(sessionID, result);
  void result.then((project) => {
    if (!project.created) finishSessionCanvasCreate(sessionID, result);
  }, () => finishSessionCanvasCreate(sessionID, result));
  return result;
}

/** Only the load using this receipt can consume it; stale completions cannot clear a newer request. */
export function finishSessionCanvasCreate(sessionID: string, result: Promise<CanvasProject>): void {
  if (sessionCreates.get(sessionID) === result) sessionCreates.delete(sessionID);
}

/** Serialize writes across board mounts and capture handoffs for one session. */
export function enqueueSessionCanvasSave<T>(sessionID: string, save: () => Promise<T>): Promise<T> {
  const previous = sessionSaves.get(sessionID) ?? Promise.resolve();
  const result = previous.then(save);
  // The stored tail always fulfills. A failed caller cannot block later writes,
  // and cleanup never creates an unobserved rejected finally() promise.
  const tail = result.then(() => {}, () => {});
  sessionSaves.set(sessionID, tail);
  void tail.then(() => {
    if (sessionSaves.get(sessionID) === tail) sessionSaves.delete(sessionID);
  });
  return result;
}

/** Wait through writes appended while waiting, until this session is idle. */
export async function waitForSessionCanvasSaves(sessionID: string): Promise<void> {
  let tail = sessionSaves.get(sessionID);
  while (tail) {
    await tail;
    const next = sessionSaves.get(sessionID);
    if (next === tail) return;
    tail = next;
  }
}
