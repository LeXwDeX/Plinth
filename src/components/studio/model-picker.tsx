'use client';

import { useEffect, useMemo } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { TableSections, type TableCell, type TableSection } from '@/components/studio/table-popover';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { pickExplicitModel3D, pickExplicitModelImage } from '@/lib/chat/explicit-model';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { unavailableLabel } from '@/lib/unavailable';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { RETEXTURE, buildModelPickerTable, pickerTriggerNames, type PickerCellValue } from '@/lib/workspace/model-picker';
import { retiredModelSwitch } from '@/lib/workspace/retired-models';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore, type WorkspaceDomain } from '@/store/workspace-store';

const COLUMNS: Record<WorkspaceDomain, string> = {
  image: 'minmax(0,2.05fr) minmax(0,1fr) 44px 52px 34px',
  'model-3d': 'minmax(0,1.95fr) minmax(0,1fr) 46px 46px 44px',
};

const HEAD_KEYS: Record<WorkspaceDomain, string[]> = {
  image: ['model', 'provider', 'image_to_image', 'mask', 'credits'],
  'model-3d': ['model', 'provider', 'text_to_3d', 'image_to_3d', 'retexture'],
};

function cellText(cell: PickerCellValue, unknownPrice: string): { text: string; muted: boolean } {
  if (cell.kind === 'tick') return { text: cell.on ? '✓' : '—', muted: !cell.on };
  if (cell.kind === 'native-price') return { text: cell.price ?? unknownPrice, muted: cell.price === null };
  // 不计积分的 Provider 显示 "/"；计积分但无估算显示 "—"。
  if (!cell.metered) return { text: '/', muted: true };
  return { text: cell.credits == null ? '—' : String(cell.credits), muted: false };
}

export interface DomainPick {
  section: TableSection;
  /** 选中模型的简称和全名（供应商只在同名时才带上，#324）；未选时都是"选择模型"。 */
  short: string;
  full: string;
  /**
   * #546: drop the person's pick for this domain so the Agent chooses again: the picker shows no selection and the
   * session's explicit lock is cleared through the same serialized write a pick uses (an empty model clears it).
   */
  clear: () => void;
}

/**
 * 一个域（2D 或 3D）的小表格：列出模型、供应商、能力对勾和积分。会话一律按 auto 模式
 * 工作，两张表都可用（范围选择器已删除，#103）。
 */
