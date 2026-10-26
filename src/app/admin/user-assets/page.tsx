'use client';

import { useCallback, useMemo, useState } from 'react';
import useSWR from 'swr';
import {
  Box,
  Calendar,
  ChevronLeft,
  ChevronRight,
  Download,
  History,
  Image,
  Package,
  Search,
  Sparkles,
  Upload,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Surface } from '@/components/ui/surface';
import { SchemaDateField } from '@/components/form/schema-date-field';
import { dateRangeBounds, formatLocalDate, localDayBoundary } from '@/components/ui/date-field-math';
import { Field } from '@/components/ui/field';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { ThumbnailCell } from '@/components/common/thumbnail-cell';
import { useProductSchema } from '@/components/product-schema-provider';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { downloadAdminTaskAsset } from '@/lib/admin-utils';
import {
  AdministrationUserAssetsBrowserError,
  createAdministrationUserAssetsBrowserClient,
} from '@/lib/administration/user-assets-browser-client';
import {
  createAdministrationUsersBrowserClient,
} from '@/lib/administration/users-browser-client';
import {
  createAdministrationGenerationsBrowserClient,
} from '@/lib/administration/generations-browser-client';
import {
  schemaText,
  taskStatePresentation,
  type AdministrationProductSchema,
  type ProductSchema,
} from '@/lib/product-schema';
import { formatNativePrice } from '@/lib/pricing';

type UserAssetsSchema = AdministrationProductSchema['user_assets'];
type Tab = 'uploads' | 'generations' | 'downloads';

function template(
  source: string,
  values: Record<string, string | number>,
): string {
  let result = source;
  for (const [key, value] of Object.entries(values)) {
    result = result.replaceAll(`{${key}}`, String(value));
  }
  return result;
}

function truncate(value: string, maximum: number, suffix: string): string {
  return value.length > maximum
    ? `${value.slice(0, maximum)}${suffix}`
    : value;
}

function initialDate(daysAgo: number): string {
  const date = new Date();
  date.setDate(date.getDate() - daysAgo);
  return formatLocalDate(date);
}

function dateBoundary(value: string, endOfDay: boolean): string | undefined {
  return localDayBoundary(value, endOfDay)?.toISOString();
}

function parseResultURLs(value: string): string[] {
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed)
      ? parsed.filter((item): item is string => typeof item === 'string')
      : [];
  } catch {
    return [];
  }
}

function formatDateTime(
  value: string,
  presentation: UserAssetsSchema['presentation'],
): string {
  if (!value) return presentation.empty_placeholder;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return presentation.empty_placeholder;
  return date.toLocaleString(
    presentation.date_locale,
    presentation.date_time_options as Intl.DateTimeFormatOptions,
  );
}

function formatSize(
  bytes: number,
  presentation: UserAssetsSchema['presentation'],
): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return `0 ${presentation.size_units[0] ?? ''}`.trim();
  }
  const maximumIndex = presentation.size_units.length - 1;
  const rawIndex = Math.floor(
    Math.log(bytes) / Math.log(presentation.bytes_per_unit),
  );
  const index = Math.min(Math.max(rawIndex, 0), maximumIndex);
  const value = bytes / Math.pow(presentation.bytes_per_unit, index);
  const decimals = index === 0 ? 0 : presentation.size_decimal_places;
  return `${value.toFixed(decimals)} ${presentation.size_units[index] ?? ''}`.trim();
}

function oneParameterRoute(route: string, value: string): string | null {
  const tokens = route.match(/\{[^{}]+\}/g) ?? [];
  if (tokens.length !== 1) return null;
  return route.replaceAll(tokens[0]!, encodeURIComponent(value));
}

function generationPreviewSource(
  _schema: ProductSchema,
  thumbnailURL: string | null,
  resultURLs: string,
  _taskID: string,
): string | null {
  if (thumbnailURL && safeAssetURL(thumbnailURL)) return safeAssetURL(thumbnailURL);
  const first = parseResultURLs(resultURLs).find((url) => safeAssetURL(url));
  return first ? safeAssetURL(first) : null;
}

