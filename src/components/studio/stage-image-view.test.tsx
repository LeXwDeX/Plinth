// @vitest-environment jsdom
import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { MultiviewUploadPanel } from '@/components/studio/multiview-upload-panel';
import { StageImageView } from '@/components/studio/stage-image-view';
import type { MultiviewOffering } from '@/lib/workspace/multiview-upload';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

vi.hoisted(() => {
  const memory = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, value); },
      removeItem: (key: string) => { memory.delete(key); }, clear: () => memory.clear(),
    },
  });
});
const schema = defaultCatalog.application;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

const target = {
  offering: { model_name: 'Tripo H3' },
  views: [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }, { value: 'back', label: '背面' }, { value: 'right', label: '右侧' }],
  minPictures: 2,
  maxPictures: 4,
} as unknown as MultiviewOffering;
const images = ['/api/v1/tasks/t1/assets/a1', '/api/v1/tasks/t1/assets/a2'];

beforeEach(() => {
  useStudioStore.setState({ stageImage: null });
  useChatStore.setState({
    activeSessionId: 's1',
    messagesBySession: { s1: [{ id: 'm', role: 'assistant', content: '', createdAt: 1, attachments: [{ handle: '@img26', kind: 'image', taskId: 't1', assetId: 'a2' }] }] },
  });
  useWorkspaceStore.setState({ attachmentSources: {} });
});
afterEach(cleanup);

describe('a view-confirmation thumbnail opens the picture in the stage area (#87)', () => {
  it('opens the clicked picture with its chat handle, inside the stage and not full screen', () => {
    const view = render(<div><MultiviewUploadPanel images={images} target={target} onConfirm={vi.fn()} /><StageImageView /></div>);
    const thumbs = view.getAllByTestId('studio-multiview-upload-thumb');
    expect(thumbs).toHaveLength(2);
    expect(thumbs[1].getAttribute('aria-label')).toBe('在舞台上查看第 2 张图');
    fireEvent.click(thumbs[1]);
    const modal = view.getByTestId('stage-image-view');
    expect(modal.className).toContain('absolute inset-0');
    expect(view.getByTestId('stage-image-picture').getAttribute('src')).toBe(images[1]);
    expect(view.getByTestId('stage-image-label').textContent).toBe('第 2 张图');
    expect(view.getByTestId('stage-image-handle').textContent).toBe('img26');
    // The first picture has no handle in the messages: none is shown.
    fireEvent.click(thumbs[0]);
    expect(view.queryByTestId('stage-image-handle')).toBeNull();
  });

  it('closes on Esc before the stage sees it, and on a press outside the picture', () => {
    const view = render(<StageImageView />);
    act(() => { useStudioStore.getState().openStageImage({ url: images[0], label: 'x' }); });
    const stageKey = vi.fn();
    window.addEventListener('keydown', stageKey);
    fireEvent.keyDown(window, { key: 'Escape' });
    window.removeEventListener('keydown', stageKey);
    expect(stageKey).not.toHaveBeenCalled();
    expect(view.queryByTestId('stage-image-view')).toBeNull();

    act(() => { useStudioStore.getState().openStageImage({ url: images[0], label: 'x' }); });
    fireEvent.pointerDown(view.getByTestId('stage-image-picture'));
    expect(view.queryByTestId('stage-image-view')).not.toBeNull();
    fireEvent.pointerDown(view.getByTestId('stage-image-view'));
    expect(view.queryByTestId('stage-image-view')).toBeNull();
  });

  it('closes when the session changes', () => {
    const view = render(<StageImageView />);
    act(() => { useStudioStore.getState().openStageImage({ url: images[0], label: 'x' }); });
    act(() => { useChatStore.setState({ activeSessionId: 's2' }); });
    expect(view.queryByTestId('stage-image-view')).toBeNull();
  });
});
