import type { VersionTile } from '@/lib/workspace/versions';

/**
 * "Use this picture to retexture a model": which models may be the target, and
 * which one is chosen to begin with. Retexture regenerates the model's whole
 * texture set, so the choice matters and is always the person's to change.
 */

/** Finished 3D versions of the session, oldest first. */
export function retextureTargets(tiles: readonly VersionTile[]): VersionTile[] {
  return tiles
    .filter((tile) => tile.domain === 'model' && tile.status === 'ready')
    .sort((left, right) => left.version - right.version);
}

const SEARCH_DEPTH = 8;

/** The 3D version the picture came from, following its known sources back (a screenshot of a model, a painted copy of that). */
export function sourceModelOf(tiles: readonly VersionTile[], pictureKey: string | null): VersionTile | null {
  if (!pictureKey) return null;
  const byKey = new Map(tiles.map((tile) => [tile.key, tile]));
  const seen = new Set<string>();
  let frontier = [pictureKey];
  for (let depth = 0; depth < SEARCH_DEPTH && frontier.length > 0; depth += 1) {
    const next: string[] = [];
    for (const key of frontier) {
      if (seen.has(key)) continue;
      seen.add(key);
      for (const parent of byKey.get(key)?.parents ?? []) {
        if (!parent.known) continue;
        const tile = byKey.get(parent.key);
        if (tile?.domain === 'model') return tile;
        next.push(parent.key);
      }
    }
    frontier = next;
  }
  return null;
}

export type RetextureDefaultReason = 'from_image' | 'latest';

/** The model chosen to begin with: the one the picture came from, else the newest 3D version. */
export function defaultRetextureTarget(
  tiles: readonly VersionTile[],
  pictureKey: string | null,
): { tile: VersionTile; reason: RetextureDefaultReason } | null {
  const targets = retextureTargets(tiles);
  const source = sourceModelOf(tiles, pictureKey);
  const fromImage = source ? targets.find((tile) => tile.key === source.key) : undefined;
  if (fromImage) return { tile: fromImage, reason: 'from_image' };
  const latest = targets[targets.length - 1];
  return latest ? { tile: latest, reason: 'latest' } : null;
}
