/**
 * Shortcut descriptors declared in the Product Schema ("mod+alt+shift+e", "mod+/", "f7").
 * `mod` is Ctrl on Windows/Linux and Cmd on macOS. Matching is exact on modifiers and falls back to the
 * physical key (event.code) because Alt or Shift change what event.key reports.
 */
export interface ShortcutEvent {
  key: string;
  code: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

export interface ParsedShortcut {
  mod: boolean;
  ctrl: boolean;
  meta: boolean;
  alt: boolean;
  shift: boolean;
  key: string;
}

const NAMED_CODES: Record<string, string> = {
  '/': 'Slash',
  '=': 'Equal',
  '-': 'Minus',
  '[': 'BracketLeft',
  ']': 'BracketRight',
  ',': 'Comma',
  '.': 'Period',
  ';': 'Semicolon',
  "'": 'Quote',
  '\\': 'Backslash',
  '`': 'Backquote',
};

export function parseShortcut(descriptor: string): ParsedShortcut | null {
  let parts = descriptor.trim().toLowerCase().split('+');
  // "mod++" names the plus key itself.
  if (parts.length >= 2 && parts[parts.length - 1] === '') parts = [...parts.slice(0, -2), '+'];
  const key = parts[parts.length - 1];
  if (!key) return null;
  const parsed: ParsedShortcut = { mod: false, ctrl: false, meta: false, alt: false, shift: false, key };
  for (const modifier of parts.slice(0, -1)) {
    if (modifier !== 'mod' && modifier !== 'ctrl' && modifier !== 'meta' && modifier !== 'alt' && modifier !== 'shift') return null;
    parsed[modifier] = true;
  }
  return parsed;
}

function physicalCode(key: string): string | null {
  if (NAMED_CODES[key]) return NAMED_CODES[key];
  if (/^[a-z]$/.test(key)) return `Key${key.toUpperCase()}`;
  if (/^[0-9]$/.test(key)) return `Digit${key}`;
  if (/^f\d{1,2}$/.test(key)) return key.toUpperCase();
  return null;
}

export function matchesShortcut(event: ShortcutEvent, descriptor: string): boolean {
  const shortcut = parseShortcut(descriptor);
  if (!shortcut) return false;
  const platformMod = event.ctrlKey || event.metaKey;
  if (shortcut.mod ? !platformMod : false) return false;
  if (!shortcut.mod && !shortcut.ctrl && event.ctrlKey) return false;
  if (!shortcut.mod && !shortcut.meta && event.metaKey) return false;
  if (shortcut.ctrl && !event.ctrlKey) return false;
  if (shortcut.meta && !event.metaKey) return false;
  if (shortcut.alt !== event.altKey || shortcut.shift !== event.shiftKey) return false;
  if (event.key.toLowerCase() === shortcut.key) return true;
  const code = physicalCode(shortcut.key);
  return code !== null && event.code === code;
}

/** First descriptor of the list that matches the event, or null. */
export function matchingShortcut(event: ShortcutEvent, descriptors: readonly string[]): string | null {
  return descriptors.find((descriptor) => matchesShortcut(event, descriptor)) ?? null;
}
