import { describe, expect, it } from 'vitest';

import { encodeMentions } from './attachments';
import { attachedIndex, insertReference, namesHandle } from './composer-reference';
import { buildMentionCandidates, filterMentions, mentionCandidates, mentionClaim, mentionToken, mentionsForTurn } from './mentions';

const history = mentionCandidates([
  { attachments: [{ handle: '@img1', kind: 'image', imageSlot: 1 }, { handle: '@img2', kind: 'image', taskId: 't1', assetId: 'a1' }], images: [{ url: '/in/1.png', taskId: '' }] },
  { attachments: [{ handle: '@img3', kind: 'image', taskId: 't2', assetId: 'a2' }] },
]);

const sources = {
  history,
  uploads: [
    { url: 'data:image/png;base64,AAAA', name: '上传的图 1' },
    { url: 'data:image/png;base64,BBBB', name: '上传的图 2' },
    { url: '/api/v1/tasks/t2/assets/a2', name: '上传的图 3', claim: { taskId: 't2', assetId: 'a2' } },
  ],
  versions: [
    { name: '2D v1', taskId: 't1', assetId: 'a1', url: '/api/v1/tasks/t1/assets/a1' },
    { name: '2D v4', taskId: 't4', assetId: 'a4', url: '/api/v1/tasks/t4/assets/a4' },
  ],
  layers: [
    { name: '图层 1', url: '/api/v1/tasks/t4/assets/a4', taskId: 't4' },
    { name: '图层 2', url: '/api/v1/tasks/t9/assets/a9', taskId: 't9' },
  ],
};

describe('buildMentionCandidates', () => {
  const list = buildMentionCandidates(sources);

  it('lists history, waiting uploads, versions and canvas layers, each picture once', () => {
    expect(list.map((item) => item.handle)).toEqual(['@img3', '@img1', '@img2', '@上传的图1', '@上传的图2', '@2Dv4', '@图层2']);
    expect(list.map((item) => item.source)).toEqual(['history', 'history', 'history', 'upload', 'upload', 'version', 'canvas']);
  });

  it('keeps the stable server handle for a picture found in several places and finds it by every name', () => {
    const merged = list.find((item) => item.handle === '@img2');
    expect(merged?.aliases).toContain('2D v1');
    expect(filterMentions(list, '2d v1').map((item) => item.handle)).toEqual(['@img2']);
    const version = list.find((item) => item.handle === '@2Dv4');
    expect(version?.aliases).toContain('图层 1');
    expect(filterMentions(list, '图层1').map((item) => item.handle)).toEqual(['@2Dv4']);
  });

  it('gives a history picture its address from the message slot', () => {
    expect(history.find((item) => item.handle === '@img1')?.url).toBe('/in/1.png');
  });

  it('filters by what is typed after the @', () => {
    // "上传的图 3" is the session's @img3 attached again: the same picture, found by either name.
    expect(filterMentions(list, '上传').map((item) => item.handle)).toEqual(['@img3', '@上传的图1', '@上传的图2']);
    expect(filterMentions(list, 'img').map((item) => item.handle)).toEqual(['@img3', '@img1', '@img2']);
    expect(filterMentions(list, '').length).toBe(list.length);
  });

  it('names a picture to the server by platform IDs only', () => {
    expect(mentionClaim(list.find((item) => item.handle === '@2Dv4')!)).toEqual({ kind: 'image', taskId: 't4', assetId: 'a4', url: '/api/v1/tasks/t4/assets/a4' });
    expect(mentionClaim(list.find((item) => item.handle === '@上传的图1')!)).toBeNull();
  });
  it('builds a token without whitespace', () => {
    expect(mentionToken('2D v3')).toBe('@2Dv3');
  });
});

describe('mentionsForTurn (submit payload mapping)', () => {
  const list = buildMentionCandidates(sources);

  it('sends nothing for history handles: the server resolves them from the session', () => {
    expect(mentionsForTurn('改 @img2', list)).toEqual([]);
  });

  it('names a waiting upload by its position among the message images and leaves the text alone', () => {
    expect(mentionsForTurn('把 @上传的图2 当风格', list)).toEqual([{ token: '@上传的图2', claim: { kind: 'image', imageSlot: 2 } }]);
  });

  it('names a version or layer by platform IDs or address, in the order written in the candidate list', () => {
    const mentions = mentionsForTurn('参考 @图层2 和 @2Dv4', list);
    expect(mentions).toEqual([
      { token: '@2Dv4', claim: { kind: 'image', taskId: 't4', assetId: 'a4', url: '/api/v1/tasks/t4/assets/a4' } },
      { token: '@图层2', claim: { kind: 'image', url: '/api/v1/tasks/t9/assets/a9', taskId: 't9' } },
    ]);
  });

  it('does not match a longer token that merely starts with a shorter one', () => {
    expect(mentionsForTurn('@上传的图10', list)).toEqual([]);
  });
});

describe('mentions on the wire', () => {
  it('encodes the token next to the claim with the Schema member names', () => {
    const schema = { chat: { api: { attachment: { kind: 'kind', url: 'url', asset_id: 'assetId', task_id: 'taskId', capture_id: 'captureId', image_slot: 'imageSlot', token: 'token' } } } } as never;
    expect(encodeMentions(schema, [{ token: '@2Dv4', claim: { kind: 'image', taskId: 't4', assetId: 'a4' } }, { token: '@上传的图1', claim: { kind: 'image', imageSlot: 1 } }])).toEqual([
      { kind: 'image', assetId: 'a4', taskId: 't4', token: '@2Dv4' },
      { kind: 'image', imageSlot: 1, token: '@上传的图1' },
    ]);
  });
});

