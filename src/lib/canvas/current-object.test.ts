import { describe, expect, it } from 'vitest';

import { artboardDiffersFromObject, canvasSnapshotClaim, currentObjectSource } from './current-object';
import type { CanvasNode } from '@/store/canvas-store';

const artboard = { x: 0, y: 0, width: 512, height: 512 };
const image = (id: string, extra: Partial<CanvasNode> = {}): CanvasNode => ({
  id, type: 'image', imageUrl: `/a/${id}`, taskId: `t-${id}`, x: 10, y: 10, width: 200, height: 200, zIndex: 1, ...extra,
});
const rectangle = (id: string): CanvasNode => ({
  id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 20, y: 20, width: 50, height: 50, zIndex: 2,
});
const same = (url: string) => url;

describe('Agent current object source', () => {
  it('sends the artboard whenever it holds anything, even the object\'s one untouched picture', () => {
    expect(currentObjectSource([image('a')], artboard)).toBe('canvas');
    expect(currentObjectSource([image('a', { x: 0, y: 0, width: 512, height: 512 })], artboard)).toBe('canvas');
    expect(currentObjectSource([image('a'), rectangle('r')], artboard)).toBe('canvas');
    expect(currentObjectSource([image('a', { rotation: 10 })], artboard)).toBe('canvas');
  });

  it('an empty artboard (hidden or outside layers only), or no artboard, keeps the object', () => {
    expect(currentObjectSource([image('far', { x: 2000 })], artboard)).toBe('object');
    expect(currentObjectSource([image('a', { visible: false })], artboard)).toBe('object');
    expect(currentObjectSource([image('a'), rectangle('r')], null)).toBe('object');
  });
});

describe('artboard differs from the object (merge is offered)', () => {
  const object = { url: '/a/a' };

  it('not while the artboard holds that one untouched picture, or nothing', () => {
    expect(artboardDiffersFromObject([image('a')], artboard, object, same)).toBe(false);
    expect(artboardDiffersFromObject([image('a'), image('b', { visible: false })], artboard, object, same)).toBe(false);
    expect(artboardDiffersFromObject([image('a', { x: 2000 })], artboard, object, same)).toBe(false);
  });

  it('once a drawing or a second visible picture joins it', () => {
    expect(artboardDiffersFromObject([image('a'), rectangle('r')], artboard, object, same)).toBe(true);
    expect(artboardDiffersFromObject([image('a'), image('b', { x: 250 })], artboard, object, same)).toBe(true);
  });

  it('once the picture is cropped, rotated, faded or brush-baked', () => {
    expect(artboardDiffersFromObject([image('a', { crop: { x: 0, y: 0, width: 5, height: 5 } })], artboard, object, same)).toBe(true);
    expect(artboardDiffersFromObject([image('a', { rotation: 10 })], artboard, object, same)).toBe(true);
    expect(artboardDiffersFromObject([image('a', { opacity: 0.5 })], artboard, object, same)).toBe(true);
    expect(artboardDiffersFromObject([image('a', { imageUrl: '/a/baked' })], artboard, object, same)).toBe(true);
  });
});

describe('artboard snapshot claim (#272)', () => {
  it('counts the visible layers flattened into the snapshot, by the rasteriser\'s own rule', () => {
    expect(canvasSnapshotClaim('/api/v1/input-assets/s', [image('a'), image('b', { x: 300 })], artboard))
      .toEqual({ kind: 'image', url: '/api/v1/input-assets/s', canvasLayers: 2, canvasSources: ['/a/a', '/a/b'] });
    // Hidden layers and pictures outside the artboard are not in the picture.
    expect(canvasSnapshotClaim('/s', [image('a'), image('b', { visible: false }), image('c', { x: 2000 }), rectangle('r')], artboard))
      .toEqual({ kind: 'image', url: '/s', canvasLayers: 2, canvasSources: ['/a/a'] });
  });

  // #189: the snapshot names the generated pictures it flattens, so a tool that takes a source Task (the
  // Creative Lab build) can resolve it back to the preview it shows. The server verifies every one.
  it('names only the generated picture layers as the snapshot sources', () => {
    const nodes = [
      image('preview'),
      image('again', { x: 250, imageUrl: '/a/preview' }),
      image('upload', { x: 100, taskId: '' }),
      image('pasted', { x: 120, imageUrl: 'data:image/png;base64,AAAA' }),
      image('capture', { x: 140, captureId: 'cap-1' }),
      rectangle('r'),
    ];
    expect(canvasSnapshotClaim('/s', nodes, artboard)).toEqual({ kind: 'image', url: '/s', canvasLayers: 6, canvasSources: ['/a/preview'] });
  });

  it('claims no count for an empty artboard', () => {
    expect(canvasSnapshotClaim('/s', [image('far', { x: 2000 })], artboard)).toEqual({ kind: 'image', url: '/s' });
  });
});
