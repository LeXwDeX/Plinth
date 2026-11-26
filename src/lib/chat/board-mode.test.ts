import { describe, expect, it } from 'vitest';

import { boardInputSource, boardMode } from './board-mode';

// #644: a message is a canvas turn only when the person says so (the toggle, 「发送到对话」) or the board itself
// asks for it (a selection or mask, `@画布`); every other turn leaves the board out.
describe('boardMode', () => {
  const off = { chosen: false, artboard: true, maskSteps: 0, namesBoard: false };

  it('is off by default, even with a board on the session', () => {
    expect(boardMode(off)).toEqual({ on: false, forced: false });
  });

  it('is on for the message the person switched on', () => {
    expect(boardMode({ ...off, chosen: true })).toEqual({ on: true, forced: false });
  });

  it('turns on by itself for a selection or mask on the board, and for @画布', () => {
    expect(boardMode({ ...off, maskSteps: 2 })).toEqual({ on: true, forced: true });
    expect(boardMode({ ...off, namesBoard: true })).toEqual({ on: true, forced: true });
    expect(boardMode({ ...off, chosen: true, maskSteps: 1 })).toEqual({ on: true, forced: true });
  });

  it('is never on without a board to send', () => {
    expect(boardMode({ chosen: true, artboard: false, maskSteps: 3, namesBoard: true })).toEqual({ on: false, forced: false });
  });
});

describe('boardInputSource', () => {
  it('names the Schema canvas source only for a turn that sends the snapshot', () => {
    expect(boardInputSource(true, 'canvas')).toBe('canvas');
    expect(boardInputSource(false, 'canvas')).toBeUndefined();
    expect(boardInputSource(true, '')).toBeUndefined();
  });
});
