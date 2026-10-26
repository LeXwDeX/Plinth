/**
 * Edits that live in the browser before they reach the canvas nodes (the brush's local bitmap) register a
 * flusher here. Every entry that sends the board upstream (export, send to chat, merge, inpaint, the Agent's
 * current object) awaits `flushPendingEdits` first, so what is delivered is what is on screen.
 */
type Flusher = () => Promise<unknown>;

const flushers = new Map<string, Set<Flusher>>();

export function registerPendingEdits(sessionId: string, flush: Flusher): () => void {
  const set = flushers.get(sessionId) ?? new Set<Flusher>();
  set.add(flush);
  flushers.set(sessionId, set);
  return () => {
    set.delete(flush);
    if (set.size === 0 && flushers.get(sessionId) === set) flushers.delete(sessionId);
  };
}

/**
 * Layers whose pixels currently live in a local bitmap (the brush's), per session (#246). A canvas resize
 * must not refit such a layer to the new frame even while it is still marked unpainted: its bitmap has the
 * old size and would be stretched, on screen and in the upload that follows.
 */
type BitmapLister = () => Iterable<string>;

const bitmapListers = new Map<string, Set<BitmapLister>>();

export function registerLocalBitmaps(sessionId: string, list: BitmapLister): () => void {
  const set = bitmapListers.get(sessionId) ?? new Set<BitmapLister>();
  set.add(list);
  bitmapListers.set(sessionId, set);
  return () => {
    set.delete(list);
    if (set.size === 0 && bitmapListers.get(sessionId) === set) bitmapListers.delete(sessionId);
  };
}

export function localBitmapNodeIds(sessionId: string): Set<string> {
  const ids = new Set<string>();
  for (const list of bitmapListers.get(sessionId) ?? []) for (const id of list()) ids.add(id);
  return ids;
}

/**
 * Resolves false when a flusher reports that its edits are still not uploaded (the brush's upload failed):
 * the board's nodes then do not hold what is on screen. Callers that only need a best effort ignore it;
 * sending the board to chat refuses to send a stale picture (#286).
 */
export async function flushPendingEdits(sessionId: string): Promise<boolean> {
  const set = flushers.get(sessionId);
  if (!set) return true;
  const results = await Promise.all([...set].map((flush) => flush()));
  return results.every((result) => result !== false);
}
