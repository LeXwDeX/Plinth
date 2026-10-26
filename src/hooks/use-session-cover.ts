'use client';

import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { coverOfTasks, type SessionCover } from '@/lib/workspace/session-cover';

const RECENT_TASKS = 6;

/**
 * 一个会话的封面。只在行出现时读一次最近的几个成功任务；会话有新活动（updatedAt 变了）
 * 才重新读。读不到就没有封面，行里显示占位图标。
 */
export function useSessionCover(sessionId: string, updatedAt: string): SessionCover | null {
  const { schema } = useProductSchema();
  const route = schema.runtime.http.routes.tasks;
  const successStates = schema.task_states.filter((state) => state.outcome === 'success').map((state) => state.internal_state);
  const { data } = useSWR<SessionCover | null>(
    route && successStates.length > 0 ? ['session-cover', sessionId, updatedAt, successStates.join(',')] : null,
    async () => {
      const params = new URLSearchParams({ session_id: sessionId, limit: String(RECENT_TASKS) });
      for (const state of successStates) params.append('state', state);
      const response = await fetch(`${route}?${params.toString()}`);
      if (!response.ok) return null;
      return coverOfTasks(await response.json().catch(() => null));
    },
    { revalidateOnFocus: false },
  );
  return data ?? null;
}
