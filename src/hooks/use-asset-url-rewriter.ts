'use client';

import { useMemo } from 'react';
import { useProductSchema } from '@/components/product-schema-provider';
import { createAssetURLRewriter } from '@/lib/core/tasks/asset-url-rewriter';

export function useAssetURLRewriter() {
  const { schema } = useProductSchema();
  return useMemo(() => createAssetURLRewriter(schema), [schema]);
}
