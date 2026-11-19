'use client';

import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import { ListChecks } from 'lucide-react';

import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import {
  AssetListError,
  type AssetListClient,
  type AssetListCrop,
  type AssetListDecision,
  type AssetListDetail,
  type AssetListEditInput,
  type AssetListItem,
  type AssetListItemInput,
  type AssetListReference,
  type AssetListRunAction,
  type CropInstruction,
  type CropRegion,
} from '@/lib/asset-lists/client';
import { runIsTerminal, runProgressText, runStatusText } from '@/lib/asset-lists/run';
import {
  formatSchemaText, schemaCountMessage, schemaText, type ProductSchema,
} from '@/lib/product-schema';

export interface AssetListCardProps {
  detail: AssetListDetail;
  schema: ProductSchema;
  projectId: string;
  /** The client the parent built with `assetListClient(schema)` or a test double. */
  client: AssetListClient;
  variant?: 'chat' | 'panel';
  /** A decision succeeded; the parent re-reads the list or the project. */
  onDecided?: () => void;
  /** #328: a run control or a skip succeeded; the parent re-reads the list or the project. */
  onRunChanged?: () => void;
  /** #331: an edit succeeded; the parent shows the returned detail. */
  onEdited?: (detail: AssetListDetail) => void;
  /** #331: the list changed under the editor (409); the parent re-reads it. */
  onStale?: () => void;
}

type Conflict = { id: string; title: string };

/**
 * #331: the fallback shortest side a crop region may have, in source pixels.
 * The public Schema carries the current value as
 * `application.asset_list.limits.minimum_crop_short_edge` (projected from
 * `decompose.crop.minimum_short_edge`); an older server omits it and the editor
 * falls back to this constant. The server refuses anything smaller with
 * `asset_list_region_too_small`, so the editor refuses it first.
 */
export const MIN_CROP_SHORT_EDGE = 64;

/** #331: one item while the card is editing. */
interface DraftItem {
  /** React key and the identity merge keeps: a merged item keeps the first one. */
  key: string;
  kind: string;
  name: string;
  description: string;
  /** Empty on an item drawn by this editor: the server then picks the route. */
  routeID: string;
  references: AssetListReference[];
  state: string;
  /** The region the item's crop was cut from, when the browser resolved it. */
  origin?: AssetListCrop;
  /** An unsaved re-crop instruction to send with the edit. */
  crop?: CropInstruction;
}

/** #331: one pointer drag over the source picture, in normalized display space. */
interface CropDrag {
  startX: number;
  startY: number;
  currentX: number;
  currentY: number;
  /** The overlay's display size at pointerdown; the minimum-edge fallback. */
  width: number;
  height: number;
}

/** #331: a drawn region waiting for its name and kind. */
interface PendingCrop {
  sourceID: string;
  region: CropRegion;
}

/**
 * #331: the list as editing began. A save carries this snapshot's version,
 * title and brief, never the live detail's: a list that advanced under the
 * draft must not be overwritten with a version the person never edited.
 */
interface EditBaseline {
  id: string;
  version: number;
  title: string;
  brief: Record<string, unknown>;
  status: string;
}

function clamp01(value: number): number {
  return Math.min(1, Math.max(0, value));
}

function round4(value: number): number {
  return Math.round(value * 10_000) / 10_000;
}

/** A scalar brief value; nested objects and arrays have no place on the card. */
function scalar(value: unknown): value is string | number | boolean {
  return typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean';
}

/** #331: a region is usable when it has an area and finite coordinates. */
function usableRegion(region: CropRegion | undefined): region is CropRegion {
  if (!region) return false;
  return [region.x, region.y, region.width, region.height].every(Number.isFinite)
    && region.width > 0 && region.height > 0;
}

/** #331: the normalized union of two regions, like the design fixes it. */
export function unionRegion(first: CropRegion, second: CropRegion): CropRegion {
  const x = Math.min(first.x, second.x);
  const y = Math.min(first.y, second.y);
  return {
    x,
    y,
    width: Math.max(first.x + first.width, second.x + second.width) - x,
    height: Math.max(first.y + first.height, second.y + second.height) - y,
  };
}

/** #331: a reference's identity while a merge de-duplicates the two sides. */
function referenceIdentity(reference: AssetListReference): string {
  if (reference.input_asset_id) return `input:${reference.input_asset_id}`;
  if (reference.asset_id) return `asset:${reference.asset_id}`;
  return `task:${reference.task_id ?? ''}`;
}

/**
 * #331: the item's crop reference is its last input-asset reference, matching
 * the server's "last cut picture" rule. A merge keeps every other reference and
 * drops this one: the merged item gets its own new cut.
 */
function cropReferenceIndex(references: AssetListReference[]): number {
  for (let index = references.length - 1; index >= 0; index -= 1) {
    if (references[index].input_asset_id) return index;
  }
  return -1;
}

/**
 * #327: one production list as the chat and the project panel show it. The
 * card renders the list's own states and kinds from the Product Schema, offers
 * the confirm/reject decision while the list is an unexpired draft, and turns
 * a 409 conflict into the replace decision that resolves it.
 *
 * #331: an unexpired draft is editable: every item shows its reference
 * thumbnail, can be renamed or deleted, and two items with a resolved crop
 * origin can be merged into one re-cut over their united region. Saving is one
 * whole-list replacement; every other status stays read-only.
 */
