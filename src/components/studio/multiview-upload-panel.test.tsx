// @vitest-environment jsdom
import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { MultiviewUploadPanel } from '@/components/studio/multiview-upload-panel';
import type { MultiviewOffering } from '@/lib/workspace/multiview-upload';

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
  offering: { model_name: 'Tripo P2 多视图' },
  views: [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }],
  minPictures: 2,
  maxPictures: 4,
} as unknown as MultiviewOffering;
const images = ['/api/v1/assets/a1', '/api/v1/assets/a2'];
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

afterEach(cleanup);

// #373: the panel makes the model it names even when the session's 3D pick takes no views; it says so in one line.
describe('the multiview panel note about the picked 3D model', () => {
  it('names the pick that cannot make several views and the model this one uses', () => {
    const view = render(<MultiviewUploadPanel images={images} target={target} lockNote={{ locked: 'Rodin Gen-2.5', model: 'Tripo P2 多视图' }} onConfirm={vi.fn()} />);
    const note = view.getByTestId('studio-multiview-upload-lock-note');
    expect(note.textContent).toBe(messages['ui.studio.multiview_upload.lock_note'].replace('{locked}', 'Rodin Gen-2.5').replace('{model}', 'Tripo P2 多视图'));
    expect(note.textContent).toContain('Rodin Gen-2.5');
    expect(note.textContent).toContain('Tripo P2 多视图');
  });

  it('shows no note when the target is the pick or nothing is picked', () => {
    const same = render(<MultiviewUploadPanel images={images} target={target} lockNote={null} onConfirm={vi.fn()} />);
    expect(same.queryByTestId('studio-multiview-upload-lock-note')).toBeNull();
    same.unmount();
    const absent = render(<MultiviewUploadPanel images={images} target={target} onConfirm={vi.fn()} />);
    expect(absent.queryByTestId('studio-multiview-upload-lock-note')).toBeNull();
  });
});
