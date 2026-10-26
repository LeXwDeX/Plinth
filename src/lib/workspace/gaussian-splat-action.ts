import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';

export type GaussianSplatTool = ProductSchema['chat']['tools'][number];

/** Use the public tool contract to find its capability; do not maintain a provider/capability route map here. */
export function gaussianSplatTool(schema: ProductSchema): GaussianSplatTool | undefined {
  return schema.chat.tools.find((tool) => tool.name === 'create_gaussian_splat');
}

/** Ambiguous or unavailable offerings stay disabled; backend routing enforces the same declared constraint. */
export function canCreateGaussianSplat(tool: GaussianSplatTool | undefined, offerings: readonly OfferingWire[] | undefined): boolean {
  const count = offerings?.filter((offering) => offering.available).length ?? 0;
  return tool?.require_unique_offering === true ? count === 1 : count > 0;
}

export function gaussianSplatAvailableCount(offerings: readonly OfferingWire[] | undefined): number {
  return offerings?.filter((offering) => offering.available).length ?? 0;
}
