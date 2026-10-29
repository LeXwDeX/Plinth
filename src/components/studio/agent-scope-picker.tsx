'use client';

import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { TablePopover } from '@/components/studio/table-popover';
import { useAgentScopeClient, useSessionAgentScope } from '@/hooks/use-session-agent-scope';
import { agentScopeOptions } from '@/lib/chat/agent-scope';
import { schemaText } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';

/**
 * 输入区右下角的 Agent 模式选择：2D 模式 / 3D 模式 / 自动模式。它限定 Agent 能用的功能和
 * 操作范围（服务端按它给 Agent 的工具清单，并拒绝范围之外的调用），Agent 自己也知道这个选择。
 * 和模型选择器同一种锚定小表格；宽度由所在格子决定，不随当前值变化。
 */
export function AgentScopePicker() {
  const { schema } = useProductSchema();
  const client = useAgentScopeClient();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const { scope, busy, setScope } = useSessionAgentScope(client, activeSessionId);
  const options = agentScopeOptions(schema);
  if (options.length === 0) return null;
  const current = options.find((option) => option.scope === scope) ?? options[0];
  const label = schemaText(schema, 'ui.studio.agent_scope.label');

  return (
    <TablePopover
      testId="workspace-agent-scope-picker"
      label={label}
      value={schemaText(schema, `ui.studio.agent_scope.short_${current.scope}`)}
      fullValue={current.label}
      disabled={!activeSessionId || busy}
      sections={[{
        title: label,
        head: [schemaText(schema, 'ui.studio.agent_scope.col.mode'), schemaText(schema, 'ui.studio.agent_scope.col.description')],
        columns: 'minmax(0,1fr) minmax(0,2.4fr)',
        rows: options.map((option) => ({
          key: option.scope,
          cells: [{ text: option.label, strong: true, kind: 'name' }, { text: option.description, muted: true }],
        })),
        selectedKey: current.scope,
        foot: schemaText(schema, 'ui.studio.agent_scope.note'),
        onPick: (key) => {
          const next = options.find((option) => option.scope === key);
          if (!next || next.scope === scope) return;
          void setScope(next.scope).then((ok) => {
            if (!ok) toast.error(schemaText(schema, 'ui.studio.agent_scope.change_failed'));
          });
        },
      }]}
    />
  );
}
