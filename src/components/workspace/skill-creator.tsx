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
import type { AgentSkillConfiguration } from '@/lib/agent-configuration';
import { createPersonalSkill, PersonalSkillNotFound, updatePersonalSkill } from '@/lib/personal-skills';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';

/** 编辑目标：个人 Skill 的 ID 与当前内容；ID 在保存前后不变。 */
export interface SkillEditTarget {
  id: string;
  name: string;
  prompt: string;
  /** 表单不编辑尺寸；整体替换时显式提交该行现值，避免静默改写。 */
  recommendedSize: string;
}

export function SkillCreator({ sessionId, disabled, onCreated, onUpdated, onMissing, editing, control }: {
  sessionId: string | null; disabled: boolean; onCreated?: (id: string) => void;
  /** 编辑成功：独立回调，不改变当前选择。 */
  onUpdated?: (skill: AgentSkillConfiguration) => void;
  /** 编辑期间该 Skill 已不可访问：让调用方刷新列表。 */
  onMissing?: () => void;
  /** 给出时进入编辑模式，表单预填该 Skill；缺省为创建模式。 */
  editing?: SkillEditTarget | null;
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
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const request = useRef<AbortController | null>(null);
  const ownerRef = useRef(user?.id);
  ownerRef.current = user?.id;
  const ownOpen = Boolean(user?.id && sessionId && openScope?.userId === user.id && openScope?.sessionId === sessionId);
  const open = control ? Boolean(control.open && user?.id && sessionId) : ownOpen;
  const setOpen = (next: boolean) => {
    if (control) { control.onOpenChange(next); return; }
    if (!next) setOpenScope(null);
  };
  const dirty = Boolean(editing) && (name !== editing?.name || prompt !== editing?.prompt);
  useEffect(() => {
    setSaving(false);
    return () => { request.current?.abort(); request.current = null; };
  }, [sessionId, user?.id]);
  useEffect(() => {
    setName(''); setPrompt(''); setError(''); setConfirmDiscard(false); setOpenScope(null);
  }, [user?.id]);
  // 每次打开时装载当前内容：创建为空白，编辑为预填；输入过程不重置。
  useEffect(() => {
    if (!open) return;
    setName(editing?.name ?? '');
    setPrompt(editing?.prompt ?? '');
    setError('');
    setConfirmDiscard(false);
  }, [open, editing?.id, editing?.name, editing?.prompt]);
  // 关闭确认出现时把焦点移到"继续编辑"；回到编辑时移到"取消"，键盘用户不会掉出对话框。
  const wasConfirming = useRef(false);
  useEffect(() => {
    if (confirmDiscard) {
      wasConfirming.current = true;
      document.querySelector<HTMLElement>('[data-testid="skill-unsaved-keep-editing"]')?.focus();
    } else if (wasConfirming.current) {
      wasConfirming.current = false;
      document.querySelector<HTMLElement>('[data-testid="skill-edit-cancel"]')?.focus();
    }
  }, [confirmDiscard]);
  const requestClose = () => {
    if (saving) return;
    if (dirty) { setConfirmDiscard(true); return; }
    setOpen(false);
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (request.current || !user?.id || !sessionId || confirmDiscard) return;
    if (!name.trim() || !prompt.trim()) {
      setError(text('content_invalid')); return;
    }
    const scope = captureSessionScope();
    const owner = user.id;
    const controller = new AbortController(); request.current = controller;
    setSaving(true); setError('');
    const input = {
      name: name.trim(), prompt: prompt.trim(),
      recommended_size: editing?.recommendedSize || schema.chat.skills.creation.recommended_size,
    };
    try {
      const skill = editing
        ? await updatePersonalSkill(schema, editing.id, input, controller.signal)
        : await createPersonalSkill(schema, input, controller.signal);
      if (controller.signal.aborted || ownerRef.current !== owner || !isCurrentSessionScope(scope)) return;
      if (editing) {
        onUpdated?.(skill); setOpen(false);
        toast.success(text('updated'));
      } else {
        onCreated?.(skill.id); setOpen(false); setName(''); setPrompt('');
        toast.success(text('saved'));
      }
    } catch (cause) {
      if (!controller.signal.aborted && ownerRef.current === owner && isCurrentSessionScope(scope)) {
        if (cause instanceof PersonalSkillNotFound) { setError(text('not_found')); onMissing?.(); }
        else setError(text('save_failed'));
      }
    } finally {
      if (request.current === controller) { request.current = null; setSaving(false); }
    }
  };

  return <>
    {control ? null : <Action type="button" variant="outline" size="sm" disabled={disabled || !sessionId || !user?.id} onClick={() => {
      if (sessionId && user?.id) { setError(''); setOpenScope({ userId: user.id, sessionId }); }
    }}><Plus className="size-3.5" />{text('create')}</Action>}
    <Dialog open={open} onOpenChange={next => { if (next) { setOpen(true); return; } requestClose(); }}>
      <DialogContent showCloseButton={false} className="flex h-[min(680px,calc(100dvh-80px))] max-h-[calc(100dvh-80px)] w-[min(768px,calc(100vw-80px))] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none" data-testid={editing ? 'workspace-skill-editor' : 'workspace-skill-creator'}>
        <div className="shrink-0 space-y-2 border-b px-5 py-4">
          <DialogTitle>{text(editing ? 'edit' : 'create')}</DialogTitle>
          <DialogDescription>{text(editing ? 'edit_description' : 'create_description')}</DialogDescription>
        </div>
        <form onSubmit={event => void save(event)} className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">
            <Field id="personal-skill-name" label={text('name')}>{props => <input {...props} value={name} onChange={event => setName(event.target.value)} maxLength={schema.chat.skills.creation.max_name_characters} required disabled={saving} placeholder={text('name_placeholder')} className="w-full rounded-lg border bg-surface px-3 py-2 text-sm" />}</Field>
            <Field id="personal-skill-prompt" label={text('prompt')}>{props => <textarea {...props} value={prompt} onChange={event => setPrompt(event.target.value)} maxLength={schema.agent_configuration.limits.max_skill_prompt_characters} required disabled={saving} placeholder={text('prompt_placeholder')} className="min-h-[320px] w-full resize-y rounded-lg border bg-surface px-3 py-2 text-sm" />}</Field>
          </div>
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t px-5 py-4">
            <div className="min-w-0">{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}</div>
            <div className="ml-auto flex gap-2">
              {/* 两个操作组常驻：点击"继续编辑"时按钮不能在自己的 click 事件里被卸载，
                  否则 Base UI 的外部点击检测会把它当成外部目标并关闭对话框。 */}
              <div className={confirmDiscard ? 'hidden' : 'flex gap-2'} data-testid="skill-edit-actions">
                <Action type="button" variant="ghost" data-testid="skill-edit-cancel" disabled={saving} onClick={requestClose}>{text('cancel')}</Action>
                <Action type="submit" disabled={saving}>{text(saving ? 'saving' : editing ? 'save_changes' : 'save')}</Action>
              </div>
              <div className={confirmDiscard ? 'flex gap-2' : 'hidden'} data-testid="skill-unsaved-actions">
                {confirmDiscard ? <p role="alert" className="self-center text-xs text-destructive">{text('unsaved')}</p> : null}
                <Action type="button" variant="ghost" data-testid="skill-unsaved-keep-editing" onClick={() => setConfirmDiscard(false)}>{text('keep_editing')}</Action>
                <Action type="button" variant="destructive" data-testid="skill-unsaved-discard" onClick={() => { setConfirmDiscard(false); setOpen(false); }}>{text('discard')}</Action>
              </div>
            </div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  </>;
}