'use client';

import { useState, Suspense } from 'react';
import { ArrowLeft, Menu } from 'lucide-react';
import { usePathname } from '@/lib/browser-navigation';
import { useAuth } from '@/hooks/use-auth';
import { useProductSchema } from '@/components/product-schema-provider';
import type { ProductSchema } from '@/lib/product-schema';
import Link from '@/components/ui/browser-link';
import { Action, buttonVariants } from '@/components/ui/button';
import { Surface } from '@/components/ui/surface';
import { ConnectivityBanner } from './connectivity-banner';
import { AppTopbar } from './app-topbar';
import { CacheActivityIndicator } from './cache-activity';
import { Sidebar } from './sidebar';

/**
 * 外壳服务管理区和我的资源页（工作区自带全屏外壳）；登录页不带导航。
 * 顶栏和工作区是同一个 AppTopbar。管理区保留左侧导航；
 * 我的资源页是独立页面，不显示左侧导航，只在顶栏放一个返回创作工作台的入口。
 */
export function projectMainLayoutMode(
  pathname: string,
  schema: ProductSchema,
) {
  return {
    showChrome: pathname !== schema.runtime.frontend.client_routes.login,
  };
}

interface MainLayoutProps {
  children: React.ReactNode;
  /** false 时不渲染左侧导航，顶栏改为显示返回创作工作台的入口。 */
  sidebar?: boolean;
}

export function MainLayout({ children, sidebar = true }: MainLayoutProps) {
  const pathname = usePathname();
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const { schema, text } = useProductSchema();
  const { isAdmin } = useAuth();
  const mode = projectMainLayoutMode(pathname, schema);

  if (!mode.showChrome) {
    return <>{children}</>;
  }

  const menuToggle = sidebar ? (
    <Action variant="ghost" size="icon-sm"
      onClick={() => setSidebarOpen(!sidebarOpen)}
      className="lg:hidden"
      aria-label={text('ui.header.menu_toggle')}
    >
      <Menu className="h-5 w-5" />
    </Action>
  ) : null;
  const backToWorkspace = sidebar ? null : (
    <Link
      href={schema.runtime.frontend.client_routes.home}
      className={buttonVariants({ variant: 'ghost', size: 'sm' })}
      data-testid="back-to-workspace"
    >
      <ArrowLeft className="h-4 w-4" />
      {text('ui.account.back_to_workspace')}
    </Link>
  );

  return (
    <Surface variant="base" radius="none" className="flex h-dvh flex-col overflow-hidden">
      <ConnectivityBanner />
      <AppTopbar start={menuToggle} leading={backToWorkspace} actions={<CacheActivityIndicator />} />
      <div className="flex min-h-0 flex-1 overflow-hidden">
        {sidebar ? (
          <Suspense>
            <Sidebar open={sidebarOpen} onClose={() => setSidebarOpen(false)} isAdmin={isAdmin} />
          </Suspense>
        ) : null}
        <main className="flex-1 min-h-0 overflow-y-auto p-6">
          {children}
        </main>
      </div>
    </Surface>
  );
}
