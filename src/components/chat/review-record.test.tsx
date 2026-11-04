// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ReviewRecordCard } from '@/components/chat/review-record';
import type { MessagePicture } from '@/lib/chat/message-pictures';
import { reviewRecordOf } from '@/lib/chat/model-review';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatToolCall } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => (url ? `preview:${url}` : '') }),
}));

const picture = (url: string, index: number): MessagePicture => ({ image: { url, taskId: '' }, index });

const inspect = (failed: readonly string[]): ChatToolCall => ({
  name: 'inspect_model',
  summary: {
    tool: 'inspect', model: '@model3', triangles: 19000, has_uv: true, watertight: false, has_skin: true,
    usage: {
      purpose: 'game_prop', purpose_label: '游戏道具', purpose_source: 'agent', note: '只适用于移动端',
      checks: [
        { check: 'triangles', status: failed.includes('triangles') ? 'fail' : 'pass', fix_hint: '减面到 2 万以内' },
        { check: 'uv', status: failed.includes('uv') ? 'fail' : 'pass' },
        { check: 'watertight', status: 'pass' },
        { check: 'texture_size', status: failed.includes('texture_size') ? 'fail' : 'pass' },
      ],
    },
  },
});
const renderCall: ChatToolCall = {
  name: 'render_model_views',
  summary: { tool: 'render', model: '@model3', views: ['front', 'left'], failed_views: ['back'] },
};
const compareCall: ChatToolCall = {
  name: 'compare_models',
  summary: { tool: 'compare', models: ['@model3', '@model4'] },
};
const toolCalls = [inspect(['triangles', 'texture_size']), renderCall, compareCall, inspect(['uv'])];
const renders = [picture('/api/v1/tasks/t1/assets/r1', 0), picture('/api/v1/tasks/t1/assets/r2', 1)];
const record = reviewRecordOf(schema, toolCalls, renders);

const renderRecord = (leftover: MessagePicture[] = record.leftover) =>
  render(<ReviewRecordCard calls={record.calls} leftover={leftover} />);

beforeEach(() => { useStudioStore.setState({ stageImage: null }); });
afterEach(cleanup);

