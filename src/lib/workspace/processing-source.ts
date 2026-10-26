import { offeringParamSchemas, type OfferingWire } from '@/lib/agent-offerings';
import type { ParamSchema } from '@/lib/providers/types';
import type { TrackedAsset } from '@/lib/workspace/task-projection';

export interface ProcessingSourceTask {
  id: string;
  session_id?: string;
  capability: string;
  provider_id: string;
  offering_id?: string;
  state: string;
  assets: TrackedAsset[];
  /** When the Task was created (RFC 3339); the age of its upstream job counts from here (#325). */
  created_at?: string;
  /** When the Task holding its upstream job was created, when that is another one (a recovered result, #326). */
  upstream_job_created_at?: string;
  /** A later step naming this Task's upstream job failed because the provider no longer keeps it (#326). */
  upstream_job_gone?: boolean;
}

/** `sourceRole` 是参数承担的来源角色（不是真实参数名）：真实参数名由
 * Offering 声明，消费方按语义定位。 */
export type ProcessingSource =
  | { sourceRole: 'source_asset'; value: string; /** The original task is past its age, so the saved file goes instead (#325). */ taskExpired?: true }
  | { sourceRole: 'source_task'; value: string };

export type SourceParameter = OfferingWire['parameters'][number];
export type SourceParameterRole = 'task' | 'asset' | 'url';

/**
 * 来源参数角色判定：Schema semantic 声明优先，遗留参数名回退（B7 mask
 * 同款方法论）。已声明的非来源语义（如 reference-image）不会被名称回退
 * 覆盖。
 */
export function sourceParameterRole(parameter: SourceParameter): SourceParameterRole | null {
  if (parameter.semantic) {
    if (parameter.semantic === 'source-task') return 'task';
    if (parameter.semantic === 'source-asset') return 'asset';
    if (parameter.semantic === 'source-url') return 'url';
    return null;
  }
  if (parameter.name === 'source_task' && parameter.type === 'task') return 'task';
  if (parameter.name === 'source_asset' && parameter.type === 'asset') return 'asset';
  if (parameter.name === 'source_assets') return 'asset';
  if (parameter.name === 'source_url' && parameter.type === 'text') return 'url';
  return null;
}

export function isSourceParameter(parameter: SourceParameter): boolean {
  return sourceParameterRole(parameter) !== null;
}

/** 动作参数判定：semantic=action 优先，遗留名 action_id 回退。 */
export function isActionParameter(parameter: SourceParameter): boolean {
  return parameter.semantic === 'action'
    || (!parameter.semantic && parameter.name === 'action_id');
}

/** 动作参数定位：semantic=action 优先，遗留名 action_id 回退（catalog
 * 保证同一 Offering 至多一个 action 语义参数）。 */
export function actionParameter(offering: OfferingWire): SourceParameter | undefined {
  return offering.parameters.find((parameter) => parameter.semantic === 'action')
    ?? offering.parameters.find((parameter) => !parameter.semantic && parameter.name === 'action_id');
}

/**
 * Mirrors Go generation.UpstreamJobExpired (#325, #326): an upstream-job task parameter no longer reaches the
 * Provider's job once a step naming it failed as source_expired, or once the Task holding the job is older than the
 * declared upstream_job_max_age_seconds.
 * A Task without a creation time never counts as expired here; the server refuses it if it is.
 */
export function upstreamJobExpired(parameter: SourceParameter,
  task: Pick<ProcessingSourceTask, 'created_at' | 'upstream_job_created_at' | 'upstream_job_gone'>, now: number = Date.now()): boolean {
  if (parameter.type !== 'task' || (parameter.task_source_kind && parameter.task_source_kind !== 'upstream_job')) return false;
  if (task.upstream_job_gone) return true;
  const maxAge = parameter.upstream_job_max_age_seconds ?? 0;
  const from = task.upstream_job_created_at || task.created_at;
  if (maxAge <= 0 || !from) return false;
  const created = Date.parse(from);
  return Number.isFinite(created) && now - created >= maxAge * 1000;
}

