'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import type { ChatContextCompaction } from '@/lib/chat/browser-client';
import { schemaCountMessage } from '@/lib/product-schema';

/**
 * #322: a display-only row in the conversation where the context was compacted. The chat history
 * itself is unchanged; this is not a message and is never copied or sent.
 */
export function ContextCompactedDivider({ compaction }: { compaction: ChatContextCompaction }) {
  const { schema } = useProductSchema();
  const key = compaction.trigger === 'auto' ? 'ui.chat.context_compacted_auto' : 'ui.chat.context_compacted';
  return (
    <div
      role="note"
      data-testid="chat-context-compacted"
      data-trigger={compaction.trigger}
      className="my-2 flex items-center gap-2 px-1 text-[11px] leading-4 text-muted-foreground select-none"
    >
      <span aria-hidden className="h-px min-w-4 flex-1 bg-border" />
      <span className="max-w-[85%] text-center">{schemaCountMessage(schema, key, compaction.rounds, { rounds: compaction.rounds })}</span>
      <span aria-hidden className="h-px min-w-4 flex-1 bg-border" />
    </div>
  );
}
