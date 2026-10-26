import { describe, expect, it } from 'vitest';

import { layerBarShown, selectedImageLayer } from './selected-layer';

const nodes = [
  { id: 'a', type: 'image' },
  { id: 'b', type: 'image' },
  { id: 'c', type: 'text' },
];

describe('selectedImageLayer', () => {
  it('is the picture layer when exactly one layer is selected and it is a picture', () => {
    expect(selectedImageLayer(nodes, ['a'])).toBe(nodes[0]);
  });

  it('is nothing for no selection, several layers, a layer that is not a picture or an unknown id', () => {
    expect(selectedImageLayer(nodes, [])).toBeNull();
    expect(selectedImageLayer(nodes, ['a', 'b'])).toBeNull();
    expect(selectedImageLayer(nodes, ['c'])).toBeNull();
    expect(selectedImageLayer(nodes, ['gone'])).toBeNull();
    expect(selectedImageLayer(undefined, ['a'])).toBeNull();
  });
});

describe('layerBarShown', () => {
  it('shows the bar for a selected picture layer in the select tool only', () => {
    expect(layerBarShown('select', nodes[0])).toBe(true);
    expect(layerBarShown('brush', nodes[0])).toBe(false);
    expect(layerBarShown('select', null)).toBe(false);
  });
});
