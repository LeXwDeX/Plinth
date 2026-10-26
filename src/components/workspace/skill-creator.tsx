'use client';

import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Plus } from 'lucide-react';
import { toast } from 'sonner';
import { useAuth } from '@/hooks/use-auth';
import { useProductSchema } from '@/components/product-schema-provider';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Action } from '@/components/ui/button';
import { Field } from '@/components/ui/field';
import { schemaText } from '@/lib/product-schema';
import { createPersonalSkill } from '@/lib/personal-skills';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';

export function SkillCreator({ sessionId, disabled, onCreated, control }: {
  sessionId: string | null; disabled: boolean; onCreated: (id: string) => void;
  /** 由外部（如 Skill 选择弹层的"新建"）打开时传入；此时不渲染自己的触发按钮。 */
  control?: { open: boolean; onOpenChange: (open: boolean) => void };
}) {
  const { schema } = useProductSchema();
  const { user } = useAuth();
  const text = (key: string) => schemaText(schema, `ui.workspace.skills.${key}`);
  const [openScope, setOpenScope] = useState<{ userId: string; sessionId: string } | null>(null);
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const request = useRef<AbortController | null>(null);
  const ownerRef = useRef(user?.id);
  ownerRef.current = user?.id;
  const ownOpen = Boolean(user?.id && sessionId && openScope?.userId === user.id && openScope?.sessionId === sessionId);
  const open = control ? Boolean(control.open && user?.id && sessionId) : ownOpen;
  const setOpen = (next: boolean) => {
    if (control) { control.onOpenChange(next); return; }
    if (!next) setOpenScope(null);
  };
  useEffect(() => {
    setSaving(false);
    return () => { request.current?.abort(); request.current = null; };
  }, [sessionId, user?.id]);
  useEffect(() => {
    setName(''); setPrompt(''); setError(''); setOpenScope(null);
  }, [user?.id]);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (request.current || !user?.id || !sessionId) return;
    if (!name.trim() || !prompt.trim()) {
      setError(text('content_invalid')); return;
    }
    const scope = captureSessionScope();
    const owner = user.id;
    const controller = new AbortController(); request.current = controller;
    setSaving(true); setError('');
    try {
      const skill = await createPersonalSkill(schema, { name: name.trim(), prompt: prompt.trim(), recommended_size: schema.chat.skills.creation.recommended_size }, controller.signal);
      if (controller.signal.aborted || ownerRef.current !== owner || !isCurrentSessionScope(scope)) return;
      onCreated(skill.id); setOpen(false); setName(''); setPrompt('');
      toast.success(text('saved'));
    } catch {
      if (!controller.signal.aborted && ownerRef.current === owner && isCurrentSessionScope(scope)) setError(text('save_failed'));
    } finally {
      if (request.current === controller) { request.current = null; setSaving(false); }
    }
  };

  return <>
    {control ? null : <Action type="button" variant="outline" size="sm" disabled={disabled || !sessionId || !user?.id} onClick={() => {
      if (sessionId && user?.id) { setError(''); setOpenScope({ userId: user.id, sessionId }); }
    }}><Plus className="size-3.5" />{text('create')}</Action>}
    <Dialog open={open} onOpenChange={next => { if (!next && !saving) setOpen(false); }}>
      <DialogContent showCloseButton={false} className="flex h-[min(680px,calc(100dvh-80px))] max-h-[calc(100dvh-80px)] w-[min(768px,calc(100vw-80px))] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none" data-testid="workspace-skill-creator">
        <div className="shrink-0 space-y-2 border-b px-5 py-4">
          <DialogTitle>{text('create')}</DialogTitle>
          <DialogDescription>{text('create_description')}</DialogDescription>
        </div>
        <form onSubmit={event => void save(event)} className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">
            <Field id="personal-skill-name" label={text('name')}>{props => <input {...props} value={name} onChange={event => setName(event.target.value)} maxLength={schema.chat.skills.creation.max_name_characters} required disabled={saving} placeholder={text('name_placeholder')} className="w-full rounded-lg border bg-surface px-3 py-2 text-sm" />}</Field>
            <Field id="personal-skill-prompt" label={text('prompt')}>{props => <textarea {...props} value={prompt} onChange={event => setPrompt(event.target.value)} maxLength={schema.agent_configuration.limits.max_skill_prompt_characters} required disabled={saving} placeholder={text('prompt_placeholder')} className="min-h-[320px] w-full resize-y rounded-lg border bg-surface px-3 py-2 text-sm" />}</Field>
          </div>
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t px-5 py-4">
            <div className="min-w-0">{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}</div>
            <div className="ml-auto flex gap-2"><Action type="button" variant="ghost" disabled={saving} onClick={() => setOpen(false)}>{text('cancel')}</Action><Action type="submit" disabled={saving}>{text(saving ? 'saving' : 'save')}</Action></div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  </>;
}
