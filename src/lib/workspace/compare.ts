/** Compare mode: which pictures or models sit side by side and how. */

export type CompareMode = 'two' | 'four' | 'slider';

export interface CompareSlot {
  /** task:asset, so one picture is never in two panes. */
  key: string;
  taskId: string;
  assetId: string;
  /** Display URL (already through the platform asset route): a picture, or a model file. */
  url: string;
  label: string;
  /** A model turns in its pane instead of zooming; absent means a picture. */
  kind?: 'image' | 'model';
}

export function isModelSlot(slot: CompareSlot): boolean {
  return slot.kind === 'model';
}

/** The slider overlays two pictures; a model in it cannot be sliced, so only pictures count. */
export function sliderSlots(slots: readonly CompareSlot[]): CompareSlot[] {
  return slots.filter((slot) => !isModelSlot(slot));
}

export interface CompareState {
  mode: CompareMode;
  slots: CompareSlot[];
}

export const COMPARE_MODES: readonly CompareMode[] = ['two', 'four', 'slider'];

export function compareCapacity(mode: CompareMode): number {
  return mode === 'four' ? 4 : 2;
}

export function slotKey(taskId: string, assetId: string): string {
  return `${taskId}:${assetId}`;
}

/** Picking a picture that is already in a pane takes it out; a full set drops its oldest. */
export function toggleCompareSlot(state: CompareState, slot: CompareSlot): CompareState {
  if (state.slots.some((item) => item.key === slot.key)) {
    return { ...state, slots: state.slots.filter((item) => item.key !== slot.key) };
  }
  const capacity = compareCapacity(state.mode);
  const slots = [...state.slots, slot];
  return { ...state, slots: slots.slice(Math.max(0, slots.length - capacity)) };
}

/** Switching mode keeps the most recently picked pictures that still fit. */
export function setCompareMode(state: CompareState, mode: CompareMode): CompareState {
  const capacity = compareCapacity(mode);
  return { mode, slots: state.slots.slice(Math.max(0, state.slots.length - capacity)) };
}

export function removeCompareSlot(state: CompareState, key: string): CompareState {
  return { ...state, slots: state.slots.filter((item) => item.key !== key) };
}

/** A candidate group goes in as a whole: up to four pictures, in the grid that fits their number. */
export function compareFromGroup(slots: readonly CompareSlot[]): CompareState {
  const picked = slots.slice(0, 4);
  return { mode: picked.length > 2 ? 'four' : 'two', slots: picked };
}
