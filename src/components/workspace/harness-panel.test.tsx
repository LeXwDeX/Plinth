import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { HarnessSelection, HarnessSelectionInput, HarnessView } from '@/lib/harness';
import type { ProductSchema } from '@/lib/product-schema';
import { HarnessPanel, harnessAutoSelectOffering, harnessClearSelection, harnessConversationFields, harnessOfferingSelection, harnessOperationSelection, harnessOperationsForModel, harnessParameterFields, harnessSelectionInput } from './harness-panel';

const mock = vi.hoisted(() => ({ draft: null as HarnessSelectionInput | null, view: null as HarnessView | null, owner: 'owner', busy: false, error: null as unknown, hook: vi.fn(), save: vi.fn(), offerings: {} as Record<string, OfferingWire[]>, tiles: [] as unknown[] }));
const schema = catalog.application as unknown as ProductSchema;
let activeSchema = schema;
// Seed a dirty editor draft without adding a DOM renderer dependency. The real
// component still computes its actual radio, Offering and save disabled props.
vi.mock('react', async importOriginal => {
  const actual = await importOriginal<typeof import('react')>();
  return { ...actual, useState: ((initial: unknown) => {
    const state = actual.useState(initial);
    return typeof initial === 'function' && mock.draft && state[0] && typeof state[0] === 'object' && 'enabled_operations' in state[0] ? [mock.draft, state[1]] : state;
  }) as typeof actual.useState };
});
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: activeSchema, text: (key: string) => key }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: mock.owner ? { id: mock.owner } : null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: (session: string | null, owner: string) => { mock.hook(session, owner); return { view: mock.view, busy: mock.busy, error: mock.error, reload: vi.fn(), save: mock.save }; } }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles: mock.tiles, isLoading: false }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ modelByDomain: { image: 'unrelated', 'model-3d': 'unrelated' } }) }));
vi.mock('swr', () => ({ default: () => ({ data: mock.offerings, mutate: vi.fn() }) }));
vi.mock('@/components/ui/option-select', () => ({ OptionSelect: ({ value, options, placeholder, ...props }: { value: string; options: Array<{ value: string; label: string; disabled?: boolean }>; placeholder?: string }) => <select disabled={(props as { disabled?: boolean }).disabled} data-placeholder={placeholder} value={value} aria-label={props['aria-label' as keyof typeof props] as string} onChange={() => {}}>{options.map(option => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}</select> }));

const offering: OfferingWire = {
  id: 'private-offering-id', revision: 'r1', provider_id: 'provider', provider_name: 'Public service', model_id: 'model', model_name: 'Public model', capability: 'remesh', available: true,
  parameters: [
    { name: 'source_task', label: '来源任务', type: 'task', semantic: 'source-task' },
    { name: 'source_asset', label: '来源文件', type: 'asset', semantic: 'source-asset' },
    { name: 'topology', label: 'Topology', type: 'select', options: [{ value: 'quad', label: 'Quad' }], default: 'quad' },
    { name: 'target', label: 'Target', type: 'number', default: 100 },
    { name: 'hidden', label: 'Hidden', type: 'text', presentation: { surface: 'hidden' } },
    { name: 'fixed', label: 'Fixed', type: 'text', fixed: true, default: 'fixed' },
    { name: 'reference', label: 'Reference', type: 'asset', semantic: 'reference-image' },
    { name: 'mask', label: 'Mask', type: 'text', semantic: 'mask' },
    { name: 'consent', label: 'Consent', type: 'boolean' },
  ], extra_credit_consent_parameter: 'consent',
};
const selection: HarnessSelection = { session_id: 's1', revision: 2, digest: 'd2', operation_id: 'remesh', offering_id: offering.id, offering_revision: offering.revision, enabled_operations: ['remesh'], sources: [], fixed_parameters: { target: 120 }, fillable_parameters: ['topology'], allow_prerequisites: true, updated_at: '' };
const render = () => renderToStaticMarkup(<HarnessPanel sessionId="s1" object={null} />);
beforeEach(() => { activeSchema = schema; mock.draft = null; mock.owner = 'owner'; mock.busy = false; mock.error = null; mock.hook.mockClear(); mock.save.mockReset(); mock.tiles = []; mock.offerings = { remesh: [offering] }; mock.view = { selection, status: 'needs_input', reason: '', missing_parameters: ['source_task'], can_propose: false, estimated_credits: 0 }; });

describe('Harness panel', () => {
  it('uses the English singular estimated credits for one credit (#363)', () => {
    const english = structuredClone(catalog.application) as unknown as ProductSchema;
    english.localization = { ...english.localization!, active_locale: 'en' };
    english.content.messages = {
      ...english.content.messages,
      'ui.harness.estimated_credits': 'Estimated {credits} credits',
      'ui.harness.estimated_credits_one': 'Estimated {credits} credit',
    };
    activeSchema = english;
    mock.view = { ...mock.view!, estimated_credits: 1 };
    mock.offerings = { remesh: [{ ...offering, credits_metered: true }] };

    const html = render();
    expect(html).toContain('Estimated 1 credit');
    expect(html).not.toContain('Estimated 1 credits');
  });

  it('starts a first animation selection with the Schema allowed prerequisites, without saving or submitting work', () => {
    const draft = harnessOperationSelection({ ...selection, operation_id: undefined, enabled_operations: [] }, 'animation', schema.chat.harness!.default_enabled_operations, schema.chat.harness!.default_allow_prerequisites);
    const payload = harnessSelectionInput({ ...draft, allow_prerequisites: true }, undefined);
    expect(payload.enabled_operations).toEqual(expect.arrayContaining(['animation', 'rigging']));
    expect(payload.enabled_operations).toEqual([...new Set([...schema.chat.harness!.default_enabled_operations, 'animation'])]);
    expect(payload.allow_prerequisites).toBe(true);
    expect(mock.save).not.toHaveBeenCalled();
    const bounded = harnessOperationSelection({ ...selection, enabled_operations: ['remesh'] }, 'animation', schema.chat.harness!.default_enabled_operations, schema.chat.harness!.default_allow_prerequisites);
    expect(bounded.enabled_operations).toEqual(['remesh', 'animation']);
  });

  // #327 (#324 note 99119): 清空选择 rewrote enabled_operations — it saved the draft's list, which
  // had the cleared operation added, and the next pick replaced the list with the Schema defaults,
  // dropping mesh-completion.
  it('clears only the selected operation, service, sources and parameters, keeping the saved enabled list', () => {
    const saved = { ...selection, enabled_operations: ['mesh-completion', 'remesh'] };
    const draft = harnessOperationSelection(saved, 'animation', schema.chat.harness!.default_enabled_operations, true);
    expect(draft.enabled_operations).toContain('animation');
    const cleared = harnessClearSelection(saved);
    expect(cleared).toMatchObject({ operation_id: undefined, offering_id: undefined, offering_revision: undefined, sources: [], fixed_parameters: {}, fillable_parameters: [] });
    expect(cleared.enabled_operations).toEqual(['mesh-completion', 'remesh']);
  });

  it('keeps the enabled list a cleared selection saved when a new goal is picked', () => {
    const afterClear = { ...selection, operation_id: undefined, offering_id: undefined, enabled_operations: ['mesh-completion', 'remesh'], allow_prerequisites: false };
    const picked = harnessOperationSelection(afterClear, 'animation', schema.chat.harness!.default_enabled_operations, true);
    expect(picked.enabled_operations).toEqual(['mesh-completion', 'remesh', 'animation']);
  });

  it.each([true, false])('uses the Schema prerequisite default %s only on the first explicit goal', defaultAllow => {
    const free = { ...selection, operation_id: undefined, enabled_operations: [], allow_prerequisites: false };
    expect(harnessOperationSelection(free, 'animation', ['animation', 'rigging'], defaultAllow).allow_prerequisites).toBe(defaultAllow);
    const active = { ...selection, allow_prerequisites: !defaultAllow };
    expect(harnessOperationSelection(active, 'animation', ['animation', 'rigging'], defaultAllow).allow_prerequisites).toBe(!defaultAllow);
    expect(free.allow_prerequisites).toBe(false);
  });

  it('keeps common operations independent of the generation-model picker', () => {
    const operations = [{ id: 'common', label: '', description: '', capability: 'remesh', group: 'common' as const, model_ids: ['other'] }, { id: 'specific', label: '', description: '', capability: 'remesh', group: 'model' as const, model_ids: ['other'] }];
    expect(harnessOperationsForModel(operations, 'common', ['picked']).map(operation => operation.id)).toEqual(['common']);
    expect(harnessOperationsForModel(operations, 'model', ['picked'])).toEqual([]);
  });
  it('reuses editable public fields and removes references, uploads, fixed values and consent', () => {
    expect(harnessParameterFields(offering).map(field => field.name)).toEqual(['topology', 'target']);
    const input = harnessSelectionInput({ ...selection, fixed_parameters: { target: 120, hidden: 'secret', source_task: 'foreign' }, fillable_parameters: ['topology', 'consent'] }, offering);
    expect(input.fixed_parameters).toEqual({ target: 120 });
    expect(input.fillable_parameters).toEqual(['topology']);
  });
  it('names the reference images that come from the conversation instead of leaving them unfillable (#187 D)', () => {
    expect(harnessConversationFields(offering).map(field => field.name)).toEqual(['reference']);
    const raw = catalog.offerings.find(row => row.id === 'hyper3d/texture/local-model')!;
    const texture = { ...raw, available: true } as unknown as OfferingWire;
    const current = { ...selection, operation_id: 'retexture', offering_id: texture.id, offering_revision: texture.revision, enabled_operations: ['retexture'], fixed_parameters: {}, fillable_parameters: ['prompt'], allow_prerequisites: false };
    mock.view = { ...mock.view!, selection: current, status: 'ready', missing_parameters: [] };
    mock.offerings = { retexture: [texture] };
    const html = render();
    expect(html).toContain('ui.harness.conversation_input_required');
    expect(html).toContain('ui.harness.conversation_input_help');
    expect(harnessSelectionInput(current, texture).fixed_parameters).toEqual({});
    expect(mock.save).not.toHaveBeenCalled();
  });
  it('binds the request to the logged-in owner and session without submitting any work', () => {
    const html = render();
    expect(mock.hook).toHaveBeenCalledWith('s1', 'owner');
    expect(html).toContain('通用功能');
    expect(html).toContain(schema.chat.harness!.groups.find(group => group.id === 'model')!.empty_message);
    expect(html).toContain('ui.harness.save_note');
    expect(mock.save).not.toHaveBeenCalled();
  });
  it('preserves an unavailable selection and offers recovery without exposing private identifiers as copy', () => {
    mock.view = { ...mock.view!, selection: { ...selection, offering_revision: 'retired' }, status: 'unavailable' };
    const html = render();
    expect(html).toContain('ui.harness.selection_unavailable');
    expect(html).toContain('ui.harness.status_unavailable');
    expect(html).not.toContain('retired');
    expect(mock.save).not.toHaveBeenCalled();
  });
  it('only lists completed model tasks as sources', () => {
    mock.tiles = [
      { taskId: 'ready-task', origin: 'task', domain: 'model', status: 'ready', version: 1, assets: [{ kind: 'model' }] },
      { taskId: 'pending-task', origin: 'task', domain: 'model', status: 'running', version: 2, assets: [{ kind: 'model' }] },
      { taskId: 'image-task', origin: 'task', domain: 'image', status: 'ready', version: 3, assets: [{ kind: 'image' }] },
      { taskId: 'upload', origin: 'upload', domain: 'model', status: 'ready', version: 4, assets: [{ kind: 'model' }] },
    ];
    const html = render();
    expect(html).toContain('value="ready-task"');
    expect(html).not.toContain('pending-task');
    expect(html).not.toContain('image-task');
    expect(html).not.toContain('value="upload"');
  });
  it('does not open an editor without a logged-in owner', () => {
    mock.owner = '';
    expect(render()).toContain('ui.harness.sign_in');
    expect(mock.hook).toHaveBeenCalledWith('s1', '');
    expect(mock.save).not.toHaveBeenCalled();
  });
  it('keeps every qualified common radio selectable in the actual published Schema while Repair stays disabled', () => {
    const html = render();
    for (const id of ['uv-unwrap', 'resize', 'analyze-printability', 'multi-color-print']) {
      const radio = html.match(new RegExp(`<input[^>]*type="radio"[^>]*value="${id}"[^>]*>`))?.[0];
      expect(radio).toBeDefined();
      expect(radio).not.toContain('disabled');
    }
    expect(html.match(/<input[^>]*type="radio"[^>]*value="repair-printability"[^>]*>/)?.[0]).toContain('disabled');
    expect(mock.save).not.toHaveBeenCalled();
  });

  it.each(['uv-unwrap', 'resize', 'analyze-printability', 'multi-color-print'])('allows a dirty available %s draft but blocks unavailable runtime save without submitting', capability => {
    const raw = catalog.offerings.find(row => row.capability === capability)!;
    const candidate = { ...raw, available: true } as unknown as OfferingWire;
    const current = { ...selection, operation_id: capability, offering_id: candidate.id, offering_revision: candidate.revision, enabled_operations: [capability], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false };
    mock.view = { ...mock.view!, selection: current, status: 'ready' };
    mock.draft = { ...current, allow_prerequisites: true };
    mock.offerings = { [capability]: [candidate] };
    const saveButton = (html: string) => html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g)?.find(button => button.includes('ui.harness.save'))?.split('>')[0];
    const availableHTML = render();
    expect(saveButton(availableHTML)).toBeDefined();
    expect(saveButton(availableHTML)).not.toMatch(/\sdisabled(?:=|(?=\s|$))/);
    mock.offerings = { [capability]: [{ ...candidate, available: false }] };
    const unavailableHTML = render();
    expect(unavailableHTML).toContain('ui.harness.not_connected');
    expect(unavailableHTML.match(new RegExp(`<option[^>]*value="${candidate.id}"[^>]*>`))?.[0]).toContain('disabled');
    expect(saveButton(unavailableHTML)).toMatch(/\sdisabled(?:=|(?=\s|$))/);
    expect(mock.save).not.toHaveBeenCalled();
  });

});

