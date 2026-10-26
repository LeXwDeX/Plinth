/**
 * Stage keyboard shortcuts: Space opens and closes the large view, the arrow
 * keys step through versions and Esc leaves the large view or compare. They
 * never fire while the user is typing, and Space and the arrows are left to a
 * control that already owns them (a button, a menu, a slider) unless that
 * control opted in.
 */

export interface StageKeyConfig {
  /** KeyboardEvent.code of the Space key (canvas.frontend.keyboard.space_code). */
  spaceCode: string;
  previousKey: string;
  nextKey: string;
  closeKey: string;
}

export type StageKeyAction = 'toggle-lightbox' | 'previous' | 'next' | 'escape';

export interface StageKeyEvent {
  key: string;
  code: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  repeat?: boolean;
  isComposing?: boolean;
  target: unknown;
}

type ElementLike = { closest?: (selector: string) => unknown; matches?: (selector: string) => boolean; isContentEditable?: boolean; tagName?: string };

const EDITABLE = 'input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]):not([type=range]),textarea,select,[contenteditable=""],[contenteditable="true"],[role=textbox],[role=combobox],[role=searchbox]';
const NATIVE_KEY_OWNERS = 'button,a[href],summary,input,[role=button],[role=menuitem],[role=menuitemradio],[role=menuitemcheckbox],[role=option],[role=tab],[role=radio],[role=checkbox],[role=switch],[role=slider],[role=separator],[role=listbox],[role=menu],[role=tablist],[role=dialog]';
const PASSTHROUGH = '[data-shortcut-passthrough]';
/** A control that keeps Space for itself (it is a button) but lets the arrows step versions. */
const ARROWS_PASSTHROUGH = '[data-shortcut-arrows]';

function asElement(target: unknown): ElementLike | null {
  return target !== null && typeof target === 'object' ? (target as ElementLike) : null;
}

/** A text field (or anything editable): no shortcut fires while the caret is in one. */
export function isEditableTarget(target: unknown): boolean {
  const element = asElement(target);
  if (!element) return false;
  if (element.isContentEditable === true) return true;
  return Boolean(element.closest?.(EDITABLE));
}

/**
 * A control that uses Space and the arrows itself. data-shortcut-passthrough on the
 * control that owns the keys (the large view's own frame) opts it out; data-shortcut-arrows
 * on it or any ancestor frees only the arrows (strip tiles, controls inside the large view).
 */
export function ownsNavigationKeys(target: unknown, keys: 'space' | 'arrows' = 'space'): boolean {
  const element = asElement(target);
  if (!element?.closest) return false;
  const owner = asElement(element.closest(NATIVE_KEY_OWNERS));
  if (!owner) return false;
  if (owner.matches?.(PASSTHROUGH)) return false;
  if (keys === 'arrows' && element.closest(ARROWS_PASSTHROUGH)) return false;
  return true;
}

export function stageKeyAction(event: StageKeyEvent, config: StageKeyConfig): StageKeyAction | null {
  if (event.ctrlKey || event.metaKey || event.altKey || event.isComposing) return null;
  if (isEditableTarget(event.target)) return null;
  if (event.code === config.spaceCode) {
    return ownsNavigationKeys(event.target) ? null : 'toggle-lightbox';
  }
  if (event.key === config.previousKey) return ownsNavigationKeys(event.target, 'arrows') ? null : 'previous';
  if (event.key === config.nextKey) return ownsNavigationKeys(event.target, 'arrows') ? null : 'next';
  if (event.key === config.closeKey) return 'escape';
  return null;
}

/**
 * Space is also the canvas's hold-to-pan key, so it opens the large view only as
 * a tap: a press that saw a pointer go down before it was released was a pan.
 */
export function createSpaceGesture() {
  let held = false;
  let panned = false;
  return {
    press(): void {
      held = true;
      panned = false;
    },
    pointerDown(): void {
      if (held) panned = true;
    },
    /** True when the release ends a tap that should toggle the large view. */
    release(): boolean {
      const tapped = held && !panned;
      held = false;
      panned = false;
      return tapped;
    },
    cancel(): void {
      held = false;
      panned = false;
    },
    get isHeld(): boolean {
      return held;
    },
  };
}
