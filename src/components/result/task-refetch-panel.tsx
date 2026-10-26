'use client';

import { useLayoutEffect, useRef, useState } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';

import { Action } from '@/components/ui/button';
import { useProductSchema } from '@/components/product-schema-provider';
import { schemaCountMessage, schemaMessage, schemaText } from '@/lib/product-schema';
import { fetchRefetchEligibility, refetchUnavailableText, startRefetch, type RefetchEligibility } from '@/lib/workspace/task-refetch';
import { useWorkspaceStore } from '@/store/workspace-store';

type RefetchPanelProps = {
  taskId: string;
  sessionId: string;
  capability: string;
  prompt: string;
  onOpenTask: (taskId: string, sessionId: string, capability: string) => void;
};

// TaskRefetchPanel offers the owner of a failed task to fetch its already
// charged provider result again (#295). It renders nothing when the task did
// not fail after its provider finished; the server decides everything else.
export function TaskRefetchPanel(props: RefetchPanelProps) {
  const { schema } = useProductSchema();
  // A return to the same task is a new mounted authority; an old request never resumes it.
  return <ScopedRefetchPanel key={JSON.stringify([props.sessionId, props.taskId, schema.revision, schema.runtime.http.routes.task_refetch])} {...props} />;
}

function ScopedRefetchPanel({ taskId, sessionId, capability, prompt, onOpenTask }: RefetchPanelProps) {
  const { schema } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const enabled = Boolean(schema.runtime.http.routes.task_refetch && taskId);
  const eligibility = useSWR<RefetchEligibility>(
    enabled ? ['task-refetch', sessionId, taskId] : null,
    () => fetchRefetchEligibility(schema, taskId),
    { revalidateOnFocus: true, revalidateOnReconnect: true, shouldRetryOnError: false },
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const authority = useRef({ active: false });
  useLayoutEffect(() => {
    const mounted = { active: true };
    authority.current = mounted;
    return () => { mounted.active = false; };
  }, []);

  if (!enabled) return null;
  const t = (key: string) => schemaText(schema, key);
  const view = eligibility.data;
  if (eligibility.error) {
    return <p className="text-xs text-muted-foreground" role="status" data-testid="task-refetch-check-failed">{t('ui.task.refetch.check_failed')}</p>;
  }
  if (!view || view.reason === 'not_applicable') return null;

  const open = (id: string) => onOpenTask(id, sessionId, capability);
  const begin = async () => {
    if (busy) return;
    const mounted = authority.current;
    setBusy(true); setError('');
    try {
      const result = await startRefetch(schema, taskId);
      if (!mounted.active) return;
      void eligibility.mutate(result.eligibility, false);
      if (!result.taskId) return; // the server explained why; the reason renders below
      if (!result.replayed) {
        registerTask({ taskId: result.taskId, sessionId, capability, prompt, progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
        toast.success(t('ui.task.refetch.started'));
      }
      open(result.taskId);
    } catch (cause) {
      if (mounted.active) setError(schemaMessage(schema, 'ui.task.refetch.start_failed', { reason: cause instanceof Error ? cause.message : '' }));
    } finally { if (mounted.active) setBusy(false); }
  };

  // A delivery that failed after the provider charged says how much (#330).
  const charged = view.chargedCredits ? (
    <span className="block" data-testid="task-refetch-charged">{schemaCountMessage(schema, 'ui.task.refetch.charged', view.chargedCredits, { credits: view.chargedCredits })}</span>
  ) : null;
  // A refetch that already delivered (#330) is opened below; any other ineligible view says why.
  if (!view.eligible && !(view.reason === 'delivered' && view.refetchTaskId)) {
    return (
      <p className="rounded-md border border-border px-3 py-2 text-xs text-muted-foreground" role="note" data-testid="task-refetch-unavailable" data-reason={view.reason}>
        {charged}
        {refetchUnavailableText(schema, view)}
      </p>
    );
  }
  const existing = view.refetchTaskId;
  return (
    <section className="flex flex-col gap-2 rounded-md border border-border bg-muted/30 p-3" aria-label={t('ui.task.refetch.action')} data-testid="task-refetch-panel">
      <p className="text-xs text-muted-foreground">
        {existing ? null : charged}
        {existing ? t(view.refetchState === 'SUCCEEDED' ? 'ui.task.refetch.succeeded' : 'ui.task.refetch.in_progress') : t('ui.task.refetch.description')}
      </p>
      {error ? <p className="text-xs text-destructive" role="alert" data-testid="task-refetch-error">{error}</p> : null}
      {existing ? (
        <Action type="button" variant="outline" size="sm" onClick={() => open(existing)} data-testid="task-refetch-open">{t('ui.task.refetch.open')}</Action>
      ) : (
        <Action type="button" size="sm" disabled={busy} onClick={() => void begin()} data-testid="task-refetch-start">
          {busy ? t('ui.task.refetch.starting') : t('ui.task.refetch.action')}
        </Action>
      )}
    </section>
  );
}
