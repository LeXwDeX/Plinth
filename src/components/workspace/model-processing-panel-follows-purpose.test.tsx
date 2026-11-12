// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { ModelProcessingPanel } from './model-processing-panel';

// #292: the export texture size of a Tripo processing step follows the session purpose. The panel does not
// freeze its visible default (8K) as the person's choice: until they pick a size the field says it follows the
// purpose and the request leaves it out, so the server gives it the purpose's value. A size they pick is
// submitted as picked. The Offering is the shipped one, as the server projects it.

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;
const shipped = defaultCatalog.offerings.find((offering) => offering.id === 'tripo/processing/rigging-v3')!;
const rigging = {
  id: shipped.id, revision: shipped.revision, provider_id: 'tripo', provider_name: 'Tripo', model_id: 'tripo-rigging', model_name: 'Rigging',
  capability: 'rigging', available: true, estimated_credits: 35, credits_metered: true, parameters: shipped.parameters,
} as unknown as OfferingWire;

const mock = vi.hoisted(() => ({ fetch: vi.fn(), reload: vi.fn() }));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }),
}));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: 'session' }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ registerTask: vi.fn() }) }));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({ useModelProcessingCapabilities: () => ({
  available: [{ id: 'rigging', label_cn: '绑骨' }], offerings: { rigging: [rigging] }, error: undefined,
  refresh: async () => ({ rigging: [rigging] }),
}) }));
vi.mock('swr', () => ({ default: (key: string | unknown[]) => Array.isArray(key)
  ? { data: { id: 'source', session_id: 'session', capability: 'text-to-3d', provider_id: 'tripo', offering_id: 'tripo/tripo-v3.1-full/text-to-3d',
    state: 'SUCCEEDED', assets: [{ id: 'asset', kind: 'model', role: 'model', url: '/model.glb', size: 1000, mediaType: 'model/gltf-binary', variant: 'glb' }] } }
  : { data: [], mutate: mock.reload },
}));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));

const sizeLabel = shipped.parameters.find((parameter) => parameter.name === 'export_texture_size')!.label;

function renderPanel() {
  render(<ModelProcessingPanel selection={{ kind: 'model', taskId: 'source', assetId: 'asset', url: '/model.glb', sessionId: 'session' }} />);
  fireEvent.click(screen.getByRole('checkbox'));
}

/** Opens the size field and picks the option the way a person does; the dropdown highlights an option on hover before a click picks it. */
async function chooseSize(label: string) {
  fireEvent.click(screen.getByLabelText(sizeLabel));
  const option = await screen.findByRole('option', { name: label });
  fireEvent.mouseMove(option);
  fireEvent.click(option);
}

async function confirm(): Promise<{ steps: Array<{ offering_id: string; input: Record<string, unknown> }> }> {
  fireEvent.click(screen.getByRole('button', { name: new RegExp(messages['ui.workspace.processing.plan_confirm']) }));
  await waitFor(() => expect(mock.fetch).toHaveBeenCalledOnce());
  return JSON.parse(String(mock.fetch.mock.calls[0][1].body));
}

beforeEach(() => {
  vi.stubGlobal('React', React);
  mock.fetch.mockReset();
  mock.fetch.mockResolvedValue({ ok: true, json: async () => ({}) });
  mock.reload.mockReset();
  vi.stubGlobal('fetch', mock.fetch);
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('a step parameter that follows the session purpose (#292)', () => {
  it('is declared by the shipped Schema on the export size', () => {
    const size = shipped.parameters.find((parameter) => parameter.name === 'export_texture_size') as { presentation?: { follows_purpose?: boolean }; default?: string };
    expect(size.presentation?.follows_purpose).toBe(true);
    expect(size.default).toBe('8k');
  });

  it('shows the untouched field as following the purpose, not as the 8K default', () => {
    renderPanel();
    const trigger = screen.getByLabelText(sizeLabel);
    expect(trigger.textContent).toContain(messages['ui.studio.params.follows_purpose']);
    expect(trigger.textContent).not.toContain('8K');
  });

  it('leaves the untouched size out of the request so the server fills it from the purpose', async () => {
    renderPanel();
    const body = await confirm();
    expect(body.steps).toHaveLength(1);
    expect(body.steps[0].offering_id).toBe('tripo/processing/rigging-v3');
    expect(body.steps[0].input).not.toHaveProperty('export_texture_size');
    // The rest of the step is what the panel has always sent.
    expect(body.steps[0].input).toMatchObject({ rig_type: 'biped', spec: 'tripo' });
  });

  it('submits the size the person picked, even the default', async () => {
    renderPanel();
    for (const [label, value] of [['8K（8192 像素）', '8k'], ['2K（2048 像素）', '2k']] as const) {
      mock.fetch.mockClear();
      await chooseSize(label);
      await waitFor(() => expect(screen.getByLabelText(sizeLabel).textContent).toContain(label));
      const body = await confirm();
      expect(body.steps[0].input).toMatchObject({ export_texture_size: value });
    }
  });

  it('can be given back to the purpose after a pick', async () => {
    renderPanel();
    await chooseSize('4K（4096 像素）');
    await waitFor(() => expect(screen.getByLabelText(sizeLabel).textContent).toContain('4K'));
    await chooseSize(messages['ui.studio.params.follows_purpose']);
    await waitFor(() => expect(screen.getByLabelText(sizeLabel).textContent).toContain(messages['ui.studio.params.follows_purpose']));
    const body = await confirm();
    expect(body.steps[0].input).not.toHaveProperty('export_texture_size');
  });
});
