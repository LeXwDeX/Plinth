import { describe, expect, it } from 'vitest';
import type { CanvasNode } from '@/store/canvas-store';
import { anchorOffset, resizeCanvasNodes } from './canvas-resize';
import { initialLayerNode } from './initial-layer';

const center = { x: 600, y: 400 };
const square = (id: string, x: number, y: number, size = 100): CanvasNode => ({
  id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x, y, width: size, height: size, zIndex: 1,
});

describe('anchorOffset (#246 phase 2)', () => {
  it('keeps content where it is for the centre anchor (the frame grows around its centre)', () => {
    expect(anchorOffset({ width: 1000, height: 1000 }, { width: 2000, height: 1600 }, 'center')).toEqual({ x: 0, y: 0 });
  });

  it('keeps the distance to the anchored edges', () => {
    // Growing 1000 -> 2000 wide moves the left edge 500 further left; anchored left, content follows it.
    expect(anchorOffset({ width: 1000, height: 1000 }, { width: 2000, height: 1600 }, 'top-left')).toEqual({ x: -500, y: -300 });
    expect(anchorOffset({ width: 1000, height: 1000 }, { width: 2000, height: 1600 }, 'bottom-right')).toEqual({ x: 500, y: 300 });
    expect(anchorOffset({ width: 1000, height: 1000 }, { width: 2000, height: 1600 }, 'top')).toEqual({ x: 0, y: -300 });
    expect(anchorOffset({ width: 2000, height: 2000 }, { width: 1000, height: 1000 }, 'left')).toEqual({ x: 500, y: 0 });
  });
});

describe('resizeCanvasNodes (#246 phase 2)', () => {
  it('keeps every layer at its pixel size and its place relative to the anchor', () => {
    const nodes = [square('a', 100, 0), square('b', 500, 300)];
    const resized = resizeCanvasNodes(nodes, { width: 1000, height: 1000 }, { width: 2000, height: 2000 }, 'top-left', center);
    expect(resized.nodes.map((node) => [node.x, node.y, node.width, node.height])).toEqual([[-400, -500, 100, 100], [0, -200, 100, 100]]);
    // Relative to the frame's top-left corner nothing moved: old frame (100,-100), new frame (-400,-600).
    expect(resized.nodes[0].x - (-400)).toBe(100 - 100);
    expect(resized.clipped).toEqual([]);
  });

  it('stretches nothing: an image keeps its size and crop', () => {
    const image: CanvasNode = { ...square('img', 300, 100, 400), type: 'image', imageUrl: '/api/v1/assets/a', crop: { x: 1, y: 2, width: 3, height: 4 } };
    const [node] = resizeCanvasNodes([image], { width: 1000, height: 1000 }, { width: 3000, height: 1000 }, 'center', center).nodes;
    expect(node).toEqual(image);
  });

  it('names the layers a shrink pushes partly outside the canvas, without dropping them', () => {
    const inside = square('inside', 550, 350);
    const nearEdge = square('edge', 150, 350);
    const alreadyOutside = square('outside', -900, 0);
    const resized = resizeCanvasNodes([inside, nearEdge, alreadyOutside], { width: 1000, height: 1000 }, { width: 500, height: 500 }, 'center', center);
    expect(resized.nodes).toHaveLength(3);
    expect(resized.nodes.map((node) => node.id)).toEqual(['inside', 'edge', 'outside']);
    // Only what the shrink cut is reported: the layer that was already outside was outside before.
    expect(resized.clipped).toEqual(['edge']);
  });

  it('lets a still-blank initial layer follow the canvas, and keeps a painted one as content', () => {
    const blank = initialLayerNode('layer', { x: 100, y: -100, width: 1000, height: 1000 }, { fill: 'transparent', name: '画布图层', taskId: 'upload' }, 'transparent');
    const painted = { ...blank, id: 'painted', blank: undefined, imageUrl: '/api/v1/assets/painted' };
    const resized = resizeCanvasNodes([blank, painted], { width: 1000, height: 1000 }, { width: 2000, height: 1200 }, 'top-left', center);
    expect(resized.nodes[0]).toMatchObject({ x: -400, y: -200, width: 2000, height: 1200, blank: true });
    expect(resized.nodes[1]).toMatchObject({ x: -400, y: -200, width: 1000, height: 1000 });
    expect(resized.clipped).toEqual([]);
  });

  it('keeps a blank layer whose first strokes are not uploaded yet as content, so its bitmap is never stretched', () => {
    const blank = initialLayerNode('layer', { x: 100, y: -100, width: 1000, height: 1000 }, { fill: 'transparent', name: '画布图层', taskId: 'upload' }, 'transparent');
    const resized = resizeCanvasNodes([blank], { width: 1000, height: 1000 }, { width: 2000, height: 1200 }, 'top-left', center, new Set(['layer']));
    // Same pixel size as its local bitmap, moved with the anchor like any other layer.
    expect(resized.nodes[0]).toMatchObject({ x: -400, y: -200, width: 1000, height: 1000, blank: true });
  });
});

describe('local bitmap registry (#246 phase 2)', () => {
  it('lists the layers with a live brush bitmap per session until the lister is removed', async () => {
    const { localBitmapNodeIds, registerLocalBitmaps } = await import('./pending-edits');
    const off = registerLocalBitmaps('s', () => ['layer']);
    const other = registerLocalBitmaps('other', () => ['x']);
    expect([...localBitmapNodeIds('s')]).toEqual(['layer']);
    off();
    other();
    expect([...localBitmapNodeIds('s')]).toEqual([]);
  });
});
