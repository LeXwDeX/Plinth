import { afterEach, describe, expect, it, vi } from 'vitest';

import catalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { ChoiceActionError, type ChoiceActionReason } from '@/lib/chat/choice-actions';
import { HarnessBrowserError, type HarnessSelection } from '@/lib/harness';
import type { ProductSchema } from '@/lib/product-schema';
import { applyChoiceAction } from './harness-choice-action';
import { harnessClearSelection } from './harness-panel';

const schema = catalog.application as unknown as ProductSchema;
const declaration = schema.chat.harness!;

// A session with a function fixed (the three-view-to-3D style case of #374) and a bounded enabled list.
const saved: HarnessSelection = {
  session_id: 's1', revision: 4, digest: 'd4', operation_id: 'remesh', offering_id: 'tripo/decimate', offering_revision: 'r1',
  enabled_operations: ['retexture', 'remesh', 'mesh-completion'], sources: [{ kind: 'task_asset', selection_mode: 'agent', parameter: '*' }],
  fixed_parameters: { target: 120 }, fillable_parameters: ['topology'], allow_prerequisites: false, updated_at: '2026-11-19T00:00:00Z',
};
const free: HarnessSelection = { ...saved, operation_id: undefined, offering_id: undefined, offering_revision: undefined, sources: [], fixed_parameters: {}, fillable_parameters: [] };

const service = (patch: Partial<OfferingWire> = {}) => ({
  id: 'meshy/texture', revision: 'r7', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'meshy-processing', model_name: 'Texture',
  capability: 'retexture', available: true,
  parameters: [
    { name: 'source_task', label: '来源任务', type: 'task', semantic: 'source-task' },
    { name: 'prompt', label: 'Prompt', type: 'text' },
  ],
  ...patch,
}) as unknown as OfferingWire;

const switchTo = { type: 'switch_operation', operationId: 'retexture' } as const;
const harness = (selection: HarnessSelection | null = saved) => ({ selection, save: vi.fn(async () => undefined) });
const reasonOf = async (promise: Promise<unknown>): Promise<ChoiceActionReason> => {
  const error = await promise.then(() => null, (reason: unknown) => reason);
  expect(error).toBeInstanceOf(ChoiceActionError);
  return (error as ChoiceActionError).reason;
};

afterEach(() => vi.unstubAllGlobals());

