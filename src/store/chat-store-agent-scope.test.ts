import { beforeEach, describe, expect, it } from 'vitest';
import { useChatStore } from '@/store/chat-store';

const session = (id: string) => ({ id, title: id, createdAt: '', updatedAt: '' });

describe('chat store agent scope', () => {
  beforeEach(() => {
    useChatStore.setState({ sessions: [session('a'), session('b')], streamingBuffers: {} });
  });

  it('updates only the named session and nothing else about it', () => {
    useChatStore.getState().updateSessionApproval('a', { approvalMode: 'auto', estimatedCredits: 5 });
    useChatStore.getState().updateSessionAgentScope('a', '3d');
    const [first, second] = useChatStore.getState().sessions;
    expect(first).toMatchObject({ id: 'a', agentScope: '3d', approvalMode: 'auto', estimatedCredits: 5 });
    expect(second.agentScope).toBeUndefined();
  });

  it('ignores an unknown session', () => {
    useChatStore.getState().updateSessionAgentScope('missing', '2d');
    expect(useChatStore.getState().sessions.every((item) => item.agentScope === undefined)).toBe(true);
  });
});
