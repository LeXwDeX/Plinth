// @vitest-environment jsdom
import React from 'react';
import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useViewportWidth } from './use-viewport-width';

function Probe() {
  return <span data-testid="width">{useViewportWidth()}</span>;
}

function setInnerWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width });
}

afterEach(() => {
  vi.restoreAllMocks();
  setInnerWidth(1024);
});

describe('the viewport width (#695)', () => {
  it('starts at the window width and follows a resize once per frame', () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    setInnerWidth(1440);
    render(<Probe />);
    expect(screen.getByTestId('width').textContent).toBe('1440');

    setInnerWidth(1100);
    act(() => {
      window.dispatchEvent(new Event('resize'));
      window.dispatchEvent(new Event('resize'));
    });
    // Two resize events in one frame ask for one read.
    expect(frames).toHaveLength(1);
    expect(screen.getByTestId('width').textContent).toBe('1440');
    act(() => frames[0](0));
    expect(screen.getByTestId('width').textContent).toBe('1100');
  });

  it('stops listening when unmounted', () => {
    const cancel = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => undefined);
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 7);
    const removed = vi.spyOn(window, 'removeEventListener');
    const { unmount } = render(<Probe />);
    act(() => { window.dispatchEvent(new Event('resize')); });
    unmount();
    expect(removed).toHaveBeenCalledWith('resize', expect.any(Function));
    expect(cancel).toHaveBeenCalledWith(7);
  });
});
