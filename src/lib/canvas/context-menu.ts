/**
 * Which items of the canvas right-click menu apply. Pure rules; the board supplies the handlers.
 * The menu holds only manual edits of the selected layers (order, copy, cut, 复制一份, paste, delete, group, lock,
 * align, 下载这一层). Nothing that works on the whole board or calls a model (发送到对话, 转 3D, 去背景) is here:
 * those are the stage toolbar's.
 */
import { reorderLayers } from './layer-order';

export type CanvasMenuItemId =
  | 'front' | 'forward' | 'backward' | 'back'
  | 'copy' | 'cut' | 'duplicate' | 'paste' | 'delete' | 'group' | 'ungroup' | 'lock' | 'unlock'
  | 'align_left' | 'align_hcenter' | 'align_right' | 'align_top' | 'align_vcenter' | 'align_bottom'
  | 'download'
  | 'recenter' | 'select_all';

export interface MenuNode {
  id: string;
  type: string;
  zIndex: number;
  groupId?: string;
  locked?: boolean;
  /** undefined / true = visible. */
  visible?: boolean;
}

export interface MenuState {
  nodes: readonly MenuNode[];
  selectedIds: readonly string[];
  clipboardCount: number;
  canRecenter: boolean;
  hasDownloadHandler: boolean;
  /** The dashed frame exists: a single layer can be aligned to it. */
  hasArtboard?: boolean;
}

export const ALIGN_MENU_IDS = ['align_left', 'align_hcenter', 'align_right', 'align_top', 'align_vcenter', 'align_bottom'] as const;

export interface CanvasMenuItem {
  id: CanvasMenuItemId;
  enabled: boolean;
}

/** Ids that Select All picks: hidden and locked nodes stay out of the selection. */
export function selectableNodeIds(nodes: readonly (Pick<MenuNode, 'id' | 'locked' | 'visible'> & { type?: string; blank?: boolean })[]): string[] {
  // Select all leaves out an unpainted initial layer (#246): it is the canvas, managed in the layers panel.
  return nodes.filter((node) => node.visible !== false && !node.locked && !(node.type === 'image' && node.blank === true)).map((node) => node.id);
}

/** Items for a right-click on a node (the board has already made it part of the selection). */
export function nodeMenuItems(state: MenuState): CanvasMenuItem[] {
  const selected = state.nodes.filter((node) => state.selectedIds.includes(node.id));
  const groupIds = new Set(selected.map((node) => node.groupId ?? ''));
  const singleImage = selected.length === 1 && selected[0].type === 'image';
  // A group aligns as one unit and locked layers stay put: align needs two units, or one and the frame.
  const movable = selected.filter((node) => !node.locked);
  const units = new Set(movable.map((node) => node.groupId ?? `layer:${node.id}`)).size;
  const canAlign = units >= 2 || (units === 1 && state.hasArtboard === true);
  const layer = (op: 'front' | 'forward' | 'backward' | 'back') => ({
    id: op,
    enabled: reorderLayers(state.nodes, state.selectedIds, op) !== null,
  });
  return [
    layer('front'), layer('forward'), layer('backward'), layer('back'),
    { id: 'copy', enabled: selected.length > 0 },
    { id: 'cut', enabled: movable.length > 0 },
    { id: 'duplicate', enabled: selected.length > 0 },
    { id: 'paste', enabled: state.clipboardCount > 0 },
    { id: 'delete', enabled: selected.some((node) => !node.locked) },
    // Grouping needs two or more nodes that are not already exactly one group.
    { id: 'group', enabled: selected.length > 1 && !(groupIds.size === 1 && !groupIds.has('')) },
    { id: 'ungroup', enabled: selected.some((node) => node.groupId) },
    // One toggle, same rule as the lock shortcut: lock unless every selected node is already locked.
    { id: selected.length > 0 && selected.every((node) => node.locked) ? 'unlock' : 'lock', enabled: selected.length > 0 },
    ...ALIGN_MENU_IDS.map((id) => ({ id, enabled: canAlign })),
    ...(state.hasDownloadHandler ? [{ id: 'download' as const, enabled: singleImage }] : []),
  ];
}

/** Items for a right-click on empty canvas. */
export function canvasMenuItems(state: MenuState): CanvasMenuItem[] {
  return [
    { id: 'paste', enabled: state.clipboardCount > 0 },
    { id: 'recenter', enabled: state.canRecenter },
    { id: 'select_all', enabled: selectableNodeIds(state.nodes).length > 0 },
  ];
}

export interface LayerKeyEvent {
  key: string;
  code: string;
  shiftKey: boolean;
}

function matchesBracket(event: LayerKeyEvent, configured: string): boolean {
  if (event.key.toLowerCase() === configured.toLowerCase()) return true;
  // Shift turns ']' into '}' on most layouts; the physical key still identifies it.
  return (configured === ']' && event.code === 'BracketRight') || (configured === '[' && event.code === 'BracketLeft');
}

/** Ctrl/Cmd+] up one, +[ down one; with Shift to the top / bottom. The caller checks the modifier. */
export function layerShortcut(
  event: LayerKeyEvent,
  keys: { forward: string; backward: string },
): 'front' | 'forward' | 'backward' | 'back' | null {
  if (matchesBracket(event, keys.forward)) return event.shiftKey ? 'front' : 'forward';
  if (matchesBracket(event, keys.backward)) return event.shiftKey ? 'back' : 'backward';
  return null;
}
