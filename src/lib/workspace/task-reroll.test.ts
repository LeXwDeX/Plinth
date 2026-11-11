import { afterEach, describe, expect, it, vi } from 'vitest';

import type { ProductSchema } from '@/lib/product-schema';
import { parseRetryOutcome } from '@/lib/workspace/task-retry';
import { requestReroll, rerollRoute, RerollRefused, taskRerollKey } from '@/lib/workspace/task-reroll';

const schema = { runtime: { http: { routes: { task_reroll: '/api/v1/tasks/{taskID}/reroll' } } } } as unknown as ProductSchema;

afterEach(() => vi.unstubAllGlobals());

describe('reroll route', () => {
  it('renders the task route and returns null when the Schema declares none', () => {
    expect(rerollRoute(schema, 'task 1')).toBe('/api/v1/tasks/task%201/reroll');
    expect(rerollRoute({ runtime: { http: { routes: {} } } } as unknown as ProductSchema, 't')).toBeNull();
  });

  it('generates a fresh, bounded key per request', () => {
    const first = taskRerollKey();
    const second = taskRerollKey();
    expect(first).not.toBe('');
    expect(first).not.toBe(second);
  });
});

describe('requestReroll', () => {
  it('sends the Idempotency-Key and parses the submitted task', async () => {
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      expect(init?.method).toBe('POST');
      expect((init?.headers as Record<string, string>)['Idempotency-Key']).toBe('client-1');
      return new Response(JSON.stringify({ status: 'submitted', task_id: 'task-2', replayed: false }), { status: 202 });
    });
    vi.stubGlobal('fetch', fetchMock);
    await expect(requestReroll(schema, 'task-1', 'client-1', 'reroll failed')).resolves.toEqual({
      status: 'submitted', taskId: 'task-2',
    });
  });

  it('parses awaiting_decision exactly like retry', () => {
    expect(parseRetryOutcome({
      status: 'awaiting_decision', prepared_input_id: 'p1', request_id: 'r1', idempotency_key: 'k1',
    })).toEqual({ status: 'awaiting_decision', preparedInputId: 'p1', requestId: 'r1', key: 'k1' });
  });

  it('turns a 409 into the server Schema text', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ detail: '只有已成功的任务才能再来一个。' }), { status: 409 })));
    await expect(requestReroll(schema, 'task-1', 'client-1', 'fallback')).rejects.toBeInstanceOf(RerollRefused);
  });
});
