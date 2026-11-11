import { describe, expect, it } from 'vitest';

import { candidateSlots, candidateTile } from './candidate-selection';

const tiles = [
  { key: 'a', taskId: 'task-a', taskIds: ['task-a'] },
  { key: 'group', taskIds: ['task-b', 'task-c'] },
];

describe('candidate selection wiring', () => {
  it('finds a candidate by its own task or by its candidate group', () => {
    expect(candidateTile(tiles, 'task-a')?.key).toBe('a');
    expect(candidateTile(tiles, 'task-c')?.key).toBe('group');
    expect(candidateTile(tiles, 'missing')).toBeUndefined();
  });

  it('maps completed candidates to stage slots and skips the ones without a tile or slot', () => {
    const slots = candidateSlots(tiles, ['task-a', 'task-c', 'missing', 'task-b'], (tile) => tile.key === 'a' ? 'left' : tile.key === 'group' ? 'right' : null);
    expect(slots).toEqual(['left', 'right']);
  });

  it('keeps the declared candidate order even when tiles are out of order', () => {
    expect(candidateSlots(tiles, ['task-c', 'task-a'], (tile) => tile.key)).toEqual(['group', 'a']);
  });
});
