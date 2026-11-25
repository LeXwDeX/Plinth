import React, { Children, isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { blankTile, type VersionTile } from '@/lib/workspace/versions';

const state = vi.hoisted(() => ({ tiles: [] as VersionTile[], pick: null as null | { kind: string; taskId: string; assetId: string }, compare: null as object | null, object: null as null | { taskId: string; assetId: string; kind: string; url: string; sessionId: string }, stage: vi.fn(), detail: vi.fn(), toggle: vi.fn(() => true), buttons: [] as ReactElement<Record<string, any>>[], targets: [] as any[], messages: [] as any[] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles: state.tiles }), tileOfObject: (_versions: unknown, object: { taskId?: string } | null) => (object ? state.tiles.find((entry) => entry.taskId === object.taskId) ?? null : null) }));
vi.mock('@/hooks/use-session-pick', () => ({ useSessionPick: () => ({ pick: state.pick }) }));
// #559: a strip click goes through viewOnStage (look, do not place); the record says which action ran.
// #593: a double click goes through insertOnCanvas, the same call as 「插入到画布」 in the detail.
vi.mock('@/hooks/use-object-actions', () => ({ useObjectActions: () => ({ openOnStage: state.stage, viewOnStage: (object: object) => state.stage({ ...object, via: 'view' }), insertOnCanvas: (object: object) => state.stage({ ...object, via: 'insert' }) }) }));
vi.mock('@/hooks/use-stage-viewer', () => ({ useStageViewer: () => ({ toggleTileInCompare: state.toggle, slotOf: () => null }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (selector: any) => selector({ activeSessionId: 'session', messagesBySession: { session: state.messages } }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (selector: any) => selector({ currentObject: state.object }) }));
vi.mock('@/store/studio-store', () => ({ useStudioStore: (selector: any) => selector({ compare: state.compare, setAllOutputsOpen: vi.fn() }) }));
// The popovers render in place, so a static render shows what an open 「全部」 lists.
vi.mock('@base-ui/react/popover', () => {
  const pass = ({ children }: { children?: ReactNode }) => children;
  const element = (tag: string) => ({ children, positionMethod: _p, side: _s, align: _a, sideOffset: _o, collisionPadding: _c, ...props }: Record<string, any>) => React.createElement(tag, props, children);
  return { Popover: { Root: pass, Portal: pass, Positioner: pass, Popup: element('div'), Title: element('h2'), Trigger: element('button') } };
});
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: ReactNode }) => children }));
vi.mock('@/components/result/result-card-parts', () => ({
  openResultDetail: state.detail,
  useCanvasDragSource: () => ({ onDragStart: vi.fn() }),
  CardDownload: () => null,
  ResultContextMenu: ({ children, target, render }: any) => {
    state.targets.push(target);
    const collect = (nodes: ReactNode) => Children.forEach(nodes, (node) => { if (isValidElement(node)) { const element = node as ReactElement<Record<string, any>>; if (element.type === 'button' || element.props['data-testid'] === 'version-detail') state.buttons.push(element); collect(element.props.children); } });
    collect(children);
    return React.cloneElement(render, {}, children);
  },
}));
import { VersionStrip } from './version-strip';
const tile = (patch: Partial<VersionTile> = {}) => blankTile({ key: 'n:old', taskId: 'old', taskIds: ['old'], domain: 'image', status: 'ready', origin: 'task', assets: [{ id: 'a', kind: 'image', url: '/a', role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }], ...patch });
const primary = () => state.buttons.find((button) => button.props['data-shortcut-arrows'])!;
const detail = () => state.buttons.find((button) => button.props['data-testid'] === 'version-detail')!;
const click = { detail: 1, stopPropagation: vi.fn(), currentTarget: { focus: vi.fn() } };
beforeEach(() => { state.tiles = [tile()]; state.pick = null; state.object = null; state.compare = null; state.buttons = []; state.targets = []; state.messages = []; vi.clearAllMocks(); });

