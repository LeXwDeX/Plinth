import type { ChatAttachmentClaim, ChatMentionClaim } from '@/lib/chat/attachments';
import type { MultiviewTurnRequest } from '@/lib/chat/browser-client';
import type { AttachmentPurpose } from '@/lib/chat/attachment-purpose';
import { sessionTurnRunning } from '@/lib/chat/turn-running';
import type { ChatMessage } from '@/store/chat-store';

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
  /** The per-picture purposes the person set before the turn was queued. */
  attachmentPurposes?: { imageSlot: number; purpose: AttachmentPurpose }[];
  /** The source the person chose when the turn was queued (#266): the canvas when `selection` is its snapshot. */
  inputSource?: string;
  /** #244: the frozen selection mask that was uploaded with the queued canvas turn. */
  selectionMask?: { maskAssetId: string };
  /** #65: the structured multiview request the turn was queued with. */
  multiview?: MultiviewTurnRequest;
  /** #154: the one-turn style-lock exemption the turn was queued with. */
  styleLockSkipped?: boolean;
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

/**
 * #665: whether the oldest queued turn may go out now. Not while this page is busy with a turn of its own, not before
 * the session's messages were read (after a reload the stream buffer is empty, so only the read can tell that the
 * server still runs a turn), and not while the newest page has a reply still pending (a turn this page did not start).
 */
export function queueMayDrain(input: {
  busy: boolean;
  historyLoading: boolean;
  messages: readonly ChatMessage[] | undefined;
  streaming: boolean;
}): boolean {
  return !input.busy && !input.historyLoading && !sessionTurnRunning(input.messages, input.streaming);
}

/**
 * #665: forgets the queues of sessions the server no longer gives this account (deleted in another tab or device, or
 * another account's after a change of login), so their words do not stay in the browser. A session the page knows
 * is kept; one the server cannot be asked about (offline, a server error) is kept until a later load.
 */
export async function pruneQueuedSessions(
  sessionIds: readonly string[],
  known: ReadonlySet<string>,
  getSession: (sessionId: string) => Promise<unknown>,
  forget: (sessionId: string) => void,
): Promise<void> {
  for (const sessionId of sessionIds) {
    if (known.has(sessionId)) continue;
    try {
      await getSession(sessionId);
    } catch (error) {
      const status = (error as { status?: unknown } | null)?.status;
      if (status === 403 || status === 404 || status === 410) forget(sessionId);
    }
  }
}
