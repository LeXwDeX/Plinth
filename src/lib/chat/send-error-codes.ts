/**
 * #665: the problem codes the chat send endpoint answers an Idempotency-Key refusal with. The Product Schema declares
 * the texts of these refusals (`error.idempotency_*`) but not their codes, which are the server's wire contract
 * (`internal/transport/httpapi`); they are kept here, in one place, instead of as literals where they are read.
 */

/** The key was used for another request (its stored fingerprint differs from this one). */
export const IDEMPOTENCY_CONFLICT_CODE = 'idempotency_conflict';

/**
 * The key itself was refused (for example over-long). The server names it `idempotency_key_required` today and may
 * rename it `idempotency_key_invalid`; both are read until only one is sent.
 */
export const IDEMPOTENCY_KEY_REFUSED_CODES: readonly string[] = ['idempotency_key_required', 'idempotency_key_invalid'];

/** Every code that means the server refused the send because of its key, never because of what it asked. */
export const IDEMPOTENCY_REFUSAL_CODES: readonly string[] = [IDEMPOTENCY_CONFLICT_CODE, ...IDEMPOTENCY_KEY_REFUSED_CODES];

/** Whether the error is the server's refusal of the send's Idempotency-Key (an error carrying one of those codes). */
export function idempotencyRefused(error: unknown): boolean {
  const code = error instanceof Error ? (error as { code?: unknown }).code : undefined;
  return typeof code === 'string' && IDEMPOTENCY_REFUSAL_CODES.includes(code);
}
