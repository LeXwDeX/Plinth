import { beforeEach, describe, expect, it } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { configureCanvasFrontend, historyWasTruncated, useCanvasStore, type CanvasNode } from '@/store/canvas-store';

const frontend = (defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } }).application.canvas.frontend;

function node(id: string, extra: Partial<CanvasNode> = {}): Omit<CanvasNode, 'zIndex'> {
  return { id, type: 'shape', shapeType: 'rectangle', imageUrl: '', taskId: '', x: 0, y: 0, width: 10, height: 10, ...extra };
}
const state = () => useCanvasStore.getState();
const nodesOf = () => state().nodesBySession.s ?? [];
const ascending = () => [...nodesOf()].sort((a, b) => a.zIndex - b.zIndex).map((n) => n.id);

describe('canvas store, Photoshop habits', () => {
  beforeEach(() => {
    configureCanvasFrontend(frontend);
    useCanvasStore.setState({
      nodesBySession: {}, selectedNodeIds: [], _undoStackBySession: {}, _redoStackBySession: {},
      _lastUndoPushTime: {}, _coalesceEntry: {}, backgroundColor: '', brushSize: 12,
      lastStyle: { strokeWidth: 2, strokeColor: '#111111', fillColor: 'transparent' },
    });
    configureCanvasFrontend(frontend);
  });

  describe('history panel (goToHistory)', () => {
    function fiveSteps() {
      for (const id of ['a', 'b', 'c', 'd', 'e']) state().addNode('s', node(id, { x: id.charCodeAt(0) }));
    }

    it('going to step 2 shows the canvas as it was after the second step, with later steps redoable', () => {
      fiveSteps();
      state().goToHistory('s', 2);
      expect(ascending()).toEqual(['a', 'b']);
      expect(state()._undoStackBySession.s).toHaveLength(2);
      expect(state()._redoStackBySession.s).toHaveLength(3);
      state().goToHistory('s', 5);
      expect(ascending()).toEqual(['a', 'b', 'c', 'd', 'e']);
    });

    it('a new edit after going back drops steps 3 to 5', () => {
      fiveSteps();
      state().goToHistory('s', 2);
      state().addNode('s', node('z'));
      expect(state()._redoStackBySession.s).toEqual([]);
      expect(state()._undoStackBySession.s).toHaveLength(3);
      expect(ascending()).toEqual(['a', 'b', 'z']);
    });

    it('position 0 is the opening state and out-of-range positions are clamped', () => {
      fiveSteps();
      state().goToHistory('s', 0);
      expect(nodesOf()).toEqual([]);
      state().goToHistory('s', 99);
      expect(nodesOf()).toHaveLength(5);
      state().goToHistory('s', -4);
      expect(nodesOf()).toEqual([]);
    });

    it('keeps each step time through undo and redo', () => {
      state().addNode('s', node('a'));
      const stamp = state()._undoStackBySession.s[0].timestamp;
      state().undo('s');
      expect(state()._redoStackBySession.s[0].timestamp).toBe(stamp);
      state().redo('s');
      expect(state()._undoStackBySession.s[0].timestamp).toBe(stamp);
    });
  });

  describe('Ctrl+J (duplicateSelected)', () => {
    it('puts the copy directly above the original, selected, in one undo step', () => {
      state().addNode('s', node('a', { x: 5, y: 6 }));
      state().addNode('s', node('b'));
      state().selectNodes(['a']);
      const created = state().duplicateSelected('s');
      expect(created).toHaveLength(1);
      expect(ascending()).toEqual(['a', created[0], 'b']);
      const copy = nodesOf().find((n) => n.id === created[0]);
      expect(copy).toMatchObject({ x: 5, y: 6 });
      expect(state().selectedNodeIds).toEqual(created);
      state().undo('s');
      expect(ascending()).toEqual(['a', 'b']);
    });

    it('a duplicated group becomes a group of its own and an unlocked copy', () => {
      state().addNode('s', node('a', { groupId: 'g', locked: true }));
      state().addNode('s', node('b', { groupId: 'g' }));
      state().selectNodes(['a', 'b']);
      const created = state().duplicateSelected('s');
      const copies = nodesOf().filter((n) => created.includes(n.id));
      expect(new Set(copies.map((n) => n.groupId)).size).toBe(1);
      expect(copies[0].groupId).not.toBe('g');
      expect(copies.every((n) => !n.locked)).toBe(true);
    });

    it('does nothing without a selection', () => {
      state().addNode('s', node('a'));
      expect(state().duplicateSelected('s')).toEqual([]);
      expect(nodesOf()).toHaveLength(1);
    });
  });

  describe('align and distribute', () => {
    it('left-aligns three layers in one undo step', () => {
      state().addNode('s', node('a', { x: 10 }));
      state().addNode('s', node('b', { x: 50 }));
      state().addNode('s', node('c', { x: 90 }));
      state().selectNodes(['a', 'b', 'c']);
      state().alignSelected('s', 'left');
      expect(new Set(nodesOf().map((n) => n.x))).toEqual(new Set([10]));
      expect(state()._undoStackBySession.s).toHaveLength(4);
      state().undo('s');
      expect(nodesOf().map((n) => n.x)).toEqual([10, 50, 90]);
    });

    it('one layer aligns to the frame; nothing moving records no step', () => {
      state().addNode('s', node('a', { x: 3 }));
      state().selectNodes(['a']);
      const steps = state()._undoStackBySession.s.length;
      state().alignSelected('s', 'left', { x: 3, y: 0, width: 100, height: 100 });
      expect(state()._undoStackBySession.s).toHaveLength(steps);
      state().alignSelected('s', 'right', { x: 0, y: 0, width: 100, height: 100 });
      expect(nodesOf()[0].x).toBe(90);
    });

    it('distributes evenly', () => {
      state().addNode('s', node('a', { x: 0 }));
      state().addNode('s', node('b', { x: 15 }));
      state().addNode('s', node('c', { x: 90 }));
      state().selectNodes(['a', 'b', 'c']);
      state().distributeSelected('s', 'horizontal');
      expect(nodesOf().map((n) => n.x)).toEqual([0, 45, 90]);
    });
  });

  describe('groups as folders', () => {
    it('hides and locks a whole group as one step', () => {
      state().addNode('s', node('a', { groupId: 'g' }));
      state().addNode('s', node('b', { groupId: 'g' }));
      state().addNode('s', node('c'));
      state().toggleGroupVisibility('s', 'g');
      expect(nodesOf().map((n) => n.visible)).toEqual([false, false, undefined]);
      state().toggleGroupVisibility('s', 'g');
      expect(nodesOf().map((n) => n.visible)).toEqual([true, true, undefined]);
      state().toggleGroupLock('s', 'g');
      expect(nodesOf().map((n) => Boolean(n.locked))).toEqual([true, true, false]);
      state().toggleGroupLock('s', 'g');
      expect(nodesOf().map((n) => Boolean(n.locked))).toEqual([false, false, false]);
    });

    it('arrangeLayers applies a new order and group membership as one step', () => {
      for (const id of ['a', 'b', 'c']) state().addNode('s', node(id));
      const steps = state()._undoStackBySession.s.length;
      state().arrangeLayers('s', ['c', 'a', 'b'], { c: 'g', a: 'g' });
      expect(ascending()).toEqual(['c', 'a', 'b']);
      expect(nodesOf().find((n) => n.id === 'a')?.groupId).toBe('g');
      expect(nodesOf().find((n) => n.id === 'b')?.groupId).toBeUndefined();
      expect(state()._undoStackBySession.s).toHaveLength(steps + 1);
      state().undo('s');
      expect(ascending()).toEqual(['a', 'b', 'c']);
    });
  });

  describe('colours and brush size', () => {
    it('X swaps the foreground and background colour, D restores the Schema defaults', () => {
      expect(state().backgroundColor).toBe(frontend.style.default_background_color);
      state().setBackgroundColor('#ff8800');
      state().swapColors();
      expect(state().lastStyle.strokeColor).toBe('#ff8800');
      expect(state().backgroundColor).toBe('#111111');
      state().resetColors();
      expect(state().lastStyle.strokeColor).toBe(frontend.style.default_stroke_color);
      expect(state().backgroundColor).toBe(frontend.style.default_background_color);
    });

    it('[ and ] change the brush by 10 percent within the Schema range, and never touch the line width', () => {
      state().setBrushSize(100);
      state().stepBrushSize(1);
      expect(state().brushSize).toBe(110);
      state().stepBrushSize(-1);
      expect(state().brushSize).toBe(99);
      state().setBrushSize(5);
      state().stepBrushSize(-1);
      expect(state().brushSize).toBe(4);
      state().setBrushSize(1);
      state().stepBrushSize(-1);
      expect(state().brushSize).toBe(frontend.brush.minimum_size);
      state().setBrushSize(500);
      expect(state().brushSize).toBe(frontend.brush.maximum_size);
      expect(state().lastStyle.strokeWidth).toBe(2);
    });
  });
});

