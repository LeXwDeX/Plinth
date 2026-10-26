/**
 * The eyedropper: the colour under a board point, read from the topmost visible layer that covers it.
 * Pictures are sampled from their pixels, shapes / text / strokes give their own colour, and a point on
 * no layer gives nothing (the caller keeps the current colour). Pure apart from the injected pixel reader.
 */
export interface SampleNode {
  id: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
  zIndex: number;
  visible?: boolean;
  strokeColor?: string;
  fillColor?: string;
  crop?: { x: number; y: number; width: number; height: number };
}

export type PixelReader = (nodeId: string, sourceX: number, sourceY: number) => [number, number, number, number] | null;

const hex = (value: number) => Math.max(0, Math.min(255, Math.round(value))).toString(16).padStart(2, '0');

/** "#rrggbb" for an opaque enough pixel; fully transparent pixels are no colour at all. */
export function pixelHex(red: number, green: number, blue: number, alpha: number): string | null {
  if (alpha <= 0) return null;
  return `#${hex(red)}${hex(green)}${hex(blue)}`;
}

/** The point in the node's own frame (rotation is around its top-left corner). */
export function toLocalPoint(node: Pick<SampleNode, 'x' | 'y' | 'rotation'>, world: { x: number; y: number }): { x: number; y: number } {
  const angle = ((node.rotation ?? 0) * Math.PI) / 180;
  const dx = world.x - node.x;
  const dy = world.y - node.y;
  return { x: dx * Math.cos(angle) + dy * Math.sin(angle), y: -dx * Math.sin(angle) + dy * Math.cos(angle) };
}

/**
 * `naturalSize` is the picture's pixel size for nodes without a crop. Returns a "#rrggbb" or null when no
 * layer gives a colour at the point.
 */
export function sampleColor(
  nodes: readonly SampleNode[],
  world: { x: number; y: number },
  readPixel: PixelReader,
  naturalSize: (nodeId: string) => { width: number; height: number } | null,
): string | null {
  const stack = [...nodes].sort((a, b) => b.zIndex - a.zIndex);
  for (const node of stack) {
    if (node.visible === false) continue;
    const local = toLocalPoint(node, world);
    if (local.x < 0 || local.y < 0 || local.x > node.width || local.y > node.height) continue;
    if (node.type === 'image') {
      const natural = node.crop ?? (() => {
        const size = naturalSize(node.id);
        return size ? { x: 0, y: 0, ...size } : null;
      })();
      if (!natural) continue;
      const pixel = readPixel(node.id, natural.x + (local.x / node.width) * natural.width, natural.y + (local.y / node.height) * natural.height);
      const color = pixel ? pixelHex(...pixel) : null;
      if (color) return color;
      continue;
    }
    const own = node.type === 'shape' && node.fillColor && node.fillColor !== 'transparent' ? node.fillColor : node.strokeColor;
    if (own && own !== 'erase' && own.startsWith('#')) return own;
  }
  return null;
}