/**
 * Only a locally saved model or a valid same-provider task can feed a processing Offering. Mirrors Go
 * generation.BindModelSource: a same-provider task wins while the Provider still keeps it; past the
 * declared age the saved file goes instead (#325), marked taskExpired so the panel can say why.
 */
export function chooseProcessingSource(
  offering: OfferingWire,
  task: ProcessingSourceTask,
  asset: TrackedAsset,
  now: number = Date.now(),
): ProcessingSource | null {
  if (!offering.available || task.state !== 'SUCCEEDED' || asset.kind !== 'model' || !asset.id || !asset.url) return null;
  const taskParameter = offering.parameters.find((parameter) => sourceParameterRole(parameter) === 'task' && parameter.type === 'task');
  let taskExpired = false;
  if (taskParameter
    && (!taskParameter.task_capabilities?.length || taskParameter.task_capabilities.includes(task.capability))
    && (!taskParameter.task_offerings?.length || Boolean(task.offering_id && taskParameter.task_offerings.includes(task.offering_id)))) {
    if (taskParameter.task_source_kind === 'assets') {
      // Match the server's unique stored-model selector using public metadata.
      // A chosen asset never hides ambiguity among other compatible outputs.
      const candidates = task.assets.filter((candidate) => candidate.role === 'model' && candidate.kind === 'model'
        && candidate.id && candidate.url && candidate.size > 0
        && Boolean(taskParameter.max_bytes && candidate.size <= taskParameter.max_bytes)
        && Boolean(taskParameter.media_types?.includes(candidate.mediaType))
        && Boolean(taskParameter.extensions?.some((extension) => extension.replace(/^\./, '').toLowerCase() === candidate.variant.toLowerCase())));
      if ((!taskParameter.same_provider || task.provider_id === offering.provider_id)
        && candidates.length === 1 && candidates[0].id === asset.id) {
        return { sourceRole: 'source_task', value: task.id };
      }
    } else if ((!taskParameter.task_source_kind || taskParameter.task_source_kind === 'upstream_job')
      && task.provider_id === offering.provider_id) {
      if (!upstreamJobExpired(taskParameter, task, now)) return { sourceRole: 'source_task', value: task.id };
      taskExpired = true;
    }
  }
  const assetParameter = offering.parameters.find((parameter) => sourceParameterRole(parameter) === 'asset' && parameter.type === 'asset');
  if (assetParameter && !assetParameter.uploaded_model?.native_only && (!assetParameter.max_bytes || asset.size <= assetParameter.max_bytes)
    && (!assetParameter.media_types?.length || assetParameter.media_types.includes(asset.mediaType))) {
    return taskExpired ? { sourceRole: 'source_asset', value: asset.id, taskExpired: true } : { sourceRole: 'source_asset', value: asset.id };
  }
  return null;
}

/** A declaration-only dependency route, with no saved Task, asset or byte proof. */
export interface ProjectedProcessingSource {
  sourceRole: ProcessingSource['sourceRole'];
  parameter: string;
}

/** Mirrors the kernel's declaration-only binder. Actual source bytes are
 * checked again when each step is prepared; predictions carry no object ID. */
export function chooseProjectedProcessingSource(target: OfferingWire, source: OfferingWire): ProjectedProcessingSource | null {
  if (!target.available) return null;
  const outputs = (source.outputs ?? []).filter((output): output is Record<string, unknown> =>
    output !== null && typeof output === 'object' && !Array.isArray(output));
  for (const parameter of target.parameters) {
    if (sourceParameterRole(parameter) !== 'task' || parameter.type !== 'task'
      || (parameter.task_capabilities?.length && !parameter.task_capabilities.includes(source.capability))
      || (parameter.task_offerings?.length && !parameter.task_offerings.includes(source.id))) continue;
    if (parameter.task_source_kind === 'assets') {
      if (!source.id || (parameter.same_provider && source.provider_id !== target.provider_id)
        || !projectedTaskAssetCompatible(parameter, outputs)) continue;
    } else if ((!parameter.task_source_kind || parameter.task_source_kind === 'upstream_job')
      && source.provider_id === target.provider_id) {
      // Legacy tasks pass the provider job, not a predicted saved asset.
    } else continue;
    return { sourceRole: 'source_task', parameter: parameter.name };
  }
  for (const parameter of target.parameters) {
    if (sourceParameterRole(parameter) !== 'asset' || parameter.type !== 'asset' || parameter.uploaded_model?.native_only) continue;
    if (outputs.some((output) => output.role === 'model' && (!parameter.media_types?.length
      || (Array.isArray(output.media_types) && output.media_types.some((type: unknown) => typeof type === 'string'
        && parameter.media_types!.some((allowed) => allowed === type || (allowed.endsWith('/*') && type.startsWith(allowed.slice(0, -1))))))))) {
      return { sourceRole: 'source_asset', parameter: parameter.name };
    }
  }
  return null;
}

