'use client';

/**
 * 3D 结果的交付格式：逐格式状态、单个下载、「下载全部 (ZIP)」与
 * 「补齐格式（约 N 积分）」。
 *
 * 清单、可补齐的格式、积分估算与不可得原因全部来自服务端的格式清单
 * （按 Product Schema 的 model_delivery 计算）；这里不持有格式表、转换
 * 关系或原因文案。补齐动作只请求服务端建立处理计划，积分只显示，不加
 * 额外流程。
 *
 * 转换计划暂停时（供给项变化、任务取消、需要同意等），这里直接给出
 * 「继续」：计划按 `plan_id` 经处理计划的单项路由读取，继续的同意与版本校验
 * 由和「加工」标签共用的 `useProcessingPlanResume` 完成。
 */

import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import useSWR from 'swr';
import { Ban, ChevronDown, ChevronRight, Download, FileArchive, Loader2 } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useModelFormatCoverage } from '@/hooks/use-model-format-coverage';
import { useProcessingPlanResume } from '@/hooks/use-processing-plan-resume';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { schemaCountMessage, schemaMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { saveArchive } from '@/lib/workspace/batch-export';
import { fetchProcessingPlan, type ProcessingPlan } from '@/lib/workspace/processing-plan';
import {
  archiveFailureText,
  completionIsFree,
  conversionIsFree,
  conversionProviderLabel,
  deliveryFormatRows,
  failedSteps,
  formatRowAction,
  formatsFooter,
  modelArchiveRoute,
  modelFormatsCompleteBody,
  modelFormatsCompleteRoute,
  modelFormatsRoute,
  parseModelFormatCoverage,
  pausedSteps,
  planRetryRoute,
  problemText,
  refetchOffered,
  stepsInProgress,
  taskAssetRoute,
  type DeliveryFormatPhase,
  type FormatRowAction,
  type ModelFormatCoverage,
  type ModelFormatRow,
  type ModelFormatStep,
} from '@/lib/workspace/model-formats';
import { startRefetch } from '@/lib/workspace/task-refetch';
import { useWorkspaceStore } from '@/store/workspace-store';
import { cn } from '@/lib/utils';
import { formatNativePrice, formatPriceTotal } from '@/lib/pricing';

/**
 * 一键下载该任务全部已保存格式与贴图的 ZIP；服务端按需组包，不触发供应商调用。
 *
 * 组包要等 Prepare 打开并检查完所有文件才发出响应字节，所以这里先显示「正在
 * 准备压缩包」并允许取消，响应到达后再进入下载。取消中止请求，已完成的工作
 * 不会写到磁盘。
 */
export function ModelArchiveLink({ taskId, className }: { taskId: string; className?: string }) {
  const { schema } = useProductSchema();
  const [phase, setPhase] = useState<'idle' | 'preparing' | 'downloading'>('idle');
  const [error, setError] = useState('');
  const pending = useRef<AbortController | null>(null);
  // Leaving the panel cancels an unfinished preparation instead of saving a
  // package the person no longer asked for.
  useEffect(() => () => pending.current?.abort(), []);
  const route = modelArchiveRoute(schema, taskId);
  if (!route) return null;
  const message = (key: string) => schemaText(schema, key);
  const prepare = async () => {
    if (phase !== 'idle') return;
    const controller = new AbortController();
    pending.current = controller;
    setError('');
    setPhase('preparing');
    try {
      const response = await fetch(route, { credentials: 'same-origin', signal: controller.signal });
      if (!response.ok) {
        const reason = await archiveFailureText(schema, response);
        if (!controller.signal.aborted) setError(reason);
        return;
      }
      setPhase('downloading');
      const blob = await response.blob();
      if (controller.signal.aborted) return;
      saveArchive(blob, archiveFilename(response.headers.get('content-disposition')) || `${taskId}.zip`);
    } catch {
      if (!controller.signal.aborted) setError(message('ui.workspace.model_formats.download_failed'));
    } finally {
      if (pending.current === controller) pending.current = null;
      setPhase('idle');
    }
  };
  const busy = phase !== 'idle';
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      <Action
        type="button"
        variant="outline"
        size="sm"
        className={className}
        loading={busy}
        disabled={busy}
        onClick={() => void prepare()}
        data-testid="model-archive-link"
        data-state={phase}
        data-archive-route={route}
      >
        {!busy ? <FileArchive aria-hidden="true" /> : null}
        {phase === 'preparing'
          ? message('ui.workspace.model_formats.preparing')
          : phase === 'downloading'
            ? message('ui.workspace.model_formats.downloading')
            : message('ui.workspace.model_formats.download_all')}
      </Action>
      {busy ? (
        <Action
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => pending.current?.abort()}
          data-testid="model-archive-cancel"
        >
          {message('ui.workspace.model_formats.cancel_download')}
        </Action>
      ) : null}
      {error ? <span role="alert" className="basis-full text-xs text-destructive">{error}</span> : null}
    </span>
  );
}

