'use client';

import { useEffect, useLayoutEffect, useReducer, useRef } from 'react';

/** Used where there is no window (server render, node tests): a common desktop width. */
export const VIEWPORT_WIDTH_FALLBACK = 1440;

function currentViewportWidth(): number {
  if (typeof window === 'undefined' || !Number.isFinite(window.innerWidth) || window.innerWidth <= 0) return VIEWPORT_WIDTH_FALLBACK;
  return window.innerWidth;
}

/**
 * A value read from the window's inner width, followed on resize at most once per animation frame (#695, #696):
 * the chat column's widest width and the right panel's folding follow it. The component renders again only when
 * the value it shows changes, not on every frame of a window drag; `derive` may close over other inputs, and a
 * render for those reads it again at the current width.
 */
export function useViewportValue<T>(derive: (width: number) => T): T {
  const width = useRef<number | null>(null);
  if (width.current === null) width.current = currentViewportWidth();
  const deriveRef = useRef(derive);
  deriveRef.current = derive;
  const shown = useRef<{ value: T } | null>(null);
  const [, rerender] = useReducer((count: number) => count + 1, 0);
  useEffect(() => {
    if (typeof window === 'undefined') return undefined;
    let frame = 0;
    const read = () => {
      frame = 0;
      width.current = currentViewportWidth();
      if (!shown.current || !Object.is(deriveRef.current(width.current), shown.current.value)) rerender();
    };
    const onResize = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(read);
    };
    // The window may have changed between the first render and this effect.
    read();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, []);
  const value = derive(width.current);
  // What the committed render shows; a resize that reads the same value renders nothing.
  useLayoutEffect(() => { shown.current = { value }; });
  return value;
}

const identity = (width: number) => width;

/** The window's inner width in pixels; renders on every width change (see `useViewportValue` for a derived value). */
export function useViewportWidth(): number {
  return useViewportValue(identity);
}
