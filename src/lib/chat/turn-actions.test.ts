import { describe, expect, it } from 'vitest';

import { editAttachments, editTarget, lastExchange, regenerateTarget, restoreAttachments, turnPayload } from '@/lib/chat/turn-actions';
import type { ChatMessage } from '@/store/chat-store';

const PREFIX = 'optimistic-';
const msg = (id: string, role: ChatMessage['role'], extra: Partial<ChatMessage> = {}): ChatMessage => ({ id, role, content: id, createdAt: 1, ...extra });

describe('turn actions', () => {
  it('builds the resend payload from text, images and verified attachment claims only', () => {
    const payload = turnPayload({
      content: 'hello',
      images: [{ url: '/a.png', taskId: 't' }],
      attachments: [
        { handle: '@img1', kind: 'image', imageSlot: 1, assetId: 'a1', taskId: 't1', captureId: 'c1' },
        { handle: '@model1', kind: 'model', taskId: 'm1' },
        { handle: '@img2', kind: 'image', assetId: 'a2' },
      ],
    });
    expect(payload).toEqual({ text: 'hello', images: ['/a.png'], claims: [{ kind: 'image', imageSlot: 1, assetId: 'a1', taskId: 't1', captureId: 'c1' }] });
  });

  it('finds the last user message with the replies after it', () => {
    const messages = [msg('u1', 'user'), msg('a1', 'assistant'), msg('u2', 'user'), msg('a2', 'assistant'), msg('a3', 'assistant')];
    const exchange = lastExchange(messages, PREFIX);
    expect(exchange?.user.id).toBe('u2');
    expect(exchange?.replies.map((message) => message.id)).toEqual(['a2', 'a3']);
    expect(lastExchange([msg('a1', 'assistant')], PREFIX)).toBeNull();
    expect(lastExchange([], PREFIX)).toBeNull();
  });

  it('offers nothing while the last turn is still running or only local', () => {
    expect(lastExchange([msg('u1', 'user'), msg('a1', 'assistant', { isPending: true })], PREFIX)).toBeNull();
    expect(lastExchange([msg('u1', 'user'), msg('a1', 'assistant', { isStreaming: true })], PREFIX)).toBeNull();
    expect(lastExchange([msg(`${PREFIX}1`, 'user')], PREFIX)).toBeNull();
    expect(regenerateTarget([msg('u1', 'user'), msg('a1', 'assistant', { isPending: true })], PREFIX)).toBeNull();
    expect(editTarget([msg(`${PREFIX}1`, 'user')], PREFIX)).toBeNull();
  });

  it('regenerates the last reply of the last turn: both the user message and its replies are replaced', () => {
    const messages = [msg('u1', 'user'), msg('a1', 'assistant'), msg('u2', 'user', { content: 'draw a fox' }), msg('a2', 'assistant')];
    const target = regenerateTarget(messages, PREFIX);
    expect(target?.assistantId).toBe('a2');
    expect(target?.user.content).toBe('draw a fox');
    expect(target?.deleteIds).toEqual(['a2', 'u2']);
    // A user message with no reply yet has nothing to regenerate.
    expect(regenerateTarget([msg('u1', 'user')], PREFIX)).toBeNull();
  });

  it('edits the last user message and removes only that turn when the edit is sent', () => {
    const messages = [msg('u1', 'user'), msg('a1', 'assistant'), msg('u2', 'user'), msg('a2', 'assistant')];
    expect(editTarget(messages, PREFIX)).toEqual({ userId: 'u2', deleteIds: ['a2', 'u2'] });
    expect(editTarget([msg('u1', 'user')], PREFIX)).toEqual({ userId: 'u1', deleteIds: ['u1'] });
    expect(editTarget([msg('a1', 'assistant')], PREFIX)).toBeNull();
  });
});

describe('attachments carried back into the composer', () => {
  const message = {
    images: [{ url: '/a.png', taskId: '' }, { url: '/b.png', taskId: '' }],
    attachments: [
      { kind: 'image' as const, imageSlot: 2, assetId: 'asset-2' },
      { kind: 'image' as const, imageSlot: 1, taskId: 'task-1', assetId: 'asset-1' },
    ],
  };

  it('returns each image with the source it was sent from, matched by slot', () => {
    expect(editAttachments(message as never)).toEqual([
      { url: '/a.png', assetId: 'asset-1', taskId: 'task-1' },
      { url: '/b.png', assetId: 'asset-2' },
    ]);
  });

  it('keeps a bare URL when there is no source, rather than inventing one', () => {
    expect(editAttachments({ images: [{ url: '/c.png', taskId: '' }] } as never)).toEqual([{ url: '/c.png' }]);
    expect(editAttachments({} as never)).toEqual([]);
  });

  it('restores from the claims that were about to be sent', () => {
    expect(restoreAttachments(['/a.png', '/b.png'], [{ kind: 'image', imageSlot: 2, captureId: 'cap-1' }]))
      .toEqual([{ url: '/a.png' }, { url: '/b.png', captureId: 'cap-1' }]);
  });
});
