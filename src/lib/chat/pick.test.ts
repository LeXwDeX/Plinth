import { describe, expect, it } from 'vitest';

import { candidateAttachment, isPickedCandidate, pickClaim } from '@/lib/chat/pick';
import type { ChatAttachment, ChatImage } from '@/store/chat-store';

const candidate = (taskId: string, url = `/api/v1/tasks/${taskId}/assets/a-${taskId}`): ChatImage => ({ url, taskId });
const attachment = (taskId: string, assetId: string, slot: number): ChatAttachment => ({
  handle: `@img${slot}`, kind: 'image', role: 'generated', taskId, assetId, imageSlot: slot,
});

describe('candidate pick', () => {
  it('finds the structured attachment of the n-th message image by its slot', () => {
    const attachments = [attachment('t1', 'a1', 1), attachment('t2', 'a2', 2)];
    expect(candidateAttachment({ attachments }, 1)?.assetId).toBe('a2');
    expect(candidateAttachment({ attachments }, 2)).toBeUndefined();
    expect(candidateAttachment({}, 0)).toBeUndefined();
  });

  it('claims the verified asset when known, else the task and picture, and never an upload', () => {
    expect(pickClaim(candidate('t1'), attachment('t1', 'a1', 1))).toEqual({ kind: 'image', taskId: 't1', assetId: 'a1' });
    expect(pickClaim(candidate('t1', '/x.png'))).toEqual({ kind: 'image', taskId: 't1', url: '/x.png' });
    // An uploaded picture has no source task: it is not a candidate.
    expect(pickClaim(candidate(''))).toBeNull();
  });

  it('marks exactly the picked candidate, by asset when known and by picture otherwise', () => {
    const pick: ChatAttachment = { handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1', url: '/api/v1/tasks/t1/assets/a1' };
    expect(isPickedCandidate(pick, candidate('t1'), attachment('t1', 'a1', 1))).toBe(true);
    // Same task, another candidate of the same result.
    expect(isPickedCandidate(pick, candidate('t1'), attachment('t1', 'a2', 2))).toBe(false);
    expect(isPickedCandidate(pick, candidate('t2'), attachment('t2', 'a1', 1))).toBe(false);
    // Before the attachment record exists the picture decides.
    expect(isPickedCandidate(pick, candidate('t1', '/api/v1/tasks/t1/assets/a1'))).toBe(true);
    expect(isPickedCandidate(pick, candidate('t1', '/other.png'))).toBe(false);
    expect(isPickedCandidate(null, candidate('t1'))).toBe(false);
    expect(isPickedCandidate({ ...pick, kind: 'model' }, candidate('t1'), attachment('t1', 'a1', 1))).toBe(false);
  });

  it('marks a picked candidate whether or not the pick carries a handle', () => {
    // The server can accept a picture that has no handle; the pick then has an empty one and still counts.
    const unnamed: ChatAttachment = { handle: '', kind: 'image', taskId: 't1', assetId: 'a1' };
    expect(isPickedCandidate(unnamed, candidate('t1'), attachment('t1', 'a1', 1))).toBe(true);
    expect(isPickedCandidate(unnamed, candidate('t1'), attachment('t1', 'a2', 2))).toBe(false);
  });
});
