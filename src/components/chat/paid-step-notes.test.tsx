import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { PaidStepNotes } from './paid-step-notes';

const steps = [{ taskId: 't1', credits: 3 }] as unknown as React.ComponentProps<typeof PaidStepNotes>['steps'];
const base = { steps, creditsText: (c: number) => `${c} 积分`, failedText: (c: number) => `未完成 ${c}`, autoApprovedText: '自动批准', failed: () => true };

describe('PaidStepNotes (#94)', () => {
  it('draws a blocked source retry as the unavailable notice with the full note in the hint', () => {
    const html = renderToStaticMarkup(<PaidStepNotes {...base} note={() => '完整说明'} noteUnavailableLabel={() => '无法使用，来源模型不能用于重新生成'} />);
    expect(html).toContain('data-testid="unavailable-icon"');
    expect(html).toContain('无法使用，来源模型不能用于重新生成');
    expect(html).toMatch(/data-testid="unavailable-detail"[^>]*>完整说明/);
  });

  it('keeps a plain note for a retry that will work', () => {
    const html = renderToStaticMarkup(<PaidStepNotes {...base} note={() => '改用已保存的文件'} noteUnavailableLabel={() => ''} />);
    expect(html).toContain('改用已保存的文件');
    expect(html).not.toContain('unavailable-icon');
  });
});
