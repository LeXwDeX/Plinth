/**
 * Style edits of a selection. Shape-like nodes (shape, pencil) take line colour, width, fill and
 * opacity; text takes font colour (stored as strokeColor), font size and opacity; pictures and
 * frames are left alone. A patch only carries the fields the control changed.
 */
import type { CanvasNode } from '@/store/canvas-store';

export interface StylePatch {
  strokeColor?: string;
  strokeWidth?: number;
  fillColor?: string;
  opacity?: number;
  fontSize?: number;
}

type StyleNode = Pick<CanvasNode, 'id' | 'type'> & Partial<CanvasNode>;

const SHAPE_FIELDS: ReadonlyArray<keyof StylePatch> = ['strokeColor', 'strokeWidth', 'fillColor', 'opacity'];
const PENCIL_FIELDS: ReadonlyArray<keyof StylePatch> = ['strokeColor', 'strokeWidth', 'opacity'];
const TEXT_FIELDS: ReadonlyArray<keyof StylePatch> = ['strokeColor', 'fontSize', 'opacity'];

export function styleFieldsFor(type: CanvasNode['type']): ReadonlyArray<keyof StylePatch> {
  if (type === 'shape') return SHAPE_FIELDS;
  if (type === 'pencil') return PENCIL_FIELDS;
  if (type === 'text') return TEXT_FIELDS;
  return [];
}

/** The per-node patches a style change produces; nodes it does not apply to are omitted. */
export function styleUpdates(
  nodes: readonly StyleNode[],
  selectedIds: readonly string[],
  patch: StylePatch,
): Array<{ id: string; patch: StylePatch }> {
  const selected = new Set(selectedIds);
  const updates: Array<{ id: string; patch: StylePatch }> = [];
  for (const node of nodes) {
    if (!selected.has(node.id) || node.locked) continue;
    const fields = styleFieldsFor(node.type);
    const own: StylePatch = {};
    for (const field of fields) {
      if (patch[field] !== undefined) (own as Record<string, unknown>)[field] = patch[field];
    }
    if (Object.keys(own).length > 0) updates.push({ id: node.id, patch: own });
  }
  return updates;
}

export interface SelectionStyle {
  hasShape: boolean;
  hasText: boolean;
  strokeColor?: string;
  strokeWidth?: number;
  fillColor?: string;
  opacity: number;
  fontSize?: number;
}

/** What the controls show: the first styled node's values (mixed selections show the first). */
export function selectionStyle(
  nodes: readonly StyleNode[],
  selectedIds: readonly string[],
  defaults: { fontSize: number },
): SelectionStyle | null {
  const selected = new Set(selectedIds);
  const picked = nodes.filter((node) => selected.has(node.id) && styleFieldsFor(node.type).length > 0);
  if (picked.length === 0) return null;
  const firstShape = picked.find((node) => node.type === 'shape' || node.type === 'pencil');
  const firstText = picked.find((node) => node.type === 'text');
  const first = picked[0];
  return {
    hasShape: Boolean(firstShape),
    hasText: Boolean(firstText),
    strokeColor: (firstShape ?? firstText)?.strokeColor,
    strokeWidth: firstShape?.strokeWidth,
    fillColor: firstShape?.fillColor,
    opacity: first.opacity ?? 1,
    fontSize: firstText ? (firstText.fontSize ?? defaults.fontSize) : undefined,
  };
}

/**
 * Undo coalescing for continuous controls (sliders): one history entry per gesture. `begin`
 * returns true only for the first change of a gesture; `end` closes it.
 */
export function createStyleGesture() {
  let open = false;
  return {
    begin(): boolean {
      if (open) return false;
      open = true;
      return true;
    },
    end(): void {
      open = false;
    },
  };
}
