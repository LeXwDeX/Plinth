'use client';

import { useId, type ReactNode } from 'react';
import type { ParamSchema } from '@/lib/providers/types';
import { formatSchemaText, parameterConditionMatches, parameterDisabled, parameterForcedValue, parameterNotices, parameterVisible, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { useProductSchema } from '@/components/product-schema-provider';
import { ChoiceChips } from '@/components/ui/choice-chips';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { ImageUpload } from './image-upload';

export function schemaParameterValues(parameters: ParamSchema[], values: Record<string, unknown>): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const parameter of parameters) {
    const value = values[parameter.name];
    if (value === undefined || value === '') continue;
    // 隐藏/禁用参数不提交：服务端按 Schema 默认值与 forced_when 最终裁决
    if (!parameterVisible(parameter, values) || parameterDisabled(parameter, values)) continue;
    result[parameter.name] = parameter.type === 'number' || parameter.type === 'slider' ? Number(value) : value;
  }
  return result;
}

/** A boolean that declares exactly the options "false" and "true" is a two-choice mode, not a checkbox. */
export function isBooleanChoice(parameter: ParamSchema): boolean {
  const values = (parameter.options ?? []).map(option => option.value).sort();
  return parameter.type === 'boolean' && values.length === 2 && values[0] === 'false' && values[1] === 'true';
}

/**
 * The value a locked parameter shows instead of a control: the same effective value the
 * control edits (a missing value falls back to the Schema default), written as text.
 */
export function parameterLockedText(
  parameter: ParamSchema,
  value: unknown,
  separator: string,
  words: { on: string; off: string },
): string {
  const current = value === undefined || value === null || value === '' ? parameter.default : value;
  const label = (entry: unknown) => parameter.options?.find(option => String(option.value) === String(entry))?.label ?? String(entry ?? '');
  if (parameter.type === 'boolean') {
    return parameter.options?.length ? label(String(Boolean(current))) : Boolean(current) ? words.on : words.off;
  }
  if (parameter.type === 'string-array' || parameter.type === 'multi-select') {
    return Array.isArray(current) ? current.map(label).join(separator) : '';
  }
  if (parameter.type === 'select') return label(current);
  return current === undefined || current === null ? '' : String(current);
}

