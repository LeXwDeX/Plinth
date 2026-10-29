import { describe, expect, it } from 'vitest';

import catalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { buildInpaintInput, buildModelPickerTable, pickerTriggerNames } from '@/lib/workspace/model-picker';

function offering(overrides: Partial<OfferingWire>): OfferingWire {
  return {
    id: 'p/m/text-to-image', revision: '1', provider_id: 'p', provider_name: 'P', model_id: 'm', model_name: 'M',
    capability: 'text-to-image', available: true, parameters: [], outputs: [], ...overrides,
  };
}

describe('2D model table', () => {
  const offerings = {
    'text-to-image': [
      offering({ id: 'a/t', provider_id: 'a', provider_name: 'Alpha', model_id: 'x', model_name: 'X', estimated_credits: 6, credits_metered: true }),
      offering({ id: 'b/t', provider_id: 'b', provider_name: 'Beta', model_id: 'y', model_name: 'Y', estimated_credits: 4, credits_metered: true, recommended: true }),
      offering({ id: 'd/t', provider_id: 'd', provider_name: 'Delta', model_id: 'w', model_name: 'W', estimated_credits: 3 }),
      offering({ id: 'c/t', provider_id: 'c', provider_name: 'Gamma', model_id: 'z', model_name: 'Z', available: false }),
    ],
    'image-to-image': [
      offering({ id: 'a/i', capability: 'image-to-image', provider_id: 'a', provider_name: 'Alpha', model_id: 'x', model_name: 'X', estimated_credits: 8, credits_metered: true,
        parameters: [{ name: 'mask', label: 'Mask', type: 'asset', semantic: 'mask' },
          { name: 'instruction', label: 'Prompt', type: 'text', semantic: 'prompt' },
          { name: 'image', label: 'Image', type: 'asset', semantic: 'reference-image' }] }),
    ],
  };

  it('ticks only what the available Offerings declare and shows the cheapest credits', () => {
    const table = buildModelPickerTable('image', offerings, null);
    const x = table.rows.find((row) => row.modelId === 'x')!;
    expect(x.cells).toEqual([{ kind: 'tick', on: true }, { kind: 'tick', on: true }, { kind: 'credits', credits: 6, metered: true }]);
    const y = table.rows.find((row) => row.modelId === 'y')!;
    expect(y.cells).toEqual([{ kind: 'tick', on: false }, { kind: 'tick', on: false }, { kind: 'credits', credits: 4, metered: true }]);
    expect(y.recommended).toBe(true);
    const w = table.rows.find((row) => row.modelId === 'w')!;
    expect(w.cells[2]).toEqual({ kind: 'credits', credits: null, metered: false });
    expect(table.maskModels).toEqual(['X']);
  });

  it('keeps unavailable models as dimmed rows after the usable ones', () => {
    const table = buildModelPickerTable('image', offerings, null);
    expect(table.rows.map((row) => `${row.modelId}:${row.enabled}`)).toEqual(['x:true', 'y:true', 'w:true', 'z:false']);
  });
});

describe('3D model table', () => {
  it('prices each way in and ticks re-texturing by the provider that offers it', () => {
    const table = buildModelPickerTable(
      'model-3d',
      {
        'text-to-3d': [offering({ id: 'm/t', capability: 'text-to-3d', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'm7', model_name: 'Meshy 7', estimated_credits: 25, credits_metered: true })],
        'image-to-3d': [
          offering({ id: 'm/i', capability: 'image-to-3d', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'm7', model_name: 'Meshy 7', estimated_credits: 35, credits_metered: true }),
          offering({ id: 't/i', capability: 'image-to-3d', provider_id: 'tripo', provider_name: 'Tripo', model_id: 't3', model_name: 'Tripo 3', estimated_credits: 20, credits_metered: true }),
        ],
      },
      [offering({ id: 'm/r', capability: 'retexture', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'retex' })],
    );
    expect(table.rows[0].cells).toEqual([
      { kind: 'credits', credits: 25, metered: true }, { kind: 'credits', credits: 35, metered: true }, { kind: 'tick', on: true },
    ]);
    expect(table.rows[1].cells).toEqual([
      { kind: 'credits', credits: null, metered: true }, { kind: 'credits', credits: 20, metered: true }, { kind: 'tick', on: false },
    ]);
    expect(table.retextureProviders).toEqual(['Meshy']);
  });
});

