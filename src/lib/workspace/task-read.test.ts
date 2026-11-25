import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { TaskStateSchema } from '@/lib/product-schema';
import { branchUnavailableReason } from '@/lib/chat/session-branch';
import { refreshTasksAfterDone } from '@/lib/chat/turn-settle';
import { useWorkspaceStore } from '@/store/workspace-store';

import { readTask, refreshSessionTasks } from './task-read';

// #605: the page read a turn's tasks only at its task poll, so after `done` a task the server had ended stayed
// running for up to a polling interval: the branch stayed disabled and the running task line stayed up.

const state = (internal: string, terminal: boolean, outcome: TaskStateSchema['outcome']): TaskStateSchema => ({
  internal_state: internal, public_state: internal, label: internal, color: '', terminal, outcome, progress: { mode: 'reported' },
});
const taskStates = [state('RUNNING', false, 'running'), state('SUCCEEDED', true, 'success')];
const options = { route: '/api/v1/tasks/{taskID}', taskStates, progressMaximum: 100, timeoutMilliseconds: 15_000 };

const detail = (taskState: string) => new Response(JSON.stringify({ id: 'task_1', state: taskState, progress: 50, assets: [] }), {
  status: 200, headers: { 'Content-Type': 'application/json' },
});

const register = (taskId: string, sessionId: string) =>
  useWorkspaceStore.getState().registerTask({ taskId, sessionId, capability: 'image', progressMaximum: 100, autoPreview: false });
const unended = (sessionId: string) =>
  useWorkspaceStore.getState().trackedTasks.filter((task) => task.sessionId === sessionId && !task.notFound && !task.terminal);

