'use client';

import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { Check, Pencil, Trash2 } from 'lucide-react';

import { HintTooltip } from '@/components/ui/hint-tooltip';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';
import { cn } from '@/lib/utils';

/**
 * Latin letters of the composer's pickers read in capitals through CSS only (#108): the Schema text, the data,
 * the accessible names and the values sent stay as they are, and Chinese is unaffected. Shared by the option cells
 * that carry names and the context meter under the composer.
 */
export const PICKER_UPPERCASE_CLASS = 'uppercase tracking-wide';

export interface TableCell {
  text: string;
  /** 暗淡的单元格：不支持的能力、供应商名等次要信息。 */
  muted?: boolean;
  /** 紧跟文字的小标签，如"推荐""未启用"。 */
  tag?: string;
  /** 标签的语气：reminder 用橙色底提醒（如"推荐"），缺省为灰色。 */
  tagTone?: 'reminder';
  strong?: boolean;
  /**
   * 单元格的语义。`name` 是名称类（Skill、Agent 范围、对话模型、思考档位）：拉丁字母用 CSS 显示为大写（#108），
   * 是否大写由表格统一按这一语义决定，调用方不再各写样式；模型名列不标 `name`，保持原样。
   */
  kind?: 'name';
}

export interface TableRowAction {
  label: string;
  onClick: () => void;
  busy?: boolean;
  testId?: string;
  /** 行尾按钮的图标；缺省为删除。 */
  kind?: 'edit' | 'delete';
}

export interface TableRow {
  key: string;
  cells: TableCell[];
  disabled?: boolean;
  /** 行停用时的短原因（#94）：写在该行的悬停提示第一行（「无法使用，…」）；缺省则无提示。 */
  disabledReason?: string;
  /** Let keyboard users focus an unavailable row to read its reason, without allowing selection. */
  focusableDisabled?: boolean;
  /**
   * 行尾的独立操作（如删除，#238）。它是行按钮旁的兄弟按钮，不嵌在行里：
   * 点击不会选中该行，键盘可单独聚焦。
   */
  action?: TableRowAction;
  /** 行尾的多个独立操作（如编辑 + 删除）；给出时覆盖 action。 */
  actions?: TableRowAction[];
}

/** 设置页里的一张小表格：一组单选行。模型选择把 2D 和 3D 两张并排放，Skill 和对话模型各占一页。 */
export interface TableSection {
  title: string;
  hint?: string;
  head: string[];
  /** CSS grid-template-columns；最后一列（选中标记）由组件追加。 */
  columns: string;
  rows: TableRow[];
  selectedKey: string | null;
  foot?: ReactNode;
  emptyText?: string;
  loadingText?: string;
  loading?: boolean;
  failedText?: string;
  /**
   * 整张表停用时的完整说明（写明为什么不能用，只在悬停提示里）；此时所有行变暗且不可点。
   * 页面上只显示 `disabledLabel`（「无法使用，<短原因>」，#94）；缺省时整句说明就是那一行。
   */
  disabledReason?: string;
  disabledLabel?: string;
  testId?: string;
  onPick: (key: string) => void;
}

export interface TableSectionsProps {
  /** 一张表时占满一栏；多张表并排（容器够宽时）或上下排。 */
  sections: TableSection[];
  /**
   * 一个页里的两步选择（#223，对话模型与思考档位）：两张表左右两栏，左右方向键在两栏之间移动焦点
   * （落到该栏当前选中行，没有则第一行）；容器放不下两栏时退化为上下堆叠。
   */
  columns?: boolean;
  /** 整个内容区的 test id（原来各弹层的 `…-panel`）。 */
  testId?: string;
  className?: string;
}

/**
 * 紧凑的"小表格"：每行一项，列出模型 / Skill 的关键差异，选中项有勾，不可用的行变暗且不可点。
 * 它是设置页（#405）各页的内容区，原先输入区的四个选择器弹层里装的就是它：模型选择（2D、3D 两张）、
 * Skill、对话模型与思考档位共用。选择即时生效，不收起任何东西；列数随内容区宽度（容器查询）而变。
 */
