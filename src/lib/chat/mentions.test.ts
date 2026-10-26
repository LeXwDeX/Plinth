import { describe, expect, it } from 'vitest';

import { boardMentionCandidate, mentionsBoard, mentionsForTurn, withBoardMention, type MentionCandidate, activeMention, mentionsToken, filterMentions, insertMention, mentionCandidates, overridingHandle, typedHandles } from './mentions';

const att = (handle: string, kind: 'image' | 'model', taskId = 't') => ({ handle, kind, taskId });

describe('mentionCandidates', () => {
  it('lists each server-assigned handle once, newest first', () => {
    const messages = [
      { attachments: [att('@img1', 'image'), att('@model1', 'model')] },
      { attachments: undefined },
      { attachments: [att('@img2', 'image'), att('@img1', 'image')] },
    ];
    expect(mentionCandidates(messages).map((item) => item.handle)).toEqual(['@img2', '@img1', '@model1']);
  });

  it('is empty when no message has attachments', () => {
    expect(mentionCandidates([{ attachments: undefined }])).toEqual([]);
  });
});

describe('activeMention', () => {
  it('finds the @ word before the caret', () => {
    expect(activeMention('把 @im', 5)).toEqual({ start: 2, query: 'im' });
    expect(activeMention('@', 1)).toEqual({ start: 0, query: '' });
    expect(activeMention('做成 @model1', 10)).toEqual({ start: 3, query: 'model1' });
  });

  it('ignores an @ inside a word or after a space has ended the word', () => {
    expect(activeMention('a@b', 3)).toBeNull();
    expect(activeMention('@img1 然后', 8)).toBeNull();
    expect(activeMention('没有', 2)).toBeNull();
    expect(activeMention('@x', 0)).toBeNull();
  });
});

describe('filterMentions and insertMention', () => {
  const list = [att('@img2', 'image'), att('@img1', 'image'), att('@model1', 'model')];
  it('filters by what follows the @', () => {
    expect(filterMentions(list, '').length).toBe(3);
    expect(filterMentions(list, 'mod').map((item) => item.handle)).toEqual(['@model1']);
    expect(filterMentions(list, '@img').length).toBe(2);
    expect(filterMentions(list, 'zzz')).toEqual([]);
  });

  it('writes the handle in place of the typed word and puts the caret after it', () => {
    const mention = activeMention('把 @im 改成蓝色', 5)!;
    expect(insertMention('把 @im 改成蓝色', mention, 5, '@img2')).toEqual({ value: '把 @img2 改成蓝色', caret: 8 });
    const tail = activeMention('用 @', 3)!;
    expect(insertMention('用 @', tail, 3, '@model1')).toEqual({ value: '用 @model1 ', caret: 10 });
  });
});

describe('typedHandles and overridingHandle', () => {
  it('finds the handles written in the text, in order', () => {
    expect(typedHandles('用 @img1 去背景，参考 @model2')).toEqual(['@img1', '@model2']);
    expect(typedHandles('@img3\n再来')).toEqual(['@img3']);
    expect(typedHandles('邮箱 a@img1.com 和 @image 不算')).toEqual([]);
  });

  it('names the handle that takes the turn from the current object', () => {
    expect(overridingHandle('用 @img1 去背景', '@model1')).toBe('@img1');
    expect(overridingHandle('把 @img1 和 @img2 合起来', '@model1')).toBe('@img2');
  });

  it('does not take the turn when the text names the current object, names nothing, or the object has no handle', () => {
    expect(overridingHandle('改一下 @img5', '@img5')).toBe('');
    expect(overridingHandle('把 @img5 和 @img1 合起来', '@img5')).toBe('');
    expect(overridingHandle('换成红黑配色', '@img5')).toBe('');
    expect(overridingHandle('用 @img1 去背景', '')).toBe('');
  });

  it('does not mistake a longer number for the current handle', () => {
    expect(overridingHandle('用 @img12 去背景', '@img1')).toBe('@img12');
  });
});

describe('mentionsToken', () => {
  it('allows CJK and punctuation after a token but not an ASCII word character', () => {
    expect(mentionsToken('@图层2改成红色', '@图层2')).toBe(true);
    expect(mentionsToken('see @img1, ok', '@img1')).toBe(true);
    expect(mentionsToken('@img1x', '@img1')).toBe(false);
    expect(mentionsToken('@img1_', '@img1')).toBe(false);
    expect(mentionsToken('a@img1', '@img1')).toBe(false);
  });
});

describe('canvas (board) mention', () => {
  const board = boardMentionCandidate('画布');
  const layer: MentionCandidate = { handle: '@图层1', kind: 'image', url: '/u', source: 'canvas', sources: ['canvas'], name: '图层 1' };

  it('is first in the list and filterable', () => {
    const list = withBoardMention([layer], board);
    expect(list[0].handle).toBe('@画布');
    expect(filterMentions(list, '画')[0].handle).toBe('@画布');
    expect(withBoardMention([layer], null)).toEqual([layer]);
  });
  it('is detected in the text but never sent as a mention claim', () => {
    const list = withBoardMention([layer], board);
    expect(mentionsBoard('把 @画布 变亮', list)).toBe(true);
    expect(mentionsBoard('把 @画布2 变亮', list)).toBe(false);
    expect(mentionsForTurn('把 @画布 变亮', list, [board])).toEqual([]);
  });
});