export function AssetListCard({
  detail, schema, projectId, client, variant = 'chat', onDecided, onRunChanged, onEdited, onStale,
}: AssetListCardProps) {
  const { previewAssetUrl } = useAssetURLRewriter();
  const [pending, setPending] = useState<AssetListDecision | null>(null);
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [error, setError] = useState<string | null>(null);
  // #328: run controls are a second, independent busy axis, and stop asks first.
  const [runPending, setRunPending] = useState<AssetListRunAction | null>(null);
  const [stopConfirm, setStopConfirm] = useState(false);
  const [skipPending, setSkipPending] = useState<string | null>(null);
  // #331: a draft list being edited. Null means the read-only rendering.
  const [draft, setDraft] = useState<DraftItem[] | null>(null);
  // #331: the snapshot every save is built from, taken when editing began.
  const [baseline, setBaseline] = useState<EditBaseline | null>(null);
  const [mergeSelection, setMergeSelection] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  // #331: the source picture's drawing surface: a live drag, a drawn region
  // waiting for a name, and the item a drag re-crops next.
  const [drag, setDrag] = useState<CropDrag | null>(null);
  const [pendingCrop, setPendingCrop] = useState<PendingCrop | null>(null);
  const [recropKey, setRecropKey] = useState<string | null>(null);
  const [newName, setNewName] = useState('');
  const [newKind, setNewKind] = useState('');
  const [cropError, setCropError] = useState('');
  const [naturalSourceSize, setNaturalSourceSize] = useState<{ width: number; height: number } | null>(null);
  const overlay = useRef<HTMLDivElement | null>(null);
  const newItemKey = useRef(0);
  const run = detail.run ?? null;
  const text = (key: string) => schemaText(schema, key);
  const busy = pending !== null;
  const expired = detail.effective_status === 'expired';
  const statusLabel = expired
    ? text('ui.asset_list.expired')
    : schema.asset_list?.states.find((state) => state.id === detail.effective_status)?.label
      ?? detail.effective_status;
  const items = Array.isArray(detail.items) ? detail.items : [];
  const itemCount = typeof detail.item_count === 'number' ? detail.item_count : items.length;
  const itemsCountText = schemaCountMessage(schema, 'ui.asset_list.items_count', itemCount, { count: itemCount });
  const plannedStepsText = formatSchemaText(text('ui.asset_list.planned_steps'), { steps: detail.planned_step_count });
  const model3dStepsText = detail.planned_model3d_step_count > 0
    ? formatSchemaText(text('ui.asset_list.planned_model3d_steps'), { steps: detail.planned_model3d_step_count })
    : '';
  const briefFields = schema.chat.model3d_usage?.brief?.fields ?? [];
  const briefEntries = Object.entries(detail.brief ?? {})
    .filter((entry): entry is [string, string | number | boolean] => scalar(entry[1]))
    .slice(0, 3);
  const briefLabel = (key: string) => briefFields.find((field) => field.name === key)?.label || key;
  const itemUnavailable = (item: AssetListItem) =>
    Array.isArray(item.route) && item.route.some((step) => step.available === false);
  const unavailableCount = items.filter(itemUnavailable).length;
  const availabilityWarning = unavailableCount > 0
    ? schemaCountMessage(schema, 'ui.asset_list.availability_warning', unavailableCount, { count: unavailableCount })
    : '';
  const kindLabel = (id: string) => schema.asset_list?.kinds.find((kind) => kind.id === id)?.label || id;
  const itemStateLabel = (id: string) => schema.asset_list?.item_states.find((state) => state.id === id)?.label || id;
  const hint = expired
    ? text('ui.asset_list.expired')
    : detail.status === 'draft'
      ? text('ui.asset_list.pending_hint')
      : detail.status === 'confirmed'
        ? text('ui.asset_list.confirmed_hint')
        : detail.status === 'rejected'
          ? text('ui.asset_list.rejected_hint')
          : '';
  const canDecide = detail.status === 'draft' && !expired;
  // #331: only an unexpired, un-replaced draft is editable; every other status
  // stays read-only.
  const canEdit = canDecide && !detail.superseded_by;
  // #331: the shared cache advanced past the snapshot this draft was built from
  // (another tab, the Agent, or a parent re-read). Saving would submit a list
  // the person never edited, so the editor locks until an explicit reload.
  const editOutdated = Boolean(draft && baseline
    && (detail.id !== baseline.id || detail.version !== baseline.version));
  // #331: a drawn box waiting for its name, or a drag still in flight, is an
  // unfinished gesture: saving and the entries that would silently clear it are
  // held back, and only an explicit cancel drops the typed name.
  const cropFormOpen = Boolean(pendingCrop || drag);
  const saveDisabled = saving || editOutdated || cropFormOpen;
  const nameMaxLength = schema.asset_list?.limits.name_max_length;
  // #328: only a confirmed, live list runs; stopped and completed are final.
  // Start needs the run row created at confirm: its idle state is the only one
  // with a version token to send. A missing run (execution disabled, or a lost
  // EnsureRun) has nothing to start, so no start button is offered.
  const canStart = detail.status === 'confirmed' && !expired && run?.status === 'idle';
  const canPause = detail.status === 'confirmed' && !expired && run?.status === 'running';
  const canResume = detail.status === 'confirmed' && !expired && run?.status === 'paused';
  const canStop = detail.status === 'confirmed' && !expired && Boolean(run) && !runIsTerminal(run);
  const showRunControls = canStart || canPause || canResume || canStop;
  const canSkip = (item: AssetListItem) =>
    Boolean(run) && run?.status !== 'stopped' && (item.state === 'todo' || item.state === 'working');
  // A server detail that names a Schema message is shown in the active locale;
  // anything else falls back to the card's own failure copy.
  const failureMessage = (failure: unknown, fallback = 'ui.asset_list.decide_failed') => {
    const detailText = failure instanceof AssetListError
      ? failure.detail
      : failure instanceof Error ? failure.message : '';
    return (detailText ? text(detailText) : '') || text(fallback);
  };
  // #331: an edit refusal is the server's Schema copy already (region invalid,
  // too small, a bad reference, the image item limit); a Schema key is
  // translated, any other server detail is shown as sent. A network failure
  // has no server detail and falls back to the card's own copy.
  const editFailureMessage = (failure: unknown) => {
    const serverDetail = failure instanceof AssetListError ? failure.detail : '';
    return text(serverDetail) || serverDetail || text('ui.asset_list.edit_save_failed');
  };

  // #331: the item's last input-asset reference is its reference picture,
  // matching the server's "last cut picture" rule. Task-output references have
  // no input picture; the card then shows no tile.
  const inputAssetURL = (assetID: string): string => {
    const template = schema.runtime.http.routes.asset_item;
    if (!assetID || !template) return '';
    return previewAssetUrl(template.replace('{assetID}', encodeURIComponent(assetID)));
  };
  const referenceImageURL = (references: AssetListReference[]): string => {
    const index = cropReferenceIndex(references);
    return index >= 0 ? inputAssetURL(references[index].input_asset_id ?? '') : '';
  };
  // #331: every reference of a merged item except its own crop reference.
  const otherReferences = (entry: DraftItem): AssetListReference[] => {
    const index = cropReferenceIndex(entry.references);
    return index >= 0 ? entry.references.filter((_, position) => position !== index) : entry.references;
  };
  // #331: the detail carries the provenance of the item's crop reference; an
// item without one (no crop, or an older server) offers no merge entry.
  const originOf = (item: AssetListItem): AssetListCrop | undefined => {
    const origin = item.crop;
    if (!origin || !origin.source_input_asset_id || !usableRegion(origin.region)) return undefined;
    return origin;
  };
  const canMerge = (entry: DraftItem) =>
    Boolean(entry.origin) && entry.references.some((reference) => Boolean(reference.input_asset_id));

  // #331: the source picture is the first resolvable crop origin, editing or
  // read-only. The server keeps one source picture per list; if an older
  // local state ever mixed sources, the other source's boxes are not drawn and
  // the card explains why. A list with no origins has no picture to draw on.
  const draftOrigins = (draft ?? []).map((entry) => entry.origin);
  const readOnlyOrigins = items.map(originOf);
  const origins = draft ? draftOrigins : readOnlyOrigins;
  const sourceOrigin = origins.find(
    (origin): origin is AssetListCrop => Boolean(origin?.source_input_asset_id && usableRegion(origin.region)),
  );
  const sourceID = sourceOrigin?.source_input_asset_id ?? '';
  const sourceImageURL = inputAssetURL(sourceID);
  const sourceBoxes = (draft
    ? draft.flatMap((entry, index) => (
      entry.origin && entry.origin.source_input_asset_id === sourceID && usableRegion(entry.origin.region)
        ? [{ index, name: entry.name, region: entry.origin.region }]
        : []
    ))
    : items.flatMap((item, index) => {
      const origin = originOf(item);
      return origin && origin.source_input_asset_id === sourceID
        ? [{ index, name: item.name, region: origin.region }]
        : [];
    }));
  const mixedSources = Boolean(sourceID) && origins.some(
    (origin) => Boolean(origin?.source_input_asset_id) && origin?.source_input_asset_id !== sourceID,
  );
  // The crop tool measures the minimum short edge in source pixels; the
  // detail's provenance carries the source size. An older server without it
  // falls back to the image's natural size, then to the display size.
  const measuredWidth = Math.max(0, ...origins.map((origin) => origin?.source_width ?? 0));
  const measuredHeight = Math.max(0, ...origins.map((origin) => origin?.source_height ?? 0));
  const sourceSize = measuredWidth > 0 && measuredHeight > 0
    ? { width: measuredWidth, height: measuredHeight }
    : naturalSourceSize;
  const maxItemsPerImage = schema.asset_list?.limits.maximum_items_per_image;
  // The current bound from the public Schema; an older server omits it.
  const minimumCropShortEdge = schema.asset_list?.limits.minimum_crop_short_edge ?? MIN_CROP_SHORT_EDGE;
  const sourceItemCount = draft
    ? draft.filter((entry) => entry.origin?.source_input_asset_id === sourceID).length
    : 0;
  const regionStyle = (region: CropRegion) => ({
    left: `${region.x * 100}%`,
    top: `${region.y * 100}%`,
    width: `${region.width * 100}%`,
    height: `${region.height * 100}%`,
  });

  /** #331: leave every transient crop gesture; the draft itself is untouched. */
  const cancelCropGesture = () => {
    setDrag(null);
    setPendingCrop(null);
    setRecropKey(null);
    setCropError('');
  };

  const startEditing = () => {
    setError(null);
    setMergeSelection([]);
    cancelCropGesture();
    setBaseline({
      id: detail.id,
      version: detail.version,
      title: detail.title,
      brief: detail.brief,
      status: detail.status,
    });
    setDraft(items.map((item): DraftItem => ({
      key: item.id,
      kind: item.kind,
      name: item.name,
      description: item.description ?? '',
      routeID: item.route_id,
      references: Array.isArray(item.references) ? item.references : [],
      state: item.state,
      origin: originOf(item),
    })));
  };

  const cancelEditing = () => {
    setDraft(null);
    setBaseline(null);
    setMergeSelection([]);
    setError(null);
    cancelCropGesture();
  };

  /** #331: the explicit reload a locked editor asks for: discard the draft and
   * rebuild it from the latest detail, then let the parent re-read the list. */
  const reloadLatest = () => {
    startEditing();
    onStale?.();
  };

  const renameDraft = (key: string, name: string) => {
    setDraft((current) => current?.map((entry) => (entry.key === key ? { ...entry, name } : entry)) ?? null);
  };

  const deleteDraft = (key: string) => {
    setDraft((current) => (current && current.length > 1 ? current.filter((entry) => entry.key !== key) : current));
    setMergeSelection((current) => current.filter((entry) => entry !== key));
  };

  const toggleMerge = (key: string) => {
    setMergeSelection((current) => current.includes(key)
      ? current.filter((entry) => entry !== key)
      : current.length >= 2 ? current : [...current, key]);
  };

  const mergeSelectionItems = (draft ?? []).filter((entry) => mergeSelection.includes(entry.key));
  const [mergeFirst, mergeSecond] = mergeSelectionItems;
  const mergeHasOrigins = mergeSelection.length === 2
    && mergeSelectionItems.length === 2
    && Boolean(mergeFirst.origin)
    && Boolean(mergeSecond.origin);
  const mergeReady = mergeHasOrigins
    && mergeFirst.origin!.source_input_asset_id === mergeSecond.origin!.source_input_asset_id;
  const mergeMismatch = mergeHasOrigins && !mergeReady;

  // Merge is local until save: the first selected item keeps its name,
  // description, kind, route and references, and carries one crop instruction
  // over the united region; the second item's plain references (everything but
  // its own crop picture) join the first, de-duplicated; the second item leaves
  // the list.
  const mergeSelected = () => {
    if (!draft || !mergeReady) return;
    const first = mergeFirst;
    const second = mergeSecond;
    const origin: AssetListCrop = {
      ...first.origin!,
      region: unionRegion(first.origin!.region, second.origin!.region),
    };
    const crop: CropInstruction = {
      source_input_asset_id: origin.source_input_asset_id,
      region: origin.region,
    };
    const references = [...first.references, ...otherReferences(second)].filter(
      (reference, position, all) => all.findIndex(
        (candidate) => referenceIdentity(candidate) === referenceIdentity(reference),
      ) === position,
    );
    setDraft(draft
      .filter((entry) => entry.key !== second.key)
      .map((entry) => (entry.key === first.key ? { ...entry, references, origin, crop } : entry)));
    setMergeSelection([]);
  };

  // #331: a drag is normalized against the displayed picture, so the same
  // gesture works at any rendered size. All four edges are rounded to four
  // decimals before the region is stored or submitted.
  const regionFromDrag = (state: CropDrag): CropRegion => {
    const left = round4(clamp01(Math.min(state.startX, state.currentX)));
    const top = round4(clamp01(Math.min(state.startY, state.currentY)));
    const right = round4(clamp01(Math.max(state.startX, state.currentX)));
    const bottom = round4(clamp01(Math.max(state.startY, state.currentY)));
    return { x: left, y: top, width: round4(right - left), height: round4(bottom - top) };
  };

  const cropTooSmall = (region: CropRegion, display: { width: number; height: number }): boolean => {
    const sourceWidth = sourceSize?.width || display.width;
    const sourceHeight = sourceSize?.height || display.height;
    return region.width * sourceWidth < minimumCropShortEdge
      || region.height * sourceHeight < minimumCropShortEdge;
  };

  const pointerPoint = (event: ReactPointerEvent<HTMLDivElement>) => {
    const rect = overlay.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) return null;
    return {
      x: clamp01((event.clientX - rect.left) / rect.width),
      y: clamp01((event.clientY - rect.top) / rect.height),
      width: rect.width,
      height: rect.height,
    };
  };

  const startCropDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    // A naming form is an unfinished gesture: a new drag may not silently drop
    // the typed name; the person confirms or cancels it first.
    if (!draft || saving || pendingCrop) return;
    if (event.pointerType === 'mouse' && event.button !== 0) return;
    const point = pointerPoint(event);
    if (!point) return;
    event.preventDefault();
    // Capture keeps the drag alive when the pointer leaves the picture; jsdom
    // and older browsers do not implement it.
    try { event.currentTarget.setPointerCapture?.(event.pointerId); } catch { /* no capture */ }
    setPendingCrop(null);
    setCropError('');
    setDrag({
      startX: point.x,
      startY: point.y,
      currentX: point.x,
      currentY: point.y,
      width: point.width,
      height: point.height,
    });
  };

  const moveCropDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!drag) return;
    const point = pointerPoint(event);
    if (!point) return;
    setDrag({ ...drag, currentX: point.x, currentY: point.y });
  };

  const applyRecrop = (key: string, region: CropRegion) => {
    if (!sourceID) return;
    const crop: CropInstruction = { source_input_asset_id: sourceID, region };
    setDraft((current) => current?.map((entry) => (entry.key === key
      ? { ...entry, origin: { ...entry.origin, source_input_asset_id: sourceID, region }, crop }
      : entry)) ?? null);
    setRecropKey(null);
    setCropError('');
  };

  const finishCropDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!drag) return;
    try {
      if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
        event.currentTarget.releasePointerCapture?.(event.pointerId);
      }
    } catch { /* no capture */ }
    const finished = drag;
    setDrag(null);
    const region = regionFromDrag(finished);
    if (!usableRegion(region)) return;
    if (cropTooSmall(region, finished)) {
      setCropError(formatSchemaText(text('ui.asset_list.edit_crop_too_small'), { minimum: minimumCropShortEdge }));
      return;
    }
    if (recropKey) {
      applyRecrop(recropKey, region);
      return;
    }
    // The drawn region is one pending new item: the edited list already holds
    // `sourceItemCount` items of this source, so the addition would be
    // `sourceItemCount + 1`. Merging and re-cropping never reach this check and
    // never increase the count.
    if (maxItemsPerImage && sourceItemCount + 1 > maxItemsPerImage) {
      setCropError(formatSchemaText(text('ui.asset_list.edit_crop_image_limit'), { maximum: maxItemsPerImage }));
      return;
    }
    setPendingCrop({ sourceID, region });
    setNewName('');
    setNewKind(schema.asset_list?.kinds[0]?.id ?? '');
  };

  const confirmPendingCrop = () => {
    if (!draft || !pendingCrop) return;
    const name = newName.trim();
    if (!name || !newKind) return;
    // The Schema can change while the form is open; the pending item is the
    // addition the limit counts.
    if (maxItemsPerImage && sourceItemCount + 1 > maxItemsPerImage) {
      setCropError(formatSchemaText(text('ui.asset_list.edit_crop_image_limit'), { maximum: maxItemsPerImage }));
      return;
    }
    const crop: CropInstruction = { source_input_asset_id: pendingCrop.sourceID, region: pendingCrop.region };
    newItemKey.current += 1;
    setDraft([...draft, {
      key: `new-${newItemKey.current}`,
      kind: newKind,
      name,
      description: '',
      routeID: '',
      references: [],
      state: 'todo',
      origin: crop,
      crop,
    }]);
    setPendingCrop(null);
    setNewName('');
    setCropError('');
  };

  // #331: Esc leaves whichever crop gesture is in flight (a drag, the naming
  // form, or a re-crop); it never leaves the whole edit. The listener runs in
  // the capture phase and prevents the default so an enclosing SideDrawer,
  // which closes on a not-prevented Escape, stays open for as long as the
  // gesture lasts. With no gesture the listener is not installed and Escape
  // keeps its usual meaning.
  const selectingCrop = Boolean(drag || pendingCrop || recropKey);
  useEffect(() => {
    if (!draft || !selectingCrop) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      setDrag(null);
      setPendingCrop(null);
      setRecropKey(null);
      setCropError('');
    };
    window.addEventListener('keydown', onKeyDown, { capture: true });
    return () => window.removeEventListener('keydown', onKeyDown, { capture: true });
  }, [draft, selectingCrop]);

  // #331: a draft stays editable only while the card still shows the same,
  // unexpired draft the baseline was taken from. A decision elsewhere, an
  // expiry, a replacement or a different list ends the edit and its gestures;
  // the parent re-reads its summary so the card and the panel agree.
  useEffect(() => {
    if (!draft) return;
    const editable = detail.status === 'draft'
      && detail.effective_status === 'draft'
      && !detail.superseded_by;
    if (editable && detail.id === baseline?.id) return;
    setDraft(null);
    setBaseline(null);
    setMergeSelection([]);
    setDrag(null);
    setPendingCrop(null);
    setRecropKey(null);
    setCropError('');
    setError(null);
    onStale?.();
  }, [draft, baseline, detail.id, detail.status, detail.effective_status, detail.superseded_by, onStale]);

  const save = async () => {
    // The baseline is the edited list: a cache that advanced under the draft,
    // an unfinished gesture or an in-flight save all hold the request back.
    if (!draft || !baseline || saveDisabled) return;
    setSaving(true);
    setError(null);
    try {
      const payload: AssetListEditInput = {
        // The snapshot, not the live detail: the request replaces exactly the
        // list the person edited.
        title: baseline.title,
        expected_version: baseline.version,
        // The route replaces the brief too; omitting it would clear it.
        brief: baseline.brief,
        items: draft.map((entry): AssetListItemInput => ({
          kind: entry.kind,
          name: entry.name,
          description: entry.description,
          // An item drawn by this editor names no route: the server picks the
          // reference route for its crop, or the kind's default route.
          ...(entry.routeID ? { route_id: entry.routeID } : {}),
          references: entry.references,
          ...(entry.crop ? { crop: entry.crop } : {}),
        })),
      };
      const updated = await client.edit(projectId, baseline.id, payload);
      setDraft(null);
      setBaseline(null);
      setMergeSelection([]);
      cancelCropGesture();
      onEdited?.(updated);
    } catch (failure) {
      const code = failure instanceof AssetListError ? failure.code : '';
      if (failure instanceof AssetListError && failure.status === 409 && code === 'conflict') {
        // A concurrent edit is not overwritten: the parent re-reads the list,
        // the local draft stays on screen, and the reload action appears once
        // the new detail arrives.
        setError(text('ui.asset_list.edit_conflict'));
        onStale?.();
      } else if (code === 'asset_list_expired') {
        // The server names the expiry; show its copy and leave the editor, the
        // draft can no longer be stored.
        setError(editFailureMessage(failure));
        setDraft(null);
        setBaseline(null);
        setMergeSelection([]);
        cancelCropGesture();
        onStale?.();
      } else {
        // Any other refusal (or a network failure) shows the server's own
        // detail, or the card's retry copy when there is none. No reload is
        // promised here.
        setError(editFailureMessage(failure));
      }
    } finally {
      setSaving(false);
    }
  };

  const decide = async (decision: AssetListDecision) => {
    if (busy) return;
    setPending(decision);
    setError(null);
    try {
      await client.decide(projectId, detail.id, decision);
      setConflict(null);
      onDecided?.();
    } catch (failure) {
      if (failure instanceof AssetListError && failure.status === 409 && failure.activeListId) {
        setConflict({ id: failure.activeListId, title: failure.activeListTitle || failure.activeListId });
        return;
      }
      setError(failureMessage(failure));
    } finally {
      setPending(null);
    }
  };

  // #328: expected_version is the run's opaque version token this card
  // rendered; the server refuses a stale card instead of overwriting a newer
  // run. The list's numeric version belongs to decisions and edits.
  const control = async (action: AssetListRunAction) => {
    if (runPending || !run) return;
    setRunPending(action);
    setError(null);
    try {
      await client.controlRun(projectId, detail.id, action, run.version);
      setStopConfirm(false);
      onRunChanged?.();
    } catch (failure) {
      setError(failureMessage(failure));
    } finally {
      setRunPending(null);
    }
  };

  // Skipping is a per-item CAS on the item's own version, not the list's.
  const skip = async (item: AssetListItem) => {
    if (skipPending) return;
    setSkipPending(item.id);
    setError(null);
    try {
      await client.skipItem(projectId, detail.id, item.id, item.version);
      onRunChanged?.();
    } catch (failure) {
      setError(failureMessage(failure));
    } finally {
      setSkipPending(null);
    }
  };

  return (
    <article
      className="flex flex-col gap-1.5 rounded-lg border border-border bg-panel-raised p-3 text-xs"
      data-testid="asset-list-card"
      data-status={detail.effective_status}
      data-variant={variant}
      data-editing={draft ? 'true' : undefined}
      aria-label={text(variant === 'chat' ? 'ui.asset_list.chat_card_title' : 'ui.asset_list.section_title')}
    >
      <header className="flex items-center gap-1.5">
        <ListChecks aria-hidden className="size-4 shrink-0 text-primary" />
        <span className="min-w-0 truncate text-[13px] font-semibold [overflow-wrap:anywhere]">{detail.title}</span>
        <span
          className="ml-auto shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground"
          data-testid="asset-list-status"
        >
          {statusLabel}
        </span>
      </header>
      <p className="flex flex-wrap gap-x-2 tabular-nums text-muted-foreground" data-testid="asset-list-counts">
        <span>{itemsCountText}</span>
        <span>{plannedStepsText}</span>
        {model3dStepsText ? <span>{model3dStepsText}</span> : null}
      </p>
      {run ? (
        <p className="flex flex-wrap items-baseline gap-x-2 tabular-nums text-muted-foreground" data-testid="asset-list-run">
          <span
            className="shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-foreground"
            data-testid="asset-list-run-status"
            data-status={run.status}
            data-pause-reason={run.pause_reason || undefined}
          >
            {runStatusText(schema, run)}
          </span>
          <span data-testid="asset-list-run-progress">{runProgressText(schema, run)}</span>
        </p>
      ) : null}
      {briefEntries.length > 0 ? (
        <section className="flex flex-col gap-0.5" data-testid="asset-list-brief">
          <span className="text-[11px] text-foreground/70">{text('ui.asset_list.brief_summary')}</span>
          <dl className="flex flex-col gap-0.5 text-[11px] text-muted-foreground">
            {briefEntries.map(([key, value]) => (
              <div key={key} className="flex min-w-0 gap-1.5">
                <dt className="shrink-0 text-foreground/70">{briefLabel(key)}</dt>
                <dd className="min-w-0 [overflow-wrap:anywhere]">{String(value)}</dd>
              </div>
            ))}
          </dl>
        </section>
      ) : null}
      {availabilityWarning ? (
        <p className="text-warning" data-testid="asset-list-availability-warning">{availabilityWarning}</p>
      ) : null}
      {detail.status === 'draft' ? (
        <section className="flex flex-col gap-1" data-testid="asset-list-source">
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="text-[11px] text-foreground/70">{text('ui.asset_list.edit_crop_title')}</span>
            {draft && sourceID ? (
              <Action
                type="button"
                variant="outline"
                size="xs"
                className="ml-auto"
                disabled={saving || cropFormOpen}
                onClick={() => {
                  setRecropKey(null);
                  setPendingCrop(null);
                  setCropError('');
                  overlay.current?.focus();
                }}
                data-testid="asset-list-crop-add"
              >
                {text('ui.asset_list.edit_crop_add')}
              </Action>
            ) : null}
          </div>
          {sourceImageURL ? (
            <>
              <div className="relative w-fit max-w-full self-center" data-testid="asset-list-source-frame">
                <img
                  src={sourceImageURL}
                  alt={text('ui.asset_list.edit_crop_source')}
                  className="block max-h-64 w-auto max-w-full rounded-md border border-border bg-muted"
                  onLoad={(event) => {
                    const image = event.currentTarget;
                    if (image.naturalWidth > 0 && image.naturalHeight > 0) {
                      setNaturalSourceSize({ width: image.naturalWidth, height: image.naturalHeight });
                    }
                  }}
                  data-testid="asset-list-source-image"
                />
                <div
                  ref={overlay}
                  role="group"
                  tabIndex={draft ? 0 : undefined}
                  aria-label={text(draft ? 'ui.asset_list.edit_crop_hint' : 'ui.asset_list.edit_crop_title')}
                  className={`absolute inset-0 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-primary/60 ${
                    draft ? 'cursor-crosshair touch-none' : ''
                  }`}
                  onPointerDown={draft ? startCropDrag : undefined}
                  onPointerMove={draft ? moveCropDrag : undefined}
                  onPointerUp={draft ? finishCropDrag : undefined}
                  onPointerCancel={draft ? cancelCropGesture : undefined}
                  data-testid="asset-list-crop-overlay"
                >
                  {sourceBoxes.map((box) => {
                    const targeted = Boolean(draft && recropKey && draft[box.index]?.key === recropKey);
                    return (
                      <div
                        key={box.index}
                        className={`absolute rounded-sm border ${
                          targeted ? 'border-warning bg-warning/15' : 'border-primary bg-primary/10'
                        }`}
                        style={regionStyle(box.region)}
                        aria-hidden
                        data-testid={`asset-list-crop-box-${box.index}`}
                      >
                        <HintTooltip name={box.name} className="absolute left-0 top-0">
                          <span className="rounded-sm bg-primary px-0.5 text-[9px] leading-3 text-primary-foreground">
                            {box.index + 1}
                          </span>
                        </HintTooltip>
                      </div>
                    );
                  })}
                  {drag ? (
                    <div
                      className="absolute rounded-sm border border-dashed border-primary bg-primary/15"
                      style={regionStyle(regionFromDrag(drag))}
                      aria-hidden
                      data-testid="asset-list-crop-drag"
                    />
                  ) : pendingCrop ? (
                    <div
                      className="absolute rounded-sm border border-dashed border-primary bg-primary/15"
                      style={regionStyle(pendingCrop.region)}
                      aria-hidden
                      data-testid="asset-list-crop-pending"
                    />
                  ) : null}
                </div>
              </div>
              {draft ? (
                <div className="flex flex-wrap items-center gap-1.5 text-[11px] leading-4 text-muted-foreground">
                  <p data-testid="asset-list-crop-hint">
                    {text(recropKey ? 'ui.asset_list.edit_crop_recrop_hint' : 'ui.asset_list.edit_crop_hint')}
                  </p>
                  {recropKey ? (
                    <Action
                      type="button"
                      variant="ghost"
                      size="xs"
                      onClick={cancelCropGesture}
                      data-testid="asset-list-crop-cancel"
                    >
                      {text('ui.asset_list.edit_crop_cancel')}
                    </Action>
                  ) : null}
                </div>
              ) : null}
              {mixedSources ? (
                <p className="text-warning" data-testid="asset-list-crop-mixed">
                  {text('ui.asset_list.edit_crop_mixed_source')}
                </p>
              ) : null}
            </>
          ) : (
            <p
              className="text-[11px] leading-4 text-muted-foreground"
              data-testid="asset-list-crop-no-source"
            >
              {text('ui.asset_list.edit_crop_no_source')}
            </p>
          )}
          {draft && pendingCrop ? (
            <div
              className="flex flex-wrap items-end gap-1.5 rounded-md border border-border p-2 text-[11px]"
              data-testid="asset-list-crop-form"
              data-region={`${pendingCrop.region.x},${pendingCrop.region.y},${pendingCrop.region.width},${pendingCrop.region.height}`}
            >
              <label className="flex min-w-0 flex-col gap-0.5 text-muted-foreground">
                {text('ui.asset_list.edit_name')}
                <input
                  type="text"
                  autoFocus
                  value={newName}
                  maxLength={nameMaxLength}
                  onChange={(event) => setNewName(event.target.value)}
                  className="min-w-0 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground"
                  data-testid="asset-list-crop-name"
                />
              </label>
              <label className="flex flex-col gap-0.5 text-muted-foreground">
                {text('ui.asset_list.edit_kind')}
                <select
                  value={newKind}
                  onChange={(event) => setNewKind(event.target.value)}
                  className="rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground"
                  data-testid="asset-list-crop-kind"
                >
                  {(schema.asset_list?.kinds ?? []).map((kind) => (
                    <option key={kind.id} value={kind.id}>{kind.label}</option>
                  ))}
                </select>
              </label>
              {newName.trim() ? null : (
                <p className="text-warning" data-testid="asset-list-crop-name-required">
                  {text('ui.asset_list.edit_crop_name_required')}
                </p>
              )}
              <div className="ml-auto flex items-center gap-1.5">
                <Action
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={cancelCropGesture}
                  data-testid="asset-list-crop-form-cancel"
                >
                  {text('ui.common.cancel')}
                </Action>
                <Action
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={!newName.trim() || !newKind}
                  onClick={confirmPendingCrop}
                  data-testid="asset-list-crop-form-add"
                >
                  {text('ui.common.confirm')}
                </Action>
              </div>
            </div>
          ) : null}
          {cropError ? (
            <p role="alert" className="text-warning" data-testid="asset-list-crop-error">{cropError}</p>
          ) : null}
        </section>
      ) : null}
      {draft ? (
        <ul className="flex flex-col gap-0.5" data-testid="asset-list-items">
          {draft.map((entry, index) => {
            const thumbnail = referenceImageURL(entry.references);
            const selected = mergeSelection.includes(entry.key);
            return (
              <li
                key={entry.key}
                className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md border px-1.5 py-1 ${
                  selected ? 'border-primary' : 'border-border/70'
                }`}
                data-testid={`asset-list-item-${index}`}
                data-kind={entry.kind}
                data-state={entry.state}
                data-merge-selected={selected ? 'true' : undefined}
              >
                {thumbnail ? (
                  <span className="size-8 shrink-0 overflow-hidden rounded border border-border bg-muted">
                    <img
                      src={thumbnail}
                      alt={text('ui.asset_list.edit_reference')}
                      className="size-full object-cover"
                      data-testid={`asset-list-item-thumb-${index}`}
                    />
                  </span>
                ) : null}
                <input
                  type="text"
                  value={entry.name}
                  maxLength={nameMaxLength}
                  aria-label={text('ui.asset_list.edit_name')}
                  onChange={(event) => renameDraft(entry.key, event.target.value)}
                  className="min-w-0 flex-1 rounded border border-border bg-background px-1.5 py-0.5 text-xs"
                  data-testid={`asset-list-item-name-${index}`}
                />
                <span className="text-muted-foreground">{kindLabel(entry.kind)}</span>
                {entry.description ? (
                  <span className="min-w-0 text-muted-foreground [overflow-wrap:anywhere]">{entry.description}</span>
                ) : null}
                {sourceID ? (
                  <Action
                    type="button"
                    variant="ghost"
                    size="xs"
                    className={canMerge(entry) ? '' : 'ml-auto'}
                    aria-pressed={recropKey === entry.key}
                    disabled={saving || cropFormOpen}
                    onClick={() => {
                      setRecropKey(entry.key);
                      setPendingCrop(null);
                      setDrag(null);
                      setCropError('');
                    }}
                    data-testid={`asset-list-item-recrop-${index}`}
                  >
                    {text('ui.asset_list.edit_crop_recrop')}
                  </Action>
                ) : null}
                {canMerge(entry) ? (
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    className={sourceID ? '' : 'ml-auto'}
                    aria-pressed={selected}
                    disabled={saving || (!selected && mergeSelection.length >= 2)}
                    onClick={() => toggleMerge(entry.key)}
                    data-testid={`asset-list-item-merge-${index}`}
                  >
                    {text('ui.asset_list.edit_merge')}
                  </Action>
                ) : null}
                <Action
                  type="button"
                  variant="ghost"
                  size="xs"
                  className={canMerge(entry) || sourceID ? '' : 'ml-auto'}
                  disabled={saving || draft.length <= 1}
                  onClick={() => deleteDraft(entry.key)}
                  data-testid={`asset-list-item-delete-${index}`}
                >
                  {text('ui.asset_list.edit_delete')}
                </Action>
              </li>
            );
          })}
        </ul>
      ) : items.length > 0 ? (
        <ul className="flex flex-col gap-0.5" data-testid="asset-list-items">
          {items.map((item, index) => {
            const thumbnail = detail.status === 'draft' ? referenceImageURL(item.references) : '';
            return (
              <li
                key={item.id || index}
                className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 rounded-md border border-border/70 px-1.5 py-1"
                data-testid={`asset-list-item-${index}`}
                data-kind={item.kind}
                data-state={item.state}
              >
                {thumbnail ? (
                  <span className="size-8 shrink-0 self-center overflow-hidden rounded border border-border bg-muted">
                    <img
                      src={thumbnail}
                      alt={text('ui.asset_list.edit_reference')}
                      className="size-full object-cover"
                      data-testid={`asset-list-item-thumb-${index}`}
                    />
                  </span>
                ) : null}
                <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{item.name}</span>
                <span className="text-muted-foreground">{kindLabel(item.kind)}</span>
                {detail.status === 'draft' && item.description ? (
                  <span className="min-w-0 text-muted-foreground [overflow-wrap:anywhere]">{item.description}</span>
                ) : null}
                <span className="text-muted-foreground">{itemStateLabel(item.state)}</span>
                <span className="tabular-nums text-muted-foreground">
                  {formatSchemaText(text('ui.asset_list.planned_steps'), { steps: item.planned_step_count })}
                </span>
                {item.planned_model3d_step_count > 0 ? (
                  <span className="tabular-nums text-muted-foreground">
                    {formatSchemaText(text('ui.asset_list.planned_model3d_steps'), { steps: item.planned_model3d_step_count })}
                  </span>
                ) : null}
                {itemUnavailable(item) ? (
                  <span className="text-warning" data-testid="asset-list-item-unavailable">
                    {text('ui.asset_list.item_unavailable')}
                  </span>
                ) : null}
                {canSkip(item) ? (
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    className="ml-auto"
                    disabled={skipPending !== null}
                    loading={skipPending === item.id}
                    onClick={() => void skip(item)}
                    data-testid={`asset-list-item-skip-${index}`}
                  >
                    {text('ui.asset_list.run_skip')}
                  </Action>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}
      {draft && mergeSelection.length > 0 ? (
        <div
          className="flex flex-col gap-1 rounded-md border border-border p-2 text-[11px] leading-4 text-muted-foreground"
          data-testid="asset-list-merge-panel"
        >
          <p>{text('ui.asset_list.edit_merge_hint')}</p>
          {mergeMismatch ? (
            <p className="text-warning" data-testid="asset-list-merge-mismatch">
              {text('ui.asset_list.edit_merge_source_mismatch')}
            </p>
          ) : null}
          {mergeSelection.length === 2 ? (
            <Action
              type="button"
              variant="outline"
              size="xs"
              className="self-start"
              disabled={!mergeReady}
              onClick={mergeSelected}
              data-testid="asset-list-merge"
            >
              {text('ui.asset_list.edit_merge_selected')}
            </Action>
          ) : null}
        </div>
      ) : null}
      <footer className="flex flex-col gap-1.5" data-testid="asset-list-actions">
        {hint ? <p className="text-muted-foreground" data-testid="asset-list-hint">{hint}</p> : null}
        {conflict ? (
          <p
            className="text-warning [overflow-wrap:anywhere]"
            data-testid="asset-list-conflict"
            data-active-list-id={conflict.id}
          >
            {formatSchemaText(text('ui.asset_list.active_conflict'), { title: conflict.title })}
          </p>
        ) : null}
        {draft ? (
          <div className="flex flex-col gap-1.5">
            {editOutdated ? (
              <div
                className="flex flex-wrap items-center gap-1.5 rounded-md border border-warning/50 bg-warning/10 p-2 text-[11px] leading-4 text-warning"
                data-testid="asset-list-edit-stale"
              >
                <p>{text('ui.asset_list.edit_stale')}</p>
                <Action
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={saving}
                  onClick={reloadLatest}
                  data-testid="asset-list-edit-reload"
                >
                  {text('ui.asset_list.edit_reload')}
                </Action>
              </div>
            ) : null}
            <div className="flex flex-wrap items-center gap-1.5">
              <Action
                type="button"
                size="sm"
                loading={saving}
                disabled={saveDisabled}
                onClick={() => void save()}
                data-testid="asset-list-save"
              >
                {text('ui.asset_list.edit_save')}
              </Action>
              <Action
                type="button"
                size="sm"
                variant="outline"
                disabled={saving}
                onClick={cancelEditing}
                data-testid="asset-list-cancel"
              >
                {text('ui.asset_list.edit_cancel')}
              </Action>
            </div>
          </div>
        ) : null}
        {!draft && canEdit ? (
          <div className="flex flex-wrap items-center gap-1.5">
            <Action
              type="button"
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={startEditing}
              data-testid="asset-list-edit"
            >
              {text('ui.asset_list.edit')}
            </Action>
          </div>
        ) : null}
        {!draft && (canDecide || conflict) ? (
          <div className="flex flex-wrap items-center gap-1.5">
            {canDecide ? (
              <Action
                type="button"
                size="sm"
                disabled={busy}
                loading={pending === 'confirm'}
                onClick={() => void decide('confirm')}
                data-testid="asset-list-confirm"
              >
                {text('ui.asset_list.confirm')}
              </Action>
            ) : null}
            {canDecide ? (
              <Action
                type="button"
                size="sm"
                variant="outline"
                disabled={busy}
                loading={pending === 'reject'}
                onClick={() => void decide('reject')}
                data-testid="asset-list-reject"
              >
                {text('ui.asset_list.reject')}
              </Action>
            ) : null}
            {conflict ? (
              <Action
                type="button"
                size="sm"
                disabled={busy}
                loading={pending === 'replace'}
                onClick={() => void decide('replace')}
                data-testid="asset-list-replace"
              >
                {text('ui.asset_list.replace')}
              </Action>
            ) : null}
          </div>
        ) : null}
        {showRunControls ? (
          <div className="flex flex-col gap-1.5" data-testid="asset-list-run-controls">
            <div className="flex flex-wrap items-center gap-1.5">
              {canStart ? (
                <Action
                  type="button"
                  size="sm"
                  disabled={runPending !== null}
                  loading={runPending === 'start'}
                  onClick={() => void control('start')}
                  data-testid="asset-list-run-start"
                >
                  {text('ui.asset_list.run_start')}
                </Action>
              ) : null}
              {canPause ? (
                <Action
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={runPending !== null}
                  loading={runPending === 'pause'}
                  onClick={() => void control('pause')}
                  data-testid="asset-list-run-pause"
                >
                  {text('ui.asset_list.run_pause')}
                </Action>
              ) : null}
              {canResume ? (
                <Action
                  type="button"
                  size="sm"
                  disabled={runPending !== null}
                  loading={runPending === 'resume'}
                  onClick={() => void control('resume')}
                  data-testid="asset-list-run-resume"
                >
                  {text('ui.asset_list.run_resume')}
                </Action>
              ) : null}
              {canStop ? (
                <Action
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={runPending !== null}
                  loading={runPending === 'stop' && !stopConfirm}
                  onClick={() => setStopConfirm(true)}
                  data-testid="asset-list-run-stop"
                >
                  {text('ui.asset_list.run_stop')}
                </Action>
              ) : null}
            </div>
            {stopConfirm ? (
              <div
                className="rounded-md border border-border p-2 text-[11px] leading-4 text-muted-foreground"
                data-testid="asset-list-run-stop-confirm"
              >
                <p>{text('ui.asset_list.run_stop_confirm')}</p>
                <div className="mt-1.5 flex justify-end gap-1.5">
                  <Action
                    type="button"
                    variant="ghost"
                    size="xs"
                    disabled={runPending !== null}
                    onClick={() => setStopConfirm(false)}
                    data-testid="asset-list-run-stop-cancel"
                  >
                    {text('ui.common.cancel')}
                  </Action>
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    disabled={runPending !== null}
                    loading={runPending === 'stop'}
                    onClick={() => void control('stop')}
                    data-testid="asset-list-run-stop-confirmed"
                  >
                    {text('ui.common.confirm')}
                  </Action>
                </div>
              </div>
            ) : null}
          </div>
        ) : null}
      </footer>
      {error ? (
        <p role="alert" className="text-destructive [overflow-wrap:anywhere]" data-testid="asset-list-error">{error}</p>
      ) : null}
    </article>
  );
}