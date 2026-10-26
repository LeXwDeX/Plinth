/**
 * Layer ordering for the 2D canvas. Order is the nodes' zIndex. Operations work on the whole
 * selection (group siblings travel with it), keep the selected nodes' relative order and move a
 * group as one unit, never splitting it or hopping over part of another group.
 */
export type LayerOp = 'front' | 'back' | 'forward' | 'backward';

interface OrderNode {
  id: string;
  zIndex: number;
  groupId?: string;
}

/** Nodes bottom to top, with every group collected into one unit at its lowest member. */
function unitsOf<T extends OrderNode>(nodes: readonly T[]): T[][] {
  const sorted = [...nodes].sort((a, b) => a.zIndex - b.zIndex);
  const units: T[][] = [];
  const byGroup = new Map<string, T[]>();
  for (const node of sorted) {
    if (!node.groupId) {
      units.push([node]);
      continue;
    }
    const unit = byGroup.get(node.groupId);
    if (unit) unit.push(node);
    else {
      const created = [node];
      byGroup.set(node.groupId, created);
      units.push(created);
    }
  }
  return units;
}

/** Ids a layer operation applies to: the selection plus the group siblings of any member. */
export function layerTargetIds(nodes: readonly OrderNode[], selectedIds: readonly string[]): Set<string> {
  const selected = new Set(selectedIds);
  const groups = new Set<string>();
  for (const node of nodes) if (selected.has(node.id) && node.groupId) groups.add(node.groupId);
  return new Set(nodes.filter((node) => selected.has(node.id) || (node.groupId && groups.has(node.groupId))).map((node) => node.id));
}

/**
 * New bottom-to-top id order after the operation, or null when nothing would change
 * (nothing selected, or already at the edge) so the caller records no undo entry.
 */
export function reorderLayers(
  nodes: readonly OrderNode[],
  selectedIds: readonly string[],
  op: LayerOp,
): string[] | null {
  const targets = layerTargetIds(nodes, selectedIds);
  if (targets.size === 0) return null;
  const units = unitsOf(nodes);
  const isMoving = (unit: OrderNode[]) => unit.some((node) => targets.has(node.id));
  let next: OrderNode[][];
  if (op === 'front' || op === 'back') {
    const moving = units.filter(isMoving);
    const rest = units.filter((unit) => !isMoving(unit));
    next = op === 'front' ? [...rest, ...moving] : [...moving, ...rest];
  } else {
    next = [...units];
    if (op === 'forward') {
      for (let i = next.length - 2; i >= 0; i--) {
        if (isMoving(next[i]) && !isMoving(next[i + 1])) [next[i], next[i + 1]] = [next[i + 1], next[i]];
      }
    } else {
      for (let i = 1; i < next.length; i++) {
        if (isMoving(next[i]) && !isMoving(next[i - 1])) [next[i], next[i - 1]] = [next[i - 1], next[i]];
      }
    }
  }
  const before = [...nodes].sort((a, b) => a.zIndex - b.zIndex).map((node) => node.id);
  const after = next.flat().map((node) => node.id);
  return before.every((id, index) => id === after[index]) ? null : after;
}
