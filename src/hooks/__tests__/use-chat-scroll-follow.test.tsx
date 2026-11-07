// @vitest-environment jsdom

import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useChatScrollFollow } from '@/hooks/use-chat-scroll-follow';

function Harness({ changeKey, pinKey = '', ownMessage = false }: { changeKey: string; pinKey?: string; ownMessage?: boolean }) {
  const { containerRef, endRef, showLatest, onScroll } = useChatScrollFollow({
    changeKey,
    pinKey,
    ownMessage,
    resetKey: 'session-1',
    settleMilliseconds: 400,
  });
  return (
    <div>
      <div ref={containerRef} onScroll={onScroll} data-testid="list">
        <div ref={endRef} />
      </div>
      <output data-testid="latest">{showLatest ? '1' : ''}</output>
    </div>
  );
}

/** jsdom has no layout: give the list the metrics the behaviour reads. */
function measure(list: HTMLElement, top: number, height = 1000, client = 400) {
  Object.defineProperty(list, 'scrollTop', { value: top, writable: true, configurable: true });
  Object.defineProperty(list, 'scrollHeight', { value: height, configurable: true });
  Object.defineProperty(list, 'clientHeight', { value: client, configurable: true });
}

function stubSmoothScroll(list: HTMLElement) {
  const scrollTo = vi.fn(function (this: HTMLElement, options: ScrollToOptions) {
    if (typeof options.top === 'number') this.scrollTop = options.top;
  });
  Object.defineProperty(list, 'scrollTo', { value: scrollTo, writable: true, configurable: true });
  return scrollTo;
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe('the message list scroll follow (#177)', () => {
  it('pins new content instantly while the reader is at the end', () => {
    const view = render(<Harness changeKey="a" />);
    const list = view.getByTestId('list');
    measure(list, 600);
    const scrollTo = stubSmoothScroll(list);
    scrollTo.mockClear();

    view.rerender(<Harness changeKey="b" />);

    // No animation while the end is already in view: nothing can be seen moving.
    expect(scrollTo).not.toHaveBeenCalled();
    expect(list.scrollTop).toBe(1000);
  });

  it('brings a scrolled-up reader down smoothly for their own message', () => {
    const view = render(<Harness changeKey="a" />);
    const list = view.getByTestId('list');
    measure(list, 600);
    const scrollTo = stubSmoothScroll(list);
    act(() => { fireEvent.scroll(list); });
    // The reader scrolled up; their own message still brings them to the end.
    list.scrollTop = 100;
    act(() => { fireEvent.scroll(list); });
    scrollTo.mockClear();

    view.rerender(<Harness changeKey="b" ownMessage />);

    expect(scrollTo).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' });
    expect(view.getByTestId('latest').textContent).toBe('');
  });

  it('pins a status change instantly instead of animating it', () => {
    const view = render(<Harness changeKey="a" />);
    const list = view.getByTestId('list');
    measure(list, 0);
    const scrollTo = stubSmoothScroll(list);
    scrollTo.mockClear();

    view.rerender(<Harness changeKey="a" pinKey="正在生成" />);

    // No animation at all: the end is set before paint, so the status line cannot show as a jump.
    expect(scrollTo).not.toHaveBeenCalled();
    expect(list.scrollTop).toBe(1000);
  });

  it('leaves a reader who scrolled up where they are', () => {
    const view = render(<Harness changeKey="a" />);
    const list = view.getByTestId('list');
    measure(list, 600);
    const scrollTo = stubSmoothScroll(list);
    act(() => { fireEvent.scroll(list); });
    // The reader scrolls up, away from the end.
    list.scrollTop = 200;
    act(() => { fireEvent.scroll(list); });
    scrollTo.mockClear();

    view.rerender(<Harness changeKey="a" pinKey="正在生成" />);
    expect(list.scrollTop).toBe(200);

    view.rerender(<Harness changeKey="b" pinKey="正在生成" />);
    expect(scrollTo).not.toHaveBeenCalled();
    expect(view.getByTestId('latest').textContent).toBe('1');
  });

  it('lets the reader take over in the middle of our own smooth scroll', () => {
    const view = render(<Harness changeKey="a" />);
    const list = view.getByTestId('list');
    measure(list, 600);
    const scrollTo = stubSmoothScroll(list);
    act(() => { fireEvent.scroll(list); });
    // The reader is above the end when their own message starts the smooth scroll.
    list.scrollTop = 100;
    act(() => { fireEvent.scroll(list); });
    scrollTo.mockClear();

    view.rerender(<Harness changeKey="b" ownMessage />);
    expect(scrollTo).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' });
    // The smooth animation scrolls down through the end.
    act(() => { fireEvent.scroll(list); });

    // A wheel or key that scrolls up during the 600 ms window is the reader's, not ours.
    list.scrollTop = 200;
    act(() => { fireEvent.scroll(list); });
    scrollTo.mockClear();
    view.rerender(<Harness changeKey="c" />);

    expect(scrollTo).not.toHaveBeenCalled();
    expect(view.getByTestId('latest').textContent).toBe('1');
  });
});
