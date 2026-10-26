import { expect, it } from 'vitest';

import { handleOfObject } from '@/lib/workspace/object-handle';

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
