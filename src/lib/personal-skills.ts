import type { ProductSchema } from '@/lib/product-schema';
import type { AgentSkillConfiguration } from '@/lib/agent-configuration';
import { RequestFailure, responseRequestId } from '@/lib/problem-reference';

export interface PersonalSkillInput {
  name: string;
  prompt: string;
  category?: string;
  recommended_size: string;
}

export async function createPersonalSkill(schema: ProductSchema, input: PersonalSkillInput, signal: AbortSignal) {
  const route = schema.chat.skills.creation.route;
  if (!route.startsWith('/') || route.startsWith('//')) throw new Error('Invalid Skill route');
  const response = await fetch(route, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input), signal,
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const body: { skill?: AgentSkillConfiguration } = await response.json();
  if (!body.skill || typeof body.skill.id !== 'string' || !body.skill.id) throw new Error('Invalid Skill response');
  return body.skill;
}

function sameOriginRoute(route: string | undefined): string {
  if (!route || !route.startsWith('/') || route.startsWith('//')) throw new Error('Invalid Skill route');
  return route;
}

/** 本人的个人 Skill ID（服务端按登录身份限定）。用来判断哪些行可以删除（#238）。 */
export async function listPersonalSkillIds(schema: ProductSchema, signal?: AbortSignal): Promise<string[]> {
  const response = await fetch(sameOriginRoute(schema.chat.skills.creation.route), { signal, cache: 'no-store' });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const body: { skills?: Array<{ id?: unknown }> } = await response.json();
  return (body.skills ?? []).flatMap((skill) => (typeof skill.id === 'string' && skill.id ? [skill.id] : []));
}

/** 删除本人的一个个人 Skill；失败时抛出，调用方保留列表与选择。 */
export async function deletePersonalSkill(schema: ProductSchema, id: string): Promise<void> {
  const template = sameOriginRoute(schema.chat.skills.creation.item_route);
  if (!template.includes('{skillID}')) throw new Error('Invalid Skill route');
  const response = await fetch(template.replace('{skillID}', encodeURIComponent(id)), { method: 'DELETE' });
  if (!response.ok) {
    const problem: unknown = await response.json().catch(() => ({}));
    throw new RequestFailure(`HTTP ${response.status}`, responseRequestId(response, problem));
  }
}
