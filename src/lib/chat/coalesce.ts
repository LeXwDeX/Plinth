/**
 * #665: one read per key at a time. A stream replayed from its first event delivers every status and task
 * registration again; each would ask the server once more. While a read for the key is out, further asks are folded
 * into one more read made after it ends, so a replay of any length costs at most two reads.
 */
export function createCoalescer() {
  const running = new Map<string, boolean>();
  return function coalesce(key: string, read: () => Promise<unknown>): void {
    if (running.has(key)) {
      running.set(key, true);
      return;
    }
    running.set(key, false);
    void read().catch(() => undefined).finally(() => {
      const again = running.get(key) === true;
      running.delete(key);
      if (again) coalesce(key, read);
    });
  };
}
