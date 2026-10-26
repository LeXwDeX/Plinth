'use client';

import { useEffect, useMemo, useRef, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { Surface } from '@/components/ui/surface';
import {
  createLLMSettingsClient,
  LLMSettingsError,
  toggleLevel,
  type LLMConnectionView,
  type LLMSettings,
  type LLMSettingsManagement,
} from '@/lib/llm-settings';
import { formatTokenCount } from '@/lib/chat/token-count';

/**
 * #322: the administrator's LLM settings. Connections come from the runtime environment; this page shows
 * whether each variable is set (never its value), tests a connection on request, and publishes which models
 * people may use, the levels each offers and the default model. Every change names the revision it was
 * based on, like the Agent configuration.
 */
export default function LLMSettingsPage() {
  const { schema } = useProductSchema();
  const client = useMemo(() => createLLMSettingsClient(schema), [schema]);
  const ui = (key: string) => schema.llm_settings?.ui[key] ?? '';
  const [management, setManagement] = useState<LLMSettingsManagement | null>(null);
  const [draft, setDraft] = useState<LLMSettings | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const lock = useRef(false);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    client.get(controller.signal).then((value) => {
      if (!controller.signal.aborted) { setManagement(value); setDraft(structuredClone(value.settings)); }
    }).catch(() => { if (!controller.signal.aborted) setError(ui('load_failed')); });
    return () => { alive.current = false; controller.abort(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  async function run(action: () => Promise<LLMSettingsManagement>, message: string, keepDraft = false) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(''); setNotice('');
    try {
      const value = await action();
      if (!alive.current) return;
      setManagement(value);
      if (!keepDraft) setDraft(structuredClone(value.settings));
      setNotice(message);
    } catch (cause) {
      if (alive.current) setError(cause instanceof LLMSettingsError && cause.detail ? cause.detail : ui('save_failed'));
    } finally {
      lock.current = false;
      if (alive.current) setBusy(false);
    }
  }

  async function test(connection: LLMConnectionView) {
    setTesting(connection.id);
    try {
      await run(() => client.test(connection.id), ui('tested'), true);
    } finally {
      if (alive.current) setTesting(null);
    }
  }

  const changed = management && draft ? JSON.stringify(draft) !== JSON.stringify(management.settings) : false;
  const updateModel = (id: string, update: (current: LLMSettings['models'][string]) => LLMSettings['models'][string]) => {
    if (!draft) return;
    setDraft({ ...draft, models: { ...draft.models, [id]: update(draft.models[id]) } });
    setNotice('');
  };
  const defaultConnection = management?.connections.find((connection) => connection.id === draft?.default_model);

  return <main className="space-y-5" data-testid="llm-settings-admin">
    <div>
      <h1 className="text-xl font-semibold">{ui('title')}</h1>
      <p className="mt-1 text-sm text-muted-foreground">{ui('description')}</p>
    </div>
    {error ? <p role="alert" className="text-sm text-destructive" data-testid="llm-settings-error">{error}</p> : null}
    {notice ? <p role="status" className="text-sm" data-testid="llm-settings-notice">{notice}</p> : null}
    {!management || !draft ? <p>{error ? ui('load_failed') : ui('loading')}</p> : <>
      <Surface padding="md" className="space-y-3">
        <p className="break-all text-xs text-muted-foreground">
          {ui('revision')}: {management.revision} · {ui('source')}: {management.history.find((version) => version.revision === management.revision)?.source ?? ui('defaults')}
        </p>
        <Field id="llm-default-model" label={ui('default_model')}>
          {(props) => <OptionSelect {...props} value={draft.default_model} disabled={busy}
            onValueChange={(value) => { setDraft({ ...draft, default_model: value }); setNotice(''); }}
            options={management.connections.map((connection) => ({ value: connection.id, label: `${connection.model || connection.id} (${connection.id})` }))} />}
        </Field>
        {!management.default_available ? <p className="text-sm text-warning" data-testid="llm-default-unavailable">{ui('default_unavailable')}</p> : null}
        {defaultConnection?.check?.vision === 'unsupported' ? <p className="text-sm text-warning">{ui('vision_required')}</p> : null}
      </Surface>

      <Surface padding="md" className="space-y-4">
        <div>
          <h2 className="font-semibold">{ui('connections')}</h2>
          <p className="mt-1 text-xs text-muted-foreground">{ui('connections_note')}</p>
        </div>
        {management.connections.length === 0 ? <p>{ui('no_connections')}</p> : management.connections.map((connection) => {
          const model = draft.models[connection.id];
          if (!model) return null;
          const check = connection.check;
          const variable = (index: number, set: boolean, needed: boolean) => (
            <li key={index} className="flex flex-wrap gap-1">
              <code className="text-xs">{connection.environment[index]}</code>
              <span className={set || !needed ? 'text-muted-foreground' : 'text-warning'}>{!needed ? ui('not_needed') : set ? ui('set') : ui('missing')}</span>
            </li>
          );
          return <Surface key={connection.id} variant="inset" padding="md" className="space-y-3" data-testid={`llm-connection-${connection.id}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <h3 className="break-all font-medium">
                  {connection.model || connection.id} <span className="text-xs text-muted-foreground">({connection.id})</span>
                  {connection.deployment_default ? <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[11px]">{ui('default_connection')}</span> : null}
                  {draft.default_model === connection.id ? <span className="ml-2 rounded bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">{ui('default_tag')}</span> : null}
                </h3>
                <p className={connection.configured ? 'text-xs text-muted-foreground' : 'text-xs text-warning'} data-testid={`llm-configured-${connection.id}`}>
                  {connection.configured ? ui('configured') : ui('not_configured')}
                  {' · '}{ui('context_window')}: {connection.context_window > 0 ? formatTokenCount(connection.context_window) : ui('window_unknown')}
                </p>
              </div>
              <Action variant="outline" size="sm" disabled={busy || testing !== null} onClick={() => void test(connection)} data-testid={`llm-test-${connection.id}`}>
                {testing === connection.id ? ui('testing') : ui('test')}
              </Action>
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              <div className="space-y-1 text-xs">
                <p className="font-medium">{ui('environment')}</p>
                <ul className="space-y-0.5">
                  {variable(0, connection.base_url_set, connection.credentials_required)}
                  {variable(1, connection.api_key_set, connection.credentials_required)}
                  {variable(2, connection.model_set, true)}
                </ul>
              </div>
              <div className="space-y-1 text-xs" data-testid={`llm-check-${connection.id}`}>
                <p className="font-medium">{ui('connectivity')} · {ui('vision')}</p>
                {check ? <>
                  <p className={check.reachable ? '' : 'text-warning'}>
                    {check.reachable ? ui('reachable') : ui('unreachable')} · {ui(`outcome_${check.outcome}`) || check.outcome}
                  </p>
                  <p>{check.vision === 'supported' ? ui('vision_supported') : check.vision === 'unsupported' ? ui('vision_unsupported') : ui('vision_unknown')}</p>
                  <p className="text-muted-foreground">{ui('checked_at')}: {check.checked_at}</p>
                </> : <p className="text-muted-foreground">{ui('never_tested')}</p>}
              </div>
            </div>
            <Field id={`llm-enabled-${connection.id}`} label={ui('enabled')} layout="inline">
              {(props) => <input {...props} type="checkbox" disabled={busy} checked={model.enabled}
                onChange={(event) => updateModel(connection.id, (current) => ({ ...current, enabled: event.target.checked }))} />}
            </Field>
            {connection.profile_levels.length === 0 ? <p className="text-xs text-muted-foreground">{ui('no_effort')}</p> : <div className="flex flex-wrap items-end gap-4">
              <fieldset className="space-y-1">
                <legend className="text-sm font-medium">{ui('effort_levels')}</legend>
                <div className="flex flex-wrap gap-3">
                  {connection.profile_levels.map((level) => (
                    <Field key={level} id={`llm-level-${connection.id}-${level}`} label={level} layout="inline">
                      {(props) => <input {...props} type="checkbox" disabled={busy || (model.effort_levels.length === 1 && model.effort_levels[0] === level)}
                        checked={model.effort_levels.includes(level)}
                        onChange={(event) => updateModel(connection.id, (current) => toggleLevel(connection, current, level, event.target.checked))} />}
                    </Field>
                  ))}
                </div>
              </fieldset>
              <Field id={`llm-default-effort-${connection.id}`} label={ui('default_effort')} className="min-w-32">
                {(props) => <OptionSelect {...props} value={model.default_effort} disabled={busy}
                  onValueChange={(value) => updateModel(connection.id, (current) => ({ ...current, default_effort: value }))}
                  options={model.effort_levels.map((level) => ({ value: level, label: level }))} />}
              </Field>
            </div>}
          </Surface>;
        })}
      </Surface>

      <div className="flex flex-wrap items-center gap-2">
        <Action disabled={busy || !changed} onClick={() => void run(() => client.publish(management.revision, draft), ui('published'))} data-testid="llm-publish">{ui('publish')}</Action>
        <Action variant="outline" disabled={busy} onClick={() => void run(() => client.reset(management.revision), ui('restored'))}>{ui('restore_preset')}</Action>
        {changed ? <span className="text-sm text-muted-foreground">{ui('unsaved')}</span> : null}
      </div>

      <Surface padding="md" className="space-y-3">
        <h2 className="font-semibold">{ui('history')}</h2>
        {management.history.length === 0 ? <p>{ui('history_empty')}</p> : <ul className="space-y-2">
          {management.history.map((version) => <li key={version.revision} className="flex flex-wrap items-center justify-between gap-2 text-xs">
            <span className="break-all">{version.revision} · {version.created_at} · {version.source}</span>
            <Action variant="outline" size="sm" disabled={busy || version.revision === management.revision}
              onClick={() => void run(() => client.restore(management.revision, version.revision), ui('restored'))}>{ui('restore_version')}</Action>
          </li>)}
        </ul>}
      </Surface>
    </>}
  </main>;
}
