import { beforeEach, describe, expect, it } from 'vitest';
import { useChatStore, type ChatMessage } from '@/store/chat-store';

// #665: turn 1's end-of-turn read can come back after its fallback cleared the buffer and turn 2 started. That
// stale read must neither remove turn 2's buffer nor merge turn 1's older page over turn 2.

const message = (id: string, role: ChatMessage['role'], content: string): ChatMessage => ({ id, role, content, createdAt: 1 });

describe('a stale end of an earlier turn', () => {
  beforeEach(() => {
    useChatStore.setState({ activeSessionId: 's', messagesBySession: { s: [] }, olderCursorBySession: {}, streamingBuffers: {}, toolCallCounts: {} });
  });

  it('leaves the buffer of the turn that started after it', () => {
    const store = useChatStore.getState();
    const first = store.initStreaming('s', { content: 'one' });
    // The 5 s fallback ends turn 1 without its page.
    useChatStore.getState().finishStreaming('s', first);
    const second = useChatStore.getState().initStreaming('s', { content: 'two' });
    useChatStore.getState().appendStreamText('s', 'turn two text');
    expect(second).not.toBe(first);

    // Turn 1's slow read arrives now.
    const applied = useChatStore.getState().settleStreaming('s', { messages: [message('u1', 'user', 'one')], nextCursor: '' }, first);
    useChatStore.getState().finishStreaming('s', first);

    expect(applied).toBe(false);
    expect(useChatStore.getState().streamingBuffers.s?.textContent).toBe('turn two text');
    expect(useChatStore.getState().messagesBySession.s).toEqual([]);
  });

  it('does not merge an earlier page after a later turn already settled', () => {
    const first = useChatStore.getState().initStreaming('s', { content: 'one' });
    useChatStore.getState().finishStreaming('s', first);
    const second = useChatStore.getState().initStreaming('s', { content: 'two' });
    const newest = [message('u1', 'user', 'one'), message('a1', 'assistant', 'a'), message('u2', 'user', 'two')];
    expect(useChatStore.getState().settleStreaming('s', { messages: newest, nextCursor: '' }, second)).toBe(true);

    expect(useChatStore.getState().settleStreaming('s', { messages: [message('u1', 'user', 'one')], nextCursor: '' }, first)).toBe(false);
    expect(useChatStore.getState().messagesBySession.s?.map((item) => item.id)).toEqual(['u1', 'a1', 'u2']);
  });

  it('still settles its own turn, and a settle without a turn keeps the old behaviour', () => {
    const turn = useChatStore.getState().initStreaming('s', { content: 'one' });
    expect(useChatStore.getState().settleStreaming('s', { messages: [message('u1', 'user', 'one')], nextCursor: '' }, turn)).toBe(true);
    expect(useChatStore.getState().streamingBuffers.s).toBeUndefined();
    useChatStore.getState().initStreaming('s', { content: 'two' });
    useChatStore.getState().finishStreaming('s');
    expect(useChatStore.getState().streamingBuffers.s).toBeUndefined();
  });
});
