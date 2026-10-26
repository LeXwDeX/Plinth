import { describe, expect, it } from 'vitest';

import { handleReferences, objectReference, splitHandles } from '@/lib/chat/composer-reference';

describe('handle references', () => {
  const messages = [
    { attachments: [{ kind: 'image' as const, handle: '@img1', imageSlot: 1 }], images: [{ url: '/sent.png', taskId: '' }] },
    { attachments: [
      { kind: 'image' as const, handle: '@img2', taskId: 't2', assetId: 'a2', url: '/tasks/t2/assets/a2' },
      { kind: 'model' as const, handle: '@model1', taskId: 't3' },
    ] },
  ];

  it('lists every session picture by its server handle, sent pictures by their slot, and models by name only', () => {
    const references = handleReferences(messages);
    expect(references.get('@img1')).toEqual({ handle: '@img1', url: '/sent.png' });
    expect(references.get('@img2')).toEqual({ handle: '@img2', url: '/tasks/t2/assets/a2', taskId: 't2', assetId: 'a2' });
    expect(references.get('@model1')).toEqual({ handle: '@model1', kind: 'model', url: '', taskId: 't3' });
  });

  it('splits text around the known picture handles only', () => {
    const known = (handle: string) => handle === '@img1';
    expect(splitHandles('把 @img1 的雾调淡，@img2 不变', known)).toEqual([
      { text: '把 ' }, { text: '@img1', handle: '@img1' }, { text: ' 的雾调淡，@img2 不变' },
    ]);
    expect(splitHandles('@img1。', known)).toEqual([{ text: '@img1', handle: '@img1' }, { text: '。' }]);
    expect(splitHandles('a@img1 @img10 @img1x', known)).toEqual([{ text: 'a@img1 @img10 @img1x' }]);
    expect(splitHandles('把 @model1 换贴图', (handle) => handle === '@model1')).toEqual([
      { text: '把 ' }, { text: '@model1', handle: '@model1' }, { text: ' 换贴图' },
    ]);
    expect(splitHandles('', known)).toEqual([]);
  });

  it('brings the stage picture by its session handle, else by its IDs; a model by name only', () => {
    const references = handleReferences(messages);
    expect(objectReference({ kind: 'image', url: '/x', taskId: 't2', assetId: 'a2' }, '@img2', references)).toEqual(references.get('@img2'));
    expect(objectReference({ kind: 'image', url: '/c.png', taskId: 'canvas:1' }, '@img9', references)).toEqual({ handle: '@img9', url: '/c.png' });
    expect(objectReference({ kind: 'model', url: '/m.glb', taskId: 't3' }, '@model1', references)).toEqual(references.get('@model1'));
    expect(objectReference({ kind: 'model', url: '/m.glb', taskId: 't9' }, '@model9', references)).toEqual({ handle: '@model9', kind: 'model', url: '' });
  });
});
