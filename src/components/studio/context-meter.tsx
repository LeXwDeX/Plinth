'use client';

import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useSessionContext } from '@/hooks/use-session-context';
import { formatContextUsage, formatTokenCount } from '@/lib/chat/token-count';
import { withErrorReference } from '@/lib/problem-reference';
import { formatSchemaText, schemaCountMessage, schemaText, type ChatProductSchema, type ProductSchema } from '@/lib/product-schema';
import { PICKER_UPPERCASE_CLASS } from '@/components/studio/table-popover';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';

type CompactionSettings = NonNullable<ChatProductSchema['context_compaction']>;

/** How full the context is: warning from the Schema's warning ratio, critical from the automatic compaction point. */
export function contextLevel(tokens: number | null, window: number | null, settings: CompactionSettings): 'normal' | 'warning' | 'critical' {
  if (tokens === null || window === null || window <= 0) return 'normal';
  const ratio = tokens / window;
  if (ratio >= settings.auto_threshold_ratio) return 'critical';
  if (ratio >= settings.warning_ratio) return 'warning';
  return 'normal';
}

const LEVEL_CLASS = { normal: 'text-muted-foreground', warning: 'text-warning', critical: 'text-destructive' } as const;

function contextHint(schema: ProductSchema, tokens: number | null, window: number | null, settings: CompactionSettings): string {
  if (tokens === null) return schemaText(schema, 'ui.studio.context.hint_unknown');
  const used = formatTokenCount(tokens);
  if (window === null || window <= 0) return formatSchemaText(schemaText(schema, 'ui.studio.context.hint_used'), { used });
  // Without automatic compaction the window line would name a compaction point that does not exist.
  if (!settings.enabled) {
    return formatSchemaText(schemaText(schema, 'ui.studio.context.hint_used'), { used: `${used} / ${formatTokenCount(window)}` });
  }
  return formatSchemaText(schemaText(schema, 'ui.studio.context.hint_window'), {
    used,
    window: formatTokenCount(window),
    threshold: Math.round(settings.auto_threshold_ratio * 100),
  });
}

/**
 * #322: the session's context usage under the input box ("356K / 1M", as the upstream reported it,
 * never estimated) and the action that compacts it now. The chat history never changes; a compaction
 * shows as a divider in the conversation. A compaction is refused while a reply runs.
 */
export function ContextMeter() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { supported, canCompact, context, compacting, turnRunning, compact } = useSessionContext(activeSessionId);
  const settings = schema.chat.context_compaction;
  if (!supported || !settings) return null;
  const tokens = context?.tokens ?? null;
  const window = context?.window ?? null;
  const label = schemaText(schema, 'ui.studio.context.label');
  const text = formatContextUsage(tokens, window, schemaText(schema, 'ui.studio.context.unknown'));
  const level = contextLevel(tokens, window, settings);
  const compactLabel = schemaText(schema, compacting ? 'ui.studio.context.compacting' : 'ui.studio.context.compact');

  const run = () => {
    void compact().then((result) => {
      if (result.ok) {
        toast.success(schemaText(schema, 'ui.studio.context.compacted'));
        return;
      }
      const fallback = schemaText(schema, 'ui.studio.context.compact_failed');
      toast.error(result.message ? withErrorReference(result.message, result.cause) : fallback);
    });
  };

  return (
    // A full row of the composer's picker grid when narrow, two tracks when wide: the figure sits on the left
    // edge and the compact action on the right edge, in the same one-picker height as the cells above.
    <div className="col-span-full flex h-7 min-w-0 items-center justify-between gap-1 @[40rem]:col-span-2" data-testid="composer-context">
      <HintTooltip name={`${label}: ${text}`} hint={contextHint(schema, tokens, window, settings)} className="min-w-0 shrink-0">
        <span
          tabIndex={0}
          aria-label={`${label}: ${text}`}
          data-testid="composer-context-meter"
          data-level={level}
          className={cn('truncate rounded-sm px-1 text-[11px] leading-7 tabular-nums focus-visible:outline-2 focus-visible:outline-ring', PICKER_UPPERCASE_CLASS, LEVEL_CLASS[level])}
        >
          {text}
        </span>
      </HintTooltip>
      {canCompact ? (
        <HintTooltip
          name={compactLabel}
          hint={schemaCountMessage(schema, 'ui.studio.context.compact_hint', settings.keep_recent_rounds, { rounds: settings.keep_recent_rounds })}
          reason={turnRunning ? schemaText(schema, 'ui.studio.context.compact_busy') : undefined}
          className="min-w-0"
        >
          <Action
            type="button"
            variant="ghost"
            size="xs"
            loading={compacting}
            disabled={!activeSessionId || turnRunning || compacting}
            onClick={run}
            data-testid="composer-context-compact"
            className={cn('min-w-0 shrink text-[11px] font-normal text-muted-foreground', PICKER_UPPERCASE_CLASS)}
          >
            <span className="truncate">{compactLabel}</span>
          </Action>
        </HintTooltip>
      ) : null}
    </div>
  );
}
