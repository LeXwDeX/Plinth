'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  type ReactNode,
} from 'react';
import useSWR from 'swr';
import {
  configureClientErrorReporting,
  installGlobalErrorReporting,
} from '@/lib/client-error-report';
import { activeLocale } from '@/lib/i18n/locale';
import { configureErrorReference } from '@/lib/problem-reference';
import {
  projectRuntimeProductSchema,
  schemaText,
  type ProductSchema,
} from '@/lib/product-schema';

interface ProductSchemaResponse {
  schema: ProductSchema;
}

interface RuntimeVersionResponse {
  version: string;
}

interface ProductSchemaContextValue {
  schema: ProductSchema;
  text: (key: string) => string;
  refreshing: boolean;
}

const ProductSchemaContext = createContext<ProductSchemaContextValue | null>(null);

const fetchJSON = async <T,>(url: string): Promise<T> => {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`);
  }
  return response.json();
};

export function ProductSchemaProvider({
  initialSchema,
  children,
}: {
  initialSchema: ProductSchema;
  children: ReactNode;
}) {
  const { data, isLoading } = useSWR<ProductSchemaResponse>(
    initialSchema.runtime.http.routes.product_schema,
    fetchJSON<ProductSchemaResponse>,
    {
      fallbackData: { schema: initialSchema },
      revalidateOnFocus: false,
      revalidateOnMount: true,
      keepPreviousData: true,
    },
  );
  const baseSchema = data?.schema ?? initialSchema;
  const { data: versionData, isLoading: versionLoading } =
    useSWR<RuntimeVersionResponse>(
      baseSchema.runtime.http.legacy_routes.version,
      fetchJSON<RuntimeVersionResponse>,
      {
        revalidateOnFocus: false,
        revalidateOnMount: true,
        refreshInterval:
          baseSchema.runtime.frontend.product_version_refresh_milliseconds,
      },
    );
  const schema = useMemo(
    () =>
      projectRuntimeProductSchema(
        baseSchema,
        initialSchema,
        versionData?.version,
      ),
    [baseSchema, initialSchema, versionData?.version],
  );
  // #233：浏览器错误按公开 Schema 声明的路由回传服务端日志。
  useEffect(() => {
    configureClientErrorReporting(
      schema.runtime.http.routes.client_errors,
      schema.content.service.version,
    );
  }, [schema.runtime.http.routes.client_errors, schema.content.service.version]);
  useEffect(() => installGlobalErrorReporting(), []);
  // `html lang` follows the projection's locale (screen readers, hyphenation, fonts).
  const locale = activeLocale(schema);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  useEffect(() => {
    configureErrorReference(schema.chat.presentation.messages.error_reference);
  }, [schema.chat.presentation.messages.error_reference]);
  const text = useCallback((key: string) => schemaText(schema, key), [schema]);
  const value = useMemo(
    () => ({ schema, text, refreshing: isLoading || versionLoading }),
    [schema, text, isLoading, versionLoading],
  );

  return (
    <ProductSchemaContext.Provider value={value}>
      {children}
    </ProductSchemaContext.Provider>
  );
}

export function useProductSchema(): ProductSchemaContextValue {
  const value = useContext(ProductSchemaContext);
  if (value === null) {
    throw new Error('useProductSchema requires ProductSchemaProvider');
  }
  return value;
}
