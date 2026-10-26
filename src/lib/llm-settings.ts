import type { ProductSchema } from '@/lib/product-schema';

/** #322: the administrator's choices for one chat model connection. */
export interface LLMModelSettings {
  enabled: boolean;
  effort_levels: string[];
  default_effort: string;
}

export interface LLMSettings {
  default_model: string;
  models: Record<string, LLMModelSettings>;
}

/** The latest connection test: a classified outcome only, never upstream text. */
export interface LLMConnectionCheck {
  connection_id: string;
  model: string;
  checked_at: string;
  reachable: boolean;
  vision: 'supported' | 'unsupported' | 'unknown';
  outcome: string;
  actor_id: string;
}

/** One connection as the environment declares it: whether each variable is set, never its value. */
export interface LLMConnectionView {
  id: string;
  model: string;
  deployment_default: boolean;
  /** Variable names: base URL, key, model. */
  environment: string[];
  base_url_set: boolean;
  api_key_set: boolean;
  model_set: boolean;
  credentials_required: boolean;
  configured: boolean;
  profile_levels: string[];
  profile_default: string;
  context_window: number;
  check: LLMConnectionCheck | null;
}

export interface LLMSettingsManagement {
  revision: string;
  settings: LLMSettings;
  defaults: LLMSettings;
  connections: LLMConnectionView[];
  history: Array<{ revision: string; created_at?: string; actor_id?: string; source?: string }>;
  default_available: boolean;
  effective_default: string;
}

export class LLMSettingsError extends Error {
  constructor(public readonly detail: string, public readonly status: number) {
    super(detail);
    this.name = 'LLMSettingsError';
  }
}

export function createLLMSettingsClient(schema: ProductSchema, fetcher: typeof fetch = fetch) {
  const routes = schema.llm_settings?.routes;
  async function request(route: string | undefined, payload?: unknown, signal?: AbortSignal): Promise<LLMSettingsManagement> {
    if (!route || !route.startsWith('/') || route.startsWith('//')) throw new LLMSettingsError('', 0);
    const response = await fetcher(route, {
      method: payload === undefined ? 'GET' : 'POST',
      ...(payload === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) }),
      signal,
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new LLMSettingsError(typeof body.detail === 'string' ? body.detail : '', response.status);
    return body as LLMSettingsManagement;
  }
  return {
    get: (signal?: AbortSignal) => request(routes?.admin, undefined, signal),
    publish: (expectedRevision: string, settings: LLMSettings, signal?: AbortSignal) =>
      request(routes?.publish, { expected_revision: expectedRevision, settings }, signal),
    restore: (expectedRevision: string, targetRevision: string, signal?: AbortSignal) =>
      request(routes?.restore, { expected_revision: expectedRevision, target_revision: targetRevision }, signal),
    reset: (expectedRevision: string, signal?: AbortSignal) => request(routes?.reset, { expected_revision: expectedRevision }, signal),
    test: (connectionId: string, signal?: AbortSignal) => request(routes?.test, { connection_id: connectionId }, signal),
  };
}

/** Toggles one level of a model's offered levels, keeping the profile's order and a valid default. */
export function toggleLevel(connection: LLMConnectionView, model: LLMModelSettings, level: string, on: boolean): LLMModelSettings {
  const chosen = new Set(model.effort_levels);
  if (on) chosen.add(level); else chosen.delete(level);
  const levels = connection.profile_levels.filter((item) => chosen.has(item));
  const fallback = levels.includes(model.default_effort) ? model.default_effort
    : levels.includes(connection.profile_default) ? connection.profile_default : levels[0] ?? '';
  return { ...model, effort_levels: levels, default_effort: fallback };
}
