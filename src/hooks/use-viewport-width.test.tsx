// @vitest-environment jsdom
import React from 'react';
import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useViewportValue, useViewportWidth } from './use-viewport-width';

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

describe('a value read from the viewport width', () => {
  it('renders again only when the value changes, not on every resize frame, and follows its own inputs', () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    let renders = 0;
    function Cap({ reserve }: { reserve: number }) {
      renders += 1;
      return <span data-testid="cap">{useViewportValue((width) => Math.min(900, width - reserve))}</span>;
    }
    const resize = (width: number) => {
      setInnerWidth(width);
      act(() => { window.dispatchEvent(new Event('resize')); });
      act(() => frames[frames.length - 1](0));
    };
    setInnerWidth(1600);
    const view = render(<Cap reserve={300} />);
    expect(screen.getByTestId('cap').textContent).toBe('900');
    const settled = renders;
    // The window narrows, but the clamped value stays the same: nothing renders again.
    resize(1500);
    resize(1300);
    expect(renders).toBe(settled);
    resize(1000);
    expect(screen.getByTestId('cap').textContent).toBe('700');
    expect(renders).toBe(settled + 1);
    // Its own inputs change: the value is read again at the current width.
    view.rerender(<Cap reserve={500} />);
    expect(screen.getByTestId('cap').textContent).toBe('500');
    // A resize whose value equals what was shown before the input changed still shows the new value.
    resize(1200);
    expect(screen.getByTestId('cap').textContent).toBe('700');
  });
});