describe('output strip navigation and details', () => {
  it('selects the historical output on primary activation and keeps request details separate', () => {
    renderToStaticMarkup(<VersionStrip />);
    primary().props.onClick(click);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'a', sessionId: 'session' }));
    expect(state.detail).not.toHaveBeenCalled();
    state.stage.mockClear(); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'a' }));
    expect(state.stage).not.toHaveBeenCalled();
    expect(click.currentTarget.focus).toHaveBeenCalled();
  });
  it('opens the locally viewed second candidate even when the persisted pick names the first', () => {
    state.tiles = [tile({ assets: [tile().assets[0], { ...tile().assets[0], id: 'second', url: '/second' }] })];
    state.pick = { kind: 'image', taskId: 'old', assetId: 'a' };
    state.object = { kind: 'image', taskId: 'old', assetId: 'second', url: '/second', sessionId: 'session' };
    renderToStaticMarkup(<VersionStrip />); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'second', url: '/second' }));
    primary().props.onClick(click);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'second' }));
  });
  it.each([
    ['another-task', 'session', 'a'],
    ['old', 'another-session', 'a'],
    ['old', 'session', 'foreign-asset'],
  ])('does not borrow a displayed candidate from unrelated identity %s/%s/%s', (taskId, sessionId, assetId) => {
    state.pick = { kind: 'image', taskId: 'old', assetId: 'a' };
    state.object = { kind: 'image', taskId, sessionId, assetId, url: '/foreign' };
    renderToStaticMarkup(<VersionStrip />); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'a', url: '/a' }));
  });
  it('rejects a displayed three-view task and asset that belong to different declared slots', () => {
    state.tiles = [tile({
      taskIds: ['old', 'side'],
      assets: [tile().assets[0], { ...tile().assets[0], id: 'side-asset', url: '/side' }],
      slots: [{ taskId: 'old', assetId: 'a', slot: 'front', label: 'Front' }, { taskId: 'side', assetId: 'side-asset', slot: 'side', label: 'Side' }],
    })];
    state.object = { taskId: 'side', assetId: 'a', kind: 'image', url: '/foreign-pair', sessionId: 'session' };
    state.pick = { taskId: 'side', assetId: 'side-asset', kind: 'image' };
    renderToStaticMarkup(<VersionStrip />); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'side', assetId: 'side-asset', url: '/side' }));
  });
  it('falls back to the default candidate when a persisted pick names a foreign asset', () => {
    state.pick = { kind: 'image', taskId: 'old', assetId: 'foreign' };
    renderToStaticMarkup(<VersionStrip />); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'a', url: '/a' }));
  });
  it('uses the selected three-view member for both independent actions', () => {
    state.pick = { kind: 'image', taskId: 'side', assetId: 'a' };
    state.tiles = [tile({ taskIds: ['old', 'side'], slots: [{ taskId: 'side', assetId: 'a', slot: 'side', label: '侧面' }] })];
    renderToStaticMarkup(<VersionStrip />); primary().props.onClick(click); detail().props.onClick(click);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'side', assetId: 'a' }));
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'side', assetId: 'a' }));
  });
  it('looks at the version on a click and never asks to place it on the board (#559)', () => {
    renderToStaticMarkup(<VersionStrip />);
    primary().props.onClick(click);
    expect(state.stage).toHaveBeenCalledTimes(1);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', via: 'view' }));
  });
  it('gives an upload or an Agent crop a detail without a Task, by its stage object id (#587)', () => {
    state.tiles = [tile({ key: 'u:crop-1', taskId: 'upload:crop-1', taskIds: ['upload:crop-1'], origin: 'upload', operation: 'crop',
      assets: [{ id: 'crop-1', kind: 'image', url: '/api/v1/assets/crop-1', role: 'image', variant: '', mediaType: 'image/png', ordinal: 0 }] })];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).toContain('裁图');
    detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: undefined, stageObjectId: 'upload:crop-1', assetId: 'crop-1', url: '/api/v1/assets/crop-1' }));
    primary().props.onClick(click);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'upload:crop-1', via: 'view' }));
  });
  it.each(['merge', 'capture'] as const)('reserves aligned rows without request details for %s', (origin) => {
    state.tiles = [tile({ origin })];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).toContain('version-caption'); expect(html).toContain('version-detail-row');
    expect(detail()).toBeUndefined(); expect(state.targets[0].taskId).toBeUndefined();
    primary().props.onClick(click); expect(state.stage).toHaveBeenCalledTimes(1);
  });
  it('leaves a failed attempt out: it has nothing to show or take, and the conversation says why it failed', () => {
    state.tiles = [tile(), tile({ key: 'n:failed', taskId: 'failed', taskIds: ['failed'], status: 'failed', assets: [] })];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).not.toContain('data-key="n:failed"');
    expect(html).toContain('data-key="n:old"');
    state.tiles = [tile({ status: 'failed', assets: [] })];
    expect(renderToStaticMarkup(<VersionStrip />)).toContain('data-testid="version-strip-empty"');
  });

  it.each(['pending', 'running'] as const)('disables unavailable %s display and retains its request details', (status) => {
    state.tiles = [tile({ status, assets: [] })]; renderToStaticMarkup(<VersionStrip />);
    expect(primary().props.disabled).toBe(true); detail().props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'old', status }));
    expect(state.stage).not.toHaveBeenCalled();
  });
  it('keeps compare toggles separate from request details', () => {
    state.compare = { slots: [] }; renderToStaticMarkup(<VersionStrip />); primary().props.onClick(click);
    expect(state.toggle).toHaveBeenCalledWith(state.tiles[0]); expect(state.stage).not.toHaveBeenCalled();
    detail().props.onClick(click); expect(state.detail).toHaveBeenCalledTimes(1);
  });
  const siblings = (count: number) => Array.from({ length: count }, (_, index) => tile({
    key: `n:${index}`, taskId: `t${index}`, taskIds: [`t${index}`],
    assets: [{ id: `a${index}`, kind: 'image', url: `/a${index}`, role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }],
    parents: [{ key: 'parent', known: true, kind: 'source_task', evidence: 'recorded', link: 'edit', step: '' }],
  }));
  const keysIn = (html: string) => [...html.matchAll(/data-testid="version-tile" data-key="([^"]+)"/g)].map((match) => match[1]);
  const foldList = (html: string) => html.slice(html.indexOf('data-testid="version-fold-list"'));
  const primaries = () => state.buttons.filter((button) => button.props['data-shortcut-arrows']);
  const details = () => state.buttons.filter((button) => button.props['data-testid'] === 'version-detail');
  it.each([3, 4, 8])('folds %i results of one step into the first one and 「全部」, which lists every one of them in order (#596)', (count) => {
    state.tiles = siblings(count);
    const html = renderToStaticMarkup(<VersionStrip />);
    const strip = html.slice(0, html.indexOf('data-testid="version-fold-list"'));
    // One 50px compact result plus a 32px 「全部」 remains inside the strip; no "+N" count.
    expect(keysIn(strip)).toEqual(['n:0']);
    expect(html).toMatch(/data-testid="version-fold-trigger"[^>]*>全部<\/button>/);
    expect(html).not.toMatch(/>\+\d+</);
    expect(html).not.toContain('同一来源的其他结果');
    expect(html).toContain('h-[34px]'); expect(html).toContain('h-5'); expect(html).toContain('h-8');
    // The list is everything this step made, the one outside included, in the order they were made.
    expect(foldList(html)).toContain('这一步生成的全部内容');
    expect(keysIn(foldList(html))).toEqual(Array.from({ length: count }, (_, index) => `n:${index}`));
  });
  it('references a folded result on a click without swapping the one outside or reordering the list (#596)', () => {
    state.tiles = siblings(4);
    const before = renderToStaticMarkup(<VersionStrip />);
    // The list is [n:0 outside, then n:0..n:3 inside 「全部」]: a click on n:2 references it, stage only.
    primaries()[3].props.onClick(click);
    expect(state.stage).toHaveBeenCalledTimes(1);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 't2', assetId: 'a2', via: 'view' }));
    // It is now the current object (what the input's reference chip carries); the strip keeps its order and its first.
    state.object = { kind: 'image', taskId: 't2', assetId: 'a2', url: '/a2', sessionId: 'session' };
    state.buttons = [];
    const after = renderToStaticMarkup(<VersionStrip />);
    expect(keysIn(after)).toEqual(keysIn(before));
    expect(keysIn(after.slice(0, after.indexOf('data-testid="version-fold-list"')))).toEqual(['n:0']);
    expect(after).toMatch(/aria-current="true" data-testid="version-fold-trigger"/);
    expect(foldList(after)).toMatch(/data-key="n:2" data-status="ready" data-current="true"/);
    expect(after).toMatch(/data-key="n:0" data-status="ready" data-current="false"/);
  });
  it('inserts a folded result on a double click and opens its own detail from 「详情」 (#596)', () => {
    state.tiles = siblings(4);
    renderToStaticMarkup(<VersionStrip />);
    primaries()[4].props.onDoubleClick();
    expect(state.stage).toHaveBeenCalledTimes(1);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 't3', assetId: 'a3', via: 'insert' }));
    details()[2].props.onClick(click);
    expect(state.detail).toHaveBeenCalledWith(expect.objectContaining({ taskId: 't1', assetId: 'a1' }));
  });
  it('shows both of two results of one step, with no 「全部」', () => {
    state.tiles = siblings(2);
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(keysIn(html)).toEqual(['n:0', 'n:1']);
    expect(html).not.toContain('version-fold-trigger');
  });
  it('keeps the tallest card and its detail row above a horizontal scrollbar', () => {
    const html = renderToStaticMarkup(<VersionStrip />);
    // A regular card: 2+2 padding, 56 thumbnail, 2 gap, 18 label, 12 caption, 20 「详情」 button row (#593) = 112px.
    for (const part of ['p-0.5', 'size-14', 'gap-0.5', 'h-[18px]', 'h-3', 'h-5']) expect(html).toContain(part);
    expect(html).toMatch(/data-testid="version-strip" class="[^"]*\bh-36\b/);
    expect(html).toMatch(/<ol class="[^"]*\bpy-3\b/);
    // #546: 144px strip − 1px top border − an 11px thin scrollbar (Chromium) leaves 132px: 12px above the card, the card,
    // and at least 8px below it, so the scrollbar only takes bottom padding.
    expect(144 - 1 - 11).toBeGreaterThanOrEqual(12 + 112 + 8);
    // The selected card's 2px ring sits 12px below the strip's top edge, with 10px of room left above it.
    expect(12 - 2).toBeGreaterThanOrEqual(8);
  });
  it('inserts a 2D version on a double click, through the same insertOnCanvas as the detail (#593)', () => {
    renderToStaticMarkup(<VersionStrip />);
    // A double click is two clicks and a dblclick: the first click looks, the second repeats nothing, the dblclick inserts.
    primary().props.onClick({ detail: 1 }); primary().props.onClick({ detail: 2 }); primary().props.onDoubleClick({});
    expect(state.stage.mock.calls.map(([object]) => object.via)).toEqual(['view', 'insert']);
    expect(state.stage).toHaveBeenLastCalledWith(expect.objectContaining({ taskId: 'old', assetId: 'a', url: '/a', via: 'insert' }));
  });
  it('inserts an Agent crop on a double click, by its upload object (#587/#593)', () => {
    state.tiles = [tile({ key: 'u:crop-1', taskId: 'upload:crop-1', taskIds: ['upload:crop-1'], origin: 'upload', operation: 'crop',
      assets: [{ id: 'crop-1', kind: 'image', url: '/api/v1/assets/crop-1', role: 'image', variant: '', mediaType: 'image/png', ordinal: 0 }] })];
    renderToStaticMarkup(<VersionStrip />); primary().props.onDoubleClick({});
    expect(state.stage).toHaveBeenCalledTimes(1);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'upload:crop-1', assetId: 'crop-1', via: 'insert' }));
  });
  it('keeps a double click on a 3D version to what its click does: it opens on the stage, nothing is inserted', () => {
    state.tiles = [blankTile({ key: 'n:m1', taskId: 'm1', taskIds: ['m1'], domain: 'model', status: 'ready', origin: 'task',
      assets: [{ id: 'glb', kind: 'model', url: '/m.glb', role: 'output', variant: '', mediaType: 'model/gltf-binary', ordinal: 0 }] })];
    renderToStaticMarkup(<VersionStrip />);
    primary().props.onClick({ detail: 1 }); primary().props.onClick({ detail: 2 }); primary().props.onDoubleClick({});
    expect(state.stage).toHaveBeenCalledTimes(1);
    expect(state.stage).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'm1', kind: 'model' }));
    expect(state.stage.mock.calls.some(([object]) => object.via === 'insert')).toBe(false);
  });
  it('does not insert from a double click while comparing or after a drag', () => {
    state.compare = { slots: [] }; renderToStaticMarkup(<VersionStrip />);
    primary().props.onDoubleClick({}); expect(state.stage).not.toHaveBeenCalled();
    state.compare = null; state.buttons = []; renderToStaticMarkup(<VersionStrip />);
    primary().props.onDragStart({}); primary().props.onDoubleClick({}); expect(state.stage).not.toHaveBeenCalled();
  });
  it('makes 「详情」 an outline button as wide as its tile, for a task and for an upload alike (#593)', () => {
    renderToStaticMarkup(<VersionStrip />);
    expect(detail().props).toMatchObject({ variant: 'outline', size: 'xs' });
    expect(detail().props.className).toMatch(/\bw-full\b/);
    expect(detail().props.className).toMatch(/\bjustify-center\b/);
    state.buttons = [];
    state.tiles = [tile({ key: 'u:crop-1', taskId: 'upload:crop-1', taskIds: ['upload:crop-1'], origin: 'upload', operation: 'crop',
      assets: [{ id: 'crop-1', kind: 'image', url: '/api/v1/assets/crop-1', role: 'image', variant: '', mediaType: 'image/png', ordinal: 0 }] })];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(detail().props.className).toMatch(/\bw-full\b/);
    expect(html).toMatch(/data-testid="version-detail"[^>]*>详情</);
    // The tile is 96px (w-24); the row and its button fill it.
    expect(html).toMatch(/data-testid="version-tile"[^>]*/);
    expect(html).toContain('w-24');
  });
  it('does not turn a drag into pointer navigation and still permits keyboard activation', () => {
    renderToStaticMarkup(<VersionStrip />); primary().props.onDragStart({}); primary().props.onClick(click);
    expect(state.stage).not.toHaveBeenCalled(); primary().props.onClick({ detail: 0 }); expect(state.stage).toHaveBeenCalledTimes(1);
  });
});