function projectedTaskAssetCompatible(parameter: SourceParameter, outputs: Record<string, unknown>[]): boolean {
  if (!parameter.max_bytes || parameter.max_bytes <= 0 || !parameter.media_types?.length || !parameter.extensions?.length) return false;
  const models = outputs.filter((output) => output.role === 'model');
  if (models.length !== 1) return false;
  const [model] = models;
  return (!model.delivery || model.delivery === 'required') && (model.minimum === undefined || model.minimum === 1)
    && typeof model.variant === 'string' && Boolean(model.variant) && !model.variant_from && !model.variant_prefix
    && typeof model.max_bytes === 'number' && model.max_bytes > 0 && model.max_bytes <= parameter.max_bytes
    && parameter.extensions.some((extension) => extension.replace(/^\./, '').toLowerCase() === (model.variant as string).toLowerCase())
    && Array.isArray(model.media_types) && model.media_types.length > 0
    && model.media_types.every((type: unknown) => typeof type === 'string' && type !== '' && parameter.media_types!.includes(type));
}

export interface ProcessingSourceStep {
  capability: string;
  offering: OfferingWire;
  route: ProcessingSource | ProjectedProcessingSource | null;
  /** The available Offerings, in server order, that accept this step's source; the person picks among them. */
  choices: OfferingWire[];
  /** An earlier step cannot run, so this one cannot start either; it is not judged by that step's fallback (#321 E). */
  blocked?: boolean;
}

/**
 * Each chosen step's service (#321). Candidates are the available Offerings in the server's
 * order (Schema priority, then recommended, then ID); only those that accept the step's source
 * can be chosen. The first step binds the real source; later steps bind the previous step's
 * chosen Offering by declaration. The default is the first compatible Offering, the rule the
 * Agent plan tool uses; a chosen ID that is no longer available or compatible falls back to it.
 * With no compatible Offering the first available one is kept without a route, so the panel
 * states why instead of silently changing service.
 */
export function processingSourceSteps(
  capabilities: readonly { id: string }[],
  selected: readonly string[],
  offerings: Record<string, OfferingWire[]> | undefined,
  source: ProcessingSourceTask,
  asset: TrackedAsset,
  chosen: Readonly<Record<string, string>> = {},
): ProcessingSourceStep[] {
  const steps: ProcessingSourceStep[] = [];
  let previous: OfferingWire | undefined;
  let blocked = false;
  for (const capability of capabilities.filter((entry) => selected.includes(entry.id))) {
    const available = offerings?.[capability.id]?.filter((choice) => choice.available) ?? [];
    if (available.length === 0) continue;
    if (blocked) {
      steps.push({ capability: capability.id, offering: available[0], route: null, choices: [], blocked: true });
      continue;
    }
    const bound = available.map((offering) => ({
      offering,
      route: previous ? chooseProjectedProcessingSource(offering, previous) : chooseProcessingSource(offering, source, asset),
    }));
    const compatible = bound.filter((entry) => entry.route !== null);
    const pick = compatible.find((entry) => entry.offering.id === chosen[capability.id]) ?? compatible[0] ?? { offering: available[0], route: null };
    steps.push({ capability: capability.id, offering: pick.offering, route: pick.route, choices: compatible.map((entry) => entry.offering) });
    previous = pick.offering;
    blocked = pick.route === null;
  }
  return steps;
}

