import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// #291: the board draws a zoomed-out layer from a reduced copy; every other reader keeps the original.
const board = readFileSync(new URL('./canvas-board.tsx', import.meta.url), 'utf8');

describe('layer display copies (#291)', () => {
  it('asks the cache for a display picture by device pixels shown per original pixel', () => {
    expect(board).toContain('const shownPerPixel = viewport.zoom * devicePixelRatio;');
    expect(board).toContain('node.width * shownPerPixel / (node.crop?.width ?? loadedSize.width)');
    expect(board).toContain('node.height * shownPerPixel / (node.crop?.height ?? loadedSize.height)');
    expect(board).toContain('images.display(node.imageUrl, ratio)');
    // The brush's local bitmap still wins over any picture of the cache.
    expect(board).toContain('const image = local ?? displayed ?? loaded;');
  });

  it('scales a crop to the copy it is drawn from', () => {
    expect(board).toContain('x: node.crop.x * cropScale,');
    expect(board).toContain('width: node.crop.width * cropScale,');
  });

  it('keeps the original for picking colours, sizes and painting', () => {
    expect(board).toContain('const source = brush.canvasFor(nodeId) ?? (node ? images.get(node.imageUrl) : undefined);');
    expect(board.match(/images\.display\(/g)).toHaveLength(1);
  });
});
