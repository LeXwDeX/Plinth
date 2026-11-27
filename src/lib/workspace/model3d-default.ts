import { offeringShowsCredits, offeringUsesNativePricing, pickDefaultOffering, type OfferingWire } from '@/lib/agent-offerings';
import { formatNativePrice } from '@/lib/pricing';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { IMAGE_TO_3D } from '@/lib/workspace/model-picker';

/**
 * #678: the 3D model 转 3D offers when the person has not picked one: the Schema's recommended available image-to-3D
 * Offering, else the first available one (the same rule the pickers use, `pickDefaultOffering`). Null while the
 * Offerings are loading or none is available.
 */
export function defaultImageTo3DOffering(offeringsByCapability: Record<string, OfferingWire[]> | undefined): OfferingWire | null {
  return pickDefaultOffering(offeringsByCapability?.[IMAGE_TO_3D] ?? []);
}

/** The cost line of the confirmation: its credits, or its native default estimate; empty when neither is known. */
export function model3DCostText(schema: ProductSchema, offering: Pick<OfferingWire, 'price_mode' | 'default_native_quote' | 'credits_metered' | 'estimated_credits'>): string {
  if (offeringUsesNativePricing(offering)) {
    const quote = offering.default_native_quote;
    const amount = quote ? formatNativePrice(quote.total_amount, quote.unit_label) : null;
    return amount ? formatSchemaText(schemaText(schema, 'ui.pricing.default_estimate'), { amount }) : '';
  }
  const credits = offering.estimated_credits ?? 0;
  return offeringShowsCredits(offering) && credits > 0
    ? schemaCountMessage(schema, 'ui.studio.model3d_confirm.cost', credits, { credits })
    : '';
}
