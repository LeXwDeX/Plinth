import { describe, expect, it } from 'vitest';

import type { OfferingWire } from '@/lib/agent-offerings';
import { buildGenerationParams, effectiveOfferingValues, readonlyParameterText, defaultParamsChipText, deliverySize, staleSizeValues, estimateCredits, offeringForTurn, paramsChipText, paramsSummary, ratioOfSize, sizeOptionText, skillSizeDefault } from './generation-params';

const option = (...values: string[]) => values.map((value) => ({ value, label: value }));

const gpt = (capability: string, extra: object[] = []): OfferingWire => ({
  id: `openai/gpt-image-2/${capability}`, revision: 'r', provider_id: 'openai', provider_name: 'OpenAI',
  model_id: 'gpt-image-2', model_name: 'GPT Image 2', capability, available: true, estimated_credits: 6, credits_metered: true,
  estimated_credits_unit_parameter: 'n',
  parameters: [
    { name: 'prompt', label: 'Prompt', type: 'text', semantic: 'prompt' },
    { name: 'size', label: '输出尺寸', type: 'text', semantic: 'output-size', options: option('auto', '1024x1024', '1536x1024'), default: 'auto', presentation: { surface: 'provider' } },
    { name: 'n', label: '生成数量', type: 'number', default: 1, minimum: 1, maximum: 10, integer: true, presentation: { surface: 'provider' } },
    { name: 'output_format', label: '格式', type: 'select', options: option('png', 'jpeg'), default: 'png', presentation: { surface: 'provider' } },
    ...extra,
  ],
}) as unknown as OfferingWire;

const seedream: OfferingWire = {
  id: 'jimeng/seedream-4-5/text-to-image', revision: 'r', provider_id: 'jimeng', provider_name: 'Jimeng',
  model_id: 'seedream-4-5', model_name: 'Seedream', capability: 'text-to-image', available: true, estimated_credits: 6, credits_metered: true,
  estimated_credits_unit_parameter: 'max_images',
  parameters: [
    { name: 'size', label: '输出尺寸', type: 'text', semantic: 'output-size', options: option('2K', '4K'), default: '2K', presentation: { surface: 'provider' } },
    { name: 'sequential_image_generation', label: '组图', type: 'select', options: option('disabled', 'auto'), default: 'disabled', presentation: { surface: 'provider' } },
    { name: 'max_images', label: '最多生成数量', type: 'number', default: 1, minimum: 1, maximum: 15, integer: true,
      omit_when: [{ parameter: 'sequential_image_generation', operator: 'equals', value: 'disabled' }],
      presentation: { surface: 'provider', disabled_when: [{ parameter: 'sequential_image_generation', operator: 'equals', value: 'disabled' }] } },
    { name: 'watermark', label: '水印', type: 'boolean', default: false, presentation: { surface: 'provider' } },
  ],
} as unknown as OfferingWire;

const gemini: OfferingWire = {
  id: 'gemini/g/text-to-image', revision: 'r', provider_id: 'gemini', provider_name: 'Gemini', model_id: 'g', model_name: 'G',
  capability: 'text-to-image', available: true, estimated_credits: 6, credits_metered: true,
  parameters: [
    { name: 'aspect_ratio', label: '画面比例', type: 'select', semantic: 'output-size', options: option('1:1', '16:9'), default: '1:1', presentation: { surface: 'canvas' } },
  ],
} as unknown as OfferingWire;

describe('the extra-credit consent flag', () => {
  it('is not a setting: a step with extra credits asks on its approval card', () => {
    const tripo = { ...gpt('text-to-3d', [{ name: 'authorize_extra_credits', label: '同意额外积分消耗', type: 'boolean', default: false, presentation: { surface: 'provider' } }]), extra_credit_consent_parameter: 'authorize_extra_credits' } as OfferingWire;
    const model = buildGenerationParams([tripo]);
    expect(model.moreParams.map((param) => param.name)).not.toContain('authorize_extra_credits');
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('authorize_extra_credits');
  });
});

