'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useProductSchema } from '@/components/product-schema-provider';
import { createAgentConfigurationClient, AgentConfigurationError, type AgentConfiguration, type AgentConfigurationSnapshot } from '@/lib/agent-configuration';
import { schemaText } from '@/lib/product-schema';
import { Field } from '@/components/ui/field';
import { OptionSelect } from '@/components/ui/option-select';
import { Action } from '@/components/ui/button';
import { Surface } from '@/components/ui/surface';

function newSkillID() { return `skill_${Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('')}`; }

export default function AgentConfigurationPage() {
  const { schema } = useProductSchema();
  const client = useMemo(() => createAgentConfigurationClient(schema), [schema]);
  const ui = (key: string) => schema.agent_configuration.ui[key] ?? schemaText(schema, `ui.agent_configuration.${key}`);
  const [snapshot, setSnapshot] = useState<AgentConfigurationSnapshot | null>(null);
  const [draft, setDraft] = useState<AgentConfiguration | null>(null);
  const [validated, setValidated] = useState<string | null>(null);
  const [preview, setPreview] = useState<Record<string, string> | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [skillSearch, setSkillSearch] = useState('');
  const [editingSkillId, setEditingSkillId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const alive = useRef(true);
  const operation = useRef<AbortController | null>(null);
  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    client.get(controller.signal).then((value) => {
      if (!controller.signal.aborted) { setSnapshot(value); setDraft(structuredClone(value.configuration)); }
    }).catch(() => { if (!controller.signal.aborted) setError(schema.agent_configuration.ui.load_failed); });
    return () => { alive.current = false; controller.abort(); operation.current?.abort(); };
  }, [client, schema]);

  async function run(action: () => Promise<AgentConfigurationSnapshot | null>, message: string) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(''); setNotice('');
    try {
      const value = await action();
      if (!alive.current) return;
      if (value) { setSnapshot(value); setDraft(structuredClone(value.configuration)); setValidated(null); setPreview(null); setEditingSkillId(null); }
      setNotice(message);
    } catch (cause) {
      if (alive.current) setError(cause instanceof AgentConfigurationError ? schemaText(schema, cause.detail) || ui('save_failed') : ui('save_failed'));
    } finally { lock.current = false; if (alive.current) setBusy(false); }
  }
  const revise = (next: AgentConfiguration) => { setDraft(next); setValidated(null); setPreview(null); setNotice(''); };
  const signal = () => { operation.current = new AbortController(); return operation.current.signal; };
  const updateSkill = (index: number, field: 'name' | 'name_zh' | 'category' | 'prompt' | 'image_to_image_template', value: string) => {
    if (!draft) return;
    revise({ ...draft, skills: draft.skills.map((skill, at) => at === index ? { ...skill, [field]: value } : skill) });
  };
  const toggle = (field: 'enabled_tool_names' | 'enabled_mcp_preset_ids', name: string, enabled: boolean) => {
    if (!draft) return;
    revise({ ...draft, [field]: enabled ? [...draft[field], name] : draft[field].filter((item) => item !== name) });
  };

  return <main className="space-y-5" data-testid="agent-configuration-admin">
    <div><h1 className="text-xl font-semibold">{ui('title')}</h1><p className="mt-1 text-sm text-muted-foreground">{ui('description')}</p></div>
    {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
    {notice ? <p role="status" className="text-sm">{notice}</p> : null}
    {!snapshot || !draft ? <p>{error ? ui('load_failed') : ui('loading')}</p> : <>
      <Surface padding="md" className="space-y-3">
        <p className="break-all text-xs text-muted-foreground">{ui('revision')}: {snapshot.revision} · {ui('source')}: {snapshot.history.find(version => version.revision === snapshot.revision)?.source ?? ui('defaults')}</p>
        <p className="text-sm text-muted-foreground">{ui('policy_locked')}</p>
        <Field id="agent-system-prompt" label={ui('system_prompt')}>{props => <textarea {...props} value={draft.system_prompt} disabled={busy} maxLength={schema.agent_configuration.limits.max_prompt_characters} onChange={event => revise({ ...draft, system_prompt: event.target.value })} rows={8} className="w-full rounded-md border bg-background p-3 text-sm" />}</Field>
        {Object.entries(draft.domain_context).map(([domain, value]) => <Field key={domain} id={`agent-domain-${domain}`} label={ui(domain === 'image' ? 'domain_image' : 'domain_model_3d')}>{props => <textarea {...props} value={value} disabled={busy} maxLength={schema.agent_configuration.limits.max_prompt_characters} onChange={event => revise({ ...draft, domain_context: { ...draft.domain_context, [domain]: event.target.value } })} rows={4} className="w-full rounded-md border bg-background p-3 text-sm" />}</Field>)}
      </Surface>
      <Surface padding="md" className="space-y-3"><h2 className="font-semibold">{ui('tools')}</h2>
        {snapshot.tools.map(tool => <Field key={tool.name} id={`agent-tool-${tool.name}`} label={tool.name} help={tool.description} layout="inline">{props => <input {...props} type="checkbox" disabled={busy} checked={draft.enabled_tool_names.includes(tool.name)} onChange={event => toggle('enabled_tool_names', tool.name, event.target.checked)} />}</Field>)}
      </Surface>
      <Surface padding="md" className="space-y-3"><h2 className="font-semibold">{ui('skills')}</h2>
        <Field id="agent-skill-search" label={ui('search')}>{props => <input {...props} type="search" value={skillSearch} onChange={event => setSkillSearch(event.target.value)} className="w-full rounded border bg-background p-2 text-sm" />}</Field>
        <Field id="agent-skill-edit" label={ui('skills')}>{props => <OptionSelect {...props} value={editingSkillId ?? draft.skills[0]?.id ?? ''} onValueChange={setEditingSkillId} options={draft.skills.filter(skill => skill.id === (editingSkillId ?? draft.skills[0]?.id) || `${skill.name} ${skill.name_zh ?? ''} ${skill.category}`.toLocaleLowerCase().includes(skillSearch.toLocaleLowerCase())).map(skill => ({ value: skill.id, label: skill.name_zh || skill.name }))} />}</Field>
        {draft.skills.map((skill, index) => ({ skill, index })).filter(({ skill }) => skill.id === (editingSkillId ?? draft.skills[0]?.id)).map(({ skill, index }) => <Surface key={skill.id} variant="inset" padding="md" className="space-y-2">
          <p className="break-all text-xs text-muted-foreground">{skill.id}</p>
          {(['name', 'name_zh', 'category'] as const).map(field => <Field key={field} id={`agent-skill-${skill.id}-${field}`} label={ui(field === 'category' ? 'skill_category' : 'skill_name')}>{props => <input {...props} value={skill[field] ?? ''} disabled={busy} onChange={event => updateSkill(index, field, event.target.value)} className="w-full rounded-md border bg-background p-2 text-sm" />}</Field>)}
          <Field id={`agent-skill-${skill.id}-prompt`} label={ui('skill_prompt')}>{props => <textarea {...props} value={skill.prompt} maxLength={schema.agent_configuration.limits.max_skill_prompt_characters} disabled={busy} onChange={event => updateSkill(index, 'prompt', event.target.value)} rows={4} className="w-full rounded-md border bg-background p-2 text-sm" />}</Field>
          <Field id={`agent-skill-${skill.id}-image-prompt`} label={ui('image_to_image_template')}>{props => <textarea {...props} value={skill.image_to_image_template ?? ''} maxLength={schema.agent_configuration.limits.max_skill_prompt_characters} disabled={busy} onChange={event => updateSkill(index, 'image_to_image_template', event.target.value)} rows={3} className="w-full rounded-md border bg-background p-2 text-sm" />}</Field>
          <div className="flex flex-wrap gap-2"><Action variant="outline" disabled={busy || draft.skills.length >= schema.agent_configuration.limits.max_skill_count} onClick={() => { const id = newSkillID(); revise({ ...draft, skills: [...draft.skills, { ...structuredClone(skill), id }] }); setEditingSkillId(id); }}>{ui('add_skill')}</Action><Action variant="outline" disabled={busy} onClick={() => { revise({ ...draft, skills: draft.skills.filter((_, at) => at !== index) }); setEditingSkillId(null); }}>{ui('remove_skill')}</Action></div>
        </Surface>)}
        {draft.skills.length === 0 && snapshot.presets.skills[0] ? <Action disabled={busy} onClick={() => revise({ ...draft, skills: [{ ...structuredClone(snapshot.presets.skills[0]), id: newSkillID() }] })}>{ui('add_skill')}</Action> : null}
      </Surface>
      <Surface padding="md" className="space-y-3"><h2 className="font-semibold">{ui('mcp')}</h2>
        {snapshot.mcp_presets.length === 0 ? <p className="text-sm text-muted-foreground">{ui('empty')}</p> : snapshot.mcp_presets.map(preset => <Field key={preset.id} id={`agent-mcp-${preset.id}`} label={preset.name} help={<>{preset.description}{preset.available === false ? <><br /><span>{ui('mcp_unavailable')}</span></> : null}<br />{ui('mcp_protocol')}: {preset.protocol_version ?? ''}<br />{ui('transport')}: {preset.transport ?? ''}<br />{ui('mcp_tools')}: {preset.tool_names.join(', ')}</>} layout="inline">{props => <input {...props} type="checkbox" disabled={busy} checked={draft.enabled_mcp_preset_ids.includes(preset.id)} onChange={event => toggle('enabled_mcp_preset_ids', preset.id, event.target.checked)} />}</Field>)}
      </Surface>
      <div className="flex flex-wrap gap-2">
        <Action disabled={busy} onClick={() => void run(async () => {
          const fingerprint = JSON.stringify(draft);
          const result = await client.validate(draft, signal());
          if (result.valid && alive.current) { setValidated(fingerprint); setPreview(result.effective_prompts ?? {}); }
          return null;
        }, ui('validated'))}>{ui('preview')}</Action>
        <Action disabled={busy || validated !== JSON.stringify(draft)} onClick={() => void run(() => client.publish(snapshot.revision, draft, signal()), ui('published'))}>{ui('publish')}</Action>
        <Action variant="outline" disabled={busy} onClick={() => void run(() => client.reset(snapshot.revision, signal()), ui('restore_preset'))}>{ui('restore_preset')}</Action>
      </div>
      {validated !== JSON.stringify(draft) ? <p className="text-sm text-muted-foreground">{ui('preview_required')}</p> : null}
      {preview ? <Surface padding="md" className="space-y-3"><h2 className="font-semibold">{ui('preview')}</h2>{Object.entries(preview).map(([domain, prompt]) => <div key={domain}><h3 className="text-sm font-medium">{ui(domain === 'image' ? 'domain_image' : 'domain_model_3d')}</h3><pre className="mt-2 whitespace-pre-wrap break-words text-xs">{prompt}</pre></div>)}</Surface> : null}
      <Surface padding="md" className="space-y-3"><h2 className="font-semibold">{ui('history')}</h2>
        {snapshot.history.length === 0 ? <p>{ui('history_empty')}</p> : <ul className="space-y-2">{snapshot.history.map(version => <li key={version.revision} className="flex flex-wrap items-center justify-between gap-2 text-xs"><span className="break-all">{version.revision} · {version.created_at} · {version.source}</span><Action variant="outline" size="sm" disabled={busy || version.revision === snapshot.revision} onClick={() => void run(() => client.restore(snapshot.revision, version.revision, signal()), ui('restore_version'))}>{ui('restore_version')}</Action></li>)}</ul>}
      </Surface>
    </>}
  </main>;
}
