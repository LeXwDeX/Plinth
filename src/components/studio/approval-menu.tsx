'use client';

import { useMemo, useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { Check, ChevronDown, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import { approvalModeOptions, sessionCreditsText, sessionPriceTotalsText } from '@/lib/chat/approval-modes';
import { useSessionApproval } from '@/hooks/use-session-approval';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { useChatStore, type ApprovalMode } from '@/store/chat-store';

/**
 * 批准方式下拉：手工 / 自动，写明两者的区别。切到自动要先确认（之后的付费步骤
 * 不再询问）；切换只影响之后的步骤，已经在等待的批准不会因此被通过。
 */
export function ApprovalMenu() {
  const { schema } = useProductSchema();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const approval = useSessionApproval(chat, activeSessionId, schema.chat.approval_policy.default_mode === 'auto' ? 'auto' : 'manual');
  const options = useMemo(() => approvalModeOptions(schema), [schema]);
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState<ApprovalMode | null>(null);
  const layer = useAnchoredPositioner();
  const current = options.find((option) => option.mode === approval.mode);
  // The steps manual mode asks about are the Schema's, not a list typed here.
  const askedAbout = schema.chat.approval_policy.manual_approval_capabilities
    .map((id) => schema.capabilities.find((capability) => capability.id === id)?.label_cn ?? id)
    .join(schemaText(schema, 'ui.studio.list_separator'));
  if (!activeSessionId || options.length === 0) return null;

  const change = async (mode: ApprovalMode) => {
    if (!(await approval.setMode(mode))) toast.error(schemaText(schema, 'ui.workspace.approval.change_failed'));
    else setConfirming(null);
  };
  const choose = (mode: ApprovalMode) => {
    if (mode === approval.mode) { setConfirming(null); return; }
    // Auto removes the question for later paid steps, so it asks first.
    if (mode === 'auto') { setConfirming(mode); return; }
    void change(mode);
  };

  return (
    <>
      <Popover.Root open={open} onOpenChange={(next) => { setOpen(next); if (!next) setConfirming(null); }}>
        <HintTooltip name={schemaText(schema, 'ui.workspace.approval.label')} hint={current?.tooltip} className="shrink-0">
          <Popover.Trigger
            data-testid="studio-approval-menu"
            aria-label={`${schemaText(schema, 'ui.workspace.approval.label')}: ${current?.label ?? ''}`}
            className="flex h-8 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:bg-primary/15 data-popup-open:text-primary"
          >
            <ShieldCheck aria-hidden className="size-3.5" />
            <span>{schemaText(schema, 'ui.workspace.approval.label')}</span>
            <span className={cn('font-medium', approval.mode === 'auto' ? 'text-warning' : 'text-foreground')}>{current?.label}</span>
            <ChevronDown aria-hidden className="size-3" />
          </Popover.Trigger>
        </HintTooltip>
        <Popover.Portal>
          <Popover.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={12} className={layer}>
            <Popover.Popup className="flex w-80 max-w-(--available-width) flex-col gap-1 rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10">
              <Popover.Title className="px-1 pb-1 text-xs font-semibold">{schemaText(schema, 'ui.studio.approval.title')}</Popover.Title>
              <div role="radiogroup" aria-label={schemaText(schema, 'ui.studio.approval.title')} className="flex flex-col gap-0.5">
                {options.map((option) => {
                  const selected = option.mode === approval.mode;
                  return (
                    <button
                      key={option.mode}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      disabled={approval.busy}
                      onClick={() => choose(option.mode)}
                      className={cn(
                        'flex items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50',
                        selected && 'bg-primary/15',
                      )}
                    >
                      <span className="mt-0.5 flex size-3.5 shrink-0 text-primary">{selected ? <Check aria-hidden className="size-3.5" /> : null}</span>
                      <span className="flex min-w-0 flex-col gap-0.5">
                        <span className="text-xs font-medium">{option.label}</span>
                        <span className="text-[11px] leading-snug text-muted-foreground">{option.tooltip}</span>
                      </span>
                    </button>
                  );
                })}
              </div>
              {confirming === 'auto' ? (
                <div role="alert" data-testid="studio-approval-confirm" className="mt-1 flex flex-col gap-2 rounded-md bg-warning/10 p-2 text-xs text-warning">
                  <span>{formatSchemaText(schemaText(schema, 'ui.studio.approval.confirm_auto'), { capabilities: askedAbout })}</span>
                  <span className="flex gap-1.5">
                    <Action type="button" size="xs" loading={approval.busy} onClick={() => void change('auto')}>{schemaText(schema, 'ui.studio.approval.confirm_action')}</Action>
                    <Action type="button" variant="ghost" size="xs" disabled={approval.busy} onClick={() => setConfirming(null)}>{schemaText(schema, 'ui.studio.approval.confirm_cancel')}</Action>
                  </span>
                </div>
              ) : null}
              <p className="px-1 pt-1 text-[11px] leading-snug text-muted-foreground">{schemaText(schema, 'ui.studio.approval.pending_note')}</p>
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
      <HintTooltip name={approval.priceTotals ? sessionPriceTotalsText(schema, approval.priceTotals) : sessionCreditsText(schema, approval.credits)} className="shrink-0">
        <span className="px-2 text-xs tabular-nums text-muted-foreground" data-testid="studio-session-credits">
          {approval.priceTotals
            ? sessionPriceTotalsText(schema, approval.priceTotals)
            : schemaCountMessage(schema, 'ui.studio.approval.credits', approval.credits, { credits: approval.credits })}
        </span>
      </HintTooltip>
    </>
  );
}
