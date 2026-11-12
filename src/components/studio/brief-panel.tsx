'use client';

import { useEffect, useId, useMemo, useState } from 'react';
import { Pencil } from 'lucide-react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { ChatBrowserError, createChatBrowserClient } from '@/lib/chat/browser-client';
import {
  briefIsEmpty,
  briefPayload,
  briefSelectText,
  briefValueText,
  parseProductionBrief,
  productionBriefFields,
  type ProductionBrief,
  type ProductionBriefField,
} from '@/lib/chat/production-brief';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';

const EMPTY: ProductionBrief = {};

function hasValue(value: unknown): boolean {
  return value !== undefined && value !== null && value !== '';
}

function purposeLabel(schema: ReturnType<typeof useProductSchema>['schema'], value: unknown): string {
  if (typeof value !== 'string' || value === '') return '';
  return schema.chat.model3d_usage?.profiles?.find((profile) => profile.id === value)?.label ?? value;
}

/** #239: the 2D purpose labels come from the public image2d_usage projection. */
function imagePurposeLabel(schema: ReturnType<typeof useProductSchema>['schema'], value: unknown): string {
  if (typeof value !== 'string' || value === '') return '';
  return schema.chat.image2d_usage?.profiles?.find((profile) => profile.id === value)?.label ?? value;
}

function valueText(
  schema: ReturnType<typeof useProductSchema>['schema'],
  field: ProductionBriefField,
  value: unknown,
): string {
  if (field.type === 'profile') return purposeLabel(schema, value);
  if (field.type === 'image_profile') return imagePurposeLabel(schema, value);
  if (field.type === 'select') return briefSelectText(field, value);
  return briefValueText(value, schemaText(schema, 'ui.studio.brief.yes'), schemaText(schema, 'ui.studio.brief.no'));
}

