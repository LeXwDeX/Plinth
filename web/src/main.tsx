import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';

import '@/app/globals.css';
import { AppErrorBoundary } from '@/components/common/app-error-boundary';
import { ProductSchemaProvider } from '@/components/product-schema-provider';
import { activeLocale } from '@/lib/i18n/locale';
import type { ProductSchema } from '@/lib/product-schema';
import { AuthenticationGate } from './authentication-gate';
import { ProductRouter } from './router';

declare global {
  interface Window {
    __ART_AI_PRODUCT_SCHEMA__?: ProductSchema;
  }
}

const schema = window.__ART_AI_PRODUCT_SCHEMA__;
if (!schema) {
  throw new Error('Go Web Host did not inject Product Schema');
}

document.documentElement.lang = activeLocale(schema);
document.title = schema.content.brand.metadata_title;
document.documentElement.classList.add('dark');

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ProductSchemaProvider initialSchema={schema}>
      <AppErrorBoundary>
        <AuthenticationGate>
          <ProductRouter />
        </AuthenticationGate>
      </AppErrorBoundary>
      <Toaster
        position={schema.runtime.frontend.toaster.position}
        richColors={schema.runtime.frontend.toaster.rich_colors}
      />
    </ProductSchemaProvider>
  </StrictMode>,
);
