'use client';

import { useId, useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { ChevronDown, ChevronRight, Minus, Plus, SlidersHorizontal } from 'lucide-react';

import { SchemaParameters } from '@/components/form/schema-parameters';
import { useProductSchema } from '@/components/product-schema-provider';
import { ChoiceChips } from '@/components/ui/choice-chips';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { OptionSelect } from '@/components/ui/option-select';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import type { useAgentEffectiveConfiguration } from '@/hooks/use-agent-effective-configuration';
import type { CanvasDelivery } from '@/hooks/use-canvas-artboard';
import type { GenerationParams } from '@/hooks/use-generation-params';
import { CanvasSizeNotice, ReferenceSizeNotice } from '@/components/canvas/canvas-size-notice';
import { canvasResizeForChoice } from '@/lib/workspace/canvas-size';
import { applyCanvasResize } from '@/lib/canvas/apply-canvas-resize';
import { CanvasResizeAnchor } from '@/components/canvas/canvas-resize-anchor';
import { formatSchemaText, parameterConditionMatches, parameterDisabled, parameterVisible, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { formatNativePrice, parseNativeQuote } from '@/lib/pricing';
import { PICKER_TRIGGER_CLASS } from '@/components/studio/table-popover';
import { defaultParamsChipText, generationCount, paramsChipText, paramsRatioText, readonlyParameterText, sizeOptionText } from '@/lib/workspace/generation-params';
import type { ParamSchema } from '@/lib/providers/types';

/** More options than this read better as a list than as a row of chips. */
const CHIP_LIMIT = 8;

export interface ParamsPopoverProps {
  params: GenerationParams;
  domains: GenerationParams[];
  /** Settings are saved per session; without one there is nothing to save to. */
  hasSession: boolean;
  disabled?: boolean;
  /** The selected Skill, whose recommended size becomes the size default. */
  skill: { name: string; recommendedSize: string } | null;
  effective: ReturnType<typeof useAgentEffectiveConfiguration>;
  /** #246: the session's canvas. A 2D size chosen here resizes it; a mismatch with the model is shown. */
  canvas?: { sessionId: string; delivery: CanvasDelivery } | null;
}

function ChoiceControl({ param, value, disabled, onChange, optionText }: {
  param: ParamSchema;
  value: unknown;
  disabled: boolean;
  onChange: (value: string) => void;
  /** How an option reads; size options also say their ratio. */
  optionText?: (option: { value: string; label: string }) => string;
}) {
  const options = (param.options ?? []).map((option) => ({ ...option, label: optionText ? optionText(option) : option.label }));
  const current = String(value ?? param.default ?? '');
  if (options.length > 0 && options.length <= CHIP_LIMIT && param.type === 'select') {
    return <ChoiceChips label={param.label} options={options} current={current} disabled={disabled} onChange={onChange} />;
  }
  return <OptionSelect aria-label={param.label} value={current} options={options} disabled={disabled} onValueChange={onChange} className="h-8 text-xs" />;
}

function Row({ label, note, children }: { label: string; note?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium">{label}</span>
        {note ? <span className="truncate text-[11px] text-primary">{note}</span> : null}
      </div>
      {children}
    </div>
  );
}

/** Source is projected by the server; a control interaction alone creates a user pin. */
function ParameterOrigin({ params, name, disabled = false, readonly = false, size = false, onReset }: { params: GenerationParams; name: string; disabled?: boolean; readonly?: boolean; size?: boolean; onReset?: () => void }) {
  const { schema } = useProductSchema();
  const text = (key: string) => schemaText(schema, key);
  const state = params.parameterStates?.[name] ?? { source: 'default' as const };
  const manual = state.source === 'user';
  // #267: an output size always names a target (the canvas matches it), so it is never "自动";
  // without a choice it is the Schema default, and resetting returns to that default.
  const unpinned = size ? 'ui.studio.params.size_default' : 'ui.studio.params.auto';
  const reset = size ? 'ui.studio.params.reset_size_default' : 'ui.studio.params.reset_auto';
  return <div className="flex flex-col gap-1 text-[11px] leading-4 text-muted-foreground" data-testid="parameter-origin" data-parameter={name} data-source={state.source}>
    <div className="flex items-baseline gap-2">
      <span className={manual ? 'font-medium text-foreground' : ''}>{text(manual ? 'ui.studio.params.explicit' : unpinned)}</span>
      {!manual ? <span>{text(state.source === 'agent' ? 'ui.studio.params.source_agent' : 'ui.studio.params.source_default')}</span> : null}
      {!readonly && state.source !== 'default' ? <button type="button" disabled={disabled} onClick={() => { params.setValue(name, null); onReset?.(); }} className="ml-auto shrink-0 rounded text-primary underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40" data-testid="parameter-reset-auto">{text(reset)}</button> : null}
    </div>
    {state.source === 'agent' && state.reason ? <p className="[overflow-wrap:anywhere]" data-testid="parameter-agent-reason">{state.reason}</p> : null}
  </div>;
}

/** 参数弹层外框：固定高度（不超过可用高度），见 #243。 */
export const PARAMS_PANEL_CLASS =
  'flex h-[min(34rem,var(--available-height))] w-[44rem] max-w-(--available-width) flex-col overflow-hidden rounded-lg bg-popover p-3 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10';

/**
 * 参数：比例、尺寸、数量，以及该模型声明的其他设置。内容全部来自所选 Offering 的
 * Schema，只显示模型支持的项；写入与 Agent 的 set_params 共用同一个会话参数 store。
 */
export function ParamsPopover({ params, domains, hasSession, disabled = false, skill, effective, canvas = null }: ParamsPopoverProps) {
  const { schema } = useProductSchema();
  const layer = useAnchoredPositioner();
  const [open, setOpen] = useState(false);
  const text = (key: string) => schemaText(schema, key);
  const hasModel = params.modelName !== '';
  const countText = (value: number) => schemaCountMessage(schema, 'ui.studio.params.summary_count', value, { count: value });
  // Before a model is chosen the chip still says what a new turn starts from: the product's defaults.
  const summary = hasModel
    ? paramsChipText(params.nextTurn.model, params.nextTurn.values, params.nextTurn.estimate, countText) || text('ui.studio.params.chip_none')
    : params.domain === 'image'
      ? defaultParamsChipText(text('ui.studio.params.default_ratio'), Number(text('ui.studio.params.default_count')), countText)
      : text('ui.studio.params.chip_pick_model');
  const label = text('ui.studio.params.title');
  const compact = (hasModel ? paramsRatioText(params.nextTurn.model, params.nextTurn.values) : params.domain === 'image' ? text('ui.studio.params.default_ratio') : '') || label;

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      {/* The same frame as the pickers beside it; the full summary is the hover hint, the button shows the ratio only. */}
      <HintTooltip name={`${label}: ${summary}`} hint={text('ui.studio.params.chip_hint')} className="w-full min-w-0">
        <Popover.Trigger
          disabled={disabled}
          data-testid="composer-params"
          aria-label={`${label}: ${summary}`}
          className={PICKER_TRIGGER_CLASS}
        >
          <SlidersHorizontal aria-hidden className="size-3 shrink-0 text-primary" />
          <span className="flex-1 truncate text-left tabular-nums" data-testid="composer-params-summary">{compact}</span>
          <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        </Popover.Trigger>
      </HintTooltip>
      <Popover.Portal>
        <Popover.Positioner side="top" align="end" sideOffset={8} collisionPadding={12} className={layer}>
          <Popover.Popup
            data-testid="composer-params-panel"
            // #243：固定外框高度（不超过可用高度），切换适用操作或展开更多参数时外框、标题与操作选择不再移动；
            // 内容在下方区域内滚动。弹层锚在触发器上方，只用 max-h 时内容一变高上边缘就会上移。
            className={PARAMS_PANEL_CLASS}
          >
            <div className="flex items-baseline justify-between gap-2 pb-2">
              <Popover.Title className="text-xs font-semibold">{text('ui.studio.params.title')}</Popover.Title>
              {hasModel ? (
                <Popover.Description className="truncate text-[11px] text-muted-foreground">
                  {formatSchemaText(text('ui.studio.params.model'), { model: params.modelName })}
                </Popover.Description>
              ) : null}
            </div>
            <div className="flex min-h-0 flex-col gap-3 overflow-y-auto">
              <div className="grid items-start gap-4 min-[760px]:grid-cols-2" data-testid="params-domains">
                {domains.map((domain) => (
                  <ParamsDomainSection
                    key={domain.domain}
                    params={domain}
                    hasSession={hasSession}
                    disabled={disabled}
                    skill={domain.domain === params.domain ? skill : null}
                    canvas={domain.domain === 'image' ? canvas : null}
                  />
                ))}
              </div>

              <details className="border-t border-border pt-2 text-[11px] text-muted-foreground" data-testid="params-effective">
                <summary className="cursor-pointer select-none text-xs font-medium">{schema.agent_configuration.ui.effective_title}</summary>
                <div className="mt-1.5 flex flex-col gap-1">
                  {effective.loading ? <p>{text('ui.workspace.harness.loading')}</p> : effective.failed ? <p role="alert">{schema.agent_configuration.ui.load_failed}</p> : effective.data ? (
                    <>
                      <p>{schema.agent_configuration.ui.dialogue_model}: {effective.data.model.label}</p>
                      <p>{schema.agent_configuration.ui.revision}: {effective.data.revision}</p>
                      <p>{schema.agent_configuration.ui.source}: {effective.data.parameter_source}</p>
                    </>
                  ) : <p>{schema.agent_configuration.ui.effective_empty}</p>}
                </div>
              </details>
              <p className="text-[11px] leading-4 text-muted-foreground">{text('ui.workspace.harness.agent_decides')}</p>
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** One capability owns one form and one parameter scope. Read-only exposure is explicitly opt-in. */
export function ParamsDomainSection({ params, hasSession, disabled, skill, canvas = null }: Pick<ParamsPopoverProps, 'params' | 'hasSession' | 'disabled' | 'skill' | 'canvas'>) {
  const { schema } = useProductSchema();
  /**
   * #246: a size the person picks (or resets to the default) is the canvas size too: the boundary changes as
   * one undo step, content keeping its pixel size and its place relative to the chosen anchor. Only a canvas
   * whose size is already known is resized; a canvas still loading starts from the chosen size anyway.
   */
  const resizeCanvasTo = (name: string, value: unknown) => {
    if (!canvas) return;
    const pixels = canvasResizeForChoice(canvas.delivery.known, params.offering, params.model.sizeParams, params.values,
      name, value, schema.canvas.frontend.artboard, schema.canvas.limits);
    if (pixels) applyCanvasResize(schema, canvas.sessionId, pixels);
  };
  const text = (key: string) => schemaText(schema, key);
  const [showMore, setShowMore] = useState(false);
  const { model, values, setValue, estimate } = params;
  const hasModel = params.modelName !== '';
  const unavailable = params.offering?.available === false;
  disabled = disabled || !params.allowed || unavailable || params.failed || !params.ready;
  const count = model.countParam;
  const countDisabled = count ? parameterDisabled(count, values) : false;
  const countValue = generationCount(count, values);
  const reasonId = useId();
  const disabledReason = !hasSession ? text('ui.studio.params.no_session')
    : unavailable ? text('ui.studio.params.unavailable')
    : !params.allowed ? params.restrictedReason
    : params.failed ? text('ui.studio.params.load_failed')
    : !params.ready ? text('ui.studio.params.loading')
    : disabled ? text('ui.studio.params.edit_disabled') : '';
  const conditionReason = (param: ParamSchema) => {
    const conditions = (param.disabled_when ?? []).filter((condition) => parameterConditionMatches(condition, values));
    if (!conditions.length) return '';
    const labels = conditions.map((condition) => {
      const dependency = model.paramSchemas.find((entry) => entry.name === condition.parameter);
      const value = values[condition.parameter];
      const label = dependency?.options?.find((option) => option.value === String(value))?.label ?? String(value ?? '');
      return `${dependency?.label ?? condition.parameter}: ${label}`;
    });
    return formatSchemaText(text('ui.studio.params.condition_disabled'), { conditions: labels.join(text('ui.studio.list_separator')) });
  };
  const countReason = count ? disabledReason || conditionReason(count) : '';
  const countLocked = !hasSession || disabled || countDisabled;
  const countMinimum = count?.min ?? 1;
  const countMaximum = count?.max;
  const lessReason = countReason || (countValue <= countMinimum ? schemaCountMessage(schema, 'ui.studio.params.count_minimum', countMinimum, { count: countMinimum }) : '');
  const moreReason = countReason || (countMaximum !== undefined && countValue >= countMaximum ? schemaCountMessage(schema, 'ui.studio.params.count_maximum', countMaximum, { count: countMaximum }) : '');
  const sizeText = (option: { value: string; label: string }) => sizeOptionText(option, text('ui.studio.params.size_option'));
  const nothingToSet = hasModel && model.paramSchemas.length === 0 && model.readonlyParams.length === 0;
  const capabilityLabel = (id: string) => schema.capabilities.find((capability) => capability.id === id)?.label_cn ?? id;
  const offeringOptions = params.offerings.map((offering) => ({
    value: offering.id,
    label: capabilityLabel(offering.capability),
  }));
  const readonlyParams = model.readonlyParams.filter((wire) =>
    parameterVisible(wire.presentation ?? {}, values)
    && !(wire.omit_when ?? []).some((condition) => parameterConditionMatches(condition, values)));
  const nativeQuote = parseNativeQuote(estimate.nativeDefaultQuote);
  const nativeUpperQuote = parseNativeQuote(estimate.nativeUpperQuote);
  const nativePriceLines = estimate.nativeDefaultQuote !== undefined || params.offering?.price_mode === 'native'
    ? nativeQuote ? [
        formatSchemaText(text('ui.pricing.default_estimate'), { amount: formatNativePrice(nativeQuote.total_amount, nativeQuote.unit_label) ?? text('ui.pricing.unknown') }),
        formatSchemaText(text('ui.pricing.base_amount'), { amount: formatNativePrice(nativeQuote.base_amount, nativeQuote.unit_label) ?? text('ui.pricing.unknown') }),
        ...(nativeQuote.components ?? []).map((component) => `${component.label}: ${formatNativePrice(component.amount, nativeQuote.unit_label) ?? text('ui.pricing.unknown')}`),
        ...(nativeUpperQuote && (nativeUpperQuote.unit !== nativeQuote.unit || nativeUpperQuote.total_amount !== nativeQuote.total_amount)
          ? [formatSchemaText(text('ui.pricing.upper_estimate'), { amount: formatNativePrice(nativeUpperQuote.total_amount, nativeUpperQuote.unit_label) ?? text('ui.pricing.unknown') })]
          : []),
      ] : [text('ui.pricing.unknown')]
    : [];
  return (
    <section className="flex min-w-0 flex-col gap-3 rounded-md border border-border p-3" data-testid="params-domain" data-domain={params.domain} data-offering={params.offering?.id} aria-disabled={!hasSession || disabled}>
      <div>
        <h3 className="text-xs font-semibold">{text(params.domain === 'image' ? 'ui.studio.params.section_2d' : 'ui.studio.params.section_3d')}</h3>
        <p className="truncate text-[11px] text-muted-foreground">{params.modelName}</p>
      </div>
      {params.offerings.length > 1 ? (
        <Row label={text('ui.studio.params.capability')}>
          <OptionSelect
            aria-label={text('ui.studio.params.capability')}
            value={params.offering?.id ?? ''}
            options={offeringOptions}
            onValueChange={params.selectOffering}
          />
        </Row>
      ) : params.offering ? (
        <p className="text-[11px] text-muted-foreground">{capabilityLabel(params.offering.capability)}</p>
      ) : null}
      {disabledReason ? <p id={reasonId} role="status" className="text-xs text-muted-foreground" data-testid="params-disabled-reason">{disabledReason}</p> : null}
      {params.failed ? (
        <p role="alert" className="text-xs text-destructive">{text('ui.studio.params.load_failed')}</p>
      ) : !params.ready && hasModel ? (
        <p role="status" className="text-xs text-muted-foreground">{text('ui.studio.params.loading')}</p>
      ) : null}
      {unavailable ? (
        <p role="status" className="text-xs text-muted-foreground">{text('ui.studio.params.unavailable')}</p>
      ) : null}
      {params.restrictedReason ? (
        <p role="status" className="text-xs text-muted-foreground" data-testid="params-scope-restriction">{params.restrictedReason}</p>
      ) : null}
      {params.saveError ? <p role="alert" className="text-xs text-destructive">{params.saveError}</p> : null}
      {!hasModel ? (
        <p role="status" className="text-xs leading-5 text-muted-foreground" data-testid="params-no-model">
          {text(params.failed ? 'ui.studio.params.load_failed' : params.ready ? 'ui.studio.params.no_model' : 'ui.studio.params.loading')}
        </p>
      ) : nothingToSet ? (
        <p role="status" className="text-xs leading-5 text-muted-foreground">{text('ui.studio.params.nothing')}</p>
      ) : null}

      {model.sizeParams.filter((param) => parameterVisible(param, values)).map((param) => {
        const fromSkill = skill !== null && skill.recommendedSize !== '' && String(values[param.name] ?? '') === skill.recommendedSize;
        return (
          <Row
            key={param.name}
            label={param.label}
            note={fromSkill ? formatSchemaText(text('ui.studio.params.from_skill'), { skill: skill?.name ?? '' }) : undefined}
          >
            <ParameterOrigin params={params} name={param.name} size disabled={!hasSession || disabled || parameterDisabled(param, values)} onReset={() => resizeCanvasTo(param.name, null)} />
            <ChoiceControl param={param} value={values[param.name]} disabled={!hasSession || disabled || parameterDisabled(param, values)} onChange={(value) => { setValue(param.name, value); resizeCanvasTo(param.name, value); }} optionText={sizeText} />
            {conditionReason(param) ? <p className="text-[11px] text-muted-foreground" data-testid="parameter-disabled-reason">{conditionReason(param)}</p> : null}
          </Row>
        );
      })}

      {canvas && model.sizeParams.length > 0 ? (
        <>
          <ReferenceSizeNotice delivery={canvas.delivery} />
          <CanvasSizeNotice sessionId={canvas.sessionId} delivery={canvas.delivery} />
          <p className="text-[11px] leading-4 text-muted-foreground" data-testid="canvas-resize-hint">{text('ui.canvas.size.resize_hint')}</p>
          <CanvasResizeAnchor disabled={!hasSession || disabled} />
        </>
      ) : null}

      {count && parameterVisible(count, values) ? (
        <>
          {model.countDependencies.filter((param) => parameterVisible(param, values)).map((param) => (
            <Row key={param.name} label={param.label}>
              <ParameterOrigin params={params} name={param.name} disabled={!hasSession || disabled || parameterDisabled(param, values)} />
              <ChoiceControl param={param} value={values[param.name]} disabled={!hasSession || disabled || parameterDisabled(param, values)} onChange={(value) => setValue(param.name, value)} />
              {conditionReason(param) ? <p className="text-[11px] text-muted-foreground" data-testid="parameter-disabled-reason">{conditionReason(param)}</p> : null}
            </Row>
          ))}
          <Row label={count.label}>
            <ParameterOrigin params={params} name={count.name} disabled={!hasSession || disabled || countDisabled} />
            <div className="flex items-center gap-2" data-testid="params-count" aria-disabled={countLocked} aria-describedby={disabledReason ? reasonId : countReason ? `${reasonId}-count` : undefined}>
              <div className={`flex items-center rounded-md border ${countLocked ? 'border-border bg-muted text-muted-foreground' : 'border-foreground/30 bg-surface text-foreground'}`}>
                <HintTooltip name={text('ui.studio.params.count_less')} reason={lessReason}>
                  <button
                    type="button"
                    aria-label={text('ui.studio.params.count_less')}
                    aria-description={lessReason || undefined}
                    disabled={countLocked || countValue <= countMinimum}
                    onClick={() => setValue(count.name, generationCount(count, { ...values, [count.name]: countValue - (count.step ?? 1) }))}
                    className="flex size-7 items-center justify-center rounded-l-md hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground"
                  >
                    <Minus aria-hidden className="size-3" />
                  </button>
                </HintTooltip>
                <output aria-live="polite" className="min-w-8 text-center text-xs tabular-nums" data-testid="params-count-value">{countValue}</output>
                <HintTooltip name={text('ui.studio.params.count_more')} reason={moreReason}>
                  <button
                    type="button"
                    aria-label={text('ui.studio.params.count_more')}
                    aria-description={moreReason || undefined}
                    disabled={countLocked || (countMaximum !== undefined && countValue >= countMaximum)}
                    onClick={() => setValue(count.name, generationCount(count, { ...values, [count.name]: countValue + (count.step ?? 1) }))}
                    className="flex size-7 items-center justify-center rounded-r-md hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground"
                  >
                    <Plus aria-hidden className="size-3" />
                  </button>
                </HintTooltip>
              </div>
              <span className="min-w-0 text-[11px] leading-4 text-muted-foreground" data-testid="params-cost">
                {nativePriceLines.length > 0 ? nativePriceLines.join(' · ') : estimate.total === null
                  ? (estimate.metered ? text('ui.workspace.harness.credits_unknown') : null)
                  : estimate.count > 1
                    ? formatSchemaText(text('ui.studio.params.cost_many'), { unit: estimate.unit ?? 0, total: estimate.total })
                    : formatSchemaText(text('ui.studio.params.cost_one'), { total: estimate.total })}
              </span>
            </div>
            {!disabledReason && countReason ? <p id={`${reasonId}-count`} className="text-[11px] text-muted-foreground" data-testid="parameter-disabled-reason">{countReason}</p> : null}
          </Row>
        </>
      ) : hasModel && nativePriceLines.length > 0 ? (
        <div className="flex flex-col gap-1 text-[11px] text-muted-foreground" data-testid="params-cost">
          {nativePriceLines.map((line, index) => <p key={index}>{line}</p>)}
        </div>
      ) : hasModel && estimate.total !== null ? (
        <p className="text-[11px] text-muted-foreground" data-testid="params-cost">{formatSchemaText(text('ui.studio.params.cost_one'), { total: estimate.total })}</p>
      ) : null}

      {model.moreParams.length > 0 ? (
        <div className="flex flex-col gap-2 border-t border-border pt-2">
          <button
            type="button"
            aria-expanded={showMore}
            onClick={() => setShowMore((value) => !value)}
            className="flex items-center gap-1 self-start rounded text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
          >
            {showMore ? <ChevronDown aria-hidden className="size-3.5" /> : <ChevronRight aria-hidden className="size-3.5" />}
            {text('ui.studio.params.more')}
          </button>
          {showMore ? (
            <div className="flex flex-col gap-3 text-xs" data-testid="params-more">
              <SchemaParameters parameters={model.moreParams} values={values} disabled={!hasSession || disabled} onChange={setValue} parameterMeta={(param) => <><ParameterOrigin params={params} name={param.name} disabled={!hasSession || disabled || parameterDisabled(param, values)} />{conditionReason(param) ? <p className="text-[11px] text-muted-foreground" data-testid="parameter-disabled-reason">{conditionReason(param)}</p> : null}</>} />
            </div>
          ) : null}
        </div>
      ) : null}

      {readonlyParams.map((wire) => (
        <Row key={wire.name} label={wire.label} note={text(wire.adjustment_policy === 'advised' ? 'ui.studio.params.not_recommended' : 'ui.studio.params.readonly')}>
          {wire.adjustment_policy === 'advised' ? <ParameterOrigin params={params} name={wire.name} readonly /> : null}
          <output className="text-xs leading-5" data-testid="params-readonly" data-parameter={wire.name}>
            {readonlyParameterText(wire, values, text('ui.studio.list_separator'))}
          </output>
          {wire.adjustment_reason || wire.description ? <p className="text-[11px] leading-4 text-muted-foreground">{wire.adjustment_reason || wire.description}</p> : null}
        </Row>
      ))}
    </section>
  );
}
