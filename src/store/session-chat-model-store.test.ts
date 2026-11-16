import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ChatBrowserError, type ChatBrowserClient, type ChatModelChoice } from '@/lib/chat/browser-client';
import { loadSessionChatModel, switchSessionChatModel, useSessionContextStore } from '@/store/session-context-store';

const choice = (selected: string, extra: Partial<ChatModelChoice> = {}): ChatModelChoice => ({
  selected, chosen: selected, defaultId: 'LLM', fallback: false,
  options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm-5.3' }], ...extra,
});

function fakeClient(overrides: Partial<ChatBrowserClient>): ChatBrowserClient {
  return overrides as ChatBrowserClient;
}

beforeEach(() => useSessionContextStore.setState({ reasoning: {}, reasoningBusy: {}, reasoningFailed: {}, context: {}, compacting: {}, chatModel: {}, chatModelBusy: {}, chatModelFailed: {} }));

describe('session chat model state (#322)', () => {
  it('keeps each session its own model', async () => {
    const client = fakeClient({ getChatModel: vi.fn(async (id: string) => choice(id === 'a' ? 'LLM' : 'GLM')) });
    await loadSessionChatModel(client, 'a');
    await loadSessionChatModel(client, 'b');
    expect(useSessionContextStore.getState().chatModel.a.selected).toBe('LLM');
    expect(useSessionContextStore.getState().chatModel.b.selected).toBe('GLM');
  });

  it('switches, then reads the level and the window of the new model again', async () => {
    const getReasoning = vi.fn(async () => ({ effort: 'max', levels: ['max'], defaultLevel: 'max' }));
    const getContext = vi.fn(async () => ({ tokens: null, window: 200_000, compactions: [] }));
    const client = fakeClient({ setChatModel: vi.fn(async () => choice('GLM')), getReasoning, getContext });
    useSessionContextStore.setState({ chatModel: { a: choice('LLM', { chosen: '' }) } });
    expect(await switchSessionChatModel(client, 'a', 'GLM')).toBe(true);
    const state = useSessionContextStore.getState();
    expect(state.chatModel.a.selected).toBe('GLM');
    expect(state.reasoning.a.levels).toEqual(['max']);
    expect(state.context.a.window).toBe(200_000);
    expect(state.chatModelBusy.a).toBeFalsy();
  });

  it('keeps the previous model when the server refuses', async () => {
    const client = fakeClient({ setChatModel: vi.fn(async () => { throw new ChatBrowserError('no', 400); }) });
    useSessionContextStore.setState({ chatModel: { a: choice('LLM') } });
    expect(await switchSessionChatModel(client, 'a', 'GLM')).toBe(false);
    expect(useSessionContextStore.getState().chatModel.a.selected).toBe('LLM');
    expect(useSessionContextStore.getState().chatModelBusy.a).toBeFalsy();
  });
});

// #314: the store remembers that the latest read of a session's chat model failed, so the picker can say so while it
// holds no model list and the page knows to read again when the window regains focus.
describe('session chat model read failures (#314)', () => {
  const failing = (error: unknown = new ChatBrowserError('', 429)) => fakeClient({ getChatModel: vi.fn(async () => { throw error; }) });

  it('marks a failed read, hands the failure on and keeps the session without a model', async () => {
    const refusal = new ChatBrowserError('', 429);
    await expect(loadSessionChatModel(failing(refusal), 'a')).rejects.toBe(refusal);
    const state = useSessionContextStore.getState();
    expect(state.chatModelFailed.a).toBe(true);
    expect(state.chatModel.a).toBeUndefined();
    expect(state.chatModelFailed.b).toBeUndefined();
  });

  it('clears the mark when a later read succeeds', async () => {
    await expect(loadSessionChatModel(failing(), 'a')).rejects.toBeTruthy();
    await loadSessionChatModel(fakeClient({ getChatModel: vi.fn(async () => choice('GLM')) }), 'a');
    const state = useSessionContextStore.getState();
    expect(state.chatModelFailed.a).toBeUndefined();
    expect(state.chatModel.a.selected).toBe('GLM');
  });

  it('keeps the model it holds when a later read fails, and still marks the failure', async () => {
    useSessionContextStore.setState({ chatModel: { a: choice('LLM') } });
    await expect(loadSessionChatModel(failing(), 'a')).rejects.toBeTruthy();
    const state = useSessionContextStore.getState();
    expect(state.chatModel.a.selected).toBe('LLM');
    expect(state.chatModelFailed.a).toBe(true);
  });

  it('does not take a cancelled read for a failure', async () => {
    const controller = new AbortController();
    const aborted = new DOMException('aborted', 'AbortError');
    const client = fakeClient({ getChatModel: vi.fn(async () => { controller.abort(); throw aborted; }) });
    await expect(loadSessionChatModel(client, 'a', controller.signal)).rejects.toBe(aborted);
    expect(useSessionContextStore.getState().chatModelFailed.a).toBeUndefined();
  });

  it('does not store the answer of a read cancelled while it was answered', async () => {
    const controller = new AbortController();
    const client = fakeClient({ getChatModel: vi.fn(async () => { controller.abort(); return choice('GLM'); }) });
    useSessionContextStore.setState({ chatModelFailed: { a: true } });
    await loadSessionChatModel(client, 'a', controller.signal);
    const state = useSessionContextStore.getState();
    expect(state.chatModel.a).toBeUndefined();
    expect(state.chatModelFailed.a).toBe(true);
  });
});
