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
import {
  createPersonalSkill, draftPersonalSkill, patchPersonalSkill, PersonalSkillInvalid, PersonalSkillNotFound,
  SkillDraftQuotaExhausted, type PersonalSkillField,
} from '@/lib/personal-skills';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';

/** 编辑目标：个人 Skill 的 ID 与当前内容；ID 在保存前后不变。 */
export interface SkillEditTarget {
  id: string;
  name: string;
  prompt: string;
  /** 表单不编辑尺寸。 */
  recommendedSize: string;
  /** #548 的标准字段；descriptionDerived 表示说明是服务端派生的（没写过）。 */
  slug?: string;
  description?: string;
  descriptionDerived?: boolean;
}

/** Skill 的两种生效方式（#548，协议值）。 */
const APPLY_STYLE = 'generation-prompt';
const APPLY_INSTRUCTION = 'instruction';

type Values = Record<PersonalSkillField, string>;
const EMPTY: Values = { name: '', prompt: '', slug: '', description: '' };
/** 某个字段的校验文案键：正文的标签是 body。 */
const invalidKey = (field: PersonalSkillField) => `${field === 'prompt' ? 'body' : field}_invalid`;

/**
 * 个人 Skill 的创建与编辑（#548）。创建分两步：先写名称和要求，“生成”调一次对话模型起草 slug、说明、正文与类型；
 * 第二步逐项可改，“保存”才落库；也可以“我自己写”直接进第二步。编辑时名称、标识、说明、正文各自保存、各自报错，
 * 只影响之后的对话轮次。
 */
