'use client';

import type { ReactNode } from 'react';
import { Box, LogOut, Settings2, UserRound } from 'lucide-react';
import { toast } from 'sonner';

import { LocaleMenuGroup } from '@/components/common/locale-switcher';
import { useProductSchema } from '@/components/product-schema-provider';
import Link from '@/components/ui/browser-link';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { WorkspaceBalance } from '@/components/workspace/workspace-balance';
import { useAuth } from '@/hooks/use-auth';
import { createAuthenticationBrowserClient } from '@/lib/authentication/browser-client';
import { schemaText } from '@/lib/product-schema';
import { HintTooltip } from '@/components/ui/hint-tooltip';

function UserMenu() {
  const { schema } = useProductSchema();
  const { user, isAdmin } = useAuth();
  const adminEntry = isAdmin
    ? schema.administration.navigation.find((item) => item.match === 'exact') ?? schema.administration.navigation[0]
    : undefined;
  const accountEntry = schema.content.navigation.find((item) => item.key === 'account');
  const logoutLabel = schemaText(schema, 'ui.workspace.user.logout');
  const initial = (user?.name ?? schemaText(schema, 'ui.dashboard.fallback_initial')).slice(0, 1);

  const logout = async () => {
    try {
      await createAuthenticationBrowserClient(schema).logout();
    } catch {
      // Logging out still returns to the sign-in page when the request fails.
    } finally {
      window.location.href = schema.runtime.frontend.client_routes.login;
    }
  };

  return (
    <DropdownMenu>
      <HintTooltip name={user?.name ?? ''}>
        <DropdownMenuTrigger
          className="flex size-8 items-center justify-center rounded-full border border-input bg-panel-raised text-xs font-semibold text-foreground transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
          aria-label={user?.name ?? schemaText(schema, 'ui.studio.user.menu')}
          data-testid="app-user-menu"
        >
          {initial}
        </DropdownMenuTrigger>
      </HintTooltip>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuGroup>
          {accountEntry ? (
            <DropdownMenuItem render={<Link href={accountEntry.href} />}>
              <UserRound className="size-4" />
              {accountEntry.label}
            </DropdownMenuItem>
          ) : null}
          {adminEntry ? (
            <DropdownMenuItem render={<a href={adminEntry.href} />}>
              <Settings2 className="size-4" />
              {adminEntry.label}
            </DropdownMenuItem>
          ) : null}
        </DropdownMenuGroup>
        <LocaleMenuGroup />
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem
            onClick={() => {
              logout().catch(() => toast.error(logoutLabel));
            }}
          >
            <LogOut className="size-4" />
            {logoutLabel}
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

interface AppTopbarProps {
  /** Controls before the brand, such as the menu toggle that opens the admin navigation on small screens. */
  start?: ReactNode;
  /** Page-specific controls next to the brand, such as the session menu or a way back. */
  leading?: ReactNode;
  /** Page-specific controls before the balance and the user menu. */
  actions?: ReactNode;
  testId?: string;
}

/**
 * 全站唯一的 48px 顶栏：左边是品牌和页面自己的入口，右边是页面操作、余额和用户菜单。
 * 工作台、我的资源和管理后台都渲染这一个组件，所以高度、品牌和右侧区域始终一致。
 */
export function AppTopbar({ start, leading, actions, testId = 'app-topbar' }: AppTopbarProps) {
  const { schema } = useProductSchema();
  const version = schema.content.service.version;
  return (
    <header
      className="relative z-sticky flex h-12 shrink-0 items-center gap-3 border-b border-border bg-panel px-3"
      data-testid={testId}
    >
      {start}
      <HintTooltip name={version ? `${schema.content.brand.version_prefix}${version}` : ''} className="shrink-0">
        <Link
          href={schema.runtime.frontend.client_routes.home}
          className="flex shrink-0 items-center gap-2"
        >
          <span className="flex size-6 items-center justify-center rounded-md bg-primary text-primary-foreground" aria-hidden="true">
            <Box className="size-3.5" />
          </span>
          <span className="hidden text-sm font-semibold tracking-tight md:inline">{schema.content.brand.title}</span>
        </Link>
      </HintTooltip>
      {leading ? (
        <>
          <span className="h-5 w-px shrink-0 bg-border" aria-hidden="true" />
          {leading}
        </>
      ) : null}
      <div className="ml-auto flex shrink-0 items-center gap-1">
        {actions}
        <WorkspaceBalance />
        <UserMenu />
      </div>
    </header>
  );
}
