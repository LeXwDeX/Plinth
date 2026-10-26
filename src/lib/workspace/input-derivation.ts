/**
 * Input images re-encoded to fit an Offering's limits (#276): the server lists, for the task owner
 * (and the administrator's generation detail), which reference image was sent as a compressed copy,
 * from which source, how, and at what size. Width and height never change. All wording is Product
 * Schema text; this module only reads the wire and fills the templates.
 */

import { formatBytes } from '@/lib/admin-utils';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

export interface InputDerivation {
  parameter: string;
  /** Position in the parameter's value (0 for a single-asset parameter). */
  index: number;
  assetId: string;
  mediaType: string;
  size: number;
  /** 0 when the server could not read the header. */
  width: number;
  height: number;
  /** Schema-ordered step that produced it: lossless-png, jpeg-q<N>, flatten-jpeg-q<N>. */
  method: string;
  /** Empty when the source is no longer the owner's readable asset. */
  sourceAssetId: string;
  sourceMediaType: string;
  sourceSize: number;
}

const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const isRecord = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);

export function parseInputDerivations(raw: unknown): InputDerivation[] {
  if (!Array.isArray(raw)) return [];
  return raw.filter(isRecord).filter((item) => str(item.asset_id) && str(item.method)).map((item) => ({
    parameter: str(item.parameter),
    index: num(item.index),
    assetId: str(item.asset_id),
    mediaType: str(item.media_type),
    size: num(item.size),
    width: num(item.width),
    height: num(item.height),
    method: str(item.method),
    sourceAssetId: str(item.source_asset_id),
    sourceMediaType: str(item.source_media_type),
    sourceSize: num(item.source_size),
  }));
}

const formatName = (mediaType: string): string => (mediaType.split('/')[1] ?? '').toUpperCase();
const LOSSY = /^(flatten-)?jpeg-q(\d+)$/;

/** The Schema's words for a recorded method; an unknown method reads as recorded. */
export function inputDerivationMethodText(schema: ProductSchema, method: string): string {
  const lossy = LOSSY.exec(method);
  if (lossy) {
    const key = lossy[1] ? 'ui.result.detail.input_derivation.method.flatten_jpeg' : 'ui.result.detail.input_derivation.method.jpeg';
    return formatSchemaText(schemaText(schema, key), { quality: Number(lossy[2]) });
  }
  return schemaText(schema, `ui.result.detail.input_derivation.method.${method}`) || method;
}

/** One line per derived image: what was sent instead of which source, how, and its unchanged dimensions. */
export function inputDerivationText(schema: ProductSchema, derivation: InputDerivation, label: string): string {
  const values = {
    label,
    source_format: formatName(derivation.sourceMediaType) || schemaText(schema, 'ui.result.detail.input_derivation.source_unknown'),
    source_size: formatBytes(derivation.sourceSize),
    format: formatName(derivation.mediaType),
    size: formatBytes(derivation.size),
    method: inputDerivationMethodText(schema, derivation.method),
    width: derivation.width,
    height: derivation.height,
  };
  const key = derivation.width > 0 && derivation.height > 0
    ? 'ui.result.detail.input_derivation.value'
    : 'ui.result.detail.input_derivation.value_without_dimensions';
  return formatSchemaText(schemaText(schema, key), values);
}
