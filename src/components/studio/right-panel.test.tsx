import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { PanelTabBar, RightPanel } from '@/components/studio/right-panel';
import { panelTabViews } from '@/lib/workspace/panel-tabs';
import type { ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;

const mock = vi.hoisted(() => ({
  sessionId: 's1' as string | null,
  object: null as null | { kind: 'image' | 'model'; taskId: string; assetId: string; url: string; sessionId: string },
  nodes: 0,
  processing: { available: [{ id: 'retexture_model' }] as Array<{ id: string }>, offerings: {} as Record<string, unknown> | undefined, error: undefined as unknown },
}));

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages[key] ?? '' }),
}));
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: object) => unknown) => selector({ activeSessionId: mock.sessionId }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: object) => unknown) => selector({ currentObject: mock.object }),
}));
vi.mock('@/store/canvas-store', () => ({
  useCanvasStore: (selector: (state: object) => unknown) => selector({ nodesBySession: { s1: Array.from({ length: mock.nodes }, (_, index) => ({ id: `n${index}` })) } }),
}));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({
  useModelProcessingCapabilities: () => mock.processing,
}));

// Server rendering reads a zustand store's initial snapshot, so the selectors read the live state instead.
vi.mock('@/store/studio-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/studio-store')>();
  const useStudioStore = Object.assign(
    (selector: (state: ReturnType<typeof actual.useStudioStore.getState>) => unknown) => selector(actual.useStudioStore.getState()),
    actual.useStudioStore,
  );
  return { ...actual, useStudioStore };
});

// The bodies have tests of their own; here only which one the panel puts under which tab matters.
vi.mock('@/components/workspace/harness-panel', () => ({
  HarnessPanel: ({ sessionId, object }: { sessionId: string | null; object: { taskId: string } | null }) => <div data-testid="stub-operations" data-session={sessionId ?? ''} data-task={object?.taskId ?? ''} />,
}));
vi.mock('@/components/canvas/layers-panel', () => ({
  CanvasLayersPanel: ({ sessionId }: { sessionId: string }) => <div data-testid="stub-layers" data-session={sessionId} />,
}));
vi.mock('@/components/canvas/history-panel', () => ({
  CanvasHistoryPanel: ({ sessionId }: { sessionId: string }) => <div data-testid="stub-history" data-session={sessionId} />,
}));
vi.mock('@/components/studio/object-details', () => ({
  ObjectDetails: ({ object }: { object: { taskId: string } }) => <div data-testid="stub-details" data-task={object.taskId} />,
}));
vi.mock('@/components/studio/model-parts-panel', () => ({
  ModelPartsPanel: ({ object }: { object: { taskId: string } }) => <div data-testid="stub-parts" data-task={object.taskId} />,
}));
vi.mock('@/components/workspace/model-processing-panel', () => ({
  ModelProcessingPanel: ({ selection }: { selection: { taskId: string } }) => <div data-testid="stub-process" data-task={selection.taskId} />,
}));
vi.mock('@/components/workspace/model-formats', () => ({
  ModelArchiveLink: ({ taskId }: { taskId: string }) => <a data-testid="stub-archive" data-task={taskId} />,
  ModelFormatsPanel: ({ taskId, pinned, header }: { taskId: string; pinned?: boolean; header?: React.ReactNode }) => (
    <div data-testid="stub-formats" data-task={taskId} data-pinned={pinned ? 'true' : 'false'}>{header}</div>
  ),
}));

const picture = { kind: 'image' as const, taskId: 't-img', assetId: 'a-img', url: '/i.png', sessionId: 's1' };
const model = { kind: 'model' as const, taskId: 't-3d', assetId: 'a-3d', url: '/m.glb', sessionId: 's1' };

const render = () => renderToStaticMarkup(<RightPanel />);
const tabIds = (html: string) => [...html.matchAll(/role="tab"[^>]*data-testid="right-panel-tab-([a-z]+)"/g)].map((match) => match[1]);
const selectedTab = (html: string) => [...html.matchAll(/aria-selected="true"[^>]*data-testid="right-panel-tab-([a-z]+)"/g)].map((match) => match[1]);
const bodyOf = (html: string) => html.match(/role="tabpanel"[^>]*data-testid="right-panel-([a-z]+)"/)?.[1] ?? null;

beforeEach(() => {
  mock.sessionId = 's1';
  mock.object = null;
  mock.nodes = 0;
  mock.processing = { available: [{ id: 'retexture_model' }], offerings: {}, error: undefined };
  useStudioStore.setState({ panelTab2d: null, panelTab3d: null });
});
afterEach(() => {
  useStudioStore.setState({ panelTab2d: null, panelTab3d: null });
});