export function TableSections({ sections, columns = false, testId, className }: TableSectionsProps) {
  const paired = sections.length > 1;
  const sideBySide = paired && !columns;
  const twoColumns = paired && columns;
  const sectionsRef = useRef<HTMLDivElement>(null);
  // #223: move focus into the neighbouring column, onto its current choice or else its first usable row.
  const focusSection = (index: number): boolean => {
    const root = sectionsRef.current?.querySelector(`[data-picker-section="${index}"]`);
    const rows = Array.from(root?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []).filter((row) => !row.disabled);
    const target = rows.find((row) => row.getAttribute('aria-checked') === 'true') ?? rows[0];
    target?.focus();
    return Boolean(target);
  };

  return (
    <div data-testid={testId} data-layout={twoColumns ? 'columns' : undefined} className={cn('@container min-w-0', className)}>
      {/* Side by side when the content is wide enough, otherwise one table under the other. */}
      <div ref={sectionsRef} className={cn('grid gap-x-3 gap-y-2', sideBySide && '@[52rem]:grid-cols-2', twoColumns && '@[30rem]:grid-cols-2')}>
        {sections.map((section, index) => (
          <Section key={section.testId ?? section.title} section={section} index={index}
            onCross={twoColumns ? (direction) => focusSection(index + direction) : undefined} />
        ))}
      </div>
    </div>
  );
}

/**
 * A picker row's classes. The selected row keeps its tint, inset accent and check mark while
 * hovered or focused (#236), so hover never makes it look like any other row.
 */
export function pickerRowClass(selected: boolean): string {
  return cn(
    'grid h-7.5 w-full items-center gap-2 rounded-md px-2 text-left text-xs tabular-nums transition-colors duration-150 hover:bg-panel-raised focus-visible:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent',
    selected && 'bg-primary/15 ring-1 ring-inset ring-primary/45 hover:bg-primary/20 focus-visible:bg-primary/20',
  );
}

