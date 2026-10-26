/**
 * Multi-selection dragging for the 2D canvas. Pure geometry: the board follows the node under the
 * pointer and derives every other moving node's position from the drag start snapshot plus that
 * node's displacement (never accumulating per-move deltas against stale props).
 *
 * Snapping works on units (#273): the moving set is one box, the visible members it covers on
 * screen; a group that is not moving is one target box, the union of its visible members. Hidden
 * members still move with their group but never shape a box. A rotated node counts with the
 * axis-aligned box it covers, so a group with a rotated member still snaps as a whole.
 */
import type { SnapRect } from './snapping';

export interface DragNode {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
  groupId?: string;
  locked?: boolean;
  visible?: boolean;
  type?: string;
}

export interface Point {
  x: number;
  y: number;
}

export interface DragSession {
  draggedId: string;
  /** Position of the dragged node when the drag began. */
  draggedStart: Point;
  /** Start position of every moving node (the dragged node included). */
  starts: ReadonlyMap<string, Point>;
  /** Start bounds of the moving set's visible members, plus whether snapping may be applied. */
  union: SnapRect;
  snappable: boolean;
}

/** Snap targets: one box per unit, plus (when the Schema asks for them) group members' own boxes. */
export interface SnapTargets {
  primary: SnapRect[];
  secondary: SnapRect[];
}

export interface SnapTargetOptions {
  /** Schema `canvas.frontend.snap.group_member_targets`: members of a group are secondary targets too. */
  groupMemberTargets?: boolean;
}

/**
 * Nodes that move together with the dragged one: the whole selection when the dragged node is part
 * of it, otherwise only the dragged node; group siblings of any member follow. Locked nodes stay
 * put (the dragged node itself is always kept: Konva has already moved it).
 */
export function movingNodeIds(
  nodes: readonly DragNode[],
  selectedIds: readonly string[],
  draggedId: string,
): Set<string> {
  const base = selectedIds.includes(draggedId) ? new Set(selectedIds) : new Set([draggedId]);
  const groups = new Set<string>();
  for (const node of nodes) {
    if (base.has(node.id) && node.groupId) groups.add(node.groupId);
  }
  const ids = new Set<string>();
  for (const node of nodes) {
    if (node.id === draggedId) {
      ids.add(node.id);
    } else if ((base.has(node.id) || (node.groupId && groups.has(node.groupId))) && !node.locked) {
      ids.add(node.id);
    }
  }
  return ids;
}

/**
 * The axis-aligned box a node covers on screen. Konva rotates a node about its own x/y (every node
 * type keeps its origin at the top-left corner of its box), and a line's width/height may be negative.
 */
export function nodeBounds(node: DragNode): SnapRect {
  const rotation = node.rotation ?? 0;
  const upright = rotation % 360 === 0;
  const cos = upright ? 1 : Math.cos((rotation * Math.PI) / 180);
  const sin = upright ? 0 : Math.sin((rotation * Math.PI) / 180);
  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;
  for (const [cx, cy] of [[0, 0], [node.width, 0], [0, node.height], [node.width, node.height]]) {
    const x = node.x + cx * cos - cy * sin;
    const y = node.y + cx * sin + cy * cos;
    left = Math.min(left, x);
    top = Math.min(top, y);
    right = Math.max(right, x);
    bottom = Math.max(bottom, y);
  }
  return { x: left, y: top, width: right - left, height: bottom - top, rotation: 0 };
}

export function unionBounds(nodes: readonly SnapRect[]): SnapRect {
  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;
  for (const node of nodes) {
    left = Math.min(left, node.x);
    top = Math.min(top, node.y);
    right = Math.max(right, node.x + node.width);
    bottom = Math.max(bottom, node.y + node.height);
  }
  return { x: left, y: top, width: right - left, height: bottom - top, rotation: 0 };
}

export function startDragSession(
  nodes: readonly DragNode[],
  selectedIds: readonly string[],
  draggedId: string,
): DragSession | null {
  const dragged = nodes.find((node) => node.id === draggedId);
  if (!dragged) return null;
  const ids = movingNodeIds(nodes, selectedIds, draggedId);
  const moving = nodes.filter((node) => ids.has(node.id));
  // Hidden members move along but do not shape the box: guides must land on what is drawn.
  const visible = moving.filter((node) => node.visible !== false);
  return {
    draggedId,
    draggedStart: { x: dragged.x, y: dragged.y },
    starts: new Map(moving.map((node) => [node.id, { x: node.x, y: node.y }])),
    union: unionBounds((visible.length > 0 ? visible : moving).map(nodeBounds)),
    snappable: visible.length > 0,
  };
}

/** The moving set's union bounds at the dragged node's new position. */
export function movedUnion(session: DragSession, dragged: Point): SnapRect {
  return {
    ...session.union,
    x: session.union.x + dragged.x - session.draggedStart.x,
    y: session.union.y + dragged.y - session.draggedStart.y,
  };
}

/** Per-node updates for the dragged node's new position, all shifted by the same delta. */
export function dragUpdates(session: DragSession, dragged: Point): Array<{ id: string; x: number; y: number }> {
  const dx = dragged.x - session.draggedStart.x;
  const dy = dragged.y - session.draggedStart.y;
  return [...session.starts].map(([id, start]) => ({ id, x: start.x + dx, y: start.y + dy }));
}

/**
 * Snap targets among the visible non-frame nodes that are not moving. Each group is one unit: its
 * visible members form a single box, so its inner edges are not targets. A node outside any group is
 * its own unit. With `groupMemberTargets`, every group member's own box is also a secondary target
 * (consulted only on an axis no unit is within reach of).
 */
export function snapTargetsFor(
  nodes: readonly DragNode[],
  session: DragSession,
  options: SnapTargetOptions = {},
): SnapTargets {
  const primary: SnapRect[] = [];
  const secondary: SnapRect[] = [];
  const groups = new Map<string, SnapRect[]>();
  for (const node of nodes) {
    if (session.starts.has(node.id) || node.visible === false || node.type === 'frame') continue;
    const bounds = nodeBounds(node);
    if (!node.groupId) {
      primary.push(bounds);
      continue;
    }
    const members = groups.get(node.groupId) ?? [];
    members.push(bounds);
    groups.set(node.groupId, members);
    if (options.groupMemberTargets) secondary.push(bounds);
  }
  for (const members of groups.values()) primary.push(unionBounds(members));
  return { primary, secondary };
}
