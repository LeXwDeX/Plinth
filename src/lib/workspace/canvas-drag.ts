/**
 * Dragging a picture card (version strip, all outputs, chat result card, a picture sent in the chat,
 * a composer attachment) onto the 2D canvas.
 * The payload travels in a custom MIME type so that file drops and text drags are unaffected.
 */
export const CANVAS_ITEM_MIME = 'application/x-artai-canvas-item';

export interface CanvasDragItem {
  /** Empty for a picture the person uploaded (`source: 'upload'`): it has no task. */
  taskId: string;
  /** Empty for an uploaded picture whose asset id is not known where it was dragged from. */
  assetId: string;
  url: string;
  kind: 'image';
  width?: number;
  height?: number;
  label?: string;
  /** A picture the person uploaded (sent in the chat or attached in the composer), not a task result. */
  source?: 'upload';
}

export function encodeCanvasDrag(item: CanvasDragItem): string {
  return JSON.stringify(item);
}

/** Drag data is untrusted: anything that is not a complete image item is rejected. */
export function decodeCanvasDrag(raw: string | null | undefined): CanvasDragItem | null {
  if (!raw) return null;
  try {
    const value = JSON.parse(raw) as Partial<CanvasDragItem> | null;
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    if (value.kind !== 'image') return null;
    if (typeof value.url !== 'string' || value.url === '') return null;
    const upload = value.source === 'upload';
    // An upload carries no task; its asset id is kept when known. A task result needs both.
    const taskId = upload ? '' : value.taskId;
    const assetId = upload ? (typeof value.assetId === 'string' ? value.assetId : '') : value.assetId;
    if (!upload && (typeof taskId !== 'string' || taskId === '')) return null;
    if (!upload && (typeof assetId !== 'string' || assetId === '')) return null;
    return {
      taskId: taskId as string,
      assetId: assetId as string,
      url: value.url,
      kind: 'image',
      ...(upload ? { source: 'upload' as const } : {}),
      ...(typeof value.width === 'number' && value.width > 0 ? { width: value.width } : {}),
      ...(typeof value.height === 'number' && value.height > 0 ? { height: value.height } : {}),
      ...(typeof value.label === 'string' && value.label !== '' ? { label: value.label } : {}),
    };
  } catch {
    return null;
  }
}

/** An item only when task, asset and url are all known. */
export function canvasDragItem(source: {
  taskId?: string | null; assetId?: string | null; url?: string | null; width?: number; height?: number; label?: string;
}): CanvasDragItem | null {
  if (!source.taskId || !source.assetId || !source.url) return null;
  return decodeCanvasDrag(JSON.stringify({ ...source, kind: 'image' }));
}

/** An uploaded picture (no task); null without a url. */
export function uploadDragItem(source: { url?: string | null; assetId?: string | null; label?: string }): CanvasDragItem | null {
  if (!source.url) return null;
  return decodeCanvasDrag(JSON.stringify({ url: source.url, assetId: source.assetId ?? '', label: source.label, kind: 'image', source: 'upload' }));
}

/**
 * A composer attachment that is not uploaded yet is a raster data URI. Dropped on the canvas it goes the
 * file-drop path (upload, then a layer), so the board never stores the data URI itself. Null for anything else.
 */
export function dataURIFile(uri: string, name: string): File | null {
  const match = /^data:image\/(png|jpeg|webp|gif);base64,([A-Za-z0-9+/]+=*)$/.exec(uri);
  if (!match) return null;
  try {
    const binary = atob(match[2]);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return new File([bytes], `${name}.${match[1] === 'jpeg' ? 'jpg' : match[1]}`, { type: `image/${match[1]}` });
  } catch {
    return null;
  }
}

export function hasCanvasDrag(types: ReadonlyArray<string> | DOMStringList | null | undefined): boolean {
  return Boolean(types && Array.from(types as ArrayLike<string>).includes(CANVAS_ITEM_MIME));
}

/** Fills the data transfer with the item and a uri-list fallback; `image` is the drag preview. */
export function startCanvasDrag(
  transfer: Pick<DataTransfer, 'setData' | 'setDragImage'>,
  item: CanvasDragItem,
  image?: Element | null,
): void {
  transfer.setData(CANVAS_ITEM_MIME, encodeCanvasDrag(item));
  // A not-yet-uploaded attachment is a data URI of several MB: no uri-list text for other drop targets.
  if (!item.url.startsWith('data:')) transfer.setData('text/uri-list', item.url);
  if (image) transfer.setDragImage(image, 0, 0);
}
