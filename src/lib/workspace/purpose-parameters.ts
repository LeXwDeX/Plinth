import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';

/**
 * The values the session's production brief gives the parameters that follow the session purpose
 * (presentation.follows_purpose, #292), by Offering ID and then parameter name (#306). The server resolves them
 * with the same rule that fills the plan the panel creates, so the panel can say what an untouched parameter
 * will take before the step is confirmed. Nothing here knows a purpose, a platform or a value.
 */
export type PurposeValues = Record<string, Record<string, unknown>>;

/** Whether the Offering declares a parameter that follows the session purpose. */
export function followsPurpose(offering: OfferingWire): boolean {
  return (offering.parameters ?? []).some((parameter) => parameter.presentation?.follows_purpose === true);
}

/** The Schema route that answers for one Offering of one session; null when the Schema has none (an older server). */
export function purposeParametersRoute(schema: ProductSchema, sessionId: string, offeringId: string): string | null {
  const template = schema.runtime.http.routes.chat_session_purpose_parameters;
  return template
    ? `${template.replace('{sessionID}', encodeURIComponent(sessionId))}?offering_id=${encodeURIComponent(offeringId)}`
    : null;
}

/** The parameters of an answer: the object under `parameters`; any other shape has nothing to show. */
export function readPurposeParameters(payload: unknown): Record<string, unknown> {
  if (payload === null || typeof payload !== 'object' || Array.isArray(payload)) return {};
  const parameters = (payload as Record<string, unknown>).parameters;
  return parameters !== null && typeof parameters === 'object' && !Array.isArray(parameters)
    ? { ...(parameters as Record<string, unknown>) } : {};
}

/**
 * What the server answers for each of the Offerings, one request apiece. An Offering whose answer is an error,
 * is not JSON or never came is left out: the parameter then says only that it follows the purpose, as before,
 * and never shows an earlier value that may no longer hold.
 */
export async function fetchPurposeParameters(
  schema: ProductSchema,
  sessionId: string,
  offeringIds: readonly string[],
  fetcher: typeof fetch = fetch,
): Promise<PurposeValues> {
  const answers = await Promise.all(offeringIds.map(async (offeringId): Promise<[string, Record<string, unknown>] | null> => {
    const route = purposeParametersRoute(schema, sessionId, offeringId);
    if (!route) return null;
    try {
      const response = await fetcher(route);
      return response.ok ? [offeringId, readPurposeParameters(await response.json())] : null;
    } catch {
      return null;
    }
  }));
  return Object.fromEntries(answers.filter((answer): answer is [string, Record<string, unknown>] => answer !== null));
}
