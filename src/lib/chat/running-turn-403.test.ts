import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { recoverChatSession } from '@/lib/chat/session-recovery';
import { editTarget, lastExchange, regenerateTarget } from '@/lib/chat/turn-actions';
import { useChatStore, type ChatMessage } from '@/store/chat-store';

// #403: the server marks the reply of a turn that has not ended as pending, whatever text it holds. The
// reply of a multi-round turn already holds its first round's text and tool calls, so the browser must keep
// them, reconnect the stream of the running turn after a reload, and offer no replace actions meanwhile.

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

const OPTIMISTIC_PREFIX = 'optimistic';

const user: ChatMessage = { id: 'u1', role: 'user', content: 'draw it twice', createdAt: 1 };
// What the server stores after round one of a running turn: real text and a tool call, no placeholder.
const runningReply: ChatMessage = {
  id: 'a1', role: 'assistant', content: 'Let me draw the first one.', isPending: true,
  toolCalls: [{ name: 'generate_image' }], toolCallCount: 1, createdAt: 2,
};
const finishedReply: ChatMessage = { ...runningReply, content: 'Both are done.', isPending: false };

describe('reading the message projection of a running turn', () => {
  it('keeps the stored text and tool calls of a reply the server marks as pending', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ messages: [
        { [fields.id]: 'u1', [fields.role]: schema.chat.roles.user, [fields.content]: 'draw it twice', [fields.created_at]: 1 },
        {
          [fields.id]: 'a1', [fields.role]: schema.chat.roles.assistant, [fields.content]: 'Let me draw the first one.',
          [fields.is_pending]: true, [fields.created_at]: 2,
          [fields.tool_calls]: JSON.stringify([{ [fields.tool_name]: 'generate_image' }]),
        },
      ], next_cursor: '' }),
    });
    const { messages } = await client.listMessagePage('session', { limit: 50 });
    expect(messages[1]).toMatchObject({
      id: 'a1', isPending: true, content: 'Let me draw the first one.', toolCalls: [{ name: 'generate_image' }], toolCallCount: 1,
    });
    expect(messages[0].isPending).toBe(false);
  });

  it('still shows the placeholder text as nothing', async () => {
    const schema = schemaFixture();
    const fields = schema.chat.api.response_fields;
    const client = createChatBrowserClient(schema, {
      fetcher: async () => Response.json({ messages: [
        { [fields.id]: 'a1', [fields.role]: schema.chat.roles.assistant, [fields.content]: schema.chat.state.pending_content, [fields.created_at]: 2 },
        // The server may mark the placeholder too: it still reads as empty.
        { [fields.id]: 'a2', [fields.role]: schema.chat.roles.assistant, [fields.content]: schema.chat.state.pending_content, [fields.is_pending]: true, [fields.created_at]: 3 },
      ], next_cursor: '' }),
    });
    const { messages } = await client.listMessagePage('session', { limit: 50 });
    expect(messages.map((message) => [message.isPending, message.content])).toEqual([[true, ''], [true, '']]);
  });
});

describe('no replace action while the turn runs', () => {
  it('offers neither regenerate nor edit-and-resend on a running reply that holds its first round', () => {
    expect(lastExchange([user, runningReply], OPTIMISTIC_PREFIX)).toBeNull();
    expect(regenerateTarget([user, runningReply], OPTIMISTIC_PREFIX)).toBeNull();
    expect(editTarget([user, runningReply], OPTIMISTIC_PREFIX)).toBeNull();
  });

  it('offers both again once the turn has ended and the server stops marking the reply', () => {
    expect(regenerateTarget([user, finishedReply], OPTIMISTIC_PREFIX)).toMatchObject({ assistantId: 'a1', deleteIds: ['a1', 'u1'] });
    expect(editTarget([user, finishedReply], OPTIMISTIC_PREFIX)).toMatchObject({ userId: 'u1' });
  });
});

describe('reloading a session whose turn is still running', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useChatStore.setState({ activeSessionId: 'session-a', messagesBySession: {}, streamingBuffers: {} });
  });
  afterEach(() => vi.useRealTimers());

  function recovery(pages: ChatMessage[][]) {
    const closed = vi.fn();
    const callbacks = { onDone: () => {}, onError: () => {} };
    const connect = vi.fn((_id: string, options: { fromId: number; onDone(): void; onError(): void }) => {
      Object.assign(callbacks, options);
      return closed;
    });
    const listMessagePage = vi.fn(async () => ({ messages: pages.length > 1 ? pages.shift()! : pages[0], nextCursor: '' }));
    const stop = recoverChatSession({
      sessionId: 'session-a', client: { listMessagePage }, pageSize: 50, connect, pollMilliseconds: 500,
      onLoading: () => {}, onLoadError: () => {},
    });
    return { stop, connect, callbacks, closed, listMessagePage };
  }

  it('reconnects the stream from the start of the turn although the reply already holds text', async () => {
    const f = recovery([[user, runningReply], [user, finishedReply]]);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.connect).toHaveBeenCalledTimes(1);
    expect(f.connect.mock.calls[0][1].fromId).toBe(0);
    expect(useChatStore.getState().streamingBuffers['session-a']).toBeDefined();
    f.callbacks.onDone();
    await vi.advanceTimersByTimeAsync(0);
    expect(useChatStore.getState().streamingBuffers['session-a']).toBeUndefined();
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual([user, finishedReply]);
    f.stop();
  });

  it('keeps reading the turn when its stream cannot be joined, and stops once the reply is no longer marked', async () => {
    const f = recovery([[user, runningReply], [user, runningReply], [user, finishedReply]]);
    await vi.advanceTimersByTimeAsync(0);
    f.callbacks.onError();
    await vi.advanceTimersByTimeAsync(500);
    expect(f.listMessagePage).toHaveBeenCalledTimes(2);
    expect(useChatStore.getState().messagesBySession['session-a']?.[1]).toMatchObject({ isPending: true, content: runningReply.content });
    await vi.advanceTimersByTimeAsync(500);
    expect(f.listMessagePage).toHaveBeenCalledTimes(3);
    expect(useChatStore.getState().messagesBySession['session-a']).toEqual([user, finishedReply]);
    await vi.advanceTimersByTimeAsync(5000);
    expect(f.listMessagePage).toHaveBeenCalledTimes(3);
    // The one stream attempt was made; reading the turn never reopened it.
    expect(f.connect).toHaveBeenCalledTimes(1);
    f.stop();
  });
});
