import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import { SWRConfig } from 'swr';

import '@/app/globals.css';
import { AppErrorBoundary } from '@/components/common/app-error-boundary';
import { ProductSchemaProvider } from '@/components/product-schema-provider';
import { installRequestIdTracking } from '@/lib/client-error-report';
import { activeLocale } from '@/lib/i18n/locale';
import { swrPollingConfig } from '@/lib/polling';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { AuthenticationGate } from './authentication-gate';
import { ProductRouter } from './router';

declare global {
  interface Window {
    __ART_AI_PRODUCT_SCHEMA__?: ProductSchema;
    /** Set by public/browser-support.js when the browser is below the supported minimum (#387). */
    __PLINTH_UNSUPPORTED_BROWSER__?: boolean;
  }
}

// Remember each response's request ID from the very first request on, so an error report can name the request
// that came just before it (#387).
installRequestIdTracking();

const schema = window.__ART_AI_PRODUCT_SCHEMA__;
if (!schema) {
  throw new Error('Go Web Host did not inject Product Schema');
}

// public/browser-support.js has already drawn its notice in a browser below the supported minimum; starting the
// app would replace that with a page which cannot work there.
if (!window.__PLINTH_UNSUPPORTED_BROWSER__) {
  document.documentElement.lang = activeLocale(schema);
  document.title = schema.content.brand.metadata_title;
  document.documentElement.classList.add('dark');

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      {/* #385: every repeating read stands still in a hidden tab and after the login ended. */}
      <SWRConfig value={swrPollingConfig}>
        <ProductSchemaProvider initialSchema={schema}>
          <AppErrorBoundary>
            <AuthenticationGate>
              <ProductRouter />
            </AuthenticationGate>
          </AppErrorBoundary>
          <Toaster
            position={schema.runtime.frontend.toaster.position}
            richColors={schema.runtime.frontend.toaster.rich_colors}
            duration={schema.runtime.frontend.toaster.duration_milliseconds}
            containerAriaLabel={schemaText(schema, 'ui.notifications.region')}
          />
        </ProductSchemaProvider>
      </SWRConfig>
    </StrictMode>,
  );
}
