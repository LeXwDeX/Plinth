'use client';

import { useMemo, useState } from 'react';
import useSWR from 'swr';
import {
  Shield,
  UserPlus,
  Pencil,
  UserX,
  UserCheck,
  Trash2,
} from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Action } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { Input } from '@/components/ui/input';
import { Field } from '@/components/ui/field';
import { Surface } from '@/components/ui/surface';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { toast } from 'sonner';
import { useProductSchema } from '@/components/product-schema-provider';
import {
  AdministrationUsersBrowserError,
  createAdministrationUsersBrowserClient,
  type AdministrationUser,
} from '@/lib/administration/users-browser-client';
import { formatSchemaText } from '@/lib/product-schema';

interface FormData {
  username: string;
  password: string;
  name: string;
  email: string;
  isAdmin: boolean;
}

const emptyForm: FormData = {
  username: '',
  password: '',
  name: '',
  email: '',
  isAdmin: false,
};

export default function AdminUsersPage() {
  const { schema, text: productText } = useProductSchema();
  const usersSchema = schema.administration.users;
  const presentation = schema.administration.users.presentation;
  const copy = presentation.text;
  const client = useMemo(
    () => createAdministrationUsersBrowserClient({
      schema: usersSchema,
      authenticationAPI: schema.authentication.api,
      routes: {
        collection: schema.runtime.http.legacy_routes.admin_users,
        item: schema.runtime.http.legacy_routes.admin_user,
        session: schema.runtime.http.legacy_routes.auth_session,
      },
    }),
    [schema, usersSchema],
  );
  const {
    data: users = [],
    isLoading,
    error,
    mutate,
  } = useSWR(
    ['administration-users', schema.revision],
    () => client.listUsers(),
    { revalidateOnFocus: false },
  );
  const { data: currentUserId = null } = useSWR(
    ['administration-current-user', schema.revision],
    () => client.currentUserID(),
    { revalidateOnFocus: false },
  );

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<AdministrationUser | null>(
    null,
  );
  const [form, setForm] = useState<FormData>(emptyForm);
  const [saving, setSaving] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [deletingUser, setDeletingUser] =
    useState<AdministrationUser | null>(null);
  const [deleting, setDeleting] = useState(false);

  const text = (
    key: string,
    variables: Record<string, string | number> = {},
  ) => formatSchemaText(copy[key], variables);

  const formatDate = (value: string): string =>
    new Intl.DateTimeFormat(
      presentation.date_locale,
      presentation.date_time_options as Intl.DateTimeFormatOptions,
    ).format(new Date(value));

  const openCreate = () => {
    setEditingUser(null);
    setForm(emptyForm);
    setDialogOpen(true);
  };

  const openEdit = (user: AdministrationUser) => {
    setEditingUser(user);
    setForm({
      username: user.username,
      password: '',
      name: user.name,
      email: user.email,
      isAdmin: user.isAdmin,
    });
    setDialogOpen(true);
  };

  const handleSave = async () => {
    const minimum = usersSchema.limits.password_minimum_characters;
    if (form.password && Array.from(form.password).length < minimum) {
      toast.error(text('password_too_short', { minimum }));
      return;
    }
    setSaving(true);
    try {
      if (editingUser) {
        await client.updateUser(editingUser.id, {
          name: form.name,
          email: form.email,
          isAdmin: form.isAdmin,
          ...(form.password ? { password: form.password } : {}),
        });
        toast.success(text('updated'));
      } else {
        await client.createUser(form);
        toast.success(text('created'));
      }
      setDialogOpen(false);
      await mutate();
    } catch (caught) {
      toast.error(
        caught instanceof Error ? caught.message : text('operation_failed'),
      );
    } finally {
      setSaving(false);
    }
  };

  const toggleActive = async (user: AdministrationUser) => {
    const action = text(user.isActive ? 'deactivate' : 'activate');
    try {
      await client.updateUser(user.id, { isActive: !user.isActive });
      toast.success(text('active_changed', {
        action,
        user: user.username || user.name,
      }));
      await mutate();
    } catch (caught) {
      toast.error(
        caught instanceof Error ? caught.message : text('operation_failed'),
      );
    }
  };

  const openDelete = (user: AdministrationUser) => {
    setDeletingUser(user);
    setDeleteDialogOpen(true);
  };

  const handleDelete = async () => {
    if (!deletingUser) return;
    setDeleting(true);
    try {
      await client.deleteUser(deletingUser.id);
      toast.success(text('deleted', {
        user: deletingUser.username || deletingUser.name,
      }));
      setDeleteDialogOpen(false);
      setDeletingUser(null);
      await mutate();
    } catch (caught) {
      toast.error(
        caught instanceof Error ? caught.message : text('delete_failed'),
      );
    } finally {
      setDeleting(false);
    }
  };

  if (
    error instanceof AdministrationUsersBrowserError &&
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

  const placeholder = presentation.empty_placeholder;
  const activeUsers = users.filter((user) => user.isActive).length;
  const passwordMinimum = usersSchema.limits.password_minimum_characters;
  const usernameMinimum = usersSchema.limits.username_minimum_characters;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {text('count_summary', {
            total: users.length,
            active: activeUsers,
          })}
        </p>
        <Action size="sm" onClick={openCreate}>
          <UserPlus className="h-4 w-4 mr-1.5" />
          {text('add_user')}
        </Action>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{text('list_title')}</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {Array.from(
                { length: presentation.loading_rows },
                (_, index) => (
                  <Skeleton key={index} className="h-12 w-full" />
                ),
              )}
            </div>
          ) : users.length === 0 ? (
            <p className="text-sm text-muted-foreground text-center py-8">
              {text('empty')}
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left">
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_username')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_name')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground">
                      {text('column_email')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-center">
                      {text('column_role')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-center">
                      {text('column_status')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_created_at')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_last_login')}
                    </th>
                    <th className="pb-2 font-medium text-muted-foreground text-right">
                      {text('column_actions')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((user) => {
                    const ownUser = user.id === currentUserId;
                    return (
                      <tr key={user.id} className="border-b last:border-0">
                        <td className="py-3 font-medium">
                          {user.username || placeholder}
                        </td>
                        <td className="py-3">
                          {user.name || user.nickname || placeholder}
                        </td>
                        <td className="py-3 text-muted-foreground">
                          {user.email || placeholder}
                        </td>
                        <td className="py-3 text-center">
                          {user.isAdmin ? (
                            <Badge
                              variant="default"
                              className="bg-primary"
                            >
                              {text('role_admin')}
                            </Badge>
                          ) : (
                            <Badge variant="secondary">
                              {text('role_user')}
                            </Badge>
                          )}
                        </td>
                        <td className="py-3 text-center">
                          {user.isActive ? (
                            <Badge
                              variant="outline"
                              className="text-success border-success/30 bg-success/10"
                            >
                              {text('status_active')}
                            </Badge>
                          ) : (
                            <Badge
                              variant="outline"
                              className="text-muted-foreground border-border"
                            >
                              {text('status_inactive')}
                            </Badge>
                          )}
                        </td>
                        <td className="py-3 text-right text-xs text-muted-foreground">
                          {formatDate(user.createdAt)}
                        </td>
                        <td className="py-3 text-right text-xs text-muted-foreground">
                          {user.lastLogin
                            ? formatDate(user.lastLogin)
                            : placeholder}
                        </td>
                        <td className="py-3 text-right">
                          <div className="flex items-center justify-end gap-1">
                            <Action
                              variant="ghost"
                              size="icon-sm"
                              onClick={() => openEdit(user)}
                              aria-label={text('edit')}
                            >
                              <Pencil className="h-3.5 w-3.5" />
                            </Action>
                            <Action
                              variant="ghost"
                              size="icon-sm"
                              onClick={() => toggleActive(user)}
                              disabled={ownUser}
                              aria-label={ownUser
                                ? text('self_deactivate')
                                : text(user.isActive
                                  ? 'deactivate'
                                  : 'activate')}
                            >
                              {user.isActive ? (
                                <UserX className="h-3.5 w-3.5 text-destructive" />
                              ) : (
                                <UserCheck className="h-3.5 w-3.5 text-success" />
                              )}
                            </Action>
                            <Action
                              variant="ghost"
                              size="icon-sm"
                              onClick={() => openDelete(user)}
                              disabled={ownUser}
                              aria-label={ownUser
                                ? text('self_delete')
                                : text('delete_user')}
                            >
                              <Trash2 className="h-3.5 w-3.5 text-destructive" />
                            </Action>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent closeLabel={productText('ui.dialog.close')} className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {text(editingUser ? 'edit_title' : 'create_title')}
            </DialogTitle>
            <DialogDescription>
              {text(
                editingUser ? 'edit_description' : 'create_description',
              )}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <Field id="dlg-username" label={text('username_label')}>
              {(controlProps) => (<Input
                {...controlProps}
                value={form.username}
                onChange={(event) => setForm((current) => ({
                  ...current,
                  username: event.target.value,
                }))}
                placeholder={text('username_placeholder', {
                  minimum: usernameMinimum,
                })}
                disabled={!!editingUser}
              />)}
            </Field>
            <Field id="dlg-password" label={text(editingUser ? 'new_password_label' : 'password_label')}>
              {(controlProps) => (<Input
                {...controlProps}
                type="password"
                value={form.password}
                onChange={(event) => setForm((current) => ({
                  ...current,
                  password: event.target.value,
                }))}
                placeholder={text(
                  editingUser
                    ? 'new_password_placeholder'
                    : 'password_placeholder',
                  { minimum: passwordMinimum },
                )}
              />)}
            </Field>
            <Field id="dlg-name" label={text('name_label')}>
              {(controlProps) => (<Input
                {...controlProps}
                value={form.name}
                onChange={(event) => setForm((current) => ({
                  ...current,
                  name: event.target.value,
                }))}
                placeholder={text('name_placeholder')}
              />)}
            </Field>
            <Field id="dlg-email" label={text('email_label')}>
              {(controlProps) => (<Input
                {...controlProps}
                type="email"
                value={form.email}
                onChange={(event) => setForm((current) => ({
                  ...current,
                  email: event.target.value,
                }))}
                placeholder={text('email_placeholder')}
              />)}
            </Field>
            <Surface variant="panel" padding="md">
              <Field id="dlg-admin" label={text('admin_label')} help={text('admin_description')}>
                {(controlProps) => <Switch
                  {...controlProps}
                  checked={form.isAdmin}
                  onCheckedChange={(value) => setForm((current) => ({
                    ...current,
                    isAdmin: value,
                  }))}
                />}
              </Field>
            </Surface>
          </div>
          <DialogFooter>
            <Action
              variant="outline"
              onClick={() => setDialogOpen(false)}
            >
              {text('cancel')}
            </Action>
            <Action onClick={handleSave} loading={saving}>
              {text(saving ? 'saving' : 'save')}
            </Action>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <DialogContent closeLabel={productText('ui.dialog.close')} className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{text('delete_title')}</DialogTitle>
            <DialogDescription>{text('delete_description')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Action
              variant="outline"
              onClick={() => {
                setDeleteDialogOpen(false);
                setDeletingUser(null);
              }}
            >
              {text('cancel')}
            </Action>
            <Action
              variant="destructive"
              onClick={handleDelete}
              loading={deleting}
            >
              {text(deleting ? 'deleting' : 'delete_confirm')}
            </Action>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
