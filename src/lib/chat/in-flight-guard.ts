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
}
