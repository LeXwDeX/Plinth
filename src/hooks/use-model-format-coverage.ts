'use client';

import useSWR from 'swr';

import { parseModelFormatCoverage, stepsInProgress, type ModelFormatCoverage } from '@/lib/workspace/model-formats';

/**
 * A task's format list from the server (computed from the Product Schema's
 * model_delivery). The formats panel and the result detail read the same SWR
 * entry, so the detail knows which files the list already offers (#189).
 * `route` null reads nothing.
 */
export function useModelFormatCoverage(route: string | null) {
  return useSWR<ModelFormatCoverage | null>(
    route,
    async (target: string) => {
      const response = await fetch(target);
      if (!response.ok) throw new Error(`formats ${response.status}`);
      return parseModelFormatCoverage(await response.json());
    },
    {
      revalidateOnFocus: false,
      // 补齐进行中时轮询，完成后文件自然出现在清单里。
      refreshInterval: (latest) => (latest && stepsInProgress(latest) ? 3000 : 0),
    },
  );
}
