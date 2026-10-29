'use client';

import { useEffect, useRef, useState } from 'react';
import { Plus } from 'lucide-react';
import { toast } from 'sonner';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover, type TableRow } from '@/components/studio/table-popover';
import { SkillCreator } from '@/components/workspace/skill-creator';
import type { AgentEffectiveConfiguration } from '@/lib/agent-configuration';
import type { ChatSkillCatalog } from '@/lib/chat/browser-client';
import { deletePersonalSkill, listPersonalSkillIds } from '@/lib/personal-skills';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { withErrorReference } from '@/lib/problem-reference';

/** The row id of "use no Skill"; it cannot collide with a catalog skill id. */
const NO_SKILL = '';

/**
 * Skill 表格的行：目录里的每个 Skill 一行（当前模型不可用的变暗），末尾是“不使用”。
 * 只有本人的个人 Skill 带删除操作（#238）；“不使用”与系统/共享 Skill 都没有。
 */
export function skillPickerRows({
  catalog, effective, personalIds, noneLabel, noneHint, deleteLabel, deleting, onDelete,
}: {
  catalog: ChatSkillCatalog | null;
  effective: AgentEffectiveConfiguration | null;
  personalIds: ReadonlySet<string>;
  noneLabel: string;
  noneHint: string;
  deleteLabel: (name: string) => string;
  deleting: ReadonlySet<string>;
  onDelete: (id: string) => void;
}): TableRow[] {
  return [
    ...(catalog?.skills ?? []).map((skill) => ({
      key: skill.id,
      disabled: !effective?.skills.some((item) => item.id === skill.id),
      cells: [
        { text: skill.displayName, strong: true, kind: 'name' as const },
        { text: skill.prompt.replace(/\s+/g, ' '), muted: true },
        { text: skill.recommendedSize || '—', muted: true },
      ],
      action: personalIds.has(skill.id)
        ? { label: deleteLabel(skill.displayName), onClick: () => onDelete(skill.id), busy: deleting.has(skill.id), testId: 'skill-delete' }
        : undefined,
    })),
    { key: NO_SKILL, cells: [{ text: noneLabel, strong: true, kind: 'name' as const }, { text: noneHint, muted: true }, { text: '—', muted: true }] },
  ];
}

export interface SkillPickerProps {
  sessionId: string | null;
  catalog: ChatSkillCatalog | null;
  effective: AgentEffectiveConfiguration | null;
  selectedId: string | null;
  unavailable: boolean;
  loading: boolean;
  failed: boolean;
  disabled: boolean;
  onSelect: (id: string | null) => void;
  onCreated: (id: string) => void;
  /** 本人删除了一个个人 Skill（#238）；调用方刷新目录并把指向它的选择改为“不使用”。 */
  onDeleted?: (id: string) => void;
}

