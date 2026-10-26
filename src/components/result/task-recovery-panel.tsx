'use client';

import { useLayoutEffect, useRef, useState } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';

import { Action } from '@/components/ui/button';
import { useProductSchema } from '@/components/product-schema-provider';
import { submitDirectTask, type PendingDirectTask } from '@/components/workspace/canvas-image-actions';
import { fetchRecoveryEligibility, prepareRecovery, type RecoveryEligibility, type RecoveryGrant } from '@/lib/workspace/task-recovery';
import { useWorkspaceStore } from '@/store/workspace-store';
import type { ProductSchema } from '@/lib/product-schema';

function storageKey(sessionId: string, sourceTaskId: string): string {
  return `art-ai:asset-recovery:${encodeURIComponent(sessionId)}:${encodeURIComponent(sourceTaskId)}`;
}

function readPending(sessionId: string, sourceTaskId: string): RecoveryGrant | 'denied' | null {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(storageKey(sessionId, sourceTaskId)) ?? 'null');
    if (value === 'denied') return 'denied';
    if (!value || typeof value !== 'object') return null;
    const grant = value as Partial<RecoveryGrant>;
    if (grant.sessionId !== sessionId || !grant.preparedInputId || !grant.requestId || !grant.key || !grant.offeringId || !grant.capability || typeof grant.grantApproved !== 'boolean') return null;
    return grant as RecoveryGrant;
  } catch { return null; }
}

function savePending(sessionId: string, sourceTaskId: string, grant: RecoveryGrant | 'denied' | null): void {
  try {
    const key = storageKey(sessionId, sourceTaskId);
    if (grant) sessionStorage.setItem(key, JSON.stringify(grant));
    else sessionStorage.removeItem(key);
  } catch { /* Recovery can still proceed in this mounted panel when session storage is unavailable. */ }
}

function recoveryLabel(schema: ProductSchema, key: string): string {
  const recovery = schema.task_history.asset_recovery;
  return recovery ? recovery[key as keyof typeof recovery] ?? '' : '';
}

type RecoveryPanelProps = {
  taskId: string;
  sessionId: string;
  capability: string;
  prompt: string;
  onOpenTask: (taskId: string, sessionId: string, capability: string) => void;
};

export function TaskRecoveryPanel(props: RecoveryPanelProps) {
  const { schema } = useProductSchema();
  // A return to the same task creates a new mounted authority, never reactivating an old request.
  const identity = JSON.stringify([props.sessionId, props.taskId, props.capability, props.prompt, schema.revision, Boolean(schema.task_history.asset_recovery), schema.runtime.http.routes]);
  return <ScopedRecoveryPanel key={identity} {...props} />;
}

