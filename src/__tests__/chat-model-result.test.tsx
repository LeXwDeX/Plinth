import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { TrackedTask } from '@/store/workspace-store';

/**
 * 3D 工具结果在对话里按 3D 结果呈现：模型的图是 3D 结果卡的封面（点封面上舞台，角标在卡片里转动预览），
 * 不是 2D 候选；卡片不再写格式清单，积分写在已提交步骤的说明里；文案只来自 Schema。
 */

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: defaultCatalog.application,
    text: (key: string) => messages[key] ?? '',
  }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));

const asset = (id: string, kind: 'image' | 'model', variant: string, ordinal: number) => ({
  id, role: kind === 'model' ? 'model' : 'thumbnail', variant, delivery: 'required', ordinal,
  mediaType: kind === 'model' ? 'model/gltf-binary' : 'image/png', size: 1, url: `/assets/${id}`, kind,
});
const finished = (taskId: string, sessionId: string): TrackedTask => {
  const downloadables = [asset(`${taskId}-thumb`, 'image', '', 0), asset(`${taskId}-glb`, 'model', 'glb', 1), asset(`${taskId}-fbx`, 'model', 'fbx', 2)];
  return { taskId, sessionId, capability: 'format-conversion', prompt: '', state: 'SUCCEEDED', progress: 100, progressMaximum: 100,
    terminal: true, failure: null, liveError: '', assets: downloadables, downloadables, resources: [], resourcesPending: false,
    notFound: false, updatedAt: 0 };
};

const storeState: { trackedTasks: TrackedTask[]; setCurrentObject: () => void } = { trackedTasks: [finished('task-a', 'session-a'), finished('task-b', 'session-b')], setCurrentObject: () => {} };
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: typeof storeState) => unknown) => selector(storeState),
}));
vi.mock('@/store/chat-store', () => ({
  useChatStore: Object.assign((selector: (state: object) => unknown) => selector({ activeSessionId: 'session-a' }),
    { getState: () => ({ activeSessionId: 'session-a' }) }),
}));

// Zustand renders its initial state on the server, so the card's live-viewer flag is mocked to be set per test.
const previewState = { taskId: null as string | null };
vi.mock('@/store/inline-model-preview-store', () => ({
  useInlineModelPreviewStore: Object.assign((selector: (state: typeof previewState) => unknown) => selector(previewState),
    { getState: () => ({ ...previewState, open: () => {}, close: () => {} }) }),
}));

const message = (taskId: string) => ({
  id: 'm1', role: 'assistant' as const, content: 'done', createdAt: 0,
  images: [{ url: `/assets/${taskId}-thumb`, taskId }],
  taskIds: [taskId],
  paidSteps: [{ taskId, credits: 10, approvedByPolicy: true }],
});

it('shows a finished 3D result as a card with its picture as the cover, not as a 2D candidate', () => {
  previewState.taskId = null;
  const html = renderToStaticMarkup(<ChatMessage message={message('task-a')} sessionId="session-a" />);
  expect(html).toContain('data-testid="chat-model-results"');
  expect(html).toContain('data-available="true"');
  // The cover is the model's picture and opens the model on the stage.
  expect(html).toMatch(/<button[^>]*data-testid="chat-model-cover"[^>]*>\s*<img[^>]*src="\/assets\/task-a-thumb"/);
  expect(html).toContain(messages['ui.studio.result.capture_2d']);
  expect(html).toContain('data-testid="chat-result-detail"');
  expect(html).toContain('data-testid="paid-step-notes"');
  expect(html).toContain(messages['ui.workspace.approval.auto_approved_note']);
  // The picture is not offered as a 2D result, and the card has no second way to the stage and no format line.
  expect(html).not.toContain('data-testid="chat-image-result"');
  expect(html).not.toContain('data-testid="candidate-pick"');
  expect(html).not.toContain('data-testid="chat-result-to3d"');
  expect(html).not.toContain('data-testid="chat-result-open"');
  expect(html).not.toContain('glb / fbx');
});

