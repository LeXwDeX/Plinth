import { beforeEach, expect, it } from 'vitest';

import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';
import { captureSessionScope, isCurrentSessionScope, selectionForSession } from './session-scope';

beforeEach(() => {
  useChatStore.setState(useChatStore.getInitialState(), true);
  useWorkspaceStore.setState(useWorkspaceStore.getInitialState(), true);
});

it('clears creative inputs at navigation while preserving task facts and other conversations', () => {
  const chat = useChatStore.getState();
  chat.setActiveSession('a');
  chat.setMessages('a', [{ id: 'message', role: 'user', content: 'A', createdAt: 1 }]);
  chat.initStreaming('a', { content: 'running A' });
  const workspace = useWorkspaceStore.getState();
  workspace.registerTask({ taskId: 'task-a', sessionId: 'a', capability: 'text-to-image', progressMaximum: 100 });
  workspace.setCurrentObject({ taskId: 'task-a', sessionId: 'a', kind: 'image', url: '/a.png' });
  workspace.setCanvasReference('/a.png');
  workspace.queueChatAttachments(['/a.png']);
  chat.setActiveSession('b');
  expect(useWorkspaceStore.getState()).toMatchObject({ currentObject: null, canvasReference: null,
    pendingChatAttachments: [], pendingPreviewTaskId: null });
  expect(useWorkspaceStore.getState().trackedTasks[0].taskId).toBe('task-a');
  expect(useChatStore.getState().messagesBySession.a[0].content).toBe('A');
  expect(useChatStore.getState().streamingBuffers.a).toBeDefined();
});

it('rejects pending work after leaving and returning to the same session', () => {
  const chat = useChatStore.getState();
  chat.setActiveSession('a');
  const pending = captureSessionScope();
  chat.setActiveSession('a');
  expect(isCurrentSessionScope(pending)).toBe(true);
  chat.setActiveSession('b');
  chat.setActiveSession('a');
  expect(isCurrentSessionScope(pending)).toBe(false);
  expect(isCurrentSessionScope(captureSessionScope())).toBe(true);
});

it('requires explicit matching ownership for preview and clears inputs when the active session is removed', () => {
  const selection = { taskId: 'task', sessionId: 'a', kind: 'model' as const, url: '/model.glb' };
  expect(selectionForSession(selection, 'a')).toBe(selection);
  expect(selectionForSession(selection, 'b')).toBeNull();
  expect(selectionForSession({ ...selection, sessionId: undefined }, 'a')).toBeNull();
  expect(selectionForSession(selection, null)).toBeNull();
  useChatStore.getState().setActiveSession('a');
  useWorkspaceStore.getState().setCurrentObject(selection);
  useChatStore.getState().removeSession('a');
  expect(useWorkspaceStore.getState().currentObject).toBeNull();
  expect(useChatStore.getState().activeSessionId).toBeNull();
});
