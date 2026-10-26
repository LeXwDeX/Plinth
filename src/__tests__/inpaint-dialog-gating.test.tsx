import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { InpaintDialog } from '@/components/workspace/canvas-image-actions';
import { readFileSync } from 'node:fs';
import { buildInpaintInput, buildModelPickerTable, inpaintParameters } from '@/lib/workspace/model-picker';

/**
 * 局部重绘对话框门控回归（R1-1 / R1-10）：
 * 所选模型未声明 mask → 显示不可用提示；
 * estimated_credits 缺失 → 显示「费用未知」而不是 0 积分；
 * 声明了积分 → 按 Schema 模板渲染。
 */

const catalogMessages = (
  defaultCatalog.application as {
    content?: { messages?: Record<string, string> };
  }
).content?.messages;

// The keys this change adds are injected here until the shared Catalog is updated by the parent.
Object.assign(catalogMessages ?? {}, {
  'ui.canvas.inpaint.output_size': '将按 {size} 输出，与画布和蒙版一致。',
  'ui.canvas.inpaint.output_size_nearest': '这个模型不能输出画布尺寸 {canvas}（最接近的合法尺寸是 {size}）。请换一个支持该尺寸的模型，或先把画布改成该尺寸。',
  'ui.canvas.inpaint.output_size_unconfirmed': '无法确认这个模型的输出尺寸与画布 {size} 一致，不能提交局部重绘。请选择支持该尺寸的模型。',
  'ui.canvas.inpaint.mask_scope': '蒙版属于整张画布（{canvas}）；不透明区域保留，透明区域重绘。',
  'ui.canvas.inpaint.mask_preview_alt': '局部重绘蒙版预览：不透明处保留，透明处重绘',
  'ui.canvas.inpaint.layer_size_mismatch': '重绘结果尺寸是 {actual}，与画布和蒙版 {expected} 不一致，未放到画板；结果可在任务中心查看。',
});

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: defaultCatalog.application,
    text: (key: string) => catalogMessages?.[key] ?? '',
  }),
}));

vi.mock('swr', () => ({ default: () => ({ data: undefined }) }));

vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  DialogTitle: ({ children }: { children?: React.ReactNode }) => <h3>{children}</h3>,
}));

function offeringWire(overrides: Partial<OfferingWire>): OfferingWire {
  return {
    id: 'offering-a',
    revision: '1',
    provider_id: 'p',
    provider_name: 'P',
    model_id: 'model-a',
    model_name: 'Model A',
    capability: 'image-to-image',
    available: true,
    estimated_credits: 12,
    credits_metered: true,
    parameters: [
      { name: 'instructions', type: 'text', semantic: 'prompt', label: 'Prompt', required: true },
      { name: 'picture', type: 'asset-array', semantic: 'reference-image', label: 'Picture', required: true },
      { name: 'region', type: 'asset', semantic: 'mask', label: 'Mask', required: true },
    ],
    ...overrides,
  };
}

function renderDialog(offerings: OfferingWire[], modelId: string | null, size = { width: 10, height: 10 }): string {
  return renderToStaticMarkup(
    <InpaintDialog
      request={{ imageUrl: '/api/v1/assets/a1', source: new Blob(), mask: new Blob(), frame: { x: 0, y: 0, width: size.width, height: size.height }, size }}
      offerings={offerings}
      modelId={modelId}
      uploadBlob={vi.fn(async () => '/api/v1/assets/new')}
      onClose={vi.fn()}
    />,
  );
}

function sizedOffering(options: string[], imageSize?: NonNullable<OfferingWire['parameters'][number]['image_size']>): OfferingWire {
  const base = offeringWire({});
  return {
    ...base,
    parameters: [
      ...base.parameters,
      {
        name: 'size', label: 'Size', type: 'select', semantic: 'output-size',
        options: options.map((value) => ({ value, label: value })),
        ...(imageSize ? { image_size: imageSize } : {}),
      },
    ],
  };
}

it('shows unavailable notice when the selected model has no mask offering', () => {
  const html = renderDialog([offeringWire({ model_id: 'model-a', parameters: [] })], 'model-a');
  expect(html).toContain(catalogMessages?.['ui.canvas.inpaint.missing_semantics']);
  expect(html).not.toContain('ui.canvas.inpaint.cost');
});

it('shows unknown-cost notice instead of 0 credits when estimated_credits is missing', () => {
  const html = renderDialog(
    [
      offeringWire({
        model_id: 'model-a',
        estimated_credits: undefined,
      }),
    ],
    'model-a',
  );
  expect(html).toContain(catalogMessages?.['ui.canvas.inpaint.cost_unknown']);
  expect(html).not.toContain('预计消耗 0 积分');
});

it('shows the configured per-credit cost when declared', () => {
  const html = renderDialog(
    [
      offeringWire({
        model_id: 'model-a',
      }),
    ],
    'model-a',
  );
  expect(html).toContain('预计消耗 12 积分');
});

it('shows no credits for a provider the Schema does not mark credits_metered', () => {
  const html = renderDialog(
    [
      offeringWire({
        model_id: 'model-a',
        credits_metered: undefined,
      }),
    ],
    'model-a',
  );
  expect(html).not.toContain('预计消耗 12 积分');
  expect(html).not.toContain(catalogMessages?.['ui.canvas.inpaint.cost_unknown']);
});

