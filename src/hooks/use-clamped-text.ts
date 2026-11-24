import { useCallback, useRef, useState } from 'react';

/**
 * #546: whether a text element cut by `line-clamp` or `truncate` hides some of its text. Layout has no size in a
 * test, so the check is a pure function of the measured sizes.
 */
export function isClamped(element: Pick<HTMLElement, 'scrollHeight' | 'clientHeight' | 'scrollWidth' | 'clientWidth'> | null): boolean {
  if (!element) return false;
  return element.scrollHeight > element.clientHeight + 1 || element.scrollWidth > element.clientWidth + 1;
}

/** The marker on a clamped text inside a larger control (a choice option, a list row); the hook measures those. */
export const CLAMP_ATTRIBUTE = 'data-clamp';

/** Whether the element, or any marked text inside it, is cut. */
export function hasClampedText(element: HTMLElement | null): boolean {
  if (!element) return false;
  const marked = Array.from(element.querySelectorAll<HTMLElement>(`[${CLAMP_ATTRIBUTE}]`));
  return marked.length > 0 ? marked.some(isClamped) : isClamped(element);
}

/**
 * The ref for a clamped text (or a control holding texts marked with `data-clamp`) and whether it is cut, read when the pointer or the keyboard reaches its control (only
 * then can its width have changed). The caller suppresses its full-text hint while `clamped` is false, so a short
 * description never shows a hint that only repeats it.
 */
export function useClampedText<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [clamped, setClamped] = useState(false);
  const measure = useCallback(() => setClamped(hasClampedText(ref.current)), []);
  return { ref, clamped, measure };
}
