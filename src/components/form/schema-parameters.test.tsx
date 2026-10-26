import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ParamSchema } from '@/lib/providers/types';
import { isBooleanChoice, SchemaParameters, schemaParameterValues } from './schema-parameters';

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => key }),
}));
afterEach(() => vi.unstubAllGlobals());

it('renders a newly declared numeric parameter and editable size presets without model-specific code', () => {
  vi.stubGlobal('React', React);
  const parameters: ParamSchema[] = [
    { name: 'future', label: '新增参数', type: 'number', min: 1, max: 10, validation: { type: 'number', integer: true } },
    { name: 'size', label: '尺寸', type: 'text', options: [{ value: '1024x1024', label: '正方形' }] },
  ];
  const values = { future: '3', size: '2048x1152', unknown: 'ignored' };
  const html = renderToStaticMarkup(<SchemaParameters parameters={parameters} values={values} onChange={() => {}} />);
  expect(html).toContain('新增参数');
  expect(html).toContain('type="number"');
  expect(html).toContain('step="1"');
  expect(html).toContain('<datalist');
  expect(html).toContain('value="2048x1152"');
  expect(schemaParameterValues(parameters, values)).toEqual({ future: 3, size: '2048x1152' });
});

it('omits disabled parameters while preserving false and zero', () => {
  const parameters: ParamSchema[] = [
    { name: 'multi_view', label: '多视图', type: 'boolean' },
    { name: 'aspect_ratio', label: '比例', type: 'select', disabled_when: [{ parameter: 'multi_view', operator: 'equals', value: true }] },
    { name: 'watermark', label: '水印', type: 'boolean' },
    { name: 'compression', label: '压缩', type: 'number' },
  ];
  expect(schemaParameterValues(parameters, { multi_view: true, aspect_ratio: '16:9', watermark: false, compression: 0 })).toEqual({ multi_view: true, watermark: false, compression: 0 });
});

it.each(['text-to-image', 'image-to-image'])('renders Gemini %s aspect options; image_size remains advised readonly', (capability) => {
  vi.stubGlobal('React', React);
  const offering = defaultCatalog.offerings.find(item =>
    item.model_id === 'gemini-3-pro-image' && item.capability === capability && item.enabled);
  expect(offering).toBeDefined();
  const imageSize = offering!.parameters.find(parameter => parameter.name === 'image_size') as
    ParamSchema & { fixed?: boolean; adjustment_policy?: 'advised'; presentation?: { surface?: string } };
  // 最高分辨率仍为默认：用户只读，Agent 调整须走建议理由与冻结批准。
  expect(imageSize.default).toBe('4K');
  expect(imageSize.fixed).not.toBe(true);
  expect(imageSize.adjustment_policy).toBe('advised');
  expect(imageSize.presentation?.surface).toBe('readonly');
  const parameters = offering!.parameters.filter(parameter => parameter.name === 'aspect_ratio') as ParamSchema[];
  expect(parameters[0].default).toBe('1:1');
  expect(parameters[0].options?.map(option => option.value)).toEqual([
    '1:1', '2:3', '3:2', '3:4', '4:3', '4:5', '5:4', '9:16', '16:9', '21:9',
  ]);
  const values = { aspect_ratio: '16:9', size: 'unsupported' };
  const html = renderToStaticMarkup(<SchemaParameters parameters={parameters} values={values} onChange={() => {}} />);
  // The project's own dropdown, not a native select: the trigger shows the chosen label.
  expect(html).not.toContain('<select');
  expect(html).toMatch(/role="combobox"[^>]*>(?:(?!<\/button>).)*>16:9<\/span>/);
  expect(html).toContain('value="16:9"');
  expect(schemaParameterValues(parameters, values)).toEqual({ aspect_ratio: '16:9' });
});

it('matting sensitivity 由 Catalog 声明驱动：slider 控件 + mask_refine 条件可见', () => {
  vi.stubGlobal('React', React);
  const offering = defaultCatalog.offerings.find(item =>
    item.id === 'matting/default/background-removal');
  expect(offering).toBeDefined();
  const raw = offering!.parameters.find(parameter => parameter.name === 'sensitivity') as
    ParamSchema & {
      minimum?: number;
      maximum?: number;
      presentation?: { control?: string; visible_when?: ParamSchema['visible_when']; step?: number };
    };
  // 声明真源：slider 控件 + visible_when（mask_refine truthy）+ 数值边界
  expect(raw.presentation?.control).toBe('slider');
  expect(raw.presentation?.visible_when).toEqual([
    { parameter: 'mask_refine', operator: 'truthy' },
  ]);
  const parameters: ParamSchema[] = [
    { name: 'mask_refine', label: 'Mask 精炼', type: 'boolean' },
    {
      name: 'sensitivity',
      label: raw.label,
      type: raw.presentation!.control as ParamSchema['type'],
      min: raw.minimum,
      max: raw.maximum,
      step: raw.presentation!.step,
      default: raw.default,
      visible_when: raw.presentation!.visible_when,
    },
  ];

  // mask_refine 开启：渲染 range 控件，边界与步长来自声明
  const visible = renderToStaticMarkup(
    <SchemaParameters parameters={parameters} values={{ mask_refine: true, sensitivity: 0.3 }} onChange={() => {}} />,
  );
  expect(visible).toContain('type="range"');
  expect(visible).toContain('min="0"');
  expect(visible).toContain('max="1"');
  expect(visible).toContain('step="0.01"');
  expect(visible).toContain('0.3');

  // mask_refine 关闭：控件不渲染（visible_when 条件改变渲染面）
  const hidden = renderToStaticMarkup(
    <SchemaParameters parameters={parameters} values={{ mask_refine: false, sensitivity: 0.3 }} onChange={() => {}} />,
  );
  expect(hidden).not.toContain('type="range"');
  expect(hidden).not.toContain('精炼灵敏度');
  expect(hidden).toContain('Mask 精炼');
});