export function SchemaParameters({ parameters, values, onChange, disabled, readOnly, lockedHint, parameterMeta }: {
  parameters: ParamSchema[];
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
  /** A temporary lock (saving, processing): the value stays visible, but no control renders. */
  disabled?: boolean;
  /** The person delegated this parameter (to the Agent): it is never a control they can set. */
  readOnly?: boolean;
  /** What a read-only parameter says instead of its value, such as "由 Agent 结合对话补充". */
  lockedHint?: string;
  parameterMeta?: (parameter: ParamSchema) => ReactNode;
}) {
  const { text, schema } = useProductSchema();
  const separator = schemaText(schema, 'ui.studio.list_separator');
  const id = useId();
  return <>{parameters.map(parameter => {
    if (!parameterVisible(parameter, values)) return null;
    const value = values[parameter.name];
    // A locked parameter is not a control: it shows its value (or why it has none) as plain text (#94, #180).
    const locked = disabled || readOnly || parameterDisabled(parameter, values);
    // A parameter its own condition locks is not submitted: the server applies the Schema's forced value, else the
    // default. Show that, not a choice made before the condition held (#277: a skeleton naming only a biped can pick).
    const forced = locked && !disabled && !readOnly ? parameterForcedValue(parameter, values) : { found: false };
    const fieldId = `${id}-${parameter.name}`;
    const validation = parameter.validation;
    const notices = parameterNotices(parameter, values);
    // A parameter the current settings lock says so (#94): the short reason inline, the conditions in the hint.
    const conditions = disabled ? [] : (parameter.disabled_when ?? []).filter(condition => parameterConditionMatches(condition, values));
    const conditionDetail = conditions.length
      ? formatSchemaText(schemaText(schema, 'ui.studio.params.condition_disabled'), {
        conditions: conditions.map(condition => {
          const dependency = parameters.find(entry => entry.name === condition.parameter);
          const current = values[condition.parameter];
          return `${dependency?.label ?? condition.parameter}: ${dependency?.options?.find(option => option.value === String(current))?.label ?? String(current ?? '')}`;
        }).join(schemaText(schema, 'ui.studio.list_separator')),
      })
      : '';
    const help = parameter.description || notices.length > 0 || conditionDetail
      ? <>
          {parameter.description && <p>{parameter.description}</p>}
          {notices.map(notice => <p key={notice.message_key}>{text(notice.message_key)}</p>)}
          {conditionDetail ? <UnavailableNotice testId="parameter-disabled-reason" label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.params.condition'))} detail={conditionDetail} /> : null}
        </>
      : undefined;
    const group = parameter.type === 'string-array' || parameter.type === 'multi-select';
    const choice = isBooleanChoice(parameter);
    const assetCount = Array.isArray(value) ? value.length : value ? 1 : 0;
    const lockedDisplay = !locked ? '' : (readOnly && lockedHint)
      || (parameter.type === 'asset'
        ? assetCount > 0 ? schemaCountMessage(schema, 'ui.studio.params.files_selected', assetCount, { count: assetCount }) : ''
        : parameterLockedText(parameter, forced.found ? forced.value : value, separator, { on: schemaText(schema, 'ui.studio.params.on'), off: schemaText(schema, 'ui.studio.params.off') }))
      || schemaText(schema, 'ui.studio.params.not_set');

    return <Field key={parameter.name} id={fieldId} label={parameter.label} help={help} group={group} required={parameter.required} meta={parameterMeta?.(parameter)} layout={parameter.type === 'boolean' && !choice && !locked ? 'inline' : 'stacked'}>
      {control => locked
        ? <output id={control.id} aria-describedby={control['aria-describedby']} className="text-sm text-muted-foreground" data-testid="parameter-locked-value">{lockedDisplay}</output>
        : choice
        ? <ChoiceChips id={control.id} describedBy={control['aria-describedby']} label={parameter.label} options={parameter.options ?? []} current={String(Boolean(value ?? parameter.default))} disabled={locked} onChange={selected => onChange(parameter.name, selected === 'true')} />
        : parameter.type === 'boolean'
        ? <input {...control} type="checkbox" checked={Boolean(value)} disabled={locked} onChange={event => onChange(parameter.name, event.target.checked)} />
        : parameter.type === 'select'
          ? <OptionSelect {...control} required={parameter.required} disabled={locked} value={String(value ?? '')} onValueChange={selected => onChange(parameter.name, selected)}
              placeholder={text('ui.common.select.placeholder')}
              options={[...(!parameter.required && parameter.default === undefined ? [{ value: '', label: text('ui.common.select.none') }] : []), ...(parameter.options ?? [])]} />
          : parameter.type === 'asset'
            ? <ImageUpload {...control} disabled={locked} accept={validation?.media_types?.join(',')} multiple={validation?.type === 'asset-array'} maxFiles={validation?.max_items ?? 1} maxBytes={validation?.max_bytes || undefined} onUpload={images => onChange(parameter.name, validation?.type === 'asset-array' ? images : images[0] ?? '')} />
            : group
              ? <div className="flex flex-wrap gap-x-3 gap-y-2">{parameter.options?.map((option, index) => <label key={option.value} htmlFor={`${fieldId}-${index}`} className="inline-flex items-center gap-1 text-sm"><input id={`${fieldId}-${index}`} type="checkbox" disabled={locked} checked={Array.isArray(value) && value.includes(option.value)} onChange={event => { const selected = Array.isArray(value) ? value : []; onChange(parameter.name, event.target.checked ? [...selected, option.value] : selected.filter(item => item !== option.value)); }} />{option.label}</label>)}</div>
              : parameter.type === 'slider'
                ? <div className="flex items-center gap-3">
                    <input {...control} type="range" className="w-full" disabled={locked} required={parameter.required} min={parameter.min} max={parameter.max} step={parameter.step ?? (validation?.integer ? 1 : 'any')} value={String(value ?? parameter.default ?? parameter.min ?? 0)} onChange={event => onChange(parameter.name, event.target.value === '' ? '' : Number(event.target.value))} />
                    <output htmlFor={fieldId} className="shrink-0 text-sm text-muted-foreground tabular-nums">{String(value ?? parameter.default ?? '')}</output>
                  </div>
                : <>
                    <input {...control} className="w-full rounded-md border bg-background px-3 py-2" type={parameter.type === 'number' ? 'number' : 'text'} value={String(value ?? '')} onChange={event => onChange(parameter.name, event.target.value === '' ? '' : parameter.type === 'number' ? Number(event.target.value) : event.target.value)} disabled={locked} required={parameter.required} min={parameter.min} max={parameter.max} step={parameter.step ?? (validation?.integer ? 1 : 'any')} maxLength={validation?.max_length} list={parameter.options?.length ? `${fieldId}-options` : undefined} />
                    {parameter.options?.length ? <datalist id={`${fieldId}-options`}>{parameter.options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</datalist> : null}
                  </>}
    </Field>;
  })}</>;
}