/**
 * The step to bring in with capabilityID when the current model cannot feed it directly (animation
 * needs a rigged source, say). Only a step that can itself run on the model and whose output feeds
 * the chosen step is brought in; a prerequisite that cannot run either would leave the panel stuck
 * with a disabled confirm (#321 E), so then there is none and the step says why instead.
 */
export function processingPrerequisite(
  capabilityID: string,
  capabilities: readonly { id: string }[],
  offerings: Record<string, OfferingWire[]> | undefined,
  source: ProcessingSourceTask,
  asset: TrackedAsset,
): string | null {
  const available = offerings?.[capabilityID]?.filter((choice) => choice.available) ?? [];
  if (available.length === 0 || available.some((choice) => chooseProcessingSource(choice, source, asset))) return null;
  const wanted = new Set(available.flatMap((choice) => choice.parameters
    .filter((parameter) => sourceParameterRole(parameter) === 'task')
    .flatMap((parameter) => parameter.task_capabilities ?? [])));
  for (const candidate of capabilities) {
    if (candidate.id === capabilityID || !wanted.has(candidate.id)) continue;
    const runs = (offerings?.[candidate.id] ?? []).filter((choice) => choice.available && chooseProcessingSource(choice, source, asset));
    if (runs.some((first) => available.some((step) => chooseProjectedProcessingSource(step, first)))) return candidate.id;
  }
  return null;
}

/** A step that would have to come first (rigging before animation) and why none of its services runs on the model (#326). */
export interface PrerequisiteRefusal {
  capability: string;
  refusals: SourceRefusal[];
}

/**
 * Why the step that would come first cannot be brought in (#326, the #321 E rule): capabilityID cannot take the
 * model directly, and the step whose results it takes (rigging, for animation) has services whose output feeds it,
 * but none of them runs on the model either (an expired rigged character over Meshy Rigging's 73.4 MB, say). Null when
 * the step takes the model, a prerequisite runs (processingPrerequisite brings it in), or no step would feed it.
 * Mirrors the Go Agent tools' refusal (generationtool noServiceText), so both say the same.
 */
export function prerequisiteRefusal(
  capabilityID: string,
  capabilities: readonly { id: string }[],
  offerings: Record<string, OfferingWire[]> | undefined,
  source: ProcessingSourceTask,
  asset: TrackedAsset,
  now: number = Date.now(),
): PrerequisiteRefusal | null {
  const available = offerings?.[capabilityID]?.filter((choice) => choice.available) ?? [];
  if (available.length === 0 || available.some((choice) => chooseProcessingSource(choice, source, asset, now))
    || processingPrerequisite(capabilityID, capabilities, offerings, source, asset)) return null;
  const wanted = new Set(available.flatMap((choice) => choice.parameters
    .filter((parameter) => sourceParameterRole(parameter) === 'task')
    .flatMap((parameter) => parameter.task_capabilities ?? [])));
  for (const candidate of capabilities) {
    if (candidate.id === capabilityID || !wanted.has(candidate.id)) continue;
    const feeders = (offerings?.[candidate.id] ?? []).filter((choice) => choice.available
      && available.some((step) => chooseProjectedProcessingSource(step, choice)));
    if (feeders.length > 0) return { capability: candidate.id, refusals: sourceRefusals(feeders, source, asset, now) };
  }
  return null;
}

/**
 * Why an available service does not accept the model: over its size limit, a file type it does not take, only a task of
 * its own kind, or (#325) only its own original task, which the Provider no longer keeps.
 */
export type SourceRefusal =
  | { offering: OfferingWire; kind: 'too_large'; limit: number }
  | { offering: OfferingWire; kind: 'media' }
  | { offering: OfferingWire; kind: 'needs_task'; capabilities: string[] }
  | { offering: OfferingWire; kind: 'expired' };

