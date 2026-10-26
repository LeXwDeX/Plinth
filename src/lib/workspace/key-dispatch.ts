/**
 * The studio's one key dispatcher. Every window-level shortcut is assigned to a focus
 * area, and the areas are tried in a fixed order:
 *
 *   input   a text field has the caret: nothing fires, ever.
 *   dialog  a modal (the result detail, the shortcut panel) is open: it owns the keys.
 *           The large view (lightbox) is itself modal and is handled by the stage rows
 *           below because it already is the topmost layer.
 *   canvas  the 2D board is shown: its own handler (canvas-board) owns tool keys, Ctrl+A,
 *           nudging and Ctrl+0; this module only decides when the stage must stay out of
 *           its way (arrows nudge a selection instead of stepping versions).
 *   stage   Space, ←/→, Esc, Ctrl+0 on a 3D model, and "?".
 *
 * Key settings come from the Product Schema (canvas.frontend.keyboard and
 * chat.frontend.lightbox_*); nothing here names a key. The same Schema values feed the
 * shortcut panel and the key caps in the hover hints (see shortcutSections / descriptorCaps).
 */

import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { isEditableTarget, ownsNavigationKeys } from '@/lib/workspace/stage-keys';

export type FocusArea = 'input' | 'dialog' | 'canvas' | 'stage';

export interface KeyBindings {
  /** KeyboardEvent.code of the Space key. */
  spaceCode: string;
  previousKey: string;
  nextKey: string;
  closeKey: string;
  /** Pressed with Ctrl/Cmd: recenters the canvas, resets the 3D view. */
  recenterKey: string;
  helpKey: string;
}

export function keyBindingsOf(schema: ProductSchema): KeyBindings {
  const keyboard = schema.canvas.frontend.keyboard;
  const frontend = schema.chat.frontend;
  return {
    spaceCode: keyboard.space_code,
    previousKey: frontend.lightbox_previous_key,
    nextKey: frontend.lightbox_next_key,
    closeKey: frontend.lightbox_close_key,
    recenterKey: keyboard.recenter_key,
    helpKey: keyboard.help_key,
  };
}

/** What the dispatcher needs to know about the studio at the moment of the key press. */
export interface DispatchContext {
  /** The shared 任务详情 dialog is open. */
  resultDetailOpen: boolean;
  /** The keyboard-shortcut panel is open. */
  shortcutsOpen: boolean;
  lightboxOpen: boolean;
  compareOpen: boolean;
  /** An anchored popup (popover, menu, list) is open and owns Esc. */
  popupOpen: boolean;
  /** What the stage shows: the 2D board or a 3D model. */
  surface: 'canvas' | 'model';
  canvasTool: string;
  /** Layers selected on the board. */
  canvasSelection: number;
}

export type KeyCommand =
  | { type: 'shortcuts-toggle' }
  | { type: 'detail-step'; delta: 1 | -1 }
  | { type: 'lightbox-gesture' }
  | { type: 'lightbox-step'; delta: 1 | -1 }
  | { type: 'lightbox-close' }
  | { type: 'stage-step'; delta: 1 | -1 }
  | { type: 'compare-end' }
  | { type: 'view-reset' };

export interface KeyResolution {
  command: KeyCommand;
  area: FocusArea;
  preventDefault: boolean;
  /** The key is spent here: nothing under it (the dialog primitive, a mask) sees it. */
  stopPropagation: boolean;
}

export interface DispatchEvent {
  key: string;
  code: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  repeat?: boolean;
  isComposing?: boolean;
  target: unknown;
}

/** The area the key belongs to. Input wins over everything; an open modal over the surfaces under it. */
export function focusAreaOf(target: unknown, context: Pick<DispatchContext, 'resultDetailOpen' | 'shortcutsOpen' | 'lightboxOpen' | 'surface'>): FocusArea {
  if (isEditableTarget(target)) return 'input';
  if (!context.lightboxOpen && (context.resultDetailOpen || context.shortcutsOpen)) return 'dialog';
  return context.surface === 'canvas' && !context.lightboxOpen ? 'canvas' : 'stage';
}

function resolution(command: KeyCommand, area: FocusArea, flags: { preventDefault?: boolean; stopPropagation?: boolean } = {}): KeyResolution {
  return { command, area, preventDefault: flags.preventDefault ?? false, stopPropagation: flags.stopPropagation ?? false };
}

/** A 3D viewer keeps the arrows for orbiting the camera. */
function ownsArrows(target: unknown): boolean {
  const element = target as { tagName?: string } | null;
  return typeof element?.tagName === 'string' && element.tagName.toLowerCase() === 'model-viewer';
}

/**
 * Turns one key press into at most one command. Pure: the caller applies the command
 * and the preventDefault / stopPropagation it asks for.
 */
