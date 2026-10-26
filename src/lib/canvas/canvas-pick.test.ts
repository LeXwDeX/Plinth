import { describe, expect, it } from 'vitest';
import type { CanvasNode } from '@/store/canvas-store';
import { cropTargetAt, marqueeSelection, pickableOnCanvas } from './canvas-pick';
import { selectableNodeIds } from './context-menu';
import { initialLayerNode } from './initial-layer';

const frame = { x: -424, y: -624, width: 2048, height: 2048 };
const blank = initialLayerNode('canvas-layer', frame, { fill: 'transparent', name: '画布图层', taskId: 'upload' }, 'transparent');
const shape: CanvasNode = { id: 'shape', type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 500, y: 300, width: 100, height: 100, zIndex: 1 };
const picture: CanvasNode = { id: 'picture', type: 'image', imageUrl: '/api/v1/assets/p', taskId: 't', x: 0, y: 0, width: 400, height: 400, zIndex: 2 };

describe('canvas picking leaves the unpainted initial layer to the layers panel (#246 phase 2)', () => {
  it('a marquee over the canvas picks the layers inside, never the blank layer under them', () => {
    expect(marqueeSelection([blank, shape, picture], { left: 450, top: 250, right: 700, bottom: 500 })).toEqual(['shape']);
    expect(marqueeSelection([blank], { left: -1000, top: -1000, right: 3000, bottom: 3000 })).toEqual([]);
  });

  it('the crop tool finds the picture under the point, not the blank layer', () => {
    expect(cropTargetAt([blank, picture], { x: 100, y: 100 })?.id).toBe('picture');
    expect(cropTargetAt([blank, picture], { x: 1000, y: 1000 })).toBeUndefined();
  });

  it('select all leaves it out; once painted it is an ordinary picture again', () => {
    expect(selectableNodeIds([blank, shape, picture])).toEqual(['shape', 'picture']);
    const painted = { ...blank, blank: undefined, imageUrl: '/api/v1/assets/painted' };
    expect(pickableOnCanvas(painted)).toBe(true);
    expect(selectableNodeIds([painted])).toEqual(['canvas-layer']);
    expect(marqueeSelection([painted], { left: 0, top: 0, right: 10, bottom: 10 })).toEqual(['canvas-layer']);
  });
});
