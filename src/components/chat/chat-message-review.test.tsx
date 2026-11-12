// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatMessage as ChatMessageType } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({
    previewAssetUrl: (url: string) => (url ? `preview:${url}` : ''),
    proxyAssetUrl: (url: string) => `proxy:${url}`,
  }),
}));
const selfCheckData = vi.hoisted(() => ({
  current: undefined as unknown,
  keys: [] as unknown[],
  mutateCalls: 0,
}));
vi.mock('swr', () => ({
  default: (key: unknown) => {
    selfCheckData.keys.push(key);
    return {
      data: selfCheckData.current,
      mutate: async () => { selfCheckData.mutateCalls += 1; },
    };
  },
}));

const longReply = Array.from({ length: 14 }, (_, index) => `第 ${index + 1} 行`).join('\n');
const reply = (extra: Partial<ChatMessageType> = {}): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: '完成。', createdAt: 1, ...extra,
});
const renderMessage = (message: ChatMessageType) => render(<ChatMessage message={message} sessionId="s1" />);

const summary = (value: Record<string, unknown>) => value;
const inspectCall = {
  name: 'inspect_model',
  summary: summary({
    tool: 'inspect', model: '@model3', triangles: 19000, has_uv: true, watertight: false,
    usage: { purpose: 'game_prop', purpose_label: '游戏道具', note: '', checks: [] },
  }),
};
const renderCall = { name: 'render_model_views', summary: summary({ tool: 'render', model: '@model3', views: ['front'] }) };
const renderImage = { url: '/api/v1/tasks/t1/assets/render-front', taskId: 't1' };
const renderAttachment = { handle: '@render1', kind: 'image' as const, role: 'render', imageSlot: 1 };

beforeEach(() => {
  selfCheckData.current = undefined;
  selfCheckData.keys = [];
  selfCheckData.mutateCalls = 0;
  // jsdom has no media queries; the streaming spinner reads prefers-reduced-motion.
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  });
  useStudioStore.setState({ stageImage: null });
  useWorkspaceStore.setState({ trackedTasks: [] });
});
afterEach(cleanup);

describe('folding a long Agent reply (#103)', () => {
  it('starts a finished reply longer than the Schema line budget folded, and expands on request', () => {
    const view = renderMessage(reply({ content: longReply }));
    const folded = view.getByTestId('chat-reply-folded');
    expect(folded.getAttribute('style')).toContain(
      `max-height: ${schema.chat.frontend.reply_collapse_lines * 1.625}em`,
    );
    const toggle = view.getByTestId('chat-reply-toggle');
    expect(toggle.textContent).toBe(messages['ui.chat.message.expand_full']);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(view.queryByTestId('chat-reply-folded')).toBeNull();
    expect(toggle.textContent).toBe(messages['ui.chat.message.collapse_full']);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    fireEvent.click(toggle);
    expect(view.getByTestId('chat-reply-folded')).toBeTruthy();
  });

  it('never folds a streaming reply or a user message', () => {
    const streaming = renderMessage(reply({ content: longReply, isStreaming: true }));
    expect(streaming.queryByTestId('chat-reply-folded')).toBeNull();
    expect(streaming.queryByTestId('chat-reply-toggle')).toBeNull();
    cleanup();

    const user = renderMessage(reply({ role: 'user', content: longReply }));
    expect(user.queryByTestId('chat-reply-folded')).toBeNull();
    expect(user.queryByTestId('chat-reply-toggle')).toBeNull();
  });

  it('leaves a short finished reply whole', () => {
    const view = renderMessage(reply({ content: '完成。' }));
    expect(view.queryByTestId('chat-reply-folded')).toBeNull();
    expect(view.queryByTestId('chat-reply-toggle')).toBeNull();
  });
});

