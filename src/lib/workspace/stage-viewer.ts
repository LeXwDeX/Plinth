import { stepIndex } from '@/lib/workspace/lightbox';
import type { VersionTile } from '@/lib/workspace/versions';

/**
 * The version before or after the current one among those that can be shown
 * (finished ones; work in flight and failures have no file). Wraps at both
 * ends. With no current version, forward starts at the first and back at the last.
 */
export function neighbourTile(tiles: readonly VersionTile[], currentKey: string | null, delta: number): VersionTile | null {
  const ready = tiles.filter((tile) => tile.status === 'ready');
  if (ready.length === 0) return null;
  const at = ready.findIndex((tile) => tile.key === currentKey);
  if (at < 0) return delta >= 0 ? ready[0] : ready[ready.length - 1];
  if (ready.length === 1) return null;
  return ready[stepIndex(at, delta, ready.length)];
}