it('opens the task details of the model from a small icon with the schema name', () => {
  previewState.taskId = null;
  const html = renderToStaticMarkup(<ChatMessage message={message('task-a')} sessionId="session-a" />);
  const detail = html.match(/<button[^>]*data-testid="chat-result-detail"[^>]*>/)?.[0] ?? '';
  expect(detail).toContain(`aria-label="${messages['ui.result.card.detail']}"`);
  // The hover hint is a HintTooltip around the icon, not a native title.
  expect(detail).not.toContain('title=');
});

it('turns a cover into a live viewer only for the card that holds it', () => {
  const both = { ...message('task-a'), taskIds: ['task-a', 'task-c'], images: [...message('task-a').images, { url: '/assets/task-c-thumb', taskId: 'task-c' }] };
  storeState.trackedTasks = [finished('task-a', 'session-a'), finished('task-c', 'session-a')];
  try {
    previewState.taskId = null;
    const idle = renderToStaticMarkup(<ChatMessage message={both} sessionId="session-a" />);
    expect(idle.match(/data-testid="chat-model-cover"/g)).toHaveLength(2);
    expect(idle).not.toContain('data-testid="chat-model-preview"');
    expect(idle).toContain(messages['ui.studio.result.preview_3d']);

    previewState.taskId = 'task-c';
    const live = renderToStaticMarkup(<ChatMessage message={both} sessionId="session-a" />);
    // One viewer for the whole chat: the other card stays on its cover.
    expect(live.match(/data-testid="chat-model-preview"/g)).toHaveLength(1);
    expect(live.match(/data-testid="chat-model-cover"/g)).toHaveLength(1);
    expect(live).toContain(`aria-label="${messages['ui.studio.result.preview_3d_close']}"`);
    expect(live.match(/data-active="true"/g)).toHaveLength(1);
    expect(live.match(/data-testid="chat-model-preview-toggle"/g)).toHaveLength(2);
  } finally {
    previewState.taskId = null;
    storeState.trackedTasks = [finished('task-a', 'session-a'), finished('task-b', 'session-b')];
  }
});

it('keeps the pictures of a model the tracker has no file for as a card without the actions that need the file', () => {
  // A session opened from history restores only its latest tasks, and a task may not be ready yet.
  const old = {
    ...message('task-old'),
    attachments: [{ handle: '@model3', kind: 'model' as const, taskId: 'task-old', imageSlot: 1 }],
  };
  previewState.taskId = null;
  const html = renderToStaticMarkup(<ChatMessage message={old} sessionId="session-a" />);
  expect(html).toContain('data-testid="chat-model-results"');
  expect(html).toContain('data-available="false"');
  expect(html).toContain('/assets/task-old-thumb');
  expect(html).toContain('data-handle="@model3"');
  expect(html).not.toContain('data-testid="chat-image-result"');
  expect(html).not.toContain('data-testid="chat-model-preview-toggle"');
  expect(html).not.toContain('data-testid="model-archive-link"');
  expect(html).not.toContain(messages['ui.studio.result.capture_2d']);
});

const withTasks = (tasks: TrackedTask[], run: () => void) => {
  const saved = storeState.trackedTasks;
  storeState.trackedTasks = tasks;
  previewState.taskId = null;
  try { run(); } finally { storeState.trackedTasks = saved; }
};

it('shows a new 3D message that carries only the cover as one card with its model handle', () => {
  const single = {
    ...message('task-a'),
    attachments: [{ handle: '@model2', kind: 'model' as const, taskId: 'task-a', imageSlot: 1 }],
  };
  const html = renderToStaticMarkup(<ChatMessage message={single} sessionId="session-a" />);
  expect(html.match(/data-testid="chat-model-results"/g)).toHaveLength(1);
  expect(html).toContain('data-available="true"');
  expect(html).toContain('data-handle="@model2"');
  expect(html).toContain('/assets/task-a-thumb');
  expect(html).not.toContain('data-testid="chat-image-result"');
});

