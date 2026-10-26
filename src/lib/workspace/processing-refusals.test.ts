import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { prerequisiteRefusalText, sizeText, sourceRefusalText } from './processing-refusals';

const schema = defaultCatalog.application as unknown as ProductSchema;
const service = (provider: string, model: string) => ({ id: model, revision: '1', provider_id: provider, provider_name: provider, model_id: model, model_name: model, capability: 'retexture', available: true, parameters: [] }) as OfferingWire;

describe('why the model cannot go into a step (#321 E)', () => {
  it('formats sizes like the server, in decimal MB (#327): limits round down, sizes up', () => {
    expect(sizeText(73400320, false)).toBe('73.4 MB');
    expect(sizeText(150000000, false)).toBe('150 MB');
    expect(sizeText(110_000_001, true)).toBe('110.01 MB');
    expect(sizeText(2048, true)).toBe('2.05 KB');
  });

  it('names every service with its reason and what to do', () => {
    const text = sourceRefusalText(schema, [
      { offering: service('Meshy', 'Retexture'), kind: 'too_large', limit: 73400320 },
      { offering: service('Tripo3D', 'Texture'), kind: 'needs_task', capabilities: ['text-to-3d', 'image-to-3d'] },
    ], 110_000_000);
    expect(text).toContain('110 MB');
    expect(text).toContain('Meshy · Retexture 最多接受 73.4 MB');
    expect(text).toMatch(/Tripo3D · Texture 只接受 Tripo3D 的.+、.+结果/);
    expect(text).toContain('换一个');
    expect(sourceRefusalText(schema, [], 1)).toBe('');
  });
});

describe('an expired original task (#325)', () => {
  it('says the provider no longer keeps it', () => {
    const text = sourceRefusalText(schema, [{ offering: service('Meshy AI', 'Meshy Animation'), kind: 'expired' }], 1024);
    expect(text).toContain('Meshy AI · Meshy Animation');
    expect(text).toContain('保留期');
  });
});

// #326: the step that would come first (rigging before animation) cannot run on the model either.
describe('a prerequisite that cannot run on the model', () => {
  it('names the step, why each of its services refuses, and what works', () => {
    const text = prerequisiteRefusalText(schema, { capability: 'rigging', refusals: [
      { offering: service('Meshy AI', 'Meshy Rigging'), kind: 'too_large', limit: 73400320 },
    ] }, 'animation');
    expect(text).toContain('绑骨');
    expect(text).toContain('Meshy AI · Meshy Rigging 最多接受 73.4 MB');
    expect(text).toContain('动画生成');
    expect(text).not.toMatch(/rigging|animation/);
    expect(prerequisiteRefusalText(schema, null, 'animation')).toBe('');
  });
});
