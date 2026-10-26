import React from 'react';
import { beforeEach, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { RetextureWithImageAction } from '@/components/studio/retexture-with-image';
import type { ActionView } from '@/lib/workspace/stage-actions';
import type { VersionTile } from '@/lib/workspace/versions';
import type { CurrentObject } from '@/store/workspace-store';

// Exercise component event wiring without a DOM: keep hook state between renders,
// while leaving the real target, object and handle resolution in the component.
const fixture = vi.hoisted(() => ({ states: [] as unknown[], cursor: 0, messages: [] as unknown[], execute: vi.fn() }));
vi.mock('react', async (original) => {
  const actual = await original<typeof import('react')>();
  return { ...actual, useMemo: (fn: () => unknown) => fn(), useState: (initial: unknown) => {
    const index = fixture.cursor++;
    if (!(index in fixture.states)) fixture.states[index] = initial;
    return [fixture.states[index], (value: unknown) => { fixture.states[index] = value; }];
  } };
});
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('@/components/ui/overlay', () => ({ useAnchoredPositioner: () => '' }));
vi.mock('@/hooks/use-object-actions', () => ({ useObjectActions: () => ({ retextureWithImage: fixture.execute }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ activeSessionId: 's1', messagesBySession: { s1: fixture.messages } }) }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles }), tileOfObject: () => null }));

const tile = (taskId: string, version: number): VersionTile => ({
  key: taskId, taskId, taskIds: [taskId], domain: 'model', version, status: 'ready', progress: 0,
  capability: '', createdAt: '', thumbnailUrl: '', groupLabel: '', groupComplete: false,
  slots: [], views: [], parents: [], failure: '', origin: 'task', operation: '', captureId: '', size: null,
  assets: [{ id: `${taskId}-glb`, kind: 'model', url: `/${taskId}.glb`, mediaType: 'model/gltf-binary', role: 'model', variant: 'glb', ordinal: 0 }],
});
const tiles = [tile('m1', 1), tile('m2', 2)];
const object: CurrentObject = { kind: 'image', taskId: 'image-task', assetId: 'picture', url: '/picture.png', sessionId: 's1' };
const view = { id: 'retexture_with_image', label: '换贴图', testId: 'retexture', callsModel: true } as ActionView;
type Props = Record<string, unknown> & { children?: React.ReactNode };
function elements(node: React.ReactNode): React.ReactElement<Props>[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement<Props>(node)) return [];
  return [node, ...elements(node.props.children)];
}
function render() {
  fixture.cursor = 0;
  return elements(RetextureWithImageAction({ object, view }));
}
const find = (nodes: ReturnType<typeof render>, testId: string) => nodes.find((node) => node.props['data-testid'] === testId)!;
beforeEach(() => { fixture.states = []; fixture.cursor = 0; fixture.execute.mockReset(); fixture.messages = [{ attachments: [{ kind: 'model', taskId: 'm1', assetId: 'm1-zip', handle: '@model1' }] }]; });

it('selects an enabled GLB tile through its existing ZIP handle and confirms the unchanged handle payload', () => {
  let nodes = render();
  const rows = nodes.filter((node) => node.props.role === 'radio');
  expect(rows[0].props.disabled).toBe(false);
  expect(rows[1].props.disabled).toBe(true); // latest model has no server reference
  expect(find(nodes, 'stage-retexture-confirm').props.disabled).toBe(true);
  (rows[0].props.onClick as () => void)();
  nodes = render();
  expect(nodes.find((node) => node.props.role === 'radio')?.props['aria-checked']).toBe(true);
  const confirm = find(nodes, 'stage-retexture-confirm');
  expect(confirm.props.disabled).toBe(false);
  (confirm.props.onClick as () => void)();
  expect(fixture.execute).toHaveBeenCalledExactlyOnceWith(object, '@model1', '');
});

it('cancels without executing after choosing a model, and missing references remain disabled', () => {
  let nodes = render();
  (nodes.find((node) => node.props.role === 'radio')!.props.onClick as () => void)();
  nodes = render();
  const cancel = nodes.find((node) => node.props.variant === 'ghost' && typeof node.props.onClick === 'function')!;
  (cancel.props.onClick as () => void)();
  expect(fixture.execute).not.toHaveBeenCalled();
  expect(fixture.states[0]).toBe(false);
  fixture.messages = [];
  nodes = render();
  expect(nodes.filter((node) => node.props.role === 'radio').every((node) => node.props.disabled === true)).toBe(true);
  expect(find(nodes, 'stage-retexture-confirm').props.disabled).toBe(true);
});