describe('the self-check record of one reply (#103)', () => {
  it('folds every review call of the reply into one card and counts them', () => {
    const view = renderRecord();
    const card = view.getByTestId('chat-review-record');
    expect(within(card).getByText(messages['ui.chat.review.record_title'])).toBeTruthy();
    expect(card.textContent).toContain(
      messages['ui.chat.review.summary_count'].replace('{count}', String(record.calls.length)),
    );
    expect(view.getAllByTestId('chat-review-line')).toHaveLength(4);
    expect(view.getAllByTestId('chat-review-round')).toHaveLength(2);
  });

  it('writes each round as found, handled and result, with the recheck closing the round', () => {
    const view = renderRecord();
    const rounds = view.getAllByTestId('chat-review-round');
    const first = within(rounds[0]);
    expect(rounds[0].textContent).toContain(messages['ui.chat.review.record_step'].replace('{index}', '1'));
    expect(first.getByTestId('chat-review-found').textContent).toContain('面数');
    expect(first.getByTestId('chat-review-found').textContent).toContain('贴图尺寸');
    const handled = first.getByTestId('chat-review-handled');
    // The render and comparison calls are the actions taken after the inspection.
    expect(handled.textContent).toContain('渲染了 @model3：正面、左侧 · 1 个视角失败');
    expect(handled.textContent).toContain('对比了 @model3、@model4');
    expect(first.getByTestId('chat-review-result').textContent).toContain(
      messages['ui.chat.review.recheck_rest'].replace('{count}', '1'),
    );

    const second = within(rounds[1]);
    expect(second.getByTestId('chat-review-found').textContent).toContain('UV');
    expect(second.getByTestId('chat-review-handled').textContent).toContain(messages['ui.chat.review.no_actions']);
    expect(second.getByTestId('chat-review-result').textContent).toContain(messages['ui.chat.review.pending_recheck']);
  });

  it('keeps one line per call with its summary, and the whole card folds away', () => {
    const view = renderRecord();
    const lines = view.getAllByTestId('chat-review-line');
    expect(lines[0].textContent).toContain('检查了 @model3：1.9万 三角面 · 有 UV · 不水密 · 已绑骨 · 贴图尺寸不符合预算');
    expect(lines[1].textContent).toContain('渲染了 @model3：正面、左侧 · 1 个视角失败');
    expect(lines[2].textContent).toContain('对比了 @model3、@model4');
    expect(lines[3].textContent).toContain('检查了 @model3：1.9万 三角面 · 有 UV · 不水密 · 已绑骨');
    for (const line of lines) {
      // The summary never carries its details until the line is opened.
      expect(line.textContent).not.toContain('减面到 2 万以内');
    }
    fireEvent.click(view.getByTestId('chat-review-record-toggle'));
    expect(view.queryAllByTestId('chat-review-line')).toHaveLength(0);
    expect(view.getByText(messages['ui.chat.review.record_title'])).toBeTruthy();
    fireEvent.click(view.getByTestId('chat-review-record-toggle'));
    expect(view.getAllByTestId('chat-review-line')).toHaveLength(4);
  });

  it('opens the checks of an inspection line with their status and the usage note', () => {
    const view = renderRecord();
    const line = view.getAllByTestId('chat-review-line')[0];
    expect(within(line).queryByTestId('chat-review-details')).toBeNull();
    fireEvent.click(within(line).getByTestId('chat-review-line-toggle'));
    const details = within(line).getByTestId('chat-review-details');
    expect(details.textContent).toContain('面数不符合预算');
    expect(details.textContent).toContain('UV符合预算');
    expect(details.textContent).toContain('减面到 2 万以内');
    expect(details.textContent).toContain('游戏道具');
    expect(details.textContent).toContain('只适用于移动端');
    // A non-inspection line opens the views it rendered instead.
    const renderLine = view.getAllByTestId('chat-review-line')[1];
    fireEvent.click(within(renderLine).getByTestId('chat-review-line-toggle'));
    expect(within(renderLine).getByTestId('chat-review-details').textContent).toContain('正面、左侧');
    expect(within(renderLine).getByTestId('chat-review-details').textContent).toContain('背面');
  });

  it('shows rendered views as 48px thumbnails and opens one large on the stage', () => {
    const view = renderRecord();
    const thumbs = view.getAllByTestId('chat-review-thumbnail');
    expect(thumbs).toHaveLength(2);
    expect(thumbs[0].getAttribute('aria-label')).toBe(messages['ui.chat.review.view_names.front']);
    expect(thumbs[1].getAttribute('aria-label')).toBe(messages['ui.chat.review.view_names.left']);
    expect(thumbs[0].getAttribute('style')).toContain(`width: ${schema.chat.frontend.review_thumbnail_pixels}px`);
    expect(within(thumbs[0]).getByRole('img').getAttribute('src')).toBe('preview:/api/v1/tasks/t1/assets/r1');
    fireEvent.click(thumbs[1]);
    expect(useStudioStore.getState().stageImage).toEqual({
      url: '/api/v1/tasks/t1/assets/r2',
      label: messages['ui.chat.review.view_names.left'],
    });
  });

  it('keeps an unclaimed rendered picture in the record instead of dropping it', () => {
    const view = renderRecord([picture('/api/v1/tasks/t1/assets/r9', 2)]);
    const leftover = view.getByTestId('chat-review-leftover');
    expect(within(leftover).getAllByTestId('chat-review-thumbnail')).toHaveLength(1);
    fireEvent.click(within(leftover).getByTestId('chat-review-thumbnail'));
    expect(useStudioStore.getState().stageImage?.url).toBe('/api/v1/tasks/t1/assets/r9');
  });
});
