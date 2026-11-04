import type { ProductSchema } from '@/lib/product-schema';
import type { AgentScope } from '@/store/chat-store';

/**
 * The session's Agent scope, as the public API still reports it. The composer no longer offers a scope
 * picker (#103): the workspace always runs in auto mode, so this module only normalizes the value the
 * server returns when it is stored on the session (kept for the administrator API and older snapshots).
 */
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