export function SkillCreator({ sessionId, disabled, onCreated, onUpdated, onMissing, editing, control }: {
  sessionId: string | null; disabled: boolean; onCreated?: (id: string) => void;
  /** 编辑中某一项保存成功：独立回调，不改变当前选择。 */
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
  const creation = schema.chat.skills.creation;
  const [openScope, setOpenScope] = useState<{ userId: string; sessionId: string } | null>(null);
  const [step, setStep] = useState<'request' | 'review'>('request');
  const [requirement, setRequirement] = useState('');
  const [values, setValues] = useState<Values>(EMPTY);
  const [apply, setApply] = useState(APPLY_STYLE);
  // 编辑模式的已保存值：判断某一项是否有未保存的修改。
  const [saved, setSaved] = useState<Values>(EMPTY);
  const [derived, setDerived] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<PersonalSkillField, string>>>({});
  const [savedFields, setSavedFields] = useState<ReadonlySet<PersonalSkillField>>(() => new Set());
  const [error, setError] = useState('');
  const [busy, setBusy] = useState<'' | 'draft' | 'save' | PersonalSkillField>('');
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
  const fields: PersonalSkillField[] = ['name', 'slug', 'description', 'prompt'];
  const dirty = Boolean(editing) && fields.some((field) => values[field] !== saved[field]);
  const working = busy !== '';
  useEffect(() => {
    setBusy('');
    return () => { request.current?.abort(); request.current = null; };
  }, [sessionId, user?.id]);
  useEffect(() => {
    setValues(EMPTY); setRequirement(''); setError(''); setConfirmDiscard(false); setOpenScope(null);
  }, [user?.id]);
  // 每次打开时装载当前内容：创建为空白的第一步，编辑为预填；输入过程不重置。
  useEffect(() => {
    if (!open) return;
    const initial = editing
      ? { name: editing.name, prompt: editing.prompt, slug: editing.slug ?? '', description: editing.description ?? '' }
      : EMPTY;
    setValues(initial); setSaved(initial); setDerived(Boolean(editing?.descriptionDerived));
    setStep('request'); setRequirement(''); setApply(APPLY_STYLE);
    setFieldErrors({}); setSavedFields(new Set()); setError(''); setConfirmDiscard(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在打开或换一个 Skill 时装载，保存后不覆盖其余未保存的输入
  }, [open, editing?.id]);
  // 关闭确认出现时把焦点移到"继续编辑"；回到编辑时移到"关闭"，键盘用户不会掉出对话框。
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
    if (working) return;
    if (dirty) { setConfirmDiscard(true); return; }
    setOpen(false);
  };
  const setValue = (field: PersonalSkillField, value: string) => {
    setValues((current) => ({ ...current, [field]: value }));
    setFieldErrors((current) => ({ ...current, [field]: undefined }));
    setSavedFields((current) => { const next = new Set(current); next.delete(field); return next; });
  };

  /** 发起一次请求：同一时间只有一个；会话或用户变了就丢弃结果。 */
  const run = async (kind: NonNullable<typeof busy>, work: (signal: AbortSignal) => Promise<void>, fail: (cause: unknown) => void) => {
    if (request.current || !user?.id || !sessionId || confirmDiscard) return;
    const scope = captureSessionScope();
    const owner = user.id;
    const controller = new AbortController(); request.current = controller;
    setBusy(kind); setError('');
    const current = () => !controller.signal.aborted && ownerRef.current === owner && isCurrentSessionScope(scope);
    try {
      await work(controller.signal);
    } catch (cause) {
      if (current()) fail(cause);
    } finally {
      if (request.current === controller) { request.current = null; setBusy(''); }
    }
  };

  const generate = (event: FormEvent) => {
    event.preventDefault();
    const name = values.name.trim();
    if (!name || !requirement.trim()) { setError(text('draft_invalid')); return; }
    void run('draft', async (signal) => {
      // 起草跟随当前会话的对话模型（#619）：额度用完时可以在对话框换模型后重试。
      const draft = await draftPersonalSkill(schema, { name, requirement: requirement.trim(), ...(sessionId ? { session_id: sessionId } : {}) }, signal);
      if (signal.aborted) return;
      setValues({ name, slug: draft.slug, description: draft.description, prompt: draft.prompt });
      setApply(draft.apply === APPLY_INSTRUCTION ? APPLY_INSTRUCTION : APPLY_STYLE);
      setFieldErrors({}); setStep('review');
    }, (cause) => setError(text(cause instanceof SkillDraftQuotaExhausted ? 'draft_quota_exhausted' : 'draft_failed')));
  };

  const failFor = (field: PersonalSkillField | '') => (cause: unknown) => {
    if (cause instanceof PersonalSkillInvalid && cause.field) {
      setFieldErrors((current) => ({ ...current, [cause.field]: text(invalidKey(cause.field as PersonalSkillField)) }));
    } else if (cause instanceof PersonalSkillNotFound) {
      setError(text('not_found')); onMissing?.();
    } else if (field) {
      setFieldErrors((current) => ({ ...current, [field]: text('save_failed') }));
    } else {
      setError(text('save_failed'));
    }
  };

  const create = (event: FormEvent) => {
    event.preventDefault();
    const missing = (['name', 'description', 'prompt'] as const).filter((field) => !values[field].trim());
    if (missing.length) {
      setFieldErrors(Object.fromEntries(missing.map((field) => [field, text(invalidKey(field))])));
      return;
    }
    void run('save', async (signal) => {
      const skill = await createPersonalSkill(schema, {
        name: values.name.trim(), prompt: values.prompt.trim(), slug: values.slug.trim(),
        description: values.description.trim(), apply, recommended_size: creation.recommended_size,
      }, signal);
      if (signal.aborted) return;
      onCreated?.(skill.id); setOpen(false); setValues(EMPTY); setRequirement('');
      toast.success(text('saved'));
    }, failFor(''));
  };

  const saveField = (field: PersonalSkillField) => {
    if (!editing) return;
    const value = values[field].trim();
    // 标识可以清空：服务端改回按 ID 派生的那个。
    if (!value && field !== 'slug') { setFieldErrors((current) => ({ ...current, [field]: text(invalidKey(field)) })); return; }
    void run(field, async (signal) => {
      const skill = await patchPersonalSkill(schema, editing.id, { [field]: value }, signal);
      if (signal.aborted) return;
      const stored = field === 'name' ? skill.name : field === 'prompt' ? skill.prompt : skill[field] ?? value;
      setValues((current) => ({ ...current, [field]: stored }));
      setSaved((current) => ({ ...current, [field]: stored }));
      if (field === 'description') setDerived(false);
      setSavedFields((current) => new Set(current).add(field));
      onUpdated?.(skill);
    }, failFor(field));
  };

  const renderField = (field: PersonalSkillField, multiline: boolean, extra: { help?: string; maxLength?: number; placeholder?: string } = {}) => {
    const label = text(field === 'prompt' ? 'body' : field);
    const help = [extra.help, field === 'description' && derived && values.description === saved.description ? text('description_derived') : '']
      .filter(Boolean).join(' ');
    const className = 'w-full rounded-lg border bg-surface px-3 py-2 text-sm';
    return <div className="space-y-2" key={field}>
      <Field id={`personal-skill-${field}`} label={label} help={help || undefined} error={fieldErrors[field]}>{props => multiline
        ? <textarea {...props} value={values[field]} onChange={event => setValue(field, event.target.value)} maxLength={extra.maxLength} disabled={working} placeholder={extra.placeholder} className={`${className} ${field === 'prompt' ? 'min-h-[240px]' : 'min-h-[64px]'} resize-y`} />
        : <input {...props} value={values[field]} onChange={event => setValue(field, event.target.value)} maxLength={extra.maxLength} disabled={working} placeholder={extra.placeholder} className={className} />}
      </Field>
      {editing
        ? <div className="flex items-center justify-end gap-2">
          {savedFields.has(field) ? <span className="text-xs text-muted-foreground" data-testid={`skill-field-saved-${field}`}>{text('field_saved')}</span> : null}
          <Action type="button" size="sm" variant="outline" data-testid={`skill-field-save-${field}`}
            disabled={working || values[field] === saved[field]} onClick={() => saveField(field)}>
            {busy === field ? text('saving') : text('field_save')}
          </Action>
        </div>
        : null}
    </div>;
  };

  const reviewFields = <>
    {renderField('name', false, { maxLength: creation.max_name_characters, placeholder: text('name_placeholder') })}
    {renderField('slug', false, { help: text('slug_hint'), maxLength: 64 })}
    {renderField('description', true, { help: text('description_hint'), maxLength: 1024 })}
    {renderField('prompt', true, { maxLength: schema.agent_configuration.limits.max_skill_prompt_characters, placeholder: text('prompt_placeholder') })}
  </>;

  const body = editing
    ? reviewFields
    : step === 'request'
      ? <>
        {renderField('name', false, { maxLength: creation.max_name_characters, placeholder: text('name_placeholder') })}
        <Field id="personal-skill-requirement" label={text('requirement')}>{props => <textarea {...props} value={requirement} onChange={event => setRequirement(event.target.value)} maxLength={creation.max_requirement_characters} disabled={working} placeholder={text('requirement_placeholder')} className="min-h-[200px] w-full resize-y rounded-lg border bg-surface px-3 py-2 text-sm" />}</Field>
      </>
      : <>
        {reviewFields}
        <Field id="personal-skill-apply" label={text('apply')}>{props => <select {...props} value={apply} onChange={event => setApply(event.target.value)} disabled={working} className="w-full rounded-lg border bg-surface px-3 py-2 text-sm">
          <option value={APPLY_STYLE}>{text('apply_generation_prompt')}</option>
          <option value={APPLY_INSTRUCTION}>{text('apply_instruction')}</option>
        </select>}</Field>
      </>;

  const actions = editing
    ? <Action type="button" variant="ghost" data-testid="skill-edit-cancel" disabled={working} onClick={requestClose}>{text('close')}</Action>
    : step === 'request'
      ? <>
        <Action type="button" variant="ghost" data-testid="skill-edit-cancel" disabled={working} onClick={requestClose}>{text('cancel')}</Action>
        <Action type="button" variant="outline" data-testid="skill-write-myself" disabled={working} onClick={() => {
          setError(''); setFieldErrors({}); setApply(APPLY_STYLE); setStep('review');
        }}>{text('write_myself')}</Action>
        <Action type="submit" data-testid="skill-generate" disabled={working || !creation.draft_route}>{text(busy === 'draft' ? 'generating' : 'generate')}</Action>
      </>
      : <>
        <Action type="button" variant="ghost" data-testid="skill-back" disabled={working} onClick={() => { setError(''); setStep('request'); }}>{text('back')}</Action>
        <Action type="submit" disabled={working}>{text(busy === 'save' ? 'saving' : 'save')}</Action>
      </>;

  return <>
    {control ? null : <Action type="button" variant="outline" size="sm" disabled={disabled || !sessionId || !user?.id} onClick={() => {
      if (sessionId && user?.id) { setError(''); setOpenScope({ userId: user.id, sessionId }); }
    }}><Plus className="size-3.5" />{text('create')}</Action>}
    <Dialog open={open} onOpenChange={next => { if (next) { setOpen(true); return; } requestClose(); }}>
      <DialogContent showCloseButton={false} className="flex h-[min(720px,calc(100dvh-80px))] max-h-[calc(100dvh-80px)] w-[min(768px,calc(100vw-80px))] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none" data-testid={editing ? 'workspace-skill-editor' : 'workspace-skill-creator'}>
        <div className="shrink-0 space-y-2 border-b px-5 py-4">
          <DialogTitle>{text(editing ? 'edit' : 'create')}</DialogTitle>
          <DialogDescription>{text(editing ? 'edit_description' : 'create_description')}</DialogDescription>
        </div>
        <form onSubmit={event => (editing ? event.preventDefault() : step === 'request' ? generate(event) : create(event))} className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-5">{body}</div>
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t px-5 py-4">
            <div className="min-w-0">{error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}</div>
            <div className="ml-auto flex gap-2">
              {/* 两个操作组常驻：点击"继续编辑"时按钮不能在自己的 click 事件里被卸载，
                  否则 Base UI 的外部点击检测会把它当成外部目标并关闭对话框。 */}
              <div className={confirmDiscard ? 'hidden' : 'flex gap-2'} data-testid="skill-edit-actions">{actions}</div>
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
