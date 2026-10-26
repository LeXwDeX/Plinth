import { beforeEach, describe, expect, it } from 'vitest';

import { editableAt, rectangleRegion } from '@/lib/canvas/inpaint-export';
import { maskStrokeCount, objectKey, sameMaskDraft, sameObject, useMaskStore, type MaskDraft, type MaskStroke } from './mask-store';
import { useCanvasStore } from './canvas-store';
import { useWorkspaceStore } from './workspace-store';
import type { CurrentObject } from './workspace-store';

const stroke = (x: number, size = 12): MaskStroke => ({ points: [x, x, x + 5, x + 5], size });
const draft = (strokes: MaskStroke[], sessionId = 's1'): MaskDraft => ({ sessionId, strokes, redo: [], current: null });
const object = (taskId: string, extra: Partial<CurrentObject> = {}): CurrentObject => ({ taskId, kind: 'image', url: `/a/${taskId}`, sessionId: 's1', ...extra });
const paint = (x: number, size: number, sessionId = 's1') => {
  const mask = useMaskStore.getState();
  mask.beginStroke(sessionId, { x, y: x }, size);
  mask.extendStroke(sessionId, { x: x + 5, y: x + 5 });
  mask.endStroke(sessionId);
};
const node = (id: string, taskId: string) => ({
  id, type: 'image' as const, imageUrl: `/a/${taskId}`, taskId, x: 0, y: 0, width: 100, height: 100,
});

describe('object identity helpers', () => {
  it('compares objects by task, picture and asset', () => {
    expect(sameObject(object('t1'), object('t1'))).toBe(true);
    expect(sameObject(object('t1'), object('t1', { assetId: 'x' }))).toBe(false);
    expect(sameObject(null, null)).toBe(true);
    expect(sameObject(object('t1'), null)).toBe(false);
    expect(objectKey(object('t1'))).toContain('t1');
  });

  it('counts only the strokes of the session in view', () => {
    expect(maskStrokeCount(draft([stroke(1), stroke(3)]), 's1')).toBe(2);
    expect(maskStrokeCount(draft([stroke(1)]), 's2')).toBe(0);
    expect(maskStrokeCount(null, 's1')).toBe(0);
    expect(maskStrokeCount(draft([stroke(1)]), null)).toBe(0);
  });
});

