// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ResizeHandle } from './chat-column';

const frames: FrameRequestCallback[] = [];

beforeEach(() => {
  frames.length = 0;
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    frames.push(callback);
    return frames.length;
  });
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => undefined);
  // jsdom has no pointer capture.
  Object.assign(HTMLElement.prototype, {
    setPointerCapture: () => undefined,
    releasePointerCapture: () => undefined,
    hasPointerCapture: () => false,
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

// jsdom has no PointerEvent with coordinates; a mouse event of the pointer type carries clientX to React.
function pointer(target: HTMLElement, type: 'pointerdown' | 'pointermove' | 'pointerup', clientX: number) {
  fireEvent(target, new MouseEvent(type, { bubbles: true, cancelable: true, clientX }));
}

function setup(width = 500, max = 720) {
  const onPreview = vi.fn();
  const onCommit = vi.fn();
  render(<ResizeHandle width={width} max={max} label="resize" onPreview={onPreview} onCommit={onCommit} />);
  return { handle: screen.getByTestId('chat-resize-handle'), onPreview, onCommit };
}

describe('the chat divider (#695)', () => {
  it('tells its range from the window', () => {
    const { handle } = setup(500, 720);
    expect(handle.getAttribute('aria-valuemin')).toBe('440');
    expect(handle.getAttribute('aria-valuemax')).toBe('720');
    expect(handle.getAttribute('aria-valuenow')).toBe('500');
  });

  it('steps 16px with an arrow, 64px with Shift, and goes to either end with Home and End', () => {
    const { handle, onCommit } = setup(500, 720);
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    fireEvent.keyDown(handle, { key: 'ArrowLeft', shiftKey: true });
    fireEvent.keyDown(handle, { key: 'Home' });
    fireEvent.keyDown(handle, { key: 'End' });
    expect(onCommit.mock.calls.map(([value]) => value)).toEqual([516, 440, 440, 720]);
  });

  it('never steps past the window cap', () => {
    const { handle, onCommit } = setup(700, 720);
    fireEvent.keyDown(handle, { key: 'ArrowRight', shiftKey: true });
    expect(onCommit).toHaveBeenLastCalledWith(720);
  });

  it('shows a drag once per frame and stores it only when the drag ends', () => {
    const { handle, onPreview, onCommit } = setup(500, 720);
    pointer(handle, 'pointerdown', 100);
    pointer(handle, 'pointermove', 150);
    pointer(handle, 'pointermove', 900);
    expect(frames).toHaveLength(1);
    expect(onCommit).not.toHaveBeenCalled();
    act(() => frames[0](0));
    // The last position of the frame, held at the cap.
    expect(onPreview).toHaveBeenLastCalledWith(720);
    pointer(handle, 'pointerup', 900);
    expect(onCommit).toHaveBeenCalledTimes(1);
    expect(onCommit).toHaveBeenCalledWith(720);
    expect(onPreview).toHaveBeenLastCalledWith(null);
  });
});
