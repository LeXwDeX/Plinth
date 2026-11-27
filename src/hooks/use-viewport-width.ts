'use client';

import { useEffect, useState } from 'react';

/** Used where there is no window (server render, node tests): a common desktop width. */
export const VIEWPORT_WIDTH_FALLBACK = 1440;

function currentViewportWidth(): number {
  if (typeof window === 'undefined' || !Number.isFinite(window.innerWidth) || window.innerWidth <= 0) return VIEWPORT_WIDTH_FALLBACK;
  return window.innerWidth;
}

/**
 * The window's inner width in pixels, followed on resize at most once per animation frame (#695, #696):
 * the chat column's widest width and the right panel's folding follow it.
 */
export function useViewportWidth(): number {
  const [width, setWidth] = useState(currentViewportWidth);
  useEffect(() => {
    if (typeof window === 'undefined') return undefined;
    let frame = 0;
    const read = () => {
      frame = 0;
      setWidth(currentViewportWidth());
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
  return width;
}
