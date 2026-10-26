import { describe, expect, it } from 'vitest';

import { currentObjectLayer, inpaintEntry } from './inpaint-entry';

const proxy = (url: string, taskId: string) => `/proxy/${taskId}?u=${encodeURIComponent(url)}`;
const picture = (id: string, taskId: string, url: string) => ({ id, type: 'image' as const, imageUrl: proxy(url, taskId) });

describe('inpaintEntry (#278)', () => {
  it('works on the artboard whether or not the version\'s own layer is still there', () => {
    // The old entry required that layer and reported 导出失败 without it; the entry now only asks the artboard.
    expect(inpaintEntry({ boardHasContent: true, maskSupported: true, hasSelection: false })).toBe('mask_tool');
    expect(inpaintEntry({ boardHasContent: true, maskSupported: true, hasSelection: true })).toBe('submit_selection');
    expect(inpaintEntry({ boardHasContent: true, maskSupported: false, hasSelection: false })).toBe('switch_model');
    expect(inpaintEntry({ boardHasContent: true, maskSupported: false, hasSelection: true })).toBe('switch_model');
  });

  it('says the frame is empty before anything else: there is nothing to paint on or to switch models for', () => {
    for (const maskSupported of [true, false]) {
      for (const hasSelection of [true, false]) {
        expect(inpaintEntry({ boardHasContent: false, maskSupported, hasSelection })).toBe('board_empty');
      }
    }
  });
});

describe('currentObjectLayer (#278)', () => {
  const v1 = { kind: 'image', taskId: 't1', url: 'https://cdn/v1.png', sessionId: 's' };
  const v2 = { kind: 'image', taskId: 't2', url: 'https://cdn/v2.png', sessionId: 's' };
  const nodes = [picture('n1', 't1', v1.url), { id: 'blank', type: 'image' as const, imageUrl: '' }];

  it('finds the version\'s layer by its proxied URL', () => {
    expect(currentObjectLayer(nodes, v1, 's', proxy)?.id).toBe('n1');
  });

  it('is undefined once that layer is deleted, even though another picture stays on the board', () => {
    // The reported case: the version picked on the stage (v2) lost its layer; v1 still fills the artboard.
    expect(currentObjectLayer(nodes, v2, 's', proxy)).toBeUndefined();
  });

  it('ignores models, other sessions and missing objects', () => {
    expect(currentObjectLayer(nodes, { ...v1, kind: 'model' }, 's', proxy)).toBeUndefined();
    expect(currentObjectLayer(nodes, v1, 'other', proxy)).toBeUndefined();
    expect(currentObjectLayer(nodes, { ...v1, sessionId: undefined }, 's', proxy)).toBeUndefined();
    expect(currentObjectLayer(nodes, null, 's', proxy)).toBeUndefined();
    expect(currentObjectLayer(undefined, v1, 's', proxy)).toBeUndefined();
    expect(currentObjectLayer(nodes, v1, 's', () => null)).toBeUndefined();
  });
});
