import {
  consentAwaitingDecision,
  projectConsentState,
  type AgentHarnessClient,
} from './agent-params';
import { startPoller, type IdlePolling, type PollClock, type Poller, type PollingGate } from './polling';

export interface SessionConsentSnapshot {
  state: Record<string, unknown> | null;
  busy: boolean;
}

type ConsentClient = Pick<AgentHarnessClient, 'getConsent' | 'putConsent'>;

/** How the controller's reads are paced (#385); every part has a browser default and is only replaced by tests. */
export interface ConsentPolling {
  /** Slows the reads down while they find nothing new, and while `busy` is false. */
  idle?: IdlePolling;
  /** A turn or a task of the session is running: the Agent may ask for a decision at any moment. */
  busy?: () => boolean;
  gate?: PollingGate;
  clock?: PollClock;
}

function sameState(a: Record<string, unknown> | null, b: Record<string, unknown> | null): boolean {
  return a === b || JSON.stringify(a) === JSON.stringify(b);
}

/** Coordinates requests for one session; consent facts remain server owned. */
export class SessionConsentController {
  private snapshot: SessionConsentSnapshot = { state: null, busy: false };
  private listeners = new Set<() => void>();
  private active = false;
  private revision = 0;
  private poller: Poller | null = null;
  private polling: AbortController | null = null;
  private deciding: AbortController | null = null;

  constructor(
    private readonly scope: string | null,
    private readonly client: ConsentClient,
    private readonly pollMilliseconds: number,
    private readonly pacing: ConsentPolling = {},
  ) {}

  getSnapshot = (): SessionConsentSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  start(): void {
    if (this.active || !this.scope) return;
    this.active = true;
    // Reads at once, then waits; stands still while the tab is hidden or the login has ended, reads once more when
    // the tab is shown again, and waits longer while nothing changes and nothing runs.
    this.poller = startPoller({
      read: () => this.poll(),
      intervalMilliseconds: this.pollMilliseconds,
      ...this.pacing,
    });
  }

  /** Reads now and waits from the base interval again: something started that may change the answer. */
  refresh(): void {
    this.poller?.refresh();
  }

  stop(): void {
    this.active = false;
    this.revision++;
    this.poller?.stop();
    this.poller = null;
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

  /** One read; false when it found nothing new, which is what slows the next ones down. */
  private async poll(): Promise<boolean> {
    // A decision in flight is something happening: not a quiet read.
    if (!this.active || !this.scope || this.polling || this.snapshot.busy) return true;
    const request = new AbortController();
    const revision = this.revision;
    this.polling = request;
    try {
      const state = await this.client.getConsent(this.scope, request.signal);
      if (this.active && revision === this.revision && !request.signal.aborted) {
        const changed = !sameState(this.snapshot.state, state);
        // An unchanged answer keeps the snapshot: nothing re-renders for a quiet read.
        if (changed) this.publish({ state, busy: false });
        return changed;
      }
    } catch {
      // A transient observation failure keeps the last server facts visible.
    } finally {
      if (this.polling === request) this.polling = null;
    }
    return false;
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
