/**
 * #403: following the event stream of the turn a person just sent. A stream that breaks is not the end of the
 * turn: the server keeps running it. So the server is asked whether the turn still runs, and if it does the
 * stream is attached again from the turn's first event (the server replays the turn's events), a bounded number
 * of times. `subscribe()` already re-opens a silent stream by itself (#375) and reports an error only after its
 * own bound; this bound is the second layer and counts every attachment made after the server confirmed the turn
 * runs, so a stream that keeps breaking can never loop. Past it, or once the turn is over, the follow ends as
 * interrupted and the caller reads what is stored.
 */

export type TurnFollowEnd = 'done' | 'aborted' | 'interrupted';

export interface TurnFollowOptions {
  /** Opens the stream from the turn's first event. `onDone` is the end of the turn, `onError` a stream that broke. Returns its closer. */
  connect(callbacks: { onDone(): void; onError(): void }): () => void;
  /** Asks the server whether the turn still runs (a message read); a failed read rejects. */
  stillRunning(signal: AbortSignal): Promise<boolean>;
  /** Drops what the broken stream delivered, because the replay delivers it again. Called before every new attachment. */
  rewind(): void;
  /** How many times the stream may be attached again after the first one. */
  attempts: number;
  signal: AbortSignal;
}

export function followTurnStream(options: TurnFollowOptions): Promise<TurnFollowEnd> {
  const { connect, stillRunning, rewind, attempts, signal } = options;
  return new Promise<TurnFollowEnd>((resolve, reject) => {
    let ended = false;
    let generation = 0;
    let reattached = 0;
    let close: (() => void) | undefined;
    const onAbort = () => finish('aborted');
    function finish(end: TurnFollowEnd): void {
      if (ended) return;
      ended = true;
      signal.removeEventListener('abort', onAbort);
      close?.();
      resolve(end);
    }
    // A subscription this follow has already replaced says nothing any more.
    const attach = (): void => {
      const mine = ++generation;
      close = connect({
        onDone: () => { if (mine === generation) finish('done'); },
        onError: () => { if (mine === generation) void lost(); },
      });
    };
    const lost = async (): Promise<void> => {
      close?.();
      if (ended) return;
      if (reattached >= attempts) {
        finish('interrupted');
        return;
      }
      let running: boolean;
      try {
        running = await stillRunning(signal);
      } catch {
        running = false;
      }
      if (ended) return;
      if (!running) {
        finish('interrupted');
        return;
      }
      reattached += 1;
      rewind();
      try {
        attach();
      } catch {
        finish('interrupted');
      }
    };
    if (signal.aborted) {
      finish('aborted');
      return;
    }
    signal.addEventListener('abort', onAbort, { once: true });
    try {
      attach();
    } catch (error) {
      // The first stream could not be opened at all: the sender reports it as it always did.
      ended = true;
      signal.removeEventListener('abort', onAbort);
      reject(error);
    }
  });
}
