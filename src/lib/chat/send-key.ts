/**
 * #665: every send that starts a turn carries an Idempotency-Key. When the request reached the server but its answer
 * was lost, the page thinks the send failed and puts the words back; sending the same request again then reuses the
 * key, and the server answers the turn the first request started instead of starting a second, paid one. A new send
 * (anything that differs from the request whose outcome is unknown) gets a new key.
 *
 * The send whose outcome is unknown is kept per session, so a send in another session does not replace it, and in the
 * tab's sessionStorage, so a reload keeps it. It is kept for UNCERTAIN_SEND_MAX_AGE_MS at most (the server keeps a key
 * for 24 hours), for MAX_UNCERTAIN_SENDS sessions at most, and goes once a send of its session is answered.
 */

export function newSendKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

/** A send whose outcome the page does not know: the request it carried, the key it went out with and when. */
export interface UncertainSend {
  signature: string;
  key: string;
  /** When it went out (ms since the epoch). */
  at: number;
  /**
   * A queued message's words put back in the box are sent again as a typed message: that request (the same words and
   * options, with no queued item) also reuses the key.
   */
  typedSignature?: string;
}

/** Per session id, its send whose outcome the page does not know. */
export type UncertainSends = Readonly<Record<string, UncertainSend>>;

/** The sessionStorage entry the uncertain sends are kept in. */
export const UNCERTAIN_SENDS_KEY = 'plinth.uncertainSends';
/** How long an uncertain send's key is reused: well inside the 24 hours the server keeps a key. */
export const UNCERTAIN_SEND_MAX_AGE_MS = 60 * 60 * 1000;
/** How many sessions keep an uncertain send; the oldest goes first. */
export const MAX_UNCERTAIN_SENDS = 20;
// A record from a clock this far ahead is taken for corrupt.
const CLOCK_SLACK_MS = 60 * 1000;

/** What else a request depends on besides its body options. */
export interface SendContext {
  /** The UI locale: the server's request fingerprint includes it, so a send in another locale is another request. */
  locale?: string;
  /** A queued message's id: two queued messages with the same words are still two requests. */
  queuedId?: string;
}

/** The identity of one request: the session, the words, every option the request body is built from and its context. */
export function sendSignature(sessionId: string, text: string, options: unknown, context: SendContext = {}): string {
  return JSON.stringify([sessionId, text, options, context.locale ?? '', context.queuedId ?? '']);
}

function live(send: UncertainSend | null | undefined, now: number): send is UncertainSend {
  return !!send && now - send.at <= UNCERTAIN_SEND_MAX_AGE_MS && send.at <= now + CLOCK_SLACK_MS;
}

/**
 * The key a send goes out with: the uncertain send's key when this is the same request again (and it is not too old),
 * otherwise `preferred` (a queued message's own key) or a fresh one.
 */
export function sendKeyFor(
  uncertain: UncertainSend | null | undefined, signature: string, preferred?: string, now = Date.now(),
): string {
  if (live(uncertain, now) && (uncertain.signature === signature || uncertain.typedSignature === signature)) return uncertain.key;
  return preferred || newSendKey();
}

/** The stored uncertain sends, without malformed, too old or surplus ones. */
export function parseUncertainSends(raw: string | null, now: number): UncertainSends {
  if (!raw) return {};
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  const sends: Record<string, UncertainSend> = {};
  for (const [sessionId, item] of Object.entries(value as Record<string, unknown>)) {
    if (!item || typeof item !== 'object') continue;
    const { signature, key, at, typedSignature } = item as Record<string, unknown>;
    if (typeof signature !== 'string' || typeof key !== 'string' || key === '' || typeof at !== 'number') continue;
    const send: UncertainSend = { signature, key, at, ...(typeof typedSignature === 'string' ? { typedSignature } : {}) };
    if (live(send, now)) sends[sessionId] = send;
  }
  return bounded(sends);
}