it('keeps native multiview separate from the single-image model and shows its supplied quote as a bare number', () => {
 const multiview = offering({ id: 'meshy/meshy-7.1-multiview/image-to-3d', provider_id: 'meshy', model_id: 'meshy-7.1-multiview', model_name: 'Meshy 7.1 多视图', capability: 'image-to-3d', price_mode: 'native', credits_metered: true, reference_selection_policy: 'preserve', default_native_quote: { version: 1, unit: 'meshy.credit', unit_label: 'Meshy credits', base_amount: '20', extra_amount: '20', total_amount: '40', components: [{ id: 'texture', label: '纹理', amount: '10' }, { id: 'texture_8k', label: '8K', amount: '5' }, { id: 'ultra_geometry', label: 'Ultra', amount: '5' }] } });
 const single = offering({ id: 'meshy/meshy-7.1/image-to-3d', provider_id: 'meshy', model_id: 'meshy-7.1', capability: 'image-to-3d', estimated_credits: 40, credits_metered: true });
 const table = buildModelPickerTable('model-3d', { 'image-to-3d': [single, multiview] }, []);
 expect(table.rows).toHaveLength(2);
 expect(table.rows[1].modelId).toBe('meshy-7.1-multiview');
 expect(table.rows[1].enabled).toBe(true);
 // #235: the picker cell shows only the amount; the unit name made cells truncate ("40 M…").
 expect(table.rows[1].cells[1]).toEqual({ kind: 'native-price', price: '40' });
});

describe('model table vendor order and hidden Offerings (#100)', () => {
  const rows = (providers: string[]) => buildModelPickerTable('model-3d', {
    'text-to-3d': providers.map((provider, index) => offering({
      id: `${provider}/${index}`, capability: 'text-to-3d', provider_id: provider, provider_name: provider, model_id: `${provider}-${index}`, model_name: `${provider}-${index}`,
      provider_order: ({ meshy: 1, tripo: 2, hyper3d: 3 } as Record<string, number>)[provider],
    })),
  }, null).rows.map((row) => row.modelId);

  it('groups the rows by the Schema vendor order and keeps the catalog order inside a vendor', () => {
    expect(rows(['hyper3d', 'tripo', 'meshy', 'tripo', 'meshy'])).toEqual(['meshy-2', 'meshy-4', 'tripo-1', 'tripo-3', 'hyper3d-0']);
  });

  it('puts vendors without a declared order after the ranked ones, in catalog order', () => {
    expect(rows(['zeta', 'tripo', 'alpha', 'meshy'])).toEqual(['meshy-3', 'tripo-1', 'zeta-0', 'alpha-2']);
  });

  it('lists none of the Creative Lab Offerings the shipped Schema declares disabled, vendors in Schema order', () => {
    const rank = new Map<string, number>();
    for (const entry of catalog.offerings as Array<{ provider_id: string; provider_order?: number }>) {
      if (entry.provider_order) rank.set(entry.provider_id, entry.provider_order);
    }
    const offered = (catalog.offerings as Array<Record<string, any>>).filter((entry) => entry.enabled);
    expect(offered.some((entry) => String(entry.id).startsWith('meshy/creative-lab/'))).toBe(false);
    const byCapability: Record<string, OfferingWire[]> = {};
    for (const entry of offered.filter((item) => item.domain === 'model-3d')) {
      (byCapability[entry.capability] ??= []).push(offering({
        id: entry.id, capability: entry.capability, provider_id: entry.provider_id, provider_name: entry.provider_name,
        model_id: entry.model_id, model_name: entry.model_name, provider_order: rank.get(entry.provider_id),
      }));
    }
    const table = buildModelPickerTable('model-3d', byCapability, null);
    expect(table.rows.some((row) => row.modelId.startsWith('creative-lab-'))).toBe(false);
    const vendors = table.rows.map((row) => row.providerId);
    const firstSeen = [...new Set(vendors)];
    expect(vendors).toEqual([...vendors].sort((left, right) => firstSeen.indexOf(left) - firstSeen.indexOf(right)));
    expect(firstSeen.slice(0, 3)).toEqual(['meshy', 'tripo', 'hyper3d']);
  });
});

