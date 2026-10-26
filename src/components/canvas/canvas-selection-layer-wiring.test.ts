import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

/** The board source without comments, so the order checks read only the rendered tree. */
const board = readFileSync(new URL('canvas-board.tsx', import.meta.url), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/(^|[^:])\/\/.*$/gm, '$1');

/** The `<Layer …>` tag that encloses the given position (the last one opened before it). */
function enclosingLayer(position: number): { start: number; tag: string } {
  const start = board.lastIndexOf('<Layer', position);
  expect(start).toBeGreaterThanOrEqual(0);
  // The layer must still be open at that position.
  expect(board.indexOf('</Layer>', start)).toBeGreaterThan(position);
  return { start, tag: board.slice(start, board.indexOf('>', start) + 1) };
}

/**
 * #311: a picture moved outside the canvas by a resize is dimmed (it is not exported), but its selection box
 * and transform handles must not be: drawn under the dim they looked unselected. The handles therefore live in
 * a layer stacked above the dim layer; the dim layer and everything else keep their place.
 */
describe('selection handles above the outside-canvas dim (#311)', () => {
  const dim = board.indexOf('outsideDimRects(');
  const handles = board.indexOf('ref={transformerRef}');

  it('draws the selection Transformer in a pointer-listening layer stacked above the dim layer', () => {
    expect(dim).toBeGreaterThan(0);
    expect(board.split('ref={transformerRef}').length - 1).toBe(1);
    const dimLayer = enclosingLayer(dim);
    expect(dimLayer.tag).toContain('listening={false}');
    const handleLayer = enclosingLayer(handles);
    // A later sibling layer, not the dim layer and not the picture layer below it.
    expect(handleLayer.start).toBeGreaterThan(board.indexOf('</Layer>', dimLayer.start));
    // The handles are dragged and rotated, so their layer listens to the pointer.
    expect(handleLayer.tag).not.toContain('listening={false}');
  });

  it('keeps the pictures, mask strokes, brush preview and crop frame under the dim', () => {
    const pictures = board.indexOf('{sorted.map((node) =>');
    for (const marker of [pictures, board.indexOf('name="mask-overlay"'), board.indexOf('name="mask-brush-cursor"'), board.indexOf('brushHover && ('), board.indexOf('ref={cropTransformerRef}')]) {
      expect(marker).toBeGreaterThan(0);
      expect(marker).toBeLessThan(dim);
      expect(enclosingLayer(marker).start).toBe(enclosingLayer(pictures).start);
    }
  });
});
