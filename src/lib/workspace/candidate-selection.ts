/** A version-strip tile, structurally compatible with the studio's session versions. */
export interface CandidateTile {
  taskId?: string;
  taskIds: string[];
}

/** The version tile that owns a candidate task, whether it names it directly or in a group. */
export function candidateTile(tiles: readonly CandidateTile[], taskId: string): CandidateTile | undefined {
  return tiles.find((tile) => tile.taskId === taskId || tile.taskIds.includes(taskId));
}

/** The stage slots for the candidate tasks, in the declared order; missing tiles/slots are skipped. */
export function candidateSlots(tiles: readonly CandidateTile[], taskIds: readonly string[], slotOf: (tile: CandidateTile) => string | null): string[] {
  const seen = new Set<string>();
  const slots: string[] = [];
  for (const taskId of taskIds) {
    const tile = candidateTile(tiles, taskId);
    const slot = tile ? slotOf(tile) : null;
    // Two candidates can live in one grouped tile; the same stage slot must
    // not be compared with itself.
    if (!slot || seen.has(slot)) continue;
    seen.add(slot);
    slots.push(slot);
  }
  return slots;
}