/** The server's `Content-Disposition` name, preferring RFC 5987 when present. */
function archiveFilename(header: string | null): string {
  if (!header) return '';
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (encoded) {
    try {
      return decodeURIComponent(encoded[1]);
    } catch {
      // fall back to the plain parameter
    }
  }
  const plain = /filename="([^"]+)"/i.exec(header) ?? /filename=([^;]+)/i.exec(header);
  return plain ? plain[1].trim() : '';
}

/** The control that continues a paused conversion; the panel owns the consent grant and the request behind it. */
export interface FormatsResume {
  label: string;
  disabled: boolean;
  busy: boolean;
  onResume: () => void;
}

interface ModelFormatsViewProps {
  schema: ProductSchema;
  /** Null until the formats API has answered (loading) or when it failed. */
  coverage: ModelFormatCoverage | null;
  phase?: DeliveryFormatPhase;
  busy: boolean;
  error: string;
  onComplete: () => void;
  onRetry: (step: ModelFormatStep) => void;
  /**
   * Starts the conversion of one format from its row (#323): the same completion route as 补齐格式, narrowed to the
   * step that delivers that format. Without it the rows offer only retries.
   */
  onConvert?: (format: string) => void;
  /**
   * Fetches a failed step's already charged result again for free (#330, the task refetch of #295). Without it a
   * failed charged step offers only its retry, which charges again.
   */
  onRefetch?: (step: ModelFormatStep) => void;
  /** The format whose row action is in flight; its button spins while `busy`. */
  pendingFormat?: string;
  /**
   * `flow` (default) lists everything in one block, as the task detail and the chat show it. `pinned` is the
   * right panel's 格式 tab: the rows scroll and the actions (resume, 补齐格式) stay at the bottom, full width.
   */
  layout?: 'flow' | 'pinned';
  /** Shown above the rows (the ZIP link in the 格式 tab). */
  header?: ReactNode;
  /** Present while a paused conversion can be continued; without it the paused note is only a note. */
  resume?: FormatsResume | null;
  /** Why the paused conversion waits (the plan's own reason text), when the server named one. */
  pausedReason?: string;
}

/**
 * The price of one obtainable row: 免费 for a conversion that costs nothing, otherwise the native quote or the
 * credit estimate as before; an unmetered conversion with an estimate says the credits do not apply.
 */
function conversionPrice(schema: ProductSchema, row: ModelFormatRow): string {
  if (conversionIsFree(row)) return schemaText(schema, 'ui.workspace.model_formats.free');
  if (row.nativeQuote !== undefined) {
    return row.nativeQuote
      ? schemaMessage(schema, 'ui.pricing.default_estimate', { amount: formatNativePrice(row.nativeQuote.total_amount, row.nativeQuote.unit_label) ?? schemaMessage(schema, 'ui.pricing.unknown', {}) })
      : schemaMessage(schema, 'ui.pricing.unknown', {});
  }
  if (row.creditsMetered) return schemaCountMessage(schema, 'ui.workspace.model_formats.credits', row.estimatedCredits, { credits: row.estimatedCredits });
  return row.estimatedCredits > 0 ? schemaText(schema, 'ui.result.detail.not_applicable') : '';
}

/**
 * The button, its accessible name and the note of a row action. A failed run that was already charged (#330) says
 * so: the free refetch says it charges nothing, the retry says it charges again.
 */
