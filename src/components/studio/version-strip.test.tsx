import React, { Children, isValidElement, type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { blankTile, type VersionTile } from '@/lib/workspace/versions';

const state = vi.hoisted(() => ({ tiles: [] as VersionTile[], pick: null as null | { kind: string; taskId: string; assetId: string }, compare: null as object | null, object: null as null | { taskId: string; assetId: string; kind: string; url: string; sessionId: string }, stage: vi.fn(), detail: vi.fn(), toggle: vi.fn(() => true), buttons: [] as ReactElement<Record<string, any>>[], targets: [] as any[] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles: state.tiles }), tileOfObject: () => null }));
vi.mock('@/hooks/use-session-pick', () => ({ useSessionPick: () => ({ pick: state.pick }) }));
vi.mock('@/hooks/use-object-actions', () => ({ useObjectActions: () => ({ openOnStage: state.stage }) }));
vi.mock('@/hooks/use-stage-viewer', () => ({ useStageViewer: () => ({ toggleTileInCompare: state.toggle, slotOf: () => null }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (selector: any) => selector({ activeSessionId: 'session' }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (selector: any) => selector({ currentObject: state.object }) }));
vi.mock('@/store/studio-store', () => ({ useStudioStore: (selector: any) => selector({ compare: state.compare, setAllOutputsOpen: vi.fn() }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: ReactNode }) => children }));
vi.mock('@/components/result/result-card-parts', () => ({
  openResultDetail: state.detail,
  useCanvasDragSource: () => ({ onDragStart: vi.fn() }),
  CardDownload: () => null,
  ResultContextMenu: ({ children, target, render }: any) => {
    state.targets.push(target);
    const collect = (nodes: ReactNode) => Children.forEach(nodes, (node) => { if (isValidElement(node)) { const element = node as ReactElement<Record<string, any>>; if (element.type === 'button') state.buttons.push(element); collect(element.props.children); } });
    collect(children);
    return React.cloneElement(render, {}, children);
  },
}));
import { VersionStrip } from './version-strip';
const tile = (patch: Partial<VersionTile> = {}) => blankTile({ key: 'n:old', taskId: 'old', taskIds: ['old'], domain: 'image', status: 'ready', origin: 'task', assets: [{ id: 'a', kind: 'image', url: '/a', role: 'output', variant: '', mediaType: 'image/png', ordinal: 0 }], ...patch });
const primary = () => state.buttons.find((button) => button.props['data-shortcut-arrows'])!;
const detail = () => state.buttons.find((button) => button.props['data-testid'] === 'version-detail')!;
const click = { detail: 1, stopPropagation: vi.fn(), currentTarget: { focus: vi.fn() } };
beforeEach(() => { state.tiles = [tile()]; state.pick = null; state.object = null; state.compare = null; state.buttons = []; state.targets = []; vi.clearAllMocks(); });

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
  it.each(['upload', 'merge', 'capture'] as const)('reserves aligned rows without request details for %s', (origin) => {
    state.tiles = [tile({ origin })];
    const html = renderToStaticMarkup(<VersionStrip />);
    expect(html).toContain('version-caption'); expect(html).toContain('version-detail-row');
    expect(detail()).toBeUndefined(); expect(state.targets[0].taskId).toBeUndefined();
    primary().props.onClick(click); expect(state.stage).toHaveBeenCalledTimes(1);
  });
  it.each(['pending', 'running', 'failed'] as const)('disables unavailable %s display and retains its request details', (status) => {
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
  it.each([3, 4, 8])('folds a %i-sibling branch into one compact tile and an accessible expansion', (count) => {
    state.tiles = Array.from({ length: count }, (_, index) => tile({
      key: `n:${index}`, taskId: `t${index}`, taskIds: [`t${index}`],
      parents: [{ key: 'parent', known: true, kind: 'source_task', evidence: 'recorded', link: 'edit', step: '' }],
    }));
    const html = renderToStaticMarkup(<VersionStrip />);
    // One 50px compact result plus a 32px expansion remains inside the 112px strip.
    expect(html.match(/data-testid="version-tile"/g)).toHaveLength(1);
    expect(html).toContain('data-testid="version-fold-trigger"');
    expect(html).toContain(`+${count - 1}`);
    expect(html).toContain('h-[34px]'); expect(html).toContain('h-4'); expect(html).toContain('h-8');
  });
  it('does not turn a drag into pointer navigation and still permits keyboard activation', () => {
    renderToStaticMarkup(<VersionStrip />); primary().props.onDragStart({}); primary().props.onClick(click);
    expect(state.stage).not.toHaveBeenCalled(); primary().props.onClick({ detail: 0 }); expect(state.stage).toHaveBeenCalledTimes(1);
  });
});
