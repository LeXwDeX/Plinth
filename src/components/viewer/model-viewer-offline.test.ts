import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Intranet browsers have no internet: model-viewer's CDN defaults (gstatic Draco/Basis, jsDelivr
// Meshopt) must always be overridden with same-origin decoder paths served by the Go web build.
describe('model viewer decoder locations', () => {
  const source = readFileSync(new URL('./model-viewer-3d.tsx', import.meta.url), 'utf8');
  it('points every decoder at this origin and never at a CDN', () => {
    for (const key of ['dracoDecoderLocation', 'ktx2TranscoderLocation', 'meshoptDecoderLocation']) {
      expect(source).toMatch(new RegExp(`${key}: '/[^']+'`));
    }
    expect(source).not.toMatch(/https?:\/\//);
  });
});
