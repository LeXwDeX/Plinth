import { describe, expect, it } from 'vitest';

import { ChatBrowserError } from './browser-client';
import { turnFailureText } from './turn-error-text';

describe('the text of a failed turn (#665)', () => {
  it('keeps the server refusal and the Schema interruption text', () => {
    expect(turnFailureText(new ChatBrowserError('余额不足', 402), [], 'send failed')).toContain('余额不足');
    expect(turnFailureText(new Error('连接中断'), ['连接中断'], 'send failed')).toBe('连接中断');
  });

  it('never shows a raw browser error', () => {
    expect(turnFailureText(new TypeError('Failed to fetch'), ['连接中断'], 'send failed')).toBe('send failed');
    expect(turnFailureText('boom', [], 'send failed')).toBe('send failed');
  });
});