describe('chat handle badge on a version tile (#87)', () => {
  const routed = (assetId: string) => `/api/v1/tasks/old/assets/${assetId}`;
  it('shows the handle the session messages gave this one picture, without its marker', () => {
    state.tiles = [tile({ assets: [{ id: 'a', kind: 'image', url: routed('a'), role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }] })];
    state.messages = [{ attachments: [{ handle: '@img25', kind: 'image', taskId: 'old', assetId: 'a' }] }];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).toMatch(/data-testid="version-handle-badge"[^>]*>img25</);
    expect(html).toContain('aria-label="对话里的句柄 @img25"');
  });
  it('shows no badge when no message names the picture', () => {
    state.tiles = [tile({ assets: [{ id: 'a', kind: 'image', url: routed('a'), role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }] })];
    state.messages = [{ attachments: [{ handle: '@img3', kind: 'image', taskId: 'old', assetId: 'other' }] }];
    expect(renderToStaticMarkup(<VersionStrip />)).not.toContain('version-handle-badge');
  });
});

describe('where a version came from, in time order (#558)', () => {
  const picture = (taskId: string, version: number) => tile({ key: `n:${taskId}`, taskId, taskIds: [taskId], version,
    assets: [{ id: `${taskId}-a`, kind: 'image', url: `/api/v1/tasks/${taskId}/assets/${taskId}-a`, role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }] });
  const from = (key: string, link: 'to_model' | 'edit') => ({ key, known: true, kind: 'reference' as const, evidence: 'recorded' as const, link, step: '' });
  const model = (taskId: string, parents: ReturnType<typeof from>[]) => blankTile({ key: `n:${taskId}`, taskId, taskIds: [taskId], domain: 'model', status: 'ready', origin: 'task', version: 1, parents });

  it('names every picture of a multi-view 3D beside it, with no one picture as its source', () => {
    state.tiles = [picture('p1', 1), picture('p2', 2), picture('p3', 3), model('m1', [from('n:p1', 'to_model'), from('n:p2', 'to_model')])];
    state.messages = [{ attachments: [{ handle: '@img2', kind: 'image', taskId: 'p1', assetId: 'p1-a' }] }];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).toMatch(/data-testid="version-sources"[^>]*data-count="2"/);
    expect(html.match(/data-testid="version-source-name"[^>]*>([^<]*)</g)?.map((entry) => entry.slice(entry.lastIndexOf('>') + 1, -1))).toEqual(['@img2', '2D v2']);
    expect(html).toContain('data-full-label="由 @img2 2D v2 转成 3D"');
    expect(html.match(/data-testid="version-link"/g)).toHaveLength(1);
  });

  it('keeps a single source as one link and names it when it is not the version just before', () => {
    state.tiles = [picture('p1', 1), picture('p2', 2), model('m1', [from('n:p1', 'to_model')])];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).not.toContain('version-sources');
    expect(html).toContain('data-full-label="由 2D v1 转成 3D"');
    expect(html).toMatch(/data-testid="version-link-source"[^>]*>2D v1</);
  });
});
