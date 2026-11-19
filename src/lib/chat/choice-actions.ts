import { HarnessBrowserError } from '@/lib/harness';
import type { ChoiceAction } from '@/lib/chat/choice-cards';

/**
 * Answering a choice card whose option carries an action (#374). The action saves the function selection (through the
 * session's Harness, which only the person changes) and the answer follows once it is saved. When it cannot be saved the
 * answer is not sent at all, so the conversation never moves on under a selection the person did not get; the card stays
 * open and says why in the Schema's words.
 */

/** Why an option's action did not complete; each reason has its own Schema words (ui.chat.choice.action_*). */
export type ChoiceActionReason = 'failed' | 'conflict' | 'unavailable' | 'service_choose' | 'service_none';

export class ChoiceActionError extends Error {
  readonly reason: ChoiceActionReason;

  constructor(reason: ChoiceActionReason) {
    super(`choice action ${reason}`);
    this.name = 'ChoiceActionError';
    this.reason = reason;
  }
}

const FAILURE_KEYS: Record<ChoiceActionReason, string> = {
  failed: 'ui.chat.choice.action_failed',
  conflict: 'ui.chat.choice.action_conflict',
  unavailable: 'ui.chat.choice.action_unavailable',
  service_choose: 'ui.chat.choice.action_service_choose',
  service_none: 'ui.chat.choice.action_service_none',
};

/** The Schema key of the words that say why an action did not complete. A revision conflict (409) is its own case. */
export function choiceActionFailureKey(error: unknown): string {
  if (error instanceof ChoiceActionError) return FAILURE_KEYS[error.reason];
  if (error instanceof HarnessBrowserError && error.status === 409) return FAILURE_KEYS.conflict;
  return FAILURE_KEYS.failed;
}

export type ChoiceAnswerOutcome =
  /** `send` ran; whether the server took the answer is the caller's own signal. */
  | { status: 'sent' }
  /** The function selection was not saved; nothing was sent. */
  | { status: 'action_failed'; failureKey: string }
  /** The person moved to another session while the selection was saved; nothing was sent. */
  | { status: 'left' };

/**
 * The order of an answer: the option's action first, the answer after it. Without an action the answer goes at once.
 * An error from `send` itself is not caught here.
 */
export async function runChoiceAnswer(
  action: ChoiceAction | undefined,
  run: {
    /** Saves the function selection the action asks for; rejects when it cannot. */
    apply: (action: ChoiceAction) => Promise<void>;
    /** Whether the person is still in the session the card belongs to. */
    stillHere: () => boolean;
    send: () => Promise<void>;
  },
): Promise<ChoiceAnswerOutcome> {
  if (action) {
    try {
      await run.apply(action);
    } catch (error) {
      return { status: 'action_failed', failureKey: choiceActionFailureKey(error) };
    }
    if (!run.stillHere()) return { status: 'left' };
  }
  await run.send();
  return { status: 'sent' };
}
