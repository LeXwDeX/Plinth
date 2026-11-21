import type { ProductSchema } from '@/lib/product-schema';

/**
 * #330 §6: one asset session's live 3D step standing (an external MCP asset,
 * #332). The asset is the chat session; the cap and the person's grant only
 * exist in the cap mode, where the browser offers "give it more steps".
 */
export interface SessionSteps {
  mode: string;
  used: number;
  cap?: number;
  extra?: number;
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

/** The route of one session, or '' when the deployment declares none. */
export function sessionStepsRoute(schema: ProductSchema, sessionID: string): string {
  const template = schema.runtime.http.routes.chat_session_steps;
  if (!template || !sessionID) return '';
  return template.replace('{sessionID}', encodeURIComponent(sessionID));
}

/** The asset's limit under the cap mode: the owner's cap plus the grant. */
export function sessionStepsLimit(steps: SessionSteps | null): number {
  if (!steps || !steps.cap) return 0;
  return steps.cap + (steps.extra ?? 0);
}

/** Whether the session used up its steps (the cap mode only). */
export function sessionStepsExhausted(steps: SessionSteps | null): boolean {
  const limit = sessionStepsLimit(steps);
  return Boolean(steps) && steps?.mode === 'cap' && limit > 0 && (steps as SessionSteps).used >= limit;
}

/** Whether this deployment can extend a session (step policy and route declared). */
export function canExtendSessionSteps(schema: ProductSchema): boolean {
  return Boolean(schema.step_policy?.extra_steps && schema.runtime.http.routes.chat_session_steps);
}

/** Projects the server's wire answer; null is not the shape the browser needs. */
export function projectSessionSteps(value: unknown): SessionSteps | null {
  const body = record(value);
  if (!body || typeof body.mode !== 'string' || typeof body.used !== 'number') return null;
  const steps: SessionSteps = { mode: body.mode, used: body.used };
  if (typeof body.cap === 'number') steps.cap = body.cap;
  if (typeof body.extra === 'number') steps.extra = body.extra;
  return steps;
}

async function payloadOf(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

/**
 * Reads one session's standing. A session that is no owner's asset (a plain
 * session, another owner's session) is 404, which is "not shown" rather than
 * an error: the bar simply does not render.
 */
export async function readSessionSteps(url: string): Promise<SessionSteps | null> {
  const response = await fetch(url);
  if (response.status === 404) return null;
  const payload = await payloadOf(response);
  if (!response.ok) return null;
  return projectSessionSteps(payload);
}

/**
 * Raises one asset's whole grant. `target` is the grant, not an increment, so
 * a retried request cannot add twice; a stale (lower) target is the server's
 * 409 and the caller re-reads instead of lowering the grant.
 */
export async function extendSessionSteps(url: string, target: number): Promise<SessionSteps> {
  const response = await fetch(url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ extra_steps: target }),
  });
  const payload = await payloadOf(response);
  if (!response.ok) {
    const detail = record(payload)?.detail;
    throw new Error(typeof detail === 'string' && detail ? detail : `HTTP ${response.status}`);
  }
  const steps = projectSessionSteps(payload);
  if (!steps) throw new Error('invalid session steps response');
  return steps;
}