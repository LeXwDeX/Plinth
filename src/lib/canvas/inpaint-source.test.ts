import { describe, expect, it } from 'vitest';

import {
  inpaintMaskURL,
  isInpaintSource,
  isMaskAssetId,
  maskAssetIdFromURL,
} from './inpaint-source';

const schema = {
  runtime: { http: { routes: { asset_item: '/api/v1/assets/{assetID}' } } },
} as never;
const id = 'asset_0123456789abcdef0123456789abcdef';

describe('inpaint mask source', () => {
  it('accepts only platform input-asset IDs with positive integer pixels', () => {
    expect(isInpaintSource({ maskAssetId: id, width: 3840, height: 2160 })).toBe(true);
    for (const bad of [
      { maskAssetId: 'asset_short', width: 1, height: 1 },
      { maskAssetId: 'https://example.test/a.png', width: 1, height: 1 },
      { maskAssetId: 'data:image/png;base64,AA', width: 1, height: 1 },
      { maskAssetId: id, width: 0, height: 1 },
      { maskAssetId: id, width: 1.5, height: 1 },
      { maskAssetId: id, width: -1, height: 1 },
      { maskAssetId: id, width: 1, height: Number.NaN },
      null,
      {},
    ]) {
      expect(isInpaintSource(bad)).toBe(false);
    }
    expect(isMaskAssetId(id)).toBe(true);
    expect(isMaskAssetId(id.toUpperCase())).toBe(false);
  });

  it('builds the same-origin asset route from the schema template', () => {
    expect(inpaintMaskURL(schema, { maskAssetId: id, width: 3840, height: 2160 })).toBe(`/api/v1/assets/${id}`);
    expect(inpaintMaskURL({ runtime: { http: { routes: { asset_item: '/other/{assetID}' } } } } as never,
      { maskAssetId: id, width: 1, height: 1 })).toBe(`/other/${id}`);
    expect(inpaintMaskURL(schema, { maskAssetId: '../x', width: 1, height: 1 })).toBe('');
  });

  it('recovers the ID only from the declared asset route on this origin', () => {
    expect(maskAssetIdFromURL(schema, `/api/v1/assets/${id}`)).toBe(id);
    expect(maskAssetIdFromURL(schema, `/api/v1/assets/${id}?size=preview`)).toBe(id);
    expect(maskAssetIdFromURL(schema, `/api/v1/tasks/t/assets/${id}`)).toBeNull();
    expect(maskAssetIdFromURL(schema, `https://example.test/api/v1/assets/${id}`)).toBeNull();
    expect(maskAssetIdFromURL(schema, 'data:image/png;base64,AA')).toBeNull();
    expect(maskAssetIdFromURL(schema, `/api/v1/assets/not-an-asset`)).toBeNull();
  });
});
