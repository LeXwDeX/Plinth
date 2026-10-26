/**
 * 刷新回灌：工作区挂载后从服务端任务列表把仍在运行的任务重新注册进
 * 跟踪集合，使托盘/轮询在刷新后自动恢复，不依赖丢失的 SSE 现场。
 *
 * 沿 v1 任务列表的 next_cursor 翻页直到结束，客户端仅注册 Product Schema
 * `task_states[]` 明确认识的非终态任务——不向服务端传 state 词汇，避免
 * Schema 里并存的两套状态词汇（internal/public）与 API 校验漂移；
 * 响应消费 v1 任务列表投影（tasks[]，next_cursor 游标），注册走
 * workspace store 的 registerTask（按 taskId 幂等去重），与对话内 SSE
 * taskRegistered 双路径可安全叠加。
 */

import type { TaskStateSchema } from '@/lib/product-schema';
import { projectNativeTaskDetail, type TrackedTaskProjection } from '@/lib/workspace/task-projection';

export interface RunningTaskHydrationInput {
  taskId: string;
  sessionId?: string;
  capability: string;
  progressMaximum: number;
}

export interface HydrateRunningTasksOptions {
  tasksRoute: string;
  taskStates: Array<{ internal_state: string; terminal: boolean }>;
  progressMaximum: number;
  register: (input: RunningTaskHydrationInput) => void;
  fetchFn?: typeof fetch;
  signal?: AbortSignal;
}

export interface HydrateSessionTasksOptions {
  tasksRoute: string;
  sessionId: string;
  limit: number;
  taskStates: TaskStateSchema[];
  progressMaximum: number;
  consume: (input: RunningTaskHydrationInput, projection: TrackedTaskProjection) => void;
  fetchFn?: typeof fetch;
  signal?: AbortSignal;
}

const HYDRATION_PAGE_LIMIT = '50';

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export async function hydrateRunningTasks(
  options: HydrateRunningTasksOptions,
): Promise<void> {
  const fetchFn = options.fetchFn ?? fetch;
  const knownStates = new Set(options.taskStates.map((state) => state.internal_state));
  const terminalStates = new Set(options.taskStates.filter((state) => state.terminal).map((state) => state.internal_state));
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  while (!options.signal?.aborted) {
    if (options.signal?.aborted) return;
    const hashIndex = options.tasksRoute.indexOf('#');
    const routeWithoutHash = hashIndex < 0 ? options.tasksRoute : options.tasksRoute.slice(0, hashIndex);
    const hash = hashIndex < 0 ? '' : options.tasksRoute.slice(hashIndex);
    const queryIndex = routeWithoutHash.indexOf('?');
    const baseRoute = queryIndex < 0 ? routeWithoutHash : routeWithoutHash.slice(0, queryIndex);
    const params = new URLSearchParams(queryIndex < 0 ? '' : routeWithoutHash.slice(queryIndex + 1));
    params.set('limit', HYDRATION_PAGE_LIMIT);
    if (cursor) params.set('cursor', cursor);
    else params.delete('cursor');
    const route = `${baseRoute}?${params.toString()}${hash}`;
    const response = await fetchFn(route, { signal: options.signal });
    if (options.signal?.aborted || !response.ok) return;
    const page: unknown = await response.json();
    if (options.signal?.aborted) return;
    // v1 任务列表投影键为 tasks[]；兼容 data[] 以防投影别名演进。
    const items = isRecord(page) && Array.isArray(page.tasks)
      ? page.tasks
      : isRecord(page) && Array.isArray(page.data) ? page.data : [];
    for (const item of items) {
      if (options.signal?.aborted) return;
      if (!isRecord(item) || typeof item.id !== 'string' || item.id === '') continue;
      // 仅回灌当前 Schema 明确认识的非终态，避免将未知/新态误当成运行中。
      if (typeof item.state !== 'string' || !knownStates.has(item.state) || terminalStates.has(item.state)) continue;
      options.register({
        taskId: item.id,
        sessionId: typeof item.session_id === 'string' && item.session_id !== '' ? item.session_id : undefined,
        capability: typeof item.capability === 'string' ? item.capability : '',
        progressMaximum: options.progressMaximum,
      });
    }
    const nextCursor = isRecord(page) && typeof page.next_cursor === 'string' ? page.next_cursor : '';
    if (!nextCursor || seenCursors.has(nextCursor)) return;
    seenCursors.add(nextCursor);
    cursor = nextCursor;
  }
}

/** Restore the current session's recent task cards from the server after a
 * history switch or browser reload. Older versions remain in paged history. */
export async function hydrateSessionTasks(options: HydrateSessionTasksOptions): Promise<void> {
  if (!options.sessionId || options.signal?.aborted) return;
  const hashIndex = options.tasksRoute.indexOf('#');
  const routeWithoutHash = hashIndex < 0 ? options.tasksRoute : options.tasksRoute.slice(0, hashIndex);
  const queryIndex = routeWithoutHash.indexOf('?');
  const baseRoute = queryIndex < 0 ? routeWithoutHash : routeWithoutHash.slice(0, queryIndex);
  const params = new URLSearchParams(queryIndex < 0 ? '' : routeWithoutHash.slice(queryIndex + 1));
  params.set('session_id', options.sessionId);
  params.set('limit', String(options.limit));
  params.delete('cursor');
  const route = `${baseRoute}?${params.toString()}`;
  const response = await (options.fetchFn ?? fetch)(route, { signal: options.signal });
  if (!response.ok || options.signal?.aborted) return;
  const page: unknown = await response.json();
  if (options.signal?.aborted) return;
  const items = isRecord(page) && Array.isArray(page.tasks)
    ? page.tasks
    : isRecord(page) && Array.isArray(page.data) ? page.data : [];
  const knownStates = new Set(options.taskStates.map((state) => state.internal_state));
  for (const item of items) {
    if (options.signal?.aborted) return;
    if (!isRecord(item) || typeof item.id !== 'string' || !item.id || item.session_id !== options.sessionId) continue;
    if (typeof item.state !== 'string' || !knownStates.has(item.state)) continue;
    const projection = projectNativeTaskDetail(item, options.taskStates, options.progressMaximum);
    if (!projection) continue;
    options.consume({ taskId: item.id, sessionId: options.sessionId,
      capability: typeof item.capability === 'string' ? item.capability : '',
      progressMaximum: options.progressMaximum }, projection);
  }
}