export function resolveKey(event: DispatchEvent, context: DispatchContext, bindings: KeyBindings): KeyResolution | null {
  if (event.isComposing) return null;
  const area = focusAreaOf(event.target, context);
  if (area === 'input') return null;
  const modifier = event.ctrlKey || event.metaKey;

  if (modifier) {
    // Ctrl/Cmd+0 is the stage's "reset the view": on a 3D model it resets the camera, on the canvas the
    // board's own handler recenters (this module stays out of it so exactly one of them acts).
    if (!event.altKey && event.key === bindings.recenterKey && context.surface === 'model' && area === 'stage') {
      return resolution({ type: 'view-reset' }, area, { preventDefault: true });
    }
    return null;
  }
  if (event.altKey) return null;

  // "?" opens (and closes) the shortcut panel from anywhere that is not a text field.
  if (event.key === bindings.helpKey && !event.repeat) {
    // Any other open dialog, menu or list keeps its keys; the panel itself closes on the same key.
    if (!context.shortcutsOpen && (area === 'dialog' || context.popupOpen)) return null;
    return resolution({ type: 'shortcuts-toggle' }, area, { preventDefault: true });
  }

  if (area === 'dialog') {
    // The dialog primitive closes on Esc itself; the arrows page through the result's group.
    if (context.shortcutsOpen) return null;
    if (event.key === bindings.previousKey && !ownsArrows(event.target)) return resolution({ type: 'detail-step', delta: -1 }, area, { preventDefault: true });
    if (event.key === bindings.nextKey && !ownsArrows(event.target)) return resolution({ type: 'detail-step', delta: 1 }, area, { preventDefault: true });
    return null;
  }

  if (event.code === bindings.spaceCode) {
    if (ownsNavigationKeys(event.target)) return null;
    return resolution({ type: 'lightbox-gesture' }, area, { preventDefault: context.lightboxOpen });
  }

  if (event.key === bindings.previousKey || event.key === bindings.nextKey) {
    if (ownsNavigationKeys(event.target, 'arrows')) return null;
    const delta = event.key === bindings.nextKey ? 1 : -1;
    if (context.lightboxOpen) return resolution({ type: 'lightbox-step', delta }, area, { preventDefault: true });
    if (context.compareOpen) return null;
    // Selected layers in the select tool own the arrows (a nudge, in the board): the stage does not step.
    if (context.surface === 'canvas' && context.canvasTool === 'select' && context.canvasSelection > 0) return null;
    return resolution({ type: 'stage-step', delta }, area, { preventDefault: true });
  }

  if (event.key === bindings.closeKey) {
    // The large view is modal: Esc is spent here and goes no further.
    if (context.lightboxOpen) return resolution({ type: 'lightbox-close' }, area, { stopPropagation: true });
    // A popover, menu or list is open: Esc closes that first.
    if (context.popupOpen) return null;
    // The comparison lies over the stage: Esc leaves it before anything under it (a mask being painted) sees the key.
    if (context.compareOpen) return resolution({ type: 'compare-end' }, area, { stopPropagation: true });
    // Anything else (painting a mask asks before dropping strokes; the 2D board has its own Esc) is left to its owner.
    return null;
  }
  return null;
}

// ---------------------------------------------------------------------------------------------
// Display: the shortcut panel and the tool tooltips read the same Schema keys.

export interface PlatformInfo { mac: boolean }

export function currentPlatform(): PlatformInfo {
  return { mac: typeof navigator !== 'undefined' && /mac/i.test(navigator.platform) };
}

/** One key as it is printed on a key cap. */
export function keyCap(key: string, text: (key: string) => string): string {
  if (key === ' ' || key === 'Space') return text('ui.shortcuts.key.space');
  const arrows: Record<string, string> = { ArrowLeft: '←', ArrowRight: '→', ArrowUp: '↑', ArrowDown: '↓' };
  if (arrows[key]) return arrows[key];
  if (key === 'Escape') return 'Esc';
  if (key.length === 1) return key.toUpperCase();
  return key.charAt(0).toUpperCase() + key.slice(1);
}

/** "mod+alt+shift+e" (or a bare key with a leading list of modifiers) as key caps. */
export function descriptorCaps(descriptor: string, text: (key: string) => string, platform: PlatformInfo = currentPlatform()): string[] {
  return descriptor.split(/\+(?=.)/).map((part) => {
    const name = part.toLowerCase();
    if (name === 'mod') return platform.mac ? '⌘' : 'Ctrl';
    if (name === 'ctrl') return platform.mac ? '⌃' : 'Ctrl';
    if (name === 'meta') return platform.mac ? '⌘' : 'Win';
    if (name === 'alt') return platform.mac ? '⌥' : 'Alt';
    if (name === 'shift') return platform.mac ? '⇧' : 'Shift';
    return keyCap(part, text);
  });
}

export interface ShortcutItem {
  id: string;
  label: string;
  /** Alternatives, each a list of key caps ("mod+z" and "mod+y" are two). */
  keys: string[][];
}

