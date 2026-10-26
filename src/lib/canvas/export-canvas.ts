import type { CanvasNode } from '@/store/canvas-store';
import type { CanvasProductSchema } from '@/lib/product-schema';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { canvasText } from './presentation';
import { inpaintGeometry } from './inpaint-export';
import { layoutText } from './text-layout';
import { blankLayerFill, isBlankLayer, isTransparentBlank } from './initial-layer';

/**
 * #286: which part of rasterising failed. `source` / `fetch` / `decode` are reading the pictures on the
 * board; `frame` / `empty` / `context` / `draw` / `encode` are building the picture itself.
 */
export type CanvasExportStep = 'frame' | 'empty' | 'source' | 'context' | 'fetch' | 'decode' | 'draw' | 'encode';

/** The steps that read the board's pictures (as opposed to drawing or encoding the export). */
export const CANVAS_READ_STEPS: readonly CanvasExportStep[] = ['source', 'fetch', 'decode'];

/**
 * An export failure that says where it failed. The message stays the Schema's text, so callers that show
 * it are unchanged; `step` and `detail` (sizes, status, the picture's path without any query) are for the
 * client error report only: never picture content, prompts or credentials.
 */
export class CanvasExportError extends Error {
  readonly step: CanvasExportStep;
  readonly detail: Record<string, string | number>;

  constructor(message: string, step: CanvasExportStep, detail: Record<string, string | number> = {}) {
    super(message);
    this.name = 'CanvasExportError';
    this.step = step;
    this.detail = detail;
  }
}

function errorSummary(error: unknown): string {
  if (error instanceof Error) return `${error.name}: ${error.message}`;
  return String(error);
}

