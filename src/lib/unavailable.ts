import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

/** The inline line of the shared "unavailable" notice: "无法使用，<short reason>" in the Schema's words (#93, #94). */
export function unavailableLabel(schema: ProductSchema, reason: string): string {
  return formatSchemaText(schemaText(schema, 'ui.unavailable.label'), { reason });
}
