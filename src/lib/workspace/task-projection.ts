/**
 * 原生任务详情（GET /api/v1/tasks/{taskID}）到工作区任务卡片的纯投影。
 *
 * 只消费 Go HTTP API 的公开投影字段与 Product Schema 的 task_states /
 * content.messages：状态与终态判定来自 schema.task_states，错误文案
 * 经 error_mapping 产出的 error.* 词汇键或已解析安全文本透传，
 * 下载入口只认真实资产 URL（绝不产出 '#' 占位链接）。
 */

import type { TaskStateSchema } from '@/lib/product-schema';

export type TrackedAssetKind = 'image' | 'model' | 'splat' | 'other';

export interface TrackedAsset {
  id: string;
  role: string;
  variant: string;
  delivery: string;
  deliveryProfile?: string;
  ordinal: number;
  mediaType: string;
  size: number;
  url: string;
  kind: TrackedAssetKind;
  /** A required output of the task, as against an attachment (a rigging result's animation previews); #326. */
  required?: boolean;
  /** The public name of a model file's format ("GLB") the server names; never the Provider's result field (#327). */
  format?: string;
}

export interface TrackedTaskFailure {
  failureClass: string;
  detail: string;
  retryable: boolean;
  /** 结果不明：上游可能已经接单，不能当作可安全重试的失败。 */
  ambiguous: boolean;
  /**
   * The owner's judgment of running a step that failed as source_expired again (#326), from the task detail
   * only: saved_file (it will use the saved model file), source_expired or source_incompatible (it cannot succeed).
   */
  sourceRetry?: string;
}

export interface TrackedResource {
  role: string;
  variant: string;
  variantPrefix: string;
  delivery: string;
  state: string;
  reason: string;
}

