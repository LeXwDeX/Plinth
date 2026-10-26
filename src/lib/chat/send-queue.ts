import type { ChatAttachmentClaim, ChatMentionClaim } from '@/lib/chat/attachments';

/**
 * A message the person sent while the Agent was still on the previous turn.
 * It waits in the box above the composer ("排队中") and goes out, as the one
 * next turn, when the running turn ends. What it should act on (pictures, the
 * claims that prove their origin, the object on the stage) is captured when it
 * is queued, so a later change of the stage does not change what was asked.
 */
export interface QueuedTurn {
  id: string;
  text: string;
  images: string[];
  claims: ChatAttachmentClaim[];
  selection: ChatAttachmentClaim[];
  /** The `@` references of the text, resolved by the server when the turn runs. */
  mentions?: ChatMentionClaim[];
  /** The source the person chose when the turn was queued (#266): the canvas when `selection` is its snapshot. */
  inputSource?: string;
}

export type QueueBySession = Readonly<Record<string, readonly QueuedTurn[]>>;

/** Queued turns of one session, oldest first. */
export function queuedFor(queue: QueueBySession, sessionId: string | null): readonly QueuedTurn[] {
  return sessionId ? queue[sessionId] ?? [] : [];
}

export function enqueueTurn(queue: QueueBySession, sessionId: string, turn: QueuedTurn): QueueBySession {
  return { ...queue, [sessionId]: [...(queue[sessionId] ?? []), turn] };
}

/** The cancelled turn is simply dropped: it was never sent, so nothing is undone. */
export function cancelQueuedTurn(queue: QueueBySession, sessionId: string, id: string): QueueBySession {
  const rest = (queue[sessionId] ?? []).filter((turn) => turn.id !== id);
  const { [sessionId]: _removed, ...others } = queue;
  return rest.length === 0 ? others : { ...others, [sessionId]: rest };
}

/** Takes the oldest queued turn of the session; `turn` is null when none waits. */
export function takeQueuedTurn(queue: QueueBySession, sessionId: string): { turn: QueuedTurn | null; queue: QueueBySession } {
  const [turn, ...rest] = queue[sessionId] ?? [];
  if (!turn) return { turn: null, queue };
  const { [sessionId]: _taken, ...others } = queue;
  return { turn, queue: rest.length === 0 ? others : { ...others, [sessionId]: rest } };
}
