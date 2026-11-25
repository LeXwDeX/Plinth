/**
 * Which picture the Agent works on when a turn is sent. The dashed artboard is the real canvas sent upstream:
 * everything visible inside it is rasterised into one picture at the chosen output pixels, so the current
 * object is:
 *  - the artboard rasterised into one picture (source: the canvas) when the current picture's own layer is
 *    inside it, even when it holds just that untouched picture — the original's own size or framing is not
 *    what the person chose;
 *  - otherwise the object's own picture (keeping its @img handle and source task): no artboard, an artboard
 *    that holds nothing, or one that holds only other pictures (#610: a version clicked on the strip is not on
 *    the board, and the bow left there must not become image 1 of the axe's edit). No current picture means no
 *    board either; the person sends the board explicitly with @画布 or a painted mask.
 * Pure rules; the caller rasterises and uploads.
 */
import { artboardContent } from './export-canvas';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { CanvasNode } from '@/store/canvas-store';

export type CurrentObjectSource = 'object' | 'canvas';

interface ArtboardRect { x: number; y: number; width: number; height: number }

/** A picture node that still shows the original picture as it was generated or uploaded. */
export function untouchedPicture(node: CanvasNode): boolean {
  return node.type === 'image' && node.crop === undefined && !node.rotation && (node.opacity ?? 1) === 1;
}

/** The current object as the board rules read it. */
export interface BoardObject { kind: string; url: string; taskId: string; captureId?: string }

/** Maps an object's address to the one its board layer stores (the proxied asset URL). */
export type LayerUrl = (url: string, taskId: string) => string | null | undefined;

/**
 * Whether a board layer shows the current picture: the same address, a layer of the same Task (a brush-baked
 * copy keeps the Task while its address changes), or the same 3D capture. A `canvas:` ID names no Task (a
 * picture made on the board, shared by several layers), so only the address counts for it.
 */
export function showsObject(node: CanvasNode, object: BoardObject, resolveUrl: LayerUrl): boolean {
  if (node.type !== 'image' || object.kind !== 'image') return false;
  const url = resolveUrl(object.url, object.taskId) ?? object.url;
  if (url && node.imageUrl === url) return true;
  // A capture names the model it was taken from as its Task: another capture of that model is not this one.
  if (object.captureId) return node.captureId === object.captureId;
  return object.taskId !== '' && !object.taskId.startsWith('canvas:') && node.taskId === object.taskId;
}

export function currentObjectSource(
  nodes: readonly CanvasNode[],
  artboard: ArtboardRect | null,
  object: BoardObject | null | undefined,
  resolveUrl: LayerUrl,
): CurrentObjectSource {
  if (!artboard || !object || object.kind !== 'image') return 'object';
  // An artboard without the object's layer (empty, or other pictures only) is not what the person works on.
  return artboardContent(nodes, artboard).some((node) => showsObject(node, object, resolveUrl)) ? 'canvas' : 'object';
}

/**
 * The selection claim of the artboard snapshot a turn sends (#272): the uploaded picture and how many visible
 * layers were flattened into it (the same artboardContent rule the rasteriser uses). The server measures the
 * picture's size itself and gives it the canvas role on a canvas turn; the count only tells the Agent how many
 * layers the layout holds.
 */
export function canvasSnapshotClaim(
  url: string,
  nodes: readonly CanvasNode[],
  artboard: ArtboardRect,
): ChatAttachmentClaim {
  const content = artboardContent(nodes, artboard);
  const sources = snapshotSources(content);
  return {
    kind: 'image', url,
    ...(content.length > 0 ? { canvasLayers: content.length } : {}),
    ...(sources.length > 0 ? { canvasSources: sources } : {}),
  };
}

/**
 * #189: the generated pictures the snapshot flattens (each once), so a tool that takes a source Task — the
 * Creative Lab build — can resolve the snapshot back to the preview it shows. Uploads, pasted pictures and 3D
 * captures name no source Task. The server verifies every URL for the owner and session before using it.
 */
function snapshotSources(content: readonly CanvasNode[]): string[] {
  const sources: string[] = [];
  for (const node of content) {
    if (node.type !== 'image' || !node.taskId || node.captureId || !node.imageUrl || node.imageUrl.startsWith('data:')) continue;
    if (!sources.includes(node.imageUrl)) sources.push(node.imageUrl);
  }
  return sources;
}

/**
 * Whether the artboard holds more than the object's own untouched picture (a second picture, a drawing, a
 * crop, a hand-painted change). Only decides whether "merge as a new version" is offered; what is sent
 * upstream is always the artboard (currentObjectSource).
 */
export function artboardDiffersFromObject(
  nodes: readonly CanvasNode[],
  artboard: ArtboardRect,
  object: { url: string },
  resolveUrl: (url: string) => string,
): boolean {
  const content = artboardContent(nodes, artboard);
  if (content.length !== 1) return content.length > 1;
  const [only] = content;
  return !untouchedPicture(only) || only.imageUrl !== resolveUrl(object.url);
}
