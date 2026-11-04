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

/**
 * #91: a selected layer outside the canvas shows at full brightness. The veil is one custom shape drawn first
 * in its non-listening layer, so its `destination-out` cut only removes the veil; the Schema switch gates it,
 * and the veil follows every redraw of the picture layer while something is selected (drag, transform).
 */
describe('selected layers outside the canvas undimmed (#91)', () => {
  const veil = board.indexOf('name="outside-dim"');

  it('draws the veil as the first shape of the non-listening dim layer and cuts the selection out of it', () => {
    expect(veil).toBeGreaterThan(0);
    const layer = enclosingLayer(veil);
    expect(layer.tag).toBe('<Layer ref={outsideDimLayerRef} listening={false}>');
    expect(board.slice(layer.start + layer.tag.length, veil)).toMatch(/^\s*\{artboard \? \(\s*<Shape\s*$/);
    const scene = board.slice(veil, board.indexOf('/>', board.indexOf('drawOutsideDim(', veil)));
    expect(scene).toContain('outsideDim.reveal_selected && selectedIds.length > 0');
    expect(scene).toContain('revealSteps(paintedNodes(stage, sorted.map((node) => node.id)), new Set(selectedIds), artboard)');
    expect(scene).toContain('konvaRevealPainter(stage, context.canvas, outsideDimScratch.current)');
  });

  it('redraws the veil with the picture layer while a selection can be revealed', () => {
    expect(board).toContain('<Layer ref={contentLayerRef}>');
    expect(board).toContain("content.on('draw.outsideDim', follow);");
    expect(board).toContain("content.off('draw.outsideDim', follow);");
    expect(board).toContain('if (!content || !veil || !revealSelected || !hasSelection) return;');
  });
});
