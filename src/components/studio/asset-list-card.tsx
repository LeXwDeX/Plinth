'use client';

import { useState } from 'react';
import { ListChecks } from 'lucide-react';

import { Action } from '@/components/ui/button';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import {
  AssetListError,
  type AssetListClient,
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

/** #331: one item while the card is editing. */
interface DraftItem {
  /** React key and the identity merge keeps: a merged item keeps the first one. */
  key: string;
  kind: string;
  name: string;
  description: string;
  routeID: string;
  references: AssetListReference[];
  state: string;
  /** The region the item's crop was cut from, when the browser resolved it. */
  origin?: CropInstruction;
  /** An unsaved re-crop instruction to send with the edit. */
  crop?: CropInstruction;
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
  const [mergeSelection, setMergeSelection] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
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
  // #331: only an unexpired draft is editable; every other status stays read-only.
  const canEdit = canDecide;
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

  // #331: the first input-asset reference is the item's reference picture.
  // Task-output references have no input picture; the card then shows no tile.
  const referenceImageURL = (references: AssetListReference[]): string => {
    const assetID = references.find((reference) => reference.input_asset_id)?.input_asset_id ?? '';
    const template = schema.runtime.http.routes.asset_item;
    if (!assetID || !template) return '';
    return previewAssetUrl(template.replace('{assetID}', encodeURIComponent(assetID)));
  };
  // #331: the detail carries the provenance of the item's crop reference; an
// item without one (no crop, or an older server) offers no merge entry.
  const originOf = (item: AssetListItem): CropInstruction | undefined => {
    const origin = item.crop;
    if (!origin || !origin.source_input_asset_id || !usableRegion(origin.region)) return undefined;
    return origin;
  };
  const canMerge = (entry: DraftItem) =>
    Boolean(entry.origin) && entry.references.some((reference) => Boolean(reference.input_asset_id));

  const startEditing = () => {
    setError(null);
    setMergeSelection([]);
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
    setMergeSelection([]);
    setError(null);
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
  // description and references, and carries one crop instruction over the
  // united region; the second item leaves the list.
  const mergeSelected = () => {
    if (!draft || !mergeReady) return;
    const first = mergeFirst;
    const second = mergeSecond;
    const crop: CropInstruction = {
      source_input_asset_id: first.origin!.source_input_asset_id,
      region: unionRegion(first.origin!.region, second.origin!.region),
    };
    setDraft(draft
      .filter((entry) => entry.key !== second.key)
      .map((entry) => (entry.key === first.key ? { ...entry, origin: crop, crop } : entry)));
    setMergeSelection([]);
  };

  const save = async () => {
    if (!draft || saving) return;
    setSaving(true);
    setError(null);
    try {
      const payload: AssetListEditInput = {
        title: detail.title,
        expected_version: detail.version,
        // The route replaces the brief too; omitting it would clear it.
        brief: detail.brief,
        items: draft.map((entry): AssetListItemInput => ({
          kind: entry.kind,
          name: entry.name,
          description: entry.description,
          route_id: entry.routeID,
          references: entry.references,
          ...(entry.crop ? { crop: entry.crop } : {}),
        })),
      };
      const updated = await client.edit(projectId, detail.id, payload);
      setDraft(null);
      setMergeSelection([]);
      onEdited?.(updated);
    } catch (failure) {
      // A concurrent edit is not overwritten: the parent re-reads the list,
      // the local draft stays on screen and a later save uses its version.
      if (failure instanceof AssetListError && failure.status === 409) {
        setError(text('ui.asset_list.edit_conflict'));
        onStale?.();
      } else {
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
                {canMerge(entry) ? (
                  <Action
                    type="button"
                    variant="outline"
                    size="xs"
                    className="ml-auto"
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
                  className={canMerge(entry) ? '' : 'ml-auto'}
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
          <div className="flex flex-wrap items-center gap-1.5">
            <Action
              type="button"
              size="sm"
              loading={saving}
              disabled={saving}
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