describe('Rig Check operation boundary', () => {
  it('is selectable as an available Schema operation, keeps the upload limitation, and neither saves nor executes on render', () => {
    const op=schema.chat.harness!.operations.find(op=>op.id==='rig-check')!;
    expect(op.capability).toBe('rig-check');expect(op.unavailable_reason).toBeFalsy();expect(op.uploaded_source_unavailable_reason).toBeTruthy();
    const html=render();expect(html).toContain(op.label);expect(html).toContain(op.description);
    expect(html.match(/<input[^>]*type="radio"[^>]*value="rig-check"[^>]*>/)?.[0]).not.toContain('disabled');
    expect(mock.save).not.toHaveBeenCalled();
  });
});


describe('Unique service selection', () => {
  const operation = schema.chat.harness!.operations.find(item => item.id === 'remesh')!;
  const empty = () => ({ ...selection, offering_id: undefined, offering_revision: undefined, fixed_parameters: {}, fillable_parameters: [] });
  it('adopts one available service using the manual parameter defaults without persistence', () => {
    const current = empty();
    const selected = harnessAutoSelectOffering(current, operation, [offering, { ...offering, id: 'offline', available: false }]);
    expect(selected).toEqual(harnessOfferingSelection(current, offering));
    expect(selected.fillable_parameters).toEqual(['topology', 'target']);
    expect(current.offering_id).toBeUndefined();
    expect(mock.save).not.toHaveBeenCalled();
  });
  it.each([{ choices: [] }, { choices: [{ ...offering, available: false }] }, { choices: [offering, { ...offering, id: 'second' }] }])('keeps zero or multiple available services unresolved', ({ choices }) => {
    const current = empty();
    expect(harnessAutoSelectOffering(current, operation, choices)).toBe(current);
  });
  it.each([{ offering_id: 'saved', offering_revision: 'retired' }, { offering_id: 'saved', offering_revision: undefined }, { offering_id: undefined, offering_revision: 'retired' }])('never replaces a saved or partial identity %s', identity => {
    const current = { ...empty(), ...identity };
    expect(harnessAutoSelectOffering(current, operation, [offering])).toBe(current);
  });
  it('rechecks busy, selected operation and runtime availability when delayed discovery arrives', () => {
    const current = empty();
    expect(harnessAutoSelectOffering(current, operation, [offering], true)).toBe(current);
    expect(harnessAutoSelectOffering(current, { ...operation, id: 'changed' }, [offering])).toBe(current);
    expect(harnessAutoSelectOffering(current, { ...operation, unavailable_reason: 'offline' }, [offering])).toBe(current);
    expect(harnessAutoSelectOffering(current, undefined, [offering])).toBe(current);
    expect(harnessAutoSelectOffering(current, operation, [offering], false).offering_id).toBe(offering.id);
  });
  it('only considers the operation capability and allowed models', () => {
    const current = empty();
    const bounded = { ...operation, model_ids: ['allowed'] };
    expect(harnessAutoSelectOffering(current, bounded, [offering])).toBe(current);
    expect(harnessAutoSelectOffering(current, operation, [{ ...offering, capability: 'wrong' }])).toBe(current);
    expect(harnessAutoSelectOffering(current, bounded, [offering, { ...offering, id: 'allowed', model_id: 'allowed' }]).offering_id).toBe('allowed');
  });
  it('reserves feedback space in both clean and dirty drafts', () => {
    expect(render()).toContain('class="min-h-5 space-y-1" data-testid="harness-feedback"');
    mock.draft = { ...selection, allow_prerequisites: false };
    expect(render()).toContain('class="min-h-5 space-y-1" data-testid="harness-feedback"');
    expect(render()).toContain('ui.harness.unsaved');
  });
});
