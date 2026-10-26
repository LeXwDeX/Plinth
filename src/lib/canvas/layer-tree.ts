/**
 * The layers panel as a tree: groups (by groupId) are folders that can be collapsed, and a layer row
 * can be dragged above / below another row or into a group. Pure rules; the store applies the result as
 * one undo step.
 */
export interface TreeNode {
  id: string;
  zIndex: number;
  groupId?: string;
  visible?: boolean;
  locked?: boolean;
}

export type LayerRow<T extends TreeNode = TreeNode> =
  | { kind: 'group'; key: string; groupId: string; memberIds: string[]; collapsed: boolean; visible: boolean; locked: boolean }
  | { kind: 'layer'; key: string; node: T; depth: 0 | 1 };

export const groupRowKey = (groupId: string) => `group:${groupId}`;
export const layerRowKey = (nodeId: string) => `layer:${nodeId}`;

/** Rows top to bottom: a group sits at its highest member, its members follow (indented) unless collapsed. */
export function layerRows<T extends TreeNode>(nodes: readonly T[], collapsed: ReadonlySet<string>): LayerRow<T>[] {
  const ordered = [...nodes].sort((a, b) => b.zIndex - a.zIndex);
  const members = new Map<string, T[]>();
  for (const node of ordered) {
    if (!node.groupId) continue;
    const list = members.get(node.groupId);
    if (list) list.push(node);
    else members.set(node.groupId, [node]);
  }
  const rows: LayerRow<T>[] = [];
  const emitted = new Set<string>();
  for (const node of ordered) {
    if (!node.groupId) {
      rows.push({ kind: 'layer', key: layerRowKey(node.id), node, depth: 0 });
      continue;
    }
    if (emitted.has(node.groupId)) continue;
    emitted.add(node.groupId);
    const list = members.get(node.groupId) ?? [];
    const isCollapsed = collapsed.has(node.groupId);
    rows.push({
      kind: 'group',
      key: groupRowKey(node.groupId),
      groupId: node.groupId,
      memberIds: list.map((member) => member.id),
      collapsed: isCollapsed,
      visible: list.some((member) => member.visible !== false),
      locked: list.every((member) => member.locked === true),
    });
    if (!isCollapsed) for (const member of list) rows.push({ kind: 'layer', key: layerRowKey(member.id), node: member, depth: 1 });
  }
  return rows;
}

export type DropPosition = 'above' | 'below' | 'into';

export interface DropTarget {
  /** A layer row (nodeId) or a group row (groupId). */
  nodeId?: string;
  groupId?: string;
  position: DropPosition;
}

export interface Arrangement {
  /** Layer ids bottom to top. */
  order: string[];
  /** The groupId each layer ends up with (undefined = not in a group). */
  groupIds: Record<string, string | undefined>;
}

/**
 * Where the dragged layers go. `movingIds` is one layer, or every member of a dragged group. Returns
 * null when the drop changes nothing or makes no sense (onto itself, a group into one of its own members).
 */
export function arrangeLayers(nodes: readonly TreeNode[], movingIds: readonly string[], target: DropTarget): Arrangement | null {
  const ascending = [...nodes].sort((a, b) => a.zIndex - b.zIndex);
  const moving = new Set(movingIds);
  if (moving.size === 0) return null;
  const byId = new Map(ascending.map((node) => [node.id, node]));
  const movingNodes = ascending.filter((node) => moving.has(node.id));
  if (movingNodes.length !== moving.size) return null;
  const draggedWholeGroup = movingNodes.length > 0
    && movingNodes[0].groupId !== undefined
    && movingNodes.every((node) => node.groupId === movingNodes[0].groupId)
    && ascending.filter((node) => node.groupId === movingNodes[0].groupId).length === movingNodes.length;

  // The anchor layer the block lands next to, and the group it joins.
  let anchor: TreeNode | undefined;
  let joinGroup: string | undefined;
  let after: boolean;
  if (target.groupId !== undefined) {
    const groupMembers = ascending.filter((node) => node.groupId === target.groupId);
    if (groupMembers.length === 0) return null;
    if (groupMembers.some((node) => moving.has(node.id)) && target.position !== 'into') {
      // A drop next to the group the layer already belongs to moves it out of the group's way.
      if (draggedWholeGroup) return null;
    }
    if (target.position === 'into') {
      if (draggedWholeGroup) return null;
      anchor = groupMembers[groupMembers.length - 1];
      after = true;
      joinGroup = target.groupId;
    } else if (target.position === 'above') {
      anchor = groupMembers[groupMembers.length - 1];
      after = true;
    } else {
      anchor = groupMembers[0];
      after = false;
    }
  } else if (target.nodeId !== undefined) {
    const node = byId.get(target.nodeId);
    if (!node || moving.has(node.id)) return null;
    anchor = node;
    after = target.position === 'above';
    if (draggedWholeGroup) {
      // A group never lands inside another group: it goes next to that group instead.
      if (node.groupId !== undefined) {
        const siblings = ascending.filter((candidate) => candidate.groupId === node.groupId);
        anchor = after ? siblings[siblings.length - 1] : siblings[0];
      }
    } else {
      joinGroup = node.groupId;
    }
  } else {
    return null;
  }
  if (!anchor || moving.has(anchor.id)) return null;

  const rest = ascending.filter((node) => !moving.has(node.id));
  const anchorIndex = rest.findIndex((node) => node.id === anchor.id);
  if (anchorIndex < 0) return null;
  const insertAt = after ? anchorIndex + 1 : anchorIndex;
  const order = [...rest.slice(0, insertAt), ...movingNodes, ...rest.slice(insertAt)].map((node) => node.id);

  const groupIds: Record<string, string | undefined> = {};
  for (const node of ascending) groupIds[node.id] = node.groupId;
  if (!draggedWholeGroup) for (const node of movingNodes) groupIds[node.id] = joinGroup;
  // A group left with a single layer is no group.
  const counts = new Map<string, number>();
  for (const id of Object.keys(groupIds)) {
    const groupId = groupIds[id];
    if (groupId !== undefined) counts.set(groupId, (counts.get(groupId) ?? 0) + 1);
  }
  for (const id of Object.keys(groupIds)) {
    const groupId = groupIds[id];
    if (groupId !== undefined && (counts.get(groupId) ?? 0) < 2) groupIds[id] = undefined;
  }

  const before = ascending.map((node) => node.id);
  const sameOrder = before.length === order.length && before.every((id, index) => id === order[index]);
  const sameGroups = ascending.every((node) => node.groupId === groupIds[node.id]);
  return sameOrder && sameGroups ? null : { order, groupIds };
}