/** One editable control per declared field; the field type and limits come from the Schema. */
function BriefFieldInput({
  field, id, value, onChange,
}: {
  field: ProductionBriefField;
  id: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const { schema } = useProductSchema();
  const shared = 'w-full rounded-md border border-border bg-background px-2 py-1 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring';
  if (field.type === 'boolean') {
    return (
      <input
        id={id}
        type="checkbox"
        checked={value === true}
        onChange={(event) => onChange(event.target.checked)}
        className="size-4 accent-primary"
        data-testid={`studio-brief-field-${field.name}`}
      />
    );
  }
  if (field.type === 'profile' || field.type === 'image_profile') {
    const profiles = field.type === 'image_profile'
      ? schema.chat.image2d_usage?.profiles ?? []
      : schema.chat.model3d_usage?.profiles ?? [];
    return (
      <select
        id={id}
        value={typeof value === 'string' ? value : ''}
        onChange={(event) => onChange(event.target.value || undefined)}
        className={shared}
        data-testid={`studio-brief-field-${field.name}`}
      >
        <option value="">{schemaText(schema, 'ui.studio.brief.none')}</option>
        {profiles.map((profile) => (
          <option key={profile.id} value={profile.id}>{profile.label}</option>
        ))}
      </select>
    );
  }
  if (field.type === 'select') {
    const options = field.options ?? [];
    const current = typeof value === 'string' ? value : '';
    // #286: a stored value that is no option (text written before the field
    // became a select) stays visible and selectable, marked as the earlier
    // entry it is, so saving the form does not silently drop it.
    const unlisted = current !== '' && !options.some((option) => option.id === current);
    return (
      <select
        id={id}
        value={current}
        onChange={(event) => onChange(event.target.value || undefined)}
        className={shared}
        data-testid={`studio-brief-field-${field.name}`}
      >
        <option value="">{schemaText(schema, 'ui.studio.brief.none')}</option>
        {unlisted ? (
          <option value={current}>{formatSchemaText(schemaText(schema, 'ui.studio.brief.unlisted'), { value: current })}</option>
        ) : null}
        {options.map((option) => (
          <option key={option.id} value={option.id}>{option.label}</option>
        ))}
      </select>
    );
  }
  if (field.type === 'integer') {
    return (
      <input
        id={id}
        type="number"
        min={field.minimum}
        max={field.maximum}
        value={typeof value === 'number' || typeof value === 'string' ? value : ''}
        onChange={(event) => onChange(event.target.value === '' ? undefined : Number(event.target.value))}
        className={`${shared} tabular-nums`}
        data-testid={`studio-brief-field-${field.name}`}
      />
    );
  }
  const text = typeof value === 'string' ? value : '';
  const maximum = field.maximumCharacters;
  // #103: the Schema declares which text fields are long and what hint they
  // show; no field name or character-count heuristic decides that here.
  const placeholder = field.placeholder;
  return field.multiline ? (
    <textarea
      id={id}
      value={text}
      maxLength={maximum}
      rows={3}
      placeholder={placeholder}
      onChange={(event) => onChange(event.target.value)}
      className={`${shared} min-h-16 resize-y`}
      data-testid={`studio-brief-field-${field.name}`}
    />
  ) : (
    <input
      id={id}
      type="text"
      value={text}
      maxLength={maximum}
      placeholder={placeholder}
      onChange={(event) => onChange(event.target.value)}
      className={shared}
      data-testid={`studio-brief-field-${field.name}`}
    />
  );
}

/**
 * #103: the session's production brief in the right panel's 详情 tab. The
 * server owns the stored brief; the panel reads it when the tab opens and
 * saves the whole declared object back. Field names, labels and limits come
 * from the Schema (`chat.model3d_usage.brief.fields`). An empty brief invites
 * the person to start 3D work; editing is explicit, so the read view stays
 * compact.
 */
export function BriefPanel({ sessionId }: { sessionId: string | null }) {
  const { schema } = useProductSchema();
  const fields = useMemo(() => productionBriefFields(schema), [schema]);
  const supported = Boolean(schema.runtime.http.routes.chat_session_brief) && fields.length > 0;
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const stored = useChatStore((state) => state.sessions.find((session) => session.id === sessionId)?.brief);
  const updateSessionBrief = useChatStore((state) => state.updateSessionBrief);
  const { data, error, isLoading, mutate } = useSWR(
    supported && sessionId ? ['session-brief', schema.revision, sessionId] : null,
    ([, , id]: readonly [string, string, string]) => client.getBrief(id),
    { revalidateOnFocus: false },
  );
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<ProductionBrief>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const idPrefix = useId();

  useEffect(() => {
    if (data && sessionId) updateSessionBrief(sessionId, data);
  }, [data, sessionId, updateSessionBrief]);
  useEffect(() => {
    setEditing(false);
    setDraft(EMPTY);
    setSaving(false);
    setSaved(false);
    setSaveError(null);
  }, [sessionId]);

  if (!supported || !sessionId) return null;
  const brief = parseProductionBrief(data ?? stored ?? EMPTY);
  const empty = briefIsEmpty(brief);
  const text = (key: string) => schemaText(schema, key);

  const startEdit = () => {
    setDraft({ ...brief });
    setEditing(true);
    setSaved(false);
    setSaveError(null);
  };
  const save = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const next = await client.setBrief(sessionId, briefPayload(fields, draft));
      updateSessionBrief(sessionId, next);
      await mutate(next, { revalidate: false });
      setEditing(false);
      setSaved(true);
    } catch (failure) {
      const known = failure instanceof ChatBrowserError ? schemaText(schema, failure.message) : '';
      const reason = known || schema.chat.presentation.messages.unknown_error;
      setSaveError(formatSchemaText(text('ui.studio.brief.save_failed'), { reason }));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="flex flex-col gap-2 border-b border-border px-3 py-2" data-testid="studio-brief">
      <header className="flex items-center gap-2">
        <h2 className="flex-1 text-xs font-semibold" data-testid="studio-brief-title">{text('ui.studio.brief.title')}</h2>
        {!editing ? (
          <button
            type="button"
            className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
            onClick={startEdit}
            data-testid="studio-brief-edit"
          >
            <Pencil aria-hidden className="size-3" />
            {text('ui.studio.brief.edit')}
          </button>
        ) : null}
      </header>
      {error ? <p className="text-[11px] text-destructive" role="alert" data-testid="studio-brief-load-failed">{text('ui.studio.brief.load_failed')}</p> : null}
      {isLoading && empty ? <p className="text-[11px] text-muted-foreground" data-testid="studio-brief-loading">{text('ui.studio.brief.loading')}</p> : null}
      {saved ? <p className="text-[11px] text-muted-foreground" role="status" data-testid="studio-brief-saved">{text('ui.studio.brief.saved')}</p> : null}
      {saveError ? <p className="text-[11px] text-destructive" role="alert" data-testid="studio-brief-error">{saveError}</p> : null}
      {editing ? (
        <form
          className="flex flex-col gap-2"
          data-testid="studio-brief-form"
          onSubmit={(event) => { event.preventDefault(); void save(); }}
        >
          {fields.map((field) => {
            const id = `${idPrefix}-${field.name}`;
            return (
              <div key={field.name} className="grid grid-cols-[5.5rem_minmax(0,1fr)] items-start gap-2 text-xs">
                <label htmlFor={id} className="pt-1 text-muted-foreground">{field.label}</label>
                <BriefFieldInput
                  field={field}
                  id={id}
                  value={draft[field.name]}
                  onChange={(value) => setDraft((current) => ({ ...current, [field.name]: value }))}
                />
              </div>
            );
          })}
          <div className="flex flex-wrap items-center gap-1.5">
            <Action type="submit" size="sm" loading={saving} data-testid="studio-brief-save">
              {text(saving ? 'ui.studio.brief.saving' : 'ui.studio.brief.save')}
            </Action>
            <Action
              type="button"
              size="sm"
              variant="outline"
              disabled={saving}
              onClick={() => { setEditing(false); setSaveError(null); }}
              data-testid="studio-brief-cancel"
            >
              {text('ui.studio.brief.cancel')}
            </Action>
            <Action
              type="button"
              size="sm"
              variant="ghost"
              disabled={saving}
              onClick={() => setDraft(EMPTY)}
              data-testid="studio-brief-clear"
            >
              {text('ui.studio.brief.clear')}
            </Action>
          </div>
        </form>
      ) : empty ? (
        <p className="text-[11px] leading-relaxed text-muted-foreground" data-testid="studio-brief-empty">{text('ui.studio.brief.empty')}</p>
      ) : (
        <dl className="flex flex-col divide-y divide-border/60" data-testid="studio-brief-values">
          {fields.filter((field) => hasValue(brief[field.name])).map((field) => (
            <div key={field.name} className="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-2 py-1 text-xs">
              <dt className="text-muted-foreground">{field.label}</dt>
              <dd className="min-w-0 [overflow-wrap:anywhere]" data-testid={`studio-brief-value-${field.name}`}>
                {valueText(schema, field, brief[field.name])}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  );
}
