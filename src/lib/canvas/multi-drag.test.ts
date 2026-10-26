import { describe, expect, it } from 'vitest';
import { dragUpdates, movedUnion, movingNodeIds, nodeBounds, snapTargetsFor, startDragSession, type DragNode } from './multi-drag';
import { snapRect } from './snapping';

const node = (id: string, x: number, y: number, extra: Partial<DragNode> = {}): DragNode => ({
  id, x, y, width: 100, height: 50, ...extra,
});

describe('multi-drag', () => {
  const nodes = [node('a', 0, 0), node('b', 300, 100), node('c', 600, 0)];

  it('moves both selected nodes by the same delta', () => {
    const session = startDragSession(nodes, ['a', 'b'], 'a')!;
    const updates = dragUpdates(session, { x: 40, y: 10 });
    expect(updates).toEqual([{ id: 'a', x: 40, y: 10 }, { id: 'b', x: 340, y: 110 }]);
  });

  it('computes from the start snapshot, not from the latest props', () => {
    const session = startDragSession(nodes, ['a', 'b'], 'a')!;
    dragUpdates(session, { x: 40, y: 10 });
    expect(dragUpdates(session, { x: 5, y: 0 })).toEqual([{ id: 'a', x: 5, y: 0 }, { id: 'b', x: 305, y: 100 }]);
  });

  it('includes group siblings of any selected node', () => {
    const grouped = [node('a', 0, 0), node('b', 300, 100, { groupId: 'g' }), node('c', 600, 0, { groupId: 'g' }), node('d', 900, 0)];
    expect([...movingNodeIds(grouped, ['a', 'b'], 'a')].sort()).toEqual(['a', 'b', 'c']);
  });

  it('leaves locked nodes in the selection in place', () => {
    const locked = [node('a', 0, 0), node('b', 300, 100, { locked: true })];
    const session = startDragSession(locked, ['a', 'b'], 'a')!;
    expect(dragUpdates(session, { x: 10, y: 10 })).toEqual([{ id: 'a', x: 10, y: 10 }]);
  });

  it('moves an unselected dragged node alone, or with its group', () => {
    expect([...movingNodeIds(nodes, ['b', 'c'], 'a')]).toEqual(['a']);
    const grouped = [node('a', 0, 0, { groupId: 'g' }), node('b', 300, 100, { groupId: 'g' }), node('c', 600, 0)];
    expect([...movingNodeIds(grouped, ['c'], 'a')].sort()).toEqual(['a', 'b']);
  });

  it('snaps the union box and shifts every node equally; moving nodes are not targets', () => {
    const session = startDragSession(nodes, ['a', 'b'], 'a')!;
    expect(session.union).toMatchObject({ x: 0, y: 0, width: 400, height: 150 });
    const targets = snapTargetsFor(nodes, session);
    expect(targets.primary).toHaveLength(1);
    // Union right edge (x+400) is 3 away from c's left edge (600) after moving by 197.
    const union = movedUnion(session, { x: 197, y: 0 });
    const snapped = snapRect(union, targets.primary, 5);
    expect(snapped.x).toBe(200);
    const updates = dragUpdates(session, { x: session.draggedStart.x + snapped.x - session.union.x, y: 0 });
    expect(updates.map((u) => u.x)).toEqual([200, 500]);
  });
});

/** One drag step as the board runs it: where the dragged node lands after snapping, and the guides drawn. */
function dragTo(
  nodes: DragNode[],
  selected: string[],
  dragged: string,
  to: { x: number; y: number },
  groupMemberTargets = false,
) {
  const session = startDragSession(nodes, selected, dragged)!;
  const targets = snapTargetsFor(nodes, session, { groupMemberTargets });
  const union = movedUnion(session, to);
  const snapped = snapRect(union, targets.primary, 5, targets.secondary);
  const position = { x: to.x + snapped.x - union.x, y: to.y + snapped.y - union.y };
  return { session, targets, position, guides: snapped.guides, updates: dragUpdates(session, position) };
}

