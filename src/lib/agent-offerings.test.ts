import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from './product-schema';
import {
  fetchCapabilityOfferings,
  offeringFixedValues,
  offeringParamSchemas,
  pickDefaultOffering,
  selectableOptions,
  type OfferingWire,
} from './agent-offerings';

function offering(overrides: Partial<OfferingWire> = {}): OfferingWire {
  return {
    id: 'meshy/nano-banana-pro/text-to-image',
    revision: '2026-09-08.1',
    provider_id: 'meshy',
    provider_name: 'Meshy AI',
    model_id: 'nano-banana-pro',
    model_name: 'Nano Banana Pro',
    capability: 'text-to-image',
    available: true,
    recommended: true,
    priority: 50,
    estimated_credits: 6,
    parameters: [],
    outputs: [],
    ...overrides,
  };
}

describe('offeringParamSchemas', () => {
  it('maps wire parameters to renderable ParamSchema fields', () => {
    const wire = offering({
      parameters: [
        {
          name: 'aspect_ratio',
          label: '宽高比',
          type: 'select',
          default: '1:1',
          options: [{ value: '1:1', label: '1:1' }, { value: '16:9', label: '16:9' }],
          presentation: {
            surface: 'provider',
            control: 'select',
            disabled_when: [
              { parameter: 'generate_multi_view', operator: 'equals', value: true },
            ],
          },
        },
        {
          name: 'prompt',
          label: '提示词',
          type: 'text',
          required: true,
          max_length: 10000,
          semantic: 'prompt',
        },
        {
          name: 'seed',
          label: '种子',
          type: 'number',
          minimum: 0,
          maximum: 999999,
          integer: true,
          presentation: { surface: 'provider', control: 'number', step: 1 },
        },
        {
          name: 'internal_flag',
          label: '内部',
          type: 'boolean',
          presentation: { surface: 'hidden', control: 'boolean' },
        },
      ],
    });
    const schemas = offeringParamSchemas(wire);
    expect(schemas.map((item) => item.name)).toEqual(['aspect_ratio', 'prompt', 'seed']);
    const [select, text, number] = schemas;
    expect(select.default).toBe('1:1');
    expect(select.disabled_when).toEqual([
      { parameter: 'generate_multi_view', operator: 'equals', value: true },
    ]);
    expect(text.required).toBe(true);
    expect(text.validation).toMatchObject({ max_length: 10000 });
    expect(number).toMatchObject({ min: 0, max: 999999, step: 1 });
    expect(number.validation).toMatchObject({ integer: true });
  });

  it('keeps asset parameters with their upload validation', () => {
    const wire = offering({
      parameters: [
        {
          name: 'image',
          label: '参考图',
          type: 'asset',
          media_types: ['image/png'],
          max_bytes: 52428800,
          max_items: 4,
        },
      ],
    });
    const [asset] = offeringParamSchemas(wire);
    expect(asset.type).toBe('asset');
    expect(asset.validation).toMatchObject({
      media_types: ['image/png'],
      max_bytes: 52428800,
      max_items: 4,
    });
  });

  it('treats missing presentation as provider surface', () => {
    const wire = offering({
      parameters: [{ name: 'mode', label: '模式', type: 'text' }],
    });
    expect(offeringParamSchemas(wire)).toHaveLength(1);
  });
});

describe('pickDefaultOffering', () => {
  it('prefers the recommended available offering, then first available', () => {
    const first = offering({ id: 'a', available: true, recommended: false });
    const recommended = offering({ id: 'b', available: true, recommended: true });
    expect(pickDefaultOffering([first, recommended])?.id).toBe('b');
    expect(
      pickDefaultOffering([
        offering({ id: 'a', available: true, recommended: false }),
        offering({ id: 'b', available: false, recommended: true }),
      ])?.id,
    ).toBe('a');
    expect(pickDefaultOffering([offering({ available: false })])).toBeNull();
    expect(pickDefaultOffering([])).toBeNull();
  });
});

describe('fetchCapabilityOfferings', () => {
  it('requests the schema-declared offerings route scoped to the capability', async () => {
    const schema = {
      runtime: { http: { routes: { offerings: '/api/v1/offerings' } } },
    } as unknown as ProductSchema;
    const fetcher = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ offerings: [offering()] }),
    })) as unknown as typeof fetch;
    const offerings = await fetchCapabilityOfferings(
      schema,
      'text-to-image',
      fetcher,
    );
    expect(offerings).toHaveLength(1);
    expect(String((fetcher as unknown as { mock: { calls: string[][] } }).mock.calls[0][0])).toBe(
      '/api/v1/offerings?capability=text-to-image',
    );
  });

  it('throws on non-ok responses', async () => {
    const schema = {
      runtime: { http: { routes: { offerings: '/api/v1/offerings' } } },
    } as unknown as ProductSchema;
    const fetcher = vi.fn(async () => ({
      ok: false,
      status: 500,
      json: async () => ({}),
    })) as unknown as typeof fetch;
    await expect(
      fetchCapabilityOfferings(schema, 'text-to-image', fetcher),
    ).rejects.toThrow();
  });
});

