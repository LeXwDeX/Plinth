import { describe, expect, it } from 'vitest';

import type { VersionTile } from './versions';
import { neighbourTile } from './stage-viewer';

const tile = (key: string, status: VersionTile['status'] = 'ready') => ({ key, status }) as VersionTile;

describe('neighbourTile', () => {
  const tiles = [tile('a'), tile('b', 'failed'), tile('c'), tile('d', 'running'), tile('e')];

  it('steps over versions that have no file', () => {
    expect(neighbourTile(tiles, 'a', 1)?.key).toBe('c');
    expect(neighbourTile(tiles, 'c', 1)?.key).toBe('e');
    expect(neighbourTile(tiles, 'c', -1)?.key).toBe('a');
  });

  it('wraps at both ends', () => {
    expect(neighbourTile(tiles, 'e', 1)?.key).toBe('a');
    expect(neighbourTile(tiles, 'a', -1)?.key).toBe('e');
  });

  it('starts at an end when nothing is current, and has nowhere to go with one or none', () => {
    expect(neighbourTile(tiles, null, 1)?.key).toBe('a');
    expect(neighbourTile(tiles, null, -1)?.key).toBe('e');
    expect(neighbourTile([tile('a')], 'a', 1)).toBeNull();
    expect(neighbourTile([tile('x', 'failed')], null, 1)).toBeNull();
    expect(neighbourTile([], null, 1)).toBeNull();
  });
});
