import { describe, expect, it } from 'vitest';

import { handleLabel, handleOfObject, handleOfPicture } from '@/lib/workspace/object-handle';

const message = (...attachments: Array<{ handle: string; kind: 'image' | 'model'; taskId?: string; assetId?: string; url?: string }>) => ({ attachments });

it('finds the handle the server gave this exact asset', () => {
  const messages = [
    message({ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' }, { handle: '@img2', kind: 'image', taskId: 't1', assetId: 'a2' }),
  ];
  expect(handleOfObject(messages, { taskId: 't1', assetId: 'a2', kind: 'image', url: '' })).toBe('@img2');
});

it('falls back to the only attachment of the task when the asset is not named', () => {
  const messages = [message({ handle: '@model1', kind: 'model', taskId: 'm1' })];
  expect(handleOfObject(messages, { taskId: 'm1', assetId: 'x', kind: 'model', url: '' })).toBe('@model1');
});

it('never invents a handle', () => {
  expect(handleOfObject([message({ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' })], { taskId: 't2', kind: 'image', url: '' })).toBe('');
  expect(handleOfObject([], null)).toBe('');
});

const model = { taskId: 'm1', assetId: 'glb', kind: 'model' as const, url: '/model.glb' };

it('resolves a model task across ZIP/GLB artifacts, including repeated copies of one handle', () => {
  const zip = message({ handle: '@model1', kind: 'model', taskId: 'm1', assetId: 'zip' });
  expect(handleOfObject([zip, zip], model)).toBe('@model1');
});

it('prefers exact model assets over fallback and URL candidates regardless of history order', () => {
  const exact = message({ handle: '@model2', kind: 'model', taskId: 'm1', assetId: 'glb' });
  const fallback = message({ handle: '@model1', kind: 'model', taskId: 'm1', assetId: 'zip' });
  const url = message({ handle: '@model3', kind: 'model', taskId: 'm1', url: '/model.glb' });
  for (const history of [[exact, fallback, url], [url, fallback, exact]]) {
    expect(handleOfObject(history, model)).toBe('@model2');
  }
});

it('rejects conflicting model handles at either the exact or fallback priority', () => {
  for (const assetId of ['glb', 'zip']) {
    const one = message({ handle: '@model1', kind: 'model', taskId: 'm1', assetId });
    const two = message({ handle: '@model2', kind: 'model', taskId: 'm1', assetId });
    expect(handleOfObject([one, two], model)).toBe('');
    expect(handleOfObject([two, one], model)).toBe('');
  }
});

it('does not replace a missing exact handle with a different model reference', () => {
  expect(handleOfObject([
    message({ handle: '', kind: 'model', taskId: 'm1', assetId: 'glb' }),
    message({ handle: '@model1', kind: 'model', taskId: 'm1', assetId: 'zip' }),
  ], model)).toBe('');
  expect(handleOfObject([message({ handle: '', kind: 'model', taskId: 'm1', assetId: 'zip' })], model)).toBe('');
});

it('isolates task and kind, and does not fall back across different image assets even with equal URLs', () => {
  expect(handleOfObject([
    message({ handle: '@model1', kind: 'model', taskId: 'other', assetId: 'glb' }),
    message({ handle: '@img1', kind: 'image', taskId: 'm1', assetId: 'glb' }),
  ], model)).toBe('');
  expect(handleOfObject([message({ handle: '@img1', kind: 'image', taskId: 'm1', assetId: 'other', url: '/same' })],
    { ...model, kind: 'image', url: '/same' })).toBe('');
});

it('keeps legacy exact URL resolution and rejects ambiguous unnamed images', () => {
  const image = { taskId: 't1', kind: 'image' as const, url: '/result.png' };
  expect(handleOfObject([message({ handle: '@img1', kind: 'image', taskId: 't1', url: '/result.png' })], image)).toBe('@img1');
  expect(handleOfObject([message({ handle: '@img1', kind: 'image', taskId: 't1' }, { handle: '@img2', kind: 'image', taskId: 't1' })], image)).toBe('');
});

describe('handleOfPicture (#87)', () => {
  const generated = message({ handle: '@img25', kind: 'image', taskId: 't1', assetId: 'a1' }, { handle: '@img26', kind: 'image', taskId: 't1', assetId: 'a2' });

  it('finds the handle of the asset a platform address names, query and absolute origin included', () => {
    expect(handleOfPicture([generated], { url: '/api/v1/tasks/t1/assets/a2' })).toBe('@img26');
    expect(handleOfPicture([generated], { url: 'https://host.example/api/v1/tasks/t1/assets/a1?variant=preview' })).toBe('@img25');
  });

  it('never borrows the handle of another picture of the same task', () => {
    expect(handleOfPicture([message({ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' })], { url: 'blob:painted' })).toBe('');
    expect(handleOfPicture([generated], { url: '/api/v1/tasks/t1/assets/a3' })).toBe('');
  });

  it('matches a model capture by its capture id and a sent picture by its address', () => {
    const capture = message({ handle: '@img4', kind: 'image', taskId: 'm1', assetId: 'x' });
    (capture.attachments[0] as { captureId?: string }).captureId = 'c1';
    expect(handleOfPicture([capture], { url: '/captures/c1.png', captureId: 'c1' })).toBe('@img4');
    expect(handleOfPicture([capture], { url: '/captures/c2.png', captureId: 'c2' })).toBe('');
    const sent = { attachments: [{ handle: '@img1', kind: 'image' as const, imageSlot: 1 }], images: [{ url: '/uploads/u1.png', taskId: '' }] };
    expect(handleOfPicture([sent], { url: '/uploads/u1.png' })).toBe('@img1');
  });

  it('gives no handle when two handles name the picture, and ignores models', () => {
    const twice = message({ handle: '@img1', kind: 'image', taskId: 't1', assetId: 'a1' }, { handle: '@img9', kind: 'image', taskId: 't1', assetId: 'a1' });
    expect(handleOfPicture([twice], { url: '/api/v1/tasks/t1/assets/a1' })).toBe('');
    expect(handleOfPicture([message({ handle: '@model1', kind: 'model', taskId: 't1', assetId: 'a1' })], { url: '/api/v1/tasks/t1/assets/a1' })).toBe('');
  });

  it('labels a handle without its marker', () => {
    expect(handleLabel('@img25')).toBe('img25');
    expect(handleLabel('img3')).toBe('img3');
  });
});
