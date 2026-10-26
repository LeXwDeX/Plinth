import { beforeEach, describe, expect, it } from 'vitest';
import { decodeCanvasDrag, encodeCanvasDrag, hasCanvasDrag, CANVAS_ITEM_MIME } from '@/lib/workspace/canvas-drag';
import { useCanvasStore } from '@/store/canvas-store';
import { dropNodeRect, dropPointToWorld } from './drop-placement';

describe('dropPointToWorld', () => {
  it('undoes the element offset, the pan and the zoom', () => {
    expect(dropPointToWorld({ x: 500, y: 300 }, { left: 100, top: 50 }, { zoom: 2, panX: 40, panY: -10 })).toEqual({ x: 180, y: 130 });
    expect(dropPointToWorld({ x: 100, y: 50 }, { left: 100, top: 50 }, { zoom: 1, panX: 0, panY: 0 })).toEqual({ x: 0, y: 0 });
  });
});

describe('dropNodeRect', () => {
  it('centres the picture on the drop point at its natural size', () => {
    expect(dropNodeRect({ x: 100, y: 100 }, { width: 200, height: 100 })).toEqual({ x: 0, y: 50, width: 200, height: 100 });
  });
  it('shrinks to fit the artboard but never enlarges', () => {
    expect(dropNodeRect({ x: 0, y: 0 }, { width: 2000, height: 1000 }, { width: 1000, height: 1000 })).toEqual({ x: -500, y: -250, width: 1000, height: 500 });
    expect(dropNodeRect({ x: 0, y: 0 }, { width: 100, height: 100 }, { width: 1000, height: 1000 })).toMatchObject({ width: 100, height: 100 });
  });
});

describe('canvas drag payload', () => {
  it('round-trips an item and rejects malformed data', () => {
    const item = { taskId: 't1', assetId: 'a1', url: '/a/1', kind: 'image' as const, width: 10, height: 20, label: 'v1' };
    expect(decodeCanvasDrag(encodeCanvasDrag(item))).toEqual(item);
    expect(decodeCanvasDrag('not json')).toBeNull();
    expect(decodeCanvasDrag(JSON.stringify({ kind: 'model', url: '/m' }))).toBeNull();
    expect(decodeCanvasDrag(JSON.stringify({ kind: 'image', url: '', taskId: 't', assetId: 'a' }))).toBeNull();
    expect(decodeCanvasDrag(null)).toBeNull();
    expect(hasCanvasDrag(['Files', CANVAS_ITEM_MIME])).toBe(true);
    expect(hasCanvasDrag(['Files'])).toBe(false);
  });
});

describe('dropping a card', () => {
  beforeEach(() => useCanvasStore.setState({ nodesBySession: {}, _undoStackBySession: {}, _redoStackBySession: {}, selectedNodeIds: [] }));
  it('adds one node carrying the task id and one undo entry; undo removes it', () => {
    const world = dropPointToWorld({ x: 300, y: 200 }, { left: 0, top: 0 }, { zoom: 1, panX: 0, panY: 0 });
    const rect = dropNodeRect(world, { width: 100, height: 80 });
    useCanvasStore.getState().addNode('s1', { id: 'n1', type: 'image', taskId: 't1', imageUrl: '/a/1', ...rect });
    const state = useCanvasStore.getState();
    expect(state.nodesBySession.s1).toHaveLength(1);
    expect(state.nodesBySession.s1[0]).toMatchObject({ taskId: 't1', x: 250, y: 160 });
    expect(state._undoStackBySession.s1).toHaveLength(1);
    state.undo('s1');
    expect(useCanvasStore.getState().nodesBySession.s1).toHaveLength(0);
  });
});
