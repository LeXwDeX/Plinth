/**
 * How much of the stage's left edge the 2D tool rail covers. The rail is 38px wide collapsed (30px buttons)
 * and, expanded, as wide as its longest name, between 96px and 112px. The option bar beside it and the
 * fit-to-stage inset clear the rail by these numbers, so an expanded rail never sits on top of either.
 */
export const RAIL_COLLAPSED_WIDTH = 38;
export const RAIL_EXPANDED_MIN_WIDTH = 96;
export const RAIL_EXPANDED_MAX_WIDTH = 112;
/** Distance from the stage's left edge to the 2D rail. */
export const CANVAS_RAIL_LEFT = 12;
/** Space kept between the rail and whatever sits beside it. */
export const RAIL_GAP = 14;

/** The stage's left strip (px) the 2D rail needs, plus its gap. 64 while collapsed, 138 expanded at most. */
export function railClearance(expanded: boolean): number {
  return CANVAS_RAIL_LEFT + (expanded ? RAIL_EXPANDED_MAX_WIDTH : RAIL_COLLAPSED_WIDTH) + RAIL_GAP;
}

/** Strips of the stage covered by the floating option bar (top) and zoom bar (bottom, right). */
const STAGE_FIT_INSETS = { top: 56, right: 16, bottom: 64 };

/** What a fit keeps clear of the floating controls: those strips plus the rail's, collapsed or expanded. */
export function stageFitInsets(railExpanded: boolean): { left: number; top: number; right: number; bottom: number } {
  return { ...STAGE_FIT_INSETS, left: railClearance(railExpanded) };
}

/**
 * The floating bar over the selected layer sits at the stage's top-right, `LAYER_BAR_RIGHT` from the edge, and
 * is never wider than `LAYER_BAR_MAX_WIDTH` (its layer name truncates). The option bar at the top is as wide
 * as the stage minus the rail's strip on the left; while the layer bar is shown it also keeps out of the bar's
 * strip on the right, so the two never overlap (the option bar wraps to a second row instead).
 */
export const LAYER_BAR_RIGHT = 12;
export const LAYER_BAR_MAX_WIDTH = 256;
/** Space kept between the layer bar and the option bar beside it. */
export const LAYER_BAR_GAP = 14;

/** The stage's right strip (px) the layer bar needs, plus its gap. */
export function layerBarClearance(): number {
  return LAYER_BAR_RIGHT + LAYER_BAR_MAX_WIDTH + LAYER_BAR_GAP;
}