/** The reasons, per available service that does not accept the model, in server order (#321 E). */
export function sourceRefusals(offerings: readonly OfferingWire[], task: ProcessingSourceTask, asset: TrackedAsset, now: number = Date.now()): SourceRefusal[] {
  const refusals: SourceRefusal[] = [];
  for (const offering of offerings) {
    if (!offering.available || chooseProcessingSource(offering, task, asset, now)) continue;
    const assetParameter = offering.parameters.find((parameter) => sourceParameterRole(parameter) === 'asset' && parameter.type === 'asset'
      && !parameter.uploaded_model?.native_only);
    const expiredTask = offering.parameters.find((parameter) => sourceParameterRole(parameter) === 'task' && parameter.type === 'task'
      && (!parameter.task_capabilities?.length || parameter.task_capabilities.includes(task.capability))
      && task.provider_id === offering.provider_id && upstreamJobExpired(parameter, task, now));
    if (expiredTask && !assetParameter) {
      refusals.push({ offering, kind: 'expired' });
    } else if (assetParameter && assetParameter.media_types?.length && !assetParameter.media_types.includes(asset.mediaType)) {
      refusals.push({ offering, kind: 'media' });
    } else if (assetParameter?.max_bytes && asset.size > assetParameter.max_bytes) {
      refusals.push({ offering, kind: 'too_large', limit: assetParameter.max_bytes });
    } else {
      const taskParameter = offering.parameters.find((parameter) => sourceParameterRole(parameter) === 'task');
      refusals.push({ offering, kind: 'needs_task', capabilities: taskParameter?.task_capabilities ?? [] });
    }
  }
  return refusals;
}

/** 2D 能力的输入图参数定位：semantic=reference-image 的唯一资产参数，
 * 与 matting adapter 的 mattingAssetParameter 同一真源（零个或多个候选
 * 返回 undefined，不按名字猜）。要求恰好一张输入图的 Offering 才能由本对话框提交。 */
export function inputImageParameter(offering: OfferingWire): SourceParameter | undefined {
  const candidates = offering.parameters.filter((parameter) => parameter.semantic === 'reference-image'
    && (parameter.type === 'asset' || parameter.type === 'asset-array'));
  if (candidates.length !== 1) return undefined;
  const [parameter] = candidates;
  if (parameter.type === 'asset-array' && (parameter.min_items ?? 1) > 1) return undefined;
  return parameter;
}

/** 通用参数表单的可见字段：来源参数由处理计划注入、动作参数由专用分组
 * 控件承担，两者都不得在通用表单里二次渲染（否则提交值被静默覆盖）。 */
export function visibleOfferingFields(offering: OfferingWire): ParamSchema[] {
  const hidden = new Set(
    offering.parameters
      .filter((parameter) => isSourceParameter(parameter) || isActionParameter(parameter))
      .map((parameter) => parameter.name),
  );
  return offeringParamSchemas(offering).filter((parameter) => !hidden.has(parameter.name));
}

/**
 * The person's own consent to extra credits, for the panel that submits a step
 * directly. The Schema hides the flag from every generic form (the Agent and the
 * session settings never see it; in chat the approval card is the consent), but a
 * step submitted from this panel has no card, so the panel keeps its own checkbox
 * for it. Null when the Offering has no such flag.
 */
export function consentField(offering: OfferingWire): ParamSchema | null {
  const name = offering.extra_credit_consent_parameter;
  const wire = name ? offering.parameters.find((parameter) => parameter.name === name) : undefined;
  if (!wire) return null;
  const shown: OfferingWire = {
    ...offering,
    parameters: [{ ...wire, presentation: { ...wire.presentation, surface: 'provider', control: 'boolean' } }],
  };
  return offeringParamSchemas(shown)[0] ?? null;
}

/** The fields of the processing panel: the generic ones plus the panel's own consent checkbox. */
export function panelFields(offering: OfferingWire): ParamSchema[] {
  const fields = visibleOfferingFields(offering);
  const consent = consentField(offering);
  return consent && !fields.some((field) => field.name === consent.name) ? [...fields, consent] : fields;
}

/**
 * The 加工 panel's source row (#327): the provider and the file's public format ("meshy · GLB"); without a known format
 * its media type, then the step. The Provider's own result field (rigged_character_glb_url) is never shown.
 */
export function processingSourceLabel(source: Pick<ProcessingSourceTask, 'provider_id' | 'capability'>, asset: Pick<TrackedAsset, 'format' | 'mediaType'> | null): string {
  return `${source.provider_id} · ${asset?.format || asset?.mediaType || source.capability}`;
}
