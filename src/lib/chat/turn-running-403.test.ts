import { describe, expect, it } from 'vitest';
import { hasSentWords, sessionTurnRunning } from '@/lib/chat/turn-running';
import type { ChatMessage } from '@/store/chat-store';

// #403: whether the session's turn still runs is read from what the message projection already says (a reply the
// server marks as pending) and from a live stream buffer. No second list of turn states is kept in the browser.

const user: ChatMessage = { id: 'u', role: 'user', content: 'draw', createdAt: 1 };
const running: ChatMessage = { id: 'a', role: 'assistant', content: 'first round', isPending: true, createdAt: 2 };
const finished: ChatMessage = { id: 'a', role: 'assistant', content: 'done', isPending: false, createdAt: 2 };

describe('whether the turn of a session still runs', () => {
  it('runs while a reply is pending, whatever text it holds', () => {
    expect(sessionTurnRunning([user, running], false)).toBe(true);
  });

  it('runs while a live stream is attached', () => {
    expect(sessionTurnRunning([user, finished], true)).toBe(true);
  });

  it('does not run when every reply is settled and nothing streams', () => {
    expect(sessionTurnRunning([user, finished], false)).toBe(false);
    expect(sessionTurnRunning([], false)).toBe(false);
  });

  it('counts a session that has not been read yet as running, so nothing is called finished before it is known', () => {
    expect(sessionTurnRunning(undefined, false)).toBe(true);
  });
});

describe('the sent words a stream buffer shows', () => {
  it('has words when the buffer holds text or pictures', () => {
    expect(hasSentWords({ content: 'draw a chair' })).toBe(true);
    expect(hasSentWords({ content: '', images: [{ url: '/a.png' }] })).toBe(true);
  });

  it('has none for a stream joined after a reload, which carries no words of its own', () => {
    expect(hasSentWords({ content: '' })).toBe(false);
    expect(hasSentWords({ content: '', images: [] })).toBe(false);
    expect(hasSentWords(null)).toBe(false);
    expect(hasSentWords(undefined)).toBe(false);
  });
});
