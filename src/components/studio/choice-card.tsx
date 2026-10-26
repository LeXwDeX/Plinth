'use client';

import { useId, useRef, useState, type KeyboardEvent } from 'react';
import { Check, ListChecks } from 'lucide-react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { fetchCapabilityOfferings } from '@/lib/agent-offerings';
import {
  choiceAnswerText,
  choiceOptionQuote,
  choiceOptionTitle,
  choiceStatusText,
  radioKeyTarget,
  type ChoiceCardModel,
  type ChoiceSelection,
} from '@/lib/chat/choice-cards';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

export interface ChoiceCardProps {
  card: ChoiceCardModel;
  /** The answer is on its way. */
  busy: boolean;
  /** No answer can be sent now (a turn is running). */
  disabled: boolean;
  /** The answer was confirmed and waits for the composer's pictures to be read (#335). */
  waiting?: boolean;
  /** Why the last answer was refused, in words. */
  error: string | null;
  /** The selection and the words of the user message that answers it (titles as the card shows them). */
  onAnswer: (selection: ChoiceSelection, text: string) => void;
}

/**
 * 对话里的选择卡（#321）：工具有多个可用服务时请用户选一个。单选列表（方向键、
 * Home/End 移动，Enter 确认），「确认」提交所选，「用默认」提交默认项。已答复或
 * 已失效的卡片只读显示结果。所有文字来自 Schema。
 */
export function ChoiceCard({ card, busy, disabled, waiting = false, error, onAnswer }: ChoiceCardProps) {
  const { schema } = useProductSchema();
  const questionId = useId();
  const open = card.state === 'pending';
  const { data: offerings } = useSWR(
    card.capability ? ['choice-card-offerings', schema.revision, card.capability] : null,
    () => fetchCapabilityOfferings(schema, card.capability),
    { revalidateOnFocus: false },
  );
  const initial = card.options.some((option) => option.id === card.defaultOptionId) ? card.defaultOptionId : card.options[0]?.id ?? '';
  const [selected, setSelected] = useState(initial);
  const optionRefs = useRef<Array<HTMLDivElement | null>>([]);
  const checkedId = open ? selected : card.answer?.optionId ?? '';
  const locked = busy || disabled || waiting;
  const capability = schema.capabilities.find((item) => item.id === card.capability);
  const step = capability?.label_cn || capability?.label || card.capability;
  const defaultOption = card.options.find((option) => option.id === card.defaultOptionId);
  const defaultLabel = defaultOption
    ? formatSchemaText(schemaText(schema, 'ui.chat.choice.use_default'), { title: choiceOptionTitle(defaultOption, offerings) })
    : '';
  const status = choiceStatusText(schema, card, offerings);
  const answer = (selection: ChoiceSelection) => onAnswer(selection, choiceAnswerText(schema, card, selection, offerings));

  const choose = (index: number, focus: boolean) => {
    const option = card.options[index];
    if (!option || !open || locked) return;
    setSelected(option.id);
    if (focus) optionRefs.current[index]?.focus();
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>, index: number) => {
    if (!open) return;
    if (event.key === ' ') {
      event.preventDefault();
      choose(index, false);
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (!locked && selected) answer({ optionId: selected });
      return;
    }
    const target = radioKeyTarget(event.key, index, card.options.length);
    if (target === null) return;
    event.preventDefault();
    choose(target, true);
  };

  return (
    <article
      data-testid="chat-choice-card"
      data-choice-id={card.id}
      data-state={card.state}
      aria-label={schemaText(schema, 'ui.chat.choice.label')}
      className={cn('flex flex-col gap-2 rounded-lg border bg-panel-raised p-3 text-xs', open ? 'border-primary/40' : 'border-border opacity-90')}
    >
      <header className="flex items-center gap-2">
        <ListChecks aria-hidden className={cn('size-4 shrink-0', open ? 'text-primary' : 'text-muted-foreground')} />
        <span id={questionId} className="text-[13px] font-semibold [overflow-wrap:anywhere]">
          {formatSchemaText(schemaText(schema, 'ui.chat.choice.offering_question'), { step })}
        </span>
      </header>
      <div role="radiogroup" aria-labelledby={questionId} aria-disabled={!open || locked ? true : undefined} className="flex flex-col gap-1" data-testid="chat-choice-options">
        {card.options.map((option, index) => {
          const checked = option.id === checkedId;
          return (
            <div
              key={option.id}
              ref={(node) => { optionRefs.current[index] = node; }}
              role="radio"
              aria-checked={checked}
              tabIndex={open ? (checked ? 0 : -1) : -1}
              aria-disabled={!open || locked ? true : undefined}
              data-testid="chat-choice-option"
              data-option-id={option.id}
              onClick={() => choose(index, true)}
              onKeyDown={(event) => onKeyDown(event, index)}
              className={cn(
                'flex items-start gap-2 rounded-md border px-2 py-1.5 outline-none focus-visible:ring-2 focus-visible:ring-ring',
                checked ? 'border-primary bg-primary/10' : 'border-border',
                open && !locked ? 'cursor-pointer hover:bg-muted' : 'cursor-default',
              )}
            >
              <span aria-hidden className={cn('mt-0.5 flex size-3.5 shrink-0 items-center justify-center rounded-full border', checked ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground')}>
                {checked ? <Check className="size-2.5" /> : null}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="font-medium [overflow-wrap:anywhere]">
                  {choiceOptionTitle(option, offerings)}
                  {option.id === card.defaultOptionId ? (
                    <span className="ml-1.5 inline-block whitespace-nowrap rounded bg-muted px-1 py-px text-[10px] font-normal text-muted-foreground" data-testid="chat-choice-default-badge">
                      {schemaText(schema, 'ui.chat.choice.default_badge')}
                    </span>
                  ) : null}
                </span>
                <span className="text-muted-foreground tabular-nums" data-testid="chat-choice-quote">{choiceOptionQuote(schema, option, offerings)}</span>
              </span>
            </div>
          );
        })}
      </div>
      {status ? <p role="status" className="text-muted-foreground" data-testid="chat-choice-status">{status}</p> : null}
      {open && waiting ? <p role="status" className="text-muted-foreground" data-testid="chat-choice-waiting">{schemaText(schema, 'ui.chat.choice.waiting_attachments')}</p> : null}
      {error ? <p role="alert" className="text-destructive" data-testid="chat-choice-error">{error}</p> : null}
      {open ? (
        <footer className="flex flex-wrap items-center gap-1.5">
          <Action type="button" size="sm" disabled={locked || !selected} loading={busy || waiting} onClick={() => answer({ optionId: selected })} data-testid="chat-choice-confirm">
            {schemaText(schema, 'ui.chat.choice.confirm')}
          </Action>
          {card.allowDefault && defaultOption ? (
            // A long service name must not push the button past the card (#321 C): it shrinks to the
            // card width and truncates; the whole text is in the hover hint.
            <HintTooltip name={defaultLabel} className="min-w-0 max-w-full">
              <Action type="button" size="sm" variant="outline" disabled={locked} onClick={() => answer({ useDefault: true })} data-testid="chat-choice-default"
                className="min-w-0 max-w-full shrink">
                <span className="truncate">{defaultLabel}</span>
              </Action>
            </HintTooltip>
          ) : null}
        </footer>
      ) : null}
    </article>
  );
}