function ScopedRecoveryPanel({ taskId, sessionId, capability, prompt, onOpenTask }: RecoveryPanelProps) {
  const { schema } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const route = schema.runtime.http.routes.task_recovery;
  const labels = schema.task_history.asset_recovery;
  const enabled = Boolean(route && schema.runtime.http.routes.task_recovery_prepare && labels && taskId && sessionId);
  const eligibility = useSWR<RecoveryEligibility | null>(
    enabled ? ['task-recovery', sessionId, taskId] : null,
    () => fetchRecoveryEligibility(schema, taskId),
    { revalidateOnFocus: true, revalidateOnReconnect: true, shouldRetryOnError: false },
  );
  const [stored, setStored] = useState<RecoveryGrant | 'denied' | null>(() => typeof window === 'undefined' ? null : readPending(sessionId, taskId));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const authority = useRef({ active: false });
  useLayoutEffect(() => {
    const mounted = { active: true };
    authority.current = mounted;
    return () => { mounted.active = false; };
  }, []);

  if (!enabled) return null;
  const pending = stored === 'denied' ? null : stored;
  const t = (key: string) => recoveryLabel(schema, key);

  const submit = async (grant: RecoveryGrant, isActive: () => boolean) => {
    const pendingTask: PendingDirectTask = {
      preparedInputId: grant.preparedInputId, requestId: grant.requestId, key: grant.key,
      sessionId: grant.sessionId, offeringId: grant.offeringId, prompt: grant.prompt,
      grantApproved: grant.grantApproved, approvedByPolicy: grant.approvedByPolicy,
    };
    const newTaskId = await submitDirectTask(schema.runtime.http.routes, pendingTask, isActive);
    if (!isActive()) return;
    if (!newTaskId) throw new Error('recovery task response malformed');
    savePending(sessionId, taskId, null);
    setStored(null);
    registerTask({ taskId: newTaskId, sessionId, capability: grant.capability, prompt, progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
    onOpenTask(newTaskId, sessionId, grant.capability);
    toast.success(t('submitted'));
  };

  const begin = async () => {
    if (!eligibility.data?.eligible || busy) return;
    const mounted = authority.current;
    const isActive = () => mounted.active;
    if (!isActive()) return;
    setBusy(true); setError('');
    try {
      const result = await prepareRecovery(schema, taskId, eligibility.data, prompt, isActive);
      if (!isActive()) return;
      if (result.kind === 'existing_task') {
        eligibility.mutate({ ...eligibility.data, recoveryTaskId: result.taskId }, false);
        onOpenTask(result.taskId, eligibility.data.sessionId ?? sessionId, eligibility.data.capability ?? capability);
        return;
      }
      if (result.kind === 'denied') {
        savePending(sessionId, taskId, 'denied');
        setStored('denied');
        return;
      }
      if (!result.grant.grantApproved) {
        savePending(sessionId, taskId, result.grant);
        setStored(result.grant);
        return;
      }
      await submit(result.grant, isActive);
    } catch (cause) {
      if (isActive()) setError(cause instanceof Error && cause.message ? cause.message : t('prepare_failed'));
    } finally { if (isActive()) setBusy(false); }
  };

  const decide = async (decision: 'approve' | 'deny') => {
    if (!pending || busy) return;
    const mounted = authority.current;
    const isActive = () => mounted.active;
    if (!isActive()) return;
    setBusy(true); setError('');
    try {
      if (decision === 'deny') {
        const response = await fetch(schema.runtime.http.routes.task_grant_decision.replace('{requestID}', encodeURIComponent(pending.requestId)), {
          method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ decision: 'deny' }),
        });
        if (!isActive()) return;
        if (!response.ok) throw new Error(`grant decision ${response.status}`);
        savePending(sessionId, taskId, 'denied'); setStored('denied');
        return;
      }
      await submit({ ...pending, grantApproved: false }, isActive);
    } catch (cause) {
      if (isActive()) setError(cause instanceof Error && cause.message ? cause.message : t('submit_failed'));
    } finally { if (isActive()) setBusy(false); }
  };

  const existingTaskId = eligibility.data?.recoveryTaskId;
  if (stored === 'denied') return null;
  if (eligibility.isLoading && !eligibility.data && !pending) return <p className="text-xs text-muted-foreground" role="status" data-testid="task-recovery-loading">{t('eligibility_loading')}</p>;
  if (eligibility.error && !pending) return <div className="flex flex-col gap-2 rounded-md border border-border p-3" data-testid="task-recovery-error"><p className="text-xs text-muted-foreground" role="alert">{t('eligibility_failed')}</p><Action type="button" variant="outline" size="sm" onClick={() => void eligibility.mutate()}>{t('action')}</Action></div>;
  if (!eligibility.data?.eligible && !pending) return null;

  return (
    <section className="flex flex-col gap-2 rounded-md border border-border bg-muted/30 p-3" aria-label={t('action')} data-testid="task-recovery-panel">
      <p className="text-xs text-muted-foreground">{t('description')}</p>
      {error ? <p className="text-xs text-destructive" role="alert" data-testid="task-recovery-error-message">{error}</p> : null}
      {existingTaskId ? (
        <Action type="button" variant="outline" size="sm" disabled={busy} onClick={() => onOpenTask(existingTaskId, eligibility.data?.sessionId ?? sessionId, eligibility.data?.capability ?? capability)} data-testid="task-recovery-open-existing">{t('open_recovery_task')}</Action>
      ) : pending ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs" role="status">{t('awaiting_approval')}</span>
          <Action type="button" size="sm" disabled={busy} onClick={() => void decide('approve')} data-testid="task-recovery-approve">{busy ? t('submitting') : t('approve')}</Action>
          <Action type="button" variant="outline" size="sm" disabled={busy} onClick={() => void decide('deny')} data-testid="task-recovery-deny">{t('deny')}</Action>
        </div>
      ) : (
        <Action type="button" size="sm" disabled={busy} onClick={() => void begin()} data-testid="task-recovery-prepare">{busy ? t('preparing') : t('action')}</Action>
      )}
    </section>
  );
}
