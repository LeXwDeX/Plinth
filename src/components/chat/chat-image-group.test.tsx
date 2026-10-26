import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import { useWorkspaceStore, type TrackedTask } from '@/store/workspace-store';
import type { ChatAttachment, ChatMessage as ChatMessageType } from '@/store/chat-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));
// A server render reads a zustand store's initial state; read the live state so a test can seed the tracker.
vi.mock('@/store/workspace-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/workspace-store')>();
  const store = actual.useWorkspaceStore;
  return { ...actual, useWorkspaceStore: Object.assign((selector: (state: ReturnType<typeof store.getState>) => unknown) => selector(store.getState()), store) };
});

const url = (task: string, variant: string) => `/api/v1/tasks/${task}/assets/${task}-${variant}`;
const tracked = (taskId: string, capability: string, variants: string[]): TrackedTask => ({
  taskId, sessionId: 's', capability, prompt: '', state: 'SUCCEEDED', progress: 100, progressMaximum: 100, terminal: true,
  failure: null, liveError: '', resources: [], resourcesPending: false, notFound: false, updatedAt: 1, downloadables: [],
  assets: variants.map((variant, ordinal) => ({ id: `${taskId}-${variant}`, role: 'image', variant, delivery: 'required', ordinal, mediaType: 'image/jpeg', size: 10, url: url(taskId, variant), kind: 'image' })),
});
// The tool result lists the pictures in whatever order they were delivered; the card must not rely on it.
const message = (task: string, variants: string[]): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: 'done', createdAt: 1,
  images: variants.map((variant) => ({ url: url(task, variant), taskId: task })),
  attachments: variants.map((variant, index): ChatAttachment => ({ handle: `@img${index + 1}`, kind: 'image', role: 'generated', taskId: task, assetId: `${task}-${variant}`, imageSlot: index + 1 })),
});
const pictureLabels = (html: string) => [...html.matchAll(/<button[^>]*data-testid="chat-result-picture"[^>]*>/g)].map((match) => match[0].match(/aria-label="([^"]*)"/)?.[1]);

afterEach(() => useWorkspaceStore.setState({ trackedTasks: [] }));

it('shows a four-view Task as one named group in front, left, back, right order with each view labelled', () => {
  useWorkspaceStore.setState({ trackedTasks: [tracked('g', 'image-to-multiview', ['right', 'back', 'front', 'left'])] });
  const html = renderToStaticMarkup(<ChatMessage message={message('g', ['right', 'back', 'front', 'left'])} sessionId="s" pick={null} onPick={() => {}} />);
  expect(html).toContain('data-image-group="image-to-multiview"');
  expect(html).toContain('生成四视角图片 · 4 张');
  expect(html).not.toContain('4 张候选');
  expect(pictureLabels(html)).toEqual(['生成四视角图片 · 正面', '生成四视角图片 · 左侧', '生成四视角图片 · 背面', '生成四视角图片 · 右侧']);
  expect([...html.matchAll(/data-testid="chat-result-view-label"[^>]*>([^<]*)</g)].map((match) => match[1])).toEqual(['正面', '左侧', '背面', '右侧']);
  expect(html).not.toContain('data-testid="chat-image-group-partial"');
  // Each view keeps its own handle, so the Agent can still be pointed at one of them.
  expect(html).toContain('data-handle="@img3"');
});

it('shows an edit that delivered one view as that view only and says the other views stay in the source', () => {
  useWorkspaceStore.setState({ trackedTasks: [tracked('e', 'edit-multiview', ['back'])] });
  const html = renderToStaticMarkup(<ChatMessage message={message('e', ['back'])} sessionId="s" pick={null} onPick={() => {}} />);
  expect(html).toContain('编辑四视角图片 · 1 张');
  expect(pictureLabels(html)).toEqual(['编辑四视角图片 · 背面']);
  expect(html).toContain('data-testid="chat-image-group-partial"');
  expect(html).toContain(messages['ui.studio.result.image_group_partial']);
  expect(html.match(/data-testid="chat-result-picture"/g)).toHaveLength(1);
});

it('keeps ordinary candidates (and a group whose Task is not tracked yet) as plain numbered candidates', () => {
  const html = renderToStaticMarkup(<ChatMessage message={message('g', ['front', 'left'])} sessionId="s" pick={null} onPick={() => {}} />);
  expect(html).not.toContain('data-image-group=');
  expect(html).toContain('2 张候选');
  expect(pictureLabels(html)).toEqual([`${messages['ui.chat.message.generated_image']} 1`, `${messages['ui.chat.message.generated_image']} 2`]);
  // A Task of another session never lends its group identity to this card.
  useWorkspaceStore.setState({ trackedTasks: [{ ...tracked('g', 'image-to-multiview', ['front', 'left']), sessionId: 'other' }] });
  expect(renderToStaticMarkup(<ChatMessage message={message('g', ['front', 'left'])} sessionId="s" pick={null} onPick={() => {}} />)).not.toContain('data-image-group=');
});

it('shows a generated four-view Task that lacks a view as plain candidates, without the edit note', () => {
  useWorkspaceStore.setState({ trackedTasks: [tracked('g', 'image-to-multiview', ['front', 'left', 'back'])] });
  const html = renderToStaticMarkup(<ChatMessage message={message('g', ['front', 'left', 'back'])} sessionId="s" pick={null} onPick={() => {}} />);
  expect(html).not.toContain('data-image-group=');
  expect(html).not.toContain('data-testid="chat-image-group-partial"');
  expect(html).toContain('3 张候选');
});
