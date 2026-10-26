/**
 * Which picture the Agent works on when a turn is sent. The dashed artboard is the real canvas sent upstream:
 * whatever it holds, everything visible inside it is rasterised into one picture at the chosen output pixels,
 * so the current object is:
 *  - the object's own picture (keeping its @img handle and source task) only when there is no artboard or the
 *    artboard holds nothing;
 *  - otherwise the artboard rasterised into one picture (source: the canvas), even when it holds just the
 *    object's untouched picture — the original's own size or framing is not what the person chose.
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

export function currentObjectSource(
  nodes: readonly CanvasNode[],
  artboard: ArtboardRect | null,
): CurrentObjectSource {
  if (!artboard) return 'object';
  // An empty artboard has nothing to rasterise: the object, if any, is still what the turn continues from.
  return artboardContent(nodes, artboard).length === 0 ? 'object' : 'canvas';
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
