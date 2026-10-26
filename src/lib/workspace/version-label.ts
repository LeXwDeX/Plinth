import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { VersionTile } from '@/lib/workspace/versions';

/**
 * "2D v3" / "3D v1": the number comes from the order the session's versions were made in. An
 * uploaded picture is not a step of that line and carries no number: it is just "上传".
 */
export function versionLabel(
  schema: ProductSchema,
  tile: Pick<VersionTile, 'domain' | 'version' | 'groupLabel'> & Partial<Pick<VersionTile, 'origin'>> | null,
  kind: 'image' | 'model',
): string {
  const domain = tile?.domain ?? kind;
  if (tile?.origin === 'upload') return schemaText(schema, 'ui.studio.version.upload');
  if (tile?.origin === 'merge') return formatSchemaText(schemaText(schema, 'ui.studio.version.label_merge'), { version: tile.version });
  const key = domain === 'model' ? 'ui.studio.version.label_3d' : 'ui.studio.version.label_2d';
  if (!tile || tile.version <= 0) return schemaText(schema, domain === 'model' ? 'ui.studio.version.unnumbered_3d' : 'ui.studio.version.unnumbered_2d');
  return formatSchemaText(schemaText(schema, key), { version: tile.version });
}

/**
 * What a version is, beside its number: the group it belongs to ("三视图"), that it was drawn by hand
 * ("手绘"), or which model it is a screenshot of ("3D v1 截图"). Empty for an ordinary result.
 */
export function versionCaption(schema: ProductSchema, tile: VersionTile, tiles: readonly VersionTile[]): string {
  if (tile.origin === 'capture') {
    const parent = tile.parents.find((item) => item.known);
    const source = parent ? tiles.find((item) => item.key === parent.key) : undefined;
    return source
      ? formatSchemaText(schemaText(schema, 'ui.studio.version.capture_caption'), { parent: versionLabel(schema, source, source.domain) })
      : schemaText(schema, 'ui.studio.version.capture_caption_alone');
  }
	if (tile.groupLabel) return tile.groupLabel;
	if (tile.conversion) return schemaText(schema, 'ui.studio.version.conversion_caption');
	const capability = schema.capabilities?.find((entry) => entry.id === tile.capability);
	const slots = capability?.image_result_group?.slots;
	if (slots && tile.domain === 'image') {
		const images = tile.assets.filter((asset) => asset.kind === 'image');
		const selected = images.length === 1 ? slots.find((slot) => slot.variant === images[0].variant) : undefined;
		return selected ? `${capability.label_cn} · ${selected.label}` : capability.label_cn;
	}
  return tile.groupLabel;
}

/** "2D v3 · 三视图": the label with its caption, as one line for places that have room. */
export function versionTitle(schema: ProductSchema, tile: VersionTile, tiles: readonly VersionTile[]): string {
  const label = versionLabel(schema, tile, tile.domain);
  const caption = versionCaption(schema, tile, tiles);
  return caption ? [label, caption].join(' · ') : label;
}
