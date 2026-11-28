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

/**
 * The board's debounced node save, per session (#709). The board registers it while it shows that session; a flush
 * saves the nodes now instead of when the debounce (canvas.frontend.auto_save_delay_milliseconds) runs out.
 */
const boardSavers = new Map<string, Set<Flusher>>();

export function registerBoardSave(sessionId: string, save: Flusher): () => void {
  const set = boardSavers.get(sessionId) ?? new Set<Flusher>();
  set.add(save);
  boardSavers.set(sessionId, set);
  return () => {
    set.delete(save);
    if (set.size === 0 && boardSavers.get(sessionId) === set) boardSavers.delete(sessionId);
  };
}

/** How the wait before a turn ended: no board, a canvas turn (not waited for here), saved, failed, or cut at the bound. */
export type BoardSettle = 'idle' | 'canvas_turn' | 'saved' | 'failed' | 'timed_out';

/**
 * #709: before a turn that is not a canvas turn, wait (at most `waitMilliseconds`, canvas.frontend.send_save_wait_milliseconds)
 * until the board's pending edits are on the server: first the brush's local bitmaps, then the debounced node save. The
 * Agent's fallback reading of the board (list_canvas_layers) then sees what is on screen, a picture dragged in just before
 * Enter included. This only keeps the server's board fresh: the turn carries nothing of the board (no snapshot, no input
 * source, no selection), so it is not a hidden canvas turn (#644). A slow or failed save never holds the turn back: the
 * caller sends anyway, without a dialog or a refusal (#700). A canvas turn is left alone: its own snapshot path flushes
 * the brush before it rasterises, as before.
 */
export async function settleBoardForTurn(input: {
  sessionId: string;
  inputSource: string | undefined;
  canvasInputSource: string;
  waitMilliseconds: number;
}): Promise<BoardSettle> {
  if (input.canvasInputSource !== '' && input.inputSource === input.canvasInputSource) return 'canvas_turn';
  if (!flushers.has(input.sessionId) && !boardSavers.has(input.sessionId)) return 'idle';
  const work = (async (): Promise<BoardSettle> => {
    // The brush upload changes the layer's source in the store; the node save that follows reads the store then.
    const flushed = await flushPendingEdits(input.sessionId);
    const saves = [...(boardSavers.get(input.sessionId) ?? [])];
    const results = await Promise.all(saves.map((save) => save()));
    return flushed && results.every((result) => result !== false) ? 'saved' : 'failed';
  })().catch((): BoardSettle => 'failed');
  let timer: ReturnType<typeof setTimeout> | undefined;
  const bound = new Promise<BoardSettle>((resolve) => {
    timer = setTimeout(() => resolve('timed_out'), Math.max(0, input.waitMilliseconds));
  });
  try {
    return await Promise.race([work, bound]);
  } finally {
    clearTimeout(timer);
  }
}
