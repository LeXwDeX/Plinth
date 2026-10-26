import { describe, expect, it } from 'vitest';
import { createStyleGesture, selectionStyle, styleUpdates } from './style-patch';

const nodes = [
  { id: 's', type: 'shape' as const, strokeColor: '#111', fillColor: '#f00', strokeWidth: 2 },
  { id: 'p', type: 'pencil' as const, strokeColor: '#222' },
  { id: 't', type: 'text' as const, strokeColor: '#333', fontSize: 40, opacity: 0.5 },
  { id: 'i', type: 'image' as const },
  { id: 'l', type: 'shape' as const, locked: true },
];

describe('styleUpdates', () => {
  it('sets fill on shapes only, skipping text, pencil, pictures and locked nodes', () => {
    expect(styleUpdates(nodes, ['s', 'p', 't', 'i', 'l'], { fillColor: 'transparent' })).toEqual([
      { id: 's', patch: { fillColor: 'transparent' } },
    ]);
  });
  it('applies colour to shapes, pencil and text, size only to text, opacity to all styled', () => {
    expect(styleUpdates(nodes, ['s', 'p', 't', 'i'], { strokeColor: '#fff', fontSize: 30, opacity: 0.2 })).toEqual([
      { id: 's', patch: { strokeColor: '#fff', opacity: 0.2 } },
      { id: 'p', patch: { strokeColor: '#fff', opacity: 0.2 } },
      { id: 't', patch: { strokeColor: '#fff', fontSize: 30, opacity: 0.2 } },
    ]);
  });
  it('is empty for a picture-only selection', () => {
    expect(styleUpdates(nodes, ['i'], { opacity: 0.5 })).toEqual([]);
    expect(selectionStyle(nodes, ['i'], { fontSize: 24 })).toBeNull();
  });
});

describe('selectionStyle', () => {
  it('shows the first values and flags which kinds are selected', () => {
    expect(selectionStyle(nodes, ['t', 's'], { fontSize: 24 })).toMatchObject({
      hasShape: true, hasText: true, strokeColor: '#111', fillColor: '#f00', fontSize: 40, opacity: 1,
    });
    expect(selectionStyle([{ id: 'x', type: 'text' as const }], ['x'], { fontSize: 24 })?.fontSize).toBe(24);
  });
});

describe('createStyleGesture', () => {
  it('opens one history entry per gesture', () => {
    const gesture = createStyleGesture();
    expect([gesture.begin(), gesture.begin(), gesture.begin()]).toEqual([true, false, false]);
    gesture.end();
    expect(gesture.begin()).toBe(true);
  });
  it('a missed commit does not swallow the next drag: closing on pointer down starts a fresh entry', () => {
    const gesture = createStyleGesture();
    expect(gesture.begin()).toBe(true); // first drag, onValueCommit never fires
    gesture.end(); // next pointerdown closes the stale gesture
    expect(gesture.begin()).toBe(true); // the second drag records its own entry
    expect(gesture.begin()).toBe(false);
  });
});
