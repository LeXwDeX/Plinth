/**
 * #665: one read per key at a time. A stream replayed from its first event delivers every status and task
 * registration again; each would ask the server once more. While a read for the key is out, further asks are folded
 * into one more read made after it ends, so a replay of any length costs at most two reads. The trailing read uses
 * the latest ask's closure (a session-bound read may have been replaced meanwhile), and a read that throws before
 * returning its promise releases the key like one that fails.
 */
export function createCoalescer() {
  // The key's latest folded ask, or null while only the running read was asked for.
  const running = new Map<string, (() => Promise<unknown>) | null>();
  return function coalesce(key: string, read: () => Promise<unknown>): void {
    if (running.has(key)) {
      running.set(key, read);
      return;
    }
    running.set(key, null);
    let pending: Promise<unknown>;
    try {
      pending = read();
    } catch {
      pending = Promise.resolve();
    }
    void Promise.resolve(pending).catch(() => undefined).finally(() => {
      const again = running.get(key);
      running.delete(key);
      if (again) coalesce(key, again);
    });
  };
}
