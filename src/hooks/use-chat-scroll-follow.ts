import { useCallback, useEffect, useRef, useState } from 'react';

import { isNearBottom, shouldFollow } from '@/lib/chat/scroll-follow';

interface ScrollFollowOptions {
  /** Changes whenever the list grows or settles (message count, status, cards...). */
  changeKey: string;
  /** The newest message is the person's own: it always brings them to the bottom. */
  ownMessage: boolean;
  /** Another session starts at the bottom again. */
  resetKey: string | null;
  settleMilliseconds: number;
}

/**
 * Follows the end of the message list only while the reader is at the bottom. A reader who scrolled up is
 * not pulled back by new content; the list says there is more and offers a way down instead.
 */
export function useChatScrollFollow({ changeKey, ownMessage, resetKey, settleMilliseconds }: ScrollFollowOptions) {
  const containerRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const ours = useRef(false);
  const [showLatest, setShowLatest] = useState(false);

  const scrollToEnd = useCallback((behavior: ScrollBehavior) => {
    ours.current = true;
    endRef.current?.scrollIntoView(behavior === 'smooth' ? { behavior } : { block: 'end' });
    window.setTimeout(() => { ours.current = false; }, 600);
  }, []);

  const onScroll = useCallback(() => {
    const element = containerRef.current;
    if (!element) return;
    const near = isNearBottom(element);
    // Our own (smooth) scrolling passes through positions that are not the bottom.
    if (ours.current && !near) return;
    following.current = near;
    if (near) setShowLatest(false);
  }, []);

  useEffect(() => { following.current = true; setShowLatest(false); }, [resetKey]);

  useEffect(() => {
    if (!shouldFollow({ following: following.current, ownMessage })) {
      setShowLatest(true);
      return undefined;
    }
    following.current = true;
    setShowLatest(false);
    scrollToEnd('smooth');
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

  return { containerRef, endRef, showLatest, jumpToLatest, onScroll };
}
