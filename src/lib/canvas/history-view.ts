/**
 * The history panel's rows from the canvas undo / redo stacks. Done steps come first (oldest first),
 * then the undone ones (greyed, still reachable until a new edit drops them). Row 0 is the state the
 * session opened in; once the history cap has dropped the oldest steps (`truncated`) that state is gone and
 * row 0 is the state left by a dropped step, so it is flagged `truncated` and the panel names it as the earlier,
 * no longer reachable work instead of the opening state. Pure; the store's `goToHistory` applies a click.
 */
export interface HistoryEntry {
  descriptionKey: string;
  timestamp: number;
}

export interface HistoryRow {
  /** Position this row stands for: the number of steps applied once the row is clicked (0 = the opening state). */
  position: number;
  descriptionKey: string | null;
  timestamp: number | null;
  state: 'initial' | 'done' | 'undone';
  /** The row for the canvas as it is now. */
  current: boolean;
  /** Row 0 only: the opening state was dropped by the history cap, so this is the state after the dropped steps. */
  truncated: boolean;
}

/** `redo` is the store's redo stack: its last entry is the next step to redo. */
export function historyRows(undo: readonly HistoryEntry[], redo: readonly HistoryEntry[], truncated = false): HistoryRow[] {
  const rows: HistoryRow[] = [{ position: 0, descriptionKey: null, timestamp: null, state: 'initial', current: undo.length === 0, truncated }];
  undo.forEach((entry, index) => {
    rows.push({ position: index + 1, descriptionKey: entry.descriptionKey, timestamp: entry.timestamp, state: 'done', current: index === undo.length - 1, truncated: false });
  });
  [...redo].reverse().forEach((entry, index) => {
    rows.push({ position: undo.length + index + 1, descriptionKey: entry.descriptionKey, timestamp: entry.timestamp, state: 'undone', current: false, truncated: false });
  });
  return rows;
}
