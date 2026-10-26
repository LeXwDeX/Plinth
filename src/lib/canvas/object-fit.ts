/** What the board last did for the current object: its key, and whether it joined quietly (#262). */
export interface ObjectFitState {
  key: string;
  quiet: boolean;
  /** A restored selection whose saved viewport is kept; a later explicit pick fits (#312 follow-up). */
  restored?: boolean;
}

/**
 * How the board treats the current object's view on each run of its fit effect:
 * - `fresh`: a new object (or one the person picked themselves): select it and fit the stage to it;
 * - `restored`: the object restored when the session opened, with a saved viewport: select it, keep the view;
 * - `quiet`: a result picked automatically while the board holds other work: leave selection and view alone;
 * - `hold`: a later run (rail toggle, stage resize) for that quiet or restored pick: still leave the view alone;
 * - `follow`: keep the fitted view following the stage size until the person pans or zooms.
 */
export function objectFitStep(
  previous: ObjectFitState,
  current: { key: string; quiet: boolean; othersOnBoard: boolean; restored?: boolean; savedViewport?: boolean },
): 'fresh' | 'restored' | 'quiet' | 'hold' | 'follow' {
  if (previous.key !== current.key) {
    // A quiet automatic pick never takes the person's selection, even when Stage also marked the first
    // result after opening as restored: both flags are set in that flow (#262 before #312 follow-up).
    if (current.quiet && current.othersOnBoard) return 'quiet';
    if (current.restored && current.savedViewport) return 'restored';
    return 'fresh';
  }
  if (previous.restored) return current.restored ? 'hold' : 'fresh';
  if (previous.quiet) return current.quiet ? 'hold' : 'fresh';
  return 'follow';
}
