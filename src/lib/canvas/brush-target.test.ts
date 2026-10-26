import { describe, expect, it } from 'vitest';

import { brushTargetAt } from '@/lib/canvas/brush-target';
import type { CanvasNode } from '@/store/canvas-store';

const image = (id: string, extra: Partial<CanvasNode> = {}): CanvasNode =>
  ({ id, type: 'image', x: 0, y: 0, width: 100, height: 100, imageUrl: `/${id}.png`, ...extra }) as CanvasNode;

describe('brushTargetAt (#245)', () => {
  const ready = () => true;

  it('targets the topmost visible image under the pointer, regardless of selection', () => {
    const nodes = [image('bottom'), image('top', { x: 50 })];
    expect(brushTargetAt(nodes, { x: 60, y: 10 }, ready)).toMatchObject({ state: 'ready', node: { id: 'top' } });
    expect(brushTargetAt(nodes, { x: 10, y: 10 }, ready)).toMatchObject({ state: 'ready', node: { id: 'bottom' } });
  });

  it('does not pass through a locked top image to the one beneath', () => {
    const nodes = [image('bottom'), image('top', { locked: true })];
    expect(brushTargetAt(nodes, { x: 10, y: 10 }, ready)).toMatchObject({ state: 'locked', node: { id: 'top' } });
  });

  it('skips hidden images and non-image layers', () => {
    const nodes = [image('bottom'), image('hidden', { visible: false }), { ...image('text'), type: 'text' } as CanvasNode];
    expect(brushTargetAt(nodes, { x: 10, y: 10 }, ready)).toMatchObject({ state: 'ready', node: { id: 'bottom' } });
  });

  it('reports a picture that is still loading', () => {
    expect(brushTargetAt([image('a')], { x: 10, y: 10 }, () => false)).toMatchObject({ state: 'loading', node: { id: 'a' } });
  });

  it('reports no target outside every image, including for rotated ones', () => {
    expect(brushTargetAt([image('a')], { x: 150, y: 10 }, ready)).toEqual({ node: null, state: 'none' });
    const rotated = image('r', { rotation: 90 });
    expect(brushTargetAt([rotated], { x: 10, y: 10 }, ready).state).toBe('none');
    expect(brushTargetAt([rotated], { x: -10, y: 10 }, ready).state).toBe('ready');
  });
});
