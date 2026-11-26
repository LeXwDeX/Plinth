import type { ProductSchema } from '@/lib/product-schema';
import type { NativeQuote, PriceTotal } from '@/lib/pricing';
import { parseNativeQuote, parsePriceTotals } from '@/lib/pricing';
import { parseProductionPlan, type ProductionPlan } from '@/lib/chat/budget';
import { approvalDecisionBody } from '@/lib/approval-decision';

/**
 * 脱离轮次仍待决的付费步骤（服务端保存，默认 24 小时有效）。
 * 这里只解析响应并发出决定；批准、过期与提交都由服务端裁决，前端不推断。
 */

export type PendingApprovalStatus = 'pending' | 'expired';

export interface PendingApprovalInput {
  parameter: string;
  /** 'asset' is a picture the step sends; 'task' is the earlier task (a 3D model) it works on. */
  kind: string;
  role: string;
  assetId: string;
  mediaType?: string;
  taskId: string;
  handle: string;
  /** For a multiview step, the view key and its localized label this picture stands for. */
  view?: string;
  viewLabel?: string;
}

export interface ParameterAdjustment {
  /** 'agent', or 'profile:<id>' when the session's production purpose contributed the value (#103). */
  name: string; label: string; recommendedValue: unknown; value: unknown; reason: string; advice: string; source: string;
  /** The purpose id when the source is 'profile:<id>' (#103). */
  profile?: string;
}
/** Values are already frozen by Generation; never recalculate them from the live parameter panel. */
export function parseParameterAdjustments(raw: unknown): ParameterAdjustment[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap(value => {
    const item = record(value);
    const source = text(item.source);
    if ((source !== 'agent' && !source.startsWith('profile:')) || !text(item.name) || !text(item.label) || !text(item.reason) || !('value' in item) || !('recommended_value' in item)) return [];
    return [{ name: text(item.name), label: text(item.label), recommendedValue: item.recommended_value, value: item.value, reason: text(item.reason), advice: text(item.advice), source, ...(text(item.profile) ? { profile: text(item.profile) } : {}) }];
  });
}

/** #103/#239: who chose the step's model, with the purpose and reason when the purpose did. */
export interface ModelChoice {
  source: 'user' | 'agent' | 'profile' | 'default' | string;
  profile?: string;
  reason?: string;
}

/** A frozen model-choice record; an unknown or empty source reads as the default. */
export function parseModelChoice(raw: unknown): ModelChoice | undefined {
  const value = record(raw);
  const source = text(value.source);
  if (source === '') return undefined;
  return {
    source,
    ...(text(value.profile) ? { profile: text(value.profile) } : {}),
    ...(text(value.reason) ? { reason: text(value.reason) } : {}),
  };
}

/** #548: the style Skill a step applies and who chose it. */
export interface SkillChoice {
  name: string;
  source: 'user' | 'agent';
}

/** A frozen style-Skill record; one without a name or a known source is left out. */
export function parseSkillChoice(raw: unknown): SkillChoice | undefined {
  const value = record(raw);
  const name = text(value.name);
  const source = text(value.source);
  if (!name || (source !== 'user' && source !== 'agent')) return undefined;
  return { name, source };
}

