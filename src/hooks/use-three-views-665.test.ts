import { describe, expect, it, vi } from 'vitest';

import { ChatBrowserError } from '@/lib/chat/browser-client';
import { startEachRemaining, threeViewErrorText } from './use-three-views';

describe('generate remaining views across a session switch (#665)', () => {
  it('stops starting paid views once the session it was started in is no longer in view', async () => {
    let current = true;
    const start = vi.fn(async (slot: string) => {
      if (slot === 'front') current = false; // the person switched sessions while the first view was starting
      return null;
    });
    await startEachRemaining(['front', 'side', 'back'], start, () => current);
    expect(start.mock.calls.map(([slot]) => slot)).toEqual(['front']);
  });

  it('stops at the first refusal and returns its text', async () => {
    const start = vi.fn(async (slot: string) => (slot === 'side' ? 'refused' : null));
    expect(await startEachRemaining(['front', 'side', 'back'], start, () => true)).toBe('refused');
    expect(start).toHaveBeenCalledTimes(2);
  });
});

describe('three-view error texts (#665)', () => {
  it('shows the server text but never a raw browser error', () => {
    expect(threeViewErrorText(new ChatBrowserError('视图已在生成', 409), 'fallback')).toBe('视图已在生成');
    expect(threeViewErrorText(new TypeError('Failed to fetch'), 'fallback')).toBe('fallback');
  });
});
