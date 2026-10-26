import { beforeEach, describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { boardAutosaveTrigger, conflictReloadApplies } from './board-autosave';
import { computeNodesHash } from './node-persistence';

const node: CanvasNode = { id: 'n', type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 };
const trigger = () => boardAutosaveTrigger(useCanvasStore.getState(), 's');

beforeEach(() => {
  configureCanvasFrontend(
    (defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } })
      .application.canvas.frontend,
  );
  useCanvasStore.setState({
    nodesBySession: { s: [node] }, selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {},
    _lastUndoPushTime: {}, _coalesceEntry: {}, _savedHashBySession: { s: computeNodesHash([node]) },
    canvasSizeBySession: {}, _canvasSizeDirtyBySession: {}, placedResultsBySession: {}, _placedDirtyBySession: {},
  });
  useCanvasStore.getState().loadCanvasSize('s', { width: 1024, height: 1024 });
  useCanvasStore.getState().loadPlacedResults('s', []);
});

describe('board autosave trigger (#246)', () => {
  it('changes for every new canvas size, also while an earlier size is still unsaved', () => {
    const store = useCanvasStore.getState();
    const clean = trigger();
    store.resizeCanvas('s', { width: 2048, height: 2048 }, { anchor: 'center', center: { x: 600, y: 400 } });
    const sizeA = trigger();
    expect(sizeA).not.toEqual(clean);
    // A save of size A is in flight; the person picks size B. Nodes do not change and the size stays dirty,
    // so only the size itself can schedule the save of B.
    store.resizeCanvas('s', { width: 1536, height: 1024 }, { anchor: 'center', center: { x: 600, y: 400 } });
    const sizeB = trigger();
    expect(sizeB).not.toEqual(sizeA);
    // The save of A returns: B is still pending, and the trigger still asks for its save.
    useCanvasStore.getState()._markCanvasSizeSaved('s', { width: 2048, height: 2048 });
    expect(useCanvasStore.getState()._canvasSizeDirtyBySession.s).toBe(true);
    expect(trigger()).toEqual(sizeB);
  });

  it('changes for every new placed-results registration, also while one is still unsaved', () => {
    const store = useCanvasStore.getState();
    store.setPlacedResults('s', ['task-1#a']);
    const first = trigger();
    store.setPlacedResults('s', ['task-1#a', 'task-2#b']);
    expect(trigger()).not.toEqual(first);
  });

  it('is stable while nothing a save writes changes', () => {
    const before = trigger();
    useCanvasStore.setState({ selectedNodeIds: ['n'] });
    expect(trigger()).toEqual(before);
  });
});

describe('conflict reload after a failed board save (#246)', () => {
  const request = () => {
    const state = useCanvasStore.getState();
    return {
      nodesHash: computeNodesHash(state.getNodes('s')),
      size: state.canvasSizeBySession.s,
      placed: state.placedResultsBySession.s,
    };
  };

  it('adopts the server canvas while the local canvas is still what the save sent', () => {
    useCanvasStore.getState().resizeCanvas('s', { width: 2048, height: 2048 }, { anchor: 'center', center: { x: 600, y: 400 } });
    const sent = request();
    expect(conflictReloadApplies(useCanvasStore.getState(), 's', sent)).toBe(true);
  });

  it('leaves a size picked after the save was sent for its own save', () => {
    useCanvasStore.getState().resizeCanvas('s', { width: 2048, height: 2048 }, { anchor: 'center', center: { x: 600, y: 400 } });
    const sent = request();
    useCanvasStore.getState().resizeCanvas('s', { width: 1536, height: 1024 }, { anchor: 'center', center: { x: 600, y: 400 } });
    expect(conflictReloadApplies(useCanvasStore.getState(), 's', sent)).toBe(false);
  });

  it('leaves node edits and new registrations made after the save was sent alone', () => {
    const sent = request();
    useCanvasStore.getState().setPlacedResults('s', ['task-1#a']);
    expect(conflictReloadApplies(useCanvasStore.getState(), 's', sent)).toBe(false);
    const sentAgain = request();
    useCanvasStore.setState({ nodesBySession: { s: [{ ...node, x: 5 }] } });
    expect(conflictReloadApplies(useCanvasStore.getState(), 's', sentAgain)).toBe(false);
  });
});
