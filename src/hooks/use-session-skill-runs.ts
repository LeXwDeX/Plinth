'use client';

import useSWR from 'swr';
import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';

export type SkillStepPhase = 'planned' | 'prepared' | 'grant_pending' | 'task_bound' | 'output_ready';

export interface SessionSkillStep {
  key: string;
  label: string;
  phase: SkillStepPhase;
  taskId: string | null;
}

export interface SessionSkillRun {
  id: string;
  sessionId: string;
  steps: SessionSkillStep[];
}

const phases = new Set<SkillStepPhase>(['planned', 'prepared', 'grant_pending', 'task_bound', 'output_ready']);

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

/** Only display server steps bound to the requested session and a known phase. */
export function projectSessionSkillRuns(response: unknown, sessionId: string): SessionSkillRun[] {
  const raw = record(response)?.runs;
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((value): SessionSkillRun[] => {
    const run = record(value);
    if (!run || typeof run.id !== 'string' || run.session_id !== sessionId || !Array.isArray(run.steps)) return [];
    const steps = run.steps.flatMap((value): SessionSkillStep[] => {
      const step = record(value);
      const spec = record(step?.spec);
      if (!step || !spec || typeof spec.key !== 'string' || typeof spec.label !== 'string' ||
          typeof step.phase !== 'string' || !phases.has(step.phase as SkillStepPhase)) return [];
      return [{
        key: spec.key,
        label: spec.label,
        phase: step.phase as SkillStepPhase,
        taskId: typeof step.task_id === 'string' && step.task_id ? step.task_id : null,
      }];
    });
    return [{ id: run.id, sessionId, steps }];
  });
}

export function useSessionSkillRuns(sessionId: string | null) {
  const { schema } = useProductSchema();
  const { user } = useAuth();
  const route = schema.runtime.http.routes.skill_runs;
  const url = route && sessionId
    ? `${route}?session_id=${encodeURIComponent(sessionId)}&limit=${schema.runtime.frontend.session_menu_page_size}`
    : null;
  const { data, error, isLoading, mutate } = useSWR<unknown>(
    user?.id && url ? [url, user.id, sessionId] as const : null,
    async ([requestURL]) => {
      const response = await fetch(requestURL);
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const body: unknown = await response.json();
      if (!Array.isArray(record(body)?.runs)) throw new Error('Invalid SkillRun response');
      return body;
    },
    { refreshInterval: schema.runtime.frontend.task_polling_interval_milliseconds },
  );
  return {
    runs: sessionId ? projectSessionSkillRuns(data, sessionId) : [],
    error,
    isLoading,
    /** Reads the list again (the 重试 after a failed load). */
    reload: () => { void mutate(); },
  };
}
