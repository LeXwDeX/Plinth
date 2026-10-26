/**
 * The facts shown in the shared 任务详情 (ResultDetail): which rows, in which order, and which
 * actions a given result offers. Pure functions over the public task wire, the offering that ran
 * it and the Product Schema; no component state. All wording is Product Schema text.
 */

import { offeringShowsCredits, type OfferingParameterWire, type OfferingWire } from '@/lib/agent-offerings';
import { formatSchemaText, schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { findTaskState, projectTaskAssets, type TrackedAsset } from '@/lib/workspace/task-projection';
import { ratioOfSize } from '@/lib/workspace/generation-params';
import type { AgentScope } from '@/store/chat-store';
import { primaryParent, type VersionTiles } from '@/lib/workspace/versions';
import { versionLabel } from '@/lib/workspace/version-label';
import { formatNativePrice, parseNativeQuote, type NativeQuote } from '@/lib/pricing';
import { inputDerivationText, parseInputDerivations, type InputDerivation } from '@/lib/workspace/input-derivation';

/** GET /api/v1/tasks/{id}. `input` and `credits_charged` are the owner-scoped projection of the frozen input and the settled cost; older servers omit them. */
export interface TaskDetailWire {
  id: string;
  sessionId: string;
  capability: string;
  offeringId: string;
  providerId: string;
  modelId: string;
  state: string;
  progress: number;
  failure: { detail: string; retryable: boolean } | null;
  createdAt: string;
  finishedAt: string;
  prompt: string;
  parameters: Record<string, unknown>;
  creditsCharged: number | null;
  nativeQuote?: NativeQuote | null;
  recoverySource: { sourceTaskId: string; sourceAttemptId: string; sourceOfferingId: string; sourceOfferingRevision: string } | null;
  /** Owner-scoped: reference images sent as a re-encoded copy to fit the Offering (#276); older servers omit it. */
  inputDerivations: InputDerivation[];
  assetCount: number;
  /** Owner-scoped public artifacts, including files that have no media preview. */
  assets: TrackedAsset[];
}

const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const isRecord = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);

export function parseTaskDetail(raw: unknown): TaskDetailWire | null {
  if (!isRecord(raw) || !str(raw.id)) return null;
  const input = isRecord(raw.input) ? raw.input : {};
  const parameters = isRecord(input.parameters) ? input.parameters : {};
  const failure = isRecord(raw.failure) ? { detail: str(raw.failure.detail), retryable: raw.failure.retryable === true } : null;
  return {
    id: str(raw.id),
    sessionId: str(raw.session_id),
    capability: str(raw.capability),
    offeringId: str(raw.offering_id),
    providerId: str(raw.provider_id),
    modelId: str(raw.model_id),
    state: str(raw.state),
    progress: typeof raw.progress === 'number' ? raw.progress : 0,
    failure,
    createdAt: str(raw.created_at),
    finishedAt: str(raw.finished_at),
    prompt: str(input.prompt) || str(parameters.prompt),
    parameters,
    creditsCharged: typeof raw.credits_charged === 'number' && Number.isFinite(raw.credits_charged) ? raw.credits_charged : null,
    ...(Object.hasOwn(raw, 'native_quote') ? { nativeQuote: parseNativeQuote(raw.native_quote) } : {}),
    recoverySource: isRecord(raw.recovery_source) && str(raw.recovery_source.source_task_id)
      ? {
        sourceTaskId: str(raw.recovery_source.source_task_id),
        sourceAttemptId: str(raw.recovery_source.source_attempt_id),
        sourceOfferingId: str(raw.recovery_source.source_offering_id),
        sourceOfferingRevision: str(raw.recovery_source.source_offering_revision),
      }
      : null,
    inputDerivations: parseInputDerivations(raw.input_derivations),
    assets: projectTaskAssets(raw.assets),
    assetCount: Array.isArray(raw.assets) ? raw.assets.filter((asset) => isRecord(asset) && str(asset.media_type).startsWith('image/')).length : 0,
  };
}

export interface DetailRow {
  key: string;
  label: string;
  value: string;
  parentKey?: string;
}

