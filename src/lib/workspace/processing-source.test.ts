import { describe, expect, it } from 'vitest';
import type { OfferingWire } from '@/lib/agent-offerings';
import { actionParameter, chooseProcessingSource, chooseProjectedProcessingSource, prerequisiteRefusal, processingPrerequisite, processingSourceSteps, sourceRefusals, inputImageParameter, isActionParameter, isSourceParameter, sourceParameterRole, consentField, panelFields, visibleOfferingFields, type ProcessingSourceTask } from './processing-source';

const asset = { id: 'asset-a', role: 'model', variant: 'glb', delivery: 'required', ordinal: 0, mediaType: 'model/gltf-binary', size: 100, url: '/assets/a', kind: 'model' as const };
const task: ProcessingSourceTask = { id: 'task-a', capability: 'text-to-3d', provider_id: 'provider-a', state: 'SUCCEEDED', assets: [asset] };
const offering = (parameters: OfferingWire['parameters']): OfferingWire => ({ id: 'processing', revision: '1', provider_id: 'provider-b', provider_name: 'B', model_id: 'model', model_name: 'Model', capability: 'remesh', available: true, parameters });

describe('chooseProcessingSource', () => {
  it('passes the owned local asset across providers', () => {
    expect(chooseProcessingSource(offering([{ name: 'source_asset', label: 'model', type: 'asset', max_bytes: 100 }]), task, asset)).toEqual({ sourceRole: 'source_asset', value: 'asset-a' });
  });
  it('rejects an incompatible asset and a cross-provider task reference', () => {
    expect(chooseProcessingSource(offering([
      { name: 'source_asset', label: 'model', type: 'asset', max_bytes: 99 },
      { name: 'source_task', label: 'task', type: 'task', same_provider: true },
    ]), task, asset)).toBeNull();
  });
  it('permits only the declared same-provider task capability', () => {
    const animation = { ...offering([{ name: 'source_task', label: 'task', type: 'task', same_provider: true, task_capabilities: ['rigging'] }]), provider_id: 'provider-a' };
    expect(chooseProcessingSource(animation, task, asset)).toBeNull();
    expect(chooseProcessingSource(animation, { ...task, capability: 'rigging' }, asset)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
  });
  it('prefers a declared same-provider task over its output asset', () => {
    const sameProvider = { ...offering([
      { name: 'source_asset', label: 'model', type: 'asset' },
      { name: 'source_task', label: 'task', type: 'task', same_provider: true, task_capabilities: ['text-to-3d'] },
    ]), provider_id: 'provider-a' };
    expect(chooseProcessingSource(sameProvider, task, asset)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
    expect(chooseProcessingSource(sameProvider, { ...task, provider_id: 'provider-c' }, asset)).toEqual({ sourceRole: 'source_asset', value: 'asset-a' });
  });
  it('never uses unfinished tasks or unsaved assets', () => {
    const remesh = offering([{ name: 'source_asset', label: 'model', type: 'asset' }]);
    expect(chooseProcessingSource(remesh, { ...task, state: 'MATERIALIZING' }, asset)).toBeNull();
    expect(chooseProcessingSource(remesh, task, { ...asset, id: '' })).toBeNull();
  });
  it('resolves semantically declared sources with arbitrary parameter names', () => {
    const renamed = offering([
      { name: 'input_model', label: 'model', type: 'asset', semantic: 'source-asset' },
      { name: 'input_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['text-to-3d'] },
    ]);
    expect(chooseProcessingSource({ ...renamed, provider_id: 'provider-a' }, task, asset)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
    expect(chooseProcessingSource(renamed, { ...task, provider_id: 'provider-c' }, asset)).toEqual({ sourceRole: 'source_asset', value: 'asset-a' });
  });
  it('declared non-source semantics override legacy name fallback', () => {
    expect(sourceParameterRole({ name: 'source_assets', label: '图片', type: 'asset-array', semantic: 'reference-image' })).toBeNull();
    expect(isSourceParameter({ name: 'source_assets', label: '图片', type: 'asset-array' })).toBe(true);
    expect(isSourceParameter({ name: 'topology', label: '拓扑', type: 'select' })).toBe(false);
  });
  it('locates the action parameter by semantic with name fallback', () => {
    const wire = { ...offering([]) };
    expect(actionParameter({ ...wire, parameters: [{ name: 'action_id', label: '动作', type: 'number', semantic: 'action' }] })?.name).toBe('action_id');
    expect(actionParameter({ ...wire, parameters: [{ name: 'action_id', label: '动作', type: 'number' }] })?.name).toBe('action_id');
    expect(actionParameter({ ...wire, parameters: [{ name: 'topology', label: '拓扑', type: 'select' }] })).toBeUndefined();
    expect(actionParameter({ ...wire, parameters: [{ name: 'action_id', label: '动作', type: 'number' }, { name: 'move', label: '动作', type: 'number', semantic: 'action' }] })?.name).toBe('move');
  });
});

describe('visibleOfferingFields', () => {
  it('hides source and action parameters from the generic parameter form', () => {
    const animation = offering([
      { name: 'input_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true },
      { name: 'action_id', label: 'action', type: 'number', semantic: 'action' },
      { name: 'quality', label: 'quality', type: 'select' },
    ]);
    expect(visibleOfferingFields(animation).map((parameter) => parameter.name)).toEqual(['quality']);
  });
  it('hides every declared action parameter, not only the first match', () => {
    expect(visibleOfferingFields(offering([
      { name: 'first_action', label: 'a', type: 'number', semantic: 'action' },
      { name: 'second_action', label: 'b', type: 'number', semantic: 'action' },
      { name: 'quality', label: 'q', type: 'select' },
    ])).map((parameter) => parameter.name)).toEqual(['quality']);
    expect(isActionParameter({ name: 'action_id', label: 'a', type: 'number' })).toBe(true);
    expect(isActionParameter({ name: 'action_id', label: 'a', type: 'number', semantic: 'reference-image' })).toBe(false);
  });
  it('hides the legacy action parameter by name fallback', () => {
    const legacy = offering([
      { name: 'source_asset', label: 'model', type: 'asset' },
      { name: 'action_id', label: 'action', type: 'number' },
    ]);
    expect(visibleOfferingFields(legacy).map((parameter) => parameter.name)).toEqual([]);
  });
  it('does not hide a declared non-source parameter that merely looks like a source', () => {
    expect(visibleOfferingFields(offering([
      { name: 'source_assets', label: '图片', type: 'asset-array', semantic: 'reference-image' },
      { name: 'expand', label: '扩展', type: 'number' },
    ])).map((parameter) => parameter.name)).toEqual(['source_assets', 'expand']);
  });
});

describe('inputImageParameter', () => {
  it('resolves the declared input image parameter with either asset arity', () => {
    expect(inputImageParameter(offering([
      { name: 'source_assets', label: '图片', type: 'asset-array', semantic: 'reference-image' },
    ]))?.name).toBe('source_assets');
    expect(inputImageParameter(offering([
      { name: 'input_image', label: '图片', type: 'asset', semantic: 'reference-image' },
    ]))?.name).toBe('input_image');
  });
  it('refuses to guess when the declaration is missing or ambiguous', () => {
    expect(inputImageParameter(offering([{ name: 'format', label: '格式', type: 'select' }]))).toBeUndefined();
    expect(inputImageParameter(offering([
      { name: 'first', label: '图', type: 'asset', semantic: 'reference-image' },
      { name: 'second', label: '图', type: 'asset', semantic: 'reference-image' },
    ]))).toBeUndefined();
  });
});

describe('inputImageParameter arity guard', () => {
  it('refuses offerings that require more than one input image', () => {
    expect(inputImageParameter(offering([
      { name: 'source_assets', label: '图片', type: 'asset-array', semantic: 'reference-image', min_items: 2 },
    ]))).toBeUndefined();
    expect(inputImageParameter(offering([
      { name: 'source_assets', label: '图片', type: 'asset-array', semantic: 'reference-image', min_items: 1 },
    ]))?.name).toBe('source_assets');
  });
});

describe('the extra-credit consent of the processing panel', () => {
  const hiddenConsent = { name: 'authorize_extra_credits', label: '同意额外积分消耗', type: 'boolean', presentation: { surface: 'hidden', control: 'boolean' } };
  const conversion = {
    ...offering([
      { name: 'source_asset', label: 'model', type: 'asset' },
      { name: 'quad', label: '四边面', type: 'boolean', presentation: { surface: 'provider', control: 'boolean' } },
      hiddenConsent,
    ]),
    extra_credit_consent_parameter: 'authorize_extra_credits',
  };

  it('keeps the flag out of the generic form, which the Agent and session settings share', () => {
    expect(visibleOfferingFields(conversion).map((parameter) => parameter.name)).toEqual(['quad']);
  });

  it('still gives the panel its own checkbox, so a direct submission is never silently charged', () => {
    expect(consentField(conversion)).toMatchObject({ name: 'authorize_extra_credits', label: '同意额外积分消耗', type: 'boolean' });
    expect(panelFields(conversion).map((parameter) => parameter.name)).toEqual(['quad', 'authorize_extra_credits']);
  });

  it('adds nothing for an Offering without a consent flag, and never twice', () => {
    expect(consentField(offering([{ name: 'quality', label: 'q', type: 'select' }]))).toBeNull();
    const shown = { ...conversion, parameters: conversion.parameters.map((parameter) => parameter.name === 'authorize_extra_credits'
      ? { ...parameter, presentation: { surface: 'provider', control: 'boolean' } } : parameter) };
    expect(panelFields(shown).filter((parameter) => parameter.name === 'authorize_extra_credits')).toHaveLength(1);
  });
});

describe('asset-backed task sources', () => {
  const gltf = { ...asset, id: 'converted', variant: 'gltf', mediaType: 'model/gltf+json' };
  const local: ProcessingSourceTask = { ...task, capability: 'format-conversion', offering_id: 'local/forward', assets: [gltf] };
  const reverse = offering([{ name: 'source_task', label: 'source', type: 'task', semantic: 'source-task', task_source_kind: 'assets', task_offerings: ['local/forward'], task_capabilities: ['format-conversion'], media_types: ['model/gltf+json'], extensions: ['.gltf'], max_bytes: 100 }]);
  it('permits the unique declared asset across providers without an upstream job', () => {
    expect(chooseProcessingSource(reverse, local, gltf)).toEqual({ sourceRole: 'source_task', value: local.id });
  });
  it.each(['offering', 'capability', 'provider', 'multiple', 'size', 'media', 'extension', 'chosen'] as const)('rejects an incompatible %s without falling back', (reason) => {
    let source = local;
    let selected = gltf;
    let target = reverse;
    if (reason === 'offering') source = { ...local, offering_id: 'unrelated' };
    if (reason === 'capability') source = { ...local, capability: 'text-to-3d' };
    if (reason === 'provider') target = { ...reverse, parameters: [{ ...reverse.parameters[0], same_provider: true }] };
    if (reason === 'multiple') source = { ...local, assets: [gltf, { ...gltf, id: 'another' }] };
    if (reason === 'size') source = { ...local, assets: [{ ...gltf, size: 101 }] };
    if (reason === 'media') source = { ...local, assets: [{ ...gltf, mediaType: 'model/gltf-binary' }] };
    if (reason === 'extension') target = { ...reverse, parameters: [{ ...reverse.parameters[0], extensions: ['.fbx'] }] };
    if (reason === 'chosen') selected = { ...gltf, id: 'another' };
    expect(chooseProcessingSource(target, source, selected)).toBeNull();
  });
  it('ignores unrelated output files while requiring the same unique model', () => {
    expect(chooseProcessingSource(reverse, { ...local, assets: [gltf, { ...asset, id: 'binary' }] }, gltf)).toEqual({ sourceRole: 'source_task', value: local.id });
  });
  it('keeps the existing upstream-job provider boundary', () => {
    expect(chooseProcessingSource({ ...reverse, parameters: [{ ...reverse.parameters[0], task_source_kind: 'upstream_job' }] }, local, gltf)).toBeNull();
  });
});

 it('keeps native-upload-only routes unavailable for Task assets', () => {
  const target = offering([{ name: 'source_asset', type: 'asset', label: 'upload', uploaded_model: {profile_id: 'static-glb-v1', native_only: true} }]);
  expect(chooseProcessingSource(target, task, asset)).toBeNull();
 });

describe('predicted processing sources use declarations, never invented saved bytes', () => {
  const forward: OfferingWire = { ...offering([{ name: 'source_asset', label: 'source', type: 'asset', media_types: ['model/gltf-binary'], max_bytes: 100 }]), id: 'local/forward', capability: 'format-conversion',
    outputs: [{ role: 'model', variant: 'gltf', minimum: 1, media_types: ['model/gltf+json'], max_bytes: 200 }] };
  const reverse: OfferingWire = { ...offering([{ name: 'saved_model', label: 'source', type: 'task', semantic: 'source-task', task_source_kind: 'assets', task_offerings: ['local/forward'], task_capabilities: ['format-conversion'], media_types: ['model/gltf+json'], extensions: ['.gltf'], max_bytes: 200 }]), provider_id: 'other', capability: 'reverse' };

  it('predicts the exact declared cross-provider assets input without a Task or asset value', () => {
    expect(chooseProjectedProcessingSource(reverse, forward)).toEqual({ sourceRole: 'source_task', parameter: 'saved_model' });
  });

  it.each(['offering', 'capability', 'provider', 'multiple', 'optional', 'minimum', 'dynamic', 'variant', 'media', 'mixed-media', 'budget', 'unbounded', 'no-output'] as const)('rejects the incompatible %s declaration', (reason) => {
    let source = structuredClone(forward);
    let target = structuredClone(reverse);
    const output = source.outputs![0] as Record<string, unknown>;
    if (reason === 'offering') source.id = 'unrelated';
    if (reason === 'capability') source.capability = 'rigging';
    if (reason === 'provider') target.parameters[0].same_provider = true;
    if (reason === 'multiple') source.outputs!.push({ ...output });
    if (reason === 'optional') output.delivery = 'optional';
    if (reason === 'minimum') output.minimum = 2;
    if (reason === 'dynamic') output.variant_from = 'format';
    if (reason === 'variant') output.variant = 'fbx';
    if (reason === 'media') output.media_types = ['model/gltf-binary'];
    if (reason === 'mixed-media') output.media_types = ['model/gltf+json', 'model/gltf-binary'];
    if (reason === 'budget') output.max_bytes = 201;
    if (reason === 'unbounded') delete output.max_bytes;
    if (reason === 'no-output') source.outputs = [];
    expect(chooseProjectedProcessingSource(target, source)).toBeNull();
  });

  it('preserves legacy same-provider tasks and asset MIME selection without imposing task-assets budgets', () => {
    const source = { ...forward, outputs: [{ role: 'model', media_types: ['model/gltf+json'] }, { role: 'model', media_types: ['model/gltf-binary'] }] };
    const legacy = { ...reverse, provider_id: source.provider_id, parameters: [{ ...reverse.parameters[0], task_source_kind: 'upstream_job' as const }] };
    expect(chooseProjectedProcessingSource(legacy, source)?.sourceRole).toBe('source_task');
    expect(chooseProjectedProcessingSource({ ...legacy, provider_id: 'different' }, source)).toBeNull();
    const assetTarget = offering([{ name: 'model', label: 'source', type: 'asset', semantic: 'source-asset', media_types: ['model/gltf-binary'] }]);
    expect(chooseProjectedProcessingSource(assetTarget, source)).toEqual({ sourceRole: 'source_asset', parameter: 'model' });
    expect(chooseProjectedProcessingSource({ ...assetTarget, parameters: [{ ...assetTarget.parameters[0], uploaded_model: { profile_id: 'static', native_only: true } }] }, source)).toBeNull();
  });

  it('uses real bytes only for the first step and declarations thereafter', () => {
    const steps = processingSourceSteps([{ id: 'format-conversion' }, { id: 'reverse' }], ['format-conversion', 'reverse'], { 'format-conversion': [forward], reverse: [reverse] }, task, asset);
    expect(steps.map((step) => step.route?.sourceRole)).toEqual(['source_asset', 'source_task']);
    expect(steps[1].route).not.toHaveProperty('value');
    expect(processingSourceSteps([{ id: 'format-conversion' }], ['format-conversion'], { 'format-conversion': [forward] }, { ...task, state: 'RUNNING' }, asset)[0].route).toBeNull();
  });

  it('defaults to the first compatible Offering, as the Agent plan tool does, and lists only compatible ones (#321)', () => {
    const incompatible = { ...reverse, id: 'first', parameters: [{ ...reverse.parameters[0], task_offerings: ['unrelated'] }] };
    const unavailable = { ...reverse, id: 'off', available: false };
    const second = { ...reverse, id: 'second' };
    const steps = processingSourceSteps([{ id: 'format-conversion' }, { id: 'reverse' }], ['format-conversion', 'reverse'], { 'format-conversion': [forward], reverse: [unavailable, incompatible, reverse, second] }, task, asset);
    expect(steps[1].offering.id).toBe(reverse.id);
    expect(steps[1].route).toEqual({ sourceRole: 'source_task', parameter: 'saved_model' });
    expect(steps[1].choices.map((choice) => choice.id)).toEqual([reverse.id, 'second']);
    expect(steps[0].choices.map((choice) => choice.id)).toEqual([forward.id]);
  });

  it('keeps the first available Offering with no route when none is compatible', () => {
    const incompatible = { ...reverse, id: 'first', parameters: [{ ...reverse.parameters[0], task_offerings: ['unrelated'] }] };
    const steps = processingSourceSteps([{ id: 'format-conversion' }, { id: 'reverse' }], ['format-conversion', 'reverse'], { 'format-conversion': [forward], reverse: [{ ...reverse, available: false }, incompatible] }, task, asset);
    expect(steps[1].offering.id).toBe('first');
    expect(steps[1].route).toBeNull();
    expect(steps[1].choices).toEqual([]);
  });

  it('honours an explicit choice and binds the next step to the chosen Offering', () => {
    // Two compatible first steps; only the second one's declared output feeds the next step.
    const plainForward = { ...forward, id: 'local/plain', outputs: [{ role: 'model', variant: 'glb', minimum: 1, media_types: ['model/gltf-binary'], max_bytes: 200 }] };
    const offerings = { 'format-conversion': [plainForward, forward], reverse: [reverse] };
    const capabilities = [{ id: 'format-conversion' }, { id: 'reverse' }];
    const byDefault = processingSourceSteps(capabilities, ['format-conversion', 'reverse'], offerings, task, asset);
    expect(byDefault[0].offering.id).toBe('local/plain');
    expect(byDefault[0].choices.map((choice) => choice.id)).toEqual(['local/plain', 'local/forward']);
    expect(byDefault[1].route).toBeNull();
    const chosen = processingSourceSteps(capabilities, ['format-conversion', 'reverse'], offerings, task, asset, { 'format-conversion': 'local/forward' });
    expect(chosen[0].offering.id).toBe('local/forward');
    expect(chosen[1].offering.id).toBe(reverse.id);
    expect(chosen[1].route).toEqual({ sourceRole: 'source_task', parameter: 'saved_model' });
  });

  it('falls back to the default when the chosen Offering is unavailable or no longer compatible', () => {
    const second = { ...forward, id: 'local/second' };
    const offerings = { 'format-conversion': [forward, { ...second, available: false }] };
    expect(processingSourceSteps([{ id: 'format-conversion' }], ['format-conversion'], offerings, task, asset, { 'format-conversion': 'local/second' })[0].offering.id).toBe(forward.id);
    expect(processingSourceSteps([{ id: 'format-conversion' }], ['format-conversion'], offerings, task, asset, { 'format-conversion': 'missing' })[0].offering.id).toBe(forward.id);
  });
});

describe('a step the current model cannot feed (#321 E)', () => {
  // A Rodin result over the asset limits: Meshy services accept up to 60 MB, or their own tasks.
  const rodin: ProcessingSourceTask = { ...task, provider_id: 'hyper3d', capability: 'text-to-3d' };
  const big = { ...asset, size: 110 * 1024 * 1024 };
  const meshyAsset = (id: string, capability: string): OfferingWire => ({
    ...offering([
      { name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['text-to-3d', 'image-to-3d', 'remesh', 'retexture'] },
      { name: 'source_asset', label: 'model', type: 'asset', semantic: 'source-asset', max_bytes: 62914560 },
    ]), id, capability, provider_id: 'meshy', provider_name: 'Meshy', model_name: id,
    outputs: [{ role: 'model', variant: 'glb', minimum: 1, media_types: ['model/gltf-binary'], max_bytes: 100 * 1024 * 1024 }],
  });
  const remesh = meshyAsset('meshy-remesh', 'remesh');
  const retexture = meshyAsset('meshy-retexture', 'retexture');
  const hyper3dTexture: OfferingWire = { ...offering([{ name: 'source_asset', label: 'model', type: 'asset', semantic: 'source-asset', max_bytes: 10485760, media_types: ['model/gltf-binary'] }]),
    id: 'hyper3d-texture', capability: 'retexture', provider_id: 'hyper3d', provider_name: 'Hyper3D', model_name: 'Texture' };
  const tripoTexture: OfferingWire = { ...offering([{ name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['text-to-3d', 'image-to-3d'], task_source_kind: 'upstream_job' }]),
    id: 'tripo-texture', capability: 'retexture', provider_id: 'tripo', provider_name: 'Tripo3D', model_name: 'Texture' };
  const offerings = { remesh: [remesh], retexture: [retexture, hyper3dTexture, tripoTexture] };
  const capabilities = [{ id: 'remesh' }, { id: 'retexture' }];

  it('does not bring in a prerequisite step that cannot run on the model either', () => {
    expect(processingPrerequisite('retexture', capabilities, offerings, rodin, big)).toBeNull();
  });

  it('still brings in a prerequisite that runs on the model and feeds the step', () => {
    const rigging: OfferingWire = { ...offering([{ name: 'source_asset', label: 'model', type: 'asset', semantic: 'source-asset', max_bytes: 200 }]), id: 'rig', capability: 'rigging', provider_id: 'meshy',
      outputs: [{ role: 'model', variant: 'glb', minimum: 1, media_types: ['model/gltf-binary'], max_bytes: 100 }] };
    const animation: OfferingWire = { ...offering([{ name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['rigging'] }]), id: 'anim', capability: 'animation', provider_id: 'meshy' };
    expect(processingPrerequisite('animation', [{ id: 'rigging' }, { id: 'animation' }], { rigging: [rigging], animation: [animation] }, task, asset)).toBe('rigging');
    // A step the model feeds directly needs none.
    expect(processingPrerequisite('rigging', [{ id: 'rigging' }, { id: 'animation' }], { rigging: [rigging], animation: [animation] }, task, asset)).toBeNull();
  });

  it('names why each available service refuses the model', () => {
    expect(sourceRefusals([retexture, hyper3dTexture, tripoTexture, { ...retexture, id: 'off', available: false }], rodin, big)).toEqual([
      { offering: retexture, kind: 'too_large', limit: 62914560 },
      { offering: hyper3dTexture, kind: 'too_large', limit: 10485760 },
      { offering: tripoTexture, kind: 'needs_task', capabilities: ['text-to-3d', 'image-to-3d'] },
    ]);
    expect(sourceRefusals([hyper3dTexture], rodin, { ...asset, mediaType: 'application/zip' })).toEqual([{ offering: hyper3dTexture, kind: 'media' }]);
  });

  // #326: an expired Meshy Rigging result whose rigged character (105 MB) is over Meshy Rigging's 60 MB: animating it
  // through "rig first" is not possible, so nothing is brought in, and the panel says why and what works.
  it('says why a prerequisite step cannot run on the model either', () => {
    const rigging: OfferingWire = { ...meshyAsset('meshy-rigging', 'rigging'),
      outputs: [{ role: 'model', variant: 'rigged_character_glb_url', minimum: 1, media_types: ['model/gltf-binary'], max_bytes: 100 }] };
    const animation: OfferingWire = { ...offering([{ name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['rigging'], upstream_job_max_age_seconds: 172800 }]),
      id: 'meshy-animation', capability: 'animation', provider_id: 'meshy' };
    const rig: ProcessingSourceTask = { ...task, provider_id: 'meshy', capability: 'rigging', created_at: '2026-10-13T00:00:00Z' };
    const character = { ...asset, size: 105_271_888 };
    const all = { rigging: [rigging], animation: [animation] };
    const caps = [{ id: 'rigging' }, { id: 'animation' }];
    expect(processingPrerequisite('animation', caps, all, rig, character)).toBeNull();
    expect(prerequisiteRefusal('animation', caps, all, rig, character)).toEqual({
      capability: 'rigging', refusals: [{ offering: rigging, kind: 'too_large', limit: 62914560 }],
    });
    // A rigged character that fits is rigged again first, and there is nothing to explain.
    const small = { ...asset, size: 30_000_000 };
    expect(processingPrerequisite('animation', caps, all, rig, small)).toBe('rigging');
    expect(prerequisiteRefusal('animation', caps, all, rig, small)).toBeNull();
  });

  it('does not judge a step after one that cannot run by the first step\'s fallback', () => {
    const steps = processingSourceSteps(capabilities, ['remesh', 'retexture'], offerings, rodin, big);
    expect(steps[0].route).toBeNull();
    expect(steps[1].route).toBeNull();
    expect(steps[1].choices).toEqual([]);
    expect(steps[1].blocked).toBe(true);
    expect(steps[0].blocked).toBeFalsy();
  });
});

// #325: the Provider keeps its own task only for the Schema's declared age; past it the saved file goes instead.
describe('an expired original task', () => {
  const now = Date.parse('2026-10-22T12:00:00Z');
  const created = (hours: number) => new Date(now - hours * 3600 * 1000).toISOString();
  const retexture = { ...offering([
    { name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['text-to-3d', 'retexture'], upstream_job_max_age_seconds: 172800 },
    { name: 'source_asset', label: 'model', type: 'asset', semantic: 'source-asset', max_bytes: 1000 },
  ]), provider_id: 'provider-a', capability: 'retexture' };
  const animation = { ...offering([
    { name: 'source_task', label: 'task', type: 'task', semantic: 'source-task', same_provider: true, task_capabilities: ['rigging'], upstream_job_max_age_seconds: 172800 },
  ]), provider_id: 'provider-a', capability: 'animation' };

  it('keeps the original task while it is fresh', () => {
    expect(chooseProcessingSource(retexture, { ...task, created_at: created(47) }, asset, now)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
    expect(chooseProcessingSource(retexture, task, asset, now)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
  });
  it('uses the saved file once the original task is past its age, for generation and processing results alike', () => {
    for (const capability of ['text-to-3d', 'retexture']) {
      expect(chooseProcessingSource(retexture, { ...task, capability, created_at: created(48) }, asset, now))
        .toEqual({ sourceRole: 'source_asset', value: 'asset-a', taskExpired: true });
    }
  });
  // #326 N1: a recovered result's job is its failed source's, and a job the provider answered 404 for is gone.
  it('counts the age from the task that holds the job, and treats a gone job as expired', () => {
    const recovered = { ...task, created_at: created(30), upstream_job_created_at: created(60) };
    expect(chooseProcessingSource(retexture, recovered, asset, now)).toEqual({ sourceRole: 'source_asset', value: 'asset-a', taskExpired: true });
    const gone = { ...task, created_at: created(1), upstream_job_gone: true };
    expect(chooseProcessingSource(retexture, gone, asset, now)).toEqual({ sourceRole: 'source_asset', value: 'asset-a', taskExpired: true });
    expect(chooseProcessingSource(animation, { ...gone, capability: 'rigging' }, asset, now)).toBeNull();
  });
  it('refuses a service that takes only the original task and says the task expired', () => {
    const rig = { ...task, capability: 'rigging', created_at: created(120) };
    expect(chooseProcessingSource(animation, rig, asset, now)).toBeNull();
    expect(sourceRefusals([animation], rig, asset, now)).toEqual([{ offering: animation, kind: 'expired' }]);
    expect(chooseProcessingSource(animation, { ...rig, created_at: created(1) }, asset, now)).toEqual({ sourceRole: 'source_task', value: 'task-a' });
  });
});