/** Skill 选择：与模型选择同样的小表格；Skill 不可用于当前模型时该行变暗。 */
export function SkillPicker({
  sessionId, catalog, effective, selectedId, unavailable, loading, failed, disabled, onSelect, onCreated, onDeleted,
}: SkillPickerProps) {
  const { schema } = useProductSchema();
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<ReadonlySet<string>>(() => new Set());
  // Deleted here: hidden at once, before the refreshed catalog arrives (#238).
  const [removed, setRemoved] = useState<ReadonlySet<string>>(() => new Set());
  // Set when a delete succeeded while the keyboard was in the list; consumed once the row has left the page.
  const refocus = useRef(false);
  // 只有本人的个人 Skill 可删；系统与共享 Skill 不出现删除按钮（#238）。
  const canDelete = Boolean(onDeleted && schema.chat.skills.creation.item_route);
  const personal = useSWR(
    canDelete ? ['personal-skills', schema.chat.skills.creation.route, catalog?.revision ?? ''] : null,
    () => listPersonalSkillIds(schema),
    // The key follows the catalog revision; keep the old ids while the new revision loads, or every
    // personal row would lose its trash button for a moment and remount, dropping focus (#238).
    { revalidateOnFocus: false, keepPreviousData: true },
  );
  const personalIds = new Set(personal.data ?? []);
  const remove = async (id: string) => {
    setDeleting((current) => new Set(current).add(id));
    try {
      await deletePersonalSkill(schema, id);
      await personal.mutate((ids) => (ids ?? []).filter((item) => item !== id), { revalidate: false });
      refocus.current = true;
      setRemoved((current) => new Set(current).add(id));
      onDeleted?.(id);
    } catch (cause) {
      toast.error(withErrorReference(schemaText(schema, 'ui.workspace.skills.delete_failed'), cause));
    } finally {
      setDeleting((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  };
  // The deleted row took the focused trash button with it (and a busy, disabled button may already have
  // dropped focus to the body): keep the keyboard in the list instead of the page (#238). Runs after the
  // render that removed the row, so the list it focuses is the one on screen.
  useEffect(() => {
    if (!refocus.current) return;
    refocus.current = false;
    const panel = document.querySelector<HTMLElement>('[data-testid="workspace-skill-picker-panel"]');
    if (!panel || !focusWasLost(document.activeElement, panel)) return;
    // The first row can be disabled (三视图 on a 3D stage); focus() on it does nothing, so take the first usable
    // row ("不使用" always is).
    panel.querySelector<HTMLElement>('[role="menuitemradio"]:not([disabled])')?.focus();
  }, [removed]);
  const selected = catalog?.skills.find((skill) => skill.id === selectedId);
  const resolvedSelection = selected && !loading && !failed && !unavailable ? selected : null;
  const value = resolvedSelection
    ? formatSchemaText(schemaText(schema, 'ui.studio.skill.selected'), { name: resolvedSelection.displayName })
    : schemaText(schema, 'ui.studio.skill.short');
  const fullValue = resolvedSelection?.displayName ?? schemaText(schema, 'ui.studio.skill.label');

  const visibleCatalog = catalog && removed.size > 0
    ? { ...catalog, skills: catalog.skills.filter((skill) => !removed.has(skill.id)) }
    : catalog;
  const rows = skillPickerRows({
    catalog: visibleCatalog, effective, personalIds: canDelete ? personalIds : new Set(),
    noneLabel: schemaText(schema, 'ui.studio.skill.none'),
    noneHint: schemaText(schema, 'ui.studio.skill.none_hint'),
    deleteLabel: (name) => formatSchemaText(schemaText(schema, 'ui.workspace.skills.delete_label'), { name }),
    deleting, onDelete: (id) => { void remove(id); },
  });

  return (
    <>
      <TablePopover
        testId="workspace-skill-picker"
        label={schemaText(schema, 'ui.studio.skill.title')}
        value={value}
        fullValue={fullValue}
        disabled={disabled || !sessionId}
        sections={[{
          title: schemaText(schema, 'ui.studio.skill.title'),
          hint: schemaText(schema, 'ui.studio.skill.hint'),
          head: [schemaText(schema, 'ui.studio.skill.col.name'), schemaText(schema, 'ui.studio.skill.col.prompt'), schemaText(schema, 'ui.studio.skill.col.size')],
          columns: 'minmax(0,1.2fr) minmax(0,2fr) 40px',
          rows,
          selectedKey: selectedId ?? NO_SKILL,
          loading,
          loadingText: schemaText(schema, 'ui.workspace.skills.loading'),
          failedText: failed ? schemaText(schema, 'ui.workspace.skills.load_failed') : undefined,
          emptyText: schemaText(schema, 'ui.workspace.skills.empty'),
          onPick: (key) => onSelect(key === NO_SKILL ? null : key),
          foot: (close) => (
            <div className="flex flex-col gap-1">
              {selectedId && !loading && (failed || unavailable)
                ? <p role="alert" className="text-destructive">{schemaText(schema, failed ? 'ui.workspace.skills.load_failed' : 'ui.workspace.skills.unavailable')}</p>
                : null}
              <button
                type="button"
                onClick={() => { close(); setCreating(true); }}
                className="flex h-6 items-center gap-1 self-start rounded-md px-1.5 text-[11px] text-muted-foreground hover:bg-panel-raised hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
              >
                <Plus aria-hidden className="size-3" />
                {schemaText(schema, 'ui.workspace.skills.create')}
              </button>
            </div>
          ),
        }]}
      />
      <SkillCreator
        sessionId={sessionId}
        disabled={disabled}
        onCreated={onCreated}
        control={{ open: creating, onOpenChange: setCreating }}
      />
    </>
  );
}

/** Focus that no control holds: nothing, the page body or root, or the picker panel's own container. */
export function focusWasLost(active: Element | null, panel: Element): boolean {
  return !active || active === document.body || active === document.documentElement || active === panel;
}