export interface DetailFacts {
  /** The task as the server reports it; null for an uploaded asset or before the answer arrives. */
  task: TaskDetailWire | null;
  offering?: OfferingWire | undefined;
  /** Prompt known on this side (task tracker, the card that was clicked) when the server's answer has none. */
  fallbackPrompt?: string;
  /** Measured pixels of the loaded picture, when the parameters name no size. */
  measured?: { width: number; height: number } | null;
  /** Capability to name when the server has not answered yet. */
  capability?: string;
  source?: DetailSource;
}

export interface DetailSource { label: string; parentKey: string }

/** Same recorded parent used by the stage header, also retained for file-only tasks. Unloaded and unknown parents stay unknown. */
export function detailSource(schema: ProductSchema, taskId: string, versions: VersionTiles): DetailSource {
  const key = versions.byTask.get(taskId);
  const tile = key ? versions.tiles.find((entry) => entry.key === key) : undefined;
  const parent = primaryParent(tile ?? { parents: versions.fileTaskParents.get(taskId) ?? [] });
  const source = parent?.known ? versions.tiles.find((entry) => entry.key === parent.key && entry.status === 'ready') : undefined;
  return source ? { label: formatSchemaText(schemaText(schema, 'ui.result.detail.made_from'), { parent: versionLabel(schema, source, source.domain) }), parentKey: source.key }
    : { label: schemaText(schema, 'ui.result.detail.unknown_source'), parentKey: '' };
}

const field = (schema: ProductSchema, name: string): string => schemaText(schema, `ui.result.detail.field.${name}`);

/** A task parameter that is about the request text or its inputs, never a "main parameter". */
const NON_DISPLAY_SEMANTICS = new Set(['prompt', 'negative-prompt', 'reference-image', 'source-asset', 'source-task', 'source-url', 'source-name', 'mask', 'action', 'task-id']);
const NON_DISPLAY_TYPES = new Set(['asset', 'asset-array']);
const SIZE_NAMES = new Set(['size', 'resolution']);
const RATIO_NAMES = new Set(['aspect_ratio', 'ratio']);
const COUNT_NAMES = new Set(['n', 'max_images', 'count']);

function optionLabel(param: OfferingParameterWire, value: unknown): string {
  const text = String(value);
  return param.options?.find((option) => String(option.value) === text)?.label ?? text;
}

function valueText(schema: ProductSchema, param: OfferingParameterWire, value: unknown): string {
  // A boolean with declared options (a two-choice mode) reads as the chosen option's label.
  if (typeof value === 'boolean' && param.options?.some((option) => option.value === String(value))) return optionLabel(param, value);
  if (typeof value === 'boolean') return schemaText(schema, value ? 'ui.result.detail.yes' : 'ui.result.detail.no');
  if (Array.isArray(value)) {
    // A frozen task keeps the values it was submitted with. An option the current Offering no
    // longer declares (a withdrawn format such as BLEND) is left out instead of shown raw.
    const declared = param.options?.length
      ? value.filter((item) => param.options?.some((option) => String(option.value) === String(item)))
      : value;
    return declared.map((item) => optionLabel(param, item)).join(schemaText(schema, 'ui.studio.list_separator'));
  }
  if (isRecord(value)) return '';
  return optionLabel(param, value);
}

function shownParameters(offering: OfferingWire | undefined): OfferingParameterWire[] {
  return (offering?.parameters ?? []).filter((param) => param.presentation?.surface !== 'hidden'
    && !NON_DISPLAY_TYPES.has(param.type) && !(param.semantic && NON_DISPLAY_SEMANTICS.has(param.semantic)));
}

function formatTime(schema: ProductSchema, iso: string): string {
  const parsed = Date.parse(iso);
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString(schema.runtime.frontend.display_locale) : '';
}

export function formatDuration(schema: ProductSchema, startIso: string, endIso: string): string {
  const start = Date.parse(startIso);
  const end = Date.parse(endIso);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '';
  const total = Math.round((end - start) / 1000);
  const minutes = Math.floor(total / 60);
  return minutes > 0
    ? formatSchemaText(schemaText(schema, 'ui.result.detail.duration_minutes'), { minutes, seconds: total % 60 })
    : formatSchemaText(schemaText(schema, 'ui.result.detail.duration_seconds'), { seconds: total });
}

