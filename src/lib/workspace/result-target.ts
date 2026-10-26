import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { viewableGaussianSplatAsset, viewableModelAsset, type TrackedAsset } from './task-projection';
import type { VersionTile } from '@/lib/workspace/versions';
import type { CurrentObject } from '@/store/workspace-store';
import { objectOfTile, type TilePick } from '@/lib/workspace/version-object';
import type { DetailStatus } from '@/lib/workspace/task-detail';
import type { ProductSchema } from '@/lib/product-schema';
import { imageResultSlots, orderedImageResults } from '@/lib/workspace/image-result-group';

/**
 * What the shared 任务详情 dialog is opened on. A target with a taskId is a generated result
 * (the dialog reads the public task); one without is an uploaded asset (preview, facts, download).
 */
export interface ResultTarget {
  taskId?: string;
  sessionId?: string;
  kind: 'image' | 'model' | 'splat' | 'other';
  /** Same-origin file URL; empty while the task is still running or after it failed. */
  url: string;
  assetId?: string;
  /** Title: the version label, or the asset's file name. */
  title: string;
  /** Media type of an uploaded asset ("image/png"); lets the dialog pick image or video preview. */
  mediaType?: string;
  deliveryProfile?: string;
  assetRole?: string;
  assetVariant?: string;
  /** One line under the title (an uploaded asset's size and date). */
  detail?: string;
  /** Prompt known where the card is (task tracker), used until the server's answer brings the stored one. */
  prompt?: string;
  capability?: string;
  /** Status known where the card is; the server's answer takes over. */
  status?: DetailStatus;
  imageUrls?: string[];
  captureId?: string;
  /** Download name for an uploaded asset. */
  fileName?: string;
  /** The results that came out together (the candidates of one request); ←/→ in the dialog pages through them. */
  group?: ResultTarget[];
}

/** Identity of a result inside its group. */
function sameResult(left: ResultTarget, right: ResultTarget): boolean {
  return left.taskId === right.taskId && (left.assetId ?? left.url) === (right.assetId ?? right.url);
}

/** Position of a target in its own group; null when it has none or is not listed. */
export function groupPosition(target: ResultTarget | null): { index: number; count: number } | null {
  if (!target?.group || target.group.length < 2) return null;
  const index = target.group.findIndex((item) => sameResult(item, target));
  return index < 0 ? null : { index, count: target.group.length };
}

/** The neighbour of a target within its group (the group travels with it); null at the ends or without a group. */
export function stepResultTarget(target: ResultTarget | null, delta: number): ResultTarget | null {
  const position = groupPosition(target);
  if (!target?.group || !position) return null;
  const next = target.group[position.index + delta];
  return next ? { ...next, group: target.group } : null;
}

export function targetOfTile(tile: VersionTile, sessionId: string, title: string, pick: TilePick = {}, prompt = '', schema?: ProductSchema): ResultTarget {
  const slots = imageResultSlots(schema, tile.capability);
  const images = orderedImageResults(tile.assets, slots);
  const chosen = !pick.assetId && slots.length > 0 && images.length > 0 ? { ...pick, assetId: images[0].id } : pick;
  const target = singleTargetOfTile(tile, sessionId, title, chosen, prompt);
  if (tile.status === 'ready' && tile.domain !== 'model' && tile.slots.length === 0 && images.length > 1) {
    const group = images.map((asset) => {
      const label = slots.find((slot) => slot.variant === asset.variant)?.label;
      return singleTargetOfTile(tile, sessionId, label ? `${title} · ${label}` : title, { assetId: asset.id }, prompt);
    });
    return { ...target, group };
  }
  return target;
}

function singleTargetOfTile(tile: VersionTile, sessionId: string, title: string, pick: TilePick, prompt: string): ResultTarget {
  const object = objectOfTile(tile, sessionId, pick);
  const splat = viewableGaussianSplatAsset(tile.assets);
  const attachment = !object && tile.status === 'ready' && tile.assets.length > 0 && tile.assets.every(asset => asset.kind === 'other')
    ? tile.assets.find(asset => asset.id === pick.assetId) ?? tile.assets[0] : undefined;
  return {
    taskId: object?.taskId ?? tile.taskId,
    sessionId,
    kind: splat ? 'splat' : object?.kind ?? (attachment ? 'other' : tile.domain === 'model' ? 'model' : 'image'),
    url: splat ? safeAssetURL(splat.url) : object?.url ?? (attachment ? safeAssetURL(attachment.url) : ''),
    ...(attachment ? { assetId: attachment.id, mediaType: attachment.mediaType } : {}),
    ...(object?.assetId ? { assetId: object.assetId } : {}),
    ...(object?.assetId && tile.assets.find((asset) => asset.id === object.assetId)
      ? { assetRole: tile.assets.find((asset) => asset.id === object.assetId)!.role,
          assetVariant: tile.assets.find((asset) => asset.id === object.assetId)!.variant } : {}),
    ...(splat ? { assetId: splat.id, mediaType: splat.mediaType, deliveryProfile: splat.deliveryProfile, assetRole: splat.role, assetVariant: splat.variant } : {}),
    title,
    capability: tile.capability,
    status: tile.status,
    ...(prompt ? { prompt } : {}),
    ...(object?.imageUrls ? { imageUrls: object.imageUrls } : {}),
    ...(object?.captureId ? { captureId: object.captureId } : {}),
  };
}

/** The object the old click put on the stage; null for a result that has no file. */
export function objectOfTarget(target: ResultTarget): CurrentObject | null {
  if (!target.taskId || !target.sessionId || !target.url || target.kind === 'other' || target.kind === 'splat') return null;
  return {
    taskId: target.taskId, kind: target.kind, url: target.url, sessionId: target.sessionId,
    ...(target.assetId ? { assetId: target.assetId } : {}),
    ...(target.imageUrls ? { imageUrls: target.imageUrls } : {}),
    ...(target.captureId ? { captureId: target.captureId } : {}),
  };
}

/** Public Task assets determine the detail target; an attachment must never become a stage object. */
export function targetOfTaskAssets(target: Omit<ResultTarget, 'kind' | 'url' | 'assetId' | 'mediaType'>, assets: ReadonlyArray<TrackedAsset>): ResultTarget {
  const splat = viewableGaussianSplatAsset(assets);
  if (splat) return {
    ...target, kind: 'splat', url: safeAssetURL(splat.url), assetId: splat.id,
    mediaType: splat.mediaType, deliveryProfile: splat.deliveryProfile,
    assetRole: splat.role, assetVariant: splat.variant,
  };
  const hasModel = assets.some(asset => asset.kind === 'model');
  const image = assets.find(asset => asset.kind === 'image');
  const media = hasModel ? viewableModelAsset(assets) : image ?? assets.find(asset => asset.kind === 'other');
  const kind = hasModel ? 'model' : image ? 'image' : assets.some(asset => asset.kind === 'other') ? 'other' : 'image';
  return { ...target, kind, url: media ? safeAssetURL(media.url) : '', ...(media ? { assetId: media.id, mediaType: media.mediaType } : {}) };
}
