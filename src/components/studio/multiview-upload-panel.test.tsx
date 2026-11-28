// @vitest-environment jsdom
import { Component, type ReactNode } from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { MultiviewUploadPanel } from '@/components/studio/multiview-upload-panel';
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
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

/** Stands in for the chat region's error boundary: records what the real one would have replaced the chat with. */
class Boundary extends Component<{ caught: Error[]; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(error: Error) { this.props.caught.push(error); }
  render() { return this.state.failed ? <p data-testid="boundary-fallback" /> : this.props.children; }
}

beforeEach(() => {
  useStudioStore.setState({ stageImage: null });
  useChatStore.setState({ activeSessionId: 's1', messagesBySession: { s1: [] } });
  useWorkspaceStore.setState({ attachmentSources: {} });
  // React logs a caught render error; the assertions below are what count.
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('dismissing the multiview question keeps the hook order (#387)', () => {
  it('hides the panel without a render error and shows it again for different pictures', () => {
    const caught: Error[] = [];
    const onDismiss = vi.fn();
    const panel = (pictures: readonly string[]) => (
      <Boundary caught={caught}><MultiviewUploadPanel images={pictures} target={target} onConfirm={vi.fn()} onDismiss={onDismiss} /></Boundary>
    );
    const view = render(panel(images));
    expect(view.getByTestId('studio-multiview-upload')).toBeTruthy();

    // "收起" (#721; "不是" before): the panel returns null on the next render. Returning before the store hooks ran made React throw
    // #300 ("Rendered fewer hooks than expected") and the chat region fell back to its error boundary.
    fireEvent.click(view.getByTestId('studio-multiview-upload-dismiss'));
    expect(caught.map((error) => error.message)).toEqual([]);
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(view.queryByTestId('studio-multiview-upload')).toBeNull();

    // New pictures make the question relevant again; the reverse hook-count change (#310) must not throw either.
    view.rerender(panel([...images, '/api/v1/tasks/t1/assets/a3']));
    expect(caught.map((error) => error.message)).toEqual([]);
    expect(view.getByTestId('studio-multiview-upload')).toBeTruthy();
    expect(view.getAllByTestId('studio-multiview-upload-thumb')).toHaveLength(3);
  });
});

// #373: the panel makes the model it names even when the session's 3D pick takes no views; it says so in one line.
describe('the multiview panel note about the picked 3D model', () => {
  it('names the pick that cannot make several views and the model this one uses', () => {
    const view = render(<MultiviewUploadPanel images={images} target={target} lockNote={{ locked: 'Rodin Gen-2.5', model: 'Tripo P2 多视图' }} onConfirm={vi.fn()} />);
    const note = view.getByTestId('studio-multiview-upload-lock-note');
    expect(note.textContent).toBe(messages['ui.studio.multiview_upload.lock_note'].replace('{locked}', 'Rodin Gen-2.5').replace('{model}', 'Tripo P2 多视图'));
    expect(note.textContent).toContain('Rodin Gen-2.5');
    expect(note.textContent).toContain('Tripo P2 多视图');
  });

  // #670: each picture and its view picker are named in words, not by a bare number.
  it('names each picture and its view picker in words', () => {
    const messages = schema.content.messages as Record<string, string>;
    const view = render(<MultiviewUploadPanel images={images} target={target} onConfirm={vi.fn()} />);
    const second = (key: string) => messages[key].replace('{index}', '2');
    expect(view.getAllByRole('img').map((node) => node.getAttribute('alt'))[1]).toBe(second('ui.studio.multiview_upload.image_label'));
    expect(view.getAllByTestId('studio-multiview-upload-view')[1].getAttribute('aria-label')).toBe(second('ui.studio.multiview_upload.view_label'));
  });

  it('shows no note when the target is the pick or nothing is picked', () => {
    const same = render(<MultiviewUploadPanel images={images} target={target} lockNote={null} onConfirm={vi.fn()} />);
    expect(same.queryByTestId('studio-multiview-upload-lock-note')).toBeNull();
    same.unmount();
    const absent = render(<MultiviewUploadPanel images={images} target={target} onConfirm={vi.fn()} />);
    expect(absent.queryByTestId('studio-multiview-upload-lock-note')).toBeNull();
  });
});
