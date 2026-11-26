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
 * server still runs a turn), not while the newest page has a reply still pending (a turn this page did not start),
 * not while another tab holds the session's send (it claimed a queued turn and its turn runs), and not until a read
 * made after another tab's claim or send (awaitingRead): this tab's messages cannot show that turn before then.
 */
export function queueMayDrain(input: {
  busy: boolean;
  historyLoading: boolean;
  messages: readonly ChatMessage[] | undefined;
  streaming: boolean;
  foreignHold?: boolean;
  awaitingRead?: boolean;
}): boolean {
  return !input.busy && !input.historyLoading && !input.foreignHold && !input.awaitingRead &&
    !sessionTurnRunning(input.messages, input.streaming);
}

/** #665: the stored queue of every tab of the browser. */
export const SEND_QUEUE_KEY = 'plinth.queuedTurns';
/** #665: the stored hold of one session's send (see SendHold). */
export const SEND_HOLD_PREFIX = 'plinth.queuedTurns.hold:';
/**
 * #665: how long a hold lasts without a heartbeat. The holding tab refreshes it every third of this while its turn
 * runs, and again when it is shown (a hidden tab's timers are slowed down; its Web Lock on the turn, where the browser
 * has one, still keeps other tabs' claims out); a tab that closed or crashed lets it lapse, and the other tabs drain.
 */
export const SEND_HOLD_MS = 30_000;

/**
 * #665: a tab that claimed a queued turn of a session holds the session's send until the turn it sent ends, so another
 * tab, whose messages cannot show that turn yet, does not send the next queued turn beside it.
 */
export interface SendHold {
  owner: string;
  expiresAt: number;
}

export function sendHoldKey(sessionId: string): string {
  return SEND_HOLD_PREFIX + sessionId;
}

/** #665: the time a hold written by one tab may still be on its way when another reads it. */
const HOLD_CLOCK_SLACK_MS = 1_000;

/**
 * #665: a stored hold, or null when it cannot be read. With `now`, a hold that ends later than any tab writes one (now
 * plus SEND_HOLD_MS, with a second for the time between the write and this read) is a corrupt value, not a hold: it is
 * ignored, so it can neither keep the send held for ever nor put a wait beyond what a timer can count.
 */
export function parseSendHold(raw: string | null, now?: number): SendHold | null {
  try {
    const value: unknown = JSON.parse(raw ?? 'null');
    if (!value || typeof value !== 'object') return null;
    const { owner, expiresAt } = value as Record<string, unknown>;
    if (typeof owner !== 'string' || typeof expiresAt !== 'number' || !Number.isFinite(expiresAt)) return null;
    if (now !== undefined && expiresAt > now + SEND_HOLD_MS + HOLD_CLOCK_SLACK_MS) return null;
    return { owner, expiresAt };
  } catch {
    return null;
  }
}

/** #665: the shortest wait the drain sets, so a hold that lapses now does not make it run again in a tight loop. */
export const MIN_DRAIN_WAIT_MS = 50;
/** The longest delay setTimeout can count; a longer one fires at once. */
const MAX_TIMER_MS = 2 ** 31 - 1;

/** #665: a timer delay kept between MIN_DRAIN_WAIT_MS and what setTimeout can count. */
export function drainWait(milliseconds: number): number {
  if (!Number.isFinite(milliseconds)) return MAX_TIMER_MS;
  return Math.min(Math.max(milliseconds, MIN_DRAIN_WAIT_MS), MAX_TIMER_MS);
}

/** #665: another tab's hold that has not lapsed; null when the send is free or held by this tab. */
export function foreignHold(hold: SendHold | null, self: string, now: number): SendHold | null {
  return hold && hold.owner !== self && hold.expiresAt > now ? hold : null;
}

export function parseQueue(raw: string | null): QueueBySession {
  try {
    const parsed: unknown = JSON.parse(raw ?? '{}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed as Record<string, unknown>).filter(
      (entry): entry is [string, QueueBySession[string]] => Array.isArray(entry[1]) && entry[1].length > 0,
    ));
  } catch {
    return {};
  }
}

export function writeQueue(storage: Pick<Storage, 'setItem' | 'removeItem'>, queue: QueueBySession): void {
  if (Object.keys(queue).length === 0) storage.removeItem(SEND_QUEUE_KEY);
  else storage.setItem(SEND_QUEUE_KEY, JSON.stringify(queue));
}

/**
 * #665: claims a queued turn in the stored queue for the tab `owner`: refused while another tab holds the session's
 * send (`held`) or when the turn is no longer queued (`gone`: sent or cancelled elsewhere). A claim takes the turn out
 * and sets this tab's hold in one step; run it under a Web Lock where the browser has one, so no other tab reads
 * between. Without one the hold is read back: of two tabs writing at once, only the last writer sends.
 */
export function claimStoredTurn(
  storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>,
  sessionId: string,
  id: string,
  owner: string,
  now: number,
): 'claimed' | 'held' | 'gone' {
  const key = sendHoldKey(sessionId);
  if (foreignHold(parseSendHold(storage.getItem(key), now), owner, now)) return 'held';
  const queue = parseQueue(storage.getItem(SEND_QUEUE_KEY));
  if (!(queue[sessionId] ?? []).some((turn) => turn.id === id)) return 'gone';
  writeQueue(storage, cancelQueuedTurn(queue, sessionId, id));
  storage.setItem(key, JSON.stringify({ owner, expiresAt: now + SEND_HOLD_MS } satisfies SendHold));
  return parseSendHold(storage.getItem(key), now)?.owner === owner ? 'claimed' : 'held';
}

