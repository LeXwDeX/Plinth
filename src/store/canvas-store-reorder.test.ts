import { beforeEach, describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';


function node(id: string, zIndex: number): CanvasNode {
  return {
    id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '',
    x: 0, y: 0, width: 10, height: 10, zIndex,
  };
}

describe('canvas store reorder semantics (layers panel regression)', () => {
  beforeEach(() => {
    configureCanvasFrontend(
      (
        defaultCatalog as unknown as {
          application: {
            canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] };
          };
        }
      ).application.canvas.frontend,
    );
    useCanvasStore.setState({
      nodesBySession: {},
      selectedNodeIds: [],
      _undoStackBySession: {},
      _redoStackBySession: {},
      _lastUndoPushTime: {},
      _savedHashBySession: {},
    });
  });

  it('moves a node up by ascending index even when zIndex values are non-contiguous', () => {
    // zIndex 1/3/7（删除后不连续）：索引 0 的节点「上移一格」应到索引 1，
    // 而不是按 zIndex 值跳到 zIndex+1=2 之类的空洞位置。
    useCanvasStore.setState((state) => ({
      nodesBySession: { ...state.nodesBySession, s: [node('a', 1), node('b', 3), node('c', 7)] },
    }));
    useCanvasStore.getState().reorderNodes('s', 'a', 1);
    const order = useCanvasStore.getState().getNodes('s')
      .sort((x, y) => x.zIndex - y.zIndex).map((item) => item.id);
    expect(order).toEqual(['b', 'a', 'c']);
  });

  it('moves a node down by ascending index', () => {
    useCanvasStore.setState((state) => ({
      nodesBySession: { ...state.nodesBySession, s: [node('a', 1), node('b', 2), node('c', 3)] },
    }));
    useCanvasStore.getState().reorderNodes('s', 'c', 1);
    const order = useCanvasStore.getState().getNodes('s')
      .sort((x, y) => x.zIndex - y.zIndex).map((item) => item.id);
    expect(order).toEqual(['a', 'c', 'b']);
  });

  it('reindexes zIndex 1..n contiguously after a reorder', () => {
    useCanvasStore.setState((state) => ({
      nodesBySession: { ...state.nodesBySession, s: [node('a', 1), node('b', 3), node('c', 7)] },
    }));
    useCanvasStore.getState().reorderNodes('s', 'a', 2);
    const sorted = useCanvasStore.getState().getNodes('s').sort((x, y) => x.zIndex - y.zIndex);
    expect(sorted.map((item) => item.zIndex)).toEqual([1, 2, 3]);
    expect(sorted.map((item) => item.id)).toEqual(['b', 'c', 'a']);
  });

  it('pastes copies of locked layers unlocked and leaves the originals locked', () => {
    useCanvasStore.setState((state) => ({
      nodesBySession: { ...state.nodesBySession, s: [{ ...node('a', 1), locked: true }, node('b', 2)] },
      selectedNodeIds: ['a', 'b'],
    }));
    const store = useCanvasStore.getState();
    store.copySelectedNodes('s');
    store.pasteNodes('s');
    const nodes = useCanvasStore.getState().getNodes('s');
    expect(nodes).toHaveLength(4);
    expect(nodes.find((n) => n.id === 'a')?.locked).toBe(true);
    expect(nodes.filter((n) => n.id !== 'a' && n.id !== 'b').every((n) => !n.locked)).toBe(true);
    // A second paste (the clipboard follows the last paste) stays unlocked too.
    useCanvasStore.getState().pasteNodes('s');
    expect(useCanvasStore.getState().getNodes('s').filter((n) => n.locked)).toHaveLength(1);
  });
});
