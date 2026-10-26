'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import {
  countPending,
  DecisionLedger,
  decidePendingApproval,
  displayedApprovals,
  fetchPendingApprovals,
  pendingApprovalsRoute,
  retainExpired,
  type PendingApproval,
  type PendingApprovalDecision,
  type PolledApprovals,
} from '@/lib/chat/pending-approvals';

/** 过期与新建的待批准步骤都由服务端裁决；这里只定期重读，不在前端倒计时。 */
const POLL_MILLISECONDS = 20_000;

interface RetainedApprovals { sessionId: string | null; items: PendingApproval[] }

/**
 * 当前会话里脱离轮次仍待决的付费步骤。决定后立即重读列表，调用方可根据返回
 * 的 messageId 重新读取消息（服务端会插入"已批准 / 已拒绝"结果消息）。
 *
 * `items` 是要显示的卡片：服务端列表，加上本页看到过、随后因过期而不再列出的卡片
 * （以过期状态留在对话里，可"重新发起"）；`pendingCount` 只数仍可决定的。
 */
export function useSessionPendingApprovals(sessionId: string | null) {
  const { schema } = useProductSchema();
  const enabled = sessionId !== null && pendingApprovalsRoute(schema, sessionId) !== null;
  const { data, mutate } = useSWR<PolledApprovals>(
    enabled ? ['pending-approvals', sessionId] : null,
    async ([, id]: readonly [string, string]) => {
      const startedAt = Date.now();
      return { startedAt, items: await fetchPendingApprovals(schema, id) };
    },
    { refreshInterval: POLL_MILLISECONDS, revalidateOnFocus: true, keepPreviousData: false },
  );
  const previous = useRef<RetainedApprovals>({ sessionId, items: [] });
  // Cards this page decided: when one leaves the list it was decided, not expired.
  const decided = useRef(new Set<string>());
  // The same cards, until a read sent after the decision arrives: a poll that was
  // already on its way when the decision landed must not bring the card back.
  const ledger = useRef(new DecisionLedger());
  const [decisions, setDecisions] = useState(0);
  const [retained, setRetained] = useState<RetainedApprovals>({ sessionId, items: [] });
  // `decisions` makes a fresh decision hide its card at once, not at the next read.
  const listed = useMemo(() => (enabled && data ? ledger.current.visible(data) : null), [data, decisions, enabled]);
  useEffect(() => {
    if (!data) return;
    const visible = ledger.current.visible(data);
    ledger.current.settle(data);
    const seen = (previous.current.sessionId === sessionId ? previous.current.items : [])
      .filter((item) => !decided.current.has(item.requestId));
    previous.current = { sessionId, items: visible };
    setRetained((state) => {
      const base = state.sessionId === sessionId ? state.items : [];
      const next = retainExpired(seen, visible, base, Date.now());
      const unchanged = next.length === base.length && next.every((item, index) => item === base[index]);
      return unchanged && state.sessionId === sessionId ? state : { sessionId, items: next };
    });
  }, [data, sessionId]);
  const kept = retained.sessionId === sessionId ? retained.items : null;
  const items = useMemo(() => displayedApprovals(listed ?? [], kept ?? []), [listed, kept]);
  const decide = useCallback(async (requestId: string, approve: boolean): Promise<PendingApprovalDecision> => {
    try {
      const outcome = await decidePendingApproval(schema, requestId, approve);
      decided.current.add(requestId);
      ledger.current.markDecided(requestId, Date.now());
      setDecisions((count) => count + 1);
      return outcome;
    } finally {
      // 无论成功或失败（409 过期、已处理）都以服务端当前列表为准。
      void mutate();
    }
  }, [mutate, schema]);
  return { items, pendingCount: countPending(items, Date.now()), refresh: () => mutate(), decide };
}
