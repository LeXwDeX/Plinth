'use client';

import { useMemo, useState, type FormEvent } from 'react';
import useSWR from 'swr';
import {
  ChevronLeft,
  ChevronRight,
  Search,
  Shield,
  Trash2,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Action } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { toast } from 'sonner';
import { useProductSchema } from '@/components/product-schema-provider';
import {
  AdministrationAccessKeysBrowserError,
  createAdministrationAccessKeysBrowserClient,
  type AdministrationAccessKey,
} from '@/lib/administration/access-keys-browser-client';
import { createAdministrationUsersBrowserClient } from '@/lib/administration/users-browser-client';
import { formatSchemaText } from '@/lib/product-schema';
import { withErrorReference } from '@/lib/problem-reference';

export default function AdminAccessKeysPage() {
  const { schema, text: productText } = useProductSchema();
  const accessKeysSchema = schema.administration.access_keys;
  const presentation = accessKeysSchema.presentation;
  const copy = presentation.text;
  const limits = accessKeysSchema.limits;
  const defaultPage = 1;
  const client = useMemo(
    () => createAdministrationAccessKeysBrowserClient({
      schema: accessKeysSchema,
      routes: {
        collection: schema.runtime.http.legacy_routes.admin_access_keys,
        item: schema.runtime.http.legacy_routes.admin_access_key,
        switch: schema.runtime.http.legacy_routes.admin_access_keys_switch,
      },
    }),
    [accessKeysSchema, schema.runtime.http.legacy_routes],
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

  const [page, setPage] = useState(defaultPage);
  const [searchInput, setSearchInput] = useState('');
  const [search, setSearch] = useState('');
  const [ownerID, setOwnerID] = useState('');
  const [pendingSwitch, setPendingSwitch] = useState<boolean | null>(null);
  const [switching, setSwitching] = useState(false);
  const [revokingKey, setRevokingKey] =
    useState<AdministrationAccessKey | null>(null);
  const [revoking, setRevoking] = useState(false);

  const text = (
    key: string,
    variables: Record<string, string | number> = {},
  ) => formatSchemaText(copy[key], variables);

  const errorText = (caught: unknown): string =>
    caught instanceof Error && caught.message
      ? caught.message
      : accessKeysSchema.messages.internal_error;

  const { data: users = [] } = useSWR(
    ['administration-access-keys-owners', schema.revision],
    () => usersClient.listUsers(),
    { revalidateOnFocus: false },
  );
  const {
    data,
    isLoading,
    error,
    mutate,
  } = useSWR(
    [
      'administration-access-keys',
      schema.revision,
      page,
      limits.default_page_size,
      search,
      ownerID,
    ],
    () => client.list({
      page,
      pageSize: limits.default_page_size,
      ...(search ? { search } : {}),
      ...(ownerID ? { ownerID } : {}),
    }),
    { revalidateOnFocus: false, keepPreviousData: true },
  );

  const rows = data?.data ?? [];
  const total = data?.total ?? 0;
  const effectivePageSize = data?.pageSize || limits.default_page_size;
  const totalPages = Math.max(1, Math.ceil(total / effectivePageSize));
  const enabled = data?.enabled === true;
  const placeholder = presentation.empty_placeholder;
  const formatDate = (value: string | null): string => {
    if (!value) return placeholder;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return placeholder;
    return new Intl.DateTimeFormat(
      presentation.date_locale,
      presentation.date_time_options as Intl.DateTimeFormatOptions,
    ).format(date);
  };
  const scopeLabels = schema.access_keys?.scopes ?? [];
  const scopeLabel = (value: string): string =>
    scopeLabels.find((scope) => scope.value === value)?.label ?? value;
  const ownerLabel = (accessKey: AdministrationAccessKey): string =>
    accessKey.ownerName ||
    accessKey.ownerUsername ||
    accessKey.ownerId ||
    placeholder;
  const ownerOptions = users.map((user) => ({
    id: user.id,
    label: user.nickname || user.name || user.username || user.id,
  }));

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPage(defaultPage);
    setSearch(searchInput.trim());
  };

  const handleRevoke = async () => {
    if (!revokingKey) return;
    setRevoking(true);
    try {
      await client.revoke(revokingKey.id);
      toast.success(text('revoked'));
      setRevokingKey(null);
      const remaining = (data?.data.length ?? 0) - 1;
      await mutate();
      if (remaining === 0 && page > defaultPage) {
        setPage(page - 1);
      }
    } catch (caught) {
      toast.error(withErrorReference(errorText(caught), caught));
    } finally {
      setRevoking(false);
    }
  };

  const handleSwitch = async () => {
    if (pendingSwitch === null) return;
    setSwitching(true);
    try {
      const state = await client.setEnabled(pendingSwitch);
      toast.success(text(state ? 'switched_on' : 'switched_off'));
      setPendingSwitch(null);
      await mutate();
    } catch (caught) {
      toast.error(withErrorReference(errorText(caught), caught));
    } finally {
      setSwitching(false);
    }
  };

  if (
    error instanceof AdministrationAccessKeysBrowserError &&
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

  const statusLabel = (accessKey: AdministrationAccessKey): string =>
    accessKey.active
      ? text('status_active')
      : accessKey.revokedAt
        ? text('status_revoked')
        : text('status_expired');

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{text('switch_title')}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-center justify-between gap-4">
            <p className="text-sm text-muted-foreground">
              {text('switch_description')}
            </p>
            <Switch
              checked={enabled}
              disabled={!data || switching}
              aria-label={text('switch_title')}
              onCheckedChange={(value) => setPendingSwitch(value)}
            />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex flex-wrap items-center justify-between gap-2">
            <span>{text('title')}</span>
            <span className="text-sm font-normal text-muted-foreground">
              {text('total_summary', { total })}
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="mb-4 flex flex-wrap items-end gap-3">
            <form onSubmit={submitSearch} className="flex items-end gap-2">
              <Field
                id="access-keys-search"
                label={text('search_placeholder')}
                labelHidden
              >
                {(controlProps) => (<Input
                  {...controlProps}
                  value={searchInput}
                  maxLength={limits.search_max_length}
                  placeholder={text('search_placeholder')}
                  onChange={(event) => setSearchInput(event.target.value)}
                />)}
              </Field>
              <Action type="submit" size="default">
                <Search className="h-4 w-4 mr-1.5" />
                {text('search')}
              </Action>
            </form>
            <Field id="access-keys-owner" label={text('column_owner')}>
              {(controlProps) => (<Select
                value={ownerID}
                onValueChange={(value) => {
                  setOwnerID(value ?? '');
                  setPage(defaultPage);
                }}
              >
                <SelectTrigger {...controlProps} className="w-[220px]">
                  <span>
                    {ownerOptions.find((owner) => owner.id === ownerID)?.label ??
                      text('all_owners')}
                  </span>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">{text('all_owners')}</SelectItem>
                  {ownerOptions.map((owner) => (
                    <SelectItem key={owner.id} value={owner.id}>
                      {owner.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>)}
            </Field>
          </div>

          {error ? (
            <p role="alert" className="mb-3 text-sm text-destructive">
              {withErrorReference(errorText(error), error)}
            </p>
          ) : null}

          {isLoading ? (
            <div className="space-y-3">
              {Array.from(
                { length: presentation.loading_rows },
                (_, index) => (
                  <Skeleton key={index} className="h-10 w-full" />
                ),
              )}
            </div>
          ) : rows.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-12">
              {text('empty')}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left">
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_owner')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_name')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_prefix')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_scopes')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_created_at')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_last_used_at')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_request_count')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_expires_at')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_revoked_at')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-center">
                      {text('column_status')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_actions')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((accessKey) => (
                    <tr
                      key={accessKey.id}
                      className="border-b last:border-0 hover:bg-muted/30 transition-colors"
                    >
                      <td className="py-3">
                        {ownerLabel(accessKey)}
                        {accessKey.ownerUsername &&
                          accessKey.ownerName &&
                          accessKey.ownerUsername !== accessKey.ownerName ? (
                          <span className="text-xs text-muted-foreground ml-1">
                            {accessKey.ownerUsername}
                          </span>
                        ) : null}
                      </td>
                      <td className="py-3 font-medium">
                        {accessKey.name || placeholder}
                      </td>
                      <td className="py-3 font-mono text-xs text-muted-foreground">
                        {accessKey.prefix || placeholder}
                      </td>
                      <td className="py-3">
                        {accessKey.scopes.length > 0
                          ? accessKey.scopes.map(scopeLabel).join(', ')
                          : placeholder}
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground whitespace-nowrap">
                        {formatDate(accessKey.createdAt)}
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground whitespace-nowrap">
                        {formatDate(accessKey.lastUsedAt)}
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground">
                        {accessKey.requestCount}
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground whitespace-nowrap">
                        {formatDate(accessKey.expiresAt)}
                      </td>
                      <td className="py-3 text-right text-xs text-muted-foreground whitespace-nowrap">
                        {formatDate(accessKey.revokedAt)}
                      </td>
                      <td className="py-3 text-center">
                        {accessKey.active ? (
                          <Badge
                            variant="outline"
                            className="text-success border-success/30 bg-success/10"
                          >
                            {statusLabel(accessKey)}
                          </Badge>
                        ) : (
                          <Badge
                            variant="outline"
                            className="text-muted-foreground border-border"
                          >
                            {statusLabel(accessKey)}
                          </Badge>
                        )}
                      </td>
                      <td className="py-3 text-right">
                        <Action
                          variant="ghost"
                          size="icon-sm"
                          disabled={!accessKey.active || revoking}
                          aria-label={text('revoke')}
                          onClick={() => setRevokingKey(accessKey)}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-destructive" />
                        </Action>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {totalPages > 1 ? (
            <div className="flex items-center justify-between pt-4 border-t mt-4">
              <Action
                variant="outline"
                size="sm"
                disabled={page <= defaultPage}
                onClick={() => setPage(page - 1)}
              >
                <ChevronLeft className="h-3.5 w-3.5 mr-1" />
                {text('pagination_previous')}
              </Action>
              <span className="text-xs text-muted-foreground">
                {text('pagination_summary', {
                  page,
                  total_pages: totalPages,
                  total,
                })}
              </span>
              <Action
                variant="outline"
                size="sm"
                disabled={page >= totalPages}
                onClick={() => setPage(page + 1)}
              >
                {text('pagination_next')}
                <ChevronRight className="h-3.5 w-3.5 ml-1" />
              </Action>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Dialog
        open={revokingKey !== null}
        onOpenChange={(open) => {
          if (!open) setRevokingKey(null);
        }}
      >
        <DialogContent
          closeLabel={productText('ui.dialog.close')}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>{text('revoke')}</DialogTitle>
            <DialogDescription>{text('revoke_confirm')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Action
              variant="outline"
              onClick={() => setRevokingKey(null)}
            >
              {text('cancel')}
            </Action>
            <Action
              variant="destructive"
              onClick={handleRevoke}
              loading={revoking}
            >
              {text('revoke')}
            </Action>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={pendingSwitch !== null}
        onOpenChange={(open) => {
          if (!open) setPendingSwitch(null);
        }}
      >
        <DialogContent
          closeLabel={productText('ui.dialog.close')}
          className="sm:max-w-md"
        >
          <DialogHeader>
            <DialogTitle>{text('switch_title')}</DialogTitle>
            <DialogDescription>{text('switch_description')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Action
              variant="outline"
              onClick={() => setPendingSwitch(null)}
            >
              {text('cancel')}
            </Action>
            <Action
              variant={pendingSwitch ? 'default' : 'destructive'}
              onClick={handleSwitch}
              loading={switching}
            >
              {text(pendingSwitch ? 'switch_on' : 'switch_off')}
            </Action>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}