describe('fixed parameters', () => {
  const formats = ['glb', 'fbx'].map((value) => ({ value, label: value }));
  const wire = offering({
    parameters: [
      { name: 'texture_resolution', label: '分辨率', type: 'select', default: '8k', fixed: true, options: [{ value: '8k', label: '8K' }, { value: '4k', label: '4K' }] },
      { name: 'enable_pbr', label: 'PBR', type: 'boolean', default: true, fixed: true },
      { name: 'target_formats', label: '输出格式', type: 'string-array', default: ['glb', 'fbx'], fixed: true, options: formats, min_items: 2, max_items: 2 },
      // Without Schema `fixed`, a single option or a full-set list is still a form field.
      { name: 'one_option', label: '唯一', type: 'select', options: [{ value: 'a', label: 'A' }] },
      { name: 'all_formats', label: '全部格式', type: 'string-array', options: formats, min_items: 2, max_items: 2 },
      { name: 'pick_formats', label: '可选格式', type: 'string-array', options: formats, min_items: 1, max_items: 2 },
      { name: 'quality', label: '质量', type: 'select', options: [{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }] },
    ],
  });

  it('keeps only Schema-fixed parameters out of the form', () => {
    expect(offeringParamSchemas(wire).map((item) => item.name)).toEqual(['one_option', 'all_formats', 'pick_formats', 'quality']);
  });

  it('still reports their values so a request carries them', () => {
    expect(offeringFixedValues(wire)).toEqual({
      texture_resolution: '8k', enable_pbr: true, target_formats: ['glb', 'fbx'],
    });
  });
});

it('keeps the public preservation policy and declared image bounds without choosing or truncating views', async () => {
  const wire = offering({ capability: 'image-to-3d', reference_selection_policy: 'preserve', parameters: [{ name: 'reference_assets', type: 'asset-array', label: 'Views', semantic: 'reference-image', min_items: 1, max_items: 4, media_types: ['image/png', 'image/jpeg', 'image/webp'], max_bytes: 20 * 1024 * 1024 }] });
  const schema = { runtime: { http: { routes: { offerings: '/api/v1/offerings' } } } } as unknown as ProductSchema;
  const values = await fetchCapabilityOfferings(schema, 'image-to-3d', vi.fn(async () => ({ ok: true, json: async () => ({ offerings: [wire] }) })) as unknown as typeof fetch);
  expect(values[0].reference_selection_policy).toBe('preserve');
  expect(offeringParamSchemas(values[0])[0].validation).toMatchObject({ min_items: 1, max_items: 4, max_bytes: 20 * 1024 * 1024 });
});

// #267: Seedream 4.5 keeps its low tiers in the Schema and forces each to the same ratio's highest one; the
// picker must not offer an option that never takes effect (the canvas then said the model outputs 4096×4096).
describe('selectableOptions', () => {
  const seedream = (defaultCatalog as unknown as { offerings: OfferingWire[] }).offerings
    .find((entry) => entry.id === 'jimeng/seedream-4-5/text-to-image')!;
  const size = seedream.parameters.find((wire) => wire.name === 'size')!;

  it('leaves out the Seedream low tiers that forced_when replaces, keeping every top tier', () => {
    expect(size.options?.some((option) => option.value === '2048x2048')).toBe(true);
    const shown = selectableOptions(size).map((option) => option.value);
    expect(shown).toEqual(['4096x4096', '4704x3520', '3520x4704', '5504x3040', '3040x5504']);
    expect(offeringParamSchemas(seedream).find((param) => param.name === 'size')?.options?.map((option) => option.value)).toEqual(shown);
  });

  it('keeps options that are forced only under another parameter or forced to themselves', () => {
    expect(selectableOptions({
      name: 'format', type: 'select', options: [{ value: 'png', label: 'png' }, { value: 'webp', label: 'webp' }],
      forced_when: [{ when: { parameter: 'background', operator: 'equals', value: 'transparent' }, value: 'png' }],
    } as never).map((option) => option.value)).toEqual(['png', 'webp']);
  });
});