describe('buildGenerationParams', () => {
  it('splits size, count and the rest from the Offering declaration', () => {
    const model = buildGenerationParams([gpt('text-to-image')]);
    expect(model.sizeParams.map((param) => param.name)).toEqual(['size']);
    expect(model.countParam?.name).toBe('n');
    expect(model.moreParams.map((param) => param.name)).toEqual(['output_format']);
    // the prompt is the conversation's, never a setting
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('prompt');
  });

  it('shows only what the model supports: no count for a per-request model, aspect ratio from the canvas surface', () => {
    const model = buildGenerationParams([gemini]);
    expect(model.countParam).toBeNull();
    expect(model.sizeParams.map((param) => param.name)).toEqual(['aspect_ratio']);
    expect(buildGenerationParams([])).toMatchObject({ sizeParams: [], countParam: null, paramSchemas: [] });
  });

  it('uses the exact first Offering rather than merging capability semantics and pulls in count dependencies', () => {
    const withMask = gpt('image-to-image', [{ name: 'mask', label: 'Mask', type: 'asset', semantic: 'mask' }, { name: 'only_here', label: 'x', type: 'boolean' }]);
    const model = buildGenerationParams([gpt('text-to-image'), withMask]);
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('mask');
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('only_here');
    expect(buildGenerationParams([withMask]).paramSchemas.map((param) => param.name)).toContain('only_here');
    const seed = buildGenerationParams([seedream]);
    expect(seed.countParam?.name).toBe('max_images');
    expect(seed.countDependencies.map((param) => param.name)).toEqual(['sequential_image_generation']);
    expect(seed.moreParams.map((param) => param.name)).toEqual(['watermark']);
  });
});

describe('estimateCredits', () => {
  it('multiplies the unit price by the count and follows it', () => {
    expect(estimateCredits(gpt('text-to-image'), { n: 1 })).toEqual({ unit: 6, count: 1, total: 6, metered: true });
    expect(estimateCredits(gpt('text-to-image'), { n: 4 })).toEqual({ unit: 6, count: 4, total: 24, metered: true });
    expect(estimateCredits(gpt('text-to-image'), {})).toMatchObject({ count: 1, total: 6, metered: true });
  });

  it('floors, clamps and falls back to one for a bad count', () => {
    expect(estimateCredits(gpt('text-to-image'), { n: 3.9 }).count).toBe(3);
    expect(estimateCredits(gpt('text-to-image'), { n: 99 }).count).toBe(10);
    expect(estimateCredits(gpt('text-to-image'), { n: 0 }).count).toBe(1);
    expect(estimateCredits(gpt('text-to-image'), { n: 'abc' }).count).toBe(1);
  });

  it('counts one when the request leaves the count out', () => {
    expect(estimateCredits(seedream, { max_images: 5, sequential_image_generation: 'disabled' }).total).toBe(6);
    expect(estimateCredits(seedream, { max_images: 5, sequential_image_generation: 'auto' }).total).toBe(30);
    expect(estimateCredits(seedream, { max_images: 5 }).total).toBe(6); // default is disabled
  });

  it('reports no estimate for an Offering that declares no price', () => {
    expect(estimateCredits(undefined, {}).total).toBeNull();
    expect(estimateCredits({ ...gemini, estimated_credits: undefined } as OfferingWire, {}).total).toBeNull();
    expect(estimateCredits(gemini, {})).toEqual({ unit: 6, count: 1, total: 6, metered: true });
  });

  it('shows no price for an Offering the Schema does not mark credits_metered', () => {
    const unmetered = { ...gemini, credits_metered: undefined } as OfferingWire;
    expect(estimateCredits(unmetered, {})).toEqual({ unit: null, count: 1, total: null, metered: false });
  });
});

describe('offeringForTurn and skillSizeDefault', () => {
  const text = gpt('text-to-image');
  const image = gpt('image-to-image', [{ name: 'reference_assets', label: 'ref', type: 'asset-array', semantic: 'reference-image' }]);
  it('prices the Offering that takes the turn’s inputs', () => {
    expect(offeringForTurn([text, image], true)?.capability).toBe('image-to-image');
    expect(offeringForTurn([text, image], false)?.capability).toBe('text-to-image');
    expect(offeringForTurn([text], true)?.capability).toBe('text-to-image');
    expect(offeringForTurn([], true)).toBeUndefined();
  });

  it('applies a Skill size only when the model lists it as an option', () => {
    const { sizeParams } = buildGenerationParams([text]);
    expect(skillSizeDefault(sizeParams, '1536x1024')).toEqual({ name: 'size', value: '1536x1024' });
    expect(skillSizeDefault(sizeParams, '4096x4096')).toBeNull();
    expect(skillSizeDefault(sizeParams, '')).toBeNull();
  });
});

describe('paramsSummary', () => {
  it('names the chosen sizes and the count only above one', () => {
    const model = buildGenerationParams([gpt('text-to-image')]);
    const countText = (count: number) => `×${count}`;
    expect(paramsSummary(model, {}, { count: 1 }, countText)).toBe('auto');
    expect(paramsSummary(model, { size: '1536x1024', n: 3 }, { count: 3 }, countText)).toBe('1536x1024 · ×3');
    expect(paramsSummary(buildGenerationParams([gemini]), {}, { count: 1 }, countText)).toBe('1:1');
    expect(paramsSummary(buildGenerationParams([]), {}, { count: 1 }, countText)).toBe('');
  });
});

