/**
 * Where an uploaded picture lands on the board (F4). One rule for every way in:
 * - a picture after another in the same batch goes to the right of it, tops aligned;
 * - a drop or paste centres the picture on the pointer;
 * - the upload button on a board whose frame is still free fits the picture into the frame;
 * - otherwise the button puts the picture in the middle of the visible board.
 */
export interface UploadRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface UploadPlacementInput {
  /** The node size already derived from the picture (Schema edge limits). */
  size: { width: number; height: number };
  /** The picture before it in the same batch. */
  previous?: UploadRect | null;
  /** Board point under the pointer (drop or paste). */
  at?: { x: number; y: number } | null;
  /** The slot imageNodePlacement offers when the frame holds nothing yet. */
  frameSlot?: UploadRect | null;
  /** Board point at the middle of the visible area. */
  viewCenter?: { x: number; y: number } | null;
  /** Schema gap between pictures. */
  gap: number;
  /** Where the Schema puts the first picture when nothing else says (no view yet). */
  fallback: { x: number; y: number };
}

export function uploadPlacement(input: UploadPlacementInput): UploadRect {
  const { width, height } = input.size;
  if (input.previous) {
    return { x: input.previous.x + input.previous.width + input.gap, y: input.previous.y, width, height };
  }
  if (input.at) return { x: Math.round(input.at.x - width / 2), y: Math.round(input.at.y - height / 2), width, height };
  if (input.frameSlot) return { ...input.frameSlot };
  if (input.viewCenter) return { x: Math.round(input.viewCenter.x - width / 2), y: Math.round(input.viewCenter.y - height / 2), width, height };
  return { ...input.fallback, width, height };
}
