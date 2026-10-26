/**
 * One paste path for the canvas. Ctrl+C on the canvas copies the layers into the app's own clipboard and
 * leaves a marker in the system clipboard; when Ctrl+V arrives the marker tells which is newer:
 *  - the marker is still there: the layers were copied last, paste them;
 *  - a picture is there instead (something was copied elsewhere afterwards): paste that picture;
 *  - neither: paste the layers if any were copied.
 */
export const CLIPBOARD_MARKER_PREFIX = 'art-ai-canvas:';

export function clipboardMarker(id: string): string {
  return `${CLIPBOARD_MARKER_PREFIX}${id}`;
}

/** True for clipboard text that is only a canvas marker: it never goes into a text box as words. */
export function isClipboardMarker(text: string): boolean {
  const trimmed = text.trim();
  return trimmed.startsWith(CLIPBOARD_MARKER_PREFIX) && !/\s/.test(trimmed);
}

/**
 * Ctrl/Cmd+C and Ctrl/Cmd+X belong to the canvas only while it is the active area (pointer or focus on it)
 * and no text is selected elsewhere on the page. Selected chat text is the person's copy: writing the canvas
 * marker over it is what turned copied chat text into an `art-ai-canvas:` reference (#281).
 */
export function canvasOwnsCopy(input: {
  canvasActive: boolean;
  selection: Pick<Selection, 'isCollapsed' | 'rangeCount' | 'anchorNode'> | null;
  canvas: Pick<Node, 'contains'> | null;
}): boolean {
  if (!input.canvasActive) return false;
  const { selection } = input;
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return true;
  // A selection inside the canvas (a text layer's overlay) is the canvas's own.
  return Boolean(input.canvas && selection.anchorNode && input.canvas.contains(selection.anchorNode));
}

export type ClipboardPaste = 'nodes' | 'image' | 'none';

export function chooseClipboardPaste(input: {
  /** Image files on the system clipboard. */
  imageFiles: number;
  /** Plain text on the system clipboard. */
  text: string;
  /** Marker of the layers copied inside the app, if any. */
  internalMarker: string | null;
  internalNodes: number;
}): ClipboardPaste {
  const ours = input.internalNodes > 0 && input.internalMarker !== null;
  if (ours && input.text === clipboardMarker(input.internalMarker as string)) return 'nodes';
  if (input.imageFiles > 0) return 'image';
  return ours ? 'nodes' : 'none';
}

/** Where pasted things land: under the pointer when it is over the canvas, otherwise the middle of the view. */
export function pasteAnchor(
  pointer: { clientX: number; clientY: number; inside: boolean } | null,
  rect: { left: number; top: number; width: number; height: number },
  viewport: { zoom: number; panX: number; panY: number },
): { x: number; y: number } {
  const client = pointer && pointer.inside
    ? { x: pointer.clientX, y: pointer.clientY }
    : { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
  return { x: (client.x - rect.left - viewport.panX) / viewport.zoom, y: (client.y - rect.top - viewport.panY) / viewport.zoom };
}
