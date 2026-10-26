'use client';

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import { Shield, Search, Eye } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { SchemaDateField } from '@/components/form/schema-date-field';
import { dateRangeBounds, formatLocalDate, localDayBoundary } from '@/components/ui/date-field-math';
import { Field } from '@/components/ui/field';
import { Surface } from '@/components/ui/surface';
import { Select, SelectTrigger, SelectContent, SelectItem } from '@/components/ui/select';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useProductSchema } from '@/components/product-schema-provider';
import {
  AdministrationAuditLogsBrowserError,
  createAdministrationAuditLogsBrowserClient,
  type AdministrationAuditLog,
} from '@/lib/administration/audit-logs-browser-client';
import {
  createAdministrationUsersBrowserClient,
} from '@/lib/administration/users-browser-client';
import {
  formatSchemaText,
  type AdministrationProductSchema,
} from '@/lib/product-schema';

type AuditPresentation =
  AdministrationProductSchema['audit_logs']['presentation'];

type TextFormatter = (
  key: string,
  variables?: Record<string, string | number>,
) => string;

function formatDetail(raw: string): Record<string, unknown> | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (
      parsed &&
      typeof parsed === 'object' &&
      !Array.isArray(parsed) &&
      Object.keys(parsed).length > 0
    ) {
      return parsed as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

function actionBadgeClass(
  action: string,
  presentation: AuditPresentation,
): string {
  const exact = presentation.action_badge_classes[action];
  if (exact) return exact;
  for (const [prefix, className] of Object.entries(
    presentation.action_prefix_badge_classes,
  )) {
    if (action.startsWith(prefix)) return className;
  }
  return presentation.default_badge_class;
}

function AuditDetailDialog({
  log,
  open,
  onOpenChange,
  presentation,
  actionLabels,
  text,
}: {
  log: AdministrationAuditLog | null;
  open: boolean;
  onOpenChange: (value: boolean) => void;
  presentation: AuditPresentation;
  actionLabels: Record<string, string>;
  text: TextFormatter;
}) {
  const { text: productText } = useProductSchema();
  if (!log) return null;
  const detail = formatDetail(log.detail);
  const placeholder = presentation.empty_placeholder;
  const formatDate = (value: string): string =>
    new Intl.DateTimeFormat(
      presentation.date_locale,
      presentation.date_time_options as Intl.DateTimeFormatOptions,
    ).format(new Date(value));
  const rows: { label: string; value: React.ReactNode }[] = [
    {
      label: text('detail_id'),
      value: (
        <span className="font-mono text-xs break-all">{log.id}</span>
      ),
    },
    { label: text('detail_created_at'), value: formatDate(log.createdAt) },
    {
      label: text('detail_user_name'),
      value: log.userName || placeholder,
    },
    {
      label: text('detail_user_id'),
      value: (
        <span className="font-mono text-xs break-all">{log.userId}</span>
      ),
    },
    {
      label: text('detail_action'),
      value: (
        <Badge
          variant="secondary"
          className={actionBadgeClass(log.action, presentation)}
        >
          {actionLabels[log.action] || log.action}
        </Badge>
      ),
    },
    {
      label: text('detail_resource_type'),
      value: log.resourceType || placeholder,
    },
    {
      label: text('detail_resource_id'),
      value: log.resourceId ? (
        <span className="font-mono text-xs break-all">{log.resourceId}</span>
      ) : placeholder,
    },
    {
      label: text('detail_ip_address'),
      value: log.ipAddress || placeholder,
    },
    {
      label: text('detail_user_agent'),
      value: log.userAgent ? (
        <span className="text-xs break-all leading-relaxed">
          {log.userAgent}
        </span>
      ) : placeholder,
    },
  ];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent closeLabel={productText('ui.dialog.close')} className="sm:max-w-lg max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{text('detail_title')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Surface variant="panel">
            <table className="w-full text-sm">
              <tbody>
                {rows.map((row) => (
                  <tr key={row.label} className="border-b last:border-0">
                    <td className="px-3 py-2 font-medium text-muted-foreground whitespace-nowrap w-24">
                      {row.label}
                    </td>
                    <td className="px-3 py-2">{row.value}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Surface>
          {detail && (
            <div className="space-y-1.5">
              <p className="text-xs font-medium text-muted-foreground">
                {text('detail_payload')}
              </p>
              <Surface variant="muted" padding="md"><pre className="text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all leading-relaxed">
                {JSON.stringify(detail, null, 2)}
              </pre></Surface>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function getRecentDays(days: number): { from: string; to: string } {
  const today = new Date();
  const start = new Date(today);
  start.setDate(start.getDate() - (days - 1));
  return { from: formatLocalDate(start), to: formatLocalDate(today) };
}

function validDate(value: string, endOfDay: boolean): Date | undefined {
  return localDayBoundary(value, endOfDay);
}

export function AuditLogPanel() {
  const { schema } = useProductSchema();
  const auditSchema = schema.administration.audit_logs;
  const presentation = auditSchema.presentation;
  const copy = presentation.text;
  const defaultDates = getRecentDays(presentation.default_recent_days);
  const [page, setPage] = useState(auditSchema.limits.default_page);
  const [action, setAction] = useState('');
  const [userID, setUserID] = useState('');
  const [from, setFrom] = useState(defaultDates.from);
  const [to, setTo] = useState(defaultDates.to);
  const [selectedLog, setSelectedLog] =
    useState<AdministrationAuditLog | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const auditClient = useMemo(
    () => createAdministrationAuditLogsBrowserClient({
      schema: auditSchema,
      route: schema.runtime.http.legacy_routes.admin_audit_logs,
    }),
    [auditSchema, schema.runtime.http.legacy_routes.admin_audit_logs],
  );
  const usersClient = useMemo(
    () => createAdministrationUsersBrowserClient({
      schema: schema.administration.users,
      authenticationAPI: schema.authentication.api,
      routes: {
        collection: schema.runtime.http.legacy_routes.admin_users,
        item: schema.runtime.http.legacy_routes.admin_user,
        session: schema.runtime.http.legacy_routes.auth_session,
      },
    }),
    [schema],
  );
  const text: TextFormatter = (
    key,
    variables = {},
  ) => formatSchemaText(copy[key], variables);
  const actionLabels = useMemo(
    () => Object.fromEntries(
      schema.audit_actions.map((item) => [item.value, item.label]),
    ),
    [schema.audit_actions],
  );
  const actionOptions = useMemo(
    () => [
      { value: '', label: text('all_actions') },
      ...schema.audit_actions,
    ],
    [copy, schema.audit_actions],
  );
  const { data: users = [] } = useSWR(
    ['administration-audit-users', schema.revision],
    () => usersClient.listUsers(),
    { revalidateOnFocus: false },
  );
  const userOptions = useMemo(
    () => users.map((user) => {
      const name =
        user.nickname ||
        user.name ||
        user.username ||
        text('unnamed_user');
      const identifier = user.email || user.username || user.id;
      return {
        id: user.id,
        label: text('user_option_label', { name, identifier }),
      };
    }),
    [copy, users],
  );
  const pageSize = auditSchema.limits.default_page_size;
  const {
    data,
    isLoading,
    error,
  } = useSWR(
    [
      'administration-audit-logs',
      schema.revision,
      page,
      pageSize,
      action,
      userID,
      from,
      to,
    ],
    () => auditClient.listAuditLogs({
      page,
      pageSize,
      ...(action ? { action } : {}),
      ...(userID ? { userID } : {}),
      ...(validDate(from, false)
        ? { from: validDate(from, false) }
        : {}),
      ...(validDate(to, true) ? { to: validDate(to, true) } : {}),
    }),
    {
      revalidateOnFocus: false,
      keepPreviousData: true,
    },
  );
  const logs = data?.data ?? [];
  const total = data?.total ?? 0;
  const effectivePageSize = data?.pageSize || pageSize;
  const totalPages = Math.max(1, Math.ceil(total / effectivePageSize));
  const formatDate = (value: string): string =>
    new Intl.DateTimeFormat(
      presentation.date_locale,
      presentation.date_time_options as Intl.DateTimeFormatOptions,
    ).format(new Date(value));

  if (
    error instanceof AdministrationAuditLogsBrowserError &&
    error.status === 403
  ) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Shield className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold">{text('forbidden_title')}</h2>
        <p className="text-sm text-muted-foreground mt-1">
          {text('forbidden_description')}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="pt-4 pb-4">
          <div className="flex flex-wrap items-end gap-3">
            <Field id="audit-action" label={text('filter_action')}>
              {(controlProps) => (<Select value={action} onValueChange={(value) => setAction(value ?? '')}>
                <SelectTrigger {...controlProps} className="w-[160px]"><span>{actionOptions.find((option) => option.value === action)?.label ?? ''}</span></SelectTrigger>
                <SelectContent>
                {actionOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
              </Select>)}
            </Field>
            <Field id="audit-userID" label={text('filter_user')}>
              {(controlProps) => (<Select value={userID} onValueChange={(value) => setUserID(value ?? '')}>
                <SelectTrigger {...controlProps} className="w-[200px]"><span>{userOptions.find((user) => user.id === userID)?.label ?? text('all_users')}</span></SelectTrigger>
                <SelectContent>
                <SelectItem value="">{text('all_users')}</SelectItem>
                {userOptions.map((user) => (
                  <SelectItem key={user.id} value={user.id}>
                    {user.label}
                  </SelectItem>
                ))}
              </SelectContent>
              </Select>)}
            </Field>
            <Field id="audit-from" label={text('filter_from')}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).start} value={from} onValueChange={setFrom} />)}
            </Field>
            <Field id="audit-to" label={text('filter_to')}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).end} value={to} onValueChange={setTo} />)}
            </Field>
            <Action
              size="default"
              onClick={() => setPage(auditSchema.limits.default_page)}
            >
              <Search className="h-4 w-4 mr-1.5" />
              {text('filter_submit')}
            </Action>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center justify-between">
            <span>{text('title')}</span>
            <span className="text-sm font-normal text-muted-foreground">
              {text('total_summary', { total })}
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {Array.from(
                { length: presentation.loading_rows },
                (_, index) => (
                  <Skeleton key={index} className="h-10 w-full" />
                ),
              )}
            </div>
          ) : logs.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-12">
              {text('empty')}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left">
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_time')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_user')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_action')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_resource_type')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_resource_id')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_ip_address')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_actions')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {logs.map((log) => (
                    <tr
                      key={log.id}
                      className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                    >
                      <td className="py-3 text-xs text-muted-foreground whitespace-nowrap">
                        {formatDate(log.createdAt)}
                      </td>
                      <td className="py-3">
                        <span className="font-medium">
                          {log.userName || presentation.empty_placeholder}
                        </span>
                        <span className="text-xs text-muted-foreground ml-1">
                          {log.userId
                            ? `(${log.userId.slice(
                              0,
                              presentation.user_id_preview_characters,
                            )}${presentation.preview_suffix})`
                            : ''}
                        </span>
                      </td>
                      <td className="py-3">
                        <Badge
                          variant="secondary"
                          className={actionBadgeClass(
                            log.action,
                            presentation,
                          )}
                        >
                          {actionLabels[log.action] || log.action}
                        </Badge>
                      </td>
                      <td className="py-3 text-muted-foreground">
                        {log.resourceType || presentation.empty_placeholder}
                      </td>
                      <td className="py-3 text-muted-foreground font-mono text-xs">
                        {log.resourceId ? (
                          <HintTooltip name={log.resourceId}>
                            <span>
                              {log.resourceId.length >
                              presentation.resource_id_preview_characters
                                ? `${log.resourceId.slice(
                                  0,
                                  presentation.resource_id_preview_characters,
                                )}${presentation.preview_suffix}`
                                : log.resourceId}
                            </span>
                          </HintTooltip>
                        ) : presentation.empty_placeholder}
                      </td>
                      <td className="py-3 text-xs text-muted-foreground">
                        {log.ipAddress || presentation.empty_placeholder}
                      </td>
                      <td className="py-3">
                        <Action
                          variant="ghost"
                          size="sm"

                          onClick={() => {
                            setSelectedLog(log);
                            setDetailOpen(true);
                          }}
                        >
                          <Eye className="h-3.5 w-3.5 mr-1" />
                          {text('detail_button')}
                        </Action>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {totalPages > 1 && (
            <div className="flex items-center justify-between pt-4 border-t mt-4">
              <Action
                variant="outline"
                size="sm"
                disabled={page <= auditSchema.limits.default_page}
                onClick={() => setPage((current) => Math.max(
                  auditSchema.limits.default_page,
                  current - 1,
                ))}
              >
                {text('pagination_previous')}
              </Action>
              <span className="text-sm text-muted-foreground">
                {text('pagination_summary', { page, totalPages })}
              </span>
              <Action
                variant="outline"
                size="sm"
                disabled={page >= totalPages}
                onClick={() => setPage((current) =>
                  Math.min(totalPages, current + 1))}
              >
                {text('pagination_next')}
              </Action>
            </div>
          )}
        </CardContent>
      </Card>

      <AuditDetailDialog
        log={selectedLog}
        open={detailOpen}
        onOpenChange={setDetailOpen}
        presentation={presentation}
        actionLabels={actionLabels}
        text={text}
      />
    </div>
  );
}