describe('nothing on the stage and an empty board', () => {
  it('shows the board tabs with only function settings selectable, plus the empty-canvas line', () => {
    const html = render();
    expect(html).toContain('data-testid="right-panel"');
    expect(html).toContain('data-kind="none"');
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task=""');
    expect(html).toContain('role="tablist"');
    expect(html).toContain('data-tab="operations"');
    expect(html).toMatch(/id="right-panel-tab-layers"[^>]*aria-disabled="true"/);
    expect(html).toMatch(/id="right-panel-tab-history"[^>]*aria-disabled="true"/);
    expect(html).not.toMatch(/id="right-panel-tab-operations"[^>]*aria-disabled/);
    expect(html).toContain('right-panel-board-empty');
  });

  it('still takes its Schema width, so the stage does not jump when the panel fills', () => {
    expect(render()).toContain('style="width:300px"');
  });

  it('does not hand another session’s object to function settings', () => {
    mock.object = { ...picture, sessionId: 'other' };
    const html = render();
    expect(html).toContain('data-kind="none"');
    expect(html).toContain('data-task=""');
  });

  it('is the same without an active session', () => {
    mock.sessionId = null;
    mock.object = picture;
    const html = render();
    expect(html).toContain('data-kind="none"');
    expect(html).toContain('data-testid="stub-operations" data-session="" data-task=""');
  });
});

describe('a picture on the stage', () => {
  beforeEach(() => { mock.object = picture; });

  it('adds 功能 after the existing picture tabs and still opens on 图层', () => {
    const html = render();
    expect(html).toContain('data-kind="image"');
    expect(tabIds(html)).toEqual(['layers', 'history', 'details', 'operations']);
    expect(selectedTab(html)).toEqual(['layers']);
    expect(bodyOf(html)).toBe('layers');
    expect(html).toContain('data-testid="stub-layers" data-session="s1"');
  });

  it('shows the remembered 2D tab', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    const history = render();
    expect(selectedTab(history)).toEqual(['history']);
    expect(history).toContain('data-testid="stub-history" data-session="s1"');
    useStudioStore.getState().setPanelTab('image', 'details');
    const details = render();
    expect(selectedTab(details)).toEqual(['details']);
    expect(details).toContain('data-testid="stub-details" data-task="t-img"');
  });

  it('can open function settings while the stage holds a picture', () => {
    useStudioStore.getState().setPanelTab('image', 'operations');
    const html = render();
    expect(selectedTab(html)).toEqual(['operations']);
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task="t-img"');
  });

  it('ignores a remembered tab it does not offer', () => {
    useStudioStore.setState({ panelTab2d: 'parts' });
    expect(selectedTab(render())).toEqual(['layers']);
  });

  it('does not ask the processing Offerings anything for a picture', () => {
    mock.processing = { available: [], offerings: {}, error: undefined };
    expect(selectedTab(render())).toEqual(['layers']);
  });
});

describe('the bare board', () => {
  beforeEach(() => { mock.nodes = 3; });

  it('adds 功能 after 图层 and 历史 and still opens on 图层', () => {
    const html = render();
    expect(html).toContain('data-kind="board"');
    expect(tabIds(html)).toEqual(['layers', 'history', 'operations']);
    expect(selectedTab(html)).toEqual(['layers']);
    expect(html).not.toContain('right-panel-empty');
  });

  it('shares the picture\'s 2D choice, and falls back to 图层 when that choice was 详情', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    expect(selectedTab(render())).toEqual(['history']);
    useStudioStore.getState().setPanelTab('image', 'details');
    const html = render();
    expect(selectedTab(html)).toEqual(['layers']);
    expect(bodyOf(html)).toBe('layers');
  });
});

describe('function settings on the bare board', () => {
  it('keeps the operation editor accessible without a stage source', () => {
    mock.nodes = 2;
    useStudioStore.getState().setPanelTab('board', 'operations');
    const html = render();
    expect(bodyOf(html)).toBe('operations');
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task=""');
  });
});

describe('a 3D model on the stage', () => {
  beforeEach(() => { mock.object = model; });

  it('has 功能, 加工, 格式, 部件 and 详情 and opens on 功能', () => {
    const html = render();
    expect(html).toContain('data-kind="model"');
    expect(tabIds(html)).toEqual(['operations', 'process', 'formats', 'parts', 'details']);
    expect(selectedTab(html)).toEqual(['operations']);
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task="t-3d"');
  });

  it('keeps function settings open while no processing step can run', () => {
    mock.processing = { available: [], offerings: { retexture_model: [] }, error: undefined };
    const html = render();
    expect(selectedTab(html)).toEqual(['operations']);
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task="t-3d"');
    // 加工 stays a tab; the person can still open it.
    expect(tabIds(html)).toContain('process');
  });

  it('keeps function settings available when processing discovery fails or loads', () => {
    mock.processing = { available: [], offerings: undefined, error: new Error('x') };
    expect(selectedTab(render())).toEqual(['operations']);
    mock.processing = { available: [], offerings: undefined, error: undefined };
    expect(selectedTab(render())).toEqual(['operations']);
  });

  it('keeps a chosen 加工 even though it has nothing to offer', () => {
    mock.processing = { available: [], offerings: { retexture_model: [] }, error: undefined };
    useStudioStore.getState().showProcess(null);
    expect(selectedTab(render())).toEqual(['process']);
  });

  it('shows the remembered 3D tab and does not read the 2D one', () => {
    useStudioStore.setState({ panelTab2d: 'history', panelTab3d: 'parts' });
    const html = render();
    expect(selectedTab(html)).toEqual(['parts']);
    expect(html).toContain('data-testid="stub-parts" data-task="t-3d"');
    useStudioStore.setState({ panelTab3d: null });
    expect(selectedTab(render())).toEqual(['operations']);
  });

  it('puts the ZIP link above the format list, pinned to the bottom actions', () => {
    useStudioStore.getState().setPanelTab('model', 'formats');
    const html = render();
    expect(html).toContain('data-testid="stub-formats" data-task="t-3d" data-pinned="true"');
    expect(html).toMatch(/data-testid="stub-formats"[^>]*><a data-testid="stub-archive" data-task="t-3d"/);
  });
});

