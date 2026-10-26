import { describe, expect, it } from 'vitest';

import { removeBackgroundTarget } from './remove-background-target';

const selected = { id: 'layer-selected', imageUrl: '/assets/selected.png' };
const current = { id: 'layer-current', imageUrl: '/assets/current.png' };

describe('removeBackgroundTarget', () => {
  it('acts on the selected picture layer when there is one, without moving the selection', () => {
    expect(removeBackgroundTarget(selected, current)).toEqual({ layer: selected, selectLayer: false });
  });

  it('acts on the selected layer even when the current version has no layer on the board', () => {
    expect(removeBackgroundTarget(selected, undefined)).toEqual({ layer: selected, selectLayer: false });
  });

  it('falls back to the current version\'s picture and selects its layer', () => {
    expect(removeBackgroundTarget(null, current)).toEqual({ layer: current, selectLayer: true });
    expect(removeBackgroundTarget(undefined, current)).toEqual({ layer: current, selectLayer: true });
  });

  it('on a board without a version, works on the one selected picture layer and on nothing else', () => {
    // The stage toolbar enables 去背景 only while a picture layer is selected there; the target rule agrees.
    expect(removeBackgroundTarget(selected, null)).toEqual({ layer: selected, selectLayer: false });
    expect(removeBackgroundTarget(null, null)).toBeNull();
  });

  it('has no target when nothing is selected and the current version has no layer', () => {
    expect(removeBackgroundTarget(null, undefined)).toBeNull();
    expect(removeBackgroundTarget(null, null)).toBeNull();
  });
});
