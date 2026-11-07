// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest';

import { useChatStore, type ChatMessage } from '@/store/chat-store';

const message = (id: string, role: ChatMessage['role'], content: string): ChatMessage => ({ id, role, content, createdAt: 0 });

beforeEach(() => {
  useChatStore.setState({
    messagesBySession: {},
    olderCursorBySession: {},
    streamingBuffers: {},
    toolCallCounts: {},
  });
});

describe('settling a streaming reply (#177)', () => {
  it('puts the stored page in place of the buffer in one update, never dropping the turn', () => {
    useChatStore.setState({
      messagesBySession: { s1: [message('m1', 'user', 'first')] },
      olderCursorBySession: { s1: '' },
    });
    useChatStore.getState().initStreaming('s1', { content: 'second' });
    useChatStore.getState().appendStreamText('s1', 'the answer');

    const seen: { buffered: boolean; contents: string[] }[] = [];
    const unsubscribe = useChatStore.subscribe((state) => {
      seen.push({
        buffered: Boolean(state.streamingBuffers.s1),
        contents: (state.messagesBySession.s1 ?? []).map((item) => item.content),
      });
    });
    useChatStore.getState().settleStreaming('s1', {
      messages: [message('m1', 'user', 'first'), message('m2', 'user', 'second'), message('m3', 'assistant', 'the answer')],
      nextCursor: '',
    });
    unsubscribe();

    expect(useChatStore.getState().streamingBuffers.s1).toBeUndefined();
    expect(useChatStore.getState().messagesBySession.s1.map((item) => item.content)).toEqual(['first', 'second', 'the answer']);
    // No snapshot may hold the cleared buffer without the stored turn: that frame shrank the list (#177).
    for (const snapshot of seen.filter((item) => !item.buffered)) {
      expect(snapshot.contents).toContain('the answer');
    }
  });
});
