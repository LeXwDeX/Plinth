/**
 * What the stage toolbar's 局部重绘 does when pressed (#278). Inpainting paints a mask on the dashed artboard
 * and submits everything visible inside it (completeMask → exportArtboardToBlob): the mask belongs to the
 * canvas, so what it needs is content in the artboard, not the current version's own layer. That layer can be
 * gone (the person deleted it, e.g. a result they judged failed) while other pictures remain; the entry then
 * still works on the artboard. Only an artboard with nothing in it has nothing to repaint: the toolbar keeps
 * the button off with that reason, and a press that races a deletion says the same instead of a generic
 * export failure (nothing was exported at this point).
 *
 * The order below is the order the checks matter in: no content beats a model without mask parameters (there
 * is no point switching models to paint on nothing), and a selection already made is submitted as it is.
 */
import type { CanvasNode } from '@/store/canvas-store';

export type InpaintEntry =
  /** Nothing visible in the artboard: say so (Schema `ui.studio.stage.action.inpaint_board_empty`). */
  | 'board_empty'
  /** The chosen 2D model takes no mask: offer one that does. */
  | 'switch_model'
  /** A marquee, lasso or painted mask exists: submit it without going through the mask tool. */
  | 'submit_selection'
  /** Enter the mask tool to paint the area. */
  | 'mask_tool';

export function inpaintEntry(input: { boardHasContent: boolean; maskSupported: boolean; hasSelection: boolean }): InpaintEntry {
  if (!input.boardHasContent) return 'board_empty';
  if (!input.maskSupported) return 'switch_model';
  if (input.hasSelection) return 'submit_selection';
  return 'mask_tool';
}

/**
 * The layer on the board that shows the current version's picture, matched by its (proxied) URL. Undefined
 * when the object is not a picture of this session or its layer was removed; callers must not treat that as
 * "nothing on the board" (other layers may remain, see inpaintEntry).
 */
export function currentObjectLayer<T extends Pick<CanvasNode, 'type' | 'imageUrl'>>(
  nodes: readonly T[] | undefined,
  object: { kind: string; url: string; taskId: string; sessionId?: string } | null | undefined,
  sessionId: string | null | undefined,
  resolveUrl: (url: string, taskId: string) => string | null | undefined,
): T | undefined {
  if (!nodes || !object || object.kind !== 'image' || !sessionId || object.sessionId !== sessionId) return undefined;
  const url = resolveUrl(object.url, object.taskId);
  return url ? nodes.find((node) => node.type === 'image' && node.imageUrl === url) : undefined;
}
