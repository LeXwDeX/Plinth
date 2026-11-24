import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

import { InFlightGuard } from '@/lib/chat/in-flight-guard';
import { PendingApprovalError } from '@/lib/chat/pending-approvals';
import { continuePlanOnce, type PlanContinueHooks } from '@/lib/chat/plan-continue';
import type { ProductSchema } from '@/lib/product-schema';

// #503: the first 「继续」 click used to return before the request because the guard's
// claim was read inverted, and it kept the plan claimed, so only a second click sent it.
describe('continuing an approved plan once (#410, #503)', () => {
  const schema = {
    runtime: { http: { routes: {
      chat_session_budget_continue: '/api/v1/chat/sessions/{sessionID}/budgets/{budgetID}/continue',
    } } },
  } as unknown as ProductSchema;

  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

  function hooks() {
    return {
      started: vi.fn<PlanContinueHooks['started']>(),
      succeeded: vi.fn<PlanContinueHooks['succeeded']>(),
      failed: vi.fn<PlanContinueHooks['failed']>(),
      settled: vi.fn<PlanContinueHooks['settled']>(),
    } satisfies PlanContinueHooks;
  }

  it('sends the continue request on the first call and releases the plan afterwards', async () => {
    const guard = new InFlightGuard();
    const fetcher = vi.fn(async () => response({ turn_message_id: 'm1' }));
    const events = hooks();
    const ran = await continuePlanOnce(guard, schema, 's 1', 'b/1', events, fetcher as unknown as typeof fetch);
    expect(ran).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    const call = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(call[0]).toBe('/api/v1/chat/sessions/s%201/budgets/b%2F1/continue');
    expect(call[1].method).toBe('POST');
    expect(events.started).toHaveBeenCalledTimes(1);
    expect(events.succeeded).toHaveBeenCalledTimes(1);
    expect(events.failed).not.toHaveBeenCalled();
    expect(events.settled).toHaveBeenCalledTimes(1);
    expect(guard.has('b/1')).toBe(false);

    await continuePlanOnce(guard, schema, 's 1', 'b/1', hooks(), fetcher as unknown as typeof fetch);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('sends one request for two clicks made while the first is still running', async () => {
    const guard = new InFlightGuard();
    let answer: (value: Response) => void = () => {};
    const fetcher = vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; }));
    const first = hooks();
    const second = hooks();
    const running = continuePlanOnce(guard, schema, 's1', 'b1', first, fetcher as unknown as typeof fetch);
    const repeated = continuePlanOnce(guard, schema, 's1', 'b1', second, fetcher as unknown as typeof fetch);
    expect(await repeated).toBe(false);
    expect(second.started).not.toHaveBeenCalled();
    expect(guard.has('b1')).toBe(true);
    answer(response({ turn_message_id: 'm1' }));
    expect(await running).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(guard.has('b1')).toBe(false);
  });

  it('reports a refused continue and releases the plan so it can be tried again', async () => {
    const guard = new InFlightGuard();
    const fetcher = vi.fn(async () => response({ detail: 'ui.studio.plan.continue_inactive' }, 409));
    const events = hooks();
    expect(await continuePlanOnce(guard, schema, 's1', 'b1', events, fetcher as unknown as typeof fetch)).toBe(true);
    expect(events.succeeded).not.toHaveBeenCalled();
    expect(events.failed).toHaveBeenCalledTimes(1);
    const error = events.failed.mock.calls[0][0];
    expect(error).toBeInstanceOf(PendingApprovalError);
    expect((error as PendingApprovalError).status).toBe(409);
    expect(events.settled).toHaveBeenCalledTimes(1);
    expect(guard.has('b1')).toBe(false);

    await continuePlanOnce(guard, schema, 's1', 'b1', hooks(), fetcher as unknown as typeof fetch);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('is what the chat column runs for 「继续」', () => {
    const chat = readFileSync(new URL('../../components/studio/chat-column.tsx', import.meta.url), 'utf8');
    expect(chat).toContain('continuePlanOnce(deciding.current, schema, sessionId, budgetId,');
  });
});