export interface PendingApproval {
  requestId: string;
  status: PendingApprovalStatus;
  /** 'plan' is a production plan card (#103); its request id is the budget id. */
  kind: 'step' | 'plan';
  /** The plan a plan card asks to approve; null on a step card. */
  plan: ProductionPlan | null;
  /**
   * #120: the limits the person approves, one per credit unit and never summed
   * (plan cards only; empty on a step card).
   */
  budgets: PriceTotal[];
  /**
   * #251: the server's split of the worst-case estimate, one figure per credit
   * unit. Step totals are the main route; contingency totals are the declared
   * repairs already counted in the approved limit.
   */
  stepTotals?: PriceTotal[];
  contingencyTotals?: PriceTotal[];
  tool: string;
  capability: string;
  model: string;
  provider: string;
  providerId: string;
  priceMode?: string;
  nativeQuote?: NativeQuote | null;
  estimatedCredits: number;
  /** The price before the extras, and the extras with what they pay for; zero/empty when there are none. */
  baseCredits: number;
  extraCredits: number;
  extraLabel: string;
  extraItems: string[];
  prompt: string;
  /** Set when an earlier attempt of the same kind was charged but its result was not stored (#294): approving charges again. */
  rechargeTaskId?: string;
  /** The server's sentence (in the reader's language) that the step uses only the first of the pictures given (#329 E). */
  referenceNote?: string;
  /** #244: the server's sentence that the step changes only the masked region; present only with a mask input. */
  maskNote?: string;
  inputs: PendingApprovalInput[];
  parameterAdjustments?: ParameterAdjustment[];
  /** #239: who chose the model and why; absent when the server did not report it. */
  modelChoice?: ModelChoice;
  /** #548: the style Skill the step applies; absent when it applies none. */
  skillChoice?: SkillChoice;
  messageId: string;
  createdAt: string;
  expiresAt: string;
}