describe('mask store', () => {
  beforeEach(() => useMaskStore.setState({ draft: null, question: null }));

  it('updates the draft by value or by function and clears the question with it', () => {
    useMaskStore.getState().setDraft(draft([]));
    useMaskStore.getState().setDraft((current) => (current ? { ...current, strokes: [...current.strokes, stroke(1)] } : current));
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
    useMaskStore.getState().ask({ kind: 'exit' });
    expect(useMaskStore.getState().question).toEqual({ kind: 'exit' });
    useMaskStore.getState().clearDraft();
    expect(useMaskStore.getState()).toMatchObject({ draft: null, question: null });
  });

  it('keeps the draft when the question is dismissed', () => {
    useMaskStore.getState().setDraft(draft([stroke(1)]));
    useMaskStore.getState().ask({ kind: 'exit' });
    useMaskStore.getState().dismissQuestion();
    expect(useMaskStore.getState().question).toBeNull();
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
  });

  // Bug 4: undo walks one stroke at a time.
  it('undoes and redoes one stroke per step, and a new stroke clears the redo stack', () => {
    paint(10, 12);
    paint(30, 12);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
    expect(useMaskStore.getState().redoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(2);
    useMaskStore.getState().undoStroke('s1');
    paint(50, 12);
    expect(useMaskStore.getState().redoStroke('s1')).toBe(false);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(false);
  });

  it('undoing a stroke leaves the canvas history, and so the inserted picture, alone', () => {
    useCanvasStore.setState({ nodesBySession: {}, _undoStackBySession: {}, _redoStackBySession: {}, selectedNodeIds: [] });
    useCanvasStore.getState().addNode('s1', node('img', 't1'));
    paint(10, 12);
    useMaskStore.getState().undoStroke('s1');
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(0);
    expect(useCanvasStore.getState().nodesBySession.s1.map((n) => n.id)).toEqual(['img']);
    expect(useCanvasStore.getState().canUndo('s1')).toBe(true);
  });

  // Bug 6: each stroke keeps its own brush size.
  it('stores the brush size on every stroke when it is drawn', () => {
    paint(10, 12);
    paint(30, 48);
    paint(50, 6);
    expect(useMaskStore.getState().draft?.strokes.map((s) => s.size)).toEqual([12, 48, 6]);
    // Changing the brush afterwards does not touch the strokes already painted.
    useCanvasStore.getState().setBrushSize(99);
    expect(useMaskStore.getState().draft?.strokes.map((s) => s.size)).toEqual([12, 48, 6]);
  });

  // Bug 7: one mask for the whole canvas; nothing resets it.
  it('keeps every stroke when painting continues anywhere, and when the stage object changes', () => {
    useWorkspaceStore.setState({ currentObject: null, pendingResult: null, pendingPreviewTaskId: null });
    useCanvasStore.setState({ activeTool: 'mask' });
    useWorkspaceStore.getState().setCurrentObject(object('t1'));
    paint(10, 12);
    useWorkspaceStore.getState().setCurrentObject(object('t2'));
    paint(300, 20);
    useWorkspaceStore.getState().setCurrentObject(object('t1'));
    paint(600, 8);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(3);
    expect(useMaskStore.getState().draft?.strokes[0].points[0]).toBe(10);
    expect(useCanvasStore.getState().activeTool).toBe('mask');
    useCanvasStore.setState({ activeTool: 'select' });
  });

  it('puts the mask tool down, keeping the mask, when a model takes the stage', () => {
    useCanvasStore.setState({ activeTool: 'mask' });
    paint(10, 12);
    useWorkspaceStore.getState().setCurrentObject(object('m1', { kind: 'model' }));
    expect(useCanvasStore.getState().activeTool).toBe('select');
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
  });

  it('ignores strokes of another session', () => {
    paint(10, 12);
    useMaskStore.getState().extendStroke('s2', { x: 1, y: 1 });
    expect(useMaskStore.getState().undoStroke('s2')).toBe(false);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
  });
});

// Undoing the insert is canvas history only: the mask is not touched, and putting the picture back is one undo step.
describe('canvas object put-back', () => {
  it('restores the removed picture at its old position as a single undo step', () => {
    useCanvasStore.setState({ nodesBySession: {}, _undoStackBySession: {}, _redoStackBySession: {}, selectedNodeIds: [] });
    useCanvasStore.getState().addNode('s1', { ...node('img', 't1'), x: 40, y: 60 });
    const removed = useCanvasStore.getState().nodesBySession.s1[0];
    useCanvasStore.getState().undo('s1');
    expect(useCanvasStore.getState().nodesBySession.s1).toHaveLength(0);
    const { zIndex: _z, ...restored } = removed;
    useCanvasStore.getState().addNode('s1', restored);
    expect(useCanvasStore.getState().nodesBySession.s1[0]).toMatchObject({ id: 'img', x: 40, y: 60 });
    const undoDepth = useCanvasStore.getState()._undoStackBySession.s1.length;
    useCanvasStore.getState().undo('s1');
    expect(useCanvasStore.getState().nodesBySession.s1).toHaveLength(0);
    expect(useCanvasStore.getState()._undoStackBySession.s1.length).toBe(undoDepth - 1);
  });
});

describe('mask across sessions', () => {
  beforeEach(() => useMaskStore.setState({ draft: null, parked: {}, question: null }));

  it('parks a session mask when another session is painted on, and brings it back untouched', () => {
    paint(10, 12, 's1');
    paint(50, 8, 's2');
    expect(useMaskStore.getState().draft?.sessionId).toBe('s2');
    expect(useMaskStore.getState().parked.s1.strokes).toHaveLength(1);
    useMaskStore.getState().activate('s1');
    expect(useMaskStore.getState().draft?.strokes.map((s) => s.points[0])).toEqual([10]);
    expect(useMaskStore.getState().parked.s2.strokes).toHaveLength(1);
    // A session that never had a mask simply has none in view.
    useMaskStore.getState().activate('s3');
    expect(useMaskStore.getState().draft).toBeNull();
    expect(maskStrokeCount(useMaskStore.getState().draft, 's3')).toBe(0);
  });
});

describe('mask when the dashed frame changes', () => {
  const frame = { x: 344, y: 144, width: 512, height: 512 };
  const taller = { x: 344, y: 229.5, width: 512, height: 341 };
  beforeEach(() => useMaskStore.setState({ draft: null, parked: {}, question: null }));

  const paintIn = (points: number[], size = 20) => {
    const mask = useMaskStore.getState();
    mask.beginStroke('s1', { x: points[0], y: points[1] }, size, frame);
    for (let index = 2; index < points.length; index += 2) mask.extendStroke('s1', { x: points[index], y: points[index + 1] });
    mask.endStroke('s1');
  };

  /** Where a stroke lies on the canvas (world coordinates): what the user sees, whatever frame it is kept in. */
  const world = (points: number[] | undefined) => {
    const at = useMaskStore.getState().draft?.frame ?? { x: 0, y: 0 };
    return points?.map((value, index) => value + (index % 2 === 0 ? at.x : at.y));
  };

  it('moves strokes with the picture; the mask tool undo never shifts them off it (the frame stays)', () => {
    paintIn([100, 200, 150, 260]);
    expect(world(useMaskStore.getState().draft?.strokes[0].points)).toEqual([444, 344, 494, 404]);
    expect(useMaskStore.getState().remapFrame('s1', taller)).toBe('moved');
    expect(useMaskStore.getState().draft?.strokes[0].points).toEqual([100, 114.5, 150, 174.5]);
    expect(useMaskStore.getState().remapFrame('s1', taller)).toBe('none');
    // The frame change is not the mask's to undo: Ctrl+Z in the mask tool takes back the stroke itself,
    // and a redo puts it back where it was painted, in the frame that is there now.
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toEqual([]);
    expect(useMaskStore.getState().redoStroke('s1')).toBe(true);
    expect(world(useMaskStore.getState().draft?.strokes[0].points)).toEqual([444, 344, 494, 404]);
    // Undoing the frame change elsewhere (the canvas history) brings the frame back, and the mask follows it.
    expect(useMaskStore.getState().remapFrame('s1', frame)).toBe('moved');
    expect(useMaskStore.getState().draft?.strokes[0].points).toEqual([100, 200, 150, 260]);
  });

  it('keeps every step of the mask history on the picture across frame changes', () => {
    paintIn([100, 200, 150, 260]);
    paintIn([300, 300, 320, 320]);
    expect(useMaskStore.getState().clearSelection('s1')).toBe(true);
    // An empty mask follows the frame too, and so must the history it can still undo / redo into.
    expect(useMaskStore.getState().remapFrame('s1', taller)).toBe('none');
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(world(useMaskStore.getState().draft?.strokes[0].points)).toEqual([444, 344, 494, 404]);
    expect(world(useMaskStore.getState().draft?.strokes[1].points)).toEqual([644, 444, 664, 464]);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().remapFrame('s1', { ...taller, x: 300 })).toBe('moved');
    expect(useMaskStore.getState().redoStroke('s1')).toBe(true);
    expect(world(useMaskStore.getState().draft?.strokes[1].points)).toEqual([644, 444, 664, 464]);
  });

  it('clears a mask that no longer reaches the frame, and one undo brings it back where it was', () => {
    paintIn([100, 480, 150, 500], 10);
    paintIn([10, 20, 30, 40], 10);
    const away = { x: 344, y: 144 - 600, width: 512, height: 341 };
    expect(useMaskStore.getState().remapFrame('s1', away)).toBe('cleared');
    expect(useMaskStore.getState().draft?.strokes).toEqual([]);
    expect(maskStrokeCount(useMaskStore.getState().draft, 's1')).toBe(0);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(2);
    expect(world(useMaskStore.getState().draft?.strokes[0].points)).toEqual([444, 624, 494, 644]);
    // Bringing the frame back shows the mask on the picture again.
    expect(useMaskStore.getState().remapFrame('s1', frame)).toBe('moved');
    expect(useMaskStore.getState().draft?.strokes[0].points).toEqual([100, 480, 150, 500]);
    // Older strokes undo one by one afterwards.
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
  });

  it('adopts the frame of a mask that had none, and ignores sessions without a mask', () => {
    useMaskStore.getState().setDraft(draft([stroke(1)]));
    expect(useMaskStore.getState().remapFrame('s1', frame)).toBe('none');
    expect(useMaskStore.getState().draft?.frame).toEqual(frame);
    expect(useMaskStore.getState().remapFrame('other', frame)).toBe('none');
  });
});

