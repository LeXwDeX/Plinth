import { readFileSync, readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

/**
 * Every entry that sends the board upstream goes through the one artboard rasteriser. A second path
 * (rasterising only the selected nodes) would deliver something other than what is on screen.
 */
describe('one artboard rasteriser', () => {
  it('has no selected-only raster path left', () => {
    const roots = ['../../components', '../../lib/canvas', '../../lib/workspace', '../../hooks'];
    const offenders: string[] = [];
    for (const root of roots) {
      const directory = new URL(`${root}/`, import.meta.url);
      for (const entry of readdirSync(directory, { recursive: true, encoding: 'utf8' })) {
        if (!/\.(ts|tsx)$/.test(entry) || /\.test\./.test(entry)) continue;
        if (/selectedOnly|selectedIds\s*:\s*\[/.test(readFileSync(new URL(entry, directory), 'utf8'))) offenders.push(`${root}/${entry}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  // #286: a path that rasterises the board and uploads it reports the failing step (send-snapshot),
  // not one generic "upload failed" for saving, exporting and uploading alike.
  it('sends every uploaded board picture through the staged snapshot steps', () => {
    const roots = ['../../components', '../../hooks'];
    const rasterises = /exportArtboardToBlob\(|rasteriseSessionBoard\(|rasteriseBoard\(/;
    const uploads = /uploadWorkspaceInputAsset\(|onUploadBlob\(|uploadBlob\(/;
    const offenders: string[] = [];
    let staged = 0;
    for (const root of roots) {
      const directory = new URL(`${root}/`, import.meta.url);
      for (const entry of readdirSync(directory, { recursive: true, encoding: 'utf8' })) {
        if (!/\.(ts|tsx)$/.test(entry) || /\.test\.|__tests__/.test(entry)) continue;
        const source = readFileSync(new URL(entry, directory), 'utf8');
        if (!rasterises.test(source) || !uploads.test(source)) continue;
        if (/exportAndUploadSnapshot\(/.test(source) && /reportSnapshotFailure\(/.test(source)) staged += 1;
        else offenders.push(`${root}/${entry}`);
      }
    }
    expect(offenders).toEqual([]);
    // chat-column, canvas-board, workspace-canvas-board, use-object-actions.
    expect(staged).toBe(4);
  });
});
