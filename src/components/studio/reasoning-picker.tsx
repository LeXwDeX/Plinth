'use client';


import { useProductSchema } from '@/components/product-schema-provider';
import type { TableSection } from '@/components/studio/table-popover';
import type { ChatReasoning } from '@/lib/chat/browser-client';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';

/**
 * #322: the table of the chat model's thinking levels. The rows are the model's own level values,
 * shown verbatim; the model's default carries a small tag.
 */
export function reasoningSection(
  schema: ProductSchema,
  reasoning: ChatReasoning | null,
  onPick: (level: string) => void,
): TableSection {
  const label = schemaText(schema, 'ui.studio.reasoning.label');
  const levels = reasoning?.levels ?? [];
  return {
    title: label,
    hint: schemaText(schema, 'ui.studio.reasoning.hint'),
    head: [schemaText(schema, 'ui.studio.reasoning.col.level')],
    columns: 'minmax(0,1fr)',
    rows: levels.map((level) => ({
      key: level,
      cells: [{
        text: level,
        strong: true,
        kind: 'name',
        ...(level === reasoning?.defaultLevel ? { tag: schemaText(schema, 'ui.studio.reasoning.default_tag') } : {}),
      }],
    })),
    selectedKey: reasoning?.effort || null,
    foot: schemaText(schema, 'ui.studio.reasoning.note'),
    emptyText: schemaText(schema, 'ui.studio.reasoning.unavailable'),
    ...(reasoning && levels.length === 0 ? {
      disabledReason: schemaText(schema, 'ui.studio.reasoning.unavailable'),
      disabledLabel: unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.picker.reasoning_none')),
    } : {}),
    onPick,
  };
}
