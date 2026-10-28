import type { ProductSchema } from '@/lib/product-schema';
import type { NativeQuote } from '@/lib/pricing';
import { parseNativeQuote } from '@/lib/pricing';

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
}

export interface ParameterAdjustment {
  name: string; label: string; recommendedValue: unknown; value: unknown; reason: string; advice: string; source: 'agent';
}
/** Values are already frozen by Generation; never recalculate them from the live parameter panel. */
export function parseParameterAdjustments(raw: unknown): ParameterAdjustment[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap(value => {
    const item = record(value);
    if (item.source !== 'agent' || !text(item.name) || !text(item.label) || !text(item.reason) || !('value' in item) || !('recommended_value' in item)) return [];
    return [{ name: text(item.name), label: text(item.label), recommendedValue: item.recommended_value, value: item.value, reason: text(item.reason), advice: text(item.advice), source: 'agent' as const }];
  });
}

export interface PendingApproval {
  requestId: string;
  status: PendingApprovalStatus;
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
  inputs: PendingApprovalInput[];
  parameterAdjustments?: ParameterAdjustment[];
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
    if (!requestId || !status) continue;
    const inputs = (Array.isArray(item.inputs) ? item.inputs : []).map((value) => {
      const input = record(value);
      return {
        parameter: text(input.parameter), kind: text(input.kind), role: text(input.role), assetId: text(input.asset_id),
        taskId: text(input.task_id), handle: text(input.handle),
        ...(text(input.media_type) ? { mediaType: text(input.media_type) } : {}),
      };
    });
    result.push({
      requestId, status,
      tool: text(item.tool), capability: text(item.capability), model: text(item.model), provider: text(item.provider), providerId: text(item.provider_id),
      priceMode: text(item.price_mode) || (item.native_quote !== undefined ? 'native' : ''),
      nativeQuote: parseNativeQuote(item.native_quote),
      estimatedCredits: typeof item.estimated_credits === 'number' ? item.estimated_credits : 0,
      baseCredits: typeof item.base_credits === 'number' ? item.base_credits : 0,
      extraCredits: typeof item.extra_credits === 'number' ? item.extra_credits : 0,
      extraLabel: text(item.extra_label),
      extraItems: (Array.isArray(item.extra_items) ? item.extra_items : []).filter((value): value is string => typeof value === 'string' && value !== ''),
      ...(Array.isArray(item.parameter_adjustments) ? { parameterAdjustments: parseParameterAdjustments(item.parameter_adjustments) } : {}),
      prompt: text(item.prompt), inputs, messageId: text(item.message_id),
      ...(text(item.recharge_task_id) ? { rechargeTaskId: text(item.recharge_task_id) } : {}),
      ...(text(item.reference_note) ? { referenceNote: text(item.reference_note) } : {}),
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
    this.decidedAt.set(requestId, at);
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
  const route = pendingApprovalsRoute(schema, sessionId);
  if (!route) return [];
  const response = await fetcher(route, { signal });
  if (!response.ok) throw new Error(`pending approvals ${response.status}`);
  return parsePendingApprovals(await response.json().catch(() => null));
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
  const route = schema.runtime.http.routes.task_grant_decision.replace('{requestID}', encodeURIComponent(requestId));
  const response = await fetcher(route, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ decision: approve ? 'approve' : 'deny' }),
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
