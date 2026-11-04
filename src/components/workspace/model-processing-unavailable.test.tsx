// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { useStudioStore } from '@/store/studio-store';
import { ModelProcessingPanel } from './model-processing-panel';

// #93: a step that cannot run says so in one shared shape (warning icon, "无法使用，<short reason>", the full
// explanation with what to do next only in the hover hint); a step the Meshy task ID makes available shows none.

const mock = vi.hoisted(() => ({ source: {} as Record<string, unknown> }));
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const offering = (id: string) => ({ ...(defaultCatalog.offerings as Array<Record<string, unknown>>).find((entry) => entry.id === id), available: true });
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => (defaultCatalog.application.content.messages as Record<string, string>)[key] ?? '' }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: 'session' }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ registerTask: vi.fn() }) }));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({ useModelProcessingCapabilities: () => ({
  available: [{ id: 'remesh', label_cn: '重拓扑' }, { id: 'uv-unwrap', label_cn: 'UV 展开' }],
  offerings: { remesh: [offering('meshy/processing/remesh')], 'uv-unwrap': [offering('meshy/processing/uv-unwrap')] },
  refresh: vi.fn(),
}) }));
vi.mock('swr', () => ({ default: (key: unknown) => (Array.isArray(key) ? { data: mock.source } : { data: [], mutate: vi.fn() }) }));

const selection = { kind: 'model' as const, taskId: 'source', assetId: 'asset', url: '/model.glb', sessionId: 'session' };
function source(provider: string, size: number) {
  mock.source = { id: 'source', session_id: 'session', capability: 'image-to-3d', provider_id: provider,
    offering_id: provider === 'meshy' ? 'meshy/meshy-7.1/image-to-3d' : 'tripo/v3/image-to-3d', state: 'SUCCEEDED', created_at: new Date().toISOString(),
    assets: [{ id: 'asset', role: 'model', variant: 'glb', delivery: 'required', ordinal: 0, mediaType: 'model/gltf-binary', size, url: '/model.glb', kind: 'model' }] };
}
function chooseUV() {
  render(<ModelProcessingPanel selection={selection} />);
  fireEvent.click(screen.getByLabelText('UV 展开'));
}

beforeEach(() => useStudioStore.getState().setStageParts(null));
afterEach(cleanup);

describe('the shared unavailable notice on the processing panel (#93)', () => {
  it('a Meshy model goes into UV unwrap by its task ID, whatever its size, with no warning', () => {
    source('meshy', 31_270_000);
    chooseUV();
    expect(screen.getByTestId('processing-route-uv-unwrap').textContent).toContain('Meshy AI');
    expect(screen.queryByTestId('processing-refused-uv-unwrap')).toBeNull();
    expect(screen.queryByTestId('unavailable-icon')).toBeNull();
  });

  it('a step that cannot run shows the icon, one short line, and the limit with the next step only in the hint', () => {
    source('tripo', 80_000_000);
    chooseUV();
    const notice = screen.getByTestId('processing-refused-uv-unwrap');
    expect(notice.querySelector('[data-testid="unavailable-icon"]')).not.toBeNull();
    const label = notice.querySelector('[data-testid="unavailable-label"]')!.textContent!;
    expect(label.startsWith('无法使用，')).toBe(true);
    expect(label).toContain('73.4 MB');
    expect(label.length).toBeLessThan(24);
    const detail = notice.querySelector('[data-testid="unavailable-detail"]')!.textContent!;
    expect(detail).toContain('80 MB');
    expect(detail).toContain('Remesh');
    expect(screen.queryByTestId('processing-route-uv-unwrap')).toBeNull();
  });

  it('a Meshy model over the face limit brings in Remesh at the most faces UV unwrap takes', () => {
    source('meshy', 31_270_000);
    useStudioStore.getState().setStageParts({ key: 'source:asset', parts: [{ id: 'mesh', name: '', triangles: 600_000 }] });
    chooseUV();
    expect((screen.getByLabelText('重拓扑') as HTMLInputElement).checked).toBe(true);
    expect(screen.getByTestId('processing-route-remesh').textContent).toContain('Meshy AI');
    expect(screen.getByTestId('processing-route-uv-unwrap').textContent).toContain('Meshy AI');
    expect(screen.queryByTestId('unavailable-icon')).toBeNull();
    expect((screen.getByDisplayValue('20000') as HTMLInputElement)).toBeTruthy();
  });

  it('a model from another task says so in one short line, the instruction only in the hint (#94)', () => {
    source('meshy', 31_270_000);
    mock.source = { ...mock.source, session_id: 'other' };
    render(<ModelProcessingPanel selection={selection} />);
    const notice = screen.getByTestId('processing-session-mismatch');
    expect(notice.querySelector('[data-testid="unavailable-icon"]')).not.toBeNull();
    expect(notice.querySelector('[data-testid="unavailable-label"]')!.textContent).toBe('无法使用，不是当前任务的模型');
    expect(notice.querySelector('[data-testid="unavailable-detail"]')!.textContent).toBe(messages['ui.workspace.processing.session_mismatch']);
  });

  it('a model of the current task shows no session notice, and a model without a usable asset says so (#94)', () => {
    source('meshy', 31_270_000);
    render(<ModelProcessingPanel selection={selection} />);
    expect(screen.queryByTestId('processing-session-mismatch')).toBeNull();
    expect(screen.queryByTestId('processing-asset-unavailable')).toBeNull();
    cleanup();
    mock.source = { ...mock.source, assets: [] };
    render(<ModelProcessingPanel selection={selection} />);
    expect(screen.getByTestId('processing-asset-unavailable').querySelector('[data-testid="unavailable-label"]')!.textContent).toBe('无法使用，模型没有可用资产');
  });

  it('a chosen step with the confirm button grey names the reason in a few words (#94)', () => {
    source('tripo', 80_000_000);
    chooseUV();
    expect(screen.getByTestId('processing-plan-incompatible').querySelector('[data-testid="unavailable-label"]')!.textContent).toBe('无法使用，所选步骤没有输入路径');
    expect(screen.getByTestId('processing-plan-incompatible').querySelector('[data-testid="unavailable-detail"]')!.textContent).toBe(messages['ui.workspace.processing.plan_incompatible']);
  });

  it('the copy is the Schema\'s', () => {
    expect(messages['ui.unavailable.label']).toBe('无法使用，{reason}');
  });
});