it('required 属性透传 select 控件；隐藏/禁用值不进入提交', () => {
  vi.stubGlobal('React', React);
  const parameters: ParamSchema[] = [
    { name: 'style', label: '风格', type: 'select', required: true, options: [{ value: 'a', label: 'A' }] },
    { name: 'hidden_field', label: '隐藏项', type: 'text', visible_when: [{ parameter: 'style', operator: 'equals', value: 'z' }] },
    { name: 'disabled_field', label: '禁用项', type: 'text', disabled_when: [{ parameter: 'style', operator: 'truthy' }] },
  ];
  const html = renderToStaticMarkup(
    <SchemaParameters parameters={parameters} values={{ style: 'a', hidden_field: 'x', disabled_field: 'y' }} onChange={() => {}} />,
  );
  expect(html).toContain('required=""');
  expect(html).not.toContain('隐藏项');
  expect(html).toContain('禁用项');
  expect(html).toContain('disabled=""');
  expect(schemaParameterValues(parameters, { style: 'a', hidden_field: 'x', disabled_field: 'y' })).toEqual({ style: 'a' });
});

it('connects Schema help to a control and groups multiple options', () => {
  const parameters: ParamSchema[] = [
    { name: 'quality', label: '质量', description: '选择画质', type: 'select', options: [{ value: 'high', label: '高' }] },
    { name: 'actions', label: '动作', type: 'multi-select', options: [{ value: 'run', label: '跑步' }] },
  ];
  const html = renderToStaticMarkup(
    <SchemaParameters parameters={parameters} values={{ quality: 'high', actions: ['run'] }} onChange={() => {}} />,
  );
  const selectId = html.match(/<label for="([^"]+)"[^>]*>质量<\/label>/)?.[1];
  expect(selectId).toBeDefined();
  expect(html).toContain(`id="${selectId}"`);
  expect(html).toContain(`aria-describedby="${selectId}-help"`);
  expect(html).toContain(`<div id="${selectId}-help"`);
  expect(html).toContain('<fieldset');
  expect(html).toContain('动作</legend>');
});

it('renders a boolean with false/true options as two chips and writes a real boolean; a plain boolean stays a checkbox', () => {
  vi.stubGlobal('React', React);
  const mode: ParamSchema = { name: 'mode', label: '生成模式', type: 'boolean', default: false, options: [{ value: 'false', label: '常规' }, { value: 'true', label: '分部件' }] };
  const plain: ParamSchema = { name: 'flag', label: '开关', type: 'boolean' };
  expect(isBooleanChoice(mode)).toBe(true);
  expect(isBooleanChoice(plain)).toBe(false);
  expect(isBooleanChoice({ ...mode, options: [{ value: 'true', label: '开' }] })).toBe(false);
  const html = renderToStaticMarkup(<SchemaParameters parameters={[mode, plain]} values={{ mode: true, flag: true }} onChange={() => {}} />);
  expect(html).toContain('role="radiogroup"');
  expect(html).toMatch(/aria-checked="true"[^>]*>分部件/);
  expect(html).toMatch(/aria-checked="false"[^>]*>常规/);
  expect(html.match(/type="checkbox"/g)).toHaveLength(1);
});

// #329 follow-up B: the Meshy Retexture style picture allows 20,000,000 bytes; the upload hint states it the way
// every size is written (decimal MB, #327), not as an unrounded MiB figure ("19.073486328125MB").
it('states the Retexture style picture limit as 20 MB', () => {
  vi.stubGlobal('React', React);
  const offering = defaultCatalog.offerings.find(item => item.id === 'meshy/processing/retexture');
  const style = offering?.parameters.find(parameter => parameter.name === 'style_asset') as { max_bytes?: number; media_types?: string[] } | undefined;
  expect(style?.max_bytes).toBe(20_000_000);
  const parameters: ParamSchema[] = [{
    name: 'style_asset', label: '风格参考图', type: 'asset',
    validation: { type: 'asset', max_bytes: style!.max_bytes, media_types: style!.media_types },
  }];
  const html = renderToStaticMarkup(<SchemaParameters parameters={parameters} values={{}} onChange={() => {}} />);
  expect(html).toContain('20 MB');
  expect(html).not.toContain('19.07');
});

// #329 follow-up: the style picture hint lists only the formats the parameter declares (PNG/JPEG/WebP).
it('lists only the Retexture style picture formats the Schema declares', () => {
  vi.stubGlobal('React', React);
  const offering = defaultCatalog.offerings.find(item => item.id === 'meshy/processing/retexture');
  const style = offering?.parameters.find(parameter => parameter.name === 'style_asset') as { max_bytes?: number; media_types?: string[] } | undefined;
  expect(style?.media_types).toEqual(['image/png', 'image/jpeg', 'image/webp']);
  const parameters: ParamSchema[] = [{
    name: 'style_asset', label: '风格参考图', type: 'asset',
    validation: { type: 'asset', max_bytes: style!.max_bytes, media_types: style!.media_types },
  }];
  const html = renderToStaticMarkup(<SchemaParameters parameters={parameters} values={{}} onChange={() => {}} />);
  expect(html).toContain('支持 PNG、JPG、WebP，最大 20 MB');
  expect(html).not.toContain('GIF');
  expect(html).not.toContain('BMP');
});