function rowActionTexts(schema: ProductSchema, action: FormatRowAction, format: string): { button: string; name: string; note: string } {
  const charged = action.step.chargedCredits;
  const named = (key: string) => schemaMessage(schema, key, { format });
  const counted = (key: string) => schemaCountMessage(schema, key, charged, { credits: charged });
  switch (action.kind) {
    case 'start':
      // A run that failed and could not be retried as it was is converted again as a new run (#330).
      return { button: schemaText(schema, 'ui.workspace.model_formats.convert'), name: named('ui.workspace.model_formats.convert_named'), note: schemaText(schema, action.step.previousFailure ? 'ui.workspace.model_formats.restart_hint' : 'ui.workspace.model_formats.obtainable_hint') };
    case 'refetch':
      return { button: schemaText(schema, 'ui.workspace.model_formats.refetch'), name: named('ui.workspace.model_formats.refetch_named'), note: counted('ui.workspace.model_formats.refetch_hint') };
    default:
      return charged > 0
        ? { button: schemaText(schema, 'ui.workspace.model_formats.retry_charged'), name: named('ui.workspace.model_formats.retry_charged_named'), note: counted('ui.workspace.model_formats.retry_charged_hint') }
        : { button: schemaText(schema, 'ui.workspace.model_formats.retry'), name: named('ui.workspace.model_formats.retry_named'), note: schemaText(schema, 'ui.workspace.model_formats.retry_hint') };
  }
}

