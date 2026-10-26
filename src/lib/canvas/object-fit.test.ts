import { describe, expect, it } from 'vitest';
import { objectFitStep, type ObjectFitState } from './object-fit';

const none: ObjectFitState = { key: '', quiet: false };

describe('fitting the stage to the current object (#262)', () => {
  it('selects and fits a newly opened object', () => {
    expect(objectFitStep(none, { key: 'a', quiet: false, othersOnBoard: true })).toBe('fresh');
    // An automatic pick on an empty board still fits: there is nothing to keep the view on.
    expect(objectFitStep(none, { key: 'a', quiet: true, othersOnBoard: false })).toBe('fresh');
  });

  it('lets a quiet automatic pick join without moving the view', () => {
    expect(objectFitStep(none, { key: 'a', quiet: true, othersOnBoard: true })).toBe('quiet');
  });

  it('keeps the view still on later re-runs (rail toggle, stage resize) after a quiet pick', () => {
    const quiet: ObjectFitState = { key: 'a', quiet: true };
    expect(objectFitStep(quiet, { key: 'a', quiet: true, othersOnBoard: true })).toBe('hold');
    expect(objectFitStep(quiet, { key: 'a', quiet: true, othersOnBoard: false })).toBe('hold');
  });

  it('fits once the person picks the quietly joined object themselves', () => {
    expect(objectFitStep({ key: 'a', quiet: true }, { key: 'a', quiet: false, othersOnBoard: true })).toBe('fresh');
  });

  it('keeps following the stage size for an object the person opened', () => {
    expect(objectFitStep({ key: 'a', quiet: false }, { key: 'a', quiet: false, othersOnBoard: true })).toBe('follow');
    expect(objectFitStep({ key: 'a', quiet: false }, { key: 'a', quiet: true, othersOnBoard: true })).toBe('follow');
  });

  it('selects a restored object but keeps its saved viewport (#312 follow-up)', () => {
    const restored = { key: 'a', quiet: false, othersOnBoard: true, restored: true, savedViewport: true };
    expect(objectFitStep(none, restored)).toBe('restored');
    // A layout change (rail toggle, stage resize) must not fit a restored object either.
    expect(objectFitStep({ key: 'a', quiet: false, restored: true }, restored)).toBe('hold');
    // Without a saved viewport the restored object is the first content and still gets its fit.
    expect(objectFitStep(none, { ...restored, savedViewport: false })).toBe('fresh');
    // A brand-new object is not restored, even when a viewport is already saved.
    expect(objectFitStep(none, { ...restored, restored: false })).toBe('fresh');
  });

  it('fits a restored object once the person picks it explicitly', () => {
    expect(objectFitStep({ key: 'a', quiet: false, restored: true }, {
      key: 'a', quiet: false, othersOnBoard: true, restored: false, savedViewport: true,
    })).toBe('fresh');
  });

  it('keeps a quiet first result quiet even when it is also restored with a saved viewport', () => {
    // Stage marks a session opened before any object was restored. Its first later result is therefore
    // `restored` and `quiet` at once; the automatic pick must not take the person's selection (#262).
    const late = { key: 'a', quiet: true, othersOnBoard: true, restored: true, savedViewport: true };
    expect(objectFitStep(none, late)).toBe('quiet');
    expect(objectFitStep({ key: 'a', quiet: true }, late)).toBe('hold');
    // An explicit pick of that same result still fits.
    expect(objectFitStep({ key: 'a', quiet: true }, { ...late, quiet: false, restored: false })).toBe('fresh');
    // Without other layers there is nothing to protect: the restored rule may select it.
    expect(objectFitStep(none, { ...late, othersOnBoard: false })).toBe('restored');
  });
});
