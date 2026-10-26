import { describe, expect, it, vi } from 'vitest';

import { resolveInitialSession, sessionFromAddress, writeSessionToAddress } from '@/lib/chat/session-url';
import type { ChatSession } from '@/store/chat-store';

const session = (id: string): ChatSession => ({ id, title: id, createdAt: '', updatedAt: '' });

function address(href: string) {
  const location = { href };
  const history = {
    state: { kept: true },
    replaceState: vi.fn((_state: unknown, _unused: string, url: string) => {
      location.href = new URL(url, location.href).href;
    }),
  };
  return { location, history };
}

describe('session in the page address', () => {
  it('reads the session id, ignoring an empty value', () => {
    expect(sessionFromAddress({ href: 'http://h/?session=abc' })).toBe('abc');
    expect(sessionFromAddress({ href: 'http://h/?session=' })).toBeNull();
    expect(sessionFromAddress({ href: 'http://h/' })).toBeNull();
  });

  it('writes in place and keeps other params, hash and history state', () => {
    const { location, history } = address('http://h/studio?x=1#top');
    writeSessionToAddress('s1', location, history);
    expect(location.href).toBe('http://h/studio?x=1&session=s1#top');
    expect(history.replaceState).toHaveBeenCalledWith({ kept: true }, '', '/studio?x=1&session=s1#top');
  });

  it('does nothing when the address already names the session', () => {
    const { location, history } = address('http://h/?session=s1');
    writeSessionToAddress('s1', location, history);
    expect(history.replaceState).not.toHaveBeenCalled();
  });

  it('removes the session id when none is open', () => {
    const { location, history } = address('http://h/?session=s1&x=1');
    writeSessionToAddress(null, location, history);
    expect(location.href).toBe('http://h/?x=1');
  });
});

describe('resolveInitialSession', () => {
  const client = (found: Record<string, ChatSession>, latest: ChatSession[]) => ({
    getSession: vi.fn(async (id: string) => {
      if (!found[id]) throw new Error('not found');
      return found[id];
    }),
    listSessionPage: vi.fn(async () => ({ sessions: latest, nextCursor: '', total: latest.length })),
  });

  it('reopens the session the address names, not the latest', async () => {
    const c = client({ old: session('old') }, [session('latest')]);
    await expect(resolveInitialSession(c, 'old')).resolves.toEqual({ session: session('old'), sessions: [session('old')] });
    expect(c.listSessionPage).not.toHaveBeenCalled();
  });

  it('falls back to the latest when the named session is gone', async () => {
    const c = client({}, [session('latest')]);
    await expect(resolveInitialSession(c, 'gone')).resolves.toEqual({ session: session('latest'), sessions: [session('latest')] });
  });

  it('opens the latest when the address names none, and nothing when none exists', async () => {
    await expect(resolveInitialSession(client({}, [session('latest')]), null)).resolves.toMatchObject({ session: session('latest') });
    await expect(resolveInitialSession(client({}, []), null)).resolves.toEqual({ session: null, sessions: [] });
  });

  it('does not fall back after the load was cancelled', async () => {
    const controller = new AbortController();
    controller.abort();
    const c = client({}, [session('latest')]);
    await expect(resolveInitialSession(c, 'old', controller.signal)).rejects.toThrow();
    expect(c.listSessionPage).not.toHaveBeenCalled();
  });
});
