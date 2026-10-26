export type GaussianSplatReadyState = 'pending' | 'ready' | 'failed';

export class GaussianSplatReadyGate {
  private currentState: GaussianSplatReadyState = 'pending';

  constructor(private readonly startedAt: number, private readonly deadlineMs: number) {}

  get state(): GaussianSplatReadyState {
    return this.currentState;
  }

  get canContinue(): boolean {
    return this.currentState !== 'failed';
  }

  markReady(now: number): boolean {
    if (this.currentState !== 'pending' || now - this.startedAt >= this.deadlineMs) return false;
    this.currentState = 'ready';
    return true;
  }

  fail(): boolean {
    if (this.currentState === 'failed') return false;
    this.currentState = 'failed';
    return true;
  }
}
