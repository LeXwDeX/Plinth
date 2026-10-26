import { describe, expect, it } from 'vitest';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';
import { canCreateGaussianSplat, gaussianSplatTool } from './gaussian-splat-action';

const available = (id: string, enabled = true) => ({ id, available: enabled }) as OfferingWire;
const schema = (tools: ProductSchema['chat']['tools']): ProductSchema => ({ chat: { tools } } as unknown as ProductSchema);

describe('Gaussian Splat action routing declaration', () => {
  it('uses the public tool capability and requires exactly one available Offering when declared', () => {
    const tool = gaussianSplatTool(schema([{ name: 'create_gaussian_splat', capability: 'image-to-splat', fallback_capability: 'image-to-splat', pending_capability: 'image-to-splat', require_unique_offering: true }]));
    expect(tool?.capability).toBe('image-to-splat');
    expect(canCreateGaussianSplat(tool, [])).toBe(false);
    expect(canCreateGaussianSplat(tool, [available('a', false)])).toBe(false);
    expect(canCreateGaussianSplat(tool, [available('a')])).toBe(true);
    expect(canCreateGaussianSplat(tool, [available('a'), available('b')])).toBe(false);
  });

  it('preserves legacy non-unique semantics only when the public tool omits the constraint', () => {
    const tool = gaussianSplatTool(schema([{ name: 'create_gaussian_splat', capability: 'image-to-splat', fallback_capability: 'image-to-splat', pending_capability: 'image-to-splat' }]));
    expect(canCreateGaussianSplat(tool, [available('a'), available('b')])).toBe(true);
    expect(gaussianSplatTool(schema([]))).toBeUndefined();
  });
});
