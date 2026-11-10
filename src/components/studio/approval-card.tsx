'use client';

import { useState } from 'react';
import { Box, Clock, Image as ImageIcon, ShieldQuestion } from 'lucide-react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { useBalance } from '@/hooks/use-balance';
import { useHandleReferences } from '@/hooks/use-handle-references';
import { HandleTag } from '@/components/chat/handle-tag';
import { fetchCapabilityOfferings, NO_CREDITS } from '@/lib/agent-offerings';
import { accountOfCard, approvalInputParameterLabel, approvalOfferingNames, approvalShowsCredits, creditBalanceOf, isRetextureCard, remainingValidity, type ApprovalCardModel } from '@/lib/chat/approval-card';
import type { ParameterAdjustment } from '@/lib/chat/pending-approvals';
import { safeAssetURL, safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { formatNumber } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { formatNativePrice, formatPriceTotal } from '@/lib/pricing';
import { unavailableLabel } from '@/lib/unavailable';
import { taskAssetRoute } from '@/lib/workspace/model-formats';
import { cn } from '@/lib/utils';

export interface ApprovalCardProps {
  model: ApprovalCardModel;
  busy: boolean;
  /** Expiry is judged by the server's time; the clock here only words the remaining time. */
  now: number;
  onDecide: (approved: boolean) => void;
  /** An expired step cannot be revived: the user asks the Agent again. */
  onRerequest: () => void;
  /** Why the server refused the last decision, in words; shown on the card until the next try. */
  error?: string | null;
}

interface ProfileAdjustmentGroup {
  id: string;
  items: ParameterAdjustment[];
}

/** #103: adjustments that came from one usage profile are shown together, with one shared reason. */
function profileAdjustmentGroups(adjustments: readonly ParameterAdjustment[]): ProfileAdjustmentGroup[] {
  const groups = new Map<string, ParameterAdjustment[]>();
  const order: string[] = [];
  for (const item of adjustments) {
    const id = item.profile;
    if (!id) continue;
    const known = groups.get(id);
    if (known) {
      known.push(item);
    } else {
      groups.set(id, [item]);
      order.push(id);
    }
  }
  return order.map((id) => ({ id, items: groups.get(id) ?? [] }));
}

/**
 * #103: one shared line for a profile group. Every distinct reason of its
 * adjustments is shown, not just the first one, so a second changed parameter
 * never hides why it was changed.
 */
function profileGroupReason(group: ProfileAdjustmentGroup): string {
  return Array.from(new Set(group.items.map((item) => item.reason).filter((reason) => reason !== ''))).join(' ');
}

function adjustmentValueText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (value === null || value === undefined) return '';
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function toggleReason(current: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(current);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

/**
 * 批准卡片：步骤、模型、输入图、积分、批准 / 不批准。它在对话里，不弹窗，也不会
 * 因为 Esc 或点击别处产生默认决定；过期后只剩"重新发起"和"放弃"。
 */
export function ApprovalCard({ model, busy, now, onDecide, onRerequest, error = null }: ApprovalCardProps) {
  const { schema } = useProductSchema();
  const handleReferences = useHandleReferences();
  const { previewAssetUrl } = useAssetURLRewriter();
  const { accounts } = useBalance();
  const [expandedPrompt, setExpandedPrompt] = useState(false);
  const [parametersOpen, setParametersOpen] = useState(false);
  const [openReasons, setOpenReasons] = useState<ReadonlySet<string>>(() => new Set());
  const adjustments = model.parameterAdjustments ?? [];
  const profileGroups = profileAdjustmentGroups(adjustments);
  const agentAdjustments = adjustments.filter((item) => !item.profile);
  const profileLabel = (id: string) => schema.chat.model3d_usage?.profiles?.find((profile) => profile.id === id)?.label
    ?? schema.chat.image2d_usage?.profiles?.find((profile) => profile.id === id)?.label ?? id;
  const profileSummary = (group: ProfileAdjustmentGroup) => schemaCountMessage(
    schema,
    'ui.studio.approval.card.parameter_summary',
    group.items.length,
    { profile: profileLabel(group.id), count: group.items.length },
  );
  const agentLine = (item: ParameterAdjustment) => formatSchemaText(
    schemaText(schema, 'ui.studio.approval.card.parameter_agent_line'),
    {
      name: item.label,
      recommended: adjustmentValueText(item.recommendedValue),
      actual: adjustmentValueText(item.value),
    },
  );
  const capability = schema.capabilities.find((item) => item.id === model.capability);
  const { data: offerings, isLoading: offeringsLoading } = useSWR(
    model.capability ? ['approval-card-offerings', schema.revision, model.capability] : null,
    () => fetchCapabilityOfferings(schema, model.capability),
    { revalidateOnFocus: false },
  );
  const names = approvalOfferingNames(offerings, model);
  // Credits follow the Offering's credits_metered: a provider that does not bill in credits shows "/".
  // While the Offering list is still being read, the credit cells hold a neutral placeholder instead
  // of a number that may turn into "/" (or the reverse); a failed read falls back to the server's number.
  const creditsPending = offeringsLoading === true;
  const showCredits = !creditsPending && approvalShowsCredits(offerings, model);
  const creditsCell = (credits: number | null | undefined, line?: string): string => {
    if (creditsPending) return schemaText(schema, 'ui.loading');
    if (!showCredits || credits == null) return NO_CREDITS;
    return line ?? schemaCountMessage(schema, 'ui.studio.approval.card.credits_about', credits, { credits });
  };
  const expired = model.status === 'expired' || remainingValidity(model.expiresAt, now).unit === 'expired';
  const validity = remainingValidity(model.expiresAt, now);
  const account = accountOfCard(accounts, model);
  const providerLabel = account?.label ?? names.providerName;
  // A provider that also prices natively (Tripo) reports its credits as an exact amount only (#324).
  const balance = creditBalanceOf(account);
  const nativePricing = model.priceMode === 'native' || model.nativeQuote != null
    || offerings?.some((offering) => offering.model_id === model.model && offering.provider_id === model.provider && offering.price_mode === 'native') === true;
  const nativeBalance = account?.priceMode === 'native' && model.nativeQuote && account.nativeBalance?.unit === model.nativeQuote.unit
    ? formatPriceTotal(account.nativeBalance)
    : null;
  const short = !expired && !nativePricing && showCredits && model.credits != null && balance !== null && balance < model.credits;
  const retexture = isRetextureCard(model);
  const invalidNativeQuote = nativePricing && model.nativeQuote == null;
  const extra = model.extra;
  // #239: the card says who chose the model and, for a purpose, why. The
  // server translated the reason; the source code maps to the Schema's words.
  const modelChoiceSource = (choice: NonNullable<ApprovalCardModel['modelChoice']>): string => {
    if (choice.source === 'profile') {
      return formatSchemaText(schemaText(schema, 'ui.studio.approval.card.model_source_profile'), {
        profile: profileLabel(choice.profile ?? ''),
      });
    }
    const key = choice.source === 'user' || choice.source === 'agent' || choice.source === 'default'
      ? choice.source
      : 'default';
    return schemaText(schema, `ui.studio.approval.card.model_source_${key}`);
  };
  const modelChoiceText = model.modelChoice
    ? formatSchemaText(
      schemaText(schema, model.modelChoice.reason
        ? 'ui.studio.approval.card.model_choice_line'
        : 'ui.studio.approval.card.model_choice_line_no_reason'),
      {
        model: names.modelName || model.model,
        source: modelChoiceSource(model.modelChoice),
        reason: model.modelChoice.reason ?? '',
      },
    )
    : '';
  // What the extra credits pay for ("附加导出格式：FBX、USDZ，约 N 积分"), all from the Schema's words.
  const extraLine = extra
    ? schemaCountMessage(
      schema,
      extra.items.length > 0 ? 'ui.studio.approval.card.extra_line' : 'ui.studio.approval.card.extra_line_no_items',
      extra.credits,
      { label: extra.label, items: extra.items.join(schemaText(schema, 'ui.studio.approval.card.extra_items_separator')), credits: extra.credits },
    )
    : '';

  const validityText = expired
    ? schemaText(schema, 'ui.studio.approval.card.expired')
    : validity.unit === 'hours'
      ? schemaCountMessage(schema, 'ui.studio.approval.card.valid_hours', validity.value, { hours: validity.value })
      : validity.unit === 'minutes'
        ? schemaCountMessage(schema, 'ui.studio.approval.card.valid_minutes', validity.value, { minutes: validity.value })
        : schemaText(schema, 'ui.studio.approval.card.waiting');

  // #103: only the card's body scrolls inside the Schema height; the header and
  // the approve / decline footer stay visible.
  const maxHeight = schema.chat.frontend.approval_card_max_height_pixels;
  return (
    <article
      data-testid="studio-approval-card"
      data-status={expired ? 'expired' : 'pending'}
      aria-label={schemaText(schema, 'ui.studio.approval.card.title')}
      className={cn(
        'flex flex-col gap-2 rounded-lg border bg-panel-raised p-3 text-xs',
        expired ? 'border-border opacity-90' : 'border-warning/40',
        maxHeight > 0 && 'overflow-hidden',
      )}
      style={maxHeight > 0 ? { maxHeight } : undefined}
    >
      <header className="flex shrink-0 items-center gap-2">
        <ShieldQuestion aria-hidden className={cn('size-4 shrink-0', expired ? 'text-muted-foreground' : 'text-warning')} />
        <span className="text-[13px] font-semibold">{schemaText(schema, 'ui.studio.approval.card.title')}</span>
        <span className={cn('ml-auto flex items-center gap-1', expired ? 'text-muted-foreground' : 'text-warning')} data-testid="studio-approval-validity">
          <Clock aria-hidden className="size-3" />
          {validityText}
        </span>
      </header>
      <div
        className={cn('flex min-h-0 flex-col gap-2', maxHeight > 0 && 'overflow-y-auto')}
        data-testid="studio-approval-body"
      >
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
        <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.step')}</dt>
        <dd className="truncate font-medium">{capability?.label_cn || capability?.label || model.capability}</dd>
        {capability ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.kind')}</dt>
            <dd className="font-medium" data-testid="studio-approval-kind" data-domain={capability.domain}>
              {schemaText(schema, capability.domain === 'image' ? 'ui.studio.picker.image.prefix' : 'ui.studio.picker.model.prefix')}
            </dd>
          </>
        ) : null}
        <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.model')}</dt>
        <dd className="truncate" data-testid="studio-approval-model">{[names.modelName, names.providerName].filter(Boolean).join(' · ')}</dd>
        {modelChoiceText ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.parameter_source')}</dt>
            <dd className="text-muted-foreground" data-testid="studio-approval-model-choice">{modelChoiceText}</dd>
          </>
        ) : null}
        {model.nativeQuote ? (
          <>
            <dt className="text-muted-foreground">{formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount: formatNativePrice(model.nativeQuote.total_amount, model.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') })}</dt>
            <dd className="font-medium tabular-nums" data-testid="studio-approval-native-price">
              <span>{formatSchemaText(schemaText(schema, 'ui.pricing.base_amount'), { amount: formatNativePrice(model.nativeQuote.base_amount, model.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') })}</span>
              {model.nativeQuote.components?.map((component) => <span key={component.id} className="ml-2 text-muted-foreground">{component.label}: {formatNativePrice(component.amount, model.nativeQuote!.unit_label) ?? schemaText(schema, 'ui.pricing.unknown')}</span>)}
            </dd>
          </>
        ) : invalidNativeQuote ? (
          <><dt className="text-muted-foreground">{formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount: schemaText(schema, 'ui.pricing.unknown') })}</dt><dd>
            <UnavailableNotice testId="studio-approval-native-price-unknown" label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.approval.price_unknown'))} detail={schemaText(schema, 'ui.studio.approval.card.price_unknown_detail')} />
          </dd></>
        ) : extra && (model.credits != null || !showCredits) ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.credits_base')}</dt>
            <dd className="tabular-nums" data-testid="studio-approval-base-credits">{creditsCell(extra.baseCredits)}</dd>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.credits_extra')}</dt>
            <dd className="tabular-nums" data-testid="studio-approval-extra-credits">{creditsCell(extra.credits, extraLine)}</dd>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.credits_total')}</dt>
            <dd className="font-medium tabular-nums" data-testid="studio-approval-credits">{creditsCell(model.credits)}</dd>
          </>
        ) : model.credits != null || !showCredits ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.credits')}</dt>
            <dd className="font-medium tabular-nums" data-testid="studio-approval-credits">{creditsCell(model.credits)}</dd>
          </>
        ) : null}
        {!expired && (account || names.providerName) ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.balance')}</dt>
            <dd className={cn('tabular-nums', short && 'text-warning')} data-testid="studio-approval-balance">
              {nativePricing
                ? nativeBalance ?? schemaText(schema, 'ui.pricing.balance_unknown')
                : balance !== null
                ? schemaCountMessage(schema, 'ui.studio.approval.card.balance_line', balance, { provider: providerLabel, balance: formatNumber(balance, activeLocale(schema)) })
                : formatSchemaText(schemaText(schema, 'ui.studio.approval.card.balance_unknown'), { provider: providerLabel })}
            </dd>
          </>
        ) : null}
        {model.prompt ? (
          <>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.prompt')}</dt>
            <dd>
              <button
                type="button"
                aria-expanded={expandedPrompt}
                onClick={() => setExpandedPrompt((value) => !value)}
                className={cn('text-left [overflow-wrap:anywhere] focus-visible:outline-2 focus-visible:outline-ring', !expandedPrompt && 'line-clamp-2')}
              >
                {model.prompt}
              </button>
            </dd>
          </>
        ) : null}
      </dl>
      {adjustments.length > 0 ? (
        <section className="flex flex-col gap-1.5 border-t border-border pt-2" data-testid="studio-approval-parameter-adjustments" aria-label={schemaText(schema, 'ui.studio.approval.card.parameter_adjustments')}>
          <div className="flex items-center gap-2">
            <h4 className="font-medium">{schemaText(schema, 'ui.studio.approval.card.parameter_adjustments')}</h4>
            <button
              type="button"
              aria-expanded={parametersOpen}
              onClick={() => setParametersOpen((value) => !value)}
              className="ml-auto text-[11px] text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
              data-testid="studio-approval-parameters-toggle"
            >
              {parametersOpen
                ? schemaText(schema, 'ui.studio.approval.card.parameters_hide')
                : schemaCountMessage(schema, 'ui.studio.approval.card.parameters_toggle', adjustments.length, { count: adjustments.length })}
            </button>
          </div>
          {/* 摘要行：用途调整按来源合并、理由只出现一次；Agent 调整逐项一行。参数表默认收起。 */}
          {profileGroups.map((group) => (
            <div key={group.id} className="flex flex-col gap-0.5" data-testid="studio-approval-profile-summary" data-profile={group.id}>
              <span className="font-medium">{profileSummary(group)}</span>
              <HintTooltip name={profileGroupReason(group)} className="min-w-0">
                <button
                  type="button"
                  aria-expanded={openReasons.has(`profile:${group.id}`)}
                  onClick={() => setOpenReasons((current) => toggleReason(current, `profile:${group.id}`))}
                  className={cn(
                    'w-full text-left text-[11px] leading-snug text-muted-foreground [overflow-wrap:anywhere] hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
                    !openReasons.has(`profile:${group.id}`) && 'line-clamp-1',
                  )}
                  data-testid="studio-approval-profile-reason"
                >
                  {profileGroupReason(group)}
                </button>
              </HintTooltip>
            </div>
          ))}
          {agentAdjustments.map((item) => {
            const reasonOpen = openReasons.has(`agent-line:${item.name}`);
            const reasonText = item.advice ? `${item.reason} ${item.advice}` : item.reason;
            return (
              <div
                key={`agent-line:${item.name}`}
                className="flex flex-col gap-0.5"
                data-testid="studio-approval-agent-line"
                data-parameter={item.name}
              >
                <span className="font-medium [overflow-wrap:anywhere]">{agentLine(item)}</span>
                {reasonText ? (
                  <HintTooltip name={reasonText} className="min-w-0">
                    <button
                      type="button"
                      aria-expanded={reasonOpen}
                      onClick={() => setOpenReasons((current) => toggleReason(current, `agent-line:${item.name}`))}
                      className={cn(
                        'w-full text-left text-[11px] leading-snug text-muted-foreground [overflow-wrap:anywhere] hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
                        !reasonOpen && 'line-clamp-1',
                      )}
                      data-testid="studio-approval-agent-line-reason"
                    >
                      {item.reason}
                      {reasonOpen && item.advice ? ` ${item.advice}` : ''}
                    </button>
                  </HintTooltip>
                ) : null}
              </div>
            );
          })}
          {parametersOpen ? (
            <table className="w-full table-fixed border-collapse text-[11px]" data-testid="studio-approval-parameter-table">
              <colgroup>
                <col className="w-[34%]" />
                <col className="w-[44%]" />
                <col className="w-[22%]" />
              </colgroup>
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="pb-0.5 font-normal">{schemaText(schema, 'ui.studio.params.label')}</th>
                  <th className="pb-0.5 font-normal">{schemaText(schema, 'ui.studio.approval.card.recommended_value')} → {schemaText(schema, 'ui.studio.approval.card.actual_value')}</th>
                  <th className="pb-0.5 text-right font-normal">{schemaText(schema, 'ui.studio.approval.card.parameter_source')}</th>
                </tr>
              </thead>
              <tbody>
                {profileGroups.flatMap((group) => {
                  const sourceText = formatSchemaText(schemaText(schema, 'ui.studio.approval.card.parameter_source_profile'), { profile: profileLabel(group.id) });
                  return group.items.map((item) => {
                    const valueText = `${adjustmentValueText(item.recommendedValue)} → ${adjustmentValueText(item.value)}`;
                    return (
                      <tr key={`${group.id}:${item.name}`} className="border-t border-border/60" data-parameter={item.name} data-profile={group.id}>
                        <td className="truncate py-0.5 pr-1">
                          <HintTooltip name={item.label} className="block max-w-full">
                            <span className="block truncate">{item.label}</span>
                          </HintTooltip>
                        </td>
                        <td className="truncate py-0.5 pr-1">
                          <HintTooltip name={valueText} className="block max-w-full">
                            <span className="block truncate">{valueText}</span>
                          </HintTooltip>
                        </td>
                        <td className="truncate py-0.5 text-right text-muted-foreground">
                          <HintTooltip name={sourceText} className="block max-w-full">
                            <span className="block truncate">{sourceText}</span>
                          </HintTooltip>
                        </td>
                      </tr>
                    );
                  });
                })}
                {agentAdjustments.map((item) => {
                  const reasonOpen = openReasons.has(`agent:${item.name}`);
                  const valueText = `${adjustmentValueText(item.recommendedValue)} → ${adjustmentValueText(item.value)}`;
                  const reasonText = item.advice ? `${item.reason} ${item.advice}` : item.reason;
                  return [
                    <tr key={`agent:${item.name}`} className="border-t border-border/60" data-parameter={item.name} data-source="agent">
                      <td className="truncate py-0.5 pr-1">
                        <HintTooltip name={item.label} className="block max-w-full">
                          <span className="block truncate">{item.label}</span>
                        </HintTooltip>
                      </td>
                      <td className="truncate py-0.5 pr-1">
                        <HintTooltip name={valueText} className="block max-w-full">
                          <span className="block truncate">{valueText}</span>
                        </HintTooltip>
                      </td>
                      <td className="truncate py-0.5 text-right text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.parameter_source_agent')}</td>
                    </tr>,
                    <tr key={`agent-reason:${item.name}`} data-testid="studio-approval-agent-reason">
                      <td colSpan={3} className="pb-1">
                        <HintTooltip name={reasonText} className="block w-full">
                          <button
                            type="button"
                            aria-expanded={reasonOpen}
                            onClick={() => setOpenReasons((current) => toggleReason(current, `agent:${item.name}`))}
                            className={cn(
                              'w-full text-left text-[11px] leading-snug text-muted-foreground [overflow-wrap:anywhere] hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
                              !reasonOpen && 'line-clamp-1',
                            )}
                          >
                            {item.reason}
                            {reasonOpen && item.advice ? ` ${item.advice}` : ''}
                          </button>
                        </HintTooltip>
                      </td>
                    </tr>,
                  ];
                })}
              </tbody>
            </table>
          ) : null}
        </section>
      ) : null}
      {retexture ? (
        <p className="text-muted-foreground" data-testid="studio-approval-retexture-note">{schemaText(schema, 'ui.studio.approval.card.retexture_note')}</p>
      ) : null}
      {model.inputs.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5" aria-label={schemaText(schema, 'ui.studio.approval.card.inputs')}>
          {model.inputs.map((input, index) => {
            const role = input.kind === 'task' ? schemaText(schema, 'ui.studio.approval.card.input_target_model') : '';
            // What the session cannot name is named by the Offering's own parameter label (#324: a source model file).
            const parameterLabel = approvalInputParameterLabel(offerings, model, input.parameter);
            const fallback = formatSchemaText(schemaText(schema, 'ui.chat.input.attachment_alt'), { index: index + 1 });
            const repeated = model.inputs.filter((item) => item.parameter === input.parameter).length > 1;
            const label = input.label || (!role && !input.handle ? (parameterLabel ? `${parameterLabel}${repeated ? ` ${index + 1}` : ''}` : fallback) : '');
            // The handle goes into the next message: a picture the step sends is attached too, the model it works on is named only.
            const reference = !input.handle ? null
              : handleReferences.get(input.handle)
                ?? (input.kind === 'task'
                  ? { handle: input.handle, kind: 'model' as const, url: '', ...(input.taskId ? { taskId: input.taskId } : {}) }
                  : input.taskId && input.assetId ? { handle: input.handle, url: taskAssetRoute(schema, input.taskId, input.assetId), taskId: input.taskId, assetId: input.assetId } : null);
            // A generated picture is read through its task; one uploaded in this chat has no task, so its
            // thumbnail is the chat's own authenticated address for that handle (#329 follow-up D).
            const uploadedURL = reference && reference.kind !== 'model' ? reference.url : '';
            // A frozen input picture is read as that input asset, through its owner-scoped asset route. Its
            // TaskID may name the session attachment it came from, but the copy is not an asset of that Task
            // (#80): never combine the two IDs into a task-asset address.
            const inputAssetURL = input.kind === 'asset' && input.assetId && input.previewable !== false
              ? schema.runtime.http.routes.asset_item.replace('{assetID}', encodeURIComponent(input.assetId))
              : '';
            const taskURL = input.kind !== 'asset' && input.taskId && input.assetId
              ? taskAssetRoute(schema, input.taskId, input.assetId)
              : '';
            const src = input.previewable === false ? '' : inputAssetURL
              ? previewAssetUrl(inputAssetURL) || safePreviewURL(inputAssetURL)
              : taskURL ? safeAssetURL(taskURL)
              : uploadedURL ? previewAssetUrl(uploadedURL) || safePreviewURL(uploadedURL) : '';
            return (
              <li key={`${input.handle}-${index}`} className="flex items-center gap-1.5 rounded-md border border-border bg-surface p-0.5 pr-1.5" data-testid="studio-approval-input" data-kind={input.kind}>
                <InputThumbnail src={src} handle={input.handle || label || role} model={input.kind === 'task' || input.previewable === false} />
                <span className="text-[11px]">
                  {role || label ? <span className="font-medium text-foreground" data-testid="studio-approval-input-label">{[role, label].filter(Boolean).join(' · ')}</span> : null}
                  {input.viewLabel ? <span className="ml-1 rounded-sm bg-muted px-1 text-muted-foreground" data-testid="studio-approval-input-view">{formatSchemaText(schemaText(schema, 'ui.pending_approval.view_label'), { view: input.viewLabel })}</span> : null}
                  {input.handle && reference
                    ? <HandleTag reference={reference} testId="studio-approval-input-handle" wrapperClassName={cn((role || label) && 'ml-1')} className="px-0.5 py-0 text-[11px] leading-normal text-muted-foreground hover:text-foreground" />
                    : input.handle ? <span className={cn('text-muted-foreground', (role || label) && 'ml-1')}>{input.handle}</span> : null}
                </span>
              </li>
            );
          })}
        </ul>
      ) : null}
      {model.referenceNote ? (
        // The server's reference note is the step's source note (which views or source model it is made from), or the
        // count cut; it is never on its own a block of this step, so it stays a plain note, not an "unavailable" warning
        // (the composer already warns about pictures it will not send, #94).
        <p className="text-muted-foreground [overflow-wrap:anywhere]" data-testid="studio-approval-reference-note">{model.referenceNote}</p>
      ) : null}
      {short ? (
        <UnavailableNotice
          testId="studio-approval-balance-low"
          label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.approval.balance_low'))}
          detail={schemaCountMessage(schema, 'ui.studio.approval.card.balance_low', model.credits ?? 0, { provider: providerLabel, balance: balance ?? 0, credits: model.credits ?? 0 })}
        />
      ) : null}
      {model.rechargeTaskId && !expired ? (
        <p role="status" className="text-warning" data-testid="studio-approval-recharge" data-task-id={model.rechargeTaskId}>
          {schemaText(schema, 'ui.studio.approval.card.recharge_notice')}
        </p>
      ) : null}
      {extra && showCredits && !expired ? (
        <p className="text-muted-foreground" data-testid="studio-approval-extra-hint">{schemaText(schema, 'ui.studio.approval.card.extra_hint')}</p>
      ) : null}
      {error ? (
        <p role="alert" className="text-destructive" data-testid="studio-approval-error">{error}</p>
      ) : null}
      </div>
      <footer className="flex shrink-0 items-center gap-1.5" data-testid="studio-approval-footer">
        {expired ? (
          <>
            <Action type="button" size="sm" variant="outline" disabled={busy} onClick={onRerequest} data-testid="studio-approval-rerequest">{schemaText(schema, 'ui.studio.approval.card.rerequest')}</Action>
            <Action type="button" size="sm" variant="ghost" disabled={busy} onClick={() => onDecide(false)}>{schemaText(schema, 'ui.studio.approval.card.drop')}</Action>
          </>
        ) : (
          <>
            <HintTooltip
              name={schemaText(schema, 'ui.studio.approval.card.approve')}
              reason={short ? schemaText(schema, 'ui.unavailable.reason.approval.balance_low') : invalidNativeQuote ? schemaText(schema, 'ui.unavailable.reason.approval.price_unknown') : undefined}
            >
              <Action type="button" size="sm" disabled={busy || short || invalidNativeQuote} loading={busy} onClick={() => onDecide(true)} data-testid="studio-approval-approve">{schemaText(schema, 'ui.studio.approval.card.approve')}</Action>
            </HintTooltip>
            <Action type="button" size="sm" variant="outline" disabled={busy} onClick={() => onDecide(false)} data-testid="studio-approval-deny">{schemaText(schema, 'ui.studio.approval.card.deny')}</Action>
          </>
        )}
      </footer>
    </article>
  );
}

/** Platform asset thumbnail; a neutral placeholder (a cube for a model) stands in when there is none or it fails to load. */
function InputThumbnail({ src, handle, model }: { src: string; handle: string; model: boolean }) {
  const [failed, setFailed] = useState(false);
  return (
    <span className="flex size-8 items-center justify-center overflow-hidden rounded-sm bg-muted" data-testid="studio-approval-thumb" data-state={src && !failed ? 'image' : 'placeholder'}>
      {src && !failed
        ? <img src={src} alt={handle} onError={() => setFailed(true)} className="size-full object-cover" />
        : model ? <Box aria-hidden className="size-3.5 text-muted-foreground" /> : <ImageIcon aria-hidden className="size-3.5 text-muted-foreground" />}
    </span>
  );
}
