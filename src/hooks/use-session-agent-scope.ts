'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { agentScopeOf, defaultAgentScope } from '@/lib/chat/agent-scope';
import { createChatBrowserClient, type ChatBrowserClient } from '@/lib/chat/browser-client';
import { useChatStore, type AgentScope } from '@/store/chat-store';

/** 当前会话的 Agent 范围。会话还没有加载出来或没有这个成员时，是 Schema 的默认模式。 */
export function useActiveAgentScope(): AgentScope {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const stored = useChatStore((state) => state.sessions.find((item) => item.id === activeSessionId)?.agentScope);
  return stored === undefined ? defaultAgentScope(schema) : agentScopeOf(schema, stored);
}

/**
 * 当前会话的 Agent 范围与切换。范围按会话保存在服务端；切换先在界面上生效，服务端
 * 没有接受就退回原来的值。它只影响之后发出的消息，已经开始的一轮保持开始时的范围。
 */
export function useSessionAgentScope(client: ChatBrowserClient | null, sessionId: string | null) {
  const { schema } = useProductSchema();
  const scope = useActiveAgentScope();
  const updateSessionAgentScope = useChatStore((state) => state.updateSessionAgentScope);
  const [busy, setBusy] = useState(false);

  // The session list may not carry the member (an older response, or a page that
  // was loaded before the switch), so the session's own value is read once.
  useEffect(() => {
    if (!client || !sessionId) return;
    const controller = new AbortController();
    client.getAgentScope(sessionId, controller.signal)
      .then((value) => updateSessionAgentScope(sessionId, value))
      .catch(() => undefined);
    return () => controller.abort();
  }, [client, sessionId, updateSessionAgentScope]);

  const setScope = useCallback(async (next: AgentScope): Promise<boolean> => {
    if (!client || !sessionId) return false;
    const previous = scope;
    updateSessionAgentScope(sessionId, next);
    setBusy(true);
    try {
      updateSessionAgentScope(sessionId, await client.setAgentScope(sessionId, next));
      return true;
    } catch {
      updateSessionAgentScope(sessionId, previous);
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, scope, sessionId, updateSessionAgentScope]);

  return useMemo(() => ({ scope, busy, setScope, defaultScope: defaultAgentScope(schema) }), [scope, busy, setScope, schema]);
}

/** 组件里直接创建浏览器客户端的便捷入口，与批准方式下拉一致。 */
export function useAgentScopeClient(): ChatBrowserClient {
  const { schema } = useProductSchema();
  return useMemo(() => createChatBrowserClient(schema), [schema]);
}
