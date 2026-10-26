import { describe, expect, it } from 'vitest';

import {
  CANVAS_RAIL_LEFT, LAYER_BAR_GAP, LAYER_BAR_MAX_WIDTH, LAYER_BAR_RIGHT, RAIL_COLLAPSED_WIDTH, RAIL_EXPANDED_MAX_WIDTH,
  RAIL_EXPANDED_MIN_WIDTH, RAIL_GAP, layerBarClearance, railClearance,
} from './tool-rail-layout';

describe('how much of the stage the 2D rail covers', () => {
  it('is 38px wide collapsed and 96 to 112px expanded', () => {
    expect(RAIL_COLLAPSED_WIDTH).toBe(38);
    expect(RAIL_EXPANDED_MIN_WIDTH).toBe(96);
    expect(RAIL_EXPANDED_MAX_WIDTH).toBeGreaterThanOrEqual(RAIL_EXPANDED_MIN_WIDTH);
  });

  it('clears the rail, its distance from the edge and a gap, so the option bar and the fit never sit under it', () => {
    expect(railClearance(false)).toBe(CANVAS_RAIL_LEFT + RAIL_COLLAPSED_WIDTH + RAIL_GAP);
    expect(railClearance(true)).toBe(CANVAS_RAIL_LEFT + RAIL_EXPANDED_MAX_WIDTH + RAIL_GAP);
  });

  it('asks for more room when the rail is expanded, and for at least the widest expanded rail', () => {
    expect(railClearance(true)).toBeGreaterThan(railClearance(false));
    expect(railClearance(true)).toBeGreaterThanOrEqual(CANVAS_RAIL_LEFT + RAIL_EXPANDED_MIN_WIDTH + RAIL_GAP);
  });
});

describe('how much of the stage the floating bar over the selected layer covers', () => {
  it('keeps the option bar out of the bar\'s strip: its distance from the edge, its widest width and a gap', () => {
    expect(layerBarClearance()).toBe(LAYER_BAR_RIGHT + LAYER_BAR_MAX_WIDTH + LAYER_BAR_GAP);
    // Name (at most 8rem), three 28px buttons and the padding fit inside the width the strip reserves.
    expect(LAYER_BAR_MAX_WIDTH).toBeGreaterThanOrEqual(128 + 3 * 28 + 24);
  });
});