export interface PendingApprovalDecision {
  requestId: string;
  status: string;
  taskId: string;
  /** 提交后服务端插入的结果消息；有值时需要重新读取消息列表。 */
  messageId: string;
  /**
   * #134: the assistant message of the turn the server started to run an
   * approved plan; the chat attaches to its stream. Absent when none started.
   */
  turnMessageId?: string;
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function parsePendingApprovals(payload: unknown): PendingApproval[] {
  const items = record(payload).items;
  if (!Array.isArray(items)) return [];
  const result: PendingApproval[] = [];
  for (const raw of items) {
    const item = record(raw);
    const requestId = text(item.request_id);
    const status = item.status === 'expired' ? 'expired' : item.status === 'pending' ? 'pending' : null;
    const kind = text(item.kind) === 'plan' ? 'plan' : 'step';
    // A plan card without its plan cannot be decided or shown; leave it out
    // rather than showing an approval with nothing to approve.
    const plan = kind === 'plan' ? parseProductionPlan(item.plan) : null;
    if (!requestId || !status || (kind === 'plan' && !plan)) continue;
    const inputs = (Array.isArray(item.inputs) ? item.inputs : []).map((value) => {
      const input = record(value);
      return {
        parameter: text(input.parameter), kind: text(input.kind), role: text(input.role), assetId: text(input.asset_id),
        taskId: text(input.task_id), handle: text(input.handle),
        ...(text(input.media_type) ? { mediaType: text(input.media_type) } : {}),
        ...(text(input.view) ? { view: text(input.view) } : {}),
        ...(text(input.view_label) ? { viewLabel: text(input.view_label) } : {}),
      };
    });
    const modelChoice = parseModelChoice(item.model_choice);
    const skillChoice = parseSkillChoice(item.skill_choice);
    result.push({
      requestId, status, kind, plan,
      budgets: kind === 'plan' ? parsePriceTotals(item.budgets) ?? [] : [],
      stepTotals: kind === 'plan' ? parsePriceTotals(item.step_totals) ?? [] : [],
      contingencyTotals: kind === 'plan' ? parsePriceTotals(item.contingency_totals) ?? [] : [],
      tool: text(item.tool), capability: text(item.capability), model: text(item.model), provider: text(item.provider), providerId: text(item.provider_id),
      priceMode: text(item.price_mode) || (item.native_quote !== undefined ? 'native' : ''),
      nativeQuote: parseNativeQuote(item.native_quote),
      estimatedCredits: typeof item.estimated_credits === 'number' ? item.estimated_credits : 0,
      baseCredits: typeof item.base_credits === 'number' ? item.base_credits : 0,
      extraCredits: typeof item.extra_credits === 'number' ? item.extra_credits : 0,
      extraLabel: text(item.extra_label),
      extraItems: (Array.isArray(item.extra_items) ? item.extra_items : []).filter((value): value is string => typeof value === 'string' && value !== ''),
      ...(Array.isArray(item.parameter_adjustments) ? { parameterAdjustments: parseParameterAdjustments(item.parameter_adjustments) } : {}),
      ...(modelChoice ? { modelChoice } : {}),
      ...(skillChoice ? { skillChoice } : {}),
      prompt: text(item.prompt), inputs, messageId: text(item.message_id),
      ...(text(item.recharge_task_id) ? { rechargeTaskId: text(item.recharge_task_id) } : {}),
      ...(text(item.reference_note) ? { referenceNote: text(item.reference_note) } : {}),
      ...(text(item.mask_note) ? { maskNote: text(item.mask_note) } : {}),
      createdAt: text(item.created_at), expiresAt: text(item.expires_at),
    });
  }
  return result;
}

/** Whether the server's own expiry instant has passed. */
export function hasExpired(expiresAt: string, now: number): boolean {
  const at = Date.parse(expiresAt);
  return Number.isFinite(at) && at <= now;
}

/**
 * 服务端不再列出已过期的步骤（它既不计数也不占名额），但对话里的卡片要留着，
 * 以过期状态显示"重新发起"。这里把上次还在、这次已不在列表里且已到期的卡片留下；
 * 还在有效期内就消失的卡片是被决定或取消了，不保留。
 */
export function retainExpired(
  previous: readonly PendingApproval[],
  listed: readonly PendingApproval[],
  retained: readonly PendingApproval[],
  now: number,
): PendingApproval[] {
  const current = new Set(listed.map((item) => item.requestId));
  const result = retained.filter((item) => !current.has(item.requestId));
  const kept = new Set(result.map((item) => item.requestId));
  for (const item of previous) {
    if (current.has(item.requestId) || kept.has(item.requestId)) continue;
    if (item.status === 'expired' || hasExpired(item.expiresAt, now)) {
      result.push(item.status === 'expired' ? item : { ...item, status: 'expired' });
      kept.add(item.requestId);
    }
  }
  return result;
}

/** One read of the server's list, stamped with the moment its request was sent. */
export interface PolledApprovals {
  startedAt: number;
  items: PendingApproval[];
  /**
   * #665: how far the server's clock is ahead of this browser's (ms), from the response's Date header; 0 when unknown.
   * Expiry is the server's instant, so it is compared with the server's time, not with a fast or slow local clock.
   */
  clockOffset?: number;
}

/** #665: a Date header names whole seconds, so a smaller lead is its rounding, not the server's clock. */
const CLOCK_OFFSET_PRECISION_MS = 1_000;
/** #665: a larger lead from a cached answer without an Age header is taken for a stale header, not a clock. */
const MAX_CACHED_CLOCK_OFFSET_MS = 60 * 60 * 1_000;
/** #665: the headers by which common caches and proxies say an answer came from their store. */
const CACHE_STATUS_HEADERS = ['X-Cache', 'X-Cache-Status', 'CF-Cache-Status', 'X-Proxy-Cache'];

/** #665: whether a cache says it answered from its store (its Date is then as old as the stored copy). */
function servedFromCache(headers: Headers | undefined): boolean {
  return CACHE_STATUS_HEADERS.some((name) => /\b(hit|stale)\b/i.test(headers?.get(name) ?? ''));
}

/**
 * #665: the server clock's lead over the browser's, read from a response's Date header (plus its Age, when a cache
 * answered); 0 when it has none or the lead is within the header's one-second precision. A badly wrong browser clock
 * is exactly when this matters, so a fresh answer's lead is taken whatever its size. Only an answer a cache says it
 * served from its store without an Age header has a Date of unknown staleness: a lead over an hour is then ignored.
 * Each read stands alone: the caller keeps only the freshest response's.
 */
export function serverClockOffset(response: Pick<Response, 'headers'>, receivedAt: number): number {
  const date = Date.parse(response.headers?.get('Date') ?? '');
  if (!Number.isFinite(date)) return 0;
  const age = Number(response.headers?.get('Age') ?? '');
  const aged = Number.isFinite(age) && age > 0;
  const offset = date + (aged ? age * 1_000 : 0) - receivedAt;
  if (Math.abs(offset) < CLOCK_OFFSET_PRECISION_MS) return 0;
  if (!aged && servedFromCache(response.headers) && Math.abs(offset) > MAX_CACHED_CLOCK_OFFSET_MS) return 0;
  return offset;
}

/** #665: how many decided cards a page remembers; far more than one session ever shows at once. */
export const DECIDED_LIMIT = 200;

/** #665: adds a decided card to a page's set, dropping the oldest once it holds DECIDED_LIMIT. */
export function rememberDecided(decided: Set<string>, requestId: string): void {
  decided.delete(requestId);
  decided.add(requestId);
  while (decided.size > DECIDED_LIMIT) decided.delete(decided.values().next().value as string);
}

/**
 * Cards this page has decided. A poll sent before a decision can come back after
 * it and still list the card as pending (the server answered before it committed);
 * showing that would re-open a decided card. So a decided card stays hidden from
 * every read whose request was sent no later than the decision finished, and is
 * forgotten once a read sent after it arrives: from then on the server's list is
 * the truth again, whether or not it still lists the card.
 */
export class DecisionLedger {
  private readonly decidedAt = new Map<string, number>();

