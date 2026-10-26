/**
 * Align and distribute (Photoshop's options-bar buttons). Pure geometry on axis-aligned bounds
 * (a rotated layer counts by the box around its rotated corners). A group moves as one unit, so
 * aligning never pulls its members apart. Locked layers stay where they are.
 */
export type AlignMode = 'left' | 'hcenter' | 'right' | 'top' | 'vcenter' | 'bottom';
export type DistributeAxis = 'horizontal' | 'vertical';

export interface AlignNode {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
  groupId?: string;
  locked?: boolean;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PositionUpdate {
  id: string;
  x: number;
  y: number;
}

/** Axis-aligned box around a node, rotation included (Konva rotates about the top-left corner). */
export function nodeBox(node: Pick<AlignNode, 'x' | 'y' | 'width' | 'height' | 'rotation'>): Rect {
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  if (angle === 0) return { x: node.x, y: node.y, width: node.width, height: node.height };
  const cos = Math.cos(angle);
  const sin = Math.sin(angle);
  const xs: number[] = [];
  const ys: number[] = [];
  for (const [cx, cy] of [[0, 0], [node.width, 0], [0, node.height], [node.width, node.height]]) {
    xs.push(node.x + cx * cos - cy * sin);
    ys.push(node.y + cx * sin + cy * cos);
  }
  const left = Math.min(...xs);
  const top = Math.min(...ys);
  return { x: left, y: top, width: Math.max(...xs) - left, height: Math.max(...ys) - top };
}

export function unionRect(rects: readonly Rect[]): Rect {
  const left = Math.min(...rects.map((rect) => rect.x));
  const top = Math.min(...rects.map((rect) => rect.y));
  const right = Math.max(...rects.map((rect) => rect.x + rect.width));
  const bottom = Math.max(...rects.map((rect) => rect.y + rect.height));
  return { x: left, y: top, width: right - left, height: bottom - top };
}

interface Unit {
  members: AlignNode[];
  box: Rect;
}

/** The movable units among the selected nodes: each group once, every other node alone. Locked nodes are left out. */
function unitsOf(nodes: readonly AlignNode[], selectedIds: readonly string[]): Unit[] {
  const selected = new Set(selectedIds);
  const groups = new Map<string, AlignNode[]>();
  const units: AlignNode[][] = [];
  for (const node of nodes) {
    if (!selected.has(node.id) || node.locked) continue;
    if (!node.groupId) {
      units.push([node]);
      continue;
    }
    const existing = groups.get(node.groupId);
    if (existing) existing.push(node);
    else {
      const created = [node];
      groups.set(node.groupId, created);
      units.push(created);
    }
  }
  return units.map((members) => ({ members, box: unionRect(members.map(nodeBox)) }));
}

function shift(unit: Unit, dx: number, dy: number): PositionUpdate[] {
  return unit.members.map((node) => ({ id: node.id, x: node.x + dx, y: node.y + dy }));
}

/**
 * Updates that align the selection. With two or more units they align to the box around all of them;
 * with one unit they align to `target` (the dashed frame) when given. Units already in place produce
 * no update, so an empty result means nothing changed (and no undo step is recorded).
 */
export function alignUpdates(
  nodes: readonly AlignNode[],
  selectedIds: readonly string[],
  mode: AlignMode,
  target?: Rect | null,
): PositionUpdate[] {
  const units = unitsOf(nodes, selectedIds);
  if (units.length === 0) return [];
  const reference = units.length > 1 ? unionRect(units.map((unit) => unit.box)) : target ?? null;
  if (!reference) return [];
  const updates: PositionUpdate[] = [];
  for (const unit of units) {
    const box = unit.box;
    let dx = 0;
    let dy = 0;
    if (mode === 'left') dx = reference.x - box.x;
    else if (mode === 'hcenter') dx = reference.x + reference.width / 2 - (box.x + box.width / 2);
    else if (mode === 'right') dx = reference.x + reference.width - (box.x + box.width);
    else if (mode === 'top') dy = reference.y - box.y;
    else if (mode === 'vcenter') dy = reference.y + reference.height / 2 - (box.y + box.height / 2);
    else dy = reference.y + reference.height - (box.y + box.height);
    if (dx !== 0 || dy !== 0) updates.push(...shift(unit, dx, dy));
  }
  return updates;
}

/**
 * Updates that space three or more units evenly along an axis: the outermost two stay, the gaps
 * between neighbouring boxes become equal. Positions are whole pixels, so gaps differ by at most one.
 */
export function distributeUpdates(
  nodes: readonly AlignNode[],
  selectedIds: readonly string[],
  axis: DistributeAxis,
): PositionUpdate[] {
  const units = unitsOf(nodes, selectedIds);
  if (units.length < 3) return [];
  const horizontal = axis === 'horizontal';
  const start = (unit: Unit) => (horizontal ? unit.box.x : unit.box.y);
  const size = (unit: Unit) => (horizontal ? unit.box.width : unit.box.height);
  const ordered = [...units].sort((left, right) => start(left) - start(right));
  const first = ordered[0];
  const last = ordered[ordered.length - 1];
  const span = start(last) + size(last) - start(first);
  const occupied = ordered.reduce((sum, unit) => sum + size(unit), 0);
  const gap = (span - occupied) / (ordered.length - 1);
  const updates: PositionUpdate[] = [];
  let cursor = start(first) + size(first) + gap;
  for (const unit of ordered.slice(1, -1)) {
    const position = Math.round(cursor);
    const delta = position - start(unit);
    if (delta !== 0) updates.push(...shift(unit, horizontal ? delta : 0, horizontal ? 0 : delta));
    cursor += size(unit) + gap;
  }
  return updates;
}

/** How many movable units the selection has (the options bar shows align at 2+, distribute at 3+). */
export function alignUnitCount(nodes: readonly AlignNode[], selectedIds: readonly string[]): number {
  return unitsOf(nodes, selectedIds).length;
}
