'use client';

import { useMemo, useState } from 'react';
import { useSearchParams } from '@/lib/browser-navigation';
import { Suspense } from 'react';
import { AlertTriangle, Box, Eye, EyeOff, Loader2 } from 'lucide-react';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Field } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Surface } from '@/components/ui/surface';
import { LocaleSelect } from '@/components/common/locale-switcher';
import { useProductSchema } from '@/components/product-schema-provider';
import {
  AuthenticationBrowserError,
  createAuthenticationBrowserClient,
  safeRedirectPath,
} from '@/lib/authentication/browser-client';

function LoginForm() {
  const { schema } = useProductSchema();
  const login = schema.authentication.login;
  const messages = schema.authentication.messages;
  const searchParams = useSearchParams();
  const redirect = safeRedirectPath(
    searchParams.get(login.redirect_query),
    login.default_redirect,
  );
  const authentication = useMemo(
    () => createAuthenticationBrowserClient(schema),
    [schema],
  );

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const version = schema.content.service.version;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      await authentication.login(username, password);
      window.location.href = redirect;
    } catch (cause: unknown) {
      setError(
        cause instanceof AuthenticationBrowserError
          ? cause.message
          : messages.network_error,
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <Surface variant="base" radius="none" className="relative flex min-h-screen items-center justify-center overflow-hidden px-4">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -top-40 left-1/2 h-[480px] w-[720px] -translate-x-1/2 rounded-full bg-primary/25 blur-[140px]"
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -bottom-52 right-[-160px] h-[420px] w-[560px] rounded-full bg-primary/10 blur-[140px]"
      />
      <div className="absolute right-4 top-4 w-36">
        <LocaleSelect />
      </div>
      <main className="relative w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex size-14 items-center justify-center rounded-2xl bg-gradient-to-br from-primary to-ai-mark shadow-lg shadow-primary/30">
            <Box className="size-7 text-primary-foreground" />
          </div>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">
              {login.title}
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {login.description}
            </p>
          </div>
        </div>
        <Surface
          as="form"
          variant="frosted"
          radius="xl"
          padding="xl"
          elevation="xl"
          onSubmit={handleSubmit}
          className="space-y-4"
        >
          <Field id="username" label={login.username_label}>
            {(control) => <Input
              {...control}
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={login.username_placeholder}
              autoComplete="username"
              autoFocus
              required
            />}
          </Field>
          <Field id="password" label={login.password_label}>
            {(control) => <div className="relative">
              <Input
                {...control}
                type={passwordVisible ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={login.password_placeholder}
                autoComplete="current-password"
                className="pr-9"
                required
              />
              <HintTooltip name={passwordVisible ? login.hide_password : login.show_password} className="absolute inset-y-0 right-0">
                <Action
                  type="button"
                  variant="ghost"
                  size="icon-lg"
                  onClick={() => setPasswordVisible((value) => !value)}
                  aria-label={passwordVisible ? login.hide_password : login.show_password}
                  className="text-muted-foreground"
                >
                  {passwordVisible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </Action>
              </HintTooltip>
            </div>}
          </Field>
          {error ? (
            <Surface
              variant="danger"
              padding="md"
              role="alert"
              className="flex items-start gap-2 text-sm"
            >
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              <span>{error}</span>
            </Surface>
          ) : null}
          <Action type="submit" className="h-9 w-full" loading={loading}>
            {loading ? login.submitting_label : login.submit_label}
          </Action>
          <p className="text-center text-xs text-muted-foreground/60">
            {version
              ? `${schema.content.brand.version_prefix}${version}`
              : null}
          </p>
        </Surface>
      </main>
    </Surface>
  );
}

export default function LoginPage() {
  const { schema } = useProductSchema();
  return (
    <Suspense
      fallback={
        <Surface variant="base" radius="none" className="flex min-h-screen items-center justify-center">
          <p className="text-sm text-muted-foreground">
            {schema.authentication.login.loading_label}
          </p>
        </Surface>
      }
    >
      <LoginForm />
    </Suspense>
  );
}
