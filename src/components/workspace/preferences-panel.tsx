'use client';

import { useId, useMemo, useState } from 'react';
import { Pencil, Trash2 } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { ChoiceChips } from '@/components/ui/choice-chips';
import { CollapsibleSection } from '@/components/ui/collapsible-section';
import { Field } from '@/components/ui/field';
import { Textarea } from '@/components/ui/textarea';
import { useSessionPreferences } from '@/hooks/use-session-preferences';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { preferenceReasonLabel, preferenceReasonPresetIds, type ChatPreference } from '@/lib/chat/preferences';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

function preferenceKindLabel(schema: ReturnType<typeof useProductSchema>['schema'], kind: string): string {
  const key = `ui.workspace.preferences.kind.${kind}`;
  return schemaText(schema, key) || kind;
}

/**
 * #241: the 功能 tab's collapsible preference summary. It reads the same session-scoped list the Agent's prompt is
 * built from: an entry edited here applies to the next turn, and a deleted entry is no longer injected. The panel
 * only edits what the server recorded; it cannot invent a preference the person never acted on.
 */
export function PreferencesPanel({ sessionId }: { sessionId: string | null }) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const preferences = useSessionPreferences(client, sessionId);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [direction, setDirection] = useState('');
  const [preset, setPreset] = useState('');
  const [reason, setReason] = useState('');
  const [error, setError] = useState('');
  const baseId = useId();
  const maximumDirection = schema.chat.preferences?.maximum_direction_characters ?? 0;
  const maximumReason = schema.chat.preferences?.maximum_reason_characters ?? 0;
  const presets = preferenceReasonPresetIds(schema);
  if (!sessionId || !preferences.available) return null;

  const startEdit = (preference: ChatPreference) => {
    setEditing(preference.id);
    setDirection(preference.direction);
    setPreset(preference.reasonPreset);
    setReason(preference.reasonText);
    setError('');
    setOpen(true);
  };
  const save = async (preferenceID: string) => {
    const directionLength = [...direction].length;
    const reasonLength = [...reason].length;
    if (maximumDirection > 0 && directionLength > maximumDirection) {
      setError(schemaText(schema, 'ui.workspace.preferences.too_long').replace('{maximum}', String(maximumDirection)));
      return;
    }
    if (maximumReason > 0 && reasonLength > maximumReason) {
      setError(schemaText(schema, 'ui.workspace.preferences.too_long').replace('{maximum}', String(maximumReason)));
      return;
    }
    setError('');
    const ok = await preferences.update(preferenceID, {
      direction: direction.trim(), reasonPreset: preset, reasonText: reason.trim(),
    });
    if (ok) setEditing(null);
    else setError(schemaText(schema, 'ui.workspace.preferences.save_failed'));
  };
  const remove = async (preferenceID: string) => {
    const ok = await preferences.remove(preferenceID);
    if (!ok) setError(schemaText(schema, 'ui.workspace.preferences.delete_failed'));
    else if (editing === preferenceID) setEditing(null);
  };

  return (
    <CollapsibleSection
      id={`${baseId}-preferences`}
      title={schemaText(schema, 'ui.workspace.preferences.title')}
      summary={preferences.loaded ? String(preferences.preferences.length) : undefined}
      open={open}
      onOpenChange={setOpen}
      data-testid="preferences-section"
    >
      <div className="flex flex-col gap-2 p-3" data-testid="preferences-panel">
        <p className="text-xs text-muted-foreground">{schemaText(schema, 'ui.workspace.preferences.hint')}</p>
        {preferences.failed ? (
          <p className="text-xs text-destructive" role="alert">{schemaText(schema, 'ui.workspace.preferences.load_failed')}</p>
        ) : !preferences.loaded ? null : preferences.preferences.length === 0 ? (
          <p className="text-xs text-muted-foreground" data-testid="preferences-empty">{schemaText(schema, 'ui.workspace.preferences.empty')}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {preferences.preferences.map((preference) => (
              <li key={preference.id} className="rounded-md border border-border p-2" data-testid="preference-item" data-kind={preference.kind}>
                <div className="flex items-start gap-1.5">
                  <span className={cn(
                    'shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium',
                    preference.kind === 'selected' && 'border-primary/40 bg-primary/10 text-primary',
                    preference.kind === 'rejected' && 'border-destructive/40 bg-destructive/10 text-destructive',
                    preference.kind === 'signal' && 'border-border bg-muted text-muted-foreground',
                  )}>{preferenceKindLabel(schema, preference.kind)}</span>
                  <span className="min-w-0 flex-1 break-words text-xs">
                    {preference.direction || preference.handle || '—'}
                  </span>
                  <Action type="button" variant="ghost" size="icon-xs"
                    aria-label={schemaText(schema, 'ui.workspace.preferences.edit')}
                    onClick={() => startEdit(preference)}>
                    <Pencil />
                  </Action>
                  <Action type="button" variant="ghost" size="icon-xs"
                    aria-label={schemaText(schema, 'ui.workspace.preferences.delete')}
                    onClick={() => void remove(preference.id)}>
                    <Trash2 />
                  </Action>
                </div>
                {preference.handle ? <p className="mt-1 text-[11px] text-muted-foreground">{preference.handle}</p> : null}
                {preference.reasonPreset || preference.reasonText ? (() => {
                  const parts = [preferenceReasonLabel(schema, preference.reasonPreset), preference.reasonText].filter(Boolean);
                  const reason = parts.length > 1
                    ? formatSchemaText(schemaText(schema, 'ui.workspace.preferences.reason_join'), { preset: parts[0], text: parts[1] })
                    : parts[0] ?? '';
                  return (
                    <p className="mt-1 text-[11px] text-muted-foreground" data-testid="preference-reason">
                      {formatSchemaText(schemaText(schema, 'ui.workspace.preferences.reason_line'), { reason })}
                    </p>
                  );
                })() : null}
                {preference.alternatives.length > 0 ? (
                  <p className="mt-1 text-[11px] text-muted-foreground">{preference.alternatives.join(' · ')}</p>
                ) : null}
                {editing === preference.id ? (
                  <div className="mt-2 flex flex-col gap-2 border-t border-border pt-2">
                    <Field id={`${baseId}-direction-${preference.id}`} label={schemaText(schema, 'ui.workspace.preferences.direction_label')}>
                      {(control) => (
                        <Textarea {...control} value={direction} rows={2}
                          maxLength={maximumDirection > 0 ? maximumDirection : undefined}
                          onChange={(event) => setDirection(event.target.value)} />
                      )}
                    </Field>
                    {presets.length > 0 ? (
                      <Field id={`${baseId}-preset-${preference.id}`} label={schemaText(schema, 'ui.workspace.preferences.reason_preset_label')} group>
                        {() => (
                          <ChoiceChips
                            label={schemaText(schema, 'ui.workspace.preferences.reason_preset_label')}
                            options={presets.map((id) => ({ value: id, label: preferenceReasonLabel(schema, id) }))}
                            current={preset}
                            onChange={setPreset}
                          />
                        )}
                      </Field>
                    ) : null}
                    <Field id={`${baseId}-reason-${preference.id}`} label={schemaText(schema, 'ui.workspace.preferences.reason_label')}>
                      {(control) => (
                        <Textarea {...control} value={reason} rows={2}
                          maxLength={maximumReason > 0 ? maximumReason : undefined}
                          onChange={(event) => setReason(event.target.value)} />
                      )}
                    </Field>
                    {error ? <p className="text-xs text-destructive" role="alert">{error}</p> : null}
                    <div className="flex gap-1">
                      <Action type="button" variant="secondary" size="xs" onClick={() => void save(preference.id)}>
                        {schemaText(schema, 'ui.workspace.preferences.save')}
                      </Action>
                      <Action type="button" variant="ghost" size="xs" onClick={() => { setEditing(null); setError(''); }}>
                        {schemaText(schema, 'ui.workspace.preferences.cancel')}
                      </Action>
                    </div>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {error && editing === null ? <p className="text-xs text-destructive" role="alert">{error}</p> : null}
      </div>
    </CollapsibleSection>
  );
}