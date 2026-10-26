import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ChatBrowserError, type ChatBrowserClient, type ChatContextUsage, type ChatReasoning } from '@/lib/chat/browser-client';
import {
  compactSessionContext,
  loadSessionContext,
  loadSessionReasoning,
  switchSessionReasoning,
  useSessionContextStore,
} from '@/store/session-context-store';

const reasoning = (effort: string): ChatReasoning => ({ effort, levels: ['low', 'high', 'max'], defaultLevel: 'high' });
const usage = (tokens: number | null): ChatContextUsage => ({ tokens, window: 1_000_000, compactions: [] });

function fakeClient(overrides: Partial<ChatBrowserClient>): ChatBrowserClient {
  return overrides as ChatBrowserClient;
}

beforeEach(() => useSessionContextStore.setState({ reasoning: {}, reasoningBusy: {}, context: {}, compacting: {} }));

describe('session thinking level state (#322)', () => {
  it('keeps each session its own level', async () => {
    const client = fakeClient({ getReasoning: vi.fn(async (id: string) => reasoning(id === 'a' ? 'low' : 'max')) });
    await loadSessionReasoning(client, 'a');
    await loadSessionReasoning(client, 'b');
    expect(useSessionContextStore.getState().reasoning.a.effort).toBe('low');
    expect(useSessionContextStore.getState().reasoning.b.effort).toBe('max');
  });

  it('shows a switch at once and keeps the value the server stored', async () => {
    let release!: (value: ChatReasoning) => void;
    const client = fakeClient({ setReasoning: vi.fn(() => new Promise<ChatReasoning>((resolve) => { release = resolve; })) });
    useSessionContextStore.setState({ reasoning: { a: reasoning('high') } });
    const switching = switchSessionReasoning(client, 'a', 'max');
    expect(useSessionContextStore.getState().reasoning.a.effort).toBe('max');
    expect(useSessionContextStore.getState().reasoningBusy.a).toBe(true);
    release(reasoning('max'));
    expect(await switching).toBe(true);
    expect(useSessionContextStore.getState().reasoning.a.effort).toBe('max');
    expect(useSessionContextStore.getState().reasoningBusy.a).toBeFalsy();
  });

  it('puts the previous level back when the server refuses the switch', async () => {
    const client = fakeClient({ setReasoning: vi.fn(async () => { throw new ChatBrowserError('no', 400); }) });
    useSessionContextStore.setState({ reasoning: { a: reasoning('high') } });
    expect(await switchSessionReasoning(client, 'a', 'max')).toBe(false);
    expect(useSessionContextStore.getState().reasoning.a.effort).toBe('high');
    expect(useSessionContextStore.getState().reasoningBusy.a).toBeFalsy();
  });
});

describe('session context state (#322)', () => {
  it('keeps each session its own usage', async () => {
    const client = fakeClient({ getContext: vi.fn(async (id: string) => usage(id === 'a' ? 12_000 : null)) });
    await loadSessionContext(client, 'a');
    await loadSessionContext(client, 'b');
    expect(useSessionContextStore.getState().context.a.tokens).toBe(12_000);
    expect(useSessionContextStore.getState().context.b.tokens).toBeNull();
  });

  it('stores the context the compaction returned and reports success', async () => {
    const after: ChatContextUsage = {
      tokens: null, window: 1_000_000,
      compactions: [{ id: 'c1', createdAt: '2026-10-22T01:00:00Z', trigger: 'manual', rounds: 6, throughMessageId: 'm12' }],
    };
    let release!: (value: ChatContextUsage) => void;
    const client = fakeClient({ compactContext: vi.fn(() => new Promise<ChatContextUsage>((resolve) => { release = resolve; })) });
    useSessionContextStore.setState({ context: { a: usage(356_000) } });
    const compacting = compactSessionContext(client, 'a');
    expect(useSessionContextStore.getState().compacting.a).toBe(true);
    release(after);
    expect(await compacting).toEqual({ ok: true });
    expect(useSessionContextStore.getState().context.a).toEqual(after);
    expect(useSessionContextStore.getState().compacting.a).toBeFalsy();
  });

  it('keeps the usage unchanged and hands back the server text when the compaction is refused', async () => {
    const client = fakeClient({ compactContext: vi.fn(async () => { throw new ChatBrowserError('too short', 422); }) });
    useSessionContextStore.setState({ context: { a: usage(356_000) } });
    const result = await compactSessionContext(client, 'a');
    expect(result).toMatchObject({ ok: false, message: 'too short' });
    expect(useSessionContextStore.getState().context.a.tokens).toBe(356_000);
    expect(useSessionContextStore.getState().compacting.a).toBeFalsy();
  });

  it('does not start a second compaction of a session while one runs', async () => {
    const compact = vi.fn(() => new Promise<ChatContextUsage>(() => undefined));
    const client = fakeClient({ compactContext: compact });
    void compactSessionContext(client, 'a');
    expect(await compactSessionContext(client, 'a')).toMatchObject({ ok: false });
    expect(compact).toHaveBeenCalledTimes(1);
  });
});
