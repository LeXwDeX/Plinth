/** One immutable UI scope owns its asynchronous result and abort lifetime. */
export class AgentConfigurationResource<T> {
  private snapshot: { data: T | null; failed: boolean; loading: boolean } = { data: null, failed: false, loading: true };
  private listeners = new Set<() => void>();
  private request: AbortController | null = null;
  constructor(private readonly load: (signal: AbortSignal) => Promise<T>) {}
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  start() {
    if (this.request) return;
    const request = new AbortController(); this.request = request;
    this.load(request.signal).then(data => {
      if (this.request === request && !request.signal.aborted) this.publish({ data, failed: false, loading: false });
    }).catch(() => {
      if (this.request === request && !request.signal.aborted) this.publish({ data: null, failed: true, loading: false });
    });
  }
  stop() {
    this.request?.abort(); this.request = null;
    this.publish({ data: null, failed: false, loading: true });
  }
  private publish(snapshot: typeof this.snapshot) { this.snapshot = snapshot; this.listeners.forEach(listener => listener()); }
}
export function sessionSkillId(selection: Readonly<Record<string, string | null>>, sessionId: string | null) {
  return sessionId ? selection[sessionId] ?? null : null;
}
