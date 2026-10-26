import { describe, expect, it } from 'vitest';

import { mergeNewestPage, prependOlderPage } from './message-pages';

const ids = (messages: Array<{ id: string }>) => messages.map((message) => message.id);
const m = (...names: string[]) => names.map((id) => ({ id }));

describe('conversation pages', () => {
  it('starts from the newest page when nothing is loaded', () => {
    const merged = mergeNewestPage(undefined, { messages: m('c', 'd'), nextCursor: 'before-c' });
    expect(ids(merged.messages)).toEqual(['c', 'd']);
    expect(merged.olderCursor).toBe('before-c');
  });

  it('keeps earlier pages in front when the fresh page joins onto them', () => {
    const loaded = { messages: m('a', 'b', 'c', 'd'), olderCursor: 'before-a' };
    const merged = mergeNewestPage(loaded, { messages: m('c', 'd', 'e'), nextCursor: 'before-c' });
    expect(ids(merged.messages)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(merged.olderCursor).toBe('before-a');
  });

  it('drops a message deleted from the end and keeps what is before it', () => {
    const loaded = { messages: m('a', 'b', 'c', 'd'), olderCursor: 'before-a' };
    const merged = mergeNewestPage(loaded, { messages: m('b', 'c'), nextCursor: 'before-b' });
    expect(ids(merged.messages)).toEqual(['a', 'b', 'c']);
  });

  it('takes the fresh page alone when it does not join, or when it reaches the start', () => {
    const loaded = { messages: m('a', 'b'), olderCursor: 'before-a' };
    expect(mergeNewestPage(loaded, { messages: m('x', 'y'), nextCursor: 'before-x' })).toEqual({
      messages: m('x', 'y'), olderCursor: 'before-x',
    });
    expect(mergeNewestPage(loaded, { messages: m('b', 'c'), nextCursor: '' })).toEqual({
      messages: m('b', 'c'), olderCursor: '',
    });
  });

  it('puts an earlier page in front without repeating messages', () => {
    const loaded = { messages: m('c', 'd'), olderCursor: 'before-c' };
    const merged = prependOlderPage(loaded, { messages: m('a', 'b', 'c'), nextCursor: '' });
    expect(ids(merged.messages)).toEqual(['a', 'b', 'c', 'd']);
    expect(merged.olderCursor).toBe('');
  });

  it('keeps the loaded object of a message read again unchanged, so the list redraws only what changed (#291)', () => {
    const loaded = { messages: [{ id: 'a', content: 'one' }, { id: 'b', content: 'two' }, { id: 'c', content: '' }], olderCursor: 'before-a' };
    const joined = mergeNewestPage(loaded, { messages: [{ id: 'b', content: 'two' }, { id: 'c', content: 'done' }], nextCursor: 'before-b' });
    expect(joined.messages[0]).toBe(loaded.messages[0]);
    expect(joined.messages[1]).toBe(loaded.messages[1]);
    expect(joined.messages[2]).not.toBe(loaded.messages[2]);
    expect(joined.messages[2].content).toBe('done');
    const whole = mergeNewestPage(loaded, { messages: [{ id: 'a', content: 'one' }, { id: 'b', content: 'two' }, { id: 'c', content: '' }], nextCursor: '' });
    expect(whole.messages).toBe(loaded.messages);
  });
});
