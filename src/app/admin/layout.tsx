'use client';

import Link from '@/components/ui/browser-link';
import { usePathname } from '@/lib/browser-navigation';
import { buttonVariants } from '@/components/ui/button';
import { BarChart3, Cpu, Users, FileText, Sparkles, FolderSearch } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';

const iconMap = {
  chart: BarChart3,
  users: Users,
  'file-text': FileText,
  sparkles: Sparkles,
  'folder-search': FolderSearch,
  cpu: Cpu,
};

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { schema } = useProductSchema();

  return (
    <div className="space-y-6">
      {/* Tab Navigation */}
      <nav className="flex flex-wrap gap-1">
        {schema.administration.navigation.map(({ href, label, icon, match }) => {
          const Icon = iconMap[icon as keyof typeof iconMap] ?? BarChart3;
          const isActive =
            match === 'exact' ? pathname === href : pathname.startsWith(href);
          return (
            <Link
              key={href}
              href={href}
              aria-current={isActive ? 'page' : undefined}
              className={buttonVariants({ variant: isActive ? 'default' : 'ghost' })}
            >
              <Icon className="h-4 w-4" />
              {label}
            </Link>
          );
        })}
      </nav>
      {children}
    </div>
  );
}
