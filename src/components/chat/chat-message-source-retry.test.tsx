import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ChatMessage as ChatMessageType } from '@/store/chat-store';
import type { TrackedTask } from '@/store/workspace-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

const judged = vi.hoisted(() => ({ data: undefined as Record<string, string> | undefined, keys: [] as unknown[], tasks: [] as unknown[],
  fresh: undefined as Record<string, string> | undefined, clicks: new Map<string, () => unknown>() }));
// The static render reads a store's initial state, so the tracked tasks come from here.
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ trackedTasks: judged.tasks }) }));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));
vi.mock('swr', () => ({ default: (key: unknown) => {
  judged.keys.push(key);
  return { data: key ? judged.data : undefined, mutate: async () => judged.fresh };
} }));
vi.mock('@/components/ui/button', () => ({ Action: ({ children, onClick }: { children: React.ReactNode; onClick?: () => unknown }) => {
  const label = React.Children.toArray(children).filter((child) => typeof child === 'string').join('');
  if (onClick) judged.clicks.set(label, onClick);
  return <button data-testid={label === '重新生成（约 15 积分）' ? 'chat-regenerate' : undefined}>{children}</button>;
} }));

const failedTask = (taskId: string, failureClass: string): TrackedTask => ({
  taskId, sessionId: 's', capability: 'retexture', prompt: '', state: 'FAILED', progress: 0, progressMaximum: 100, terminal: true,
  failure: { failureClass, detail: '', retryable: false, ambiguous: false }, liveError: '',
  assets: [], downloadables: [], resources: [], resourcesPending: false, notFound: false, updatedAt: 0,
});
const card: ChatMessageType = { id: 'm1', role: 'assistant', content: '生成失败', createdAt: 1,
  paidSteps: [{ taskId: 'step', credits: 15, approvedByPolicy: false }] };
const regenerated = vi.fn();
const render = () => renderToStaticMarkup(<ChatMessage message={card} sessionId="s" onRegenerate={regenerated} regenerateCredits={15} />);
const paidRegenerate = () => `重新生成（约 15 积分）`;

beforeEach(() => {
  judged.data = undefined;
  judged.fresh = undefined;
  judged.keys = [];
  judged.clicks.clear();
  regenerated.mockReset();
});

// #326: an old card whose step failed as source_expired was written at failure time ("生成失败") with a paid
// regenerate. The card asks the server, by the rule plans use, whether running the step again can succeed.
describe('a failed card whose source original task is gone', () => {
  it('offers no paid regenerate when the step is certain to fail again, and says why', () => {
    judged.tasks = [failedTask('step', 'source_expired')];
    judged.data = { step: 'source_expired' };
    const html = render();
    expect(judged.keys.some((key) => Array.isArray(key) && key.includes('step'))).toBe(true);
    expect(html).toContain(messages['ui.studio.result.source_retry.source_expired']);
    expect(html).not.toContain(paidRegenerate());
    expect(html).not.toContain('data-testid="chat-regenerate"');
  });

  it('keeps the regenerate when it will use the saved model file, and says so', () => {
    judged.tasks = [failedTask('step', 'source_expired')];
    judged.data = { step: 'saved_file' };
    const html = render();
    expect(html).toContain(messages['ui.studio.result.source_retry.saved_file']);
    expect(html).toContain(paidRegenerate());
  });

  it('asks nothing about a step that failed for another reason', () => {
    judged.tasks = [failedTask('step', 'rejected')];
    judged.data = { step: 'source_expired' };
    const html = render();
    expect(judged.keys.every((key) => key === null)).toBe(true);
    expect(html).toContain(paidRegenerate());
    expect(html).not.toContain(messages['ui.studio.result.source_retry.source_expired']);
  });

  it('judges the step again when the regenerate is clicked', async () => {
    judged.tasks = [failedTask('step', 'source_expired')];
    judged.data = { step: 'saved_file' };
    render();
    judged.fresh = { step: 'source_incompatible' };
    await judged.clicks.get(paidRegenerate())!();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(regenerated).not.toHaveBeenCalled();
    judged.fresh = { step: 'saved_file' };
    await judged.clicks.get(paidRegenerate())!();
    await vi.waitFor(() => expect(regenerated).toHaveBeenCalledOnce());
  });
});
