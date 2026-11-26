import { beforeEach, describe, expect, it } from 'vitest';
import { useChatStore, type ChatMessage } from '@/store/chat-store';

// #403: a turn whose stream broke is followed again from its first event. The buffer gives up what the
// first stream delivered (the replay delivers it again) but keeps the sent words and what they were sent after.

const earlier: ChatMessage = { id: 'u0', role: 'user', content: 'earlier request', createdAt: 1 };

describe('rewinding the stream buffer of a turn that is followed again', () => {
  beforeEach(() => {
    useChatStore.setState({
      activeSessionId: 'session-a',
      messagesBySession: { 'session-a': [earlier] },
      streamingBuffers: {},
      toolCallCounts: {},
    });
  });

  it('drops what the broken stream delivered and keeps the sent words and the message they follow', () => {
    const store = useChatStore.getState();
    store.initStreaming('session-a', { content: 'draw it twice', images: [{ url: '/a.png', taskId: '' }] });
    store.setAssistantDbId('session-a', 'assistant-1');
    store.appendStreamText('session-a', 'Let me draw');
    store.addStreamImages('session-a', [{ url: '/first.png', taskId: 't1' }]);
    store.addStreamPaidStep('session-a', { taskId: 't1', credits: 3, approvedByPolicy: true });
    store.updateStreamGenState('session-a', 'generating', 40);
    // The person's list changed meanwhile (an approval message arrived): the anchor must not move to it.
    useChatStore.setState({ messagesBySession: { 'session-a': [earlier, { id: 'u1', role: 'user', content: 'draw it twice', createdAt: 2 }] } });

    useChatStore.getState().rewindStreaming('session-a');

    expect(useChatStore.getState().streamingBuffers['session-a']).toEqual({
      assistantDbId: null,
      userMessage: { content: 'draw it twice', images: [{ url: '/a.png', taskId: '' }] },
      previousUserMessageId: 'u0',
      textContent: '',
      images: [],
      paidSteps: [],
      isStreaming: true,
      genState: { statusText: '', progress: null },
      turn: expect.any(Number),
    });
  });

  it('leaves the tool call count alone, which only rises', () => {
    const store = useChatStore.getState();
    store.initStreaming('session-a', { content: 'go' });
    store.observeToolCallCount('session-a', 4);
    useChatStore.getState().rewindStreaming('session-a');
    expect(useChatStore.getState().toolCallCounts['session-a']).toBe(4);
  });

  it('does nothing for a session without a buffer', () => {
    const before = useChatStore.getState().streamingBuffers;
    useChatStore.getState().rewindStreaming('session-a');
    expect(useChatStore.getState().streamingBuffers).toBe(before);
  });
});
