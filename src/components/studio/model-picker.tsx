'use client';

import { useEffect, useMemo } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover, type TableCell, type TableSection } from '@/components/studio/table-popover';
import { useActiveAgentScope } from '@/hooks/use-session-agent-scope';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import { pickerDisabledReason, scopeAllowsDomain } from '@/lib/chat/agent-scope';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { RETEXTURE, buildModelPickerTable, pickerTriggerNames, type PickerCellValue } from '@/lib/workspace/model-picker';
import { retiredModelSwitch } from '@/lib/workspace/retired-models';
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

interface DomainPick {
  section: TableSection;
  /** 触发按钮上的简称和悬停里的全名；未选时都是"选择模型"。 */
  short: string;
  full: string;
}

/**
 * 一个域（2D 或 3D）的小表格：列出模型、供应商、能力对勾和积分。Agent 模式不含这个域时
 * 整张表变暗并写明原因，已选的模型保留。
 */
export function useDomainModelPick(domain: WorkspaceDomain): DomainPick {
  const { schema } = useProductSchema();
  const selectedModel = useWorkspaceStore((state) => state.modelByDomain[domain]);
  const setModel = useWorkspaceStore((state) => state.setModel);
  const { offeringsByCapability, isLoading, error } = useWorkspaceOfferings(schema, domain);
  const { data: retexture } = useSWR<OfferingWire[]>(
    domain === 'model-3d' ? ['workspace-offerings', schema.revision, RETEXTURE] : null,
    () => fetchCapabilityOfferings(schema, RETEXTURE),
    { revalidateOnFocus: false },
  );
  const scope = domain === 'image' ? 'image' : 'model';
  // The session's Agent scope can switch this domain off: in 2D mode the Agent has no 3D tools,
  // so the 3D model cannot be used (and the other way round). The pick itself is kept.
  const agentScope = useActiveAgentScope();
  const outOfScope = !scopeAllowsDomain(schema, agentScope, domain);
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
  return {
    short: names.short,
    full: names.full,
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
      disabledReason: outOfScope ? pickerDisabledReason(schema, agentScope) : undefined,
      loading: isLoading,
      loadingText: schemaText(schema, 'ui.workspace.models.loading'),
      failedText: error ? schemaText(schema, 'ui.workspace.models.load_failed') : undefined,
      emptyText: schemaText(schema, 'ui.workspace.models.empty'),
      onPick: (key) => setModel(domain, key),
    },
  };
}

/**
 * 生成模型选择：一个按钮显示 2D 和 3D 各自选中的模型，点开是左 2D、右 3D 两张小表格，
 * 每张各单选一项，互不影响。Agent 跨域时各用各的。
 */
export function ModelPicker() {
  const { schema } = useProductSchema();
  const image = useDomainModelPick('image');
  const model = useDomainModelPick('model-3d');
  const pair = schemaText(schema, 'ui.studio.picker.pair');

  return (
    <TablePopover
      testId="workspace-model-picker"
      label={schemaText(schema, 'ui.studio.picker.title')}
      value={formatSchemaText(pair, { image: image.short, model: model.short })}
      // Each name is cut on its own, so a long 2D name never hides the 3D one (#324); the separator is the pair's own.
      valueParts={{ parts: [image.short, model.short], separator: formatSchemaText(pair, { image: '', model: '' }) }}
      fullValue={formatSchemaText(pair, { image: image.full, model: model.full })}
      sections={[image.section, model.section]}
    />
  );
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
        <p key={notice.from} role="status" className="text-[11px] leading-4 text-warning" data-testid="workspace-model-retired-notice">
          {formatSchemaText(schemaText(schema, 'ui.studio.picker.model_retired'), { from: notice.from, to: notice.to })}
        </p>
      ))}
    </>
  );
}
