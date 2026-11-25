import type { ProductSchema } from '@/lib/product-schema';
import type { AgentSkillConfiguration } from '@/lib/agent-configuration';
import { RequestFailure, responseRequestId } from '@/lib/problem-reference';

export interface PersonalSkillInput {
  name: string;
  prompt: string;
  category?: string;
  recommended_size: string;
  /** #548 的标准字段：slug 留空时服务端按 ID 派生；apply 为 generation-prompt（画风）或 instruction（做法）。 */
  slug?: string;
  description?: string;
  apply?: string;
}

/** 起草结果（#548）：未保存；slug 为空表示模型给的不合规或已占用，保存时按 ID 派生。 */
export interface PersonalSkillDraft {
  slug: string;
  description: string;
  prompt: string;
  apply: string;
}

/** 可单独保存的字段（#548 PATCH）。 */
export type PersonalSkillField = 'name' | 'prompt' | 'slug' | 'description';

/** 服务端校验拒绝了某个字段；field 为空表示没有指明字段。 */
export class PersonalSkillInvalid extends Error {
  constructor(message: string, readonly field: PersonalSkillField | '') { super(message); }
}

async function rejectResponse(response: Response): Promise<never> {
  const problem: unknown = await response.json().catch(() => ({}));
  if (response.status === 404) throw new PersonalSkillNotFound(`HTTP ${response.status}`);
  const code = problem && typeof problem === 'object' ? (problem as { code?: unknown }).code : undefined;
  if (response.status === 400 && typeof code === 'string' && code.startsWith('invalid_personal_skill')) {
    const field = code.replace(/^invalid_personal_skill_?/, '');
    throw new PersonalSkillInvalid(`HTTP ${response.status}`, (['name', 'prompt', 'slug', 'description'] as const).find((item) => item === field) ?? '');
  }
  throw new RequestFailure(`HTTP ${response.status}`, responseRequestId(response, problem));
}

function skillFrom(body: { skill?: AgentSkillConfiguration }): AgentSkillConfiguration {
  if (!body.skill || typeof body.skill.id !== 'string' || !body.skill.id) throw new Error('Invalid Skill response');
  return body.skill;
}

export async function createPersonalSkill(schema: ProductSchema, input: PersonalSkillInput, signal: AbortSignal) {
  const route = schema.chat.skills.creation.route;
  if (!route.startsWith('/') || route.startsWith('//')) throw new Error('Invalid Skill route');
  const response = await fetch(route, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input), signal,
  });
  if (!response.ok) await rejectResponse(response);
  return skillFrom(await response.json());
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

/** 个人 Skill 不可访问（不存在或不属于当前用户）；服务端不区分两者。 */
export class PersonalSkillNotFound extends Error {}

/** 整体替换本人的一个个人 Skill，ID 不变；返回更新后的对象。 */
export async function updatePersonalSkill(schema: ProductSchema, id: string, input: PersonalSkillInput, signal?: AbortSignal) {
  const template = sameOriginRoute(schema.chat.skills.creation.item_route);
  if (!template.includes('{skillID}')) throw new Error('Invalid Skill route');
  const response = await fetch(template.replace('{skillID}', encodeURIComponent(id)), {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input), signal,
  });
  if (response.status === 404) throw new PersonalSkillNotFound(`HTTP ${response.status}`);
  if (!response.ok) {
    const problem: unknown = await response.json().catch(() => ({}));
    throw new RequestFailure(`HTTP ${response.status}`, responseRequestId(response, problem));
  }
  const body: { skill?: AgentSkillConfiguration } = await response.json();
  if (!body.skill || typeof body.skill.id !== 'string' || !body.skill.id) throw new Error('Invalid Skill response');
  return body.skill;
}

/** 由名称与要求起草一个 Skill（#548）：服务端调一次对话模型，结果不落库。 */
export async function draftPersonalSkill(schema: ProductSchema, input: { name: string; requirement: string }, signal?: AbortSignal): Promise<PersonalSkillDraft> {
  const route = sameOriginRoute(schema.chat.skills.creation.draft_route);
  const response = await fetch(route, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input), signal,
  });
  if (!response.ok) {
    const problem: unknown = await response.json().catch(() => ({}));
    throw new RequestFailure(`HTTP ${response.status}`, responseRequestId(response, problem));
  }
  const body: { draft?: Partial<PersonalSkillDraft> } = await response.json();
  const draft = body.draft;
  if (!draft || typeof draft.prompt !== 'string' || typeof draft.description !== 'string') throw new Error('Invalid draft response');
  return {
    slug: typeof draft.slug === 'string' ? draft.slug : '',
    description: draft.description,
    prompt: draft.prompt,
    apply: typeof draft.apply === 'string' ? draft.apply : '',
  };
}

/** 只修改给出的字段（#548 PATCH），ID 不变；返回更新后的对象。 */
export async function patchPersonalSkill(schema: ProductSchema, id: string, patch: Partial<Record<PersonalSkillField, string>>, signal?: AbortSignal) {
  const template = sameOriginRoute(schema.chat.skills.creation.item_route);
  if (!template.includes('{skillID}')) throw new Error('Invalid Skill route');
  const response = await fetch(template.replace('{skillID}', encodeURIComponent(id)), {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch), signal,
  });
  if (!response.ok) await rejectResponse(response);
  return skillFrom(await response.json());
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