describe('composer reference (handle tag click)', () => {
  it('finds a picture that is already attached by address or by platform IDs', () => {
    const images = ['/a.png', '/api/v1/tasks/t1/assets/a1'];
    const known = { '/a.png': { taskId: 't7', assetId: 'a7' } };
    expect(attachedIndex(images, known, { url: '/api/v1/tasks/t1/assets/a1' })).toBe(1);
    expect(attachedIndex(images, known, { url: '/other.png', taskId: 't7', assetId: 'a7' })).toBe(0);
    expect(attachedIndex(images, known, { url: '/other.png', taskId: 't8', assetId: 'a8' })).toBe(-1);
  });

  it('writes the handle at the caret with a space on each side that needs one', () => {
    expect(insertReference('把图改成红色', 1, '@img3')).toEqual({ value: '把 @img3 图改成红色', caret: 8 });
    expect(insertReference('', 0, '@img3')).toEqual({ value: '@img3 ', caret: 6 });
    expect(insertReference('看 ', 2, '@img3')).toEqual({ value: '看 @img3 ', caret: 8 });
  });

  it('knows the text already names a handle', () => {
    expect(namesHandle('改 @img3 然后', '@img3')).toBe(true);
    expect(namesHandle('改 @img30', '@img3')).toBe(false);
  });
});

describe('mentions before send: names are client-side only', () => {
  const data = (tag: string) => `data:image/png;base64,${tag}`;

  it('gives two pictures with the same name two tokens, two claims and no clash', () => {
    const list = buildMentionCandidates({
      history: [],
      uploads: [{ url: data('AAAA'), name: 'photo.png' }, { url: data('BBBB'), name: 'photo.png' }],
      versions: [], layers: [],
    });
    expect(list.map((item) => item.handle)).toEqual(['@photo.png', '@photo.png_2']);
    const mentions = mentionsForTurn('mix @photo.png with @photo.png_2', list);
    expect(mentions).toEqual([
      { token: '@photo.png', claim: { kind: 'image', imageSlot: 1 } },
      { token: '@photo.png_2', claim: { kind: 'image', imageSlot: 2 } },
    ]);
    // The shorter token does not claim the longer one's text.
    expect(mentionsForTurn('only @photo.png_2', list)).toEqual([{ token: '@photo.png_2', claim: { kind: 'image', imageSlot: 2 } }]);
  });

  it('never reuses a server handle or another source name for a different picture', () => {
    const list = buildMentionCandidates({
      history: [{ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' }],
      uploads: [{ url: data('AAAA'), name: 'img1' }],
      versions: [{ name: '图层 1', taskId: 't5', assetId: 'a5', url: '/api/v1/tasks/t5/assets/a5' }],
      layers: [{ name: '图层 1', url: '/api/v1/tasks/t6/assets/a6', taskId: 't6' }],
    });
    expect(new Set(list.map((item) => item.handle)).size).toBe(list.length);
    expect(list.map((item) => item.handle)).toEqual(['@img1', '@img1_2', '@图层1', '@图层1_2']);
  });

  it('keeps a picked layer token meaning its own picture after another layer is deleted', () => {
    const layer = (n: number) => ({ name: `图层 ${n}`, url: `/api/v1/tasks/t${n}/assets/a${n}`, taskId: `t${n}` });
    const before = buildMentionCandidates({ history: [], uploads: [], versions: [], layers: [layer(1), layer(2), layer(3)] });
    const picked = before.filter((item) => item.handle === '@图层1' || item.handle === '@图层3');
    // Layer 1 is deleted: the old 图层3 is now 图层2, the old 图层2 is now 图层1.
    const after = buildMentionCandidates({ history: [], uploads: [], versions: [], layers: [layer(2), layer(3)] });
    const mentions = mentionsForTurn('use @图层1 and @图层3', after, picked);
    // The deleted layer's token is dropped (plain text, as the server treats an unknown mention); the other keeps its picture.
    expect(mentions).toEqual([{ token: '@图层3', claim: { kind: 'image', taskId: 't3', url: '/api/v1/tasks/t3/assets/a3' } }]);
    // The renumbered layer that took the deleted one's name is not sent under it.
    expect(mentions.some((item) => item.claim.taskId === 't2')).toBe(false);
  });

  it('drops a removed upload and renumbers the slot of the one that is still attached', () => {
    const upload = (tag: string, n: number) => ({ url: data(tag), name: `上传的图 ${n}` });
    const before = buildMentionCandidates({ history: [], uploads: [upload('AAAA', 1), upload('BBBB', 2), upload('CCCC', 3)], versions: [], layers: [] });
    const picked = before.filter((item) => item.handle === '@上传的图1' || item.handle === '@上传的图3');
    const after = buildMentionCandidates({ history: [], uploads: [upload('BBBB', 1), upload('CCCC', 2)], versions: [], layers: [] });
    expect(mentionsForTurn('@上传的图1 vs @上传的图3', after, picked)).toEqual([
      { token: '@上传的图3', claim: { kind: 'image', imageSlot: 2 } },
    ]);
  });

  it('still reads names typed by hand when nothing was picked', () => {
    const after = buildMentionCandidates({ history: [], uploads: [], versions: [], layers: [{ name: '图层 1', url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' }] });
    expect(mentionsForTurn('@图层1', after, [])).toHaveLength(1);
  });
});