export function useDomainModelPick(domain: WorkspaceDomain): DomainPick {
  const { schema } = useProductSchema();
  const selectedModel = useWorkspaceStore((state) => state.modelByDomain[domain]);
  const setModel = useWorkspaceStore((state) => state.setModel);
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const client = useMemo(() => createChatBrowserClient(schema), [schema]);
  const { offeringsByCapability, isLoading, error } = useWorkspaceOfferings(schema, domain);
  const { data: retexture } = useSWR<OfferingWire[]>(
    domain === 'model-3d' ? ['workspace-offerings', schema.revision, RETEXTURE] : null,
    () => fetchCapabilityOfferings(schema, RETEXTURE),
    { revalidateOnFocus: false },
  );
  const scope = domain === 'image' ? 'image' : 'model';
  const text = (key: string) => schemaText(schema, `ui.studio.picker.${key}`);

  const table = useMemo(
    () => buildModelPickerTable(domain, offeringsByCapability, retexture),
    [domain, offeringsByCapability, retexture],
  );
  // #329: a saved pick of a retired model (e.g. Meshy 7) moves to its successor once the choices have
  // loaded, and the notice under the composer says so.
  const retired = schema.runtime.frontend.retired_generation_models;
  const switchRetiredModel = useWorkspaceStore((state) => state.switchRetiredModel);
  useEffect(() => {
    if (isLoading || error || !offeringsByCapability) return;
    const choices = table.rows.filter((row) => row.enabled).map((row) => ({ model_id: row.modelId, model_name: row.name }));
    const change = retiredModelSwitch(retired, selectedModel, choices);
    if (change) switchRetiredModel(domain, change.to, { from: change.from, to: change.toName });
  }, [domain, error, isLoading, offeringsByCapability, retired, selectedModel, switchRetiredModel, table.rows]);
  const rows = table.rows.map((row) => ({
    key: row.modelId,
    disabled: !row.enabled,
    ...(!row.enabled ? { disabledReason: schemaText(schema, 'ui.unavailable.reason.picker.model_disabled') } : {}),
    cells: [
      {
        text: row.name,
        strong: true,
        tag: !row.enabled ? schemaText(schema, 'ui.studio.picker.tag.disabled') : row.recommended ? schemaText(schema, 'ui.studio.picker.tag.recommended') : undefined,
        tagTone: row.enabled && row.recommended ? 'reminder' as const : undefined,
      },
      { text: row.provider, muted: true },
      ...row.cells.map((cell): TableCell => cellText(cell, schemaText(schema, 'ui.pricing.unknown'))),
    ],
  }));
  const selected = table.rows.find((row) => row.modelId === selectedModel);
  const hasNativePrices = table.rows.some((row) => row.cells.some((cell) => cell.kind === 'native-price'));
  const separator = schemaText(schema, 'ui.studio.list_separator');
  const foot = domain === 'image' && table.maskModels.length > 0
    ? formatSchemaText(schemaText(schema, 'ui.studio.picker.foot_mask'), { models: table.maskModels.join(separator) })
    : domain === 'model-3d' && table.retextureProviders.length > 0
      ? formatSchemaText(schemaText(schema, 'ui.studio.picker.foot_retexture'), { providers: table.retextureProviders.join(separator) })
      : undefined;

  // The trigger names the chosen model (#324), with the vendor short name only when two rows share the name.
  const names = selected
    ? pickerTriggerNames(selected, table.rows, {
      shortWithProvider: schemaText(schema, 'ui.studio.picker.short_with_provider'),
      fullWithProvider: schemaText(schema, 'ui.studio.picker.full_with_provider'),
    })
    : { short: schemaText(schema, 'ui.studio.picker.choose'), full: schemaText(schema, 'ui.studio.picker.choose') };
  // #103/#239: a 2D or 3D pick is the person's explicit lock for this session; null clears it (#546). The restore and
  // retired-successor paths above call setModel directly and do not mark it. A pick before the session exists waits
  // for the first send; later picks are serialized per session and the newest wins.
  const choose = (key: string | null) => {
    setModel(domain, key);
    const route = domain === 'image'
      ? schema.runtime.http.routes.chat_session_model2d
      : schema.runtime.http.routes.chat_session_model3d;
    if (!route) return;
    const write = domain === 'image'
      ? (sessionId: string, model: string) => client.setExplicitModelImage(sessionId, model)
      : (sessionId: string, model: string) => client.setExplicitModel3D(sessionId, model);
    const pick = domain === 'image' ? pickExplicitModelImage : pickExplicitModel3D;
    pick({
      sessionId: activeSessionId,
      model: key ?? '',
      previous: selectedModel ?? null,
      write,
      onFailure: (previous) => {
        setModel(domain, previous);
        toast.error(schemaText(schema, 'ui.studio.picker.lock_failed'));
      },
    });
  };
  return {
    short: names.short,
    full: names.full,
    clear: () => choose(null),
    section: {
      testId: `workspace-model-picker-${scope}`,
      title: text(`${scope}.title`),
      hint: text(`${scope}.hint`),
      head: HEAD_KEYS[domain].map((key) => key === 'credits' && hasNativePrices
        ? schemaText(schema, 'ui.pricing.totals_heading')
        : text(`col.${key}`)),
      columns: COLUMNS[domain],
      rows,
      selectedKey: selectedModel,
      foot,
      loading: isLoading,
      loadingText: schemaText(schema, 'ui.workspace.models.loading'),
      failedText: error ? schemaText(schema, 'ui.workspace.models.load_failed') : undefined,
      emptyText: schemaText(schema, 'ui.workspace.models.empty'),
      onPick: (key) => choose(key),
    },
  };
}

/**
 * 2D 与 3D 各自选中的简称，按 Schema 的「{image} · {model}」写成一句（设置页的「已更改」提示用它）。
 */
export function generationModelPair(schema: ProductSchema, image: DomainPick, model: DomainPick): string {
  return formatSchemaText(schemaText(schema, 'ui.studio.picker.pair'), { image: image.short, model: model.short });
}

/**
 * 设置页的「生成模型」页：左 2D、右 3D 两张小表格，每张各单选一项，互不影响。两个域的取数与选择逻辑
 * （`useDomainModelPick`）由常驻的 ComposerSettings 持有，这里只摆内容：页面关着时，停用模型的改用与提示照常工作。
 */
export function ModelPicker({ image, model }: { image: DomainPick; model: DomainPick }) {
  return <TableSections testId="workspace-model-picker-panel" sections={[image.section, model.section]} />;
}

/**
 * #329: the model saved or restored for a domain is no longer offered, so its successor is used; this line
 * says so until the person picks a model.
 */
export function GenerationModelNotice() {
  const { schema } = useProductSchema();
  const notices = useWorkspaceStore((state) => state.retiredModelNotice);
  const lines = (['image', 'model-3d'] as const).flatMap((domain) => (notices[domain] ? [notices[domain]!] : []));
  if (lines.length === 0) return null;
  return (
    <>
      {lines.map((notice) => (
        <UnavailableNotice
          key={notice.from}
          testId="workspace-model-retired-notice"
          className="text-[11px] leading-4"
          label={unavailableLabel(schema, formatSchemaText(schemaText(schema, 'ui.unavailable.reason.picker.model_retired'), { from: notice.from }))}
          detail={formatSchemaText(schemaText(schema, 'ui.studio.picker.model_retired'), { from: notice.from, to: notice.to })}
        />
      ))}
    </>
  );
}