  /** Record a decision that the server accepted, finished at `at`. */
  markDecided(requestId: string, at: number): void {
    this.decidedAt.delete(requestId);
    this.decidedAt.set(requestId, at);
    // #665: a page that is never read again keeps no unbounded list; the oldest decisions go first.
    while (this.decidedAt.size > DECIDED_LIMIT) this.decidedAt.delete(this.decidedAt.keys().next().value as string);
  }

  /** Whether a read sent at `startedAt` must not be believed about this card. */
  private stale(requestId: string, startedAt: number): boolean {
    const at = this.decidedAt.get(requestId);
    return at !== undefined && startedAt <= at;
  }

  /** The poll without the cards it cannot know are decided. Pure: forgets nothing. */
  visible(polled: PolledApprovals): PendingApproval[] {
    if (this.decidedAt.size === 0) return polled.items;
    return polled.items.filter((item) => !this.stale(item.requestId, polled.startedAt));
  }

  /** Forget the decisions that a read sent after them has now superseded. */
  settle(polled: PolledApprovals): void {
    for (const [requestId, at] of [...this.decidedAt]) {
      if (polled.startedAt > at) this.decidedAt.delete(requestId);
    }
  }
}

/** The cards to show: the server's list, then the expired ones it no longer lists. */
export function displayedApprovals(listed: readonly PendingApproval[], retained: readonly PendingApproval[]): PendingApproval[] {
  const current = new Set(listed.map((item) => item.requestId));
  return [...listed, ...retained.filter((item) => !current.has(item.requestId))];
}

/** How many still wait for a decision: an expired card is shown but is not pending. */
export function countPending(items: readonly PendingApproval[], now: number): number {
  return items.filter((item) => item.status === 'pending' && !hasExpired(item.expiresAt, now)).length;
}

export function pendingApprovalsRoute(schema: ProductSchema, sessionId: string): string | null {
  const template = schema.runtime.http.routes.chat_session_pending_approvals;
  return template ? template.replace('{sessionID}', encodeURIComponent(sessionId)) : null;
}

export async function fetchPendingApprovals(
  schema: ProductSchema,
  sessionId: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch,
): Promise<PendingApproval[]> {
  return (await readPendingApprovals(schema, sessionId, signal, fetcher)).items;
}

/** The list with the server clock's lead over this browser's (see PolledApprovals.clockOffset). */
export async function readPendingApprovals(
  schema: ProductSchema,
  sessionId: string,
  signal?: AbortSignal,
  fetcher: typeof fetch = fetch,
): Promise<{ items: PendingApproval[]; clockOffset: number }> {
  const route = pendingApprovalsRoute(schema, sessionId);
  if (!route) return { items: [], clockOffset: 0 };
  const response = await fetcher(route, { signal });
  if (!response.ok) throw new Error(`pending approvals ${response.status}`);
  const clockOffset = serverClockOffset(response, Date.now());
  return { items: parsePendingApprovals(await response.json().catch(() => null)), clockOffset };
}

export class PendingApprovalError extends Error {
  readonly status: number;
  /** 服务端的 error.* 词汇键或安全文本；由调用方经 Schema 解析。 */
  readonly detail: string;
  constructor(detail: string, status: number) {
    super(detail);
    this.name = 'PendingApprovalError';
    this.status = status;
    this.detail = detail;
  }
}

/** #103: the decision member is Schema data; see `approvalDecisionBody`. */
function decisionBody(schema: ProductSchema, approve: boolean): Record<string, string> {
  return approvalDecisionBody(schema, approve);
}

/**
 * 对一条待批准步骤做决定：PUT 任务授权。批准 / 拒绝都由服务端检查有效期和
 * 来源；过期的批准返回 409，拒绝仍可。
 */
export async function decidePendingApproval(
  schema: ProductSchema,
  requestId: string,
  approve: boolean,
  fetcher: typeof fetch = fetch,
): Promise<PendingApprovalDecision> {
  const body = decisionBody(schema, approve);
  const route = schema.runtime.http.routes.task_grant_decision.replace('{requestID}', encodeURIComponent(requestId));
  const response = await fetcher(route, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const payload = record(await response.json().catch(() => null));
  if (!response.ok) throw new PendingApprovalError(text(payload.detail), response.status);
  return {
    requestId: text(payload.request_id) || requestId,
    status: text(payload.status),
    taskId: text(payload.task_id),
    messageId: text(payload.message_id),
  };
}

/**
 * #103: a production plan is decided on its own session route. Approving makes
 * it the session's active budget; denying ends it. The response is the budget's
 * state, not a task.
 */
export async function decideSessionBudget(
  schema: ProductSchema,
  sessionId: string,
  budgetId: string,
  approve: boolean,
  fetcher: typeof fetch = fetch,
): Promise<PendingApprovalDecision> {
  const template = schema.runtime.http.routes.chat_session_budget;
  if (!template) throw new PendingApprovalError('', 0);
  const body = decisionBody(schema, approve);
  const route = template
    .replace('{sessionID}', encodeURIComponent(sessionId))
    .replace('{budgetID}', encodeURIComponent(budgetId));
  const response = await fetcher(route, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const payload = record(await response.json().catch(() => null));
  if (!response.ok) throw new PendingApprovalError(text(payload.detail), response.status);
  return {
    requestId: text(payload.budget_id) || budgetId,
    status: text(payload.state),
    taskId: '',
    messageId: text(payload.message_id),
    ...(text(payload.turn_message_id) ? { turnMessageId: text(payload.turn_message_id) } : {}),
  };
}

/** Decide a pending card on the route its kind uses. */
export function decidePendingCard(
  schema: ProductSchema,
  sessionId: string,
  item: Pick<PendingApproval, 'requestId' | 'kind'>,
  approve: boolean,
  fetcher: typeof fetch = fetch,
): Promise<PendingApprovalDecision> {
  return item.kind === 'plan'
    ? decideSessionBudget(schema, sessionId, item.requestId, approve, fetcher)
    : decidePendingApproval(schema, item.requestId, approve, fetcher);
}

/**
 * #410: start the turn that runs a plan the person left unfinished again. The
 * plan is already approved, so this decides nothing: the server refuses with
 * 409 when the plan is no longer the session's active one or its turn still
 * runs. The response carries the assistant message of the new turn so the
 * caller can follow it like any reply.
 */
export async function continueSessionBudget(
  schema: ProductSchema,
  sessionId: string,
  budgetId: string,
  fetcher: typeof fetch = fetch,
): Promise<{ turnMessageId: string }> {
  const template = schema.runtime.http.routes.chat_session_budget_continue;
  if (!template) throw new PendingApprovalError('', 0);
  const route = template
    .replace('{sessionID}', encodeURIComponent(sessionId))
    .replace('{budgetID}', encodeURIComponent(budgetId));
  const response = await fetcher(route, { method: 'POST' });
  const payload = record(await response.json().catch(() => null));
  if (!response.ok) throw new PendingApprovalError(text(payload.detail), response.status);
  return { turnMessageId: text(payload.turn_message_id) };
}