function downloadPreviewSource(
  schema: ProductSchema,
  resourceType: string,
  resourceID: string,
): string | null {
  if (!resourceID) return null;
  const userAssets = schema.administration.user_assets;
  if (
    resourceType === userAssets.filters.generated_image_resource_type
  ) {
    return oneParameterRoute(
      schema.runtime.http.legacy_routes.generated_image,
      resourceID,
    );
  }
  return safeAssetURL(resourceID) || null;
}

function userOptionLabel(
  schema: UserAssetsSchema,
  user: {
    username: string;
    nickname: string;
    name: string;
    email: string;
  },
): string {
  const text = schema.presentation.text;
  const name =
    user.nickname ||
    user.name ||
    user.username ||
    text.unnamed_user;
  return template(text.user_option_label, {
    name,
    identifier: user.email || user.username,
  });
}

function Pagination({
  page,
  total,
  pageSize,
  schema,
  onPageChange,
}: {
  page: number;
  total: number;
  pageSize: number;
  schema: UserAssetsSchema;
  onPageChange: (page: number) => void;
}) {
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  if (totalPages <= 1) return null;
  const text = schema.presentation.text;
  return (
    <div className="flex items-center justify-between pt-3 border-t mt-3">
      <Action
        variant="outline"
        size="sm"
        disabled={page <= schema.limits.default_page}
        onClick={() => onPageChange(page - 1)}
      >
        <ChevronLeft className="h-3.5 w-3.5 mr-1" />
        {text.pagination_previous}
      </Action>
      <span className="text-xs text-muted-foreground">
        {template(text.pagination_summary, {
          page,
          total_pages: totalPages,
          total,
        })}
      </span>
      <Action
        variant="outline"
        size="sm"
        disabled={page >= totalPages}
        onClick={() => onPageChange(page + 1)}
      >
        {text.pagination_next}
        <ChevronRight className="h-3.5 w-3.5 ml-1" />
      </Action>
    </div>
  );
}

