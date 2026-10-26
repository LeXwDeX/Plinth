'use client';

import { useMemo } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { fetchCapabilityOfferings, type OfferingWire } from '@/lib/agent-offerings';
import { parseTaskDetail, type TaskDetailWire } from '@/lib/workspace/task-detail';
import { findTaskState } from '@/lib/workspace/task-projection';

/** How often an unfinished task is re-read while its details are open. */
const LIVE_REFRESH_MS = 2000;

/**
 * The public task (state, times, stored input) and the offering that ran it (model name,
 * declared parameters, credits metering). Both are read through the existing routes; an
 * unfinished task is re-read while the dialog is open so its status moves on.
 */
export function useTaskDetail(taskId: string | undefined, capabilityHint?: string) {
  const { schema } = useProductSchema();
  const route = schema.runtime.http.routes.task_item;
  const task = useSWR<TaskDetailWire | null>(
    taskId ? ['task-detail', taskId] : null,
    async () => {
      const response = await fetch(route.replace('{taskID}', encodeURIComponent(taskId ?? '')));
      if (!response.ok) throw new Error(`task ${response.status}`);
      return parseTaskDetail(await response.json());
    },
    {
      revalidateOnFocus: false,
      refreshInterval: (data) => (data && findTaskState(schema.task_states, data.state)?.terminal ? 0 : LIVE_REFRESH_MS),
    },
  );
  const capability = task.data?.capability || capabilityHint || '';
  const offerings = useSWR<OfferingWire[]>(
    capability ? ['task-detail-offerings', capability] : null,
    () => fetchCapabilityOfferings(schema, capability),
    { revalidateOnFocus: false },
  );
  const offering = useMemo(
    () => offerings.data?.find((item) => item.id === task.data?.offeringId),
    [offerings.data, task.data?.offeringId],
  );
  return { task: task.data ?? null, offering, error: task.error as Error | undefined, isLoading: task.isLoading };
}
