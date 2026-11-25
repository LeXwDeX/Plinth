import { beforeEach, describe, expect, it, vi } from 'vitest';
import { messagesBesideStream, readUntilSettled } from './turn-settle';
import { branchUnavailableReason } from './session-branch';
import { useChatStore, type ChatMessage } from '@/store/chat-store';

// #597: the server appends a turn's `done` event before it marks the turn ended, so the page read right after
// `done` can still say the reply is pending. Kept as it was, that reply stayed "running" for the page (its branch
// stayed disabled) and was hidden with its pictures while the next turn streamed.

const user1: ChatMessage = {
  id: 'u1', role: 'user', content: '按这个模具做图标', createdAt: 1,
  images: [{ url: '/api/v1/assets/asset_mold', taskId: '' }],
  attachments: [{ handle: '@img1', kind: 'image', assetId: 'asset_mold', imageSlot: 1 }],
};
const reply1 = (pending: boolean): ChatMessage => ({
  id: 'a1', role: 'assistant', content: '已生成', createdAt: 2, isPending: pending,
  images: [{ url: '/api/v1/tasks/task_1/assets/asset_icon', taskId: 'task_1' }], taskIds: ['task_1'],
});
const user2: ChatMessage = { id: 'u2', role: 'user', content: '改为真实系', createdAt: 3 };
const reply2: ChatMessage = { id: 'a2', role: 'assistant', content: '', createdAt: 4, isPending: true };

describe('messagesBesideStream (#597)', () => {
  it('shows every stored message when no turn streams', () => {
    const messages = [user1, reply1(true)];
    expect(messagesBesideStream(messages, undefined)).toBe(messages);
  });

  it('keeps an earlier reply left pending, with its pictures, while the next turn streams', () => {
    const shown = messagesBesideStream([user1, reply1(true), user2, reply2], { assistantDbId: 'a2' });
    expect(shown.map((message) => message.id)).toEqual(['u1', 'a1', 'u2']);
    expect(shown[0].images).toEqual(user1.images);
    expect(shown[0].attachments).toEqual(user1.attachments);
    expect(shown[1].images).toEqual(reply1(true).images);
  });

  it('hides only the streaming reply itself, which the live buffer stands in for', () => {
    expect(messagesBesideStream([user1, reply1(false), user2, reply2], { assistantDbId: 'a2' }).map((m) => m.id))
      .toEqual(['u1', 'a1', 'u2']);
  });

  it('hides every pending message before the stream has named its reply', () => {
    expect(messagesBesideStream([user1, reply1(false), user2, reply2], { assistantDbId: null }).map((m) => m.id))
      .toEqual(['u1', 'a1', 'u2']);
  });
});

describe('readUntilSettled (#597)', () => {
  beforeEach(() => {
    useChatStore.setState({ messagesBySession: {}, olderCursorBySession: {}, streamingBuffers: {}, toolCallCounts: {} });
  });

  const page = (pending: boolean) => ({ messages: [user1, reply1(pending)], nextCursor: '' });
  const wait = () => Promise.resolve();

  it('reads again after done until the reply is no longer pending, and the branch is then available', async () => {
    // The turn's own settle read raced the server's turn end.
    useChatStore.getState().initStreaming('s1', { content: '按这个模具做图标' });
    useChatStore.getState().settleStreaming('s1', page(true));
    expect(useChatStore.getState().messagesBySession.s1[1].isPending).toBe(true);

    const reads = [page(true), page(false)];
    const read = vi.fn(async () => reads.shift() ?? page(false));
    const settled = await readUntilSettled({
      read,
      apply: (next) => useChatStore.getState().applyNewestMessagePage('s1', next),
      isCurrent: () => true,
      delays: [0, 0, 0],
      wait,
    });

    expect(settled).toBe('settled');
    expect(read).toHaveBeenCalledTimes(2);
    const stored = useChatStore.getState().messagesBySession.s1;
    expect(stored[1].isPending).toBe(false);
    expect(stored[0].images).toEqual(user1.images);
    const running = stored.some((message) => message.isPending);
    expect(branchUnavailableReason({ running })).toBe('');
  });

  it('gives up after its bounded reads and says the turn still runs', async () => {
    const read = vi.fn(async () => page(true));
    const settled = await readUntilSettled({ read, apply: () => {}, isCurrent: () => true, delays: [0, 0], wait });
    expect(settled).toBe('running');
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('stops when the session is no longer the one shown or a new turn started', async () => {
    const read = vi.fn(async () => page(true));
    const apply = vi.fn();
    let current = true;
    const settled = await readUntilSettled({
      read: async () => { current = false; return read(); }, apply, isCurrent: () => current, delays: [0, 0], wait,
    });
    expect(settled).toBe('left');
    expect(apply).not.toHaveBeenCalled();
  });

  it('keeps reading after a failed read', async () => {
    const read = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(page(false));
    const apply = vi.fn();
    expect(await readUntilSettled({ read, apply, isCurrent: () => true, delays: [0, 0], wait })).toBe('settled');
    expect(apply).toHaveBeenCalledTimes(1);
  });
});
