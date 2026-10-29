'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Coins } from 'lucide-react';
import { Popover } from '@base-ui/react/popover';

import { useAnchoredPositioner } from '@/components/ui/overlay';
import { useProductSchema } from '@/components/product-schema-provider';
import { useBalance, type ProviderBalanceAccountView } from '@/hooks/use-balance';
import { formatBalanceAmount } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatPriceTotal } from '@/lib/pricing';
import { formatSchemaText } from '@/lib/product-schema';

export function knownBalances(accounts: ReadonlyArray<ProviderBalanceAccountView>) {
  return accounts
    .filter((account) => typeof account.balance === 'number' && Number.isFinite(account.balance))
    .sort((a, b) => (a.balance ?? 0) - (b.balance ?? 0));
}

export function WorkspaceBalance() {
  const { schema, text } = useProductSchema();
  const { accounts } = useBalance();
  const known = useMemo(() => knownBalances(accounts), [accounts]);
  const minimum = known[0];
  const native = accounts.find((account) => account.priceMode === 'native');
  const [open, setOpen] = useState(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Base UI may return focus to the trigger after Escape or an outside press.
  // That focus belongs to dismissal, not a new request to open the popover.
  const dismissFocusReturn = useRef(false);
  const layer = useAnchoredPositioner();
  useEffect(() => {
    // A later Tab is a new keyboard navigation, including when an outside
    // press moved focus elsewhere without ever blurring the trigger.
    const onTab = (event: KeyboardEvent) => {
      if (event.key === 'Tab') dismissFocusReturn.current = false;
    };
    document.addEventListener('keydown', onTab, true);
    return () => {
      document.removeEventListener('keydown', onTab, true);
      if (closeTimer.current) clearTimeout(closeTimer.current);
    };
  }, []);
  const keepOpen = () => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = null;
    dismissFocusReturn.current = false;
    setOpen(true);
  };
  const closeLater = () => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(() => setOpen(false), 120);
  };
  const points = text('ui.workspace.balance.points');
  const unavailable = text('ui.workspace.balance.unavailable');
  const nativeUnknown = text('ui.pricing.balance_unknown');
  const locale = activeLocale(schema);
  const format = (amount: number) => formatBalanceAmount(amount, locale);
  const labelValue = (label: string | null, value: string) => formatSchemaText(text('ui.common.label_value'), { label: label ?? '', value });
  const summary = minimum
    ? labelValue(minimum.label, `${format(minimum.balance!)}${points}`)
    : native
      ? labelValue(native.label, (native.nativeBalance ? formatPriceTotal(native.nativeBalance) : null) ?? nativeUnknown)
      : unavailable;

  return <Popover.Root open={open} onOpenChange={(nextOpen) => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = null;
    dismissFocusReturn.current = !nextOpen;
    setOpen(nextOpen);
  }}>
    <Popover.Trigger
      type="button"
      onPointerEnter={(event) => { if (event.pointerType === 'mouse') keepOpen(); }}
      onPointerLeave={(event) => { if (event.pointerType === 'mouse') closeLater(); }}
      onFocus={(event) => {
        if (!dismissFocusReturn.current && event.currentTarget.matches(':focus-visible')) keepOpen();
      }}
      onBlur={() => { dismissFocusReturn.current = false; }}
      className="flex h-9 min-w-0 max-w-[108px] shrink-0 items-center gap-1 rounded-lg px-1 text-[11px] text-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary sm:max-w-44 sm:px-2 sm:text-xs"
      aria-label={summary}
    >
      <Coins className="size-3.5 shrink-0 text-warning" aria-hidden="true" />
      <span className="min-w-0 truncate whitespace-nowrap tabular-nums">
        {summary}
      </span>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Positioner side="bottom" align="end" sideOffset={8} className={layer}>
        <Popover.Popup
          onPointerEnter={(event) => { if (event.pointerType === 'mouse') keepOpen(); }}
          onPointerLeave={(event) => { if (event.pointerType === 'mouse') closeLater(); }}
          className="max-h-[min(60dvh,320px)] min-w-44 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-xl bg-popover p-2 text-popover-foreground shadow-lg ring-1 ring-foreground/10 outline-none"
        >
          <Popover.Title className="px-2 py-1 text-xs font-medium text-muted-foreground">
            {text('ui.dashboard.balance')}
          </Popover.Title>
          {accounts.length ? accounts.map((account) => <div key={account.provider} className="flex items-center justify-between gap-4 rounded-md px-2 py-1.5 text-xs">
            <span className="truncate">{account.label}</span>
            <span className="shrink-0 tabular-nums">{account.priceMode === 'native'
              ? account.nativeBalance ? formatPriceTotal(account.nativeBalance) : nativeUnknown
              : typeof account.balance === 'number' ? `${format(account.balance)}${points}` : unavailable}</span>
          </div>) : <p className="px-2 py-1.5 text-xs text-muted-foreground">{unavailable}</p>}
        </Popover.Popup>
      </Popover.Positioner>
    </Popover.Portal>
  </Popover.Root>;
}