/**
 * #665: what the drain of the active session does next, once a turn waits and the page is free: wait for another tab's
 * hold to end (`wait`, a delay to look again), ask for the read it waits for (`read`), look again later when that read
 * failed or was cut short (`wait`), or claim the turn and send it (`claim`). `forcedRead` is the read the drain last
 * asked for.
 */
export type DrainStep = { kind: 'wait'; delay: number } | { kind: 'read'; required: number } | { kind: 'claim' };

export function nextDrainStep(input: {
  hold: SendHold | null;
  now: number;
  required: number | undefined;
  forcedRead: number | null;
  pollMilliseconds: number;
}): DrainStep {
  if (input.hold) return { kind: 'wait', delay: drainWait(input.hold.expiresAt - input.now + MIN_DRAIN_WAIT_MS) };
  if (input.required !== undefined) {
    if (input.forcedRead !== input.required) return { kind: 'read', required: input.required };
    return { kind: 'wait', delay: drainWait(input.pollMilliseconds) };
  }
  return { kind: 'claim' };
}

/**
 * #665: the sessions whose hold this tab gives up now: every one whose turn is over. A session's turn runs while this
 * page is still sending it, or while it is the session in view and its reply is still running; a session the person
 * left is released too, so another tab, which reads the session first, may go on with its queue.
 */
export function holdsToRelease(
  held: Iterable<string>,
  input: { sending: string | null; activeSessionId: string | null; activeTurnRunning: boolean },
): string[] {
  return [...held].filter((sessionId) => sessionId !== input.sending &&
    !(sessionId === input.activeSessionId && input.activeTurnRunning));
}

/**
 * #665: the reads a session's drain waits for, by session: the recovery generation whose read must complete first.
 * Set when this tab's claim fails or another tab's hold of the session changes; cleared by that read.
 */
export type DrainGate = Readonly<Record<string, number>>;

export function holdDrainForRead(gate: DrainGate, sessionId: string, generation: number): DrainGate {
  return gate[sessionId] !== undefined && gate[sessionId] >= generation ? gate : { ...gate, [sessionId]: generation };
}

/** A read of recovery `generation` completed: a wait for that generation or an earlier one is over. */
export function readCompleted(gate: DrainGate, sessionId: string, generation: number): DrainGate {
  const required = gate[sessionId];
  if (required === undefined || generation < required) return gate;
  const { [sessionId]: _done, ...rest } = gate;
  return rest;
}

export function drainAwaitsRead(gate: DrainGate, sessionId: string): boolean {
  return gate[sessionId] !== undefined;
}

/**
 * #665: per page load, at most this many asks about unknown sessions may go unanswered (offline, a server error, a
 * gateway's own 404); the rest wait for a later load. Answered asks (the session is there, or is gone) do not count, so
 * sessions that are still there do not keep a deleted one behind them for ever; MAX_QUEUE_PROBE_TOTAL bounds them.
 */
export const MAX_QUEUE_PROBES = 5;
/** #665: at most this many unknown sessions are asked about per page load, answered or not. */
export const MAX_QUEUE_PROBE_TOTAL = 4 * MAX_QUEUE_PROBES;

/**
 * #665: whether a failed read of a session says the server itself does not have it: a 404 or 410 answered with the
 * API's own error body. A 404 from a gateway or proxy (during a deploy, say) has no such body and proves nothing.
 */
export function sessionGone(error: unknown): boolean {
  const { status, serverAnswered } = (error ?? {}) as { status?: unknown; serverAnswered?: unknown };
  return (status === 404 || status === 410) && serverAnswered === true;
}

/**
 * #665: forgets the queues of sessions the server no longer gives this account (deleted in another tab or device, or
 * another account's after a change of login, which the server answers as not found), so their words do not stay in
 * the browser. A session the page knows is kept; one the server cannot be asked about (offline, a server error, a
 * gateway's own 404) is kept until a later load. Sessions are asked about one at a time, until MAX_QUEUE_PROBES asks
 * went unanswered or MAX_QUEUE_PROBE_TOTAL were made, and an aborted signal (the page unmounted) stops the rest.
 */
export async function pruneQueuedSessions(
  sessionIds: readonly string[],
  known: ReadonlySet<string>,
  getSession: (sessionId: string, signal?: AbortSignal) => Promise<unknown>,
  forget: (sessionId: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  let probes = 0;
  let unanswered = 0;
  for (const sessionId of sessionIds) {
    if (known.has(sessionId)) continue;
    if (signal?.aborted || unanswered >= MAX_QUEUE_PROBES || probes >= MAX_QUEUE_PROBE_TOTAL) return;
    probes += 1;
    try {
      await getSession(sessionId, signal);
    } catch (error) {
      if (signal?.aborted) return;
      if (sessionGone(error)) forget(sessionId);
      else unanswered += 1;
    }
  }
}
