'use client';

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import {
  AlertTriangle,
  Coins,
  Shield,
  TrendingDown,
  Users,
} from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';
import { Badge } from '@/components/ui/badge';
import { Surface } from '@/components/ui/surface';
import { SchemaDateField } from '@/components/form/schema-date-field';
import { dateRangeBounds } from '@/components/ui/date-field-math';
import { Field } from '@/components/ui/field';
import { Action } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { Skeleton } from '@/components/ui/skeleton';
import {
  AdministrationUsageBrowserError,
  createAdministrationUsageBrowserClient,
} from '@/lib/administration/usage-browser-client';
import { priceTotalsAmountsText, sessionPriceTotalsText } from '@/lib/chat/approval-modes';
import { schemaText } from '@/lib/product-schema';
import { formatPriceTotal } from '@/lib/pricing';
import { formatBalanceAmount } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';

interface DateRange {
  start: string;
  end: string;
}

function formatDate(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function defaultDateRange(days: number): DateRange {
  const end = new Date();
  const start = new Date();
  start.setDate(start.getDate() - days);
  return { start: formatDate(start), end: formatDate(end) };
}

function template(
  value: string,
  variables: Record<string, string>,
): string {
  let result = value;
  for (const [name, replacement] of Object.entries(variables)) {
    result = result.replaceAll(`{${name}}`, replacement);
  }
  return result;
}

export default function AdminPage() {
  const { schema } = useProductSchema();
  const usageSchema = schema.administration.usage;
  const presentation = usageSchema.presentation;
  const copy = (key: string): string =>
    presentation.text[key] ?? usageSchema.messages.internal_error;
  const [days, setDays] = useState(usageSchema.defaults.days);
  const [customRange, setCustomRange] = useState<DateRange | null>(null);
  const [showCustomPicker, setShowCustomPicker] = useState(false);
  const initialRange = defaultDateRange(
    usageSchema.defaults.custom_range_days,
  );
  const [draftStart, setDraftStart] = useState(initialRange.start);
  const [draftEnd, setDraftEnd] = useState(initialRange.end);
  const [rangeError, setRangeError] = useState<string | null>(null);
  const usageRoute = schema.runtime.http.legacy_routes.admin_usage;
  const balanceRoute =
    schema.runtime.http.legacy_routes.admin_balance_summary;
  const client = useMemo(
    () => createAdministrationUsageBrowserClient({
      schema: usageSchema,
      routes: {
        usage: usageRoute,
        balance: balanceRoute,
      },
    }),
    [balanceRoute, usageRoute, usageSchema],
  );
  const usageQuery = customRange
    ? {
      startDate: customRange.start,
      endDate: customRange.end,
    }
    : { days };
  const usageKey = client.usageURL(usageQuery);
  const {
    data: balance,
    isLoading: balanceLoading,
  } = useSWR(
    balanceRoute,
    () => client.getBalance(),
    { revalidateOnFocus: false },
  );
  const {
    data: usage,
    isLoading: usageLoading,
    error: usageError,
  } = useSWR(
    usageKey,
    () => client.usageOverview(usageQuery),
    { revalidateOnFocus: false },
  );
  const users = usage?.users ?? [];
  const rangeLabel = customRange
    ? [
      customRange.start,
      customRange.end,
    ].join(presentation.range_separator)
    : presentation.quick_ranges.find(
      (range) => range.days === days,
    )?.label ?? presentation.empty_placeholder;
  // The server adds every user up per unit (#327); a Provider-native quote no longer makes it unknown.
  const total = usage?.total ?? null;
  const totalText = total === null
    ? presentation.empty_placeholder
    : total.priceTotals !== undefined
      ? priceTotalsAmountsText(schema, total.priceTotals)
      : String(total.estimatedCredits);
  const totalTasks = users.reduce(
    (sum, user) => sum + user.totalTasks,
    0,
  );

  const selectQuickRange = (value: number) => {
    setDays(value);
    setCustomRange(null);
    setShowCustomPicker(false);
    setRangeError(null);
  };
  const showRangePicker = () => {
    setShowCustomPicker(true);
    setRangeError(null);
    const nextRange = defaultDateRange(
      usageSchema.defaults.custom_range_days,
    );
    setDraftStart(nextRange.start);
    setDraftEnd(nextRange.end);
  };
  const queryRange = () => {
    if (draftEnd < draftStart) {
      setRangeError(usageSchema.messages.invalid_range);
      return;
    }
    setRangeError(null);
    setCustomRange({ start: draftStart, end: draftEnd });
  };

  if (
    usageError instanceof AdministrationUsageBrowserError &&
    usageError.status === httpForbiddenStatus
  ) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Shield className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold">
          {copy('forbidden_title')}
        </h2>
        <p className="text-sm text-muted-foreground mt-1">
          {copy('forbidden_description')}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center gap-3">
              <Surface variant="warning" className="flex h-10 w-10 items-center justify-center">
                <Coins className="h-5 w-5 text-warning" />
              </Surface>
              <div>
                <p className="text-xs text-muted-foreground">
                  {copy('balance_label')}
                </p>
                {balanceLoading ? (
                  <Skeleton className="h-7 w-24 mt-0.5" />
                ) : (
                  <p className="text-2xl font-bold tabular-nums">
                    {balance?.priceMode === 'native'
                      ? balance.nativeBalance ? formatPriceTotal(balance.nativeBalance) : schemaText(schema, 'ui.pricing.balance_unknown')
                      : typeof balance?.currentBalance === 'number' ? formatBalanceAmount(balance.currentBalance, activeLocale(schema)) : presentation.empty_placeholder}
                  </p>
                )}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center gap-3">
              <Surface variant="info" className="flex h-10 w-10 items-center justify-center">
                <TrendingDown className="h-5 w-5 text-info" />
              </Surface>
              <div>
                <p className="text-xs text-muted-foreground">
                  {template(copy('usage_label'), { range: rangeLabel })}
                </p>
                {usageLoading ? (
                  <Skeleton className="h-7 w-24 mt-0.5" />
                ) : (
                  <p data-testid="admin-usage-total" className={total?.priceTotals !== undefined ? 'text-base font-bold tabular-nums break-words' : 'text-2xl font-bold tabular-nums'}>
                    {total === null ? totalText : `${presentation.approximation_prefix}${totalText}`}
                  </p>
                )}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center gap-3">
              <Surface variant="success" className="flex h-10 w-10 items-center justify-center">
                <Users className="h-5 w-5 text-success" />
              </Surface>
              <div>
                <p className="text-xs text-muted-foreground">
                  {template(copy('task_count_label'), {
                    range: rangeLabel,
                  })}
                </p>
                {usageLoading ? (
                  <Skeleton className="h-7 w-24 mt-0.5" />
                ) : (
                  <p className="text-2xl font-bold tabular-nums">
                    {totalTasks}
                  </p>
                )}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Surface variant="warning" padding="md" className="flex items-start gap-2 text-sm">
        <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" />
        <span>
          {copy('disclaimer_prefix')}
          <strong>{copy('disclaimer_emphasis')}</strong>
          {copy('disclaimer_suffix')}
        </span>
      </Surface>

      <div className="space-y-3">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm text-muted-foreground">
            {copy('time_range')}
          </span>
          {presentation.quick_ranges.map((range) => (
            <Action
              key={range.days}
              variant={
                !customRange && days === range.days
                  ? 'default'
                  : 'outline'
              }
              size="sm"
              onClick={() => selectQuickRange(range.days)}
            >
              {range.label}
            </Action>
          ))}
          <Action
            variant={
              customRange || showCustomPicker
                ? 'default'
                : 'outline'
            }
            size="sm"
            onClick={showRangePicker}
          >
            {copy('custom_range')}
          </Action>
        </div>

        {showCustomPicker && (
          <div className="flex items-end gap-2 flex-wrap">
            <Field id="usage-start_date" label={schema.administration.user_assets.presentation.text.start_date}>
              {(controlProps) => (<SchemaDateField {...controlProps} {...dateRangeBounds(draftStart, draftEnd).start} value={draftStart} onValueChange={(next) => { setDraftStart(next); setRangeError(null); }} />)}
            </Field>
            <span className="text-sm text-muted-foreground">
              {presentation.date_separator}
            </span>
            <Field id="usage-end_date" label={schema.administration.user_assets.presentation.text.end_date}>
              {(controlProps) => (<SchemaDateField {...controlProps} {...dateRangeBounds(draftStart, draftEnd).end} value={draftEnd} onValueChange={(next) => { setDraftEnd(next); setRangeError(null); }} />)}
            </Field>
            <Action size="sm" onClick={queryRange}>
              {copy('query')}
            </Action>
          </div>
        )}
        {rangeError ? <p role="alert" className="text-sm text-destructive">{rangeError}</p> : null}
      </div>

      <Separator />

      <Card>
        <CardHeader>
          <CardTitle className="text-base">
            {copy('ranking_title')}
          </CardTitle>
          <CardDescription>
            {copy('ranking_description')}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {usageLoading ? (
            <div className="space-y-3">
              {Array.from(
                { length: presentation.loading_rows },
                (_, index) => (
                  <Skeleton
                    key={index}
                    className="h-12 w-full"
                  />
                ),
              )}
            </div>
          ) : users.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              {copy('empty')}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left">
                    <th className="pb-2 font-medium text-muted-foreground">
                      {copy('column_user')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {copy('column_tasks')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {copy('column_succeeded')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {copy('column_failed')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {copy('column_credits')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {copy('column_last_active')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((user) => (
                    <tr
                      key={user.userId}
                      className="border-b last:border-0"
                    >
                      <td className="py-3">
                        <div>
                          <p className="font-medium">
                            {user.userName || user.userId}
                          </p>
                          <p className="text-xs text-muted-foreground">
                            {user.userEmail}
                          </p>
                        </div>
                      </td>
                      <td className="py-3 text-right tabular-nums">
                        {user.totalTasks}
                      </td>
                      <td className="py-3 text-right tabular-nums text-success">
                        {user.succeededTasks}
                      </td>
                      <td className="py-3 text-right tabular-nums text-destructive">
                        {user.failedTasks}
                      </td>
                      <td className="py-3 text-right">
                        <Badge variant="secondary">
                          {user.priceTotals
                            ? sessionPriceTotalsText(schema, user.priceTotals)
                            : `${presentation.approximation_prefix}${user.estimatedCredits}`}
                        </Badge>
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground">
                        {new Date(user.lastActiveAt).toLocaleString(
                          presentation.date_locale,
                          presentation.date_time_options as
                            Intl.DateTimeFormatOptions,
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

const httpForbiddenStatus = 403;