// #374: what the page saves in the session's Harness before the answer of a direction card option is sent.
describe('applyChoiceAction', () => {
  describe('clear_operation', () => {
    it('saves the 清空选择 selection of the 功能 panel: no function, service, source or parameter, the enabled list kept', async () => {
      const target = harness();
      await applyChoiceAction({ type: 'clear_operation' }, { schema, harness: target });
      expect(target.save).toHaveBeenCalledTimes(1);
      const [selection] = target.save.mock.calls[0] as unknown as [HarnessSelection];
      expect(selection).toEqual(harnessClearSelection(saved));
      expect(selection).toMatchObject({
        operation_id: undefined, offering_id: undefined, offering_revision: undefined, sources: [], fixed_parameters: {}, fillable_parameters: [],
        enabled_operations: ['retexture', 'remesh', 'mesh-completion'], allow_prerequisites: false, session_id: 's1',
      });
    });

    it('saves nothing when no function is selected, so answering again after a refused send is harmless', async () => {
      const target = harness(free);
      await applyChoiceAction({ type: 'clear_operation' }, { schema, harness: target });
      expect(target.save).not.toHaveBeenCalled();
    });
  });

  describe('switch_operation', () => {
    it('fixes the function with the panel helper and adopts the only available service, then saves once', async () => {
      const target = harness();
      const offerings = vi.fn(async () => [service(), service({ id: 'meshy/other', available: false }), service({ id: 'tripo/remesh', capability: 'remesh' })]);
      await applyChoiceAction(switchTo, { schema, harness: target, offerings });
      expect(offerings).toHaveBeenCalledWith('retexture');
      expect(target.save).toHaveBeenCalledTimes(1);
      const [selection] = target.save.mock.calls[0] as unknown as [HarnessSelection];
      expect(selection).toMatchObject({
        operation_id: 'retexture', offering_id: 'meshy/texture', offering_revision: 'r7',
        // The old function's source and parameters are gone; the Schema's default source mode applies to the new service.
        fixed_parameters: {}, fillable_parameters: ['prompt'], sources: [{ kind: 'task_asset', selection_mode: 'agent', parameter: '*' }],
        // The function joins the enabled list the person saved; the rest of it stays.
        enabled_operations: ['retexture', 'remesh', 'mesh-completion'], allow_prerequisites: false, session_id: 's1', revision: 4,
      });
    });

    it('adds the function to a bounded enabled list and starts a free session with the Schema defaults', async () => {
      const bounded = harness({ ...saved, enabled_operations: ['remesh'] });
      await applyChoiceAction(switchTo, { schema, harness: bounded, offerings: async () => [service()] });
      expect((bounded.save.mock.calls[0] as unknown as [HarnessSelection])[0].enabled_operations).toEqual(['remesh', 'retexture']);
      const fresh = harness({ ...free, enabled_operations: [] });
      await applyChoiceAction(switchTo, { schema, harness: fresh, offerings: async () => [service()] });
      const [selection] = fresh.save.mock.calls[0] as unknown as [HarnessSelection];
      expect(selection.enabled_operations).toEqual([...new Set([...declaration.default_enabled_operations, 'retexture'])]);
      expect(selection.allow_prerequisites).toBe(declaration.default_allow_prerequisites);
    });

    it('asks the public Offering list of the function capability by default', async () => {
      const fetcher = vi.fn(async () => new Response(JSON.stringify({ offerings: [service()] }), { status: 200 }));
      vi.stubGlobal('fetch', fetcher);
      const target = harness();
      await applyChoiceAction(switchTo, { schema, harness: target });
      expect(fetcher).toHaveBeenCalledWith(`${schema.runtime.http.routes.offerings}?capability=retexture`, expect.anything());
      expect(target.save).toHaveBeenCalledTimes(1);
    });

    it('refuses, saving nothing, when several services could run the function: the person picks one in the 功能 panel', async () => {
      const target = harness();
      const offerings = async () => [service(), service({ id: 'tripo/texture', revision: 'r2', model_id: 'tripo-texture' })];
      expect(await reasonOf(applyChoiceAction(switchTo, { schema, harness: target, offerings }))).toBe('service_choose');
      expect(target.save).not.toHaveBeenCalled();
    });

    it('refuses, saving nothing, when no service is available or the Offering list is empty', async () => {
      const target = harness();
      expect(await reasonOf(applyChoiceAction(switchTo, { schema, harness: target, offerings: async () => [service({ available: false })] }))).toBe('service_none');
      expect(await reasonOf(applyChoiceAction(switchTo, { schema, harness: target, offerings: async () => [] }))).toBe('service_none');
      expect(target.save).not.toHaveBeenCalled();
    });

    it('refuses a function the Schema does not list or declares unavailable, before asking for any service', async () => {
      const target = harness();
      const offerings = vi.fn(async () => [service()]);
      expect(await reasonOf(applyChoiceAction({ type: 'switch_operation', operationId: 'removed_function' }, { schema, harness: target, offerings }))).toBe('unavailable');
      const unavailable = structuredClone(schema);
      unavailable.chat.harness!.operations = unavailable.chat.harness!.operations.map((operation) => operation.id === 'retexture' ? { ...operation, unavailable_reason: 'not wired' } : operation);
      expect(await reasonOf(applyChoiceAction(switchTo, { schema: unavailable, harness: target, offerings }))).toBe('unavailable');
      const undeclared = structuredClone(schema);
      delete undeclared.chat.harness;
      expect(await reasonOf(applyChoiceAction(switchTo, { schema: undeclared, harness: target, offerings }))).toBe('unavailable');
      expect(offerings).not.toHaveBeenCalled();
      expect(target.save).not.toHaveBeenCalled();
    });

    it('saves nothing when that function and a service are already saved, e.g. after the person chose them in the panel', async () => {
      const target = harness({ ...saved, operation_id: 'retexture', offering_id: 'meshy/texture', offering_revision: 'r7' });
      const offerings = vi.fn(async () => [service()]);
      await applyChoiceAction(switchTo, { schema, harness: target, offerings });
      expect(offerings).not.toHaveBeenCalled();
      expect(target.save).not.toHaveBeenCalled();
    });
  });

  it('refuses while the session Harness is not loaded, since there is no revision to save against', async () => {
    const target = harness(null);
    expect(await reasonOf(applyChoiceAction({ type: 'clear_operation' }, { schema, harness: target }))).toBe('failed');
    expect(await reasonOf(applyChoiceAction(switchTo, { schema, harness: target, offerings: async () => [service()] }))).toBe('failed');
    expect(target.save).not.toHaveBeenCalled();
  });

  it('lets the failure of the save itself through, a revision conflict (409) included', async () => {
    const conflict = new HarnessBrowserError('error.harness_conflict', 409);
    const target = { selection: saved as HarnessSelection | null, save: vi.fn(async () => { throw conflict; }) };
    await expect(applyChoiceAction({ type: 'clear_operation' }, { schema, harness: target })).rejects.toBe(conflict);
    await expect(applyChoiceAction(switchTo, { schema, harness: target, offerings: async () => [service()] })).rejects.toBe(conflict);
    expect(target.save).toHaveBeenCalledTimes(2);
  });

  // #709: continue_on_canvas changes no function selection; it hands the person's previous words to a canvas turn.
  describe('continue_on_canvas', () => {
    const canvasAction = { type: 'continue_on_canvas' } as const;
    const canvas = (patch: Partial<{ hasBoard: boolean; originalWords: string }> = {}) => ({
      hasBoard: true, originalWords: '把这个角色做成三视图', openBoardMode: vi.fn(), ...patch,
    });

    it('returns the previous words for the canvas turn and saves no selection, even before the Harness loaded', async () => {
      const target = harness(null);
      const board = canvas();
      await expect(applyChoiceAction(canvasAction, { schema, harness: target, canvas: board }))
        .resolves.toEqual({ canvasTurnText: '把这个角色做成三视图' });
      expect(target.save).not.toHaveBeenCalled();
      expect(board.openBoardMode).not.toHaveBeenCalled();
    });

    it('refuses without a board frame, so nothing is sent as a plain turn', async () => {
      const board = canvas({ hasBoard: false });
      expect(await reasonOf(applyChoiceAction(canvasAction, { schema, harness: harness(), canvas: board }))).toBe('board_unavailable');
      expect(await reasonOf(applyChoiceAction(canvasAction, { schema, harness: harness() }))).toBe('board_unavailable');
      expect(board.openBoardMode).not.toHaveBeenCalled();
    });

    it('opens 以画板为主 when the previous words are gone, so typing and sending is the one step left', async () => {
      const board = canvas({ originalWords: '' });
      expect(await reasonOf(applyChoiceAction(canvasAction, { schema, harness: harness(), canvas: board }))).toBe('original_missing');
      expect(board.openBoardMode).toHaveBeenCalledTimes(1);
    });
  });
});
