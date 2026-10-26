import type { ProductSchema } from '@/lib/product-schema';
import { withRequestReference } from '@/lib/problem-reference';

export interface RecoveryEligibility {
  sourceTaskId: string;
  eligible: boolean;
  receiptDigest?: string;
  sessionId?: string;
  capability?: string;
  idempotencyKey?: string;
  recoveryTaskId?: string;
}

export interface RecoveryGrant {
  preparedInputId: string;
  requestId: string;
  key: string;
  sessionId: string;
  offeringId: string;
  capability: string;
  prompt?: string;
  grantApproved: boolean;
  approvedByPolicy: boolean;
}

export type PreparedRecovery = { kind: 'existing_task'; taskId: string } | { kind: 'prepared'; grant: RecoveryGrant } | { kind: 'denied' };

const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const str = (value: unknown): string => typeof value === 'string' ? value : '';

export function recoveryRoute(schema: ProductSchema, taskId: string): string | null {
  return schema.runtime.http.routes.task_recovery?.replace('{taskID}', encodeURIComponent(taskId)) ?? null;
}

export function parseRecoveryEligibility(raw: unknown, taskId: string): RecoveryEligibility | null {
  if (!record(raw) || raw.source_task_id !== taskId || typeof raw.eligible !== 'boolean') return null;
  const result: RecoveryEligibility = { sourceTaskId: taskId, eligible: raw.eligible };
  if (!raw.eligible) return result;
  const receiptDigest = str(raw.receipt_digest);
  const sessionId = str(raw.session_id);
  const capability = str(raw.capability);
  const idempotencyKey = str(raw.idempotency_key);
  if (!receiptDigest || !sessionId || !capability || !idempotencyKey) return null;
  return {
    ...result, receiptDigest, sessionId, capability, idempotencyKey,
    ...(str(raw.recovery_task_id) ? { recoveryTaskId: str(raw.recovery_task_id) } : {}),
  };
}

async function responseProblem(response: Response, fallback: string): Promise<Error> {
  const body = await response.json().catch(() => null);
  return new Error(withRequestReference(record(body) && typeof body.detail === 'string' && body.detail ? body.detail : fallback, body));
}

export async function fetchRecoveryEligibility(schema: ProductSchema, taskId: string): Promise<RecoveryEligibility> {
  const route = recoveryRoute(schema, taskId);
  if (!route) throw new Error('recovery route unavailable');
  const response = await fetch(route);
  if (!response.ok) throw await responseProblem(response, `recovery ${response.status}`);
  const eligibility = parseRecoveryEligibility(await response.json().catch(() => null), taskId);
  if (!eligibility) throw new Error('recovery response malformed');
  return eligibility;
}

export async function prepareRecovery(schema: ProductSchema, taskId: string, eligibility: RecoveryEligibility, prompt = '', isActive: () => boolean = () => true): Promise<PreparedRecovery> {
  const route = schema.runtime.http.routes.task_recovery_prepare?.replace('{taskID}', encodeURIComponent(taskId));
  if (!route || !eligibility.eligible || !eligibility.receiptDigest || !eligibility.sessionId || !eligibility.capability || !eligibility.idempotencyKey) {
    throw new Error('recovery is unavailable');
  }
  if (!isActive()) throw new Error('recovery panel inactive');
  const response = await fetch(route, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ receipt_digest: eligibility.receiptDigest }),
  });
  if (!response.ok) throw await responseProblem(response, `recovery prepare ${response.status}`);
  const body = await response.json().catch(() => null);
  if (!record(body)) throw new Error('recovery prepare response malformed');
  const recovery = parseRecoveryEligibility(body.recovery, taskId);
  if (!recovery?.eligible || recovery.receiptDigest !== eligibility.receiptDigest) throw new Error('recovery receipt changed');
  const existingTaskId = recovery.recoveryTaskId;
  if (existingTaskId) return { kind: 'existing_task', taskId: existingTaskId };
  const preparedInputId = str(body.prepared_input_id);
  const capability = str(body.capability);
  const sessionId = str(body.session_id);
  const offeringId = str(body.offering_id);
  if (response.status !== 201 || !preparedInputId || !capability || !sessionId || !offeringId
    || sessionId !== eligibility.sessionId || capability !== eligibility.capability) throw new Error('recovery prepare response malformed');
  if (!isActive()) throw new Error('recovery panel inactive');
  const grantResponse = await fetch(schema.runtime.http.routes.task_grants, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': eligibility.idempotencyKey },
    body: JSON.stringify({ prepared_input_id: preparedInputId }),
  });
  if (!grantResponse.ok) throw await responseProblem(grantResponse, `task grant ${grantResponse.status}`);
  const grantBody = await grantResponse.json().catch(() => null);
  if (!record(grantBody) || !str(grantBody.request_id) || !str(grantBody.status)) throw new Error('task grant response malformed');
  if (grantBody.status === 'denied') return { kind: 'denied' };
  if (grantBody.status !== 'approved' && grantBody.status !== 'pending') throw new Error('task grant status unsupported');
  const approvedByPolicy = grantBody.approved_by_policy === true;
  return {
    kind: 'prepared',
    grant: {
      preparedInputId, requestId: str(grantBody.request_id), key: eligibility.idempotencyKey,
      sessionId, offeringId, capability, prompt,
      grantApproved: grantBody.status === 'approved', approvedByPolicy,
    },
  };
}
