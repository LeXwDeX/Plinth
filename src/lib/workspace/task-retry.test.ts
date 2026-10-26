import { afterEach, describe, expect, it, vi } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { alternativeOffering, canRetryTask, isBalanceFailure, parseRetryOutcome, requestRetry, retryRoute, RetryRefused } from '@/lib/workspace/task-retry';

const schema = { runtime: { http: { routes: { task_retry: '/api/v1/tasks/{taskID}/retry' } } } } as unknown as ProductSchema;
const failure = (patch: Partial<{ retryable: boolean; ambiguous: boolean }> = {}) =>
  ({ failureClass: 'upstream_unavailable', detail: 'x', retryable: true, ambiguous: false, ...patch });

afterEach(() => vi.unstubAllGlobals());

describe('canRetryTask', () => {
  it('offers retry only for finished, retryable, unambiguous failures', () => {
    expect(canRetryTask({ terminal: true, failure: failure() })).toBe(true);
    expect(canRetryTask({ terminal: false, failure: failure() })).toBe(false);
    expect(canRetryTask({ terminal: true, failure: null })).toBe(false);
    expect(canRetryTask({ terminal: true, failure: failure({ retryable: false }) })).toBe(false);
    // The upstream may already have taken an ambiguous job; resubmitting could charge twice.
    expect(canRetryTask({ terminal: true, failure: failure({ ambiguous: true }) })).toBe(false);
    expect(canRetryTask({ terminal: true, notFound: true, failure: failure() })).toBe(false);
  });
});

describe('insufficient balance', () => {
  const balance = failure({ retryable: true });
  balance.failureClass = 'insufficient_balance';

  it('never offers retry: the same model would fail the same way', () => {
    expect(isBalanceFailure(balance)).toBe(true);
    expect(isBalanceFailure(failure())).toBe(false);
    expect(canRetryTask({ terminal: true, failure: balance })).toBe(false);
  });

  const offering = (id: string, model: string, patch: Record<string, unknown> = {}) =>
    ({ id, model_id: model, model_name: model.toUpperCase(), capability: 'text-to-image', available: true, ...patch });

  it('offers another available model of the same capability, the recommended one first', () => {
    const list = [offering('o1', 'a'), offering('o2', 'b'), offering('o3', 'c', { recommended: true })];
    expect(alternativeOffering(list, { capability: 'text-to-image', offeringId: 'o1', modelId: 'a' })?.id).toBe('o3');
  });

  it('offers nothing when the capability has no other enabled model (a retexture with one provider)', () => {
    const only = [offering('o1', 'a')];
    expect(alternativeOffering(only, { capability: 'text-to-image', offeringId: 'o1', modelId: 'a' })).toBeNull();
    expect(alternativeOffering([offering('o1', 'a'), offering('o2', 'b', { available: false })], { capability: 'text-to-image', offeringId: 'o1', modelId: 'a' })).toBeNull();
    expect(alternativeOffering([offering('o1', 'a'), offering('o2', 'b', { capability: 'image-to-image' })], { capability: 'text-to-image', offeringId: 'o1', modelId: 'a' })).toBeNull();
    expect(alternativeOffering(undefined, { capability: 'text-to-image' })).toBeNull();
  });

  it('does not offer another Offering of the very model that just failed', () => {
    const list = [offering('o1', 'a'), offering('o2', 'a')];
    expect(alternativeOffering(list, { capability: 'text-to-image', offeringId: 'o1', modelId: 'a' })).toBeNull();
  });
});

describe('retry route and answers', () => {
  it('renders the Schema route with an escaped task id', () => {
    expect(retryRoute(schema, 'a/b')).toBe('/api/v1/tasks/a%2Fb/retry');
  });

  it('parses both answers and rejects anything else', () => {
    expect(parseRetryOutcome({ status: 'submitted', task_id: 't2' })).toEqual({ status: 'submitted', taskId: 't2' });
    expect(parseRetryOutcome({ status: 'awaiting_decision', prepared_input_id: 'p', request_id: 'r', idempotency_key: 'k' }))
      .toEqual({ status: 'awaiting_decision', preparedInputId: 'p', requestId: 'r', key: 'k' });
    expect(parseRetryOutcome({ status: 'awaiting_decision', prepared_input_id: 'p' })).toBeNull();
    expect(parseRetryOutcome({ status: 'submitted' })).toBeNull();
    expect(parseRetryOutcome(null)).toBeNull();
  });
});

describe('requestRetry', () => {
  it('sends one POST with no body and returns the answer', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'submitted', task_id: 't2' }), { status: 202 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(requestRetry(schema, 't1', 'fallback')).resolves.toEqual({ status: 'submitted', taskId: 't2' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/tasks/t1/retry', { method: 'POST' });
  });

  it('surfaces a refusal with the server text and other failures as plain errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ detail: '不能重试' }), { status: 409 })));
    await expect(requestRetry(schema, 't1', 'fallback')).rejects.toMatchObject({ message: '不能重试', constructor: RetryRefused });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 409 })));
    await expect(requestRetry(schema, 't1', 'fallback')).rejects.toThrow('fallback');
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 500 })));
    await expect(requestRetry(schema, 't1', 'fallback')).rejects.not.toBeInstanceOf(RetryRefused);
  });
});