function Section({ section, index, onCross }: {
  section: TableSection;
  index: number;
  /** Two-column layout only: the left and right arrows move focus to the neighbouring column. */
  onCross?: (direction: -1 | 1) => boolean;
}) {
  const {
    title, hint, head, columns, rows, selectedKey, foot, emptyText, loadingText, loading = false, failedText,
    disabledReason, disabledLabel, testId, onPick,
  } = section;
  const items = useRef<Array<HTMLButtonElement | null>>([]);
  const actionsOf = (row: TableRow) => row.actions ?? (row.action ? [row.action] : []);
  const actionCount = Math.max(0, ...rows.map((row) => actionsOf(row).length));
  // 有行尾操作时多留一列给它（操作按钮浮在这一列上），勾选标记仍在最右。
  // 多个操作各占 20px，间距 2px；没有操作的行保持原有宽度。
  const actionWidth = actionCount > 0 ? actionCount * 20 + (actionCount - 1) * 2 : 0;
  const template = actionCount > 0 ? `${columns} ${actionWidth}px 16px` : `${columns} 16px`;
  const off = Boolean(disabledReason);

  const navigate = (event: KeyboardEvent, index: number) => {
    if (onCross && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
      event.preventDefault();
      onCross(event.key === 'ArrowLeft' ? -1 : 1);
      return;
    }
    const enabled = rows.map((row, position) => (row.disabled && !row.focusableDisabled ? -1 : position)).filter((position) => position >= 0);
    if (enabled.length === 0) return;
    const at = enabled.indexOf(index);
    let next: number | undefined;
    if (event.key === 'ArrowDown') next = enabled[(at + 1) % enabled.length];
    else if (event.key === 'ArrowUp') next = enabled[(at <= 0 ? enabled.length : at) - 1];
    else if (event.key === 'Home') next = enabled[0];
    else if (event.key === 'End') next = enabled[enabled.length - 1];
    if (next === undefined) return;
    event.preventDefault();
    items.current[next]?.focus();
  };

  return (
    <div className="flex min-w-0 flex-col" data-testid={testId} data-picker-section={index}>
      <div className="flex items-center justify-between gap-2 px-2 pb-1.5 pt-0.5">
        <span className="text-xs font-semibold">{title}</span>
        {off
          ? (
            <UnavailableNotice
              label={disabledLabel ?? disabledReason ?? ''}
              detail={disabledLabel ? disabledReason : undefined}
              testId={testId ? `${testId}-unavailable` : undefined}
              className="text-[11px]"
            />
          )
          : hint ? <span className="text-[11px] text-muted-foreground">{hint}</span> : null}
      </div>
      <div
        className="sticky top-0 grid h-6.5 items-center gap-2 border-b border-border bg-popover px-2 text-[11px] text-muted-foreground"
        style={{ gridTemplateColumns: template }}
        aria-hidden="true"
      >
        {head.map((cell) => <span key={cell} className="truncate">{cell}</span>)}
        <span />
      </div>
      <div role="menu" aria-label={title} className="pt-0.5">
        {loading ? (
          <p role="status" className="px-2 py-4 text-xs text-muted-foreground">{loadingText}</p>
        ) : failedText ? (
          <p role="alert" className="px-2 py-4 text-xs text-destructive">{failedText}</p>
        ) : rows.length === 0 ? (
          <p role="status" className="px-2 py-4 text-xs text-muted-foreground">{emptyText}</p>
        ) : rows.map((row, index) => {
          const selected = row.key === selectedKey;
          const rowButton = (
            <button
              ref={(node) => { items.current[index] = node; }}
              type="button"
              role="menuitemradio"
              aria-checked={selected}
              aria-disabled={row.disabled && row.focusableDisabled ? true : undefined}
              aria-label={row.disabled && row.focusableDisabled ? `${row.cells[0]?.text ?? ''}: ${row.disabledReason ?? ''}` : undefined}
              disabled={off || (row.disabled && !row.focusableDisabled)}
              onKeyDown={(event) => navigate(event, index)}
              onClick={() => { if (row.disabled) return; onPick(row.key); }}
              style={{ gridTemplateColumns: template }}
              className={cn(pickerRowClass(selected), row.disabled && row.focusableDisabled && 'cursor-not-allowed opacity-40 hover:bg-transparent')}
            >
              {row.cells.map((cell, position) => (
                cell.tag ? (
                  <span
                    key={position}
                    className={cn('flex min-w-0 items-center', cell.muted && 'text-muted-foreground', cell.strong && 'font-medium', cell.kind === 'name' && PICKER_UPPERCASE_CLASS)}
                  >
                    <span className="min-w-0 truncate">{cell.text}</span>
                    <span className={cn('ml-1.5 shrink-0 whitespace-nowrap rounded-sm px-1 text-[10px] leading-3.5', cell.tagTone === 'reminder' ? 'bg-reminder font-medium text-reminder-foreground' : 'bg-panel-raised text-muted-foreground')}>{cell.tag}</span>
                  </span>
                ) : (
                  <span
                    key={position}
                    className={cn('truncate', cell.muted && 'text-muted-foreground', cell.strong && 'font-medium', cell.kind === 'name' && PICKER_UPPERCASE_CLASS)}
                  >
                    {cell.text}
                  </span>
                )
              ))}
              {actionCount > 0 ? <span aria-hidden /> : null}
              <span className="flex text-primary">{selected ? <Check aria-hidden className="size-3.5" /> : null}</span>
            </button>
          );
          const actions = actionsOf(row);
          return (
            <div key={row.key} className="relative">
            {row.disabled && row.disabledReason && !off ? (
              <HintTooltip name={row.cells[0]?.text ?? ''} reason={row.disabledReason} className="w-full min-w-0">{rowButton}</HintTooltip>
            ) : rowButton}
            {actions.length ? (
              <div className="absolute right-8 top-1/2 flex -translate-y-1/2 items-center gap-0.5">
                {actions.map((action) => (
                  <HintTooltip key={action.label} name={action.label}>
                    <button
                      type="button"
                      aria-label={action.label}
                      disabled={off || action.busy}
                      onClick={action.onClick}
                      data-testid={action.testId}
                      className="flex size-5 items-center justify-center rounded-sm text-muted-foreground transition-colors duration-150 hover:bg-destructive/15 hover:text-destructive focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40"
                    >
                      {action.kind === 'edit' ? <Pencil aria-hidden className="size-3.5" /> : <Trash2 aria-hidden className="size-3.5" />}
                    </button>
                  </HintTooltip>
                ))}
              </div>
            ) : null}
            </div>
          );
        })}
      </div>
      {foot ? <div className="mt-1 border-t border-border px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground">{foot}</div> : null}
    </div>
  );
}
