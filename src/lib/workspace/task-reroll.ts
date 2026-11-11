import type { ProductSchema } from '@/lib/product-schema';
import { withRequestReference } from '@/lib/problem-reference';
import { parseRetryOutcome, type RetryOutcome } from '@/lib/workspace/task-retry';

/**
 * 「再来一个」(#242): the server replays the succeeded task's frozen input with
 * fresh Schema-declared seeds. Every request carries an Idempotency-Key, so a
 * double click returns the same task instead of charging twice.
 */

export function taskRerollKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function rerollRoute(schema: ProductSchema, taskId: string): string | null {
  const template = schema.runtime.http.routes.task_reroll;
  return template ? template.replace('{taskID}', encodeURIComponent(taskId)) : null;
}

/** The server refuses a reroll (its detail is already Schema text). */
export class RerollRefused extends Error {}

/** Request one more of a succeeded task; `key` is reused when a decision is pending. */
export async function requestReroll(
  schema: ProductSchema,
  taskId: string,
  key: string,
  fallback: string,
): Promise<RetryOutcome> {
  const route = rerollRoute(schema, taskId);
  if (!route) throw new Error('reroll route unavailable');
  const response = await fetch(route, { method: 'POST', headers: { 'Idempotency-Key': key } });
  if (!response.ok) {
    const problem: { detail?: unknown } = await response.json().catch(() => ({}));
    const detail = withRequestReference(
      typeof problem.detail === 'string' && problem.detail !== '' ? problem.detail : fallback,
      problem,
    );
    if (response.status === 409 || response.status === 404 || response.status === 400) throw new RerollRefused(detail);
    throw new Error(`reroll ${response.status}`);
  }
  const outcome = parseRetryOutcome(await response.json().catch(() => null));
  if (!outcome) throw new Error('reroll response malformed');
  return outcome;
}