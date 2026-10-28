'use client';

import { useState } from 'react';
import { Box, Clock, Image as ImageIcon, ShieldQuestion } from 'lucide-react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { useBalance } from '@/hooks/use-balance';
import { useHandleReferences } from '@/hooks/use-handle-references';
import { HandleTag } from '@/components/chat/handle-tag';
import { fetchCapabilityOfferings, NO_CREDITS } from '@/lib/agent-offerings';
import { accountOfCard, approvalInputParameterLabel, approvalOfferingNames, approvalShowsCredits, creditBalanceOf, isRetextureCard, remainingValidity, type ApprovalCardModel } from '@/lib/chat/approval-card';
import { safeAssetURL, safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { formatNumber } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { formatNativePrice, formatPriceTotal } from '@/lib/pricing';
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

  return (
    <article
      data-testid="studio-approval-card"
      data-status={expired ? 'expired' : 'pending'}
      aria-label={schemaText(schema, 'ui.studio.approval.card.title')}
      className={cn(
        'flex flex-col gap-2 rounded-lg border bg-panel-raised p-3 text-xs',
        expired ? 'border-border opacity-90' : 'border-warning/40',
      )}
    >
      <header className="flex items-center gap-2">
        <ShieldQuestion aria-hidden className={cn('size-4 shrink-0', expired ? 'text-muted-foreground' : 'text-warning')} />
        <span className="text-[13px] font-semibold">{schemaText(schema, 'ui.studio.approval.card.title')}</span>
        <span className={cn('ml-auto flex items-center gap-1', expired ? 'text-muted-foreground' : 'text-warning')} data-testid="studio-approval-validity">
          <Clock aria-hidden className="size-3" />
          {validityText}
        </span>
      </header>
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
        {model.nativeQuote ? (
          <>
            <dt className="text-muted-foreground">{formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount: formatNativePrice(model.nativeQuote.total_amount, model.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') })}</dt>
            <dd className="font-medium tabular-nums" data-testid="studio-approval-native-price">
              <span>{formatSchemaText(schemaText(schema, 'ui.pricing.base_amount'), { amount: formatNativePrice(model.nativeQuote.base_amount, model.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown') })}</span>
              {model.nativeQuote.components?.map((component) => <span key={component.id} className="ml-2 text-muted-foreground">{component.label}: {formatNativePrice(component.amount, model.nativeQuote!.unit_label) ?? schemaText(schema, 'ui.pricing.unknown')}</span>)}
            </dd>
          </>
        ) : invalidNativeQuote ? (
          <><dt className="text-muted-foreground">{formatSchemaText(schemaText(schema, 'ui.pricing.frozen_estimate'), { amount: schemaText(schema, 'ui.pricing.unknown') })}</dt><dd role="alert" data-testid="studio-approval-native-price-unknown">{schemaText(schema, 'ui.pricing.unknown')}</dd></>
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
      {(model.parameterAdjustments?.length ?? 0) > 0 ? <section className="flex flex-col gap-2 border-t border-border pt-2" data-testid="studio-approval-parameter-adjustments" aria-label={schemaText(schema, 'ui.studio.approval.card.parameter_adjustments')}>
        <h4 className="font-medium">{schemaText(schema, 'ui.studio.approval.card.parameter_adjustments')}</h4>
        {model.parameterAdjustments?.map(item => <div key={item.name} className="flex flex-col gap-1 [overflow-wrap:anywhere]" data-parameter={item.name}>
          <p className="font-medium">{item.label} <span className="font-normal text-muted-foreground">{schemaText(schema, 'ui.studio.params.source_agent')}</span></p>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.recommended_value')}</dt><dd>{typeof item.recommendedValue === 'string' ? item.recommendedValue : JSON.stringify(item.recommendedValue)}</dd>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.actual_value')}</dt><dd>{typeof item.value === 'string' ? item.value : JSON.stringify(item.value)}</dd>
            <dt className="text-muted-foreground">{schemaText(schema, 'ui.studio.approval.card.adjustment_reason')}</dt><dd>{item.reason}</dd>
          </dl>
          {item.advice ? <p className="text-muted-foreground">{item.advice}</p> : null}
        </div>)}
      </section> : null}
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
        <p className="text-muted-foreground [overflow-wrap:anywhere]" data-testid="studio-approval-reference-note">{model.referenceNote}</p>
      ) : null}
      {short ? (
        <p role="status" className="text-warning" data-testid="studio-approval-balance-low">
          {schemaCountMessage(schema, 'ui.studio.approval.card.balance_low', model.credits ?? 0, { provider: providerLabel, balance: balance ?? 0, credits: model.credits ?? 0 })}
        </p>
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
      <footer className="flex items-center gap-1.5">
        {expired ? (
          <>
            <Action type="button" size="sm" variant="outline" disabled={busy} onClick={onRerequest} data-testid="studio-approval-rerequest">{schemaText(schema, 'ui.studio.approval.card.rerequest')}</Action>
            <Action type="button" size="sm" variant="ghost" disabled={busy} onClick={() => onDecide(false)}>{schemaText(schema, 'ui.studio.approval.card.drop')}</Action>
          </>
        ) : (
          <>
            <Action type="button" size="sm" disabled={busy || short || invalidNativeQuote} loading={busy} onClick={() => onDecide(true)} data-testid="studio-approval-approve">{schemaText(schema, 'ui.studio.approval.card.approve')}</Action>
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
