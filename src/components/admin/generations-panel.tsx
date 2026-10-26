'use client';

import {
  useCallback,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import useSWR from 'swr';
import {
  ChevronDown,
  ChevronUp,
  Download,
  ExternalLink,
  Eye,
  Package,
  Search,
  Shield,
} from 'lucide-react';
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
import { ThumbnailCell } from '@/components/common/thumbnail-cell';
import { useProductSchema } from '@/components/product-schema-provider';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import {
  AdministrationGenerationsBrowserError,
  createAdministrationGenerationsBrowserClient,
  type AdministrationGenerationDetail,
  type AdministrationGenerationRecord,
} from '@/lib/administration/generations-browser-client';
import { formatNativePrice } from '@/lib/pricing';
import { inputDerivationText } from '@/lib/workspace/input-derivation';
import {
  createAdministrationUsersBrowserClient,
} from '@/lib/administration/users-browser-client';
import {
  downloadAdminTaskAsset,
  formatParamsTooltip,
  parseResultUrls,
} from '@/lib/admin-utils';
import {
  formatSchemaText,
  schemaText,
  taskStatePresentation,
  type AdministrationProductSchema,
  type CapabilitySchema,
  type ProductSchema,
} from '@/lib/product-schema';

type GenerationSchema = AdministrationProductSchema['generations'];

type TextFormatter = (
  key: string,
  values?: Record<string, string | number>,
) => string;

function getRecentDays(days: number): { from: string; to: string } {
  const today = new Date();
  const start = new Date(today);
  start.setDate(start.getDate() - (days - 1));
  return { from: formatLocalDate(start), to: formatLocalDate(today) };
}

function isoDate(value: string, endOfDay: boolean): string | undefined {
  return localDayBoundary(value, endOfDay)?.toISOString();
}

function configuredPreviewSource(
  record: AdministrationGenerationRecord,
  _schema: ProductSchema,
  generationSchema: GenerationSchema,
): string | null {
  if (record.thumbnailUrl && safeAssetURL(record.thumbnailUrl)) return safeAssetURL(record.thumbnailUrl);

  const imageExtensions = new Set(
    generationSchema.assets.preview_image_extensions.map(
      (extension) => extension.replace(/^\./, '').toLowerCase(),
    ),
  );
  const fallback = parseResultUrls(record.resultUrls).find((candidate) => {
    if (!safeAssetURL(candidate)) return false;
    try {
      const path = new URL(candidate, window.location.origin).pathname.toLowerCase();
      return [...imageExtensions].some(
        (extension) => path.endsWith(`.${extension}`),
      );
    } catch {
      return false;
    }
  });
  return fallback ? safeAssetURL(fallback) : null;
}

function capabilityForTaskType(
  taskType: string,
  schema: ProductSchema,
): CapabilitySchema | undefined {
  const capabilityID =
    schema.task_stream.aliases.find(
      (alias) => alias.external_type === taskType,
    )?.capability ?? taskType;
  return schema.capabilities.find(
    (capability) => capability.id === capabilityID,
  );
}

function modelParameterValue(
  record: AdministrationGenerationRecord,
  schema: ProductSchema,
  placeholder: string,
): string {
  const field = capabilityForTaskType(
    record.taskType,
    schema,
  )?.legacy_api?.model_parameter;
  if (!field) return placeholder;
  try {
    const parameters = JSON.parse(record.params) as unknown;
    if (
      typeof parameters !== 'object' ||
      parameters === null ||
      Array.isArray(parameters)
    ) {
      return placeholder;
    }
    const value = (parameters as Record<string, unknown>)[field];
    return typeof value === 'string' && value ? value : placeholder;
  } catch {
    return placeholder;
  }
}

function GenerationDetailDialog({
  open,
  onOpenChange,
  loading,
  detail,
  cacheExpanded,
  onCacheExpandedChange,
  downloadingTaskID,
  onDownload,
  schema,
  generationSchema,
  text,
  formatDate,
  isPackageTask,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  loading: boolean;
  detail: AdministrationGenerationDetail | null;
  cacheExpanded: boolean;
  onCacheExpandedChange: (value: boolean) => void;
  downloadingTaskID: string | null;
  onDownload: (taskID: string) => void;
  schema: ProductSchema;
  generationSchema: GenerationSchema;
  text: TextFormatter;
  formatDate: (value: string) => string;
  isPackageTask: (taskType: string) => boolean;
}) {
  const placeholder = generationSchema.presentation.empty_placeholder;
  const ledger = detail?.ledger;
  const detailRows: Array<{ label: string; value: ReactNode }> = ledger
    ? [
        {
          label: text('detail_task_id'),
          value: (
            <span className="font-mono text-xs break-all">
              {ledger.taskId}
            </span>
          ),
        },
        {
          label: text('detail_user'),
          value: `${ledger.userName || placeholder} (${
            ledger.userEmail || placeholder
          })`,
        },
        { label: text('detail_task_type'), value: ledger.taskType },
        {
          label: text('detail_status'),
          value: (
            <Badge
              variant="secondary"
              className={taskStatePresentation(
                schema,
                ledger.taskStatus,
              ).color}
            >
              {taskStatePresentation(schema, ledger.taskStatus).label}
            </Badge>
          ),
        },
        {
          label: text('detail_estimated_credits'),
          value: ledger.nativeQuote !== undefined
            ? ledger.nativeQuote
              ? schemaText(schema, 'ui.pricing.frozen_estimate').replace('{amount}', formatNativePrice(ledger.nativeQuote.total_amount, ledger.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown'))
              : schemaText(schema, 'ui.pricing.unknown')
            : text('estimated_credits', { credits: ledger.estimatedCredits }),
        },
        {
          label: text('detail_created_at'),
          value: formatDate(ledger.createdAt),
        },
        {
          label: text('detail_finished_at'),
          value: ledger.finishedAt
            ? formatDate(ledger.finishedAt)
            : placeholder,
        },
      ]
    : [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent closeLabel={schemaText(schema, 'ui.dialog.close')} className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{text('detail_title')}</DialogTitle>
        </DialogHeader>
        {loading ? (
          <div
            className="flex justify-center py-8"
            aria-label={text('detail_loading')}
          >
            <Skeleton className="h-6 w-32" />
          </div>
        ) : ledger ? (
          <div className="space-y-4">
            <Surface variant="panel">
              <table className="w-full text-sm">
                <tbody>
                  {detailRows.map((row) => (
                    <tr key={row.label} className="border-b last:border-0">
                      <td className="px-3 py-2 font-medium text-muted-foreground w-28">
                        {row.label}
                      </td>
                      <td className="px-3 py-2">{row.value}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Surface>

            {Object.keys(ledger.params).length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-muted-foreground">
                  {text('detail_params')}
                </p>
                <Surface variant="muted" padding="md"><pre className="text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all leading-relaxed">
                  {JSON.stringify(ledger.params, null, 2)}
                </pre></Surface>
              </div>
            )}

            {detail && detail.inputDerivations.length > 0 && (
              <div className="space-y-1.5" data-testid="admin-input-derivations">
                <p className="text-xs font-medium text-muted-foreground">
                  {schemaText(schema, 'ui.result.detail.field.input_derivation')}
                </p>
                <Surface variant="muted" padding="md">
                  <ul className="space-y-2 text-xs leading-relaxed">
                    {detail.inputDerivations.map((derivation) => (
                      <li key={`${derivation.parameter}.${derivation.index}`}>
                        <p>{inputDerivationText(schema, derivation, `${derivation.parameter} ${derivation.index + 1}`)}</p>
                        <p className="font-mono text-muted-foreground break-all">
                          {formatSchemaText(schemaText(schema, 'ui.admin.generations.input_derivation_assets'), {
                            asset: derivation.assetId,
                            source: derivation.sourceAssetId || schemaText(schema, 'ui.admin.generations.input_derivation_source_unknown'),
                          })}
                        </p>
                      </li>
                    ))}
                  </ul>
                </Surface>
              </div>
            )}

            {ledger.resultUrls.length > 0 ? (
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-muted-foreground">
                    {text('result_count', {
                      count: ledger.resultUrls.length,
                    })}
                  </p>
                  <Action
                    variant="outline"
                    size="sm"

                    loading={downloadingTaskID === ledger.taskId}
                    onClick={() => onDownload(ledger.taskId)}
                  >
                    {isPackageTask(ledger.taskType) ? (
                      <Package className="h-3.5 w-3.5 mr-1" />
                    ) : (
                      <Download className="h-3.5 w-3.5 mr-1" />
                    )}
                    {downloadingTaskID === ledger.taskId
                      ? text('downloading')
                      : isPackageTask(ledger.taskType)
                        ? text('download_all_package')
                        : text('download_all')}
                  </Action>
                </div>
                <div className="space-y-2">
                  {ledger.resultUrls.map((url) => (
                    <Surface
                      key={url}
                      variant="panel" padding="sm"
                      className="flex items-center gap-2"
                    >
                      <span className="flex-1 text-xs font-mono break-all text-muted-foreground">
                        {safeAssetURL(url) || placeholder}
                      </span>
                      {safeAssetURL(url) && <Action variant="ghost" size="icon-sm" nativeButton={false} render={<a
                        href={safeAssetURL(url)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="shrink-0"
                        aria-label={text('open_task')}
                       />} >
                        <ExternalLink className="h-3.5 w-3.5" />
                      </Action>}
                      {safeAssetURL(url) && <Action variant="ghost" size="icon-sm" nativeButton={false} render={<a
                        href={safeAssetURL(url)}
                        download
                        className="shrink-0"
                        aria-label={text('download')}
                       />} >
                        <Download className="h-3.5 w-3.5" />
                      </Action>}
                    </Surface>
                  ))}
                </div>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                {text('detail_no_result_urls')}
              </p>
            )}

            {detail?.taskCache && (
              <div className="space-y-1.5">
                <Action variant="ghost" size="sm" aria-expanded={cacheExpanded}
                  onClick={() =>
                    onCacheExpandedChange(!cacheExpanded)}
                >
                  {cacheExpanded ? (
                    <ChevronUp className="h-3 w-3" />
                  ) : (
                    <ChevronDown className="h-3 w-3" />
                  )}
                  {text('detail_cache_full')}
                </Action>
                {cacheExpanded && (
                  <Surface variant="muted" padding="md"><pre className="text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all leading-relaxed max-h-[300px] overflow-y-auto">
                    {JSON.stringify(detail.taskCache.data, null, 2)}
                  </pre></Surface>
                )}
              </div>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground text-center py-8">
            {text('detail_empty')}
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function GenerationsPanel() {
  const { schema } = useProductSchema();
  const generationSchema = schema.administration.generations;
  const presentation = generationSchema.presentation;
  const copy = presentation.text;
  const text: TextFormatter = (
    key,
    values = {},
  ) => formatSchemaText(copy[key], values);
  const defaultDates = getRecentDays(
    generationSchema.limits.default_recent_days,
  );
  const [page, setPage] = useState(
    generationSchema.limits.default_page,
  );
  const [userID, setUserID] = useState('');
  const [taskType, setTaskType] = useState('');
  const [status, setStatus] = useState('');
  const [from, setFrom] = useState(defaultDates.from);
  const [to, setTo] = useState(defaultDates.to);
  const [downloadingTaskID, setDownloadingTaskID] =
    useState<string | null>(null);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailData, setDetailData] =
    useState<AdministrationGenerationDetail | null>(null);
  const [cacheExpanded, setCacheExpanded] = useState(false);

  const generationsClient = useMemo(
    () => createAdministrationGenerationsBrowserClient({
      schema: generationSchema,
      routes: {
        collection:
          schema.runtime.http.legacy_routes.admin_generations,
        item: schema.runtime.http.legacy_routes.admin_generation,
        download:
          schema.runtime.http.legacy_routes.admin_generation_download,
      },
    }),
    [generationSchema, schema.runtime.http.legacy_routes],
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
  const { data: users = [] } = useSWR(
    ['administration-generation-users', schema.revision],
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
  const taskTypeOptions = useMemo(() => {
    const nativeOptions = [...schema.capabilities]
      .sort((left, right) => left.order - right.order)
      .map((capability) => ({
        value: capability.id,
        label: capability.label,
      }));
    const legacyOptions = schema.task_stream.aliases
      .filter(
        (alias) =>
          alias.external_type !== alias.capability &&
          !schema.capabilities.some(
            (capability) => capability.id === alias.external_type,
          ),
      )
      .map((alias) => ({
        value: alias.external_type,
        label: text('legacy_animation'),
      }));
    return [
      { value: '', label: text('all_task_types') },
      ...nativeOptions,
      ...legacyOptions,
    ];
  }, [copy, schema.capabilities, schema.task_stream.aliases]);
  const statusOptions = useMemo(
    () => [
      { value: '', label: text('all_statuses') },
      ...schema.task_states.filter(
        (state, index, states) =>
          states.findIndex(
            (candidate) =>
              candidate.internal_state === state.internal_state,
          ) === index,
      ).map((state) => ({
        value: state.internal_state,
        label: state.label,
      })),
    ],
    [copy, schema.task_states],
  );
  const packageTaskTypes = useMemo(() => {
    const capabilities = new Set(
      schema.capabilities
        .filter(
          (capability) =>
            capability.domain === 'model-3d' ||
            capability.domain === 'processing',
        )
        .map((capability) => capability.id),
    );
    for (const alias of schema.task_stream.aliases) {
      if (capabilities.has(alias.capability)) {
        capabilities.add(alias.external_type);
      }
    }
    return capabilities;
  }, [schema.capabilities, schema.task_stream.aliases]);
  const isPackageTask = useCallback(
    (value: string): boolean => packageTaskTypes.has(value),
    [packageTaskTypes],
  );
  const pageSize = generationSchema.limits.default_page_size;
  const {
    data,
    isLoading,
    error,
  } = useSWR(
    [
      'administration-generations',
      schema.revision,
      page,
      pageSize,
      userID,
      taskType,
      status,
      from,
      to,
    ],
    () => generationsClient.listGenerations({
      page,
      pageSize,
      ...(userID ? { userId: userID } : {}),
      ...(taskType ? { taskType } : {}),
      ...(status ? { status } : {}),
      ...(isoDate(from, false)
        ? { from: isoDate(from, false) }
        : {}),
      ...(isoDate(to, true) ? { to: isoDate(to, true) } : {}),
    }),
    {
      revalidateOnFocus: false,
      keepPreviousData: true,
    },
  );
  const records = data?.records ?? [];
  const total = data?.total ?? 0;
  const effectivePageSize = data?.pageSize || pageSize;
  const totalPages = Math.max(
    1,
    Math.ceil(total / effectivePageSize),
  );
  const formatDate = useCallback(
    (value: string): string => new Intl.DateTimeFormat(
      presentation.date_locale,
      presentation.date_time_options as Intl.DateTimeFormatOptions,
    ).format(new Date(value)),
    [presentation],
  );

  const openDetail = useCallback(
    async (taskID: string) => {
      setDetailOpen(true);
      setDetailLoading(true);
      setDetailData(null);
      setCacheExpanded(false);
      try {
        setDetailData(await generationsClient.getGeneration(taskID));
      } catch {
        setDetailData(null);
      } finally {
        setDetailLoading(false);
      }
    },
    [generationsClient],
  );

  const handleDownload = useCallback(
    async (taskID: string) => {
      setDownloadingTaskID(taskID);
      setDownloadError(null);
      try {
        await downloadAdminTaskAsset({
          url: generationsClient.generationDownloadURL(taskID),
          errorField: generationSchema.api.fields.error,
          redirectField: generationSchema.api.fields.redirect,
          failedMessage: generationSchema.messages.download_failed,
          unknownResponseMessage:
            generationSchema.messages.unknown_download_response,
          fallbackFilename:
            generationSchema.assets.download_filename_template.replaceAll(
              '{task_id}',
              taskID,
            ),
        });
      } catch (caught) {
        setDownloadError(
          caught instanceof Error
            ? caught.message
            : generationSchema.messages.download_failed,
        );
      } finally {
        setDownloadingTaskID(null);
      }
    },
    [generationSchema, generationsClient],
  );

  if (
    error instanceof AdministrationGenerationsBrowserError &&
    error.status === 403
  ) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Shield className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold">
          {text('forbidden_title')}
        </h2>
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
            <Field id="generations-userID" label={text('filter_user')}>
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
            <Field id="generations-taskType" label={text('filter_task_type')}>
              {(controlProps) => (<Select value={taskType} onValueChange={(value) => setTaskType(value ?? '')}>
                <SelectTrigger {...controlProps} className="w-[160px]"><span>{taskTypeOptions.find((option) => option.value === taskType)?.label ?? ''}</span></SelectTrigger>
                <SelectContent>
                {taskTypeOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
              </Select>)}
            </Field>
            <Field id="generations-status" label={text('filter_status')}>
              {(controlProps) => (<Select value={status} onValueChange={(value) => setStatus(value ?? '')}>
                <SelectTrigger {...controlProps} className="w-[140px]"><span>{statusOptions.find((option) => option.value === status)?.label ?? ''}</span></SelectTrigger>
                <SelectContent>
                {statusOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
              </Select>)}
            </Field>
            <Field id="generations-from" label={text('filter_from')}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).start} value={from} onValueChange={setFrom} />)}
            </Field>
            <Field id="generations-to" label={text('filter_to')}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).end} value={to} onValueChange={setTo} />)}
            </Field>
            <Action
              size="default"
              onClick={() =>
                setPage(generationSchema.limits.default_page)}
            >
              <Search className="h-4 w-4 mr-1.5" />
              {text('filter_submit')}
            </Action>
          </div>
          {downloadError ? <p role="alert" className="mt-3 text-sm text-destructive">{downloadError}</p> : null}
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
          ) : records.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-12">
              {text('empty')}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left">
                    {[
                      'column_preview',
                      'column_time',
                      'column_user',
                      'column_task_type',
                      'column_model_params',
                      'column_task_id',
                      'column_status',
                      'column_estimated_credits',
                      'column_actions',
                    ].map((key) => (
                      <th
                        key={key}
                        className="pb-2 font-medium text-muted-foreground"
                      >
                        {text(key)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {records.map((record) => {
                    const state = taskStatePresentation(
                      schema,
                      record.taskStatus,
                    );
                    const packageTask = isPackageTask(record.taskType);
                    return (
                      <tr
                        key={record.id}
                        className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                      >
                        <td className="py-2">
                          <ThumbnailCell
                            src={configuredPreviewSource(
                              record,
                              schema,
                              generationSchema,
                            )}
                          />
                        </td>
                        <td className="py-3 text-xs text-muted-foreground whitespace-nowrap">
                          {formatDate(record.createdAt)}
                        </td>
                        <td className="py-3">
                          <span className="font-medium">
                            {record.userName ||
                              presentation.empty_placeholder}
                          </span>
                          {record.userId && (
                            <span className="text-xs text-muted-foreground ml-1">
                              (
                              {record.userId.slice(
                                0,
                                presentation.user_id_preview_characters,
                              )}
                              {record.userId.length >
                              presentation.user_id_preview_characters
                                ? presentation.preview_suffix
                                : ''}
                              )
                            </span>
                          )}
                        </td>
                        <td className="py-3 text-muted-foreground">
                          {record.taskType}
                        </td>
                        <td className="py-3">
                          <HintTooltip name={formatParamsTooltip(record.params)}>
                            <span className="text-xs text-muted-foreground cursor-help underline decoration-dotted">
                              {modelParameterValue(
                                record,
                                schema,
                                presentation.empty_placeholder,
                              )}
                            </span>
                          </HintTooltip>
                        </td>
                        <td className="py-3 text-muted-foreground font-mono text-xs">
                          <HintTooltip name={record.taskId}>
                            <span>
                              {record.taskId.slice(
                                0,
                                presentation.task_id_preview_characters,
                              )}
                              {record.taskId.length >
                              presentation.task_id_preview_characters
                                ? presentation.preview_suffix
                                : ''}
                            </span>
                          </HintTooltip>
                        </td>
                        <td className="py-3">
                          <Badge
                            variant="secondary"
                            className={state.color}
                          >
                            {state.label}
                          </Badge>
                        </td>
                        <td className="py-3 text-muted-foreground tabular-nums">
                          {record.nativeQuote !== undefined
                            ? record.nativeQuote
                              ? schemaText(schema, 'ui.pricing.frozen_estimate').replace('{amount}', formatNativePrice(record.nativeQuote.total_amount, record.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown'))
                              : schemaText(schema, 'ui.pricing.unknown')
                            : text('estimated_credits', { credits: record.estimatedCredits })}
                        </td>
                        <td className="py-3">
                          <div className="flex items-center gap-1">
                            <Action
                              variant="ghost"
                              size="sm"

                              onClick={() => openDetail(record.taskId)}
                            >
                              <Eye className="h-3.5 w-3.5 mr-1" />
                              {text('detail_button')}
                            </Action>
                            {state.outcome === 'success' && (
                              <HintTooltip
                                name={
                                  packageTask
                                    ? text('package_download_tooltip')
                                    : text('download')
                                }
                              >
                                <Action
                                  variant="ghost"
                                  size="sm"

                                  loading={
                                    downloadingTaskID === record.taskId
                                  }
                                  onClick={() =>
                                    handleDownload(record.taskId)}
                                >
                                  {packageTask ? (
                                    <Package className="h-3.5 w-3.5 mr-1" />
                                  ) : (
                                    <Download className="h-3.5 w-3.5 mr-1" />
                                  )}
                                  {downloadingTaskID === record.taskId
                                    ? text('downloading')
                                    : packageTask
                                      ? text('package')
                                      : text('download')}
                                </Action>
                              </HintTooltip>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          {totalPages > 1 && (
            <div className="flex items-center justify-between pt-4 border-t mt-4">
              <Action
                variant="outline"
                size="sm"
                disabled={page <= generationSchema.limits.default_page}
                onClick={() =>
                  setPage((value) => Math.max(
                    generationSchema.limits.default_page,
                    value - 1,
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
                onClick={() =>
                  setPage((value) => Math.min(totalPages, value + 1))}
              >
                {text('pagination_next')}
              </Action>
            </div>
          )}
        </CardContent>
      </Card>

      <GenerationDetailDialog
        open={detailOpen}
        onOpenChange={setDetailOpen}
        loading={detailLoading}
        detail={detailData}
        cacheExpanded={cacheExpanded}
        onCacheExpandedChange={setCacheExpanded}
        downloadingTaskID={downloadingTaskID}
        onDownload={handleDownload}
        schema={schema}
        generationSchema={generationSchema}
        text={text}
        formatDate={formatDate}
        isPackageTask={isPackageTask}
      />
    </div>
  );
}
