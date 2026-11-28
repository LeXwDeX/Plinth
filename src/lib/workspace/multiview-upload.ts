import type { OfferingWire } from '@/lib/agent-offerings';
import type { MultiviewTurnRequest } from '@/lib/chat/browser-client';
import type { ParamOption } from '@/lib/providers/types';

/** The view every multiview set must include. */
export const REQUIRED_VIEW = 'front';
const DEFAULT_MIN_PICTURES = 2;
const DEFAULT_MAX_PICTURES = 4;

export interface MultiviewOffering {
  offering: OfferingWire;
  /** The views the Offering accepts, in the Schema's order. */
  views: ParamOption[];
  minPictures: number;
  maxPictures: number;
}

function describe(offering: OfferingWire): MultiviewOffering | null {
  const views = offering.parameters.find((parameter) => parameter.semantic === 'reference-views')?.options ?? [];
  if (views.length === 0) return null;
  const pictures = offering.parameters.find((parameter) => parameter.semantic === 'reference-image');
  return {
    offering, views,
    minPictures: pictures?.min_items ?? DEFAULT_MIN_PICTURES,
    maxPictures: pictures?.max_items ?? DEFAULT_MAX_PICTURES,
  };
}

/**
 * The available Offering that takes several views of one object, found only by its `reference-views`
 * parameter. The one the person picked wins when it qualifies; otherwise the first qualifying one.
 */
export function multiviewOffering(offerings: readonly OfferingWire[], preferredModelId?: string): MultiviewOffering | null {
  const found = offerings.filter((offering) => offering.available).flatMap((offering) => describe(offering) ?? []);
  return found.find((item) => preferredModelId && item.offering.model_id === preferredModelId) ?? found[0] ?? null;
}

/**
 * #721: whether the composer shows its 用作多视角 entry beside the attachments: 2–4 attached pictures, a multiview
 * model to make them with, and no sent message being edited. The entry asks nothing and changes nothing until pressed.
 */
export function multiviewEntryOffered(target: MultiviewOffering | null, pictures: number, editing: boolean): boolean {
  return Boolean(target) && pictures >= DEFAULT_MIN_PICTURES && pictures <= DEFAULT_MAX_PICTURES && !editing;
}

/** The two models a panel names when it makes a model other than the one picked for 3D (#373). */
export interface MultiviewLockNote {
  /** The 3D model the person picked, which cannot make a model from several views right now. */
  locked: string;
  /** The multiview model this panel makes the model with. */
  model: string;
}

/**
 * The note for a target that is not the 3D model the person picked: the request carries the target and the
 * server runs it for this turn without touching the session's pick. Null when nothing is picked or the pick
 * is the target. A pick no service carries (a retired model) is named by its id.
 */
export function multiviewLockNote(offerings: readonly OfferingWire[], target: MultiviewOffering, pickedModelId?: string | null): MultiviewLockNote | null {
  if (!pickedModelId || pickedModelId === target.offering.model_id) return null;
  const picked = offerings.find((offering) => offering.model_id === pickedModelId);
  return { locked: picked?.model_name || pickedModelId, model: target.offering.model_name };
}

/** Image i gets the i-th view in the Offering's order; pictures beyond the views get none. */
export function defaultViewChoices(count: number, views: readonly ParamOption[]): string[] {
  return Array.from({ length: count }, (_, index) => views[index]?.value ?? '');
}

/** Chosen views are unique, include the front view, and their count is within the Offering's picture limits. */
export function validViewChoices(choices: readonly string[], minPictures = DEFAULT_MIN_PICTURES, maxPictures = DEFAULT_MAX_PICTURES): boolean {
  const chosen = choices.filter((view) => view !== '');
  return new Set(chosen).size === chosen.length
    && chosen.includes(REQUIRED_VIEW)
    && chosen.length >= minPictures
    && chosen.length <= maxPictures;
}

/** The chosen labels in picture order, for the turn's words. */
export function chosenViewLabels(choices: readonly string[], views: readonly ParamOption[]): string[] {
  return choices.flatMap((choice) => views.find((view) => view.value === choice)?.label ?? []);
}

export interface AssetRoutes {
  asset_item: string;
  task_asset: string;
}

/** Matches an address against a route template such as `/api/v1/tasks/{taskID}/assets/{assetID}`; returns its placeholders. */
function matchRoute(template: string, url: string): Record<string, string> | null {
  const names: string[] = [];
  const pattern = template.split(/\{(\w+)\}/).map((part, index) => {
    if (index % 2 === 1) { names.push(part); return '([^/?#]+)'; }
    return part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }).join('');
  const match = new RegExp(`${pattern}(?:[?#].*)?$`).exec(url);
  if (!match) return null;
  return Object.fromEntries(names.map((name, index) => {
    try { return [name, decodeURIComponent(match[index + 1])]; } catch { return [name, match[index + 1]]; }
  }));
}

/** The asset an uploaded-input or generated-picture address names; null for anything else. */
export function assetFromURL(routes: AssetRoutes, url: string): { assetId: string; taskId?: string } | null {
  const task = matchRoute(routes.task_asset, url);
  if (task?.assetID) return { assetId: task.assetID, ...(task.taskID ? { taskId: task.taskID } : {}) };
  const asset = matchRoute(routes.asset_item, url);
  return asset?.assetID ? { assetId: asset.assetID } : null;
}

/**
 * The turn's multiview request from the pictures' addresses (already uploaded) and each one's view;
 * pictures with an empty view are left out. Null when a chosen picture names no asset.
 */
export function buildMultiviewRequest(routes: AssetRoutes, model: string, urls: readonly string[], views: readonly string[]): MultiviewTurnRequest | null {
  const entries: MultiviewTurnRequest['views'] = [];
  for (const [index, url] of urls.entries()) {
    const view = views[index] ?? '';
    if (view === '') continue;
    const asset = assetFromURL(routes, url);
    if (!asset) return null;
    entries.push({ view, ...(asset.taskId ? { taskId: asset.taskId } : {}), assetId: asset.assetId });
  }
  return entries.length > 0 ? { model, views: entries } : null;
}