export interface TrackedTaskProjection {
  state: string;
  progress: number;
  terminal: boolean;
  failure: TrackedTaskFailure | null;
  assets: TrackedAsset[];
  downloadables: TrackedAsset[];
  resources: TrackedResource[];
  resourcesPending: boolean;
  /** The Offering and model the task ran on, when the server says; used to offer another model after a failure. */
  offeringId?: string;
  modelId?: string;
  /** When the server accepted the task (ms since epoch); the progress card counts elapsed time from it. */
  createdAt?: number;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function numberOrZero(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/** 进度钳制到 Schema 百分比刻度（runtime.frontend.percentage_multiplier）。 */
export function clampProgress(value: unknown, maximum: number): number {
  if (!(maximum > 0)) return 0;
  const numeric = numberOrZero(value);
  return Math.min(Math.max(numeric, 0), maximum);
}

/** Public artifact roles distinguish model results from other files using model/* MIME.
 * Missing-role legacy records retain MIME fallback; image covers keep their existing image semantics.
 */
export function assetKind(mediaType: string, role = '', variant = '', deliveryProfile = ''): TrackedAssetKind {
  if (role === 'gaussian-splat' && variant === 'splat' && deliveryProfile === 'gaussian-splat-32-v1'
    && mediaType.toLowerCase().split(';')[0].trim() === 'application/vnd.art-ai.gaussian-splat') return 'splat';
  if (role === 'model') return 'model';
  if (mediaType.startsWith('image/')) return 'image';
  if (mediaType.startsWith('model/')) return role === '' ? 'model' : 'other';
  return 'other';
}

/** 真实 URL 才可下载：空串 / 缺失 / '#' 占位一律排除。 */
export function isDownloadableAsset(asset: { url: string }): boolean {
  return asset.url !== '' && asset.url !== '#';
}

export function projectTaskAssets(raw: unknown): TrackedAsset[] {
  if (!Array.isArray(raw)) return [];
  const assets: TrackedAsset[] = [];
  for (const entry of raw) {
    if (!isRecord(entry)) continue;
    const mediaType = text(entry.media_type);
    assets.push({
      id: text(entry.id),
      role: text(entry.role),
      variant: text(entry.variant),
      delivery: text(entry.delivery),
      deliveryProfile: text(entry.delivery_profile),
      ordinal: numberOrZero(entry.ordinal),
      mediaType,
      size: numberOrZero(entry.size),
      url: text(entry.url),
      kind: assetKind(mediaType, text(entry.role), text(entry.variant), text(entry.delivery_profile)),
      required: entry.required === true,
      ...(text(entry.format) ? { format: text(entry.format) } : {}),
    });
  }
  return assets;
}

/** Gaussian Splat preview is opt-in by the complete server-declared representation identity. */
export function isGaussianSplatAsset(asset: Pick<TrackedAsset, 'role' | 'variant' | 'deliveryProfile' | 'mediaType'>): boolean {
  return asset.role === 'gaussian-splat'
    && asset.variant === 'splat'
    && asset.deliveryProfile === 'gaussian-splat-32-v1'
    && asset.mediaType.toLowerCase().split(';')[0].trim() === 'application/vnd.art-ai.gaussian-splat';
}

export type GaussianSplatAssetCandidate = Pick<TrackedAsset, 'role' | 'variant' | 'deliveryProfile' | 'mediaType' | 'url' | 'ordinal'> & { id: string };

export function viewableGaussianSplatAsset<T extends GaussianSplatAssetCandidate>(assets: ReadonlyArray<T>): T | null {
  return assets.filter((asset) => isGaussianSplatAsset(asset) && isDownloadableAsset(asset))
    .sort(byOrdinal)[0] ?? null;
}

function projectFailure(raw: unknown): TrackedTaskFailure | null {
  if (!isRecord(raw)) return null;
  return {
    failureClass: text(raw.class),
    detail: text(raw.detail),
    retryable: raw.retryable === true,
    ambiguous: raw.ambiguous === true,
    ...(text(raw.source_retry) ? { sourceRetry: text(raw.source_retry) } : {}),
  };
}

function projectResources(raw: unknown): TrackedResource[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((entry): TrackedResource[] => {
    if (!isRecord(entry)) return [];
    const role = text(entry.role);
    const state = text(entry.state);
    if (!role || !state) return [];
    return [{
      role,
      variant: text(entry.variant),
      variantPrefix: text(entry.variant_prefix),
      delivery: text(entry.delivery),
      state,
      reason: text(entry.reason),
    }];
  });
}

/** 终态与状态展示只从 schema.task_states 判定；未知状态按非终态处理。 */
export function findTaskState(
  states: TaskStateSchema[],
  state: string,
): TaskStateSchema | null {
  return (
    states.find((entry) => entry.internal_state === state) ??
    states.find((entry) => entry.public_state === state) ??
    null
  );
}

export function projectNativeTaskDetail(
  raw: unknown,
  states: TaskStateSchema[],
  progressMaximum: number,
): TrackedTaskProjection | null {
  if (!isRecord(raw)) return null;
  const state = text(raw.state);
  const assets = projectTaskAssets(raw.assets);
  const presentation = findTaskState(states, state);
  return {
    state,
    progress: clampProgress(raw.progress, progressMaximum),
    terminal: presentation?.terminal ?? false,
    failure: projectFailure(raw.failure),
    assets,
    downloadables: presentation?.terminal && presentation.outcome === 'success'
      ? assets.filter(isDownloadableAsset)
      : [],
    resources: projectResources(raw.resources),
    resourcesPending: raw.resources_pending === true,
    offeringId: text(raw.offering_id),
    modelId: text(raw.model_id),
    ...(Number.isFinite(Date.parse(text(raw.created_at))) ? { createdAt: Date.parse(text(raw.created_at)) } : {}),
  };
}

/**
 * 错误文案解析（error_mapping 消费端）：detail 为 error.* 词汇键时经
 * content.messages 映射；已是服务端解析后的安全文本则透传；缺失走 fallback。
 */
export function resolveErrorText(
  messages: Record<string, string>,
  detail: unknown,
  fallback: string,
): string {
  if (typeof detail !== 'string' || detail === '') return fallback;
  return messages[detail] ?? detail;
}

function byOrdinal(a: { ordinal: number }, b: { ordinal: number }): number {
  return a.ordinal - b.ordinal;
}

/**
 * The 3D stage reads GLB only (product direction 2026-10-09). Every other model format a provider
 * delivers (glTF JSON with external buffers, 3MF, FBX, OBJ, USDZ, STL, and historical BLEND files
 * that are no longer offered) is download-only:
 * feeding it to the viewer fails to parse (a 3MF is a ZIP). The format is read from the asset's
 * media type, which the server fills from the Schema-declared result_assets; variant is only a
 * fallback for assets whose media type is missing or generic.
 */
const VIEWER_MODEL_MEDIA_TYPES: readonly string[] = ['model/gltf-binary'];

type ViewerCandidate = { url: string; kind: TrackedAssetKind; ordinal: number; mediaType?: string; variant?: string; required?: boolean };

/** Placeholder types some CDNs answer for any file; the server keeps them when the claim named no type (same set as Go genericMediaType). */
const GENERIC_MEDIA_TYPES: readonly string[] = ['', 'application/octet-stream', 'binary/octet-stream', 'application/binary'];

function viewerRank(asset: ViewerCandidate): number {
  const media = (asset.mediaType ?? '').toLowerCase().split(';')[0].trim();
  let rank = VIEWER_MODEL_MEDIA_TYPES.indexOf(media);
  if (rank < 0 && GENERIC_MEDIA_TYPES.includes(media)) {
    const variant = (asset.variant ?? '').toLowerCase().split('.').pop() ?? '';
    rank = variant === 'glb' ? 0 : -1;
  }
  return rank;
}

/** Whether the 3D viewer can load this asset (a GLB model). */
export function isViewableModelAsset(asset: ViewerCandidate): boolean {
  return asset.kind === 'model' && isDownloadableAsset(asset) && viewerRank(asset) >= 0;
}

/**
 * The model the 3D viewer shows: the GLB; of several, a required output before an attachment (#326: a
 * Meshy Rigging result lists its animation skeletons first), then the lowest ordinal. This is the main
 * model the 加工 panel binds and the Agent's model handle names (Go generation.PrimaryModelAsset). Every
 * other format stays in download lists only; with no GLB this returns null so callers show the "preview
 * unavailable" state instead of parsing a non-GLB file.
 */
export function viewableModelAsset<T extends ViewerCandidate>(assets: ReadonlyArray<T>): T | null {
  const models = assets
    .filter(isViewableModelAsset)
    .sort((a, b) => Number(Boolean(b.required)) - Number(Boolean(a.required)) || viewerRank(a) - viewerRank(b) || byOrdinal(a, b));
  return models[0] ?? null;
}

/** 2D 画布图片资产：全部可下载 image/* 资产（按 ordinal 排序）。 */
export function imageAssets<T extends { url: string; kind: TrackedAssetKind; ordinal: number }>(
  assets: ReadonlyArray<T>,
): T[] {
  return assets
    .filter((asset) => asset.kind === 'image' && isDownloadableAsset(asset))
    .sort(byOrdinal);
}

/**
 * The rendered preview of a 3D result: the output the Schema declares with the `thumbnail` role
 * (Offering outputs / result_assets). Texture maps and other pictures a model task carries are
 * surfaces of the model, not a picture of it, so they are never used here; with no thumbnail the
 * caller shows a neutral 3D placeholder.
 */
export function modelPreviewAsset<T extends { role: string; kind: string; variant?: string }>(assets: readonly T[]): T | undefined {
  const thumbnails = assets.filter((asset) => asset.role === 'thumbnail' && asset.kind === 'image');
  return thumbnails.find((asset) => asset.variant === 'primary') ?? thumbnails[0];
}
