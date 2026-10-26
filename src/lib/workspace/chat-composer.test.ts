import { describe, expect, it } from 'vitest';

import {
  CHAT_COMPOSER_ELEMENT_ID,
  focusChatComposer,
} from '@/lib/workspace/chat-composer';

describe('chat composer focus', () => {
  it('focuses the composer by its stable id, not the first textarea', () => {
    // 旧实现是 document.querySelector('textarea')：参数面板或弹窗一旦出现
    // textarea，"生成"入口就会聚焦错对象。
    const focused: string[] = [];
    const element = {
      id: CHAT_COMPOSER_ELEMENT_ID,
      focus: () => focused.push(element.id),
    };
    const root = {
      getElementById: (id: string) => (id === CHAT_COMPOSER_ELEMENT_ID ? element : null),
    } as unknown as Document;
    expect(focusChatComposer(root)).toBe(true);
    expect(focused).toEqual([CHAT_COMPOSER_ELEMENT_ID]);
  });

  it('reports a miss instead of throwing when the composer is unmounted', () => {
    const root = { getElementById: () => null } as unknown as Document;
    expect(focusChatComposer(root)).toBe(false);
  });
});
