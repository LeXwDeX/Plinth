import { expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import {
  hydrateRunningTasks,
  hydrateSessionTasks,
  type RunningTaskHydrationInput,
} from '@/lib/workspace/task-hydration';
import type { TaskStateSchema } from '@/lib/product-schema';

/**
 * 刷新回灌：取最近一页任务，客户端按 Schema 终态跳过，只注册仍在进行
 * 的任务；registerTask 幂等去重，与 SSE taskRegistered 双路径安全叠加。
 * 不向服务端传 state 词汇（Schema 内并存 internal/public 两套词汇，传
 * 参会与 API 校验漂移——真机实测 400 的教训）。
 */

const appSchema = defaultCatalog.application;

const taskStates = appSchema.task_states.map((state) => ({
  internal_state: state.internal_state,
  terminal: state.terminal,
}));

async function run(
  payload: unknown,
  ok = true,
): Promise<{ registered: RunningTaskHydrationInput[]; url: string }> {
  const registered: RunningTaskHydrationInput[] = [];
  let url = '';
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    url = String(input);
    if (!ok) return new Response('boom', { status: 500 });
    return new Response(JSON.stringify(payload), { status: 200 });
  });
  await hydrateRunningTasks({
    tasksRoute: appSchema.runtime.http.routes.tasks,
    taskStates,
    progressMaximum: appSchema.runtime.frontend.percentage_multiplier,
    register: (input) => registered.push(input),
    fetchFn: fetchFn as unknown as typeof fetch,
  });
  return { registered, url };
}

it('accepts the v1 tasks projection key with next_cursor', async () => {
  const { registered } = await run({
    tasks: [
      { id: 'task_a', state: 'RUNNING', session_id: 's1', capability: 'text-to-image' },
    ],
    next_cursor: null,
  });
  expect(registered.map((task) => task.taskId)).toEqual(['task_a']);
});

it('registers only known non-terminal tasks and preserves session IDs', async () => {
  const { registered, url } = await run({
    tasks: [
      {
        id: 'task_running',
        session_id: 'session_a',
        capability: 'text-to-3d',
        state: 'RUNNING',
      },
      { id: 'task_done', state: 'SUCCEEDED' },
      { id: 'task_upstream', state: 'UPSTREAM_COMPLETED', session_id: 'session_b' },
      { id: 'task_unknown', state: 'FUTURE_STATE' },
      { id: 'task_cancel', state: 'CANCELED' },
      { session_id: 'session_c' },
      'garbage',
    ],
  });

  expect(registered.map((task) => task.taskId)).toEqual([
    'task_running',
    'task_upstream',
  ]);
  expect(registered[0]).toMatchObject({
    sessionId: 'session_a',
    capability: 'text-to-3d',
  });
  expect(url).toContain('limit=50');
  expect(url).not.toContain('state=');
});

it('follows next_cursor pages and stops when a cursor repeats', async () => {
  const registered: RunningTaskHydrationInput[] = [];
  const urls: string[] = [];
  const responses = [
    { tasks: [{ id: 'task_first', state: 'RUNNING', session_id: 's1' }], next_cursor: 'c1' },
    { tasks: [{ id: 'task_second', state: 'QUEUED', session_id: 's2' }], next_cursor: 'c1' },
  ];
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    urls.push(String(input));
    return new Response(JSON.stringify(responses[urls.length - 1]), { status: 200 });
  });
  await hydrateRunningTasks({
    tasksRoute: '/api/v1/tasks?session_id=owned',
    taskStates,
    progressMaximum: 100,
    register: (input) => registered.push(input),
    fetchFn: fetchFn as unknown as typeof fetch,
  });
  expect(registered.map(({ taskId }) => taskId)).toEqual(['task_first', 'task_second']);
  expect(registered.map(({ sessionId }) => sessionId)).toEqual(['s1', 's2']);
  expect(urls).toEqual([
    '/api/v1/tasks?session_id=owned&limit=50',
    '/api/v1/tasks?session_id=owned&limit=50&cursor=c1',
  ]);
  expect(fetchFn).toHaveBeenCalledTimes(2);
});

