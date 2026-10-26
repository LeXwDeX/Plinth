import { describe, expect, it } from 'vitest';

import { canvasOwnsCopy, chooseClipboardPaste, clipboardMarker, isClipboardMarker, pasteAnchor } from './clipboard';

describe('which clipboard Ctrl+V pastes', () => {
  const internal = { internalMarker: 'abc', internalNodes: 2 };

  it('copying a node and then a picture elsewhere pastes only the picture', () => {
    expect(chooseClipboardPaste({ ...internal, imageFiles: 1, text: '' })).toBe('image');
  });

  it('copying only inside the canvas pastes the layers, the marker still being there', () => {
    expect(chooseClipboardPaste({ ...internal, imageFiles: 0, text: clipboardMarker('abc') })).toBe('nodes');
    // A picture left in the system clipboard from before does not beat a newer copy of ours.
    expect(chooseClipboardPaste({ ...internal, imageFiles: 1, text: clipboardMarker('abc') })).toBe('nodes');
  });

  it('a marker of an older copy does not count, nothing copied pastes nothing', () => {
    expect(chooseClipboardPaste({ ...internal, imageFiles: 1, text: clipboardMarker('old') })).toBe('image');
    expect(chooseClipboardPaste({ internalMarker: null, internalNodes: 0, imageFiles: 0, text: '' })).toBe('none');
  });

  it('falls back to the layers when the system clipboard shows nothing of its own', () => {
    expect(chooseClipboardPaste({ ...internal, imageFiles: 0, text: '' })).toBe('nodes');
  });
});

describe('where pasted things land', () => {
  const rect = { left: 100, top: 50, width: 800, height: 600 };
  const viewport = { zoom: 2, panX: 40, panY: -20 };

  it('uses the pointer position in board coordinates when the pointer is over the canvas', () => {
    expect(pasteAnchor({ clientX: 500, clientY: 250, inside: true }, rect, viewport)).toEqual({ x: (500 - 100 - 40) / 2, y: (250 - 50 + 20) / 2 });
  });

  it('uses the centre of the view when the pointer is elsewhere or unknown', () => {
    const centre = { x: (100 + 400 - 100 - 40) / 2, y: (50 + 300 - 50 + 20) / 2 };
    expect(pasteAnchor({ clientX: 5, clientY: 5, inside: false }, rect, viewport)).toEqual(centre);
    expect(pasteAnchor(null, rect, viewport)).toEqual(centre);
  });
});

describe('who owns Ctrl/Cmd+C and Ctrl/Cmd+X (#281)', () => {
  const inside = { id: 'text-overlay' } as unknown as Node;
  const chat = { id: 'chat-text' } as unknown as Node;
  const canvas = { contains: (node: Node | null) => node === inside };
  const selected = (anchorNode: Node) => ({ isCollapsed: false, rangeCount: 1, anchorNode });
  const none = { isCollapsed: true, rangeCount: 0, anchorNode: null };

  it('is the canvas while it is active and nothing is selected elsewhere', () => {
    expect(canvasOwnsCopy({ canvasActive: true, selection: none, canvas })).toBe(true);
    expect(canvasOwnsCopy({ canvasActive: true, selection: null, canvas })).toBe(true);
  });

  it('is the page off the canvas: copying chat text never writes the canvas marker over it', () => {
    expect(canvasOwnsCopy({ canvasActive: false, selection: none, canvas })).toBe(false);
    expect(canvasOwnsCopy({ canvasActive: false, selection: selected(chat), canvas })).toBe(false);
  });

  it('is the page when chat text is selected, even with the pointer resting on the canvas', () => {
    expect(canvasOwnsCopy({ canvasActive: true, selection: selected(chat), canvas })).toBe(false);
  });

  it('is the canvas for a selection inside it', () => {
    expect(canvasOwnsCopy({ canvasActive: true, selection: selected(inside), canvas })).toBe(true);
  });
});

describe('the canvas marker as text', () => {
  it('is recognised alone or with surrounding blanks, never inside other words', () => {
    expect(isClipboardMarker(clipboardMarker('8a2aae8b-09dc'))).toBe(true);
    expect(isClipboardMarker(` ${clipboardMarker('x')}\n`)).toBe(true);
    expect(isClipboardMarker('第一行文字\n第二行文字')).toBe(false);
    expect(isClipboardMarker(`见 ${clipboardMarker('x')}`)).toBe(false);
    expect(isClipboardMarker('')).toBe(false);
  });
});
