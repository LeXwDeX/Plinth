'use client';

import { useState } from 'react';
import { ListChecks } from 'lucide-react';

import { Action } from '@/components/ui/button';
import {
  AssetListError,
  type AssetListClient,
  type AssetListDecision,
  type AssetListDetail,
  type AssetListItem,
  type AssetListRunAction,
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
}

type Conflict = { id: string; title: string };

/** A scalar brief value; nested objects and arrays have no place on the card. */
function scalar(value: unknown): value is string | number | boolean {
  return typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean';
}

/**
 * #327: one production list as the chat and the project panel show it. The
 * card renders the list's own states and kinds from the Product Schema, offers
 * the confirm/reject decision while the list is an unexpired draft, and turns
 * a 409 conflict into the replace decision that resolves it.
 */
export function AssetListCard({
  detail, schema, projectId, client, variant = 'chat', onDecided, onRunChanged,
}: AssetListCardProps) {
  const [pending, setPending] = useState<AssetListDecision | null>(null);
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [error, setError] = useState<string | null>(null);
  // #328: run controls are a second, independent busy axis, and stop asks first.
  const [runPending, setRunPending] = useState<AssetListRunAction | null>(null);
  const [stopConfirm, setStopConfirm] = useState(false);
  const [skipPending, setSkipPending] = useState<string | null>(null);
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
  // #328: only a confirmed, live list runs; stopped and completed are final.
  const canStart = detail.status === 'confirmed' && !expired && !run;
  const canPause = detail.status === 'confirmed' && !expired && run?.status === 'running';
  const canResume = detail.status === 'confirmed' && !expired && run?.status === 'paused';
  const canStop = detail.status === 'confirmed' && !expired && Boolean(run) && !runIsTerminal(run);
  const showRunControls = canStart || canPause || canResume || canStop;
  const canSkip = (item: AssetListItem) =>
    Boolean(run) && run?.status !== 'stopped' && (item.state === 'todo' || item.state === 'working');
  // A server detail that names a Schema message is shown in the active locale;
  // anything else falls back to the card's own failure copy.
  const failureMessage = (failure: unknown) => {
    const detailText = failure instanceof AssetListError
      ? failure.detail
      : failure instanceof Error ? failure.message : '';
    return (detailText ? text(detailText) : '') || text('ui.asset_list.decide_failed');
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

  // #328: expected_version is the list's own version this card rendered; a
  // stale card is refused by the server instead of overwriting a newer list.
  const control = async (action: AssetListRunAction) => {
    if (runPending) return;
    setRunPending(action);
    setError(null);
    try {
      await client.controlRun(projectId, detail.id, action, detail.version);
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
      {items.length > 0 ? (
        <ul className="flex flex-col gap-0.5" data-testid="asset-list-items">
          {items.map((item, index) => (
            <li
              key={item.id || index}
              className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 rounded-md border border-border/70 px-1.5 py-1"
              data-testid={`asset-list-item-${index}`}
              data-kind={item.kind}
              data-state={item.state}
            >
              <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{item.name}</span>
              <span className="text-muted-foreground">{kindLabel(item.kind)}</span>
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
          ))}
        </ul>
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
        {canDecide || conflict ? (
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