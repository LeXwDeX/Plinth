import { Suspense, type ComponentType } from 'react';
import { usePathname } from '@/lib/browser-navigation';

import AdminLayout from '@/app/admin/layout';
import NotFound from '@/app/not-found';
import { reloadableLazy } from '@/components/common/reloadable-lazy';
import { MainLayout } from '@/components/layout/main-layout';
import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';
import { projectProductRoute } from '@/lib/administration/route-access';

// AGENTS 模式工作区是唯一的主创作入口：旧 per-capability 表单页与
// chat/canvas/tasks 页面已整体下线，双标签（图片/3D）工作区取而代之。
// #511: pages load through reloadableLazy: a tab opened before a release asks for the old page code, which is gone;
// it is tried once more and then the error boundary asks for a page reload instead of a retry that cannot work.
const WorkspacePage = reloadableLazy(() => import('@/app/workspace/page'));
const AccountPage = reloadableLazy(() => import('@/app/account/page'));
const LoginPage = reloadableLazy(() => import('@/app/login/page'));

const administrationPages: Record<string, ComponentType> = {
  'agent-configuration': reloadableLazy(() => import('@/app/admin/agent-configuration/page')),
  llm: reloadableLazy(() => import('@/app/admin/llm/page')),
  dashboard: reloadableLazy(() => import('@/app/admin/page')),
  users: reloadableLazy(() => import('@/app/admin/users/page')),
  audit: reloadableLazy(() => import('@/app/admin/audit/page')),
  generations: reloadableLazy(() => import('@/app/admin/generations/page')),
  'user-assets': reloadableLazy(() => import('@/app/admin/user-assets/page')),
  'access-keys': reloadableLazy(() => import('@/app/admin/access-keys/page')),
};

const administrableKeys = Object.keys(administrationPages);

export function ProductRouter() {
  const pathname = usePathname();
  const { schema } = useProductSchema();
  const { isAdmin } = useAuth();
  const route = projectProductRoute({
    schema,
    pathname,
    isAdmin,
    administrableKeys,
  });

  let page: React.ReactNode;
  if (route.kind === 'workspace') {
    page = <WorkspacePage />;
  } else if (route.kind === 'account') {
    page = <MainLayout sidebar={false}><AccountPage /></MainLayout>;
  } else if (route.kind === 'login') {
    page = <LoginPage />;
  } else if (route.kind === 'administration') {
    const AdministrationPage = administrationPages[route.key];
    page = (
      <MainLayout>
        <AdminLayout>
          <AdministrationPage />
        </AdminLayout>
      </MainLayout>
    );
  } else if (route.kind === 'forbidden') {
    // 管理 API 另有 RBAC 兜底；这里只保证非管理员看不到管理外壳与空数据表。
    page = <NotFound />;
  } else {
    page = <NotFound />;
  }

  return (
    <Suspense fallback={
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        {schema.authentication.login.loading_label}
      </div>
    }>
      {page}
    </Suspense>
  );
}
