import { describe, expect, it } from 'vitest';

import { matchesShortcut, matchingShortcut, parseShortcut, type ShortcutEvent } from './shortcut';

const event = (key: string, code: string, extra: Partial<ShortcutEvent> = {}): ShortcutEvent => ({
  key, code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...extra,
});

describe('canvas shortcut descriptors', () => {
  it('parses modifiers and the key', () => {
    expect(parseShortcut('mod+alt+shift+e')).toMatchObject({ mod: true, alt: true, shift: true, key: 'e' });
    expect(parseShortcut('f7')).toMatchObject({ mod: false, key: 'f7' });
    expect(parseShortcut('mod++')).toMatchObject({ mod: true, key: '+' });
    expect(parseShortcut('hyper+e')).toBeNull();
    expect(parseShortcut('')).toBeNull();
  });

  it('mod accepts Ctrl or Cmd and matches exact modifiers', () => {
    expect(matchesShortcut(event('a', 'KeyA', { ctrlKey: true }), 'mod+a')).toBe(true);
    expect(matchesShortcut(event('a', 'KeyA', { metaKey: true }), 'mod+a')).toBe(true);
    expect(matchesShortcut(event('a', 'KeyA'), 'mod+a')).toBe(false);
    expect(matchesShortcut(event('a', 'KeyA', { ctrlKey: true, shiftKey: true }), 'mod+a')).toBe(false);
    expect(matchesShortcut(event('F7', 'F7'), 'f7')).toBe(true);
    expect(matchesShortcut(event('F7', 'F7', { ctrlKey: true }), 'f7')).toBe(false);
  });

  it('falls back to the physical key when Alt changes what key reports (macOS Alt+E)', () => {
    const alt = event('´', 'KeyE', { ctrlKey: true, altKey: true, shiftKey: true });
    expect(matchesShortcut(alt, 'mod+alt+shift+e')).toBe(true);
    expect(matchesShortcut(event('/', 'Slash', { ctrlKey: true }), 'mod+/')).toBe(true);
    expect(matchesShortcut(event('=', 'Equal', { ctrlKey: true }), 'mod+=')).toBe(true);
  });

  it('returns the first matching descriptor of a list', () => {
    expect(matchingShortcut(event('d', 'KeyD', { ctrlKey: true }), ['mod+a', 'mod+d'])).toBe('mod+d');
    expect(matchingShortcut(event('x', 'KeyX', { ctrlKey: true }), ['mod+a', 'mod+d'])).toBeNull();
  });
});