describe('the tab bar', () => {
  beforeEach(() => { mock.object = model; });
  const html = () => render();

  it('is a 44px tab list named by the Schema, with an icon and the name on each tab', () => {
    const markup = html();
    expect(markup).toMatch(/role="tablist"[^>]*aria-label="右侧栏标签"/);
    expect(markup).toContain('right-panel-tabs');
    expect(markup).toMatch(/class="flex h-11 shrink-0 border-b/);
    const tabs = markup.match(/<button[^>]*role="tab"[\s\S]*?<\/button>/g) ?? [];
    expect(tabs).toHaveLength(5);
    for (const tab of tabs) {
      expect(tab).toContain('<svg');
      expect(tab).toContain('h-11');
    }
    const labels = tabs.map((tab) => tab.match(/<span class="truncate">([^<]+)<\/span>/)?.[1]);
    expect(labels).toEqual(['功能', '加工', '格式', '部件', '详情']);
  });

  it('underlines the active tab only, 2px', () => {
    const markup = html();
    expect(markup.match(/data-testid="right-panel-underline"/g)).toHaveLength(1);
    expect(markup).toMatch(/class="absolute inset-x-1 bottom-0 h-0\.5 bg-foreground"/);
    const active = markup.match(/<button[^>]*aria-selected="true"[\s\S]*?<\/button>/)?.[0] ?? '';
    expect(active).toContain('right-panel-underline');
    expect(active).toContain('right-panel-tab-operations');
  });

  it('keeps one tab in the tab order and links each tab to the one body', () => {
    const markup = html();
    expect(markup.match(/tabindex="0"/g)).toHaveLength(1);
    expect(markup.match(/tabindex="-1"/g)).toHaveLength(4);
    expect(markup.match(/aria-controls="right-panel-body"/g)).toHaveLength(5);
    expect(markup).toMatch(/role="tabpanel" id="right-panel-body" aria-labelledby="right-panel-tab-operations"/);
  });

  it('carries no native title: the hover hint is the only help', () => {
    expect(html()).not.toMatch(/\stitle="/);
    const bar = renderToStaticMarkup(<PanelTabBar views={panelTabViews(schema, 'image')} active="layers" onSelect={() => {}} label="x" />);
    expect(bar).not.toMatch(/\stitle="/);
    expect(bar).not.toContain('aria-label="图层"');
  });

  it('draws the tabs of a given kind in the Schema order, whatever the active one is', () => {
    const bar = renderToStaticMarkup(<PanelTabBar views={panelTabViews(schema, 'image')} active="details" onSelect={() => {}} label="x" />);
    expect(tabIds(bar)).toEqual(['layers', 'history', 'details', 'operations']);
    expect(selectedTab(bar)).toEqual(['details']);
  });

  it('has no tab selected for a kind that offers none', () => {
    const bar = renderToStaticMarkup(<PanelTabBar views={[]} active={null} onSelect={() => {}} label="x" />);
    expect(tabIds(bar)).toEqual([]);
  });
});

describe('what the panel does not have', () => {
  const source = readFileSync(new URL('./right-panel.tsx', import.meta.url), 'utf8');
  const code = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

  it('has no collapse button and no remembered width', () => {
    expect(code).not.toMatch(/collapse|expanded|localStorage|sessionStorage|resize/i);
    expect(code).not.toContain('title=');
  });

  it('keys the 加工 body by task and asset, so another model starts a fresh form', () => {
    expect(source).toContain('<ModelProcessingPanel key={`${object.taskId}:${object.assetId}`} selection={object} />');
    expect(source).toContain('<ModelFormatsPanel\n          key={object.taskId}');
  });

  it('writes the chosen tab to the studio store for the kind it shows', () => {
    expect(source).toContain('onSelect={(id) => setPanelTab(context, id)}');
  });

  it('carries no user text of its own: every word comes from the Schema', () => {
    expect(code).not.toMatch(/[一-鿿]/);
  });
});
