import { beforeEach, describe, expect, it } from 'vitest';

import { forgetUntrackedTasks } from '@/lib/workspace/task-progress';
import { MAX_CACHED_SESSIONS, useChatStore } from './chat-store';
import { MAX_TRACKED_TASKS, useWorkspaceStore } from './workspace-store';

// #665: what the page keeps per task and per session stays bounded over a long working day.

describe('bounded page caches (#665)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ trackedTasks: [], currentObject: null, pendingPreviewTaskId: null });
    useChatStore.setState({ activeSessionId: null, sessions: [], messagesBySession: {}, olderCursorBySession: {}, streamingBuffers: {} });
  });

  it('drops the oldest ended tasks past the limit and keeps every running one', () => {
    const store = useWorkspaceStore.getState();
    store.registerTask({ taskId: 'running-first', capability: 'image-to-3d', progressMaximum: 100 });
    for (let index = 0; index < MAX_TRACKED_TASKS + 10; index += 1) {
      store.registerTask({ taskId: `done-${index}`, capability: 'image-to-3d', progressMaximum: 100 });
      useWorkspaceStore.getState().markTaskNotFound(`done-${index}`);
    }
    const ids = useWorkspaceStore.getState().trackedTasks.map((task) => task.taskId);
    expect(ids.length).toBeLessThanOrEqual(MAX_TRACKED_TASKS);
    expect(ids).toContain('running-first');
    expect(ids).not.toContain('done-0');
    expect(ids).toContain(`done-${MAX_TRACKED_TASKS + 9}`);
  });

  it('forgets a deleted session\'s tasks', () => {
    useWorkspaceStore.getState().registerTask({ taskId: 't1', sessionId: 's1', capability: 'image-to-3d', progressMaximum: 100 });
    useWorkspaceStore.getState().registerTask({ taskId: 't2', sessionId: 's2', capability: 'image-to-3d', progressMaximum: 100 });
    useChatStore.getState().removeSession('s1');
    expect(useWorkspaceStore.getState().trackedTasks.map((task) => task.taskId)).toEqual(['t2']);
  });

  it('keeps message lists for a bounded number of sessions, never the open or a streaming one', () => {
    const total = MAX_CACHED_SESSIONS + 5;
    for (let index = 0; index < total; index += 1) {
      useChatStore.getState().setActiveSession(`s${index}`);
      useChatStore.getState().setMessages(`s${index}`, [{ id: `m${index}`, role: 'user', content: 'x', createdAt: index }]);
      // s0's turn is still running while the person looks at other sessions.
      if (index === 0) useChatStore.getState().initStreaming('s0', { content: 'still running' });
    }
    useChatStore.getState().setActiveSession('s3');
    const cached = Object.keys(useChatStore.getState().messagesBySession);
    expect(cached.length).toBeLessThanOrEqual(MAX_CACHED_SESSIONS);
    expect(cached).toContain('s0');
    expect(cached).toContain(`s${total - 1}`);
    expect(cached).not.toContain('s1');
  });

  it('forgets when it first saw a task that is no longer tracked', () => {
    const seen = new Map([['a', 1], ['b', 2]]);
    forgetUntrackedTasks(seen, [{ taskId: 'b' }]);
    expect([...seen.keys()]).toEqual(['b']);
  });
});
