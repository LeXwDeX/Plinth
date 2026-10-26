import { describe, expect, it } from 'vitest';

import type { CanvasNode } from '@/store/canvas-store';
import { mergedObjectId, mergedVersionsOf } from './merge-version';

const node = (id: string, patch: Partial<CanvasNode> = {}): CanvasNode => ({
  id, type: 'image', imageUrl: '/a/orig', taskId: 'task1', x: 0, y: 0, width: 100, height: 100, zIndex: 1, ...patch,
});

describe('merged versions', () => {
  it('lists the nodes made by a merge, oldest first, and names them without a server task', () => {
    const a = node('a', { imageUrl: '/api/v1/assets/a', taskId: mergedObjectId('a'), mergedFrom: 'task1', mergedAt: '2026-10-08T10:02:00Z' });
    const b = node('b', { imageUrl: '/api/v1/assets/b', taskId: mergedObjectId('b'), mergedFrom: mergedObjectId('a'), mergedAt: '2026-10-08T10:05:00Z' });
    expect(mergedVersionsOf([b, node('plain'), a]).map((item) => [item.nodeId, item.from])).toEqual([['a', 'task1'], ['b', 'canvas:merged:a']]);
    expect(mergedObjectId('a').startsWith('canvas:')).toBe(true);
  });
});