describe('inpaint input output size (#308)', () => {
  const gptInpaint = (catalog.offerings as unknown as OfferingWire[])
    .find((entry) => entry.id === 'openai/gpt-image-2/image-to-image')!;

  it.each([
    [{ width: 3840, height: 2160 }, '3840x2160'],
    [{ width: 2048, height: 2048 }, '2048x2048'],
    [{ width: 2160, height: 3840 }, '2160x3840'],
  ])('sends the canvas size explicitly when the Offering accepts %o', (size, value) => {
    const input = buildInpaintInput(gptInpaint, ' edit ', 'ref', 'mask', size);
    expect(input).toMatchObject({
      prompt: 'edit',
      reference_assets: ['ref'],
      mask: 'mask',
      size: value,
    });
  });

  it('leaves the size to the Offering when no canvas size is given', () => {
    expect(buildInpaintInput(gptInpaint, 'edit', 'ref', 'mask')).not.toHaveProperty('size');
  });

  it('does not invent a size for an Offering without a choosable output-size parameter', () => {
    const noSize = offering({ id: 'no-size', capability: 'image-to-image', parameters: [
      { name: 'prompt', label: 'Prompt', type: 'text', semantic: 'prompt' },
      { name: 'reference_assets', label: 'Picture', type: 'asset-array', semantic: 'reference-image' },
      { name: 'mask', label: 'Mask', type: 'asset', semantic: 'mask' },
    ] });
    expect(buildInpaintInput(noSize, 'edit', 'ref', 'mask', { width: 3840, height: 2160 })).not.toHaveProperty('size');
  });

  it('applies the Offering forced size before sending it', () => {
    const forced = offering({ id: 'forced-size', capability: 'image-to-image', parameters: [
      { name: 'prompt', label: 'Prompt', type: 'text', semantic: 'prompt' },
      { name: 'reference_assets', label: 'Picture', type: 'asset-array', semantic: 'reference-image' },
      { name: 'mask', label: 'Mask', type: 'asset', semantic: 'mask' },
      { name: 'size', label: 'Size', type: 'select', semantic: 'output-size', options: [{ value: '2048x2048', label: '2K' }],
        forced_when: [{ when: { parameter: 'size', operator: 'equals', value: '2048x2048' }, value: '4096x4096' }] },
    ] });
    expect(buildInpaintInput(forced, 'edit', 'ref', 'mask', { width: 2048, height: 2048 })).toMatchObject({ size: '4096x4096' });
  });
});

// #324: the trigger showed only the vendor short names ("MESHY · MESHY"), so two Meshy-served 2D
// models looked the same. It shows the chosen model's name; the vendor short name joins it only
// when another row of the same table has that name. The hover names model and provider.
describe('picker trigger names (#324)', () => {
  const templates = { shortWithProvider: '{provider} {model}', fullWithProvider: '{model}（{provider}）' };
  const table = buildModelPickerTable('image', {
    'text-to-image': [
      offering({ id: 'meshy/nb', provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY', model_id: 'nano-banana-pro', model_name: 'Nano Banana Pro (高质量)' }),
      offering({ id: 'meshy/nb2', provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY', model_id: 'nano-banana-2', model_name: 'Nano Banana 2 (新版)' }),
      offering({ id: 'gemini/x', provider_id: 'gemini', provider_name: 'Google Gemini', provider_short_label: 'GEMINI', model_id: 'shared', model_name: 'Shared Name' }),
      offering({ id: 'other/x', provider_id: 'other', provider_name: 'Other', provider_short_label: 'OTHER', model_id: 'shared-2', model_name: 'Shared Name' }),
    ],
  }, null);
  const row = (id: string) => table.rows.find((item) => item.modelId === id)!;

  it('names the model, not the vendor', () => {
    expect(pickerTriggerNames(row('nano-banana-pro'), table.rows, templates)).toEqual({
      short: 'Nano Banana Pro (高质量)', full: 'Nano Banana Pro (高质量)（Meshy AI）',
    });
    expect(pickerTriggerNames(row('nano-banana-2'), table.rows, templates).short).toBe('Nano Banana 2 (新版)');
  });

  it('adds the vendor short name only when two models of the table share the name', () => {
    expect(pickerTriggerNames(row('shared'), table.rows, templates).short).toBe('GEMINI Shared Name');
    expect(pickerTriggerNames(row('shared-2'), table.rows, templates).short).toBe('OTHER Shared Name');
  });
});