describe('group snapping (#273)', () => {
  // Group g: a (0..100 × 0..100) and b (300..400 × 200..300); its box is 0..400 × 0..300, center 200 / 150.
  const groupA = node('a', 0, 0, { width: 100, height: 100, groupId: 'g' });
  const groupB = node('b', 300, 200, { width: 100, height: 100, groupId: 'g' });

  it('a group that is not moving is one target: its box, not its members', () => {
    const lone = node('c', 1000, 500, { width: 60, height: 60 });
    const nodes = [groupA, groupB, lone];
    const targets = snapTargetsFor(nodes, startDragSession(nodes, ['c'], 'c')!);
    expect(targets.primary).toEqual([{ x: 0, y: 0, width: 400, height: 300, rotation: 0 }]);
    expect(targets.secondary).toEqual([]);

    // c's left edge 3 away from b's left edge (300), an inner edge of the group: no snap by default.
    const inner = dragTo(nodes, ['c'], 'c', { x: 303, y: 500 });
    expect(inner.position).toEqual({ x: 303, y: 500 });
    expect(inner.guides).toEqual([]);

    // c's center (x + 30) 2 away from the group's center line (200): snaps, the guide spans the group box.
    const center = dragTo(nodes, ['c'], 'c', { x: 172, y: 500 });
    expect(center.position.x).toBe(170);
    expect(center.guides).toEqual([{ orientation: 'vertical', position: 200, from: 0, to: 560 }]);
  });

  it('a group snaps to another group as a whole box', () => {
    const other = [
      node('p', 1000, 0, { width: 50, height: 50, groupId: 'h' }),
      node('q', 1150, 250, { width: 50, height: 50, groupId: 'h' }),
    ];
    const nodes = [groupA, groupB, ...other];
    // Dragging a (its group follows): the group's right edge (400 + dx) near group h's left edge (1000).
    const result = dragTo(nodes, ['a', 'b'], 'a', { x: 596, y: 0 });
    expect(result.position).toEqual({ x: 600, y: 0 });
    expect(result.updates).toEqual([{ id: 'a', x: 600, y: 0 }, { id: 'b', x: 900, y: 200 }]);
    // h's box is 1000..1200 × 0..300; guides sit on that box, never on p or q alone.
    expect(result.guides).toContainEqual({ orientation: 'vertical', position: 1000, from: 0, to: 300 });
    expect(result.guides.some((g) => g.orientation === 'vertical' && [1025, 1050, 1150, 1175].includes(g.position))).toBe(false);
  });

  it('the Schema can keep member edges as secondary targets, used only where no unit is within reach', () => {
    const lone = node('c', 1000, 500, { width: 60, height: 60 });
    const nodes = [groupA, groupB, lone];
    expect(snapTargetsFor(nodes, startDragSession(nodes, ['c'], 'c')!, { groupMemberTargets: true }).secondary).toHaveLength(2);

    const inner = dragTo(nodes, ['c'], 'c', { x: 303, y: 500 }, true);
    expect(inner.position.x).toBe(300);
    expect(inner.guides).toEqual([{ orientation: 'vertical', position: 300, from: 200, to: 560 }]);

    // c's right edge 3 away from the group's right edge (400, also b's right edge): the unit supplies the guide.
    const both = dragTo(nodes, ['c'], 'c', { x: 343, y: 500 }, true);
    expect(both.position.x).toBe(340);
    expect(both.guides).toEqual([{ orientation: 'vertical', position: 400, from: 0, to: 560 }]);
  });

  it('a moving group is one box: the union of its members', () => {
    const lone = node('c', 700, 0, { width: 50, height: 50 });
    const nodes = [groupA, groupB, lone];
    // Pressing on a member drags its whole group even when only that member is selected.
    const session = startDragSession(nodes, ['a'], 'a')!;
    expect([...session.starts.keys()]).toEqual(['a', 'b']);
    expect(session.union).toEqual({ x: 0, y: 0, width: 400, height: 300, rotation: 0 });
    const result = dragTo(nodes, ['a'], 'a', { x: 303, y: 0 });
    expect(result.position).toEqual({ x: 300, y: 0 });
    expect(result.updates).toEqual([{ id: 'a', x: 300, y: 0 }, { id: 'b', x: 600, y: 200 }]);
  });

  it('hidden members move with their group but shape neither the moving box nor a target box', () => {
    const hidden = node('h', 800, 800, { width: 100, height: 100, groupId: 'g', visible: false });
    const lone = node('c', 1000, 0, { width: 50, height: 50 });
    const nodes = [groupA, groupB, hidden, lone];
    const session = startDragSession(nodes, ['a'], 'a')!;
    expect([...session.starts.keys()].sort()).toEqual(['a', 'b', 'h']);
    expect(session.union).toEqual({ x: 0, y: 0, width: 400, height: 300, rotation: 0 });
    // The visible right edge (400 + dx) near c's left edge (1000); the hidden member's edges do not count.
    const result = dragTo(nodes, ['a'], 'a', { x: 597, y: 0 });
    expect(result.position).toEqual({ x: 600, y: 0 });
    expect(result.updates).toContainEqual({ id: 'h', x: 1400, y: 800 });

    // As a target, the hidden member is left out of its group's box.
    const mover = node('m', 2000, 2000, { width: 10, height: 10 });
    const withMover = [...nodes, mover];
    const targets = snapTargetsFor(withMover, startDragSession(withMover, ['m'], 'm')!);
    expect(targets.primary).toContainEqual({ x: 0, y: 0, width: 400, height: 300, rotation: 0 });
    expect(targets.primary.some((t) => t.x + t.width === 900)).toBe(false);
  });

  it('a group with a rotated member still snaps as a whole, using the box the member covers', () => {
    // r: 100 × 100 at (300, 0) rotated 90° about its top-left corner covers 200..300 × 0..100.
    const rotated = node('r', 300, 0, { width: 100, height: 100, rotation: 90, groupId: 'k' });
    const plain = node('s', 0, 0, { width: 100, height: 100, groupId: 'k' });
    const lone = node('c', 600, 0, { width: 50, height: 50 });
    const nodes = [plain, rotated, lone];
    const box = nodeBounds(rotated);
    expect(box.x).toBeCloseTo(200);
    expect(box.y).toBeCloseTo(0);
    expect(box.width).toBeCloseTo(100);
    expect(box.height).toBeCloseTo(100);

    const session = startDragSession(nodes, ['s'], 's')!;
    expect(session.snappable).toBe(true);
    expect(session.union.x).toBeCloseTo(0);
    expect(session.union.width).toBeCloseTo(300);
    // The group's right edge (300 + dx) near c's left edge (600).
    const result = dragTo(nodes, ['s'], 's', { x: 297, y: 0 });
    expect(result.position.x).toBeCloseTo(300);
    expect(result.updates.find((u) => u.id === 'r')!.x).toBeCloseTo(600);

    // As a target, the rotated member widens its group's box the same way.
    const mover = node('m', 2000, 2000, { width: 10, height: 10 });
    const withMover = [...nodes, mover];
    const targets = snapTargetsFor(withMover, startDragSession(withMover, ['m'], 'm')!);
    const groupBox = targets.primary.find((t) => Math.abs(t.x) < 1e-9)!;
    expect(groupBox.width).toBeCloseTo(300);
  });

  it('a single rotated node snaps with the box it covers', () => {
    // 45°: a 100 × 100 square at (100, 0) covers about 29.3..170.7 × 0..141.4.
    const nodes = [node('r', 100, 0, { width: 100, height: 100, rotation: 45 }), node('c', 400, 0, { width: 50, height: 50 })];
    const session = startDragSession(nodes, ['r'], 'r')!;
    expect(session.snappable).toBe(true);
    const right = session.union.x + session.union.width;
    expect(right).toBeCloseTo(100 + 100 * Math.SQRT1_2);
    const result = dragTo(nodes, ['r'], 'r', { x: 100 + (400 - right) - 2, y: 0 });
    expect(result.position.x).toBeCloseTo(100 + (400 - right));
  });

  it('a line drawn right-to-left (negative width) is bounded by its endpoints', () => {
    expect(nodeBounds(node('l', 100, 50, { width: -80, height: -20 }))).toEqual({ x: 20, y: 30, width: 80, height: 20, rotation: 0 });
  });
});
