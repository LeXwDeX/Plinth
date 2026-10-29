import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { configureCanvasBitmapLimit, configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';

const SESSION = 'limit-session';
const LIMIT = 3;
const picture = (id: string): CanvasNode => ({ id, type: 'image', imageUrl: `/${id}`, taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 });
const text = (id: string): CanvasNode => ({ id, type: 'text', text: 'x', imageUrl: '', taskId: '', x: 0, y: 0, width: 10, height: 10, zIndex: 1 });
const { zIndex: _zIndex, ...unplaced } = picture('new');
const nodes = () => useCanvasStore.getState().getNodes(SESSION);
const pictures = () => nodes().filter((node) => node.type === 'image').length;

function seed(initial: CanvasNode[]) {
  useCanvasStore.setState({
    nodesBySession: { [SESSION]: initial }, selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {},
    _lastUndoPushTime: {}, _coalesceEntry: {}, _clipboard: [], bitmapLimitHit: null,
  });
}

describe('the canvas picture limit in the store (#149)', () => {
  beforeEach(() => {
    configureCanvasFrontend((defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } }).application.canvas.frontend);
    configureCanvasBitmapLimit(LIMIT);
  });
  afterEach(() => configureCanvasBitmapLimit(Number.POSITIVE_INFINITY));

  it('takes pictures up to the limit, refuses the next one and reports it once per refusal', () => {
    seed([picture('a'), picture('b')]);
    expect(useCanvasStore.getState().addNode(SESSION, unplaced)).toBe(true);
    expect(pictures()).toBe(LIMIT);
    expect(useCanvasStore.getState().bitmapLimitHit).toBeNull();
    expect(useCanvasStore.getState().addNode(SESSION, { ...unplaced, id: 'over' })).toBe(false);
    expect(pictures()).toBe(LIMIT);
    expect(useCanvasStore.getState().bitmapLimitHit).toEqual({ sessionId: SESSION, seq: 1 });
    useCanvasStore.getState().addNode(SESSION, { ...unplaced, id: 'over-2' });
    expect(useCanvasStore.getState().bitmapLimitHit).toEqual({ sessionId: SESSION, seq: 2 });
    // A refused add leaves no undo step behind.
    expect(useCanvasStore.getState()._undoStackBySession[SESSION]?.length).toBe(1);
  });

  it('still takes text, shapes and pencil layers when the pictures are full', () => {
    seed([picture('a'), picture('b'), picture('c')]);
    expect(useCanvasStore.getState().addNode(SESSION, { ...text('t'), id: 'text-new' })).toBe(true);
    expect(useCanvasStore.getState().addNode(SESSION, { ...text('s'), id: 'shape-new', type: 'shape' })).toBe(true);
    expect(nodes()).toHaveLength(5);
    expect(useCanvasStore.getState().bitmapLimitHit).toBeNull();
  });

  it('counts hidden and locked pictures, as the server does', () => {
    seed([{ ...picture('a'), visible: false }, { ...picture('b'), locked: true }, picture('c')]);
    expect(useCanvasStore.getState().addNode(SESSION, unplaced)).toBe(false);
  });

  it('refuses a brush layer, a merge and a duplicate that would not fit', () => {
    seed([picture('a'), picture('b'), picture('c')]);
    expect(useCanvasStore.getState().addCanvasLayer(SESSION, unplaced)).toBe(false);
    expect(useCanvasStore.getState().stampNodes(SESSION, ['a'], unplaced)).toBe(false);
    expect(nodes().every((node) => node.visible !== false)).toBe(true);
    useCanvasStore.setState({ selectedNodeIds: ['a'] });
    expect(useCanvasStore.getState().duplicateSelected(SESSION)).toEqual([]);
    expect(pictures()).toBe(LIMIT);
    expect(useCanvasStore.getState().bitmapLimitHit?.seq).toBe(3);
  });

  it('duplicates what fits and refuses a group of pictures that does not', () => {
    seed([picture('a'), picture('b'), text('t')]);
    useCanvasStore.setState({ selectedNodeIds: ['t'] });
    expect(useCanvasStore.getState().duplicateSelected(SESSION)).toHaveLength(1);
    useCanvasStore.setState({ selectedNodeIds: ['a', 'b'] });
    expect(useCanvasStore.getState().duplicateSelected(SESSION)).toEqual([]);
    useCanvasStore.setState({ selectedNodeIds: ['a'] });
    expect(useCanvasStore.getState().duplicateSelected(SESSION)).toHaveLength(1);
    expect(pictures()).toBe(LIMIT);
  });

  it('refuses a paste whose pictures do not fit, and pastes text', () => {
    seed([picture('a'), picture('b'), picture('c')]);
    useCanvasStore.setState({ _clipboard: [{ ...picture('x'), zIndex: 1 }] });
    useCanvasStore.getState().pasteNodes(SESSION);
    expect(nodes()).toHaveLength(3);
    expect(useCanvasStore.getState().bitmapLimitHit?.seq).toBe(1);
    useCanvasStore.setState({ _clipboard: [{ ...text('y'), zIndex: 1 }] });
    useCanvasStore.getState().pasteNodes(SESSION);
    expect(nodes()).toHaveLength(4);
  });

  it('frees the room again when a picture is deleted', () => {
    seed([picture('a'), picture('b'), picture('c')]);
    useCanvasStore.getState().removeNode(SESSION, 'a');
    expect(useCanvasStore.getState().addNode(SESSION, unplaced)).toBe(true);
  });

  it('refuses nothing until a limit is configured', () => {
    configureCanvasBitmapLimit(Number.POSITIVE_INFINITY);
    seed(Array.from({ length: 60 }, (_, index) => picture(`p${index}`)));
    expect(useCanvasStore.getState().addNode(SESSION, unplaced)).toBe(true);
  });
});
