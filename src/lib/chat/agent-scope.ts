import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { AgentScope } from '@/store/chat-store';
import type { WorkspaceDomain } from '@/store/workspace-store';

export interface AgentScopeOption {
  scope: AgentScope;
  label: string;
  /** 一句话说明，写在选择表里。 */
  description: string;
}

const KNOWN: readonly AgentScope[] = ['2d', '3d', 'auto'];

function isKnown(value: unknown): value is AgentScope {
  return typeof value === 'string' && (KNOWN as readonly string[]).includes(value);
}

/** Agent 范围的默认值：Schema 声明的默认；Schema 没给出可用值时是自动模式。 */
export function defaultAgentScope(schema: ProductSchema): AgentScope {
  const declared = schema.chat.agent_scope?.default_mode;
  return isKnown(declared) && offered(schema, declared) ? declared : 'auto';
}

function offered(schema: ProductSchema, scope: AgentScope): boolean {
  return (schema.chat.agent_scope?.modes ?? []).some((mode) => mode.id === scope);
}

/** 服务端给出的值若不是 Schema 提供的模式，按 Schema 默认处理（与服务端一致）。 */
export function agentScopeOf(schema: ProductSchema, value: unknown): AgentScope {
  return isKnown(value) && offered(schema, value) ? value : defaultAgentScope(schema);
}

/** 可选的模式、顺序与文案全部来自 Product Schema，不在组件里写死。 */
export function agentScopeOptions(schema: ProductSchema): AgentScopeOption[] {
  return (schema.chat.agent_scope?.modes ?? [])
    .map((mode) => mode.id)
    .filter(isKnown)
    .map((scope) => ({
      scope,
      label: schemaText(schema, `ui.studio.agent_scope.mode_${scope}`),
      description: schemaText(schema, `ui.studio.agent_scope.desc_${scope}`),
    }));
}

/** 这个模式是否让该领域的模型选择器保持可用（2D 模式停用 3D 选择器，3D 模式停用 2D 选择器）。 */
export function scopeAllowsDomain(schema: ProductSchema, scope: AgentScope, domain: WorkspaceDomain): boolean {
  const mode = (schema.chat.agent_scope?.modes ?? []).find((item) => item.id === scope);
  // 没有声明范围的 Schema（旧快照）不限制任何选择器。
  if (!mode) return (schema.chat.agent_scope?.modes ?? []).length === 0;
  return mode.workspace_domains.includes(domain);
}

/** 模型选择器因范围而停用时的悬停说明（写明当前模式）。 */
export function pickerDisabledReason(schema: ProductSchema, scope: AgentScope): string {
  return formatSchemaText(schemaText(schema, 'ui.studio.agent_scope.picker_disabled'), {
    mode: schemaText(schema, `ui.studio.agent_scope.mode_${scope}`),
  });
}
