import { describe, expect, it } from 'vitest';

import { createSpaceGesture, isEditableTarget, ownsNavigationKeys, stageKeyAction, type StageKeyConfig, type StageKeyEvent } from './stage-keys';

const config: StageKeyConfig = { spaceCode: 'Space', previousKey: 'ArrowLeft', nextKey: 'ArrowRight', closeKey: 'Escape' };

/** A stand-in for a DOM element: closest() answers true for the selector parts it was built to match. */
function element(matches: string[], extra: { isContentEditable?: boolean } = {}) {
  const hit = (part: string, name: string) => part === name || part.startsWith(`${name}:`) || part.startsWith(`${name}[`) || part === `[${name}]` || part === `[role=${name}]`;
  const self = {
    ...extra,
    matches: (selector: string) => selector.split(',').some((part) => matches.some((name) => hit(part, name))),
    closest: (selector: string): unknown => (self.matches(selector) ? self : null),
  };
  return self;
}

const press = (key: string, code = key, target: unknown = element([]), extra: Partial<StageKeyEvent> = {}): StageKeyEvent => ({
  key, code, ctrlKey: false, metaKey: false, altKey: false, target, ...extra,
});

describe('stageKeyAction', () => {
  it('maps the stage keys', () => {
    expect(stageKeyAction(press(' ', 'Space'), config)).toBe('toggle-lightbox');
    expect(stageKeyAction(press('ArrowLeft'), config)).toBe('previous');
    expect(stageKeyAction(press('ArrowRight'), config)).toBe('next');
    expect(stageKeyAction(press('Escape'), config)).toBe('escape');
    expect(stageKeyAction(press('x'), config)).toBeNull();
  });

  it('has no focus-mode key: F is an ordinary key on the stage', () => {
    expect(stageKeyAction(press('f', 'KeyF'), config)).toBeNull();
    expect(stageKeyAction(press('F', 'KeyF'), config)).toBeNull();
  });

  it('ignores every key while the focus is in a text input or textarea', () => {
    const input = element(['input']);
    const textarea = element(['textarea']);
    for (const target of [input, textarea, element([], { isContentEditable: true })]) {
      for (const [key, code] of [[' ', 'Space'], ['ArrowLeft', 'ArrowLeft'], ['ArrowRight', 'ArrowRight'], ['Escape', 'Escape']]) {
        expect(stageKeyAction(press(key, code, target), config), `${key} in ${JSON.stringify(target)}`).toBeNull();
      }
    }
    expect(isEditableTarget(element(['textarea']))).toBe(true);
    expect(isEditableTarget(element([]))).toBe(false);
    expect(isEditableTarget(null)).toBe(false);
  });

  it('leaves Space and the arrows to a control that uses them, unless it opted in', () => {
    const button = element(['button']);
    expect(stageKeyAction(press(' ', 'Space', button), config)).toBeNull();
    expect(stageKeyAction(press('ArrowLeft', 'ArrowLeft', button), config)).toBeNull();
    // Escape still works from a button
    expect(stageKeyAction(press('Escape', 'Escape', button), config)).toBe('escape');
    const passthrough = element(['button', 'data-shortcut-passthrough']);
    expect(ownsNavigationKeys(button)).toBe(true);
    expect(stageKeyAction(press(' ', 'Space', passthrough), config)).toBe('toggle-lightbox');
  });

  it('lets a strip tile keep Space but step versions with the arrows', () => {
    const tile = element(['button', 'data-shortcut-arrows']);
    expect(stageKeyAction(press(' ', 'Space', tile), config)).toBeNull();
    expect(stageKeyAction(press('ArrowLeft', 'ArrowLeft', tile), config)).toBe('previous');
    expect(stageKeyAction(press('ArrowRight', 'ArrowRight', tile), config)).toBe('next');
  });

  it('ignores chords and composition', () => {
    expect(stageKeyAction(press('ArrowLeft', 'ArrowLeft', element([]), { metaKey: true }), config)).toBeNull();
    expect(stageKeyAction(press('ArrowLeft', 'ArrowLeft', element([]), { ctrlKey: true }), config)).toBeNull();
    expect(stageKeyAction(press('Escape', 'Escape', element([]), { altKey: true }), config)).toBeNull();
    expect(stageKeyAction(press(' ', 'Space', element([]), { metaKey: true }), config)).toBeNull();
    expect(stageKeyAction(press('ArrowLeft', 'ArrowLeft', element([]), { isComposing: true }), config)).toBeNull();
  });
});

describe('createSpaceGesture', () => {
  it('treats a press and release as a tap', () => {
    const gesture = createSpaceGesture();
    gesture.press();
    expect(gesture.isHeld).toBe(true);
    expect(gesture.release()).toBe(true);
    expect(gesture.isHeld).toBe(false);
  });

  it('treats a press with a pointer drag in between as the canvas pan, not a tap', () => {
    const gesture = createSpaceGesture();
    gesture.press();
    gesture.pointerDown();
    expect(gesture.release()).toBe(false);
    // the next tap is clean again
    gesture.press();
    expect(gesture.release()).toBe(true);
  });

  it('ignores a pointer without Space and a release without a press', () => {
    const gesture = createSpaceGesture();
    gesture.pointerDown();
    expect(gesture.release()).toBe(false);
    gesture.press();
    gesture.cancel();
    expect(gesture.release()).toBe(false);
  });
});
