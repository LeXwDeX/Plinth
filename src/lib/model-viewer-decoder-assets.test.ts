import { describe, expect, it } from 'vitest';
import { validateDecoderAssets } from '../../scripts/test-model-viewer-decoder-assets.mjs';

describe('model-viewer decoder assets', () => {
  it('ships the Draco and Basis payloads with their exact Apache-2.0 licences', () => {
    // three's package is MIT; these bundled decoders are Apache-2.0. A missing or altered
    // licence must fail the normal `pnpm test` gate, not only the manual asset script.
    expect(validateDecoderAssets()).toBe(7);
  });
});
