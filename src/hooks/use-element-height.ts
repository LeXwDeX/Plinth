'use client';

import { useCallback, useState } from 'react';

/**
 * 实测一个元素的高度（随内容变化更新）。返回回调 ref 与像素高度；元素卸载时为 0。
 * #244 用它让消息区按悬浮任务栈的实际高度留出底部空间。
 */
export function useElementHeight<T extends HTMLElement>(): [(node: T | null) => void, number] {
  const [height, setHeight] = useState(0);
  const ref = useCallback((node: T | null) => {
    if (!node) {
      setHeight(0);
      return undefined;
    }
    const measure = () => setHeight(Math.ceil(node.getBoundingClientRect().height));
    measure();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  return [ref, height];
}
