import { describe, expect, it, vi } from 'vitest';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ParamSchema } from '@/lib/providers/types';
import { creditRange, inputRuleViolations, missingRequiredParameters, uploadAssetInputs } from './processing-inputs';

const retexture: OfferingWire = {
  id: 'meshy/processing/retexture', revision: '1', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'm', model_name: 'M',
  capability: 'retexture', available: true, estimated_credits: 10,
  parameters: [
    { name: 'source_task', label: '来源任务', type: 'task', semantic: 'source-task' },
    { name: 'source_asset', label: '来源模型', type: 'asset', semantic: 'source-asset' },
    { name: 'prompt', label: '提示词', type: 'text', semantic: 'prompt' },
    { name: 'style_asset', label: '风格图', type: 'asset', semantic: 'reference-image' },
  ],
  input_rules: [
    { parameters: ['source_task', 'source_asset'], minimum: 1, maximum: 1 },
    { parameters: ['prompt', 'style_asset'], minimum: 1, maximum: 1 },
  ],
};

describe('inputRuleViolations', () => {
  it('requires exactly one of the declared user inputs and ignores source rules', () => {
    expect(inputRuleViolations(retexture, {})).toEqual([['prompt', 'style_asset']]);
    expect(inputRuleViolations(retexture, { prompt: 'mossy' })).toEqual([]);
    expect(inputRuleViolations(retexture, { style_asset: 'asset-1' })).toEqual([]);
    expect(inputRuleViolations(retexture, { prompt: 'mossy', style_asset: 'asset-1' })).toEqual([['prompt', 'style_asset']]);
    expect(inputRuleViolations(retexture, { prompt: '' })).toEqual([['prompt', 'style_asset']]);
  });
  it('treats an empty list as not filled', () => {
    const multi = { ...retexture, input_rules: [{ parameters: ['style_asset'], minimum: 1, maximum: 1 }] };
    expect(inputRuleViolations(multi, { style_asset: [] })).toEqual([['style_asset']]);
  });
});

describe('uploadAssetInputs', () => {
  it('uploads data URIs once and passes platform asset IDs through', async () => {
    const upload = vi.fn(async (_blob: Blob, _name: string) => 'asset-style');
    const cache = new Map<string, string>();
    const input = { style_asset: 'data:image/png;base64,AAAA', prompt: 'ignored' };
    expect(await uploadAssetInputs(retexture, input, upload, cache)).toEqual({ style_asset: 'asset-style', prompt: 'ignored' });
    expect(await uploadAssetInputs(retexture, input, upload, cache)).toEqual({ style_asset: 'asset-style', prompt: 'ignored' });
    expect(upload).toHaveBeenCalledTimes(1);
    expect(await uploadAssetInputs(retexture, { style_asset: 'asset-existing' }, upload, cache)).toEqual({ style_asset: 'asset-existing' });
    expect(input.style_asset).toBe('data:image/png;base64,AAAA');
  });
  it('decodes the picture in the page: the CSP connect-src does not allow fetching data: addresses (#321)', async () => {
    const fetcher = vi.fn(async () => { throw new TypeError('Failed to fetch'); });
    vi.stubGlobal('fetch', fetcher);
    try {
      const upload = vi.fn(async (_blob: Blob, _name: string) => 'asset-style');
      expect(await uploadAssetInputs(retexture, { style_asset: 'data:image/png;base64,iVBORw==' }, upload, new Map())).toEqual({ style_asset: 'asset-style' });
      expect(fetcher).not.toHaveBeenCalled();
      const [blob, filename] = upload.mock.calls[0];
      expect(blob.type).toBe('image/png');
      // The server checks an input picture's extension (Hyper3D reference_image), so the name carries one.
      expect(filename).toBe('style_asset-1.png');
      expect([...new Uint8Array(await blob.arrayBuffer())]).toEqual([0x89, 0x50, 0x4e, 0x47]);
      await expect(uploadAssetInputs(retexture, { style_asset: 'data:image/png,not-base64' }, upload, new Map())).rejects.toThrow();
      await uploadAssetInputs(retexture, { style_asset: 'data:image/jpeg;base64,/9j/' }, upload, new Map());
      expect(upload.mock.calls.at(-1)?.[1]).toBe('style_asset-1.jpg');
    } finally {
      vi.unstubAllGlobals();
    }
  });
  it('names the upload by the picture bytes, not by the declared type (#187 B)', async () => {
    const upload = vi.fn(async (_blob: Blob, _name: string) => 'asset-style');
    const jpeg = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]).toString('base64');
    await uploadAssetInputs(retexture, { style_asset: `data:image/png;base64,${jpeg}` }, upload, new Map());
    expect(upload.mock.calls[0][1]).toBe('style_asset-1.jpg');
    expect(upload.mock.calls[0][0].type).toBe('image/jpeg');
    const webp = Buffer.from('RIFF\x00\x00\x00\x00WEBPVP8 ', 'latin1').toString('base64');
    await uploadAssetInputs(retexture, { style_asset: `data:application/octet-stream;base64,${webp}` }, upload, new Map());
    expect(upload.mock.calls[1][1]).toBe('style_asset-1.webp');
    expect(upload.mock.calls[1][0].type).toBe('image/webp');
  });
  it('keeps the name of the file the person chose, with the extension of its real type (#187 B)', async () => {
    vi.stubGlobal('FileReader', class {
      result: string | null = null;
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      onabort: (() => void) | null = null;
      async readAsDataURL(blob: Blob) {
        this.result = `data:${blob.type};base64,${Buffer.from(await blob.arrayBuffer()).toString('base64')}`;
        this.onload?.();
      }
    });
    try {
      const { prepareImageUpload } = await import('@/lib/image-upload');
      const policy = { maximum_pixels: 1 << 24, conversions: [] };
      const png = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1];
      const upload = vi.fn(async (_blob: Blob, _name: string) => 'asset-style');
      const chosen = await prepareImageUpload(new File([Uint8Array.from(png)], 'Wood Reference.PNG', { type: 'image/png' }), { maximumBytes: 1024, policy, rememberName: true });
      await uploadAssetInputs(retexture, { style_asset: chosen }, upload, new Map());
      expect(upload.mock.calls[0][1]).toBe('Wood Reference.PNG');
      // A name whose extension does not match the bytes keeps its stem only.
      const renamed = await prepareImageUpload(new File([Uint8Array.from([...png, 2])], 'scan.jpg', { type: 'image/jpeg' }), { maximumBytes: 1024, policy, rememberName: true });
      await uploadAssetInputs(retexture, { style_asset: renamed }, upload, new Map());
      expect(upload.mock.calls[1][1]).toBe('scan.png');
    } finally {
      vi.unstubAllGlobals();
    }
  });
  it('keeps the names of form fields only; chat attachments are uploaded under their own names (#335)', async () => {
    const { readFileSync } = await import('node:fs');
    const form = readFileSync(new URL('../../components/form/image-upload.tsx', import.meta.url), 'utf8');
    const chat = readFileSync(new URL('../../components/studio/chat-column.tsx', import.meta.url), 'utf8');
    expect(form).toMatch(/prepareImageUpload\(file, \{[^}]*rememberName: true/);
    expect(chat).not.toContain('rememberName');
  });
  it('never uploads into a source parameter', async () => {
    const upload = vi.fn(async () => 'x');
    const out = await uploadAssetInputs(retexture, { source_asset: 'data:model' }, upload, new Map());
    expect(out.source_asset).toBe('data:model');
    expect(upload).not.toHaveBeenCalled();
  });
});

