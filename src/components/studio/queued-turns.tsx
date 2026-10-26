'use client';

import { Clock, X } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import type { QueuedTurn } from '@/lib/chat/send-queue';

/**
 * 排队中的消息：Agent 还在处理上一条时发出的话。它在对话底部等着，上一轮结束就
 * 自动发出；还没发出的随时可以取消。
 */
export function QueuedTurns({ turns, onCancel }: { turns: readonly QueuedTurn[]; onCancel: (id: string) => void }) {
  const { text } = useProductSchema();
  if (turns.length === 0) return null;
  return (
    <ul className="mt-2 flex flex-col gap-1.5 px-1" aria-label={text('ui.studio.chat.queued_list')} data-testid="chat-queued-turns">
      {turns.map((turn) => (
        <li
          key={turn.id}
          data-testid="chat-queued-turn"
          className="flex items-start gap-2 rounded-lg border border-dashed border-border bg-panel-raised px-3 py-2 text-xs"
        >
          <Clock aria-hidden className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <p className="line-clamp-3 text-[13px] [overflow-wrap:anywhere]">{turn.text}</p>
            <p className="mt-0.5 text-muted-foreground">
              <span className="font-medium text-foreground" data-testid="chat-queued-label">{text('ui.studio.chat.queued')}</span>
              <span className="ml-1.5">{text('ui.studio.chat.queued_note')}</span>
            </p>
          </div>
          <HintTooltip name={text('ui.studio.chat.queued_cancel')} className="shrink-0">
            <Action
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={text('ui.studio.chat.queued_cancel')}
              data-testid="chat-queued-cancel"
              onClick={() => onCancel(turn.id)}
              className="text-muted-foreground"
            >
              <X className="size-3.5" />
            </Action>
          </HintTooltip>
        </li>
      ))}
    </ul>
  );
}
