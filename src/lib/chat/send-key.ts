/**
 * #665: every send that starts a turn carries an Idempotency-Key. When the request reached the server but its answer
 * was lost, the page thinks the send failed and puts the words back; sending the same request again then reuses the
 * key, and the server answers the turn the first request started instead of starting a second, paid one. A new send
 * (anything that differs from the request whose outcome is unknown) gets a new key.
 */

export function newSendKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
}

/** A send whose outcome the page does not know: the request it carried and the key it went out with. */
export interface UncertainSend {
  signature: string;
  key: string;
}

/** The identity of one request: the session, the words and every option the request body is built from. */
export function sendSignature(sessionId: string, text: string, options: unknown): string {
  return JSON.stringify([sessionId, text, options]);
}

/**
 * The key a send goes out with: the uncertain send's key when this is the same request again, otherwise `preferred`
 * (a queued message's own key) or a fresh one.
 */
export function sendKeyFor(uncertain: UncertainSend | null, signature: string, preferred?: string): string {
  if (uncertain && uncertain.signature === signature) return uncertain.key;
  return preferred || newSendKey();
}