describe('canvas-level selection (marquee, lasso, invert, quick-mask erase)', () => {
  const frame = { x: 0, y: 0, width: 100, height: 100 };
  const rect = (x0: number, y0: number, x1: number, y1: number) => rectangleRegion({ x: x0, y: y0 }, { x: x1, y: y1 });
  beforeEach(() => useMaskStore.setState({ draft: null, question: null, erasing: false, submitRequest: 0, parked: {} }));
  const editable = (x: number, y: number) => editableAt(useMaskStore.getState().draft?.strokes ?? [], { x, y });

  it('a marquee makes the mask exactly that rectangle, and a second one replaces it', () => {
    useMaskStore.getState().commitRegion('s1', rect(10, 10, 40, 30), 'replace', frame);
    expect([editable(20, 20), editable(50, 20), editable(5, 5)]).toEqual([true, false, false]);
    useMaskStore.getState().commitRegion('s1', rect(60, 60, 90, 90), 'replace', frame);
    expect([editable(20, 20), editable(70, 70)]).toEqual([false, true]);
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
  });

  it('Shift adds and Alt subtracts; subtracting from nothing does nothing', () => {
    useMaskStore.getState().commitRegion('s1', rect(0, 0, 10, 10), 'subtract', frame);
    expect(useMaskStore.getState().draft).toBeNull();
    useMaskStore.getState().commitRegion('s1', rect(10, 10, 40, 30), 'replace', frame);
    useMaskStore.getState().commitRegion('s1', rect(60, 60, 90, 90), 'add', frame);
    expect([editable(20, 20), editable(70, 70)]).toEqual([true, true]);
    useMaskStore.getState().commitRegion('s1', rect(15, 15, 25, 25), 'subtract', frame);
    expect([editable(20, 20), editable(30, 20), editable(70, 70)]).toEqual([false, true, true]);
  });

  it('brush strokes after a marquee merge into the one mask', () => {
    useMaskStore.getState().commitRegion('s1', rect(10, 10, 40, 30), 'replace', frame);
    useMaskStore.getState().beginStroke('s1', { x: 70, y: 70 }, 10, frame);
    useMaskStore.getState().extendStroke('s1', { x: 80, y: 70 });
    useMaskStore.getState().endStroke('s1');
    expect(useMaskStore.getState().draft?.strokes).toHaveLength(2);
    expect([editable(20, 20), editable(75, 70), editable(75, 90)]).toEqual([true, true, false]);
  });

  it('an erase stroke gives the area back to the picture', () => {
    useMaskStore.getState().commitRegion('s1', rect(0, 0, 100, 100), 'replace', frame);
    useMaskStore.getState().beginStroke('s1', { x: 50, y: 50 }, 20, frame, true);
    useMaskStore.getState().extendStroke('s1', { x: 60, y: 50 });
    useMaskStore.getState().endStroke('s1');
    expect(useMaskStore.getState().draft?.strokes[1]).toMatchObject({ erase: true });
    expect([editable(55, 50), editable(5, 5)]).toEqual([false, true]);
  });

  it('invert swaps editable and kept, and is a step that undo takes back', () => {
    useMaskStore.getState().commitRegion('s1', rect(10, 10, 40, 30), 'replace', frame);
    useMaskStore.getState().invertSelection('s1', frame);
    expect([editable(20, 20), editable(80, 80)]).toEqual([false, true]);
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect([editable(20, 20), editable(80, 80)]).toEqual([true, false]);
  });

  it('Ctrl+D drops the selection as one undoable step', () => {
    expect(useMaskStore.getState().clearSelection('s1')).toBe(false);
    useMaskStore.getState().commitRegion('s1', rect(10, 10, 40, 30), 'replace', frame);
    expect(useMaskStore.getState().clearSelection('s1')).toBe(true);
    expect(useMaskStore.getState().draft?.strokes).toEqual([]);
    useMaskStore.getState().undoStroke('s1');
    expect(editable(20, 20)).toBe(true);
    useMaskStore.getState().redoStroke('s1');
    expect(editable(20, 20)).toBe(false);
  });

  it('toggles painting and erasing, and counts a submit request', () => {
    useMaskStore.getState().toggleErasing();
    expect(useMaskStore.getState().erasing).toBe(true);
    useMaskStore.getState().setErasing(false);
    useMaskStore.getState().requestSubmit();
    expect(useMaskStore.getState()).toMatchObject({ erasing: false, submitRequest: 1 });
  });
});

