import type { OfferingWire } from '@/lib/agent-offerings';
import type { MultiviewTurnRequest } from '@/lib/chat/browser-client';
import { parseNativeQuote, type NativeQuote } from '@/lib/pricing';

/**
 * Three-view groups as the server projects them (GET .../three-views): one run
 * of the three-view recipe, three slots (the views), each with its own status.
 * Nothing here decides whether a view may start or retry: the projection says so
 * (`startable`, `retryable`) and the card only follows it.
 */

export type ThreeViewStatus =
  | 'not_started' | 'awaiting_approval' | 'denied' | 'starting' | 'running' | 'succeeded' | 'failed' | 'canceled'
  /** A finished picture the automatic consistency check refused (#243); retryable alone. */
  | 'rejected' | 'unknown';

const STATUSES: readonly ThreeViewStatus[] = [
  'not_started', 'awaiting_approval', 'denied', 'starting', 'running', 'succeeded', 'failed', 'canceled', 'rejected',
];

export interface ThreeViewSlot {
  /** The view key the multiview Offering speaks (front, left, back, right; #65). */
  key: string;
  label: string;
  /** The group is complete without this view (the right view). */
  optional: boolean;
  /** The person put an uploaded picture in place of this view: `outputAssetId` is an input asset. */
  replaced: boolean;
  status: ThreeViewStatus;
  attempt: number;
  taskId: string;
  outputAssetId: string;
  requestId: string;
  startable: boolean;
  retryable: boolean;
  /** What starting or retrying this view costs; 0 when the server does not know yet. */
  estimatedCredits: number;
  nativeQuote?: NativeQuote | null;
  priceSource?: 'default' | 'frozen';
  /** The Offering's own credits_metered declaration; no cost is shown unless it is true. */
  creditsMetered: boolean;
}

export interface ThreeViewModelSource {
  slot: string;
  available: boolean;
  /** Empty when the person replaced the view with an upload; `assetId` is then an input asset. */
  taskId: string;
  assetId: string;
  /** The Schema's statement that only this view is used for 3D. */
  note: string;
}

export interface ThreeViewGroup {
  runId: string;
  label: string;
  sourceAssetId: string;
  createdAt: string;
  complete: boolean;
  slots: ThreeViewSlot[];
  modelSource: ThreeViewModelSource;
}

export type ThreeViewAction = 'start' | 'retry' | 'replace';

const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const flag = (value: unknown): boolean => value === true;
const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

function parseSlot(value: unknown): ThreeViewSlot | null {
  if (!isRecord(value)) return null;
  const key = text(value.key);
  if (key === '') return null;
  const status = STATUSES.find((item) => item === value.status) ?? 'unknown';
  const native = Object.hasOwn(value, 'native_quote') || value.price_mode === 'native';
  return {
    key,
    label: text(value.label) || key,
    optional: flag(value.optional),
    replaced: flag(value.replaced),
    status,
    attempt: typeof value.attempt === 'number' && Number.isFinite(value.attempt) ? Math.max(0, Math.floor(value.attempt)) : 0,
    taskId: text(value.task_id),
    outputAssetId: text(value.output_asset_id),
    requestId: text(value.request_id),
    startable: flag(value.startable),
    retryable: flag(value.retryable),
    creditsMetered: flag(value.credits_metered),
    ...(native ? { nativeQuote: parseNativeQuote(value.native_quote), priceSource: value.price_source === 'default' ? 'default' as const : value.price_source === 'frozen' || text(value.task_id) !== '' ? 'frozen' as const : 'default' as const } : {}),
    estimatedCredits: !native && typeof value.estimated_credits === 'number' && Number.isFinite(value.estimated_credits) ? Math.max(0, value.estimated_credits) : 0,
  };
}

export function parseThreeViewGroup(value: unknown): ThreeViewGroup | null {
  if (!isRecord(value)) return null;
  const runId = text(value.run_id);
  if (runId === '' || !Array.isArray(value.slots)) return null;
  const slots = value.slots.map(parseSlot).filter((slot): slot is ThreeViewSlot => slot !== null);
  const source = isRecord(value.model_source) ? value.model_source : {};
  return {
    runId,
    label: text(value.label),
    sourceAssetId: text(value.source_asset_id),
    createdAt: text(value.created_at),
    complete: flag(value.complete),
    slots,
    modelSource: {
      slot: text(source.slot),
      available: flag(source.available),
      taskId: text(source.task_id),
      assetId: text(source.asset_id),
      note: text(source.note),
    },
  };
}

export function parseThreeViews(payload: unknown): ThreeViewGroup[] {
  const items = isRecord(payload) && Array.isArray(payload.items) ? payload.items : [];
  return items.map(parseThreeViewGroup).filter((group): group is ThreeViewGroup => group !== null);
}

/** What the person may ask of a view right now; null when nothing (running, done, or not retryable). */
export function slotAction(slot: ThreeViewSlot): 'start' | 'retry' | null {
  if (slot.startable) return 'start';
  if (slot.retryable) return 'retry';
  return null;
}

/** A group with work in flight keeps being read until it settles. */
export function groupIsActive(group: ThreeViewGroup): boolean {
  return group.slots.some((slot) => slot.status === 'starting' || slot.status === 'running' || slot.status === 'awaiting_approval');
}

export function groupsAreActive(groups: readonly ThreeViewGroup[] | undefined): boolean {
  return (groups ?? []).some(groupIsActive);
}

export type ThreeViewTileStatus = 'ready' | 'pending' | 'running' | 'failed';

