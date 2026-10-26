'use client';

import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';

import { cn } from '@/lib/utils';
import { schemaCountMessage, schemaText, type ProductSchema } from '@/lib/product-schema';
import { HintTooltip } from '@/components/ui/hint-tooltip';

/** 面板里所有可见文字，来自 Product Schema 的 ui.workspace.notes.* 。 */
export interface SessionNotesText {
  label: string;
  hint: string;
  filled: string;
  save: string;
  revert: string;
  tooLong: string;
  counter: (count: number, maximum: number) => string;
}

export function sessionNotesText(schema: ProductSchema): SessionNotesText {
  return {
    label: schemaText(schema, 'ui.workspace.notes.label'),
    hint: schemaText(schema, 'ui.workspace.notes.hint'),
    filled: schemaText(schema, 'ui.workspace.notes.filled'),
    save: schemaText(schema, 'ui.workspace.notes.save'),
    revert: schemaText(schema, 'ui.workspace.notes.revert'),
    tooLong: schemaText(schema, 'ui.workspace.notes.too_long'),
    counter: (count, maximum) => schemaCountMessage(schema, 'ui.workspace.notes.counter', count, { count, maximum }),
  };
}

/** 草稿相对已保存文本的状态：是否改动、是否超过 Schema 上限、能否保存。 */
export function notesDraftState(draft: string, saved: string, maximum: number) {
  const count = Array.from(draft.trim()).length;
  const dirty = draft.trim() !== saved.trim();
  const overLimit = count > maximum;
  return { count, dirty, overLimit, canSave: dirty && !overLimit };
}

/** 草稿和它所对照的已保存文本（草稿是从哪一份已保存文本开始的）。 */
export interface NotesDraft {
  draft: string;
  base: string;
}

/**
 * 已保存的文本变了（异步加载完成、Agent 更新了摘要）：用户没有改过的草稿换成新文本，改过的草稿原样保留。
 * 面板常常在设定读回之前就挂载，草稿起初是空的；只有这样，空草稿才不会在保存时把已有设定清掉。
 * 没有变化时原样返回同一个对象。
 */
export function followSavedNotes(state: NotesDraft, saved: string): NotesDraft {
  if (state.base === saved) return state;
  const untouched = state.draft.trim() === state.base.trim();
  return { draft: untouched ? saved : state.draft, base: saved };
}

export interface SessionNotesPanelProps {
  text: SessionNotesText;
  saved: string;
  maximum: number;
  busy?: boolean;
  defaultExpanded?: boolean;
  onSave: (notes: string) => Promise<boolean> | boolean;
}

/**
 * 会话设定摘要（放在 Agent 对话框顶部，可折叠）：Agent 每轮都会看到这份文字，
 * 用户可以直接查看和修改。组件不取数据，保存结果由调用方决定；调用方按会话 ID
 * 设置 key，切换会话时丢弃未保存的草稿。
 */
export function SessionNotesPanel({ text, saved, maximum, busy = false, defaultExpanded = false, onSave }: SessionNotesPanelProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [notes, setNotes] = useState<NotesDraft>(() => ({ draft: saved, base: saved }));
  // 已保存的文本变化（设定读回、Agent 更新了摘要）时，在这次渲染里就让没改过的草稿跟上，
  // 不留一帧把旧草稿当成改动。
  const followed = followSavedNotes(notes, saved);
  if (followed !== notes) setNotes(followed);
  const draft = followed.draft;
  const setDraft = (value: string) => setNotes({ draft: value, base: saved });
  const state = notesDraftState(draft, saved, maximum);
  const Chevron = expanded ? ChevronDown : ChevronRight;
  return (
    <div className="border-b border-border px-5 py-2" data-testid="session-notes-panel">
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
        className="flex w-full items-center gap-1.5 text-left text-xs font-medium text-muted-foreground hover:text-foreground"
      >
        <Chevron className="h-3.5 w-3.5" aria-hidden="true" />
        <span>{text.label}</span>
        {saved.trim() !== '' ? (
          <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary" data-testid="session-notes-filled">
            {text.filled}
          </span>
        ) : null}
      </button>
      {expanded ? (
        <div className="mt-2 flex flex-col gap-1.5" data-testid="session-notes-body">
          <p className="text-xs leading-5 text-muted-foreground">{text.hint}</p>
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            aria-label={text.label}
            rows={4}
            disabled={busy}
            className="w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-sm leading-5 outline-none focus-visible:ring-1 focus-visible:ring-primary"
          />
          <div className="flex items-center gap-2">
            <HintTooltip name={state.overLimit ? text.tooLong : ''}>
              <span
                className={cn('text-xs', state.overLimit ? 'text-destructive' : 'text-muted-foreground')}
                data-testid="session-notes-counter"
              >
                {text.counter(state.count, maximum)}
              </span>
            </HintTooltip>
            <span className="ml-auto flex items-center gap-1.5">
              <button
                type="button"
                disabled={busy || !state.dirty}
                onClick={() => setDraft(saved)}
                className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:text-foreground disabled:opacity-50"
              >
                {text.revert}
              </button>
              <button
                type="button"
                disabled={busy || !state.canSave}
                onClick={() => void onSave(draft)}
                className="rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50"
              >
                {text.save}
              </button>
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}