describe('task reads after done (#605)', () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    useWorkspaceStore.setState({ trackedTasks: [] });
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('ends the task at the first reread that finds it ended, and the branch is then available', async () => {
    register('task_1', 's1');
    fetchMock.mockResolvedValueOnce(detail('RUNNING')).mockResolvedValueOnce(detail('SUCCEEDED'));
    const wait = vi.fn(() => Promise.resolve());

    await refreshTasksAfterDone({
      refresh: () => refreshSessionTasks('s1', { ...options, isCurrent: () => true }),
      isCurrent: () => true,
      delays: [400, 1_200, 3_000],
      wait,
    });

    // Read at `done`, then at the first reread; nothing is read once no task runs.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/tasks/task_1', { signal: expect.any(AbortSignal) });
    expect(wait).toHaveBeenCalledTimes(1);
    expect(unended('s1')).toEqual([]);
    expect(branchUnavailableReason({ running: unended('s1').length > 0 })).toBe('');
  });

  it('has one read of a task in flight at a time', async () => {
    register('task_1', 's1');
    let answer: (response: Response) => void = () => {};
    fetchMock.mockReturnValueOnce(new Promise<Response>((resolve) => { answer = resolve; }));

    const poll = readTask('task_1', options);
    const afterDone = refreshSessionTasks('s1', { ...options, isCurrent: () => true });
    answer(detail('SUCCEEDED'));

    expect((await poll).kind).toBe('detail');
    expect(await afterDone).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    // Once that read is over, a later one reads again.
    fetchMock.mockResolvedValueOnce(detail('SUCCEEDED'));
    await readTask('task_1', options);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('writes nothing once the session is no longer the one shown or a new turn started', async () => {
    register('task_1', 's1');
    fetchMock.mockResolvedValue(detail('SUCCEEDED'));
    // The session was left (or a new turn of it started) while the read ran.
    expect(await refreshSessionTasks('s1', { ...options, isCurrent: () => false })).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(unended('s1').map((task) => task.taskId)).toEqual(['task_1']);

    // The follow-up stops before reading when the session is left.
    fetchMock.mockClear();
    await refreshTasksAfterDone({ refresh: () => refreshSessionTasks('s1', { ...options, isCurrent: () => false }), isCurrent: () => false, delays: [0] });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not read tasks already ended, nor those of another session', async () => {
    register('task_1', 's1');
    register('task_2', 's2');
    useWorkspaceStore.setState((store) => ({
      trackedTasks: store.trackedTasks.map((task) => (task.taskId === 'task_1' ? { ...task, terminal: true } : task)),
    }));
    const refresh = vi.fn(() => refreshSessionTasks('s1', { ...options, isCurrent: () => true }));

    await refreshTasksAfterDone({ refresh, isCurrent: () => true, delays: [400], wait: () => Promise.resolve() });

    expect(fetchMock).not.toHaveBeenCalled();
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('gives up a read that does not answer in time: nothing is written and the next read asks again', async () => {
    vi.useFakeTimers();
    register('task_1', 's1');
    const before = useWorkspaceStore.getState().trackedTasks;
    let signal: AbortSignal | undefined;
    // A half-open connection: the request never answers, not even when cancelled.
    fetchMock.mockImplementationOnce((_input, init) => {
      signal = init?.signal ?? undefined;
      return new Promise<Response>(() => {});
    });

    const hung = readTask('task_1', options);
    // A read asked for meanwhile waits for the same request.
    const joined = readTask('task_1', options);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(options.timeoutMilliseconds);

    expect(await hung).toEqual({ kind: 'unavailable' });
    expect(await joined).toEqual({ kind: 'unavailable' });
    expect(signal?.aborted).toBe(true);
    // The store was not written: the tracked tasks are the very ones from before the read.
    expect(useWorkspaceStore.getState().trackedTasks).toBe(before);

    fetchMock.mockResolvedValueOnce(detail('SUCCEEDED'));
    const next = await readTask('task_1', options);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(next.kind).toBe('detail');
  });

  it('cancels the request at the timeout and keeps no timer once a read answers', async () => {
    vi.useFakeTimers();
    register('task_1', 's1');
    fetchMock.mockImplementationOnce((_input, init) => new Promise<Response>((_, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    const read = readTask('task_1', options);
    await vi.advanceTimersByTimeAsync(options.timeoutMilliseconds);
    expect(await read).toEqual({ kind: 'unavailable' });

    fetchMock.mockResolvedValueOnce(detail('RUNNING'));
    expect((await readTask('task_1', options)).kind).toBe('detail');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('ends the task on a 404', async () => {
    register('task_1', 's1');
    fetchMock.mockResolvedValueOnce(new Response('', { status: 404 }));

    expect(await refreshSessionTasks('s1', { ...options, isCurrent: () => true })).toBe(false);
    expect(useWorkspaceStore.getState().trackedTasks.find((task) => task.taskId === 'task_1')?.notFound).toBe(true);
  });

  it.each([
    ['a 500', () => Promise.resolve(new Response('', { status: 500 }))],
    ['a 401', () => Promise.resolve(new Response('', { status: 401 }))],
    ['a network error', () => Promise.reject(new TypeError('Failed to fetch'))],
  ])('treats %s as unavailable: nothing is written and the next read asks again', async (_name, answer) => {
    register('task_1', 's1');
    const before = useWorkspaceStore.getState().trackedTasks;
    fetchMock.mockImplementationOnce(answer);

    expect(await readTask('task_1', options)).toEqual({ kind: 'unavailable' });
    expect(await refreshSessionTasks('s1', { ...options, isCurrent: () => true })).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    // The store was not written: the tracked tasks are the very ones from before the read.
    expect(useWorkspaceStore.getState().trackedTasks).toBe(before);
  });

  it('a read sent before done that still says running is put right by the rereads after done', async () => {
    register('task_1', 's1');
    let answer: (response: Response) => void = () => {};
    // The task poll asked before the turn ended; its answer comes after `done`, from before the task ended.
    fetchMock.mockReturnValueOnce(new Promise<Response>((resolve) => { answer = resolve; }));
    const poll = readTask('task_1', options);
    fetchMock.mockResolvedValueOnce(detail('SUCCEEDED'));
    const wait = vi.fn(() => Promise.resolve());

    const afterDone = refreshTasksAfterDone({
      refresh: () => refreshSessionTasks('s1', { ...options, isCurrent: () => true }),
      isCurrent: () => true,
      delays: [400, 1_200],
      wait,
    });
    answer(detail('RUNNING'));
    await poll;
    await afterDone;

    // The read at `done` joined the old one (still running); the first reread asked again and found it ended.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(wait).toHaveBeenCalledTimes(1);
    const task = useWorkspaceStore.getState().trackedTasks.find((tracked) => tracked.taskId === 'task_1');
    expect([task?.state, task?.terminal]).toEqual(['SUCCEEDED', true]);
  });
});
