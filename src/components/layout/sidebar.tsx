'use client';

import Link from '@/components/ui/browser-link';
import { usePathname } from '@/lib/browser-navigation';
import { Action, buttonVariants } from '@/components/ui/button';
import { Surface } from '@/components/ui/surface';
import { cn } from '@/lib/utils';
import { Box, Settings, Sparkles, User, X, type LucideIcon } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';
import { schemaText, selectNavigation } from '@/lib/product-schema';

/** Schema 导航项的图标词汇表。新增图标须先在此登记，否则测试会失败。 */
const navigationIcons: Record<string, LucideIcon> = {
  settings: Settings,
  sparkles: Sparkles,
  user: User,
};

interface SidebarProps {
  open: boolean;
  onClose: () => void;
  isAdmin?: boolean;
}

/**
 * AGENTS 工作区重建后，主导航（旧 per-capability 表单页 / chat / tasks /
 * canvas）已整体下线，入口收敛到共用顶栏 AppTopbar；Sidebar 只服务管理后台外壳，
 * 渲染主导航（回到工作台）与 bottom 管理入口，构成工作区↔后台往返。
 * 品牌和版本号由 AppTopbar 显示，Sidebar 不再重复。
 */
export function Sidebar({ open, onClose, isAdmin }: SidebarProps) {
  const pathname = usePathname();
  const { schema } = useProductSchema();
  const mainItems = selectNavigation(schema, 'main', !!isAdmin);
  const bottomItems = selectNavigation(schema, 'bottom', !!isAdmin);

  return (
    <>
      {/* Mobile overlay */}
      {open && (
        <div
          className="fixed inset-0 z-scrim bg-black/50 lg:hidden"
          onClick={onClose}
        />
      )}

      {/* Sidebar */}
      <Surface as="aside" variant="panel" radius="none"
        className={cn(
          'fixed inset-y-0 left-0 z-modal flex w-60 flex-col transition-transform duration-150 ease-in-out lg:static lg:translate-x-0',
          open ? 'translate-x-0' : '-translate-x-full'
        )}
      >
        {/* 品牌和版本号在共用顶栏里；这里只保留小屏关闭按钮 */}
        <div className="flex justify-end px-3 pt-3 lg:hidden">
          <Action variant="ghost" size="icon-sm"
            onClick={onClose}
            aria-label={schemaText(schema, 'ui.dialog.close')}
          >
            <X className="h-5 w-5" />
          </Action>
        </div>

        {/* Main navigation — 回到创作工作台 */}
        {mainItems.length > 0 && (
          <nav className="px-3 py-3">
            {mainItems.map((item) => (
              <SidebarLink
                key={item.href}
                href={item.href}
                label={item.label}
                icon={item.icon}
                active={pathname === item.href}
              />
            ))}
          </nav>
        )}

        {/* Bottom admin entry — 仅管理员可见 */}
        {bottomItems.length > 0 && (
          <nav className="mt-auto border-t border-sidebar-border px-3 py-3">
            {bottomItems.map((item) => (
              <SidebarLink
                key={item.href}
                href={item.href}
                label={item.label}
                icon={item.icon}
                active={pathname.startsWith(item.href)}
              />
            ))}
          </nav>
        )}
      </Surface>
    </>
  );
}

function SidebarLink({
  href,
  label,
  icon,
  active,
}: {
  href: string;
  label: string;
  icon: string;
  active: boolean;
}) {
  const Icon = navigationIcons[icon] ?? Box;
  return (
    <Link
      href={href}
      aria-current={active ? 'page' : undefined}
      className={cn(
        buttonVariants({ variant: active ? 'default' : 'ghost' }),
        'w-full justify-start'
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span>{label}</span>
    </Link>
  );
}
