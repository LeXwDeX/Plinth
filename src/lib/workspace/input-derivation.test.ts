import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { inputDerivationMethodText, inputDerivationText, parseInputDerivations } from './input-derivation';
import { buildDetailRows, parseTaskDetail } from './task-detail';

const schema = defaultCatalog.application as unknown as ProductSchema;

const derivationWire = {
  parameter: 'reference_assets', index: 0, asset_id: 'asset_derived', media_type: 'image/jpeg', size: 1_890_974,
  width: 4096, height: 4096, method: 'jpeg-q95', source_asset_id: 'asset_source', source_media_type: 'image/png', source_size: 15_707_678,
};

describe('input derivations (#276)', () => {
  it('reads the wire and drops malformed entries', () => {
    expect(parseInputDerivations([derivationWire, { asset_id: '' }, null, 'x'])).toEqual([{
      parameter: 'reference_assets', index: 0, assetId: 'asset_derived', mediaType: 'image/jpeg', size: 1_890_974,
      width: 4096, height: 4096, method: 'jpeg-q95', sourceAssetId: 'asset_source', sourceMediaType: 'image/png', sourceSize: 15_707_678,
    }]);
    expect(parseInputDerivations(undefined)).toEqual([]);
  });

  it('names each method with Schema text, and an unknown one as recorded', () => {
    expect(inputDerivationMethodText(schema, 'jpeg-q95')).toBe('JPEG 质量 95');
    expect(inputDerivationMethodText(schema, 'flatten-jpeg-q80')).toBe('按模型要求铺底去掉透明，JPEG 质量 80');
    expect(inputDerivationMethodText(schema, 'lossless-png')).toBe('无损重压缩');
    expect(inputDerivationMethodText(schema, 'future-step')).toBe('future-step');
  });

  it('says what was sent instead of the source, with sizes and unchanged dimensions', () => {
    const [derivation] = parseInputDerivations([derivationWire]);
    expect(inputDerivationText(schema, derivation, '参考图 1')).toBe('参考图 1：原图 PNG 15.71 MB，按模型上限压缩为 JPEG 1.9 MB（JPEG 质量 95），4096×4096，宽高未变');
    const [unknown] = parseInputDerivations([{ ...derivationWire, width: 0, height: 0, source_asset_id: '', source_media_type: '' }]);
    expect(inputDerivationText(schema, unknown, '参考图 1')).toBe('参考图 1：原图 格式未知 15.71 MB，按模型上限压缩为 JPEG 1.9 MB（JPEG 质量 95），宽高未变');
  });

  it('adds one task detail row per derived image, labelled by the Offering parameter', () => {
    const offering = {
      id: 'gemini/gemini-3-pro-image/image-to-image', revision: 'r', provider_id: 'gemini', provider_name: 'Gemini', model_id: 'g', model_name: 'Gemini',
      capability: 'image-to-image', available: true,
      parameters: [{ name: 'prompt', label: 'Prompt', type: 'text', semantic: 'prompt' }, { name: 'reference_assets', label: '参考图', type: 'asset-array', semantic: 'reference-image' }],
    } as unknown as OfferingWire;
    const task = parseTaskDetail({ id: 't1', capability: 'image-to-image', state: 'SUCCEEDED', assets: [], input_derivations: [derivationWire, { ...derivationWire, index: 2, asset_id: 'asset_b' }] });
    const rows = buildDetailRows(schema, { task, offering }).filter((row) => row.key.startsWith('input_derivation.'));
    expect(rows.map((row) => [row.key, row.label])).toEqual([
      ['input_derivation.reference_assets.0', '参考图压缩'],
      ['input_derivation.reference_assets.2', '参考图压缩'],
    ]);
    expect(rows[1].value.startsWith('参考图 3：原图 PNG')).toBe(true);
    expect(parseTaskDetail({ id: 't2', state: 'RUNNING', assets: [] })?.inputDerivations).toEqual([]);
  });
});