it('recovers active tasks beyond the former 20-page limit', async () => {
  const registered: RunningTaskHydrationInput[] = [];
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost');
    const cursor = url.searchParams.get('cursor');
    if (cursor === 'c20') {
      return new Response(JSON.stringify({ tasks: [{ id: 'older_live_task', state: 'RUNNING', session_id: 'old-session' }] }), { status: 200 });
    }
    const pageNumber = cursor ? Number(cursor.slice(1)) + 1 : 1;
    return new Response(JSON.stringify({ tasks: [], next_cursor: `c${pageNumber}` }), { status: 200 });
  });
  await hydrateRunningTasks({
    tasksRoute: '/api/v1/tasks', taskStates, progressMaximum: 100,
    register: (input) => registered.push(input),
    fetchFn: fetchFn as unknown as typeof fetch,
  });
  expect(fetchFn).toHaveBeenCalledTimes(21);
  expect(registered).toEqual([{
    taskId: 'older_live_task', sessionId: 'old-session', capability: '', progressMaximum: 100,
  }]);
});

it('passes AbortSignal and stops before requesting another page after cancellation', async () => {
  const controller = new AbortController();
  const registered: RunningTaskHydrationInput[] = [];
  const fetchFn = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    expect(init?.signal).toBe(controller.signal);
    controller.abort();
    return new Response(JSON.stringify({ tasks: [{ id: 'task_1', state: 'RUNNING' }], next_cursor: 'c1' }), { status: 200 });
  });
  await hydrateRunningTasks({
    tasksRoute: '/api/v1/tasks', taskStates, progressMaximum: 100,
    register: (input) => registered.push(input),
    fetchFn: fetchFn as unknown as typeof fetch, signal: controller.signal,
  });
  expect(registered).toHaveLength(0);
  expect(fetchFn).toHaveBeenCalledTimes(1);
});

it('stays silent when the task list is unavailable', async () => {
  const { registered } = await run({ tasks: [] }, false);
  expect(registered).toHaveLength(0);
});

it('reuses an existing query string on the schema route', async () => {
  const registered: RunningTaskHydrationInput[] = [];
  let url = '';
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    url = String(input);
    return new Response(JSON.stringify({ tasks: [], next_cursor: null }), {
      status: 200,
    });
  });
  await hydrateRunningTasks({
    tasksRoute: '/api/v1/tasks?capability=text-to-3d',
    taskStates,
    progressMaximum: 100,
    register: (input) => registered.push(input),
    fetchFn: fetchFn as unknown as typeof fetch,
  });
  expect(url).toBe('/api/v1/tasks?capability=text-to-3d&limit=50');
});

it('restores only the selected session task cards from server history', async () => {
  let requested = '';
  const cards: Array<{ taskId: string; sessionId?: string; terminal: boolean; assets: number }> = [];
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    requested = String(input);
    return new Response(JSON.stringify({ tasks: [
      { id: 'done', session_id: 'selected', capability: 'text-to-image', state: 'SUCCEEDED', progress: 100,
        assets: [{ id: 'asset-1', role: 'image', variant: 'png', media_type: 'image/png', url: '/api/v1/tasks/done/assets/asset-1' }] },
      { id: 'foreign', session_id: 'other', capability: 'text-to-3d', state: 'RUNNING' },
      { id: 'unknown', session_id: 'selected', state: 'FUTURE_STATE' },
    ] }), { status: 200 });
  });
  await hydrateSessionTasks({
    tasksRoute: '/api/v1/tasks?capability=text-to-image', sessionId: 'selected', limit: 12,
    taskStates: appSchema.task_states as TaskStateSchema[], progressMaximum: 100,
    fetchFn: fetchFn as unknown as typeof fetch,
    consume: (input, projection) => cards.push({ taskId: input.taskId, sessionId: input.sessionId,
      terminal: projection.terminal, assets: projection.downloadables.length }),
  });
  expect(requested).toBe('/api/v1/tasks?capability=text-to-image&session_id=selected&limit=12');
  expect(cards).toEqual([{ taskId: 'done', sessionId: 'selected', terminal: true, assets: 1 }]);
});
