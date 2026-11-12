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
vi.mock('@/components/workspace/style-lock-panel', () => ({
  StyleLockSection: ({ sessionId }: { sessionId: string | null }) => <div data-testid="stub-style-lock" data-session={sessionId ?? ''} />,
}));
vi.mock('@/components/workspace/preferences-panel', () => ({
  PreferencesPanel: ({ sessionId }: { sessionId: string | null }) => <div data-testid="stub-preferences" data-session={sessionId ?? ''} />,
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
vi.mock('@/components/studio/brief-panel', () => ({
  BriefPanel: ({ sessionId }: { sessionId: string | null }) => <div data-testid="stub-brief" data-session={sessionId ?? ''} />,
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
  useStudioStore.setState({ panelTab2d: null, panelTab3d: null, styleLockRequest: null, styleLockOpen: false, panelCollapsed: false });
});
afterEach(() => {
  useStudioStore.setState({ panelTab2d: null, panelTab3d: null, styleLockRequest: null, styleLockOpen: false, panelCollapsed: false });
});

describe('nothing on the stage and an empty board', () => {
  it('shows the board tabs with only function settings selectable, plus the empty-canvas line', () => {
    const html = render();
    expect(html).toContain('data-testid="right-panel"');
    expect(html).toContain('data-kind="none"');
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task=""');
    expect(html).toContain('data-testid="stub-style-lock" data-session="s1"');
    // #241: the preference summary sits in 功能, below the style lock and above the operation groups.
    expect(html).toContain('data-testid="stub-preferences" data-session="s1"');
    expect(html.indexOf('stub-style-lock')).toBeLessThan(html.indexOf('stub-preferences'));
    expect(html.indexOf('stub-preferences')).toBeLessThan(html.indexOf('stub-operations'));
    // The tab rail sits to the right of the content, after it in the markup.
    expect(html.indexOf('stub-operations')).toBeLessThan(html.indexOf('right-panel-tabs'));
    expect(html).toContain('role="tablist"');
    expect(html).toContain('data-tab="operations"');
    expect(html).toMatch(/id="right-panel-tab-layers"[^>]*aria-disabled="true"/);
    expect(html).toMatch(/id="right-panel-tab-history"[^>]*aria-disabled="true"/);
    expect(html).not.toMatch(/id="right-panel-tab-operations"[^>]*aria-disabled/);
    expect(html).not.toMatch(/id="right-panel-tab-details"[^>]*aria-disabled/);
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
    expect(tabIds(html)).toEqual(['layers', 'history', 'details', 'operations', 'project']);
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
    expect(details).toContain('data-testid="stub-brief" data-session="s1"');
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

  it('adds 功能 and 详情 after 图层 and 历史 and still opens on 图层', () => {
    const html = render();
    expect(html).toContain('data-kind="board"');
    expect(tabIds(html)).toEqual(['layers', 'history', 'operations', 'details', 'project']);
    expect(selectedTab(html)).toEqual(['layers']);
    expect(html).not.toContain('right-panel-empty');
  });

  it('shares the picture\'s 2D choice, including 详情, with the board', () => {
    useStudioStore.getState().setPanelTab('image', 'history');
    expect(selectedTab(render())).toEqual(['history']);
    useStudioStore.getState().setPanelTab('image', 'details');
    const html = render();
    expect(selectedTab(html)).toEqual(['details']);
    expect(bodyOf(html)).toBe('details');
    expect(html).toContain('data-testid="stub-brief" data-session="s1"');
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

/** The tab body's markup: from the tab panel to the tab rail that follows it. */
const tabBody = (html: string) => html.slice(html.indexOf('id="right-panel-body"'), html.indexOf('role="tablist"'));

describe('the 功能 tab scroll container (#254)', () => {
  beforeEach(() => {
    mock.object = picture;
    useStudioStore.getState().setPanelTab('image', 'operations');
  });

  it('makes the tab body the one vertical scroll element, covering 风格锁定 and 选择功能', () => {
    const html = render();
    const body = html.match(/<div class="([^"]*)" data-testid="right-panel-operations">/);
    expect(body).not.toBeNull();
    const classes = body![1].split(/\s+/);
    expect(classes).toContain('flex');
    expect(classes).toContain('min-h-0');
    expect(classes).toContain('flex-1');
    expect(classes).toContain('flex-col');
    expect(classes).toContain('overflow-y-auto');
    expect(classes).toContain('[scrollbar-gutter:stable]');
    // Both sections scroll together, in order: 风格锁定 first, then the operation groups and save bar.
    expect(html.indexOf('stub-style-lock')).toBeLessThan(html.indexOf('stub-operations'));
    // The tab rail scrolls on its own in a very short window; only the tab body counts here.
    const scrollBody = tabBody(html);
    expect(scrollBody.match(/overflow-y-auto/g)).toHaveLength(1);
    expect(scrollBody.match(/\[scrollbar-gutter:stable\]/g)).toHaveLength(1);
  });

  it('stays the only scrollport in a 600px-high window: the children keep their content height', () => {
    const html = render();
    // The section bodies must not add a second scrollbar when the window is short; the tab scrolls instead.
    expect(tabBody(html).match(/overflow-y-auto/g)).toHaveLength(1);
    expect(html.match(/max-h-/g) ?? []).toHaveLength(0);
  });
});

describe('a 3D model on the stage', () => {
  beforeEach(() => { mock.object = model; });

  it('has 功能, 加工, 格式, 部件 and 详情 and opens on 功能', () => {
    const html = render();
    expect(html).toContain('data-kind="model"');
    expect(tabIds(html)).toEqual(['operations', 'process', 'formats', 'parts', 'details', 'project']);
    expect(selectedTab(html)).toEqual(['operations']);
    expect(html).toContain('data-testid="stub-operations" data-session="s1" data-task="t-3d"');
    expect(html).toContain('data-testid="stub-style-lock" data-session="s1"');
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

  it('is a vertical tab rail named by the Schema, with an icon and the full name on each tab', () => {
    const markup = html();
    expect(markup).toMatch(/role="tablist"[^>]*aria-label="右侧栏标签"[^>]*aria-orientation="vertical"/);
    expect(markup).toContain('right-panel-tabs');
    expect(markup).toMatch(/class="flex w-9 shrink-0 flex-col/);
    const tabs = markup.match(/<button[^>]*role="tab"[\s\S]*?<\/button>/g) ?? [];
    expect(tabs).toHaveLength(6);
    for (const tab of tabs) {
      expect(tab).toContain('<svg');
      expect(tab).not.toContain('truncate');
    }
    const labels = tabs.map((tab) => tab.match(/<span class="[^"]*writing-mode:vertical-rl[^"]*">([^<]+)<\/span>/)?.[1]);
    expect(labels).toEqual(['功能', '加工', '格式', '部件', '详情', '项目']);
  });

  it('puts the rail after the content, at the panel\'s right edge', () => {
    const markup = html();
    expect(markup.indexOf('id="right-panel-body"')).toBeLessThan(markup.indexOf('data-testid="right-panel-tabs"'));
  });

  it('fills and outlines the active tab only', () => {
    const markup = html();
    const tabs = markup.match(/<button[^>]*role="tab"[^>]*>/g) ?? [];
    const filled = tabs.filter((tab) => /border-border bg-accent text-foreground/.test(tab));
    expect(filled).toHaveLength(1);
    expect(filled[0]).toContain('right-panel-tab-operations');
  });

  it('keeps one tab in the tab order and links each tab to the one body', () => {
    const markup = html();
    expect(markup.match(/tabindex="0"/g)).toHaveLength(1);
    expect(markup.match(/tabindex="-1"/g)).toHaveLength(5);
    expect(markup.match(/aria-controls="right-panel-body"/g)).toHaveLength(6);
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
    expect(tabIds(bar)).toEqual(['layers', 'history', 'details', 'operations', 'project']);
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

  it('has no separate collapse button and no resizable or remembered width (folding is the tabs, kept by the store)', () => {
    expect(code).not.toMatch(/localStorage|sessionStorage|resize/i);
    expect(code).not.toContain('title=');
  });

  it('keys the 加工 body by task and asset, so another model starts a fresh form', () => {
    expect(source).toContain('<ModelProcessingPanel key={`${object.taskId}:${object.assetId}`} selection={object} />');
    expect(source).toContain('<ModelFormatsPanel\n          key={object.taskId}');
  });

  it('writes the chosen tab to the studio store for the kind it shows', () => {
    expect(source).toContain('onSelect={choose(context, active)}');
    expect(source).toContain("else setPanelTab(kind === 'none' ? 'board' : kind, id);");
  });

  it('carries no user text of its own: every word comes from the Schema', () => {
    expect(code).not.toMatch(/[一-鿿]/);
  });
});

describe('folding the panel to its tab rail', () => {
  beforeEach(() => { mock.object = model; });
  const render = () => renderToStaticMarkup(<RightPanel />);

  it('is open by default: the active tab is filled and expanded, the body shown', () => {
    const markup = render();
    expect(markup).not.toContain('data-collapsed');
    expect(markup).toMatch(/aria-selected="true" aria-expanded="true"/);
    expect(markup).not.toMatch(/class="min-h-0 min-w-0 flex-col hidden"/);
  });

  it('hides the content column when folded and keeps the rail with the active tab outlined', () => {
    useStudioStore.setState({ panelCollapsed: true });
    const markup = render();
    expect(markup).toContain('data-collapsed="true"');
    expect(markup).toMatch(/class="min-h-0 min-w-0 flex-col hidden"/);
    expect(markup).toContain('data-testid="right-panel-tabs"');
    const active = markup.match(/<button[^>]*aria-selected="true"[^>]*>/)?.[0] ?? '';
    expect(active).toContain('aria-expanded="false"');
    expect(active).not.toContain('bg-accent text-foreground');
  });

  it('folds on the active tab and opens again on any tab', () => {
    const source = readFileSync(new URL('./right-panel.tsx', import.meta.url), 'utf8');
    expect(source).toContain('if (id === current && !collapsed) setPanelCollapsed(true);');
    useStudioStore.setState({ panelCollapsed: true });
    useStudioStore.getState().setPanelTab('model', 'details');
    expect(useStudioStore.getState().panelCollapsed).toBe(false);
  });
});
