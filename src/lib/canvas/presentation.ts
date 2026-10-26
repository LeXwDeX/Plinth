import {
  formatSchemaText,
  type CanvasProductSchema,
} from '../product-schema';

export function canvasText(
  schema: CanvasProductSchema,
  key: string,
): string {
  const value = schema.presentation.text[key];
  if (!value?.trim()) {
    throw new Error(`Canvas Product Schema text is missing: ${key}`);
  }
  return value;
}

export function formatCanvasText(
  schema: CanvasProductSchema,
  key: string,
  values: Record<string, string | number>,
): string {
  return formatSchemaText(canvasText(schema, key), values);
}
