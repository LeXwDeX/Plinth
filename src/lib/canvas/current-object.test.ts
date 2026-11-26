import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { alignUpdates } from './align';
import { artboardDiffersFromObject, canvasSnapshotClaim, currentObjectSource } from './current-object';
import { imageNodePlacement } from './image-placement';
import type { CanvasProductSchema } from '@/lib/product-schema';
import type { CanvasNode } from '@/store/canvas-store';

const frontend = (defaultCatalog.application.canvas as unknown as CanvasProductSchema).frontend;

const artboard = { x: 0, y: 0, width: 512, height: 512 };
const image = (id: string, extra: Partial<CanvasNode> = {}): CanvasNode => ({
  id, type: 'image', imageUrl: `/a/${id}`, taskId: `t-${id}`, x: 10, y: 10, width: 200, height: 200, zIndex: 1, ...extra,
});
const rectangle = (id: string): CanvasNode => ({
  id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 20, y: 20, width: 50, height: 50, zIndex: 2,
});
const same = (url: string) => url;

describe('Agent current object source', () => {
  const objectA = { kind: 'image', url: '/a/a', taskId: 't-a' };

  it('sends the artboard while it holds the object\'s layer, even its one untouched picture', () => {
    expect(currentObjectSource([image('a')], artboard, objectA, same)).toBe('canvas');
    expect(currentObjectSource([image('a', { x: 0, y: 0, width: 512, height: 512 })], artboard, objectA, same)).toBe('canvas');
    expect(currentObjectSource([image('a'), rectangle('r')], artboard, objectA, same)).toBe('canvas');
    expect(currentObjectSource([image('a', { rotation: 10 })], artboard, objectA, same)).toBe('canvas');
    // A brush-baked layer keeps the object's task: it is still the object, edited on the board.
    expect(currentObjectSource([image('a', { imageUrl: '/a/baked' })], artboard, objectA, same)).toBe('canvas');
  });

  it('an empty artboard (hidden or outside layers only), or no artboard, keeps the object', () => {
    expect(currentObjectSource([image('a', { x: 2000 })], artboard, objectA, same)).toBe('object');
    expect(currentObjectSource([image('a', { visible: false })], artboard, objectA, same)).toBe('object');
    expect(currentObjectSource([image('a'), rectangle('r')], null, objectA, same)).toBe('object');
  });

  // #610: the artboard is the turn's source only when the person works on the current object there. A version
  // clicked on the strip (not on the board), or no current object at all, never sends whatever else the board
  // holds; the person names the board with @画布 (or paints a mask) to send it.
  it('keeps the object when the artboard holds other pictures but not the object\'s layer', () => {
    const bow = image('bow');
    const axe = { kind: 'image', url: '/a/axe', taskId: 't-axe' };
    expect(currentObjectSource([bow], artboard, axe, same)).toBe('object');
    expect(currentObjectSource([bow, rectangle('r')], artboard, axe, same)).toBe('object');
    // The object's layer lies outside the frame while another picture is inside: still not the board.
    expect(currentObjectSource([bow, image('axe', { x: 2000 })], artboard, axe, same)).toBe('object');
    // Once the object's layer is in the frame, the frame (bow included) is what the person laid out.
    expect(currentObjectSource([bow, image('axe', { x: 250 })], artboard, axe, same)).toBe('canvas');
  });

  it('never sends the artboard without a current picture, or for a 3D object', () => {
    expect(currentObjectSource([image('a')], artboard, null, same)).toBe('object');
    expect(currentObjectSource([image('a')], artboard, { kind: 'model', url: '/a/a', taskId: 't-a' }, same)).toBe('object');
  });

  it('matches the layer by the address the board stores (the proxied URL)', () => {
    const proxied = (url: string) => `/proxy${url}`;
    const object = { kind: 'image', url: '/raw/x', taskId: 'upload:x' };
    expect(currentObjectSource([image('x', { imageUrl: '/proxy/raw/x', taskId: 'upload' })], artboard, object, proxied)).toBe('canvas');
    expect(currentObjectSource([image('x', { imageUrl: '/raw/x', taskId: 'upload' })], artboard, object, proxied)).toBe('object');
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

  // #648: the dev-test geometry. The newest picture fills the empty frame; the version put on the board after it is
  // placed next to the frame (#284), so the snapshot is one layer (the board the person sees), not two. Aligned
  // into the frame (one layer aligns to the frame), it is counted.
  it('counts what the dashed frame shows: a picture placed next to it is not in the snapshot until moved in', () => {
    const frame = { x: -424, y: -624, width: 2048, height: 2048 };
    const blank: CanvasNode = { ...image('blank'), ...frame, imageUrl: '', taskId: 'upload', blank: true, zIndex: 0 };
    const canteen = image('canteen', { x: -88, y: 16, width: 1376, height: 768 });
    const placed = imageNodePlacement([blank, canteen], { width: 2048, height: 1143 }, frontend, frame);
    const pouch = image('pouch', { ...placed, zIndex: 2 });
    expect(pouch.x).toBeGreaterThanOrEqual(frame.x + frame.width);
    expect(canvasSnapshotClaim('/s', [blank, canteen, pouch], frame))
      .toEqual({ kind: 'image', url: '/s', canvasLayers: 1, canvasSources: ['/a/canteen'] });
    let nodes = [blank, canteen, pouch];
    for (const mode of ['hcenter', 'vcenter'] as const) {
      const updates = alignUpdates(nodes, ['pouch'], mode, frame);
      nodes = nodes.map((node) => ({ ...node, ...updates.find((update) => update.id === node.id) }));
    }
    expect(canvasSnapshotClaim('/s', nodes, frame))
      .toEqual({ kind: 'image', url: '/s', canvasLayers: 2, canvasSources: ['/a/canteen', '/a/pouch'] });
  });
});
