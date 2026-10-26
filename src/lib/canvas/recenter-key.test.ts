import { describe, expect, it } from 'vitest';

import { createRecenterGesture } from './recenter-key';

describe('createRecenterGesture', () => {
  it('recenters on a short press', () => {
    const gesture = createRecenterGesture(250);
    gesture.press(1000);
    expect(gesture.release(1100)).toBe(true);
  });
  it('does not recenter after a drag', () => {
    const gesture = createRecenterGesture(250);
    gesture.press(1000);
    gesture.pointerDown();
    expect(gesture.release(1100)).toBe(false);
  });
  it('does not recenter on a long hold', () => {
    const gesture = createRecenterGesture(250);
    gesture.press(1000);
    expect(gesture.release(1400)).toBe(false);
  });
  it('ignores a release without a press and key repeat', () => {
    const gesture = createRecenterGesture(250);
    expect(gesture.release(10)).toBe(false);
    gesture.press(0);
    gesture.press(900);
    expect(gesture.release(100)).toBe(true);
  });
});
