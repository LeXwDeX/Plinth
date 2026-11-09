'use client';

import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover, type TableSection } from '@/components/studio/table-popover';
import { useSessionChatModel } from '@/hooks/use-session-chat-model';
import { useSessionReasoning } from '@/hooks/use-session-reasoning';
import type { ChatModelChoice } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { reasoningSection } from '@/components/studio/reasoning-picker';
import { unavailableLabel } from '@/lib/unavailable';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

/** The model name of an offered model id; the id itself when it is not offered (any more). */
export function modelName(choice: ChatModelChoice | null, id: string): string {
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
        kind: 'name',
        ...(option.id === choice?.defaultId ? { tag: schemaText(schema, 'ui.studio.chat_model.default_tag') } : {}),
      }],
    })),
    selectedKey: choice?.selected || null,
    foot: schemaText(schema, 'ui.studio.chat_model.note'),
    onPick,
  };
}

/**
 * #176: one entry for the chat model and its thinking level. #223: the popover shows them in two columns,
 * the offered models on the left and the levels of the highlighted (session) model on the right. Picking a
 * model keeps it open and the right column follows once the server answers with that model's levels (a level
 * the new model lacks falls back to its default there); picking a level closes it. Neither choice changes the
 * server protocol: the two existing per-session PUTs run in order. A narrow panel stacks the two columns.
 */
export function ChatModelPicker() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const chatModel = useSessionChatModel(activeSessionId);
  const reasoning = useSessionReasoning(activeSessionId);
  const models = chatModel.choice?.options ?? [];
  const multipleModels = Boolean(chatModel.supported && chatModel.choice && models.length >= 2);
  const levelsAvailable = Boolean(reasoning.supported && reasoning.reasoning);
  if (!multipleModels && !levelsAvailable) return null;
  const soloModel = models.length === 1 ? models[0].model : '';
  const selectedModel = modelName(chatModel.choice, chatModel.choice?.selected ?? '') || soloModel;
  const level = levelsAvailable ? reasoning.reasoning?.effort ?? '' : '';
  const label = schemaText(schema, multipleModels && levelsAvailable
    ? 'ui.studio.chat_model.combined_label'
    : levelsAvailable ? 'ui.studio.reasoning.label' : 'ui.studio.chat_model.label');
  const hint = schemaText(schema, multipleModels && levelsAvailable
    ? 'ui.studio.chat_model.combined_hint'
    : levelsAvailable ? 'ui.studio.reasoning.hint' : 'ui.studio.chat_model.hint');
  const sections: TableSection[] = [];
  const offline = chatModel.choice?.fallback
    ? (
      <UnavailableNotice
        className="text-[11px] leading-4"
        label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.picker.chat_model_offline'))}
        detail={formatSchemaText(schemaText(schema, 'ui.studio.chat_model.fallback'), { model: selectedModel })}
      />
    )
    : null;
  if (multipleModels) {
    const section = chatModelSection(schema, chatModel.choice, (id) => {
        if (id === chatModel.choice?.selected) return;
        void chatModel.setModel(id).then((ok) => {
          if (!ok) toast.error(schemaText(schema, 'ui.studio.chat_model.change_failed'));
        });
    });
    sections.push({
      ...section,
      ...(offline ? { foot: <div className="flex flex-col gap-1">{offline}<span>{schemaText(schema, 'ui.studio.chat_model.note')}</span></div> } : {}),
      closeOnPick: false,
      testId: 'workspace-chat-model-section',
    });
  }
  if (levelsAvailable) {
    const section = reasoningSection(schema, reasoning.reasoning, (next) => {
      if (next === reasoning.reasoning?.effort) return;
      void reasoning.setLevel(next).then((ok) => {
        if (!ok) toast.error(schemaText(schema, 'ui.studio.reasoning.change_failed'));
      });
    });
    sections.push({
      ...section,
      // #223: beside the model column the levels name the model they belong to and explain an empty list.
      ...(multipleModels ? {
        title: formatSchemaText(schemaText(schema, 'ui.studio.chat_model.levels_title'), { model: selectedModel }),
        emptyText: formatSchemaText(schemaText(schema, 'ui.studio.chat_model.levels_none'), { model: selectedModel }),
        loadingText: schemaText(schema, 'ui.studio.chat_model.levels_loading'),
      } : {}),
      closeOnPick: true,
      loading: chatModel.busy || reasoning.busy,
      testId: 'workspace-reasoning-section',
    });
  }
  if (sections.length === 0) return null;
  const value = level ? `${selectedModel} · ${level}` : selectedModel;
  const valueParts = level && selectedModel ? { parts: [selectedModel, level], separator: ' · ' } : undefined;
  return (
    <TablePopover
      testId="workspace-chat-model-picker"
      label={label}
      value={value}
      valueParts={valueParts}
      fullValue={value}
      hint={hint}
      triggerDisabledReason={!activeSessionId ? schemaText(schema, 'ui.unavailable.reason.picker.no_session') : undefined}
      disabled={!activeSessionId || chatModel.busy || reasoning.busy}
      sections={sections}
      columns
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
    <UnavailableNotice
      testId="workspace-chat-model-notice"
      className="text-[11px] leading-4"
      label={unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.picker.chat_model_offline'))}
      detail={formatSchemaText(schemaText(schema, 'ui.studio.chat_model.fallback'), { model: modelName(choice, choice.selected) })}
    />
  );
}
