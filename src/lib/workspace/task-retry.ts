import type { ProductSchema } from '@/lib/product-schema';
import type { TrackedTaskFailure } from '@/lib/workspace/task-projection';
import { withRequestReference } from '@/lib/problem-reference';

/**
 * 失败任务的"重试"。服务端从原任务的冻结输入重新走一遍准备、批准与提交；
 * 这里只判断入口是否出现，并解析服务端的两种答复：已提交，或还需要人确认。
 */

export type RetryOutcome =
  | { status: 'submitted'; taskId: string }
  | { status: 'awaiting_decision'; preparedInputId: string; requestId: string; key: string };

/** 服务端把"上游账户余额不足"归为这个失败类别：同一个模型再试一次还是同样的结果。 */
export const INSUFFICIENT_BALANCE_CLASS = 'insufficient_balance';

export function isBalanceFailure(failure: Pick<TrackedTaskFailure, 'failureClass'> | null | undefined): boolean {
  return failure?.failureClass === INSUFFICIENT_BALANCE_CLASS;
}

/**
 * 终态、带失败信息、失败可重试且不是"结果不明"（上游可能已接单）的任务才能重试。
 * 余额不足不给"重试"：钱没补上之前重试只会再失败一次，该换一个模型。
 */
export function canRetryTask(task: { terminal: boolean; notFound?: boolean; failure: TrackedTaskFailure | null }): boolean {
  return task.terminal && !task.notFound && task.failure !== null && task.failure.retryable && !task.failure.ambiguous
    && !isBalanceFailure(task.failure);
}

interface OfferingChoice { id: string; model_id: string; model_name: string; capability: string; available: boolean; recommended?: boolean; priority?: number }

/**
 * 余额不足后可以换的模型：同一个能力下另一个可用的 Offering（不同的模型）。
 * 没有别的可用 Offering（比如换贴图只有一家）就返回 null，界面不显示"换个模型"。
 */
export function alternativeOffering<T extends OfferingChoice>(
  offerings: readonly T[] | undefined,
  task: { capability: string; offeringId?: string; modelId?: string },
): T | null {
  const others = (offerings ?? []).filter((item) => item.available && item.capability === task.capability
    && item.id !== task.offeringId && item.model_id !== task.modelId);
  if (others.length === 0) return null;
  const rank = (item: T) => (item.recommended ? 0 : 1);
  return [...others].sort((left, right) => rank(left) - rank(right) || (right.priority ?? 0) - (left.priority ?? 0))[0];
}

export function retryRoute(schema: ProductSchema, taskId: string): string | null {
  const template = schema.runtime.http.routes.task_retry;
  return template ? template.replace('{taskID}', encodeURIComponent(taskId)) : null;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function parseRetryOutcome(payload: unknown): RetryOutcome | null {
  if (!payload || typeof payload !== 'object') return null;
  const body = payload as Record<string, unknown>;
  if (body.status === 'submitted' && text(body.task_id)) return { status: 'submitted', taskId: text(body.task_id) };
  if (body.status === 'awaiting_decision' && text(body.prepared_input_id) && text(body.request_id) && text(body.idempotency_key)) {
    return { status: 'awaiting_decision', preparedInputId: text(body.prepared_input_id), requestId: text(body.request_id), key: text(body.idempotency_key) };
  }
  return null;
}

/** 服务端对重试给出的拒绝文案（已是 Schema 文本）；没有就用兜底文案。 */
export async function retryProblemText(response: Response, fallback: string): Promise<string> {
  const problem: { detail?: unknown } = await response.json().catch(() => ({}));
  return withRequestReference(typeof problem.detail === 'string' && problem.detail !== '' ? problem.detail : fallback, problem);
}

/** 服务端拒绝重试（已是 Schema 文本）。 */
export class RetryRefused extends Error {}

/**
 * 请求重试。只发一个 POST：幂等键由服务端从原任务推出，重复点击得到同一结果。
 * 被拒绝时抛出 RetryRefused（带服务端的 Schema 文案），其余失败抛普通错误。
 */
export async function requestRetry(schema: ProductSchema, taskId: string, fallback: string): Promise<RetryOutcome> {
  const route = retryRoute(schema, taskId);
  if (!route) throw new Error('retry route unavailable');
  const response = await fetch(route, { method: 'POST' });
  if (!response.ok) {
    if (response.status === 409 || response.status === 404) throw new RetryRefused(await retryProblemText(response, fallback));
    throw new Error(`retry ${response.status}`);
  }
  const outcome = parseRetryOutcome(await response.json().catch(() => null));
  if (!outcome) throw new Error('retry response malformed');
  return outcome;
}