describe('updateNode on a locked layer', () => {
  it('refuses a locked layer unless the brush says its strokes were drawn before the lock', () => {
    const state = useCanvasStore.getState();
    state.addNode('lock-session', { id: 'p', type: 'image', taskId: '', imageUrl: '/old', x: 0, y: 0, width: 10, height: 10 });
    state.toggleNodeLock('lock-session', 'p');
    useCanvasStore.getState().updateNode('lock-session', 'p', { imageUrl: '/blocked' }, { recordHistory: false });
    expect(useCanvasStore.getState().getNodes('lock-session')[0].imageUrl).toBe('/old');
    useCanvasStore.getState().updateNode('lock-session', 'p', { imageUrl: '/painted' }, { recordHistory: false, ignoreLock: true });
    expect(useCanvasStore.getState().getNodes('lock-session')[0].imageUrl).toBe('/painted');
    expect(useCanvasStore.getState().getNodes('lock-session')[0].locked).toBe(true);
  });
});

describe('Schema defaults and history truncation', () => {
  it('starts the brush at the Schema default size, not a number in code', () => {
    useCanvasStore.setState({ brushSize: 0 });
    configureCanvasFrontend({ ...frontend, brush: { ...frontend.brush, default_size: 37 } });
    expect(state().brushSize).toBe(37);
    state().setBrushSize(50);
    configureCanvasFrontend(frontend);
    expect(state().brushSize).toBe(50);
  });

  it('remembers that the cap dropped the oldest steps, and forgets it when the history is replaced', () => {
    const id = 'truncated-session';
    const depth = frontend.history.maximum_depth;
    expect(historyWasTruncated(id)).toBe(false);
    for (let index = 0; index < depth; index += 1) state().addNode(id, node(`n${index}`, { x: index }));
    expect(historyWasTruncated(id)).toBe(false);
    state().addNode(id, node('over'));
    expect(historyWasTruncated(id)).toBe(true);
    state().loadNodesFromDB(id, []);
    expect(historyWasTruncated(id)).toBe(false);
  });
});