/** 纯展示：同一份清单在面板、任务详情和对话里呈现一致。 */
export function ModelFormatsView({
  schema, coverage, phase = 'ready', busy, error, onComplete, onRetry, onConvert, onRefetch, pendingFormat, layout = 'flow', header, resume, pausedReason,
}: ModelFormatsViewProps) {
  const message = (key: string) => schemaText(schema, key);
  const pinned = layout === 'pinned';
  const rows = deliveryFormatRows(schema.delivery_formats, coverage, phase, {
    notReady: message('ui.studio.formats.not_ready'),
    notListed: message('ui.studio.formats.not_listed'),
    loading: message('ui.workspace.model_formats.loading'),
    failed: message('ui.workspace.model_formats.load_failed'),
  });
  const footer = formatsFooter(coverage, rows);
  const failed = coverage ? failedSteps(coverage) : [];
  const paused = coverage ? pausedSteps(coverage) : [];
  const rowList = (
    <ul className="flex flex-col gap-1">
      {rows.map((row) => {
        const href = row.status === 'available' && row.taskId && row.assetId
          ? safeAssetURL(taskAssetRoute(schema, row.taskId, row.assetId))
          : '';
        // A start needs the completion route (onConvert); a retry is the plan's own and always available. A free
        // refetch needs its handler; without one the charged retry is what the row offers.
        let offered = formatRowAction(coverage, row);
        if (offered?.kind === 'refetch' && !onRefetch) offered = { ...offered, kind: 'retry' };
        const action = offered && (offered.kind !== 'start' || onConvert) ? offered : null;
        const texts = action ? rowActionTexts(schema, action, row.label) : null;
        const actionName = texts?.name ?? '';
        const price = row.status !== 'obtainable' ? ''
          : action?.kind === 'refetch' ? message('ui.workspace.model_formats.free') : conversionPrice(schema, row);
        // One run may convert several formats; the row says which ones it brings along.
        const together = action && action.formats.length > 1
          ? schemaMessage(schema, 'ui.workspace.model_formats.convert_together', {
            formats: action.formats.map((id) => rows.find((candidate) => candidate.id === id)?.label || id).join(message('ui.studio.list_separator')),
          })
          : '';
        if (row.status === 'obtainable') {
          // Two lines, so the narrow 格式 tab keeps the button inside the row: who converts and the action on the
          // first, the price and what clicking does on the second.
          // A long provider name may be cut in the narrow tab; the note's hover repeats it in full.
          const pill = [message('ui.workspace.model_formats.status.obtainable'), conversionProviderLabel(row)].filter(Boolean).join(' · ');
          const note = texts ? texts.note : row.reasonText;
          return (
            <li
              key={row.id}
              data-testid={`model-format-${row.id}`}
              data-status={row.status}
              className="flex min-h-9 flex-col justify-center gap-0.5 rounded-md bg-muted/40 px-2 py-1.5 text-xs"
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className="w-12 shrink-0 font-medium">{row.label}</span>
                <span className="min-w-0 truncate rounded-full bg-muted px-2 py-0.5 text-foreground">{pill}</span>
                {action && (
                  <HintTooltip
                    name={actionName}
                    hint={together}
                    cost={price ? { text: price, ai: false } : undefined}
                    className="ml-auto shrink-0"
                  >
                    <Action
                      type="button"
                      variant="outline"
                      size="xs"
                      loading={busy && pendingFormat === row.id}
                      disabled={busy}
                      onClick={() => (action.kind === 'retry' ? onRetry(action.step) : action.kind === 'refetch' ? onRefetch?.(action.step) : onConvert?.(row.id))}
                      aria-label={[actionName, together, price].filter(Boolean).join(message('ui.common.clause_separator'))}
                      data-testid={`model-format-action-${row.id}`}
                      data-action={action.kind}
                    >
                      {texts?.button}
                    </Action>
                  </HintTooltip>
                )}
              </div>
              {price || note ? (
                <HintTooltip
                  name={`${row.label} · ${pill}`}
                  hint={note}
                  target={together || undefined}
                  cost={price ? { text: price, ai: false } : undefined}
                  reopenOnKeyboardFocus
                  className="ml-14 min-w-0 rounded-sm text-muted-foreground focus-within:ring-2 focus-within:ring-ring"
                  triggerProps={{ role: 'note', tabIndex: 0, 'aria-label': `${message('ui.workspace.model_formats.reason')}: ${row.label}` }}
                >
                  <span className="block min-w-0 truncate text-left">
                    {price ? <span data-testid={`model-format-price-${row.id}`}>{price}</span> : null}
                    {price && note ? ' · ' : null}
                    {note ? <span data-testid={`model-format-hint-${row.id}`}>{note}</span> : null}
                  </span>
                </HintTooltip>
              ) : null}
            </li>
          );
        }
        return (
          <li
            key={row.id}
            data-testid={`model-format-${row.id}`}
            data-status={row.status}
            className="flex h-9 min-h-9 items-center gap-2 rounded-md bg-muted/40 px-2 text-xs"
          >
            <span className="w-12 shrink-0 font-medium">{row.label}</span>
            {row.status !== 'unknown' ? (
              <span className={cn(
                'rounded-full px-2 py-0.5',
                row.status === 'available' && 'bg-primary/15 text-primary',
                row.status === 'unavailable' && 'text-muted-foreground',
                row.status === 'converting' && 'bg-muted text-foreground',
              )}>
                {[
                  message(`ui.workspace.model_formats.status.${row.status}`),
                  row.status === 'converting' ? conversionProviderLabel(row) : '',
                ].filter(Boolean).join(' · ')}
              </span>
            ) : null}
            {/* A conversion can run at another provider than the one that made
                the model (the file is uploaded there); the pill above names which. */}
            {row.status === 'converting' && <Loader2 className="size-3 animate-spin text-muted-foreground" aria-hidden="true" />}
            {(row.status === 'unavailable' || row.status === 'unknown') && row.reasonText && (
              row.status === 'unavailable' && rows.filter((candidate) => candidate.reasonText === row.reasonText).length > 1 ? (
                <HintTooltip
                  name={row.label}
                  reason={row.reasonText}
                  reopenOnKeyboardFocus
                  className="ml-auto shrink-0 rounded p-1 text-muted-foreground hover:text-foreground focus-within:ring-2 focus-within:ring-ring"
                  triggerProps={{ role: 'img', tabIndex: 0, 'aria-label': `${message('ui.workspace.model_formats.reason')}: ${row.label}` }}
                >
                  <Ban className="size-3.5" aria-hidden="true" />
                </HintTooltip>
              ) : (
                <HintTooltip
                  name={row.label}
                  reason={row.reasonText}
                  reopenOnKeyboardFocus
                  className="min-w-0 flex-1 rounded-sm text-muted-foreground focus-within:ring-2 focus-within:ring-ring"
                  triggerProps={{ role: 'note', tabIndex: 0, 'aria-label': `${message('ui.workspace.model_formats.reason')}: ${row.label}` }}
                >
                  <span className="block min-w-0 truncate text-left">{row.reasonText}</span>
                </HintTooltip>
              )
            )}
            {href && (
              <HintTooltip name={message('ui.workspace.model_formats.download')} hint={row.label} className="ml-auto">
                <a
                  href={href}
                  download
                  aria-label={`${message('ui.workspace.model_formats.download')} ${row.label}`}
                  className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <Download className="size-3.5" aria-hidden="true" />
                </a>
              </HintTooltip>
            )}
          </li>
        );
      })}
    </ul>
  );
  const resumeControl = paused.length > 0 && resume ? (
    <Action
      type="button"
      size={pinned ? 'lg' : 'sm'}
      className={pinned ? 'w-full' : undefined}
      loading={resume.busy}
      disabled={resume.disabled || resume.busy || busy}
      onClick={resume.onResume}
      data-testid="model-formats-resume"
    >
      {resume.label}
    </Action>
  ) : null;
  const completeControl = footer === 'complete' && coverage ? (
    <Action
      type="button"
      size={pinned ? 'lg' : 'sm'}
      className={pinned ? 'w-full' : undefined}
      loading={busy}
      disabled={busy}
      onClick={onComplete}
      data-testid="model-formats-complete"
    >
      {coverage.priceTotals !== undefined
        ? `${schemaMessage(schema, 'ui.pricing.totals_heading', {})}: ${coverage.priceTotals.map(formatPriceTotal).filter((value): value is string => value !== null).join(' · ') || schemaMessage(schema, 'ui.pricing.unknown', {})}`
        : completionIsFree(coverage)
          ? schemaMessage(schema, 'ui.workspace.model_formats.complete_free', {})
          : schemaCountMessage(schema, 'ui.workspace.model_formats.complete', coverage.completionCredits, { credits: coverage.completionCredits })}
    </Action>
  ) : null;
  const errorLine = error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null;
  const converting = footer === 'converting' ? (
    <p role="status" className="flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="model-formats-converting">
      <Loader2 className="size-3 animate-spin" aria-hidden="true" />
      {message('ui.workspace.model_formats.completing')}
    </p>
  ) : null;
  const emptyNote = footer === 'not_ready' || footer === 'all_present' || footer === 'no_route' ? (
    <p role="status" className="text-xs text-muted-foreground" data-testid="model-formats-empty" data-reason={footer}>
      {message(`ui.studio.formats.footer.${footer}`)}
    </p>
  ) : null;
  const pausedNote = paused.length > 0 ? (
    <div role="status" className="flex flex-col gap-0.5 text-xs text-warning" data-testid="model-formats-paused">
      <p>{message('ui.workspace.model_formats.plan_paused')}</p>
      {pausedReason ? <p data-testid="model-formats-paused-reason">{pausedReason}</p> : null}
    </div>
  ) : null;
  // One group per failed conversion (#330): which formats at which provider, the charge only in the group of the
  // run that was charged, and a button that names the formats it is for. A charged run offers its free refetch
  // first; after that is not possible its retry says it charges again.
  const failedBlock = failed.length > 0 ? (
    <div className="flex flex-col gap-1.5 text-xs" data-testid="model-formats-failed">
      <p role="alert" className="text-destructive">{message('ui.workspace.model_formats.plan_failed')}</p>
      <ul className="flex flex-col gap-1.5">
        {failed.map((step) => {
          const free = Boolean(onRefetch) && refetchOffered(step);
          const formatsText = step.formats
            .map((id) => rows.find((candidate) => candidate.id === id)?.label || id)
            .join(message('ui.studio.list_separator'));
          const button = schemaMessage(
            schema,
            free ? 'ui.workspace.model_formats.refetch_named' : step.chargedCredits > 0 ? 'ui.workspace.model_formats.retry_charged_named' : 'ui.workspace.model_formats.retry_named',
            { format: formatsText },
          );
          return (
            <li
              key={step.planId}
              className="flex flex-col items-start gap-1 rounded-md border border-destructive/30 px-2 py-1.5"
              data-testid="model-formats-failed-step"
              data-formats={step.formats.join(' ')}
            >
              <span data-testid="model-formats-failed-title">
                {schemaMessage(schema, 'ui.workspace.model_formats.failed_step', { formats: formatsText, provider: conversionProviderLabel(step) })}
              </span>
              {step.chargedCredits > 0 ? (
                <span className="text-muted-foreground" data-testid="model-formats-charged">{schemaCountMessage(schema, 'ui.workspace.model_formats.charged_note', step.chargedCredits, { credits: step.chargedCredits })}</span>
              ) : null}
              {!free && step.retryBlocked ? (
                <span className="text-muted-foreground" data-testid="model-formats-retry-blocked">{message('ui.workspace.model_formats.retry_blocked')}</span>
              ) : (
                <Action
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={busy}
                  onClick={() => (free ? onRefetch?.(step) : onRetry(step))}
                  data-action={free ? 'refetch' : 'retry'}
                >
                  {button}
                </Action>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  ) : null;

  if (pinned) {
    const actions = resumeControl || completeControl || errorLine;
    return (
      <div className="flex min-h-0 flex-1 flex-col" data-testid="model-formats" data-layout="pinned">
        <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-3">
          {header}
          {rowList}
          {converting}
          {emptyNote}
          {pausedNote}
          {failedBlock}
        </div>
        {actions ? (
          <footer className="flex shrink-0 flex-col gap-2 border-t border-border p-3" data-testid="model-formats-actions">
            {resumeControl}
            {completeControl}
            {errorLine}
          </footer>
        ) : null}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-2" data-testid="model-formats">
      {header}
      {rowList}
      {completeControl}
      {converting}
      {emptyNote}
      {pausedNote}
      {resumeControl}
      {failedBlock}
      {errorLine}
    </div>
  );
}

interface ModelFormatsPanelProps {
  taskId: string;
  sessionId?: string | null;
  /** 折叠时只显示标题行，展开后才读取清单。 */
  collapsible?: boolean;
  /**
   * The right panel's 格式 tab: no title (the tab is the title), the rows scroll and the actions stay at the
   * bottom, full width. Otherwise the panel is one block with its own title (task detail, chat).
   */
  pinned?: boolean;
  /** Shown above the rows (the ZIP link in the 格式 tab). */
  header?: ReactNode;
  className?: string;
}

export function ModelFormatsPanel({ taskId, sessionId, collapsible = false, pinned = false, header, className }: ModelFormatsPanelProps) {
  const { schema } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const [open, setOpen] = useState(!collapsible);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const route = modelFormatsRoute(schema, taskId);
  const { data: coverage, error: loadError, mutate } = useModelFormatCoverage(open && route ? route : null);

  // A paused conversion is continued from here. Its plan (version, step, reason) is read by id from the same
  // route the 加工 tab uses; nothing is read while no conversion waits.
  const pausedPlanId = coverage ? pausedSteps(coverage).find((step) => step.planId !== '')?.planId ?? '' : '';
  const { data: pausedPlan, mutate: mutatePlan } = useSWR<ProcessingPlan>(
    open && pausedPlanId ? ['processing-plan', schema.revision, pausedPlanId] : null,
    () => fetchProcessingPlan(schema, pausedPlanId),
    { revalidateOnFocus: false },
  );
  const reloadAll = useCallback(async () => {
    await Promise.all([mutatePlan(), mutate()]);
  }, [mutate, mutatePlan]);
  const reloadAfterFailure = useCallback(() => {
    void mutatePlan();
    void mutate();
  }, [mutate, mutatePlan]);
  const resume = useProcessingPlanResume(pausedPlan, { onResumed: reloadAll, onFailed: reloadAfterFailure });

  // 补齐产生的新任务进入任务列表，用户能看到进度与结果。
  useEffect(() => {
    if (!coverage || !sessionId) return;
    for (const step of coverage.steps) {
      if (step.resultTaskId && step.capability) {
        registerTask({
          taskId: step.resultTaskId, sessionId, capability: step.capability,
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false, formatOf: taskId,
        });
      }
    }
  }, [coverage, registerTask, schema.runtime.frontend.percentage_multiplier, sessionId, taskId]);

  const complete = useCallback(async () => {
    const target = modelFormatsCompleteRoute(schema, taskId);
    if (!target || busy) return;
    setBusy(true);
    setError('');
    try {
      const response = await fetch(target, { method: 'POST' });
      if (!response.ok) throw new Error(await problemText(response, schemaText(schema, 'ui.workspace.model_formats.complete_failed')));
      await mutate(parseModelFormatCoverage(await response.json()), { revalidate: false });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.model_formats.complete_failed'));
      // A 409 means the server's view moved on (a repeat click after the plans
      // were already started, or nothing left to obtain): re-read it so the
      // action and the rows reflect the plans that exist instead of a stale list.
      void mutate();
    } finally {
      setBusy(false);
    }
  }, [busy, mutate, schema, taskId]);

  // The row action of one format (#323): the completion route narrowed to that format's step. Same plans, same
  // idempotency keys and approval policy as 补齐格式, so the two never start the same run twice.
  const [pendingFormat, setPendingFormat] = useState('');
  const convert = useCallback(async (format: string) => {
    const target = modelFormatsCompleteRoute(schema, taskId);
    if (!target || busy) return;
    setBusy(true);
    setPendingFormat(format);
    setError('');
    try {
      const response = await fetch(target, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: modelFormatsCompleteBody([format]),
      });
      if (!response.ok) throw new Error(await problemText(response, schemaText(schema, 'ui.workspace.model_formats.complete_failed')));
      await mutate(parseModelFormatCoverage(await response.json()), { revalidate: false });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.model_formats.complete_failed'));
      void mutate();
    } finally {
      setBusy(false);
      setPendingFormat('');
    }
  }, [busy, mutate, schema, taskId]);

  const retry = useCallback(async (step: ModelFormatStep) => {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const response = await fetch(planRetryRoute(schema, step.planId), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ expected_plan_version: step.planVersion, expected_offering_revision: step.offeringRevision }),
      });
      if (!response.ok) throw new Error(await problemText(response, schemaText(schema, 'ui.workspace.model_formats.complete_failed')));
      await mutate();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : schemaText(schema, 'ui.workspace.model_formats.complete_failed'));
      void mutate();
    } finally {
      setBusy(false);
    }
  }, [busy, mutate, schema]);

  // The free refetch of a failed, already charged run (#330): the task refetch route of #295 on the step's failed
  // Task. Nothing is resubmitted upstream; the refetch Task joins the task list and its files fill the rows.
  const refetch = useCallback(async (step: ModelFormatStep) => {
    if (busy || !step.resultTaskId) return;
    setBusy(true);
    setPendingFormat(step.formats[0] ?? '');
    setError('');
    try {
      const result = await startRefetch(schema, step.resultTaskId);
      if (result.taskId && !result.replayed && sessionId && step.capability) {
        registerTask({
          taskId: result.taskId, sessionId, capability: step.capability,
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false, formatOf: taskId,
        });
      }
      await mutate();
    } catch (cause) {
      setError(schemaMessage(schema, 'ui.task.refetch.start_failed', { reason: cause instanceof Error ? cause.message : '' }));
      void mutate();
    } finally {
      setBusy(false);
      setPendingFormat('');
    }
  }, [busy, mutate, registerTask, schema, sessionId, taskId]);

  if (!route) return null;
  const title = schemaText(schema, 'ui.workspace.model_formats.title');
  let body = null;
  if (open) {
    body = (
      <ModelFormatsView
        schema={schema}
        coverage={coverage ?? null}
        phase={loadError ? 'failed' : coverage ? 'ready' : 'loading'}
        busy={busy || resume.busy}
        error={error || resume.error}
        onComplete={() => void complete()}
        onRetry={(step) => void retry(step)}
        onConvert={(format) => void convert(format)}
        onRefetch={schema.runtime.http.routes.task_refetch ? (step) => void refetch(step) : undefined}
        pendingFormat={pendingFormat}
        layout={pinned ? 'pinned' : 'flow'}
        header={header}
        resume={resume.step
          ? { label: resume.label, disabled: !resume.available, busy: resume.busy, onResume: () => { setError(''); void resume.resume(); } }
          : null}
        pausedReason={pausedPlan?.failure ? schemaText(schema, `ui.workspace.processing.${pausedPlan.failure}`) : ''}
      />
    );
  }
  if (pinned) {
    return <div className={cn('flex min-h-0 flex-1 flex-col', className)} data-testid="model-formats-panel">{body}</div>;
  }
  return (
    <div className={cn('flex flex-col gap-2', className)} data-testid="model-formats-panel">
      {collapsible ? (
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((current) => !current)}
          className="flex items-center gap-1 self-start rounded text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        >
          {open ? <ChevronDown className="size-3.5" aria-hidden="true" /> : <ChevronRight className="size-3.5" aria-hidden="true" />}
          {title}
        </button>
      ) : (
        <h3 className="text-sm font-semibold">{title}</h3>
      )}
      {body}
    </div>
  );
}
