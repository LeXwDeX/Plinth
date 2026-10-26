import { describe, expect, it } from 'vitest';

import { reconcileObjectWithNodes } from './object-sync';
import type { CanvasNode } from '@/store/canvas-store';
import type { CurrentObject } from '@/store/workspace-store';

const node = (id: string, url = `/a/${id}`): CanvasNode => ({
  id, type: 'image', imageUrl: url, taskId: `t-${id}`, x: 0, y: 0, width: 10, height: 10, zIndex: 1,
});
const base: CurrentObject = { taskId: 't-base', kind: 'image', url: '/a/base', sessionId: 's' };
const merged = (id: string): Pick<CurrentObject, 'taskId' | 'kind' | 'url'> => ({ taskId: `canvas:merged:${id}`, kind: 'image', url: `/a/${id}` });

describe('current object and reference after nodes vanish', () => {
  it('undoing a merge restores the base picture and releases the merged reference', () => {
    const result = reconcileObjectWithNodes({
      previous: [node('base'), node('m')], next: [node('base')],
      currentObject: merged('m'), canvasReference: '/a/m',
      mergeBases: { 'canvas:merged:m': base },
    });
    expect(result).toEqual({ restoreObject: base, clearReference: true });
  });

  it('a deleted ordinary object is left alone, the server pick is not touched', () => {
    const result = reconcileObjectWithNodes({
      previous: [node('a')], next: [],
      currentObject: { taskId: 't-a', kind: 'image', url: '/a/a' }, canvasReference: null, mergeBases: {},
    });
    expect(result).toEqual({ restoreObject: null, clearReference: false });
  });

  it('a pinned reference to a vanished picture is released, but kept while another node shows it', () => {
    const input = { currentObject: null, mergeBases: {}, canvasReference: '/a/x' };
    expect(reconcileObjectWithNodes({ ...input, previous: [node('x')], next: [] }).clearReference).toBe(true);
    expect(reconcileObjectWithNodes({ ...input, previous: [node('x'), node('y', '/a/x')], next: [node('y', '/a/x')] }).clearReference).toBe(false);
  });

  it('does nothing while every node is still there', () => {
    const result = reconcileObjectWithNodes({
      previous: [node('a')], next: [node('a'), node('b')], currentObject: merged('a'), canvasReference: '/a/a', mergeBases: {},
    });
    expect(result).toEqual({ restoreObject: null, clearReference: false });
  });
});
