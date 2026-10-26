import type { ProductSchema } from '@/lib/product-schema';
import type { ChatSkill } from '@/lib/chat/browser-client';

export interface AgentSkillConfiguration {
  id: string;
  name: string;
  name_zh?: string;
  category: string;
  recommended_size: string;
  thumbnail_path: string;
  raw_image_url: string;
  prompt: string;
  image_to_image_template?: string;
  tags?: string[];
}
export interface AgentConfiguration {
  system_prompt: string;
  domain_context: Record<string, string>;
  skills: AgentSkillConfiguration[];
  enabled_tool_names: string[];
  enabled_mcp_preset_ids: string[];
}
export interface AgentToolSummary { name: string; description: string; domains?: string[] }
export interface AgentMCPPreset { available: boolean; id: string; name: string; description: string; tool_names: string[]; protocol_version?: string; transport?: string; connection_ref?: string }
export interface AgentConfigurationSnapshot {
  revision: string;
  configuration: AgentConfiguration;
  presets: AgentConfiguration;
  tools: AgentToolSummary[];
  mcp_presets: AgentMCPPreset[];
  history: Array<{ revision: string; created_at?: string; source?: string }>;
}
export interface AgentEffectiveConfiguration {
  revision: string;
  tools: AgentToolSummary[];
  skills: Array<Pick<AgentSkillConfiguration, 'id' | 'name' | 'name_zh' | 'category'>>;
  model: { label: string };
  parameter_source: string;
}
export class AgentConfigurationError extends Error {
  constructor(public readonly detail: string, public readonly status: number) {
    super(detail); this.name = 'AgentConfigurationError';
  }
}

export function createAgentConfigurationClient(schema: ProductSchema, fetcher: typeof fetch = fetch) {
  const routes = schema.agent_configuration.routes;
  async function request<T>(route: string, payload?: unknown, signal?: AbortSignal): Promise<T> {
    if (!route.startsWith('/') || route.startsWith('//')) throw new AgentConfigurationError('ui.agent_configuration.save_failed', 0);
    const response = await fetcher(route, {
      method: payload === undefined ? 'GET' : 'POST',
      ...(payload === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) }),
      signal,
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new AgentConfigurationError(
      typeof body.detail === 'string' ? body.detail : 'ui.agent_configuration.save_failed', response.status,
    );
    return body as T;
  }
  return {
    get: (signal?: AbortSignal) => request<AgentConfigurationSnapshot>(routes.admin, undefined, signal),
    validate: (configuration: AgentConfiguration, signal?: AbortSignal) => request<{ valid: boolean; effective_prompts: Record<string, string> }>(routes.validate, { configuration }, signal),
    publish: (expectedRevision: string, configuration: AgentConfiguration, signal?: AbortSignal) => request<AgentConfigurationSnapshot>(routes.publish, { expected_revision: expectedRevision, configuration }, signal),
    restore: (expectedRevision: string, targetRevision: string, signal?: AbortSignal) => request<AgentConfigurationSnapshot>(routes.restore, { expected_revision: expectedRevision, target_revision: targetRevision }, signal),
    reset: (expectedRevision: string, signal?: AbortSignal) => request<AgentConfigurationSnapshot>(routes.reset, { expected_revision: expectedRevision }, signal),
    effective: (domain: string, modelId?: string | null, sessionId?: string | null, signal?: AbortSignal) => {
      const query = new URLSearchParams({ creative_domain: domain });
      if (modelId) query.set('model_id', modelId);
      if (sessionId) query.set('session_id', sessionId);
      return request<AgentEffectiveConfiguration>(`${routes.effective}?${query}`, undefined, signal);
    },
  };
}

/** A saved selection stays visible when unavailable; sending must stop until cleared. */
export function resolveSessionSkill(
  selectedId: string | null,
  catalog: ChatSkill[] | null,
  effective: AgentEffectiveConfiguration | null,
): { skill: ChatSkill | null; unavailable: boolean } {
  if (!selectedId) return { skill: null, unavailable: false };
  const skill = catalog?.find((item) => item.id === selectedId) ?? null;
  return { skill, unavailable: !skill || !effective?.skills.some((item) => item.id === selectedId) };
}
