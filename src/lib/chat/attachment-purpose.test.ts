import { describe, expect, it } from 'vitest';

import { ATTACHMENT_PURPOSES, withPurposeNote } from './attachment-purpose';

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
