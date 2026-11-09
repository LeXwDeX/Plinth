import { describe, expect, it } from 'vitest';

import { ATTACHMENT_PURPOSES, purposeNote, withPurposeNote } from './attachment-purpose';

describe('withPurposeNote', () => {
  it('puts the purpose on its own line after what was written', () => {
    expect(withPurposeNote(' 按草图重画 ', '（附图用途：参考构图）')).toBe('按草图重画\n（附图用途：参考构图）');
  });

  it('sends the text as it is when no purpose was chosen', () => {
    expect(withPurposeNote('按草图重画', '')).toBe('按草图重画');
  });

  it('is only the note when nothing else was written', () => {
    expect(withPurposeNote('  ', '（附图用途：当作输入图）')).toBe('（附图用途：当作输入图）');
  });

  it('offers style, composition and input, in that order', () => {
    expect(ATTACHMENT_PURPOSES).toEqual(['style', 'layout', 'input']);
  });
});

describe('purposeNote', () => {
  const texts: Record<string, string> = {
    'ui.studio.composer.attachment.note_style': '（附图用途：参考风格）',
    'ui.studio.composer.attachment.note_input': '（附图用途：当作输入图）',
    'ui.studio.composer.attachment.note_mixed': '（附图用途：{items}）',
    'ui.studio.composer.attachment.note_item': '第 {index} 张{purpose}',
    'ui.studio.composer.attachment.note_separator': '；',
    'ui.studio.composer.attachment.purpose_style': '参考风格',
    'ui.studio.composer.attachment.purpose_layout': '参考构图',
    'ui.studio.composer.attachment.purpose_input': '当作输入图',
  };
  const text = (key: string) => texts[key] ?? key;

  it('keeps the one sentence when every picture has the same purpose', () => {
    expect(purposeNote(['style', 'style'], text)).toBe('（附图用途：参考风格）');
    expect(purposeNote(['input'], text)).toBe('（附图用途：当作输入图）');
  });

  it('names each picture when the purposes differ, instead of only the last choice (#212)', () => {
    expect(purposeNote(['style', 'input'], text)).toBe('（附图用途：第 1 张参考风格；第 2 张当作输入图）');
  });

  it('lists only the pictures that have a purpose when some have none', () => {
    expect(purposeNote([null, 'input'], text)).toBe('（附图用途：第 2 张当作输入图）');
    expect(purposeNote(['layout', null, 'layout'], text)).toBe('（附图用途：第 1 张参考构图；第 3 张参考构图）');
  });

  it('writes nothing when no picture has a purpose', () => {
    expect(purposeNote([], text)).toBe('');
    expect(purposeNote([null, null], text)).toBe('');
  });
});