describe('the self-check calls of a reply (#103)', () => {
  it('folds the review calls and their rendered pictures into one record, not a 2D result card', () => {
    const view = renderMessage(reply({
      content: '完成。',
      toolCalls: [inspectCall, renderCall],
      images: [renderImage],
      attachments: [renderAttachment],
    }));
    const record = view.getByTestId('chat-review-record');
    // The card starts folded to its title line (count only); opening it shows the calls.
    expect(record.textContent).not.toContain('检查了 @model3');
    fireEvent.click(view.getByTestId('chat-review-record-toggle'));
    expect(record.textContent).toContain('检查了 @model3：1.9万 三角面 · 有 UV · 不水密');
    expect(record.textContent).toContain('渲染了 @model3：正面');
    expect(record.textContent).toContain(messages['ui.chat.review.record_title']);
    expect(view.getByTestId('chat-review-thumbnail').getAttribute('aria-label')).toBe(
      messages['ui.chat.review.view_names.front'],
    );
    // A render attachment is a self-check picture, never a 2D candidate.
    expect(view.queryByTestId('chat-image-result')).toBeNull();
  });

  it('opens a rendered view large on the stage from the record', () => {
    const view = renderMessage(reply({
      content: '完成。', toolCalls: [renderCall], images: [renderImage], attachments: [renderAttachment],
    }));
    fireEvent.click(view.getByTestId('chat-review-record-toggle'));
    fireEvent.click(view.getByTestId('chat-review-thumbnail'));
    expect(useStudioStore.getState().stageImage).toEqual({
      url: renderImage.url,
      label: messages['ui.chat.review.view_names.front'],
    });
  });

  it('identifies a rendered picture by its attachment role, not by the tool that ran', () => {
    const view = renderMessage(reply({
      content: '完成。',
      toolCalls: [renderCall],
      images: [renderImage],
      attachments: [{ handle: '@img1', kind: 'image', imageSlot: 1 }],
    }));
    // Without role="render" the picture is an ordinary result, and the review call has no picture to show.
    expect(view.queryByTestId('chat-review-thumbnail')).toBeNull();
    expect(view.getByTestId('chat-image-result')).toBeTruthy();
  });
});

describe('the persisted self-check record of a result (#246)', () => {
  it('shows the recorded state on the model card and beside the review calls', () => {
    selfCheckData.current = [{
      taskId: 't1', status: 'passed', roundsUsed: 1, roundsLimit: 3, rendersInRound: 1, rendersLimit: 6,
      correctionsUsed: 0, correctionsLimit: 2,
      checks: [{ round: 1, kind: 'reference', tool: 'compare_model_reference', views: ['front'], handles: [], metrics: null, ok: true, error: '', at: '' }],
      conclusion: { status: 'passed', summary: 'matches the reference', findings: [], fixes: [], remaining: [], at: '' },
      updatedAt: '',
    }];
    const view = renderMessage(reply({
      content: '完成。',
      attachments: [{ handle: '@model1', kind: 'model', taskId: 't1', imageSlot: 1 }],
      images: [{ url: '/api/v1/tasks/t1/assets/preview', taskId: 't1' }],
    }));
    expect(view.getByTestId('chat-model-results')).toBeTruthy();
    const badge = view.getByTestId('chat-model-self-check-status');
    expect(badge.getAttribute('data-role')).toBe('passed');
    expect(badge.textContent).toBe(messages['ui.chat.self_check.status_passed']);
    const record = view.getByTestId('chat-self-check-record');
    fireEvent.click(view.getByTestId('chat-self-check-record-toggle'));
    expect(record.textContent).toContain('matches the reference');
  });

  it('shows an unchecked result without inventing a record', () => {
    const view = renderMessage(reply({
      content: '完成。',
      attachments: [{ handle: '@model1', kind: 'model', taskId: 't1', imageSlot: 1 }],
      images: [{ url: '/api/v1/tasks/t1/assets/preview', taskId: 't1' }],
    }));
    const badge = view.getByTestId('chat-model-self-check-status');
    expect(badge.getAttribute('data-role')).toBe('unchecked');
    expect(badge.textContent).toBe(messages['ui.chat.self_check.status_unchecked']);
    expect(view.queryByTestId('chat-self-check-record')).toBeNull();
  });

  it('reads the session records for a reply without a model card too', () => {
    // A later turn's reply has no model card of its own; it still subscribes
    // to the session's records, because its settle is what the records written
    // by that turn's tools are read after.
    const keys = () => selfCheckData.keys.filter(
      (key): key is [string, string] => Array.isArray(key) && key.length >= 2,
    );
    renderMessage(reply({ content: '对照参考图后记录结论。' }));
    expect(keys().some((key) => key[0] === 'chat-self-checks' && key[1] === 's1')).toBe(true);
  });

  it('re-reads the shared session records when a reply settles', () => {
    const streaming = reply({ content: '检查中。', isStreaming: true });
    const view = renderMessage(streaming);
    expect(selfCheckData.mutateCalls).toBe(0);
    view.rerender(<ChatMessage message={{ ...streaming, content: '完成。', isStreaming: false }} sessionId="s1" />);
    expect(selfCheckData.mutateCalls).toBeGreaterThan(0);
  });
});