/** What the credits row says: the amount only when the offering is credits-metered and the server reports one; otherwise the Schema's "not applicable" mark. */
export function creditsText(schema: ProductSchema, offering: OfferingWire | undefined, charged: number | null): string {
  if (offering && offeringShowsCredits(offering) && charged !== null) {
    return schemaCountMessage(schema, 'ui.studio.approval.card.credits_value', charged, { credits: charged });
  }
  return schemaText(schema, 'ui.result.detail.not_applicable');
}

export function detailPrompt(facts: Pick<DetailFacts, 'task' | 'fallbackPrompt'>): string {
  return (facts.task?.prompt || facts.fallbackPrompt || '').trim();
}

/**
 * Rows in reading order: type, model, size, ratio, count, the offering's other declared
 * parameters, status, credits, times. A row whose value is unknown is left out. The credits
 * row exists only for a credits-metered offering (Meshy, Tripo, ...); there it reads "/" until
 * the server reports an estimate. Unmetered providers (GPT, Gemini images) have no credits row.
 */
export function buildDetailRows(schema: ProductSchema, facts: DetailFacts): DetailRow[] {
  const { task, offering } = facts;
  const rows: DetailRow[] = [];
  const add = (key: string, value: string) => { if (value) rows.push({ key, label: field(schema, key), value }); };
  const capabilityId = task?.capability || facts.capability || offering?.capability || '';
  const capability = schema.capabilities.find((item) => item.id === capabilityId);
  add('type', capability ? (capability.label_cn || capability.label) : capabilityId);
  if (task) {
    const source = facts.source;
    rows.push({ key: 'source', label: field(schema, 'source'), value: source?.label || schemaText(schema, 'ui.result.detail.unknown_source'), ...(source?.parentKey ? { parentKey: source.parentKey } : {}) });
    add('step', capability?.label_cn || capability?.label || '');
  }
  const model = offering?.model_name || task?.modelId || '';
  const provider = offering?.provider_name || task?.providerId || '';
  add('model', [model, provider && provider !== model ? provider : ''].filter(Boolean).join(' · '));

  const params = shownParameters(offering);
  const entered = task?.parameters ?? {};
  const valueOf = (param: OfferingParameterWire) => (entered[param.name] !== undefined && entered[param.name] !== null && entered[param.name] !== '' ? entered[param.name] : undefined);
  const used = new Set<string>();
  const sizeParam = params.find((param) => param.semantic === 'output-size' || SIZE_NAMES.has(param.name));
  const sizeValue = sizeParam ? valueOf(sizeParam) : undefined;
  if (sizeParam) used.add(sizeParam.name);
  const measured = facts.measured ? `${facts.measured.width}x${facts.measured.height}` : '';
  add('size', sizeValue !== undefined ? valueText(schema, sizeParam as OfferingParameterWire, sizeValue) : measured);
  const ratioParam = params.find((param) => RATIO_NAMES.has(param.name));
  if (ratioParam) used.add(ratioParam.name);
  const ratioValue = ratioParam ? valueOf(ratioParam) : undefined;
  add('ratio', ratioValue !== undefined ? valueText(schema, ratioParam as OfferingParameterWire, ratioValue) : ratioOfSize(String(sizeValue ?? measured)) ?? '');
  const countParam = params.find((param) => COUNT_NAMES.has(param.name));
  if (countParam) used.add(countParam.name);
  const countValue = countParam ? valueOf(countParam) : undefined;
  // Without a count parameter the number of delivered pictures is the count, but only for an
  // image task: the pictures of a 3D task are its texture maps and thumbnail, not results.
  const countsPictures = capability?.domain === 'image';
  add('count', countValue !== undefined ? String(countValue) : (countsPictures && task && task.assetCount > 0 ? String(task.assetCount) : ''));

  for (const param of params) {
    if (used.has(param.name)) continue;
    const value = valueOf(param);
    if (value === undefined) continue;
    const text = valueText(schema, param, value);
    if (text) rows.push({ key: `param.${param.name}`, label: param.label, value: text });
  }

  // A reference image sent as a compressed copy (#276): one row each, in parameter order.
  for (const derivation of task?.inputDerivations ?? []) {
    const param = offering?.parameters?.find((item) => item.name === derivation.parameter);
    const name = param?.label || derivation.parameter;
    const label = param?.type === 'asset' ? name : `${name} ${derivation.index + 1}`;
    rows.push({ key: `input_derivation.${derivation.parameter}.${derivation.index}`, label: field(schema, 'input_derivation'), value: inputDerivationText(schema, derivation, label) });
  }

  const presentation = task ? findTaskState(schema.task_states, task.state) : null;
  add('status', presentation?.label ?? '');
  if (task?.nativeQuote !== undefined) {
    const quote = task.nativeQuote;
    rows.push({ key: 'native_quote', label: schemaText(schema, 'ui.pricing.totals_heading'), value: quote
      ? formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount: formatNativePrice(quote.total_amount, quote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') })
      : schemaText(schema, 'ui.pricing.unknown') });
  } else if (offering && offeringShowsCredits(offering)) {
    rows.push({ key: 'credits', label: field(schema, 'credits'), value: creditsText(schema, offering, task?.creditsCharged ?? null) });
  }
  add('created', task ? formatTime(schema, task.createdAt) : '');
  add('finished', task ? formatTime(schema, task.finishedAt) : '');
  add('duration', task ? formatDuration(schema, task.createdAt, task.finishedAt) : '');
  return rows;
}

export type DetailStatus = 'pending' | 'running' | 'ready' | 'failed';

/** Public task state to the four states the dialog tells apart, read from the Schema's task_states. */
export function detailStatus(schema: ProductSchema, state: string): DetailStatus | null {
  const presentation = findTaskState(schema.task_states, state);
  if (!presentation) return null;
  if (presentation.outcome === 'success') return 'ready';
  if (presentation.outcome === 'failure' || presentation.outcome === 'canceled') return 'failed';
  return presentation.outcome === 'running' ? 'running' : 'pending';
}

export interface ActionContext {
  /** Image or model; assets that are not generated media (video, files) offer download only. */
  kind: 'image' | 'model' | 'splat' | 'other';
  status: DetailStatus;
  /** The result came from a task (not an uploaded asset). */
  hasTask: boolean;
  /** The result belongs to the session in view, so it can take the stage. */
  inActiveSession: boolean;
  hasPrompt: boolean;
  hasFile: boolean;
  scope: AgentScope;
}

export interface DetailActions {
  download: boolean;
  openOnCanvas: boolean;
  /** Image to 3D model. */
  sendTo3D: boolean;
  /** Model screenshot to 2D. */
  sendTo2D: boolean;
  regenerate: boolean;
  copyPrompt: boolean;
}

/** Which buttons a result shows. Only a finished result has a file; the Agent mode hides the conversion that mode cannot run. */
export function detailActions(context: ActionContext): DetailActions {
  const ready = context.status === 'ready' && context.hasFile;
  const media = context.kind === 'image' || context.kind === 'model';
  return {
    download: ready,
    openOnCanvas: ready && media && context.hasTask && context.inActiveSession,
    sendTo3D: ready && context.kind === 'image' && context.hasTask && context.inActiveSession && context.scope !== '2d',
    sendTo2D: ready && context.kind === 'model' && context.hasTask && context.inActiveSession && context.scope !== '3d',
    regenerate: context.hasTask && context.hasPrompt && context.inActiveSession && (context.status === 'ready' || context.status === 'failed'),
    copyPrompt: context.hasPrompt,
  };
}

/**
 * Why an action that fits the result and the mode is still not usable, as a Schema text key; null when
 * nothing blocks it. A result of another session is shown dimmed with this reason on the button, instead
 * of vanishing. (The Agent mode hides the other mode's conversion outright: that is not a blocked action.)
 */
export function detailBlockedReason(context: ActionContext): 'ui.result.detail.disabled.other_session' | null {
  const ready = context.status === 'ready' && context.hasFile;
  const media = context.kind === 'image' || context.kind === 'model';
  return ready && media && context.hasTask && !context.inActiveSession ? 'ui.result.detail.disabled.other_session' : null;
}

/** The conversion the current mode allows for this kind of result. */
export function conversionAllowed(kind: ActionContext['kind'], scope: AgentScope): 'sendTo3D' | 'sendTo2D' | null {
  if (kind === 'image') return scope === '2d' ? null : 'sendTo3D';
  if (kind === 'model') return scope === '3d' ? null : 'sendTo2D';
  return null;
}
