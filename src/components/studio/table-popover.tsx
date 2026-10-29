'use client';

import { useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { Popover } from '@base-ui/react/popover';
import { Check, ChevronDown, Trash2 } from 'lucide-react';

import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import { cn } from '@/lib/utils';

/**
 * Latin letters of the composer's pickers read in capitals through CSS only (#108): the Schema text, the data,
 * the accessible names and the values sent stay as they are, and Chinese is unaffected. Shared by the trigger
 * frame, the option cells that carry names and the context meter.
 */
export const PICKER_UPPERCASE_CLASS = 'uppercase tracking-wide';

/** The trigger frame the composer's pickers share (also used by the parameters button). */
export const PICKER_TRIGGER_CLASS = `flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md border border-input bg-secondary px-2 text-xs text-foreground transition-colors hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 data-popup-open:border-primary data-popup-open:bg-primary/15 data-popup-open:text-primary ${PICKER_UPPERCASE_CLASS}`;

export interface TableCell {
  text: string;
  /** 暗淡的单元格：不支持的能力、供应商名等次要信息。 */
  muted?: boolean;
  /** 紧跟文字的小标签，如"推荐""未启用"。 */
  tag?: string;
  /** 标签的语气：reminder 用橙色底提醒（如"推荐"），缺省为灰色。 */
  tagTone?: 'reminder';
  strong?: boolean;
  /** 名称类单元格：拉丁字母用 CSS 显示为大写（#108）；模型名列不用。 */
  upper?: boolean;
}

export interface TableRow {
  key: string;
  cells: TableCell[];
  disabled?: boolean;
  /**
   * 行尾的独立操作（如删除，#238）。它是行按钮旁的兄弟按钮，不嵌在行里：
   * 点击不会选中该行，键盘可单独聚焦。
   */
  action?: { label: string; onClick: () => void; busy?: boolean; testId?: string };
}

/** 弹层里的一张小表格：一组单选行。模型选择把 2D 和 3D 两张并排放，Skill 和 Agent 模式各一张。 */
export interface TableSection {
  title: string;
  hint?: string;
  head: string[];
  /** CSS grid-template-columns；最后一列（选中标记）由组件追加。 */
  columns: string;
  rows: TableRow[];
  selectedKey: string | null;
  foot?: ReactNode | ((close: () => void) => ReactNode);
  emptyText?: string;
  loadingText?: string;
  loading?: boolean;
  failedText?: string;
  /** 整张表停用时的说明（写明为什么不能用）；此时所有行变暗且不可点。 */
  disabledReason?: string;
  testId?: string;
  onPick: (key: string) => void;
}

export interface TablePopoverProps {
  /** 触发按钮的无障碍名称与悬停提示。 */
  label: string;
  /** 触发按钮上的当前值。 */
  value: string;
  /**
   * 触发按钮上分段显示的值（如 2D、3D 两个模型名，#324）：每段各自截断，前一段至多占一半宽，
   * 所以一个长名字不会把后一段整个挤掉。缺省时整体显示 value。
   */
  valueParts?: { parts: string[]; separator: string };
  /** 悬停提示与无障碍名称里的完整值（触发按钮只显示简称时用）；缺省同 value。 */
  fullValue?: string;
  icon?: ReactNode;
  disabled?: boolean;
  /** 悬停提示里名称下的一行说明（如思考强度的“档位越高…”），也作为触发按钮的无障碍描述。 */
  hint?: string;
  /** 触发按钮为什么停用；停用时写在悬停提示第一行，也作为无障碍描述。 */
  triggerDisabledReason?: string;
  testId?: string;
  /** 一张表时选中即关闭；多张表并排时弹层保持打开，方便每张各选一项。 */
  sections: TableSection[];
}

/**
 * 紧凑的"小表格"锚定弹层：每行一项，列出模型 / Skill 的关键差异，选中项有勾，
 * 不可用的行变暗且不可点。模型选择（2D、3D 两列）、Skill 选择和 Agent 模式共用。
 */
export function TablePopover({
  label, value, valueParts, fullValue, icon, disabled = false, hint, triggerDisabledReason, testId, sections,
}: TablePopoverProps) {
  const layer = useAnchoredPositioner();
  const descriptionId = useId();
  const reason = disabled ? triggerDisabledReason : undefined;
  const description = [reason, hint].filter(Boolean).join(' ');
  const [open, setOpen] = useState(false);
  // Closing the panel (Esc, a pick, a click outside) hands focus back to the trigger. Base UI treats that
  // focus like keyboard focus and opened the hint above the trigger, over the neighbouring pickers, where
  // it stayed until something else took focus (#324, #322 note 99059). The hint stays closed from the
  // moment the panel opens until the pointer enters or leaves the trigger or focus moves on; it is passive,
  // so even when shown it never takes a click meant for the controls beside it.
  const [hintHeld, setHintHeld] = useState(false);
  const openRef = useRef(false);
  const changeOpen = (next: boolean) => {
    openRef.current = next;
    setOpen(next);
    if (next) setHintHeld(true);
  };
  const releaseHint = () => { if (!openRef.current) setHintHeld(false); };
  const close = () => changeOpen(false);
  const paired = sections.length > 1;

  return (
    <Popover.Root open={open} onOpenChange={changeOpen}>
      {/* The trigger fills the cell its parent gives it and cuts a long name; hovering shows the whole name. */}
      <HintTooltip
        name={`${label}: ${fullValue ?? value}`} hint={hint} reason={reason} className="w-full min-w-0"
        suppressed={open || hintHeld} passive
        triggerProps={{
          // A pointer that comes back after the panel closed hovers as usual.
          onPointerEnter: releaseHint,
          onPointerLeave: releaseHint,
          onBlur: releaseHint,
        }}
      >
        <Popover.Trigger
          data-testid={testId}
          disabled={disabled}
          aria-label={`${label}: ${fullValue ?? value}`}
          aria-describedby={description ? descriptionId : undefined}
          className={PICKER_TRIGGER_CLASS}
        >
          {icon}
          {valueParts && valueParts.parts.length === 2 ? (
            <span className="grid min-w-0 flex-1 grid-cols-[fit-content(50%)_auto_minmax(0,1fr)] text-left" data-testid={testId ? `${testId}-value` : undefined}>
              <span className="truncate" data-part="0">{valueParts.parts[0]}</span>
              <span aria-hidden className="whitespace-pre text-muted-foreground">{valueParts.separator}</span>
              <span className="truncate" data-part="1">{valueParts.parts[1]}</span>
            </span>
          ) : <span className="flex-1 truncate text-left">{value}</span>}
          <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        </Popover.Trigger>
        {description ? <span id={descriptionId} className="sr-only">{description}</span> : null}
      </HintTooltip>
      <Popover.Portal>
        <Popover.Positioner side="top" align="start" sideOffset={8} collisionPadding={12} className={layer}>
          <Popover.Popup
            data-testid={testId ? `${testId}-panel` : undefined}
            aria-label={label}
            className={cn(
              '@container flex max-h-(--available-height) max-w-(--available-width) flex-col overflow-y-auto rounded-lg bg-popover p-1.5 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10',
              paired ? 'w-[59rem]' : 'w-[36rem]',
            )}
          >
            {/* Side by side when the panel is wide enough, otherwise one table under the other. */}
            <div className={cn('grid gap-x-3 gap-y-2', paired && '@[52rem]:grid-cols-2')}>
              {sections.map((section) => (
                <Section key={section.testId ?? section.title} section={section} close={close} closeOnPick={!paired} />
              ))}
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
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

function Section({ section, close, closeOnPick }: { section: TableSection; close: () => void; closeOnPick: boolean }) {
  const {
    title, hint, head, columns, rows, selectedKey, foot, emptyText, loadingText, loading = false, failedText,
    disabledReason, testId, onPick,
  } = section;
  const items = useRef<Array<HTMLButtonElement | null>>([]);
  const hasActions = rows.some((row) => row.action);
  // 有行尾操作时多留一列给它（操作按钮浮在这一列上），勾选标记仍在最右。
  const template = hasActions ? `${columns} 20px 16px` : `${columns} 16px`;
  const off = Boolean(disabledReason);

  const navigate = (event: KeyboardEvent, index: number) => {
    const enabled = rows.map((row, position) => (row.disabled ? -1 : position)).filter((position) => position >= 0);
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
    <div className="flex min-w-0 flex-col" data-testid={testId}>
      <div className="flex items-center justify-between gap-2 px-2 pb-1.5 pt-0.5">
        <span className="text-xs font-semibold">{title}</span>
        {off
          ? <span role="note" className="truncate text-[11px] text-muted-foreground">{disabledReason}</span>
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
          return (
            <div key={row.key} className="relative">
            <button
              ref={(node) => { items.current[index] = node; }}
              type="button"
              role="menuitemradio"
              aria-checked={selected}
              disabled={off || row.disabled}
              onKeyDown={(event) => navigate(event, index)}
              onClick={() => { onPick(row.key); if (closeOnPick) close(); }}
              style={{ gridTemplateColumns: template }}
              className={pickerRowClass(selected)}
            >
              {row.cells.map((cell, position) => (
                cell.tag ? (
                  <span
                    key={position}
                    className={cn('flex min-w-0 items-center', cell.muted && 'text-muted-foreground', cell.strong && 'font-medium', cell.upper && PICKER_UPPERCASE_CLASS)}
                  >
                    <span className="min-w-0 truncate">{cell.text}</span>
                    <span className={cn('ml-1.5 shrink-0 whitespace-nowrap rounded-sm px-1 text-[10px] leading-3.5', cell.tagTone === 'reminder' ? 'bg-reminder font-medium text-reminder-foreground' : 'bg-panel-raised text-muted-foreground')}>{cell.tag}</span>
                  </span>
                ) : (
                  <span
                    key={position}
                    className={cn('truncate', cell.muted && 'text-muted-foreground', cell.strong && 'font-medium', cell.upper && PICKER_UPPERCASE_CLASS)}
                  >
                    {cell.text}
                  </span>
                )
              ))}
              {hasActions ? <span aria-hidden /> : null}
              <span className="flex text-primary">{selected ? <Check aria-hidden className="size-3.5" /> : null}</span>
            </button>
            {row.action ? (
              <HintTooltip name={row.action.label} className="absolute right-8 top-1/2 -translate-y-1/2">
                <button
                  type="button"
                  aria-label={row.action.label}
                  disabled={off || row.action.busy}
                  onClick={row.action.onClick}
                  data-testid={row.action.testId}
                  className="flex size-5 items-center justify-center rounded-sm text-muted-foreground transition-colors duration-150 hover:bg-destructive/15 hover:text-destructive focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-40"
                >
                  <Trash2 aria-hidden className="size-3.5" />
                </button>
              </HintTooltip>
            ) : null}
            </div>
          );
        })}
      </div>
      {foot ? <div className="mt-1 border-t border-border px-2 pb-0.5 pt-1.5 text-[11px] text-muted-foreground">{typeof foot === 'function' ? foot(close) : foot}</div> : null}
    </div>
  );
}
