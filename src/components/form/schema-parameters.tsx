'use client';

import { useId, type ReactNode } from 'react';
import type { ParamSchema } from '@/lib/providers/types';
import { parameterDisabled, parameterNotices, parameterVisible } from '@/lib/product-schema';
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

export function SchemaParameters({ parameters, values, onChange, disabled, parameterMeta }: {
  parameters: ParamSchema[];
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
  disabled?: boolean;
  parameterMeta?: (parameter: ParamSchema) => ReactNode;
}) {
  const { text, schema } = useProductSchema();
  const id = useId();
  return <>{parameters.map(parameter => {
    if (!parameterVisible(parameter, values)) return null;
    const value = values[parameter.name];
    const locked = disabled || parameterDisabled(parameter, values);
    const fieldId = `${id}-${parameter.name}`;
    const validation = parameter.validation;
    const notices = parameterNotices(parameter, values);
    const help = parameter.description || notices.length > 0
      ? <>
          {parameter.description && <p>{parameter.description}</p>}
          {notices.map(notice => <p key={notice.message_key}>{text(notice.message_key)}</p>)}
        </>
      : undefined;
    const group = parameter.type === 'string-array' || parameter.type === 'multi-select';
    const choice = isBooleanChoice(parameter);

    return <Field key={parameter.name} id={fieldId} label={parameter.label} help={help} group={group} required={parameter.required} layout={parameter.type === 'boolean' && !choice ? 'inline' : 'stacked'}>
      {control => <>
        {parameterMeta?.(parameter)}
        {choice
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
      </>}
    </Field>;
  })}</>;
}
