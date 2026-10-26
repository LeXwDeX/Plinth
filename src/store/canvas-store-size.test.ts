import { beforeEach, describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { canvasArtboard } from '@/lib/canvas/artboard';
import { RESIZE_ANCHORS } from '@/lib/canvas/canvas-resize';

function node(id: string, x = 0): CanvasNode {
  return { id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x, y: 0, width: 10, height: 10, zIndex: 1 };
}

const size = (s = 's') => useCanvasStore.getState().canvasSizeBySession[s];
const dirty = (s = 's') => useCanvasStore.getState()._canvasSizeDirtyBySession[s];

describe('canvas pixel size in the store (#246)', () => {
  beforeEach(() => {
    configureCanvasFrontend(
      (defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } })
        .application.canvas.frontend,
    );
    useCanvasStore.setState({
      nodesBySession: {}, selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {},
      _lastUndoPushTime: {}, _coalesceEntry: {}, _savedHashBySession: {},
      canvasSizeBySession: {}, _canvasSizeDirtyBySession: {},
    });
  });

  it('loads the server size clean, and a legacy canvas as unknown', () => {
    const store = useCanvasStore.getState();
    store.loadCanvasSize('s', { width: 2048, height: 2048 });
    expect(size()).toEqual({ width: 2048, height: 2048 });
    expect(dirty()).toBe(false);
    store.loadCanvasSize('s', null);
    expect(size()).toBeUndefined();
  });

  it('initialises or migrates once without an undo step and marks the size for saving', () => {
    useCanvasStore.getState().loadNodesFromDB('s', [node('a')]);
    useCanvasStore.getState().addNode('s', node('b'));
    useCanvasStore.getState().initializeCanvas('s', [node('a', 100)], { width: 1536, height: 1024 });
    const state = useCanvasStore.getState();
    expect(state.nodesBySession.s?.map((n) => n.x)).toEqual([100]);
    expect(state._undoStackBySession.s).toEqual([]);
    expect(size()).toEqual({ width: 1536, height: 1024 });
    expect(dirty()).toBe(true);
  });

  it('resizes as one undoable step that keeps the nodes as they are', () => {
    const store = useCanvasStore.getState();
    store.loadNodesFromDB('s', [node('a', 5)]);
    store.loadCanvasSize('s', { width: 1024, height: 1024 });
    store.resizeCanvas('s', { width: 1024, height: 1024 }, { anchor: 'center', center: { x: 600, y: 400 } });
    expect(useCanvasStore.getState()._undoStackBySession.s).toEqual([]);
    store.resizeCanvas('s', { width: 2048, height: 2048 }, { anchor: 'center', center: { x: 600, y: 400 } });
    expect(size()).toEqual({ width: 2048, height: 2048 });
    expect(dirty()).toBe(true);
    expect(useCanvasStore.getState().nodesBySession.s?.[0].x).toBe(5);
    expect(useCanvasStore.getState()._undoStackBySession.s?.at(-1)?.descriptionKey).toBe('history.canvas_size');

    // An edit after the resize, then undo both and redo both.
    useCanvasStore.getState().addNode('s', node('b'));
    useCanvasStore.getState().undo('s');
    expect(size()).toEqual({ width: 2048, height: 2048 });
    expect(useCanvasStore.getState().nodesBySession.s?.length).toBe(1);
    useCanvasStore.getState().undo('s');
    expect(size()).toEqual({ width: 1024, height: 1024 });
    useCanvasStore.getState().redo('s');
    expect(size()).toEqual({ width: 2048, height: 2048 });
    useCanvasStore.getState().redo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.length).toBe(2);
    expect(size()).toEqual({ width: 2048, height: 2048 });
  });

  it('jumps through the history panel with the size of each step', () => {
    const store = useCanvasStore.getState();
    store.loadNodesFromDB('s', []);
    store.loadCanvasSize('s', { width: 1024, height: 1024 });
    store.resizeCanvas('s', { width: 2048, height: 1152 }, { anchor: 'center', center: { x: 600, y: 400 } });
    useCanvasStore.getState().addNode('s', node('a'));
    useCanvasStore.getState().resizeCanvas('s', { width: 3840, height: 2160 }, { anchor: 'center', center: { x: 600, y: 400 } });
    useCanvasStore.getState().goToHistory('s', 0);
    expect(size()).toEqual({ width: 1024, height: 1024 });
    expect(useCanvasStore.getState().nodesBySession.s).toEqual([]);
    useCanvasStore.getState().goToHistory('s', 2);
    expect(size()).toEqual({ width: 2048, height: 1152 });
    expect(useCanvasStore.getState().nodesBySession.s?.length).toBe(1);
    useCanvasStore.getState().goToHistory('s', 3);
    expect(size()).toEqual({ width: 3840, height: 2160 });
  });

  it('clears the save mark only when the saved size is still current', () => {
    const store = useCanvasStore.getState();
    store.loadCanvasSize('s', { width: 1024, height: 1024 });
    store.resizeCanvas('s', { width: 2048, height: 2048 }, { anchor: 'center', center: { x: 600, y: 400 } });
    useCanvasStore.getState()._markCanvasSizeSaved('s', { width: 1024, height: 1024 });
    expect(dirty()).toBe(true);
    useCanvasStore.getState()._markCanvasSizeSaved('s', { width: 2048, height: 2048 });
    expect(dirty()).toBe(false);
  });

  it('keeps content at the anchor, reports what a shrink cuts, and undoes both size and positions (#246 phase 2)', () => {
    const center = { x: 600, y: 400 };
    const store = useCanvasStore.getState();
    // Canvas 1000x1000 around (600, 400): from (100,-100) to (1100, 900).
    store.loadNodesFromDB('s', [node('a', 150)]);
    store.loadCanvasSize('s', { width: 1000, height: 1000 });
    const grown = useCanvasStore.getState().resizeCanvas('s', { width: 2000, height: 1000 }, { anchor: 'left', center });
    expect(grown).toEqual([]);
    // Anchored left: the layer keeps its 50px distance to the left edge, which moved from 100 to -400.
    expect(useCanvasStore.getState().nodesBySession.s?.[0]).toMatchObject({ x: -350, width: 10 });
    const shrunk = useCanvasStore.getState().resizeCanvas('s', { width: 500, height: 1000 }, { anchor: 'right', center });
    // Anchored right, 2000 -> 500: the right edge moves 750 to the left and content moves with it, so the layer
    // near the old left edge ends up outside the new canvas: kept on the board and reported.
    expect(shrunk).toEqual(['a']);
    expect(useCanvasStore.getState().nodesBySession.s?.[0].x).toBe(-350 - 750);
    useCanvasStore.getState().undo('s');
    expect(size()).toEqual({ width: 2000, height: 1000 });
    expect(useCanvasStore.getState().nodesBySession.s?.[0].x).toBe(-350);
    useCanvasStore.getState().undo('s');
    expect(size()).toEqual({ width: 1000, height: 1000 });
    expect(useCanvasStore.getState().nodesBySession.s?.[0].x).toBe(150);
  });

  it('painting the blank layer is one undo step: undo makes it blank again, redo paints it again', () => {
    const blank: CanvasNode = { id: 'layer', type: 'image', imageUrl: '', taskId: 'upload', x: 0, y: 0, width: 100, height: 100, zIndex: 0, blank: true, name: '画布图层' };
    useCanvasStore.getState().loadNodesFromDB('s', [blank]);
    // What the brush commit does: one history step, then the uploaded picture lands on the layer.
    useCanvasStore.getState()._pushUndo('s', 'history.brush');
    useCanvasStore.getState().updateNode('s', 'layer', { imageUrl: '/api/v1/assets/painted', crop: undefined }, { recordHistory: false, ignoreLock: true });
    expect(useCanvasStore.getState().nodesBySession.s?.[0]).toMatchObject({ imageUrl: '/api/v1/assets/painted', blank: undefined });
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.[0]).toMatchObject({ imageUrl: '', blank: true, name: '画布图层' });
    useCanvasStore.getState().redo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.[0]).toMatchObject({ imageUrl: '/api/v1/assets/painted' });
    expect(useCanvasStore.getState().nodesBySession.s?.[0].blank).toBeUndefined();
  });

  it('adds a canvas layer below every other layer as one undo step (#246)', () => {
    const picture: CanvasNode = { id: 'pic', type: 'image', imageUrl: '/api/v1/assets/p', taskId: 't', x: 0, y: 0, width: 10, height: 10, zIndex: 3 };
    useCanvasStore.getState().loadNodesFromDB('s', [picture, node('shape')]);
    useCanvasStore.getState().addCanvasLayer('s', { id: 'layer', type: 'image', imageUrl: '', taskId: 'upload', x: -424, y: -624, width: 2048, height: 2048, blank: true });
    const nodes = useCanvasStore.getState().nodesBySession.s ?? [];
    const layer = nodes.find((entry) => entry.id === 'layer');
    expect(layer?.zIndex).toBeLessThan(Math.min(...nodes.filter((entry) => entry.id !== 'layer').map((entry) => entry.zIndex)));
    expect(useCanvasStore.getState()._undoStackBySession.s).toHaveLength(1);
    useCanvasStore.getState().undo('s');
    expect(useCanvasStore.getState().nodesBySession.s?.map((entry) => entry.id)).toEqual(['pic', 'shape']);
  });

  it('turns an unpainted initial layer into an ordinary picture layer when a picture lands on it', () => {
    const blank: CanvasNode = { id: 'layer', type: 'image', imageUrl: '', taskId: 'upload', x: 0, y: 0, width: 100, height: 100, zIndex: 0, blank: true, fillColor: '#ffffff' };
    useCanvasStore.getState().loadNodesFromDB('s', [blank]);
    useCanvasStore.getState().updateNode('s', 'layer', { x: 5 });
    expect(useCanvasStore.getState().nodesBySession.s?.[0]).toMatchObject({ blank: true, x: 5 });
    useCanvasStore.getState().updateNode('s', 'layer', { imageUrl: '/api/v1/assets/painted' }, { recordHistory: false });
    const painted = useCanvasStore.getState().nodesBySession.s?.[0];
    expect(painted).toMatchObject({ imageUrl: '/api/v1/assets/painted' });
    expect(painted?.blank).toBeUndefined();
    expect(painted?.fillColor).toBeUndefined();
  });

  /**
   * #311 (added coverage of existing behaviour, not test-first): the board's select tool hits a picture by
   * its Konva id `node-<id>`, skips locked layers (listening off) and never hits an unpainted canvas layer.
   * A resize in either direction, at every anchor, must leave those inputs as they were: the same ids, the
   * selection, the lock/visibility rules, pixel sizes, and the unpainted layer blank, covering the new canvas
   * and below every picture. Only positions move, by the anchor offset.
   */
  it('keeps every selection input of the board through anchored shrinks and grows (#311)', () => {
    const center = { x: 600, y: 400 };
    const start = { width: 2048, height: 2048 };
    const frame = canvasArtboard({ center_x: center.x, center_y: center.y }, start);
    const picture = (id: string, zIndex: number, extra: Partial<CanvasNode> = {}): CanvasNode => (
      { id, type: 'image', imageUrl: `/api/v1/assets/${id}`, taskId: 't', x: 344, y: 144, width: 512, height: 512, zIndex, ...extra });
    const nodes: CanvasNode[] = [
      { id: 'layer', type: 'image', imageUrl: '', taskId: 'upload', ...frame, zIndex: 0, blank: true },
      picture('pic', 1),
      picture('big', 2, { x: -424, y: -400, width: 2048, height: 1152 }),
      picture('locked', 3, { locked: true }),
      picture('hidden', 4, { visible: false }),
    ];
    const identity = (list: readonly CanvasNode[]) => list.map(({ x: _x, y: _y, ...rest }) => (rest.blank ? { id: rest.id, blank: true, zIndex: rest.zIndex } : rest));
    for (const anchor of RESIZE_ANCHORS) {
      for (const to of [{ width: 1024, height: 1024 }, { width: 3840, height: 2160 }, { width: 1024, height: 1536 }]) {
        useCanvasStore.setState({ selectedNodeIds: [] });
        useCanvasStore.getState().loadNodesFromDB('s', nodes.map((entry) => ({ ...entry })));
        useCanvasStore.getState().loadCanvasSize('s', start);
        useCanvasStore.getState().selectNodes(['pic']);
        useCanvasStore.getState().resizeCanvas('s', to, { anchor, center });

        const state = useCanvasStore.getState();
        const after = state.nodesBySession.s ?? [];
        expect(size()).toEqual(to);
        expect(state.selectedNodeIds).toEqual(['pic']);
        expect(identity(after)).toEqual(identity(nodes));
        // Every picture moved by the same anchor offset, so none slides under or out from under another.
        const moves = after.filter((entry) => !entry.blank).map((entry) => {
          const before = nodes.find((original) => original.id === entry.id)!;
          return `${entry.x - before.x},${entry.y - before.y}`;
        });
        expect(new Set(moves).size).toBe(1);
        const layer = after.find((entry) => entry.id === 'layer')!;
        expect(layer).toMatchObject({ blank: true, ...canvasArtboard({ center_x: center.x, center_y: center.y }, to) });
        expect(layer.zIndex).toBeLessThan(Math.min(...after.filter((entry) => entry.id !== 'layer').map((entry) => entry.zIndex)));

        // The picture is still the one a click selects, and the step undoes to the same selectable layers.
        useCanvasStore.getState().clearSelection();
        useCanvasStore.getState().selectNodes(['pic']);
        expect(useCanvasStore.getState().selectedNodeIds).toEqual(['pic']);
        useCanvasStore.getState().undo('s');
        expect(size()).toEqual(start);
        expect(useCanvasStore.getState().nodesBySession.s).toEqual(nodes);
      }
    }
  });
});
