'use client';

import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover, type TableSection } from '@/components/studio/table-popover';
import { useSessionReasoning } from '@/hooks/use-session-reasoning';
import type { ChatReasoning } from '@/lib/chat/browser-client';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';

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
    ...(reasoning && levels.length === 0 ? { disabledReason: schemaText(schema, 'ui.studio.reasoning.unavailable') } : {}),
    onPick,
  };
}

/**
 * 输入区底部的思考强度选择，与 Agent 模式并列：选项就是当前对话模型官方的档位值。档位按会话
 * 保存在服务端，只影响之后发出的消息；模型没有档位时停用并说明原因。思考内容本身不展示。
 */
export function ReasoningPicker() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { supported, reasoning, busy, setLevel } = useSessionReasoning(activeSessionId);
  if (!supported) return null;
  const label = schemaText(schema, 'ui.studio.reasoning.label');
  const noLevels = reasoning !== null && reasoning.levels.length === 0;
  const value = reasoning?.effort || schemaText(schema, 'ui.studio.context.unknown');
  const section = reasoningSection(schema, reasoning, (level) => {
    if (level === reasoning?.effort) return;
    void setLevel(level).then((ok) => {
      if (!ok) toast.error(schemaText(schema, 'ui.studio.reasoning.change_failed'));
    });
  });

  return (
    <TablePopover
      testId="workspace-reasoning-picker"
      label={label}
      value={value}
      hint={schemaText(schema, 'ui.studio.reasoning.hint')}
      triggerDisabledReason={noLevels ? schemaText(schema, 'ui.studio.reasoning.unavailable') : undefined}
      disabled={!activeSessionId || busy || reasoning === null || noLevels}
      sections={[section]}
    />
  );
}