/** The picture's path for a report: no query string (it could carry a signature). */
function reportPath(url: string): string {
  if (url.startsWith('blob:')) return 'blob';
  return url.split(/[?#]/, 1)[0];
}

export interface CanvasExportOptions {
  padding?: number;
  backgroundColor?: string;
  /** The dashed artboard in world coordinates. Delivery uses these exact pixels. */
  frame?: { x: number; y: number; width: number; height: number };
  /** Output pixels per board unit (default 1); merging edits asks for the picture's own resolution. */
  scale?: number;
  /**
   * With a frame: the exact output pixels (the size chosen for the model, such as 1536x1024).
   * The frame is drawn to fill them; it has the same ratio, so nothing is distorted.
   */
  size?: { width: number; height: number };
}

/** Konva 绕左上角 (x,y) 旋转：包围盒按四角旋转后取 min/max。 */
export function rotatedBounds(node: CanvasNode) {
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  const cos = Math.cos(angle);
  const sin = Math.sin(angle);
  const corners = [
    { x: 0, y: 0 },
    { x: node.width, y: 0 },
    { x: 0, y: node.height },
    { x: node.width, y: node.height },
  ].map((corner) => ({
    x: node.x + corner.x * cos - corner.y * sin,
    y: node.y + corner.x * sin + corner.y * cos,
  }));
  return {
    minX: Math.min(...corners.map((point) => point.x)),
    minY: Math.min(...corners.map((point) => point.y)),
    maxX: Math.max(...corners.map((point) => point.x)),
    maxY: Math.max(...corners.map((point) => point.y)),
  };
}

/** Visible layers that can be drawn: legacy frame nodes are editing guides, the dashed artboard is never ink. */
function exportableNodes(nodes: readonly CanvasNode[]): CanvasNode[] {
  // A transparent initial layer that has not been painted yet (#246) draws nothing.
  return nodes.filter((n) => n.visible !== false && n.type !== 'frame' && !isTransparentBlank(n));
}

/**
 * The visible layers that show inside the artboard: the single rule behind exporting the board,
 * sending it to chat, submitting an inpaint, and whether the export button is enabled.
 */
export function artboardContent(nodes: readonly CanvasNode[], frame: { x: number; y: number; width: number; height: number }): CanvasNode[] {
  return exportableNodes(nodes).filter((node) => {
    // Raster pixels cannot escape their rotated rectangle. Vector strokes,
    // arrow heads and glyphs can extend past a node's geometry, so let the
    // frame clip those nodes instead of dropping potentially visible ink.
    if (node.type !== 'image') return true;
    const bounds = rotatedBounds(node);
    return bounds.maxX > frame.x && bounds.minX < frame.x + frame.width
      && bounds.maxY > frame.y && bounds.minY < frame.y + frame.height;
  });
}

/**
 * The picture sent upstream and offered as "export board": everything visible inside the artboard,
 * clipped to it, at the chosen output pixels (artboard size when none is chosen). Inpaint draws its
 * mask on the same geometry (inpaintGeometry), so the two line up pixel for pixel. It is drawn over
 * the Schema's artboard background: transparent (#271), so empty canvas stays alpha 0 like a
 * Photoshop layer. An Offering whose upstream does not take alpha flattens it server side
 * (alpha_flatten), because a chat turn uploads the board before the Agent picks the Offering.
 */
export function exportArtboardToBlob(
  nodes: CanvasNode[],
  schema: CanvasProductSchema,
  artboard: { x: number; y: number; width: number; height: number },
  pixels?: { width: number; height: number } | null,
): Promise<Blob> {
  const geometry = inpaintGeometry(artboard, pixels);
  return exportCanvasToBlob(nodes, schema, {
    frame: geometry.frame,
    size: geometry.size,
    backgroundColor: schema.frontend.export.artboard_background_color,
  });
}

/**
 * 将画板节点导出为 PNG Blob。
 *
 * 策略：使用原生 Canvas 2D API 绘制所有可见节点，
 * 支持节点旋转、源图裁剪与不透明度。
 */
export async function exportCanvasToBlob(
  nodes: CanvasNode[],
  schema: CanvasProductSchema,
  options?: CanvasExportOptions,
): Promise<Blob> {
  const configuration = schema.frontend.export;
  const padding = options?.padding ?? configuration.padding_pixels;
  const bg =
    options?.backgroundColor ?? configuration.default_background_color;
  const frame = options?.frame;
  if (frame && (!Number.isFinite(frame.x) || !Number.isFinite(frame.y)
    || !Number.isFinite(frame.width) || !Number.isFinite(frame.height)
    || frame.width <= 0 || frame.height <= 0)) {
    throw new CanvasExportError(canvasText(schema, 'error.export_failed'), 'frame');
  }

  const visibleNodes = frame ? artboardContent(nodes, frame) : exportableNodes(nodes);

  if (visibleNodes.length === 0) {
    throw new CanvasExportError(canvasText(schema, 'error.export_empty'), 'empty');
  }

  const sorted = [...visibleNodes].sort((a, b) => a.zIndex - b.zIndex);
  const unsourced = sorted.find((node) => node.type === 'image' && !node.imageUrl && !isBlankLayer(node));
  if (unsourced) {
    throw new CanvasExportError(canvasText(schema, 'error.export_failed'), 'source', { node: unsourced.id });
  }

  const bounds = sorted.map(rotatedBounds);
  const minX = frame?.x ?? Math.min(...bounds.map((item) => item.minX));
  const minY = frame?.y ?? Math.min(...bounds.map((item) => item.minY));
  const maxX = frame ? frame.x + frame.width : Math.max(...bounds.map((item) => item.maxX));
  const maxY = frame ? frame.y + frame.height : Math.max(...bounds.map((item) => item.maxY));
  const exportPadding = frame ? 0 : padding;
  const scale = options?.scale !== undefined && Number.isFinite(options.scale) && options.scale > 0 ? options.scale : 1;
  const size = frame && options?.size && Number.isInteger(options.size.width) && Number.isInteger(options.size.height)
    && options.size.width > 0 && options.size.height > 0 ? options.size : null;
  const scaleX = size && frame ? size.width / frame.width : scale;
  const scaleY = size && frame ? size.height / frame.height : scale;
  const canvasWidth = size ? size.width : Math.ceil((maxX - minX + exportPadding * 2) * scale);
  const canvasHeight = size ? size.height : Math.ceil((maxY - minY + exportPadding * 2) * scale);

  const canvas = document.createElement('canvas');
  canvas.width = canvasWidth;
  canvas.height = canvasHeight;
  const ctx = canvas.getContext('2d');
  const exportFailed = canvasText(schema, 'error.export_failed');
  const pixels = { width: canvasWidth, height: canvasHeight };
  if (!ctx) throw new CanvasExportError(exportFailed, 'context', pixels);

  if (bg !== configuration.transparent_background_value) {
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, canvasWidth, canvasHeight);
  }
  if (scaleX !== 1 || scaleY !== 1) ctx.scale(scaleX, scaleY);
  if (frame) {
    ctx.beginPath();
    ctx.rect(0, 0, frame.width, frame.height);
    ctx.clip();
  }

  const imageMap = new Map<string, CanvasImageSource>();
  const blobUrls: string[] = [];
  // #290: the bytes are decoded off the main thread into bitmaps; drawing an <img> would decode every layer on
  // the main thread inside drawNodes (about 50 ms per 4K picture, all in one task). Released after drawing.
  const bitmaps: ImageBitmap[] = [];
  const releaseBitmaps = () => { for (const bitmap of bitmaps.splice(0)) bitmap.close(); };
  const decodeOffThread = typeof createImageBitmap === 'function';
  // What was read, for the report when drawing or encoding fails later (#286).
  let images = 0;
  let sourceBytes = 0;
  try {
    await Promise.all(
      sorted
        .filter((n) => n.type === 'image' && n.imageUrl)
        .map(async (n) => {
          const url = n.imageUrl;
          const safeURL = safeAssetURL(url);
          if (!safeURL) throw new CanvasExportError(exportFailed, 'source', { node: n.id });
          const asset = reportPath(safeURL);
          let finalSrc = safeURL;
          let bytes = -1;
          let mediaType = '';
          if (decodeOffThread || !safeURL.startsWith('blob:')) {
            let response: Response;
            try {
              response = await fetch(safeURL, { credentials: 'same-origin' });
            } catch (error) {
              throw new CanvasExportError(exportFailed, 'fetch', { asset, reason: errorSummary(error) });
            }
            if (!response.ok) throw new CanvasExportError(exportFailed, 'fetch', { asset, status: response.status });
            let blob: Blob;
            try {
              blob = await response.blob();
            } catch (error) {
              // The response started (the server logs 200) but its body did not arrive whole.
              throw new CanvasExportError(exportFailed, 'fetch', { asset, status: response.status, reason: errorSummary(error) });
            }
            bytes = blob.size;
            mediaType = blob.type;
            sourceBytes += blob.size;
            if (decodeOffThread) {
              let bitmap: ImageBitmap;
              try {
                bitmap = await createImageBitmap(blob);
              } catch {
                throw new CanvasExportError(exportFailed, 'decode', { asset, bytes, type: mediaType });
              }
              bitmaps.push(bitmap);
              imageMap.set(n.id, bitmap);
              images += 1;
              return;
            }
            finalSrc = URL.createObjectURL(blob);
            blobUrls.push(finalSrc);
          }
          await new Promise<void>((resolve, reject) => {
            const img = new Image();
            img.onload = () => { imageMap.set(n.id, img); resolve(); };
            img.onerror = () => {
              reject(new CanvasExportError(exportFailed, 'decode', { asset, bytes, type: mediaType }));
            };
            img.src = finalSrc;
          });
          images += 1;
        }),
    );
  } catch (error) {
    for (const url of blobUrls) URL.revokeObjectURL(url);
    releaseBitmaps();
    throw error;
  }

  const read = { ...pixels, images, sourceBytes };
  try {
    drawNodes(ctx, sorted, imageMap, { minX, minY, exportPadding }, schema);
  } catch (error) {
    for (const url of blobUrls) URL.revokeObjectURL(url);
    releaseBitmaps();
    if (error instanceof CanvasExportError) throw error;
    throw new CanvasExportError(exportFailed, 'draw', { ...read, reason: errorSummary(error) });
  }
  // The canvas holds what it drew; the decoded layers are not needed for the encoding.
  releaseBitmaps();

  return new Promise((resolve, reject) => {
    const encoded = (blob: Blob | null) => {
      for (const u of blobUrls) URL.revokeObjectURL(u);
      // A null blob is the browser failing to encode (for example no memory for the bitmap).
      if (blob) resolve(blob);
      else reject(new CanvasExportError(exportFailed, 'encode', { ...read, reason: 'toBlob returned null' }));
    };
    try {
      canvas.toBlob(encoded, configuration.output_media_type);
    } catch (error) {
      for (const u of blobUrls) URL.revokeObjectURL(u);
      reject(new CanvasExportError(exportFailed, 'encode', { ...read, reason: errorSummary(error) }));
    }
  });
}

function drawNodes(
  ctx: CanvasRenderingContext2D,
  sorted: readonly CanvasNode[],
  imageMap: ReadonlyMap<string, CanvasImageSource>,
  origin: { minX: number; minY: number; exportPadding: number },
  schema: CanvasProductSchema,
): void {
  const configuration = schema.frontend.export;
  const style = schema.frontend.style;
  const textStyle = schema.frontend.text;
  const { minX, minY, exportPadding } = origin;
  for (const node of sorted) {
    ctx.save();
    ctx.globalAlpha = node.opacity ?? 1;
    // Konva nodes draw in local coordinates after translating to their world origin.
    ctx.translate(node.x - minX + exportPadding, node.y - minY + exportPadding);
    ctx.rotate(((node.rotation ?? 0) * Math.PI) / 180);

    switch (node.type) {
      case 'image': {
        // An unpainted initial layer (#246) is only its fill colour.
        const fill = blankLayerFill(node);
        if (isBlankLayer(node)) {
          if (fill) {
            ctx.fillStyle = fill;
            ctx.fillRect(0, 0, node.width, node.height);
          }
          break;
        }
        const img = imageMap.get(node.id);
        if (!img) throw new CanvasExportError(canvasText(schema, 'error.export_failed'), 'draw', { node: node.id });
        if (node.crop) {
          ctx.drawImage(
            img,
            node.crop.x,
            node.crop.y,
            node.crop.width,
            node.crop.height,
            0,
            0,
            node.width,
            node.height,
          );
        } else {
          ctx.drawImage(img, 0, 0, node.width, node.height);
        }
        break;
      }
      case 'text': {
        // The same size, family, line height, colour and wrap width as the board draws it with.
        const layout = layoutText(ctx, {
          text: node.text || '',
          width: node.width,
          fontSize: node.fontSize ?? style.default_font_size,
          fontFamily: textStyle.font_family,
          lineHeight: textStyle.line_height,
        });
        ctx.fillStyle = node.strokeColor || textStyle.default_color;
        ctx.font = layout.font;
        ctx.textBaseline = 'alphabetic';
        layout.lines.forEach((line, index) => {
          ctx.fillText(line, 0, layout.firstBaselineY + index * layout.lineHeightPx);
        });
        break;
      }
      case 'frame': {
        ctx.strokeStyle = configuration.frame.stroke_color;
        ctx.lineWidth = configuration.frame.stroke_width;
        ctx.setLineDash(configuration.frame.line_dash);
        ctx.strokeRect(0, 0, node.width, node.height);
        ctx.setLineDash([]);
        break;
      }
      case 'shape': {
        const st = node.shapeType || 'rectangle';
        const strokeColor =
          node.strokeColor ?? style.default_shape_stroke_color;
        const strokeWidth =
          node.strokeWidth ?? style.default_stroke_width;
        const fillColor =
          node.fillColor ?? style.default_fill_color;
        ctx.strokeStyle = strokeColor;
        ctx.lineWidth = strokeWidth;
        if (st === 'ellipse') {
          ctx.beginPath();
          ctx.ellipse(node.width / 2, node.height / 2, node.width / 2, node.height / 2, 0, 0, Math.PI * 2);
          ctx.fillStyle = fillColor;
          ctx.fill();
          ctx.stroke();
        } else if (st === 'line') {
          ctx.beginPath();
          ctx.moveTo(0, 0);
          ctx.lineTo(node.width, node.height);
          ctx.stroke();
        } else if (st === 'arrow') {
          ctx.beginPath();
          ctx.moveTo(0, 0);
          ctx.lineTo(node.width, node.height);
          ctx.stroke();
          const angle = Math.atan2(node.height, node.width);
          const headLen = configuration.arrow.head_length;
          const headAngle =
            Math.PI / configuration.arrow.head_angle_divisor;
          ctx.beginPath();
          ctx.moveTo(node.width, node.height);
          ctx.lineTo(node.width - headLen * Math.cos(angle - headAngle), node.height - headLen * Math.sin(angle - headAngle));
          ctx.lineTo(node.width - headLen * Math.cos(angle + headAngle), node.height - headLen * Math.sin(angle + headAngle));
          ctx.closePath();
          ctx.fillStyle = strokeColor;
          ctx.fill();
        } else {
          ctx.fillStyle = fillColor;
          ctx.fillRect(0, 0, node.width, node.height);
          ctx.strokeRect(0, 0, node.width, node.height);
        }
        break;
      }
      case 'pencil': {
        if (node.pathData) {
          ctx.strokeStyle =
            node.strokeColor ?? style.default_stroke_color;
          ctx.lineWidth =
            node.strokeWidth ?? style.default_stroke_width;
          ctx.lineCap = configuration.pencil.line_cap;
          ctx.lineJoin = configuration.pencil.line_join;
          const p = new Path2D(node.pathData);
          ctx.stroke(p);
        }
        break;
      }
      default:
        break;
    }
    ctx.restore();
  }
}
