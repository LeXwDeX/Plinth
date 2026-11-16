'use client';

import { RotateCw, TriangleAlert } from 'lucide-react';
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
 * #314: the column of a part whose read failed and holds nothing: the Schema's words for what could not be read, the
 * shared "unavailable" line, and a retry. Used in place of the part's table, so a failed read never makes the part
 * silently absent.
 */
function readFailedSection(
  schema: ProductSchema,
  failed: { title: string; head: string; text: string; reason: string; testId: string; retryTestId: string; onRetry: () => void },
): TableSection {
  return {
    title: failed.title,
    head: [failed.head],
    columns: 'minmax(0,1fr)',
    rows: [],
    selectedKey: null,
    failedText: failed.text,
    foot: (
      <div className="flex flex-col gap-1">
        <UnavailableNotice
          testId={`${failed.testId}-unavailable`}
          className="text-[11px] leading-4"
          label={unavailableLabel(schema, failed.reason)}
          detail={failed.text}
        />
        <button
          type="button"
          data-testid={failed.retryTestId}
          onClick={failed.onRetry}
          className="flex h-6 items-center gap-1 self-start rounded-md px-1.5 text-[11px] text-muted-foreground hover:bg-panel-raised hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        >
          <RotateCw aria-hidden className="size-3" />
          {schemaText(schema, 'ui.studio.chat_model.retry')}
        </button>
      </div>
    ),
    closeOnPick: false,
    testId: failed.testId,
    onPick: () => undefined,
  };
}

/**
 * #176: one entry for the chat model and its thinking level. #223: the popover shows them in two columns,
 * the offered models on the left and the levels of the highlighted (session) model on the right. Picking a
 * model keeps it open and the right column follows once the server answers with that model's levels (a level
 * the new model lacks falls back to its default there); picking a level closes it. Neither choice changes the
 * server protocol: the two existing per-session PUTs run in order. A narrow panel stacks the two columns.
 * #314: a part whose read failed and that holds nothing yet is not left out: its column says the read failed and
 * offers a retry (the hooks read it again by themselves, bounded), and the trigger carries a warning mark.
 */
export function ChatModelPicker() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const chatModel = useSessionChatModel(activeSessionId);
  const reasoning = useSessionReasoning(activeSessionId);
  const models = chatModel.choice?.options ?? [];
  const multipleModels = Boolean(chatModel.supported && chatModel.choice && models.length >= 2);
  const levelsAvailable = Boolean(reasoning.supported && reasoning.reasoning);
  const modelFailed = Boolean(chatModel.supported && chatModel.failed);
  const levelsFailed = Boolean(reasoning.supported && reasoning.failed);
  const showsModel = multipleModels || modelFailed;
  const showsLevels = levelsAvailable || levelsFailed;
  if (!showsModel && !showsLevels) return null;
  const soloModel = models.length === 1 ? models[0].model : '';
  const selectedModel = modelName(chatModel.choice, chatModel.choice?.selected ?? '') || soloModel;
  const level = levelsAvailable ? reasoning.reasoning?.effort ?? '' : '';
  const anyFailed = modelFailed || levelsFailed;
  const label = schemaText(schema, showsModel && showsLevels
    ? 'ui.studio.chat_model.combined_label'
    : showsLevels ? 'ui.studio.reasoning.label' : 'ui.studio.chat_model.label');
  const hint = anyFailed
    // The shared "unavailable" line for each part that could not be read, in place of the description.
    ? [
      modelFailed ? unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.picker.chat_model_load_failed')) : '',
      levelsFailed ? unavailableLabel(schema, schemaText(schema, 'ui.unavailable.reason.picker.reasoning_load_failed')) : '',
    ].filter(Boolean).join(' ')
    : schemaText(schema, multipleModels && levelsAvailable
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
  } else if (modelFailed) {
    sections.push(readFailedSection(schema, {
      title: schemaText(schema, 'ui.studio.chat_model.label'),
      head: schemaText(schema, 'ui.studio.chat_model.col.model'),
      text: schemaText(schema, 'ui.studio.chat_model.load_failed'),
      reason: schemaText(schema, 'ui.unavailable.reason.picker.chat_model_load_failed'),
      testId: 'workspace-chat-model-section',
      retryTestId: 'workspace-chat-model-retry',
      onRetry: chatModel.retry,
    }));
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
  } else if (levelsFailed) {
    sections.push(readFailedSection(schema, {
      // Beside a loaded model list the levels column names the model it belongs to, as the levels table does (#223).
      title: multipleModels
        ? formatSchemaText(schemaText(schema, 'ui.studio.chat_model.levels_title'), { model: selectedModel })
        : schemaText(schema, 'ui.studio.reasoning.label'),
      head: schemaText(schema, 'ui.studio.reasoning.col.level'),
      text: schemaText(schema, 'ui.studio.reasoning.load_failed'),
      reason: schemaText(schema, 'ui.unavailable.reason.picker.reasoning_load_failed'),
      testId: 'workspace-reasoning-section',
      retryTestId: 'workspace-reasoning-retry',
      onRetry: reasoning.retry,
    }));
  }
  if (sections.length === 0) return null;
  // The levels and the model list load separately: until the model name is known the trigger names the level alone
  // instead of showing a bare separator (#236). The trigger is the same node either way; only its text changes.
  const loaded = [selectedModel, level].filter(Boolean).join(' · ');
  // #314: when nothing could be read the trigger says so in the Schema's words rather than showing an empty value.
  const value = loaded || (anyFailed ? schemaText(schema, 'ui.studio.chat_model.load_failed_short') : '');
  const valueParts = level && selectedModel ? { parts: [selectedModel, level], separator: ' · ' } : undefined;
  return (
    <TablePopover
      testId="workspace-chat-model-picker"
      label={label}
      value={value}
      valueParts={valueParts}
      fullValue={value}
      icon={anyFailed ? <TriangleAlert aria-hidden className="size-3.5 shrink-0 text-warning" /> : undefined}
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
