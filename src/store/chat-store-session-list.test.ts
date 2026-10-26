import { beforeEach, describe, expect, it } from 'vitest';
import { useChatStore } from '@/store/chat-store';

const session = (id: string, title = id) => ({ id, title, createdAt: '', updatedAt: '' });
const revision = () => useChatStore.getState().sessionListRevision;

/** The open session menu reads its list again whenever this revision moves (#289). */
describe('chat store session list revision', () => {
  beforeEach(() => {
    useChatStore.setState({ sessions: [session('a'), session('b')], activeSessionId: 'a', streamingBuffers: {}, sessionListRevision: 0 });
  });

  it('moves when a session is created, renamed, deleted or a turn ends', () => {
    useChatStore.getState().addSession(session('c'));
    expect(revision()).toBe(1);
    useChatStore.getState().updateSessionTitle('a', 'renamed');
    expect(revision()).toBe(2);
    useChatStore.getState().removeSession('b');
    expect(revision()).toBe(3);
    useChatStore.getState().initStreaming('a', { content: 'hi' });
    useChatStore.getState().finishStreaming('a');
    expect(revision()).toBe(4);
  });

  it('stays when nothing about the list changed', () => {
    useChatStore.getState().updateSessionTitle('a', 'a');
    useChatStore.getState().setSessions([session('a')]);
    useChatStore.getState().setActiveSession('b');
    useChatStore.getState().updateSessionApproval('a', { approvalMode: 'auto' });
    useChatStore.getState().updateSessionAgentScope('a', '3d');
    useChatStore.getState().initStreaming('a', { content: 'hi' });
    expect(revision()).toBe(0);
  });

  it('moves for the title of a session not loaded in the store', () => {
    useChatStore.getState().updateSessionTitle('elsewhere', 'title');
    expect(revision()).toBe(1);
  });
});