it('shows an old 3D message, whose second picture is the pre-model picture with an @img handle, as the same one card', () => {
  const old = {
    ...message('task-a'),
    images: [{ url: '/assets/task-a-thumb', taskId: 'task-a' }, { url: '/assets/task-a-pre', taskId: 'task-a' }],
    attachments: [
      { handle: '@model1', kind: 'model' as const, taskId: 'task-a', imageSlot: 1 },
      { handle: '@img1', kind: 'image' as const, taskId: 'task-a', assetId: 'pre', imageSlot: 2 },
    ],
  };
  const html = renderToStaticMarkup(<ChatMessage message={old} sessionId="session-a" onPick={() => {}} />);
  expect(html.match(/data-testid="chat-model-results"/g)).toHaveLength(1);
  expect(html).toContain('/assets/task-a-thumb');
  // The pre-model picture is on no card, offers no pick and no convert, and its handle is not shown.
  expect(html).not.toContain('/assets/task-a-pre');
  expect(html).not.toContain('data-testid="chat-image-result"');
  expect(html).not.toContain('data-testid="candidate-pick"');
  expect(html).not.toContain('data-handle="@img1"');
});

it('keeps the pictures of a streaming 3D message off the 2D candidates while the model is not downloadable yet', () => {
  const running = (taskId: string): TrackedTask => ({
    ...finished(taskId, 'session-a'), capability: 'text-to-3d', state: 'RUNNING', terminal: false, assets: [], downloadables: [],
  });
  // The stream message has no attachments and no tool calls yet: only the pictures of the images event and the paid step.
  const streaming = {
    id: 'stream', role: 'assistant' as const, content: '', isStreaming: true, createdAt: 0,
    images: [{ url: '/assets/task-s-thumb', taskId: 'task-s' }, { url: '/assets/task-s-pre', taskId: 'task-s' }],
    paidSteps: [{ taskId: 'task-s', credits: 10, approvedByPolicy: false }],
  };
  withTasks([running('task-s')], () => {
    const html = renderToStaticMarkup(<ChatMessage message={streaming} sessionId="session-a" onPick={() => {}} />);
    expect(html).toContain('data-testid="chat-model-results"');
    expect(html).toContain('data-available="false"');
    expect(html).toContain('/assets/task-s-thumb');
    expect(html).not.toContain('/assets/task-s-pre');
    expect(html).not.toContain('data-testid="chat-image-result"');
    expect(html).not.toContain('data-testid="candidate-pick"');
  });
  // The same pictures of a 2D task are candidates.
  withTasks([{ ...running('task-s'), capability: 'text-to-image' }], () => {
    const html = renderToStaticMarkup(<ChatMessage message={streaming} sessionId="session-a" onPick={() => {}} />);
    expect(html).toContain('data-testid="chat-image-result"');
    expect(html).not.toContain('data-testid="chat-model-results"');
  });
});

it('does not offer a viewer for a task of another session', () => {
  const html = renderToStaticMarkup(<ChatMessage message={message('task-b')} sessionId="session-a" />);
  expect(html).not.toContain('data-testid="chat-model-results"');
});

it('offers the one-click ZIP in the card and leaves the format list to the right panel', () => {
  const html = renderToStaticMarkup(<ChatMessage message={message('task-a')} sessionId="session-a" />);
  expect(html).toContain('data-testid="model-archive-link"');
  expect(html).toContain('data-archive-route="/api/v1/tasks/task-a/archive"');
  expect(html).toContain(messages['ui.workspace.model_formats.download_all']);
  // The delivery list is read in the right panel, never from the conversation.
  expect(html).not.toContain('data-testid="model-formats-panel"');
  expect(html).not.toContain('data-testid="model-formats"');
});
