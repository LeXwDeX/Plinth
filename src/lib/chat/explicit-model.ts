/**
 * #103 / #239: a model pick is the person's explicit lock for the session, so
 * it must reach the server before the next turn uses it. The 2D and 3D locks
 * are independent. Writes are serialized per session and domain and only the
 * newest pick is kept: an older request finishing late cannot overwrite a
 * newer choice, and a failed write rolls the picker back to the last selection
 * the server accepted.
 *
 * A pick made before the session exists (a brand-new chat) waits here; the
 * first send adopts it once it has a session ID and awaits the same write
 * before starting the turn.
 */

export type ExplicitModelDomain = 'image' | 'model-3d';

export type ExplicitModelWrite = (sessionId: string, model: string) => Promise<unknown>;

export type ExplicitModelFailure = (previous: string | null, failed: string) => void;

export interface ExplicitModelPick {
  /** The session the pick belongs to; null until a new chat has been created. */
  sessionId: string | null;
  model: string;
  /** The selection to restore when the write fails. */
  previous: string | null;
  write: ExplicitModelWrite;
  onFailure?: ExplicitModelFailure;
}

interface SessionLock {
  wanted: string;
  previous: string | null;
  confirmed: string | null;
  write: ExplicitModelWrite;
  onFailure?: ExplicitModelFailure;
  running: Promise<void> | null;
  failed: { error: unknown } | null;
}

const locks = new Map<string, SessionLock>();
const beforeSession = new Map<ExplicitModelDomain, { model: string; previous: string | null; write: ExplicitModelWrite; onFailure?: ExplicitModelFailure }>();

/** A failed lock write whose failure was already rolled back and reported. */
export class ExplicitModelLockError extends Error {
  readonly failure: unknown;

  constructor(failure: unknown) {
    super('the explicit model could not be stored');
    this.name = 'ExplicitModelLockError';
    this.failure = failure;
  }
}

function lockKey(domain: ExplicitModelDomain, sessionId: string): string {
  return `${domain}:${sessionId}`;
}

function start(state: SessionLock, sessionId: string): void {
  if (state.running) return;
  state.running = (async () => {
    for (;;) {
      if (state.wanted === state.confirmed) return;
      const target = state.wanted;
      try {
        await state.write(sessionId, target);
        state.confirmed = target;
        state.failed = null;
      } catch (error) {
        // A newer pick arrived while this write was in flight: the older
        // failure must not roll back the newer selection.
        if (state.wanted !== target) continue;
        state.failed = { error };
        try {
          state.onFailure?.(state.previous, target);
        } catch {
          // A failed rollback callback must not wedge the writer.
        }
        return;
      }
    }
  })().finally(() => {
    state.running = null;
  });
}

function adoptBeforeSession(domain: ExplicitModelDomain, sessionId: string, write: ExplicitModelWrite): void {
  const pending = beforeSession.get(domain);
  if (!pending) return;
  beforeSession.delete(domain);
  const key = lockKey(domain, sessionId);
  const existing = locks.get(key);
  if (existing) {
    if (existing.confirmed !== null) existing.previous = existing.confirmed;
    existing.wanted = pending.model;
    existing.write = pending.write;
    existing.failed = null;
    if (pending.onFailure) existing.onFailure = pending.onFailure;
    start(existing, sessionId);
    return;
  }
  const state: SessionLock = {
    wanted: pending.model,
    previous: pending.previous,
    confirmed: null,
    write: pending.write,
    ...(pending.onFailure ? { onFailure: pending.onFailure } : {}),
    running: null,
    failed: null,
  };
  locks.set(key, state);
  start(state, sessionId);
}

/** Record a pick for one domain. The write starts immediately when the session exists. */
export function pickExplicitModel(domain: ExplicitModelDomain, input: ExplicitModelPick): void {
  if (!input.sessionId) {
    const pending = beforeSession.get(domain);
    beforeSession.set(domain, {
      model: input.model,
      // Keep the first previous selection: several picks before a send should
      // still roll back to where the person started.
      previous: pending ? (pending.previous ?? input.previous) : input.previous,
      write: input.write,
      ...(input.onFailure ? { onFailure: input.onFailure } : {}),
    });
    return;
  }
  const sessionId = input.sessionId;
  const key = lockKey(domain, sessionId);
  const existing = locks.get(key);
  if (existing) {
    if (existing.confirmed !== null) existing.previous = existing.confirmed;
    existing.wanted = input.model;
    existing.write = input.write;
    existing.failed = null;
    if (input.onFailure) existing.onFailure = input.onFailure;
    start(existing, sessionId);
    return;
  }
  const state: SessionLock = {
    wanted: input.model,
    previous: input.previous,
    confirmed: null,
    write: input.write,
    ...(input.onFailure ? { onFailure: input.onFailure } : {}),
    running: null,
    failed: null,
  };
  locks.set(key, state);
  start(state, sessionId);
}

/**
 * Wait until every pick of one domain for the session is stored. The session
 * ID is needed to adopt a pick made before the session existed. Rejects when
 * the newest write failed; the picker has already been rolled back in that
 * case.
 */
export async function flushExplicitModel(domain: ExplicitModelDomain, sessionId: string, write: ExplicitModelWrite): Promise<void> {
  adoptBeforeSession(domain, sessionId, write);
  const state = locks.get(lockKey(domain, sessionId));
  if (!state) return;
  state.write = write;
  for (;;) {
    if (state.running) {
      await state.running;
      continue;
    }
    if (state.failed) {
      // Report the failure to this send only. The picker was already rolled
      // back, so later sends follow the last model the server accepted
      // instead of failing on the same stale write forever.
      const { error } = state.failed;
      state.failed = null;
      if (state.confirmed === null) locks.delete(lockKey(domain, sessionId));
      else state.wanted = state.confirmed;
      throw new ExplicitModelLockError(error);
    }
    if (state.wanted === state.confirmed) return;
    start(state, sessionId);
  }
}

/** Whether anything of one domain still needs a write; exposed for the send wiring and tests. */
export function explicitModelPending(domain: ExplicitModelDomain, sessionId?: string | null): boolean {
  if (sessionId) {
    const state = locks.get(lockKey(domain, sessionId));
    return state !== undefined && (state.running !== null || state.wanted !== state.confirmed || state.failed !== null);
  }
  return beforeSession.has(domain)
    || [...locks.entries()].some(([key, state]) => key.startsWith(`${domain}:`)
      && (state.running !== null || state.wanted !== state.confirmed || state.failed !== null));
}

// ---- #103 3D lock wrappers ----

export function pickExplicitModel3D(input: ExplicitModelPick): void {
  pickExplicitModel('model-3d', input);
}

export function flushExplicitModel3D(sessionId: string, write: ExplicitModelWrite): Promise<void> {
  return flushExplicitModel('model-3d', sessionId, write);
}

export function explicitModel3DPending(sessionId?: string | null): boolean {
  return explicitModelPending('model-3d', sessionId);
}

// ---- #239 2D lock wrappers ----

export function pickExplicitModelImage(input: ExplicitModelPick): void {
  pickExplicitModel('image', input);
}

export function flushExplicitModelImage(sessionId: string, write: ExplicitModelWrite): Promise<void> {
  return flushExplicitModel('image', sessionId, write);
}

export function explicitModelImagePending(sessionId?: string | null): boolean {
  return explicitModelPending('image', sessionId);
}

/** Test isolation only. */
export function resetExplicitModel(): void {
  locks.clear();
  beforeSession.clear();
}

/** Test isolation only, retained for the 3D tests. */
export function resetExplicitModel3D(): void {
  resetExplicitModel();
}