describe('mask submission lifecycle (#309)', () => {
  beforeEach(() => useMaskStore.setState({ draft: null, question: null }));

  it('a remap, an undo or a new stroke replaces the submitted draft object', () => {
    const frame = { x: 0, y: 0, width: 100, height: 100 };
    const submitted = { ...draft([stroke(10)]), frame };
    useMaskStore.setState({ draft: submitted });
    expect(sameMaskDraft(useMaskStore.getState().draft, submitted)).toBe(true);

    useMaskStore.getState().remapFrame('s1', { x: 5, y: 5, width: 100, height: 100 });
    const remapped = useMaskStore.getState().draft;
    expect(remapped).not.toBe(submitted);
    expect(sameMaskDraft(remapped, submitted)).toBe(false);

    const beforeUndo = remapped;
    expect(useMaskStore.getState().undoStroke('s1')).toBe(true);
    expect(sameMaskDraft(useMaskStore.getState().draft, beforeUndo)).toBe(false);

    const beforeStroke = useMaskStore.getState().draft;
    useMaskStore.getState().beginStroke('s1', { x: 1, y: 2 }, 8);
    expect(sameMaskDraft(useMaskStore.getState().draft, beforeStroke)).toBe(false);
  });

  it('clears only the exact draft that was submitted, never later strokes', () => {
    const submitted = draft([stroke(1)]);
    expect(sameMaskDraft(submitted, submitted)).toBe(true);
    expect(sameMaskDraft({ ...submitted, strokes: [...submitted.strokes] }, submitted)).toBe(false);
    expect(sameMaskDraft({ ...submitted, sessionId: 's2' }, submitted)).toBe(false);
    expect(sameMaskDraft({ ...submitted, current: stroke(2) }, submitted)).toBe(false);
    expect(sameMaskDraft({ ...submitted, frame: { x: 0, y: 0, width: 10, height: 10 } }, submitted)).toBe(false);
    expect(sameMaskDraft({ ...submitted, steps: [{ kind: 'stroke' }] }, submitted)).toBe(false);
    expect(sameMaskDraft(null, submitted)).toBe(false);
    expect(sameMaskDraft(submitted, null)).toBe(false);
  });
});
