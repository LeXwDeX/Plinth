import {
  consentAwaitingDecision,
  projectConsentState,
  type AgentHarnessClient,
} from './agent-params';

export interface SessionConsentSnapshot {
  state: Record<string, unknown> | null;
  busy: boolean;
}

type ConsentClient = Pick<AgentHarnessClient, 'getConsent' | 'putConsent'>;
interface PollClock {
  setInterval(callback: () => void, milliseconds: number): unknown;
  clearInterval(timer: unknown): void;
}
const browserClock: PollClock = {
  setInterval: (callback, milliseconds) => globalThis.setInterval(callback, milliseconds),
  clearInterval: (timer) => globalThis.clearInterval(timer as ReturnType<typeof setInterval>),
};

/** Coordinates requests for one session; consent facts remain server owned. */
export class SessionConsentController {
  private snapshot: SessionConsentSnapshot = { state: null, busy: false };
  private listeners = new Set<() => void>();
  private active = false;
  private revision = 0;
  private timer: unknown;
  private polling: AbortController | null = null;
  private deciding: AbortController | null = null;

  constructor(
    private readonly scope: string | null,
    private readonly client: ConsentClient,
    private readonly pollMilliseconds: number,
    private readonly clock: PollClock = browserClock,
  ) {}

  getSnapshot = (): SessionConsentSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  start(): void {
    if (this.active || !this.scope) return;
    this.active = true;
    void this.poll();
    this.timer = this.clock.setInterval(() => { void this.poll(); }, this.pollMilliseconds);
  }

  stop(): void {
    this.active = false;
    this.revision++;
    this.clock.clearInterval(this.timer);
    this.polling?.abort();
    this.deciding?.abort();
    this.polling = null;
    this.deciding = null;
    this.publish({ state: null, busy: false });
  }

  private publish(snapshot: SessionConsentSnapshot): void {
    this.snapshot = snapshot;
    this.listeners.forEach((listener) => listener());
  }

  private async poll(): Promise<void> {
    if (!this.active || !this.scope || this.polling || this.snapshot.busy) return;
    const request = new AbortController();
    const revision = this.revision;
    this.polling = request;
    try {
      const state = await this.client.getConsent(this.scope, request.signal);
      if (this.active && revision === this.revision && !request.signal.aborted) {
        this.publish({ state, busy: false });
      }
    } catch {
      // A transient observation failure keeps the last server facts visible.
    } finally {
      if (this.polling === request) this.polling = null;
    }
  }

  async decide(requestId: string, approved: boolean): Promise<void> {
    if (!this.active || !this.scope || this.snapshot.busy ||
      !consentAwaitingDecision(this.snapshot.state) ||
      projectConsentState(this.snapshot.state).requestId !== requestId) return;
    // Lock before awaiting so two clicks cannot submit the same decision concurrently.
    const revision = ++this.revision;
    this.polling?.abort();
    this.polling = null;
    const request = new AbortController();
    this.deciding = request;
    this.publish({ ...this.snapshot, busy: true });
    try {
      await this.client.putConsent(this.scope, requestId, approved, request.signal);
      if (!this.active || revision !== this.revision || request.signal.aborted) return;
      const state = await this.client.getConsent(this.scope, request.signal);
      if (this.active && revision === this.revision && !request.signal.aborted) {
        this.publish({ state, busy: true });
      }
    } catch (error) {
      if (this.active && revision === this.revision && !request.signal.aborted) throw error;
    } finally {
      if (this.active && revision === this.revision) {
        this.deciding = null;
        this.publish({ ...this.snapshot, busy: false });
      }
    }
  }
}