export interface ShortcutSection {
  area: 'global' | 'stage' | 'canvas' | 'dialog';
  title: string;
  items: ShortcutItem[];
}

/** The shortcut panel's content, entirely from the Schema's key settings and text. */
export function shortcutSections(schema: ProductSchema, platform: PlatformInfo = currentPlatform()): ShortcutSection[] {
  const text = (key: string) => schemaText(schema, key);
  const keyboard = schema.canvas.frontend.keyboard;
  const bindings = keyBindingsOf(schema);
  const caps = (descriptor: string) => descriptorCaps(descriptor, text, platform);
  const mod = (key: string, ...extra: string[]) => caps([...['mod'], ...extra, key].join('+'));
  const single = (key: string) => [keyCap(key, text)];
  const item = (id: string, keys: string[][]): ShortcutItem => ({ id, label: text(`ui.shortcuts.item.${id}`), keys });
  const section = (area: ShortcutSection['area'], items: ShortcutItem[]): ShortcutSection => ({ area, title: text(`ui.shortcuts.area.${area}`), items });

  return [
    section('global', [item('help', [single(bindings.helpKey)])]),
    section('stage', [
      item('lightbox', [single(bindings.spaceCode)]),
      item('step_versions', [single(bindings.previousKey), single(bindings.nextKey)]),
      item('escape', [single(bindings.closeKey)]),
      item('reset_view', [mod(bindings.recenterKey)]),
    ]),
    section('canvas', [
      item('recenter', [single(keyboard.space_code), mod(keyboard.recenter_key)]),
      item('undo', [mod(keyboard.undo_key)]),
      item('redo', [mod(keyboard.redo_key, 'shift'), mod(keyboard.alternate_redo_key)]),
      item('copy', [mod(keyboard.copy_key)]),
      item('paste', [mod(keyboard.paste_key)]),
      item('select_all', [mod(keyboard.select_all_key)]),
      item('group', [mod(keyboard.group_key)]),
      item('layer_order', [mod(keyboard.layer_forward_key), mod(keyboard.layer_backward_key)]),
      item('delete', keyboard.delete_keys.map(single)),
      item('lock', [caps(keyboard.lock_shortcut)]),
      item('merge_visible', [caps(keyboard.merge_visible_shortcut)]),
      item('nudge', [[keyCap('ArrowLeft', text)], [keyCap('ArrowRight', text)], [keyCap('ArrowUp', text)], [keyCap('ArrowDown', text)]]),
      item('tool_select', [single(keyboard.select_tool_key)]),
      item('tool_hand', [single(keyboard.hand_tool_key)]),
      item('tool_marquee', [single(keyboard.marquee_tool_key), single(keyboard.lasso_tool_key)]),
      item('tool_text', [single(keyboard.text_tool_key)]),
      item('tool_shape', [single(keyboard.shape_tool_key), caps(`shift+${keyboard.shape_tool_key}`)]),
      item('tool_brush', [single(keyboard.brush_tool_key), caps(`shift+${keyboard.brush_tool_key}`)]),
      item('tool_eraser', [single(keyboard.eraser_tool_key)]),
      item('tool_crop', [single(keyboard.crop_tool_key)]),
      item('tool_mask', [single(keyboard.mask_tool_key)]),
      item('tool_zoom', [single(keyboard.zoom_tool_key)]),
      item('tool_eyedropper', [single(keyboard.eyedropper_tool_key)]),
      item('brush_size', [single(keyboard.brush_size_down_key), single(keyboard.brush_size_up_key)]),
      item('swap_colors', [single(keyboard.swap_colors_key)]),
      item('default_colors', [single(keyboard.default_colors_key)]),
      item('duplicate', [caps(keyboard.duplicate_shortcut)]),
      item('cut', [caps(keyboard.cut_shortcut)]),
      item('deselect', [caps(keyboard.deselect_shortcut), single(bindings.closeKey)]),
      item('invert_selection', [caps(keyboard.invert_selection_shortcut)]),
      item('zoom_in', [caps(keyboard.zoom_in_shortcut), caps(keyboard.zoom_out_shortcut)]),
      item('zoom_actual', [caps(keyboard.zoom_actual_shortcut)]),
      item('zoom_selection', [caps(keyboard.zoom_selection_shortcut)]),
      item('panel_layers', [caps(keyboard.layers_panel_shortcut)]),
      item('panel_history', [caps(keyboard.history_panel_shortcut)]),
      item('generate', [caps(keyboard.generate_shortcut)]),
      item('mask_confirm', [single(keyboard.mask_confirm_key)]),
      item('mask_exit', [single(keyboard.mask_exit_key)]),
    ]),
    section('dialog', [
      item('detail_step', [single(bindings.previousKey), single(bindings.nextKey)]),
      item('detail_close', [single(bindings.closeKey)]),
    ]),
  ];
}
