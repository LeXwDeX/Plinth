import { schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { withRequestReference } from '@/lib/problem-reference';

// Upstream result refetch (#295): a failed task whose provider already
// finished (and charged) can have its result fetched again from the accepted
// provider job. The server decides eligibility; nothing is resubmitted or
// charged, and the browser never names a job, URL or offering.

/** `delivered`: a refetch already succeeded (#330); `eligible` is then false and `refetchTaskId` names it. */
export type RefetchReason = 'not_applicable' | 'not_supported' | 'expired' | 'provider_unavailable' | 'repeated_failure' | 'delivered';

export interface RefetchEligibility {
  sourceTaskId: string;
  eligible: boolean;
  reason?: RefetchReason;
  refetchTaskId?: string;
  refetchState?: string;
  /** repeated_failure only: the public failure class a refetch repeated; its Schema text is the cause. */
  failureClass?: string;
  /** The credits the provider already charged before the delivery failed (#330), when metered. */
  chargedCredits?: number;
  /** Earlier refetches of this task that failed or were canceled. */
  failedRefetches?: number;
}

export interface RefetchStart {
  eligibility: RefetchEligibility;
  taskId?: string;
  replayed: boolean;
}

const REASONS: readonly RefetchReason[] = ['not_applicable', 'not_supported', 'expired', 'provider_unavailable', 'repeated_failure', 'delivered'];
const FAILURE_CLASS = /^[a-z_]{1,64}$/;
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const str = (value: unknown): string => typeof value === 'string' ? value : '';
const positive = (value: unknown): boolean => typeof value === 'number' && Number.isInteger(value) && value > 0;

export function refetchRoute(schema: ProductSchema, taskId: string): string | null {
  return schema.runtime.http.routes.task_refetch?.replace('{taskID}', encodeURIComponent(taskId)) ?? null;
}

export function parseRefetchEligibility(raw: unknown, taskId: string): RefetchEligibility | null {
  if (!record(raw) || raw.source_task_id !== taskId || typeof raw.eligible !== 'boolean') return null;
  const reason = str(raw.reason);
  if (reason && !REASONS.includes(reason as RefetchReason)) return null;
  if (!raw.eligible && !reason) return null;
  return {
    sourceTaskId: taskId,
    eligible: raw.eligible,
    ...(reason ? { reason: reason as RefetchReason } : {}),
    ...(str(raw.refetch_task_id) ? { refetchTaskId: str(raw.refetch_task_id) } : {}),
    ...(str(raw.refetch_state) ? { refetchState: str(raw.refetch_state) } : {}),
    ...(FAILURE_CLASS.test(str(raw.failure_class)) ? { failureClass: str(raw.failure_class) } : {}),
    ...(positive(raw.charged_credits) ? { chargedCredits: raw.charged_credits as number } : {}),
    ...(positive(raw.failed_refetches) ? { failedRefetches: raw.failed_refetches as number } : {}),
  };
}

/**
 * The Schema reason a failed task's result cannot be fetched again. For a repeated failure the
 * cause is the Schema text of its public failure class (never an internal detail).
 */
export function refetchUnavailableText(schema: ProductSchema, view: RefetchEligibility): string {
  const key = `ui.task.refetch.unavailable.${view.reason}`;
  if (view.reason !== 'repeated_failure') return schemaText(schema, key);
  const cause = (view.failureClass && schemaText(schema, `failure.${view.failureClass}`)) || schemaText(schema, 'ui.workspace.tasks.error_title');
  return schemaMessage(schema, key, { cause });
}

async function problem(response: Response, fallback: string): Promise<Error> {
  const body = await response.json().catch(() => null);
  return new Error(withRequestReference(record(body) && typeof body.detail === 'string' && body.detail ? body.detail : fallback, body));
}

export async function fetchRefetchEligibility(schema: ProductSchema, taskId: string): Promise<RefetchEligibility> {
  const route = refetchRoute(schema, taskId);
  if (!route) throw new Error('refetch route unavailable');
  const response = await fetch(route);
  if (!response.ok) throw await problem(response, `refetch ${response.status}`);
  const eligibility = parseRefetchEligibility(await response.json().catch(() => null), taskId);
  if (!eligibility) throw new Error('refetch response malformed');
  return eligibility;
}

// startRefetch asks the server to fetch the result again. A 409 is a definite
// "cannot" with its reason; a repeat returns the refetch already running or done.
export async function startRefetch(schema: ProductSchema, taskId: string): Promise<RefetchStart> {
  const route = refetchRoute(schema, taskId);
  if (!route) throw new Error('refetch route unavailable');
  const response = await fetch(route, { method: 'POST' });
  if (!response.ok && response.status !== 409) throw await problem(response, `refetch ${response.status}`);
  const body = await response.json().catch(() => null);
  const eligibility = record(body) ? parseRefetchEligibility(body.refetch, taskId) : null;
  if (!record(body) || !eligibility) throw new Error('refetch response malformed');
  const newTaskId = str(body.task_id);
  if (response.status === 409) return { eligibility, replayed: false };
  if (!newTaskId) throw new Error('refetch response malformed');
  return { eligibility, taskId: newTaskId, replayed: body.replayed === true };
}
