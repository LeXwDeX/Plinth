import { beforeEach, describe, expect, it } from 'vitest';
import { useChatStore } from '@/store/chat-store';

const session = (id: string) => ({ id, title: id, createdAt: '', updatedAt: '' });

describe('chat store approval state', () => {
  beforeEach(() => {
    useChatStore.setState({ sessions: [session('a'), session('b')], streamingBuffers: {} });
  });

  it('updates only the named session approval mode and credits', () => {
    useChatStore.getState().updateSessionApproval('a', { approvalMode: 'auto', estimatedCredits: 12 });
    const [first, second] = useChatStore.getState().sessions;
    expect(first).toMatchObject({ id: 'a', approvalMode: 'auto', estimatedCredits: 12 });
    expect(second.approvalMode).toBeUndefined();
    expect(second.estimatedCredits).toBeUndefined();
  });

  it('keeps the other field when only one is given', () => {
    useChatStore.getState().updateSessionApproval('a', { approvalMode: 'auto', estimatedCredits: 12 });
    useChatStore.getState().updateSessionApproval('a', { estimatedCredits: 20 });
    expect(useChatStore.getState().sessions[0]).toMatchObject({ approvalMode: 'auto', estimatedCredits: 20 });
  });

  it('ignores an unknown session', () => {
    useChatStore.getState().updateSessionApproval('missing', { approvalMode: 'auto' });
    expect(useChatStore.getState().sessions.every((item) => item.approvalMode === undefined)).toBe(true);
  });

  it('records each streamed paid step once', () => {
    const store = useChatStore.getState();
    store.initStreaming('a', { content: 'go' });
    store.addStreamPaidStep('a', { taskId: 't1', credits: 3, approvedByPolicy: true });
    store.addStreamPaidStep('a', { taskId: 't1', credits: 3, approvedByPolicy: true });
    store.addStreamPaidStep('a', { taskId: 't2', credits: 40, approvedByPolicy: false });
    expect(useChatStore.getState().streamingBuffers.a.paidSteps).toEqual([
      { taskId: 't1', credits: 3, approvedByPolicy: true },
      { taskId: 't2', credits: 40, approvedByPolicy: false },
    ]);
  });
});