describe('sizeOptionText', () => {
  const template = '{width}×{height} · {ratio}';
  it('adds the reduced ratio to a pixel size and keeps every other label', () => {
    expect(sizeOptionText({ value: '3840x2160', label: '3840x2160' }, template)).toBe('3840×2160 · 16:9');
    expect(sizeOptionText({ value: '1536x1024', label: '1536x1024' }, template)).toBe('1536×1024 · 3:2');
    expect(sizeOptionText({ value: '1152x1536', label: '1152x1536' }, template)).toBe('1152×1536 · 3:4');
    expect(sizeOptionText({ value: '2048x2048', label: '2048×2048' }, template)).toBe('2048×2048 · 1:1');
    expect(sizeOptionText({ value: 'auto', label: '自动' }, template)).toBe('自动');
    expect(sizeOptionText({ value: '16:9', label: '16:9' }, template)).toBe('16:9');
    expect(sizeOptionText({ value: '2K', label: '2K' }, template)).toBe('2K');
    expect(sizeOptionText({ value: '1024x1024', label: '方图' }, template)).toBe('方图');
    // A Schema without the template keeps the Offering's own label instead of a blank row.
    expect(sizeOptionText({ value: '3840x2160', label: '3840x2160' }, '')).toBe('3840x2160');
  });
});

describe('ratioOfSize', () => {
  it('names the ratio of a pixel size the way an artist would', () => {
    expect(ratioOfSize('1024x1536')).toBe('2:3');
    expect(ratioOfSize('1536x864')).toBe('16:9');
    expect(ratioOfSize('2048 × 1152')).toBe('16:9');
    // Seedream 4K presets sit just off the named ratios; they must still read as them.
    expect(ratioOfSize('5504x3040')).toBe('16:9');
    expect(ratioOfSize('4704x3520')).toBe('4:3');
    expect(ratioOfSize('1024x1024')).toBe('1:1');
    expect(ratioOfSize('1536x1152')).toBe('4:3');
    // not exactly 16:9 but what it is called
    expect(ratioOfSize('1920x1088')).toBe('16:9');
  });

  it('has no ratio for text that is not a size', () => {
    expect(ratioOfSize('auto')).toBeNull();
    expect(ratioOfSize('2K')).toBeNull();
    expect(ratioOfSize('0x10')).toBeNull();
  });
});

describe('paramsChipText', () => {
  const countText = (count: number) => `${count} 张`;
  const model = buildGenerationParams([gpt('text-to-image')]);

  it('gives the ratio, the size as the model words it and the count, always', () => {
    expect(paramsChipText(model, { size: '1024x1536', n: 6 }, { count: 6 }, countText)).toBe('2:3 · 1024x1536 · 6 张');
    expect(paramsChipText(model, { size: '1024x1024', n: 4 }, { count: 4 }, countText)).toBe('1:1 · 1024x1024 · 4 张');
    // a count of one is still said: the person sees what a turn asks for
    expect(paramsChipText(model, {}, { count: 1 }, countText)).toBe('auto · 1 张');
  });

  it('says only the ratio for a model that has no count', () => {
    expect(paramsChipText(buildGenerationParams([gemini]), {}, { count: 1 }, countText)).toBe('1:1');
    expect(paramsChipText(buildGenerationParams([]), {}, { count: 1 }, countText)).toBe('');
  });

  it('says the product defaults before a model is chosen', () => {
    expect(defaultParamsChipText('1:1', 4, countText)).toBe('1:1 · 4 张');
  });
});

describe('deliverySize', () => {
  const size = (name: string, defaultValue?: string) => ({ name, label: name, type: 'select', default: defaultValue }) as Parameters<typeof deliverySize>[0][number];

  it('takes the ratio and the pixels from a chosen pixel size', () => {
    expect(deliverySize([size('size', 'auto')], { size: '1536x1024' })).toEqual({ ratio: '3:2', pixels: { width: 1536, height: 1024 } });
    expect(deliverySize([size('size')], { size: '1024x1536' })).toEqual({ ratio: '2:3', pixels: { width: 1024, height: 1536 } });
  });

  it('takes a ratio option as it is, with no pixels', () => {
    expect(deliverySize([size('aspect_ratio', '1:1')], { aspect_ratio: '16:9' })).toEqual({ ratio: '16:9', pixels: null });
  });

  it('falls back to the default when the value is auto, and to nothing when that is auto too', () => {
    expect(deliverySize([size('size', '1024x1024')], { size: 'auto' })).toEqual({ ratio: '1:1', pixels: { width: 1024, height: 1024 } });
    expect(deliverySize([size('size', 'auto')], { size: 'auto' })).toEqual({ ratio: null, pixels: null });
    expect(deliverySize([], {})).toEqual({ ratio: null, pixels: null });
  });

  it('lets a later size parameter give the pixels when the first only gives a ratio', () => {
    expect(deliverySize([size('aspect_ratio'), size('size')], { aspect_ratio: '9:16', size: '1152x2048' }))
      .toEqual({ ratio: '9:16', pixels: { width: 1152, height: 2048 } });
  });
});

