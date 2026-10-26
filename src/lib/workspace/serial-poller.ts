/** Run a poll only after the previous request has settled. */
export function startSerialPoller(
  poll: (signal: AbortSignal) => Promise<void>,
  intervalMilliseconds: number,
): () => void {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;

  const run = async () => {
    try {
      await poll(controller.signal);
    } catch {
      // A failed poll is retried on the next cycle; the caller owns error UI.
    } finally {
      if (!controller.signal.aborted) {
        timer = setTimeout(() => {
          timer = undefined;
          void run();
        }, intervalMilliseconds);
      }
    }
  };

  void run();
  return () => {
    controller.abort();
    if (timer !== undefined) clearTimeout(timer);
  };
}
