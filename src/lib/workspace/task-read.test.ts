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
const options = { route: '/api/v1/tasks/{taskID}', taskStates, progressMaximum: 100 };

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
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/tasks/task_1');
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
});