export default function UserAssetsPage() {
  const { schema } = useProductSchema();
  const userAssetsSchema = schema.administration.user_assets;
  const presentation = userAssetsSchema.presentation;
  const text = presentation.text;
  const generationSchema = schema.administration.generations;
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
  const userAssetsClient = useMemo(
    () => createAdministrationUserAssetsBrowserClient({
      schema: userAssetsSchema,
      routes: {
        overview:
          schema.runtime.http.legacy_routes.admin_user_assets,
        asset: schema.runtime.http.legacy_routes.asset_item,
      },
    }),
    [schema.runtime.http.legacy_routes, userAssetsSchema],
  );
  const generationsClient = useMemo(
    () => createAdministrationGenerationsBrowserClient({
      schema: generationSchema,
      routes: {
        collection: schema.runtime.http.legacy_routes.admin_generations,
        item: schema.runtime.http.legacy_routes.admin_generation,
        download:
          schema.runtime.http.legacy_routes.admin_generation_download,
      },
    }),
    [generationSchema, schema.runtime.http.legacy_routes],
  );
  const [selectedUserId, setSelectedUserId] = useState('');
  const [searchKeyword, setSearchKeyword] = useState('');
  const [uploadsPage, setUploadsPage] = useState(
    userAssetsSchema.limits.default_page,
  );
  const [generationsPage, setGenerationsPage] = useState(
    userAssetsSchema.limits.default_page,
  );
  const [downloadsPage, setDownloadsPage] = useState(
    userAssetsSchema.limits.default_page,
  );
  const [from, setFrom] = useState(
    () => initialDate(userAssetsSchema.limits.default_recent_days),
  );
  const [to, setTo] = useState(() => initialDate(0));
  const [activeTab, setActiveTab] = useState<Tab>('uploads');
  const [downloadingTaskId, setDownloadingTaskId] =
    useState<string | null>(null);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  const usersKey = useMemo(
    () => [
      schema.runtime.http.legacy_routes.admin_users,
      schema.revision,
    ] as const,
    [
      schema.revision,
      schema.runtime.http.legacy_routes.admin_users,
    ],
  );
  const { data: users = [] } = useSWR(
    usersKey,
    () => usersClient.listUsers(),
    { revalidateOnFocus: false },
  );
  const normalizedKeyword = searchKeyword.toLowerCase();
  const filteredUsers = normalizedKeyword
    ? users.filter((user) =>
        [
          user.username,
          user.nickname,
          user.email,
          user.name,
        ].some((value) => value.toLowerCase().includes(normalizedKeyword)))
    : users;
  const selectedUser = users.find((user) => user.id === selectedUserId);
  const assetsKey = selectedUserId
    ? [
        schema.runtime.http.legacy_routes.admin_user_assets,
        selectedUserId,
        uploadsPage,
        generationsPage,
        downloadsPage,
        from,
        to,
      ] as const
    : null;
  const {
    data: assetsData,
    isLoading,
    error,
  } = useSWR(
    assetsKey,
    () => userAssetsClient.getUserAssets(selectedUserId, {
      uploadsPage,
      generationsPage,
      downloadsPage,
      pageSize: userAssetsSchema.limits.frontend_page_size,
      from: dateBoundary(from, false),
      to: dateBoundary(to, true),
    }),
    { revalidateOnFocus: false },
  );
  const resetPages = useCallback(() => {
    setUploadsPage(userAssetsSchema.limits.default_page);
    setGenerationsPage(userAssetsSchema.limits.default_page);
    setDownloadsPage(userAssetsSchema.limits.default_page);
  }, [userAssetsSchema.limits.default_page]);
  const handleSelectUser = (userID: string) => {
    setSelectedUserId(userID);
    resetPages();
  };
  const handleDownload = useCallback(async (taskID: string) => {
    setDownloadingTaskId(taskID);
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
    } catch (cause) {
      setDownloadError(
        cause instanceof Error
          ? cause.message
          : generationSchema.messages.download_failed,
      );
    } finally {
      setDownloadingTaskId(null);
    }
  }, [generationSchema, generationsClient]);
  const model3DCapabilities = useMemo(
    () => new Set(
      schema.capabilities
        .filter((capability) => capability.domain === 'model-3d')
        .map((capability) => capability.id),
    ),
    [schema.capabilities],
  );
  const successfulStates = useMemo(
    () => new Set(
      schema.task_states
        .filter((state) => state.outcome === 'success')
        .flatMap((state) => [
          state.internal_state,
          state.public_state,
        ]),
    ),
    [schema.task_states],
  );
  const tabs = assetsData
    ? [
        {
          key: 'uploads' as const,
          label: text.tab_uploads,
          icon: Upload,
          count: assetsData.summary.totalUploads,
        },
        {
          key: 'generations' as const,
          label: text.tab_generations,
          icon: Sparkles,
          count: assetsData.summary.totalGenerations,
        },
        {
          key: 'downloads' as const,
          label: text.tab_downloads,
          icon: History,
          count: assetsData.summary.totalDownloads,
        },
      ]
    : [];

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="pt-4 pb-4">
          <div className="flex flex-wrap items-end gap-3">
            <Field id="assets-user" label={text.select_user_label}>
              {(controlProps) => <Select
                value={selectedUserId}
                onValueChange={(value) => handleSelectUser(value ?? '')}
              >
                <SelectTrigger {...controlProps} className="w-[300px]">
                  {selectedUser ? (
                    <span className="truncate">
                      {userOptionLabel(userAssetsSchema, selectedUser)}
                    </span>
                  ) : (
                    <SelectValue
                      placeholder={text.select_user_placeholder}
                    />
                  )}
                </SelectTrigger>
                <SelectContent>
                  <div className="p-2 border-b">
                    <Input
                      aria-label={text.search_user_placeholder}
                      placeholder={text.search_user_placeholder}
                      value={searchKeyword}
                      onChange={(event) => setSearchKeyword(event.target.value)}
                      className="h-8"
                    />
                  </div>
                  {filteredUsers.map((user) => (
                    <SelectItem key={user.id} value={user.id}>
                      {userOptionLabel(userAssetsSchema, user)}
                    </SelectItem>
                  ))}
                  {filteredUsers.length === 0 && (
                    <div className="text-xs text-muted-foreground text-center py-4">
                      {text.no_matching_users}
                    </div>
                  )}
                </SelectContent>
              </Select>}
            </Field>
            <Field id="assets-start_date" label={text.start_date}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).start} value={from} onValueChange={setFrom} />)}
            </Field>
            <Field id="assets-end_date" label={text.end_date}>
              {(controlProps) => (<SchemaDateField {...controlProps} className="w-[150px]" {...dateRangeBounds(from, to).end} value={to} onValueChange={setTo} />)}
            </Field>
            <Action
              size="default"
              onClick={resetPages}
              disabled={!selectedUserId}
            >
              <Calendar className="h-4 w-4 mr-1.5" />
              {text.filter}
            </Action>
          </div>
          {downloadError ? <p role="alert" className="mt-3 text-sm text-destructive">{downloadError}</p> : null}
        </CardContent>
      </Card>

      {!selectedUserId && (
        <div className="flex flex-col items-center justify-center py-20 text-center">
          <Search className="h-12 w-12 text-muted-foreground/30 mb-4" />
          <h2 className="text-lg font-semibold">{text.unselected_title}</h2>
          <p className="text-sm text-muted-foreground mt-1">
            {text.unselected_description}
          </p>
        </div>
      )}

      {selectedUserId && isLoading && (
        <div className="space-y-4">
          {Array.from({ length: presentation.loading_rows }).map((_, index) => (
            <Skeleton
              key={index}
              className={index === 0 ? 'h-24 w-full' : 'h-64 w-full'}
            />
          ))}
        </div>
      )}

      {error instanceof AdministrationUserAssetsBrowserError &&
        error.status === httpStatusNotFound() && (
          <div className="text-center py-12 text-sm text-muted-foreground">
            {userAssetsSchema.messages.user_not_found}
          </div>
        )}

      {assetsData && (
        <>
          <Card>
            <CardContent className="pt-4 pb-4">
              <div className="flex flex-wrap items-center gap-6">
                <div>
                  <p className="text-sm font-medium">
                    {assetsData.user.nickname ||
                      assetsData.user.name ||
                      assetsData.user.username}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {assetsData.user.email}
                  </p>
                </div>
                <div className="flex gap-4 text-center">
                  <Surface variant="info" padding="sm" className="flex flex-col items-center">
                    <Upload className="h-4 w-4 text-primary mb-0.5" />
                    <span className="text-lg font-bold text-foreground">
                      {assetsData.summary.totalUploads}
                    </span>
                    <span className="text-[10px] text-muted-foreground">
                      {text.summary_upload}
                    </span>
                  </Surface>
                  <Surface variant="info" padding="sm" className="flex flex-col items-center">
                    <Sparkles className="h-4 w-4 text-primary mb-0.5" />
                    <span className="text-lg font-bold text-foreground">
                      {assetsData.summary.totalGenerations}
                    </span>
                    <span className="text-[10px] text-muted-foreground">
                      {text.summary_generation}
                    </span>
                  </Surface>
                  <Surface variant="success" padding="sm" className="flex flex-col items-center">
                    <Download className="h-4 w-4 text-success mb-0.5" />
                    <span className="text-lg font-bold text-success">
                      {assetsData.summary.totalDownloads}
                    </span>
                    <span className="text-[10px] text-muted-foreground">
                      {text.summary_download}
                    </span>
                  </Surface>
                </div>
                <div className="text-xs text-muted-foreground">
                  {template(text.summary_storage, {
                    size: formatSize(
                      assetsData.summary.totalStorageBytes,
                      presentation,
                    ),
                  })}
                </div>
              </div>
            </CardContent>
          </Card>

          <div className="flex gap-1 flex-wrap">
            {tabs.map(({ key, label, icon: Icon, count }) => (
              <Action
                key={key}
                variant={activeTab === key ? 'default' : 'ghost'}
                aria-pressed={activeTab === key}
                onClick={() => setActiveTab(key)}
              >
                <Icon className="h-4 w-4" />
                {label}
                <Badge
                  variant="secondary"
                  className="ml-1 text-[10px] px-1.5 py-0"
                >
                  {count}
                </Badge>
              </Action>
            ))}
          </div>

          {activeTab === 'uploads' && (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{text.uploads_title}</CardTitle>
              </CardHeader>
              <CardContent>
                {assetsData.uploads.data.length === 0 ? (
                  <p className="text-sm text-muted-foreground text-center py-8">
                    {text.uploads_empty}
                  </p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b text-left">
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_time}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_filename}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_type}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_size}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_preview}
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {assetsData.uploads.data.map((item) => {
                          const imageAsset =
                            item.assetType ===
                            userAssetsSchema.filters.image_asset_type;
                          const label =
                            presentation.asset_type_labels[item.assetType] ??
                            presentation.asset_type_labels[
                              userAssetsSchema.filters.model_asset_type
                            ] ??
                            presentation.empty_placeholder;
                          return (
                            <tr
                              key={item.id}
                              className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                            >
                              <td className="py-3 text-xs text-muted-foreground whitespace-nowrap">
                                {formatDateTime(item.createdAt, presentation)}
                              </td>
                              <td className="py-3">
                                <HintTooltip name={item.originalFilename}>
                                  <span className="text-xs">
                                    {truncate(
                                      item.originalFilename,
                                      presentation.filename_preview_characters,
                                      presentation.preview_suffix,
                                    )}
                                  </span>
                                </HintTooltip>
                              </td>
                              <td className="py-3">
                                <Badge
                                  variant="outline"
                                  className="text-[10px]"
                                >
                                  {imageAsset ? (
                                    <Image className="h-3 w-3 mr-1" />
                                  ) : (
                                    <Box className="h-3 w-3 mr-1" />
                                  )}
                                  {label}
                                </Badge>
                              </td>
                              <td className="py-3 text-xs text-muted-foreground tabular-nums">
                                {formatSize(item.fileSize, presentation)}
                              </td>
                              <td className="py-3">
                                {item.mimeType.startsWith(
                                  userAssetsSchema.filters.
                                    image_media_type_prefix,
                                ) && (
                                  <a
                                    href={userAssetsClient.assetURL(item.id)}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="text-xs text-primary hover:underline"
                                  >
                                    {text.view}
                                  </a>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                <Pagination
                  page={assetsData.uploads.page}
                  total={assetsData.uploads.total}
                  pageSize={assetsData.uploads.pageSize}
                  schema={userAssetsSchema}
                  onPageChange={setUploadsPage}
                />
              </CardContent>
            </Card>
          )}

          {activeTab === 'generations' && (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">
                  {text.generations_title}
                </CardTitle>
              </CardHeader>
              <CardContent>
                {assetsData.generations.data.length === 0 ? (
                  <p className="text-sm text-muted-foreground text-center py-8">
                    {text.generations_empty}
                  </p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b text-left">
                          <th className="pb-2 font-medium text-muted-foreground w-12">
                            {text.column_preview}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_time}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_task_type}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_task_id}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_status}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_credits}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_result}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_operation}
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {assetsData.generations.data.map((record) => {
                          const urls = parseResultURLs(record.resultUrls);
                          const state = taskStatePresentation(
                            schema,
                            record.taskStatus,
                          );
                          const packageDownload =
                            model3DCapabilities.has(record.taskType);
                          return (
                            <tr
                              key={record.id}
                              className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                            >
                              <td className="py-2">
                                <ThumbnailCell
                                  src={generationPreviewSource(
                                    schema,
                                    record.thumbnailUrl,
                                    record.resultUrls,
                                    record.taskId,
                                  )}
                                />
                              </td>
                              <td className="py-3 text-xs text-muted-foreground whitespace-nowrap">
                                {formatDateTime(record.createdAt, presentation)}
                              </td>
                              <td className="py-3 text-muted-foreground">
                                {record.taskType}
                              </td>
                              <td className="py-3 font-mono text-xs text-muted-foreground">
                                <HintTooltip name={record.taskId}>
                                  <span>
                                    {truncate(
                                      record.taskId,
                                      presentation.task_id_preview_characters,
                                      presentation.preview_suffix,
                                    )}
                                  </span>
                                </HintTooltip>
                              </td>
                              <td className="py-3">
                                <Badge
                                  variant="secondary"
                                  className={state.color}
                                >
                                  {record.taskStatus}
                                </Badge>
                              </td>
                              <td className="py-3 text-muted-foreground tabular-nums">
                                {record.nativeQuote !== undefined
                                  ? record.nativeQuote
                                    ? schemaText(schema, 'ui.pricing.frozen_estimate').replace('{amount}', formatNativePrice(record.nativeQuote.total_amount, record.nativeQuote.unit_label) ?? schemaText(schema, 'ui.pricing.unknown'))
                                    : schemaText(schema, 'ui.pricing.unknown')
                                  : record.estimatedCredits}
                              </td>
                              <td className="py-3">
                                {urls.length > 0 ? (
                                  <span className="text-xs text-primary">
                                    {template(text.result_files, {
                                      count: urls.length,
                                    })}
                                  </span>
                                ) : (
                                  <span className="text-xs text-muted-foreground">
                                    {presentation.empty_placeholder}
                                  </span>
                                )}
                              </td>
                              <td className="py-3">
                                {successfulStates.has(record.taskStatus) &&
                                  urls.length > 0 && (
                                    <HintTooltip
                                      name={
                                        packageDownload
                                          ? text.package_download_tooltip
                                          : text.download
                                      }
                                    >
                                      <Action
                                        variant="ghost"
                                        size="sm"
                                        loading={
                                          downloadingTaskId === record.taskId
                                        }
                                        onClick={() =>
                                          handleDownload(record.taskId)}
                                      >
                                        {packageDownload ? (
                                          <Package className="h-3.5 w-3.5 mr-1" />
                                        ) : (
                                          <Download className="h-3.5 w-3.5 mr-1" />
                                        )}
                                        {downloadingTaskId === record.taskId
                                          ? text.downloading
                                          : packageDownload
                                            ? text.package
                                            : text.download}
                                      </Action>
                                    </HintTooltip>
                                  )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                <Pagination
                  page={assetsData.generations.page}
                  total={assetsData.generations.total}
                  pageSize={assetsData.generations.pageSize}
                  schema={userAssetsSchema}
                  onPageChange={setGenerationsPage}
                />
              </CardContent>
            </Card>
          )}

          {activeTab === 'downloads' && (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">
                  {text.downloads_title}
                </CardTitle>
              </CardHeader>
              <CardContent>
                {assetsData.downloads.data.length === 0 ? (
                  <p className="text-sm text-muted-foreground text-center py-8">
                    {text.downloads_empty}
                  </p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b text-left">
                          <th className="pb-2 font-medium text-muted-foreground w-12">
                            {text.column_preview}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_time}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_action}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_resource_type}
                          </th>
                          <th className="pb-2 font-medium text-muted-foreground">
                            {text.column_resource_id}
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {assetsData.downloads.data.map((record) => {
                          const action =
                            userAssetsSchema.filters.download_actions.find(
                              (item) => item.action === record.action,
                            ) ??
                            userAssetsSchema.filters.download_actions.at(-1);
                          return (
                            <tr
                              key={record.id}
                              className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                            >
                              <td className="py-2">
                                <ThumbnailCell
                                  src={downloadPreviewSource(
                                    schema,
                                    record.resourceType,
                                    record.resourceId,
                                  )}
                                />
                              </td>
                              <td className="py-3 text-xs text-muted-foreground whitespace-nowrap">
                                {formatDateTime(record.createdAt, presentation)}
                              </td>
                              <td className="py-3">
                                {action && (
                                  <span
                                    className={`inline-flex items-center text-[11px] ${action.text_class}`}
                                  >
                                    <span
                                      className={`w-1.5 h-1.5 rounded-full mr-1.5 shrink-0 ${action.dot_class}`}
                                    />
                                    {action.label}
                                  </span>
                                )}
                              </td>
                              <td className="py-3 text-muted-foreground">
                                {record.resourceType ||
                                  presentation.empty_placeholder}
                              </td>
                              <td className="py-3 font-mono text-xs text-muted-foreground">
                                {record.resourceId ? (
                                  <HintTooltip name={record.resourceId}>
                                    <span>
                                      {truncate(
                                        record.resourceId,
                                        presentation.
                                          resource_id_preview_characters,
                                        presentation.preview_suffix,
                                      )}
                                    </span>
                                  </HintTooltip>
                                ) : (
                                  <span>{presentation.empty_placeholder}</span>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                <Pagination
                  page={assetsData.downloads.page}
                  total={assetsData.downloads.total}
                  pageSize={assetsData.downloads.pageSize}
                  schema={userAssetsSchema}
                  onPageChange={setDownloadsPage}
                />
              </CardContent>
            </Card>
          )}
        </>
      )}
    </div>
  );
}

function httpStatusNotFound(): number {
  return 404;
}
