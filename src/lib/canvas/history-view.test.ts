import { describe, expect, it } from 'vitest';

import { historyRows } from './history-view';

const entry = (descriptionKey: string, timestamp: number) => ({ descriptionKey, timestamp });

describe('historyRows', () => {
  it('lists the opening state, then the done steps oldest first', () => {
    const rows = historyRows([entry('history.add.image', 1), entry('history.move', 2)], []);
    expect(rows.map((row) => [row.position, row.state, row.current])).toEqual([[0, 'initial', false], [1, 'done', false], [2, 'done', true]]);
  });

  it('puts the undone steps after them in the order they will be redone', () => {
    // The last redo entry is the next one to redo: here "b" was undone after "c".
    const rows = historyRows([entry('a', 1)], [entry('c', 3), entry('b', 2)]);
    expect(rows.map((row) => [row.position, row.descriptionKey, row.state])).toEqual([
      [0, null, 'initial'], [1, 'a', 'done'], [2, 'b', 'undone'], [3, 'c', 'undone'],
    ]);
    expect(rows[1].current).toBe(true);
  });

  it('marks the opening state as current when nothing is done', () => {
    expect(historyRows([], [entry('a', 1)])[0].current).toBe(true);
  });

  it('flags row 0 as truncated once the cap dropped older steps, without borrowing the next step\'s label', () => {
    const undo = [entry('history.move', 5), entry('history.brush', 6)];
    expect(historyRows(undo, [], true)[0]).toMatchObject({ position: 0, descriptionKey: null, timestamp: null, truncated: true });
    expect(historyRows(undo, [], true)[1]).toMatchObject({ descriptionKey: 'history.move', truncated: false });
    expect(historyRows(undo, [], false)[0]).toMatchObject({ descriptionKey: null, timestamp: null, truncated: false });
  });
});