describe('staleSizeValues', () => {
  const seedream = {
    name: 'size', label: 'size', type: 'text', default: '2K',
    options: ['2K', '4K', '2048x2048', '2560x1440', '1440x2560'].map((value) => ({ value, label: value })),
  } as Parameters<typeof staleSizeValues>[0][number];

  it('replaces a size another model offered, keeping its ratio when it can', () => {
    expect(staleSizeValues([seedream], { size: '1024x1024' })).toEqual({ size: '2048x2048' });
    expect(staleSizeValues([seedream], { size: '1920x1080' })).toEqual({ size: '2560x1440' });
  });

  it('falls back to the default when no option has the ratio', () => {
    expect(staleSizeValues([seedream], { size: '1536x1024' })).toEqual({ size: '2K' });
  });

  it('leaves offered, empty and option-less values alone', () => {
    expect(staleSizeValues([seedream], { size: '4K' })).toEqual({});
    expect(staleSizeValues([seedream], {})).toEqual({});
    expect(staleSizeValues([{ ...seedream, options: [] }], { size: '1024x1024' })).toEqual({});
  });
});

describe('public read-only and effective parameter values', () => {
  const offering = gpt('text-to-image', [
    { name: 'quality', label: '雕刻精度', type: 'select', fixed: true, default: 'ultra', options: [{ value: 'ultra', label: '极精细' }], presentation: { surface: 'readonly' }, forced_when: [{ when: { parameter: 'n', operator: 'equals', value: 2 }, value: 'fine' }] },
    { name: 'internal', label: 'Secret setting', type: 'text', fixed: true, default: 'secret', presentation: { surface: 'hidden' } },
    { name: 'private_fixed', label: 'Unpublished', type: 'text', fixed: true, default: 'private' },
  ]);
  it('exposes only explicitly public fixed values, never hidden or merely noneditable settings', () => {
    const model = buildGenerationParams([offering]);
    expect(model.readonlyParams.map((param) => param.name)).toEqual(['quality']);
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('quality');
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('internal');
  });
  it('uses declared enum text and fixed/forced defaults even when saved choices try to downgrade', () => {
    const values = effectiveOfferingValues(offering, { quality: 'low', size: 'unrelated-size', n: 1 });
    expect(values.quality).toBe('ultra'); expect(values.size).toBe('auto');
    expect(readonlyParameterText(offering.parameters.find((param) => param.name === 'quality')!, values)).toBe('极精细');
    expect(effectiveOfferingValues(offering, { quality: 'low', n: 2 }).quality).toBe('fine');
  });
  it('does not keep unsupported same-name parameters from another Offering', () => {
    expect(effectiveOfferingValues(gemini, { size: '4K', n: 5, aspect_ratio: '16:9' })).toEqual({ aspect_ratio: '16:9' });
  });
});

describe('array and boolean option validation', () => {
  const offering = gpt('text-to-image', [
    { name: 'formats', label: 'Formats', type: 'string-array', default: ['png'], options: [{ value: 'png', label: 'PNG' }, { value: 'jpeg', label: 'JPEG' }] },
    { name: 'flag', label: 'Flag', type: 'boolean', default: false, options: [{ value: 'true', label: 'On' }, { value: 'false', label: 'Off' }] },
  ]);
  it('preserves all supported array members and typed boolean choices', () => {
    expect(readonlyParameterText(offering.parameters.find((param) => param.name === 'formats')!, { formats: ['png', 'jpeg'] }, ' / ')).toBe('PNG / JPEG');
    expect(effectiveOfferingValues(offering, { formats: ['png', 'jpeg'], flag: true })).toMatchObject({ formats: ['png', 'jpeg'], flag: true });
    expect(effectiveOfferingValues(offering, { formats: ['png', 'foreign'] }).formats).toEqual(['png']);
  });
});
