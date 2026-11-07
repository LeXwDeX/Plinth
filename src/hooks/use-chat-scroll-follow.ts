import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import { isNearBottom, shouldFollow } from '@/lib/chat/scroll-follow';

/** How long after our own scroll a scroll event is still treated as ours. */
const OUR_SCROLL_MILLISECONDS = 600;

interface ScrollFollowOptions {
  /** Changes whenever the list grows or settles (message count, cards...). */
  changeKey: string;
  /**
   * Changes when content already at the end only changes height (status line). While the reader
   * follows, the end is re-pinned before paint instead of animated (#177).
   */
  pinKey?: string;
  /** The newest message is the person's own: it always brings them to the bottom. */
  ownMessage: boolean;
  /** Another session starts at the bottom again. */
  resetKey: string | null;
  settleMilliseconds: number;
}

/**
 * Follows the end of the message list only while the reader is at the bottom. A reader who scrolled up is
 * not pulled back by new content; the list says there is more and offers a way down instead.
 *
 * #177: a reader who is already at the end is re-pinned without animation. Smooth scrolling a list whose
 * height changes at the same moment (a status line appears or goes away, task cards pad the bottom) makes
 * the content visibly jump; an unanimated pin cannot be seen. Smooth scrolling is kept only when the
 * reader is brought down from above.
 */
export function useChatScrollFollow({ changeKey, pinKey, ownMessage, resetKey, settleMilliseconds }: ScrollFollowOptions) {
  const containerRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const ours = useRef(false);
  const lastTop = useRef(0);
  const [showLatest, setShowLatest] = useState(false);

  const markOurs = useCallback(() => {
    ours.current = true;
    window.setTimeout(() => { ours.current = false; }, OUR_SCROLL_MILLISECONDS);
  }, []);

  const scrollToEnd = useCallback((behavior: ScrollBehavior) => {
    const element = containerRef.current;
    if (!element) return;
    lastTop.current = element.scrollTop;
    markOurs();
    if (behavior === 'smooth' && typeof element.scrollTo === 'function') {
      element.scrollTo({ top: element.scrollHeight, behavior });
    } else {
      element.scrollTop = element.scrollHeight;
    }
  }, [markOurs]);

  /** Keeps the end in view without animating, for a reader who was already at the bottom. */
  const keepPinned = useCallback(() => {
    if (!following.current) return;
    scrollToEnd('auto');
  }, [scrollToEnd]);

  const onScroll = useCallback(() => {
    const element = containerRef.current;
    if (!element) return;
    const top = element.scrollTop;
    const near = isNearBottom(element);
    // Our own scroll only ever moves down. An upward move during it is the reader taking over (#177).
    const movedUp = top < lastTop.current - 1;
    lastTop.current = top;
    if (ours.current && !movedUp) {
      if (!near) return;
      ours.current = false;
      following.current = true;
      setShowLatest(false);
      return;
    }
    ours.current = false;
    following.current = near;
    if (near) setShowLatest(false);
  }, []);

  useEffect(() => { following.current = true; setShowLatest(false); }, [resetKey]);

  // A taller composer or a resized window changes how much of the list is visible; a reader at the end
  // stays at the end (the floating task cards use the same idea through their measured padding) (#177).
  useEffect(() => {
    const element = containerRef.current;
    if (!element || typeof ResizeObserver === 'undefined') return undefined;
    const observer = new ResizeObserver(() => { keepPinned(); });
    observer.observe(element);
    return () => observer.disconnect();
  }, [keepPinned]);

  // Before paint: when the status line appears or goes away, the list must not show the old position.
  useLayoutEffect(() => {
    if (pinKey) keepPinned();
  }, [pinKey, keepPinned]);

  useLayoutEffect(() => {
    if (!shouldFollow({ following: following.current, ownMessage })) {
      setShowLatest(true);
      return undefined;
    }
    const atEnd = following.current;
    following.current = true;
    setShowLatest(false);
    // Already at the end: pin instantly. Otherwise the reader's own message brings them down smoothly.
    scrollToEnd(atEnd ? 'auto' : 'smooth');
    // Cards and pictures settle their height a moment later; look again once they have.
    const settle = window.setTimeout(() => {
      if (following.current) scrollToEnd('auto');
    }, settleMilliseconds);
    return () => window.clearTimeout(settle);
    // ownMessage is read at the moment the list changes, not a trigger of its own.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeKey, scrollToEnd, settleMilliseconds]);

  const jumpToLatest = useCallback(() => {
    following.current = true;
    setShowLatest(false);
    scrollToEnd('smooth');
  }, [scrollToEnd]);

  return { containerRef, endRef, showLatest, jumpToLatest, keepPinned, onScroll };
}