it('never falls back to a parameter name without a declared semantic', () => {
  const html = renderDialog(
    [
      offeringWire({
        model_id: 'model-a',
        parameters: [{ name: 'mask' } as OfferingWire['parameters'][number]],
      }),
    ],
    'model-a',
  );
  expect(html).toContain(catalogMessages?.['ui.canvas.inpaint.missing_semantics']);
});

it('builds renamed inputs and respects a single versus array reference', () => {
  const offering = offeringWire({});
  expect(buildInpaintInput(offering, ' edit ', 'a', 'm')).toEqual({ instructions: 'edit', picture: ['a'], region: 'm' });
  offering.parameters[1].type = 'asset';
  expect(buildInpaintInput(offering, 'edit', 'a', 'm')).toEqual({ instructions: 'edit', picture: 'a', region: 'm' });
});

it.each(['prompt', 'reference-image', 'mask'])('excludes inpaint when %s is missing', (semantic) => {
  const offering = offeringWire({});
  offering.parameters = offering.parameters.filter((parameter) => parameter.semantic !== semantic);
  expect(inpaintParameters(offering)).toBeNull();
  expect(buildModelPickerTable('image', { 'image-to-image': [offering] }, []).maskModels).toEqual([]);
  expect(() => buildInpaintInput(offering, 'edit', 'a', 'm')).toThrow();
});

it('guards inpaint input construction against literal parameter keys and fallbacks', () => {
  const dialog = readFileSync('src/components/workspace/canvas-image-actions.tsx', 'utf8');
  const inputBuilder = readFileSync('src/lib/workspace/model-picker.ts', 'utf8').split('export function buildInpaintInput')[1].split('export type PickerCellValue')[0];
  const inpaintPath = dialog.split('export function InpaintDialog')[1].split('export function BackgroundRemovalDialog')[0];
  expect(inpaintPath).toContain('buildInpaintInput(offering, prompt, referenceId, maskId, request.size)');
  for (const source of [inputBuilder, inpaintPath]) {
    expect(source).not.toMatch(/\b(reference_assets|mask)\s*:/);
    expect(source).not.toMatch(/\bprompt\s*:\s*prompt\.trim\(\)/);
    expect(source).not.toMatch(/\?\?\s*['"](?:prompt|reference_assets|mask)['"]/);
    expect(source).not.toMatch(/parameter\.name\s*===\s*['"](?:prompt|reference_assets|mask)['"]/);
  }
});

it('states the exact output size the picture and mask will use (#308)', () => {
  const html = renderDialog(
    [sizedOffering(['2048x2048'], { min_pixels: 655360, max_pixels: 8294400, max_edge: 3840, multiple: 16, max_ratio: 3 })],
    'model-a',
    { width: 3840, height: 2160 },
  );
  expect(html).toContain('将按 3840×2160 输出');
  expect(html).not.toContain('这个模型不能输出画布尺寸');
});

it('names the nearest legal size instead of pretending the canvas size will come back', () => {
  const html = renderDialog([sizedOffering(['1024x1024', '2048x2048'])], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('这个模型不能输出画布尺寸 3840×2160');
  expect(html).toContain('最接近的合法尺寸是 2048×2048');
});

it('refuses an unconfirmed size when the Offering has no output-size parameter', () => {
  const html = renderDialog([offeringWire({})], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('无法确认这个模型的输出尺寸与画布 3840×2160 一致');
  expect(html).toMatch(/\sdisabled=""/);
});

it('refuses a fixed size that does not prove the canvas pixels', () => {
  const fixed = offeringWire({});
  fixed.parameters = [...fixed.parameters, { name: 'size', label: 'Size', type: 'select', semantic: 'output-size', fixed: true, default: '2048x2048', options: [{ value: '2048x2048', label: '2048' }] }];
  const html = renderDialog([fixed], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('无法确认这个模型的输出尺寸与画布 3840×2160 一致');
  expect(html).toMatch(/\sdisabled=""/);
});

it('states the exact pixels when the declared size produces exactly the canvas pixels', () => {
  const html = renderDialog([sizedOffering(['3840x2160'])], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('将按 3840×2160 输出，与画布和蒙版一致。');
});

it('states the exact pixels when a ratio resolves to exactly the canvas pixels', () => {
  const html = renderDialog([sizedOffering(['16:9'])], 'model-a', { width: 1344, height: 768 });
  expect(html).toContain('将按 1344×768 输出，与画布和蒙版一致。');
});

it('refuses a ratio whose Schema pixels are not the canvas pixels', () => {
  const html = renderDialog([sizedOffering(['16:9'])], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('最接近的合法尺寸是 1344×768');
  expect(html).toMatch(/\sdisabled=""/);
});

it('says the mask belongs to the whole canvas and names its editable area', () => {
  const html = renderDialog([sizedOffering(['2048x2048'])], 'model-a', { width: 3840, height: 2160 });
  expect(html).toContain('蒙版属于整张画布（3840×2160）');
});
