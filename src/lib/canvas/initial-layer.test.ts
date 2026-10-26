import { describe, expect, it } from 'vitest';
import type { CanvasNode } from '@/store/canvas-store';
import { artboardContent } from './export-canvas';
import { artboardIsFree, imageNodePlacement } from './image-placement';
import { blankBitmapSize, blankLayerFill, brushCreatesCanvasLayer, canvasLayerNode, initialCanvasNodes, initialLayerNode, isBlankLayer, withInitialLayer } from './initial-layer';
import { nodeToRecord, recordToNode } from './node-persistence';
import { selectedImageLayer } from './selected-layer';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';

const canvas = catalog.application.canvas as unknown as CanvasProductSchema;
const frame = { x: -424, y: -624, width: 2048, height: 2048 };
const options = { fill: 'transparent', name: '画布图层', taskId: 'upload' };

describe('initial bitmap layer (#246 phase 2)', () => {
  it('is a normal, unlocked, visible picture layer covering the canvas, with no picture yet', () => {
    const layer = initialLayerNode('layer-1', frame, options, 'transparent');
    expect(layer).toMatchObject({ id: 'layer-1', type: 'image', imageUrl: '', blank: true, name: '画布图层', ...frame, zIndex: 0 });
    expect(layer.locked).toBeUndefined();
    expect(layer.visible).toBeUndefined();
    expect(isBlankLayer(layer)).toBe(true);
    expect(blankLayerFill(layer, 'transparent')).toBeNull();
    expect(blankLayerFill(initialLayerNode('l', frame, { ...options, fill: '#ffffff' }, 'transparent'), 'transparent')).toBe('#ffffff');
  });

  it('is added only to a new empty canvas when the Schema enables it', () => {
    const made = withInitialLayer([], frame, canvas, '画布图层', () => 'new-layer');
    expect(made).toHaveLength(1);
    expect(made[0]).toMatchObject({ id: 'new-layer', blank: true, name: '画布图层', width: 2048 });
    const existing: CanvasNode[] = [{ id: 'x', type: 'text', text: 'hi', imageUrl: '', taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 }];
    expect(withInitialLayer(existing, frame, canvas, '画布图层', () => 'n')).toBe(existing);
    const disabled = { ...canvas, frontend: { ...canvas.frontend, initial_layer: { ...canvas.frontend.initial_layer, enabled: false } } };
    expect(withInitialLayer([], frame, disabled, '画布图层', () => 'n')).toEqual([]);
  });

  it('is added on the first sizing of a new empty canvas only: never to a saved, migrated or captured canvas', async () => {
    const { resolveCanvasFrame } = await import('./canvas-frame');
    const newId = () => 'layer';
    const name = '画布图层';
    const capture: CanvasNode = { id: 'capture', type: 'image', imageUrl: '/api/v1/assets/c', taskId: 't', x: 0, y: 0, width: 10, height: 10, zIndex: 1 };
    const legacyRect: CanvasNode = { id: 'r', type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 344, y: 144, width: 512, height: 512, zIndex: 1 };
    const initial = { width: 2048, height: 2048 };
    // New pixel canvas, nothing on it yet.
    const fresh = resolveCanvasFrame(canvas, { frame: null, coordinateVersion: 2 }, [], initial);
    expect(initialCanvasNodes(fresh, frame, canvas, name, newId).map((node) => node.id)).toEqual(['layer']);
    // A saved size: the canvas is not new.
    const sized = resolveCanvasFrame(canvas, { frame: { width: 2048, height: 2048, coordinateVersion: 2 }, coordinateVersion: 2 }, [], initial);
    expect(initialCanvasNodes(sized, frame, canvas, name, newId)).toEqual([]);
    // A legacy canvas being migrated keeps exactly its own (scaled) nodes.
    const migrated = resolveCanvasFrame(canvas, { frame: null, coordinateVersion: null }, [legacyRect], initial);
    expect(initialCanvasNodes(migrated, frame, canvas, name, newId).map((node) => node.id)).toEqual(['r']);
    // A new canvas a 3D capture already wrote to keeps the capture and gets no layer under it.
    const captured = resolveCanvasFrame(canvas, { frame: null, coordinateVersion: 2 }, [capture], initial);
    expect(initialCanvasNodes(captured, frame, canvas, name, newId)).toEqual([capture]);
  });

  it('caps the bitmap of a large canvas at the Schema maximum, keeping the ratio', () => {
    expect(blankBitmapSize({ width: 2048, height: 2048 }, canvas.frontend.initial_layer.maximum_bitmap_pixels)).toEqual({ width: 2048, height: 2048 });
    expect(blankBitmapSize({ width: 8192, height: 8192 }, 16_777_216)).toEqual({ width: 4096, height: 4096 });
    expect(blankBitmapSize({ width: 8192, height: 2048 }, 16_777_216)).toEqual({ width: 8192, height: 2048 });
    const wide = blankBitmapSize({ width: 8192, height: 4096 }, 16_777_216);
    expect(wide.width * wide.height).toBeLessThanOrEqual(16_777_216);
    expect(wide.width / wide.height).toBeCloseTo(2, 2);
  });

  it('survives a save and reload unchanged (persistence round trip)', () => {
    const layer = initialLayerNode('layer-1', frame, options, 'transparent');
    const record = nodeToRecord(layer);
    expect(JSON.parse(record.data)).toMatchObject({ blank: true, name: '画布图层' });
    expect(recordToNode(record)).toMatchObject({ blank: true, imageUrl: '', name: '画布图层' });
    // A painted layer (a real picture) is not blank any more.
    expect(recordToNode(nodeToRecord({ ...layer, blank: undefined, imageUrl: '/api/v1/assets/p' }))?.blank).toBeUndefined();
  });

  it('a transparent blank layer is not content: export, the frame-is-free rule and picture actions ignore it', () => {
    const layer = initialLayerNode('layer-1', frame, options, 'transparent');
    expect(artboardContent([layer], frame)).toEqual([]);
    expect(artboardIsFree([layer], frame)).toBe(true);
    // A result placed on a new canvas lands inside the frame, on top of the blank layer.
    expect(imageNodePlacement([layer], { width: 1024, height: 1024 }, canvas.frontend, frame)).toEqual({ x: 88, y: -112, width: 1024, height: 1024 });
    expect(selectedImageLayer([layer], ['layer-1'])).toBeNull();
    // A colour-filled blank layer is visible ink, so it counts as content.
    const white = initialLayerNode('w', frame, { ...options, fill: '#ffffff' }, 'transparent');
    expect(artboardContent([white], frame)).toEqual([white]);
  });

  it('the brush on a part of the canvas with no picture under it gets a canvas layer; nothing else does', () => {
    const inside = { x: 600, y: 400 };
    expect(brushCreatesCanvasLayer('none', 'brush', inside, frame, true)).toBe(true);
    // Outside the canvas, over a picture, over a locked or loading picture, with the eraser, or when the
    // Schema turns the initial layer off: the existing #245 behaviour.
    expect(brushCreatesCanvasLayer('none', 'brush', { x: -1000, y: 400 }, frame, true)).toBe(false);
    expect(brushCreatesCanvasLayer('ready', 'brush', inside, frame, true)).toBe(false);
    expect(brushCreatesCanvasLayer('locked', 'brush', inside, frame, true)).toBe(false);
    expect(brushCreatesCanvasLayer('loading', 'brush', inside, frame, true)).toBe(false);
    expect(brushCreatesCanvasLayer('none', 'eraser', inside, frame, true)).toBe(false);
    expect(brushCreatesCanvasLayer('none', 'brush', inside, null, true)).toBe(false);
    expect(brushCreatesCanvasLayer('none', 'brush', inside, frame, false)).toBe(false);
    expect(canvasLayerNode('new', frame, canvas, '画布图层')).toMatchObject({ id: 'new', blank: true, name: '画布图层', ...frame });
  });
});
