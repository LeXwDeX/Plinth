import { describe, expect, it, vi } from 'vitest';
import { onHeldKeysLost } from './held-keys';

function page() {
  const win = new EventTarget();
  const doc = Object.assign(new EventTarget(), { visibilityState: 'visible' as DocumentVisibilityState });
  return { win, doc };
}

describe('held keys lost with the window (#263)', () => {
  it('releases when the window loses focus, e.g. Cmd+Tab while Space is held', () => {
    const { win, doc } = page();
    const release = vi.fn();
    onHeldKeysLost(win, doc, release);
    win.dispatchEvent(new Event('blur'));
    expect(release).toHaveBeenCalledTimes(1);
  });

  it('releases when the page becomes hidden, not when it becomes visible again', () => {
    const { win, doc } = page();
    const release = vi.fn();
    onHeldKeysLost(win, doc, release);
    doc.visibilityState = 'hidden';
    doc.dispatchEvent(new Event('visibilitychange'));
    expect(release).toHaveBeenCalledTimes(1);
    doc.visibilityState = 'visible';
    doc.dispatchEvent(new Event('visibilitychange'));
    expect(release).toHaveBeenCalledTimes(1);
  });

  it('stops listening once disposed', () => {
    const { win, doc } = page();
    const release = vi.fn();
    const dispose = onHeldKeysLost(win, doc, release);
    dispose();
    win.dispatchEvent(new Event('blur'));
    doc.visibilityState = 'hidden';
    doc.dispatchEvent(new Event('visibilitychange'));
    expect(release).not.toHaveBeenCalled();
  });
});