describe('creditRange', () => {
  it('shows a range only when optional billed choices exist', () => {
    expect(creditRange(retexture)).toEqual({ low: 10, high: 10 });
    expect(creditRange({ ...retexture, estimated_credits: 5, maximum_estimated_credits: 10 })).toEqual({ low: 5, high: 10 });
    expect(creditRange({ ...retexture, estimated_credits: undefined })).toBeNull();
    expect(creditRange({ ...retexture, price_mode: 'native', estimated_credits: 99 })).toBeNull();
  });
});

describe('retexture panel inputs', () => {
  it('offers the prompt and the style image from the Schema and hides the source parameters', async () => {
    const { visibleOfferingFields } = await import('./processing-source');
    expect(visibleOfferingFields(retexture).map((parameter) => parameter.name)).toEqual(['prompt', 'style_asset']);
  });
});

describe('missingRequiredParameters', () => {
  const reference: ParamSchema = { name: 'reference_image', label: '参考图', type: 'asset', required: true };
  const prompt: ParamSchema = { name: 'prompt', label: '提示词', type: 'text' };
  const gated: ParamSchema = { name: 'height', label: '高度', type: 'number', required: true, visible_when: [{ parameter: 'auto', operator: 'equals', value: false }] } as ParamSchema;
  it('names every visible required field that has no value yet (#321)', () => {
    expect(missingRequiredParameters([reference, prompt], {})).toEqual([reference]);
    expect(missingRequiredParameters([reference, prompt], { reference_image: '' })).toEqual([reference]);
    expect(missingRequiredParameters([reference], { reference_image: [] })).toEqual([reference]);
    expect(missingRequiredParameters([reference, prompt], { reference_image: 'data:image/png;base64,AA' })).toEqual([]);
  });
  it('ignores a required field the Schema currently hides', () => {
    expect(missingRequiredParameters([gated], { auto: true })).toEqual([]);
    expect(missingRequiredParameters([gated], { auto: false })).toEqual([gated]);
    expect(missingRequiredParameters([gated], { auto: false, height: 0 })).toEqual([]);
  });
});
