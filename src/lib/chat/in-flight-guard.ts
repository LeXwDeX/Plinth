/**
 * Which requests are being decided right now, one flag per request ID. It is read
 * and written synchronously, so two clicks in the same tick cannot both pass (React
 * state would still read as idle for the second); and a busy card does not block
 * the others. It lives in a ref; the page keeps a separate state copy only to draw
 * the busy buttons.
 */
export class InFlightGuard {
  private readonly active = new Set<string>();

  /** Claims the request; false when it is already being decided. */
  begin(requestId: string): boolean {
    if (this.active.has(requestId)) return false;
    this.active.add(requestId);
    return true;
  }

  end(requestId: string): void {
    this.active.delete(requestId);
  }

  has(requestId: string): boolean {
    return this.active.has(requestId);
  }

  /**
   * Runs `work` while holding the request, and releases it however `work` ends.
   * Resolves false, without running `work`, when the request is already being
   * decided. Handlers go through this instead of pairing `begin` and `end` by hand
   * (#503: a hand-written claim once read the result inverted).
   */
  async run(requestId: string, work: () => Promise<void>): Promise<boolean> {
    if (!this.begin(requestId)) return false;
    try {
      await work();
      return true;
    } finally {
      this.end(requestId);
    }
  }
}
