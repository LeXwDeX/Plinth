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

beforeEach(() => useSessionContextStore.setState({ reasoning: {}, reasoningBusy: {}, context: {}, compacting: {}, chatModel: {}, chatModelBusy: {} }));

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
