/**
 * What a click on a layer selects (the move tool's "auto-select: group / layer" option). In "group"
 * mode a click on a member selects the whole group; Ctrl/Cmd turns the mode around for that click, so
 * one layer of a group (or, in "layer" mode, the whole group) can be picked. Shift adds or removes.
 */
export type AutoSelectMode = 'group' | 'layer';

interface SelectNode {
  id: string;
  groupId?: string;
}

export function oppositeMode(mode: AutoSelectMode): AutoSelectMode {
  return mode === 'group' ? 'layer' : 'group';
}

/** The ids one click stands for: the layer itself, or all of its group. */
export function clickTargets(nodes: readonly SelectNode[], nodeId: string, mode: AutoSelectMode, ctrl: boolean): string[] {
  const effective = ctrl ? oppositeMode(mode) : mode;
  const node = nodes.find((item) => item.id === nodeId);
  if (!node) return [];
  if (effective === 'layer' || !node.groupId) return [nodeId];
  return nodes.filter((item) => item.groupId === node.groupId).map((item) => item.id);
}

/** The selection after a click. */
export function selectionAfterClick(
  current: readonly string[],
  nodes: readonly SelectNode[],
  nodeId: string,
  mode: AutoSelectMode,
  modifiers: { shift: boolean; ctrl: boolean },
): string[] {
  const targets = clickTargets(nodes, nodeId, mode, modifiers.ctrl);
  if (targets.length === 0) return [...current];
  if (!modifiers.shift) return targets;
  const selected = new Set(current);
  return targets.every((id) => selected.has(id))
    ? current.filter((id) => !targets.includes(id))
    : [...current, ...targets.filter((id) => !selected.has(id))];
}