/** How a group's strip tile reads when none of its views has a picture yet. */
export function groupTileStatus(group: ThreeViewGroup): ThreeViewTileStatus {
  if (group.slots.some((slot) => slot.status === 'succeeded')) return 'ready';
  if (group.slots.some((slot) => slot.status === 'starting' || slot.status === 'running')) return 'running';
  if (group.slots.some((slot) => slot.status === 'awaiting_approval')) return 'pending';
  return 'failed';
}

export function succeededCount(group: ThreeViewGroup): number {
  return group.slots.filter((slot) => slot.status === 'succeeded').length;
}

/** A view whose approval or Task is in flight cannot be replaced; any other view can (#65). */
export function slotReplaceable(slot: ThreeViewSlot): boolean {
  return slot.status !== 'awaiting_approval' && slot.status !== 'starting' && slot.status !== 'running';
}

/** The required views the person has not started yet: what "generate remaining views" starts, one approval each. */
export function remainingSlots(group: ThreeViewGroup): ThreeViewSlot[] {
  return group.slots.filter((slot) => !slot.optional && slot.startable && slot.status === 'not_started');
}

/** The estimate of starting the remaining views, or 0 when no metered estimate is known for every one of them. */
export function remainingCredits(slots: readonly ThreeViewSlot[]): number {
  if (slots.length === 0 || slots.some((slot) => slot.nativeQuote !== undefined || !slot.creditsMetered || slot.estimatedCredits <= 0)) return 0;
  return slots.reduce((sum, slot) => sum + slot.estimatedCredits, 0);
}

/** A model that takes several views of one object, read from its Offering's public declaration (#65). */
export interface MultiviewModel {
  modelId: string;
  modelName: string;
  /** The view keys and labels, in the Offering's order. */
  views: { value: string; label: string }[];
  minimumViews: number;
  /** The picture types the Offering takes (empty when it does not say). */
  mediaTypes: string[];
}

/** The Schema semantic of the parameter that names the view of each reference picture. */
export const REFERENCE_VIEWS_SEMANTIC = 'reference-views';

/** The multiview model an Offering declares, or null: only the reference-views parameter makes one. */
export function multiviewModelOf(offering: OfferingWire): MultiviewModel | null {
  const views = offering.parameters.find((parameter) => parameter.semantic === REFERENCE_VIEWS_SEMANTIC);
  if (!views?.options?.length) return null;
  const pictures = offering.parameters.find((parameter) => parameter.semantic === 'reference-image');
  return {
    modelId: offering.model_id,
    modelName: offering.model_name || offering.model_id,
    views: views.options.map((option) => ({ value: String(option.value), label: option.label || String(option.value) })),
    minimumViews: Math.max(2, pictures?.min_items ?? 2),
    mediaTypes: pictures?.media_types ?? [],
  };
}

/** The multiview model to use: the person's 3D pick when it is one, else the first available. */
export function pickMultiviewModel(
  offeringsByCapability: Record<string, OfferingWire[]> | undefined,
  picked: string | undefined,
): MultiviewModel | null {
  const models = Object.values(offeringsByCapability ?? {}).flat()
    .filter((offering) => offering.available)
    .map(multiviewModelOf)
    .filter((model): model is MultiviewModel => model !== null);
  return models.find((model) => model.modelId === picked) ?? models[0] ?? null;
}

/** The finished views of a group the model takes, in the group's order; the request the button sends. */
export function groupMultiview(group: ThreeViewGroup, model: MultiviewModel): {
  slots: ThreeViewSlot[];
  ready: boolean;
  request: MultiviewTurnRequest;
} {
  const keys = new Set(model.views.map((view) => view.value));
  const slots = group.slots.filter((slot) => slot.status === 'succeeded' && slot.outputAssetId !== '' && keys.has(slot.key));
  const ready = slots.some((slot) => slot.key === group.modelSource.slot) && slots.length >= model.minimumViews;
  return {
    slots,
    ready,
    request: {
      group: group.runId,
      model: model.modelId,
      views: slots.map((slot) => ({ view: slot.key, assetId: slot.outputAssetId, ...(slot.replaced || !slot.taskId ? {} : { taskId: slot.taskId }) })),
    },
  };
}

export type TimelineItem<T> = { kind: 'message'; message: T } | { kind: 'group'; group: ThreeViewGroup };

/**
 * A group's card sits in the conversation where it was made: before the first
 * message that is newer than the group, so the Agent's reply about it follows
 * the card. Groups newer than every message close the list.
 */
export function interleaveGroups<T extends { createdAt: number }>(messages: readonly T[], groups: readonly ThreeViewGroup[]): TimelineItem<T>[] {
  const parsed = groups.map((group) => ({ group, time: Date.parse(group.createdAt) }));
  // A group whose time cannot be read goes last rather than blocking the others.
  const dated = [
    ...parsed.filter((entry) => Number.isFinite(entry.time)).sort((left, right) => left.time - right.time),
    ...parsed.filter((entry) => !Number.isFinite(entry.time)),
  ];
  const items: TimelineItem<T>[] = [];
  let next = 0;
  for (const message of messages) {
    while (next < dated.length && Number.isFinite(dated[next].time) && dated[next].time < message.createdAt) {
      items.push({ kind: 'group', group: dated[next].group });
      next += 1;
    }
    items.push({ kind: 'message', message });
  }
  for (; next < dated.length; next += 1) items.push({ kind: 'group', group: dated[next].group });
  return items;
}
