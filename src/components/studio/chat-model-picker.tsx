'use client';

import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover, type TableSection } from '@/components/studio/table-popover';
import { useSessionChatModel } from '@/hooks/use-session-chat-model';
import type { ChatModelChoice } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

/** The model name of an offered model id; the id itself when it is not offered (any more). */
function modelName(choice: ChatModelChoice | null, id: string): string {
  return choice?.options.find((option) => option.id === id)?.model ?? id;
}

/**
 * #322: the table of the chat models the administrator offers. Each row is the upstream model name; the
 * administrator's default carries a small tag. Picking one applies to later messages only.
 */
export function chatModelSection(
  schema: ProductSchema,
  choice: ChatModelChoice | null,
  onPick: (id: string) => void,
): TableSection {
  return {
    title: schemaText(schema, 'ui.studio.chat_model.label'),
    hint: schemaText(schema, 'ui.studio.chat_model.hint'),
    head: [schemaText(schema, 'ui.studio.chat_model.col.model')],
    columns: 'minmax(0,1fr)',
    rows: (choice?.options ?? []).map((option) => ({
      key: option.id,
      cells: [{
        text: option.model,
        strong: true,
        ...(option.id === choice?.defaultId ? { tag: schemaText(schema, 'ui.studio.chat_model.default_tag') } : {}),
      }],
    })),
    selectedKey: choice?.selected || null,
    foot: schemaText(schema, 'ui.studio.chat_model.note'),
    onPick,
  };
}

/** Whether the composer shows the chat model picker for the active session (two or more models offered). */
export function useChatModelPickerShown(): boolean {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const count = useSessionContextStore((state) => (activeSessionId ? state.chatModel[activeSessionId]?.options.length ?? 0 : 0));
  return Boolean(schema.runtime.http.routes.chat_session_model) && count >= 2;
}

/**
 * 输入区底部的对话模型选择，与思考强度、token 数并列。管理员开放两个以上模型时才出现；选择按会话保存在
 * 服务端，切换后思考档位和上下文窗口随新模型变化，只影响之后的消息。
 */
export function ChatModelPicker() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { supported, choice, busy, setModel } = useSessionChatModel(activeSessionId);
  if (!supported || !choice || choice.options.length < 2) return null;
  const label = schemaText(schema, 'ui.studio.chat_model.label');
  const section = chatModelSection(schema, choice, (id) => {
    if (id === choice.selected) return;
    void setModel(id).then((ok) => {
      if (!ok) toast.error(schemaText(schema, 'ui.studio.chat_model.change_failed'));
    });
  });
  return (
    <TablePopover
      testId="workspace-chat-model-picker"
      label={label}
      value={modelName(choice, choice.selected)}
      hint={schemaText(schema, 'ui.studio.chat_model.hint')}
      disabled={!activeSessionId || busy}
      sections={[section]}
    />
  );
}

/**
 * The model the session chose was taken offline by the administrator: turns now run on the default model,
 * and this line says so. It reads what the picker loaded and shows even when only one model is left.
 */
export function ChatModelNotice() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const choice = useSessionContextStore((state) => (activeSessionId ? state.chatModel[activeSessionId] : undefined));
  if (!choice?.fallback) return null;
  return (
    <p role="status" className="text-[11px] leading-4 text-warning" data-testid="workspace-chat-model-notice">
      {formatSchemaText(schemaText(schema, 'ui.studio.chat_model.fallback'), { model: modelName(choice, choice.selected) })}
    </p>
  );
}
