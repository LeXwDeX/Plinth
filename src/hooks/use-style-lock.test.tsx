// @vitest-environment jsdom

import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { ChatBrowserClient, ChatStyleLock } from '@/lib/chat/browser-client';
import { useStyleLock } from './use-style-lock';

const lock = (overrides: Partial<ChatStyleLock> = {}): ChatStyleLock => ({ enabled: true, text: 'ink', references: ['a1'], ...overrides });

afterEach(cleanup);

function client(overrides: Partial<ChatBrowserClient> = {}) {
  return {
    getStyleLock: vi.fn(async () => lock()),
    setStyleLock: vi.fn(async (_session: string, value: ChatStyleLock) => value),
    ...overrides,
  } as unknown as ChatBrowserClient;
}

describe('useStyleLock', () => {
  it('loads per session and saves the server value', async () => {
    const api = client();
    const { result } = renderHook(() => useStyleLock(api, 's1'));
    await waitFor(() => expect(result.current.lock.text).toBe('ink'));
    await act(async () => { expect(await result.current.save(lock({ text: 'watercolor', references: ['a2', 'a1'] }))).toBe(true); });
    expect(result.current.lock).toEqual(lock({ text: 'watercolor', references: ['a2', 'a1'] }));
    expect(api.setStyleLock).toHaveBeenCalledWith('s1', lock({ text: 'watercolor', references: ['a2', 'a1'] }));
  });

  it('keeps the saved lock and exposes failures when loading or saving fails', async () => {
    const api = client({
      getStyleLock: vi.fn(async () => { throw new Error('offline'); }),
      setStyleLock: vi.fn(async () => { throw new Error('detail from server'); }),
    });
    const { result } = renderHook(() => useStyleLock(api, 's1'));
    await waitFor(() => expect(result.current.failed).toBe(true));
    await act(async () => { expect(await result.current.save(lock({ text: 'local draft' }))).toBe(false); });
    expect(result.current.saveError?.message).toBe('detail from server');
    expect(result.current.lock).toEqual({ enabled: false, text: '', references: [] });
  });

  it('does not let a previous session response replace the current session', async () => {
    const resolvers: Array<(value: ChatStyleLock) => void> = [];
    const api = client({ getStyleLock: vi.fn(() => new Promise<ChatStyleLock>((done) => { resolvers.push(done); })) });
    const { result, rerender } = renderHook(({ session }) => useStyleLock(api, session), { initialProps: { session: 'old' } });
    rerender({ session: 'new' });
    await act(async () => { resolvers[0]?.(lock({ text: 'old' })); });
    expect(result.current.lock).toEqual({ enabled: false, text: '', references: [] });
  });

  it('publishes a save to every mounted editor of the same session', async () => {
    const api = client();
    const first = renderHook(() => useStyleLock(api, 's1'));
    const second = renderHook(() => useStyleLock(api, 's1'));
    await waitFor(() => expect(first.result.current.lock.text).toBe('ink'));
    await act(async () => { expect(await first.result.current.save(lock({ text: 'watercolor' }))).toBe(true); });
    await waitFor(() => expect(second.result.current.lock.text).toBe('watercolor'));
  });
});
