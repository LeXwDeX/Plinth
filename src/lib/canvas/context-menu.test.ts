import { describe, expect, it } from 'vitest';
import { canvasMenuItems, nodeMenuItems, selectableNodeIds, type MenuState } from './context-menu';

const base: MenuState = {
  nodes: [
    { id: 'a', type: 'image', zIndex: 1 },
    { id: 'b', type: 'shape', zIndex: 2 },
    { id: 'c', type: 'image', zIndex: 3, groupId: 'g' },
    { id: 'd', type: 'image', zIndex: 4, groupId: 'g' },
  ],
  selectedIds: ['b'],
  clipboardCount: 0,
  canRecenter: true,
  hasDownloadHandler: true,
};
const enabled = (items: ReturnType<typeof nodeMenuItems>) => Object.fromEntries(items.map((i) => [i.id, i.enabled]));

describe('nodeMenuItems', () => {
  it('enables layer moves that change something and paste only with a clipboard', () => {
    const items = enabled(nodeMenuItems(base));
    expect(items).toMatchObject({ front: true, forward: true, backward: true, back: true, copy: true, paste: false, delete: true, group: false, ungroup: false, download: false });
    expect(enabled(nodeMenuItems({ ...base, clipboardCount: 2 })).paste).toBe(true);
  });
  it('disables moves past the top and bottom', () => {
    const top = enabled(nodeMenuItems({ ...base, selectedIds: ['c', 'd'] }));
    expect(top).toMatchObject({ front: false, forward: false, backward: true, back: true });
  });
  it('group needs two nodes that are not already one group; ungroup needs a grouped node', () => {
    expect(enabled(nodeMenuItems({ ...base, selectedIds: ['a', 'b'] }))).toMatchObject({ group: true, ungroup: false });
    expect(enabled(nodeMenuItems({ ...base, selectedIds: ['c', 'd'] }))).toMatchObject({ group: false, ungroup: true });
    expect(enabled(nodeMenuItems({ ...base, selectedIds: ['b', 'c'] }))).toMatchObject({ group: true, ungroup: true });
  });
  it('download (下载这一层) applies to a single picture only, and only when the board handles it', () => {
    expect(enabled(nodeMenuItems({ ...base, selectedIds: ['a'] }))).toMatchObject({ download: true });
    expect(enabled(nodeMenuItems({ ...base, selectedIds: ['a', 'c'] }))).toMatchObject({ download: false });
    expect(nodeMenuItems({ ...base, hasDownloadHandler: false }).map((i) => i.id)).not.toContain('download');
  });
  it('has no item that works on the whole board or calls a model: no send to chat, no send to 3D, no background removal', () => {
    for (const state of [base, { ...base, selectedIds: ['a'] }, { ...base, selectedIds: ['a', 'c'] }]) {
      const ids: string[] = nodeMenuItems(state).map((item) => item.id);
      for (const gone of ['send_to_chat', 'send_to_3d', 'remove_background', 'bgremoval']) expect(ids, gone).not.toContain(gone);
    }
    // The last item is the download of this layer; everything before it is an edit of the selection.
    expect(nodeMenuItems({ ...base, selectedIds: ['a'] }).at(-1)?.id).toBe('download');
  });
  it('cannot delete only locked nodes', () => {
    const locked = { ...base, nodes: [{ id: 'a', type: 'image', zIndex: 1, locked: true }], selectedIds: ['a'] };
    expect(enabled(nodeMenuItems(locked)).delete).toBe(false);
  });
});

describe('lock item', () => {
  const idOf = (state: MenuState) => nodeMenuItems(state).find((item) => item.id === 'lock' || item.id === 'unlock');
  it('says lock while any selected node is unlocked', () => {
    expect(idOf(base)).toEqual({ id: 'lock', enabled: true });
    const mixed = { ...base, nodes: [{ id: 'a', type: 'image', zIndex: 1, locked: true }, { id: 'b', type: 'shape', zIndex: 2 }], selectedIds: ['a', 'b'] };
    expect(idOf(mixed)?.id).toBe('lock');
  });
  it('says unlock once every selected node is locked, and stays out of the empty-canvas menu', () => {
    const locked = { ...base, nodes: [{ id: 'a', type: 'image', zIndex: 1, locked: true }, { id: 'b', type: 'shape', zIndex: 2, locked: true }], selectedIds: ['a', 'b'] };
    expect(idOf(locked)).toEqual({ id: 'unlock', enabled: true });
    expect(canvasMenuItems(locked).map((item) => item.id)).not.toContain('lock');
  });
});

describe('canvasMenuItems', () => {
  it('offers paste, recenter and select all by state', () => {
    expect(enabled(canvasMenuItems(base))).toEqual({ paste: false, recenter: true, select_all: true });
    expect(enabled(canvasMenuItems({ ...base, nodes: [], canRecenter: false, clipboardCount: 1 }))).toEqual({ paste: true, recenter: false, select_all: false });
  });
});

import { layerShortcut } from './context-menu';

describe('layerShortcut', () => {
  const keys = { forward: ']', backward: '[' };
  it('maps brackets, with Shift going to the edge even when the key reports a brace', () => {
    expect(layerShortcut({ key: ']', code: 'BracketRight', shiftKey: false }, keys)).toBe('forward');
    expect(layerShortcut({ key: '[', code: 'BracketLeft', shiftKey: false }, keys)).toBe('backward');
    expect(layerShortcut({ key: '}', code: 'BracketRight', shiftKey: true }, keys)).toBe('front');
    expect(layerShortcut({ key: '{', code: 'BracketLeft', shiftKey: true }, keys)).toBe('back');
    expect(layerShortcut({ key: 'a', code: 'KeyA', shiftKey: false }, keys)).toBeNull();
  });
});

describe('selectableNodeIds', () => {
  it('leaves hidden and locked nodes out of Select All', () => {
    const nodes = [
      { id: 'a', visible: true },
      { id: 'b', visible: false },
      { id: 'c', locked: true },
      { id: 'd' },
    ];
    expect(selectableNodeIds(nodes)).toEqual(['a', 'd']);
    expect(enabled(canvasMenuItems({ ...base, nodes: [{ id: 'x', type: 'image', zIndex: 1, locked: true }] })).select_all).toBe(false);
  });
});

describe('batch B menu items', () => {
  it('cut and duplicate need a selection, and cut skips locked layers', () => {
    expect(enabled(nodeMenuItems(base))).toMatchObject({ cut: true, duplicate: true });
    const locked = { ...base, nodes: [{ id: 'a', type: 'image', zIndex: 1, locked: true }], selectedIds: ['a'] };
    expect(enabled(nodeMenuItems(locked))).toMatchObject({ cut: false, duplicate: true });
  });

  it('align needs two units, or one unit and the frame; a group counts once', () => {
    const align = (state: MenuState) => enabled(nodeMenuItems(state)).align_left;
    expect(align(base)).toBe(false);
    expect(align({ ...base, hasArtboard: true })).toBe(true);
    expect(align({ ...base, selectedIds: ['a', 'b'] })).toBe(true);
    expect(align({ ...base, selectedIds: ['c', 'd'] })).toBe(false);
    expect(align({ ...base, selectedIds: ['c', 'd'], hasArtboard: true })).toBe(true);
    expect(nodeMenuItems(base).filter((item) => item.id.startsWith('align_'))).toHaveLength(6);
  });
});
