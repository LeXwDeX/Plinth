import { beforeEach, describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';

function node(id: string): CanvasNode {
  return {
    id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '',
    x: 0, y: 0, width: 10, height: 10, zIndex: 1,
    points: [{ x: 1, y: 2 }],
  } as unknown as CanvasNode;
}

describe('canvas store undo history', () => {
  beforeEach(() => {
    configureCanvasFrontend(
      (defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } })
        .application.canvas.frontend,
    );
    useCanvasStore.setState({
      nodesBySession: {}, selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {},
      _lastUndoPushTime: {}, _savedHashBySession: {},
    });
  });

  it('clears the session history when the server state is loaded or reloaded', () => {
    const store = useCanvasStore.getState();
    store.loadNodesFromDB('s', [node('a')]);
    store.addNode('s', node('b'));
    expect(useCanvasStore.getState()._undoStackBySession.s?.length).toBeGreaterThan(0);
    useCanvasStore.getState().loadNodesFromDB('s', [node('a')]);
    expect(useCanvasStore.getState()._undoStackBySession.s).toEqual([]);
    expect(useCanvasStore.getState()._redoStackBySession.s).toEqual([]);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.map((n) => n.id)).toEqual(['a']);
  });

  it('keeps undo snapshots independent of later nested mutations', () => {
    const store = useCanvasStore.getState();
    store.loadNodesFromDB('s', [node('a')]);
    store.addNode('s', node('b'));
    const live = useCanvasStore.getState().nodesBySession.s!.find((n) => n.id === 'a') as unknown as { points: { x: number }[] };
    live.points.push({ x: 9 });
    live.points[0].x = 99;
    const snapshot = useCanvasStore.getState()._undoStackBySession.s![0].nodes[0] as unknown as { points: { x: number }[] };
    expect(snapshot.points).toEqual([{ x: 1, y: 2 }]);
  });

  it('records an insert as exactly one undo step', () => {
    const store = useCanvasStore.getState();
    store.loadNodesFromDB('s', [node('a')]);
    store.addNode('s', node('b'));
    expect(useCanvasStore.getState()._undoStackBySession.s).toHaveLength(1);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.map((n) => n.id)).toEqual(['a']);
  });

  it('a freshly loaded canvas has nothing to undo, and says whether it threw earlier history away', () => {
    const store = useCanvasStore.getState();
    expect(store.loadNodesFromDB('s', [node('a')])).toBe(false);
    expect(useCanvasStore.getState().canUndo('s')).toBe(false);
    useCanvasStore.getState().addNode('s', node('b'));
    expect(useCanvasStore.getState().loadNodesFromDB('s', [node('a')])).toBe(true);
    expect(useCanvasStore.getState().canUndo('s')).toBe(false);
  });

  it('folds a burst of nudges into one undo step', () => {
    useCanvasStore.getState().loadNodesFromDB('s', [node('a')]);
    for (let step = 1; step <= 5; step += 1) useCanvasStore.getState().updateNodes('s', [{ id: 'a', x: step, y: 0 }]);
    expect(useCanvasStore.getState()._undoStackBySession.s).toHaveLength(1);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s![0].x).toBe(0);
  });

  it('locking is an undo step and a locked layer ignores style patches', () => {
    useCanvasStore.getState().loadNodesFromDB('s', [node('a')]);
    useCanvasStore.getState().selectNodes(['a']);
    useCanvasStore.getState().toggleSelectedLock('s');
    expect(useCanvasStore.getState().nodesBySession.s![0].locked).toBe(true);
    useCanvasStore.getState().patchNodes('s', [{ id: 'a', patch: { strokeWidth: 9 } }]);
    expect(useCanvasStore.getState().nodesBySession.s![0].strokeWidth).not.toBe(9);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s![0].locked).toBeFalsy();
  });

  it('stamping hides the covered layers, adds one on top, and undoes as one step', () => {
    useCanvasStore.getState().loadNodesFromDB('s', [node('a'), { ...node('b'), zIndex: 2 }]);
    useCanvasStore.getState().stampNodes('s', ['a', 'b'], {
      id: 'm', type: 'image', imageUrl: 'u', taskId: 't', x: 0, y: 0, width: 10, height: 10,
    });
    const after = useCanvasStore.getState().nodesBySession.s!;
    expect(after.filter((n) => n.visible === false).map((n) => n.id)).toEqual(['a', 'b']);
    expect(after.find((n) => n.id === 'm')!.zIndex).toBe(3);
    expect(useCanvasStore.getState()._undoStackBySession.s).toHaveLength(1);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s!.map((n) => n.id)).toEqual(['a', 'b']);
    expect(useCanvasStore.getState().nodesBySession.s!.every((n) => n.visible !== false)).toBe(true);
  });

  it('deleting a multi-selection is one step; unchanged text records nothing', () => {
    useCanvasStore.getState().loadNodesFromDB('s', [node('a'), { ...node('b'), type: 'text', text: 'hi' } as CanvasNode]);
    useCanvasStore.getState().updateNodeText('s', 'b', 'hi');
    expect(useCanvasStore.getState().canUndo('s')).toBe(false);
    useCanvasStore.getState().selectNodes(['a', 'b']);
    useCanvasStore.getState().removeSelectedNodes('s');
    expect(useCanvasStore.getState()._undoStackBySession.s).toHaveLength(1);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s).toHaveLength(2);
  });
});