function bounded(sends: Record<string, UncertainSend>): UncertainSends {
  const entries = Object.entries(sends);
  if (entries.length <= MAX_UNCERTAIN_SENDS) return sends;
  return Object.fromEntries(entries.sort(([, a], [, b]) => b.at - a.at).slice(0, MAX_UNCERTAIN_SENDS));
}

/** The session's uncertain send is now `send` (the request going out); the oldest sessions go past the bound. */
export function rememberUncertainSend(sends: UncertainSends, sessionId: string, send: UncertainSend): UncertainSends {
  return bounded({ ...sends, [sessionId]: send });
}

/** The session's uncertain send goes, when it is still the one that went out under `key` (any one without a key). */
export function forgetUncertainSend(sends: UncertainSends, sessionId: string, key?: string): UncertainSends {
  const current = sends[sessionId];
  if (!current || (key !== undefined && current.key !== key)) return sends;
  const { [sessionId]: _gone, ...rest } = sends;
  return rest;
}

type SessionStore = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

/** The uncertain sends of one tab: kept in `storage` when it can be used, otherwise for the page only. */
export interface UncertainSendStore {
  get: (sessionId: string) => UncertainSend | null;
  remember: (sessionId: string, send: UncertainSend) => void;
  forget: (sessionId: string, key?: string) => void;
}

export function createUncertainSendStore(
  storage: () => SessionStore | undefined, now: () => number = () => Date.now(),
): UncertainSendStore {
  let memory: UncertainSends = {};
  const read = (): UncertainSends => {
    try {
      const store = storage();
      if (store) memory = parseUncertainSends(store.getItem(UNCERTAIN_SENDS_KEY), now());
    } catch {
      // No storage (a private window, blocked site data): the page's own copy is kept.
    }
    return memory;
  };
  const write = (sends: UncertainSends) => {
    memory = sends;
    try {
      const store = storage();
      if (!store) return;
      if (Object.keys(sends).length === 0) store.removeItem(UNCERTAIN_SENDS_KEY);
      else store.setItem(UNCERTAIN_SENDS_KEY, JSON.stringify(sends));
    } catch {
      // A full or refused storage: the page's own copy is kept.
    }
  };
  return {
    get: (sessionId) => {
      const send = read()[sessionId];
      return live(send, now()) ? send : null;
    },
    remember: (sessionId, send) => write(rememberUncertainSend(read(), sessionId, send)),
    forget: (sessionId, key) => {
      const sends = read();
      const next = forgetUncertainSend(sends, sessionId, key);
      if (next !== sends) write(next);
    },
  };
}

/** One request to send under an Idempotency-Key. */
export interface KeyedSend {
  sessionId: string;
  signature: string;
  /** See UncertainSend.typedSignature. */
  typedSignature?: string;
  /** A queued message's own key, used when no uncertain send matches. */
  preferred?: string;
}

/**
 * Sends `request` under its key, keeping it as the session's uncertain send until the server answers it. When the
 * server refuses the key itself (`refused`: the key was used for another request, for example in another locale, or
 * it is malformed), the request the person meant to send has not started a turn: the uncertain send goes and the
 * request is sent once more under a fresh key. A second refusal is thrown, with the uncertain send gone.
 */
export async function sendUnderKey<T>(
  store: UncertainSendStore,
  request: KeyedSend,
  start: (key: string) => Promise<T>,
  refused: (error: unknown) => boolean,
  now: () => number = () => Date.now(),
): Promise<T> {
  const { sessionId, signature, typedSignature } = request;
  const attempt = async (key: string) => {
    store.remember(sessionId, { signature, key, at: now(), ...(typedSignature ? { typedSignature } : {}) });
    const result = await start(key);
    store.forget(sessionId, key);
    return result;
  };
  const first = sendKeyFor(store.get(sessionId), signature, request.preferred, now());
  try {
    return await attempt(first);
  } catch (error) {
    if (!refused(error)) throw error;
    store.forget(sessionId, first);
  }
  const fresh = newSendKey();
  try {
    return await attempt(fresh);
  } catch (error) {
    if (refused(error)) store.forget(sessionId, fresh);
    throw error;
  }
}
